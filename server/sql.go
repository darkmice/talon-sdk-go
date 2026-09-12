/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

// This file is the cgo-free Server package's SQL/query surface. It re-exports
// the versioned protocol types owned by internal/serverprotocol so Server-only
// applications never import the embedded DB package graph.
//
// DECIMAL is exact end to end: Decimal carries a big.Int coefficient and a
// retained scale, is always written as a canonical decimal string, and is never
// degraded to float64. The precision/scale bound is MaxDecimalPrecision (38),
// matching Core's DECIMAL_MAX_PRECISION and the embedded path.
//
// The surface is gated. ServerSQLCapability() reports "gated" because the
// cgo-free client has no signed artifact to read; a caller that has verified a
// released talon-bin artifact attests the surface supplies an exact release
// tag, talon-bin/Core commits, and artifact SHA-256 through
// ServerClientConfig.ServerSQLAttestation to open it for that client.

package server

import (
	"math/big"

	protocol "github.com/darkmice/talon-sdk-go/internal/serverprotocol"
)

const (
	// ServerSQLVersion is the versioned SQL payload version sent with every
	// query and required to be echoed by the remote Server.
	ServerSQLVersion = protocol.ServerSQLVersion
	// ServerSQLDecimalCapability is the capability that must be attested
	// before the Server SQL/DECIMAL surface may be treated as available.
	ServerSQLDecimalCapability = protocol.ServerSQLDecimalCapability
	// MaxDecimalPrecision is Core's DECIMAL precision/scale upper bound.
	MaxDecimalPrecision = protocol.MaxDecimalPrecision

	ServerSQLStatusGated     = protocol.ServerSQLStatusGated
	ServerSQLStatusAvailable = protocol.ServerSQLStatusAvailable
)

type ServerSQLGate = protocol.ServerSQLGate
type ServerSQLAttestation = protocol.ServerSQLAttestation

// ValueKind is the exact Talon wire type. Numeric kinds are never coerced.
type ValueKind = protocol.ValueKind

const (
	KindNull      = protocol.KindNull
	KindInteger   = protocol.KindInteger
	KindFloat     = protocol.KindFloat
	KindDecimal   = protocol.KindDecimal
	KindText      = protocol.KindText
	KindBlob      = protocol.KindBlob
	KindBoolean   = protocol.KindBoolean
	KindJSON      = protocol.KindJSON
	KindVector    = protocol.KindVector
	KindTimestamp = protocol.KindTimestamp
	KindGeoPoint  = protocol.KindGeoPoint
	KindDate      = protocol.KindDate
	KindTime      = protocol.KindTime
)

// Decimal is the exact fixed-point projection of a Talon DECIMAL value:
// Coefficient * 10^-Scale.
type Decimal = protocol.Decimal

// Value is a closed, typed Talon wire value.
type Value = protocol.Value

// GeoPoint is a WGS-84 latitude/longitude pair.
type GeoPoint = protocol.GeoPoint

// QueryRequest is a closed, versioned SQL request.
type QueryRequest = protocol.QueryRequest

// QueryResult is one verified, versioned SQL result.
type QueryResult = protocol.QueryResult

// ServerSQLCapability reports the static, un-attested gate state of the
// cgo-free Server SQL surface.
func ServerSQLCapability() ServerSQLGate { return protocol.ServerSQLCapability() }

func NewDecimal(coefficient *big.Int, scale uint8) (Decimal, error) {
	return protocol.NewDecimal(coefficient, scale)
}

func ParseDecimal(text string) (Decimal, error) { return protocol.ParseDecimal(text) }

func NewQueryRequest(sql string, params ...Value) (QueryRequest, error) {
	return protocol.NewQueryRequest(sql, params...)
}

func NullValue() Value                 { return protocol.NullValue() }
func IntegerValue(value int64) Value   { return protocol.IntegerValue(value) }
func BooleanValue(value bool) Value    { return protocol.BooleanValue(value) }
func TimestampValue(value int64) Value { return protocol.TimestampValue(value) }
func DateValue(value int32) Value      { return protocol.DateValue(value) }
func BlobValue(value []byte) Value     { return protocol.BlobValue(value) }
func DecimalValue(decimal Decimal) (Value, error) {
	return protocol.DecimalValue(decimal)
}
func FloatValue(value float64) (Value, error) { return protocol.FloatValue(value) }
func TextValue(value string) (Value, error)   { return protocol.TextValue(value) }
func JSONValue(value []byte) (Value, error)   { return protocol.JSONValue(value) }
func VectorValue(value []float32) (Value, error) {
	return protocol.VectorValue(value)
}
func GeoPointValue(latitude, longitude float64) (Value, error) {
	return protocol.GeoPointValue(latitude, longitude)
}
func TimeValue(nanosecondsSinceMidnight int64) (Value, error) {
	return protocol.TimeValue(nanosecondsSinceMidnight)
}
