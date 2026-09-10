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
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"testing"
)

func boolPointer(value bool) *bool    { return &value }
func int64Pointer(value int64) *int64 { return &value }

func compactConditionalLimitsForTest() *NativeCapabilityLimits {
	maxValue := uint64(compactConditionalMaxValueBytes)
	maxCommand := uint64(compactConditionalMaxCommandBytes)
	maxReceipt := uint64(compactConditionalMaxReceiptBytes)
	return &NativeCapabilityLimits{
		MaxValueBytes:            &maxValue,
		MaxAggregateCommandBytes: &maxCommand,
		MaxCompactReceiptBytes:   &maxReceipt,
	}
}

func mustBuildConditionalV3(t *testing.T, namespace, requestID string, conditions []ConditionalTransactionCondition, mutations []ConditionalTransactionMutation) builtConditionalV3Request {
	t.Helper()
	request, err := NewConditionalTransactionV3Request(namespace, requestID, conditions, mutations)
	if err != nil {
		t.Fatalf("build v3 request: %v", err)
	}
	built, err := buildClosedConditionalV3Request(request)
	if err != nil {
		t.Fatalf("close v3 request: %v", err)
	}
	return built
}

func mustEncodeConditionalV3Receipt(t *testing.T, receipt ConditionalTransactionV3Receipt) ([]byte, ConditionalTransactionV3Receipt) {
	t.Helper()
	digest, err := conditionalTransactionV3ReceiptSHA(receipt)
	if err != nil {
		t.Fatalf("hash v3 receipt: %v", err)
	}
	receipt.ReceiptSHA256 = digest
	encoded, err := conditionalTransactionV3ReceiptCanonicalJSON(receipt, digest)
	if err != nil {
		t.Fatalf("encode v3 receipt: %v", err)
	}
	return encoded, receipt
}

func mustEncodeConditionalV3Result(t *testing.T, receipt ConditionalTransactionV3Receipt, result ConditionalTransactionResultKind, original ConditionalTransactionOutcome, applied, duplicate bool) []byte {
	t.Helper()
	receiptJSON, _ := mustEncodeConditionalV3Receipt(t, receipt)
	encoded, err := marshalWithoutHTMLEscape(map[string]interface{}{
		"result":          string(result),
		"original_result": string(original),
		"applied":         applied,
		"duplicate":       duplicate,
		"revision":        strconv.FormatUint(receipt.Revision, 10),
		"receipt":         json.RawMessage(receiptJSON),
	})
	if err != nil {
		t.Fatalf("encode v3 result: %v", err)
	}
	return encoded
}

func TestConditionalTransactionV3CommandGoldenAndRequestIDBinding(t *testing.T) {
	conditions := []ConditionalTransactionCondition{{Key: []byte("guard"), Expected: []byte("ready"), Operator: CompareEqual}}
	mutations := []ConditionalTransactionMutation{ConditionalPut([]byte("projection"), []byte{0, 255, 1}), ConditionalIncrement([]byte("counter"), -7)}
	first := mustBuildConditionalV3(t, "terminal", "request-a", conditions, mutations)
	retry := mustBuildConditionalV3(t, "terminal", "request-b", conditions, mutations)
	if first.wire.Version != ConditionalTransactionV3Version {
		t.Fatalf("wire version = %d, want %d", first.wire.Version, ConditionalTransactionV3Version)
	}
	if first.commandSHA != retry.commandSHA {
		t.Fatal("request ID changed the semantic command digest")
	}
	if first.quorumCommandSHA == retry.quorumCommandSHA || first.quorumCommandSHA == first.commandSHA {
		t.Fatal("quorum command digest did not independently bind the request ID and consensus envelope")
	}
	if first.commandSHA == conditionalCommandMust(t, "terminal", conditions, mutations) {
		t.Fatal("v3 command digest collided with v2 framing")
	}
	const wantCommandSHA = "d8273ff35e04d1f1541ffb16f9033bf773bbe5f67a9bceed84ed7be3ade9c6b7"
	if first.commandSHA != wantCommandSHA {
		t.Fatalf("v3 command SHA-256 = %s, want SDK draft golden %s", first.commandSHA, wantCommandSHA)
	}

	changed := mustBuildConditionalV3(t, "terminal", "request-a", conditions, []ConditionalTransactionMutation{ConditionalPut([]byte("projection"), []byte{0, 255, 2}), ConditionalIncrement([]byte("counter"), -7)})
	if first.commandSHA == changed.commandSHA {
		t.Fatal("same request ID with a different command retained the digest")
	}
	if ErrorCodeOf(newNativeError("execute", "rebound", "conflict")) != CodeNativeConflict {
		t.Fatal("Core request-ID rebinding conflict lost its stable SDK classification")
	}
	if _, err := NewConditionalTransactionV3Request("terminal", conditionalRaftRequestPrefix+"node:1", nil, mutations); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("reserved Raft request ID error = %v", err)
	}
}

func conditionalCommandMust(t *testing.T, namespace string, conditions []ConditionalTransactionCondition, mutations []ConditionalTransactionMutation) string {
	t.Helper()
	digest, err := conditionalCommandSHA(namespace, conditions, mutations)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func TestConditionalTransactionV3ReceiptGolden(t *testing.T) {
	built := mustBuildConditionalV3(t, "terminal", "golden-1", []ConditionalTransactionCondition{{Key: []byte("guard"), Operator: CompareEqual}}, []ConditionalTransactionMutation{ConditionalPut([]byte("projection"), []byte("ok"))})
	receipt := ConditionalTransactionV3Receipt{
		Version: conditionalTransactionCompactVersion, RequestID: built.requestID, CommandSHA256: built.commandSHA, Revision: 7, Outcome: ConditionalOutcomeApplied,
		Conditions: []ConditionalTransactionV3Observation{{Index: 0, Keyspace: "__user_conditional__.terminal", Key: []byte("guard"), Before: conditionalValueObservation(nil), After: conditionalValueObservation(nil), Matched: boolPointer(true)}},
		Mutations:  []ConditionalTransactionV3Observation{{Index: 0, Keyspace: "__user_conditional__.terminal", Key: []byte("projection"), Before: conditionalValueObservation(nil), After: conditionalValueObservation([]byte("ok"))}},
	}
	digest, err := conditionalTransactionV3ReceiptSHA(receipt)
	if err != nil {
		t.Fatal(err)
	}
	const wantReceiptSHA = "4c7e8742a23d4696b1033069c128367948a1c2be24af59d73036823cc6a188c1"
	if digest != wantReceiptSHA {
		t.Fatalf("v3 receipt SHA-256 = %s, want SDK draft golden %s", digest, wantReceiptSHA)
	}
	encoded, receipt := mustEncodeConditionalV3Receipt(t, receipt)
	decoded, err := decodeConditionalTransactionV3Receipt(encoded, built.requestID)
	if err != nil || decoded.ReceiptSHA256 != receipt.ReceiptSHA256 {
		t.Fatalf("golden receipt failed round trip: %+v, %v", decoded, err)
	}
}

func TestConditionalValueObservationPreservesAbsentEmptyAndPresent(t *testing.T) {
	absent := conditionalValueObservation(nil)
	empty := conditionalValueObservation([]byte{})
	present := conditionalValueObservation([]byte{0xff})
	if absent.Present || absent.ByteLength != 0 || absent.SHA256 != nil {
		t.Fatalf("unexpected absent observation: %+v", absent)
	}
	if !empty.Present || empty.ByteLength != 0 || empty.SHA256 == nil || *empty.SHA256 != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("unexpected empty observation: %+v", empty)
	}
	if !present.Present || present.ByteLength != 1 || present.SHA256 == nil || equalConditionalValueObservation(absent, empty) || equalConditionalValueObservation(empty, present) {
		t.Fatalf("unexpected present observation: %+v", present)
	}
}

func TestConditionalTransactionV3RejectsUnverifiableRangeOperators(t *testing.T) {
	for _, operator := range []ConditionalCompareOperator{CompareLessThan, CompareLessOrEqual, CompareGreaterThan, CompareGreaterOrEqual} {
		if _, err := NewConditionalTransactionV3Request("terminal", "range", []ConditionalTransactionCondition{{Key: []byte("guard"), Expected: []byte("x"), Operator: operator}}, []ConditionalTransactionMutation{ConditionalPut([]byte("projection"), []byte("value"))}); ErrorCodeOf(err) != CodeInvalidArgument {
			t.Fatalf("operator %s did not fail closed: %v", operator, err)
		}
	}
	for _, operator := range []ConditionalCompareOperator{CompareEqual, CompareNotEqual} {
		if _, err := NewConditionalTransactionV3Request("terminal", "equality", []ConditionalTransactionCondition{{Key: []byte("guard"), Expected: []byte("x"), Operator: operator}}, []ConditionalTransactionMutation{ConditionalPut([]byte("projection"), []byte("value"))}); err != nil {
			t.Fatalf("operator %s was rejected: %v", operator, err)
		}
	}
}

func TestConditionalTransactionV3AcceptsEightMiBHighByteWithoutExpandingResponseBound(t *testing.T) {
	value := bytes.Repeat([]byte{0xff}, compactConditionalMaxValueBytes)
	built := mustBuildConditionalV3(t, "terminal", "large-projection", nil, []ConditionalTransactionMutation{ConditionalPut([]byte("projection"), value)})
	command, err := json.Marshal(map[string]interface{}{"module": "storage", "action": "conditional_batch", "params": built.wire})
	if err != nil {
		t.Fatal(err)
	}
	if len(command) <= maxNativeJSONRequestBytes || len(command) > compactConditionalMaxJSONRequestBytes {
		t.Fatalf("8 MiB high-byte command size = %d, want (%d,%d]", len(command), maxNativeJSONRequestBytes, compactConditionalMaxJSONRequestBytes)
	}
	if maxNativeJSONResultBytes != 16<<20 {
		t.Fatalf("compact support changed the response bound to %d", maxNativeJSONResultBytes)
	}
	value[0] = 0
	if built.mutations[0].value[0] != 0xff {
		t.Fatal("sealed v3 request retained caller-owned value storage")
	}
	tooLarge := bytes.Repeat([]byte{0xff}, compactConditionalMaxValueBytes+1)
	if _, err := NewConditionalTransactionV3Request("terminal", "too-large", nil, []ConditionalTransactionMutation{ConditionalPut([]byte("projection"), tooLarge)}); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("8 MiB+1 value error = %v", err)
	}
	oversizedConsensus := make([]ConditionalTransactionMutation, 4)
	for index := range oversizedConsensus {
		oversizedConsensus[index] = ConditionalTransactionMutation{kind: conditionalPut, key: []byte("projection-" + strconv.Itoa(index)), value: value}
	}
	if _, err := conditionalQuorumCommandSHA(conditionalTransactionCompactVersion, "aggregate-too-large", "terminal", nil, oversizedConsensus); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("consensus command above the attested 32 MiB bound error = %v", err)
	}
}

func TestConditionalTransactionV3AppliedDuplicateAndResponseLossLookup(t *testing.T) {
	large := bytes.Repeat([]byte{0xff}, compactConditionalMaxValueBytes)
	built := mustBuildConditionalV3(t, "terminal", "terminal-1", []ConditionalTransactionCondition{{Key: []byte("guard"), Operator: CompareEqual}}, []ConditionalTransactionMutation{ConditionalPut([]byte("projection"), large), ConditionalIncrement([]byte("counter"), 1)})
	matched := true
	receipt := ConditionalTransactionV3Receipt{
		Version: conditionalTransactionCompactVersion, RequestID: built.requestID, CommandSHA256: built.commandSHA, Revision: 19, Outcome: ConditionalOutcomeApplied,
		Conditions: []ConditionalTransactionV3Observation{{Index: 0, Keyspace: "__user_conditional__.terminal", Key: []byte("guard"), Before: conditionalValueObservation(nil), After: conditionalValueObservation(nil), Matched: &matched}},
		Mutations: []ConditionalTransactionV3Observation{
			{Index: 0, Keyspace: "__user_conditional__.terminal", Key: []byte("projection"), Before: conditionalValueObservation(nil), After: conditionalValueObservation(large)},
			{Index: 1, Keyspace: "__user_conditional__.terminal", Key: []byte("counter"), Before: conditionalValueObservation(nil), After: conditionalValueObservation([]byte{0, 0, 0, 0, 0, 0, 0, 1}), BeforeI64: int64Pointer(0), AfterI64: int64Pointer(1)},
		},
	}
	appliedData := mustEncodeConditionalV3Result(t, receipt, ConditionalResultApplied, ConditionalOutcomeApplied, true, false)
	if len(appliedData) >= 8<<10 {
		t.Fatalf("compact receipt unexpectedly retained the 8 MiB value: %d bytes", len(appliedData))
	}
	applied, err := decodeConditionalTransactionV3Result(appliedData, built)
	if err != nil {
		t.Fatalf("decode applied compact result: %v", err)
	}
	if !applied.Applied || applied.Duplicate || applied.Receipt.Mutations[0].After.ByteLength != uint64(len(large)) || applied.Receipt.Mutations[0].After.SHA256 == nil {
		t.Fatalf("unexpected applied result: %+v", applied)
	}
	wantLargeSHA := sha256.Sum256(large)
	if *applied.Receipt.Mutations[0].After.SHA256 != hex.EncodeToString(wantLargeSHA[:]) {
		t.Fatal("8 MiB projection digest mismatch")
	}

	duplicateData := mustEncodeConditionalV3Result(t, receipt, ConditionalResultDuplicate, ConditionalOutcomeApplied, true, true)
	duplicate, err := decodeConditionalTransactionV3Result(duplicateData, built)
	if err != nil || !duplicate.Duplicate || duplicate.Receipt.ReceiptSHA256 != applied.Receipt.ReceiptSHA256 {
		t.Fatalf("decode durable duplicate: %+v, %v", duplicate, err)
	}

	receiptJSON, durable := mustEncodeConditionalV3Receipt(t, receipt)
	lookupData, err := marshalWithoutHTMLEscape(map[string]interface{}{"result": "receipt", "request_id": built.requestID, "revision": "19", "receipt": json.RawMessage(receiptJSON)})
	if err != nil {
		t.Fatal(err)
	}
	lookup, err := decodeConditionalTransactionV3ReceiptLookup(lookupData, built)
	if err != nil || lookup.Status != ConditionalReceiptFound || lookup.Receipt == nil || lookup.Receipt.ReceiptSHA256 != durable.ReceiptSHA256 {
		t.Fatalf("response-loss lookup failed: %+v, %v", lookup, err)
	}

	different := mustBuildConditionalV3(t, "terminal", "terminal-1", built.conditions, []ConditionalTransactionMutation{ConditionalPut([]byte("projection"), append(append([]byte(nil), large[:len(large)-1]...), 0xfe)), ConditionalIncrement([]byte("counter"), 1)})
	if _, err := decodeConditionalTransactionV3ReceiptLookup(lookupData, different); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("same request ID with a different command accepted recovered receipt: %v", err)
	}
}

func TestConditionalTransactionV3ConflictAndFalseEvidenceAreRejected(t *testing.T) {
	built := mustBuildConditionalV3(t, "terminal", "conflict-1", []ConditionalTransactionCondition{{Key: []byte("guard"), Expected: []byte("ready"), Operator: CompareEqual}}, []ConditionalTransactionMutation{ConditionalPut([]byte("projection"), []byte("done"))})
	matched := false
	absent := conditionalValueObservation(nil)
	receipt := ConditionalTransactionV3Receipt{
		Version: conditionalTransactionCompactVersion, RequestID: built.requestID, CommandSHA256: built.commandSHA, Revision: 20, Outcome: ConditionalOutcomeConflict,
		Conflict:   &ConditionalTransactionConflict{Kind: ConditionalConflictConditionFailed, Index: 0},
		Conditions: []ConditionalTransactionV3Observation{{Index: 0, Keyspace: "__user_conditional__.terminal", Key: []byte("guard"), Before: absent, After: absent, Matched: &matched}},
		Mutations:  []ConditionalTransactionV3Observation{{Index: 0, Keyspace: "__user_conditional__.terminal", Key: []byte("projection"), Before: absent, After: absent}},
	}
	data := mustEncodeConditionalV3Result(t, receipt, ConditionalResultConflict, ConditionalOutcomeConflict, false, false)
	result, err := decodeConditionalTransactionV3Result(data, built)
	if err != nil || !result.Rejected || result.Duplicate || result.Receipt.Conflict == nil {
		t.Fatalf("decode compact conflict: %+v, %v", result, err)
	}

	receipt.Conditions[0].Matched = boolPointer(true)
	forged := mustEncodeConditionalV3Result(t, receipt, ConditionalResultConflict, ConditionalOutcomeConflict, false, false)
	if _, err := decodeConditionalTransactionV3Result(forged, built); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("rehashed false matched evidence was accepted: %v", err)
	}

	receipt.Conditions[0].Matched = boolPointer(false)
	receipt.CommandSHA256 = mustBuildConditionalV3(t, "terminal", "conflict-1", built.conditions, []ConditionalTransactionMutation{ConditionalPut([]byte("projection"), []byte("other"))}).commandSHA
	forged = mustEncodeConditionalV3Result(t, receipt, ConditionalResultConflict, ConditionalOutcomeConflict, false, false)
	if _, err := decodeConditionalTransactionV3Result(forged, built); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("rehashed substituted command was accepted: %v", err)
	}
}

func TestConditionalTransactionV3RejectsNonFirstCounterConflict(t *testing.T) {
	built := mustBuildConditionalV3(t, "terminal", "counter-conflict-order", nil, []ConditionalTransactionMutation{
		ConditionalIncrement([]byte("first"), 1),
		ConditionalIncrement([]byte("second"), 1),
	})
	invalid := conditionalValueObservation([]byte{0xff})
	receipt := ConditionalTransactionV3Receipt{
		Version: conditionalTransactionCompactVersion, RequestID: built.requestID, CommandSHA256: built.commandSHA, Revision: 21, Outcome: ConditionalOutcomeConflict,
		Conflict:   &ConditionalTransactionConflict{Kind: ConditionalConflictCounterInvalid, Index: 1},
		Conditions: []ConditionalTransactionV3Observation{},
		Mutations: []ConditionalTransactionV3Observation{
			{Index: 0, Keyspace: "__user_conditional__.terminal", Key: []byte("first"), Before: invalid, After: invalid},
			{Index: 1, Keyspace: "__user_conditional__.terminal", Key: []byte("second"), Before: invalid, After: invalid},
		},
	}
	forged := mustEncodeConditionalV3Result(t, receipt, ConditionalResultConflict, ConditionalOutcomeConflict, false, false)
	if _, err := decodeConditionalTransactionV3Result(forged, built); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("rehashed non-first counter conflict was accepted: %v", err)
	}

	receipt.Conflict.Index = 0
	valid := mustEncodeConditionalV3Result(t, receipt, ConditionalResultConflict, ConditionalOutcomeConflict, false, false)
	if _, err := decodeConditionalTransactionV3Result(valid, built); err != nil {
		t.Fatalf("first deterministic counter conflict was rejected: %v", err)
	}
}

func TestConditionalTransactionV3ConditionConflictRequiresIncrementEvidence(t *testing.T) {
	built := mustBuildConditionalV3(t, "terminal", "condition-before-counter", []ConditionalTransactionCondition{{Key: []byte("guard"), Expected: []byte("ready"), Operator: CompareEqual}}, []ConditionalTransactionMutation{ConditionalIncrement([]byte("counter"), 1)})
	matched := false
	absent := conditionalValueObservation(nil)
	receipt := ConditionalTransactionV3Receipt{
		Version: conditionalTransactionCompactVersion, RequestID: built.requestID, CommandSHA256: built.commandSHA, Revision: 22, Outcome: ConditionalOutcomeConflict,
		Conflict:   &ConditionalTransactionConflict{Kind: ConditionalConflictConditionFailed, Index: 0},
		Conditions: []ConditionalTransactionV3Observation{{Index: 0, Keyspace: "__user_conditional__.terminal", Key: []byte("guard"), Before: absent, After: absent, Matched: &matched}},
		Mutations:  []ConditionalTransactionV3Observation{{Index: 0, Keyspace: "__user_conditional__.terminal", Key: []byte("counter"), Before: absent, After: absent, BeforeI64: int64Pointer(0), AfterI64: int64Pointer(0)}},
	}
	valid := mustEncodeConditionalV3Result(t, receipt, ConditionalResultConflict, ConditionalOutcomeConflict, false, false)
	if _, err := decodeConditionalTransactionV3Result(valid, built); err != nil {
		t.Fatalf("condition conflict with complete unexecuted increment evidence was rejected: %v", err)
	}

	receipt.Mutations[0].BeforeI64 = nil
	receipt.Mutations[0].AfterI64 = nil
	missingEvidence := mustEncodeConditionalV3Result(t, receipt, ConditionalResultConflict, ConditionalOutcomeConflict, false, false)
	if _, err := decodeConditionalTransactionV3Result(missingEvidence, built); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("condition conflict omitted required increment evidence: %v", err)
	}
}

func TestConditionalTransactionV3RejectsMalformedCompactAndReceiptHashes(t *testing.T) {
	built := mustBuildConditionalV3(t, "terminal", "malformed-1", nil, []ConditionalTransactionMutation{ConditionalPut([]byte("projection"), []byte("ok"))})
	receipt := ConditionalTransactionV3Receipt{
		Version: conditionalTransactionCompactVersion, RequestID: built.requestID, CommandSHA256: built.commandSHA, Revision: 3, Outcome: ConditionalOutcomeApplied,
		Conditions: []ConditionalTransactionV3Observation{},
		Mutations:  []ConditionalTransactionV3Observation{{Index: 0, Keyspace: "__user_conditional__.terminal", Key: []byte("projection"), Before: conditionalValueObservation(nil), After: conditionalValueObservation([]byte("ok"))}},
	}
	encoded, _ := mustEncodeConditionalV3Receipt(t, receipt)
	var wire map[string]interface{}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	wire["receipt_sha256"] = "0000000000000000000000000000000000000000000000000000000000000000"
	tampered, _ := json.Marshal(wire)
	if _, err := decodeConditionalTransactionV3Receipt(tampered, built.requestID); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("receipt SHA-256 mismatch was accepted: %v", err)
	}

	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	mutation := wire["mutations"].([]interface{})[0].(map[string]interface{})
	before := mutation["before_compact"].(map[string]interface{})
	before["sha256"] = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	malformed, _ := json.Marshal(wire)
	if _, err := decodeConditionalTransactionV3Receipt(malformed, built.requestID); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("absent observation with SHA-256 was accepted: %v", err)
	}

	digest := sha256.Sum256([]byte("oversized-current-value"))
	digestHex := hex.EncodeToString(digest[:])
	receipt.Mutations[0].Before = ConditionalValueObservation{Present: true, ByteLength: compactConditionalMaxValueBytes + 1, SHA256: &digestHex}
	receipt.Mutations[0].After = conditionalValueObservation([]byte("ok"))
	oversized := mustEncodeConditionalV3Result(t, receipt, ConditionalResultApplied, ConditionalOutcomeApplied, true, false)
	if _, err := decodeConditionalTransactionV3Result(oversized, built); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("observation above the attested 8 MiB value bound was accepted: %v", err)
	}
}

func TestConditionalTransactionV3QuorumResultBindsCompactReceipt(t *testing.T) {
	built := mustBuildConditionalV3(t, "terminal", "quorum-1", nil, []ConditionalTransactionMutation{ConditionalPut([]byte("projection"), []byte("ok"))})
	receipt := ConditionalTransactionV3Receipt{
		Version: conditionalTransactionCompactVersion, RequestID: built.requestID, CommandSHA256: built.commandSHA, Revision: 11, Outcome: ConditionalOutcomeApplied,
		Conditions: []ConditionalTransactionV3Observation{},
		Mutations:  []ConditionalTransactionV3Observation{{Index: 0, Keyspace: "__user_conditional__.terminal", Key: []byte("projection"), Before: conditionalValueObservation(nil), After: conditionalValueObservation([]byte("ok"))}},
	}
	receiptJSON, receipt := mustEncodeConditionalV3Receipt(t, receipt)
	canonical, err := conditionalTransactionV3ReceiptCanonicalJSON(receipt, receipt.ReceiptSHA256)
	if err != nil {
		t.Fatal(err)
	}
	resultDigest := sha256.Sum256(canonical)
	quorum := map[string]interface{}{
		"version": 1, "request_id": built.requestID, "index": "11", "term": "4", "stage": "applied", "durability": "quorum_fsync", "command_sha256": built.quorumCommandSHA, "result_sha256": hex.EncodeToString(resultDigest[:]),
	}
	encode := func(quorum map[string]interface{}) []byte {
		data, marshalErr := marshalWithoutHTMLEscape(map[string]interface{}{"result": "applied", "original_result": "applied", "applied": true, "duplicate": false, "revision": "11", "receipt": json.RawMessage(receiptJSON), "quorum_receipt": quorum})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return data
	}
	if _, err := decodeConditionalTransactionV3Result(encode(quorum), built); err != nil {
		t.Fatalf("valid compact quorum receipt rejected: %v", err)
	}
	quorum["result_sha256"] = "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := decodeConditionalTransactionV3Result(encode(quorum), built); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("substituted quorum result digest was accepted: %v", err)
	}
}

func TestConditionalTransactionV3CounterEvidenceAndWireVersion(t *testing.T) {
	built := mustBuildConditionalV3(t, "terminal", "counter-1", nil, []ConditionalTransactionMutation{ConditionalIncrement([]byte("counter"), 1)})
	receipt := ConditionalTransactionV3Receipt{
		Version: conditionalTransactionCompactVersion, RequestID: built.requestID, CommandSHA256: built.commandSHA, Revision: math.MaxUint64, Outcome: ConditionalOutcomeApplied,
		Conditions: []ConditionalTransactionV3Observation{},
		Mutations:  []ConditionalTransactionV3Observation{{Index: 0, Keyspace: "__user_conditional__.terminal", Key: []byte("counter"), Before: conditionalValueObservation(nil), After: conditionalValueObservation([]byte{0, 0, 0, 0, 0, 0, 0, 1}), BeforeI64: int64Pointer(0), AfterI64: int64Pointer(1)}},
	}
	data := mustEncodeConditionalV3Result(t, receipt, ConditionalResultApplied, ConditionalOutcomeApplied, true, false)
	if _, err := decodeConditionalTransactionV3Result(data, built); err != nil {
		t.Fatalf("canonical max-u64 result rejected: %v", err)
	}

	receipt.Mutations[0].AfterI64 = int64Pointer(2)
	receipt.Mutations[0].After = conditionalValueObservation([]byte{0, 0, 0, 0, 0, 0, 0, 2})
	data = mustEncodeConditionalV3Result(t, receipt, ConditionalResultApplied, ConditionalOutcomeApplied, true, false)
	if _, err := decodeConditionalTransactionV3Result(data, built); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("rehashed false counter delta was accepted: %v", err)
	}

	receipt.Version = conditionalTransactionVersion
	data = mustEncodeConditionalV3Result(t, receipt, ConditionalResultApplied, ConditionalOutcomeApplied, true, false)
	if _, err := decodeConditionalTransactionV3Result(data, built); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("v2 receipt was accepted by v3 decoder: %v", err)
	}
	validReceipt := ConditionalTransactionV3Receipt{Version: conditionalTransactionCompactVersion, RequestID: built.requestID, CommandSHA256: built.commandSHA, Revision: 1, Outcome: ConditionalOutcomeApplied, Conditions: []ConditionalTransactionV3Observation{}, Mutations: []ConditionalTransactionV3Observation{{Index: 0, Keyspace: "__user_conditional__.terminal", Key: []byte("counter"), Before: conditionalValueObservation(nil), After: conditionalValueObservation([]byte{0, 0, 0, 0, 0, 0, 0, 1}), BeforeI64: int64Pointer(0), AfterI64: int64Pointer(1)}}}
	v3ReceiptJSON, _ := mustEncodeConditionalV3Receipt(t, validReceipt)
	if _, _, err := decodeConditionalReceipt(v3ReceiptJSON, built.requestID); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("v3 receipt was accepted by v2 decoder: %v", err)
	}
	v2ResultJSON, _ := makeAppliedConditionalWire(t, 1, false)
	var v2Result wireConditionalResult
	if err := decodeStrictJSON(v2ResultJSON, &v2Result); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeConditionalTransactionV3Receipt(v2Result.Receipt, "req-1"); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("v2 receipt was accepted by v3 decoder: %v", err)
	}
}

func TestConditionalTransactionV3CapabilityIsOptionalAndFailClosed(t *testing.T) {
	request, err := NewConditionalTransactionV3Request("terminal", "capability", nil, []ConditionalTransactionMutation{ConditionalPut([]byte("projection"), []byte("value"))})
	if err != nil {
		t.Fatal(err)
	}
	db := &DB{nativeInfo: NativeInfo{Features: []string{compactConditionalCapability, compactReceiptFeature, "conditional_transaction_command_digest_v1"}, Capabilities: []NativeCapability{{Name: compactConditionalCapability, Version: conditionalTransactionCompactVersion, Status: "available", Limits: compactConditionalLimitsForTest()}}}}
	if err := db.RequireCapability(compactConditionalCapability); err != nil {
		t.Fatalf("complete compact capability rejected: %v", err)
	}
	if _, err := db.ExecuteConditionalTransactionV3(request); ErrorCodeOf(err) != CodeDatabaseClosed {
		t.Fatalf("admitted compact request did not reach native boundary: %v", err)
	}
	for _, features := range [][]string{{compactConditionalCapability, compactReceiptFeature}, {compactConditionalCapability, "conditional_transaction_command_digest_v1"}, {compactReceiptFeature, "conditional_transaction_command_digest_v1"}, nil} {
		db.nativeInfo.Features = features
		if err := db.RequireCapability(compactConditionalCapability); ErrorCodeOf(err) != CodeCapabilityUnavailable {
			t.Fatalf("incomplete compact feature set %v error = %v", features, err)
		}
	}
	db.nativeInfo.Features = []string{compactConditionalCapability, compactReceiptFeature, "conditional_transaction_command_digest_v1"}
	db.nativeInfo.Capabilities[0].Version = 2
	if err := db.RequireCapability(compactConditionalCapability); ErrorCodeOf(err) != CodeCapabilityUnavailable {
		t.Fatalf("wrong compact capability version error = %v", err)
	}
}

func TestConditionalTransactionV3BuildManifestRequiresCoherentAttestation(t *testing.T) {
	verified, err := verifyNativeBundle(testNativePolicy(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = removeVerifiedNative(verified) })
	dirty := false
	reason := "draft contract not released"
	build := coreBuildManifest{
		ManifestVersion: 1,
		CoreSemver:      verified.Manifest.Source.CargoVersion,
		GitCommit:       verified.Manifest.Source.Commit,
		GitDirty:        &dirty,
		Target:          verified.Manifest.Build.Target,
		CargoLockSHA256: verified.Manifest.Source.CargoLockSHA256,
		HeaderSHA256:    verified.Manifest.ABI.HeaderSHA256,
		ABI:             coreBuildABI{Profile: verified.Manifest.ABI.Profile, Version: verified.Manifest.ABI.Version, RequiredSymbols: append([]string(nil), sdkRequiredSymbols...)},
		Features:        []string{"native_build_manifest_v1", "native_error_codes_v1", "sql_tlv_v1", "native_conditional_transaction_v2", "conditional_transaction_command_digest_v1", compactConditionalCapability, compactReceiptFeature},
		Capabilities: []NativeCapability{
			{Name: "native_conditional_transaction_v2", Version: 2, Status: "available"},
			{Name: compactConditionalCapability, Version: conditionalTransactionCompactVersion, Status: "gated", Reason: &reason, Limits: compactConditionalLimitsForTest()},
		},
	}
	encode := func(value coreBuildManifest) []byte {
		value.BuildBindingSHA256 = computeBuildBinding(value)
		data, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		return data
	}
	if _, err := verifyCoreBuildIdentity(encode(build), verified); err != nil {
		t.Fatalf("coherent gated compact attestation rejected: %v", err)
	}
	if err := verifyRequiredRuntimeCapabilities(build, []string{compactConditionalCapability}); err == nil {
		t.Fatal("gated compact capability satisfied a required runtime policy")
	}
	available := build
	available.Capabilities = append([]NativeCapability(nil), build.Capabilities...)
	available.Capabilities[1].Status = "available"
	available.Capabilities[1].Reason = nil
	if err := verifyRequiredRuntimeCapabilities(available, []string{compactConditionalCapability}); err != nil {
		t.Fatalf("available compact capability did not satisfy required runtime policy: %v", err)
	}
	missingFeature := build
	missingFeature.Features = append([]string(nil), build.Features[:len(build.Features)-1]...)
	if _, err := verifyCoreBuildIdentity(encode(missingFeature), verified); err == nil {
		t.Fatal("compact capability without compact_receipt_v1 feature was accepted")
	}
	wrongVersion := build
	wrongVersion.Capabilities = append([]NativeCapability(nil), build.Capabilities...)
	wrongVersion.Capabilities[1].Version = 2
	if _, err := verifyCoreBuildIdentity(encode(wrongVersion), verified); err == nil {
		t.Fatal("compact capability with version 2 was accepted")
	}
	missingLimits := build
	missingLimits.Capabilities = append([]NativeCapability(nil), build.Capabilities...)
	missingLimits.Capabilities[1].Limits = nil
	if _, err := verifyCoreBuildIdentity(encode(missingLimits), verified); err == nil {
		t.Fatal("compact capability without artifact-bound limits was accepted")
	}
	missingLimits.Capabilities[1].Status = "available"
	missingLimits.Capabilities[1].Reason = nil
	if err := verifyRequiredRuntimeCapabilities(missingLimits, []string{compactConditionalCapability}); err == nil {
		t.Fatal("required compact capability without limits was accepted")
	}
	wrongLimits := build
	wrongLimits.Capabilities = append([]NativeCapability(nil), build.Capabilities...)
	wrongLimits.Capabilities[1].Limits = compactConditionalLimitsForTest()
	*wrongLimits.Capabilities[1].Limits.MaxValueBytes++
	if _, err := verifyCoreBuildIdentity(encode(wrongLimits), verified); err == nil {
		t.Fatal("compact capability with mismatched limits was accepted")
	}
}

func TestCompactCapabilityLimitsBuildBindingGolden(t *testing.T) {
	const raw = `{"manifest_version":1,"core_semver":"0.4.0","git_commit":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","git_dirty":false,"target":"aarch64-apple-darwin","cargo_lock_sha256":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","header_sha256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","abi":{"profile":"talon-native-c","version":1,"required_symbols":["talon_open"]},"features":["native_build_manifest_v1","native_conditional_transaction_v2","native_conditional_transaction_v3","compact_receipt_v1","conditional_transaction_command_digest_v1"],"capabilities":[{"name":"native_conditional_transaction_v2","version":2,"status":"available"},{"name":"native_conditional_transaction_v3","version":3,"status":"available","limits":{"max_value_bytes":8388608,"max_aggregate_command_bytes":33554432,"max_compact_receipt_bytes":16777216}}],"build_binding_sha256":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}`
	var build coreBuildManifest
	if err := decodeStrictJSON([]byte(raw), &build); err != nil {
		t.Fatal(err)
	}
	const expected = "50bf30f81c871ccc82ca1bdbc8f71bf2e1fc51e0e032c813e9422601d05bac19"
	if actual := computeBuildBinding(build); actual != expected {
		t.Fatalf("limits build binding = %s, want SDK/Core-working-draft golden %s", actual, expected)
	}
}

func TestCompactCapabilityLimitsRejectExplicitNullForUnrelatedField(t *testing.T) {
	const raw = `{"max_value_bytes":8388608,"max_aggregate_command_bytes":33554432,"max_compact_receipt_bytes":16777216,"max_request_bytes":null}`
	var limits NativeCapabilityLimits
	if err := decodeStrictJSON([]byte(raw), &limits); err != nil {
		t.Fatal(err)
	}
	if validCompactConditionalLimits(&limits) {
		t.Fatal("explicit null unrelated limit was treated as an omitted field")
	}
}
