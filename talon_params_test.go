/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package talon

import (
	"encoding/json"
	"math"
	"sync"
	"testing"
)

func TestQueryParams_BindsWithoutSQLInterpolation(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if err := db.ExecParams("CREATE TABLE signed_snapshots (id INTEGER, catalog_id TEXT, payload TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	maliciousText := "catalog'); DROP TABLE signed_snapshots; --"
	payload := `{"catalog_version":"1","offerings":[]}`
	if err := db.ExecParams(
		"INSERT INTO signed_snapshots (id, catalog_id, payload) VALUES (?, ?, ?)",
		int64(1), maliciousText, payload,
	); err != nil {
		t.Fatalf("insert params: %v", err)
	}

	rows, err := db.QueryParams("SELECT id, catalog_id FROM signed_snapshots WHERE id = ?", int64(1))
	if err != nil {
		t.Fatalf("query params: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	id, err := rows[0].GetInt64(0)
	if err != nil || id != 1 {
		t.Fatalf("id = %d, err = %v", id, err)
	}
	catalogID, err := rows[0].GetString(1)
	if err != nil || catalogID != maliciousText {
		t.Fatalf("catalog_id = %q, err = %v", catalogID, err)
	}
	// If the payload had been interpolated, the injected DROP would remove the
	// table. A second query proves it remained data.
	if _, err := db.QueryParams("SELECT id FROM signed_snapshots"); err != nil {
		t.Fatalf("table did not survive bound text: %v", err)
	}
}

func TestQueryParams_BindsAfterLiteralInsert(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if _, err := db.SQL("CREATE TABLE query_bind_probe (id INTEGER, label TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.SQL("INSERT INTO query_bind_probe VALUES (7, 'literal')"); err != nil {
		t.Fatalf("literal insert: %v", err)
	}

	rows, err := db.QueryParams(
		"SELECT label FROM query_bind_probe WHERE id = ?",
		int64(7),
	)
	if err != nil {
		t.Fatalf("query params: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	label, err := rows[0].GetString(0)
	if err != nil || label != "literal" {
		t.Fatalf("label = %q, err = %v", label, err)
	}
}

func TestQueryParams_RejectsUnsupportedAndNonFiniteValues(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	tests := []struct {
		name  string
		value interface{}
	}{
		{name: "struct", value: struct{ Value string }{Value: "x"}},
		{name: "uint overflow", value: uint64(math.MaxUint64)},
		{name: "NaN", value: math.NaN()},
		{name: "invalid JSON", value: json.RawMessage(`{"broken":`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := db.ExecParams("SELECT ?", test.value)
			if ErrorCodeOf(err) != ErrorInvalidArgument {
				t.Fatalf("ErrorCodeOf(%v) = %q, want %q", err, ErrorCodeOf(err), ErrorInvalidArgument)
			}
		})
	}
}

func TestDecodeCell_StrictProtocolFailures(t *testing.T) {
	tests := []json.RawMessage{
		json.RawMessage(`{"Unknown":"value"}`),
		json.RawMessage(`{"Integer":1.5}`),
		json.RawMessage(`{"Integer":1,"Text":"ambiguous"}`),
		json.RawMessage(`"not-a-null"`),
		json.RawMessage(`null`),
	}
	for _, raw := range tests {
		if value, err := decodeCell(raw); err == nil {
			t.Fatalf("decodeCell(%s) = %#v, want error", raw, value)
		}
	}
}

func TestErrorCodeOf_ClosedAndEngine(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := db.QueryParams("THIS IS NOT SQL"); ErrorCodeOf(err) != ErrorEngine {
		t.Fatalf("invalid SQL code = %q, err = %v", ErrorCodeOf(err), err)
	}
	db.Close()
	if _, err := db.QueryParams("SELECT 1"); ErrorCodeOf(err) != ErrorClosed {
		t.Fatalf("closed DB code = %q, err = %v", ErrorCodeOf(err), err)
	}
}

func TestRow_StrictAccessors(t *testing.T) {
	row := Row{"text", int64(7), true, nil}
	if _, err := row.GetInt64(0); ErrorCodeOf(err) != ErrorProtocol {
		t.Fatalf("wrong type code = %q, err = %v", ErrorCodeOf(err), err)
	}
	if _, err := row.Value(9); ErrorCodeOf(err) != ErrorInvalidArgument {
		t.Fatalf("out of bounds code = %q, err = %v", ErrorCodeOf(err), err)
	}
}

func TestConcurrentCloseAndCalls(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.ExecParams("CREATE TABLE close_probe (id INTEGER, label TEXT)"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := db.ExecParams("INSERT INTO close_probe VALUES (?, ?)", int64(1), "safe"); err != nil {
		t.Fatalf("insert: %v", err)
	}

	start := make(chan struct{})
	errors := make(chan error, 64)
	var callers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			for attempt := 0; attempt < 32; attempt++ {
				rows, callErr := db.QueryParams("SELECT label FROM close_probe WHERE id = ?", int64(1))
				if callErr != nil {
					if ErrorCodeOf(callErr) != ErrorClosed {
						errors <- callErr
					}
					return
				}
				if len(rows) != 1 {
					errors <- operationError(ErrorProtocol, "test", "unexpected row count", nil)
					return
				}
			}
		}()
	}
	close(start)
	db.Close()
	callers.Wait()
	close(errors)
	for callErr := range errors {
		t.Errorf("concurrent call returned unexpected error: %v", callErr)
	}

	// Close is idempotent, and every operation after it fails before entering C.
	db.Close()
	if _, err := db.QueryParams("SELECT 1"); ErrorCodeOf(err) != ErrorClosed {
		t.Fatalf("post-close query code = %q, err = %v", ErrorCodeOf(err), err)
	}
}
