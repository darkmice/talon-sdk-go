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
	"strconv"
	"strings"
)

const (
	conditionalTransactionVersion = 2
	maxConditionalItems           = 1_000_000
	maxConditionalKeyBytes        = 65_536
	maxConditionalValueBytes      = 1 << 20
)

var (
	conditionalNamespacePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	conditionalRequestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)
)

// ConditionalCompareOperator is a bytewise comparison operator.
type ConditionalCompareOperator string

const (
	CompareEqual          ConditionalCompareOperator = "eq"
	CompareNotEqual       ConditionalCompareOperator = "ne"
	CompareLessThan       ConditionalCompareOperator = "lt"
	CompareLessOrEqual    ConditionalCompareOperator = "le"
	CompareGreaterThan    ConditionalCompareOperator = "gt"
	CompareGreaterOrEqual ConditionalCompareOperator = "ge"
)

// ConditionalTransactionCondition compares the current byte value with
// Expected. A nil Expected means that the key must be absent; a non-nil empty
// slice means that it must contain an empty value.
type ConditionalTransactionCondition struct {
	Key      []byte
	Expected []byte
	Operator ConditionalCompareOperator
}

type conditionalMutationKind uint8

const (
	conditionalPut conditionalMutationKind = iota + 1
	conditionalDelete
	conditionalIncrement
)

// ConditionalTransactionMutation is a closed mutation value. Construct it
// with ConditionalPut, ConditionalDelete, or ConditionalIncrement.
type ConditionalTransactionMutation struct {
	kind  conditionalMutationKind
	key   []byte
	value []byte
	delta int64
}

// ConditionalTransactionRequest is an immutable, validated transaction
// identity. Keep this value until the transaction reaches a final result: the
// same request is required to recover a receipt after an indeterminate
// acknowledgement.
type ConditionalTransactionRequest struct {
	namespace     string
	requestID     string
	conditions    []ConditionalTransactionCondition
	mutations     []ConditionalTransactionMutation
	commandSHA256 string
}

func ConditionalPut(key, value []byte) ConditionalTransactionMutation {
	return ConditionalTransactionMutation{kind: conditionalPut, key: append([]byte(nil), key...), value: append([]byte{}, value...)}
}

func ConditionalDelete(key []byte) ConditionalTransactionMutation {
	return ConditionalTransactionMutation{kind: conditionalDelete, key: append([]byte(nil), key...)}
}

// ConditionalIncrement adds delta to a signed big-endian i64 value. An absent
// key is treated as zero by Core.
func ConditionalIncrement(key []byte, delta int64) ConditionalTransactionMutation {
	return ConditionalTransactionMutation{kind: conditionalIncrement, key: append([]byte(nil), key...), delta: delta}
}

// NewConditionalTransactionRequest validates and copies the complete request.
// Its private state cannot be rebound by mutating the caller's input slices.
func NewConditionalTransactionRequest(namespace, requestID string, conditions []ConditionalTransactionCondition, mutations []ConditionalTransactionMutation) (ConditionalTransactionRequest, error) {
	copiedConditions := cloneConditionalTransactionConditions(conditions)
	copiedMutations := cloneConditionalTransactionMutations(mutations)
	_, commandSHA, err := buildConditionalRequest(namespace, requestID, copiedConditions, copiedMutations)
	if err != nil {
		return ConditionalTransactionRequest{}, err
	}
	return ConditionalTransactionRequest{
		namespace:     namespace,
		requestID:     requestID,
		conditions:    copiedConditions,
		mutations:     copiedMutations,
		commandSHA256: commandSHA,
	}, nil
}

func (request ConditionalTransactionRequest) Namespace() string { return request.namespace }
func (request ConditionalTransactionRequest) RequestID() string { return request.requestID }
func (request ConditionalTransactionRequest) CommandSHA256() string {
	return request.commandSHA256
}

type ConditionalTransactionOutcome string

const (
	ConditionalOutcomeApplied  ConditionalTransactionOutcome = "applied"
	ConditionalOutcomeConflict ConditionalTransactionOutcome = "conflict"
)

type ConditionalTransactionResultKind string

const (
	ConditionalResultApplied   ConditionalTransactionResultKind = "applied"
	ConditionalResultDuplicate ConditionalTransactionResultKind = "duplicate"
	ConditionalResultConflict  ConditionalTransactionResultKind = "conflict"
)

type ConditionalConflictKind string

const (
	ConditionalConflictConditionFailed  ConditionalConflictKind = "condition_failed"
	ConditionalConflictCounterInvalid   ConditionalConflictKind = "counter_invalid"
	ConditionalConflictCounterOverflow  ConditionalConflictKind = "counter_overflow"
	ConditionalConflictCounterUnderflow ConditionalConflictKind = "counter_underflow"
)

type ConditionalTransactionConflict struct {
	Kind  ConditionalConflictKind
	Index uint64
}

type ConditionalTransactionObservation struct {
	Index     uint64
	Keyspace  string
	Key       []byte
	Before    []byte
	After     []byte
	Matched   *bool
	BeforeI64 *int64
	AfterI64  *int64
}

type ConditionalTransactionReceipt struct {
	Version       uint16
	RequestID     string
	CommandSHA256 string
	Revision      uint64
	Outcome       ConditionalTransactionOutcome
	Conflict      *ConditionalTransactionConflict
	Conditions    []ConditionalTransactionObservation
	Mutations     []ConditionalTransactionObservation
	ReceiptSHA256 string
}

type QuorumReceiptStage string

const (
	QuorumReceiptAccepted  QuorumReceiptStage = "accepted"
	QuorumReceiptCommitted QuorumReceiptStage = "committed"
	QuorumReceiptApplied   QuorumReceiptStage = "applied"
)

type QuorumDurability string

const (
	QuorumDurabilityNone        QuorumDurability = "none"
	QuorumDurabilityLocalFsync  QuorumDurability = "local_fsync"
	QuorumDurabilityQuorumFsync QuorumDurability = "quorum_fsync"
)

type ConditionalQuorumReceipt struct {
	Version       uint16
	RequestID     string
	Index         uint64
	Term          uint64
	Stage         QuorumReceiptStage
	Durability    QuorumDurability
	CommandSHA256 string
	ResultSHA256  *string
}

type ConditionalTransactionResult struct {
	Result         ConditionalTransactionResultKind
	OriginalResult ConditionalTransactionOutcome
	Applied        bool
	Rejected       bool
	Duplicate      bool
	Revision       uint64
	Receipt        ConditionalTransactionReceipt
	QuorumReceipt  *ConditionalQuorumReceipt
}

type ConditionalReceiptStatus string

const (
	ConditionalReceiptFound         ConditionalReceiptStatus = "receipt"
	ConditionalReceiptIndeterminate ConditionalReceiptStatus = "indeterminate"
)

type ConditionalReceiptLookup struct {
	Status        ConditionalReceiptStatus
	RequestID     string
	Revision      *uint64
	Receipt       *ConditionalTransactionReceipt
	QuorumReceipt *ConditionalQuorumReceipt
}

type wireBytes struct {
	Value   []byte
	Present bool
	Null    bool
}

func presentWireBytes(value []byte) wireBytes {
	return wireBytes{Value: append([]byte{}, value...), Present: true}
}

func nullableWireBytes(value []byte) wireBytes {
	if value == nil {
		return wireBytes{Present: true, Null: true}
	}
	return presentWireBytes(value)
}

func (value wireBytes) MarshalJSON() ([]byte, error) {
	if value.Null {
		return []byte("null"), nil
	}
	buffer := make([]byte, 0, 2+len(value.Value)*4)
	buffer = append(buffer, '[')
	for index, item := range value.Value {
		if index != 0 {
			buffer = append(buffer, ',')
		}
		buffer = strconv.AppendUint(buffer, uint64(item), 10)
	}
	buffer = append(buffer, ']')
	return buffer, nil
}

func (value *wireBytes) UnmarshalJSON(data []byte) error {
	value.Present = true
	if bytes.Equal(data, []byte("null")) {
		value.Null = true
		value.Value = nil
		return nil
	}
	var numbers []json.RawMessage
	if err := decodeStrictJSON(data, &numbers); err != nil {
		return fmt.Errorf("byte array: %w", err)
	}
	if numbers == nil || len(numbers) > maxNativeJSONResultBytes {
		return fmt.Errorf("byte array is null or exceeds the response bound")
	}
	value.Value = make([]byte, len(numbers))
	for index, raw := range numbers {
		var number uint16
		if err := decodeStrictJSON(raw, &number); err != nil || string(raw) != strconv.FormatUint(uint64(number), 10) || number > 255 {
			return fmt.Errorf("byte array item %d exceeds 255", index)
		}
		value.Value[index] = byte(number)
	}
	return nil
}

type canonicalUint64 struct {
	Value   uint64
	Present bool
}

func (value *canonicalUint64) UnmarshalJSON(data []byte) error {
	var text string
	if err := decodeStrictJSON(data, &text); err != nil {
		return fmt.Errorf("canonical u64 must be a JSON string: %w", err)
	}
	parsed, err := strconv.ParseUint(text, 10, 64)
	if err != nil || strconv.FormatUint(parsed, 10) != text {
		return fmt.Errorf("invalid canonical u64 %q", text)
	}
	value.Value = parsed
	value.Present = true
	return nil
}

func (value canonicalUint64) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatUint(value.Value, 10))
}

type canonicalInt64 struct {
	Value   int64
	Present bool
}

func (value *canonicalInt64) UnmarshalJSON(data []byte) error {
	var text string
	if err := decodeStrictJSON(data, &text); err != nil {
		return fmt.Errorf("canonical i64 must be a JSON string: %w", err)
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil || strconv.FormatInt(parsed, 10) != text {
		return fmt.Errorf("invalid canonical i64 %q", text)
	}
	value.Value = parsed
	value.Present = true
	return nil
}

func (value canonicalInt64) MarshalJSON() ([]byte, error) {
	return json.Marshal(strconv.FormatInt(value.Value, 10))
}

type wireConditionalCondition struct {
	Key      wireBytes `json:"key"`
	Expected wireBytes `json:"expected"`
	Operator string    `json:"operator"`
}

type wireConditionalMutation struct {
	Operation string     `json:"op"`
	Key       wireBytes  `json:"key"`
	Value     *wireBytes `json:"value,omitempty"`
	Delta     *string    `json:"delta,omitempty"`
}

type wireConditionalRequest struct {
	Version    uint16                     `json:"version"`
	RequestID  string                     `json:"request_id"`
	Namespace  string                     `json:"namespace"`
	Conditions []wireConditionalCondition `json:"conditions"`
	Mutations  []wireConditionalMutation  `json:"mutations"`
}

type builtConditionalRequest struct {
	wire       wireConditionalRequest
	namespace  string
	requestID  string
	conditions []ConditionalTransactionCondition
	mutations  []ConditionalTransactionMutation
	commandSHA string
}

func buildClosedConditionalRequest(request ConditionalTransactionRequest) (builtConditionalRequest, error) {
	wire, commandSHA, err := buildConditionalRequest(request.namespace, request.requestID, request.conditions, request.mutations)
	if err != nil {
		return builtConditionalRequest{}, newError(CodeInvalidArgument, "conditional transaction request", "request was not created by NewConditionalTransactionRequest", err)
	}
	if request.commandSHA256 == "" || request.commandSHA256 != commandSHA {
		return builtConditionalRequest{}, newError(CodeInvalidArgument, "conditional transaction request", "request identity is invalid", nil)
	}
	return builtConditionalRequest{
		wire:       wire,
		namespace:  request.namespace,
		requestID:  request.requestID,
		conditions: request.conditions,
		mutations:  request.mutations,
		commandSHA: commandSHA,
	}, nil
}

func cloneConditionalTransactionConditions(conditions []ConditionalTransactionCondition) []ConditionalTransactionCondition {
	if conditions == nil {
		return nil
	}
	cloned := make([]ConditionalTransactionCondition, len(conditions))
	for index, condition := range conditions {
		cloned[index] = ConditionalTransactionCondition{
			Key:      append([]byte(nil), condition.Key...),
			Expected: cloneOptionalByteSlice(condition.Expected),
			Operator: condition.Operator,
		}
	}
	return cloned
}

func cloneConditionalTransactionMutations(mutations []ConditionalTransactionMutation) []ConditionalTransactionMutation {
	if mutations == nil {
		return nil
	}
	cloned := make([]ConditionalTransactionMutation, len(mutations))
	for index, mutation := range mutations {
		cloned[index] = ConditionalTransactionMutation{
			kind:  mutation.kind,
			key:   append([]byte(nil), mutation.key...),
			value: cloneOptionalByteSlice(mutation.value),
			delta: mutation.delta,
		}
	}
	return cloned
}

func cloneOptionalByteSlice(value []byte) []byte {
	if value == nil {
		return nil
	}
	return append([]byte{}, value...)
}

func buildConditionalRequest(namespace, requestID string, conditions []ConditionalTransactionCondition, mutations []ConditionalTransactionMutation) (wireConditionalRequest, string, error) {
	if !conditionalNamespacePattern.MatchString(namespace) {
		return wireConditionalRequest{}, "", newError(CodeInvalidArgument, "conditional transaction", "namespace must be 1-64 safe ASCII bytes", nil)
	}
	if !conditionalRequestIDPattern.MatchString(requestID) {
		return wireConditionalRequest{}, "", newError(CodeInvalidArgument, "conditional transaction", "request ID must be 1-128 safe ASCII bytes", nil)
	}
	if len(conditions) > maxConditionalItems || len(mutations) == 0 || len(mutations) > maxConditionalItems {
		return wireConditionalRequest{}, "", newError(CodeInvalidArgument, "conditional transaction", "condition/mutation count is invalid", nil)
	}
	estimatedWireBytes := int64(1_024) + int64(len(namespace)+len(requestID)) + int64(len(conditions)+len(mutations))*512
	if estimatedWireBytes > maxNativeJSONRequestBytes {
		return wireConditionalRequest{}, "", newError(CodeInvalidArgument, "conditional transaction", "encoded request exceeds the SDK bound", nil)
	}
	request := wireConditionalRequest{Version: conditionalTransactionVersion, RequestID: requestID, Namespace: namespace, Conditions: make([]wireConditionalCondition, len(conditions)), Mutations: make([]wireConditionalMutation, len(mutations))}
	conditionKeys := make(map[string]struct{}, len(conditions))
	for index, condition := range conditions {
		if len(condition.Key) == 0 || len(condition.Key) > maxConditionalKeyBytes || !validCompareOperator(condition.Operator) {
			return wireConditionalRequest{}, "", newError(CodeInvalidArgument, "conditional transaction", fmt.Sprintf("condition %d is invalid", index), nil)
		}
		if len(condition.Expected) > maxConditionalValueBytes {
			return wireConditionalRequest{}, "", newError(CodeInvalidArgument, "conditional transaction", fmt.Sprintf("condition %d expected value exceeds the SDK bound", index), nil)
		}
		estimatedWireBytes += wireByteArrayJSONSize(condition.Key)
		if condition.Expected != nil {
			estimatedWireBytes += wireByteArrayJSONSize(condition.Expected)
		}
		if estimatedWireBytes > maxNativeJSONRequestBytes {
			return wireConditionalRequest{}, "", newError(CodeInvalidArgument, "conditional transaction", "encoded request exceeds the SDK bound", nil)
		}
		key := string(condition.Key)
		if _, duplicate := conditionKeys[key]; duplicate {
			return wireConditionalRequest{}, "", newError(CodeInvalidArgument, "conditional transaction", fmt.Sprintf("condition %d repeats a condition key", index), nil)
		}
		conditionKeys[key] = struct{}{}
		request.Conditions[index] = wireConditionalCondition{Key: presentWireBytes(condition.Key), Expected: nullableWireBytes(condition.Expected), Operator: string(condition.Operator)}
	}
	mutationKeys := make(map[string]struct{}, len(mutations))
	for index, mutation := range mutations {
		if len(mutation.key) == 0 || len(mutation.key) > maxConditionalKeyBytes {
			return wireConditionalRequest{}, "", newError(CodeInvalidArgument, "conditional transaction", fmt.Sprintf("mutation %d key is empty or exceeds the SDK bound", index), nil)
		}
		key := string(mutation.key)
		if _, duplicate := mutationKeys[key]; duplicate {
			return wireConditionalRequest{}, "", newError(CodeInvalidArgument, "conditional transaction", fmt.Sprintf("mutation %d repeats a mutation key", index), nil)
		}
		mutationKeys[key] = struct{}{}
		wire := wireConditionalMutation{Key: presentWireBytes(mutation.key)}
		switch mutation.kind {
		case conditionalPut:
			if len(mutation.value) > maxConditionalValueBytes {
				return wireConditionalRequest{}, "", newError(CodeInvalidArgument, "conditional transaction", fmt.Sprintf("mutation %d value exceeds the SDK bound", index), nil)
			}
			wire.Operation = "put"
			value := presentWireBytes(mutation.value)
			wire.Value = &value
		case conditionalDelete:
			wire.Operation = "delete"
		case conditionalIncrement:
			wire.Operation = "increment"
			delta := strconv.FormatInt(mutation.delta, 10)
			wire.Delta = &delta
		default:
			return wireConditionalRequest{}, "", newError(CodeInvalidArgument, "conditional transaction", fmt.Sprintf("mutation %d was not created by a constructor", index), nil)
		}
		estimatedWireBytes += wireByteArrayJSONSize(mutation.key)
		if mutation.kind == conditionalPut {
			estimatedWireBytes += wireByteArrayJSONSize(mutation.value)
		}
		if estimatedWireBytes > maxNativeJSONRequestBytes {
			return wireConditionalRequest{}, "", newError(CodeInvalidArgument, "conditional transaction", "encoded request exceeds the SDK bound", nil)
		}
		request.Mutations[index] = wire
	}
	commandSHA, err := conditionalCommandSHA(namespace, conditions, mutations)
	if err != nil {
		return wireConditionalRequest{}, "", err
	}
	return request, commandSHA, nil
}

func wireByteArrayJSONSize(value []byte) int64 {
	size := int64(2)
	for index, item := range value {
		if index != 0 {
			size++
		}
		switch {
		case item < 10:
			size++
		case item < 100:
			size += 2
		default:
			size += 3
		}
	}
	return size
}

func validCompareOperator(operator ConditionalCompareOperator) bool {
	switch operator {
	case CompareEqual, CompareNotEqual, CompareLessThan, CompareLessOrEqual, CompareGreaterThan, CompareGreaterOrEqual:
		return true
	default:
		return false
	}
}

func conditionalCommandSHA(namespace string, conditions []ConditionalTransactionCondition, mutations []ConditionalTransactionMutation) (string, error) {
	return conditionalCommandSHAForKeyspace("__user_conditional__."+namespace, conditions, mutations)
}

func conditionalCommandSHAForKeyspace(keyspace string, conditions []ConditionalTransactionCondition, mutations []ConditionalTransactionMutation) (string, error) {
	buffer := append([]byte(nil), []byte("TLNCTX2")...)
	buffer = binary.LittleEndian.AppendUint16(buffer, conditionalTransactionVersion)
	buffer = binary.LittleEndian.AppendUint32(buffer, uint32(len(conditions)))
	writeBytes := func(value []byte) {
		buffer = binary.LittleEndian.AppendUint32(buffer, uint32(len(value)))
		buffer = append(buffer, value...)
	}
	for _, condition := range conditions {
		writeBytes([]byte(keyspace))
		writeBytes(condition.Key)
		if condition.Expected == nil {
			buffer = append(buffer, 0)
		} else {
			buffer = append(buffer, 1)
			writeBytes(condition.Expected)
		}
		operators := map[ConditionalCompareOperator]byte{CompareEqual: 0, CompareNotEqual: 1, CompareLessThan: 2, CompareLessOrEqual: 3, CompareGreaterThan: 4, CompareGreaterOrEqual: 5}
		buffer = append(buffer, operators[condition.Operator])
	}
	buffer = binary.LittleEndian.AppendUint32(buffer, uint32(len(mutations)))
	for _, mutation := range mutations {
		switch mutation.kind {
		case conditionalPut:
			buffer = append(buffer, 0)
			writeBytes([]byte(keyspace))
			writeBytes(mutation.key)
			writeBytes(mutation.value)
		case conditionalDelete:
			buffer = append(buffer, 1)
			writeBytes([]byte(keyspace))
			writeBytes(mutation.key)
		case conditionalIncrement:
			buffer = append(buffer, 2)
			writeBytes([]byte(keyspace))
			writeBytes(mutation.key)
			buffer = binary.LittleEndian.AppendUint64(buffer, uint64(mutation.delta))
		default:
			return "", newError(CodeInvalidArgument, "conditional transaction", "invalid mutation kind", nil)
		}
	}
	digest := sha256.Sum256(buffer)
	return hex.EncodeToString(digest[:]), nil
}

type wireConditionalConflict struct {
	Kind  string  `json:"kind"`
	Index *uint64 `json:"index"`
}

type wireConditionalObservation struct {
	Index     *uint64         `json:"index"`
	Keyspace  string          `json:"keyspace"`
	Key       wireBytes       `json:"key"`
	Before    wireBytes       `json:"before"`
	After     wireBytes       `json:"after"`
	Matched   json.RawMessage `json:"matched,omitempty"`
	BeforeI64 json.RawMessage `json:"before_i64,omitempty"`
	AfterI64  json.RawMessage `json:"after_i64,omitempty"`
}

type wireConditionalReceipt struct {
	Version       uint16                       `json:"version"`
	RequestID     string                       `json:"request_id"`
	CommandSHA256 string                       `json:"command_sha256"`
	Revision      canonicalUint64              `json:"revision"`
	Outcome       string                       `json:"outcome"`
	Conflict      json.RawMessage              `json:"conflict,omitempty"`
	Conditions    []wireConditionalObservation `json:"conditions"`
	Mutations     []wireConditionalObservation `json:"mutations"`
	ReceiptSHA256 string                       `json:"receipt_sha256"`
}

type wireConditionalResult struct {
	Result         string          `json:"result"`
	OriginalResult string          `json:"original_result"`
	Applied        *bool           `json:"applied"`
	Duplicate      *bool           `json:"duplicate"`
	Revision       canonicalUint64 `json:"revision"`
	Receipt        json.RawMessage `json:"receipt"`
	QuorumReceipt  json.RawMessage `json:"quorum_receipt,omitempty"`
}

type wireConditionalLookup struct {
	Result        string          `json:"result"`
	RequestID     string          `json:"request_id"`
	Revision      canonicalUint64 `json:"revision"`
	Receipt       json.RawMessage `json:"receipt,omitempty"`
	StreamReceipt json.RawMessage `json:"stream_receipt,omitempty"`
	QuorumReceipt json.RawMessage `json:"quorum_receipt,omitempty"`
}

type wireQuorumReceipt struct {
	Version       uint16          `json:"version"`
	RequestID     string          `json:"request_id"`
	Index         canonicalUint64 `json:"index"`
	Term          canonicalUint64 `json:"term"`
	Stage         string          `json:"stage"`
	Durability    string          `json:"durability"`
	CommandSHA256 string          `json:"command_sha256"`
	ResultSHA256  json.RawMessage `json:"result_sha256"`
}

func decodeConditionalTransactionResult(data []byte, request builtConditionalRequest) (ConditionalTransactionResult, error) {
	var wire wireConditionalResult
	if err := decodeStrictJSON(data, &wire); err != nil {
		return ConditionalTransactionResult{}, protocolError("decode conditional transaction", err)
	}
	if wire.Applied == nil || wire.Duplicate == nil || !wire.Revision.Present || len(wire.Receipt) == 0 || bytes.Equal(wire.Receipt, []byte("null")) {
		return ConditionalTransactionResult{}, protocolError("decode conditional transaction", fmt.Errorf("required result fields are missing"))
	}
	receiptWire, receipt, err := decodeConditionalReceipt(wire.Receipt, request.requestID)
	if err != nil {
		return ConditionalTransactionResult{}, err
	}
	if wire.Revision.Value != receipt.Revision || receipt.CommandSHA256 != request.commandSHA {
		return ConditionalTransactionResult{}, protocolError("decode conditional transaction", fmt.Errorf("top-level revision or command hash differs from receipt"))
	}
	resultKind := ConditionalTransactionResultKind(wire.Result)
	original := ConditionalTransactionOutcome(wire.OriginalResult)
	if (resultKind != ConditionalResultApplied && resultKind != ConditionalResultDuplicate && resultKind != ConditionalResultConflict) || (original != ConditionalOutcomeApplied && original != ConditionalOutcomeConflict) {
		return ConditionalTransactionResult{}, protocolError("decode conditional transaction", fmt.Errorf("unknown result enum"))
	}
	if original != receipt.Outcome || *wire.Applied != (original == ConditionalOutcomeApplied) || *wire.Duplicate != (resultKind == ConditionalResultDuplicate) || (!*wire.Duplicate && string(resultKind) != string(original)) {
		return ConditionalTransactionResult{}, protocolError("decode conditional transaction", fmt.Errorf("result/original_result/applied/duplicate fields are inconsistent"))
	}
	if err := validateReceiptAgainstRequest(receipt, request.namespace, request.conditions, request.mutations); err != nil {
		return ConditionalTransactionResult{}, protocolError("decode conditional transaction", err)
	}
	quorum, err := decodeOptionalQuorumReceipt(wire.QuorumReceipt, request.requestID, request.commandSHA, receiptWire, receipt.Revision)
	if err != nil {
		return ConditionalTransactionResult{}, err
	}
	return ConditionalTransactionResult{Result: resultKind, OriginalResult: original, Applied: *wire.Applied, Rejected: !*wire.Applied, Duplicate: *wire.Duplicate, Revision: wire.Revision.Value, Receipt: receipt, QuorumReceipt: quorum}, nil
}

func decodeConditionalReceiptLookup(data []byte, request builtConditionalRequest) (ConditionalReceiptLookup, error) {
	var wire wireConditionalLookup
	if err := decodeStrictJSON(data, &wire); err != nil {
		return ConditionalReceiptLookup{}, protocolError("decode conditional receipt", err)
	}
	if wire.RequestID != request.requestID {
		return ConditionalReceiptLookup{}, protocolError("decode conditional receipt", fmt.Errorf("request ID mismatch"))
	}
	if len(wire.StreamReceipt) != 0 {
		return ConditionalReceiptLookup{}, protocolError("decode conditional receipt", fmt.Errorf("generic receipt lookup cannot consume a revision stream receipt"))
	}
	switch wire.Result {
	case string(ConditionalReceiptIndeterminate):
		if wire.Revision.Present || len(wire.Receipt) != 0 {
			return ConditionalReceiptLookup{}, protocolError("decode conditional receipt", fmt.Errorf("indeterminate lookup included a receipt"))
		}
		quorum, err := decodeOptionalQuorumReceipt(wire.QuorumReceipt, request.requestID, request.commandSHA, nil, 0)
		if err != nil || quorum == nil {
			if err == nil {
				err = protocolError("decode conditional receipt", fmt.Errorf("indeterminate lookup omitted quorum receipt"))
			}
			return ConditionalReceiptLookup{}, err
		}
		if quorum.ResultSHA256 != nil || quorum.Stage == QuorumReceiptApplied {
			return ConditionalReceiptLookup{}, protocolError("decode conditional receipt", fmt.Errorf("indeterminate lookup included an applied result"))
		}
		return ConditionalReceiptLookup{Status: ConditionalReceiptIndeterminate, RequestID: request.requestID, QuorumReceipt: quorum}, nil
	case string(ConditionalReceiptFound):
		if !wire.Revision.Present || len(wire.Receipt) == 0 || bytes.Equal(wire.Receipt, []byte("null")) {
			return ConditionalReceiptLookup{}, protocolError("decode conditional receipt", fmt.Errorf("receipt lookup omitted receipt or revision"))
		}
		receiptWire, receipt, err := decodeConditionalReceipt(wire.Receipt, request.requestID)
		if err != nil {
			return ConditionalReceiptLookup{}, err
		}
		if wire.Revision.Value != receipt.Revision || receipt.CommandSHA256 != request.commandSHA {
			return ConditionalReceiptLookup{}, protocolError("decode conditional receipt", fmt.Errorf("receipt identity differs from exact transaction request"))
		}
		if err := validateReceiptAgainstRequest(receipt, request.namespace, request.conditions, request.mutations); err != nil {
			return ConditionalReceiptLookup{}, protocolError("decode conditional receipt", err)
		}
		quorum, err := decodeOptionalQuorumReceipt(wire.QuorumReceipt, request.requestID, request.commandSHA, receiptWire, receipt.Revision)
		if err != nil {
			return ConditionalReceiptLookup{}, err
		}
		revision := receipt.Revision
		return ConditionalReceiptLookup{Status: ConditionalReceiptFound, RequestID: request.requestID, Revision: &revision, Receipt: &receipt, QuorumReceipt: quorum}, nil
	default:
		return ConditionalReceiptLookup{}, protocolError("decode conditional receipt", fmt.Errorf("unknown receipt lookup result %q", wire.Result))
	}
}

func decodeConditionalReceipt(data []byte, requestID string) (*wireConditionalReceipt, ConditionalTransactionReceipt, error) {
	return decodeConditionalReceiptWithLimit(data, requestID, maxConditionalValueBytes, false)
}

func decodeConditionalReceiptWithLimit(data []byte, requestID string, maxValueBytes int, allowRevisionStreamInternal bool) (*wireConditionalReceipt, ConditionalTransactionReceipt, error) {
	var wire wireConditionalReceipt
	if err := decodeStrictJSON(data, &wire); err != nil {
		return nil, ConditionalTransactionReceipt{}, protocolError("decode conditional receipt", err)
	}
	if wire.Version != conditionalTransactionVersion || wire.RequestID != requestID || !conditionalRequestIDPattern.MatchString(wire.RequestID) || !wire.Revision.Present || wire.Revision.Value == 0 || !shaPattern.MatchString(wire.CommandSHA256) || !shaPattern.MatchString(wire.ReceiptSHA256) || wire.Conditions == nil || len(wire.Mutations) == 0 {
		return nil, ConditionalTransactionReceipt{}, protocolError("decode conditional receipt", fmt.Errorf("receipt identity or required fields are invalid"))
	}
	if wire.Outcome != string(ConditionalOutcomeApplied) && wire.Outcome != string(ConditionalOutcomeConflict) {
		return nil, ConditionalTransactionReceipt{}, protocolError("decode conditional receipt", fmt.Errorf("unknown receipt outcome %q", wire.Outcome))
	}
	conflictWire, err := decodeOptionalWireConflict(wire.Conflict)
	if err != nil {
		return nil, ConditionalTransactionReceipt{}, err
	}
	if (wire.Outcome == string(ConditionalOutcomeApplied)) != (conflictWire == nil) {
		return nil, ConditionalTransactionReceipt{}, protocolError("decode conditional receipt", fmt.Errorf("receipt outcome/conflict fields are inconsistent"))
	}
	conflict, err := decodeConditionalConflict(conflictWire, len(wire.Conditions), len(wire.Mutations))
	if err != nil {
		return nil, ConditionalTransactionReceipt{}, err
	}
	conditions, err := decodeConditionalObservations(wire.Conditions, true, maxValueBytes, allowRevisionStreamInternal)
	if err != nil {
		return nil, ConditionalTransactionReceipt{}, err
	}
	mutations, err := decodeConditionalObservations(wire.Mutations, false, maxValueBytes, allowRevisionStreamInternal)
	if err != nil {
		return nil, ConditionalTransactionReceipt{}, err
	}
	if wire.Outcome == string(ConditionalOutcomeApplied) {
		for _, observation := range conditions {
			if observation.Matched == nil || !*observation.Matched {
				return nil, ConditionalTransactionReceipt{}, protocolError("decode conditional receipt", fmt.Errorf("applied receipt contains a rejected condition"))
			}
		}
	} else {
		firstRejected := -1
		for index, observation := range conditions {
			if !nullableBytesEqual(observation.Before, observation.After) {
				return nil, ConditionalTransactionReceipt{}, protocolError("decode conditional receipt", fmt.Errorf("conflict receipt changed condition observation %d", index))
			}
			if firstRejected == -1 && observation.Matched != nil && !*observation.Matched {
				firstRejected = index
			}
		}
		if firstRejected >= 0 {
			if conflict == nil || conflict.Kind != ConditionalConflictConditionFailed || conflict.Index != uint64(firstRejected) {
				return nil, ConditionalTransactionReceipt{}, protocolError("decode conditional receipt", fmt.Errorf("condition conflict does not identify the first rejected observation"))
			}
		} else if conflict != nil && conflict.Kind == ConditionalConflictConditionFailed {
			return nil, ConditionalTransactionReceipt{}, protocolError("decode conditional receipt", fmt.Errorf("condition conflict has no rejected observation"))
		}
	}
	expectedHash, err := conditionalReceiptSHA(wire)
	if err != nil || expectedHash != wire.ReceiptSHA256 {
		if err == nil {
			err = fmt.Errorf("receipt SHA-256 mismatch")
		}
		return nil, ConditionalTransactionReceipt{}, protocolError("decode conditional receipt", err)
	}
	receipt := ConditionalTransactionReceipt{Version: wire.Version, RequestID: wire.RequestID, CommandSHA256: wire.CommandSHA256, Revision: wire.Revision.Value, Outcome: ConditionalTransactionOutcome(wire.Outcome), Conflict: conflict, Conditions: conditions, Mutations: mutations, ReceiptSHA256: wire.ReceiptSHA256}
	return &wire, receipt, nil
}

func decodeOptionalWireConflict(data json.RawMessage) (*wireConditionalConflict, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if bytes.Equal(data, []byte("null")) {
		return nil, protocolError("decode conditional receipt", fmt.Errorf("conflict must be omitted, not null"))
	}
	var conflict wireConditionalConflict
	if err := decodeStrictJSON(data, &conflict); err != nil {
		return nil, protocolError("decode conditional receipt", err)
	}
	return &conflict, nil
}

func decodeConditionalConflict(wire *wireConditionalConflict, conditionCount, mutationCount int) (*ConditionalTransactionConflict, error) {
	if wire == nil {
		return nil, nil
	}
	if wire.Index == nil {
		return nil, protocolError("decode conditional receipt", fmt.Errorf("conflict index is missing"))
	}
	kind := ConditionalConflictKind(wire.Kind)
	limit := mutationCount
	if kind == ConditionalConflictConditionFailed {
		limit = conditionCount
	} else if kind != ConditionalConflictCounterInvalid && kind != ConditionalConflictCounterOverflow && kind != ConditionalConflictCounterUnderflow {
		return nil, protocolError("decode conditional receipt", fmt.Errorf("unknown conflict kind %q", wire.Kind))
	}
	if *wire.Index >= uint64(limit) {
		return nil, protocolError("decode conditional receipt", fmt.Errorf("conflict index is out of range"))
	}
	return &ConditionalTransactionConflict{Kind: kind, Index: *wire.Index}, nil
}

func decodeConditionalObservations(values []wireConditionalObservation, conditions bool, maxValueBytes int, allowRevisionStreamInternal bool) ([]ConditionalTransactionObservation, error) {
	result := make([]ConditionalTransactionObservation, len(values))
	for index, wire := range values {
		namespace, hasPrefix := strings.CutPrefix(wire.Keyspace, "__user_conditional__.")
		userKeyspace := hasPrefix && conditionalNamespacePattern.MatchString(namespace)
		internalRevisionStream := allowRevisionStreamInternal && wire.Keyspace == revisionStreamInternalKeyspace
		if wire.Index == nil || *wire.Index != uint64(index) || (!userKeyspace && !internalRevisionStream) || !wire.Key.Present || wire.Key.Null || !wire.Before.Present || !wire.After.Present || len(wire.Key.Value) > maxConditionalKeyBytes || len(wire.Before.Value) > maxValueBytes || len(wire.After.Value) > maxValueBytes {
			return nil, protocolError("decode conditional receipt", fmt.Errorf("observation %d has invalid required fields", index))
		}
		matched, err := decodeOptionalBool(wire.Matched)
		if err != nil || (conditions && matched == nil) || (!conditions && matched != nil) {
			if err == nil {
				err = fmt.Errorf("observation %d matched field is invalid", index)
			}
			return nil, protocolError("decode conditional receipt", err)
		}
		beforeI64, err := decodeOptionalCanonicalInt64(wire.BeforeI64)
		if err != nil {
			return nil, protocolError("decode conditional receipt", err)
		}
		afterI64, err := decodeOptionalCanonicalInt64(wire.AfterI64)
		if err != nil || (beforeI64 == nil) != (afterI64 == nil) {
			if err == nil {
				err = fmt.Errorf("observation %d i64 fields are inconsistent", index)
			}
			return nil, protocolError("decode conditional receipt", err)
		}
		if beforeI64 != nil && (!wireBytesMatchesI64(wire.Before, *beforeI64) || !wireBytesMatchesI64(wire.After, *afterI64)) {
			return nil, protocolError("decode conditional receipt", fmt.Errorf("observation %d i64 fields do not match byte values", index))
		}
		result[index] = ConditionalTransactionObservation{Index: uint64(index), Keyspace: wire.Keyspace, Key: append([]byte(nil), wire.Key.Value...), Before: cloneNullableBytes(wire.Before), After: cloneNullableBytes(wire.After), Matched: matched, BeforeI64: beforeI64, AfterI64: afterI64}
	}
	return result, nil
}

func wireBytesMatchesI64(value wireBytes, expected int64) bool {
	if value.Null {
		return expected == 0
	}
	return len(value.Value) == 8 && int64(binary.BigEndian.Uint64(value.Value)) == expected
}

func cloneNullableBytes(value wireBytes) []byte {
	if value.Null {
		return nil
	}
	return append([]byte{}, value.Value...)
}

func decodeOptionalBool(data json.RawMessage) (*bool, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if bytes.Equal(data, []byte("null")) {
		return nil, fmt.Errorf("optional bool must be omitted, not null")
	}
	var value bool
	if err := decodeStrictJSON(data, &value); err != nil {
		return nil, err
	}
	return &value, nil
}

func decodeOptionalCanonicalInt64(data json.RawMessage) (*int64, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if bytes.Equal(data, []byte("null")) {
		return nil, fmt.Errorf("optional i64 must be omitted, not null")
	}
	var value canonicalInt64
	if err := decodeStrictJSON(data, &value); err != nil {
		return nil, err
	}
	return &value.Value, nil
}

func conditionalReceiptSHA(receipt wireConditionalReceipt) (string, error) {
	data, err := conditionalReceiptCanonicalJSON(receipt, "")
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func conditionalReceiptCanonicalJSON(receipt wireConditionalReceipt, receiptSHA string) ([]byte, error) {
	conflict, err := decodeOptionalWireConflict(receipt.Conflict)
	if err != nil {
		return nil, err
	}
	observationValue := func(observation wireConditionalObservation) (map[string]interface{}, error) {
		value := map[string]interface{}{
			"index":    *observation.Index,
			"keyspace": observation.Keyspace,
			"key":      observation.Key,
			"before":   observation.Before,
			"after":    observation.After,
		}
		if len(observation.Matched) != 0 {
			var matched bool
			if err := decodeStrictJSON(observation.Matched, &matched); err != nil {
				return nil, err
			}
			value["matched"] = matched
		}
		for name, raw := range map[string]json.RawMessage{"before_i64": observation.BeforeI64, "after_i64": observation.AfterI64} {
			if len(raw) == 0 {
				continue
			}
			var number canonicalInt64
			if err := decodeStrictJSON(raw, &number); err != nil {
				return nil, err
			}
			value[name] = strconv.FormatInt(number.Value, 10)
		}
		return value, nil
	}
	conditions := make([]map[string]interface{}, len(receipt.Conditions))
	for index, observation := range receipt.Conditions {
		conditions[index], err = observationValue(observation)
		if err != nil {
			return nil, err
		}
	}
	mutations := make([]map[string]interface{}, len(receipt.Mutations))
	for index, observation := range receipt.Mutations {
		mutations[index], err = observationValue(observation)
		if err != nil {
			return nil, err
		}
	}
	canonical := map[string]interface{}{
		"version":        receipt.Version,
		"request_id":     receipt.RequestID,
		"command_sha256": receipt.CommandSHA256,
		"revision":       strconv.FormatUint(receipt.Revision.Value, 10),
		"outcome":        receipt.Outcome,
		"conditions":     conditions,
		"mutations":      mutations,
		"receipt_sha256": receiptSHA,
	}
	if conflict != nil {
		canonical["conflict"] = map[string]interface{}{"kind": conflict.Kind, "index": *conflict.Index}
	}
	return marshalWithoutHTMLEscape(canonical)
}

func marshalWithoutHTMLEscape(value interface{}) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}

func decodeOptionalQuorumReceipt(data json.RawMessage, requestID, commandSHA string, conditional *wireConditionalReceipt, revision uint64) (*ConditionalQuorumReceipt, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if bytes.Equal(data, []byte("null")) {
		return nil, protocolError("decode quorum receipt", fmt.Errorf("quorum_receipt must be omitted, not null"))
	}
	var wire wireQuorumReceipt
	if err := decodeStrictJSON(data, &wire); err != nil {
		return nil, protocolError("decode quorum receipt", err)
	}
	if wire.Version != 1 || wire.RequestID != requestID || wire.CommandSHA256 != commandSHA || !wire.Index.Present || wire.Index.Value == 0 || !wire.Term.Present || !shaPattern.MatchString(wire.CommandSHA256) {
		return nil, protocolError("decode quorum receipt", fmt.Errorf("quorum receipt identity is invalid"))
	}
	stage := QuorumReceiptStage(wire.Stage)
	if stage != QuorumReceiptAccepted && stage != QuorumReceiptCommitted && stage != QuorumReceiptApplied {
		return nil, protocolError("decode quorum receipt", fmt.Errorf("unknown quorum stage %q", wire.Stage))
	}
	durability := QuorumDurability(wire.Durability)
	if durability != QuorumDurabilityNone && durability != QuorumDurabilityLocalFsync && durability != QuorumDurabilityQuorumFsync {
		return nil, protocolError("decode quorum receipt", fmt.Errorf("unknown quorum durability %q", wire.Durability))
	}
	if (stage == QuorumReceiptAccepted && durability != QuorumDurabilityLocalFsync) || (stage != QuorumReceiptAccepted && durability != QuorumDurabilityQuorumFsync) {
		return nil, protocolError("decode quorum receipt", fmt.Errorf("quorum stage/durability fields are inconsistent"))
	}
	resultSHA, err := decodeNullableSHA256(wire.ResultSHA256)
	if err != nil {
		return nil, protocolError("decode quorum receipt", err)
	}
	if conditional != nil {
		if wire.Index.Value != revision || stage != QuorumReceiptApplied || durability != QuorumDurabilityQuorumFsync || resultSHA == nil {
			return nil, protocolError("decode quorum receipt", fmt.Errorf("applied quorum receipt is inconsistent with transaction receipt"))
		}
		encoded, err := conditionalReceiptCanonicalJSON(*conditional, conditional.ReceiptSHA256)
		if err != nil {
			return nil, protocolError("decode quorum receipt", err)
		}
		digest := sha256.Sum256(encoded)
		if hex.EncodeToString(digest[:]) != *resultSHA {
			return nil, protocolError("decode quorum receipt", fmt.Errorf("quorum result SHA-256 differs from the conditional receipt"))
		}
	}
	return &ConditionalQuorumReceipt{Version: wire.Version, RequestID: wire.RequestID, Index: wire.Index.Value, Term: wire.Term.Value, Stage: stage, Durability: durability, CommandSHA256: wire.CommandSHA256, ResultSHA256: cloneStringPointer(resultSHA)}, nil
}

func decodeNullableSHA256(data json.RawMessage) (*string, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("result_sha256 is missing")
	}
	if bytes.Equal(data, []byte("null")) {
		return nil, nil
	}
	var value string
	if err := decodeStrictJSON(data, &value); err != nil || !shaPattern.MatchString(value) {
		return nil, fmt.Errorf("invalid optional SHA-256")
	}
	return &value, nil
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func validateReceiptAgainstRequest(receipt ConditionalTransactionReceipt, namespace string, conditions []ConditionalTransactionCondition, mutations []ConditionalTransactionMutation) error {
	keyspace := "__user_conditional__." + namespace
	if len(receipt.Conditions) != len(conditions) || len(receipt.Mutations) != len(mutations) {
		return fmt.Errorf("receipt observation counts differ from request")
	}
	for index, observation := range receipt.Conditions {
		condition := conditions[index]
		if observation.Index != uint64(index) || observation.Keyspace != keyspace || !bytes.Equal(observation.Key, condition.Key) || observation.Matched == nil || observation.BeforeI64 != nil || observation.AfterI64 != nil {
			return fmt.Errorf("condition observation %d differs from request", index)
		}
		if *observation.Matched != conditionalValueMatches(observation.Before, condition.Expected, condition.Operator) {
			return fmt.Errorf("condition observation %d matched flag is false evidence", index)
		}
	}
	for index, observation := range receipt.Mutations {
		if observation.Index != uint64(index) || observation.Keyspace != keyspace || !bytes.Equal(observation.Key, mutations[index].key) || observation.Matched != nil {
			return fmt.Errorf("mutation observation %d differs from request", index)
		}
		mutation := mutations[index]
		if mutation.kind != conditionalIncrement && (observation.BeforeI64 != nil || observation.AfterI64 != nil) {
			return fmt.Errorf("non-increment mutation %d included i64 fields", index)
		}
		if receipt.Outcome == ConditionalOutcomeConflict {
			if !nullableBytesEqual(observation.Before, observation.After) {
				return fmt.Errorf("rejected mutation %d changed state", index)
			}
			if receipt.Conflict != nil && receipt.Conflict.Index == uint64(index) && receipt.Conflict.Kind != ConditionalConflictConditionFailed {
				if mutation.kind != conditionalIncrement {
					return fmt.Errorf("counter conflict %d does not identify an increment", index)
				}
				switch receipt.Conflict.Kind {
				case ConditionalConflictCounterInvalid:
					if observation.Before == nil || len(observation.Before) == 8 || observation.BeforeI64 != nil || observation.AfterI64 != nil {
						return fmt.Errorf("counter-invalid conflict %d has invalid evidence", index)
					}
				case ConditionalConflictCounterOverflow, ConditionalConflictCounterUnderflow:
					if observation.BeforeI64 == nil || observation.AfterI64 == nil || *observation.BeforeI64 != *observation.AfterI64 {
						return fmt.Errorf("counter range conflict %d has invalid numeric evidence", index)
					}
					_, overflow := addInt64(*observation.BeforeI64, mutation.delta)
					if !overflow || (receipt.Conflict.Kind == ConditionalConflictCounterOverflow) != (mutation.delta > 0) || (receipt.Conflict.Kind == ConditionalConflictCounterUnderflow) != (mutation.delta < 0) {
						return fmt.Errorf("counter range conflict %d does not match the requested delta", index)
					}
				}
			}
			continue
		}
		switch mutation.kind {
		case conditionalPut:
			if observation.After == nil || !bytes.Equal(observation.After, mutation.value) {
				return fmt.Errorf("put mutation %d after value is invalid", index)
			}
		case conditionalDelete:
			if observation.After != nil {
				return fmt.Errorf("delete mutation %d after value is not absent", index)
			}
		case conditionalIncrement:
			if observation.BeforeI64 == nil || observation.AfterI64 == nil || !bytesMatchI64(observation.Before, *observation.BeforeI64) || !bytesMatchI64(observation.After, *observation.AfterI64) {
				return fmt.Errorf("increment mutation %d evidence is invalid", index)
			}
			expected, overflow := addInt64(*observation.BeforeI64, mutation.delta)
			if overflow || expected != *observation.AfterI64 {
				return fmt.Errorf("increment mutation %d delta evidence is invalid", index)
			}
		}
	}
	return nil
}

func addInt64(left, right int64) (int64, bool) {
	value := left + right
	return value, (right > 0 && value < left) || (right < 0 && value > left)
}

func conditionalValueMatches(actual, expected []byte, operator ConditionalCompareOperator) bool {
	comparison := compareNullableBytes(actual, expected)
	switch operator {
	case CompareEqual:
		return comparison == 0
	case CompareNotEqual:
		return comparison != 0
	case CompareLessThan:
		return comparison < 0
	case CompareLessOrEqual:
		return comparison <= 0
	case CompareGreaterThan:
		return comparison > 0
	case CompareGreaterOrEqual:
		return comparison >= 0
	default:
		return false
	}
}

func compareNullableBytes(left, right []byte) int {
	if left == nil {
		if right == nil {
			return 0
		}
		return -1
	}
	if right == nil {
		return 1
	}
	return bytes.Compare(left, right)
}

func nullableBytesEqual(left, right []byte) bool {
	return (left == nil) == (right == nil) && bytes.Equal(left, right)
}

func bytesMatchI64(value []byte, expected int64) bool {
	if value == nil {
		return expected == 0
	}
	return len(value) == 8 && int64(binary.BigEndian.Uint64(value)) == expected
}

func protocolError(operation string, cause error) error {
	return newError(CodeProtocolViolation, operation, "native conditional transaction response violated its typed contract", cause)
}
