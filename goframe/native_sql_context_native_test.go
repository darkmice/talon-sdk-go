package goframe

import (
	"context"
	"errors"
	"fmt"
	talon "github.com/darkmice/talon-sdk-go"
	"github.com/gogf/gf/v2/database/gdb"
	"os"
	"strings"
	"testing"
	"time"
)

// This uses the ordinary SDK build and an ordinary Core, without any test seam.
func TestNativeSQLContextNormalLocalDevelopment(t *testing.T) {
	if os.Getenv("TALON_TEST_SQL_CONTEXT_NORMAL") != "1" {
		t.Skip("requires explicit ordinary local development Core with native_sql_context v1")
	}
	path := t.TempDir()
	peer, err := talon.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if err = peer.RequireCapability("native_sql_context"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := gdb.New(gdb.ConfigNode{Type: DriverName, Name: path})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	if _, err = db.Exec(ctx, "CREATE TABLE normal_context_receipt(id INT PRIMARY KEY, digest TEXT)"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO normal_context_receipt VALUES(1,'exact-normal-request')"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	rows, err := peer.QueryResultContext(ctx, "SELECT digest FROM normal_context_receipt WHERE id=?", talon.IntegerValue(1))
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0].Str(0) != "exact-normal-request" {
		t.Fatalf("ordinary GoFrame receipt %+v %v", rows, err)
	}
	// Root rollback cleanup must execute even with an already expired context.
	if err = peer.ExecContext(ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	if err = peer.ExecContext(ctx, "INSERT INTO normal_context_receipt VALUES(2,'pending')"); err != nil {
		t.Fatal(err)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if err = peer.SQLRollbackContext(expired); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired rollback cause: %v", err)
	}
	rows, err = peer.QueryResultContext(ctx, "SELECT id FROM normal_context_receipt WHERE id=2")
	if err != nil || len(rows.Rows) != 0 {
		t.Fatalf("rollback retained mutation %+v %v", rows, err)
	}
	if _, err = db.Exec(ctx, "INSERT INTO normal_context_receipt VALUES(3,'peer-after-cleanup')"); err != nil {
		t.Fatal(err)
	}
	info := peer.NativeInfo()
	if info.Admission != talon.NativeAdmissionLocalDevelopment || info.ReleaseTag != "" {
		t.Fatalf("incorrect release provenance %+v", info)
	}
	t.Logf("ordinary GoFrame context transaction, exact receipt, expired rollback, peer write passed; admission=%s library=%s core=%s dirty=%t", info.Admission, info.LibrarySHA256, info.CoreCommit, info.CoreGitDirty)
}

func TestNativeSQLContextNormalLargeScan(t *testing.T) {
	if os.Getenv("TALON_TEST_SQL_CONTEXT_NORMAL") != "1" {
		t.Skip("requires explicit ordinary context Core")
	}
	path := t.TempDir()
	db, err := talon.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.ExecContext(context.Background(), "CREATE TABLE physical_context_scan(id INT PRIMARY KEY, amount INT)"); err != nil {
		t.Fatal(err)
	}
	if err = db.ExecContext(context.Background(), "BEGIN"); err != nil {
		t.Fatal(err)
	}
	// Batched seed belongs to one explicit transaction; no test gate or sleep in SQL.
	for offset := 0; offset < 60000; offset += 1000 {
		var statement strings.Builder
		statement.WriteString("INSERT INTO physical_context_scan VALUES")
		for i := offset; i < offset+1000; i++ {
			if i != offset {
				statement.WriteByte(',')
			}
			fmt.Fprintf(&statement, "(%d,%d)", i, i%101)
		}
		if err = db.ExecContext(context.Background(), statement.String()); err != nil {
			t.Fatal(err)
		}
	}
	if err = db.ExecContext(context.Background(), "COMMIT"); err != nil {
		t.Fatal(err)
	}
	gf, err := gdb.New(gdb.ConfigNode{Type: DriverName, Name: path})
	if err != nil {
		t.Fatal(err)
	}
	defer gf.Close(context.Background())
	peer, err := talon.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	for _, route := range []string{"root", "goframe"} {
		t.Run(route, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
			defer cancel()
			start := time.Now()
			if route == "root" {
				_, err = db.QueryResultContext(ctx, "SELECT SUM(amount), COUNT(DISTINCT amount) FROM physical_context_scan")
			} else {
				_, err = gf.GetAll(ctx, "SELECT SUM(amount), COUNT(DISTINCT amount) FROM physical_context_scan")
			}
			elapsed := time.Since(start)
			if !errors.Is(err, context.DeadlineExceeded) || talon.NativeCodeOf(err) != "deadline_exceeded" {
				t.Fatalf("physical scan cancellation: elapsed=%s err=%v", elapsed, err)
			}
			// Native machine code proves this was not only the SDK entry ctx.Err check.
			if err = peer.ExecContext(context.Background(), "INSERT INTO physical_context_scan VALUES(?,?)", talon.IntegerValue(60000+int64(len(route))), talon.IntegerValue(1)); err != nil {
				t.Fatal(err)
			}
			t.Logf("ordinary no-seam 60000-row aggregate interrupted by native deadline; elapsed=%s; peer subsequent write passed", elapsed)
		})
	}
}
