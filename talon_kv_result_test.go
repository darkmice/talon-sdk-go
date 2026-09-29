package talon

import (
	"bytes"
	"slices"
	"testing"
)

// Run with TALON_TEST_PRODUCTION_NATIVE=1 and a trusted signed Core bundle.
// Each assertion checks the public return value, which previously remained at
// its zero value even when Core returned a successful nonzero JSON field.
func TestKVPublicResultsFromSignedNative(t *testing.T) {
	assertKVPublicResults(t, openTestDB(t))
}

func assertKVPublicResults(t *testing.T, db *DB) {
	t.Helper()
	ttl := uint64(120)
	if err := db.KvSet("kv-result:value", "payload", &ttl); err != nil {
		t.Fatal(err)
	}
	value, err := db.KvGet("kv-result:value")
	if err != nil || value == nil || *value != "payload" {
		t.Fatalf("KvGet = %v, %v", value, err)
	}
	exists, err := db.KvExists("kv-result:value")
	if err != nil || !exists {
		t.Fatalf("KvExists = %v, %v", exists, err)
	}
	keys, err := db.KvKeys("kv-result:")
	if err != nil || !slices.Contains(keys, "kv-result:value") {
		t.Fatalf("KvKeys = %v, %v", keys, err)
	}
	matched, err := db.KvKeysMatch("kv-result:*")
	if err != nil || !slices.Contains(matched, "kv-result:value") {
		t.Fatalf("KvKeysMatch = %v, %v", matched, err)
	}
	limited, err := db.KvKeysLimit("kv-result:", 0, 10)
	if err != nil || !slices.Contains(limited, "kv-result:value") {
		t.Fatalf("KvKeysLimit = %v, %v", limited, err)
	}
	remaining, err := db.KvTtl("kv-result:value")
	if err != nil || remaining == nil || *remaining == 0 || *remaining > ttl {
		t.Fatalf("KvTtl = %v, %v", remaining, err)
	}
	if got, err := db.KvIncr("kv-result:counter"); err != nil || got != 1 {
		t.Fatalf("KvIncr = %d, %v", got, err)
	}
	if got, err := db.KvIncrBy("kv-result:counter", 4); err != nil || got != 5 {
		t.Fatalf("KvIncrBy = %d, %v", got, err)
	}
	if got, err := db.KvDecrBy("kv-result:counter", 2); err != nil || got != 3 {
		t.Fatalf("KvDecrBy = %d, %v", got, err)
	}
	if set, err := db.KvSetNX("kv-result:created", "new", nil); err != nil || !set {
		t.Fatalf("KvSetNX = %v, %v", set, err)
	}
	if count, err := db.KvCount(); err != nil || count < 3 {
		t.Fatalf("KvCount = %d, %v", count, err)
	}
	if deleted, err := db.KvDel("kv-result:value"); err != nil || !deleted {
		t.Fatalf("KvDel = %v, %v", deleted, err)
	}
}

func TestNativeKVBinaryRoundTripFromSignedNative(t *testing.T) {
	assertNativeKVBinaryRoundTrip(t, openTestDB(t))
}

func assertNativeKVBinaryRoundTrip(t *testing.T, db *DB) {
	t.Helper()
	if err := db.RequireCapability("native_kv_read"); err != nil {
		t.Fatalf("signed Core must admit native_kv_read v1 for this test: %v", err)
	}
	key := []byte{0, 'k', 0xff}
	value := []byte{0, 0xff, 0}
	if err := db.KVSetBytes(key, value, 0); err != nil {
		t.Fatal(err)
	}
	got, err := db.KVGetBytes(key)
	if err != nil || !got.Present || !bytes.Equal(got.Value, value) {
		t.Fatalf("KVGetBytes = %#v, %v", got, err)
	}
	values, err := db.KVMGetBytes([][]byte{key, []byte("missing"), key})
	if err != nil || len(values) != 3 || !values[0].Present || values[1].Present || !values[2].Present || !bytes.Equal(values[2].Value, value) {
		t.Fatalf("KVMGetBytes = %#v, %v", values, err)
	}
	if count, err := db.KVExistsBytes([][]byte{key, key, []byte("missing")}); err != nil || count != 2 {
		t.Fatalf("KVExistsBytes = %d, %v", count, err)
	}
	if kind, err := db.KVTypeBytes(key); err != nil || kind != "string" {
		t.Fatalf("KVTypeBytes = %q, %v", kind, err)
	}
	if ttl, err := db.KVTTLBytes(key); err != nil || ttl != -1 {
		t.Fatalf("KVTTLBytes = %d, %v", ttl, err)
	}
	if missing, err := db.KVGetBytes([]byte("missing")); err != nil || missing.Present {
		t.Fatalf("missing KVGetBytes = %#v, %v", missing, err)
	}
	if err := db.KVSetBytes([]byte("empty"), nil, 0); err != nil {
		t.Fatal(err)
	}
	if empty, err := db.KVGetBytes([]byte("empty")); err != nil || !empty.Present || empty.Value == nil || len(empty.Value) != 0 {
		t.Fatalf("empty KVGetBytes = %#v, %v", empty, err)
	}
	if kind, err := db.KVTypeBytes([]byte("missing")); err != nil || kind != "none" {
		t.Fatalf("missing KVTypeBytes = %q, %v", kind, err)
	}
	if ttl, err := db.KVTTLBytes([]byte("missing")); err != nil || ttl != -2 {
		t.Fatalf("missing KVTTLBytes = %d, %v", ttl, err)
	}
}
