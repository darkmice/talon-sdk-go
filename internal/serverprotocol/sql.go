/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package serverprotocol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ServerSQLVersion is the version of the typed SQL payload owned by this SDK.
// It is negotiated per request through params.protocol_version and required to
// be echoed by the remote Server: a Server that does not echo it is treated as
// not attesting the versioned surface.
const ServerSQLVersion uint16 = 1

// ServerSQLDecimalCapability is the capability that must be attested before the
// Server SQL/DECIMAL surface may be treated as available.
const ServerSQLDecimalCapability = "server_sql_decimal_v1"

// MaxDecimalPrecision mirrors Core's DECIMAL_MAX_PRECISION and the embedded
// path's maxDecimalPrecision. It is restated rather than imported because the
// cgo-free package graph must not reach the embedded DB.
const MaxDecimalPrecision = 38

const (
	maxServerSQLStatementBytes = 1 << 20
	maxServerSQLParameters     = 4096
	maxServerSQLColumns        = 4096
	maxServerSQLRows           = 1 << 20
	maxServerSQLVectorDims     = 4096
	maxServerSQLTimeNanos      = int64(86_399_999_999_999)
	decimalWireTag             = "Decimal"
)

var (
	serverSQLReleaseTagPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	serverSQLCommitPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	serverSQLDigestPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ── capability gate ────────────────────────────────────────────────────────

// ServerSQLGate is the capability gate state of the cgo-free Server SQL
// surface. Status is "gated" until a caller supplies an attestation that a
// released talon-bin artifact attests the surface; the SDK never infers
// availability from the endpoint, a tag, or a symbol name. Available gates
// retain the exact release and artifact identity for audit/reporting.
type ServerSQLGate struct {
	Name           string
	Status         string
	Reason         string
	Version        uint16
	ReleaseTag     string
	TalonBinCommit string
	CoreCommit     string
	ArtifactSHA256 string
}

const (
	ServerSQLStatusGated     = "gated"
	ServerSQLStatusAvailable = "available"
)

// ServerSQLAttestation is the out-of-band acknowledgement that a released
// talon-bin artifact attests the versioned Server SQL/DECIMAL surface. The
// caller is responsible for verifying the deployed endpoint and release
// provenance before constructing it. Every field is validated exactly; a
// partial or development identity is rejected rather than coerced.
type ServerSQLAttestation struct {
	Capability     string
	Version        uint16
	ReleaseTag     string
	TalonBinCommit string
	CoreCommit     string
	ArtifactSHA256 string
}

// ServerSQLCapability reports the static, un-attested gate state. It is
// deliberately "gated": the cgo-free client cannot read a signed manifest, so
// availability must be asserted explicitly, per client.
func ServerSQLCapability() ServerSQLGate {
	return ServerSQLGate{
		Name:    ServerSQLDecimalCapability,
		Status:  ServerSQLStatusGated,
		Reason:  "no released talon-bin artifact attestation was supplied to this client",
		Version: ServerSQLVersion,
	}
}

// SQLCapability reports the gate state as configured for this client.
func (client *ServerClient) SQLCapability() ServerSQLGate {
	if client == nil {
		return ServerSQLCapability()
	}
	return client.sqlGate
}

func openServerSQLGate(attestation *ServerSQLAttestation) (ServerSQLGate, error) {
	gate := ServerSQLCapability()
	if attestation == nil {
		return gate, nil
	}
	if attestation.Capability != ServerSQLDecimalCapability {
		return ServerSQLGate{}, newError(CodeInvalidArgument, "create server client", fmt.Sprintf("SQL attestation must name %q", ServerSQLDecimalCapability), nil)
	}
	if attestation.Version != ServerSQLVersion {
		return ServerSQLGate{}, newError(CodeInvalidArgument, "create server client", fmt.Sprintf("SQL attestation version must be %d", ServerSQLVersion), nil)
	}
	if !serverSQLReleaseTagPattern.MatchString(attestation.ReleaseTag) {
		return ServerSQLGate{}, newError(CodeInvalidArgument, "create server client", "SQL attestation release tag must be an immutable vX.Y.Z talon-bin release", nil)
	}
	if !serverSQLCommitPattern.MatchString(attestation.TalonBinCommit) {
		return ServerSQLGate{}, newError(CodeInvalidArgument, "create server client", "SQL attestation talon-bin commit must be a full lowercase commit SHA", nil)
	}
	if !serverSQLCommitPattern.MatchString(attestation.CoreCommit) {
		return ServerSQLGate{}, newError(CodeInvalidArgument, "create server client", "SQL attestation Core commit must be a full lowercase commit SHA", nil)
	}
	if !serverSQLDigestPattern.MatchString(attestation.ArtifactSHA256) {
		return ServerSQLGate{}, newError(CodeInvalidArgument, "create server client", "SQL attestation artifact digest must be a lowercase SHA-256 digest", nil)
	}
	gate.Status = ServerSQLStatusAvailable
	gate.Reason = "an exact released talon-bin artifact identity was attested out of band by the caller"
	gate.ReleaseTag = attestation.ReleaseTag
	gate.TalonBinCommit = attestation.TalonBinCommit
	gate.CoreCommit = attestation.CoreCommit
	gate.ArtifactSHA256 = attestation.ArtifactSHA256
	return gate, nil
}

// requireServerSQL fails closed unless the caller attested the surface.
func (client *ServerClient) requireServerSQL(operation string) error {
	if client == nil || client.sqlGate.Status != ServerSQLStatusAvailable {
		gate := ServerSQLCapability()
		if client != nil {
			gate = client.sqlGate
		}
		return newError(CodeCapabilityUnavailable, operation, fmt.Sprintf("%s is %s: %s", gate.Name, gate.Status, gate.Reason), nil)
	}
	return nil
}

// ── Decimal ────────────────────────────────────────────────────────────────

// Decimal is the exact Go projection of a Talon fixed-point value:
// Coefficient * 10^-Scale. It is never a float64, and Scale is retained so a
// DECIMAL(p,s) column's display contract survives the round trip.
type Decimal struct {
	coefficient *big.Int
	scale       uint8
}

// NewDecimal validates an exact fixed-point pair against MaxDecimalPrecision.
// Out-of-range input is rejected, never rounded or degraded to a float.
func NewDecimal(coefficient *big.Int, scale uint8) (Decimal, error) {
	if coefficient == nil {
		return Decimal{}, newError(CodeInvalidArgument, "construct decimal", "decimal coefficient is nil", nil)
	}
	if scale > MaxDecimalPrecision || decimalPrecision(coefficient) > MaxDecimalPrecision {
		return Decimal{}, newError(CodeInvalidArgument, "construct decimal", fmt.Sprintf("decimal precision or scale exceeds %d", MaxDecimalPrecision), nil)
	}
	return Decimal{coefficient: new(big.Int).Set(coefficient), scale: scale}, nil
}

// ParseDecimal parses the canonical fixed-point form: an optional sign, digits,
// and at most one decimal point. Exponent notation is rejected because the wire
// contract is a canonical fixed-point string.
func ParseDecimal(text string) (Decimal, error) {
	if text == "" || strings.TrimSpace(text) != text {
		return Decimal{}, newError(CodeInvalidArgument, "parse decimal", "decimal text is empty or padded with whitespace", nil)
	}
	digits, negative, fractionDigits, err := scanDecimalLiteral(text)
	if err != nil {
		return Decimal{}, err
	}
	if fractionDigits > MaxDecimalPrecision {
		return Decimal{}, newError(CodeInvalidArgument, "parse decimal", fmt.Sprintf("decimal scale exceeds %d", MaxDecimalPrecision), nil)
	}
	trimmed := strings.TrimLeft(digits, "0")
	if len(trimmed) > MaxDecimalPrecision {
		return Decimal{}, newError(CodeInvalidArgument, "parse decimal", fmt.Sprintf("decimal precision exceeds %d", MaxDecimalPrecision), nil)
	}
	coefficient := new(big.Int)
	if trimmed != "" {
		if _, ok := coefficient.SetString(trimmed, 10); !ok {
			return Decimal{}, newError(CodeInvalidArgument, "parse decimal", "decimal digits are not a valid integer", nil)
		}
	}
	if negative {
		coefficient.Neg(coefficient)
	}
	return Decimal{coefficient: coefficient, scale: uint8(fractionDigits)}, nil
}

func scanDecimalLiteral(text string) (digits string, negative bool, fractionDigits int, err error) {
	invalid := func() error {
		return newError(CodeInvalidArgument, "parse decimal", fmt.Sprintf("invalid decimal literal %q", text), nil)
	}
	body := text
	switch body[0] {
	case '-':
		negative = true
		body = body[1:]
	case '+':
		body = body[1:]
	}
	if body == "" {
		return "", false, 0, invalid()
	}
	var builder strings.Builder
	seenPoint := false
	for index := 0; index < len(body); index++ {
		character := body[index]
		switch {
		case character >= '0' && character <= '9':
			builder.WriteByte(character)
			if seenPoint {
				fractionDigits++
			}
		case character == '.' && !seenPoint:
			seenPoint = true
		default:
			return "", false, 0, invalid()
		}
	}
	if builder.Len() == 0 {
		return "", false, 0, invalid()
	}
	return builder.String(), negative, fractionDigits, nil
}

// Coefficient returns a defensive copy of the exact coefficient.
func (decimal Decimal) Coefficient() *big.Int {
	if decimal.coefficient == nil {
		return new(big.Int)
	}
	return new(big.Int).Set(decimal.coefficient)
}

// Scale returns the retained display scale.
func (decimal Decimal) Scale() uint8 { return decimal.scale }

// Text renders the canonical fixed-point string: no exponent, and exactly
// Scale fractional digits. It round-trips through ParseDecimal exactly and
// matches the embedded path and Core's canonical form.
func (decimal Decimal) Text() string {
	coefficient := decimal.coefficient
	if coefficient == nil {
		coefficient = new(big.Int)
	}
	digits := new(big.Int).Abs(coefficient).String()
	var builder strings.Builder
	if coefficient.Sign() < 0 {
		builder.WriteByte('-')
	}
	if decimal.scale == 0 {
		builder.WriteString(digits)
		return builder.String()
	}
	scale := int(decimal.scale)
	if len(digits) <= scale {
		builder.WriteString("0.")
		builder.WriteString(strings.Repeat("0", scale-len(digits)))
		builder.WriteString(digits)
		return builder.String()
	}
	split := len(digits) - scale
	builder.WriteString(digits[:split])
	builder.WriteByte('.')
	builder.WriteString(digits[split:])
	return builder.String()
}

// MarshalJSON writes the tagged canonical form {"Decimal":"<text>"}. A JSON
// number is never produced: Number can only carry i64/u64/f64, so a 38-digit
// coefficient would be rejected or silently degraded.
func (decimal Decimal) MarshalJSON() ([]byte, error) {
	if decimal.coefficient == nil {
		return nil, fmt.Errorf("decimal coefficient is nil")
	}
	if decimal.scale > MaxDecimalPrecision || decimalPrecision(decimal.coefficient) > MaxDecimalPrecision {
		return nil, fmt.Errorf("decimal precision or scale exceeds %d", MaxDecimalPrecision)
	}
	buffer := make([]byte, 0, len(decimal.Text())+16)
	buffer = append(buffer, `{"`...)
	buffer = append(buffer, decimalWireTag...)
	buffer = append(buffer, `":"`...)
	buffer = append(buffer, decimal.Text()...)
	return append(buffer, `"}`...), nil
}

// UnmarshalJSON accepts the tagged canonical string and the explicit
// {coefficient, scale} string pair. Any JSON number form is rejected rather
// than coerced.
func (decimal *Decimal) UnmarshalJSON(data []byte) error {
	_, payload, err := unwrapTaggedPayload(data, decimalWireTag)
	if err != nil {
		return err
	}
	parsed, err := decodeDecimalPayload(payload)
	if err != nil {
		return err
	}
	*decimal = parsed
	return nil
}

func decimalPrecision(value *big.Int) int {
	if value == nil || value.Sign() == 0 {
		return 1
	}
	return len(new(big.Int).Abs(value).String())
}

// ── Value ──────────────────────────────────────────────────────────────────

// ValueKind is the exact Talon wire type. Numeric kinds are never coerced.
type ValueKind uint8

const (
	KindNull ValueKind = iota
	KindInteger
	KindFloat
	KindDecimal
	KindText
	KindBlob
	KindBoolean
	KindJSON
	KindVector
	KindTimestamp
	KindGeoPoint
	KindDate
	KindTime
)

// GeoPoint is a WGS-84 latitude/longitude pair.
type GeoPoint struct {
	Latitude  float64
	Longitude float64
}

// Value is a closed, typed representation of Core's JSON wire Value. Its fields
// are private so callers cannot construct a kind/payload mismatch.
type Value struct {
	kind    ValueKind
	integer int64
	float   float64
	text    string
	bytes   []byte
	boolean bool
	vector  []float32
	geo     GeoPoint
	decimal Decimal
}

func NullValue() Value               { return Value{kind: KindNull} }
func IntegerValue(value int64) Value { return Value{kind: KindInteger, integer: value} }
func BooleanValue(value bool) Value  { return Value{kind: KindBoolean, boolean: value} }
func TimestampValue(value int64) Value {
	return Value{kind: KindTimestamp, integer: value}
}
func DateValue(value int32) Value { return Value{kind: KindDate, integer: int64(value)} }
func BlobValue(value []byte) Value {
	return Value{kind: KindBlob, bytes: append([]byte(nil), value...)}
}

// DecimalValue wraps an already validated exact decimal. It never converts
// through float64.
func DecimalValue(decimal Decimal) (Value, error) {
	validated, err := NewDecimal(decimal.coefficient, decimal.scale)
	if err != nil {
		return Value{}, err
	}
	return Value{kind: KindDecimal, decimal: validated}, nil
}

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
	if len(value) == 0 || len(value) > maxServerSQLVectorDims {
		return Value{}, newError(CodeInvalidArgument, "construct vector value", fmt.Sprintf("vector dimension must be in 1..%d", maxServerSQLVectorDims), nil)
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
	if nanosecondsSinceMidnight < 0 || nanosecondsSinceMidnight > maxServerSQLTimeNanos {
		return Value{}, newError(CodeInvalidArgument, "construct time value", "time is outside one day in nanoseconds", nil)
	}
	return Value{kind: KindTime, integer: nanosecondsSinceMidnight}, nil
}

func (value Value) Kind() ValueKind { return value.kind }

func (value Value) IsNull() bool { return value.kind == KindNull }

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

func (value Value) Text() (string, bool) {
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
	return value.boolean, true
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

func (value Value) Date() (int32, bool) {
	if value.kind != KindDate {
		return 0, false
	}
	return int32(value.integer), true
}

func (value Value) Time() (int64, bool) {
	if value.kind != KindTime {
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

// Decimal returns the exact fixed-point projection. It never returns a float64.
func (value Value) Decimal() (Decimal, bool) {
	if value.kind != KindDecimal {
		return Decimal{}, false
	}
	return Decimal{coefficient: value.decimal.Coefficient(), scale: value.decimal.scale}, true
}

// GoValue returns a defensive, exact Go projection for compatibility APIs.
func (value Value) GoValue() interface{} {
	switch value.kind {
	case KindNull:
		return nil
	case KindInteger:
		return value.integer
	case KindFloat:
		return value.float
	case KindDecimal:
		decimal, _ := value.Decimal()
		return decimal
	case KindText:
		return value.text
	case KindBlob:
		result, _ := value.Blob()
		return result
	case KindBoolean:
		return value.boolean
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
		return int32(value.integer)
	default:
		return nil
	}
}

// MarshalJSON writes Core's externally tagged wire form. Decimal is the only
// kind whose payload is a string, and it is never a JSON number.
func (value Value) MarshalJSON() ([]byte, error) {
	switch value.kind {
	case KindNull:
		return []byte(`"Null"`), nil
	case KindInteger:
		return marshalTaggedNumber("Integer", strconv.FormatInt(value.integer, 10)), nil
	case KindFloat:
		if math.IsNaN(value.float) || math.IsInf(value.float, 0) {
			return nil, fmt.Errorf("float value is not finite")
		}
		return marshalTaggedNumber("Float", strconv.FormatFloat(value.float, 'g', -1, 64)), nil
	case KindDecimal:
		return value.decimal.MarshalJSON()
	case KindText:
		return json.Marshal(map[string]string{"Text": value.text})
	case KindBlob:
		octets := make([]int, len(value.bytes))
		for index, item := range value.bytes {
			octets[index] = int(item)
		}
		return json.Marshal(map[string][]int{"Blob": octets})
	case KindBoolean:
		return json.Marshal(map[string]bool{"Boolean": value.boolean})
	case KindJSON:
		if len(value.bytes) == 0 {
			return nil, fmt.Errorf("JSON value is empty")
		}
		buffer := make([]byte, 0, len(value.bytes)+12)
		buffer = append(buffer, `{"Jsonb":`...)
		buffer = append(buffer, value.bytes...)
		return append(buffer, '}'), nil
	case KindVector:
		return json.Marshal(map[string][]float32{"Vector": value.vector})
	case KindTimestamp:
		return marshalTaggedNumber("Timestamp", strconv.FormatInt(value.integer, 10)), nil
	case KindGeoPoint:
		return json.Marshal(map[string][]float64{"GeoPoint": {value.geo.Latitude, value.geo.Longitude}})
	case KindDate:
		return marshalTaggedNumber("Date", strconv.FormatInt(value.integer, 10)), nil
	case KindTime:
		return marshalTaggedNumber("Time", strconv.FormatInt(value.integer, 10)), nil
	default:
		return nil, fmt.Errorf("unknown Value kind %d", value.kind)
	}
}

func marshalTaggedNumber(tag, literal string) []byte {
	buffer := make([]byte, 0, len(tag)+len(literal)+6)
	buffer = append(buffer, `{"`...)
	buffer = append(buffer, tag...)
	buffer = append(buffer, `":`...)
	buffer = append(buffer, literal...)
	return append(buffer, '}')
}

// UnmarshalJSON strictly parses Core's externally tagged wire form. Unknown
// tags, mismatched payloads, and Decimal JSON numbers are rejected.
func (value *Value) UnmarshalJSON(data []byte) error {
	parsed, err := decodeWireValue(data)
	if err != nil {
		return err
	}
	*value = parsed
	return nil
}

// ── Query request / result ─────────────────────────────────────────────────

// QueryRequest is a closed, versioned SQL request. Its statement and parameters
// are fixed at construction, so a caller cannot smuggle an unvalidated
// parameter set into the transport.
type QueryRequest struct {
	sql    string
	params []Value
}

// NewQueryRequest validates a statement and its exact typed parameters.
func NewQueryRequest(sql string, params ...Value) (QueryRequest, error) {
	if strings.TrimSpace(sql) == "" {
		return QueryRequest{}, newError(CodeInvalidArgument, "remote SQL", "SQL statement is empty", nil)
	}
	if len(sql) > maxServerSQLStatementBytes {
		return QueryRequest{}, newError(CodeInvalidArgument, "remote SQL", "SQL statement exceeds the SDK bound", nil)
	}
	if !utf8.ValidString(sql) {
		return QueryRequest{}, newError(CodeInvalidArgument, "remote SQL", "SQL statement is not valid UTF-8", nil)
	}
	if len(params) > maxServerSQLParameters {
		return QueryRequest{}, newError(CodeInvalidArgument, "remote SQL", "parameter count exceeds the SDK bound", nil)
	}
	// Re-encode every parameter so a zero Value cannot reach the wire.
	for index := range params {
		if _, err := json.Marshal(params[index]); err != nil {
			return QueryRequest{}, newError(CodeInvalidArgument, "remote SQL", fmt.Sprintf("parameter %d is not a valid Talon value", index), err)
		}
	}
	return QueryRequest{sql: sql, params: append([]Value(nil), params...)}, nil
}

// Version returns the versioned SQL payload version sent with this request.
func (request QueryRequest) Version() uint16 { return ServerSQLVersion }

// SQL returns the statement.
func (request QueryRequest) SQL() string { return request.sql }

// Params returns a defensive copy of the exact typed parameters.
func (request QueryRequest) Params() []Value {
	return append([]Value(nil), request.params...)
}

// QueryResult is one verified, versioned SQL result.
type QueryResult struct {
	Version uint16
	Columns []string
	Rows    [][]Value
}

// Decimal returns the exact decimal at one cell. A non-Decimal cell reports
// ok == false; it is never coerced to a float.
func (result QueryResult) Decimal(row, column int) (Decimal, bool) {
	if row < 0 || row >= len(result.Rows) || column < 0 || column >= len(result.Rows[row]) {
		return Decimal{}, false
	}
	return result.Rows[row][column].Decimal()
}

// Query executes one versioned SQL statement over the cgo-free Server client.
//
// The call fails closed with CodeCapabilityUnavailable unless the caller
// attested ServerSQLDecimalCapability for this client, and with
// Because Core accepts reads and writes through the same endpoint, any
// transport or response failure after sending is conservatively
// CodeResultIndeterminate. A version mismatch or malformed typed result retains
// CodeProtocolViolation as a wrapped cause for diagnostics, but uncertainty is
// the primary classification.
func (client *ServerClient) Query(ctx context.Context, request QueryRequest) (QueryResult, error) {
	const operation = "remote SQL query"
	if !client.configured() {
		return QueryResult{}, newError(CodeNativeUnavailable, operation, "server client is not configured", nil)
	}
	if err := client.requireServerSQL(operation); err != nil {
		return QueryResult{}, err
	}
	validated, err := NewQueryRequest(request.sql, request.params...)
	if err != nil {
		return QueryResult{}, err
	}
	params := map[string]interface{}{
		"protocol_version": ServerSQLVersion,
		"sql":              validated.sql,
	}
	if len(validated.params) != 0 {
		params["bind"] = validated.params
	}
	// Core accepts both reads and mutations through the same SQL endpoint and
	// does not expose a trustworthy read-only discriminator. Treat every sent
	// statement as possibly applied so a lost or malformed response can never be
	// mistaken for proof that a write did not happen.
	data, err := client.do(ctx, http.MethodPost, client.sqlURL, serverCommand{Command: "sql", Action: "query", Params: params}, maxServerResponseBytes, operation, true, true)
	if err != nil {
		return QueryResult{}, err
	}
	result, err := decodeQueryResult(data)
	if err != nil {
		return QueryResult{}, newError(CodeResultIndeterminate, operation, "remote SQL response could not prove the statement outcome", err)
	}
	return result, nil
}

type wireQueryResult struct {
	ProtocolVersion *uint16       `json:"protocol_version"`
	Rows            [][]wireValue `json:"rows"`
	Columns         []string      `json:"columns"`
}

func decodeQueryResult(data []byte) (QueryResult, error) {
	var wire wireQueryResult
	if err := decodeStrictJSON(data, &wire); err != nil {
		return QueryResult{}, remoteProtocolError("decode remote SQL result", err)
	}
	if wire.ProtocolVersion == nil {
		return QueryResult{}, remoteProtocolError("decode remote SQL result", fmt.Errorf("remote Server did not echo protocol_version %d", ServerSQLVersion))
	}
	if *wire.ProtocolVersion != ServerSQLVersion {
		return QueryResult{}, remoteProtocolError("decode remote SQL result", fmt.Errorf("remote Server answered protocol_version %d, want %d", *wire.ProtocolVersion, ServerSQLVersion))
	}
	if len(wire.Columns) > maxServerSQLColumns || len(wire.Rows) > maxServerSQLRows {
		return QueryResult{}, remoteProtocolError("decode remote SQL result", fmt.Errorf("result exceeds the SDK bound"))
	}
	rows := make([][]Value, len(wire.Rows))
	for index, row := range wire.Rows {
		values := make([]Value, len(row))
		for column, cell := range row {
			if cell.err != nil {
				return QueryResult{}, remoteProtocolError("decode remote SQL result", fmt.Errorf("row %d column %d: %w", index, column, cell.err))
			}
			values[column] = cell.value
		}
		rows[index] = values
	}
	return QueryResult{Version: *wire.ProtocolVersion, Columns: append([]string(nil), wire.Columns...), Rows: rows}, nil
}

// ── wire codec ─────────────────────────────────────────────────────────────

// wireValue defers a per-cell decode failure so the result decoder can report
// the exact row and column instead of a positional JSON error.
type wireValue struct {
	value Value
	err   error
}

func (cell *wireValue) UnmarshalJSON(data []byte) error {
	parsed, err := decodeWireValue(data)
	if err != nil {
		cell.err = err
		return nil
	}
	cell.value = parsed
	return nil
}

func (cell wireValue) MarshalJSON() ([]byte, error) { return cell.value.MarshalJSON() }

func decodeWireValue(data []byte) (Value, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return Value{}, fmt.Errorf("empty Talon wire value")
	}
	if trimmed[0] == '"' {
		var tag string
		if err := decodeStrictJSON(trimmed, &tag); err != nil {
			return Value{}, err
		}
		if tag != "Null" {
			return Value{}, fmt.Errorf("unknown Talon wire unit variant %q", tag)
		}
		return NullValue(), nil
	}
	tag, payload, err := unwrapTaggedPayload(trimmed, "")
	if err != nil {
		return Value{}, err
	}
	switch tag {
	case "Integer":
		number, err := decodeWireInt64(payload)
		if err != nil {
			return Value{}, err
		}
		return IntegerValue(number), nil
	case "Float":
		number, err := decodeWireFloat64(payload)
		if err != nil {
			return Value{}, err
		}
		return FloatValue(number)
	case decimalWireTag:
		decimal, err := decodeDecimalPayload(payload)
		if err != nil {
			return Value{}, err
		}
		return Value{kind: KindDecimal, decimal: decimal}, nil
	case "Text":
		var text string
		if err := decodeStrictJSON(payload, &text); err != nil {
			return Value{}, err
		}
		return TextValue(text)
	case "Blob":
		octets, err := decodeWireOctets(payload)
		if err != nil {
			return Value{}, err
		}
		return BlobValue(octets), nil
	case "Boolean":
		var flag bool
		if err := decodeStrictJSON(payload, &flag); err != nil {
			return Value{}, err
		}
		return BooleanValue(flag), nil
	case "Jsonb":
		return JSONValue(payload)
	case "Vector":
		vector, err := decodeWireVector(payload)
		if err != nil {
			return Value{}, err
		}
		return VectorValue(vector)
	case "Timestamp":
		number, err := decodeWireInt64(payload)
		if err != nil {
			return Value{}, err
		}
		return TimestampValue(number), nil
	case "GeoPoint":
		pair, err := decodeWireFloat64Pair(payload)
		if err != nil {
			return Value{}, err
		}
		return GeoPointValue(pair[0], pair[1])
	case "Date":
		number, err := decodeWireInt32(payload)
		if err != nil {
			return Value{}, err
		}
		return DateValue(number), nil
	case "Time":
		number, err := decodeWireInt64(payload)
		if err != nil {
			return Value{}, err
		}
		return TimeValue(number)
	default:
		return Value{}, fmt.Errorf("unknown Talon wire tag %q", tag)
	}
}

// unwrapTaggedPayload requires exactly one tag key. When expected is empty any
// tag is accepted and returned; otherwise the tag must match.
func unwrapTaggedPayload(data []byte, expected string) (string, json.RawMessage, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return "", nil, fmt.Errorf("tagged payload must be a JSON object")
	}
	var object map[string]json.RawMessage
	if err := decodeStrictJSON(trimmed, &object); err != nil {
		return "", nil, err
	}
	if len(object) != 1 {
		return "", nil, fmt.Errorf("tagged payload must have exactly one tag")
	}
	for tag, payload := range object {
		if expected != "" && tag != expected {
			return "", nil, fmt.Errorf("expected tag %q, got %q", expected, tag)
		}
		return tag, payload, nil
	}
	return "", nil, fmt.Errorf("tagged payload has no tag")
}

// decodeWireDecimal accepts the canonical decimal string and the explicit
// {coefficient, scale} string pair. A JSON number is rejected: Number cannot
// carry a 38-digit coefficient, so accepting it would silently corrupt money.
func decodeDecimalPayload(payload []byte) (Decimal, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return Decimal{}, fmt.Errorf("empty decimal payload")
	}
	if trimmed[0] == '"' {
		var text string
		if err := decodeStrictJSON(trimmed, &text); err != nil {
			return Decimal{}, err
		}
		return ParseDecimal(text)
	}
	if trimmed[0] != '{' {
		return Decimal{}, fmt.Errorf("decimal payload must be a canonical decimal string or an explicit {coefficient, scale} pair")
	}
	var pair struct {
		Coefficient json.RawMessage `json:"coefficient"`
		Scale       json.RawMessage `json:"scale"`
	}
	if err := decodeStrictJSON(trimmed, &pair); err != nil {
		return Decimal{}, err
	}
	if len(pair.Coefficient) == 0 || len(pair.Scale) == 0 {
		return Decimal{}, fmt.Errorf("decimal pair must carry coefficient and scale")
	}
	var coefficientText string
	if err := decodeStrictJSON(pair.Coefficient, &coefficientText); err != nil {
		return Decimal{}, fmt.Errorf("decimal coefficient must be an integer string: %w", err)
	}
	coefficient := new(big.Int)
	if _, ok := coefficient.SetString(coefficientText, 10); !ok {
		return Decimal{}, fmt.Errorf("decimal coefficient %q is not an integer", coefficientText)
	}
	scale, err := decodeWireUint8(pair.Scale)
	if err != nil {
		return Decimal{}, err
	}
	return NewDecimal(coefficient, scale)
}

func decodeWireUint8(data []byte) (uint8, error) {
	literal, err := decodeWireIntegerLiteral(data)
	if err != nil {
		return 0, fmt.Errorf("decimal scale must be a non-negative integer: %w", err)
	}
	parsed, err := strconv.ParseUint(literal, 10, 8)
	if err != nil {
		return 0, fmt.Errorf("decimal scale is not a u8: %w", err)
	}
	return uint8(parsed), nil
}

// decodeWireIntegerLiteral requires a bare JSON integer token. Quoted numbers
// and non-integer tokens are rejected so a stringly-typed payload cannot be
// mistaken for an exact integer.
func decodeWireIntegerLiteral(data []byte) (string, error) {
	literal := string(bytes.TrimSpace(data))
	if literal == "" || !(literal[0] == '-' || (literal[0] >= '0' && literal[0] <= '9')) {
		return "", fmt.Errorf("expected a JSON integer token, got %s", literal)
	}
	if strings.ContainsAny(literal, ".eE") {
		return "", fmt.Errorf("expected a JSON integer token, got %s", literal)
	}
	return literal, nil
}

func decodeWireInt64(data []byte) (int64, error) {
	literal, err := decodeWireIntegerLiteral(data)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseInt(literal, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("integer %s is outside the i64 range: %w", literal, err)
	}
	return parsed, nil
}

func decodeWireInt32(data []byte) (int32, error) {
	literal, err := decodeWireIntegerLiteral(data)
	if err != nil {
		return 0, err
	}
	parsed, err := strconv.ParseInt(literal, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("integer %s is outside the i32 range: %w", literal, err)
	}
	return int32(parsed), nil
}

func decodeWireFloat64(data []byte) (float64, error) {
	literal := string(bytes.TrimSpace(data))
	parsed, err := strconv.ParseFloat(literal, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid float token %s: %w", literal, err)
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, fmt.Errorf("float token %s is not finite", literal)
	}
	return parsed, nil
}

// decodeWireOctets requires an array of byte literals. A base64 string, which
// encoding/json would otherwise accept for []byte, is rejected.
func decodeWireOctets(payload []byte) ([]byte, error) {
	var numbers []json.Number
	if err := decodeStrictJSON(payload, &numbers); err != nil {
		return nil, err
	}
	octets := make([]byte, len(numbers))
	for index, number := range numbers {
		parsed, err := strconv.ParseUint(number.String(), 10, 8)
		if err != nil {
			return nil, fmt.Errorf("blob element %s is not a u8", number.String())
		}
		octets[index] = uint8(parsed)
	}
	return octets, nil
}

// decodeWireVector parses f32 elements directly at 32-bit precision.
func decodeWireVector(payload []byte) ([]float32, error) {
	var numbers []json.Number
	if err := decodeStrictJSON(payload, &numbers); err != nil {
		return nil, err
	}
	vector := make([]float32, len(numbers))
	for index, number := range numbers {
		parsed, err := strconv.ParseFloat(number.String(), 32)
		if err != nil {
			return nil, fmt.Errorf("invalid Vector element %s: %w", number.String(), err)
		}
		vector[index] = float32(parsed)
	}
	return vector, nil
}

func decodeWireFloat64Pair(payload []byte) ([2]float64, error) {
	var numbers []json.Number
	if err := decodeStrictJSON(payload, &numbers); err != nil {
		return [2]float64{}, err
	}
	if len(numbers) != 2 {
		return [2]float64{}, fmt.Errorf("GeoPoint payload must be a two-element array")
	}
	var pair [2]float64
	for index, number := range numbers {
		parsed, err := strconv.ParseFloat(number.String(), 64)
		if err != nil {
			return [2]float64{}, fmt.Errorf("invalid GeoPoint element %s: %w", number.String(), err)
		}
		pair[index] = parsed
	}
	return pair, nil
}
