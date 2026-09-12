/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package serverprotocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"testing"
)

func makeConditionalSnapshotReadWire(t *testing.T, request ConditionalSnapshotReadRequest, observedRevision, snapshotRevision uint64, values ...[]byte) []byte {
	t.Helper()
	revision := wireNullableCanonicalUint64{Present: true, Null: request.requiredRevision == nil}
	if request.requiredRevision != nil {
		revision.Value = *request.requiredRevision
	}
	observations := make([]wireConditionalSnapshotReadObservation, len(request.keys))
	for index, value := range values {
		observations[index] = wireConditionalSnapshotReadObservation{
			Index: uint64(index), Key: presentWireBytes(request.keys[index]), Value: nullableWireBytes(value),
		}
	}
	wire := wireConditionalSnapshotReadResult{
		Version: request.version, Namespace: request.namespace, Revision: revision,
		ObservedRevision: canonicalUint64{Value: observedRevision, Present: true}, SnapshotRevision: canonicalUint64{Value: snapshotRevision, Present: true},
		Observations: observations,
	}
	wire.ResponseSHA256 = conditionalSnapshotReadResponseSHA(wire)
	data, err := marshalWithoutHTMLEscape(wire)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestConditionalSnapshotReadPreservesOrderAndAbsentEmptyState(t *testing.T) {
	required := uint64(9_007_199_254_740_993)
	request, err := NewConditionalSnapshotReadRequest("tenant", [][]byte{[]byte("present"), []byte("empty"), []byte("missing")}, &required)
	if err != nil {
		t.Fatal(err)
	}
	data := makeConditionalSnapshotReadWire(t, request, math.MaxUint64, math.MaxUint64, []byte("value"), []byte{}, nil)
	result, err := decodeConditionalSnapshotReadResult(data, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Observations) != 3 || !result.Observations[0].Found || string(result.Observations[0].Value) != "value" || !result.Observations[1].Found || result.Observations[1].Value == nil || len(result.Observations[1].Value) != 0 || result.Observations[2].Found || result.Observations[2].Value != nil {
		t.Fatalf("snapshot observations lost exact state: %#v", result.Observations)
	}
	if result.RequiredRevision == nil || *result.RequiredRevision != required || result.ObservedRevision != math.MaxUint64 || result.SnapshotRevision != math.MaxUint64 {
		t.Fatalf("snapshot metadata lost identity: %#v", result)
	}
	if result.ResponseSHA256 != "56d509ae69bc17be0cfda6747cbec8559a6e66db5056fc0091e94201a0d00fb3" {
		t.Fatalf("snapshot-read Core digest vector drifted: %s", result.ResponseSHA256)
	}
}

func TestConditionalSnapshotReadRejectsSubstitutionAndDigestDrift(t *testing.T) {
	request, _ := NewConditionalSnapshotReadRequest("tenant", [][]byte{[]byte("a"), []byte("b")}, nil)
	data := makeConditionalSnapshotReadWire(t, request, 8, 11, []byte("one"), []byte("two"))
	var base wireConditionalSnapshotReadResult
	if err := decodeStrictJSON(data, &base); err != nil {
		t.Fatal(err)
	}
	assertRejected := func(name string, wire wireConditionalSnapshotReadResult, recompute bool) {
		t.Helper()
		if recompute {
			wire.ResponseSHA256 = conditionalSnapshotReadResponseSHA(wire)
		}
		encoded, err := marshalWithoutHTMLEscape(wire)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeConditionalSnapshotReadResult(encoded, request); ErrorCodeOf(err) != CodeProtocolViolation {
			t.Fatalf("%s substitution error = %v", name, err)
		}
	}
	namespace := base
	namespace.Namespace = "other"
	assertRejected("namespace with recomputed digest", namespace, true)
	key := base
	key.Observations[0].Key = presentWireBytes([]byte("other"))
	assertRejected("key with recomputed digest", key, true)
	order := base
	order.Observations[0], order.Observations[1] = order.Observations[1], order.Observations[0]
	assertRejected("order with recomputed digest", order, true)
	value := base
	value.Observations[0].Value = presentWireBytes([]byte("other"))
	assertRejected("value without digest", value, false)
	stale := base
	stale.ObservedRevision.Value = 7
	assertRejected("stale revision with recomputed digest", stale, true)
	wrongDigest := base
	wrongDigest.ResponseSHA256 = mutateSHA256(wrongDigest.ResponseSHA256)
	assertRejected("same-length digest", wrongDigest, false)
}

func TestConditionalSnapshotReadRejectsMalformedWireAndBounds(t *testing.T) {
	request, _ := NewConditionalSnapshotReadRequest("tenant", [][]byte{[]byte("key")}, nil)
	data := makeConditionalSnapshotReadWire(t, request, 1, 2, []byte("value"))
	var base wireConditionalSnapshotReadResult
	if err := decodeStrictJSON(data, &base); err != nil {
		t.Fatal(err)
	}
	malformed := [][]byte{
		append([]byte(`{"unknown":true,`), data[1:]...),
		append(append([]byte(nil), data...), []byte("null")...),
		bytes.Replace(data, []byte("tenant"), []byte{'t', 'e', 'n', 0xff, 'n', 't'}, 1),
		bytes.Replace(data, []byte(`"observed_revision":"1"`), []byte(`"observed_revision":1`), 1),
	}
	for index, value := range malformed {
		if _, err := decodeConditionalSnapshotReadResult(value, request); ErrorCodeOf(err) != CodeProtocolViolation {
			t.Fatalf("malformed snapshot-read %d error = %v", index, err)
		}
	}
	tooMany := make([][]byte, maxConditionalSnapshotReadKeysV1+1)
	for index := range tooMany {
		tooMany[index] = []byte{byte(index), byte(index >> 8), byte(index >> 16)}
	}
	if _, err := NewConditionalSnapshotReadRequest("tenant", tooMany, nil); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("too many snapshot keys error = %v", err)
	}
	duplicate := [][]byte{[]byte("same"), []byte("same")}
	if _, err := NewConditionalSnapshotReadRequest("tenant", duplicate, nil); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("duplicate snapshot keys error = %v", err)
	}
	oversized := makeConditionalSnapshotReadWire(t, request, 1, 2, bytes.Repeat([]byte{255}, maxConditionalSnapshotValueBytes+1))
	if _, err := decodeConditionalSnapshotReadResult(oversized, request); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("oversized snapshot value error = %v", err)
	}
	largeKeys := [][]byte{
		bytes.Repeat([]byte{254}, maxConditionalKeyBytes),
		bytes.Repeat([]byte{255}, maxConditionalKeyBytes),
	}
	constructors := []struct {
		name string
		new  func(string, [][]byte, *uint64) (ConditionalSnapshotReadRequest, error)
	}{
		{name: "v1", new: NewConditionalSnapshotReadRequest},
		{name: "v2", new: NewConditionalSnapshotReadRequestV2},
	}
	for _, constructor := range constructors {
		if _, err := constructor.new("tenant", largeKeys, nil); ErrorCodeOf(err) != CodeInvalidArgument {
			t.Fatalf("%s oversized snapshot request error = %v", constructor.name, err)
		}
	}
}

func TestConditionalSnapshotReadRequestIsClosedAndCapabilityGated(t *testing.T) {
	keys := [][]byte{[]byte("a"), []byte("b")}
	revision := uint64(5)
	request, err := NewConditionalSnapshotReadRequest("tenant", keys, &revision)
	if err != nil {
		t.Fatal(err)
	}
	keys[0][0], revision = 'x', 6
	returnedKeys, returnedRevision := request.Keys(), request.RequiredRevision()
	returnedKeys[0][0], *returnedRevision = 'y', 7
	if string(request.Keys()[0]) != "a" || request.RequiredRevision() == nil || *request.RequiredRevision() != 5 {
		t.Fatalf("snapshot request was not sealed: keys=%q revision=%v", request.Keys(), request.RequiredRevision())
	}
	if _, err := buildConditionalSnapshotReadRequest(ConditionalSnapshotReadRequest{}); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("zero snapshot request error = %v", err)
	}
}

func TestConditionalSnapshotReadCoreDigestEncodingUsesCanonicalFields(t *testing.T) {
	request, _ := NewConditionalSnapshotReadRequest("tenant", [][]byte{[]byte("key")}, nil)
	wire := makeConditionalSnapshotReadWire(t, request, 0, 1, []byte{})
	var decoded wireConditionalSnapshotReadResult
	if err := json.Unmarshal(wire, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Observations[0].Value.Present || decoded.Observations[0].Value.Null {
		t.Fatal("empty existing value lost its presence bit")
	}
}

func TestConditionalSnapshotReadVersionedBoundsAndDigest(t *testing.T) {
	numberedKeys := func(count int) [][]byte {
		keys := make([][]byte, count)
		for index := range keys {
			keys[index] = []byte(fmt.Sprintf("fact-%03d", index))
		}
		return keys
	}
	for _, count := range []int{128, 129, 139, 256} {
		request, err := NewConditionalSnapshotReadRequestV2("v2-bounds", numberedKeys(count), nil)
		if err != nil || request.Version() != conditionalSnapshotReadVersionV2 || len(request.Keys()) != count {
			t.Fatalf("v2 count %d request = %#v, %v", count, request, err)
		}
	}
	if _, err := NewConditionalSnapshotReadRequest("v1-bound", numberedKeys(129), nil); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("v1 accepted 129 keys: %v", err)
	}
	if _, err := NewConditionalSnapshotReadRequestV2("v2-bound", numberedKeys(257), nil); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("v2 accepted 257 keys: %v", err)
	}
	if _, err := NewConditionalSnapshotReadRequestV2("v2-bound", [][]byte{[]byte("same"), []byte("same")}, nil); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("v2 accepted duplicate keys: %v", err)
	}

	required := uint64(42)
	request, err := NewConditionalSnapshotReadRequestV2("fixture", [][]byte{[]byte("a"), []byte("b"), []byte("c")}, &required)
	if err != nil {
		t.Fatal(err)
	}
	data := makeConditionalSnapshotReadWire(t, request, 42, 99, nil, []byte{}, []byte{0, 255})
	result, err := decodeConditionalSnapshotReadResult(data, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != conditionalSnapshotReadVersionV2 || result.ResponseSHA256 != "258887b8157621dce4590f90efb499e276bdf1016bd97fa2dec8c8d3e22d8a72" {
		t.Fatalf("v2 Core vector drifted: %#v", result)
	}

	var reordered wireConditionalSnapshotReadResult
	if err := decodeStrictJSON(data, &reordered); err != nil {
		t.Fatal(err)
	}
	reordered.Observations[0], reordered.Observations[1] = reordered.Observations[1], reordered.Observations[0]
	reordered.ResponseSHA256 = conditionalSnapshotReadResponseSHA(reordered)
	encoded, err := marshalWithoutHTMLEscape(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeConditionalSnapshotReadResult(encoded, request); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("v2 reordered observations error = %v", err)
	}

	var substituted wireConditionalSnapshotReadResult
	if err := decodeStrictJSON(data, &substituted); err != nil {
		t.Fatal(err)
	}
	substituted.Version = conditionalSnapshotReadVersionV1
	substituted.ResponseSHA256 = conditionalSnapshotReadResponseSHA(substituted)
	encoded, err = marshalWithoutHTMLEscape(substituted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeConditionalSnapshotReadResult(encoded, request); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("response version substitution error = %v", err)
	}
}
