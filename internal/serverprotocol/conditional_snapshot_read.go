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
)

const (
	conditionalSnapshotReadVersionV1      = 1
	conditionalSnapshotReadVersionV2      = 2
	maxConditionalSnapshotReadKeysV1      = 128
	maxConditionalSnapshotReadKeysV2      = 256
	maxConditionalSnapshotValueBytes      = 3 << 20
	maxConditionalSnapshotTotalValueBytes = 8 << 20
	maxConditionalSnapshotRequestBytes    = 512 << 10
)

// ConditionalSnapshotReadRequest is a closed request for one bounded,
// same-MVCC-snapshot observation of several unique keys. RequiredRevision is
// a lower bound, not an exact historical-read selector.
type ConditionalSnapshotReadRequest struct {
	version          uint16
	namespace        string
	keys             [][]byte
	requiredRevision *uint64
}

// NewConditionalSnapshotReadRequest validates and copies a same-snapshot key
// set. Keys are ordered and the order is part of the response digest.
func NewConditionalSnapshotReadRequest(namespace string, keys [][]byte, requiredRevision *uint64) (ConditionalSnapshotReadRequest, error) {
	return newConditionalSnapshotReadRequest(conditionalSnapshotReadVersionV1, namespace, keys, requiredRevision)
}

// NewConditionalSnapshotReadRequestV2 validates and copies a version 2
// same-snapshot key set. Version 2 increases only the single-call key bound;
// it is not pagination or a historical snapshot handle.
func NewConditionalSnapshotReadRequestV2(namespace string, keys [][]byte, requiredRevision *uint64) (ConditionalSnapshotReadRequest, error) {
	return newConditionalSnapshotReadRequest(conditionalSnapshotReadVersionV2, namespace, keys, requiredRevision)
}

func newConditionalSnapshotReadRequest(version uint16, namespace string, keys [][]byte, requiredRevision *uint64) (ConditionalSnapshotReadRequest, error) {
	if !conditionalNamespacePattern.MatchString(namespace) {
		return ConditionalSnapshotReadRequest{}, newError(CodeInvalidArgument, "conditional snapshot read", "namespace must be 1-64 safe ASCII bytes", nil)
	}
	maxKeys, ok := conditionalSnapshotReadMaxKeys(version)
	if !ok {
		return ConditionalSnapshotReadRequest{}, newError(CodeInvalidArgument, "conditional snapshot read", "version must be 1 or 2", nil)
	}
	if len(keys) == 0 || len(keys) > maxKeys {
		return ConditionalSnapshotReadRequest{}, newError(CodeInvalidArgument, "conditional snapshot read", fmt.Sprintf("version %d key count must be in 1..%d", version, maxKeys), nil)
	}
	copiedKeys := make([][]byte, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for index, key := range keys {
		if len(key) == 0 || len(key) > maxConditionalKeyBytes {
			return ConditionalSnapshotReadRequest{}, newError(CodeInvalidArgument, "conditional snapshot read", fmt.Sprintf("key %d is empty or exceeds the SDK bound", index), nil)
		}
		keyCopy := append([]byte(nil), key...)
		identity := string(keyCopy)
		if _, exists := seen[identity]; exists {
			return ConditionalSnapshotReadRequest{}, newError(CodeInvalidArgument, "conditional snapshot read", fmt.Sprintf("key %d duplicates an earlier key", index), nil)
		}
		seen[identity] = struct{}{}
		copiedKeys[index] = keyCopy
	}
	if requiredRevision != nil && *requiredRevision == 0 {
		return ConditionalSnapshotReadRequest{}, newError(CodeInvalidArgument, "conditional snapshot read", "required revision must be greater than zero", nil)
	}
	request := ConditionalSnapshotReadRequest{
		version:          version,
		namespace:        namespace,
		keys:             copiedKeys,
		requiredRevision: cloneUint64Pointer(requiredRevision),
	}
	if err := validateConditionalSnapshotReadRequestSize(request); err != nil {
		return ConditionalSnapshotReadRequest{}, err
	}
	return request, nil
}

func (request ConditionalSnapshotReadRequest) Version() uint16   { return request.version }
func (request ConditionalSnapshotReadRequest) Namespace() string { return request.namespace }
func (request ConditionalSnapshotReadRequest) Keys() [][]byte {
	keys := make([][]byte, len(request.keys))
	for index, key := range request.keys {
		keys[index] = append([]byte(nil), key...)
	}
	return keys
}
func (request ConditionalSnapshotReadRequest) RequiredRevision() *uint64 {
	return cloneUint64Pointer(request.requiredRevision)
}

// ConditionalSnapshotReadObservation is one ordered observation. Found is
// distinct from an existing empty value (Found=true, len(Value)==0).
type ConditionalSnapshotReadObservation struct {
	Index uint64
	Key   []byte
	Found bool
	Value []byte
}

// ConditionalSnapshotReadResult is a verified same-snapshot observation set.
// SnapshotRevision is local MVCC metadata, not a portable fencing token.
type ConditionalSnapshotReadResult struct {
	Version          uint16
	Namespace        string
	RequiredRevision *uint64
	ObservedRevision uint64
	SnapshotRevision uint64
	Observations     []ConditionalSnapshotReadObservation
	ResponseSHA256   string
}

type wireConditionalSnapshotReadRequest struct {
	Version   uint16                      `json:"version"`
	Namespace string                      `json:"namespace"`
	Keys      []wireBytes                 `json:"keys"`
	Revision  wireNullableCanonicalUint64 `json:"revision"`
}

type wireConditionalSnapshotReadObservation struct {
	Index uint64    `json:"index"`
	Key   wireBytes `json:"key"`
	Value wireBytes `json:"value"`
}

type wireConditionalSnapshotReadResult struct {
	Version          uint16                                   `json:"version"`
	Namespace        string                                   `json:"namespace"`
	Revision         wireNullableCanonicalUint64              `json:"revision"`
	ObservedRevision canonicalUint64                          `json:"observed_revision"`
	SnapshotRevision canonicalUint64                          `json:"snapshot_revision"`
	Observations     []wireConditionalSnapshotReadObservation `json:"observations"`
	ResponseSHA256   string                                   `json:"response_sha256"`
}

func buildConditionalSnapshotReadRequest(request ConditionalSnapshotReadRequest) (wireConditionalSnapshotReadRequest, error) {
	validated, err := newConditionalSnapshotReadRequest(request.version, request.namespace, request.keys, request.requiredRevision)
	if err != nil {
		return wireConditionalSnapshotReadRequest{}, newError(CodeInvalidArgument, "conditional snapshot read", "request was not created by a conditional snapshot-read constructor", err)
	}
	return conditionalSnapshotReadWire(validated), nil
}

func conditionalSnapshotReadWire(request ConditionalSnapshotReadRequest) wireConditionalSnapshotReadRequest {
	keys := make([]wireBytes, len(request.keys))
	for index, key := range request.keys {
		keys[index] = presentWireBytes(key)
	}
	revision := wireNullableCanonicalUint64{Present: true, Null: request.requiredRevision == nil}
	if request.requiredRevision != nil {
		revision.Value = *request.requiredRevision
	}
	return wireConditionalSnapshotReadRequest{
		Version: request.version, Namespace: request.namespace,
		Keys: keys, Revision: revision,
	}
}

func validateConditionalSnapshotReadRequestSize(request ConditionalSnapshotReadRequest) error {
	encoded, err := json.Marshal(conditionalSnapshotReadWire(request))
	if err != nil {
		return newError(CodeInvalidArgument, "conditional snapshot read", "request could not be encoded", err)
	}
	if len(encoded) > maxConditionalSnapshotRequestBytes {
		return newError(CodeInvalidArgument, "conditional snapshot read", "request exceeds the 512 KiB canonical JSON bound", nil)
	}
	return nil
}

func decodeConditionalSnapshotReadResult(data []byte, request ConditionalSnapshotReadRequest) (ConditionalSnapshotReadResult, error) {
	if _, err := buildConditionalSnapshotReadRequest(request); err != nil {
		return ConditionalSnapshotReadResult{}, err
	}
	var wire wireConditionalSnapshotReadResult
	if err := decodeStrictJSON(data, &wire); err != nil {
		return ConditionalSnapshotReadResult{}, conditionalSnapshotReadProtocolError("decode conditional snapshot read", err)
	}
	if wire.Version != request.version || wire.Namespace != request.namespace || !wire.Revision.Present || !sameNullableRevision(wire.Revision, request.requiredRevision) || len(wire.Observations) != len(request.keys) {
		return ConditionalSnapshotReadResult{}, conditionalSnapshotReadProtocolError("decode conditional snapshot read", fmt.Errorf("response substituted request identity or observations"))
	}
	if !wire.ObservedRevision.Present || !wire.SnapshotRevision.Present || !shaPattern.MatchString(wire.ResponseSHA256) {
		return ConditionalSnapshotReadResult{}, conditionalSnapshotReadProtocolError("decode conditional snapshot read", fmt.Errorf("required response fields are missing or invalid"))
	}
	if request.requiredRevision != nil && wire.ObservedRevision.Value < *request.requiredRevision {
		return ConditionalSnapshotReadResult{}, conditionalSnapshotReadProtocolError("decode conditional snapshot read", fmt.Errorf("observed revision predates the required revision"))
	}
	observations := make([]ConditionalSnapshotReadObservation, len(wire.Observations))
	seen := make(map[string]struct{}, len(wire.Observations))
	totalValueBytes := 0
	for index, observation := range wire.Observations {
		if observation.Index != uint64(index) || !observation.Key.Present || observation.Key.Null || !bytes.Equal(observation.Key.Value, request.keys[index]) || !observation.Value.Present {
			return ConditionalSnapshotReadResult{}, conditionalSnapshotReadProtocolError("decode conditional snapshot read", fmt.Errorf("observation %d substituted index or key", index))
		}
		identity := string(observation.Key.Value)
		if _, exists := seen[identity]; exists {
			return ConditionalSnapshotReadResult{}, conditionalSnapshotReadProtocolError("decode conditional snapshot read", fmt.Errorf("observation %d duplicated a key", index))
		}
		seen[identity] = struct{}{}
		if !observation.Value.Null {
			if len(observation.Value.Value) > maxConditionalSnapshotValueBytes {
				return ConditionalSnapshotReadResult{}, conditionalSnapshotReadProtocolError("decode conditional snapshot read", fmt.Errorf("observation %d value exceeds the point-read bound", index))
			}
			if totalValueBytes > maxConditionalSnapshotTotalValueBytes-len(observation.Value.Value) {
				return ConditionalSnapshotReadResult{}, conditionalSnapshotReadProtocolError("decode conditional snapshot read", fmt.Errorf("snapshot values exceed the aggregate bound"))
			}
			totalValueBytes += len(observation.Value.Value)
		}
		observations[index] = ConditionalSnapshotReadObservation{
			Index: uint64(index), Key: append([]byte(nil), observation.Key.Value...),
			Found: !observation.Value.Null, Value: cloneNullableBytes(observation.Value),
		}
	}
	if conditionalSnapshotReadResponseSHA(wire) != wire.ResponseSHA256 {
		return ConditionalSnapshotReadResult{}, conditionalSnapshotReadProtocolError("decode conditional snapshot read", fmt.Errorf("response SHA-256 mismatch"))
	}
	return ConditionalSnapshotReadResult{
		Version: request.version, Namespace: request.namespace,
		RequiredRevision: cloneUint64Pointer(request.requiredRevision), ObservedRevision: wire.ObservedRevision.Value,
		SnapshotRevision: wire.SnapshotRevision.Value, Observations: observations,
		ResponseSHA256: wire.ResponseSHA256,
	}, nil
}

func conditionalSnapshotReadResponseSHA(result wireConditionalSnapshotReadResult) string {
	hash := sha256.New()
	domain, ok := conditionalSnapshotReadDigestDomain(result.Version)
	if !ok {
		return ""
	}
	_, _ = hash.Write([]byte(domain))
	var u16 [2]byte
	binary.BigEndian.PutUint16(u16[:], result.Version)
	_, _ = hash.Write(u16[:])
	writePointReadBytes(hash, []byte(result.Namespace))
	if result.Revision.Null {
		_, _ = hash.Write([]byte{0})
	} else {
		_, _ = hash.Write([]byte{1})
		writePointReadUint64(hash, result.Revision.Value)
	}
	writePointReadUint64(hash, result.ObservedRevision.Value)
	writePointReadUint64(hash, result.SnapshotRevision.Value)
	var count [4]byte
	binary.BigEndian.PutUint32(count[:], uint32(len(result.Observations)))
	_, _ = hash.Write(count[:])
	for _, observation := range result.Observations {
		binary.BigEndian.PutUint32(count[:], uint32(observation.Index))
		_, _ = hash.Write(count[:])
		writePointReadBytes(hash, observation.Key.Value)
		if observation.Value.Null {
			_, _ = hash.Write([]byte{0})
		} else {
			_, _ = hash.Write([]byte{1})
			writePointReadBytes(hash, observation.Value.Value)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func conditionalSnapshotReadMaxKeys(version uint16) (int, bool) {
	switch version {
	case conditionalSnapshotReadVersionV1:
		return maxConditionalSnapshotReadKeysV1, true
	case conditionalSnapshotReadVersionV2:
		return maxConditionalSnapshotReadKeysV2, true
	default:
		return 0, false
	}
}

func conditionalSnapshotReadDigestDomain(version uint16) (string, bool) {
	switch version {
	case conditionalSnapshotReadVersionV1:
		return "TALON_CONDITIONAL_SNAPSHOT_READ_RESULT_V1", true
	case conditionalSnapshotReadVersionV2:
		return "TALON_CONDITIONAL_SNAPSHOT_READ_RESULT_V2", true
	default:
		return "", false
	}
}

func conditionalSnapshotReadProtocolError(operation string, cause error) error {
	return newError(CodeProtocolViolation, operation, "native conditional snapshot-read response violated its typed contract", cause)
}
