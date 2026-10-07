//go:build talon_sql_context_test

package goframe

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	talon "github.com/darkmice/talon-sdk-go"
	"github.com/gogf/gf/v2/database/gdb"
	"os"
	"testing"
	"time"
)

func waitContextStage(t *testing.T, gate *talon.NativeSQLContextTestGate) {
	t.Helper()
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		entered, err := gate.Entered()
		if err != nil {
			t.Fatal(err)
		}
		if entered {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("native SQL did not enter armed execution stage")
}

type contextRoute struct {
	begin    func(context.Context) error
	read     func(context.Context) error
	write    func(context.Context, string) error
	commit   func() error
	rollback func() error
	close    func()
}

func openContextRoute(t *testing.T, route, path string) contextRoute {
	t.Helper()
	switch route {
	case "root":
		db, err := talon.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		var ctx context.Context
		return contextRoute{begin: func(c context.Context) error { ctx = c; return db.ExecContext(c, "BEGIN") }, read: func(c context.Context) error {
			_, e := db.QueryResultContext(c, "SELECT id FROM context_receipt")
			return e
		}, write: func(c context.Context, s string) error { return db.ExecContext(c, s) }, commit: func() error { return db.ExecContext(ctx, "COMMIT") }, rollback: func() error { return db.SQLRollbackContext(ctx) }, close: db.Close}
	case "driver":
		raw, err := nativeConnector{path: path}.Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		conn := raw.(*nativeConn)
		var tx driver.Tx
		return contextRoute{begin: func(c context.Context) error { var e error; tx, e = conn.BeginTx(c, driver.TxOptions{}); return e }, read: func(c context.Context) error {
			rows, e := conn.QueryContext(c, "SELECT id FROM context_receipt", nil)
			if rows != nil {
				rows.Close()
			}
			return e
		}, write: func(c context.Context, s string) error { _, e := conn.ExecContext(c, s, nil); return e }, commit: func() error { return tx.Commit() }, rollback: func() error { return tx.Rollback() }, close: func() { conn.Close() }}
	case "database_sql":
		db := sql.OpenDB(nativeConnector{path: path})
		db.SetMaxOpenConns(1)
		var tx *sql.Tx
		return contextRoute{begin: func(c context.Context) error { var e error; tx, e = db.BeginTx(c, nil); return e }, read: func(c context.Context) error {
			var rows *sql.Rows
			var e error
			if tx == nil {
				rows, e = db.QueryContext(c, "SELECT id FROM context_receipt")
			} else {
				rows, e = tx.QueryContext(c, "SELECT id FROM context_receipt")
			}
			if rows != nil {
				rows.Close()
			}
			return e
		}, write: func(c context.Context, s string) error {
			var e error
			if tx == nil {
				_, e = db.ExecContext(c, s)
			} else {
				_, e = tx.ExecContext(c, s)
			}
			return e
		}, commit: func() error { return tx.Commit() }, rollback: func() error { return tx.Rollback() }, close: func() {
			if tx != nil {
				tx.Rollback()
			}
			db.Close()
		}}
	case "goframe":
		db, err := gdb.New(gdb.ConfigNode{Type: DriverName, Name: path})
		if err != nil {
			t.Fatal(err)
		}
		var tx gdb.TX
		return contextRoute{begin: func(c context.Context) error { var e error; tx, e = db.Begin(c); return e }, read: func(c context.Context) error {
			var e error
			if tx == nil {
				_, e = db.GetAll(c, "SELECT id FROM context_receipt")
			} else {
				_, e = tx.GetAll("SELECT id FROM context_receipt")
			}
			return e
		}, write: func(c context.Context, s string) error {
			var e error
			if tx == nil {
				_, e = db.Exec(c, s)
			} else {
				_, e = tx.Exec(s)
			}
			return e
		}, commit: func() error { return tx.Commit() }, rollback: func() error { return tx.Rollback() }, close: func() {
			if tx != nil {
				tx.Rollback()
			}
			db.Close(context.Background())
		}}
	}
	t.Fatalf("unknown route %s", route)
	return contextRoute{}
}

func TestNativeSQLContextEnteredCancellation(t *testing.T) {
	if os.Getenv("TALON_TEST_SQL_CONTEXT") != "1" {
		t.Skip("requires explicit separate native SQL context test-seam artifact")
	}
	// database/sql can consume a tx before our rollback callback after cancellation;
	// rollback inside driver/Core is tested directly rather than racing awaitDone.
	for _, route := range []string{"root", "driver", "database_sql", "goframe"} {
		for _, stage := range []uint32{1, 2, 3, 4, 5} {
			if stage == 5 && (route == "database_sql" || route == "goframe") {
				continue
			}
			for _, mode := range []string{"cancel", "deadline"} {
				t.Run(fmt.Sprintf("%s/stage%d/%s", route, stage, mode), func(t *testing.T) {
					path := t.TempDir()
					peer, err := talon.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					if err = peer.Exec("CREATE TABLE context_receipt(id INT PRIMARY KEY, request_digest TEXT)"); err != nil {
						t.Fatal(err)
					}
					if err = peer.Exec("INSERT INTO context_receipt VALUES(1,'seed')"); err != nil {
						t.Fatal(err)
					}
					target := openContextRoute(t, route, path)
					defer target.close()
					var base context.Context
					var cancel context.CancelFunc
					if mode == "deadline" {
						base, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond)
					} else {
						base, cancel = context.WithCancel(context.Background())
					}
					defer cancel()
					ctx, gate := talon.NativeSQLContextTestContext(base, stage)
					defer gate.Release()
					if stage != 1 {
						if err = target.begin(ctx); err != nil {
							t.Fatal(err)
						}
					}
					if stage == 2 || stage == 3 || stage == 4 || stage == 5 {
						if err = target.write(ctx, "INSERT INTO context_receipt VALUES(2,'exact-request-2')"); err != nil {
							t.Fatal(err)
						}
					}
					result := make(chan error, 1)
					go func() {
						var e error
						switch stage {
						case 1:
							e = target.begin(ctx)
						case 2:
							e = target.read(ctx)
						case 3, 4:
							e = target.commit()
						case 5:
							e = target.rollback()
						}
						result <- e
					}()
					waitContextStage(t, gate)
					if mode == "cancel" {
						cancel()
					} else {
						<-base.Done()
					}
					// The gate models an uninterruptible segment, after native entry. Deadline
					// cannot release caller ownership or fabricate rollback while it is held.
					select {
					case e := <-result:
						t.Fatalf("returned while native still held execution: %v", e)
					case <-time.After(25 * time.Millisecond):
					}
					// Another session cannot bypass the still-running native critical section.
					if stage != 5 {
						blocked, bCancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
						_, e := peer.QueryResultContext(blocked, "SELECT id FROM context_receipt")
						bCancel()
						if !errors.Is(e, context.DeadlineExceeded) {
							t.Fatalf("peer bypassed native lock: %v", e)
						}
					}
					gate.Release()
					select {
					case err = <-result:
					case <-time.After(5 * time.Second):
						t.Fatal("native did not finish after gate release")
					}
					expected := context.Canceled
					if mode == "deadline" {
						expected = context.DeadlineExceeded
					}
					if !errors.Is(err, expected) {
						t.Fatalf("lost interruption cause: %v", err)
					}
					if stage == 4 {
						if talon.ErrorCodeOf(err) != talon.CodeResultIndeterminate {
							t.Fatalf("COMMIT post-application must be unknown: %v", err)
						}
					} else if talon.ErrorCodeOf(err) == talon.CodeResultIndeterminate {
						t.Fatalf("pre-application/rollback incorrectly unknown: %v", err)
					}
					// Read the original complete receipt identity; never replay the request.
					rows, e := peer.QueryResultContext(context.Background(), "SELECT request_digest FROM context_receipt WHERE id = ?", talon.IntegerValue(2))
					if e != nil {
						t.Fatal(e)
					}
					want := 0
					if stage == 4 {
						want = 1
					}
					if len(rows.Rows) != want {
						t.Fatalf("exact receipt rows=%d expected=%d: %+v", len(rows.Rows), want, rows)
					}
					if want == 1 && rows.Rows[0].Str(0) != "exact-request-2" {
						t.Fatal("receipt identity changed")
					}
					if e = peer.ExecContext(context.Background(), "INSERT INTO context_receipt VALUES(3,'subsequent-peer-write')"); e != nil {
						t.Fatalf("transaction slot/lock leaked: %v", e)
					}
					t.Logf("entered stage=%d mode=%s cause=%s outcome=%s receipt_rows=%d; held native until release; peer write succeeded", stage, mode, talon.NativeCodeOf(err), talon.ErrorCodeOf(err), want)
				})
			}
		}
	}
}

func TestNativeSQLContextPositiveTransaction(t *testing.T) {
	if os.Getenv("TALON_TEST_SQL_CONTEXT") != "1" {
		t.Skip("requires explicit native context artifact")
	}
	for _, route := range []string{"root", "driver", "database_sql", "goframe"} {
		t.Run(route, func(t *testing.T) {
			path := t.TempDir()
			db, err := talon.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err = db.Exec("CREATE TABLE context_receipt(id INT PRIMARY KEY, request_digest TEXT)"); err != nil {
				t.Fatal(err)
			}
			target := openContextRoute(t, route, path)
			defer target.close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err = target.begin(ctx); err != nil {
				t.Fatal(err)
			}
			if err = target.write(ctx, "INSERT INTO context_receipt VALUES(1,'confirmed')"); err != nil {
				t.Fatal(err)
			}
			if err = target.commit(); err != nil {
				t.Fatal(err)
			}
			rows, err := db.QueryResultContext(ctx, "SELECT request_digest FROM context_receipt WHERE id=1")
			if err != nil || len(rows.Rows) != 1 || rows.Rows[0].Str(0) != "confirmed" {
				t.Fatalf("confirmed receipt %+v %v", rows, err)
			}
		})
	}
}
