/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package talon

/*
#include <stdlib.h>
#include "native_loader.h"
*/
import "C"

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"
	"unicode/utf8"
	"unsafe"
)

const (
	maxVectorDimensions  = 4096
	maxNativeResultBytes = 256 << 20
	// A revision-stream append may contain a 1 MiB event plus 4 MiB of side
	// bytes. JSON byte arrays expand to almost four bytes per input byte.
	maxNativeJSONRequestBytes = 32 << 20
	maxNativeJSONResultBytes  = 16 << 20
	maxWireItems              = 1 << 20
	maxWireCellBytes          = 1 << 20
	maxTimeNanos              = int64(86_399_999_999_999)
	maxDecimalPrecision       = 38
)

// ValueKind is the exact Talon wire type. Numeric kinds are never coerced.
type ValueKind uint8

const (
	KindNull ValueKind = iota
	KindInteger
	KindFloat
	KindText
	KindBlob
	KindBoolean
	KindJSON
	KindVector
	KindTimestamp
	KindGeoPoint
	KindDate
	KindTime
	KindDecimal
)

// GeoPoint is a WGS-84 latitude/longitude pair.
type GeoPoint struct {
	Latitude  float64
	Longitude float64
}

// Decimal is the exact Go projection of a Talon fixed-point value.
type Decimal struct {
	Coefficient *big.Int
	Scale       uint8
}

// Value is a closed, typed representation of the Core TLV Value enum. Its
// fields are private so callers cannot construct a kind/payload mismatch.
type Value struct {
	kind    ValueKind
	integer int64
	float   float64
	text    string
	bytes   []byte
	vector  []float32
	geo     GeoPoint
	date    int32
	decimal [16]byte
	scale   uint8
}

func NullValue() Value               { return Value{kind: KindNull} }
func IntegerValue(value int64) Value { return Value{kind: KindInteger, integer: value} }
func BooleanValue(value bool) Value {
	integer := int64(0)
	if value {
		integer = 1
	}
	return Value{kind: KindBoolean, integer: integer}
}
func TimestampValue(value int64) Value { return Value{kind: KindTimestamp, integer: value} }
func DateValue(value int32) Value      { return Value{kind: KindDate, date: value} }

func FloatValue(value float64) (Value, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return Value{}, newError(CodeInvalidArgument, "construct float value", "non-finite float is not a valid Talon wire value", nil)
	}
	return Value{kind: KindFloat, float: value}, nil
}

func TextValue(value string) (Value, error) {
	if !utf8.ValidString(value) {
		return Value{}, newError(CodeInvalidArgument, "construct text value", "text is not valid UTF-8", nil)
	}
	return Value{kind: KindText, text: value}, nil
}

func BlobValue(value []byte) Value {
	return Value{kind: KindBlob, bytes: append([]byte(nil), value...)}
}

func JSONValue(value []byte) (Value, error) {
	if len(value) == 0 {
		return Value{}, newError(CodeInvalidArgument, "construct JSON value", "JSON value is empty", nil)
	}
	var decoded interface{}
	if err := decodeStrictJSON(value, &decoded); err != nil {
		return Value{}, newError(CodeInvalidArgument, "construct JSON value", "JSON value is not strict JSON", err)
	}
	return Value{kind: KindJSON, bytes: append([]byte(nil), value...)}, nil
}

func VectorValue(value []float32) (Value, error) {
	if len(value) == 0 || len(value) > maxVectorDimensions {
		return Value{}, newError(CodeInvalidArgument, "construct vector value", "vector dimension must be in 1..4096", nil)
	}
	for _, item := range value {
		if math.IsNaN(float64(item)) || math.IsInf(float64(item), 0) {
			return Value{}, newError(CodeInvalidArgument, "construct vector value", "vector contains a non-finite value", nil)
		}
	}
	return Value{kind: KindVector, vector: append([]float32(nil), value...)}, nil
}

func GeoPointValue(latitude, longitude float64) (Value, error) {
	if math.IsNaN(latitude) || math.IsInf(latitude, 0) || math.IsNaN(longitude) || math.IsInf(longitude, 0) || latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
		return Value{}, newError(CodeInvalidArgument, "construct geospatial value", "latitude or longitude is outside WGS-84 bounds", nil)
	}
	return Value{kind: KindGeoPoint, geo: GeoPoint{Latitude: latitude, Longitude: longitude}}, nil
}

func TimeValue(nanosecondsSinceMidnight int64) (Value, error) {
	if nanosecondsSinceMidnight < 0 || nanosecondsSinceMidnight > maxTimeNanos {
		return Value{}, newError(CodeInvalidArgument, "construct time value", "time is outside one day in nanoseconds", nil)
	}
	return Value{kind: KindTime, integer: nanosecondsSinceMidnight}, nil
}

// DecimalValue constructs an exact fixed-point value represented as
// coefficient * 10^-scale. The coefficient and scale are limited to Core's
// DECIMAL precision of 38 digits.
func DecimalValue(coefficient *big.Int, scale uint8) (Value, error) {
	if coefficient == nil {
		return Value{}, newError(CodeInvalidArgument, "construct decimal value", "decimal coefficient is nil", nil)
	}
	if scale > maxDecimalPrecision || decimalPrecision(coefficient) > maxDecimalPrecision {
		return Value{}, newError(CodeInvalidArgument, "construct decimal value", "decimal precision or scale exceeds 38", nil)
	}
	encoded, ok := encodeI128LittleEndian(coefficient)
	if !ok {
		return Value{}, newError(CodeInvalidArgument, "construct decimal value", "decimal coefficient is outside signed i128", nil)
	}
	return Value{kind: KindDecimal, decimal: encoded, scale: scale}, nil
}

func (value Value) Kind() ValueKind { return value.kind }

func (value Value) Integer() (int64, bool) {
	if value.kind != KindInteger {
		return 0, false
	}
	return value.integer, true
}

func (value Value) Float64() (float64, bool) {
	if value.kind != KindFloat {
		return 0, false
	}
	return value.float, true
}

func (value Value) String() (string, bool) {
	if value.kind != KindText {
		return "", false
	}
	return value.text, true
}

func (value Value) Blob() ([]byte, bool) {
	if value.kind != KindBlob {
		return nil, false
	}
	return append([]byte(nil), value.bytes...), true
}

func (value Value) Boolean() (bool, bool) {
	if value.kind != KindBoolean {
		return false, false
	}
	return value.integer == 1, true
}

func (value Value) JSON() (json.RawMessage, bool) {
	if value.kind != KindJSON {
		return nil, false
	}
	return append(json.RawMessage(nil), value.bytes...), true
}

func (value Value) Vector() ([]float32, bool) {
	if value.kind != KindVector {
		return nil, false
	}
	return append([]float32(nil), value.vector...), true
}

func (value Value) Timestamp() (int64, bool) {
	if value.kind != KindTimestamp {
		return 0, false
	}
	return value.integer, true
}

func (value Value) GeoPoint() (GeoPoint, bool) {
	if value.kind != KindGeoPoint {
		return GeoPoint{}, false
	}
	return value.geo, true
}

func (value Value) Date() (int32, bool) {
	if value.kind != KindDate {
		return 0, false
	}
	return value.date, true
}

func (value Value) Time() (int64, bool) {
	if value.kind != KindTime {
		return 0, false
	}
	return value.integer, true
}

// Decimal returns a defensive coefficient copy and its retained scale.
func (value Value) Decimal() (*big.Int, uint8, bool) {
	if value.kind != KindDecimal {
		return nil, 0, false
	}
	return decodeI128LittleEndian(value.decimal[:]), value.scale, true
}

func (value Value) IsNull() bool { return value.kind == KindNull }

// GoValue returns a defensive, exact Go projection for compatibility APIs.
func (value Value) GoValue() interface{} {
	switch value.kind {
	case KindNull:
		return nil
	case KindInteger:
		return value.integer
	case KindFloat:
		return value.float
	case KindText:
		return value.text
	case KindBlob:
		result, _ := value.Blob()
		return result
	case KindBoolean:
		return value.integer == 1
	case KindJSON:
		result, _ := value.JSON()
		return result
	case KindVector:
		result, _ := value.Vector()
		return result
	case KindTimestamp, KindTime:
		return value.integer
	case KindGeoPoint:
		return value.geo
	case KindDate:
		return value.date
	case KindDecimal:
		coefficient, scale, _ := value.Decimal()
		return Decimal{Coefficient: coefficient, Scale: scale}
	default:
		return nil
	}
}

// Row is a row of exact typed Talon Values.
type Row []Value

func (row Row) Value(index int) (Value, bool) {
	if index < 0 || index >= len(row) {
		return Value{}, false
	}
	return row[index], true
}

func (row Row) Str(index int) string {
	value, ok := row.Value(index)
	if !ok {
		return ""
	}
	result, _ := value.String()
	return result
}

func (row Row) Int(index int) int64 {
	value, ok := row.Value(index)
	if !ok {
		return 0
	}
	result, _ := value.Integer()
	return result
}

func (row Row) Float(index int) float64 {
	value, ok := row.Value(index)
	if !ok {
		return 0
	}
	result, _ := value.Float64()
	return result
}

func (row Row) Bool(index int) bool {
	value, ok := row.Value(index)
	if !ok {
		return false
	}
	result, _ := value.Boolean()
	return result
}

func (row Row) Decimal(index int) (*big.Int, uint8, bool) {
	value, ok := row.Value(index)
	if !ok {
		return nil, 0, false
	}
	return value.Decimal()
}

func (row Row) IsNull(index int) bool {
	value, ok := row.Value(index)
	return !ok || value.IsNull()
}

// Query executes parameterized SQL over Core's binary TLV ABI. Parameters are
// closed Value instances; the SDK never interpolates SQL or guesses Go types.
func (db *DB) Query(sql string, params ...Value) ([]Row, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.handle == nil {
		return nil, newError(CodeDatabaseClosed, "query", "database is closed", nil)
	}
	if strings.TrimSpace(sql) == "" || strings.IndexByte(sql, 0) >= 0 || !utf8.ValidString(sql) {
		return nil, newError(CodeInvalidArgument, "query", "SQL is empty, contains NUL, or is not UTF-8", nil)
	}
	encoded, err := encodeParameters(params)
	if err != nil {
		return nil, err
	}
	csql := C.CString(sql)
	defer C.free(unsafe.Pointer(csql))
	var parameterPointer *C.uint8_t
	if len(encoded) > 0 {
		parameterPointer = (*C.uint8_t)(unsafe.Pointer(&encoded[0]))
	}
	var output *C.uint8_t
	var outputLength C.size_t
	var errorCode [128]C.char
	rc := C.talon_sdk_run_sql_param_bin(db.handle, csql, parameterPointer, C.size_t(len(encoded)), &output, &outputLength, &errorCode[0], C.size_t(len(errorCode)))
	if rc != 0 {
		return nil, nativeFailure("query", "native parameterized SQL failed", C.GoString(&errorCode[0]))
	}
	if output == nil || uint64(outputLength) > maxNativeResultBytes || uint64(outputLength) > math.MaxInt32 {
		if output != nil {
			C.talon_sdk_free_bytes(output, outputLength)
		}
		return nil, newError(CodeProtocolViolation, "query", "native binary result is null or exceeds the SDK bound", nil)
	}
	defer C.talon_sdk_free_bytes(output, outputLength)
	wire := C.GoBytes(unsafe.Pointer(output), C.int(outputLength))
	rows, err := decodeRows(wire)
	if err != nil {
		return nil, newError(CodeProtocolViolation, "query", "invalid native row/value wire response", err)
	}
	return rows, nil
}

// Exec executes a statement that must not return rows. Use Query for RETURNING.
func (db *DB) Exec(sql string, params ...Value) error {
	rows, err := db.Query(sql, params...)
	if err != nil {
		return err
	}
	if len(rows) != 0 {
		return newError(CodeProtocolViolation, "exec", "statement returned rows; use Query for result-bearing SQL", nil)
	}
	return nil
}

func encodeParameters(params []Value) ([]byte, error) {
	if uint64(len(params)) > math.MaxUint32 {
		return nil, newError(CodeInvalidArgument, "encode parameters", "too many parameters", nil)
	}
	buffer := make([]byte, 4)
	binary.LittleEndian.PutUint32(buffer, uint32(len(params)))
	for index, value := range params {
		var err error
		buffer, err = appendWireValue(buffer, value)
		if err != nil {
			return nil, newError(CodeInvalidArgument, "encode parameters", fmt.Sprintf("parameter %d is invalid", index), err)
		}
	}
	return buffer, nil
}

func appendWireValue(buffer []byte, value Value) ([]byte, error) {
	buffer = append(buffer, byte(value.kind))
	switch value.kind {
	case KindNull:
		return buffer, nil
	case KindInteger, KindTimestamp, KindTime:
		if value.kind == KindTime && (value.integer < 0 || value.integer > maxTimeNanos) {
			return nil, fmt.Errorf("time payload is outside one day")
		}
		return binary.LittleEndian.AppendUint64(buffer, uint64(value.integer)), nil
	case KindFloat:
		if math.IsNaN(value.float) || math.IsInf(value.float, 0) {
			return nil, fmt.Errorf("non-finite float payload")
		}
		return binary.LittleEndian.AppendUint64(buffer, math.Float64bits(value.float)), nil
	case KindText:
		if !utf8.ValidString(value.text) {
			return nil, fmt.Errorf("invalid UTF-8 text payload")
		}
		return appendLengthBytes(buffer, []byte(value.text))
	case KindBlob:
		return appendLengthBytes(buffer, value.bytes)
	case KindBoolean:
		if value.integer != 0 && value.integer != 1 {
			return nil, fmt.Errorf("invalid boolean payload")
		}
		return append(buffer, byte(value.integer)), nil
	case KindJSON:
		var decoded interface{}
		if err := decodeStrictJSON(value.bytes, &decoded); err != nil {
			return nil, fmt.Errorf("invalid strict JSON payload: %w", err)
		}
		return appendLengthBytes(buffer, value.bytes)
	case KindVector:
		if len(value.vector) == 0 || len(value.vector) > maxVectorDimensions {
			return nil, fmt.Errorf("invalid vector dimension")
		}
		buffer = binary.LittleEndian.AppendUint32(buffer, uint32(len(value.vector)))
		for _, item := range value.vector {
			if math.IsNaN(float64(item)) || math.IsInf(float64(item), 0) {
				return nil, fmt.Errorf("non-finite vector payload")
			}
			buffer = binary.LittleEndian.AppendUint32(buffer, math.Float32bits(item))
		}
		return buffer, nil
	case KindGeoPoint:
		latitude, longitude := value.geo.Latitude, value.geo.Longitude
		if math.IsNaN(latitude) || math.IsInf(latitude, 0) || math.IsNaN(longitude) || math.IsInf(longitude, 0) || latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
			return nil, fmt.Errorf("invalid WGS-84 point")
		}
		buffer = binary.LittleEndian.AppendUint64(buffer, math.Float64bits(latitude))
		return binary.LittleEndian.AppendUint64(buffer, math.Float64bits(longitude)), nil
	case KindDate:
		return binary.LittleEndian.AppendUint32(buffer, uint32(value.date)), nil
	case KindDecimal:
		coefficient := decodeI128LittleEndian(value.decimal[:])
		if value.scale > maxDecimalPrecision || decimalPrecision(coefficient) > maxDecimalPrecision {
			return nil, fmt.Errorf("invalid decimal precision or scale")
		}
		buffer = append(buffer, value.decimal[:]...)
		return append(buffer, value.scale), nil
	default:
		return nil, fmt.Errorf("unknown Value kind %d", value.kind)
	}
}

func appendLengthBytes(buffer, value []byte) ([]byte, error) {
	if uint64(len(value)) > math.MaxUint32 {
		return nil, fmt.Errorf("payload exceeds u32 wire length")
	}
	buffer = binary.LittleEndian.AppendUint32(buffer, uint32(len(value)))
	return append(buffer, value...), nil
}

func decodeRows(wire []byte) ([]Row, error) {
	if len(wire) < 8 {
		return nil, fmt.Errorf("row header is truncated")
	}
	rowCount := uint64(binary.LittleEndian.Uint32(wire[:4]))
	columnCount := uint64(binary.LittleEndian.Uint32(wire[4:8]))
	cellCount := rowCount * columnCount
	if rowCount > maxWireItems || columnCount > maxWireItems || (rowCount != 0 && columnCount > maxWireItems/rowCount) || cellCount > uint64(len(wire)-8) {
		return nil, fmt.Errorf("row or column count exceeds SDK bounds")
	}
	if rowCount == 0 && columnCount != 0 {
		return nil, fmt.Errorf("empty row set has a non-zero column count")
	}
	position := 8
	rows := make([]Row, int(rowCount))
	for rowIndex := range rows {
		rows[rowIndex] = make(Row, int(columnCount))
		for columnIndex := range rows[rowIndex] {
			value, next, err := decodeWireValue(wire, position)
			if err != nil {
				return nil, fmt.Errorf("row %d column %d: %w", rowIndex, columnIndex, err)
			}
			rows[rowIndex][columnIndex] = value
			position = next
		}
	}
	if position != len(wire) {
		return nil, fmt.Errorf("row payload has %d trailing bytes", len(wire)-position)
	}
	return rows, nil
}

func decodeWireValue(wire []byte, position int) (Value, int, error) {
	if position >= len(wire) {
		return Value{}, position, fmt.Errorf("missing type tag")
	}
	kind := ValueKind(wire[position])
	position++
	require := func(length int) ([]byte, error) {
		if length < 0 || position > len(wire)-length {
			return nil, fmt.Errorf("truncated type %d payload", kind)
		}
		value := wire[position : position+length]
		position += length
		return value, nil
	}
	switch kind {
	case KindNull:
		return NullValue(), position, nil
	case KindInteger, KindTimestamp, KindTime:
		payload, err := require(8)
		if err != nil {
			return Value{}, position, err
		}
		value := int64(binary.LittleEndian.Uint64(payload))
		if kind == KindTime && (value < 0 || value > maxTimeNanos) {
			return Value{}, position, fmt.Errorf("time payload is outside one day")
		}
		return Value{kind: kind, integer: value}, position, nil
	case KindFloat:
		payload, err := require(8)
		if err != nil {
			return Value{}, position, err
		}
		value := math.Float64frombits(binary.LittleEndian.Uint64(payload))
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return Value{}, position, fmt.Errorf("non-finite float payload")
		}
		return Value{kind: kind, float: value}, position, nil
	case KindText, KindBlob, KindJSON:
		lengthBytes, err := require(4)
		if err != nil {
			return Value{}, position, err
		}
		length := uint64(binary.LittleEndian.Uint32(lengthBytes))
		if length > maxWireCellBytes || length > uint64(len(wire)-position) {
			return Value{}, position, fmt.Errorf("declared payload length exceeds result")
		}
		payload, err := require(int(length))
		if err != nil {
			return Value{}, position, err
		}
		if kind == KindText {
			if !utf8.Valid(payload) {
				return Value{}, position, fmt.Errorf("text payload is not UTF-8")
			}
			return Value{kind: kind, text: string(payload)}, position, nil
		}
		if kind == KindJSON {
			var decoded interface{}
			if err := decodeStrictJSON(payload, &decoded); err != nil {
				return Value{}, position, fmt.Errorf("JSON payload is not strict JSON: %w", err)
			}
		}
		return Value{kind: kind, bytes: append([]byte(nil), payload...)}, position, nil
	case KindBoolean:
		payload, err := require(1)
		if err != nil {
			return Value{}, position, err
		}
		if payload[0] != 0 && payload[0] != 1 {
			return Value{}, position, fmt.Errorf("boolean payload must be 0 or 1")
		}
		return Value{kind: kind, integer: int64(payload[0])}, position, nil
	case KindVector:
		dimensionBytes, err := require(4)
		if err != nil {
			return Value{}, position, err
		}
		dimension := uint64(binary.LittleEndian.Uint32(dimensionBytes))
		if dimension == 0 || dimension > maxVectorDimensions || dimension > uint64((len(wire)-position)/4) {
			return Value{}, position, fmt.Errorf("invalid vector dimension")
		}
		vector := make([]float32, int(dimension))
		for index := range vector {
			payload, err := require(4)
			if err != nil {
				return Value{}, position, err
			}
			value := math.Float32frombits(binary.LittleEndian.Uint32(payload))
			if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
				return Value{}, position, fmt.Errorf("vector contains a non-finite value")
			}
			vector[index] = value
		}
		return Value{kind: kind, vector: vector}, position, nil
	case KindGeoPoint:
		payload, err := require(16)
		if err != nil {
			return Value{}, position, err
		}
		latitude := math.Float64frombits(binary.LittleEndian.Uint64(payload[:8]))
		longitude := math.Float64frombits(binary.LittleEndian.Uint64(payload[8:]))
		value, err := GeoPointValue(latitude, longitude)
		if err != nil {
			return Value{}, position, err
		}
		return value, position, nil
	case KindDate:
		payload, err := require(4)
		if err != nil {
			return Value{}, position, err
		}
		return DateValue(int32(binary.LittleEndian.Uint32(payload))), position, nil
	case KindDecimal:
		payload, err := require(17)
		if err != nil {
			return Value{}, position, err
		}
		coefficient := decodeI128LittleEndian(payload[:16])
		value, err := DecimalValue(coefficient, payload[16])
		if err != nil {
			return Value{}, position, err
		}
		return value, position, nil
	default:
		return Value{}, position, fmt.Errorf("unknown Value type tag %d", kind)
	}
}

func decimalPrecision(value *big.Int) int {
	if value.Sign() == 0 {
		return 1
	}
	return len(new(big.Int).Abs(value).String())
}

func encodeI128LittleEndian(value *big.Int) ([16]byte, bool) {
	var result [16]byte
	minimum := new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 127))
	maximum := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
	if value.Cmp(minimum) < 0 || value.Cmp(maximum) > 0 {
		return result, false
	}
	unsigned := new(big.Int).Set(value)
	if unsigned.Sign() < 0 {
		unsigned.Add(unsigned, new(big.Int).Lsh(big.NewInt(1), 128))
	}
	var bigEndian [16]byte
	unsigned.FillBytes(bigEndian[:])
	for index := range result {
		result[index] = bigEndian[len(bigEndian)-1-index]
	}
	return result, true
}

func decodeI128LittleEndian(value []byte) *big.Int {
	var bigEndian [16]byte
	for index := range bigEndian {
		bigEndian[len(bigEndian)-1-index] = value[index]
	}
	result := new(big.Int).SetBytes(bigEndian[:])
	if value[15]&0x80 != 0 {
		result.Sub(result, new(big.Int).Lsh(big.NewInt(1), 128))
	}
	return result
}
