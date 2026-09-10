package talon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func uint64Pointer(value uint64) *uint64   { return &value }
func rawJSON(value string) json.RawMessage { return json.RawMessage(value) }

func TestRootServerClientCompatibility(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/health":
			_, _ = w.Write([]byte(`{"ok":true,"data":{"status":"ok"}}`))
		case "/api/kv":
			var command struct {
				Action string `json:"action"`
			}
			if err := json.NewDecoder(request.Body).Decode(&command); err != nil {
				t.Fatal(err)
			}
			switch command.Action {
			case "set":
				_, _ = w.Write([]byte(`{"ok":true}`))
			case "get":
				_, _ = w.Write([]byte(`{"ok":true,"data":{"value":"value"}}`))
			case "del":
				_, _ = w.Write([]byte(`{"ok":true,"data":{"deleted":true}}`))
			case "exists":
				_, _ = w.Write([]byte(`{"ok":true,"data":{"exists":true}}`))
			case "setnx":
				_, _ = w.Write([]byte(`{"ok":true,"data":{"set":true}}`))
			default:
				t.Fatalf("unexpected KV action %q", command.Action)
			}
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()

	client, err := NewServerClient(ServerClientConfig{BaseURL: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	if health, err := client.Health(ctx); err != nil || health.Status != "ok" {
		t.Fatalf("health = %#v, %v", health, err)
	}
	if err := client.KvSet(ctx, "key", "value", nil); err != nil {
		t.Fatal(err)
	}
	if value, err := client.KvGet(ctx, "key"); err != nil || value == nil || *value != "value" {
		t.Fatalf("get = %v, %v", value, err)
	}
	if deleted, err := client.KvDel(ctx, "key"); err != nil || !deleted {
		t.Fatalf("del = %v, %v", deleted, err)
	}
	if exists, err := client.KvExists(ctx, "key"); err != nil || !exists {
		t.Fatalf("exists = %v, %v", exists, err)
	}
	if set, err := client.KvSetNX(ctx, "key", "value", nil); err != nil || !set {
		t.Fatalf("setnx = %v, %v", set, err)
	}
}

func TestRootConditionalPrefixScanCompatibility(t *testing.T) {
	request, err := NewConditionalPrefixScanRequest("root-scan", "consumer", []byte("outbox/"), ConditionalPrefixScanMaxEntries, nil)
	if err != nil || request.RequestID() != "root-scan" || request.Namespace() != "consumer" ||
		request.Limit() != ConditionalPrefixScanMaxEntries || ConditionalPrefixScanVersion != 1 ||
		ConditionalPrefixScanCursorTTLSeconds != 300 {
		t.Fatalf("root prefix scan request = %#v, %v", request, err)
	}
	cursor, err := ParseConditionalPrefixScanCursor("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	continued, err := request.Continue(cursor)
	if err != nil || continued.Cursor() == nil || continued.Cursor().String() != cursor.String() {
		t.Fatalf("root continued prefix scan request = %#v, %v", continued, err)
	}
}

func TestEmbeddedDBConditionalCompatibilityUsesSharedProtocol(t *testing.T) {
	db := &DB{nativeInfo: NativeInfo{
		Features: []string{"native_conditional_transaction_v2", "conditional_transaction_command_digest_v1", "storage_conditional_point_read_v1", "storage_conditional_snapshot_read_v1"},
		Capabilities: []NativeCapability{
			{Name: "native_conditional_transaction_v2", Version: 2, Status: "available"},
			{Name: "storage_conditional_point_read", Version: 1, Status: "available"},
			{Name: "storage_conditional_snapshot_read", Version: 1, Status: "available"},
		},
	}}
	request, err := NewConditionalTransactionRequest("tenant", "request-1", nil, []ConditionalTransactionMutation{ConditionalPut([]byte("key"), []byte("value"))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecuteConditionalTransaction(request); ErrorCodeOf(err) != CodeDatabaseClosed {
		t.Fatalf("embedded transaction compatibility error = %v", err)
	}
	point, _ := NewConditionalPointReadRequest("tenant", []byte("key"), nil)
	if _, err := db.ConditionalPointRead(point); ErrorCodeOf(err) != CodeDatabaseClosed {
		t.Fatalf("embedded point-read compatibility error = %v", err)
	}
	snapshot, _ := NewConditionalSnapshotReadRequest("tenant", [][]byte{[]byte("key")}, nil)
	if _, err := db.ConditionalSnapshotRead(snapshot); ErrorCodeOf(err) != CodeDatabaseClosed {
		t.Fatalf("embedded snapshot-read compatibility error = %v", err)
	}
}
