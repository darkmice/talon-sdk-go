package serverprotocol

// The exported names in this file are a narrow module-internal bridge used by
// the root embedded-DB compatibility package. They keep all conditional wire
// encoding, validation, hashing, and decoding in this package.

type WireBytes = wireBytes
type CanonicalUint64 = canonicalUint64
type CanonicalInt64 = canonicalInt64
type WireConditionalCondition = wireConditionalCondition
type WireConditionalMutation = wireConditionalMutation
type WireConditionalObservation = wireConditionalObservation
type WireConditionalReceipt = wireConditionalReceipt
type WireConditionalLookup = wireConditionalLookup
type ConditionalMutationKind = conditionalMutationKind

const (
	ConditionalTransactionVersion    = conditionalTransactionVersion
	ConditionalPointReadVersion      = conditionalPointReadVersion
	ConditionalSnapshotReadVersion   = conditionalSnapshotReadVersionV1
	ConditionalSnapshotReadVersionV2 = conditionalSnapshotReadVersionV2
	ConditionalSnapshotReadMaxKeys   = maxConditionalSnapshotReadKeysV1
	ConditionalSnapshotReadMaxKeysV2 = maxConditionalSnapshotReadKeysV2
	MaxConditionalKeyBytes           = maxConditionalKeyBytes
	ConditionalMutationPut           = conditionalPut
	ConditionalMutationDelete        = conditionalDelete
	ConditionalMutationIncrement     = conditionalIncrement
)

func (mutation ConditionalTransactionMutation) MutationKind() ConditionalMutationKind {
	return mutation.kind
}
func (mutation ConditionalTransactionMutation) Key() []byte {
	return append([]byte(nil), mutation.key...)
}
func (mutation ConditionalTransactionMutation) Value() []byte {
	return cloneOptionalByteSlice(mutation.value)
}
func (mutation ConditionalTransactionMutation) Delta() int64 { return mutation.delta }

func ConditionalTransactionParams(request ConditionalTransactionRequest) (interface{}, error) {
	built, err := buildClosedConditionalRequest(request)
	if err != nil {
		return nil, err
	}
	return built.wire, nil
}

func DecodeConditionalTransactionResult(data []byte, request ConditionalTransactionRequest) (ConditionalTransactionResult, error) {
	built, err := buildClosedConditionalRequest(request)
	if err != nil {
		return ConditionalTransactionResult{}, err
	}
	return decodeConditionalTransactionResult(data, built)
}

func DecodeConditionalReceiptLookup(data []byte, request ConditionalTransactionRequest) (ConditionalReceiptLookup, error) {
	built, err := buildClosedConditionalRequest(request)
	if err != nil {
		return ConditionalReceiptLookup{}, err
	}
	return decodeConditionalReceiptLookup(data, built)
}

func ConditionalPointReadParams(request ConditionalPointReadRequest) (interface{}, error) {
	return buildConditionalPointReadRequest(request)
}

func DecodeConditionalPointReadResult(data []byte, request ConditionalPointReadRequest) (ConditionalPointReadResult, error) {
	return decodeConditionalPointReadResult(data, request)
}

func ConditionalSnapshotReadParams(request ConditionalSnapshotReadRequest) (interface{}, error) {
	return buildConditionalSnapshotReadRequest(request)
}

func DecodeConditionalSnapshotReadResult(data []byte, request ConditionalSnapshotReadRequest) (ConditionalSnapshotReadResult, error) {
	return decodeConditionalSnapshotReadResult(data, request)
}

func PresentWireBytes(value []byte) WireBytes  { return presentWireBytes(value) }
func NullableWireBytes(value []byte) WireBytes { return nullableWireBytes(value) }
func DecodeStrictJSON(data []byte, destination interface{}) error {
	return decodeStrictJSON(data, destination)
}
func MarshalWithoutHTMLEscape(value interface{}) ([]byte, error) {
	return marshalWithoutHTMLEscape(value)
}
func ConditionalReceiptSHA(receipt WireConditionalReceipt) (string, error) {
	return conditionalReceiptSHA(receipt)
}
func DecodeConditionalReceiptWithLimit(data []byte, requestID string, maxValueBytes int, allowRevisionStreamInternal bool) (*WireConditionalReceipt, ConditionalTransactionReceipt, error) {
	return decodeConditionalReceiptWithLimit(data, requestID, maxValueBytes, allowRevisionStreamInternal)
}
func DecodeOptionalQuorumReceipt(data []byte, requestID, commandSHA string, conditional *WireConditionalReceipt, revision uint64) (*ConditionalQuorumReceipt, error) {
	return decodeOptionalQuorumReceipt(data, requestID, commandSHA, conditional, revision)
}
func ConditionalValueMatches(actual, expected []byte, operator ConditionalCompareOperator) bool {
	return conditionalValueMatches(actual, expected, operator)
}
func ValidConditionalNamespace(value string) bool {
	return conditionalNamespacePattern.MatchString(value)
}
func ValidConditionalRequestID(value string) bool {
	return conditionalRequestIDPattern.MatchString(value)
}
func ValidCompareOperator(value ConditionalCompareOperator) bool { return validCompareOperator(value) }
func NullableBytesEqual(left, right []byte) bool                 { return nullableBytesEqual(left, right) }
func AddInt64(left, right int64) (int64, bool)                   { return addInt64(left, right) }
func BytesMatchI64(value []byte, expected int64) bool            { return bytesMatchI64(value, expected) }
