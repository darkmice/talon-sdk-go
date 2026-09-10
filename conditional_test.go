package talon

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
)

func uint64Pointer(value uint64) *uint64 { return &value }

func rawJSON(value string) json.RawMessage { return json.RawMessage(value) }

func mustBuildConditional(t *testing.T, namespace, requestID string, conditions []ConditionalTransactionCondition, mutations []ConditionalTransactionMutation) builtConditionalRequest {
	t.Helper()
	request, err := NewConditionalTransactionRequest(namespace, requestID, conditions, mutations)
	if err != nil {
		t.Fatal(err)
	}
	built, err := buildClosedConditionalRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	return built
}

func makeAppliedConditionalWire(t *testing.T, revision uint64, quorum bool) ([]byte, string) {
	t.Helper()
	conditions := []ConditionalTransactionCondition{{Key: []byte{1}, Expected: nil, Operator: CompareEqual}}
	mutations := []ConditionalTransactionMutation{ConditionalIncrement([]byte{2}, 1)}
	commandSHA, err := conditionalCommandSHA("n", conditions, mutations)
	if err != nil {
		t.Fatal(err)
	}
	receipt := wireConditionalReceipt{
		Version:       conditionalTransactionVersion,
		RequestID:     "req-1",
		CommandSHA256: commandSHA,
		Revision:      canonicalUint64{Value: revision, Present: true},
		Outcome:       string(ConditionalOutcomeApplied),
		Conditions: []wireConditionalObservation{{
			Index: uint64Pointer(0), Keyspace: "__user_conditional__.n", Key: presentWireBytes([]byte{1}),
			Before: nullableWireBytes(nil), After: nullableWireBytes(nil), Matched: rawJSON("true"),
		}},
		Mutations: []wireConditionalObservation{{
			Index: uint64Pointer(0), Keyspace: "__user_conditional__.n", Key: presentWireBytes([]byte{2}),
			Before: nullableWireBytes(nil), After: presentWireBytes([]byte{0, 0, 0, 0, 0, 0, 0, 1}),
			BeforeI64: rawJSON(`"0"`), AfterI64: rawJSON(`"1"`),
		}},
	}
	receipt.ReceiptSHA256, err = conditionalReceiptSHA(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receiptJSON, err := marshalWithoutHTMLEscape(receipt)
	if err != nil {
		t.Fatal(err)
	}
	applied, duplicate := true, false
	result := wireConditionalResult{
		Result: string(ConditionalResultApplied), OriginalResult: string(ConditionalOutcomeApplied),
		Applied: &applied, Duplicate: &duplicate, Revision: canonicalUint64{Value: revision, Present: true}, Receipt: receiptJSON,
	}
	if quorum {
		quorumCommandSHA, quorumErr := conditionalQuorumCommandSHA(conditionalTransactionVersion, "req-1", "n", conditions, mutations)
		if quorumErr != nil {
			t.Fatal(quorumErr)
		}
		canonicalReceipt, canonicalErr := conditionalReceiptCanonicalJSON(receipt, receipt.ReceiptSHA256)
		if canonicalErr != nil {
			t.Fatal(canonicalErr)
		}
		resultDigest := sha256.Sum256(canonicalReceipt)
		result.QuorumReceipt, err = marshalWithoutHTMLEscape(wireQuorumReceipt{
			Version: 1, RequestID: "req-1", Index: canonicalUint64{Value: revision, Present: true},
			Term: canonicalUint64{Value: 9_007_199_254_740_993, Present: true}, Stage: string(QuorumReceiptApplied),
			Durability: string(QuorumDurabilityQuorumFsync), CommandSHA256: quorumCommandSHA, ResultSHA256: rawJSON(`"` + hex.EncodeToString(resultDigest[:]) + `"`),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := marshalWithoutHTMLEscape(result)
	if err != nil {
		t.Fatal(err)
	}
	return data, commandSHA
}

func makeFoundConditionalLookup(t *testing.T, request builtConditionalRequest, revision uint64) []byte {
	t.Helper()
	keyspace := "__user_conditional__." + request.namespace
	conditions := make([]wireConditionalObservation, len(request.conditions))
	for index, condition := range request.conditions {
		before := cloneOptionalByteSlice(condition.Expected)
		matched := conditionalValueMatches(before, condition.Expected, condition.Operator)
		conditions[index] = wireConditionalObservation{
			Index: uint64Pointer(uint64(index)), Keyspace: keyspace, Key: presentWireBytes(condition.Key),
			Before: nullableWireBytes(before), After: nullableWireBytes(before), Matched: rawJSON(strconv.FormatBool(matched)),
		}
	}
	mutations := make([]wireConditionalObservation, len(request.mutations))
	for index, mutation := range request.mutations {
		observation := wireConditionalObservation{Index: uint64Pointer(uint64(index)), Keyspace: keyspace, Key: presentWireBytes(mutation.key), Before: nullableWireBytes(nil)}
		switch mutation.kind {
		case conditionalPut:
			observation.After = presentWireBytes(mutation.value)
		case conditionalDelete:
			observation.Before = presentWireBytes([]byte("old"))
			observation.After = nullableWireBytes(nil)
		case conditionalIncrement:
			after := make([]byte, 8)
			binary.BigEndian.PutUint64(after, uint64(mutation.delta))
			observation.After = presentWireBytes(after)
			observation.BeforeI64 = rawJSON(`"0"`)
			observation.AfterI64 = rawJSON(strconv.Quote(strconv.FormatInt(mutation.delta, 10)))
		}
		mutations[index] = observation
	}
	receipt := wireConditionalReceipt{
		Version: conditionalTransactionVersion, RequestID: request.requestID, CommandSHA256: request.commandSHA,
		Revision: canonicalUint64{Value: revision, Present: true}, Outcome: string(ConditionalOutcomeApplied),
		Conditions: conditions, Mutations: mutations,
	}
	var err error
	receipt.ReceiptSHA256, err = conditionalReceiptSHA(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receiptJSON, err := marshalWithoutHTMLEscape(receipt)
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalWithoutHTMLEscape(map[string]interface{}{
		"result": "receipt", "request_id": request.requestID, "revision": strconv.FormatUint(revision, 10), "receipt": json.RawMessage(receiptJSON),
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func rebindLookupCommand(t *testing.T, data []byte, commandSHA string) []byte {
	t.Helper()
	var lookup wireConditionalLookup
	if err := decodeStrictJSON(data, &lookup); err != nil {
		t.Fatal(err)
	}
	var receipt wireConditionalReceipt
	if err := decodeStrictJSON(lookup.Receipt, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.CommandSHA256 = commandSHA
	var err error
	receipt.ReceiptSHA256, err = conditionalReceiptSHA(receipt)
	if err != nil {
		t.Fatal(err)
	}
	lookup.Receipt, err = marshalWithoutHTMLEscape(receipt)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := marshalWithoutHTMLEscape(lookup)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestConditionalCommandEncodingMatchesCoreGolden(t *testing.T) {
	conditions := []ConditionalTransactionCondition{{Key: []byte{1}, Expected: nil, Operator: CompareEqual}}
	mutations := []ConditionalTransactionMutation{ConditionalIncrement([]byte{2}, -1)}
	digest, err := conditionalCommandSHA("n", conditions, mutations)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "41a30ee9dda8f286ab0c7899540f23b06f91b9debf7242fb326a5a9dcf594e96"
	if digest != expected {
		t.Fatalf("command SHA = %s, want Core golden %s", digest, expected)
	}
	request, _, err := buildConditionalRequest("n", "req-1", conditions, mutations)
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalWithoutHTMLEscape(request)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"key":[1]`)) || !bytes.Contains(data, []byte(`"delta":"-1"`)) || bytes.Contains(data, []byte("AQ==")) {
		t.Fatalf("request did not preserve Core byte-array/canonical-i64 wire: %s", data)
	}
}

func TestConditionalQuorumCommandEncodingMatchesCoreGolden(t *testing.T) {
	conditions := []ConditionalTransactionCondition{{Key: []byte{1}, Expected: nil, Operator: CompareEqual}}
	mutations := []ConditionalTransactionMutation{ConditionalIncrement([]byte{2}, -1)}
	digest, err := conditionalQuorumCommandSHA(conditionalTransactionVersion, "req-1", "n", conditions, mutations)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "6b2656bc60eeca7a149ab904620f4f29a5c7e964638ecdde03ba2b89da888828"
	if digest != expected {
		t.Fatalf("quorum command SHA = %s, want Core ConsensusCommand golden %s", digest, expected)
	}
}

func TestConditionalCommandEncodingMatchesCorePublicVerifierVector(t *testing.T) {
	conditions := []ConditionalTransactionCondition{{Key: []byte("flag"), Expected: nil, Operator: CompareEqual}}
	mutations := []ConditionalTransactionMutation{
		ConditionalPut([]byte("flag"), []byte("ready")),
		ConditionalIncrement([]byte("counter"), -2),
	}
	digest, err := conditionalCommandSHAForKeyspace("digest", conditions, mutations)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "9d4169cc161d4c28d7bc00a0d74d6a58e9a46bedcbe1514acb276edaf4b1209c"
	if digest != expected {
		t.Fatalf("public Core command digest = %s, want %s", digest, expected)
	}
}

func TestConditionalReceiptHashMatchesCoreGolden(t *testing.T) {
	receipt := wireConditionalReceipt{
		Version: 2, RequestID: "req-1", CommandSHA256: strings.Repeat("a", 64),
		Revision: canonicalUint64{Value: math.MaxUint64, Present: true}, Outcome: "applied",
		Conditions: []wireConditionalObservation{{Index: uint64Pointer(0), Keyspace: "__user_conditional__.n", Key: presentWireBytes([]byte{1}), Before: nullableWireBytes(nil), After: nullableWireBytes(nil), Matched: rawJSON("true")}},
		Mutations:  []wireConditionalObservation{{Index: uint64Pointer(0), Keyspace: "__user_conditional__.n", Key: presentWireBytes([]byte{2}), Before: nullableWireBytes(nil), After: presentWireBytes([]byte{0, 0, 0, 0, 0, 0, 0, 1}), BeforeI64: rawJSON(`"0"`), AfterI64: rawJSON(`"1"`)}},
	}
	digest, err := conditionalReceiptSHA(receipt)
	if err != nil {
		t.Fatal(err)
	}
	const expected = "c907355319ca4026943b6cbd9b38b09f21a8dd3c3f29499bf8efee3cafe85e97"
	if digest != expected {
		t.Fatalf("receipt SHA = %s, want Core golden %s", digest, expected)
	}
}

func TestConditionalResultPreservesU64AndReplay(t *testing.T) {
	data, _ := makeAppliedConditionalWire(t, math.MaxUint64, true)
	conditions := []ConditionalTransactionCondition{{Key: []byte{1}, Expected: nil, Operator: CompareEqual}}
	mutations := []ConditionalTransactionMutation{ConditionalIncrement([]byte{2}, 1)}
	request := mustBuildConditional(t, "n", "req-1", conditions, mutations)
	result, err := decodeConditionalTransactionResult(data, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision != math.MaxUint64 || result.QuorumReceipt == nil || result.QuorumReceipt.Index != math.MaxUint64 || result.QuorumReceipt.Term != 9_007_199_254_740_993 || !result.Applied || result.Duplicate {
		t.Fatalf("unexpected exact result: %#v", result)
	}

	replayed := bytes.Replace(data, []byte(`"result":"applied"`), []byte(`"result":"duplicate"`), 1)
	replayed = bytes.Replace(replayed, []byte(`"duplicate":false`), []byte(`"duplicate":true`), 1)
	result, err = decodeConditionalTransactionResult(replayed, request)
	if err != nil || !result.Duplicate || result.Result != ConditionalResultDuplicate {
		t.Fatalf("duplicate replay = %#v, %v", result, err)
	}
}

func TestConditionalConflictIsARejectedTypedResult(t *testing.T) {
	conditions := []ConditionalTransactionCondition{{Key: []byte{1}, Expected: nil, Operator: CompareEqual}}
	mutations := []ConditionalTransactionMutation{ConditionalPut([]byte{2}, []byte("new"))}
	commandSHA, err := conditionalCommandSHA("n", conditions, mutations)
	if err != nil {
		t.Fatal(err)
	}
	conflictJSON := rawJSON(`{"kind":"condition_failed","index":0}`)
	receipt := wireConditionalReceipt{
		Version: 2, RequestID: "req-1", CommandSHA256: commandSHA,
		Revision: canonicalUint64{Value: 7, Present: true}, Outcome: "conflict", Conflict: conflictJSON,
		Conditions: []wireConditionalObservation{{Index: uint64Pointer(0), Keyspace: "__user_conditional__.n", Key: presentWireBytes([]byte{1}), Before: presentWireBytes([]byte{9}), After: presentWireBytes([]byte{9}), Matched: rawJSON("false")}},
		Mutations:  []wireConditionalObservation{{Index: uint64Pointer(0), Keyspace: "__user_conditional__.n", Key: presentWireBytes([]byte{2}), Before: nullableWireBytes(nil), After: nullableWireBytes(nil)}},
	}
	receipt.ReceiptSHA256, err = conditionalReceiptSHA(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receiptJSON, _ := marshalWithoutHTMLEscape(receipt)
	applied, duplicate := false, false
	data, _ := marshalWithoutHTMLEscape(wireConditionalResult{Result: "conflict", OriginalResult: "conflict", Applied: &applied, Duplicate: &duplicate, Revision: canonicalUint64{Value: 7, Present: true}, Receipt: receiptJSON})
	request := mustBuildConditional(t, "n", "req-1", conditions, mutations)
	result, err := decodeConditionalTransactionResult(data, request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Result != ConditionalResultConflict || result.Applied || !result.Rejected || result.Receipt.Conflict == nil || result.Receipt.Conflict.Kind != ConditionalConflictConditionFailed {
		t.Fatalf("unexpected rejected result: %#v", result)
	}
	replayed := bytes.Replace(data, []byte(`"result":"conflict"`), []byte(`"result":"duplicate"`), 1)
	replayed = bytes.Replace(replayed, []byte(`"duplicate":false`), []byte(`"duplicate":true`), 1)
	result, err = decodeConditionalTransactionResult(replayed, request)
	if err != nil || result.Result != ConditionalResultDuplicate || result.OriginalResult != ConditionalOutcomeConflict || !result.Rejected || !result.Duplicate {
		t.Fatalf("replayed conflict = %#v, %v", result, err)
	}
}

func TestConditionalResultRejectsMalformedAndLossyWire(t *testing.T) {
	data, _ := makeAppliedConditionalWire(t, 9_007_199_254_740_993, true)
	conditions := []ConditionalTransactionCondition{{Key: []byte{1}, Expected: nil, Operator: CompareEqual}}
	mutations := []ConditionalTransactionMutation{ConditionalIncrement([]byte{2}, 1)}
	tests := []struct {
		name string
		data []byte
	}{
		{name: "top revision number", data: bytes.Replace(data, []byte(`"revision":"9007199254740993"`), []byte(`"revision":9007199254740993`), 1)},
		{name: "top revision leading zero", data: bytes.Replace(data, []byte(`"revision":"9007199254740993"`), []byte(`"revision":"09007199254740993"`), 1)},
		{name: "quorum term number", data: bytes.Replace(data, []byte(`"term":"9007199254740993"`), []byte(`"term":9007199254740993`), 1)},
		{name: "quorum index overflow", data: bytes.Replace(data, []byte(`"index":"9007199254740993"`), []byte(`"index":"18446744073709551616"`), 1)},
		{name: "i64 number", data: bytes.Replace(data, []byte(`"before_i64":"0"`), []byte(`"before_i64":0`), 1)},
		{name: "unknown result", data: bytes.Replace(data, []byte(`"result":"applied"`), []byte(`"result":"mystery"`), 1)},
		{name: "inconsistent applied", data: bytes.Replace(data, []byte(`"applied":true`), []byte(`"applied":false`), 1)},
		{name: "tampered receipt hash", data: bytes.Replace(data, []byte(`"receipt_sha256":"`), []byte(`"receipt_sha256":"f`), 1)},
		{name: "invalid UTF-8 response", data: bytes.Replace(data, []byte("req-1"), []byte{'r', 'e', 'q', '-', 0xff}, 1)},
		{name: "duplicate key", data: append([]byte(`{"result":"applied",`), data[1:]...)},
		{name: "trailing JSON", data: append(append([]byte(nil), data...), []byte("null")...)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := mustBuildConditional(t, "n", "req-1", conditions, mutations)
			if _, err := decodeConditionalTransactionResult(test.data, request); ErrorCodeOf(err) != CodeProtocolViolation {
				t.Fatalf("malformed wire error = %v", err)
			}
		})
	}
}

func TestStrictJSONRejectsInvalidUTF8(t *testing.T) {
	var value map[string]string
	if err := decodeStrictJSON([]byte{'{', '"', 'v', '"', ':', '"', 0xff, '"', '}'}, &value); err == nil {
		t.Fatal("invalid UTF-8 JSON was accepted")
	}
}

func TestConditionalConflictRejectsFalseSemanticEvidence(t *testing.T) {
	conditions := []ConditionalTransactionCondition{{Key: []byte("guard"), Expected: []byte("expected"), Operator: CompareEqual}}
	mutations := []ConditionalTransactionMutation{ConditionalPut([]byte("value"), []byte("new"))}
	commandSHA, err := conditionalCommandSHA("n", conditions, mutations)
	if err != nil {
		t.Fatal(err)
	}
	applied, duplicate := false, false
	base := wireConditionalReceipt{
		Version: 2, RequestID: "req-1", CommandSHA256: commandSHA,
		Revision: canonicalUint64{Value: 7, Present: true}, Outcome: "conflict",
		Conflict:   rawJSON(`{"kind":"condition_failed","index":0}`),
		Conditions: []wireConditionalObservation{{Index: uint64Pointer(0), Keyspace: "__user_conditional__.n", Key: presentWireBytes([]byte("guard")), Before: presentWireBytes([]byte("actual")), After: presentWireBytes([]byte("actual")), Matched: rawJSON("false")}},
		Mutations:  []wireConditionalObservation{{Index: uint64Pointer(0), Keyspace: "__user_conditional__.n", Key: presentWireBytes([]byte("value")), Before: nullableWireBytes(nil), After: nullableWireBytes(nil)}},
	}
	assertRejected := func(name string, receipt wireConditionalReceipt) {
		t.Helper()
		receipt.ReceiptSHA256, err = conditionalReceiptSHA(receipt)
		if err != nil {
			t.Fatal(err)
		}
		receiptJSON, marshalErr := marshalWithoutHTMLEscape(receipt)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		data, marshalErr := marshalWithoutHTMLEscape(wireConditionalResult{Result: "conflict", OriginalResult: "conflict", Applied: &applied, Duplicate: &duplicate, Revision: canonicalUint64{Value: 7, Present: true}, Receipt: receiptJSON})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		request := mustBuildConditional(t, "n", "req-1", conditions, mutations)
		if _, decodeErr := decodeConditionalTransactionResult(data, request); ErrorCodeOf(decodeErr) != CodeProtocolViolation {
			t.Fatalf("%s error = %v", name, decodeErr)
		}
	}

	changedCondition := base
	changedCondition.Conditions = append([]wireConditionalObservation(nil), base.Conditions...)
	changedCondition.Conditions[0].After = presentWireBytes([]byte("tamper"))
	assertRejected("changed condition after", changedCondition)

	wrongConflict := base
	wrongConflict.Conflict = rawJSON(`{"kind":"counter_invalid","index":0}`)
	assertRejected("counter conflict with rejected condition", wrongConflict)
}

func TestConditionalCounterConflictMustMatchIncrementEvidence(t *testing.T) {
	conditions := []ConditionalTransactionCondition{}
	putMutations := []ConditionalTransactionMutation{ConditionalPut([]byte("counter"), []byte("value"))}
	putSHA, err := conditionalCommandSHA("n", conditions, putMutations)
	if err != nil {
		t.Fatal(err)
	}
	applied, duplicate := false, false
	makeData := func(receipt wireConditionalReceipt) []byte {
		t.Helper()
		receipt.ReceiptSHA256, err = conditionalReceiptSHA(receipt)
		if err != nil {
			t.Fatal(err)
		}
		receiptJSON, _ := marshalWithoutHTMLEscape(receipt)
		data, _ := marshalWithoutHTMLEscape(wireConditionalResult{Result: "conflict", OriginalResult: "conflict", Applied: &applied, Duplicate: &duplicate, Revision: canonicalUint64{Value: 8, Present: true}, Receipt: receiptJSON})
		return data
	}
	putReceipt := wireConditionalReceipt{
		Version: 2, RequestID: "req-1", CommandSHA256: putSHA, Revision: canonicalUint64{Value: 8, Present: true}, Outcome: "conflict",
		Conflict: rawJSON(`{"kind":"counter_invalid","index":0}`), Conditions: []wireConditionalObservation{},
		Mutations: []wireConditionalObservation{{Index: uint64Pointer(0), Keyspace: "__user_conditional__.n", Key: presentWireBytes([]byte("counter")), Before: presentWireBytes([]byte{1}), After: presentWireBytes([]byte{1})}},
	}
	if _, decodeErr := decodeConditionalTransactionResult(makeData(putReceipt), mustBuildConditional(t, "n", "req-1", conditions, putMutations)); ErrorCodeOf(decodeErr) != CodeProtocolViolation {
		t.Fatalf("counter conflict on put error = %v", decodeErr)
	}

	increments := []ConditionalTransactionMutation{ConditionalIncrement([]byte("counter"), 1)}
	incrementSHA, err := conditionalCommandSHA("n", conditions, increments)
	if err != nil {
		t.Fatal(err)
	}
	overflowReceipt := wireConditionalReceipt{
		Version: 2, RequestID: "req-1", CommandSHA256: incrementSHA, Revision: canonicalUint64{Value: 8, Present: true}, Outcome: "conflict",
		Conflict: rawJSON(`{"kind":"counter_underflow","index":0}`), Conditions: []wireConditionalObservation{},
		Mutations: []wireConditionalObservation{{Index: uint64Pointer(0), Keyspace: "__user_conditional__.n", Key: presentWireBytes([]byte("counter")), Before: presentWireBytes([]byte{0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}), After: presentWireBytes([]byte{0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}), BeforeI64: rawJSON(`"9223372036854775807"`), AfterI64: rawJSON(`"9223372036854775807"`)}},
	}
	if _, decodeErr := decodeConditionalTransactionResult(makeData(overflowReceipt), mustBuildConditional(t, "n", "req-1", conditions, increments)); ErrorCodeOf(decodeErr) != CodeProtocolViolation {
		t.Fatalf("wrong overflow direction error = %v", decodeErr)
	}
}

func TestConditionalResultRejectsSameLengthReceiptAndResultHashTampering(t *testing.T) {
	data, _ := makeAppliedConditionalWire(t, 9_007_199_254_740_993, true)
	conditions := []ConditionalTransactionCondition{{Key: []byte{1}, Expected: nil, Operator: CompareEqual}}
	mutations := []ConditionalTransactionMutation{ConditionalIncrement([]byte{2}, 1)}
	var result wireConditionalResult
	if err := decodeStrictJSON(data, &result); err != nil {
		t.Fatal(err)
	}

	var receipt wireConditionalReceipt
	if err := decodeStrictJSON(result.Receipt, &receipt); err != nil {
		t.Fatal(err)
	}
	receipt.ReceiptSHA256 = mutateSHA256(receipt.ReceiptSHA256)
	result.Receipt, _ = marshalWithoutHTMLEscape(receipt)
	tamperedReceipt, _ := marshalWithoutHTMLEscape(result)
	request := mustBuildConditional(t, "n", "req-1", conditions, mutations)
	if _, err := decodeConditionalTransactionResult(tamperedReceipt, request); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("same-length receipt hash tamper error = %v", err)
	}

	data, _ = makeAppliedConditionalWire(t, 9_007_199_254_740_993, true)
	if err := decodeStrictJSON(data, &result); err != nil {
		t.Fatal(err)
	}
	var quorum wireQuorumReceipt
	if err := decodeStrictJSON(result.QuorumReceipt, &quorum); err != nil {
		t.Fatal(err)
	}
	var resultSHA string
	if err := decodeStrictJSON(quorum.ResultSHA256, &resultSHA); err != nil {
		t.Fatal(err)
	}
	resultSHA = mutateSHA256(resultSHA)
	quorum.ResultSHA256 = rawJSON(strconv.Quote(resultSHA))
	result.QuorumReceipt, _ = marshalWithoutHTMLEscape(quorum)
	tamperedResult, _ := marshalWithoutHTMLEscape(result)
	if _, err := decodeConditionalTransactionResult(tamperedResult, request); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("same-length quorum result hash tamper error = %v", err)
	}
}

func mutateSHA256(value string) string {
	if value[0] == '0' {
		return "1" + value[1:]
	}
	return "0" + value[1:]
}

func TestConditionalReceiptLookupIndeterminateIsTyped(t *testing.T) {
	request := mustBuildConditional(t, "n", "req-1", []ConditionalTransactionCondition{{Key: []byte{1}, Expected: nil, Operator: CompareEqual}}, []ConditionalTransactionMutation{ConditionalIncrement([]byte{2}, 1)})
	quorum, err := marshalWithoutHTMLEscape(wireQuorumReceipt{
		Version: 1, RequestID: "req-1", Index: canonicalUint64{Value: math.MaxUint64, Present: true},
		Term: canonicalUint64{Value: 9_007_199_254_740_993, Present: true}, Stage: string(QuorumReceiptCommitted),
		Durability: string(QuorumDurabilityQuorumFsync), CommandSHA256: request.quorumCommandSHA, ResultSHA256: rawJSON("null"),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalWithoutHTMLEscape(map[string]interface{}{"result": "indeterminate", "request_id": "req-1", "quorum_receipt": json.RawMessage(quorum)})
	if err != nil {
		t.Fatal(err)
	}
	lookup, err := decodeConditionalReceiptLookup(data, request)
	if err != nil {
		t.Fatal(err)
	}
	if lookup.Status != ConditionalReceiptIndeterminate || lookup.Receipt != nil || lookup.QuorumReceipt == nil || lookup.QuorumReceipt.Index != math.MaxUint64 {
		t.Fatalf("unexpected indeterminate lookup: %#v", lookup)
	}
	var quorumWire wireQuorumReceipt
	if err := decodeStrictJSON(quorum, &quorumWire); err != nil {
		t.Fatal(err)
	}
	quorumWire.CommandSHA256 = mutateSHA256(quorumWire.CommandSHA256)
	tamperedQuorum, _ := marshalWithoutHTMLEscape(quorumWire)
	tampered, _ := marshalWithoutHTMLEscape(map[string]interface{}{"result": "indeterminate", "request_id": "req-1", "quorum_receipt": json.RawMessage(tamperedQuorum)})
	if _, err := decodeConditionalReceiptLookup(tampered, request); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("indeterminate lookup accepted another command: %v", err)
	}
}

func TestConditionalReceiptLookupFoundCrossChecksRevision(t *testing.T) {
	data, _ := makeAppliedConditionalWire(t, 9_007_199_254_740_993, true)
	request := mustBuildConditional(t, "n", "req-1", []ConditionalTransactionCondition{{Key: []byte{1}, Expected: nil, Operator: CompareEqual}}, []ConditionalTransactionMutation{ConditionalIncrement([]byte{2}, 1)})
	var result wireConditionalResult
	if err := decodeStrictJSON(data, &result); err != nil {
		t.Fatal(err)
	}
	lookupData, err := marshalWithoutHTMLEscape(map[string]interface{}{
		"result": "receipt", "request_id": "req-1", "revision": "9007199254740993",
		"receipt": json.RawMessage(result.Receipt), "quorum_receipt": json.RawMessage(result.QuorumReceipt),
	})
	if err != nil {
		t.Fatal(err)
	}
	lookup, err := decodeConditionalReceiptLookup(lookupData, request)
	if err != nil {
		t.Fatal(err)
	}
	if lookup.Status != ConditionalReceiptFound || lookup.Revision == nil || *lookup.Revision != 9_007_199_254_740_993 || lookup.Receipt == nil || lookup.QuorumReceipt == nil {
		t.Fatalf("unexpected found lookup: %#v", lookup)
	}
	tampered := bytes.Replace(lookupData, []byte(`"revision":"9007199254740993"`), []byte(`"revision":"9007199254740994"`), 1)
	if _, err := decodeConditionalReceiptLookup(tampered, request); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("revision mismatch error = %v", err)
	}
}

func TestConditionalReceiptLookupBindsCompleteOriginalTransaction(t *testing.T) {
	requestID := "same-request-id"
	conditions := []ConditionalTransactionCondition{
		{Key: []byte("guard-a"), Expected: []byte("ready"), Operator: CompareEqual},
		{Key: []byte("guard-b"), Expected: nil, Operator: CompareEqual},
	}
	mutations := []ConditionalTransactionMutation{
		ConditionalPut([]byte("value"), []byte("new")),
		ConditionalIncrement([]byte("counter"), 2),
		ConditionalDelete([]byte("obsolete")),
	}
	original := mustBuildConditional(t, "tenant", requestID, conditions, mutations)
	lookupData := makeFoundConditionalLookup(t, original, 19)
	if lookup, err := decodeConditionalReceiptLookup(lookupData, original); err != nil || lookup.Receipt == nil {
		t.Fatalf("exact request lookup = %#v, %v", lookup, err)
	}

	cloneConditions := func() []ConditionalTransactionCondition {
		return cloneConditionalTransactionConditions(conditions)
	}
	cloneMutations := func() []ConditionalTransactionMutation {
		return cloneConditionalTransactionMutations(mutations)
	}
	tests := []struct {
		name       string
		namespace  string
		conditions []ConditionalTransactionCondition
		mutations  []ConditionalTransactionMutation
	}{
		{name: "namespace", namespace: "other", conditions: cloneConditions(), mutations: cloneMutations()},
		{name: "condition key", namespace: "tenant", conditions: func() []ConditionalTransactionCondition {
			value := cloneConditions()
			value[0].Key = []byte("guard-x")
			return value
		}(), mutations: cloneMutations()},
		{name: "condition expected", namespace: "tenant", conditions: func() []ConditionalTransactionCondition {
			value := cloneConditions()
			value[0].Expected = []byte("stale")
			return value
		}(), mutations: cloneMutations()},
		{name: "condition operator", namespace: "tenant", conditions: func() []ConditionalTransactionCondition {
			value := cloneConditions()
			value[0].Operator = CompareNotEqual
			return value
		}(), mutations: cloneMutations()},
		{name: "condition order", namespace: "tenant", conditions: func() []ConditionalTransactionCondition {
			value := cloneConditions()
			value[0], value[1] = value[1], value[0]
			return value
		}(), mutations: cloneMutations()},
		{name: "mutation kind", namespace: "tenant", conditions: cloneConditions(), mutations: func() []ConditionalTransactionMutation {
			value := cloneMutations()
			value[0] = ConditionalDelete([]byte("value"))
			return value
		}()},
		{name: "mutation key", namespace: "tenant", conditions: cloneConditions(), mutations: func() []ConditionalTransactionMutation {
			value := cloneMutations()
			value[0] = ConditionalPut([]byte("other"), []byte("new"))
			return value
		}()},
		{name: "same-length put value", namespace: "tenant", conditions: cloneConditions(), mutations: func() []ConditionalTransactionMutation {
			value := cloneMutations()
			value[0] = ConditionalPut([]byte("value"), []byte("nex"))
			return value
		}()},
		{name: "increment delta", namespace: "tenant", conditions: cloneConditions(), mutations: func() []ConditionalTransactionMutation {
			value := cloneMutations()
			value[1] = ConditionalIncrement([]byte("counter"), 3)
			return value
		}()},
		{name: "mutation order", namespace: "tenant", conditions: cloneConditions(), mutations: func() []ConditionalTransactionMutation {
			value := cloneMutations()
			value[0], value[1] = value[1], value[0]
			return value
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			alternate := mustBuildConditional(t, test.namespace, requestID, test.conditions, test.mutations)
			// Recompute both command and receipt hashes for the substituted
			// request. The unchanged observations must still be rejected.
			substituted := rebindLookupCommand(t, lookupData, alternate.commandSHA)
			if _, err := decodeConditionalReceiptLookup(substituted, alternate); ErrorCodeOf(err) != CodeProtocolViolation {
				t.Fatalf("substituted receipt error = %v", err)
			}
		})
	}
}

func TestConditionalReceiptLookupBindsQuorumCommand(t *testing.T) {
	data, _ := makeAppliedConditionalWire(t, 21, true)
	request := mustBuildConditional(t, "n", "req-1", []ConditionalTransactionCondition{{Key: []byte{1}, Operator: CompareEqual}}, []ConditionalTransactionMutation{ConditionalIncrement([]byte{2}, 1)})
	var result wireConditionalResult
	if err := decodeStrictJSON(data, &result); err != nil {
		t.Fatal(err)
	}
	var quorum wireQuorumReceipt
	if err := decodeStrictJSON(result.QuorumReceipt, &quorum); err != nil {
		t.Fatal(err)
	}
	quorum.CommandSHA256 = mutateSHA256(quorum.CommandSHA256)
	result.QuorumReceipt, _ = marshalWithoutHTMLEscape(quorum)
	tamperedResult, _ := marshalWithoutHTMLEscape(result)
	if _, err := decodeConditionalTransactionResult(tamperedResult, request); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("transaction accepted substituted quorum command: %v", err)
	}

	lookupData, _ := marshalWithoutHTMLEscape(map[string]interface{}{
		"result": "receipt", "request_id": "req-1", "revision": "21",
		"receipt": json.RawMessage(result.Receipt), "quorum_receipt": json.RawMessage(result.QuorumReceipt),
	})
	if _, err := decodeConditionalReceiptLookup(lookupData, request); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("lookup accepted substituted quorum command: %v", err)
	}
}

func TestConditionalReceiptLookupRejectsMalformedWire(t *testing.T) {
	request := mustBuildConditional(t, "n", "req-1", []ConditionalTransactionCondition{{Key: []byte("guard"), Operator: CompareEqual}}, []ConditionalTransactionMutation{ConditionalPut([]byte("value"), []byte("new"))})
	data := makeFoundConditionalLookup(t, request, 4)
	tests := [][]byte{
		append([]byte(`{"unknown":true,`), data[1:]...),
		append([]byte(`{"result":"receipt",`), data[1:]...),
		append(append([]byte(nil), data...), []byte("null")...),
		bytes.Replace(data, []byte("req-1"), []byte{'r', 'e', 'q', '-', 0xff}, 1),
	}
	for index, malformed := range tests {
		if _, err := decodeConditionalReceiptLookup(malformed, request); ErrorCodeOf(err) != CodeProtocolViolation {
			t.Fatalf("malformed lookup %d error = %v", index, err)
		}
	}
}

func TestConditionalTransactionRequestIsSealedAndBounded(t *testing.T) {
	conditionKey, expected := []byte("guard"), []byte("ready")
	mutationKey, mutationValue := []byte("value"), []byte("new")
	conditions := []ConditionalTransactionCondition{{Key: conditionKey, Expected: expected, Operator: CompareEqual}}
	mutations := []ConditionalTransactionMutation{ConditionalPut(mutationKey, mutationValue)}
	request, err := NewConditionalTransactionRequest("tenant", "req-1", conditions, mutations)
	if err != nil {
		t.Fatal(err)
	}
	originalSHA := request.CommandSHA256()
	conditionKey[0], expected[0], mutationKey[0], mutationValue[0] = 'x', 'x', 'x', 'x'
	conditions[0].Operator = CompareNotEqual
	mutations[0] = ConditionalDelete([]byte("other"))
	built, err := buildClosedConditionalRequest(request)
	if err != nil || built.commandSHA != originalSHA || string(built.conditions[0].Key) != "guard" || string(built.conditions[0].Expected) != "ready" || built.conditions[0].Operator != CompareEqual || string(built.mutations[0].key) != "value" || string(built.mutations[0].value) != "new" {
		t.Fatalf("sealed request changed: %#v, %v", built, err)
	}
	if _, err := buildClosedConditionalRequest(ConditionalTransactionRequest{}); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("zero request error = %v", err)
	}
	if _, err := NewConditionalTransactionRequest("tenant", conditionalRaftRequestPrefix+"node:1", nil, []ConditionalTransactionMutation{ConditionalPut([]byte("value"), []byte("new"))}); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("reserved Raft request ID error = %v", err)
	}

	largeValue := bytes.Repeat([]byte{255}, maxConditionalValueBytes)
	largeMutations := make([]ConditionalTransactionMutation, 9)
	for index := range largeMutations {
		largeMutations[index] = ConditionalPut([]byte("large-"+strconv.Itoa(index)), largeValue)
	}
	if _, err := NewConditionalTransactionRequest("tenant", "oversized", nil, largeMutations); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("aggregate oversized request error = %v", err)
	}
}

func TestConditionalRequestDigestBindsExactRetryAndFailedIncrementDelta(t *testing.T) {
	original := mustBuildConditional(t, "tenant", "same-id", nil, []ConditionalTransactionMutation{ConditionalIncrement([]byte("counter"), 1)})
	exactRetry := mustBuildConditional(t, "tenant", "same-id", nil, []ConditionalTransactionMutation{ConditionalIncrement([]byte("counter"), 1)})
	differentDelta := mustBuildConditional(t, "tenant", "same-id", nil, []ConditionalTransactionMutation{ConditionalIncrement([]byte("counter"), 2)})
	if original.commandSHA != exactRetry.commandSHA || original.commandSHA == differentDelta.commandSHA {
		t.Fatalf("exact retry/delta digests = %s, %s, %s", original.commandSHA, exactRetry.commandSHA, differentDelta.commandSHA)
	}

	maxBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(maxBytes, uint64(math.MaxInt64))
	receipt := wireConditionalReceipt{
		Version: conditionalTransactionVersion, RequestID: original.requestID, CommandSHA256: differentDelta.commandSHA,
		Revision: canonicalUint64{Value: 9, Present: true}, Outcome: string(ConditionalOutcomeConflict),
		Conflict:   rawJSON(`{"kind":"counter_overflow","index":0}`),
		Conditions: []wireConditionalObservation{},
		Mutations: []wireConditionalObservation{{
			Index: uint64Pointer(0), Keyspace: "__user_conditional__.tenant", Key: presentWireBytes([]byte("counter")),
			Before: presentWireBytes(maxBytes), After: presentWireBytes(maxBytes), BeforeI64: rawJSON(`"9223372036854775807"`), AfterI64: rawJSON(`"9223372036854775807"`),
		}},
	}
	var err error
	receipt.ReceiptSHA256, err = conditionalReceiptSHA(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receiptJSON, _ := marshalWithoutHTMLEscape(receipt)
	lookupData, _ := marshalWithoutHTMLEscape(map[string]interface{}{
		"result": "receipt", "request_id": original.requestID, "revision": "9", "receipt": json.RawMessage(receiptJSON),
	})
	if _, err := decodeConditionalReceiptLookup(lookupData, original); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("lookup accepted failed increment receipt for another delta: %v", err)
	}

	db := &DB{nativeInfo: NativeInfo{
		Features:     []string{"native_conditional_transaction_v2", "conditional_transaction_command_digest_v1"},
		Capabilities: []NativeCapability{{Name: "native_conditional_transaction_v2", Version: 2, Status: "available"}},
	}}
	request, _ := NewConditionalTransactionRequest("tenant", "same-id", nil, []ConditionalTransactionMutation{ConditionalIncrement([]byte("counter"), 1)})
	if _, err := db.ConditionalTransactionReceiptFor(request); ErrorCodeOf(err) != CodeDatabaseClosed {
		t.Fatalf("receipt-for did not reach the native boundary with an exact request: %v", err)
	}
	if _, err := db.ConditionalTransactionReceipt(request); ErrorCodeOf(err) != CodeDatabaseClosed {
		t.Fatalf("receipt compatibility alias diverged: %v", err)
	}
}

func TestCanonicalIntegerWireAcceptsExtremesAndRejectsLossyForms(t *testing.T) {
	for _, value := range []int64{math.MinInt64, -9_007_199_254_740_993, 0, 9_007_199_254_740_993, math.MaxInt64} {
		var decoded canonicalInt64
		if err := decodeStrictJSON([]byte(strconv.Quote(strconv.FormatInt(value, 10))), &decoded); err != nil || decoded.Value != value {
			t.Fatalf("canonical i64 %d decoded as %d, %v", value, decoded.Value, err)
		}
	}
	for _, invalid := range []string{`0`, `"-0"`, `"+1"`, `"01"`, `"1e3"`, `"9223372036854775808"`, `"-9223372036854775809"`} {
		var decoded canonicalInt64
		if err := decodeStrictJSON([]byte(invalid), &decoded); err == nil {
			t.Fatalf("invalid canonical i64 %s was accepted", invalid)
		}
	}
	for _, invalid := range []string{`18446744073709551615`, `"-1"`, `"00"`, `"1.0"`, `"18446744073709551616"`} {
		var decoded canonicalUint64
		if err := decodeStrictJSON([]byte(invalid), &decoded); err == nil {
			t.Fatalf("invalid canonical u64 %s was accepted", invalid)
		}
	}
}

func TestConditionalCapabilityRequiresSelfAttestedFeature(t *testing.T) {
	db := &DB{nativeInfo: NativeInfo{
		Features:     []string{"native_conditional_transaction_v2", "conditional_transaction_command_digest_v1"},
		Capabilities: []NativeCapability{{Name: "native_conditional_transaction_v2", Version: 2, Status: "available"}},
	}}
	if err := db.RequireCapability("native_conditional_transaction_v2"); err != nil {
		t.Fatalf("available self-attested v2 capability was rejected: %v", err)
	}
	for _, features := range [][]string{{"native_conditional_transaction_v2"}, {"conditional_transaction_command_digest_v1"}, nil} {
		db.nativeInfo.Features = features
		if err := db.RequireCapability("native_conditional_transaction_v2"); ErrorCodeOf(err) != CodeCapabilityUnavailable {
			t.Fatalf("v2 with incomplete feature set %v error = %v", features, err)
		}
	}
}

func TestConditionalRequestValidation(t *testing.T) {
	validCondition := []ConditionalTransactionCondition{{Key: []byte("guard"), Expected: []byte{}, Operator: CompareEqual}}
	validMutation := []ConditionalTransactionMutation{ConditionalPut([]byte("value"), nil)}
	if _, _, err := buildConditionalRequest("safe.ns", "request:1", validCondition, validMutation); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	sameKey := []byte("cas")
	if _, _, err := buildConditionalRequest("safe.ns", "request:cas", []ConditionalTransactionCondition{{Key: sameKey, Operator: CompareEqual}}, []ConditionalTransactionMutation{ConditionalPut(append([]byte(nil), sameKey...), []byte("new"))}); err != nil {
		t.Fatalf("condition/mutation CAS on the same key was rejected: %v", err)
	}
	for _, test := range []struct {
		name       string
		namespace  string
		requestID  string
		conditions []ConditionalTransactionCondition
		mutations  []ConditionalTransactionMutation
	}{
		{name: "unsafe namespace", namespace: "../x", requestID: "r", conditions: validCondition, mutations: validMutation},
		{name: "unsafe request id", namespace: "n", requestID: "with space", conditions: validCondition, mutations: validMutation},
		{name: "unknown operator", namespace: "n", requestID: "r", conditions: []ConditionalTransactionCondition{{Operator: "contains"}}, mutations: validMutation},
		{name: "duplicate condition key", namespace: "n", requestID: "r", conditions: []ConditionalTransactionCondition{{Key: []byte("same"), Operator: CompareEqual}, {Key: []byte("same"), Operator: CompareNotEqual}}, mutations: validMutation},
		{name: "duplicate empty condition key", namespace: "n", requestID: "r", conditions: []ConditionalTransactionCondition{{Key: nil, Operator: CompareEqual}, {Key: []byte{}, Operator: CompareNotEqual}}, mutations: validMutation},
		{name: "duplicate mutation key", namespace: "n", requestID: "r", conditions: validCondition, mutations: []ConditionalTransactionMutation{ConditionalPut([]byte("same"), []byte("first")), ConditionalIncrement([]byte("same"), 1)}},
		{name: "duplicate empty mutation key", namespace: "n", requestID: "r", conditions: validCondition, mutations: []ConditionalTransactionMutation{ConditionalPut(nil, []byte("first")), ConditionalDelete([]byte{})}},
		{name: "no mutations", namespace: "n", requestID: "r", conditions: validCondition},
		{name: "unconstructed mutation", namespace: "n", requestID: "r", conditions: validCondition, mutations: []ConditionalTransactionMutation{{}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := buildConditionalRequest(test.namespace, test.requestID, test.conditions, test.mutations); ErrorCodeOf(err) != CodeInvalidArgument {
				t.Fatalf("invalid request error = %v", err)
			}
		})
	}
}

func TestConditionalTransactionRejectsDuplicateKeysBeforeNative(t *testing.T) {
	db := &DB{}
	if _, err := db.ConditionalTransaction("n", "r", nil, []ConditionalTransactionMutation{ConditionalPut(nil, nil)}); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("empty key should fail before native access: %v", err)
	}
	conditions := []ConditionalTransactionCondition{{Key: []byte("same"), Operator: CompareEqual}, {Key: append([]byte(nil), []byte("same")...), Operator: CompareNotEqual}}
	if _, err := db.ConditionalTransaction("n", "r", conditions, []ConditionalTransactionMutation{ConditionalPut([]byte("key"), nil)}); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("duplicate condition should fail before native access: %v", err)
	}
	mutations := []ConditionalTransactionMutation{ConditionalPut([]byte("same"), nil), ConditionalDelete(append([]byte(nil), []byte("same")...))}
	if _, err := db.ConditionalTransaction("n", "r", nil, mutations); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("duplicate mutation should fail before native access: %v", err)
	}
}
