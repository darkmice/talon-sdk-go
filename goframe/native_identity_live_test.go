package goframe

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"testing"

	talon "github.com/darkmice/talon-sdk-go"
)

func identityFixtureSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestNativeIdentityRealConnectionDuringTransaction(t *testing.T) {
	if os.Getenv("TALON_TEST_LOCAL_POLICY_OWNER") != "1" {
		t.Skip("requires explicit local Core policy owner acceptance")
	}
	ctx := context.Background()
	data, err := os.ReadFile(os.Getenv("TALON_NATIVE_DEV_POLICY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	pin := os.Getenv("TALON_NATIVE_DEV_POLICY_SHA256")
	pool := sql.OpenDB(nativeConnector{path: t.TempDir()})
	defer pool.Close()
	pool.SetMaxOpenConns(1)
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	info, err := VerifyConnLocalDevelopmentPolicy(conn, data, pin)
	if err != nil {
		t.Fatal(err)
	}
	if info.LibrarySHA256 != os.Getenv("TALON_TEST_OWNER_LIBRARY_SHA256") || info.Admission != talon.NativeAdmissionLocalDevelopment || info.Gates["storage_conditional_batch_v1"].Status != "gated" {
		t.Fatal("loaded identity differs from selected Core")
	}
	if _, err := conn.ExecContext(ctx, "CREATE TABLE sdk_identity_owner_probe (id INTEGER PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatal(err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO sdk_identity_owner_probe (id,value) VALUES (?,?)", 1, "pending"); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyConnLocalDevelopmentPolicy(conn, data, pin); err != nil {
		t.Fatal("identity interrupted transaction:", err)
	}
	// This profile is statically permitted and hashes match the actual files;
	// it still differs from the policy of the already-loaded SQL handle.
	bad := bytes.Replace(data, []byte(`"debug"`), []byte(`"release"`), 1)
	if bytes.Equal(bad, data) {
		t.Fatal("acceptance fixture must declare debug profile")
	}
	// Obtain the independent raw-byte pin in test code, never substitute it for
	// the loaded identity in the driver API.
	badPin := identityFixtureSHA(bad)
	if _, err := talon.VerifyLocalDevelopmentPolicy(bad, badPin); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyConnLocalDevelopmentPolicy(conn, bad, badPin); talon.ErrorCodeOf(err) != talon.CodeNativeVerification {
		t.Fatal("file-valid mismatched runtime policy was accepted")
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM sdk_identity_owner_probe").Scan(&count); err != nil || count != 1 {
		t.Fatal("identity check lost in-transaction writes", err)
	}
	if pool.Stats().OpenConnections != 1 || pool.Stats().InUse != 1 {
		t.Fatal("identity acquired another physical connection")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM sdk_identity_owner_probe").Scan(&count); err != nil || count != 0 {
		t.Fatal("rollback changed by identity check", err)
	}
	tx, err = conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO sdk_identity_owner_probe (id,value) VALUES (?,?)", 2, "committed"); err != nil {
		t.Fatal(err)
	}
	if _, err := ConnNativeInfo(conn); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM sdk_identity_owner_probe").Scan(&count); err != nil || count != 1 {
		t.Fatal("commit changed by identity check", err)
	}
	t.Logf("same-connection native identity library_sha256=%s abi=%s@%d mismatch-rejected+tx-read+rollback+commit+single-handle=passed", info.LibrarySHA256, info.ABIProfile, info.ABIVersion)
}
