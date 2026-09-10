package server_test

import (
	"context"
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
}
