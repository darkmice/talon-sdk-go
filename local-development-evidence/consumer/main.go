package main

import (
	"context"
	"fmt"
	talon "github.com/darkmice/talon-sdk-go"
	_ "github.com/darkmice/talon-sdk-go/goframe"
	"github.com/gogf/gf/v2/database/gdb"
	"os"
)

func main() {
	path, err := os.MkdirTemp("", "talon-dev-consumer-")
	must(err)
	defer os.RemoveAll(path)
	native, err := talon.Open(path)
	must(err)
	info := native.NativeInfo()
	native.Close()
	if info.Admission != talon.NativeAdmissionLocalDevelopment || !info.CoreGitDirty || info.ReleaseTag != "" || info.KeyID != "" {
		panic("wrong development provenance")
	}
	db, err := gdb.New(gdb.ConfigNode{Type: "talon", Name: path})
	must(err)
	ctx := context.Background()
	defer db.Close(ctx)
	_, err = db.Exec(ctx, "CREATE TABLE receipts (id INTEGER PRIMARY KEY, operation_id TEXT)")
	must(err)
	_, err = db.Exec(ctx, "CREATE INDEX receipt_operation ON receipts(operation_id)")
	must(err)
	tx, err := db.Begin(ctx)
	must(err)
	_, err = tx.Exec("INSERT INTO receipts VALUES (?, ?)", 1, "consumer-receipt")
	must(err)
	rows, err := tx.GetAll("SELECT id FROM receipts WHERE operation_id = ?", "consumer-receipt")
	must(err)
	if len(rows) != 1 {
		panic(fmt.Sprintf("indexed transaction rows=%d", len(rows)))
	}
	must(tx.Commit())
	fmt.Printf("consumer native admission=%s dirty=%t profile=%s core=%s library_sha256=%s indexed_tx_rows=%d\n", info.Admission, info.CoreGitDirty, info.BuildProfile, info.CoreCommit, info.LibrarySHA256, len(rows))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
