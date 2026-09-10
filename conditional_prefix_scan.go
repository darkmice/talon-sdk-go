package talon

import serverapi "github.com/darkmice/talon-sdk-go/server"

// These aliases keep the root-package ServerClient source compatible. New
// server-only applications should import github.com/darkmice/talon-sdk-go/server
// so the embedded DB and cgo files remain outside their package graph.
type ConditionalPrefixScanCursor = serverapi.ConditionalPrefixScanCursor
type ConditionalPrefixScanRequest = serverapi.ConditionalPrefixScanRequest
type ConditionalPrefixScanEntry = serverapi.ConditionalPrefixScanEntry
type ConditionalPrefixScanResult = serverapi.ConditionalPrefixScanResult

const (
	ConditionalPrefixScanVersion           = serverapi.ConditionalPrefixScanVersion
	ConditionalPrefixScanMaxEntries        = serverapi.ConditionalPrefixScanMaxEntries
	ConditionalPrefixScanMaxPrefixBytes    = serverapi.ConditionalPrefixScanMaxPrefixBytes
	ConditionalPrefixScanMaxKeyBytes       = serverapi.ConditionalPrefixScanMaxKeyBytes
	ConditionalPrefixScanMaxRequestBytes   = serverapi.ConditionalPrefixScanMaxRequestBytes
	ConditionalPrefixScanMaxValueBytes     = serverapi.ConditionalPrefixScanMaxValueBytes
	ConditionalPrefixScanMaxPageValueBytes = serverapi.ConditionalPrefixScanMaxPageValueBytes
	ConditionalPrefixScanMaxResponseBytes  = serverapi.ConditionalPrefixScanMaxResponseBytes
	ConditionalPrefixScanMaxActiveCursors  = serverapi.ConditionalPrefixScanMaxActiveCursors
	ConditionalPrefixScanCursorTTLSeconds  = serverapi.ConditionalPrefixScanCursorTTLSeconds
)

func ParseConditionalPrefixScanCursor(value string) (ConditionalPrefixScanCursor, error) {
	return serverapi.ParseConditionalPrefixScanCursor(value)
}

func NewConditionalPrefixScanRequest(requestID, namespace string, prefix []byte, limit uint16, requiredRevision *uint64) (ConditionalPrefixScanRequest, error) {
	return serverapi.NewConditionalPrefixScanRequest(requestID, namespace, prefix, limit, requiredRevision)
}
