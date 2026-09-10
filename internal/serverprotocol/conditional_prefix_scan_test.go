package serverprotocol

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
)

func TestConditionalPrefixScanCoreDigestVector(t *testing.T) {
	revision := uint64(42)
	request, err := NewConditionalPrefixScanRequest("scan-vector", "events", []byte("outbox/"), 2, &revision)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"version":1,"request_id":"scan-vector","namespace":"events","prefix":[111,117,116,98,111,120,47],"limit":2,"cursor":null,"revision":"42","observed_revision":"45","snapshot_revision":"901","position":"0","entries":[{"index":"0","key":[111,117,116,98,111,120,47,48,49],"value":[111,110,101]}],"next_cursor":null,"next_cursor_expires_at_unix_ms":null,"response_sha256":"e7e7da431dc036ef3f584881544503f6124c395c64ccf9984e08381e30b5cee5"}`)
	result, err := decodeConditionalPrefixScanResult(data, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.ResponseSHA256 != "e7e7da431dc036ef3f584881544503f6124c395c64ccf9984e08381e30b5cee5" ||
		result.ObservedRevision != 45 || result.SnapshotRevision != 901 || len(result.Entries) != 1 ||
		result.Entries[0].Index != 0 || string(result.Entries[0].Key) != "outbox/01" || string(result.Entries[0].Value) != "one" {
		t.Fatalf("Core prefix-scan vector drifted: %#v", result)
	}
}

func TestConditionalPrefixScanRequestIsSealedAndContinuationPreservesBinding(t *testing.T) {
	prefix := []byte("outbox/")
	revision := uint64(math.MaxUint64)
	request, err := NewConditionalPrefixScanRequest(" scan / 你好 ", "events", prefix, 256, &revision)
	if err != nil {
		t.Fatal(err)
	}
	prefix[0], revision = 'x', 1
	returnedPrefix, returnedRevision := request.Prefix(), request.RequiredRevision()
	returnedPrefix[0], *returnedRevision = 'y', 2
	if string(request.Prefix()) != "outbox/" || request.RequiredRevision() == nil || *request.RequiredRevision() != math.MaxUint64 || request.Cursor() != nil {
		t.Fatalf("initial request was not sealed: prefix=%q revision=%v cursor=%v", request.Prefix(), request.RequiredRevision(), request.Cursor())
	}
	cursor, err := ParseConditionalPrefixScanCursor("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	continuation, err := request.Continue(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if continuation.RequestID() != request.RequestID() || continuation.Namespace() != request.Namespace() ||
		!bytes.Equal(continuation.Prefix(), request.Prefix()) || continuation.Limit() != request.Limit() ||
		continuation.Cursor() == nil || continuation.Cursor().String() != cursor.String() ||
		continuation.RequiredRevision() == nil || *continuation.RequiredRevision() != math.MaxUint64 {
		t.Fatalf("continuation changed its scan binding: %#v", continuation)
	}
	for _, invalid := range []string{"", "A123456789abcdef0123456789abcdef", "0123456789abcdef0123456789abcdeg", "0123"} {
		if _, err := ParseConditionalPrefixScanCursor(invalid); ErrorCodeOf(err) != CodeInvalidArgument {
			t.Fatalf("cursor %q error = %v", invalid, err)
		}
	}
	if _, err := encodeConditionalPrefixScanRequest(ConditionalPrefixScanRequest{}); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("zero request error = %v", err)
	}
}

func TestConditionalPrefixScanResultBuildsTypedContinuation(t *testing.T) {
	request, err := NewConditionalPrefixScanRequest("scan-pages", "events", []byte("outbox/"), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	wire := prefixScanWireFixture(request, 7, 11, 0, [][]byte{[]byte("outbox/01")}, [][]byte{[]byte("one")})
	wire.NextCursor = presentNullableString("0123456789abcdef0123456789abcdef")
	wire.NextCursorExpiresAtUnixMS = presentNullableUint64(4_102_444_800_000)
	wire.ResponseSHA256 = conditionalPrefixScanResponseSHA(wire)
	data, err := marshalWithoutHTMLEscape(wire)
	if err != nil {
		t.Fatal(err)
	}
	result, err := decodeConditionalPrefixScanResult(data, request)
	if err != nil {
		t.Fatal(err)
	}
	next, ok, err := result.Continuation(request)
	if err != nil || !ok || next.Cursor() == nil || next.Cursor().String() != wire.NextCursor.Value {
		t.Fatalf("typed continuation = %#v, %t, %v", next, ok, err)
	}
	continuedWire := prefixScanWireFixture(next, 7, 11, 1, [][]byte{[]byte("outbox/02")}, [][]byte{[]byte{}})
	continuedWire.ResponseSHA256 = conditionalPrefixScanResponseSHA(continuedWire)
	continuedData, _ := marshalWithoutHTMLEscape(continuedWire)
	continued, err := decodeConditionalPrefixScanResult(continuedData, next)
	if err != nil || continued.Entries[0].Value == nil || len(continued.Entries[0].Value) != 0 {
		t.Fatalf("continuation lost an existing empty value: %#v, %v", continued, err)
	}
	if _, ok, err := continued.Continuation(next); err != nil || ok {
		t.Fatalf("terminal continuation = %t, %v", ok, err)
	}
	forgedTerminal := continued
	forgedTerminal.Namespace = "other"
	if _, ok, err := forgedTerminal.Continuation(next); ErrorCodeOf(err) != CodeInvalidArgument || ok {
		t.Fatalf("forged terminal continuation = %t, %v", ok, err)
	}
}

func TestConditionalPrefixScanRejectsSubstitutionDigestDriftAndMalformedWire(t *testing.T) {
	request, _ := NewConditionalPrefixScanRequest("scan-hostile", "events", []byte("outbox/"), 2, nil)
	base := prefixScanWireFixture(request, 4, 8, 0,
		[][]byte{[]byte("outbox/01"), []byte("outbox/02")},
		[][]byte{[]byte("one"), []byte("two")},
	)
	base.ResponseSHA256 = conditionalPrefixScanResponseSHA(base)
	assertRejected := func(name string, wire wireConditionalPrefixScanResult, recompute bool) {
		t.Helper()
		if recompute {
			wire.ResponseSHA256 = conditionalPrefixScanResponseSHA(wire)
		}
		data, err := marshalWithoutHTMLEscape(wire)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeConditionalPrefixScanResult(data, request); ErrorCodeOf(err) != CodeProtocolViolation {
			t.Fatalf("%s error = %v", name, err)
		}
	}
	namespace := base
	namespace.Namespace = "other"
	assertRejected("namespace substitution", namespace, true)
	prefix := base
	prefix.Prefix = presentWireBytes([]byte("other/"))
	assertRejected("prefix substitution", prefix, true)
	key := base
	key.Entries = append([]wireConditionalPrefixScanEntry(nil), base.Entries...)
	key.Entries[0].Key = presentWireBytes([]byte("other/01"))
	assertRejected("out-of-prefix key", key, true)
	index := base
	index.Entries = append([]wireConditionalPrefixScanEntry(nil), base.Entries...)
	index.Entries[1].Index.Value = 7
	assertRejected("non-contiguous index", index, true)
	order := base
	order.Entries = append([]wireConditionalPrefixScanEntry(nil), base.Entries...)
	order.Entries[0], order.Entries[1] = order.Entries[1], order.Entries[0]
	assertRejected("key order", order, true)
	value := base
	value.Entries = append([]wireConditionalPrefixScanEntry(nil), base.Entries...)
	value.Entries[0].Value = presentWireBytes([]byte("changed"))
	assertRejected("digest drift", value, false)
	badNext := base
	badNext.NextCursor = presentNullableString("0123456789abcdef0123456789abcdef")
	assertRejected("next cursor without expiry", badNext, true)

	valid, _ := marshalWithoutHTMLEscape(base)
	malformed := [][]byte{
		append([]byte(`{"unknown":true,`), valid[1:]...),
		append(append([]byte(nil), valid...), []byte("null")...),
		bytes.Replace(valid, []byte("events"), []byte{'e', 'v', 0xff, 'n', 't', 's'}, 1),
		bytes.Replace(valid, []byte(`"observed_revision":"4"`), []byte(`"observed_revision":4`), 1),
		bytes.Replace(valid, []byte(`"cursor":null`), []byte(`"omitted_cursor":null`), 1),
	}
	for index, data := range malformed {
		if _, err := decodeConditionalPrefixScanResult(data, request); ErrorCodeOf(err) != CodeProtocolViolation {
			t.Fatalf("malformed response %d error = %v", index, err)
		}
	}
}

func TestConditionalPrefixScanRequestAndResponseBounds(t *testing.T) {
	for _, test := range []struct {
		name      string
		requestID string
		namespace string
		prefix    []byte
		limit     uint16
		revision  *uint64
	}{
		{name: "empty request ID", namespace: "events", prefix: []byte("x"), limit: 1},
		{name: "whitespace request ID", requestID: " \t\n", namespace: "events", prefix: []byte("x"), limit: 1},
		{name: "invalid UTF-8 request ID", requestID: string([]byte{0xff}), namespace: "events", prefix: []byte("x"), limit: 1},
		{name: "oversized request ID", requestID: string(bytes.Repeat([]byte("x"), 129)), namespace: "events", prefix: []byte("x"), limit: 1},
		{name: "invalid namespace", requestID: "scan", namespace: "../events", prefix: []byte("x"), limit: 1},
		{name: "empty prefix", requestID: "scan", namespace: "events", limit: 1},
		{name: "oversized prefix", requestID: "scan", namespace: "events", prefix: bytes.Repeat([]byte("x"), maxConditionalPrefixScanPrefixBytes+1), limit: 1},
		{name: "zero limit", requestID: "scan", namespace: "events", prefix: []byte("x")},
		{name: "zero revision", requestID: "scan", namespace: "events", prefix: []byte("x"), limit: 1, revision: uint64TestPointer(0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewConditionalPrefixScanRequest(test.requestID, test.namespace, test.prefix, test.limit, test.revision); ErrorCodeOf(err) != CodeInvalidArgument {
				t.Fatalf("error = %v", err)
			}
		})
	}

	request, _ := NewConditionalPrefixScanRequest("scan-bounds", "events", []byte("outbox/"), 1, nil)
	oversized := prefixScanWireFixture(request, 1, 2, 0, [][]byte{[]byte("outbox/01")}, [][]byte{bytes.Repeat([]byte{1}, maxConditionalPrefixScanValueBytes+1)})
	oversized.ResponseSHA256 = conditionalPrefixScanResponseSHA(oversized)
	data, err := json.Marshal(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeConditionalPrefixScanResult(data, request); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("oversized response error = %v", err)
	}
}

func prefixScanWireFixture(request ConditionalPrefixScanRequest, observed, snapshot, position uint64, keys, values [][]byte) wireConditionalPrefixScanResult {
	entries := make([]wireConditionalPrefixScanEntry, len(keys))
	for index := range keys {
		entries[index] = wireConditionalPrefixScanEntry{
			Index: canonicalUint64{Value: position + uint64(index), Present: true},
			Key:   presentWireBytes(keys[index]), Value: presentWireBytes(values[index]),
		}
	}
	wire, err := encodeConditionalPrefixScanRequest(request)
	if err != nil {
		panic(err)
	}
	return wireConditionalPrefixScanResult{
		Version: wire.Version, RequestID: wire.RequestID, Namespace: wire.Namespace, Prefix: wire.Prefix, Limit: wire.Limit,
		Cursor: wire.Cursor, Revision: wire.Revision,
		ObservedRevision: canonicalUint64{Value: observed, Present: true}, SnapshotRevision: canonicalUint64{Value: snapshot, Present: true},
		Position: canonicalUint64{Value: position, Present: true}, Entries: entries,
		NextCursor: wireNullableString{Present: true, Null: true}, NextCursorExpiresAtUnixMS: wireNullableCanonicalUint64{Present: true, Null: true},
	}
}

func presentNullableString(value string) wireNullableString {
	return wireNullableString{Value: value, Present: true}
}

func presentNullableUint64(value uint64) wireNullableCanonicalUint64 {
	return wireNullableCanonicalUint64{Value: value, Present: true}
}

func uint64TestPointer(value uint64) *uint64 { return &value }
