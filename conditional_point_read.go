/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package talon

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

const (
	conditionalPointReadVersion       = 1
	maxConditionalPointReadValueBytes = 3 << 20
)

// ConditionalPointReadRequest is a closed snapshot-bound read of one key in a
// conditional namespace. RequiredRevision is a lower bound, not a request for
// an exact historical revision. A nil bound may be stale on a follower.
type ConditionalPointReadRequest struct {
	namespace        string
	key              []byte
	requiredRevision *uint64
}

// NewConditionalPointReadRequest validates and copies a point-read identity.
// requiredRevision may be nil; a non-nil value must be greater than zero.
func NewConditionalPointReadRequest(namespace string, key []byte, requiredRevision *uint64) (ConditionalPointReadRequest, error) {
	if !conditionalNamespacePattern.MatchString(namespace) {
		return ConditionalPointReadRequest{}, newError(CodeInvalidArgument, "conditional point read", "namespace must be 1-64 safe ASCII bytes", nil)
	}
	if len(key) == 0 || len(key) > maxConditionalKeyBytes {
		return ConditionalPointReadRequest{}, newError(CodeInvalidArgument, "conditional point read", "key is empty or exceeds the SDK bound", nil)
	}
	if requiredRevision != nil && *requiredRevision == 0 {
		return ConditionalPointReadRequest{}, newError(CodeInvalidArgument, "conditional point read", "required revision must be greater than zero", nil)
	}
	return ConditionalPointReadRequest{
		namespace:        namespace,
		key:              append([]byte(nil), key...),
		requiredRevision: cloneUint64Pointer(requiredRevision),
	}, nil
}

func (request ConditionalPointReadRequest) Namespace() string { return request.namespace }
func (request ConditionalPointReadRequest) Key() []byte       { return append([]byte(nil), request.key...) }
func (request ConditionalPointReadRequest) RequiredRevision() *uint64 {
	return cloneUint64Pointer(request.requiredRevision)
}

// ConditionalPointReadResult is a verified snapshot observation. Found is the
// authoritative existence bit: Found with an empty Value differs from absence.
// SnapshotRevision is a local MVCC identifier, not a Raft index or portable
// fencing token.
type ConditionalPointReadResult struct {
	Version          uint16
	Namespace        string
	Key              []byte
	RequiredRevision *uint64
	ObservedRevision uint64
	SnapshotRevision uint64
	Found            bool
	Value            []byte
	ResponseSHA256   string
}

type wireConditionalPointReadRequest struct {
	Version   uint16                      `json:"version"`
	Namespace string                      `json:"namespace"`
	Key       wireBytes                   `json:"key"`
	Revision  wireNullableCanonicalUint64 `json:"revision"`
}

type wireConditionalPointReadResult struct {
	Version          uint16                      `json:"version"`
	Namespace        string                      `json:"namespace"`
	Key              wireBytes                   `json:"key"`
	Revision         wireNullableCanonicalUint64 `json:"revision"`
	ObservedRevision canonicalUint64             `json:"observed_revision"`
	SnapshotRevision canonicalUint64             `json:"snapshot_revision"`
	Value            wireBytes                   `json:"value"`
	ResponseSHA256   string                      `json:"response_sha256"`
}

type wireNullableCanonicalUint64 struct {
	Value   uint64
	Present bool
	Null    bool
}

func (value wireNullableCanonicalUint64) MarshalJSON() ([]byte, error) {
	if value.Null {
		return []byte("null"), nil
	}
	return json.Marshal(fmt.Sprintf("%d", value.Value))
}

func (value *wireNullableCanonicalUint64) UnmarshalJSON(data []byte) error {
	value.Present = true
	if bytes.Equal(data, []byte("null")) {
		value.Null = true
		value.Value = 0
		return nil
	}
	var canonical canonicalUint64
	if err := decodeStrictJSON(data, &canonical); err != nil {
		return err
	}
	value.Value = canonical.Value
	return nil
}

func buildConditionalPointReadRequest(request ConditionalPointReadRequest) (wireConditionalPointReadRequest, error) {
	validated, err := NewConditionalPointReadRequest(request.namespace, request.key, request.requiredRevision)
	if err != nil {
		return wireConditionalPointReadRequest{}, newError(CodeInvalidArgument, "conditional point read", "request was not created by NewConditionalPointReadRequest", err)
	}
	revision := wireNullableCanonicalUint64{Present: true, Null: validated.requiredRevision == nil}
	if validated.requiredRevision != nil {
		revision.Value = *validated.requiredRevision
	}
	return wireConditionalPointReadRequest{
		Version: conditionalPointReadVersion, Namespace: validated.namespace,
		Key: presentWireBytes(validated.key), Revision: revision,
	}, nil
}

// ConditionalPointRead is a compatibility alias for ConditionalGet.
// Deprecated: use ConditionalGet.
func (db *DB) ConditionalPointRead(request ConditionalPointReadRequest) (ConditionalPointReadResult, error) {
	return db.ConditionalGet(request)
}

// ConditionalGet reads one key from a single Core snapshot and verifies the
// echoed identity, revision lower bound, exact absent/empty/value state, and
// response digest.
func (db *DB) ConditionalGet(request ConditionalPointReadRequest) (ConditionalPointReadResult, error) {
	wire, err := buildConditionalPointReadRequest(request)
	if err != nil {
		return ConditionalPointReadResult{}, err
	}
	if err := db.RequireCapability("storage_conditional_point_read"); err != nil {
		return ConditionalPointReadResult{}, err
	}
	data, err := db.execute("storage", "conditional_get", wire)
	if err != nil {
		return ConditionalPointReadResult{}, err
	}
	return decodeConditionalPointReadResult(data, request)
}

func decodeConditionalPointReadResult(data []byte, request ConditionalPointReadRequest) (ConditionalPointReadResult, error) {
	if _, err := buildConditionalPointReadRequest(request); err != nil {
		return ConditionalPointReadResult{}, err
	}
	var wire wireConditionalPointReadResult
	if err := decodeStrictJSON(data, &wire); err != nil {
		return ConditionalPointReadResult{}, conditionalPointReadProtocolError("decode conditional point read", err)
	}
	if wire.Version != conditionalPointReadVersion || wire.Namespace != request.namespace || !wire.Key.Present || wire.Key.Null || !bytes.Equal(wire.Key.Value, request.key) || !wire.Revision.Present || !sameNullableRevision(wire.Revision, request.requiredRevision) {
		return ConditionalPointReadResult{}, conditionalPointReadProtocolError("decode conditional point read", fmt.Errorf("response substituted request identity or revision"))
	}
	if !wire.ObservedRevision.Present || !wire.SnapshotRevision.Present || !wire.Value.Present || !shaPattern.MatchString(wire.ResponseSHA256) {
		return ConditionalPointReadResult{}, conditionalPointReadProtocolError("decode conditional point read", fmt.Errorf("required response fields are missing or invalid"))
	}
	if request.requiredRevision != nil && wire.ObservedRevision.Value < *request.requiredRevision {
		return ConditionalPointReadResult{}, conditionalPointReadProtocolError("decode conditional point read", fmt.Errorf("observed revision predates the required revision"))
	}
	if !wire.Value.Null && len(wire.Value.Value) > maxConditionalPointReadValueBytes {
		return ConditionalPointReadResult{}, conditionalPointReadProtocolError("decode conditional point read", fmt.Errorf("value exceeds the point-read bound"))
	}
	if conditionalPointReadResponseSHA(wire) != wire.ResponseSHA256 {
		return ConditionalPointReadResult{}, conditionalPointReadProtocolError("decode conditional point read", fmt.Errorf("response SHA-256 mismatch"))
	}
	return ConditionalPointReadResult{
		Version: conditionalPointReadVersion, Namespace: request.namespace, Key: append([]byte(nil), request.key...),
		RequiredRevision: cloneUint64Pointer(request.requiredRevision), ObservedRevision: wire.ObservedRevision.Value,
		SnapshotRevision: wire.SnapshotRevision.Value, Found: !wire.Value.Null,
		Value: cloneNullableBytes(wire.Value), ResponseSHA256: wire.ResponseSHA256,
	}, nil
}

func sameNullableRevision(wire wireNullableCanonicalUint64, expected *uint64) bool {
	if expected == nil {
		return wire.Null
	}
	return !wire.Null && wire.Value == *expected
}

func conditionalPointReadResponseSHA(result wireConditionalPointReadResult) string {
	hash := sha256.New()
	_, _ = hash.Write([]byte("TALON_CONDITIONAL_POINT_READ_RESULT_V1"))
	var u16 [2]byte
	binary.BigEndian.PutUint16(u16[:], result.Version)
	_, _ = hash.Write(u16[:])
	writePointReadBytes(hash, []byte(result.Namespace))
	writePointReadBytes(hash, result.Key.Value)
	if result.Revision.Null {
		_, _ = hash.Write([]byte{0})
	} else {
		_, _ = hash.Write([]byte{1})
		writePointReadUint64(hash, result.Revision.Value)
	}
	writePointReadUint64(hash, result.ObservedRevision.Value)
	writePointReadUint64(hash, result.SnapshotRevision.Value)
	if result.Value.Null {
		_, _ = hash.Write([]byte{0})
	} else {
		_, _ = hash.Write([]byte{1})
		writePointReadBytes(hash, result.Value.Value)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func writePointReadBytes(hash interface{ Write([]byte) (int, error) }, value []byte) {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(value)))
	_, _ = hash.Write(length[:])
	_, _ = hash.Write(value)
}

func writePointReadUint64(hash interface{ Write([]byte) (int, error) }, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = hash.Write(encoded[:])
}

func conditionalPointReadProtocolError(operation string, cause error) error {
	return newError(CodeProtocolViolation, operation, "native conditional point-read response violated its typed contract", cause)
}
