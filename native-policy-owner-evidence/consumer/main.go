// Independent consumer of the SDK owner and same-connection GoFrame APIs.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"reflect"

	talon "github.com/darkmice/talon-sdk-go"
	talongf "github.com/darkmice/talon-sdk-go/goframe"
	"github.com/gogf/gf/v2/database/gdb"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "consumer: code=%s error=%v\n", talon.ErrorCodeOf(err), err)
		os.Exit(1)
	}
}

func run() error {
	file := flag.String("policy", "", "external policy file")
	pin := flag.String("sha256", "", "external original policy pin")
	flag.Parse()
	beforeEnv := os.Environ()
	static, err := talon.VerifyLocalDevelopmentPolicyFile(*file, *pin)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(*file)
	if err != nil {
		return err
	}
	// Installation composition holds its external pin in the closure and calls
	// the SDK owner instead of implementing another policy/manifest validator.
	owner := func(data []byte) error { _, err := talon.VerifyLocalDevelopmentPolicy(data, *pin); return err }
	if err := owner(raw); err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "sdk-native-identity-consumer-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db, err := gdb.New(gdb.ConfigNode{Type: talongf.DriverName, Name: dir, MaxOpenConnCount: 1, MaxIdleConnCount: 1})
	if err != nil {
		return err
	}
	pool, err := db.Master()
	if err != nil {
		return err
	}
	defer pool.Close()
	ctx := context.Background()
	conn, err := pool.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	info, err := talongf.VerifyConnLocalDevelopmentPolicy(conn, raw, *pin)
	if err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := talongf.ConnNativeInfo(conn); err != nil {
		return err
	}
	if _, err := talongf.VerifyConnLocalDevelopmentPolicy(conn, raw, *pin); err != nil {
		return err
	}
	if err := tx.Rollback(); err != nil {
		return err
	}
	if pool.Stats().OpenConnections != 1 || !reflect.DeepEqual(beforeEnv, os.Environ()) {
		return fmt.Errorf("consumer identity check changed connection count or environment")
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Outcome       string `json:"outcome"`
		StaticStage   string `json:"static_stage"`
		Provenance    string `json:"provenance"`
		LibrarySHA256 string `json:"library_sha256"`
		ABIProfile    string `json:"abi_profile"`
		ABIVersion    int    `json:"abi_version"`
		ReleaseGate   string `json:"storage_conditional_batch_v1"`
		Connections   int    `json:"held_pool_connections"`
	}{"owner-callback+goframe-held-connection+transaction=passed", static.Stage, static.Provenance, info.LibrarySHA256, info.ABIProfile, info.ABIVersion, info.Gates["storage_conditional_batch_v1"].Status, pool.Stats().OpenConnections})
}
