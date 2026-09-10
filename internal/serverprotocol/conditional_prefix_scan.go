/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package serverprotocol

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	conditionalPrefixScanVersion            = 1
	maxConditionalPrefixScanEntries         = 256
	maxConditionalPrefixScanPrefixBytes     = 8 << 10
	maxConditionalPrefixScanKeyBytes        = 64 << 10
	maxConditionalPrefixScanRequestBytes    = 64 << 10
	maxConditionalPrefixScanValueBytes      = 3 << 20
	maxConditionalPrefixScanTotalValueBytes = 8 << 20
	maxConditionalPrefixScanResponseBytes   = 16 << 20
	maxConditionalPrefixScanActiveCursors   = 4_096
	conditionalPrefixScanCursorTTLSeconds   = 300
	conditionalPrefixScanCursorBytes        = 32
	conditionalPrefixScanResponseHeadroom   = 1 << 10
)

var conditionalPrefixScanCursorPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ConditionalPrefixScanCursor is one opaque, node-local continuation token.
// It is meaningful only for the exact request binding and live Server process
// that issued it. Callers must not inspect it or fall back to a fresh scan when
// the Server reports cursor_unavailable.
type ConditionalPrefixScanCursor struct {
	value string
}

// ParseConditionalPrefixScanCursor validates a persisted opaque cursor. The
// token remains server-owned; validation only enforces the v1 wire shape.
func ParseConditionalPrefixScanCursor(value string) (ConditionalPrefixScanCursor, error) {
	if !conditionalPrefixScanCursorPattern.MatchString(value) {
		return ConditionalPrefixScanCursor{}, newError(CodeInvalidArgument, "conditional prefix scan", "cursor must be an opaque 32-character lowercase hex token", nil)
	}
	return ConditionalPrefixScanCursor{value: value}, nil
}

func (cursor ConditionalPrefixScanCursor) String() string { return cursor.value }

func (cursor ConditionalPrefixScanCursor) valid() bool {
	return conditionalPrefixScanCursorPattern.MatchString(cursor.value)
}

// ConditionalPrefixScanRequest is an immutable scan binding. Continue returns
// another sealed request with the same request ID, namespace, prefix, limit,
// and revision fence, changing only the opaque cursor.
type ConditionalPrefixScanRequest struct {
	requestID        string
	namespace        string
	prefix           []byte
	limit            uint16
	cursor           *ConditionalPrefixScanCursor
	requiredRevision *uint64
}

// NewConditionalPrefixScanRequest constructs an initial cursor:null request.
// requestID must remain stable across retries and every continuation page.
func NewConditionalPrefixScanRequest(requestID, namespace string, prefix []byte, limit uint16, requiredRevision *uint64) (ConditionalPrefixScanRequest, error) {
	return newConditionalPrefixScanRequest(requestID, namespace, prefix, limit, nil, requiredRevision)
}

func newConditionalPrefixScanRequest(requestID, namespace string, prefix []byte, limit uint16, cursor *ConditionalPrefixScanCursor, requiredRevision *uint64) (ConditionalPrefixScanRequest, error) {
	if !validConditionalPrefixScanRequestID(requestID) {
		return ConditionalPrefixScanRequest{}, newError(CodeInvalidArgument, "conditional prefix scan", "request ID must be 1-128 bytes of non-whitespace UTF-8 text", nil)
	}
	if !conditionalNamespacePattern.MatchString(namespace) {
		return ConditionalPrefixScanRequest{}, newError(CodeInvalidArgument, "conditional prefix scan", "namespace must be 1-64 safe ASCII bytes", nil)
	}
	if len(prefix) == 0 || len(prefix) > maxConditionalPrefixScanPrefixBytes {
		return ConditionalPrefixScanRequest{}, newError(CodeInvalidArgument, "conditional prefix scan", "prefix must be 1-8192 bytes", nil)
	}
	if limit == 0 || limit > maxConditionalPrefixScanEntries {
		return ConditionalPrefixScanRequest{}, newError(CodeInvalidArgument, "conditional prefix scan", "limit must be in 1..256", nil)
	}
	if cursor != nil && !cursor.valid() {
		return ConditionalPrefixScanRequest{}, newError(CodeInvalidArgument, "conditional prefix scan", "cursor has an invalid v1 wire shape", nil)
	}
	if requiredRevision != nil && *requiredRevision == 0 {
		return ConditionalPrefixScanRequest{}, newError(CodeInvalidArgument, "conditional prefix scan", "required revision must be greater than zero", nil)
	}
	request := ConditionalPrefixScanRequest{
		requestID: requestID, namespace: namespace, prefix: append([]byte(nil), prefix...), limit: limit,
		cursor: cloneConditionalPrefixScanCursor(cursor), requiredRevision: cloneUint64Pointer(requiredRevision),
	}
	if _, err := encodeConditionalPrefixScanRequest(request); err != nil {
		return ConditionalPrefixScanRequest{}, err
	}
	return request, nil
}

func (request ConditionalPrefixScanRequest) RequestID() string { return request.requestID }
func (request ConditionalPrefixScanRequest) Namespace() string { return request.namespace }
func (request ConditionalPrefixScanRequest) Prefix() []byte {
	return append([]byte(nil), request.prefix...)
}
func (request ConditionalPrefixScanRequest) Limit() uint16 { return request.limit }
func (request ConditionalPrefixScanRequest) Cursor() *ConditionalPrefixScanCursor {
	return cloneConditionalPrefixScanCursor(request.cursor)
}
func (request ConditionalPrefixScanRequest) RequiredRevision() *uint64 {
	return cloneUint64Pointer(request.requiredRevision)
}

// Continue binds the next page to this exact request identity. It rejects a
// zero or malformed cursor instead of allowing a silent fresh-snapshot retry.
func (request ConditionalPrefixScanRequest) Continue(cursor ConditionalPrefixScanCursor) (ConditionalPrefixScanRequest, error) {
	return newConditionalPrefixScanRequest(request.requestID, request.namespace, request.prefix, request.limit, &cursor, request.requiredRevision)
}

// ConditionalPrefixScanEntry is one ordered key/value observation. An empty
// Value is an existing empty value; prefix scans never represent absent keys.
type ConditionalPrefixScanEntry struct {
	Index uint64
	Key   []byte
	Value []byte
}

// ConditionalPrefixScanResult is a verified page from one leased MVCC
// snapshot. SnapshotRevision is node-local metadata, not a cluster revision.
type ConditionalPrefixScanResult struct {
	Version                       uint16
	RequestID                     string
	Namespace                     string
	Prefix                        []byte
	Limit                         uint16
	Cursor                        *ConditionalPrefixScanCursor
	RequiredRevision              *uint64
	ObservedRevision              uint64
	SnapshotRevision              uint64
	Position                      uint64
	Entries                       []ConditionalPrefixScanEntry
	NextCursor                    *ConditionalPrefixScanCursor
	NextCursorExpiresAtUnixMillis *uint64
	ResponseSHA256                string
}

// Continuation returns a sealed request for the next page. A false result is a
// terminal page. Cursor expiry is intentionally not hidden by starting over.
func (result ConditionalPrefixScanResult) Continuation(request ConditionalPrefixScanRequest) (ConditionalPrefixScanRequest, bool, error) {
	if !sameConditionalPrefixScanResultBinding(result, request) {
		return ConditionalPrefixScanRequest{}, false, newError(CodeInvalidArgument, "conditional prefix scan", "result does not belong to the supplied request", nil)
	}
	if result.NextCursor == nil {
		return ConditionalPrefixScanRequest{}, false, nil
	}
	next, err := request.Continue(*result.NextCursor)
	if err != nil {
		return ConditionalPrefixScanRequest{}, false, err
	}
	return next, true, nil
}

type wireConditionalPrefixScanRequest struct {
	Version   uint16                      `json:"version"`
	RequestID string                      `json:"request_id"`
	Namespace string                      `json:"namespace"`
	Prefix    wireBytes                   `json:"prefix"`
	Limit     uint16                      `json:"limit"`
	Cursor    wireNullableString          `json:"cursor"`
	Revision  wireNullableCanonicalUint64 `json:"revision"`
}

type wireConditionalPrefixScanEntry struct {
	Index canonicalUint64 `json:"index"`
	Key   wireBytes       `json:"key"`
	Value wireBytes       `json:"value"`
}

type wireConditionalPrefixScanResult struct {
	Version                   uint16                           `json:"version"`
	RequestID                 string                           `json:"request_id"`
	Namespace                 string                           `json:"namespace"`
	Prefix                    wireBytes                        `json:"prefix"`
	Limit                     uint16                           `json:"limit"`
	Cursor                    wireNullableString               `json:"cursor"`
	Revision                  wireNullableCanonicalUint64      `json:"revision"`
	ObservedRevision          canonicalUint64                  `json:"observed_revision"`
	SnapshotRevision          canonicalUint64                  `json:"snapshot_revision"`
	Position                  canonicalUint64                  `json:"position"`
	Entries                   []wireConditionalPrefixScanEntry `json:"entries"`
	NextCursor                wireNullableString               `json:"next_cursor"`
	NextCursorExpiresAtUnixMS wireNullableCanonicalUint64      `json:"next_cursor_expires_at_unix_ms"`
	ResponseSHA256            string                           `json:"response_sha256"`
}

type wireNullableString struct {
	Value   string
	Present bool
	Null    bool
}

func (value wireNullableString) MarshalJSON() ([]byte, error) {
	if value.Null {
		return []byte("null"), nil
	}
	return json.Marshal(value.Value)
}

func (value *wireNullableString) UnmarshalJSON(data []byte) error {
	value.Present = true
	if bytes.Equal(data, []byte("null")) {
		value.Null = true
		value.Value = ""
		return nil
	}
	var decoded string
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return err
	}
	value.Null = false
	value.Value = decoded
	return nil
}

func encodeConditionalPrefixScanRequest(request ConditionalPrefixScanRequest) (wireConditionalPrefixScanRequest, error) {
	if !validConditionalPrefixScanRequestID(request.requestID) || !conditionalNamespacePattern.MatchString(request.namespace) ||
		len(request.prefix) == 0 || len(request.prefix) > maxConditionalPrefixScanPrefixBytes || request.limit == 0 ||
		request.limit > maxConditionalPrefixScanEntries || (request.cursor != nil && !request.cursor.valid()) ||
		(request.requiredRevision != nil && *request.requiredRevision == 0) {
		return wireConditionalPrefixScanRequest{}, newError(CodeInvalidArgument, "conditional prefix scan", "request was not created by NewConditionalPrefixScanRequest", nil)
	}
	cursor := wireNullableString{Present: true, Null: request.cursor == nil}
	if request.cursor != nil {
		cursor.Value = request.cursor.value
	}
	revision := wireNullableCanonicalUint64{Present: true, Null: request.requiredRevision == nil}
	if request.requiredRevision != nil {
		revision.Value = *request.requiredRevision
	}
	wire := wireConditionalPrefixScanRequest{
		Version: conditionalPrefixScanVersion, RequestID: request.requestID, Namespace: request.namespace,
		Prefix: presentWireBytes(request.prefix), Limit: request.limit, Cursor: cursor, Revision: revision,
	}
	encoded, err := marshalWithoutHTMLEscape(wire)
	if err != nil {
		return wireConditionalPrefixScanRequest{}, newError(CodeInvalidArgument, "conditional prefix scan", "request could not be encoded", err)
	}
	if len(encoded) > maxConditionalPrefixScanRequestBytes {
		return wireConditionalPrefixScanRequest{}, newError(CodeInvalidArgument, "conditional prefix scan", "request exceeds the 64 KiB canonical JSON bound", nil)
	}
	return wire, nil
}

func decodeConditionalPrefixScanResult(data []byte, request ConditionalPrefixScanRequest) (ConditionalPrefixScanResult, error) {
	if _, err := encodeConditionalPrefixScanRequest(request); err != nil {
		return ConditionalPrefixScanResult{}, err
	}
	if len(data) > maxConditionalPrefixScanResponseBytes-conditionalPrefixScanResponseHeadroom {
		return ConditionalPrefixScanResult{}, conditionalPrefixScanProtocolError("decode conditional prefix scan", fmt.Errorf("response exceeds the Core v1 JSON wire bound"))
	}
	var wire wireConditionalPrefixScanResult
	if err := decodeStrictJSON(data, &wire); err != nil {
		return ConditionalPrefixScanResult{}, conditionalPrefixScanProtocolError("decode conditional prefix scan", err)
	}
	if err := verifyConditionalPrefixScanWire(wire, request); err != nil {
		return ConditionalPrefixScanResult{}, conditionalPrefixScanProtocolError("decode conditional prefix scan", err)
	}
	return conditionalPrefixScanResultFromWire(wire), nil
}

func verifyConditionalPrefixScanWire(wire wireConditionalPrefixScanResult, request ConditionalPrefixScanRequest) error {
	if wire.Version != conditionalPrefixScanVersion || wire.RequestID != request.requestID || wire.Namespace != request.namespace ||
		!wire.Prefix.Present || wire.Prefix.Null || !bytes.Equal(wire.Prefix.Value, request.prefix) || wire.Limit != request.limit ||
		!wire.Cursor.Present || !sameNullableCursor(wire.Cursor, request.cursor) || !wire.Revision.Present ||
		!sameNullableRevision(wire.Revision, request.requiredRevision) {
		return fmt.Errorf("response substituted request identity or scan binding")
	}
	if !wire.ObservedRevision.Present || !wire.SnapshotRevision.Present || !wire.Position.Present || wire.Entries == nil ||
		!wire.NextCursor.Present || !wire.NextCursorExpiresAtUnixMS.Present || !shaPattern.MatchString(wire.ResponseSHA256) {
		return fmt.Errorf("required response fields are missing or invalid")
	}
	if request.requiredRevision != nil && wire.ObservedRevision.Value < *request.requiredRevision {
		return fmt.Errorf("observed revision predates the required revision")
	}
	if len(wire.Entries) > int(request.limit) || wire.NextCursor.Null != wire.NextCursorExpiresAtUnixMS.Null ||
		(!wire.NextCursorExpiresAtUnixMS.Null && wire.NextCursorExpiresAtUnixMS.Value == 0) ||
		(request.cursor == nil && wire.Position.Value != 0) ||
		(request.cursor != nil && (wire.Position.Value == 0 || len(wire.Entries) == 0)) ||
		(!wire.NextCursor.Null && len(wire.Entries) != int(request.limit)) ||
		(!wire.NextCursor.Null && request.cursor != nil && wire.NextCursor.Value == request.cursor.value) {
		return fmt.Errorf("response page shape is invalid")
	}
	if !wire.NextCursor.Null && !conditionalPrefixScanCursorPattern.MatchString(wire.NextCursor.Value) {
		return fmt.Errorf("next cursor has an invalid v1 wire shape")
	}
	var (
		previousKey    []byte
		totalValueSize int
	)
	for offset, entry := range wire.Entries {
		expectedIndex, overflow := addUint64(wire.Position.Value, uint64(offset))
		if overflow || !entry.Index.Present || entry.Index.Value != expectedIndex || !entry.Key.Present || entry.Key.Null ||
			!entry.Value.Present || entry.Value.Null || !bytes.HasPrefix(entry.Key.Value, request.prefix) ||
			(previousKey != nil && bytes.Compare(previousKey, entry.Key.Value) >= 0) || len(entry.Key.Value) > maxConditionalPrefixScanKeyBytes ||
			len(entry.Value.Value) > maxConditionalPrefixScanValueBytes {
			return fmt.Errorf("response entry %d is invalid", offset)
		}
		if totalValueSize > maxConditionalPrefixScanTotalValueBytes-len(entry.Value.Value) {
			return fmt.Errorf("response values exceed the aggregate bound")
		}
		totalValueSize += len(entry.Value.Value)
		previousKey = entry.Key.Value
	}
	if conditionalPrefixScanResponseSHA(wire) != wire.ResponseSHA256 {
		return fmt.Errorf("response SHA-256 mismatch")
	}
	return nil
}

func conditionalPrefixScanResultFromWire(wire wireConditionalPrefixScanResult) ConditionalPrefixScanResult {
	entries := make([]ConditionalPrefixScanEntry, len(wire.Entries))
	for index, entry := range wire.Entries {
		entries[index] = ConditionalPrefixScanEntry{
			Index: entry.Index.Value, Key: append([]byte(nil), entry.Key.Value...), Value: append([]byte{}, entry.Value.Value...),
		}
	}
	return ConditionalPrefixScanResult{
		Version: conditionalPrefixScanVersion, RequestID: wire.RequestID, Namespace: wire.Namespace,
		Prefix: append([]byte(nil), wire.Prefix.Value...), Limit: wire.Limit, Cursor: conditionalPrefixScanCursorFromWire(wire.Cursor),
		RequiredRevision: nullableRevisionFromWire(wire.Revision), ObservedRevision: wire.ObservedRevision.Value,
		SnapshotRevision: wire.SnapshotRevision.Value, Position: wire.Position.Value, Entries: entries,
		NextCursor: conditionalPrefixScanCursorFromWire(wire.NextCursor), NextCursorExpiresAtUnixMillis: nullableRevisionFromWire(wire.NextCursorExpiresAtUnixMS),
		ResponseSHA256: wire.ResponseSHA256,
	}
}

func conditionalPrefixScanResponseSHA(result wireConditionalPrefixScanResult) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("TALON_CONDITIONAL_PREFIX_SCAN_RESULT_V1"))
	writePrefixScanUint16(hash, result.Version)
	writePointReadBytes(hash, []byte(result.RequestID))
	writePointReadBytes(hash, []byte(result.Namespace))
	writePointReadBytes(hash, result.Prefix.Value)
	writePrefixScanUint16(hash, result.Limit)
	writePrefixScanOptionalString(hash, result.Cursor)
	writePrefixScanOptionalUint64(hash, result.Revision)
	writePointReadUint64(hash, result.ObservedRevision.Value)
	writePointReadUint64(hash, result.SnapshotRevision.Value)
	writePointReadUint64(hash, result.Position.Value)
	var count [4]byte
	binary.BigEndian.PutUint32(count[:], uint32(len(result.Entries)))
	_, _ = hash.Write(count[:])
	for _, entry := range result.Entries {
		writePointReadUint64(hash, entry.Index.Value)
		writePointReadBytes(hash, entry.Key.Value)
		writePointReadBytes(hash, entry.Value.Value)
	}
	writePrefixScanOptionalString(hash, result.NextCursor)
	writePrefixScanOptionalUint64(hash, result.NextCursorExpiresAtUnixMS)
	return hex.EncodeToString(hash.Sum(nil))
}

func writePrefixScanUint16(hash interface{ Write([]byte) (int, error) }, value uint16) {
	var encoded [2]byte
	binary.BigEndian.PutUint16(encoded[:], value)
	_, _ = hash.Write(encoded[:])
}

func writePrefixScanOptionalString(hash interface{ Write([]byte) (int, error) }, value wireNullableString) {
	if value.Null {
		_, _ = hash.Write([]byte{0})
		return
	}
	_, _ = hash.Write([]byte{1})
	writePointReadBytes(hash, []byte(value.Value))
}

func writePrefixScanOptionalUint64(hash interface{ Write([]byte) (int, error) }, value wireNullableCanonicalUint64) {
	if value.Null {
		_, _ = hash.Write([]byte{0})
		return
	}
	_, _ = hash.Write([]byte{1})
	writePointReadUint64(hash, value.Value)
}

func validConditionalPrefixScanRequestID(value string) bool {
	return utf8.ValidString(value) && len(value) > 0 && len(value) <= 128 && strings.TrimSpace(value) != ""
}

func sameNullableCursor(wire wireNullableString, expected *ConditionalPrefixScanCursor) bool {
	if expected == nil {
		return wire.Null
	}
	return !wire.Null && wire.Value == expected.value
}

func sameConditionalPrefixScanResultBinding(result ConditionalPrefixScanResult, request ConditionalPrefixScanRequest) bool {
	if result.Version != conditionalPrefixScanVersion || result.RequestID != request.requestID || result.Namespace != request.namespace ||
		!bytes.Equal(result.Prefix, request.prefix) || result.Limit != request.limit || !equalUint64Pointers(result.RequiredRevision, request.requiredRevision) {
		return false
	}
	if result.Cursor == nil || request.cursor == nil {
		return result.Cursor == nil && request.cursor == nil
	}
	return result.Cursor.value == request.cursor.value
}

func equalUint64Pointers(left, right *uint64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneConditionalPrefixScanCursor(value *ConditionalPrefixScanCursor) *ConditionalPrefixScanCursor {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func conditionalPrefixScanCursorFromWire(value wireNullableString) *ConditionalPrefixScanCursor {
	if value.Null {
		return nil
	}
	return &ConditionalPrefixScanCursor{value: value.Value}
}

func nullableRevisionFromWire(value wireNullableCanonicalUint64) *uint64 {
	if value.Null {
		return nil
	}
	copy := value.Value
	return &copy
}

func addUint64(left, right uint64) (uint64, bool) {
	result := left + right
	return result, result < left
}

func conditionalPrefixScanProtocolError(operation string, cause error) error {
	return newError(CodeProtocolViolation, operation, "native conditional prefix-scan response violated its typed contract", cause)
}
