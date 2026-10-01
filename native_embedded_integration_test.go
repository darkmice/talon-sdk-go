package talon

import (
	"os"
	"strings"
	"testing"
)

// Run only against a release-ready, signed go-runtime module. It exercises the
// public default Open path without any explicit native policy configuration.
func TestEmbeddedSignedRuntimeOpen(t *testing.T) {
	if os.Getenv("TALON_TEST_EMBEDDED_NATIVE") != "1" {
		t.Skip("embedded native acceptance requires a release-ready signed Go runtime")
	}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "TALON_NATIVE_") {
			t.Fatalf("embedded Open test has explicit native policy %s", name)
		}
	}
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open with embedded signed runtime: %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.Exec("CREATE TABLE embedded_items (id INTEGER PRIMARY KEY, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO embedded_items (id, name) VALUES (?, ?)", IntegerValue(1), mustText(t, "ready")); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query("SELECT id, name FROM embedded_items WHERE id = ?", IntegerValue(1))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Int(0) != 1 || rows[0].Str(1) != "ready" {
		t.Fatalf("embedded SQL rows = %#v", rows)
	}
}
