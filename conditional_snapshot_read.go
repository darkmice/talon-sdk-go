package talon

import (
	serverprotocol "github.com/darkmice/talon-sdk-go/internal/serverprotocol"
	serverapi "github.com/darkmice/talon-sdk-go/server"
)

type ConditionalSnapshotReadRequest = serverapi.ConditionalSnapshotReadRequest
type ConditionalSnapshotReadObservation = serverapi.ConditionalSnapshotReadObservation
type ConditionalSnapshotReadResult = serverapi.ConditionalSnapshotReadResult

const (
	ConditionalSnapshotReadVersion   = serverapi.ConditionalSnapshotReadVersion
	ConditionalSnapshotReadVersionV2 = serverapi.ConditionalSnapshotReadVersionV2
	ConditionalSnapshotReadMaxKeys   = serverapi.ConditionalSnapshotReadMaxKeys
	ConditionalSnapshotReadMaxKeysV2 = serverapi.ConditionalSnapshotReadMaxKeysV2
)

func NewConditionalSnapshotReadRequest(namespace string, keys [][]byte, requiredRevision *uint64) (ConditionalSnapshotReadRequest, error) {
	return serverapi.NewConditionalSnapshotReadRequest(namespace, keys, requiredRevision)
}

func NewConditionalSnapshotReadRequestV2(namespace string, keys [][]byte, requiredRevision *uint64) (ConditionalSnapshotReadRequest, error) {
	return serverapi.NewConditionalSnapshotReadRequestV2(namespace, keys, requiredRevision)
}

func (db *DB) ConditionalSnapshotGet(request ConditionalSnapshotReadRequest) (ConditionalSnapshotReadResult, error) {
	params, err := serverprotocol.ConditionalSnapshotReadParams(request)
	if err != nil {
		return ConditionalSnapshotReadResult{}, err
	}
	if err := db.requireConditionalSnapshotReadVersion(request.Version()); err != nil {
		return ConditionalSnapshotReadResult{}, err
	}
	data, err := db.execute("storage", "conditional_snapshot_get", params)
	if err != nil {
		return ConditionalSnapshotReadResult{}, err
	}
	return serverprotocol.DecodeConditionalSnapshotReadResult(data, request)
}

// ConditionalSnapshotRead is a descriptive compatibility alias for
// ConditionalSnapshotGet.
// Deprecated: use ConditionalSnapshotGet.
func (db *DB) ConditionalSnapshotRead(request ConditionalSnapshotReadRequest) (ConditionalSnapshotReadResult, error) {
	return db.ConditionalSnapshotGet(request)
}
