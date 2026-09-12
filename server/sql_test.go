/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	server "github.com/darkmice/talon-sdk-go/server"
)

// sqlConsumerMaxDecimal is 10^38 - 1: the largest coefficient Core accepts.
var sqlConsumerMaxDecimal = func() *big.Int {
	value := new(big.Int).Exp(big.NewInt(10), big.NewInt(38), nil)
	return value.Sub(value, big.NewInt(1))
}()

const sqlConsumerMaxText = "9999999999999999999999999999999999.9999"

func newSQLConsumerClient(t *testing.T, baseURL string, attest bool) *server.ServerClient {
	t.Helper()
	config := server.ServerClientConfig{BaseURL: baseURL, Timeout: 2 * time.Second}
	if attest {
		config.ServerSQLAttestation = &server.ServerSQLAttestation{
			Capability:     server.ServerSQLDecimalCapability,
			Version:        server.ServerSQLVersion,
			ReleaseTag:     "v1.2.3",
			TalonBinCommit: strings.Repeat("a", 40),
			CoreCommit:     strings.Repeat("b", 40),
			ArtifactSHA256: strings.Repeat("c", 64),
		}
	}
	client, err := server.NewServerClient(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func writeSQLTestData(t *testing.T, w http.ResponseWriter, data string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte(`{"ok":true,"data":` + data + `}`)); err != nil {
		t.Error(err)
	}
}

// The public cgo-free surface must express a 38-digit DECIMAL exactly, with no
// float64 anywhere on the path.
func TestServerSQLConsumerDecimalIsExact(t *testing.T) {
	var received map[string]json.RawMessage
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/sql" {
			t.Errorf("path = %q", request.URL.Path)
		}
		var envelope struct {
			Params map[string]json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil {
			t.Error(err)
			return
		}
		received = envelope.Params
		writeSQLTestData(t, w, `{"protocol_version":1,"columns":["amount"],"rows":[[{"Decimal":"`+sqlConsumerMaxText+`"}]]}`)
	}))
	defer httpServer.Close()

	client := newSQLConsumerClient(t, httpServer.URL, true)

	amount, err := server.ParseDecimal(sqlConsumerMaxText)
	if err != nil {
		t.Fatal(err)
	}
	if amount.Coefficient().Cmp(sqlConsumerMaxDecimal) != 0 || amount.Scale() != 4 {
		t.Fatalf("parsed = %s/%d", amount.Coefficient(), amount.Scale())
	}
	if amount.Text() != sqlConsumerMaxText {
		t.Fatalf("canonical text = %q", amount.Text())
	}

	value, err := server.DecimalValue(amount)
	if err != nil {
		t.Fatal(err)
	}
	request, err := server.NewQueryRequest("SELECT amount FROM ledger WHERE id = ?", value)
	if err != nil {
		t.Fatal(err)
	}
	if request.Version() != server.ServerSQLVersion {
		t.Fatalf("request version = %d", request.Version())
	}

	result, err := client.Query(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if string(received["protocol_version"]) != "1" {
		t.Fatalf("protocol_version = %s", received["protocol_version"])
	}
	if string(received["bind"]) != `[{"Decimal":"`+sqlConsumerMaxText+`"}]` {
		t.Fatalf("bind = %s", received["bind"])
	}

	decoded, ok := result.Decimal(0, 0)
	if !ok {
		t.Fatal("cell is not a Decimal")
	}
	if decoded.Coefficient().Cmp(sqlConsumerMaxDecimal) != 0 || decoded.Scale() != 4 {
		t.Fatalf("decoded = %s/%d", decoded.Coefficient(), decoded.Scale())
	}
	if _, isFloat := result.Rows[0][0].Float64(); isFloat {
		t.Fatal("a DECIMAL cell must never project to float64")
	}
	if result.Version != server.ServerSQLVersion || len(result.Columns) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

// Out-of-range input is rejected locally, and the transport is never reached.
func TestServerSQLConsumerOutOfRangeIsRejected(t *testing.T) {
	requests := 0
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		writeSQLTestData(t, w, `{"protocol_version":1,"rows":[]}`)
	}))
	defer httpServer.Close()

	client := newSQLConsumerClient(t, httpServer.URL, true)
	if _, err := server.ParseDecimal(strings.Repeat("9", 39)); server.ErrorCodeOf(err) != server.CodeInvalidArgument {
		t.Fatalf("39-digit parse error = %v, code = %q", err, server.ErrorCodeOf(err))
	}
	overflow := new(big.Int).Add(sqlConsumerMaxDecimal, big.NewInt(1))
	if _, err := server.NewDecimal(overflow, 0); server.ErrorCodeOf(err) != server.CodeInvalidArgument {
		t.Fatalf("39-digit coefficient error = %v", err)
	}
	if _, err := server.NewDecimal(big.NewInt(1), 39); server.ErrorCodeOf(err) != server.CodeInvalidArgument {
		t.Fatalf("scale 39 error = %v", err)
	}
	// A zero QueryRequest carries no validated statement and is refused locally.
	if _, err := client.Query(context.Background(), server.QueryRequest{}); server.ErrorCodeOf(err) != server.CodeInvalidArgument {
		t.Fatalf("zero request error = %v, code = %q", err, server.ErrorCodeOf(err))
	}
	if requests != 0 {
		t.Fatalf("rejected input must not reach the network, saw %d requests", requests)
	}
}

// The surface is gated: without an attestation it fails closed and never
// contacts the endpoint, even though the transport is fully configured.
func TestServerSQLConsumerFailsClosedWhenGated(t *testing.T) {
	requests := 0
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		writeSQLTestData(t, w, `{"protocol_version":1,"rows":[]}`)
	}))
	defer httpServer.Close()

	static := server.ServerSQLCapability()
	if static.Status != server.ServerSQLStatusGated || static.Name != server.ServerSQLDecimalCapability {
		t.Fatalf("static capability = %#v", static)
	}

	client := newSQLConsumerClient(t, httpServer.URL, false)
	if gate := client.SQLCapability(); gate.Status != server.ServerSQLStatusGated {
		t.Fatalf("client capability = %#v", gate)
	}
	request, err := server.NewQueryRequest("SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Query(context.Background(), request); server.ErrorCodeOf(err) != server.CodeCapabilityUnavailable {
		t.Fatalf("gated query error = %v, code = %q", err, server.ErrorCodeOf(err))
	}
	if requests != 0 {
		t.Fatalf("a gated client must not reach the network, saw %d requests", requests)
	}

	// An attested client reports available for itself only.
	attested := newSQLConsumerClient(t, httpServer.URL, true)
	if gate := attested.SQLCapability(); gate.Status != server.ServerSQLStatusAvailable || gate.ReleaseTag != "v1.2.3" {
		t.Fatalf("attested capability = %#v", gate)
	}
}

// A remote Server that does not echo the payload version does not attest the
// versioned surface, so the client refuses its response.
func TestServerSQLConsumerRequiresVersionEcho(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeSQLTestData(t, w, `{"rows":[]}`)
	}))
	defer httpServer.Close()

	client := newSQLConsumerClient(t, httpServer.URL, true)
	request, err := server.NewQueryRequest("SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Query(context.Background(), request); server.ErrorCodeOf(err) != server.CodeResultIndeterminate || !errors.Is(err, &server.TalonError{Code: server.CodeProtocolViolation}) {
		t.Fatalf("error = %v, code = %q", err, server.ErrorCodeOf(err))
	}
}

// The consumer surface must be reachable through the public interface set, so
// the compile-time contract stays honest.
type publicSQLClient interface {
	Query(context.Context, server.QueryRequest) (server.QueryResult, error)
	SQLCapability() server.ServerSQLGate
}

var _ publicSQLClient = (*server.ServerClient)(nil)
