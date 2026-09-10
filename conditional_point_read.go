package talon

import (
	serverprotocol "github.com/darkmice/talon-sdk-go/internal/serverprotocol"
	serverapi "github.com/darkmice/talon-sdk-go/server"
)

type ConditionalPointReadRequest = serverapi.ConditionalPointReadRequest
type ConditionalPointReadResult = serverapi.ConditionalPointReadResult

func NewConditionalPointReadRequest(namespace string, key []byte, requiredRevision *uint64) (ConditionalPointReadRequest, error) {
	return serverapi.NewConditionalPointReadRequest(namespace, key, requiredRevision)
}

// ConditionalPointRead is a compatibility alias for ConditionalGet.
// Deprecated: use ConditionalGet.
func (db *DB) ConditionalPointRead(request ConditionalPointReadRequest) (ConditionalPointReadResult, error) {
	return db.ConditionalGet(request)
}

func (db *DB) ConditionalGet(request ConditionalPointReadRequest) (ConditionalPointReadResult, error) {
	params, err := serverprotocol.ConditionalPointReadParams(request)
	if err != nil {
		return ConditionalPointReadResult{}, err
	}
	if err := db.RequireCapability("storage_conditional_point_read"); err != nil {
		return ConditionalPointReadResult{}, err
	}
	data, err := db.execute("storage", "conditional_get", params)
	if err != nil {
		return ConditionalPointReadResult{}, err
	}
	return serverprotocol.DecodeConditionalPointReadResult(data, request)
}
