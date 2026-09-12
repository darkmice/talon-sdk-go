/*
 * Copyright 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE in the project root for full license information.
 */

package server_test

import (
	"context"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"

	serverapi "github.com/darkmice/talon-sdk-go/server"
)

// Opt-in live-server end-to-end check for the versioned Server SQL/DECIMAL
// surface.
//
// By default the test is skipped: it needs a real Talon Server process, and the
// unit/protocol suites must stay hermetic. To run it:
//
//	TALON_SERVER_URL=http://127.0.0.1:7720 TALON_SERVER_TOKEN=<token> \
//	TALON_SERVER_SQL_RELEASE_TAG=vX.Y.Z \
//	TALON_SERVER_SQL_TALON_BIN_COMMIT=<40-hex> \
//	TALON_SERVER_SQL_CORE_COMMIT=<40-hex> \
//	TALON_SERVER_SQL_ARTIFACT_SHA256=<64-hex> \
//	    CGO_ENABLED=0 go test ./server/ -run TestServerSQLLive -v
//
// What it proves that the hermetic suites cannot: the cgo-free client and a real
// Core server agree byte-for-byte on the wire contract --- canonical decimal
// text, protocol_version echo, exact aggregation, and local fail-closed on
// out-of-range input.
func TestServerSQLLiveDecimalRoundTrip(t *testing.T) {
	base := os.Getenv("TALON_SERVER_URL")
	if strings.TrimSpace(base) == "" {
		t.Skip("set TALON_SERVER_URL to run the live Server SQL end-to-end check")
	}
	token := os.Getenv("TALON_SERVER_TOKEN")
	if strings.TrimSpace(token) == "" {
		t.Skip("set TALON_SERVER_TOKEN to run the live Server SQL end-to-end check")
	}
	releaseTag := os.Getenv("TALON_SERVER_SQL_RELEASE_TAG")
	talonBinCommit := os.Getenv("TALON_SERVER_SQL_TALON_BIN_COMMIT")
	coreCommit := os.Getenv("TALON_SERVER_SQL_CORE_COMMIT")
	artifactSHA256 := os.Getenv("TALON_SERVER_SQL_ARTIFACT_SHA256")
	if releaseTag == "" || talonBinCommit == "" || coreCommit == "" || artifactSHA256 == "" {
		t.Skip("set the four TALON_SERVER_SQL_* artifact identity variables to run the attested live check")
	}

	// 38 significant digits at scale 4: 10^38 - 1 rendered as a fixed point.
	const (
		maxText   = "9999999999999999999999999999999999.9999"
		negText   = "-1.2300"
		wantSum   = "9999999999999999999999999999999998.7699"
		liveTable = "sdk_live_dec"
	)

	// The surface stays gated until it is attested, even against a live server.
	gated, err := serverapi.NewServerClient(serverapi.ServerClientConfig{
		BaseURL: base,
		Token:   token,
		Timeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewServerClient (gated): %v", err)
	}
	if got := gated.SQLCapability(); got.Status != serverapi.ServerSQLStatusGated {
		t.Fatalf("gate status = %q, want %q", got.Status, serverapi.ServerSQLStatusGated)
	}
	req, err := serverapi.NewQueryRequest("SELECT id FROM " + liveTable)
	if err != nil {
		t.Fatalf("NewQueryRequest: %v", err)
	}
	if _, err := gated.Query(context.Background(), req); err == nil || !strings.Contains(err.Error(), "gated") {
		t.Fatalf("gated Query error = %v, want a gated capability failure", err)
	}

	client, err := serverapi.NewServerClient(serverapi.ServerClientConfig{
		BaseURL: base,
		Token:   token,
		Timeout: 15 * time.Second,
		ServerSQLAttestation: &serverapi.ServerSQLAttestation{
			Capability:     serverapi.ServerSQLDecimalCapability,
			Version:        serverapi.ServerSQLVersion,
			ReleaseTag:     releaseTag,
			TalonBinCommit: talonBinCommit,
			CoreCommit:     coreCommit,
			ArtifactSHA256: artifactSHA256,
		},
	})
	if err != nil {
		t.Fatalf("NewServerClient (attested): %v", err)
	}
	if got := client.SQLCapability(); got.Status != serverapi.ServerSQLStatusAvailable {
		t.Fatalf("attested gate status = %q, want %q", got.Status, serverapi.ServerSQLStatusAvailable)
	}

	ctx := context.Background()
	exec := func(sql string, params ...serverapi.Value) serverapi.QueryResult {
		t.Helper()
		request, err := serverapi.NewQueryRequest(sql, params...)
		if err != nil {
			t.Fatalf("NewQueryRequest(%q): %v", sql, err)
		}
		result, err := client.Query(ctx, request)
		if err != nil {
			t.Fatalf("Query(%q): %v", sql, err)
		}
		return result
	}

	exec("DROP TABLE IF EXISTS " + liveTable)
	exec("CREATE TABLE " + liveTable + " (id INTEGER PRIMARY KEY, d DECIMAL(38,4))")

	maxDecimal, err := serverapi.ParseDecimal(maxText)
	if err != nil {
		t.Fatalf("ParseDecimal(%q): %v", maxText, err)
	}
	negDecimal, err := serverapi.ParseDecimal(negText)
	if err != nil {
		t.Fatalf("ParseDecimal(%q): %v", negText, err)
	}
	maxValue, err := serverapi.DecimalValue(maxDecimal)
	if err != nil {
		t.Fatalf("DecimalValue: %v", err)
	}
	negValue, err := serverapi.DecimalValue(negDecimal)
	if err != nil {
		t.Fatalf("DecimalValue: %v", err)
	}
	exec("INSERT INTO "+liveTable+" (id, d) VALUES (?, ?)", serverapi.IntegerValue(1), maxValue)
	exec("INSERT INTO "+liveTable+" (id, d) VALUES (?, ?)", serverapi.IntegerValue(2), negValue)

	selected := exec("SELECT id, d FROM " + liveTable + " ORDER BY id")
	if len(selected.Rows) != 2 {
		t.Fatalf("selected rows = %d, want 2", len(selected.Rows))
	}
	if selected.Version != serverapi.ServerSQLVersion {
		t.Fatalf("result version = %d, want %d", selected.Version, serverapi.ServerSQLVersion)
	}

	got, ok := selected.Decimal(0, 1)
	if !ok {
		t.Fatal("row 0 column 1 is not a Decimal")
	}
	if got.Text() != maxText {
		t.Errorf("38-digit round trip = %q, want %q", got.Text(), maxText)
	}

	gotNeg, ok := selected.Decimal(1, 1)
	if !ok {
		t.Fatal("row 1 column 1 is not a Decimal")
	}
	if gotNeg.Text() != negText {
		t.Errorf("negative/scale round trip = %q, want %q", gotNeg.Text(), negText)
	}

	summed := exec("SELECT SUM(d) FROM " + liveTable)
	sum, ok := summed.Decimal(0, 0)
	if !ok {
		t.Fatal("SUM(d) is not a Decimal")
	}
	if sum.Text() != wantSum {
		t.Errorf("SUM(d) = %q, want %q", sum.Text(), wantSum)
	}

	// Out-of-range input is rejected locally, never degraded to float64 and
	// never put on the wire.
	tooBig := new(big.Int)
	tooBig.SetString(strings.Repeat("9", 39), 10)
	if _, err := serverapi.NewDecimal(tooBig, 0); err == nil {
		t.Error("NewDecimal accepted a 39-digit coefficient")
	}
	if _, err := serverapi.NewDecimal(big.NewInt(1), 39); err == nil {
		t.Error("NewDecimal accepted scale 39")
	}
	if _, err := serverapi.ParseDecimal("1e10"); err == nil {
		t.Error("ParseDecimal accepted exponent notation")
	}

	exec("DROP TABLE " + liveTable)
}
