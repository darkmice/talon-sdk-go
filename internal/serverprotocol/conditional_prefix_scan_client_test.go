package serverprotocol

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerClientConditionalPrefixScanUsesTypedStorageContract(t *testing.T) {
	revision := uint64(42)
	request, err := NewConditionalPrefixScanRequest("scan-vector", "events", []byte("outbox/"), 2, &revision)
	if err != nil {
		t.Fatal(err)
	}
	resultWire := []byte(`{"version":1,"request_id":"scan-vector","namespace":"events","prefix":[111,117,116,98,111,120,47],"limit":2,"cursor":null,"revision":"42","observed_revision":"45","snapshot_revision":"901","position":"0","entries":[{"index":"0","key":[111,117,116,98,111,120,47,48,49],"value":[111,110,101]}],"next_cursor":null,"next_cursor_expires_at_unix_ms":null,"response_sha256":"e7e7da431dc036ef3f584881544503f6124c395c64ccf9984e08381e30b5cee5"}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, httpRequest *http.Request) {
		if httpRequest.URL.Path != "/api/storage" || httpRequest.Method != http.MethodPost || httpRequest.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("request = %s %s auth=%q", httpRequest.Method, httpRequest.URL.Path, httpRequest.Header.Get("Authorization"))
		}
		var command struct {
			Command string                           `json:"cmd"`
			Action  string                           `json:"action"`
			Params  wireConditionalPrefixScanRequest `json:"params"`
		}
		decoder := json.NewDecoder(httpRequest.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&command); err != nil {
			t.Fatal(err)
		}
		if command.Command != "storage" || command.Action != "conditional_prefix_scan" ||
			command.Params.Version != conditionalPrefixScanVersion || command.Params.RequestID != request.RequestID() ||
			command.Params.Namespace != request.Namespace() || !command.Params.Cursor.Present || !command.Params.Cursor.Null ||
			!command.Params.Revision.Present || command.Params.Revision.Null || command.Params.Revision.Value != revision {
			t.Fatalf("wire command = %#v", command)
		}
		writeServerTestData(writer, resultWire)
	}))
	defer server.Close()
	client := newTestServerClient(t, server.URL)
	result, err := client.ConditionalPrefixScan(context.Background(), request)
	if err != nil || result.ResponseSHA256 != "e7e7da431dc036ef3f584881544503f6124c395c64ccf9984e08381e30b5cee5" {
		t.Fatalf("prefix scan = %#v, %v", result, err)
	}
}

func TestServerClientConditionalPrefixScanClassifiesStableCoreErrors(t *testing.T) {
	cases := map[string]ErrorCode{
		"invalid_request":        CodeNativeInvalidRequest,
		"unsupported_version":    CodeNativeUnsupportedVersion,
		"invalid_namespace":      CodeNativeInvalidNamespace,
		"snapshot_not_available": CodeSnapshotNotAvailable,
		"conflict":               CodeNativeConflict,
		"cursor_mismatch":        CodeNativeCursorMismatch,
		"cursor_unavailable":     CodeNativeCursorUnavailable,
		"resource_exhausted":     CodeNativeResourceExhausted,
		"result_too_large":       CodeNativeResultTooLarge,
		"corrupt_state":          CodeNativeCorruptState,
		"storage_error":          CodeNativeStorage,
	}
	request, err := NewConditionalPrefixScanRequest("scan-errors", "events", []byte("outbox/"), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	for nativeCode, expected := range cases {
		t.Run(nativeCode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeServerTestError(writer, nativeCode, "safe diagnostic")
			}))
			defer server.Close()
			client := newTestServerClient(t, server.URL)
			_, err := client.ConditionalPrefixScan(context.Background(), request)
			if ErrorCodeOf(err) != expected || NativeCodeOf(err) != nativeCode {
				t.Fatalf("error = %v, code=%q native=%q", err, ErrorCodeOf(err), NativeCodeOf(err))
			}
		})
	}
}

func TestServerClientConditionalPrefixScanResponseLossIsDefinitiveUnavailable(t *testing.T) {
	request, err := NewConditionalPrefixScanRequest("scan-loss", "events", []byte("outbox/"), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		connection, _, err := writer.(http.Hijacker).Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_ = connection.Close()
	}))
	defer server.Close()
	client := newTestServerClient(t, server.URL)
	if _, err := client.ConditionalPrefixScan(context.Background(), request); ErrorCodeOf(err) != CodeNativeUnavailable {
		t.Fatalf("read response loss error = %v", err)
	}
}
