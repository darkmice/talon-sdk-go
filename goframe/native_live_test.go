package goframe

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/gogf/gf/v2/database/gdb"
)

// Launched by TestLocalSignedCoreKVInterop with an ephemeral signed Core.
// It uses GoFrame's public gdb API, rather than the driver's fake native DB.
func TestLocalSignedCoreGoFrameInterop(t *testing.T) {
	path := os.Getenv("TALON_GOFRAME_LIVE_DB")
	if path == "" {
		t.Skip("run through the local signed Core integration fixture")
	}
	ctx := context.Background()
	db, err := gdb.New(gdb.ConfigNode{Type: DriverName, Name: path})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "CREATE TABLE gf_live (id INT PRIMARY KEY, name TEXT)"); err != nil {
		for cause := err; cause != nil; cause = errors.Unwrap(cause) {
			t.Logf("create error: %v", cause)
		}
		t.Fatalf("create: %v", err)
	}
	if result, err := db.Exec(ctx, "INSERT INTO gf_live (id, name) VALUES (?, ?)", 1, "first"); err != nil {
		t.Fatalf("insert: %v", err)
	} else if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("insert affected=%d, %v", count, err)
	}
	row, err := db.GetOne(ctx, "SELECT id, name FROM gf_live WHERE id = ?", 1)
	if err != nil || row["name"].String() != "first" {
		t.Fatalf("query row=%v, %v", row, err)
	}
	type item struct {
		ID   int    `orm:"id"`
		Name string `orm:"name"`
	}
	if _, err := db.Model("gf_live").Ctx(ctx).Save(item{ID: 1, Name: "saved"}); err != nil {
		t.Fatalf("model save: %v", err)
	}
	row, err = db.GetOne(ctx, "SELECT name FROM gf_live WHERE id = ?", 1)
	if err != nil || row["name"].String() != "saved" {
		t.Fatalf("saved row=%v, %v", row, err)
	}
	if result, err := db.Model("gf_live").Ctx(ctx).Where("id", 1).Update(struct {
		Name string `orm:"name"`
	}{Name: "updated"}); err != nil {
		t.Fatalf("model update: %v", err)
	} else if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatalf("model update affected=%d, %v", count, err)
	}
	row, err = db.Model("gf_live").Ctx(ctx).Where("id", 1).One()
	if err != nil || row["name"].String() != "updated" {
		t.Fatalf("model one=%v, %v", row, err)
	}
	fields, err := db.TableFields(ctx, "gf_live")
	if err != nil || fields["id"] == nil || fields["id"].Key != "PRI" {
		t.Fatalf("table fields=%v, %v", fields, err)
	}
	tables, err := db.Tables(ctx)
	if err != nil {
		t.Fatalf("tables: %v", err)
	}
	found := false
	for _, table := range tables {
		found = found || table == "gf_live"
	}
	if !found {
		t.Fatalf("gf_live absent from tables: %v", tables)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec("INSERT INTO gf_live (id, name) VALUES (?, ?)", 2, "rollback"); err != nil {
		t.Fatalf("transaction insert: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	row, err = db.GetOne(ctx, "SELECT id FROM gf_live WHERE id = ?", 2)
	if err != nil || len(row) != 0 {
		t.Fatalf("rolled-back row=%v, %v", row, err)
	}
}
