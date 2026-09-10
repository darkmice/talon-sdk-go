package serverprotocol

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestServerClient(t *testing.T, baseURL string) *ServerClient {
	t.Helper()
	client, err := NewServerClient(ServerClientConfig{BaseURL: baseURL, Token: "test-token", Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

func TestServerClientNilReceiverStopsAtTransportEntrypoints(t *testing.T) {
	var client *ServerClient
	if _, err := client.Health(context.Background()); ErrorCodeOf(err) != CodeNativeUnavailable {
		t.Fatalf("nil health error = %v", err)
	}
	if _, err := client.storage(context.Background(), "conditional_batch", struct{}{}, "nil storage", true); ErrorCodeOf(err) != CodeNativeUnavailable {
		t.Fatalf("nil storage error = %v", err)
	}
	if _, err := client.kv(context.Background(), "get", struct{}{}, "nil kv", false, true); ErrorCodeOf(err) != CodeNativeUnavailable {
		t.Fatalf("nil KV error = %v", err)
	}
	client.Close()
}

func writeServerTestData(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true,"data":`))
	_, _ = w.Write(data)
	_, _ = w.Write([]byte(`}`))
}

func writeServerTestError(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":false,"code":`))
	encodedCode, _ := json.Marshal(code)
	encodedMessage, _ := json.Marshal(message)
	_, _ = w.Write(encodedCode)
	_, _ = w.Write([]byte(`,"error":`))
	_, _ = w.Write(encodedMessage)
	_, _ = w.Write([]byte(`}`))
}

func TestServerClientTypedCoreOperations(t *testing.T) {
	txRequest, err := NewConditionalTransactionRequest("n", "req-1", []ConditionalTransactionCondition{{Key: []byte{1}, Expected: nil, Operator: CompareEqual}}, []ConditionalTransactionMutation{ConditionalIncrement([]byte{2}, 1)})
	if err != nil {
		t.Fatal(err)
	}
	txBuilt, err := buildClosedConditionalRequest(txRequest)
	if err != nil {
		t.Fatal(err)
	}
	txResult, _ := makeAppliedConditionalWire(t, 9, true)
	receipt := makeFoundConditionalLookup(t, txBuilt, 9)
	required := uint64(9)
	pointRequest, err := NewConditionalPointReadRequest("tenant", []byte("key"), &required)
	if err != nil {
		t.Fatal(err)
	}
	pointResult := makeConditionalPointReadWire(t, pointRequest, 9, 10, []byte("value"))
	snapshotRequest, err := NewConditionalSnapshotReadRequest("tenant", [][]byte{[]byte("a"), []byte("b")}, &required)
	if err != nil {
		t.Fatal(err)
	}
	snapshotResult := makeConditionalSnapshotReadWire(t, snapshotRequest, 9, 10, []byte("one"), nil)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		switch request.URL.Path {
		case "/health":
			if request.Method != http.MethodGet {
				t.Fatalf("health method = %s", request.Method)
			}
			writeServerTestData(w, []byte(`{"status":"ok"}`))
		case "/api/kv":
			var command struct {
				Command string          `json:"cmd"`
				Action  string          `json:"action"`
				Params  json.RawMessage `json:"params"`
			}
			if err := json.NewDecoder(request.Body).Decode(&command); err != nil {
				t.Fatal(err)
			}
			if command.Command != "kv" {
				t.Fatalf("KV command = %q", command.Command)
			}
			switch command.Action {
			case "setnx":
				writeServerTestData(w, []byte(`{"set":true}`))
			case "get":
				writeServerTestData(w, []byte(`{"value":"stored"}`))
			case "exists":
				writeServerTestData(w, []byte(`{"exists":true}`))
			default:
				t.Fatalf("unexpected KV action %q", command.Action)
			}
		case "/api/storage":
			var command struct {
				Command string          `json:"cmd"`
				Action  string          `json:"action"`
				Params  json.RawMessage `json:"params"`
			}
			if err := json.NewDecoder(request.Body).Decode(&command); err != nil {
				t.Fatal(err)
			}
			if command.Command != "storage" {
				t.Fatalf("storage command = %q", command.Command)
			}
			switch command.Action {
			case "conditional_batch":
				var params wireConditionalRequest
				if err := decodeStrictJSON(command.Params, &params); err != nil {
					t.Fatal(err)
				}
				if params.RequestID != txRequest.RequestID() || params.Namespace != txRequest.Namespace() {
					t.Fatalf("conditional transaction identity = %#v", params)
				}
				writeServerTestData(w, txResult)
			case "conditional_receipt":
				writeServerTestData(w, receipt)
			case "conditional_get":
				writeServerTestData(w, pointResult)
			case "conditional_snapshot_get":
				writeServerTestData(w, snapshotResult)
			default:
				t.Fatalf("unexpected storage action %q", command.Action)
			}
		default:
			t.Fatalf("unexpected path %q", request.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestServerClient(t, server.URL)
	ctx := context.Background()

	health, err := client.Health(ctx)
	if err != nil || health.Status != "ok" {
		t.Fatalf("health = %#v, %v", health, err)
	}
	set, err := client.KvSetNX(ctx, "key", "value", nil)
	if err != nil || !set {
		t.Fatalf("setnx = %v, %v", set, err)
	}
	value, err := client.KvGet(ctx, "key")
	if err != nil || value == nil || *value != "stored" {
		t.Fatalf("get = %v, %v", value, err)
	}
	exists, err := client.KvExists(ctx, "key")
	if err != nil || !exists {
		t.Fatalf("exists = %v, %v", exists, err)
	}
	transaction, err := client.ExecuteConditionalTransaction(ctx, txRequest)
	if err != nil || !transaction.Applied || transaction.Receipt.CommandSHA256 != txRequest.CommandSHA256() {
		t.Fatalf("transaction = %#v, %v", transaction, err)
	}
	lookup, err := client.ConditionalTransactionReceiptFor(ctx, txRequest)
	if err != nil || lookup.Status != ConditionalReceiptFound || lookup.Receipt == nil || lookup.Receipt.CommandSHA256 != txRequest.CommandSHA256() {
		t.Fatalf("receipt lookup = %#v, %v", lookup, err)
	}
	point, err := client.ConditionalGet(ctx, pointRequest)
	if err != nil || !point.Found || string(point.Value) != "value" || point.ObservedRevision != required {
		t.Fatalf("point read = %#v, %v", point, err)
	}
	snapshot, err := client.ConditionalSnapshotGet(ctx, snapshotRequest)
	if err != nil || len(snapshot.Observations) != 2 || string(snapshot.Observations[0].Value) != "one" || snapshot.Observations[1].Found {
		t.Fatalf("snapshot read = %#v, %v", snapshot, err)
	}
}

func TestServerClientRejectsHostileWire(t *testing.T) {
	request, err := NewConditionalPointReadRequest("tenant", []byte("key"), nil)
	if err != nil {
		t.Fatal(err)
	}
	valid := makeConditionalPointReadWire(t, request, 1, 2, []byte("value"))
	validEnvelope := append(append([]byte(`{"ok":true,"data":`), valid...), '}')
	cases := []struct {
		name string
		body []byte
	}{
		{name: "unknown envelope field", body: append(append([]byte(`{"ok":true,"unknown":true,"data":`), valid...), '}')},
		{name: "duplicate envelope field", body: append(append([]byte(`{"ok":true,"ok":true,"data":`), valid...), '}')},
		{name: "trailing JSON", body: append(validEnvelope, []byte("null")...)},
		{name: "invalid UTF-8", body: bytes.Replace(validEnvelope, []byte("tenant"), []byte{'t', 'e', 'n', 0xff, 'n', 't'}, 1)},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(test.body)
			}))
			defer server.Close()
			client := newTestServerClient(t, server.URL)
			if _, err := client.ConditionalGet(context.Background(), request); ErrorCodeOf(err) != CodeProtocolViolation {
				t.Fatalf("hostile wire error = %v", err)
			}
		})
	}
}

func TestServerClientConditionalResponseLossIsIndeterminate(t *testing.T) {
	request, err := NewConditionalTransactionRequest("n", "req-1", []ConditionalTransactionCondition{{Key: []byte{1}, Expected: nil, Operator: CompareEqual}}, []ConditionalTransactionMutation{ConditionalIncrement([]byte{2}, 1)})
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, httpRequest *http.Request) {
		if httpRequest.URL.Path != "/api/storage" {
			t.Fatalf("path = %q", httpRequest.URL.Path)
		}
		select {
		case received <- struct{}{}:
		default:
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_ = connection.Close()
	}))
	defer server.Close()
	client := newTestServerClient(t, server.URL)
	if _, err := client.ExecuteConditionalTransaction(context.Background(), request); ErrorCodeOf(err) != CodeResultIndeterminate {
		t.Fatalf("response loss error = %v", err)
	}
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("server did not receive conditional transaction")
	}
}

func TestServerClientBindsRequestIDAndRevision(t *testing.T) {
	request, err := NewConditionalTransactionRequest("n", "req-1", []ConditionalTransactionCondition{{Key: []byte{1}, Expected: nil, Operator: CompareEqual}}, []ConditionalTransactionMutation{ConditionalIncrement([]byte{2}, 1)})
	if err != nil {
		t.Fatal(err)
	}
	built, err := buildClosedConditionalRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	lookup := makeFoundConditionalLookup(t, built, 9)
	rebound := rebindLookupCommand(t, lookup, strings.Repeat("a", 64))
	required := uint64(10)
	pointRequest, err := NewConditionalPointReadRequest("tenant", []byte("key"), &required)
	if err != nil {
		t.Fatal(err)
	}
	stalePoint := makeConditionalPointReadWire(t, pointRequest, 9, 12, []byte("value"))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, httpRequest *http.Request) {
		if httpRequest.URL.Path != "/api/storage" {
			t.Fatalf("path = %q", httpRequest.URL.Path)
		}
		var command struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(httpRequest.Body).Decode(&command); err != nil {
			t.Fatal(err)
		}
		switch command.Action {
		case "conditional_batch":
			writeServerTestError(w, "conflict", "request id has already been bound to another command")
		case "conditional_receipt":
			writeServerTestData(w, rebound)
		case "conditional_get":
			writeServerTestData(w, stalePoint)
		default:
			t.Fatalf("action = %q", command.Action)
		}
	}))
	defer server.Close()
	client := newTestServerClient(t, server.URL)
	if _, err := client.ExecuteConditionalTransaction(context.Background(), request); ErrorCodeOf(err) != CodeNativeConflict {
		t.Fatalf("request-id rebinding error = %v", err)
	}
	if _, err := client.ConditionalTransactionReceiptFor(context.Background(), request); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("receipt command rebinding error = %v", err)
	}
	if _, err := client.ConditionalGet(context.Background(), pointRequest); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("lower-bound revision error = %v", err)
	}
}

func TestServerClientDisablesRedirectsAndEnvironmentProxy(t *testing.T) {
	var redirected atomic.Bool
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirected.Store(true)
		writeServerTestData(w, []byte(`{"status":"ok"}`))
	}))
	defer redirectTarget.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, redirectTarget.URL, http.StatusFound)
	}))
	defer redirector.Close()
	redirectClient := newTestServerClient(t, redirector.URL)
	if _, err := redirectClient.Health(context.Background()); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("redirect error = %v", err)
	}
	if redirected.Load() {
		t.Fatal("redirect target received credentials")
	}

	proxyHits := make(chan struct{}, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		select {
		case proxyHits <- struct{}{}:
		default:
		}
		http.Error(w, "proxy must not be used", http.StatusBadGateway)
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeServerTestData(w, []byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()
	client := newTestServerClient(t, "http://talon.remote.test")
	transport := client.httpClient.Transport.(*http.Transport)
	upstreamURL, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstreamURL.Host)
	}
	if _, err := client.Health(context.Background()); err != nil {
		t.Fatalf("proxy-disabled health = %v", err)
	}
	select {
	case <-proxyHits:
		t.Fatal("HTTP_PROXY was used")
	default:
	}
}

func TestServerClientResponseAndRequestLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(maxServerHealthBytes+1))
		_, _ = w.Write([]byte(`{"ok":true,"data":{"status":"ok"}}`))
	}))
	defer server.Close()
	client := newTestServerClient(t, server.URL)
	if _, err := client.Health(context.Background()); ErrorCodeOf(err) != CodeProtocolViolation {
		t.Fatalf("oversized response error = %v", err)
	}
	large := strings.Repeat("x", maxServerRequestBytes)
	if _, err := client.do(context.Background(), http.MethodPost, server.URL, struct {
		Value string `json:"value"`
	}{Value: large}, maxServerResponseBytes, "request-limit", false, true); ErrorCodeOf(err) != CodeInvalidArgument {
		t.Fatalf("oversized request error = %v", err)
	}
}

func TestServerClientTLSAndMTLS(t *testing.T) {
	ca, caKey, roots := newServerTestCA(t)
	serverCertificate := newServerTestCertificate(t, ca, caKey, false, []string{"127.0.0.1"})
	clientCertificate := newServerTestCertificate(t, ca, caKey, true, nil)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeServerTestData(w, []byte(`{"status":"ok"}`))
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{serverCertificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    roots,
	}
	server.StartTLS()
	defer server.Close()

	withoutCertificate, err := NewServerClient(ServerClientConfig{
		BaseURL: server.URL, Token: "test-token", Timeout: time.Second,
		TLS: &ServerTLSConfig{RootCAs: roots},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := withoutCertificate.Health(context.Background()); ErrorCodeOf(err) != CodeNativeUnavailable {
		t.Fatalf("missing mTLS certificate error = %v", err)
	}
	withoutCertificate.Close()
	withCertificate, err := NewServerClient(ServerClientConfig{
		BaseURL: server.URL, Token: "test-token", Timeout: time.Second,
		TLS: &ServerTLSConfig{RootCAs: roots, ClientCertificates: []tls.Certificate{clientCertificate}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer withCertificate.Close()
	if health, err := withCertificate.Health(context.Background()); err != nil || health.Status != "ok" {
		t.Fatalf("mTLS health = %#v, %v", health, err)
	}
}

func newServerTestCA(t *testing.T) (*x509.Certificate, ed25519.PrivateKey, *x509.CertPool) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	encoded, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(encoded)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	return certificate, privateKey, pool
}

func newServerTestCertificate(t *testing.T, ca *x509.Certificate, caKey ed25519.PrivateKey, client bool, names []string) tls.Certificate {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if client {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	} else {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		for _, name := range names {
			if parsed := net.ParseIP(name); parsed != nil {
				template.IPAddresses = append(template.IPAddresses, parsed)
			} else {
				template.DNSNames = append(template.DNSNames, name)
			}
		}
	}
	encoded, err := x509.CreateCertificate(rand.Reader, template, ca, publicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{encoded, ca.Raw}, PrivateKey: privateKey}
}
