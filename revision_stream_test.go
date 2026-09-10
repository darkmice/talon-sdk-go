package talon

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"testing"
)

func TestRevisionStreamV2CapabilityFailsClosed(t *testing.T) {
	reason := "authenticated proof bundle is not admitted"
	db := &DB{nativeInfo: NativeInfo{Features: []string{"revision_stream_v1", "revision_stream_v2_mmr_proof"}, Capabilities: []NativeCapability{{Name: "revision_stream", Version: 2, Status: "gated", Reason: &reason}}}}
	if _, err := db.RevisionStreamAppend(RevisionStreamAppendRequest{}); ErrorCodeOf(err) != CodeCapabilityUnavailable {
		t.Fatalf("gated append error = %v", err)
	}
	db.nativeInfo.Capabilities[0].Status, db.nativeInfo.Capabilities[0].Reason = "available", nil
	if err := db.RequireCapability("revision_stream"); err != nil {
		t.Fatal(err)
	}
	for _, features := range [][]string{{"revision_stream_v1"}, {"revision_stream_v2_mmr_proof"}, nil} {
		db.nativeInfo.Features = features
		if err := db.RequireCapability("revision_stream"); ErrorCodeOf(err) != CodeCapabilityUnavailable {
			t.Fatalf("incomplete v2 feature set passed: %v", features)
		}
	}
	build := coreBuildManifest{Features: []string{"revision_stream_v1", "revision_stream_v2_mmr_proof"}, Capabilities: []NativeCapability{{Name: "revision_stream", Version: 2, Status: "available"}}}
	if err := verifyRequiredRuntimeCapabilities(build, []string{"revision_stream"}); err != nil {
		t.Fatal(err)
	}
	build.Capabilities[0].Version = 1
	if err := verifyRequiredRuntimeCapabilities(build, []string{"revision_stream"}); err == nil {
		t.Fatal("v1 capability passed v2 admission")
	}
}

type revisionTestState struct {
	streamID []byte
	entries  []RevisionStreamEntry
	heads    []RevisionStreamHead
	nodes    map[string][sha256.Size]byte
}

func newRevisionTestState(t *testing.T, streamID []byte, times []int64, payloads [][]byte) revisionTestState {
	t.Helper()
	state := revisionTestState{streamID: append([]byte(nil), streamID...), nodes: map[string][sha256.Size]byte{}}
	var previous *RevisionStreamHead
	for index := range times {
		request, err := NewRevisionStreamAppendRequest("test-"+strconv.Itoa(index+1), "facts", streamID, previous, times[index], payloads[index], nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		built, err := buildRevisionStreamAppend(request)
		if err != nil {
			t.Fatal(err)
		}
		previousRoot := strings.Repeat("0", 64)
		if previous != nil {
			previousRoot = previous.RootSHA256
		}
		state.entries = append(state.entries, RevisionStreamEntry{Revision: uint64(index + 1), OccurredAt: times[index], Payload: append([]byte(nil), payloads[index]...), PayloadSHA256: built.payloadSHA, RootSHA256: built.head.RootSHA256, PreviousRootSHA256: previousRoot, ContentSHA256: built.head.Proof.LastContentSHA256, ProofRootSHA256: built.head.Proof.RootSHA256})
		state.heads = append(state.heads, *cloneRevisionStreamHead(&built.head))
		content, _ := decodeRevisionStreamDigest(built.head.Proof.LastContentSHA256)
		leaf := calculateRevisionStreamMerkleLeaf(streamID, uint64(index+1), content)
		var priorPeaks []RevisionStreamMerklePeak
		if previous != nil {
			priorPeaks = previous.Proof.Peaks
		}
		_, nodes, err := appendRevisionStreamMerkleLeaf(streamID, priorPeaks, uint64(index+1), leaf)
		if err != nil {
			t.Fatal(err)
		}
		for _, node := range nodes {
			state.nodes[revisionNodeID(node.height, node.startRevision)] = node.root
		}
		previous = &built.head
	}
	return state
}

func revisionNodeID(height uint8, start uint64) string {
	return strconv.Itoa(int(height)) + ":" + strconv.FormatUint(start, 10)
}

func (state revisionTestState) inclusion(t *testing.T, revision uint64) RevisionStreamInclusionProof {
	t.Helper()
	pinned := state.heads[len(state.heads)-1]
	var peakIndex int
	var peak RevisionStreamMerklePeak
	found := false
	for index, candidate := range pinned.Proof.Peaks {
		if revision >= candidate.StartRevision && revision-candidate.StartRevision < uint64(1)<<candidate.Height {
			peakIndex, peak, found = index, candidate, true
			break
		}
	}
	if !found {
		t.Fatal("test revision has no peak")
	}
	content, _ := decodeRevisionStreamDigest(state.entries[revision-1].ContentSHA256)
	leaf := calculateRevisionStreamMerkleLeaf(state.streamID, revision, content)
	local := revision - peak.StartRevision
	siblings := make([]RevisionStreamProofStep, 0, peak.Height)
	for height := uint8(0); height < peak.Height; height++ {
		span := uint64(1) << height
		blockStart := peak.StartRevision + ((local >> height) << height)
		isRight := ((local >> height) & 1) == 1
		siblingStart, side := blockStart+span, RevisionStreamProofRight
		if isRight {
			siblingStart, side = blockStart-span, RevisionStreamProofLeft
		}
		sibling, ok := state.nodes[revisionNodeID(height, siblingStart)]
		if !ok {
			t.Fatalf("missing test node %d/%d", height, siblingStart)
		}
		siblings = append(siblings, RevisionStreamProofStep{Height: height, Side: side, RootSHA256: hex.EncodeToString(sibling[:])})
	}
	return RevisionStreamInclusionProof{Version: revisionStreamProofVersion, Revision: revision, PeakIndex: uint8(peakIndex), LeafSHA256: hex.EncodeToString(leaf[:]), Siblings: siblings}
}

func (state revisionTestState) point(t *testing.T, revision uint64) *RevisionStreamProofPoint {
	t.Helper()
	entry := state.entries[revision-1]
	return &RevisionStreamProofPoint{Revision: entry.Revision, OccurredAt: entry.OccurredAt, PayloadSHA256: entry.PayloadSHA256, PreviousRootSHA256: entry.PreviousRootSHA256, ContentSHA256: entry.ContentSHA256, ProofRootSHA256: entry.ProofRootSHA256, RootSHA256: entry.RootSHA256, Inclusion: state.inclusion(t, revision)}
}

func wireInclusion(value RevisionStreamInclusionProof) wireRevisionStreamInclusionProof {
	steps := make([]wireRevisionStreamProofStep, len(value.Siblings))
	for index, step := range value.Siblings {
		steps[index] = wireRevisionStreamProofStep{Height: step.Height, Side: string(step.Side), RootSHA256: step.RootSHA256}
	}
	return wireRevisionStreamInclusionProof{Version: value.Version, Revision: canonicalUint64{Value: value.Revision, Present: true}, PeakIndex: value.PeakIndex, LeafSHA256: value.LeafSHA256, Siblings: steps}
}

func wirePoint(value *RevisionStreamProofPoint) *wireRevisionStreamProofPoint {
	if value == nil {
		return nil
	}
	return &wireRevisionStreamProofPoint{Revision: canonicalUint64{Value: value.Revision, Present: true}, OccurredAt: canonicalInt64{Value: value.OccurredAt, Present: true}, PayloadSHA256: value.PayloadSHA256, PreviousRootSHA256: value.PreviousRootSHA256, ContentSHA256: value.ContentSHA256, ProofRootSHA256: value.ProofRootSHA256, RootSHA256: value.RootSHA256, Inclusion: wireInclusion(value.Inclusion)}
}

func makeRevisionQueryWire(t *testing.T, state revisionTestState, pinned, live RevisionStreamHead, from, until int64, windowFirst, windowLast *uint64, entries []RevisionStreamEntry, hasMore bool) []byte {
	t.Helper()
	entryWires := make([]wireRevisionStreamEntry, len(entries))
	entryProofs := make([]wireRevisionStreamInclusionProof, len(entries))
	for index, entry := range entries {
		entryWires[index] = wireRevisionStreamEntry{Revision: canonicalUint64{Value: entry.Revision, Present: true}, OccurredAt: canonicalInt64{Value: entry.OccurredAt, Present: true}, Payload: presentWireBytes(entry.Payload), PayloadSHA256: entry.PayloadSHA256, RootSHA256: entry.RootSHA256, PreviousRootSHA256: entry.PreviousRootSHA256, ContentSHA256: entry.ContentSHA256, ProofRootSHA256: entry.ProofRootSHA256}
		entryProofs[index] = wireInclusion(state.inclusion(t, entry.Revision))
	}
	var predecessor, firstPoint, lastPoint, successor, pageSuccessor *RevisionStreamProofPoint
	if windowFirst != nil {
		firstPoint, lastPoint = state.point(t, *windowFirst), state.point(t, *windowLast)
		if *windowFirst > 1 {
			predecessor = state.point(t, *windowFirst-1)
		}
		if *windowLast < pinned.Revision {
			successor = state.point(t, *windowLast+1)
		}
	} else {
		for _, entry := range state.entries[:int(pinned.Revision)] {
			if entry.OccurredAt < from {
				predecessor = state.point(t, entry.Revision)
			}
			if successor == nil && entry.OccurredAt >= until {
				successor = state.point(t, entry.Revision)
			}
		}
	}
	if len(entries) > 0 && entries[len(entries)-1].Revision < pinned.Revision {
		pageSuccessor = state.point(t, entries[len(entries)-1].Revision+1)
	}
	proof := wireRevisionStreamQueryProof{Version: revisionStreamProofVersion, Scheme: "talon_mmr_sha256_v1", RootSHA256: pinned.Proof.RootSHA256, WindowPredecessor: wirePoint(predecessor), WindowFirst: wirePoint(firstPoint), WindowLast: wirePoint(lastPoint), WindowSuccessor: wirePoint(successor), PageSuccessor: wirePoint(pageSuccessor), EntryProofs: entryProofs}
	var pageFirst, pageLast, next any
	if len(entries) > 0 {
		pageFirst, pageLast = strconv.FormatUint(entries[0].Revision, 10), strconv.FormatUint(entries[len(entries)-1].Revision, 10)
	}
	if hasMore {
		next = pageLast
	}
	value := map[string]any{"pinned_head": toWireRevisionStreamHead(&pinned), "observed_live_head": toWireRevisionStreamHead(&live), "query_window_first_revision": canonicalOrNil(windowFirst), "query_window_last_revision": canonicalOrNil(windowLast), "page_first_revision": pageFirst, "page_last_revision": pageLast, "next_after_revision": next, "has_more": hasMore, "entries": entryWires, "proof": proof}
	data, err := marshalWithoutHTMLEscape(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func canonicalOrNil(value *uint64) any {
	if value == nil {
		return nil
	}
	return strconv.FormatUint(*value, 10)
}

func makeRevisionStreamAppendWire(t *testing.T, built builtRevisionStreamAppend, outcome ConditionalTransactionOutcome, duplicate bool, revision uint64) []byte {
	t.Helper()
	conditions := make([]wireConditionalObservation, len(built.conditions))
	for index, expected := range built.conditions {
		before := appendNullableBytes(expected.expected)
		matched := conditionalValueMatches(before, expected.expected, expected.operator)
		conditions[index] = wireConditionalObservation{Index: uint64Pointer(uint64(index)), Keyspace: expected.keyspace, Key: presentWireBytes(expected.key), Before: nullableWireBytes(before), After: nullableWireBytes(before), Matched: rawJSON(strconv.FormatBool(matched))}
	}
	mutations := make([]wireConditionalObservation, len(built.mutations))
	for index, expected := range built.mutations {
		before := appendNullableBytes(expected.expectedBefore)
		after := appendNullableBytes(before)
		if outcome == ConditionalOutcomeApplied && expected.kind == conditionalPut {
			after = append([]byte(nil), expected.value...)
		}
		mutations[index] = wireConditionalObservation{Index: uint64Pointer(uint64(index)), Keyspace: expected.keyspace, Key: presentWireBytes(expected.key), Before: nullableWireBytes(before), After: nullableWireBytes(after)}
	}
	receipt := wireConditionalReceipt{Version: conditionalTransactionVersion, RequestID: built.request.requestID, CommandSHA256: built.commandSHA, Revision: canonicalUint64{Value: revision, Present: true}, Outcome: string(outcome), Conditions: conditions, Mutations: mutations}
	if outcome == ConditionalOutcomeConflict {
		receipt.Conflict = rawJSON(`{"kind":"condition_failed","index":0}`)
		receipt.Conditions[0].Matched = rawJSON("false")
		receipt.Conditions[0].Before, receipt.Conditions[0].After = presentWireBytes([]byte("occupied")), presentWireBytes([]byte("occupied"))
	}
	receipt.ReceiptSHA256, _ = conditionalReceiptSHA(receipt)
	receiptJSON, _ := marshalWithoutHTMLEscape(receipt)
	applied := outcome == ConditionalOutcomeApplied
	result := wireRevisionStreamAppendResult{Result: string(outcome), OriginalResult: string(outcome), Applied: &applied, Duplicate: &duplicate, Revision: canonicalUint64{Value: revision, Present: true}, Receipt: receiptJSON}
	if duplicate {
		result.Result = string(ConditionalResultDuplicate)
	}
	if applied {
		result.StreamReceipt, _ = marshalWithoutHTMLEscape(wireRevisionStreamReceipt{Version: revisionStreamVersion, StreamID: presentWireBytes(built.request.streamID), Head: *toWireRevisionStreamHead(&built.head), PayloadSHA256: built.payloadSHA})
	}
	data, _ := marshalWithoutHTMLEscape(result)
	return data
}

func TestRevisionStreamAppendBuildsAuthenticatedMMRTransaction(t *testing.T) {
	first, err := NewRevisionStreamAppendRequest("vector-1", "facts", []byte{0, 1, 255}, nil, -1, []byte{2, 3}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	built, _ := buildRevisionStreamAppend(first)
	if built.wire.Version != 2 || built.head.Proof == nil || len(built.head.Proof.Peaks) != 1 || len(built.conditions) != 4 || len(built.mutations) != 4 {
		t.Fatalf("genesis v2 transaction shape = %#v", built)
	}
	if string(built.mutations[0].value[:7]) != "TLNRSE2" || string(built.mutations[2].value[:7]) != "TLNRSH2" || !bytes.HasPrefix(built.mutations[3].key, revisionStreamKey([]byte("MRS2"), []byte{0, 1, 255})) {
		t.Fatal("v2 internal encodings are not present")
	}
	if built.payloadSHA != "ee9040f65c341855e070ff438eb0ea9d5b831b2a2c270fb7ef592d750408e3b3" {
		t.Fatalf("payload vector changed: %s", built.payloadSHA)
	}
	if built.head.RootSHA256 != "297d73dfcc0a0d8cde53ca8e175a9f2716bc91e6b7f0c9151a78cf5b03fec528" || built.head.Proof.RootSHA256 != "b7a48699cda1f8a344b47a8248320659de324adf07b17f48cb6f51bcfb268454" || built.commandSHA != "639384b4b2eb67aa946a551831710a3ee5c8ae1cc0aef65276b0075880bbe5b6" {
		t.Fatalf("v2 fixed vector drift: root=%s proof=%s command=%s", built.head.RootSHA256, built.head.Proof.RootSHA256, built.commandSHA)
	}
	second, err := NewRevisionStreamAppendRequest("vector-2", "facts", []byte{0, 1, 255}, &built.head, 0, []byte("next"), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	built2, _ := buildRevisionStreamAppend(second)
	if len(built2.head.Proof.Peaks) != 1 || built2.head.Proof.Peaks[0].Height != 1 || len(built2.conditions) != 5 || len(built2.mutations) != 5 {
		t.Fatalf("second append did not merge leaf+parent: %#v", built2.head.Proof)
	}
}

func TestRevisionStreamAppendReceiptAndRecoveryAreExact(t *testing.T) {
	request, _ := NewRevisionStreamAppendRequest("stream-lookup", "facts", []byte("stream"), nil, 1, []byte("payload"), []ConditionalTransactionCondition{{Key: []byte("guard"), Operator: CompareEqual}}, nil)
	built, _ := buildRevisionStreamAppend(request)
	data := makeRevisionStreamAppendWire(t, built, ConditionalOutcomeApplied, false, 12)
	result, err := decodeRevisionStreamAppendResult(data, built)
	if err != nil || result.StreamReceipt == nil || !revisionStreamHeadsEqual(result.StreamReceipt.Head, built.head) {
		t.Fatalf("append result = %#v, %v", result, err)
	}
	var appendWire wireRevisionStreamAppendResult
	_ = decodeStrictJSON(data, &appendWire)
	lookupData, _ := marshalWithoutHTMLEscape(map[string]any{"result": "receipt", "request_id": request.RequestID(), "revision": "12", "receipt": json.RawMessage(appendWire.Receipt), "stream_receipt": json.RawMessage(appendWire.StreamReceipt)})
	lookup, err := decodeRevisionStreamAppendReceiptLookup(lookupData, built)
	if err != nil || lookup.StreamReceipt == nil || lookup.TransactionReceipt == nil {
		t.Fatalf("recovered lookup = %#v, %v", lookup, err)
	}
	genericRequest := mustBuildConditional(t, "facts", request.RequestID(), nil, []ConditionalTransactionMutation{ConditionalPut([]byte("unrelated"), []byte("value"))})
	if _, err := decodeConditionalReceiptLookup(lookupData, genericRequest); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("generic lookup accepted stream receipt: %v", err)
	}
	missing, _ := marshalWithoutHTMLEscape(map[string]any{"result": "receipt", "request_id": request.RequestID(), "revision": "12", "receipt": json.RawMessage(appendWire.Receipt)})
	if _, err := decodeRevisionStreamAppendReceiptLookup(missing, built); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("applied lookup without stream receipt = %v", err)
	}
	var receipt wireConditionalReceipt
	_ = decodeStrictJSON(appendWire.Receipt, &receipt)
	receipt.Mutations[len(receipt.Mutations)-1].After.Value[0] ^= 1
	receipt.ReceiptSHA256, _ = conditionalReceiptSHA(receipt)
	appendWire.Receipt, _ = marshalWithoutHTMLEscape(receipt)
	tampered, _ := marshalWithoutHTMLEscape(appendWire)
	if _, err := decodeRevisionStreamAppendResult(tampered, built); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("tampered MMR mutation passed: %v", err)
	}
}

func TestRevisionStreamQueryVerifiesPagesAndLiveRollback(t *testing.T) {
	state := newRevisionTestState(t, []byte("tenant/project"), []int64{10, 20, 30}, [][]byte{[]byte("one"), []byte("two"), []byte("three")})
	pinned := state.heads[2]
	request, _ := NewRevisionStreamQueryRequest(state.streamID, 10, 31, 2)
	first, last := uint64(1), uint64(3)
	page1, err := decodeRevisionStreamQueryPage(makeRevisionQueryWire(t, state, pinned, pinned, 10, 31, &first, &last, state.entries[:2], true), request)
	if err != nil || page1.Continuation == nil || len(page1.Entries) != 2 {
		t.Fatalf("first page = %#v, %v: %v", page1, err, errors.Unwrap(err))
	}
	next, _ := page1.Continuation.NextRequest(2)
	page2, err := decodeRevisionStreamQueryPage(makeRevisionQueryWire(t, state, pinned, pinned, 10, 31, &first, &last, state.entries[2:], false), next)
	if err != nil || page2.Continuation != nil || page2.Entries[0].Revision != 3 {
		t.Fatalf("second page = %#v, %v", page2, err)
	}
	liveState := newRevisionTestState(t, state.streamID, []int64{10, 20, 30, 40}, [][]byte{[]byte("one"), []byte("two"), []byte("three"), []byte("four")})
	request, _ = NewPinnedRevisionStreamQueryRequest(state.streamID, 10, 31, pinned, 2)
	page1, err = decodeRevisionStreamQueryPage(makeRevisionQueryWire(t, state, pinned, liveState.heads[3], 10, 31, &first, &last, state.entries[:2], true), request)
	if err != nil {
		t.Fatalf("%v: %v", err, errors.Unwrap(err))
	}
	next, _ = page1.Continuation.NextRequest(2)
	if _, err := decodeRevisionStreamQueryPage(makeRevisionQueryWire(t, state, pinned, pinned, 10, 31, &first, &last, state.entries[2:], false), next); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("live-head rollback passed: %v", err)
	}
}

func TestRevisionStreamQueryRejectsProofTamperingAndSubstitution(t *testing.T) {
	state := newRevisionTestState(t, []byte("stream"), []int64{10, 20, 30}, [][]byte{[]byte("one"), []byte("two"), []byte("three")})
	pinned := state.heads[2]
	request, _ := NewRevisionStreamQueryRequest(state.streamID, 10, 31, 2)
	first, last := uint64(1), uint64(3)
	base := makeRevisionQueryWire(t, state, pinned, pinned, 10, 31, &first, &last, state.entries[:2], true)
	mutate := func(path string, replacement any) []byte {
		var value map[string]any
		if err := json.Unmarshal(base, &value); err != nil {
			t.Fatal(err)
		}
		switch path {
		case "payload":
			value["entries"].([]any)[0].(map[string]any)["payload"] = []any{9.0}
		case "peak":
			value["pinned_head"].(map[string]any)["proof"].(map[string]any)["peaks"].([]any)[0].(map[string]any)["root_sha256"] = replacement
		case "sibling":
			value["proof"].(map[string]any)["entry_proofs"].([]any)[0].(map[string]any)["siblings"].([]any)[0].(map[string]any)["root_sha256"] = replacement
		case "scheme":
			value["proof"].(map[string]any)["scheme"] = replacement
		case "head":
			value["pinned_head"].(map[string]any)["root_sha256"] = replacement
		case "proof root":
			value["proof"].(map[string]any)["root_sha256"] = replacement
		case "entry proof root":
			value["entries"].([]any)[0].(map[string]any)["proof_root_sha256"] = replacement
		case "peak index":
			value["proof"].(map[string]any)["entry_proofs"].([]any)[0].(map[string]any)["peak_index"] = replacement
		case "window":
			value["query_window_last_revision"] = replacement
		case "cursor":
			value["next_after_revision"] = replacement
		case "downgrade":
			delete(value["pinned_head"].(map[string]any), "proof")
		}
		data, _ := marshalWithoutHTMLEscape(value)
		return data
	}
	for name, data := range map[string][]byte{"payload": mutate("payload", nil), "peak": mutate("peak", strings.Repeat("a", 64)), "sibling": mutate("sibling", strings.Repeat("b", 64)), "head": mutate("head", strings.Repeat("c", 64)), "proof root": mutate("proof root", strings.Repeat("d", 64)), "entry proof root": mutate("entry proof root", strings.Repeat("e", 64)), "peak index": mutate("peak index", float64(9)), "window": mutate("window", "2"), "scheme": mutate("scheme", "legacy"), "cursor": mutate("cursor", "1"), "downgrade": mutate("downgrade", nil), "unknown": bytes.Replace(base, []byte(`{"entries":`), []byte(`{"unknown":true,"entries":`), 1), "trailing": append(append([]byte(nil), base...), []byte("null")...)} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeRevisionStreamQueryPage(data, request); ErrorCodeOf(err) != CodeProtocolViolation {
				t.Fatalf("tamper passed: %v", err)
			}
		})
	}
	foreign := newRevisionTestState(t, []byte("foreign"), []int64{10, 20, 30}, [][]byte{[]byte("one"), []byte("two"), []byte("three")})
	if _, err := decodeRevisionStreamQueryPage(makeRevisionQueryWire(t, foreign, foreign.heads[2], foreign.heads[2], 10, 31, &first, &last, foreign.entries[:2], true), request); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("cross-stream substitution passed: %v", err)
	}
}

func TestRevisionStreamQueryAuthenticatesEmptyWindowBoundaries(t *testing.T) {
	state := newRevisionTestState(t, []byte("stream"), []int64{10, 20, 30}, [][]byte{[]byte("one"), []byte("two"), []byte("three")})
	pinned := state.heads[2]
	for name, bounds := range map[string][2]int64{"before": {0, 5}, "between": {11, 19}, "after": {31, 40}} {
		t.Run(name, func(t *testing.T) {
			request, _ := NewPinnedRevisionStreamQueryRequest(state.streamID, bounds[0], bounds[1], pinned, 10)
			page, err := decodeRevisionStreamQueryPage(makeRevisionQueryWire(t, state, pinned, pinned, bounds[0], bounds[1], nil, nil, nil, false), request)
			if err != nil || page.QueryWindow.First != nil || page.Proof == nil {
				t.Fatalf("empty authenticated window = %#v, %v", page, err)
			}
		})
	}
	windowFirst, windowLast, after := uint64(1), uint64(2), uint64(2)
	root, _ := decodeRevisionStreamDigest(state.entries[1].RootSHA256)
	continuation, err := newRevisionStreamQueryRequest(state.streamID, 10, 25, &pinned, &after, 10, &RevisionStreamRevisionRange{First: &windowFirst, Last: &windowLast}, &root, &pinned)
	if err != nil {
		t.Fatal(err)
	}
	page, err := decodeRevisionStreamQueryPage(makeRevisionQueryWire(t, state, pinned, pinned, 10, 25, &windowFirst, &windowLast, nil, false), continuation)
	if err != nil || len(page.Entries) != 0 || page.HasMore {
		t.Fatalf("terminal empty continuation = %#v, %v", page, err)
	}
}

func TestRevisionStreamProofAndRequestBounds(t *testing.T) {
	if _, err := NewRevisionStreamQueryRequest(nil, 0, 1, 1); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatal(err)
	}
	if _, err := NewRevisionStreamQueryRequest([]byte("s"), 1, 1, 1); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatal(err)
	}
	if _, err := NewRevisionStreamQueryRequest([]byte("s"), 0, 1, 1001); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatal(err)
	}
	proofless := RevisionStreamHead{Revision: 1, RootSHA256: strings.Repeat("0", 64)}
	if _, err := NewPinnedRevisionStreamQueryRequest([]byte("s"), 0, 1, proofless, 1); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("proofless v1 head accepted: %v", err)
	}
	bad := wireRevisionStreamInclusionProof{Version: 1, Revision: canonicalUint64{Value: 1, Present: true}, LeafSHA256: strings.Repeat("0", 64), Siblings: make([]wireRevisionStreamProofStep, maxRevisionStreamProofDepth+1)}
	if _, err := decodeRevisionStreamInclusion(bad); err == nil {
		t.Fatal("oversized inclusion path accepted")
	}
	tooManyPeaks := make([]RevisionStreamMerklePeak, maxRevisionStreamProofPeaks+1)
	if err := validateRevisionStreamPeaks([]byte("s"), 1, tooManyPeaks, strings.Repeat("0", 64)); err == nil {
		t.Fatal("oversized peak set accepted")
	}
	if _, err := decodeRevisionStreamQueryProof(wireRevisionStreamQueryProof{EntryProofs: make([]wireRevisionStreamInclusionProof, maxRevisionStreamQueryLimit+1)}); err == nil {
		t.Fatal("oversized entry proof set accepted")
	}
}

func TestRevisionStreamWireUsesCanonicalIntegerStrings(t *testing.T) {
	state := newRevisionTestState(t, []byte("s"), []int64{math.MinInt64}, [][]byte{nil})
	request, err := NewRevisionStreamAppendRequest("wire", "facts", []byte("s"), &state.heads[0], math.MinInt64, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	built, _ := buildRevisionStreamAppend(request)
	data, _ := json.Marshal(built.wire)
	for _, expected := range []string{`"revision":"2"`, `"occurred_at":"-9223372036854775808"`, `"leaf_count":"1"`, `"start_revision":"1"`} {
		if !bytes.Contains(data, []byte(expected)) {
			t.Fatalf("wire omitted %s: %s", expected, data)
		}
	}
	var raw [8]byte
	minimum := int64(math.MinInt64)
	binary.BigEndian.PutUint64(raw[:], uint64(minimum))
	if int64(binary.BigEndian.Uint64(raw[:])) != math.MinInt64 {
		t.Fatal("test i64 encoding drift")
	}
}
