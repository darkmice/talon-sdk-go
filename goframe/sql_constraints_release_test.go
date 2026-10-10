package goframe

import (
	"context"
	"os"
	"testing"

	talon "github.com/darkmice/talon-sdk-go"
	"github.com/gogf/gf/v2/database/gdb"
)

func TestGoFrameSQLConstraintRelease(t *testing.T) {
	if os.Getenv("TALON_TEST_EMBEDDED_NATIVE") != "1" {
		t.Skip("requires signed embedded runtime")
	}
	ctx := context.Background()
	db, e := gdb.New(gdb.ConfigNode{Type: DriverName, Name: t.TempDir()})
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConnCount(1)
	exec := func(s string, p ...interface{}) {
		t.Helper()
		if _, e := db.Exec(ctx, s, p...); e != nil {
			t.Fatalf("%s: %v", s, e)
		}
	}
	reject := func(s string, p ...interface{}) {
		t.Helper()
		_, e := db.Exec(ctx, s, p...)
		if e == nil || talon.NativeCodeOf(e) != "sql_exec_error" {
			t.Fatalf("expected UNIQUE rejection %s: %v", s, e)
		}
	}
	exec("CREATE TABLE release_gf (id INT PRIMARY KEY, checked_at INT, constraint_id TEXT, UNIQUE(checked_at,constraint_id))")
	exec("INSERT INTO release_gf VALUES (?,?,?)", 1, 11, "scope")
	reject("INSERT INTO release_gf VALUES (?,?,?)", 2, 11, "scope")
	exec("INSERT INTO release_gf VALUES (2,22,'other')")
	reject("UPDATE release_gf SET checked_at=?,constraint_id=? WHERE id=?", 11, "scope", 2)
	reject("INSERT INTO release_gf VALUES (3,33,'third'),(4,33,'third')")
	row, e := db.Model("release_gf").Ctx(ctx).Where("id", 1).One()
	if e != nil || row["checked_at"].Int() != 11 {
		t.Fatalf("model readback %v: %v", row, e)
	}
	count, e := db.Model("release_gf").Ctx(ctx).Count()
	if e != nil || count != 2 {
		t.Fatalf("atomic rows %d: %v", count, e)
	}
	tx, e := db.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec("DELETE FROM release_gf WHERE id=?", 1); e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec("INSERT INTO release_gf VALUES (?,?,?)", 3, 11, "scope"); e != nil {
		t.Fatal(e)
	}
	if e = tx.Rollback(); e != nil {
		t.Fatal(e)
	}
	reject("INSERT INTO release_gf VALUES (?,?,?)", 4, 11, "scope")
}
