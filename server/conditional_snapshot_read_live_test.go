package server_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	server "github.com/darkmice/talon-sdk-go/server"
)

// TestLiveConditionalSnapshotReadV2 is an opt-in source/release Server
// conformance check. The caller owns the identity of the Server artifact; this
// test proves wire compatibility only and must not be presented as signed
// talon-bin release evidence.
func TestLiveConditionalSnapshotReadV2(t *testing.T) {
	baseURL := os.Getenv("TALON_SERVER_URL")
	if baseURL == "" {
		t.Skip("set TALON_SERVER_URL to run the live conditional snapshot-read v2 check")
	}
	client, err := server.NewServerClient(server.ServerClientConfig{
		BaseURL: baseURL,
		Token:   os.Getenv("TALON_SERVER_TOKEN"),
		Timeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	keys := make([][]byte, 139)
	for index := range keys {
		keys[index] = []byte(fmt.Sprintf("sdk-live-snapshot-v2-%03d", index))
	}
	request, err := server.NewConditionalSnapshotReadRequestV2("sdk-live-v2", keys, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ConditionalSnapshotGet(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Version != server.ConditionalSnapshotReadVersionV2 || result.Namespace != request.Namespace() || len(result.Observations) != len(keys) || result.ResponseSHA256 == "" {
		t.Fatalf("unexpected v2 result: %#v", result)
	}
	for index, observation := range result.Observations {
		if observation.Index != uint64(index) || string(observation.Key) != string(keys[index]) {
			t.Fatalf("observation %d lost ordered identity: %#v", index, observation)
		}
	}

	if _, err := server.NewConditionalSnapshotReadRequest("sdk-live-v1", keys[:129], nil); server.ErrorCodeOf(err) != server.CodeInvalidArgument {
		t.Fatalf("v1 accepted 129 keys: %v", err)
	}
}
