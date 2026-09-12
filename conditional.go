/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package talon

import (
	serverprotocol "github.com/darkmice/talon-sdk-go/internal/serverprotocol"
	serverapi "github.com/darkmice/talon-sdk-go/server"
)

type ConditionalCompareOperator = serverapi.ConditionalCompareOperator

const (
	CompareEqual          = serverapi.CompareEqual
	CompareNotEqual       = serverapi.CompareNotEqual
	CompareLessThan       = serverapi.CompareLessThan
	CompareLessOrEqual    = serverapi.CompareLessOrEqual
	CompareGreaterThan    = serverapi.CompareGreaterThan
	CompareGreaterOrEqual = serverapi.CompareGreaterOrEqual
)

type ConditionalTransactionCondition = serverapi.ConditionalTransactionCondition
type ConditionalTransactionMutation = serverapi.ConditionalTransactionMutation
type ConditionalTransactionRequest = serverapi.ConditionalTransactionRequest
type ConditionalTransactionOutcome = serverapi.ConditionalTransactionOutcome

const (
	ConditionalOutcomeApplied  = serverapi.ConditionalOutcomeApplied
	ConditionalOutcomeConflict = serverapi.ConditionalOutcomeConflict
)

type ConditionalTransactionResultKind = serverapi.ConditionalTransactionResultKind

const (
	ConditionalResultApplied   = serverapi.ConditionalResultApplied
	ConditionalResultDuplicate = serverapi.ConditionalResultDuplicate
	ConditionalResultConflict  = serverapi.ConditionalResultConflict
)

type ConditionalConflictKind = serverapi.ConditionalConflictKind

const (
	ConditionalConflictConditionFailed  = serverapi.ConditionalConflictConditionFailed
	ConditionalConflictCounterInvalid   = serverapi.ConditionalConflictCounterInvalid
	ConditionalConflictCounterOverflow  = serverapi.ConditionalConflictCounterOverflow
	ConditionalConflictCounterUnderflow = serverapi.ConditionalConflictCounterUnderflow
)

type ConditionalTransactionConflict = serverapi.ConditionalTransactionConflict
type ConditionalTransactionObservation = serverapi.ConditionalTransactionObservation
type ConditionalTransactionReceipt = serverapi.ConditionalTransactionReceipt
type QuorumReceiptStage = serverapi.QuorumReceiptStage

const (
	QuorumReceiptAccepted  = serverapi.QuorumReceiptAccepted
	QuorumReceiptCommitted = serverapi.QuorumReceiptCommitted
	QuorumReceiptApplied   = serverapi.QuorumReceiptApplied
)

type QuorumDurability = serverapi.QuorumDurability

const (
	QuorumDurabilityNone        = serverapi.QuorumDurabilityNone
	QuorumDurabilityLocalFsync  = serverapi.QuorumDurabilityLocalFsync
	QuorumDurabilityQuorumFsync = serverapi.QuorumDurabilityQuorumFsync
)

type ConditionalQuorumReceipt = serverapi.ConditionalQuorumReceipt
type ConditionalTransactionResult = serverapi.ConditionalTransactionResult
type ConditionalReceiptStatus = serverapi.ConditionalReceiptStatus

const (
	ConditionalReceiptFound         = serverapi.ConditionalReceiptFound
	ConditionalReceiptIndeterminate = serverapi.ConditionalReceiptIndeterminate
)

type ConditionalReceiptLookup = serverapi.ConditionalReceiptLookup

func ConditionalPut(key, value []byte) ConditionalTransactionMutation {
	return serverapi.ConditionalPut(key, value)
}
func ConditionalDelete(key []byte) ConditionalTransactionMutation {
	return serverapi.ConditionalDelete(key)
}
func ConditionalIncrement(key []byte, delta int64) ConditionalTransactionMutation {
	return serverapi.ConditionalIncrement(key, delta)
}
func NewConditionalTransactionRequest(namespace, requestID string, conditions []ConditionalTransactionCondition, mutations []ConditionalTransactionMutation) (ConditionalTransactionRequest, error) {
	return serverapi.NewConditionalTransactionRequest(namespace, requestID, conditions, mutations)
}

// ConditionalTransaction executes one durable, idempotent v2 transaction on
// the embedded DB. Wire validation and receipt decoding are shared with the
// cgo-free Server client through internal/serverprotocol.
func (db *DB) ConditionalTransaction(namespace, requestID string, conditions []ConditionalTransactionCondition, mutations []ConditionalTransactionMutation) (ConditionalTransactionResult, error) {
	request, err := NewConditionalTransactionRequest(namespace, requestID, conditions, mutations)
	if err != nil {
		return ConditionalTransactionResult{}, err
	}
	return db.ExecuteConditionalTransaction(request)
}

func (db *DB) ExecuteConditionalTransaction(request ConditionalTransactionRequest) (ConditionalTransactionResult, error) {
	params, err := serverprotocol.ConditionalTransactionParams(request)
	if err != nil {
		return ConditionalTransactionResult{}, err
	}
	if err := db.RequireCapability("native_conditional_transaction_v2"); err != nil {
		return ConditionalTransactionResult{}, err
	}
	data, err := db.execute("storage", "conditional_batch", params)
	if err != nil {
		return ConditionalTransactionResult{}, err
	}
	return serverprotocol.DecodeConditionalTransactionResult(data, request)
}

// ConditionalTransactionReceipt is a compatibility alias for
// ConditionalTransactionReceiptFor.
// Deprecated: use ConditionalTransactionReceiptFor.
func (db *DB) ConditionalTransactionReceipt(request ConditionalTransactionRequest) (ConditionalReceiptLookup, error) {
	return db.ConditionalTransactionReceiptFor(request)
}

func (db *DB) ConditionalTransactionReceiptFor(request ConditionalTransactionRequest) (ConditionalReceiptLookup, error) {
	if _, err := serverprotocol.ConditionalTransactionParams(request); err != nil {
		return ConditionalReceiptLookup{}, err
	}
	if err := db.RequireCapability("native_conditional_transaction_v2"); err != nil {
		return ConditionalReceiptLookup{}, err
	}
	data, err := db.execute("storage", "conditional_receipt", struct {
		RequestID string `json:"request_id"`
	}{RequestID: request.RequestID()})
	if err != nil {
		return ConditionalReceiptLookup{}, err
	}
	return serverprotocol.DecodeConditionalReceiptLookup(data, request)
}

// Private compatibility aliases below are used only by revision_stream.go.
// Their implementation remains owned by internal/serverprotocol.
type wireBytes = serverprotocol.WireBytes
type canonicalUint64 = serverprotocol.CanonicalUint64
type canonicalInt64 = serverprotocol.CanonicalInt64
type wireConditionalCondition = serverprotocol.WireConditionalCondition
type wireConditionalMutation = serverprotocol.WireConditionalMutation
type wireConditionalObservation = serverprotocol.WireConditionalObservation
type wireConditionalReceipt = serverprotocol.WireConditionalReceipt
type wireConditionalLookup = serverprotocol.WireConditionalLookup
type conditionalMutationKind = serverprotocol.ConditionalMutationKind

const (
	conditionalTransactionVersion    = serverprotocol.ConditionalTransactionVersion
	conditionalPointReadVersion      = serverprotocol.ConditionalPointReadVersion
	conditionalSnapshotReadVersion   = serverprotocol.ConditionalSnapshotReadVersion
	conditionalSnapshotReadVersionV2 = serverprotocol.ConditionalSnapshotReadVersionV2
	maxConditionalKeyBytes           = serverprotocol.MaxConditionalKeyBytes
	conditionalPut                   = serverprotocol.ConditionalMutationPut
	conditionalDelete                = serverprotocol.ConditionalMutationDelete
	conditionalIncrement             = serverprotocol.ConditionalMutationIncrement
)

type conditionalPattern func(string) bool

func (pattern conditionalPattern) MatchString(value string) bool { return pattern(value) }

var conditionalNamespacePattern = conditionalPattern(serverprotocol.ValidConditionalNamespace)
var conditionalRequestIDPattern = conditionalPattern(serverprotocol.ValidConditionalRequestID)

func presentWireBytes(value []byte) wireBytes  { return serverprotocol.PresentWireBytes(value) }
func nullableWireBytes(value []byte) wireBytes { return serverprotocol.NullableWireBytes(value) }
func validCompareOperator(value ConditionalCompareOperator) bool {
	return serverprotocol.ValidCompareOperator(value)
}
func nullableBytesEqual(left, right []byte) bool {
	return serverprotocol.NullableBytesEqual(left, right)
}
func addInt64(left, right int64) (int64, bool) { return serverprotocol.AddInt64(left, right) }
func bytesMatchI64(value []byte, expected int64) bool {
	return serverprotocol.BytesMatchI64(value, expected)
}
func conditionalValueMatches(actual, expected []byte, operator ConditionalCompareOperator) bool {
	return serverprotocol.ConditionalValueMatches(actual, expected, operator)
}
func decodeConditionalReceiptWithLimit(data []byte, requestID string, maxValueBytes int, allowRevisionStreamInternal bool) (*wireConditionalReceipt, ConditionalTransactionReceipt, error) {
	return serverprotocol.DecodeConditionalReceiptWithLimit(data, requestID, maxValueBytes, allowRevisionStreamInternal)
}
func decodeOptionalQuorumReceipt(data []byte, requestID, commandSHA string, conditional *wireConditionalReceipt, revision uint64) (*ConditionalQuorumReceipt, error) {
	return serverprotocol.DecodeOptionalQuorumReceipt(data, requestID, commandSHA, conditional, revision)
}
func conditionalReceiptSHA(receipt wireConditionalReceipt) (string, error) {
	return serverprotocol.ConditionalReceiptSHA(receipt)
}
func marshalWithoutHTMLEscape(value interface{}) ([]byte, error) {
	return serverprotocol.MarshalWithoutHTMLEscape(value)
}
