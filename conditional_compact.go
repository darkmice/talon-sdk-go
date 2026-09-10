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
	"strconv"
)

const (
	conditionalTransactionCompactVersion  = 3
	compactConditionalMaxValueBytes       = 8 << 20
	compactConditionalMaxCommandBytes     = 32 << 20
	compactConditionalMaxReceiptBytes     = 16 << 20
	compactConditionalMaxJSONRequestBytes = 40 << 20
	compactConditionalCapability          = "native_conditional_transaction_v3"
	compactReceiptFeature                 = "compact_receipt_v1"
)

// ConditionalTransactionV3Version is the explicit draft transaction version
// that carries compact_receipt_v1 observations.
const ConditionalTransactionV3Version uint16 = conditionalTransactionCompactVersion

// CompactReceiptV1Feature is the Core self-attestation feature required by the
// v3 draft API in addition to native_conditional_transaction_v3.
const CompactReceiptV1Feature = compactReceiptFeature

// ConditionalTransactionV3Capability is the exact capability name used by the
// draft runtime-admission contract.
const ConditionalTransactionV3Capability = compactConditionalCapability

// ConditionalTransactionV3Request is an immutable v3 transaction identity.
// The SDK draft retains TLNCTX2 command framing with version=3. This matches a
// local, uncommitted Core working draft, not a released or remote Core ABI.
type ConditionalTransactionV3Request struct {
	namespace           string
	requestID           string
	conditions          []ConditionalTransactionCondition
	mutations           []ConditionalTransactionMutation
	commandSHA256       string
	quorumCommandSHA256 string
}

// NewConditionalTransactionV3Request validates and copies a compact-receipt
// request. Each expected/put value is bounded at 8 MiB. Only eq/ne conditions
// are admitted because compact value identity cannot prove byte ordering.
func NewConditionalTransactionV3Request(namespace, requestID string, conditions []ConditionalTransactionCondition, mutations []ConditionalTransactionMutation) (ConditionalTransactionV3Request, error) {
	copiedConditions := cloneConditionalTransactionConditions(conditions)
	copiedMutations := cloneConditionalTransactionMutations(mutations)
	_, commandSHA, err := buildConditionalRequestVersion(conditionalTransactionCompactVersion, compactConditionalMaxValueBytes, compactConditionalMaxJSONRequestBytes, namespace, requestID, copiedConditions, copiedMutations)
	if err != nil {
		return ConditionalTransactionV3Request{}, err
	}
	quorumCommandSHA, err := conditionalQuorumCommandSHA(conditionalTransactionCompactVersion, requestID, namespace, copiedConditions, copiedMutations)
	if err != nil {
		return ConditionalTransactionV3Request{}, err
	}
	return ConditionalTransactionV3Request{
		namespace:           namespace,
		requestID:           requestID,
		conditions:          copiedConditions,
		mutations:           copiedMutations,
		commandSHA256:       commandSHA,
		quorumCommandSHA256: quorumCommandSHA,
	}, nil
}

func (request ConditionalTransactionV3Request) Version() uint16 {
	return conditionalTransactionCompactVersion
}
func (request ConditionalTransactionV3Request) Namespace() string     { return request.namespace }
func (request ConditionalTransactionV3Request) RequestID() string     { return request.requestID }
func (request ConditionalTransactionV3Request) CommandSHA256() string { return request.commandSHA256 }

type builtConditionalV3Request struct {
	wire             wireConditionalRequest
	namespace        string
	requestID        string
	conditions       []ConditionalTransactionCondition
	mutations        []ConditionalTransactionMutation
	commandSHA       string
	quorumCommandSHA string
}

func buildClosedConditionalV3Request(request ConditionalTransactionV3Request) (builtConditionalV3Request, error) {
	wire, commandSHA, err := buildConditionalRequestVersion(conditionalTransactionCompactVersion, compactConditionalMaxValueBytes, compactConditionalMaxJSONRequestBytes, request.namespace, request.requestID, request.conditions, request.mutations)
	if err != nil {
		return builtConditionalV3Request{}, newError(CodeInvalidArgument, "conditional transaction v3 request", "request was not created by NewConditionalTransactionV3Request", err)
	}
	quorumCommandSHA, err := conditionalQuorumCommandSHA(conditionalTransactionCompactVersion, request.requestID, request.namespace, request.conditions, request.mutations)
	if err != nil {
		return builtConditionalV3Request{}, err
	}
	if request.commandSHA256 == "" || request.commandSHA256 != commandSHA || request.quorumCommandSHA256 == "" || request.quorumCommandSHA256 != quorumCommandSHA {
		return builtConditionalV3Request{}, newError(CodeInvalidArgument, "conditional transaction v3 request", "request identity is invalid", nil)
	}
	return builtConditionalV3Request{
		wire:             wire,
		namespace:        request.namespace,
		requestID:        request.requestID,
		conditions:       request.conditions,
		mutations:        request.mutations,
		commandSHA:       commandSHA,
		quorumCommandSHA: quorumCommandSHA,
	}, nil
}

// ConditionalValueObservation identifies an absent or present value without
// returning its bytes. Present empty values carry SHA-256(empty); absent values
// have ByteLength zero and SHA256 nil.
type ConditionalValueObservation struct {
	Present    bool
	ByteLength uint64
	SHA256     *string
}

// ConditionalTransactionV3Observation is the compact_receipt_v1 observation
// for one ordered condition or mutation.
type ConditionalTransactionV3Observation struct {
	Index     uint64
	Keyspace  string
	Key       []byte
	Before    ConditionalValueObservation
	After     ConditionalValueObservation
	Matched   *bool
	BeforeI64 *int64
	AfterI64  *int64
}

// ConditionalTransactionV3Receipt is the typed compact receipt. It never
// exposes full before/after values.
type ConditionalTransactionV3Receipt struct {
	Version       uint16
	RequestID     string
	CommandSHA256 string
	Revision      uint64
	Outcome       ConditionalTransactionOutcome
	Conflict      *ConditionalTransactionConflict
	Conditions    []ConditionalTransactionV3Observation
	Mutations     []ConditionalTransactionV3Observation
	ReceiptSHA256 string
}

// ConditionalTransactionV3Result is the typed result envelope for the draft
// compact transaction contract.
type ConditionalTransactionV3Result struct {
	Result         ConditionalTransactionResultKind
	OriginalResult ConditionalTransactionOutcome
	Applied        bool
	Rejected       bool
	Duplicate      bool
	Revision       uint64
	Receipt        ConditionalTransactionV3Receipt
	QuorumReceipt  *ConditionalQuorumReceipt
}

// ConditionalTransactionV3ReceiptLookup is the exact-request recovery result
// after an indeterminate acknowledgement.
type ConditionalTransactionV3ReceiptLookup struct {
	Status        ConditionalReceiptStatus
	RequestID     string
	Revision      *uint64
	Receipt       *ConditionalTransactionV3Receipt
	QuorumReceipt *ConditionalQuorumReceipt
}

// ConditionalTransactionV3 is a convenience wrapper for one v3 compact-receipt
// transaction. Callers that need safe recovery after an indeterminate
// acknowledgement must retain a value from NewConditionalTransactionV3Request
// and call ExecuteConditionalTransactionV3. Core capability admission remains
// fail closed.
func (db *DB) ConditionalTransactionV3(namespace, requestID string, conditions []ConditionalTransactionCondition, mutations []ConditionalTransactionMutation) (ConditionalTransactionV3Result, error) {
	request, err := NewConditionalTransactionV3Request(namespace, requestID, conditions, mutations)
	if err != nil {
		return ConditionalTransactionV3Result{}, err
	}
	return db.ExecuteConditionalTransactionV3(request)
}

// ExecuteConditionalTransactionV3 executes a sealed v3 request only when Core
// self-attests both native_conditional_transaction_v3 and compact_receipt_v1.
func (db *DB) ExecuteConditionalTransactionV3(request ConditionalTransactionV3Request) (ConditionalTransactionV3Result, error) {
	built, err := buildClosedConditionalV3Request(request)
	if err != nil {
		return ConditionalTransactionV3Result{}, err
	}
	if err := db.RequireCapability(compactConditionalCapability); err != nil {
		return ConditionalTransactionV3Result{}, err
	}
	data, err := db.executeWithBounds("storage", "conditional_batch", built.wire, compactConditionalMaxJSONRequestBytes, maxNativeJSONResultBytes)
	if err != nil {
		return ConditionalTransactionV3Result{}, err
	}
	return decodeConditionalTransactionV3Result(data, built)
}

// ConditionalTransactionV3ReceiptFor resolves the durable result for the same
// sealed v3 request after response loss.
func (db *DB) ConditionalTransactionV3ReceiptFor(request ConditionalTransactionV3Request) (ConditionalTransactionV3ReceiptLookup, error) {
	built, err := buildClosedConditionalV3Request(request)
	if err != nil {
		return ConditionalTransactionV3ReceiptLookup{}, err
	}
	if err := db.RequireCapability(compactConditionalCapability); err != nil {
		return ConditionalTransactionV3ReceiptLookup{}, err
	}
	data, err := db.execute("storage", "conditional_receipt", struct {
		RequestID string `json:"request_id"`
	}{built.requestID})
	if err != nil {
		return ConditionalTransactionV3ReceiptLookup{}, err
	}
	return decodeConditionalTransactionV3ReceiptLookup(data, built)
}

type wireCompactValueObservation struct {
	Present    *bool           `json:"present"`
	ByteLength *uint64         `json:"byte_length"`
	SHA256     json.RawMessage `json:"sha256,omitempty"`
}

type wireConditionalV3Observation struct {
	Index         *uint64                      `json:"index"`
	Keyspace      string                       `json:"keyspace"`
	Key           wireBytes                    `json:"key"`
	Before        json.RawMessage              `json:"before"`
	After         json.RawMessage              `json:"after"`
	Matched       json.RawMessage              `json:"matched,omitempty"`
	BeforeI64     json.RawMessage              `json:"before_i64,omitempty"`
	AfterI64      json.RawMessage              `json:"after_i64,omitempty"`
	BeforeCompact *wireCompactValueObservation `json:"before_compact"`
	AfterCompact  *wireCompactValueObservation `json:"after_compact"`
}

type wireConditionalV3Receipt struct {
	Version       uint16                         `json:"version"`
	RequestID     string                         `json:"request_id"`
	CommandSHA256 string                         `json:"command_sha256"`
	Revision      canonicalUint64                `json:"revision"`
	Outcome       string                         `json:"outcome"`
	Conflict      json.RawMessage                `json:"conflict,omitempty"`
	Conditions    []wireConditionalV3Observation `json:"conditions"`
	Mutations     []wireConditionalV3Observation `json:"mutations"`
	ReceiptSHA256 string                         `json:"receipt_sha256"`
}

func decodeConditionalTransactionV3Result(data []byte, request builtConditionalV3Request) (ConditionalTransactionV3Result, error) {
	var wire wireConditionalResult
	if err := decodeStrictJSON(data, &wire); err != nil {
		return ConditionalTransactionV3Result{}, protocolError("decode conditional transaction v3", err)
	}
	if wire.Applied == nil || wire.Duplicate == nil || !wire.Revision.Present || len(wire.Receipt) == 0 || bytes.Equal(wire.Receipt, []byte("null")) {
		return ConditionalTransactionV3Result{}, protocolError("decode conditional transaction v3", fmt.Errorf("required result fields are missing"))
	}
	receipt, err := decodeConditionalTransactionV3Receipt(wire.Receipt, request.requestID)
	if err != nil {
		return ConditionalTransactionV3Result{}, err
	}
	if wire.Revision.Value != receipt.Revision || receipt.CommandSHA256 != request.commandSHA {
		return ConditionalTransactionV3Result{}, protocolError("decode conditional transaction v3", fmt.Errorf("top-level revision or command hash differs from receipt"))
	}
	resultKind := ConditionalTransactionResultKind(wire.Result)
	original := ConditionalTransactionOutcome(wire.OriginalResult)
	if (resultKind != ConditionalResultApplied && resultKind != ConditionalResultDuplicate && resultKind != ConditionalResultConflict) || (original != ConditionalOutcomeApplied && original != ConditionalOutcomeConflict) {
		return ConditionalTransactionV3Result{}, protocolError("decode conditional transaction v3", fmt.Errorf("unknown result enum"))
	}
	if original != receipt.Outcome || *wire.Applied != (original == ConditionalOutcomeApplied) || *wire.Duplicate != (resultKind == ConditionalResultDuplicate) || (!*wire.Duplicate && string(resultKind) != string(original)) {
		return ConditionalTransactionV3Result{}, protocolError("decode conditional transaction v3", fmt.Errorf("result/original_result/applied/duplicate fields are inconsistent"))
	}
	if err := validateConditionalTransactionV3Receipt(receipt, request); err != nil {
		return ConditionalTransactionV3Result{}, protocolError("decode conditional transaction v3", err)
	}
	quorum, err := decodeOptionalConditionalV3QuorumReceipt(wire.QuorumReceipt, request.requestID, request.quorumCommandSHA, &receipt, receipt.Revision)
	if err != nil {
		return ConditionalTransactionV3Result{}, err
	}
	return ConditionalTransactionV3Result{Result: resultKind, OriginalResult: original, Applied: *wire.Applied, Rejected: !*wire.Applied, Duplicate: *wire.Duplicate, Revision: wire.Revision.Value, Receipt: receipt, QuorumReceipt: quorum}, nil
}

func decodeConditionalTransactionV3ReceiptLookup(data []byte, request builtConditionalV3Request) (ConditionalTransactionV3ReceiptLookup, error) {
	var wire wireConditionalLookup
	if err := decodeStrictJSON(data, &wire); err != nil {
		return ConditionalTransactionV3ReceiptLookup{}, protocolError("decode conditional transaction v3 receipt", err)
	}
	if wire.RequestID != request.requestID || len(wire.StreamReceipt) != 0 {
		return ConditionalTransactionV3ReceiptLookup{}, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("request identity or receipt family mismatch"))
	}
	switch wire.Result {
	case string(ConditionalReceiptIndeterminate):
		if wire.Revision.Present || len(wire.Receipt) != 0 {
			return ConditionalTransactionV3ReceiptLookup{}, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("indeterminate lookup included a receipt"))
		}
		quorum, err := decodeOptionalConditionalV3QuorumReceipt(wire.QuorumReceipt, request.requestID, request.quorumCommandSHA, nil, 0)
		if err != nil || quorum == nil {
			if err == nil {
				err = protocolError("decode conditional transaction v3 receipt", fmt.Errorf("indeterminate lookup omitted quorum receipt"))
			}
			return ConditionalTransactionV3ReceiptLookup{}, err
		}
		if quorum.ResultSHA256 != nil || quorum.Stage == QuorumReceiptApplied {
			return ConditionalTransactionV3ReceiptLookup{}, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("indeterminate lookup included an applied result"))
		}
		return ConditionalTransactionV3ReceiptLookup{Status: ConditionalReceiptIndeterminate, RequestID: request.requestID, QuorumReceipt: quorum}, nil
	case string(ConditionalReceiptFound):
		if !wire.Revision.Present || len(wire.Receipt) == 0 || bytes.Equal(wire.Receipt, []byte("null")) {
			return ConditionalTransactionV3ReceiptLookup{}, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("receipt lookup omitted receipt or revision"))
		}
		receipt, err := decodeConditionalTransactionV3Receipt(wire.Receipt, request.requestID)
		if err != nil {
			return ConditionalTransactionV3ReceiptLookup{}, err
		}
		if wire.Revision.Value != receipt.Revision || receipt.CommandSHA256 != request.commandSHA {
			return ConditionalTransactionV3ReceiptLookup{}, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("receipt identity differs from exact transaction request"))
		}
		if err := validateConditionalTransactionV3Receipt(receipt, request); err != nil {
			return ConditionalTransactionV3ReceiptLookup{}, protocolError("decode conditional transaction v3 receipt", err)
		}
		quorum, err := decodeOptionalConditionalV3QuorumReceipt(wire.QuorumReceipt, request.requestID, request.quorumCommandSHA, &receipt, receipt.Revision)
		if err != nil {
			return ConditionalTransactionV3ReceiptLookup{}, err
		}
		revision := receipt.Revision
		return ConditionalTransactionV3ReceiptLookup{Status: ConditionalReceiptFound, RequestID: request.requestID, Revision: &revision, Receipt: &receipt, QuorumReceipt: quorum}, nil
	default:
		return ConditionalTransactionV3ReceiptLookup{}, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("unknown receipt lookup result %q", wire.Result))
	}
}

func decodeConditionalTransactionV3Receipt(data []byte, requestID string) (ConditionalTransactionV3Receipt, error) {
	var wire wireConditionalV3Receipt
	if err := decodeStrictJSON(data, &wire); err != nil {
		return ConditionalTransactionV3Receipt{}, protocolError("decode conditional transaction v3 receipt", err)
	}
	if wire.Version != conditionalTransactionCompactVersion || wire.RequestID != requestID || !conditionalRequestIDPattern.MatchString(wire.RequestID) || !wire.Revision.Present || wire.Revision.Value == 0 || !shaPattern.MatchString(wire.CommandSHA256) || !shaPattern.MatchString(wire.ReceiptSHA256) || wire.Conditions == nil || len(wire.Mutations) == 0 || len(wire.Conditions) > maxConditionalItems || len(wire.Mutations) > maxConditionalItems {
		return ConditionalTransactionV3Receipt{}, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("receipt identity or required fields are invalid"))
	}
	if wire.Outcome != string(ConditionalOutcomeApplied) && wire.Outcome != string(ConditionalOutcomeConflict) {
		return ConditionalTransactionV3Receipt{}, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("unknown receipt outcome %q", wire.Outcome))
	}
	conflictWire, err := decodeOptionalWireConflict(wire.Conflict)
	if err != nil {
		return ConditionalTransactionV3Receipt{}, err
	}
	if (wire.Outcome == string(ConditionalOutcomeApplied)) != (conflictWire == nil) {
		return ConditionalTransactionV3Receipt{}, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("receipt outcome/conflict fields are inconsistent"))
	}
	conflict, err := decodeConditionalConflict(conflictWire, len(wire.Conditions), len(wire.Mutations))
	if err != nil {
		return ConditionalTransactionV3Receipt{}, err
	}
	conditions, err := decodeConditionalTransactionV3Observations(wire.Conditions, true)
	if err != nil {
		return ConditionalTransactionV3Receipt{}, err
	}
	mutations, err := decodeConditionalTransactionV3Observations(wire.Mutations, false)
	if err != nil {
		return ConditionalTransactionV3Receipt{}, err
	}
	if wire.Outcome == string(ConditionalOutcomeApplied) {
		for _, observation := range conditions {
			if observation.Matched == nil || !*observation.Matched {
				return ConditionalTransactionV3Receipt{}, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("applied receipt contains a rejected condition"))
			}
		}
	} else {
		firstRejected := -1
		for index, observation := range conditions {
			if !equalConditionalValueObservation(observation.Before, observation.After) {
				return ConditionalTransactionV3Receipt{}, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("conflict receipt changed condition observation %d", index))
			}
			if firstRejected == -1 && observation.Matched != nil && !*observation.Matched {
				firstRejected = index
			}
		}
		if firstRejected >= 0 {
			if conflict == nil || conflict.Kind != ConditionalConflictConditionFailed || conflict.Index != uint64(firstRejected) {
				return ConditionalTransactionV3Receipt{}, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("condition conflict does not identify the first rejected observation"))
			}
		} else if conflict != nil && conflict.Kind == ConditionalConflictConditionFailed {
			return ConditionalTransactionV3Receipt{}, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("condition conflict has no rejected observation"))
		}
	}
	receipt := ConditionalTransactionV3Receipt{Version: wire.Version, RequestID: wire.RequestID, CommandSHA256: wire.CommandSHA256, Revision: wire.Revision.Value, Outcome: ConditionalTransactionOutcome(wire.Outcome), Conflict: conflict, Conditions: conditions, Mutations: mutations, ReceiptSHA256: wire.ReceiptSHA256}
	expectedHash, err := conditionalTransactionV3ReceiptSHA(receipt)
	if err != nil || expectedHash != wire.ReceiptSHA256 {
		if err == nil {
			err = fmt.Errorf("receipt SHA-256 mismatch")
		}
		return ConditionalTransactionV3Receipt{}, protocolError("decode conditional transaction v3 receipt", err)
	}
	return receipt, nil
}

func decodeConditionalTransactionV3Observations(values []wireConditionalV3Observation, conditions bool) ([]ConditionalTransactionV3Observation, error) {
	result := make([]ConditionalTransactionV3Observation, len(values))
	for index, wire := range values {
		namespace, hasPrefix := cutConditionalNamespace(wire.Keyspace)
		if wire.Index == nil || *wire.Index != uint64(index) || !hasPrefix || !conditionalNamespacePattern.MatchString(namespace) || !wire.Key.Present || wire.Key.Null || len(wire.Key.Value) == 0 || len(wire.Key.Value) > maxConditionalKeyBytes || !isExactJSONNull(wire.Before) || !isExactJSONNull(wire.After) || wire.BeforeCompact == nil || wire.AfterCompact == nil {
			return nil, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("observation %d has invalid required fields", index))
		}
		before, err := decodeConditionalValueObservation(wire.BeforeCompact)
		if err != nil {
			return nil, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("observation %d before_compact: %w", index, err))
		}
		after, err := decodeConditionalValueObservation(wire.AfterCompact)
		if err != nil {
			return nil, protocolError("decode conditional transaction v3 receipt", fmt.Errorf("observation %d after_compact: %w", index, err))
		}
		matched, err := decodeOptionalBool(wire.Matched)
		if err != nil || (conditions && matched == nil) || (!conditions && matched != nil) {
			if err == nil {
				err = fmt.Errorf("observation %d matched field is invalid", index)
			}
			return nil, protocolError("decode conditional transaction v3 receipt", err)
		}
		beforeI64, err := decodeOptionalCanonicalInt64(wire.BeforeI64)
		if err != nil {
			return nil, protocolError("decode conditional transaction v3 receipt", err)
		}
		afterI64, err := decodeOptionalCanonicalInt64(wire.AfterI64)
		if err != nil || (beforeI64 == nil) != (afterI64 == nil) || (conditions && beforeI64 != nil) {
			if err == nil {
				err = fmt.Errorf("observation %d i64 fields are inconsistent", index)
			}
			return nil, protocolError("decode conditional transaction v3 receipt", err)
		}
		result[index] = ConditionalTransactionV3Observation{Index: uint64(index), Keyspace: wire.Keyspace, Key: append([]byte(nil), wire.Key.Value...), Before: before, After: after, Matched: matched, BeforeI64: beforeI64, AfterI64: afterI64}
	}
	return result, nil
}

func cutConditionalNamespace(keyspace string) (string, bool) {
	const prefix = "__user_conditional__."
	if len(keyspace) < len(prefix) || keyspace[:len(prefix)] != prefix {
		return "", false
	}
	return keyspace[len(prefix):], true
}

func isExactJSONNull(data json.RawMessage) bool {
	return bytes.Equal(data, []byte("null"))
}

func decodeConditionalValueObservation(wire *wireCompactValueObservation) (ConditionalValueObservation, error) {
	if wire.Present == nil || wire.ByteLength == nil || *wire.ByteLength > compactConditionalMaxValueBytes {
		return ConditionalValueObservation{}, fmt.Errorf("present/byte_length is missing or out of range")
	}
	if !*wire.Present {
		if *wire.ByteLength != 0 || len(wire.SHA256) != 0 {
			return ConditionalValueObservation{}, fmt.Errorf("absent value included length or SHA-256")
		}
		return ConditionalValueObservation{}, nil
	}
	var digest string
	if len(wire.SHA256) == 0 || decodeStrictJSON(wire.SHA256, &digest) != nil || !shaPattern.MatchString(digest) {
		return ConditionalValueObservation{}, fmt.Errorf("present value omitted a valid SHA-256")
	}
	return ConditionalValueObservation{Present: true, ByteLength: *wire.ByteLength, SHA256: &digest}, nil
}

func conditionalValueObservation(value []byte) ConditionalValueObservation {
	if value == nil {
		return ConditionalValueObservation{}
	}
	digest := sha256.Sum256(value)
	encoded := hex.EncodeToString(digest[:])
	return ConditionalValueObservation{Present: true, ByteLength: uint64(len(value)), SHA256: &encoded}
}

func equalConditionalValueObservation(left, right ConditionalValueObservation) bool {
	if left.Present != right.Present || left.ByteLength != right.ByteLength || (left.SHA256 == nil) != (right.SHA256 == nil) {
		return false
	}
	return left.SHA256 == nil || *left.SHA256 == *right.SHA256
}

func conditionalValueObservationMatchesI64(value ConditionalValueObservation, number int64, allowAbsentZero bool) bool {
	if allowAbsentZero && number == 0 && !value.Present {
		return true
	}
	bytes := make([]byte, 8)
	binary.BigEndian.PutUint64(bytes, uint64(number))
	return equalConditionalValueObservation(value, conditionalValueObservation(bytes))
}

func validateConditionalTransactionV3Receipt(receipt ConditionalTransactionV3Receipt, request builtConditionalV3Request) error {
	if receipt.Version != conditionalTransactionCompactVersion || receipt.RequestID != request.requestID || receipt.CommandSHA256 != request.commandSHA || len(receipt.Conditions) != len(request.conditions) || len(receipt.Mutations) != len(request.mutations) {
		return fmt.Errorf("receipt identity, command, or observation counts differ from request")
	}
	keyspace := "__user_conditional__." + request.namespace
	mutationObservations := make(map[string]ConditionalTransactionV3Observation, len(receipt.Mutations))
	var firstCounterConflict *ConditionalTransactionConflict
	for index, observation := range receipt.Mutations {
		mutation := request.mutations[index]
		if observation.Index != uint64(index) || observation.Keyspace != keyspace || !bytes.Equal(observation.Key, mutation.key) || observation.Matched != nil {
			return fmt.Errorf("mutation observation %d differs from request", index)
		}
		if mutation.kind != conditionalIncrement && (observation.BeforeI64 != nil || observation.AfterI64 != nil) {
			return fmt.Errorf("non-increment mutation %d included i64 fields", index)
		}
		if receipt.Outcome == ConditionalOutcomeConflict {
			if !equalConditionalValueObservation(observation.Before, observation.After) {
				return fmt.Errorf("rejected mutation %d changed state", index)
			}
			if mutation.kind == conditionalIncrement {
				var conflictKind ConditionalConflictKind
				if observation.BeforeI64 == nil {
					if !observation.Before.Present || observation.Before.ByteLength == 8 || observation.AfterI64 != nil {
						return fmt.Errorf("rejected increment %d has false invalid-counter evidence", index)
					}
					conflictKind = ConditionalConflictCounterInvalid
				} else {
					if observation.AfterI64 == nil || *observation.BeforeI64 != *observation.AfterI64 || !conditionalValueObservationMatchesI64(observation.Before, *observation.BeforeI64, true) || !conditionalValueObservationMatchesI64(observation.After, *observation.AfterI64, true) {
						return fmt.Errorf("rejected increment %d has false numeric evidence", index)
					}
					if _, overflow := addInt64(*observation.BeforeI64, mutation.delta); overflow {
						if mutation.delta < 0 {
							conflictKind = ConditionalConflictCounterUnderflow
						} else {
							conflictKind = ConditionalConflictCounterOverflow
						}
					}
				}
				if conflictKind != "" && firstCounterConflict == nil {
					firstCounterConflict = &ConditionalTransactionConflict{Kind: conflictKind, Index: uint64(index)}
				}
			}
		} else {
			switch mutation.kind {
			case conditionalPut:
				if !equalConditionalValueObservation(observation.After, conditionalValueObservation(mutation.value)) {
					return fmt.Errorf("put mutation %d after value digest is invalid", index)
				}
			case conditionalDelete:
				if observation.After.Present {
					return fmt.Errorf("delete mutation %d after value is not absent", index)
				}
			case conditionalIncrement:
				if observation.BeforeI64 == nil || observation.AfterI64 == nil || !conditionalValueObservationMatchesI64(observation.Before, *observation.BeforeI64, true) || !conditionalValueObservationMatchesI64(observation.After, *observation.AfterI64, false) {
					return fmt.Errorf("increment mutation %d evidence is invalid", index)
				}
				expected, overflow := addInt64(*observation.BeforeI64, mutation.delta)
				if overflow || expected != *observation.AfterI64 {
					return fmt.Errorf("increment mutation %d delta evidence is invalid", index)
				}
			}
		}
		mutationObservations[string(observation.Key)] = observation
	}
	var firstConditionConflict *ConditionalTransactionConflict
	for index, observation := range receipt.Conditions {
		condition := request.conditions[index]
		if observation.Index != uint64(index) || observation.Keyspace != keyspace || !bytes.Equal(observation.Key, condition.Key) || observation.Matched == nil || observation.BeforeI64 != nil || observation.AfterI64 != nil {
			return fmt.Errorf("condition observation %d differs from request", index)
		}
		equal := equalConditionalValueObservation(observation.Before, conditionalValueObservation(condition.Expected))
		matched := equal
		if condition.Operator == CompareNotEqual {
			matched = !equal
		} else if condition.Operator != CompareEqual {
			return fmt.Errorf("condition observation %d uses an unverifiable compact operator", index)
		}
		if *observation.Matched != matched {
			return fmt.Errorf("condition observation %d matched flag is false evidence", index)
		}
		if !matched && firstConditionConflict == nil {
			firstConditionConflict = &ConditionalTransactionConflict{Kind: ConditionalConflictConditionFailed, Index: uint64(index)}
		}
		expectedAfter := observation.Before
		if receipt.Outcome == ConditionalOutcomeApplied {
			if mutation, ok := mutationObservations[string(observation.Key)]; ok {
				expectedAfter = mutation.After
			}
		}
		if !equalConditionalValueObservation(observation.After, expectedAfter) {
			return fmt.Errorf("condition observation %d final state is inconsistent", index)
		}
	}
	expectedConflict := firstConditionConflict
	if expectedConflict == nil {
		expectedConflict = firstCounterConflict
	}
	if (receipt.Conflict == nil) != (expectedConflict == nil) || (receipt.Conflict != nil && (receipt.Conflict.Kind != expectedConflict.Kind || receipt.Conflict.Index != expectedConflict.Index)) {
		return fmt.Errorf("receipt conflict does not identify the first deterministic failure")
	}
	return nil
}

func conditionalTransactionV3ReceiptSHA(receipt ConditionalTransactionV3Receipt) (string, error) {
	data, err := conditionalTransactionV3ReceiptCanonicalJSON(receipt, "")
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func conditionalTransactionV3ReceiptCanonicalJSON(receipt ConditionalTransactionV3Receipt, receiptSHA string) ([]byte, error) {
	valueWire := func(value ConditionalValueObservation) map[string]interface{} {
		wire := map[string]interface{}{"present": value.Present, "byte_length": value.ByteLength}
		if value.SHA256 != nil {
			wire["sha256"] = *value.SHA256
		}
		return wire
	}
	observationWire := func(observation ConditionalTransactionV3Observation) map[string]interface{} {
		wire := map[string]interface{}{
			"index":          observation.Index,
			"keyspace":       observation.Keyspace,
			"key":            presentWireBytes(observation.Key),
			"before":         nil,
			"after":          nil,
			"before_compact": valueWire(observation.Before),
			"after_compact":  valueWire(observation.After),
		}
		if observation.Matched != nil {
			wire["matched"] = *observation.Matched
		}
		if observation.BeforeI64 != nil {
			wire["before_i64"] = strconv.FormatInt(*observation.BeforeI64, 10)
		}
		if observation.AfterI64 != nil {
			wire["after_i64"] = strconv.FormatInt(*observation.AfterI64, 10)
		}
		return wire
	}
	conditions := make([]map[string]interface{}, len(receipt.Conditions))
	for index, observation := range receipt.Conditions {
		conditions[index] = observationWire(observation)
	}
	mutations := make([]map[string]interface{}, len(receipt.Mutations))
	for index, observation := range receipt.Mutations {
		mutations[index] = observationWire(observation)
	}
	canonical := map[string]interface{}{
		"version":        receipt.Version,
		"request_id":     receipt.RequestID,
		"command_sha256": receipt.CommandSHA256,
		"revision":       strconv.FormatUint(receipt.Revision, 10),
		"outcome":        string(receipt.Outcome),
		"conditions":     conditions,
		"mutations":      mutations,
		"receipt_sha256": receiptSHA,
	}
	if receipt.Conflict != nil {
		canonical["conflict"] = map[string]interface{}{"kind": string(receipt.Conflict.Kind), "index": receipt.Conflict.Index}
	}
	return marshalWithoutHTMLEscape(canonical)
}

func decodeOptionalConditionalV3QuorumReceipt(data json.RawMessage, requestID, commandSHA string, receipt *ConditionalTransactionV3Receipt, revision uint64) (*ConditionalQuorumReceipt, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if bytes.Equal(data, []byte("null")) {
		return nil, protocolError("decode conditional transaction v3 quorum receipt", fmt.Errorf("quorum_receipt must be omitted, not null"))
	}
	var wire wireQuorumReceipt
	if err := decodeStrictJSON(data, &wire); err != nil {
		return nil, protocolError("decode conditional transaction v3 quorum receipt", err)
	}
	if wire.Version != 1 || wire.RequestID != requestID || wire.CommandSHA256 != commandSHA || !wire.Index.Present || wire.Index.Value == 0 || !wire.Term.Present || !shaPattern.MatchString(wire.CommandSHA256) {
		return nil, protocolError("decode conditional transaction v3 quorum receipt", fmt.Errorf("quorum receipt identity is invalid"))
	}
	stage := QuorumReceiptStage(wire.Stage)
	if stage != QuorumReceiptAccepted && stage != QuorumReceiptCommitted && stage != QuorumReceiptApplied {
		return nil, protocolError("decode conditional transaction v3 quorum receipt", fmt.Errorf("unknown quorum stage %q", wire.Stage))
	}
	durability := QuorumDurability(wire.Durability)
	if durability != QuorumDurabilityNone && durability != QuorumDurabilityLocalFsync && durability != QuorumDurabilityQuorumFsync {
		return nil, protocolError("decode conditional transaction v3 quorum receipt", fmt.Errorf("unknown quorum durability %q", wire.Durability))
	}
	if (stage == QuorumReceiptAccepted && durability != QuorumDurabilityLocalFsync) || (stage != QuorumReceiptAccepted && durability != QuorumDurabilityQuorumFsync) {
		return nil, protocolError("decode conditional transaction v3 quorum receipt", fmt.Errorf("quorum stage/durability fields are inconsistent"))
	}
	resultSHA, err := decodeNullableSHA256(wire.ResultSHA256)
	if err != nil {
		return nil, protocolError("decode conditional transaction v3 quorum receipt", err)
	}
	if receipt != nil {
		if wire.Index.Value != revision || stage != QuorumReceiptApplied || durability != QuorumDurabilityQuorumFsync || resultSHA == nil {
			return nil, protocolError("decode conditional transaction v3 quorum receipt", fmt.Errorf("applied quorum receipt is inconsistent with transaction receipt"))
		}
		encoded, err := conditionalTransactionV3ReceiptCanonicalJSON(*receipt, receipt.ReceiptSHA256)
		if err != nil {
			return nil, protocolError("decode conditional transaction v3 quorum receipt", err)
		}
		digest := sha256.Sum256(encoded)
		if hex.EncodeToString(digest[:]) != *resultSHA {
			return nil, protocolError("decode conditional transaction v3 quorum receipt", fmt.Errorf("quorum result SHA-256 differs from compact receipt"))
		}
	}
	return &ConditionalQuorumReceipt{Version: wire.Version, RequestID: wire.RequestID, Index: wire.Index.Value, Term: wire.Term.Value, Stage: stage, Durability: durability, CommandSHA256: wire.CommandSHA256, ResultSHA256: cloneStringPointer(resultSHA)}, nil
}
