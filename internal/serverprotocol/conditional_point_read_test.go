package serverprotocol

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"testing"
)

func makeConditionalPointReadWire(t *testing.T, request ConditionalPointReadRequest, observedRevision, snapshotRevision uint64, value []byte) []byte {
	t.Helper()
	revision := wireNullableCanonicalUint64{Present: true, Null: request.requiredRevision == nil}
	if request.requiredRevision != nil {
		revision.Value = *request.requiredRevision
	}
	wire := wireConditionalPointReadResult{
		Version: conditionalPointReadVersion, Namespace: request.namespace, Key: presentWireBytes(request.key), Revision: revision,
		ObservedRevision: canonicalUint64{Value: observedRevision, Present: true}, SnapshotRevision: canonicalUint64{Value: snapshotRevision, Present: true},
		Value: nullableWireBytes(value),
	}
	wire.ResponseSHA256 = conditionalPointReadResponseSHA(wire)
	data, err := marshalWithoutHTMLEscape(wire)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestConditionalPointReadPreservesExactIdentityRevisionAndPresence(t *testing.T) {
	required := uint64(9_007_199_254_740_993)
	request, err := NewConditionalPointReadRequest("tenant", []byte{0, 1, 255}, &required)
	if err != nil {
		t.Fatal(err)
	}
	emptyData := makeConditionalPointReadWire(t, request, math.MaxUint64, 9_007_199_254_740_994, []byte{})
	empty, err := decodeConditionalPointReadResult(emptyData, request)
	if err != nil {
		t.Fatal(err)
	}
	if !empty.Found || empty.Value == nil || len(empty.Value) != 0 || empty.RequiredRevision == nil || *empty.RequiredRevision != required || empty.ObservedRevision != math.MaxUint64 || empty.SnapshotRevision != 9_007_199_254_740_994 {
		t.Fatalf("empty point read lost identity: %#v", empty)
	}
	if empty.ResponseSHA256 != "16ae44a7c3be3be7a8e6d0366cb4c19961cadce5e45034116b5fa8978431bc09" {
		t.Fatalf("point-read Core digest vector drifted: %s", empty.ResponseSHA256)
	}

	absentData := makeConditionalPointReadWire(t, request, math.MaxUint64, 7, nil)
	absent, err := decodeConditionalPointReadResult(absentData, request)
	if err != nil || absent.Found || absent.Value != nil {
		t.Fatalf("absent point read = %#v, %v", absent, err)
	}
}

func TestConditionalPointReadRejectsSubstitutionAndStaleSnapshot(t *testing.T) {
	required := uint64(8)
	request, _ := NewConditionalPointReadRequest("tenant", []byte("key"), &required)
	data := makeConditionalPointReadWire(t, request, 8, 11, []byte("value"))
	var base wireConditionalPointReadResult
	if err := decodeStrictJSON(data, &base); err != nil {
		t.Fatal(err)
	}
	assertRejected := func(name string, wire wireConditionalPointReadResult, recompute bool) {
		t.Helper()
		if recompute {
			wire.ResponseSHA256 = conditionalPointReadResponseSHA(wire)
		}
		encoded, err := marshalWithoutHTMLEscape(wire)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeConditionalPointReadResult(encoded, request); ErrorCodeOf(err) != CodeProtocolViolation {
			t.Fatalf("%s substitution error = %v", name, err)
		}
	}

	namespace := base
	namespace.Namespace = "other"
	assertRejected("namespace with recomputed digest", namespace, true)
	key := base
	key.Key = presentWireBytes([]byte("other"))
	assertRejected("key with recomputed digest", key, true)
	revision := base
	revision.Revision.Value = 9
	assertRejected("revision with recomputed digest", revision, true)
	value := base
	value.Value = presentWireBytes([]byte("other"))
	assertRejected("value without digest", value, false)
	stale := base
	stale.ObservedRevision.Value = 7
	assertRejected("stale observed revision with recomputed digest", stale, true)
	wrongDigest := base
	wrongDigest.ResponseSHA256 = mutateSHA256(wrongDigest.ResponseSHA256)
	assertRejected("same-length digest", wrongDigest, false)
}

func TestConditionalPointReadRejectsMalformedAndLossyWire(t *testing.T) {
	request, _ := NewConditionalPointReadRequest("tenant", []byte("key"), nil)
	data := makeConditionalPointReadWire(t, request, math.MaxUint64, math.MaxUint64, []byte("value"))
	tests := [][]byte{
		append([]byte(`{"unknown":true,`), data[1:]...),
		append([]byte(`{"version":1,`), data[1:]...),
		append(append([]byte(nil), data...), []byte("null")...),
		bytes.Replace(data, []byte("tenant"), []byte{'t', 'e', 'n', 0xff, 'n', 't'}, 1),
		bytes.Replace(data, []byte(`"observed_revision":"18446744073709551615"`), []byte(`"observed_revision":18446744073709551615`), 1),
		bytes.Replace(data, []byte(`"revision":null`), []byte(`"revision":"0"`), 1),
	}
	for index, malformed := range tests {
		if _, err := decodeConditionalPointReadResult(malformed, request); ErrorCodeOf(err) != CodeProtocolViolation {
			t.Fatalf("malformed point-read %d error = %v", index, err)
		}
	}
}

func TestConditionalPointReadRejectsOversizedValue(t *testing.T) {
	request, _ := NewConditionalPointReadRequest("tenant", []byte("key"), nil)
	data := makeConditionalPointReadWire(t, request, 0, 1, bytes.Repeat([]byte{255}, maxConditionalPointReadValueBytes+1))
	if _, err := decodeConditionalPointReadResult(data, request); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("oversized point-read value error = %v", err)
	}
}

func TestConditionalPointReadRequestIsClosedAndCapabilityGated(t *testing.T) {
	key := []byte("key")
	revision := uint64(5)
	request, err := NewConditionalPointReadRequest("tenant", key, &revision)
	if err != nil {
		t.Fatal(err)
	}
	key[0], revision = 'x', 6
	returnedKey, returnedRevision := request.Key(), request.RequiredRevision()
	returnedKey[0], *returnedRevision = 'y', 7
	if string(request.Key()) != "key" || request.RequiredRevision() == nil || *request.RequiredRevision() != 5 || request.Namespace() != "tenant" {
		t.Fatalf("request was not sealed: key=%q revision=%v", request.Key(), request.RequiredRevision())
	}
	if _, err := buildConditionalPointReadRequest(ConditionalPointReadRequest{}); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("zero request error = %v", err)
	}
	zero := uint64(0)
	for _, test := range []struct {
		namespace string
		key       []byte
		revision  *uint64
	}{
		{namespace: "../unsafe", key: []byte("key")},
		{namespace: "tenant", key: nil},
		{namespace: "tenant", key: make([]byte, maxConditionalKeyBytes+1)},
		{namespace: "tenant", key: []byte("key"), revision: &zero},
	} {
		if _, err := NewConditionalPointReadRequest(test.namespace, test.key, test.revision); ErrorCodeOf(err) != CodeInvalidArgument {
			t.Fatalf("invalid request accepted: %#v, %v", test, err)
		}
	}

}

func TestPointReadNullableCanonicalUint64RequiresNullOrCanonicalString(t *testing.T) {
	for _, valid := range []string{"null", `"1"`, `"18446744073709551615"`} {
		var value wireNullableCanonicalUint64
		if err := decodeStrictJSON([]byte(valid), &value); err != nil || !value.Present {
			t.Fatalf("valid nullable revision %s = %#v, %v", valid, value, err)
		}
	}
	for _, invalid := range []string{"0", `"0"`, `"01"`, `"18446744073709551616"`, `"-1"`} {
		var value wireNullableCanonicalUint64
		err := decodeStrictJSON([]byte(invalid), &value)
		if invalid == `"0"` {
			if err != nil || value.Value != 0 {
				t.Fatalf("wire parser should leave semantic zero validation to the request/result layer: %v", err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("invalid nullable revision %s accepted as %s", invalid, strconv.FormatUint(value.Value, 10))
		}
	}
	encoded, err := json.Marshal(wireNullableCanonicalUint64{Present: true, Null: true})
	if err != nil || string(encoded) != "null" {
		t.Fatalf("nullable revision marshaled as %s, %v", encoded, err)
	}
}
