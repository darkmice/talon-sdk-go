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
	"math"
	"strconv"
)

const (
	revisionStreamVersion             = 2
	revisionStreamProofVersion        = 1
	revisionStreamInternalKeyspace    = "__revision_stream_v1__"
	maxRevisionStreamIDBytes          = 1_024
	maxRevisionStreamPayloadBytes     = 1 << 20
	maxRevisionStreamSidePayloadBytes = 4 << 20
	maxRevisionStreamSideItems        = 128
	maxRevisionStreamQueryLimit       = 1_000
	maxRevisionStreamProofPeaks       = 64
	maxRevisionStreamProofDepth       = 63
)

var revisionStreamZeroRoot [sha256.Size]byte

// RevisionStreamHead identifies an immutable stream prefix.
type RevisionStreamHead struct {
	Revision       uint64
	LastOccurredAt int64
	RootSHA256     string
	Proof          *RevisionStreamProofHead
}

// RevisionStreamMerklePeak is one canonical MMR peak in descending-height
// order. StartRevision is one-based and covers 2^Height leaves.
type RevisionStreamMerklePeak struct {
	Height        uint8
	StartRevision uint64
	RootSHA256    string
}

// RevisionStreamProofHead authenticates every revision up to its leaf count.
type RevisionStreamProofHead struct {
	Version           uint16
	LeafCount         uint64
	RootSHA256        string
	LastContentSHA256 string
	Peaks             []RevisionStreamMerklePeak
}

// RevisionStreamReceipt binds an applied append to its stream, new head, and
// payload digest.
type RevisionStreamReceipt struct {
	Version       uint16
	StreamID      []byte
	Head          RevisionStreamHead
	PayloadSHA256 string
}

// RevisionStreamAppendRequest is created only by NewRevisionStreamAppendRequest.
// Its revision is derived from ExpectedHead; the SDK never performs a read to
// manufacture a compare-and-swap precondition.
type RevisionStreamAppendRequest struct {
	requestID      string
	namespace      string
	streamID       []byte
	expectedHead   *RevisionStreamHead
	revision       uint64
	occurredAt     int64
	payload        []byte
	sideConditions []ConditionalTransactionCondition
	sideMutations  []ConditionalTransactionMutation
}

// NewRevisionStreamAppendRequest validates and copies an exact append request.
func NewRevisionStreamAppendRequest(requestID, namespace string, streamID []byte, expectedHead *RevisionStreamHead, occurredAt int64, payload []byte, sideConditions []ConditionalTransactionCondition, sideMutations []ConditionalTransactionMutation) (RevisionStreamAppendRequest, error) {
	request := RevisionStreamAppendRequest{
		requestID:      requestID,
		namespace:      namespace,
		streamID:       append([]byte(nil), streamID...),
		expectedHead:   cloneRevisionStreamHead(expectedHead),
		occurredAt:     occurredAt,
		payload:        append([]byte{}, payload...),
		sideConditions: cloneRevisionStreamConditions(sideConditions),
		sideMutations:  cloneRevisionStreamMutations(sideMutations),
	}
	if expectedHead == nil {
		request.revision = 1
	} else if expectedHead.Revision != math.MaxUint64 {
		request.revision = expectedHead.Revision + 1
	}
	if _, err := buildRevisionStreamAppend(request); err != nil {
		return RevisionStreamAppendRequest{}, err
	}
	return request, nil
}

func (request RevisionStreamAppendRequest) RequestID() string { return request.requestID }
func (request RevisionStreamAppendRequest) Namespace() string { return request.namespace }
func (request RevisionStreamAppendRequest) StreamID() []byte {
	return append([]byte(nil), request.streamID...)
}
func (request RevisionStreamAppendRequest) ExpectedHead() *RevisionStreamHead {
	return cloneRevisionStreamHead(request.expectedHead)
}
func (request RevisionStreamAppendRequest) Revision() uint64  { return request.revision }
func (request RevisionStreamAppendRequest) OccurredAt() int64 { return request.occurredAt }
func (request RevisionStreamAppendRequest) Payload() []byte {
	return append([]byte{}, request.payload...)
}

// RevisionStreamAppendResult is the verified append outcome and its durable
// transaction evidence.
type RevisionStreamAppendResult struct {
	Result             ConditionalTransactionResultKind
	OriginalResult     ConditionalTransactionOutcome
	Applied            bool
	Rejected           bool
	Duplicate          bool
	Revision           uint64
	TransactionReceipt ConditionalTransactionReceipt
	StreamReceipt      *RevisionStreamReceipt
	QuorumReceipt      *ConditionalQuorumReceipt
}

// RevisionStreamAppendReceiptLookup resolves both the durable transaction
// receipt and, for an applied append, the authenticated stream receipt that
// Core recovers from the original internal mutations.
type RevisionStreamAppendReceiptLookup struct {
	Status             ConditionalReceiptStatus
	RequestID          string
	Revision           *uint64
	TransactionReceipt *ConditionalTransactionReceipt
	StreamReceipt      *RevisionStreamReceipt
	QuorumReceipt      *ConditionalQuorumReceipt
}

// RevisionStreamAppend atomically appends one immutable event and applies its
// side conditions/mutations. A gated capability always fails before request
// validation or native execution.
func (db *DB) RevisionStreamAppend(request RevisionStreamAppendRequest) (RevisionStreamAppendResult, error) {
	if err := db.RequireCapability("revision_stream"); err != nil {
		return RevisionStreamAppendResult{}, err
	}
	built, err := buildRevisionStreamAppend(request)
	if err != nil {
		return RevisionStreamAppendResult{}, err
	}
	data, err := db.execute("storage", "revision_stream_append", built.wire)
	if err != nil {
		return RevisionStreamAppendResult{}, err
	}
	return decodeRevisionStreamAppendResult(data, built)
}

// RevisionStreamAppendReceipt resolves CodeResultIndeterminate without
// changing the original request ID or payload. A found receipt is checked
// against the complete append transaction, including internal stream writes.
func (db *DB) RevisionStreamAppendReceipt(request RevisionStreamAppendRequest) (RevisionStreamAppendReceiptLookup, error) {
	if err := db.RequireCapability("revision_stream"); err != nil {
		return RevisionStreamAppendReceiptLookup{}, err
	}
	built, err := buildRevisionStreamAppend(request)
	if err != nil {
		return RevisionStreamAppendReceiptLookup{}, err
	}
	data, err := db.execute("storage", "conditional_receipt", struct {
		RequestID string `json:"request_id"`
	}{RequestID: request.requestID})
	if err != nil {
		return RevisionStreamAppendReceiptLookup{}, err
	}
	return decodeRevisionStreamAppendReceiptLookup(data, built)
}

type wireRevisionStreamHead struct {
	Revision       canonicalUint64              `json:"revision"`
	LastOccurredAt canonicalInt64               `json:"last_occurred_at"`
	RootSHA256     string                       `json:"root_sha256"`
	Proof          *wireRevisionStreamProofHead `json:"proof,omitempty"`
}

type wireRevisionStreamMerklePeak struct {
	Height        uint8           `json:"height"`
	StartRevision canonicalUint64 `json:"start_revision"`
	RootSHA256    string          `json:"root_sha256"`
}

type wireRevisionStreamProofHead struct {
	Version           uint16                         `json:"version"`
	LeafCount         canonicalUint64                `json:"leaf_count"`
	RootSHA256        string                         `json:"root_sha256"`
	LastContentSHA256 string                         `json:"last_content_sha256"`
	Peaks             []wireRevisionStreamMerklePeak `json:"peaks"`
}

type wireRevisionStreamAppendRequest struct {
	Version        uint16                     `json:"version"`
	RequestID      string                     `json:"request_id"`
	Namespace      string                     `json:"namespace"`
	StreamID       wireBytes                  `json:"stream_id"`
	ExpectedHead   *wireRevisionStreamHead    `json:"expected_head"`
	Revision       canonicalUint64            `json:"revision"`
	OccurredAt     canonicalInt64             `json:"occurred_at"`
	Payload        wireBytes                  `json:"payload"`
	SideConditions []wireConditionalCondition `json:"side_conditions"`
	SideMutations  []wireConditionalMutation  `json:"side_mutations"`
}

type revisionStreamExpectedCondition struct {
	keyspace string
	key      []byte
	expected []byte
	operator ConditionalCompareOperator
}

type revisionStreamExpectedMutation struct {
	kind           conditionalMutationKind
	keyspace       string
	key            []byte
	value          []byte
	delta          int64
	strictBefore   bool
	expectedBefore []byte
}

type builtRevisionStreamAppend struct {
	wire       wireRevisionStreamAppendRequest
	request    RevisionStreamAppendRequest
	head       RevisionStreamHead
	payloadSHA string
	commandSHA string
	conditions []revisionStreamExpectedCondition
	mutations  []revisionStreamExpectedMutation
}

func buildRevisionStreamAppend(request RevisionStreamAppendRequest) (builtRevisionStreamAppend, error) {
	invalid := func(message string) (builtRevisionStreamAppend, error) {
		return builtRevisionStreamAppend{}, newError(CodeInvalidArgument, "revision stream append", message, nil)
	}
	if !conditionalRequestIDPattern.MatchString(request.requestID) {
		return invalid("request ID must be 1-128 safe ASCII bytes")
	}
	if !conditionalNamespacePattern.MatchString(request.namespace) {
		return invalid("namespace must be 1-64 safe ASCII bytes")
	}
	if len(request.streamID) == 0 || len(request.streamID) > maxRevisionStreamIDBytes {
		return invalid("stream ID must be 1-1024 bytes")
	}
	if len(request.payload) > maxRevisionStreamPayloadBytes {
		return invalid("payload exceeds 1 MiB")
	}
	if len(request.sideConditions) > maxRevisionStreamSideItems || len(request.sideMutations) > maxRevisionStreamSideItems {
		return invalid("side condition/mutation count exceeds 128")
	}
	if request.expectedHead == nil {
		if request.revision != 1 {
			return invalid("genesis revision must be 1")
		}
	} else {
		if err := validateRevisionStreamHead(request.streamID, *request.expectedHead); err != nil {
			return invalid("expected head is invalid: " + err.Error())
		}
		if request.expectedHead.Revision == math.MaxUint64 || request.revision != request.expectedHead.Revision+1 {
			return invalid("revision stream head overflow or revision mismatch")
		}
		if request.occurredAt < request.expectedHead.LastOccurredAt {
			return invalid("occurred_at must not precede expected head")
		}
	}

	userKeyspace := "__user_conditional__." + request.namespace
	wireConditions := make([]wireConditionalCondition, len(request.sideConditions))
	expectedConditions := make([]revisionStreamExpectedCondition, 0, len(request.sideConditions)+3)
	conditionKeys := make(map[string]struct{}, len(request.sideConditions))
	sideBytes := 0
	for index, condition := range request.sideConditions {
		if len(condition.Key) == 0 || len(condition.Key) > maxConditionalKeyBytes || !validCompareOperator(condition.Operator) {
			return invalid(fmt.Sprintf("side condition %d is invalid", index))
		}
		if _, exists := conditionKeys[string(condition.Key)]; exists {
			return invalid(fmt.Sprintf("side condition %d repeats a condition key", index))
		}
		conditionKeys[string(condition.Key)] = struct{}{}
		if !addRevisionStreamSideBytes(&sideBytes, len(userKeyspace), len(condition.Key), len(condition.Expected)) {
			return invalid("side payload exceeds 4 MiB")
		}
		wireConditions[index] = wireConditionalCondition{Key: presentWireBytes(condition.Key), Expected: nullableWireBytes(condition.Expected), Operator: string(condition.Operator)}
		expectedConditions = append(expectedConditions, revisionStreamExpectedCondition{keyspace: userKeyspace, key: append([]byte(nil), condition.Key...), expected: appendNullableBytes(condition.Expected), operator: condition.Operator})
	}

	wireMutations := make([]wireConditionalMutation, len(request.sideMutations))
	expectedMutations := make([]revisionStreamExpectedMutation, 0, len(request.sideMutations)+3)
	mutationKeys := make(map[string]struct{}, len(request.sideMutations))
	for index, mutation := range request.sideMutations {
		key := mutation.Key()
		value := mutation.Value()
		kind := mutation.MutationKind()
		deltaValue := mutation.Delta()
		if len(key) == 0 || len(key) > maxConditionalKeyBytes {
			return invalid(fmt.Sprintf("side mutation %d key is invalid", index))
		}
		if _, exists := mutationKeys[string(key)]; exists {
			return invalid(fmt.Sprintf("side mutation %d repeats a mutation key", index))
		}
		mutationKeys[string(key)] = struct{}{}
		wire := wireConditionalMutation{Key: presentWireBytes(key)}
		expected := revisionStreamExpectedMutation{kind: kind, keyspace: userKeyspace, key: key, delta: deltaValue}
		switch kind {
		case conditionalPut:
			if !addRevisionStreamSideBytes(&sideBytes, len(userKeyspace), len(key), len(value)) {
				return invalid("side payload exceeds 4 MiB")
			}
			wire.Operation = "put"
			wireValue := presentWireBytes(value)
			wire.Value = &wireValue
			expected.value = append([]byte{}, value...)
		case conditionalDelete:
			if !addRevisionStreamSideBytes(&sideBytes, len(userKeyspace), len(key)) {
				return invalid("side payload exceeds 4 MiB")
			}
			wire.Operation = "delete"
		case conditionalIncrement:
			if !addRevisionStreamSideBytes(&sideBytes, len(userKeyspace), len(key)) {
				return invalid("side payload exceeds 4 MiB")
			}
			wire.Operation = "increment"
			delta := strconv.FormatInt(deltaValue, 10)
			wire.Delta = &delta
		default:
			return invalid(fmt.Sprintf("side mutation %d was not created by a constructor", index))
		}
		wireMutations[index] = wire
		expectedMutations = append(expectedMutations, expected)
	}

	previousRoot := revisionStreamZeroRoot
	var previousPeaks []RevisionStreamMerklePeak
	var expectedHeadBytes []byte
	if request.expectedHead != nil {
		decoded, _ := decodeRevisionStreamDigest(request.expectedHead.RootSHA256)
		previousRoot = decoded
		previousPeaks = request.expectedHead.Proof.Peaks
		expectedHeadBytes = encodeRevisionStreamHead(*request.expectedHead)
	}
	payloadDigest := sha256.Sum256(request.payload)
	content := calculateRevisionStreamContent(request.streamID, previousRoot, request.revision, request.occurredAt, payloadDigest)
	leaf := calculateRevisionStreamMerkleLeaf(request.streamID, request.revision, content)
	peaks, merkleNodes, err := appendRevisionStreamMerkleLeaf(request.streamID, previousPeaks, request.revision, leaf)
	if err != nil {
		return invalid(err.Error())
	}
	proofRoot, err := calculateRevisionStreamMerkleRoot(request.streamID, request.revision, peaks)
	if err != nil {
		return invalid(err.Error())
	}
	root := calculateRevisionStreamHeadRoot(request.streamID, request.revision, request.occurredAt, content, proofRoot)
	head := RevisionStreamHead{Revision: request.revision, LastOccurredAt: request.occurredAt, RootSHA256: hex.EncodeToString(root[:]), Proof: &RevisionStreamProofHead{Version: revisionStreamProofVersion, LeafCount: request.revision, RootSHA256: hex.EncodeToString(proofRoot[:]), LastContentSHA256: hex.EncodeToString(content[:]), Peaks: peaks}}
	headKey := revisionStreamKey([]byte("HRS1"), request.streamID)
	eventKey := appendRevisionStreamUint64(revisionStreamKey([]byte("ERS1"), request.streamID), request.revision)
	timeKey := revisionStreamKey([]byte("TRS1"), request.streamID)
	timeKey = appendRevisionStreamUint64(timeKey, uint64(request.occurredAt)^(uint64(1)<<63))
	timeKey = appendRevisionStreamUint64(timeKey, request.revision)
	eventValue := encodeRevisionStreamEvent(request.occurredAt, previousRoot, payloadDigest, content, proofRoot, root, request.payload)
	headValue := encodeRevisionStreamHead(head)
	expectedConditions = append(expectedConditions,
		revisionStreamExpectedCondition{keyspace: revisionStreamInternalKeyspace, key: headKey, expected: expectedHeadBytes, operator: CompareEqual},
		revisionStreamExpectedCondition{keyspace: revisionStreamInternalKeyspace, key: eventKey, operator: CompareEqual},
		revisionStreamExpectedCondition{keyspace: revisionStreamInternalKeyspace, key: timeKey, operator: CompareEqual},
	)
	expectedMutations = append(expectedMutations,
		revisionStreamExpectedMutation{kind: conditionalPut, keyspace: revisionStreamInternalKeyspace, key: eventKey, value: eventValue, strictBefore: true},
		revisionStreamExpectedMutation{kind: conditionalPut, keyspace: revisionStreamInternalKeyspace, key: timeKey, value: append([]byte(nil), root[:]...), strictBefore: true},
		revisionStreamExpectedMutation{kind: conditionalPut, keyspace: revisionStreamInternalKeyspace, key: headKey, value: headValue, strictBefore: true, expectedBefore: appendNullableBytes(expectedHeadBytes)},
	)
	for _, node := range merkleNodes {
		key := revisionStreamMerkleKey(request.streamID, node.height, node.startRevision)
		expectedConditions = append(expectedConditions, revisionStreamExpectedCondition{keyspace: revisionStreamInternalKeyspace, key: key, operator: CompareEqual})
		expectedMutations = append(expectedMutations, revisionStreamExpectedMutation{kind: conditionalPut, keyspace: revisionStreamInternalKeyspace, key: key, value: append([]byte(nil), node.root[:]...), strictBefore: true})
	}
	commandSHA, err := revisionStreamCommandSHA(expectedConditions, expectedMutations)
	if err != nil {
		return invalid(err.Error())
	}
	wire := wireRevisionStreamAppendRequest{
		Version: revisionStreamVersion, RequestID: request.requestID, Namespace: request.namespace,
		StreamID: presentWireBytes(request.streamID), ExpectedHead: toWireRevisionStreamHead(request.expectedHead),
		Revision: canonicalUint64{Value: request.revision, Present: true}, OccurredAt: canonicalInt64{Value: request.occurredAt, Present: true},
		Payload: presentWireBytes(request.payload), SideConditions: wireConditions, SideMutations: wireMutations,
	}
	return builtRevisionStreamAppend{wire: wire, request: request, head: head, payloadSHA: hex.EncodeToString(payloadDigest[:]), commandSHA: commandSHA, conditions: expectedConditions, mutations: expectedMutations}, nil
}

func addRevisionStreamSideBytes(total *int, values ...int) bool {
	for _, value := range values {
		if value < 0 || *total > maxRevisionStreamSidePayloadBytes-value {
			return false
		}
		*total += value
	}
	return true
}

func appendNullableBytes(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte{}, value...)
}

func cloneRevisionStreamConditions(values []ConditionalTransactionCondition) []ConditionalTransactionCondition {
	result := make([]ConditionalTransactionCondition, len(values))
	for index, value := range values {
		result[index] = ConditionalTransactionCondition{Key: append([]byte(nil), value.Key...), Expected: appendNullableBytes(value.Expected), Operator: value.Operator}
	}
	return result
}

func cloneRevisionStreamMutations(values []ConditionalTransactionMutation) []ConditionalTransactionMutation {
	result := make([]ConditionalTransactionMutation, len(values))
	for index, value := range values {
		switch value.MutationKind() {
		case conditionalPut:
			result[index] = ConditionalPut(value.Key(), value.Value())
		case conditionalDelete:
			result[index] = ConditionalDelete(value.Key())
		case conditionalIncrement:
			result[index] = ConditionalIncrement(value.Key(), value.Delta())
		}
	}
	return result
}

func cloneRevisionStreamHead(value *RevisionStreamHead) *RevisionStreamHead {
	if value == nil {
		return nil
	}
	cloned := *value
	if value.Proof != nil {
		proof := *value.Proof
		proof.Peaks = append([]RevisionStreamMerklePeak(nil), value.Proof.Peaks...)
		cloned.Proof = &proof
	}
	return &cloned
}

func toWireRevisionStreamHead(value *RevisionStreamHead) *wireRevisionStreamHead {
	if value == nil {
		return nil
	}
	wire := &wireRevisionStreamHead{Revision: canonicalUint64{Value: value.Revision, Present: true}, LastOccurredAt: canonicalInt64{Value: value.LastOccurredAt, Present: true}, RootSHA256: value.RootSHA256}
	if value.Proof != nil {
		peaks := make([]wireRevisionStreamMerklePeak, len(value.Proof.Peaks))
		for index, peak := range value.Proof.Peaks {
			peaks[index] = wireRevisionStreamMerklePeak{Height: peak.Height, StartRevision: canonicalUint64{Value: peak.StartRevision, Present: true}, RootSHA256: peak.RootSHA256}
		}
		wire.Proof = &wireRevisionStreamProofHead{Version: value.Proof.Version, LeafCount: canonicalUint64{Value: value.Proof.LeafCount, Present: true}, RootSHA256: value.Proof.RootSHA256, LastContentSHA256: value.Proof.LastContentSHA256, Peaks: peaks}
	}
	return wire
}

func revisionStreamHeadsEqual(left, right RevisionStreamHead) bool {
	if left.Revision != right.Revision || left.LastOccurredAt != right.LastOccurredAt || left.RootSHA256 != right.RootSHA256 || (left.Proof == nil) != (right.Proof == nil) {
		return false
	}
	if left.Proof == nil {
		return true
	}
	if left.Proof.Version != right.Proof.Version || left.Proof.LeafCount != right.Proof.LeafCount || left.Proof.RootSHA256 != right.Proof.RootSHA256 || left.Proof.LastContentSHA256 != right.Proof.LastContentSHA256 || len(left.Proof.Peaks) != len(right.Proof.Peaks) {
		return false
	}
	for index := range left.Proof.Peaks {
		if left.Proof.Peaks[index] != right.Proof.Peaks[index] {
			return false
		}
	}
	return true
}

func revisionStreamKey(tag, streamID []byte) []byte {
	result := make([]byte, 0, len(tag)+4+len(streamID))
	result = append(result, tag...)
	result = binary.BigEndian.AppendUint32(result, uint32(len(streamID)))
	return append(result, streamID...)
}

func appendRevisionStreamUint64(target []byte, value uint64) []byte {
	return binary.BigEndian.AppendUint64(target, value)
}

func revisionStreamMerkleKey(streamID []byte, height uint8, startRevision uint64) []byte {
	result := revisionStreamKey([]byte("MRS2"), streamID)
	result = append(result, height)
	return binary.BigEndian.AppendUint64(result, startRevision)
}

func encodeRevisionStreamHead(head RevisionStreamHead) []byte {
	root, _ := decodeRevisionStreamDigest(head.RootSHA256)
	content, _ := decodeRevisionStreamDigest(head.Proof.LastContentSHA256)
	proofRoot, _ := decodeRevisionStreamDigest(head.Proof.RootSHA256)
	result := append([]byte(nil), []byte("TLNRSH2")...)
	result = binary.BigEndian.AppendUint64(result, head.Revision)
	result = binary.BigEndian.AppendUint64(result, uint64(head.LastOccurredAt))
	result = append(result, root[:]...)
	result = append(result, content[:]...)
	result = append(result, proofRoot[:]...)
	result = append(result, byte(len(head.Proof.Peaks)))
	for _, peak := range head.Proof.Peaks {
		peakRoot, _ := decodeRevisionStreamDigest(peak.RootSHA256)
		result = append(result, peak.Height)
		result = binary.BigEndian.AppendUint64(result, peak.StartRevision)
		result = append(result, peakRoot[:]...)
	}
	return result
}

func encodeRevisionStreamEvent(occurredAt int64, previousRoot, payloadSHA, content, proofRoot, root [sha256.Size]byte, payload []byte) []byte {
	result := append([]byte(nil), []byte("TLNRSE2")...)
	result = binary.BigEndian.AppendUint64(result, uint64(occurredAt))
	result = append(result, previousRoot[:]...)
	result = append(result, payloadSHA[:]...)
	result = append(result, content[:]...)
	result = append(result, proofRoot[:]...)
	result = append(result, root[:]...)
	result = binary.BigEndian.AppendUint32(result, uint32(len(payload)))
	return append(result, payload...)
}

func revisionStreamHash(domain string, streamID []byte, values ...[]byte) [sha256.Size]byte {
	hash := sha256.New()
	hash.Write([]byte(domain))
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(streamID)))
	hash.Write(size[:])
	hash.Write(streamID)
	for _, value := range values {
		hash.Write(value)
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func revisionStreamUint64(value uint64) []byte {
	var number [8]byte
	binary.BigEndian.PutUint64(number[:], value)
	return number[:]
}

func calculateRevisionStreamContent(streamID []byte, previousRoot [sha256.Size]byte, revision uint64, occurredAt int64, payloadSHA [sha256.Size]byte) [sha256.Size]byte {
	return revisionStreamHash("TALON_REVISION_STREAM_CONTENT_V2", streamID, previousRoot[:], revisionStreamUint64(revision), revisionStreamUint64(uint64(occurredAt)), payloadSHA[:])
}

func calculateRevisionStreamMerkleLeaf(streamID []byte, revision uint64, content [sha256.Size]byte) [sha256.Size]byte {
	return revisionStreamHash("TALON_REVISION_STREAM_MMR_LEAF_V1", streamID, revisionStreamUint64(revision), content[:])
}

func calculateRevisionStreamMerkleNode(streamID []byte, height uint8, startRevision uint64, left, right [sha256.Size]byte) [sha256.Size]byte {
	return revisionStreamHash("TALON_REVISION_STREAM_MMR_NODE_V1", streamID, []byte{height}, revisionStreamUint64(startRevision), left[:], right[:])
}

func calculateRevisionStreamMerkleRoot(streamID []byte, leafCount uint64, peaks []RevisionStreamMerklePeak) ([sha256.Size]byte, error) {
	if len(peaks) > maxRevisionStreamProofPeaks {
		return [sha256.Size]byte{}, fmt.Errorf("revision stream proof has too many peaks")
	}
	values := [][]byte{revisionStreamUint64(leafCount), []byte{byte(len(peaks))}}
	for _, peak := range peaks {
		root, err := decodeRevisionStreamDigest(peak.RootSHA256)
		if err != nil {
			return [sha256.Size]byte{}, err
		}
		values = append(values, []byte{peak.Height}, revisionStreamUint64(peak.StartRevision), root[:])
	}
	return revisionStreamHash("TALON_REVISION_STREAM_MMR_ROOT_V1", streamID, values...), nil
}

func calculateRevisionStreamHeadRoot(streamID []byte, revision uint64, occurredAt int64, content, proofRoot [sha256.Size]byte) [sha256.Size]byte {
	return revisionStreamHash("TALON_REVISION_STREAM_HEAD_V2", streamID, revisionStreamUint64(revision), revisionStreamUint64(uint64(occurredAt)), content[:], proofRoot[:])
}

type revisionStreamMerkleNode struct {
	height        uint8
	startRevision uint64
	root          [sha256.Size]byte
}

func expectedRevisionStreamPeakLayout(leafCount uint64) []struct {
	height uint8
	start  uint64
} {
	layout := make([]struct {
		height uint8
		start  uint64
	}, 0, 64)
	start := uint64(1)
	for height := 63; height >= 0; height-- {
		if leafCount&(uint64(1)<<uint(height)) != 0 {
			layout = append(layout, struct {
				height uint8
				start  uint64
			}{uint8(height), start})
			start += uint64(1) << uint(height)
		}
	}
	return layout
}

func validateRevisionStreamPeaks(streamID []byte, leafCount uint64, peaks []RevisionStreamMerklePeak, expectedRoot string) error {
	layout := expectedRevisionStreamPeakLayout(leafCount)
	if len(peaks) != len(layout) || len(peaks) > maxRevisionStreamProofPeaks {
		return fmt.Errorf("revision stream proof peak count does not match leaf count")
	}
	for index, peak := range peaks {
		if peak.Height != layout[index].height || peak.StartRevision != layout[index].start {
			return fmt.Errorf("revision stream proof peak layout is non-canonical")
		}
		if _, err := decodeRevisionStreamDigest(peak.RootSHA256); err != nil {
			return err
		}
	}
	calculated, err := calculateRevisionStreamMerkleRoot(streamID, leafCount, peaks)
	if err != nil {
		return err
	}
	expected, err := decodeRevisionStreamDigest(expectedRoot)
	if err != nil || calculated != expected {
		return fmt.Errorf("revision stream proof root does not match peaks")
	}
	return nil
}

func appendRevisionStreamMerkleLeaf(streamID []byte, previous []RevisionStreamMerklePeak, revision uint64, leaf [sha256.Size]byte) ([]RevisionStreamMerklePeak, []revisionStreamMerkleNode, error) {
	if revision == 0 {
		return nil, nil, fmt.Errorf("revision stream Merkle revision must be positive")
	}
	if revision == 1 {
		if len(previous) != 0 {
			return nil, nil, fmt.Errorf("revision stream genesis proof must not contain peaks")
		}
	} else {
		root, err := calculateRevisionStreamMerkleRoot(streamID, revision-1, previous)
		if err != nil {
			return nil, nil, err
		}
		if err := validateRevisionStreamPeaks(streamID, revision-1, previous, hex.EncodeToString(root[:])); err != nil {
			return nil, nil, err
		}
	}
	peaks := append([]RevisionStreamMerklePeak(nil), previous...)
	node := revisionStreamMerkleNode{height: 0, startRevision: revision, root: leaf}
	created := []revisionStreamMerkleNode{node}
	for len(peaks) > 0 && peaks[len(peaks)-1].Height == node.height {
		left := peaks[len(peaks)-1]
		peaks = peaks[:len(peaks)-1]
		span := uint64(1) << node.height
		if left.StartRevision+span != node.startRevision || node.height == 63 {
			return nil, nil, fmt.Errorf("revision stream proof peaks cannot be merged canonically")
		}
		leftRoot, err := decodeRevisionStreamDigest(left.RootSHA256)
		if err != nil {
			return nil, nil, err
		}
		node = revisionStreamMerkleNode{height: node.height + 1, startRevision: left.StartRevision, root: calculateRevisionStreamMerkleNode(streamID, node.height+1, left.StartRevision, leftRoot, node.root)}
		created = append(created, node)
	}
	peaks = append(peaks, RevisionStreamMerklePeak{Height: node.height, StartRevision: node.startRevision, RootSHA256: hex.EncodeToString(node.root[:])})
	root, err := calculateRevisionStreamMerkleRoot(streamID, revision, peaks)
	if err != nil {
		return nil, nil, err
	}
	if err := validateRevisionStreamPeaks(streamID, revision, peaks, hex.EncodeToString(root[:])); err != nil {
		return nil, nil, err
	}
	return peaks, created, nil
}

func revisionStreamCommandSHA(conditions []revisionStreamExpectedCondition, mutations []revisionStreamExpectedMutation) (string, error) {
	encoded := append([]byte(nil), []byte("TLNCTX2")...)
	encoded = binary.LittleEndian.AppendUint16(encoded, conditionalTransactionVersion)
	encoded = binary.LittleEndian.AppendUint32(encoded, uint32(len(conditions)))
	writeBytes := func(value []byte) error {
		if uint64(len(value)) > math.MaxUint32 {
			return fmt.Errorf("conditional transaction field is too large")
		}
		encoded = binary.LittleEndian.AppendUint32(encoded, uint32(len(value)))
		encoded = append(encoded, value...)
		return nil
	}
	operators := map[ConditionalCompareOperator]byte{CompareEqual: 0, CompareNotEqual: 1, CompareLessThan: 2, CompareLessOrEqual: 3, CompareGreaterThan: 4, CompareGreaterOrEqual: 5}
	for _, condition := range conditions {
		if err := writeBytes([]byte(condition.keyspace)); err != nil {
			return "", err
		}
		if err := writeBytes(condition.key); err != nil {
			return "", err
		}
		if condition.expected == nil {
			encoded = append(encoded, 0)
		} else {
			encoded = append(encoded, 1)
			if err := writeBytes(condition.expected); err != nil {
				return "", err
			}
		}
		encoded = append(encoded, operators[condition.operator])
	}
	encoded = binary.LittleEndian.AppendUint32(encoded, uint32(len(mutations)))
	for _, mutation := range mutations {
		switch mutation.kind {
		case conditionalPut:
			encoded = append(encoded, 0)
			if err := writeBytes([]byte(mutation.keyspace)); err != nil {
				return "", err
			}
			if err := writeBytes(mutation.key); err != nil {
				return "", err
			}
			if err := writeBytes(mutation.value); err != nil {
				return "", err
			}
		case conditionalDelete:
			encoded = append(encoded, 1)
			if err := writeBytes([]byte(mutation.keyspace)); err != nil {
				return "", err
			}
			if err := writeBytes(mutation.key); err != nil {
				return "", err
			}
		case conditionalIncrement:
			encoded = append(encoded, 2)
			if err := writeBytes([]byte(mutation.keyspace)); err != nil {
				return "", err
			}
			if err := writeBytes(mutation.key); err != nil {
				return "", err
			}
			encoded = binary.LittleEndian.AppendUint64(encoded, uint64(mutation.delta))
		default:
			return "", fmt.Errorf("unknown conditional mutation")
		}
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

type wireRevisionStreamAppendResult struct {
	Result         string          `json:"result"`
	OriginalResult string          `json:"original_result"`
	Applied        *bool           `json:"applied"`
	Duplicate      *bool           `json:"duplicate"`
	Revision       canonicalUint64 `json:"revision"`
	Receipt        json.RawMessage `json:"receipt"`
	StreamReceipt  json.RawMessage `json:"stream_receipt,omitempty"`
	QuorumReceipt  json.RawMessage `json:"quorum_receipt,omitempty"`
}

type wireRevisionStreamReceipt struct {
	Version       uint16                 `json:"version"`
	StreamID      wireBytes              `json:"stream_id"`
	Head          wireRevisionStreamHead `json:"head"`
	PayloadSHA256 string                 `json:"payload_sha256"`
}

func decodeRevisionStreamAppendResult(data []byte, built builtRevisionStreamAppend) (RevisionStreamAppendResult, error) {
	var wire wireRevisionStreamAppendResult
	if err := decodeStrictJSON(data, &wire); err != nil {
		return RevisionStreamAppendResult{}, revisionStreamProtocolError("decode revision stream append", err)
	}
	if wire.Applied == nil || wire.Duplicate == nil || !wire.Revision.Present || len(wire.Receipt) == 0 || bytes.Equal(wire.Receipt, []byte("null")) {
		return RevisionStreamAppendResult{}, revisionStreamProtocolError("decode revision stream append", fmt.Errorf("required result fields are missing"))
	}
	receiptWire, receipt, err := decodeConditionalReceiptWithLimit(wire.Receipt, built.request.requestID, maxNativeJSONResultBytes, true)
	if err != nil {
		return RevisionStreamAppendResult{}, err
	}
	if wire.Revision.Value != receipt.Revision || receipt.CommandSHA256 != built.commandSHA {
		return RevisionStreamAppendResult{}, revisionStreamProtocolError("decode revision stream append", fmt.Errorf("top-level revision or command hash differs from request/receipt"))
	}
	resultKind := ConditionalTransactionResultKind(wire.Result)
	original := ConditionalTransactionOutcome(wire.OriginalResult)
	if (resultKind != ConditionalResultApplied && resultKind != ConditionalResultDuplicate && resultKind != ConditionalResultConflict) || (original != ConditionalOutcomeApplied && original != ConditionalOutcomeConflict) {
		return RevisionStreamAppendResult{}, revisionStreamProtocolError("decode revision stream append", fmt.Errorf("unknown result enum"))
	}
	if original != receipt.Outcome || *wire.Applied != (original == ConditionalOutcomeApplied) || *wire.Duplicate != (resultKind == ConditionalResultDuplicate) || (!*wire.Duplicate && string(resultKind) != string(original)) {
		return RevisionStreamAppendResult{}, revisionStreamProtocolError("decode revision stream append", fmt.Errorf("result convenience fields are inconsistent"))
	}
	if err := validateRevisionStreamTransactionReceipt(receipt, built); err != nil {
		return RevisionStreamAppendResult{}, revisionStreamProtocolError("decode revision stream append", err)
	}
	var streamReceipt *RevisionStreamReceipt
	if original == ConditionalOutcomeApplied {
		if len(wire.StreamReceipt) == 0 || bytes.Equal(wire.StreamReceipt, []byte("null")) {
			return RevisionStreamAppendResult{}, revisionStreamProtocolError("decode revision stream append", fmt.Errorf("applied result omitted stream receipt"))
		}
		decoded, err := decodeRevisionStreamReceipt(wire.StreamReceipt, built)
		if err != nil {
			return RevisionStreamAppendResult{}, err
		}
		streamReceipt = &decoded
	} else if len(wire.StreamReceipt) != 0 {
		return RevisionStreamAppendResult{}, revisionStreamProtocolError("decode revision stream append", fmt.Errorf("rejected result included stream receipt"))
	}
	quorum, err := decodeOptionalQuorumReceipt(wire.QuorumReceipt, built.request.requestID, built.commandSHA, receiptWire, receipt.Revision)
	if err != nil {
		return RevisionStreamAppendResult{}, err
	}
	return RevisionStreamAppendResult{Result: resultKind, OriginalResult: original, Applied: *wire.Applied, Rejected: !*wire.Applied, Duplicate: *wire.Duplicate, Revision: wire.Revision.Value, TransactionReceipt: receipt, StreamReceipt: streamReceipt, QuorumReceipt: quorum}, nil
}

func decodeRevisionStreamAppendReceiptLookup(data []byte, built builtRevisionStreamAppend) (RevisionStreamAppendReceiptLookup, error) {
	var wire wireConditionalLookup
	if err := decodeStrictJSON(data, &wire); err != nil {
		return RevisionStreamAppendReceiptLookup{}, revisionStreamProtocolError("decode revision stream receipt lookup", err)
	}
	if wire.RequestID != built.request.requestID {
		return RevisionStreamAppendReceiptLookup{}, revisionStreamProtocolError("decode revision stream receipt lookup", fmt.Errorf("request ID mismatch"))
	}
	switch wire.Result {
	case string(ConditionalReceiptIndeterminate):
		if wire.Revision.Present || len(wire.Receipt) != 0 || len(wire.StreamReceipt) != 0 {
			return RevisionStreamAppendReceiptLookup{}, revisionStreamProtocolError("decode revision stream receipt lookup", fmt.Errorf("indeterminate lookup included a transaction receipt"))
		}
		quorum, err := decodeOptionalQuorumReceipt(wire.QuorumReceipt, built.request.requestID, built.commandSHA, nil, 0)
		if err != nil || quorum == nil {
			if err == nil {
				err = revisionStreamProtocolError("decode revision stream receipt lookup", fmt.Errorf("indeterminate lookup omitted quorum receipt"))
			}
			return RevisionStreamAppendReceiptLookup{}, err
		}
		if quorum.ResultSHA256 != nil || quorum.Stage == QuorumReceiptApplied {
			return RevisionStreamAppendReceiptLookup{}, revisionStreamProtocolError("decode revision stream receipt lookup", fmt.Errorf("indeterminate lookup contains an applied result"))
		}
		return RevisionStreamAppendReceiptLookup{Status: ConditionalReceiptIndeterminate, RequestID: built.request.requestID, QuorumReceipt: quorum}, nil
	case string(ConditionalReceiptFound):
		if !wire.Revision.Present || len(wire.Receipt) == 0 || bytes.Equal(wire.Receipt, []byte("null")) {
			return RevisionStreamAppendReceiptLookup{}, revisionStreamProtocolError("decode revision stream receipt lookup", fmt.Errorf("found lookup omitted receipt or revision"))
		}
		receiptWire, receipt, err := decodeConditionalReceiptWithLimit(wire.Receipt, built.request.requestID, maxNativeJSONResultBytes, true)
		if err != nil {
			return RevisionStreamAppendReceiptLookup{}, err
		}
		if wire.Revision.Value != receipt.Revision || receipt.CommandSHA256 != built.commandSHA {
			return RevisionStreamAppendReceiptLookup{}, revisionStreamProtocolError("decode revision stream receipt lookup", fmt.Errorf("receipt identity differs from exact append request"))
		}
		if err := validateRevisionStreamTransactionReceipt(receipt, built); err != nil {
			return RevisionStreamAppendReceiptLookup{}, revisionStreamProtocolError("decode revision stream receipt lookup", err)
		}
		var streamReceipt *RevisionStreamReceipt
		if receipt.Outcome == ConditionalOutcomeApplied {
			if len(wire.StreamReceipt) == 0 || bytes.Equal(wire.StreamReceipt, []byte("null")) {
				return RevisionStreamAppendReceiptLookup{}, revisionStreamProtocolError("decode revision stream receipt lookup", fmt.Errorf("applied lookup omitted stream receipt"))
			}
			decoded, err := decodeRevisionStreamReceipt(wire.StreamReceipt, built)
			if err != nil {
				return RevisionStreamAppendReceiptLookup{}, err
			}
			streamReceipt = &decoded
		} else if len(wire.StreamReceipt) != 0 {
			return RevisionStreamAppendReceiptLookup{}, revisionStreamProtocolError("decode revision stream receipt lookup", fmt.Errorf("conflict lookup included stream receipt"))
		}
		quorum, err := decodeOptionalQuorumReceipt(wire.QuorumReceipt, built.request.requestID, built.commandSHA, receiptWire, receipt.Revision)
		if err != nil {
			return RevisionStreamAppendReceiptLookup{}, err
		}
		revision := receipt.Revision
		return RevisionStreamAppendReceiptLookup{Status: ConditionalReceiptFound, RequestID: built.request.requestID, Revision: &revision, TransactionReceipt: &receipt, StreamReceipt: streamReceipt, QuorumReceipt: quorum}, nil
	default:
		return RevisionStreamAppendReceiptLookup{}, revisionStreamProtocolError("decode revision stream receipt lookup", fmt.Errorf("unknown lookup result %q", wire.Result))
	}
}

func decodeRevisionStreamReceipt(data []byte, built builtRevisionStreamAppend) (RevisionStreamReceipt, error) {
	var wire wireRevisionStreamReceipt
	if err := decodeStrictJSON(data, &wire); err != nil {
		return RevisionStreamReceipt{}, revisionStreamProtocolError("decode revision stream receipt", err)
	}
	head, err := decodeWireRevisionStreamHead(built.request.streamID, wire.Head)
	if err != nil {
		return RevisionStreamReceipt{}, err
	}
	if wire.Version != revisionStreamVersion || !wire.StreamID.Present || wire.StreamID.Null || !bytes.Equal(wire.StreamID.Value, built.request.streamID) || !revisionStreamHeadsEqual(head, built.head) || wire.PayloadSHA256 != built.payloadSHA {
		return RevisionStreamReceipt{}, revisionStreamProtocolError("decode revision stream receipt", fmt.Errorf("stream receipt differs from the exact append request"))
	}
	return RevisionStreamReceipt{Version: wire.Version, StreamID: append([]byte(nil), wire.StreamID.Value...), Head: head, PayloadSHA256: wire.PayloadSHA256}, nil
}

func validateRevisionStreamTransactionReceipt(receipt ConditionalTransactionReceipt, built builtRevisionStreamAppend) error {
	if len(receipt.Conditions) != len(built.conditions) || len(receipt.Mutations) != len(built.mutations) {
		return fmt.Errorf("transaction receipt item counts differ from append request")
	}
	for index, observation := range receipt.Conditions {
		expected := built.conditions[index]
		if observation.Index != uint64(index) || observation.Keyspace != expected.keyspace || !bytes.Equal(observation.Key, expected.key) || observation.Matched == nil || observation.BeforeI64 != nil || observation.AfterI64 != nil || !nullableBytesEqual(observation.Before, observation.After) {
			return fmt.Errorf("condition observation %d differs from append request", index)
		}
		if *observation.Matched != conditionalValueMatches(observation.Before, expected.expected, expected.operator) {
			return fmt.Errorf("condition observation %d contains false comparison evidence", index)
		}
	}
	for index, observation := range receipt.Mutations {
		expected := built.mutations[index]
		if observation.Index != uint64(index) || observation.Keyspace != expected.keyspace || !bytes.Equal(observation.Key, expected.key) || observation.Matched != nil {
			return fmt.Errorf("mutation observation %d differs from append request", index)
		}
		if expected.kind != conditionalIncrement && (observation.BeforeI64 != nil || observation.AfterI64 != nil) {
			return fmt.Errorf("non-increment mutation %d contains i64 evidence", index)
		}
		if receipt.Outcome == ConditionalOutcomeConflict {
			if !nullableBytesEqual(observation.Before, observation.After) {
				return fmt.Errorf("rejected mutation %d changed state", index)
			}
			continue
		}
		if expected.strictBefore && !nullableBytesEqual(observation.Before, expected.expectedBefore) {
			return fmt.Errorf("internal mutation %d has an impossible before value", index)
		}
		switch expected.kind {
		case conditionalPut:
			if !nullableBytesEqual(observation.After, expected.value) {
				return fmt.Errorf("put mutation %d has an incorrect after value", index)
			}
		case conditionalDelete:
			if observation.After != nil {
				return fmt.Errorf("delete mutation %d has a present after value", index)
			}
		case conditionalIncrement:
			if observation.BeforeI64 == nil || observation.AfterI64 == nil || !bytesMatchI64(observation.Before, *observation.BeforeI64) || !bytesMatchI64(observation.After, *observation.AfterI64) {
				return fmt.Errorf("increment mutation %d has invalid numeric evidence", index)
			}
			if (expected.delta > 0 && *observation.BeforeI64 > math.MaxInt64-expected.delta) || (expected.delta < 0 && *observation.BeforeI64 < math.MinInt64-expected.delta) || *observation.AfterI64 != *observation.BeforeI64+expected.delta {
				return fmt.Errorf("increment mutation %d does not match requested delta", index)
			}
		default:
			return fmt.Errorf("mutation %d has an unknown kind", index)
		}
	}
	if receipt.Outcome == ConditionalOutcomeConflict && receipt.Conflict != nil && receipt.Conflict.Kind != ConditionalConflictConditionFailed {
		index := receipt.Conflict.Index
		if index >= uint64(len(built.request.sideMutations)) || built.mutations[index].kind != conditionalIncrement {
			return fmt.Errorf("counter conflict does not identify a side increment")
		}
		observation := receipt.Mutations[index]
		if receipt.Conflict.Kind == ConditionalConflictCounterInvalid {
			if observation.Before == nil || len(observation.Before) == 8 || observation.BeforeI64 != nil || observation.AfterI64 != nil {
				return fmt.Errorf("counter-invalid conflict has false evidence")
			}
		} else {
			if observation.BeforeI64 == nil || observation.AfterI64 == nil || *observation.BeforeI64 != *observation.AfterI64 {
				return fmt.Errorf("counter range conflict has invalid evidence")
			}
			_, overflow := addInt64(*observation.BeforeI64, built.mutations[index].delta)
			if !overflow || (receipt.Conflict.Kind == ConditionalConflictCounterOverflow) != (built.mutations[index].delta > 0) || (receipt.Conflict.Kind == ConditionalConflictCounterUnderflow) != (built.mutations[index].delta < 0) {
				return fmt.Errorf("counter range conflict does not match the requested delta")
			}
		}
	}
	return nil
}

// RevisionStreamRevisionRange is nil/nil for an empty time window.
type RevisionStreamRevisionRange struct {
	First *uint64
	Last  *uint64
}

// RevisionStreamEntry is one immutable event with verified payload metadata.
type RevisionStreamEntry struct {
	Revision           uint64
	OccurredAt         int64
	Payload            []byte
	PayloadSHA256      string
	RootSHA256         string
	PreviousRootSHA256 string
	ContentSHA256      string
	ProofRootSHA256    string
}

type RevisionStreamProofSide string

const (
	RevisionStreamProofLeft  RevisionStreamProofSide = "left"
	RevisionStreamProofRight RevisionStreamProofSide = "right"
)

type RevisionStreamProofStep struct {
	Height     uint8
	Side       RevisionStreamProofSide
	RootSHA256 string
}

type RevisionStreamInclusionProof struct {
	Version    uint16
	Revision   uint64
	PeakIndex  uint8
	LeafSHA256 string
	Siblings   []RevisionStreamProofStep
}

type RevisionStreamProofPoint struct {
	Revision           uint64
	OccurredAt         int64
	PayloadSHA256      string
	PreviousRootSHA256 string
	ContentSHA256      string
	ProofRootSHA256    string
	RootSHA256         string
	Inclusion          RevisionStreamInclusionProof
}

type RevisionStreamQueryProof struct {
	Version           uint16
	Scheme            string
	RootSHA256        string
	WindowPredecessor *RevisionStreamProofPoint
	WindowFirst       *RevisionStreamProofPoint
	WindowLast        *RevisionStreamProofPoint
	WindowSuccessor   *RevisionStreamProofPoint
	PageSuccessor     *RevisionStreamProofPoint
	EntryProofs       []RevisionStreamInclusionProof
}

// RevisionStreamContinuation is emitted only by a verified page. Its private
// fields bind the next request to the original stream, time window, pinned head,
// cursor, and prior root.
type RevisionStreamContinuation struct {
	streamID        []byte
	fromOccurredAt  int64
	untilOccurredAt int64
	pinnedHead      RevisionStreamHead
	afterRevision   uint64
	window          RevisionStreamRevisionRange
	previousRoot    [sha256.Size]byte
	minimumLiveHead RevisionStreamHead
}

// RevisionStreamQueryRequest is a closed initial, pinned, or continuation
// query created by the SDK constructors.
type RevisionStreamQueryRequest struct {
	streamID        []byte
	fromOccurredAt  int64
	untilOccurredAt int64
	pinnedHead      *RevisionStreamHead
	afterRevision   *uint64
	limit           uint64
	expectedWindow  *RevisionStreamRevisionRange
	previousRoot    *[sha256.Size]byte
	minimumLiveHead *RevisionStreamHead
}

// NewRevisionStreamQueryRequest creates a first-page query that asks Core to
// pin the current live head.
func NewRevisionStreamQueryRequest(streamID []byte, fromOccurredAt, untilOccurredAt int64, limit uint64) (RevisionStreamQueryRequest, error) {
	return newRevisionStreamQueryRequest(streamID, fromOccurredAt, untilOccurredAt, nil, nil, limit, nil, nil, nil)
}

// NewPinnedRevisionStreamQueryRequest creates a first-page query at a caller-
// supplied immutable head.
func NewPinnedRevisionStreamQueryRequest(streamID []byte, fromOccurredAt, untilOccurredAt int64, pinnedHead RevisionStreamHead, limit uint64) (RevisionStreamQueryRequest, error) {
	return newRevisionStreamQueryRequest(streamID, fromOccurredAt, untilOccurredAt, &pinnedHead, nil, limit, nil, nil, nil)
}

// NextRequest creates the next page without allowing the pinned snapshot or
// query window to drift.
func (continuation RevisionStreamContinuation) NextRequest(limit uint64) (RevisionStreamQueryRequest, error) {
	window := cloneRevisionStreamRange(continuation.window)
	root := continuation.previousRoot
	return newRevisionStreamQueryRequest(continuation.streamID, continuation.fromOccurredAt, continuation.untilOccurredAt, &continuation.pinnedHead, &continuation.afterRevision, limit, &window, &root, &continuation.minimumLiveHead)
}

func (request RevisionStreamQueryRequest) StreamID() []byte {
	return append([]byte(nil), request.streamID...)
}
func (request RevisionStreamQueryRequest) FromOccurredAt() int64  { return request.fromOccurredAt }
func (request RevisionStreamQueryRequest) UntilOccurredAt() int64 { return request.untilOccurredAt }
func (request RevisionStreamQueryRequest) PinnedHead() *RevisionStreamHead {
	return cloneRevisionStreamHead(request.pinnedHead)
}
func (request RevisionStreamQueryRequest) AfterRevision() *uint64 {
	return cloneUint64Pointer(request.afterRevision)
}
func (request RevisionStreamQueryRequest) Limit() uint64 { return request.limit }

func newRevisionStreamQueryRequest(streamID []byte, fromOccurredAt, untilOccurredAt int64, pinnedHead *RevisionStreamHead, afterRevision *uint64, limit uint64, expectedWindow *RevisionStreamRevisionRange, previousRoot *[sha256.Size]byte, minimumLiveHead *RevisionStreamHead) (RevisionStreamQueryRequest, error) {
	invalid := func(message string) (RevisionStreamQueryRequest, error) {
		return RevisionStreamQueryRequest{}, newError(CodeInvalidArgument, "revision stream query", message, nil)
	}
	if len(streamID) == 0 || len(streamID) > maxRevisionStreamIDBytes {
		return invalid("stream ID must be 1-1024 bytes")
	}
	if fromOccurredAt >= untilOccurredAt {
		return invalid("from_occurred_at must be before until_occurred_at")
	}
	if limit == 0 || limit > maxRevisionStreamQueryLimit {
		return invalid("limit must be 1-1000")
	}
	if pinnedHead != nil {
		if err := validateRevisionStreamHead(streamID, *pinnedHead); err != nil {
			return invalid("pinned head is invalid: " + err.Error())
		}
	}
	if afterRevision != nil {
		if pinnedHead == nil || *afterRevision == 0 || *afterRevision >= pinnedHead.Revision {
			return invalid("continuation must be below a pinned head")
		}
		if expectedWindow == nil || previousRoot == nil || minimumLiveHead == nil {
			return invalid("continuation is not SDK-issued")
		}
	}
	request := RevisionStreamQueryRequest{streamID: append([]byte(nil), streamID...), fromOccurredAt: fromOccurredAt, untilOccurredAt: untilOccurredAt, pinnedHead: cloneRevisionStreamHead(pinnedHead), afterRevision: cloneUint64Pointer(afterRevision), limit: limit, previousRoot: previousRoot, minimumLiveHead: cloneRevisionStreamHead(minimumLiveHead)}
	if expectedWindow != nil {
		value := cloneRevisionStreamRange(*expectedWindow)
		request.expectedWindow = &value
	}
	return request, nil
}

// RevisionStreamQueryPage is a strictly decoded pinned page. Continuation is
// nil exactly when the page is terminal.
type RevisionStreamQueryPage struct {
	PinnedHead        *RevisionStreamHead
	ObservedLiveHead  *RevisionStreamHead
	QueryWindow       RevisionStreamRevisionRange
	Page              RevisionStreamRevisionRange
	NextAfterRevision *uint64
	HasMore           bool
	Entries           []RevisionStreamEntry
	Proof             *RevisionStreamQueryProof
	Continuation      *RevisionStreamContinuation
}

func (db *DB) RevisionStreamQuery(request RevisionStreamQueryRequest) (RevisionStreamQueryPage, error) {
	if err := db.RequireCapability("revision_stream"); err != nil {
		return RevisionStreamQueryPage{}, err
	}
	validated, err := newRevisionStreamQueryRequest(request.streamID, request.fromOccurredAt, request.untilOccurredAt, request.pinnedHead, request.afterRevision, request.limit, request.expectedWindow, request.previousRoot, request.minimumLiveHead)
	if err != nil {
		return RevisionStreamQueryPage{}, err
	}
	wire := wireRevisionStreamQueryRequest{Version: revisionStreamVersion, StreamID: presentWireBytes(validated.streamID), FromOccurredAt: canonicalInt64{Value: validated.fromOccurredAt, Present: true}, UntilOccurredAt: canonicalInt64{Value: validated.untilOccurredAt, Present: true}, PinnedHead: toWireRevisionStreamHead(validated.pinnedHead), Limit: validated.limit}
	if validated.afterRevision != nil {
		wire.AfterRevision = &canonicalUint64{Value: *validated.afterRevision, Present: true}
	}
	data, err := db.execute("storage", "revision_stream_query", wire)
	if err != nil {
		return RevisionStreamQueryPage{}, err
	}
	return decodeRevisionStreamQueryPage(data, validated)
}

type wireRevisionStreamQueryRequest struct {
	Version         uint16                  `json:"version"`
	StreamID        wireBytes               `json:"stream_id"`
	FromOccurredAt  canonicalInt64          `json:"from_occurred_at"`
	UntilOccurredAt canonicalInt64          `json:"until_occurred_at"`
	PinnedHead      *wireRevisionStreamHead `json:"pinned_head"`
	AfterRevision   *canonicalUint64        `json:"after_revision"`
	Limit           uint64                  `json:"limit"`
}

type nullableCanonicalUint64 struct {
	Value   uint64
	Present bool
	Null    bool
}

func (value *nullableCanonicalUint64) UnmarshalJSON(data []byte) error {
	value.Present = true
	if bytes.Equal(data, []byte("null")) {
		value.Null = true
		return nil
	}
	var decoded canonicalUint64
	if err := decodeStrictJSON(data, &decoded); err != nil {
		return err
	}
	if !decoded.Present {
		return fmt.Errorf("canonical u64 is missing")
	}
	value.Value = decoded.Value
	return nil
}

type wireRevisionStreamEntry struct {
	Revision           canonicalUint64 `json:"revision"`
	OccurredAt         canonicalInt64  `json:"occurred_at"`
	Payload            wireBytes       `json:"payload"`
	PayloadSHA256      string          `json:"payload_sha256"`
	RootSHA256         string          `json:"root_sha256"`
	PreviousRootSHA256 string          `json:"previous_root_sha256"`
	ContentSHA256      string          `json:"content_sha256"`
	ProofRootSHA256    string          `json:"proof_root_sha256"`
}

type wireRevisionStreamProofStep struct {
	Height     uint8  `json:"height"`
	Side       string `json:"side"`
	RootSHA256 string `json:"root_sha256"`
}

type wireRevisionStreamInclusionProof struct {
	Version    uint16                        `json:"version"`
	Revision   canonicalUint64               `json:"revision"`
	PeakIndex  uint8                         `json:"peak_index"`
	LeafSHA256 string                        `json:"leaf_sha256"`
	Siblings   []wireRevisionStreamProofStep `json:"siblings"`
}

type wireRevisionStreamProofPoint struct {
	Revision           canonicalUint64                  `json:"revision"`
	OccurredAt         canonicalInt64                   `json:"occurred_at"`
	PayloadSHA256      string                           `json:"payload_sha256"`
	PreviousRootSHA256 string                           `json:"previous_root_sha256"`
	ContentSHA256      string                           `json:"content_sha256"`
	ProofRootSHA256    string                           `json:"proof_root_sha256"`
	RootSHA256         string                           `json:"root_sha256"`
	Inclusion          wireRevisionStreamInclusionProof `json:"inclusion"`
}

type wireRevisionStreamQueryProof struct {
	Version           uint16                             `json:"version"`
	Scheme            string                             `json:"scheme"`
	RootSHA256        string                             `json:"root_sha256"`
	WindowPredecessor *wireRevisionStreamProofPoint      `json:"window_predecessor"`
	WindowFirst       *wireRevisionStreamProofPoint      `json:"window_first"`
	WindowLast        *wireRevisionStreamProofPoint      `json:"window_last"`
	WindowSuccessor   *wireRevisionStreamProofPoint      `json:"window_successor"`
	PageSuccessor     *wireRevisionStreamProofPoint      `json:"page_successor"`
	EntryProofs       []wireRevisionStreamInclusionProof `json:"entry_proofs"`
}

type wireRevisionStreamQueryPage struct {
	PinnedHead               json.RawMessage               `json:"pinned_head"`
	ObservedLiveHead         json.RawMessage               `json:"observed_live_head"`
	QueryWindowFirstRevision nullableCanonicalUint64       `json:"query_window_first_revision"`
	QueryWindowLastRevision  nullableCanonicalUint64       `json:"query_window_last_revision"`
	PageFirstRevision        nullableCanonicalUint64       `json:"page_first_revision"`
	PageLastRevision         nullableCanonicalUint64       `json:"page_last_revision"`
	NextAfterRevision        nullableCanonicalUint64       `json:"next_after_revision"`
	HasMore                  *bool                         `json:"has_more"`
	Entries                  []wireRevisionStreamEntry     `json:"entries"`
	Proof                    *wireRevisionStreamQueryProof `json:"proof"`
}

func decodeRevisionStreamQueryPage(data []byte, request RevisionStreamQueryRequest) (RevisionStreamQueryPage, error) {
	var wire wireRevisionStreamQueryPage
	if err := decodeStrictJSON(data, &wire); err != nil {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", err)
	}
	if len(wire.PinnedHead) == 0 || len(wire.ObservedLiveHead) == 0 || !wire.QueryWindowFirstRevision.Present || !wire.QueryWindowLastRevision.Present || !wire.PageFirstRevision.Present || !wire.PageLastRevision.Present || !wire.NextAfterRevision.Present || wire.HasMore == nil || wire.Entries == nil || wire.Proof == nil {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("required query result fields are missing"))
	}
	pinned, err := decodeNullableRevisionStreamHead(request.streamID, wire.PinnedHead, "pinned_head")
	if err != nil {
		return RevisionStreamQueryPage{}, err
	}
	live, err := decodeNullableRevisionStreamHead(request.streamID, wire.ObservedLiveHead, "observed_live_head")
	if err != nil {
		return RevisionStreamQueryPage{}, err
	}
	if pinned == nil || live == nil {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("authenticated result lacks pinned/live head"))
	}
	if request.pinnedHead != nil && !revisionStreamHeadsEqual(*pinned, *request.pinnedHead) {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("response changed the requested pinned head"))
	}
	if live.Revision < pinned.Revision || live.LastOccurredAt < pinned.LastOccurredAt || (live.Revision == pinned.Revision && !revisionStreamHeadsEqual(*live, *pinned)) || (request.pinnedHead == nil && !revisionStreamHeadsEqual(*live, *pinned)) {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("observed live head precedes or contradicts pinned head"))
	}
	if request.minimumLiveHead != nil && (live.Revision < request.minimumLiveHead.Revision || live.LastOccurredAt < request.minimumLiveHead.LastOccurredAt || (live.Revision == request.minimumLiveHead.Revision && !revisionStreamHeadsEqual(*live, *request.minimumLiveHead))) {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("observed live head rolled back across pages"))
	}
	window, err := decodeRevisionStreamRange(wire.QueryWindowFirstRevision, wire.QueryWindowLastRevision, "query window")
	if err != nil {
		return RevisionStreamQueryPage{}, err
	}
	pageRange, err := decodeRevisionStreamRange(wire.PageFirstRevision, wire.PageLastRevision, "page")
	if err != nil {
		return RevisionStreamQueryPage{}, err
	}
	if request.expectedWindow != nil && !revisionStreamRangesEqual(window, *request.expectedWindow) {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("continuation changed the pinned query window"))
	}
	if window.First != nil && (*window.First == 0 || *window.Last > pinned.Revision) {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("query window is outside pinned head"))
	}
	if (len(wire.Entries) == 0) != (pageRange.First == nil) {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("page range does not match entries"))
	}
	if window.First == nil && pageRange.First != nil {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("non-empty page has an empty query window"))
	}
	if pageRange.First != nil && (*pageRange.First < *window.First || *pageRange.Last > *window.Last) {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("page is outside query window"))
	}
	entries := make([]RevisionStreamEntry, len(wire.Entries))
	for index, entryWire := range wire.Entries {
		if !entryWire.Revision.Present || !entryWire.OccurredAt.Present || !entryWire.Payload.Present || entryWire.Payload.Null || entryWire.Revision.Value == 0 || entryWire.Revision.Value > pinned.Revision || !shaPattern.MatchString(entryWire.PayloadSHA256) || !shaPattern.MatchString(entryWire.RootSHA256) || !shaPattern.MatchString(entryWire.PreviousRootSHA256) || !shaPattern.MatchString(entryWire.ContentSHA256) || !shaPattern.MatchString(entryWire.ProofRootSHA256) {
			return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("entry %d has invalid fields", index))
		}
		entries[index] = RevisionStreamEntry{Revision: entryWire.Revision.Value, OccurredAt: entryWire.OccurredAt.Value, Payload: append([]byte{}, entryWire.Payload.Value...), PayloadSHA256: entryWire.PayloadSHA256, RootSHA256: entryWire.RootSHA256, PreviousRootSHA256: entryWire.PreviousRootSHA256, ContentSHA256: entryWire.ContentSHA256, ProofRootSHA256: entryWire.ProofRootSHA256}
	}
	if len(entries) > 0 && (*pageRange.First != entries[0].Revision || *pageRange.Last != entries[len(entries)-1].Revision) {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("page bounds differ from entries"))
	}
	if request.afterRevision == nil && pageRange.First != nil && *pageRange.First != *window.First {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("first page does not begin at query window"))
	}
	if *wire.HasMore {
		if wire.NextAfterRevision.Null || pageRange.Last == nil || wire.NextAfterRevision.Value != *pageRange.Last || *pageRange.Last >= *window.Last {
			return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("continuation cursor is inconsistent"))
		}
	} else if !wire.NextAfterRevision.Null || (pageRange.Last != nil && *pageRange.Last != *window.Last) {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("terminal page bounds/cursor are inconsistent"))
	}
	proof, err := decodeRevisionStreamQueryProof(*wire.Proof)
	if err != nil {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", err)
	}
	result := RevisionStreamQueryPage{PinnedHead: cloneRevisionStreamHead(pinned), ObservedLiveHead: cloneRevisionStreamHead(live), QueryWindow: window, Page: pageRange, HasMore: *wire.HasMore, Entries: entries, Proof: &proof}
	if !wire.NextAfterRevision.Null {
		cursor := wire.NextAfterRevision.Value
		result.NextAfterRevision = &cursor
	}
	if err := verifyRevisionStreamQueryPage(request, result); err != nil {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", err)
	}
	if request.expectedWindow != nil && !revisionStreamRangesEqual(window, *request.expectedWindow) {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("continuation changed the pinned query window"))
	}
	if request.previousRoot != nil && len(entries) > 0 && entries[0].PreviousRootSHA256 != hex.EncodeToString(request.previousRoot[:]) {
		return RevisionStreamQueryPage{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("continuation page changed its prior root"))
	}
	if *wire.HasMore {
		cursor := wire.NextAfterRevision.Value
		lastRoot, _ := decodeRevisionStreamDigest(entries[len(entries)-1].RootSHA256)
		result.Continuation = &RevisionStreamContinuation{streamID: append([]byte(nil), request.streamID...), fromOccurredAt: request.fromOccurredAt, untilOccurredAt: request.untilOccurredAt, pinnedHead: *cloneRevisionStreamHead(pinned), afterRevision: cursor, window: cloneRevisionStreamRange(window), previousRoot: lastRoot, minimumLiveHead: *cloneRevisionStreamHead(live)}
	}
	return result, nil
}

func decodeRevisionStreamQueryProof(wire wireRevisionStreamQueryProof) (RevisionStreamQueryProof, error) {
	if wire.EntryProofs == nil || len(wire.EntryProofs) > maxRevisionStreamQueryLimit {
		return RevisionStreamQueryProof{}, fmt.Errorf("query proof entry proof count is invalid")
	}
	decodePoint := func(value *wireRevisionStreamProofPoint) (*RevisionStreamProofPoint, error) {
		if value == nil {
			return nil, nil
		}
		if !value.Revision.Present || !value.OccurredAt.Present || value.Revision.Value == 0 {
			return nil, fmt.Errorf("query proof point omitted canonical integers")
		}
		for _, digest := range []string{value.PayloadSHA256, value.PreviousRootSHA256, value.ContentSHA256, value.ProofRootSHA256, value.RootSHA256} {
			if !shaPattern.MatchString(digest) {
				return nil, fmt.Errorf("query proof point contains an invalid digest")
			}
		}
		inclusion, err := decodeRevisionStreamInclusion(value.Inclusion)
		if err != nil {
			return nil, err
		}
		return &RevisionStreamProofPoint{Revision: value.Revision.Value, OccurredAt: value.OccurredAt.Value, PayloadSHA256: value.PayloadSHA256, PreviousRootSHA256: value.PreviousRootSHA256, ContentSHA256: value.ContentSHA256, ProofRootSHA256: value.ProofRootSHA256, RootSHA256: value.RootSHA256, Inclusion: inclusion}, nil
	}
	points := make([]*RevisionStreamProofPoint, 5)
	var err error
	for index, value := range []*wireRevisionStreamProofPoint{wire.WindowPredecessor, wire.WindowFirst, wire.WindowLast, wire.WindowSuccessor, wire.PageSuccessor} {
		points[index], err = decodePoint(value)
		if err != nil {
			return RevisionStreamQueryProof{}, err
		}
	}
	entryProofs := make([]RevisionStreamInclusionProof, len(wire.EntryProofs))
	for index, value := range wire.EntryProofs {
		entryProofs[index], err = decodeRevisionStreamInclusion(value)
		if err != nil {
			return RevisionStreamQueryProof{}, err
		}
	}
	return RevisionStreamQueryProof{Version: wire.Version, Scheme: wire.Scheme, RootSHA256: wire.RootSHA256, WindowPredecessor: points[0], WindowFirst: points[1], WindowLast: points[2], WindowSuccessor: points[3], PageSuccessor: points[4], EntryProofs: entryProofs}, nil
}

func decodeRevisionStreamInclusion(wire wireRevisionStreamInclusionProof) (RevisionStreamInclusionProof, error) {
	if !wire.Revision.Present || wire.Revision.Value == 0 || wire.Siblings == nil || len(wire.Siblings) > maxRevisionStreamProofDepth || !shaPattern.MatchString(wire.LeafSHA256) {
		return RevisionStreamInclusionProof{}, fmt.Errorf("Merkle inclusion proof has invalid required fields")
	}
	siblings := make([]RevisionStreamProofStep, len(wire.Siblings))
	for index, step := range wire.Siblings {
		side := RevisionStreamProofSide(step.Side)
		if (side != RevisionStreamProofLeft && side != RevisionStreamProofRight) || !shaPattern.MatchString(step.RootSHA256) {
			return RevisionStreamInclusionProof{}, fmt.Errorf("Merkle inclusion proof step is invalid")
		}
		siblings[index] = RevisionStreamProofStep{Height: step.Height, Side: side, RootSHA256: step.RootSHA256}
	}
	return RevisionStreamInclusionProof{Version: wire.Version, Revision: wire.Revision.Value, PeakIndex: wire.PeakIndex, LeafSHA256: wire.LeafSHA256, Siblings: siblings}, nil
}

func verifyRevisionStreamQueryPage(request RevisionStreamQueryRequest, result RevisionStreamQueryPage) error {
	pinned, live, proof := result.PinnedHead, result.ObservedLiveHead, result.Proof
	if pinned == nil || live == nil || proof == nil || pinned.Proof == nil {
		return fmt.Errorf("authenticated result lacks proof-bearing heads/query proof")
	}
	if proof.Version != revisionStreamProofVersion || proof.Scheme != "talon_mmr_sha256_v1" || proof.RootSHA256 != pinned.Proof.RootSHA256 || len(proof.EntryProofs) != len(result.Entries) {
		return fmt.Errorf("query proof metadata does not match pinned head/page")
	}
	for _, point := range []*RevisionStreamProofPoint{proof.WindowPredecessor, proof.WindowFirst, proof.WindowLast, proof.WindowSuccessor, proof.PageSuccessor} {
		if point != nil {
			if err := verifyRevisionStreamProofPoint(request.streamID, *pinned, *point); err != nil {
				return err
			}
		}
	}
	first, last := result.QueryWindow.First, result.QueryWindow.Last
	if first != nil && last != nil {
		if proof.WindowFirst == nil || proof.WindowLast == nil || proof.WindowFirst.Revision != *first || proof.WindowLast.Revision != *last || proof.WindowFirst.OccurredAt < request.fromOccurredAt || proof.WindowFirst.OccurredAt >= request.untilOccurredAt || proof.WindowLast.OccurredAt < request.fromOccurredAt || proof.WindowLast.OccurredAt >= request.untilOccurredAt {
			return fmt.Errorf("query proof window endpoints do not match requested time range")
		}
		if *first == 1 {
			if proof.WindowPredecessor != nil {
				return fmt.Errorf("query proof lower boundary is non-canonical")
			}
		} else if proof.WindowPredecessor == nil || proof.WindowPredecessor.Revision != *first-1 || proof.WindowPredecessor.OccurredAt >= request.fromOccurredAt {
			return fmt.Errorf("query proof does not bracket the window lower bound")
		}
		if *last == pinned.Revision {
			if proof.WindowSuccessor != nil {
				return fmt.Errorf("query proof upper boundary is non-canonical")
			}
		} else if proof.WindowSuccessor == nil || proof.WindowSuccessor.Revision != *last+1 || proof.WindowSuccessor.OccurredAt < request.untilOccurredAt {
			return fmt.Errorf("query proof does not bracket the window upper bound")
		}
	} else if first == nil && last == nil {
		if proof.WindowFirst != nil || proof.WindowLast != nil {
			return fmt.Errorf("empty query window contains endpoint proofs")
		}
		if proof.WindowPredecessor != nil && proof.WindowPredecessor.OccurredAt >= request.fromOccurredAt {
			return fmt.Errorf("empty query window predecessor is not below lower bound")
		}
		if proof.WindowSuccessor != nil && proof.WindowSuccessor.OccurredAt < request.untilOccurredAt {
			return fmt.Errorf("empty query window successor is not above upper bound")
		}
		switch {
		case proof.WindowPredecessor != nil && proof.WindowSuccessor != nil && proof.WindowPredecessor.Revision != math.MaxUint64 && proof.WindowPredecessor.Revision+1 == proof.WindowSuccessor.Revision:
		case proof.WindowPredecessor == nil && proof.WindowSuccessor != nil && proof.WindowSuccessor.Revision == 1:
		case proof.WindowPredecessor != nil && proof.WindowSuccessor == nil && proof.WindowPredecessor.Revision == pinned.Revision:
		default:
			return fmt.Errorf("empty query window does not prove a complete insertion boundary")
		}
	} else {
		return fmt.Errorf("query window revisions are partially specified")
	}

	var expectedFirst *uint64
	if request.afterRevision != nil {
		value := *request.afterRevision + 1
		expectedFirst = &value
		if first == nil || last == nil || *request.afterRevision < *first || *request.afterRevision > *last || *request.afterRevision >= pinned.Revision {
			return fmt.Errorf("query continuation is outside the authenticated window")
		}
	} else {
		expectedFirst = first
	}
	actualFirst, actualLast := result.Page.First, result.Page.Last
	if len(result.Entries) == 0 {
		if actualFirst != nil || actualLast != nil || (last != nil && (request.afterRevision == nil || *request.afterRevision != *last)) {
			return fmt.Errorf("query page is empty before the authenticated window is exhausted")
		}
	} else if actualFirst == nil || actualLast == nil || *actualFirst != result.Entries[0].Revision || *actualLast != result.Entries[len(result.Entries)-1].Revision || expectedFirst == nil || *actualFirst != *expectedFirst {
		return fmt.Errorf("query page revisions do not match entries/cursor")
	}
	if uint64(len(result.Entries)) > request.limit {
		return fmt.Errorf("query result exceeds requested page limit")
	}
	for index, entry := range result.Entries {
		if index > 0 && (result.Entries[index-1].Revision == math.MaxUint64 || result.Entries[index-1].Revision+1 != entry.Revision || entry.PreviousRootSHA256 != result.Entries[index-1].RootSHA256) {
			return fmt.Errorf("query page entry chain is disconnected")
		}
		if entry.OccurredAt < request.fromOccurredAt || entry.OccurredAt >= request.untilOccurredAt || first == nil || last == nil || entry.Revision < *first || entry.Revision > *last {
			return fmt.Errorf("query page entry is outside the authenticated time window")
		}
		if err := verifyRevisionStreamEntryInclusion(request.streamID, *pinned, entry, proof.EntryProofs[index]); err != nil {
			return err
		}
	}
	if len(result.Entries) > 0 {
		tail := result.Entries[len(result.Entries)-1]
		if tail.Revision == pinned.Revision {
			if proof.PageSuccessor != nil || tail.RootSHA256 != pinned.RootSHA256 {
				return fmt.Errorf("query page tail does not reach pinned head")
			}
		} else if proof.PageSuccessor == nil || proof.PageSuccessor.Revision != tail.Revision+1 || proof.PageSuccessor.PreviousRootSHA256 != tail.RootSHA256 {
			return fmt.Errorf("query page tail is not authenticated by its successor")
		}
	}
	expectedMore := actualLast != nil && last != nil && *actualLast < *last
	if result.HasMore != expectedMore || (expectedMore && (result.NextAfterRevision == nil || *result.NextAfterRevision != *actualLast)) || (!expectedMore && result.NextAfterRevision != nil) {
		return fmt.Errorf("query continuation metadata does not match authenticated window")
	}
	return nil
}

func verifyRevisionStreamProofPoint(streamID []byte, pinned RevisionStreamHead, point RevisionStreamProofPoint) error {
	previous, err := decodeRevisionStreamDigest(point.PreviousRootSHA256)
	if err != nil {
		return err
	}
	payload, err := decodeRevisionStreamDigest(point.PayloadSHA256)
	if err != nil {
		return err
	}
	content := calculateRevisionStreamContent(streamID, previous, point.Revision, point.OccurredAt, payload)
	expectedContent, err := decodeRevisionStreamDigest(point.ContentSHA256)
	if err != nil || content != expectedContent {
		return fmt.Errorf("query proof point content digest mismatch")
	}
	proofRoot, err := decodeRevisionStreamDigest(point.ProofRootSHA256)
	if err != nil {
		return err
	}
	root, err := decodeRevisionStreamDigest(point.RootSHA256)
	if err != nil || calculateRevisionStreamHeadRoot(streamID, point.Revision, point.OccurredAt, content, proofRoot) != root {
		return fmt.Errorf("query proof point head digest mismatch")
	}
	return verifyRevisionStreamInclusion(streamID, pinned, point.Revision, content, point.Inclusion)
}

func verifyRevisionStreamEntryInclusion(streamID []byte, pinned RevisionStreamHead, entry RevisionStreamEntry, inclusion RevisionStreamInclusionProof) error {
	payload := sha256.Sum256(entry.Payload)
	expectedPayload, err := decodeRevisionStreamDigest(entry.PayloadSHA256)
	if err != nil || payload != expectedPayload {
		return fmt.Errorf("query entry payload digest mismatch")
	}
	previous, err := decodeRevisionStreamDigest(entry.PreviousRootSHA256)
	if err != nil {
		return fmt.Errorf("query v2 entry lacks previous root")
	}
	content := calculateRevisionStreamContent(streamID, previous, entry.Revision, entry.OccurredAt, payload)
	expectedContent, err := decodeRevisionStreamDigest(entry.ContentSHA256)
	if err != nil || content != expectedContent {
		return fmt.Errorf("query entry content digest mismatch")
	}
	proofRoot, err := decodeRevisionStreamDigest(entry.ProofRootSHA256)
	if err != nil {
		return fmt.Errorf("query v2 entry lacks proof root")
	}
	root, err := decodeRevisionStreamDigest(entry.RootSHA256)
	if err != nil || calculateRevisionStreamHeadRoot(streamID, entry.Revision, entry.OccurredAt, content, proofRoot) != root {
		return fmt.Errorf("query entry head digest mismatch")
	}
	return verifyRevisionStreamInclusion(streamID, pinned, entry.Revision, content, inclusion)
}

func verifyRevisionStreamInclusion(streamID []byte, pinned RevisionStreamHead, revision uint64, content [sha256.Size]byte, inclusion RevisionStreamInclusionProof) error {
	if pinned.Proof == nil || inclusion.Version != revisionStreamProofVersion || inclusion.Revision != revision {
		return fmt.Errorf("Merkle inclusion proof version/revision mismatch")
	}
	if int(inclusion.PeakIndex) >= len(pinned.Proof.Peaks) {
		return fmt.Errorf("Merkle inclusion peak index is invalid")
	}
	peak := pinned.Proof.Peaks[inclusion.PeakIndex]
	if peak.Height > maxRevisionStreamProofDepth || revision < peak.StartRevision || uint64(revision-peak.StartRevision) >= uint64(1)<<peak.Height || len(inclusion.Siblings) != int(peak.Height) {
		return fmt.Errorf("Merkle inclusion proof does not fit its peak")
	}
	local := revision - peak.StartRevision
	leaf := calculateRevisionStreamMerkleLeaf(streamID, revision, content)
	expectedLeaf, err := decodeRevisionStreamDigest(inclusion.LeafSHA256)
	if err != nil || leaf != expectedLeaf {
		return fmt.Errorf("Merkle inclusion leaf digest mismatch")
	}
	current := leaf
	for index, step := range inclusion.Siblings {
		height := uint8(index)
		span := uint64(1) << height
		blockStart := peak.StartRevision + ((local >> height) << height)
		isRight := ((local >> height) & 1) == 1
		expectedSide := RevisionStreamProofRight
		if isRight {
			expectedSide = RevisionStreamProofLeft
		}
		if step.Height != height || step.Side != expectedSide {
			return fmt.Errorf("Merkle inclusion proof path is non-canonical")
		}
		sibling, err := decodeRevisionStreamDigest(step.RootSHA256)
		if err != nil {
			return err
		}
		parentStart := blockStart
		if isRight {
			parentStart -= span
			current = calculateRevisionStreamMerkleNode(streamID, height+1, parentStart, sibling, current)
		} else {
			current = calculateRevisionStreamMerkleNode(streamID, height+1, parentStart, current, sibling)
		}
	}
	peakRoot, err := decodeRevisionStreamDigest(peak.RootSHA256)
	if err != nil || current != peakRoot {
		return fmt.Errorf("Merkle inclusion proof does not reach pinned peak")
	}
	return nil
}

func decodeRevisionStreamRange(first, last nullableCanonicalUint64, name string) (RevisionStreamRevisionRange, error) {
	if first.Null != last.Null {
		return RevisionStreamRevisionRange{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("%s bounds have inconsistent nullability", name))
	}
	if first.Null {
		return RevisionStreamRevisionRange{}, nil
	}
	if first.Value == 0 || first.Value > last.Value {
		return RevisionStreamRevisionRange{}, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("%s bounds are invalid", name))
	}
	return RevisionStreamRevisionRange{First: cloneUint64Pointer(&first.Value), Last: cloneUint64Pointer(&last.Value)}, nil
}

func cloneRevisionStreamRange(value RevisionStreamRevisionRange) RevisionStreamRevisionRange {
	return RevisionStreamRevisionRange{First: cloneUint64Pointer(value.First), Last: cloneUint64Pointer(value.Last)}
}
func revisionStreamRangesEqual(left, right RevisionStreamRevisionRange) bool {
	return uint64PointersEqual(left.First, right.First) && uint64PointersEqual(left.Last, right.Last)
}
func uint64PointersEqual(left, right *uint64) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}
func cloneUint64Pointer(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func decodeNullableRevisionStreamHead(streamID, data []byte, name string) (*RevisionStreamHead, error) {
	if bytes.Equal(data, []byte("null")) {
		return nil, nil
	}
	var wire wireRevisionStreamHead
	if err := decodeStrictJSON(data, &wire); err != nil {
		return nil, revisionStreamProtocolError("decode revision stream query", fmt.Errorf("%s: %w", name, err))
	}
	head, err := decodeWireRevisionStreamHead(streamID, wire)
	if err != nil {
		return nil, err
	}
	return &head, nil
}

func decodeWireRevisionStreamHead(streamID []byte, wire wireRevisionStreamHead) (RevisionStreamHead, error) {
	head := RevisionStreamHead{Revision: wire.Revision.Value, LastOccurredAt: wire.LastOccurredAt.Value, RootSHA256: wire.RootSHA256}
	if !wire.Revision.Present || !wire.LastOccurredAt.Present {
		return RevisionStreamHead{}, revisionStreamProtocolError("decode revision stream head", fmt.Errorf("head omitted canonical integer fields"))
	}
	if wire.Proof == nil || !wire.Proof.LeafCount.Present || wire.Proof.Peaks == nil {
		return RevisionStreamHead{}, revisionStreamProtocolError("decode revision stream head", fmt.Errorf("v2 head omitted proof fields"))
	}
	peaks := make([]RevisionStreamMerklePeak, len(wire.Proof.Peaks))
	for index, peak := range wire.Proof.Peaks {
		if !peak.StartRevision.Present {
			return RevisionStreamHead{}, revisionStreamProtocolError("decode revision stream head", fmt.Errorf("proof peak omitted canonical start revision"))
		}
		peaks[index] = RevisionStreamMerklePeak{Height: peak.Height, StartRevision: peak.StartRevision.Value, RootSHA256: peak.RootSHA256}
	}
	head.Proof = &RevisionStreamProofHead{Version: wire.Proof.Version, LeafCount: wire.Proof.LeafCount.Value, RootSHA256: wire.Proof.RootSHA256, LastContentSHA256: wire.Proof.LastContentSHA256, Peaks: peaks}
	if err := validateRevisionStreamHead(streamID, head); err != nil {
		return RevisionStreamHead{}, revisionStreamProtocolError("decode revision stream head", err)
	}
	return head, nil
}

func validateRevisionStreamHead(streamID []byte, head RevisionStreamHead) error {
	if head.Revision == 0 {
		return fmt.Errorf("revision must be positive")
	}
	root, err := decodeRevisionStreamDigest(head.RootSHA256)
	if err != nil {
		return err
	}
	if head.Proof == nil || head.Proof.Version != revisionStreamProofVersion || head.Proof.LeafCount != head.Revision {
		return fmt.Errorf("revision stream v2 head requires matching authenticated proof")
	}
	if err := validateRevisionStreamPeaks(streamID, head.Proof.LeafCount, head.Proof.Peaks, head.Proof.RootSHA256); err != nil {
		return err
	}
	content, err := decodeRevisionStreamDigest(head.Proof.LastContentSHA256)
	if err != nil {
		return err
	}
	proofRoot, err := decodeRevisionStreamDigest(head.Proof.RootSHA256)
	if err != nil {
		return err
	}
	if calculateRevisionStreamHeadRoot(streamID, head.Revision, head.LastOccurredAt, content, proofRoot) != root {
		return fmt.Errorf("revision stream v2 head does not bind its proof root")
	}
	return nil
}

func decodeRevisionStreamDigest(value string) ([sha256.Size]byte, error) {
	var result [sha256.Size]byte
	if !shaPattern.MatchString(value) {
		return result, fmt.Errorf("SHA-256 must be 64 lowercase hexadecimal characters")
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return result, err
	}
	copy(result[:], decoded)
	return result, nil
}

func revisionStreamProtocolError(operation string, cause error) error {
	return newError(CodeProtocolViolation, operation, "native revision stream response violated its typed contract", cause)
}
