package talon

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	serverapi "github.com/darkmice/talon-sdk-go/server"
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

func TestConditionalSnapshotReadV2UsesExactCapabilityVersion(t *testing.T) {
	db := &DB{nativeInfo: NativeInfo{
		Features: []string{"storage_conditional_snapshot_read_v1", "storage_conditional_snapshot_read_v2"},
		Capabilities: []NativeCapability{
			{Name: "storage_conditional_snapshot_read", Version: 1, Status: "gated", Reason: stringPointer("v1 remains gated")},
			{Name: "storage_conditional_snapshot_read", Version: 2, Status: "available"},
		},
	}}
	v1, err := NewConditionalSnapshotReadRequest("tenant", [][]byte{[]byte("key")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ConditionalSnapshotGet(v1); ErrorCodeOf(err) != CodeCapabilityUnavailable {
		t.Fatalf("v1 did not honor its gated capability: %v", err)
	}
	v2, err := NewConditionalSnapshotReadRequestV2("tenant", [][]byte{[]byte("key")}, nil)
	if err != nil || v2.Version() != ConditionalSnapshotReadVersionV2 {
		t.Fatalf("v2 request = %#v, %v", v2, err)
	}
	if _, err := db.ConditionalSnapshotGet(v2); ErrorCodeOf(err) != CodeDatabaseClosed {
		t.Fatalf("v2 did not select the available v2 capability: %v", err)
	}

	db.nativeInfo.Features = []string{"storage_conditional_snapshot_read_v1"}
	if _, err := db.ConditionalSnapshotGet(v2); ErrorCodeOf(err) != CodeCapabilityUnavailable {
		t.Fatalf("v2 missing feature did not fail closed: %v", err)
	}

	// Capability order is artifact-bound but must not change exact version
	// selection in the SDK.
	db.nativeInfo.Features = []string{"storage_conditional_snapshot_read_v1", "storage_conditional_snapshot_read_v2"}
	db.nativeInfo.Capabilities = []NativeCapability{
		{Name: "storage_conditional_snapshot_read", Version: 2, Status: "available"},
		{Name: "storage_conditional_snapshot_read", Version: 1, Status: "available"},
	}
	if _, err := db.ConditionalSnapshotGet(v1); ErrorCodeOf(err) != CodeDatabaseClosed {
		t.Fatalf("v1 selection depended on capability order: %v", err)
	}
}

func stringPointer(value string) *string { return &value }

// TestEmbeddedAndServerDecimalSemanticsAgree pins the cgo-free Server SQL
// surface to the embedded path's DECIMAL semantics without requiring a live
// native bundle: same bound, same exactness, same rejection, same canonical
// text. Only the wire representation differs (Server uses the JSON contract
// pinned by talon-core issue #12).
func TestEmbeddedAndServerDecimalSemanticsAgree(t *testing.T) {
	if maxDecimalPrecision != serverapi.MaxDecimalPrecision {
		t.Fatalf("embedded maxDecimalPrecision = %d, server MaxDecimalPrecision = %d", maxDecimalPrecision, serverapi.MaxDecimalPrecision)
	}

	maxCoefficient, ok := new(big.Int).SetString("99999999999999999999999999999999999999", 10)
	if !ok {
		t.Fatal("fixture is not an integer")
	}
	cases := []struct {
		coefficient *big.Int
		scale       uint8
		text        string
	}{
		{maxCoefficient, 0, "99999999999999999999999999999999999999"},
		{maxCoefficient, 38, "0.99999999999999999999999999999999999999"},
		{big.NewInt(1234500), 4, "123.4500"},
		{big.NewInt(-12300), 4, "-1.2300"},
		{big.NewInt(0), 4, "0.0000"},
		{big.NewInt(-1), 4, "-0.0001"},
	}
	for _, test := range cases {
		embedded, err := DecimalValue(test.coefficient, test.scale)
		if err != nil {
			t.Fatalf("embedded DecimalValue(%s, %d): %v", test.coefficient, test.scale, err)
		}
		embeddedCoefficient, embeddedScale, ok := embedded.Decimal()
		if !ok {
			t.Fatalf("embedded value is not a decimal for %s/%d", test.coefficient, test.scale)
		}

		serverDecimal, err := serverapi.NewDecimal(test.coefficient, test.scale)
		if err != nil {
			t.Fatalf("server NewDecimal(%s, %d): %v", test.coefficient, test.scale, err)
		}
		if serverDecimal.Coefficient().Cmp(embeddedCoefficient) != 0 || serverDecimal.Scale() != embeddedScale {
			t.Fatalf("server %s/%d != embedded %s/%d", serverDecimal.Coefficient(), serverDecimal.Scale(), embeddedCoefficient, embeddedScale)
		}
		// The canonical text must be identical, and must parse back to the
		// same exact pair on the cgo-free side.
		if serverDecimal.Text() != test.text {
			t.Fatalf("server Text() = %q, want %q", serverDecimal.Text(), test.text)
		}
		reparsed, err := serverapi.ParseDecimal(serverDecimal.Text())
		if err != nil {
			t.Fatalf("server ParseDecimal(%q): %v", serverDecimal.Text(), err)
		}
		if reparsed.Coefficient().Cmp(embeddedCoefficient) != 0 || reparsed.Scale() != embeddedScale {
			t.Fatalf("server text round trip = %s/%d, want %s/%d", reparsed.Coefficient(), reparsed.Scale(), embeddedCoefficient, embeddedScale)
		}
	}

	// Rejection parity: both paths refuse anything past the 38-digit bound and
	// neither degrades to a float.
	overCoefficient := new(big.Int).Add(maxCoefficient, big.NewInt(1))
	if _, err := DecimalValue(overCoefficient, 0); err == nil {
		t.Fatal("embedded accepted a 39-digit coefficient")
	}
	if _, err := serverapi.NewDecimal(overCoefficient, 0); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("server accepted a 39-digit coefficient: %v", err)
	}
	if _, err := DecimalValue(big.NewInt(1), 39); err == nil {
		t.Fatal("embedded accepted scale 39")
	}
	if _, err := serverapi.NewDecimal(big.NewInt(1), 39); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("server accepted scale 39: %v", err)
	}
	if _, err := serverapi.ParseDecimal(strings.Repeat("9", 39)); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("server accepted a 39-digit literal: %v", err)
	}
}
