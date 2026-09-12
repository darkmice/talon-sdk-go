/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package serverprotocol

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// sqlTestMaxDecimal is 10^38 - 1, the largest coefficient Core accepts.
var sqlTestMaxDecimal = func() *big.Int {
	value := new(big.Int).Exp(big.NewInt(10), big.NewInt(38), nil)
	return value.Sub(value, big.NewInt(1))
}()

func testServerSQLAttestation() *ServerSQLAttestation {
	return &ServerSQLAttestation{
		Capability:     ServerSQLDecimalCapability,
		Version:        ServerSQLVersion,
		ReleaseTag:     "v1.2.3",
		TalonBinCommit: strings.Repeat("a", 40),
		CoreCommit:     strings.Repeat("b", 40),
		ArtifactSHA256: strings.Repeat("c", 64),
	}
}

func newAttestedServerClient(t *testing.T, baseURL string) *ServerClient {
	t.Helper()
	client, err := NewServerClient(ServerClientConfig{
		BaseURL:              baseURL,
		Token:                "test-token",
		Timeout:              2 * 1e9,
		ServerSQLAttestation: testServerSQLAttestation(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func mustParseDecimal(t *testing.T, text string) Decimal {
	t.Helper()
	decimal, err := ParseDecimal(text)
	if err != nil {
		t.Fatalf("ParseDecimal(%q): %v", text, err)
	}
	return decimal
}

// ── Decimal exactness ──────────────────────────────────────────────────────

// A 38-digit coefficient exceeds i64/u64. It must survive the wire as a
// canonical decimal string, never as a JSON number.
func TestServerSQLDecimalMaxPrecisionRoundTripsExactly(t *testing.T) {
	text := "9999999999999999999999999999999999.9999"
	decimal := mustParseDecimal(t, text)
	if decimal.Coefficient().Cmp(sqlTestMaxDecimal) != 0 {
		t.Fatalf("coefficient = %s, want %s", decimal.Coefficient(), sqlTestMaxDecimal)
	}
	if decimal.Scale() != 4 {
		t.Fatalf("scale = %d, want 4", decimal.Scale())
	}
	if decimal.Text() != text {
		t.Fatalf("Text() = %q, want %q", decimal.Text(), text)
	}

	encoded, err := json.Marshal(decimal)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"Decimal":"`+text+`"}` {
		t.Fatalf("wire form = %s", encoded)
	}
	// The payload must be a JSON string: a number would be lossy.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &probe); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(probe["Decimal"]), `"`) {
		t.Fatalf("Decimal payload is not a JSON string: %s", probe["Decimal"])
	}

	var decoded Decimal
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Coefficient().Cmp(decimal.Coefficient()) != 0 || decoded.Scale() != decimal.Scale() {
		t.Fatalf("round trip = %s/%d, want %s/%d", decoded.Coefficient(), decoded.Scale(), decimal.Coefficient(), decimal.Scale())
	}
}

func TestServerSQLDecimalNegativeAndScaleSurvive(t *testing.T) {
	for _, text := range []string{"-1.2300", "0.0000", "-0.0001", "0", "-99999999999999999999999999999999999999"} {
		decimal := mustParseDecimal(t, text)
		if decimal.Text() != text {
			t.Fatalf("Text() = %q, want %q", decimal.Text(), text)
		}
		encoded, err := json.Marshal(decimal)
		if err != nil {
			t.Fatal(err)
		}
		var decoded Decimal
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Text() != text {
			t.Fatalf("round trip = %q, want %q", decoded.Text(), text)
		}
	}
}

func TestServerSQLDecimalOutOfRangeIsRejected(t *testing.T) {
	overPrecision := strings.Repeat("9", 39)
	for _, text := range []string{
		overPrecision,
		"0." + strings.Repeat("0", 38) + "1", // scale 39
		"1e2",                                // exponent is not canonical
		"", " 1.0", "1.0 ", "1.2.3", "1,5", "abc", "--1",
	} {
		if _, err := ParseDecimal(text); err == nil {
			t.Fatalf("ParseDecimal(%q) accepted an out-of-contract literal", text)
		}
	}
	// 38 digits is still inside the bound.
	if _, err := ParseDecimal(strings.Repeat("9", 38)); err != nil {
		t.Fatalf("38-digit literal rejected: %v", err)
	}
	if _, err := NewDecimal(sqlTestMaxDecimal, 38); err != nil {
		t.Fatalf("max coefficient with scale 38 rejected: %v", err)
	}
	overflow := new(big.Int).Add(sqlTestMaxDecimal, big.NewInt(1))
	if _, err := NewDecimal(overflow, 0); err == nil {
		t.Fatal("39-digit coefficient was accepted")
	}
	if _, err := NewDecimal(big.NewInt(1), 39); err == nil {
		t.Fatal("scale 39 was accepted")
	}
}

// A JSON number payload cannot carry a 38-digit coefficient, so it is rejected
// instead of being silently degraded to float64.
func TestServerSQLDecimalRejectsJSONNumberForms(t *testing.T) {
	for _, payload := range []string{
		`{"Decimal":1.23}`,
		`{"Decimal":9007199254740993}`,
		`{"Decimal":{"coefficient":12345,"scale":2}}`,
		`{"Decimal":{"coefficient":1.5,"scale":1}}`,
		`{"Decimal":{"scale":2}}`,
		`{"Decimal":{"coefficient":"12345"}}`,
		`{"Decimal":null}`,
		`{"Decimal":"1.23","Text":"x"}`,
	} {
		var value Value
		if err := json.Unmarshal([]byte(payload), &value); err == nil {
			t.Fatalf("wire payload %s was accepted", payload)
		}
	}
}

func TestServerSQLDecimalAcceptsDocumentedInputForms(t *testing.T) {
	for _, test := range []struct{ payload, want string }{
		{`{"Decimal":"123.4500"}`, "123.4500"},
		{`{"Decimal":{"coefficient":"1234500","scale":4}}`, "123.4500"},
	} {
		var value Value
		if err := json.Unmarshal([]byte(test.payload), &value); err != nil {
			t.Fatalf("wire payload %s: %v", test.payload, err)
		}
		decimal, ok := value.Decimal()
		if !ok || decimal.Text() != test.want {
			t.Fatalf("payload %s decoded to %v/%v, want %q", test.payload, decimal.Text(), ok, test.want)
		}
	}
}

// ── value codec ────────────────────────────────────────────────────────────

// Every kind must round-trip through Core's externally tagged wire form.
func TestServerSQLValueWireFormsMatchCore(t *testing.T) {
	mustValue := func(value Value, err error) Value {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	decimalValue, err := DecimalValue(mustParseDecimal(t, "9999999999999999999999999999999999.9999"))
	if err != nil {
		t.Fatal(err)
	}
	values := []Value{
		NullValue(),
		IntegerValue(7),
		BooleanValue(true),
		BlobValue([]byte{0, 39, 255}),
		TimestampValue(1),
		DateValue(19_000),
		mustValue(TimeValue(1_000_000)),
		decimalValue,
		mustValue(FloatValue(3.14)),
		mustValue(TextValue("hi")),
		mustValue(JSONValue([]byte(`{"a":1}`))),
		mustValue(GeoPointValue(39.9, 116.4)),
		mustValue(VectorValue([]float32{0.1, 0.2})),
	}
	expected := []string{
		`"Null"`,
		`{"Integer":7}`,
		`{"Boolean":true}`,
		`{"Blob":[0,39,255]}`,
		`{"Timestamp":1}`,
		`{"Date":19000}`,
		`{"Time":1000000}`,
		`{"Decimal":"9999999999999999999999999999999999.9999"}`,
		`{"Float":3.14}`,
		`{"Text":"hi"}`,
		`{"Jsonb":{"a":1}}`,
		`{"GeoPoint":[39.9,116.4]}`,
		`{"Vector":[0.1,0.2]}`,
	}

	for index, value := range values {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %d: %v", index, err)
		}
		if string(encoded) != expected[index] {
			t.Fatalf("value %d wire form = %s, want %s", index, encoded, expected[index])
		}
		var decoded Value
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("unmarshal %d (%s): %v", index, encoded, err)
		}
		if decoded.Kind() != value.Kind() {
			t.Fatalf("value %d kind = %d, want %d", index, decoded.Kind(), value.Kind())
		}
		roundTripped, err := json.Marshal(decoded)
		if err != nil {
			t.Fatalf("re-marshal %d: %v", index, err)
		}
		if string(roundTripped) != string(encoded) {
			t.Fatalf("value %d round trip = %s, want %s", index, roundTripped, encoded)
		}
	}
}

func TestServerSQLValueRejectsHostileWire(t *testing.T) {
	for _, payload := range []string{
		`{"Unknown":1}`,
		`{"Integer":7,"Text":"x"}`,
		`{"Integer":"7"}`,
		`{"Integer":1.5}`,
		`{"Integer":99999999999999999999}`,
		`{"Text":7}`,
		`{"Blob":"AAAA"}`,
		`{"Blob":[256]}`,
		`{"Date":2147483648}`,
		`{"Time":-1}`,
		`{"GeoPoint":[1]}`,
		`{"GeoPoint":[91,0]}`,
		`{"Float":1e400}`,
		`7`,
		`null`,
		`"Integer"`,
	} {
		var value Value
		if err := json.Unmarshal([]byte(payload), &value); err == nil {
			t.Fatalf("hostile payload %s was accepted as %#v", payload, value)
		}
	}
	var null Value
	if err := json.Unmarshal([]byte(`"Null"`), &null); err != nil || !null.IsNull() {
		t.Fatalf("null wire form = %#v, %v", null, err)
	}
}

// ── query surface ──────────────────────────────────────────────────────────

func TestServerSQLCapabilityIsGatedUntilAttested(t *testing.T) {
	static := ServerSQLCapability()
	if static.Status != ServerSQLStatusGated || static.Name != ServerSQLDecimalCapability {
		t.Fatalf("static gate = %#v", static)
	}
	if static.Reason == "" {
		t.Fatal("a gated capability must explain why")
	}

	unattested := newTestServerClient(t, "http://127.0.0.1:1")
	if gate := unattested.SQLCapability(); gate.Status != ServerSQLStatusGated {
		t.Fatalf("unattested client gate = %#v", gate)
	}

	attested := newAttestedServerClient(t, "http://127.0.0.1:1")
	if gate := attested.SQLCapability(); gate.Status != ServerSQLStatusAvailable || gate.ReleaseTag != "v1.2.3" || gate.TalonBinCommit != strings.Repeat("a", 40) || gate.CoreCommit != strings.Repeat("b", 40) || gate.ArtifactSHA256 != strings.Repeat("c", 64) {
		t.Fatalf("attested client gate = %#v", gate)
	}

	for _, bad := range []*ServerSQLAttestation{
		{Capability: "something_else", Version: ServerSQLVersion},
		{Capability: ServerSQLDecimalCapability, Version: ServerSQLVersion + 1},
		{Capability: ServerSQLDecimalCapability, Version: ServerSQLVersion, ReleaseTag: "UNRELEASED", TalonBinCommit: strings.Repeat("a", 40), CoreCommit: strings.Repeat("b", 40), ArtifactSHA256: strings.Repeat("c", 64)},
		{Capability: ServerSQLDecimalCapability, Version: ServerSQLVersion, ReleaseTag: "v1.2.3", TalonBinCommit: "deadbeef", CoreCommit: strings.Repeat("b", 40), ArtifactSHA256: strings.Repeat("c", 64)},
		{Capability: ServerSQLDecimalCapability, Version: ServerSQLVersion, ReleaseTag: "v1.2.3", TalonBinCommit: strings.Repeat("a", 40), CoreCommit: "deadbeef", ArtifactSHA256: strings.Repeat("c", 64)},
		{Capability: ServerSQLDecimalCapability, Version: ServerSQLVersion, ReleaseTag: "v1.2.3", TalonBinCommit: strings.Repeat("a", 40), CoreCommit: strings.Repeat("b", 40), ArtifactSHA256: "deadbeef"},
	} {
		if _, err := NewServerClient(ServerClientConfig{BaseURL: "http://127.0.0.1:1", Timeout: 1e9, ServerSQLAttestation: bad}); err == nil {
			t.Fatalf("attestation %#v was accepted", bad)
		}
	}
}

func TestServerSQLResponseFailureIsIndeterminateForPotentialWrites(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{`))
	}))
	defer server.Close()

	client := newAttestedServerClient(t, server.URL)
	request, err := NewQueryRequest("INSERT INTO ledger (amount) VALUES (?)", IntegerValue(1))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Query(context.Background(), request)
	if ErrorCodeOf(err) != CodeResultIndeterminate {
		t.Fatalf("unreadable SQL response error = %v, code = %q", err, ErrorCodeOf(err))
	}
}

func TestServerSQLMapsDecimalDomainFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"precision exceeds 38","code":"decimal_out_of_range"}`))
	}))
	defer server.Close()

	client := newAttestedServerClient(t, server.URL)
	request, err := NewQueryRequest("SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Query(context.Background(), request)
	if ErrorCodeOf(err) != CodeNativeDecimalOutOfRange || NativeCodeOf(err) != "decimal_out_of_range" {
		t.Fatalf("decimal domain error = %v, code = %q, native = %q", err, ErrorCodeOf(err), NativeCodeOf(err))
	}
}

func TestServerSQLQueryFailsClosedWithoutAttestation(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		writeServerTestData(w, []byte(`{"protocol_version":1,"rows":[]}`))
	}))
	defer server.Close()

	client := newTestServerClient(t, server.URL)
	request, err := NewQueryRequest("SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Query(context.Background(), request)
	if ErrorCodeOf(err) != CodeCapabilityUnavailable {
		t.Fatalf("unattested query error = %v, code = %q", err, ErrorCodeOf(err))
	}
	if requests != 0 {
		t.Fatalf("an unattested client must not reach the network, saw %d requests", requests)
	}
}

func TestServerSQLQueryBindsProtocolVersionAndDecodesDecimal(t *testing.T) {
	var received map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/sql" {
			t.Errorf("path = %q", request.URL.Path)
		}
		var envelope struct {
			Command string                     `json:"cmd"`
			Action  string                     `json:"action"`
			Params  map[string]json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if envelope.Command != "sql" || envelope.Action != "query" {
			t.Errorf("command = %q/%q", envelope.Command, envelope.Action)
		}
		received = envelope.Params
		writeServerTestData(w, []byte(`{"protocol_version":1,"columns":["total"],"rows":[[{"Decimal":"9999999999999999999999999999999999.9999"}]]}`))
	}))
	defer server.Close()

	client := newAttestedServerClient(t, server.URL)
	request, err := NewQueryRequest("SELECT SUM(amount) FROM entries")
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if string(received["protocol_version"]) != "1" {
		t.Fatalf("protocol_version = %s", received["protocol_version"])
	}
	if string(received["sql"]) != `"SELECT SUM(amount) FROM entries"` {
		t.Fatalf("sql = %s", received["sql"])
	}
	if _, present := received["bind"]; present {
		t.Fatalf("an empty parameter list must not be sent: %s", received["bind"])
	}
	if result.Version != ServerSQLVersion || len(result.Rows) != 1 || len(result.Rows[0]) != 1 {
		t.Fatalf("result = %#v", result)
	}
	decimal, ok := result.Decimal(0, 0)
	if !ok {
		t.Fatal("cell is not a Decimal")
	}
	if decimal.Text() != "9999999999999999999999999999999999.9999" {
		t.Fatalf("sum = %q", decimal.Text())
	}
	if decimal.Coefficient().Cmp(sqlTestMaxDecimal) != 0 || decimal.Scale() != 4 {
		t.Fatalf("sum = %s/%d", decimal.Coefficient(), decimal.Scale())
	}
	if len(result.Columns) != 1 || result.Columns[0] != "total" {
		t.Fatalf("columns = %#v", result.Columns)
	}
	if _, ok := result.Decimal(9, 9); ok {
		t.Fatal("out-of-range cell reported a decimal")
	}
}

func TestServerSQLQuerySendsExactDecimalBind(t *testing.T) {
	var bind json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var envelope struct {
			Params map[string]json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		bind = envelope.Params["bind"]
		writeServerTestData(w, []byte(`{"protocol_version":1,"rows":[]}`))
	}))
	defer server.Close()

	client := newAttestedServerClient(t, server.URL)
	value, err := DecimalValue(mustParseDecimal(t, "9999999999999999999999999999999999.9999"))
	if err != nil {
		t.Fatal(err)
	}
	request, err := NewQueryRequest("INSERT INTO ledger (amount) VALUES (?)", value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Query(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	want := `[{"Decimal":"9999999999999999999999999999999999.9999"}]`
	if string(bind) != want {
		t.Fatalf("bind = %s, want %s", bind, want)
	}
}

func TestServerSQLQueryRequiresProtocolVersionEcho(t *testing.T) {
	for _, test := range []struct {
		name     string
		response string
	}{
		{name: "missing", response: `{"rows":[]}`},
		{name: "mismatched", response: `{"protocol_version":2,"rows":[]}`},
		{name: "wrong type", response: `{"protocol_version":"1","rows":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeServerTestData(w, []byte(test.response))
			}))
			defer server.Close()

			client := newAttestedServerClient(t, server.URL)
			request, err := NewQueryRequest("SELECT 1")
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.Query(context.Background(), request)
			if ErrorCodeOf(err) != CodeResultIndeterminate || !errors.Is(err, &TalonError{Code: CodeProtocolViolation}) {
				t.Fatalf("error = %v, code = %q", err, ErrorCodeOf(err))
			}
		})
	}
}

func TestServerSQLQueryRejectsHostileResultWire(t *testing.T) {
	for _, test := range []struct {
		name     string
		response string
	}{
		{name: "unknown tag", response: `{"protocol_version":1,"rows":[[{"Money":"1"}]]}`},
		{name: "decimal number", response: `{"protocol_version":1,"rows":[[{"Decimal":1.23}]]}`},
		{name: "decimal out of range", response: `{"protocol_version":1,"rows":[[{"Decimal":"` + strings.Repeat("9", 39) + `"}]]}`},
		{name: "unknown field", response: `{"protocol_version":1,"rows":[],"extra":1}`},
		{name: "duplicate key", response: `{"protocol_version":1,"rows":[],"rows":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeServerTestData(w, []byte(test.response))
			}))
			defer server.Close()

			client := newAttestedServerClient(t, server.URL)
			request, err := NewQueryRequest("SELECT 1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Query(context.Background(), request); ErrorCodeOf(err) != CodeResultIndeterminate || !errors.Is(err, &TalonError{Code: CodeProtocolViolation}) {
				t.Fatalf("error = %v, code = %q", err, ErrorCodeOf(err))
			}
		})
	}
}

func TestServerSQLQueryRejectsInvalidRequestsLocally(t *testing.T) {
	client := newAttestedServerClient(t, "http://127.0.0.1:1")
	for _, test := range []struct {
		name   string
		sql    string
		params []Value
	}{
		{name: "empty", sql: "   "},
		{name: "too long", sql: strings.Repeat("a", maxServerSQLStatementBytes+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewQueryRequest(test.sql, test.params...); ErrorCodeOf(err) != CodeInvalidArgument {
				t.Fatalf("error = %v, code = %q", err, ErrorCodeOf(err))
			}
		})
	}
	if _, err := client.Query(context.Background(), QueryRequest{}); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("zero request error = %v", err)
	}
}
