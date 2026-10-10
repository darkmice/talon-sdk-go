package talon

import (
	"os"
	"strings"
	"testing"
)

// The default Open path must load the released signed runtime, including after reopen.
func TestEmbeddedSQLConstraintRelease(t *testing.T) {
	if os.Getenv("TALON_TEST_EMBEDDED_NATIVE") != "1" {
		t.Skip("requires signed embedded runtime")
	}
	path := t.TempDir()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("loaded native identity: %+v", db.NativeInfo())
	exec := func(s string, p ...Value) {
		t.Helper()
		if e := db.Exec(s, p...); e != nil {
			t.Fatalf("%s: %v", s, e)
		}
	}
	reject := func(s string) {
		t.Helper()
		e := db.Exec(s)
		if e == nil || !strings.Contains(e.Error(), "UNIQUE") {
			t.Fatalf("expected UNIQUE rejection for %s: %v", s, e)
		}
	}
	exec("CREATE TABLE release_constraints (id INT PRIMARY KEY, checked_at INT, constraint_id TEXT, unique_name TEXT UNIQUE, last_checked_at INT, verified_at INT, UNIQUE(checked_at,constraint_id))")
	exec("INSERT INTO release_constraints VALUES (?,?,?,?,?,?)", IntegerValue(1), IntegerValue(11), mustText(t, "scope"), mustText(t, "first"), IntegerValue(12), IntegerValue(13))
	reject("INSERT INTO release_constraints VALUES (2,11,'scope','second',0,0)")
	reject("INSERT INTO release_constraints VALUES (2,22,'other','first',0,0)")
	reject("INSERT INTO release_constraints VALUES (2,22,'other','second',0,0),(3,22,'other','third',0,0)")
	exec("INSERT INTO release_constraints VALUES (2,22,'other','second',0,0)")
	reject("UPDATE release_constraints SET checked_at=11,constraint_id='scope' WHERE id=2")
	reject("INSERT INTO release_constraints VALUES (2,22,'other','first',0,0) ON CONFLICT(id) DO UPDATE SET unique_name=EXCLUDED.unique_name")
	exec("INSERT OR IGNORE INTO release_constraints VALUES (3,11,'scope','third',0,0),(4,44,'valid','fourth',0,0)")
	rows, e := db.Query("SELECT id,checked_at,last_checked_at,verified_at FROM release_constraints ORDER BY id")
	if e != nil || len(rows) != 3 || rows[0].Int(1) != 11 || rows[0].Int(2) != 12 || rows[0].Int(3) != 13 {
		t.Fatalf("readback %#v: %v", rows, e)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	reject("INSERT INTO release_constraints VALUES (9,11,'scope','ninth',0,0)")
}
