package server_test

import (
	"context"
	api "github.com/darkmice/talon-sdk-go/server"
	"os"
	"strings"
	"testing"
	"time"
)

func TestServerSQLConstraintRelease(t *testing.T) {
	base := os.Getenv("TALON_SERVER_URL")
	if base == "" {
		t.Skip("requires released HTTP server")
	}
	names := []string{"TALON_SERVER_SQL_RELEASE_TAG", "TALON_SERVER_SQL_TALON_BIN_COMMIT", "TALON_SERVER_SQL_CORE_COMMIT", "TALON_SERVER_SQL_ARTIFACT_SHA256"}
	vals := make([]string, 4)
	for i, n := range names {
		vals[i] = os.Getenv(n)
		if vals[i] == "" {
			t.Fatal("missing release identity " + n)
		}
	}
	client, e := api.NewServerClient(api.ServerClientConfig{BaseURL: base, Token: os.Getenv("TALON_SERVER_TOKEN"), Timeout: 15 * time.Second, ServerSQLAttestation: &api.ServerSQLAttestation{Capability: api.ServerSQLDecimalCapability, Version: api.ServerSQLVersion, ReleaseTag: vals[0], TalonBinCommit: vals[1], CoreCommit: vals[2], ArtifactSHA256: vals[3]}})
	if e != nil {
		t.Fatal(e)
	}
	query := func(s string, p ...api.Value) (api.QueryResult, error) {
		t.Helper()
		req, e := api.NewQueryRequest(s, p...)
		if e != nil {
			t.Fatal(e)
		}
		return client.Query(context.Background(), req)
	}
	exec := func(s string, p ...api.Value) api.QueryResult {
		t.Helper()
		r, e := query(s, p...)
		if e != nil {
			t.Fatalf("%s: %v", s, e)
		}
		return r
	}
	reject := func(s string) {
		t.Helper()
		_, e := query(s)
		if e == nil || !strings.Contains(e.Error(), "UNIQUE") {
			t.Fatalf("expected UNIQUE rejection %s: %v", s, e)
		}
	}
	exec("CREATE TABLE release_http (id INT PRIMARY KEY, checked_at INT, constraint_id TEXT, UNIQUE(checked_at,constraint_id))")
	text, e := api.TextValue("scope")
	if e != nil {
		t.Fatal(e)
	}
	exec("INSERT INTO release_http VALUES (?,?,?)", api.IntegerValue(1), api.IntegerValue(11), text)
	reject("INSERT INTO release_http VALUES (2,11,'scope')")
	reject("INSERT INTO release_http VALUES (2,22,'other'),(3,22,'other')")
	exec("INSERT INTO release_http VALUES (2,22,'other')")
	reject("UPDATE release_http SET checked_at=11,constraint_id='scope' WHERE id=2")
	reject("INSERT INTO release_http VALUES (2,11,'scope') ON CONFLICT(id) DO UPDATE SET checked_at=EXCLUDED.checked_at,constraint_id=EXCLUDED.constraint_id")
	result := exec("SELECT * FROM release_http ORDER BY id")
	if len(result.Rows) != 2 {
		t.Fatalf("atomic row count: %#v", result)
	}
	exec("DROP TABLE release_http")
}
