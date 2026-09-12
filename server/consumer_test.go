package server_test

import (
	"context"
	"errors"
	"testing"
	"time"

	server "github.com/darkmice/talon-sdk-go/server"
)

type publicServerClient interface {
	Health(context.Context) (server.ServerHealth, error)
	KvSet(context.Context, string, string, *uint64) error
	KvGet(context.Context, string) (*string, error)
	KvDel(context.Context, string) (bool, error)
	KvExists(context.Context, string) (bool, error)
	KvSetNX(context.Context, string, string, *uint64) (bool, error)
	ExecuteConditionalTransaction(context.Context, server.ConditionalTransactionRequest) (server.ConditionalTransactionResult, error)
	ConditionalTransactionReceiptFor(context.Context, server.ConditionalTransactionRequest) (server.ConditionalReceiptLookup, error)
	ConditionalGet(context.Context, server.ConditionalPointReadRequest) (server.ConditionalPointReadResult, error)
	ConditionalSnapshotGet(context.Context, server.ConditionalSnapshotReadRequest) (server.ConditionalSnapshotReadResult, error)
	ConditionalPrefixScan(context.Context, server.ConditionalPrefixScanRequest) (server.ConditionalPrefixScanResult, error)
}

var _ publicServerClient = (*server.ServerClient)(nil)

func TestExternalConsumerBuildsWithoutRootPackage(t *testing.T) {
	client, err := server.NewServerClient(server.ServerClientConfig{
		BaseURL: "http://127.0.0.1:8080",
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	client.Close()

	request, err := server.NewConditionalTransactionRequest(
		"consumer",
		"request-1",
		nil,
		[]server.ConditionalTransactionMutation{server.ConditionalPut([]byte("key"), []byte("value"))},
	)
	if err != nil || request.CommandSHA256() == "" {
		t.Fatalf("sealed request = %#v, %v", request, err)
	}
	prefixScan, err := server.NewConditionalPrefixScanRequest("consumer-scan", "consumer", []byte("outbox/"), server.ConditionalPrefixScanMaxEntries, nil)
	if err != nil || prefixScan.RequestID() != "consumer-scan" || prefixScan.Namespace() != "consumer" ||
		prefixScan.Limit() != server.ConditionalPrefixScanMaxEntries || server.ConditionalPrefixScanVersion != 1 ||
		server.ConditionalPrefixScanCursorTTLSeconds != 300 {
		t.Fatalf("sealed prefix scan request = %#v, %v", prefixScan, err)
	}
	v2Snapshot, err := server.NewConditionalSnapshotReadRequestV2("consumer", [][]byte{[]byte("key")}, nil)
	if err != nil || v2Snapshot.Version() != server.ConditionalSnapshotReadVersionV2 || server.ConditionalSnapshotReadMaxKeysV2 != 256 || server.ConditionalSnapshotReadMaxKeys != 128 {
		t.Fatalf("sealed v2 snapshot request = %#v, %v", v2Snapshot, err)
	}
	cursor, err := server.ParseConditionalPrefixScanCursor("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	continued, err := prefixScan.Continue(cursor)
	if err != nil || continued.Cursor() == nil || continued.Cursor().String() != cursor.String() {
		t.Fatalf("continued prefix scan request = %#v, %v", continued, err)
	}
}

func TestNilClientReturnsTypedUnavailableForEveryPublicOperation(t *testing.T) {
	transaction, err := server.NewConditionalTransactionRequest(
		"consumer", "nil-client-request", nil,
		[]server.ConditionalTransactionMutation{server.ConditionalPut([]byte("key"), []byte("value"))},
	)
	if err != nil {
		t.Fatal(err)
	}
	point, err := server.NewConditionalPointReadRequest("consumer", []byte("key"), nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := server.NewConditionalSnapshotReadRequest("consumer", [][]byte{[]byte("key")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	prefixScan, err := server.NewConditionalPrefixScanRequest("nil-client-scan", "consumer", []byte("outbox/"), 1, nil)
	if err != nil {
		t.Fatal(err)
	}

	var client *server.ServerClient
	operations := []struct {
		name string
		call func() error
	}{
		{name: "health", call: func() error { _, err := client.Health(context.Background()); return err }},
		{name: "conditional transaction", call: func() error {
			_, err := client.ConditionalTransaction(context.Background(), "consumer", "nil-client-request-2", nil, []server.ConditionalTransactionMutation{server.ConditionalPut([]byte("key"), []byte("value"))})
			return err
		}},
		{name: "execute conditional transaction", call: func() error {
			_, err := client.ExecuteConditionalTransaction(context.Background(), transaction)
			return err
		}},
		{name: "conditional receipt alias", call: func() error {
			_, err := client.ConditionalTransactionReceipt(context.Background(), transaction)
			return err
		}},
		{name: "conditional receipt", call: func() error {
			_, err := client.ConditionalTransactionReceiptFor(context.Background(), transaction)
			return err
		}},
		{name: "conditional point read alias", call: func() error { _, err := client.ConditionalPointRead(context.Background(), point); return err }},
		{name: "conditional point read", call: func() error { _, err := client.ConditionalGet(context.Background(), point); return err }},
		{name: "conditional snapshot read alias", call: func() error { _, err := client.ConditionalSnapshotRead(context.Background(), snapshot); return err }},
		{name: "conditional snapshot read", call: func() error { _, err := client.ConditionalSnapshotGet(context.Background(), snapshot); return err }},
		{name: "conditional prefix scan", call: func() error { _, err := client.ConditionalPrefixScan(context.Background(), prefixScan); return err }},
		{name: "KV set", call: func() error { return client.KvSet(context.Background(), "key", "value", nil) }},
		{name: "KV get", call: func() error { _, err := client.KvGet(context.Background(), "key"); return err }},
		{name: "KV delete", call: func() error { _, err := client.KvDel(context.Background(), "key"); return err }},
		{name: "KV exists", call: func() error { _, err := client.KvExists(context.Background(), "key"); return err }},
		{name: "KV setnx", call: func() error { _, err := client.KvSetNX(context.Background(), "key", "value", nil); return err }},
		{name: "SQL query", call: func() error {
			request, err := server.NewQueryRequest("SELECT 1")
			if err != nil {
				return err
			}
			_, err = client.Query(context.Background(), request)
			return err
		}},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			err := operation.call()
			if server.ErrorCodeOf(err) != server.CodeNativeUnavailable {
				t.Fatalf("error = %v, code = %q", err, server.ErrorCodeOf(err))
			}
			var typed *server.TalonError
			if !errors.As(err, &typed) || typed.Operation == "" {
				t.Fatalf("error is not a typed TalonError with operation: %#v", err)
			}
		})
	}
	client.Close()
}
