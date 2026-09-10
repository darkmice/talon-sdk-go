package server

import protocol "github.com/darkmice/talon-sdk-go/internal/serverprotocol"

const ServerHTTPProtocolVersion = protocol.ServerHTTPProtocolVersion

type ServerTLSConfig = protocol.ServerTLSConfig
type ServerClientConfig = protocol.ServerClientConfig
type ServerHealth = protocol.ServerHealth
type ServerClient = protocol.ServerClient

type ErrorCode = protocol.ErrorCode

const (
	CodeInvalidArgument          = protocol.CodeInvalidArgument
	CodeDatabaseClosed           = protocol.CodeDatabaseClosed
	CodeNativeVerification       = protocol.CodeNativeVerification
	CodeNativeLoad               = protocol.CodeNativeLoad
	CodeProtocolViolation        = protocol.CodeProtocolViolation
	CodeNativeUnclassified       = protocol.CodeNativeUnclassified
	CodeCapabilityUnavailable    = protocol.CodeCapabilityUnavailable
	CodeResultIndeterminate      = protocol.CodeResultIndeterminate
	CodeNativeConflict           = protocol.CodeNativeConflict
	CodeNativeNotFound           = protocol.CodeNativeNotFound
	CodeNativeNotLeader          = protocol.CodeNativeNotLeader
	CodeNativeNoQuorum           = protocol.CodeNativeNoQuorum
	CodeNativeUnavailable        = protocol.CodeNativeUnavailable
	CodeNativeFenced             = protocol.CodeNativeFenced
	CodeNativeTimeout            = protocol.CodeNativeTimeout
	CodeNativePersistence        = protocol.CodeNativePersistence
	CodeSnapshotNotAvailable     = protocol.CodeSnapshotNotAvailable
	CodeCorruptStream            = protocol.CodeCorruptStream
	CodeNativeUnsupportedVersion = protocol.CodeNativeUnsupportedVersion
	CodeNativeInvalidNamespace   = protocol.CodeNativeInvalidNamespace
	CodeNativeInvalidRequest     = protocol.CodeNativeInvalidRequest
	CodeNativeRequestTooLarge    = protocol.CodeNativeRequestTooLarge
	CodeNativeResultTooLarge     = protocol.CodeNativeResultTooLarge
	CodeNativeCursorMismatch     = protocol.CodeNativeCursorMismatch
	CodeNativeCursorUnavailable  = protocol.CodeNativeCursorUnavailable
	CodeNativeResourceExhausted  = protocol.CodeNativeResourceExhausted
	CodeNativeCorruptState       = protocol.CodeNativeCorruptState
	CodeNativeStorage            = protocol.CodeNativeStorage
	CodeNativeUnsupportedAction  = protocol.CodeNativeUnsupportedAction
	CodeNativeNotConfigured      = protocol.CodeNativeNotConfigured
)

type TalonError = protocol.TalonError
type NativeLeaderHint = protocol.NativeLeaderHint

func ErrorCodeOf(err error) ErrorCode { return protocol.ErrorCodeOf(err) }
func NativeCodeOf(err error) string   { return protocol.NativeCodeOf(err) }

type ConditionalCompareOperator = protocol.ConditionalCompareOperator

const (
	CompareEqual          = protocol.CompareEqual
	CompareNotEqual       = protocol.CompareNotEqual
	CompareLessThan       = protocol.CompareLessThan
	CompareLessOrEqual    = protocol.CompareLessOrEqual
	CompareGreaterThan    = protocol.CompareGreaterThan
	CompareGreaterOrEqual = protocol.CompareGreaterOrEqual
)

type ConditionalTransactionCondition = protocol.ConditionalTransactionCondition
type ConditionalTransactionMutation = protocol.ConditionalTransactionMutation
type ConditionalTransactionRequest = protocol.ConditionalTransactionRequest
type ConditionalTransactionOutcome = protocol.ConditionalTransactionOutcome

const (
	ConditionalOutcomeApplied  = protocol.ConditionalOutcomeApplied
	ConditionalOutcomeConflict = protocol.ConditionalOutcomeConflict
)

type ConditionalTransactionResultKind = protocol.ConditionalTransactionResultKind

const (
	ConditionalResultApplied   = protocol.ConditionalResultApplied
	ConditionalResultDuplicate = protocol.ConditionalResultDuplicate
	ConditionalResultConflict  = protocol.ConditionalResultConflict
)

type ConditionalConflictKind = protocol.ConditionalConflictKind

const (
	ConditionalConflictConditionFailed  = protocol.ConditionalConflictConditionFailed
	ConditionalConflictCounterInvalid   = protocol.ConditionalConflictCounterInvalid
	ConditionalConflictCounterOverflow  = protocol.ConditionalConflictCounterOverflow
	ConditionalConflictCounterUnderflow = protocol.ConditionalConflictCounterUnderflow
)

type ConditionalTransactionConflict = protocol.ConditionalTransactionConflict
type ConditionalTransactionObservation = protocol.ConditionalTransactionObservation
type ConditionalTransactionReceipt = protocol.ConditionalTransactionReceipt
type QuorumReceiptStage = protocol.QuorumReceiptStage

const (
	QuorumReceiptAccepted  = protocol.QuorumReceiptAccepted
	QuorumReceiptCommitted = protocol.QuorumReceiptCommitted
	QuorumReceiptApplied   = protocol.QuorumReceiptApplied
)

type QuorumDurability = protocol.QuorumDurability

const (
	QuorumDurabilityNone        = protocol.QuorumDurabilityNone
	QuorumDurabilityLocalFsync  = protocol.QuorumDurabilityLocalFsync
	QuorumDurabilityQuorumFsync = protocol.QuorumDurabilityQuorumFsync
)

type ConditionalQuorumReceipt = protocol.ConditionalQuorumReceipt
type ConditionalTransactionResult = protocol.ConditionalTransactionResult
type ConditionalReceiptStatus = protocol.ConditionalReceiptStatus

const (
	ConditionalReceiptFound         = protocol.ConditionalReceiptFound
	ConditionalReceiptIndeterminate = protocol.ConditionalReceiptIndeterminate
)

type ConditionalReceiptLookup = protocol.ConditionalReceiptLookup
type ConditionalPointReadRequest = protocol.ConditionalPointReadRequest
type ConditionalPointReadResult = protocol.ConditionalPointReadResult
type ConditionalSnapshotReadRequest = protocol.ConditionalSnapshotReadRequest
type ConditionalSnapshotReadObservation = protocol.ConditionalSnapshotReadObservation
type ConditionalSnapshotReadResult = protocol.ConditionalSnapshotReadResult
type ConditionalPrefixScanCursor = protocol.ConditionalPrefixScanCursor
type ConditionalPrefixScanRequest = protocol.ConditionalPrefixScanRequest
type ConditionalPrefixScanEntry = protocol.ConditionalPrefixScanEntry
type ConditionalPrefixScanResult = protocol.ConditionalPrefixScanResult

const (
	ConditionalPrefixScanVersion           = 1
	ConditionalPrefixScanMaxEntries        = 256
	ConditionalPrefixScanMaxPrefixBytes    = 8 << 10
	ConditionalPrefixScanMaxKeyBytes       = 64 << 10
	ConditionalPrefixScanMaxRequestBytes   = 64 << 10
	ConditionalPrefixScanMaxValueBytes     = 3 << 20
	ConditionalPrefixScanMaxPageValueBytes = 8 << 20
	ConditionalPrefixScanMaxResponseBytes  = 16 << 20
	ConditionalPrefixScanMaxActiveCursors  = 4_096
	ConditionalPrefixScanCursorTTLSeconds  = 300
)

func NewServerClient(config ServerClientConfig) (*ServerClient, error) {
	return protocol.NewServerClient(config)
}
func ConditionalPut(key, value []byte) ConditionalTransactionMutation {
	return protocol.ConditionalPut(key, value)
}
func ConditionalDelete(key []byte) ConditionalTransactionMutation {
	return protocol.ConditionalDelete(key)
}
func ConditionalIncrement(key []byte, delta int64) ConditionalTransactionMutation {
	return protocol.ConditionalIncrement(key, delta)
}
func NewConditionalTransactionRequest(namespace, requestID string, conditions []ConditionalTransactionCondition, mutations []ConditionalTransactionMutation) (ConditionalTransactionRequest, error) {
	return protocol.NewConditionalTransactionRequest(namespace, requestID, conditions, mutations)
}
func NewConditionalPointReadRequest(namespace string, key []byte, requiredRevision *uint64) (ConditionalPointReadRequest, error) {
	return protocol.NewConditionalPointReadRequest(namespace, key, requiredRevision)
}
func NewConditionalSnapshotReadRequest(namespace string, keys [][]byte, requiredRevision *uint64) (ConditionalSnapshotReadRequest, error) {
	return protocol.NewConditionalSnapshotReadRequest(namespace, keys, requiredRevision)
}
func ParseConditionalPrefixScanCursor(value string) (ConditionalPrefixScanCursor, error) {
	return protocol.ParseConditionalPrefixScanCursor(value)
}
func NewConditionalPrefixScanRequest(requestID, namespace string, prefix []byte, limit uint16, requiredRevision *uint64) (ConditionalPrefixScanRequest, error) {
	return protocol.NewConditionalPrefixScanRequest(requestID, namespace, prefix, limit, requiredRevision)
}
