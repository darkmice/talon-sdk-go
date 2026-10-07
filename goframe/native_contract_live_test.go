package goframe

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	talon "github.com/darkmice/talon-sdk-go"
	"github.com/gogf/gf/v2/database/gdb"
)

// Opt-in acceptance contracts: known-broken artifacts must fail, never skip or
// bless their wrong answers. Each route uses the same bound SQL on its own DB.
func requireNativeContract(t *testing.T) {
	t.Helper()
	if os.Getenv("TALON_TEST_EMBEDDED_NATIVE") != "1" && os.Getenv("TALON_GOFRAME_LIVE_DB") == "" {
		t.Skip("requires signed native runtime; set TALON_TEST_EMBEDDED_NATIVE=1 or use local signed fixture")
	}
}

type contractSQL struct {
	tables   func() ([]string, error)
	exec     func(string, ...talon.Value) error
	query    func(string, ...talon.Value) ([]string, error)
	begin    func() error
	commit   func() error
	rollback func() error
	close    func()
}

func openContractSQL(t *testing.T, route, path string) contractSQL {
	t.Helper()
	ctx := context.Background()
	if route == "goframe" {
		db, err := gdb.New(gdb.ConfigNode{Type: DriverName, Name: path})
		if err != nil {
			t.Fatal(err)
		}
		var tx gdb.TX
		args := func(values []talon.Value) []interface{} {
			out := make([]interface{}, len(values))
			for i := range values {
				out[i] = values[i]
			}
			return out
		}
		return contractSQL{
			tables: func() ([]string, error) { return db.Tables(ctx) },
			exec: func(s string, values ...talon.Value) error {
				var err error
				if tx != nil {
					_, err = tx.Exec(s, args(values)...)
				} else {
					_, err = db.Exec(ctx, s, args(values)...)
				}
				return err
			},
			query: func(s string, values ...talon.Value) ([]string, error) {
				var rows gdb.Result
				var err error
				if tx != nil {
					rows, err = tx.GetAll(s, args(values)...)
				} else {
					rows, err = db.GetAll(ctx, s, args(values)...)
				}
				if err != nil {
					return nil, err
				}
				out := make([]string, len(rows))
				for i, row := range rows {
					if len(row) != 1 {
						return nil, fmt.Errorf("expected one column, got %d", len(row))
					}
					for _, value := range row {
						out[i] = value.String()
					}
				}
				return out, nil
			},
			begin:    func() error { var err error; tx, err = db.Begin(ctx); return err },
			commit:   func() error { err := tx.Commit(); tx = nil; return err },
			rollback: func() error { err := tx.Rollback(); tx = nil; return err },
			close: func() {
				if err := db.Close(ctx); err != nil {
					t.Error(err)
				}
			},
		}
	}
	db, err := talon.Open(path)
	if err != nil {
		t.Fatalf("signed native Open: %v", err)
	}
	info := db.NativeInfo()
	t.Logf("route=%s release=%s core=%s library_sha256=%s platform=%s", route, info.ReleaseTag, info.CoreCommit, info.LibrarySHA256, info.Platform)
	query := func(s string, values ...talon.Value) ([]talon.Row, error) {
		if route == "binary" {
			return db.Query(s, values...)
		}
		result, err := db.QueryResult(s, values...)
		return result.Rows, err
	}
	return contractSQL{
		tables: func() ([]string, error) {
			rows, err := db.Query("SHOW TABLES")
			out := make([]string, len(rows))
			for i, row := range rows {
				out[i] = row.Str(0)
			}
			return out, err
		},
		exec: func(s string, values ...talon.Value) error {
			if route == "binary" {
				return db.Exec(s, values...)
			}
			_, err := db.QueryResult(s, values...)
			return err
		},
		query: func(s string, values ...talon.Value) ([]string, error) {
			rows, err := query(s, values...)
			if err != nil {
				return nil, err
			}
			out := make([]string, len(rows))
			for i, row := range rows {
				if len(row) != 1 {
					return nil, fmt.Errorf("expected one column, got %d", len(row))
				}
				value, err := sqlValue(row[0])
				if err != nil {
					return nil, err
				}
				out[i] = fmt.Sprint(value)
			}
			return out, nil
		},
		begin:    func() error { return db.Exec("BEGIN") },
		commit:   func() error { return db.Exec("COMMIT") },
		rollback: func() error { return db.Exec("ROLLBACK") },
		close:    db.Close,
	}
}

func contractExec(t *testing.T, db contractSQL, s string, args ...talon.Value) {
	t.Helper()
	if err := db.exec(s, args...); err != nil {
		t.Fatalf("%s: %v", s, err)
	}
}
func contractRows(t *testing.T, db contractSQL, s string, args ...talon.Value) []string {
	t.Helper()
	rows, err := db.query(s, args...)
	if err != nil {
		t.Fatalf("%s: %v", s, err)
	}
	return rows
}
func contractText(t *testing.T, s string) talon.Value {
	t.Helper()
	value, err := talon.TextValue(s)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func assertContractRows(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("rows=%v, want %v", got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("row %d=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestNativeSQLConsumerReadYourWrites(t *testing.T) {
	requireNativeContract(t)
	for _, route := range []string{"binary", "result_v2", "goframe"} {
		t.Run(route, func(t *testing.T) {
			db := openContractSQL(t, route, t.TempDir())
			defer db.close()
			contractExec(t, db, "CREATE TABLE assignments (operator_id TEXT NOT NULL, role_id TEXT NOT NULL, PRIMARY KEY (operator_id, role_id))")
			contractExec(t, db, "CREATE INDEX assignments_role ON assignments(role_id)")
			contractExec(t, db, "CREATE INDEX assignments_operator ON assignments(operator_id)")
			contractExec(t, db, "CREATE TABLE receipts (operation_id TEXT PRIMARY KEY, role_id TEXT)")
			operator, role := contractText(t, "operator-a"), contractText(t, "role-a")
			if err := db.begin(); err != nil {
				t.Fatal(err)
			}
			contractExec(t, db, "INSERT INTO assignments VALUES (?, ?)", operator, role)
			contractExec(t, db, "INSERT INTO receipts VALUES (?, ?)", operator, role)
			scan := contractRows(t, db, "SELECT role_id FROM assignments")
			equality := contractRows(t, db, "SELECT role_id FROM assignments WHERE operator_id = ?", operator)
			indexed := contractRows(t, db, "SELECT role_id FROM assignments WHERE role_id = ?", role)
			t.Logf("composite scan=%v equality=%v indexed=%v", scan, equality, indexed)
			assertContractRows(t, scan, "role-a")
			assertContractRows(t, equality, "role-a")
			assertContractRows(t, indexed, "role-a")
			assertContractRows(t, contractRows(t, db, "SELECT role_id FROM receipts WHERE operation_id = ?", operator), "role-a")
			plan, err := db.query("EXPLAIN SELECT role_id FROM assignments WHERE operator_id = 'operator-a'")
			t.Logf("composite indexed equality plan=%v error=%v", plan, err)
			if err := db.commit(); err != nil {
				t.Fatal(err)
			}
			assertContractRows(t, contractRows(t, db, "SELECT role_id FROM assignments WHERE operator_id = ?", operator), "role-a")
			if err := db.begin(); err != nil {
				t.Fatal(err)
			}
			contractExec(t, db, "DELETE FROM assignments WHERE operator_id = ?", operator)
			assertContractRows(t, contractRows(t, db, "SELECT role_id FROM assignments"))
			assertContractRows(t, contractRows(t, db, "SELECT role_id FROM assignments WHERE operator_id = ?", operator))
			if err := db.rollback(); err != nil {
				t.Fatal(err)
			}
			assertContractRows(t, contractRows(t, db, "SELECT role_id FROM assignments WHERE operator_id = ?", operator), "role-a")
		})
	}
}

func TestNativeSQLConsumerDistinctAggregate(t *testing.T) {
	requireNativeContract(t)
	for _, route := range []string{"binary", "result_v2", "goframe"} {
		t.Run(route, func(t *testing.T) {
			db := openContractSQL(t, route, t.TempDir())
			defer db.close()
			contractExec(t, db, "CREATE TABLE assignments (operator_id TEXT, role_id TEXT, PRIMARY KEY(operator_id, role_id))")
			contractExec(t, db, "INSERT INTO assignments VALUES ('a', 'read'), ('a', 'write'), ('b', 'read')")
			assertContractRows(t, contractRows(t, db, "SELECT COUNT(*) FROM assignments"), "3")
			assertContractRows(t, contractRows(t, db, "SELECT DISTINCT operator_id FROM assignments ORDER BY operator_id"), "a", "b")
			assertContractRows(t, contractRows(t, db, "SELECT COUNT(DISTINCT operator_id) FROM assignments"), "2")
		})
	}
}

func TestNativeSQLConsumerExactValues(t *testing.T) {
	requireNativeContract(t)
	for _, route := range []string{"binary", "result_v2", "goframe"} {
		t.Run(route, func(t *testing.T) {
			path := t.TempDir()
			db := openContractSQL(t, route, path)
			defer func() { db.close() }()
			contractExec(t, db, "CREATE TABLE exact_values (id INTEGER PRIMARY KEY, amount DECIMAL(18,2), nanos INTEGER, big INTEGER)")
			coefficient, _ := new(big.Int).SetString("123456789012345678", 10)
			money, err := talon.DecimalValue(coefficient, 2)
			if err != nil {
				t.Fatal(err)
			}
			nanos := int64(1791062400123456789)
			if err := db.begin(); err != nil {
				t.Fatal(err)
			}
			contractExec(t, db, "INSERT INTO exact_values VALUES (?, ?, ?, ?)", talon.IntegerValue(1), money, talon.IntegerValue(nanos), talon.IntegerValue(math.MaxInt64))
			for _, s := range []string{"SELECT amount FROM exact_values WHERE id = 1", "SELECT nanos FROM exact_values WHERE id = 1", "SELECT big FROM exact_values WHERE id = 1"} {
				want := "1234567890123456.78"
				if s == "SELECT nanos FROM exact_values WHERE id = 1" {
					want = fmt.Sprint(nanos)
				}
				if s == "SELECT big FROM exact_values WHERE id = 1" {
					want = fmt.Sprint(int64(math.MaxInt64))
				}
				assertContractRows(t, contractRows(t, db, s), want)
			}
			assertContractRows(t, contractRows(t, db, "SELECT amount FROM exact_values WHERE amount = ?", money), "1234567890123456.78")
			assertContractRows(t, contractRows(t, db, "SELECT id FROM exact_values WHERE nanos = ?", talon.IntegerValue(nanos)), "1")
			assertContractRows(t, contractRows(t, db, "SELECT id FROM exact_values WHERE big = ?", talon.IntegerValue(math.MaxInt64)), "1")
			negative, err := talon.DecimalValue(big.NewInt(-1), 2)
			if err != nil {
				t.Fatal(err)
			}
			contractExec(t, db, "INSERT INTO exact_values VALUES (?, ?, ?, ?)", talon.IntegerValue(2), negative, talon.IntegerValue(-nanos), talon.IntegerValue(math.MinInt64))
			assertContractRows(t, contractRows(t, db, "SELECT amount FROM exact_values WHERE id = 2"), "-0.01")
			assertContractRows(t, contractRows(t, db, "SELECT big FROM exact_values WHERE id = 2"), fmt.Sprint(int64(math.MinInt64)))
			if err := db.commit(); err != nil {
				t.Fatal(err)
			}
			db.close()
			db = openContractSQL(t, route, path)
			assertContractRows(t, contractRows(t, db, "SELECT amount FROM exact_values WHERE id = 1"), "1234567890123456.78")
			assertContractRows(t, contractRows(t, db, "SELECT nanos FROM exact_values WHERE id = 1"), fmt.Sprint(nanos))
			assertContractRows(t, contractRows(t, db, "SELECT big FROM exact_values WHERE id = 1"), fmt.Sprint(int64(math.MaxInt64)))
			assertContractRows(t, contractRows(t, db, "SELECT amount FROM exact_values WHERE id = 2"), "-0.01")
			assertContractRows(t, contractRows(t, db, "SELECT big FROM exact_values WHERE id = 2"), fmt.Sprint(int64(math.MinInt64)))
		})
	}
}

// A new process must see schema and committed rows even if the previous process
// exited without Close. Closing/reopening handles in one process is insufficient.
func TestNativeSQLConsumerCommitRestart(t *testing.T) {
	requireNativeContract(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"binary", "result_v2", "goframe"} {
		for _, mode := range []string{"close", "exit"} {
			t.Run(route+"/"+mode, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "db")
				for _, phase := range []string{"write", "read"} {
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					cmd := exec.CommandContext(ctx, executable, "-test.run=^TestNativeSQLConsumerRestartChild$", "-test.v")
					cmd.Env = append(os.Environ(), "TALON_SQL_RESTART_ROUTE="+route, "TALON_SQL_RESTART_MODE="+mode, "TALON_SQL_RESTART_PHASE="+phase, "TALON_SQL_RESTART_PATH="+path)
					output, err := cmd.CombinedOutput()
					cancel()
					t.Logf("%s: %s", phase, output)
					if err != nil {
						t.Fatalf("%s subprocess: %v", phase, err)
					}
				}
			})
		}
	}
}

func TestNativeSQLConsumerRestartChild(t *testing.T) {
	phase := os.Getenv("TALON_SQL_RESTART_PHASE")
	if phase == "" {
		t.Skip("subprocess helper")
	}
	db := openContractSQL(t, os.Getenv("TALON_SQL_RESTART_ROUTE"), os.Getenv("TALON_SQL_RESTART_PATH"))
	if phase == "write" {
		contractExec(t, db, "CREATE TABLE committed (id INTEGER PRIMARY KEY, value TEXT)")
		if err := db.begin(); err != nil {
			t.Fatal(err)
		}
		contractExec(t, db, "INSERT INTO committed VALUES (?, ?)", talon.IntegerValue(1), contractText(t, "durable"))
		if err := db.commit(); err != nil {
			t.Fatal(err)
		}
		if os.Getenv("TALON_SQL_RESTART_MODE") == "close" {
			db.close()
		}
		os.Exit(0)
	}
	defer db.close()
	tables, err := db.tables()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("restart tables=%v", tables)
	assertContractRows(t, tables, "committed")
	assertContractRows(t, contractRows(t, db, "SELECT value FROM committed WHERE id = 1"), "durable")
}

func assertNativeBusy(t *testing.T, err error) {
	t.Helper()
	if talon.NativeCodeOf(err) != "busy" || talon.ErrorCodeOf(err) != talon.CodeNativeUnclassified {
		t.Fatalf("expected typed busy refusal, got sdk=%q native=%q error=%v", talon.ErrorCodeOf(err), talon.NativeCodeOf(err), err)
	}
}

func TestNativeSQLConsumerSingleOwner(t *testing.T) {
	requireNativeContract(t)
	ctx := context.Background()
	path := t.TempDir()
	db, err := gdb.New(gdb.ConfigNode{Type: DriverName, Name: path})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(ctx)
	peer, err := gdb.New(gdb.ConfigNode{Type: DriverName, Name: path})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close(ctx)
	if _, err := db.Exec(ctx, "CREATE TABLE ownership (id INTEGER PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO ownership VALUES (?, ?)", 1, "seed"); err != nil {
		t.Fatal(err)
	}
	seed, err := db.GetOne(ctx, "SELECT value FROM ownership WHERE id = ?", 1)
	if err != nil || seed["value"].String() != "seed" {
		t.Fatalf("control read=%v error=%v", seed, err)
	}
	abort := fmt.Errorf("rollback contract body")
	err = db.Transaction(ctx, func(_ context.Context, tx gdb.TX) error {
		if _, err := tx.Exec("INSERT INTO ownership VALUES (?, ?)", 2, "pending"); err != nil {
			return err
		}
		own, err := tx.GetOne("SELECT value FROM ownership WHERE id = ?", 2)
		if err != nil || own["value"].String() != "pending" {
			t.Fatalf("tx read=%v error=%v", own, err)
		}
		// Fresh context deliberately prevents GoFrame's TXFromCtx from turning this
		// into a nested savepoint or silently reusing the owner's transaction.
		if unexpected, err := peer.Begin(context.Background()); err == nil {
			_ = unexpected.Rollback()
			t.Fatal("second BEGIN admitted")
		} else {
			assertNativeBusy(t, err)
		}
		for _, handle := range []gdb.DB{peer, db} {
			_, err := handle.GetAll(context.Background(), "SELECT value FROM ownership WHERE id = ?", 1)
			assertNativeBusy(t, err)
			for _, s := range []string{"INSERT INTO ownership VALUES (3, 'escaped')", "UPDATE ownership SET value = 'escaped' WHERE id = 1", "DELETE FROM ownership WHERE id = 1"} {
				_, err := handle.Exec(context.Background(), s)
				assertNativeBusy(t, err)
			}
		}
		return abort
	})
	if !errors.Is(err, abort) {
		t.Fatalf("callback cause lost: %v", err)
	}
	rows, err := db.GetAll(ctx, "SELECT id, value FROM ownership ORDER BY id")
	if err != nil || len(rows) != 1 || rows[0]["id"].Int64() != 1 || rows[0]["value"].String() != "seed" {
		t.Fatalf("rollback/refused writes leaked: %v, %v", rows, err)
	}
	if err := peer.Transaction(context.Background(), func(_ context.Context, tx gdb.TX) error {
		_, err := tx.Exec("INSERT INTO ownership VALUES (4, 'next')")
		return err
	}); err != nil {
		t.Fatalf("owner not released: %v", err)
	}
	row, err := db.GetOne(ctx, "SELECT value FROM ownership WHERE id = 4")
	if err != nil || row["value"].String() != "next" {
		t.Fatalf("next commit invisible: %v, %v", row, err)
	}
}
