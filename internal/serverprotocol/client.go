/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package serverprotocol

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

// ServerHTTPProtocolVersion is the version of Talon Server's JSON-over-HTTP
// envelope consumed by ServerClient. Operation payload versions remain owned
// by their respective typed request types (for example, conditional v2).
const ServerHTTPProtocolVersion uint16 = 1

const (
	maxServerRequestBytes  = 32 << 20
	maxServerResponseBytes = 16 << 20
	maxServerHealthBytes   = 1 << 20
	maxServerKVKeyBytes    = 4 << 10
	maxServerKVValueBytes  = 8 << 20
)

// ServerTLSConfig is the explicit TLS/mTLS trust configuration for a Talon
// Server client. It intentionally has no insecure-skip-verify option: HTTPS
// clients always authenticate the server. A nil RootCAs uses the system pool.
// Supplying one or more ClientCertificates enables mutual TLS.
type ServerTLSConfig struct {
	RootCAs            *x509.CertPool
	ClientCertificates []tls.Certificate
	ServerName         string
}

// ServerClientConfig defines one remote Talon Server authority. Timeout is
// required and applies to the complete HTTP exchange. Empty Token is allowed
// only for a Server endpoint that is intentionally configured without auth.
type ServerClientConfig struct {
	BaseURL string
	Token   string
	Timeout time.Duration
	TLS     *ServerTLSConfig
}

// ServerClient is the typed client for a Talon Server process. It never reads
// proxy environment variables and never follows redirects, preventing a
// configured endpoint or bearer token from being silently sent elsewhere.
//
// Conditional methods deliberately reuse the same closed builders and strict
// receipt/read codecs as DB's embedded methods. The transport differs; the
// typed protocol identity and validation rules do not.
type ServerClient struct {
	healthURL  string
	kvURL      string
	storageURL string
	token      string
	httpClient *http.Client
}

// NewServerClient validates configuration and constructs a Server client.
// There is no environment-derived URL, token, timeout, proxy, or TLS policy.
func NewServerClient(config ServerClientConfig) (*ServerClient, error) {
	base, err := parseServerBaseURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	if config.Timeout <= 0 {
		return nil, newError(CodeInvalidArgument, "create server client", "timeout must be positive", nil)
	}
	if err := validateServerToken(config.Token); err != nil {
		return nil, err
	}
	tlsConfig, err := buildServerTLSConfig(base, config.TLS)
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		Proxy:              nil,
		TLSClientConfig:    tlsConfig,
		ForceAttemptHTTP2:  true,
		DisableCompression: true,
	}
	return &ServerClient{
		healthURL:  serverEndpoint(base, "/health"),
		kvURL:      serverEndpoint(base, "/api/kv"),
		storageURL: serverEndpoint(base, "/api/storage"),
		token:      config.Token,
		httpClient: &http.Client{
			Timeout:   config.Timeout,
			Transport: transport,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

func parseServerBaseURL(raw string) (*url.URL, error) {
	if raw == "" || strings.TrimSpace(raw) != raw {
		return nil, newError(CodeInvalidArgument, "create server client", "base URL is empty or contains surrounding whitespace", nil)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Hostname() == "" {
		return nil, newError(CodeInvalidArgument, "create server client", "base URL must be an absolute HTTP(S) URL", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, newError(CodeInvalidArgument, "create server client", "base URL must use http or https", nil)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, newError(CodeInvalidArgument, "create server client", "base URL must not contain credentials, a query, or a fragment", nil)
	}
	if strings.ContainsAny(parsed.Host, "\r\n\x00") || strings.ContainsAny(parsed.Path, "\r\n\x00") {
		return nil, newError(CodeInvalidArgument, "create server client", "base URL contains an invalid control byte", nil)
	}
	return parsed, nil
}

func serverEndpoint(base *url.URL, suffix string) string {
	copy := *base
	copy.Path = strings.TrimRight(copy.Path, "/") + suffix
	copy.RawPath = ""
	copy.RawQuery = ""
	copy.ForceQuery = false
	copy.Fragment = ""
	return copy.String()
}

func validateServerToken(token string) error {
	for _, character := range token {
		if character < 0x21 || character > 0x7e {
			return newError(CodeInvalidArgument, "create server client", "token contains whitespace, a control byte, or non-ASCII data", nil)
		}
	}
	return nil
}

func buildServerTLSConfig(base *url.URL, options *ServerTLSConfig) (*tls.Config, error) {
	if options == nil {
		return nil, nil
	}
	if base.Scheme != "https" {
		return nil, newError(CodeInvalidArgument, "create server client", "TLS or mTLS options require an HTTPS base URL", nil)
	}
	if strings.TrimSpace(options.ServerName) != options.ServerName || strings.ContainsAny(options.ServerName, "\r\n\x00") {
		return nil, newError(CodeInvalidArgument, "create server client", "TLS server name is invalid", nil)
	}
	serverName := options.ServerName
	if serverName == "" {
		serverName = base.Hostname()
	}
	if len(serverName) > 253 {
		return nil, newError(CodeInvalidArgument, "create server client", "TLS server name is too long", nil)
	}
	rootCAs := options.RootCAs
	if rootCAs != nil {
		rootCAs = rootCAs.Clone()
	}
	config := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		RootCAs:      rootCAs,
		ServerName:   serverName,
		Certificates: append([]tls.Certificate(nil), options.ClientCertificates...),
	}
	return config, nil
}

// Close releases idle connections. It does not cancel active requests; their
// context and the configured timeout remain authoritative.
func (client *ServerClient) Close() {
	if client != nil && client.httpClient != nil {
		client.httpClient.CloseIdleConnections()
	}
}

func (client *ServerClient) configured() bool {
	return client != nil && client.httpClient != nil && client.healthURL != "" && client.kvURL != "" && client.storageURL != ""
}

// ServerHealth is the typed projection of GET /health.
type ServerHealth struct {
	Status string
}

// Health checks the remote Server health endpoint and validates its versioned
// JSON envelope. A healthy current Server reports Status == "ok".
func (client *ServerClient) Health(ctx context.Context) (ServerHealth, error) {
	if !client.configured() {
		return ServerHealth{}, newError(CodeNativeUnavailable, "server health", "server client is not configured", nil)
	}
	data, err := client.do(ctx, http.MethodGet, client.healthURL, nil, maxServerHealthBytes, "server health", false, true)
	if err != nil {
		return ServerHealth{}, err
	}
	var result struct {
		Status string `json:"status"`
	}
	if err := decodeStrictJSON(data, &result); err != nil || result.Status != "ok" {
		if err == nil {
			err = fmt.Errorf("unexpected health status %q", result.Status)
		}
		return ServerHealth{}, remoteProtocolError("decode server health", err)
	}
	return ServerHealth{Status: result.Status}, nil
}

type serverCommand struct {
	Command string `json:"cmd"`
	Action  string `json:"action"`
	Params  any    `json:"params"`
}

type serverResponseEnvelope struct {
	OK         *bool           `json:"ok"`
	Data       json.RawMessage `json:"data"`
	Error      json.RawMessage `json:"error"`
	Code       json.RawMessage `json:"code"`
	Term       json.RawMessage `json:"term"`
	LeaderHint json.RawMessage `json:"leader_hint"`
}

func (client *ServerClient) storage(ctx context.Context, action string, params any, operation string, possiblyApplied bool) ([]byte, error) {
	if !client.configured() {
		return nil, newError(CodeNativeUnavailable, operation, "server client is not configured", nil)
	}
	return client.do(ctx, http.MethodPost, client.storageURL, serverCommand{Command: "storage", Action: action, Params: params}, maxServerResponseBytes, operation, possiblyApplied, true)
}

func (client *ServerClient) kv(ctx context.Context, action string, params any, operation string, possiblyApplied bool, requireData bool) ([]byte, error) {
	if !client.configured() {
		return nil, newError(CodeNativeUnavailable, operation, "server client is not configured", nil)
	}
	return client.do(ctx, http.MethodPost, client.kvURL, serverCommand{Command: "kv", Action: action, Params: params}, maxServerResponseBytes, operation, possiblyApplied, requireData)
}

func (client *ServerClient) do(ctx context.Context, method, endpoint string, command any, responseLimit int, operation string, possiblyApplied, requireData bool) ([]byte, error) {
	if !client.configured() {
		return nil, newError(CodeNativeUnavailable, operation, "server client is not configured", nil)
	}
	if ctx == nil {
		return nil, newError(CodeInvalidArgument, operation, "context is nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return nil, newError(CodeNativeUnavailable, operation, "context ended before the request could be sent", err)
	}
	var body []byte
	if command != nil {
		var err error
		body, err = marshalWithoutHTMLEscape(command)
		if err != nil {
			return nil, newError(CodeInvalidArgument, operation, "request could not be JSON encoded", err)
		}
		if len(body) > maxServerRequestBytes {
			return nil, newError(CodeInvalidArgument, operation, "request exceeds the SDK transport bound", nil)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, newError(CodeInvalidArgument, operation, "could not construct HTTP request", err)
	}
	request.Header.Set("Accept", "application/json")
	if command != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if client.token != "" {
		request.Header.Set("Authorization", "Bearer "+client.token)
	}
	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, remoteTransportError(operation, "remote request did not yield a response", err, possiblyApplied)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, remoteResponseError(operation, fmt.Sprintf("remote server returned HTTP %d", response.StatusCode), nil, possiblyApplied)
	}
	contentType := response.Header.Get("Content-Type")
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		if err == nil {
			err = fmt.Errorf("unexpected content type %q", contentType)
		}
		return nil, remoteResponseError(operation, "remote response has an invalid content type", err, possiblyApplied)
	}
	if response.ContentLength > int64(responseLimit) {
		return nil, remoteResponseError(operation, "remote response exceeds the SDK transport bound", nil, possiblyApplied)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, int64(responseLimit)+1))
	if err != nil {
		return nil, remoteResponseError(operation, "remote response could not be read", err, possiblyApplied)
	}
	if len(data) > responseLimit {
		return nil, remoteResponseError(operation, "remote response exceeds the SDK transport bound", nil, possiblyApplied)
	}
	decoded, err := decodeServerResponse(data, operation, requireData)
	if err != nil {
		if ErrorCodeOf(err) == CodeProtocolViolation {
			return nil, remoteResponseError(operation, "remote response violated the Server protocol", err, possiblyApplied)
		}
		return nil, err
	}
	return decoded, nil
}

func decodeServerResponse(data []byte, operation string, requireData bool) ([]byte, error) {
	var envelope serverResponseEnvelope
	if err := decodeStrictJSON(data, &envelope); err != nil {
		return nil, remoteProtocolError(operation, err)
	}
	if envelope.OK == nil {
		return nil, remoteProtocolError(operation, fmt.Errorf("response omitted ok"))
	}
	if *envelope.OK {
		if len(envelope.Error) != 0 || len(envelope.Code) != 0 || len(envelope.Term) != 0 || len(envelope.LeaderHint) != 0 {
			return nil, remoteProtocolError(operation, fmt.Errorf("successful response mixed in error metadata"))
		}
		if requireData && (len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null"))) {
			return nil, remoteProtocolError(operation, fmt.Errorf("successful response omitted data"))
		}
		return append([]byte(nil), envelope.Data...), nil
	}
	if len(envelope.Data) != 0 {
		return nil, remoteProtocolError(operation, fmt.Errorf("failed response included data"))
	}
	var message string
	if len(envelope.Error) == 0 || decodeStrictJSON(envelope.Error, &message) != nil || message == "" {
		return nil, remoteProtocolError(operation, fmt.Errorf("failed response omitted a valid error message"))
	}
	var nativeCode string
	if len(envelope.Code) != 0 {
		if err := decodeStrictJSON(envelope.Code, &nativeCode); err != nil || nativeCode == "" {
			return nil, remoteProtocolError(operation, fmt.Errorf("failed response contained an invalid machine code"))
		}
	}
	var term *uint64
	if len(envelope.Term) != 0 {
		var wireTerm canonicalUint64
		if err := decodeStrictJSON(envelope.Term, &wireTerm); err != nil || !wireTerm.Present {
			return nil, remoteProtocolError(operation, fmt.Errorf("failed response contained an invalid term"))
		}
		value := wireTerm.Value
		term = &value
	}
	leaderHint, err := decodeNativeLeaderHint(envelope.LeaderHint)
	if err != nil {
		return nil, remoteProtocolError(operation, fmt.Errorf("failed response contained an invalid leader hint: %w", err))
	}
	if (term != nil || leaderHint != nil) && nativeCode == "" {
		return nil, remoteProtocolError(operation, fmt.Errorf("routing metadata lacks a machine code"))
	}
	return nil, newNativeResponseError(operation, message, nativeCode, term, leaderHint)
}

func remoteProtocolError(operation string, cause error) error {
	return newError(CodeProtocolViolation, operation, "remote Server response violated its typed contract", cause)
}

func remoteTransportError(operation, message string, cause error, possiblyApplied bool) error {
	if possiblyApplied {
		return newError(CodeResultIndeterminate, operation, "remote write acknowledgement is indeterminate", cause)
	}
	return newError(CodeNativeUnavailable, operation, message, cause)
}

func remoteResponseError(operation, message string, cause error, possiblyApplied bool) error {
	if possiblyApplied {
		return newError(CodeResultIndeterminate, operation, "remote write acknowledgement is indeterminate", cause)
	}
	return newError(CodeProtocolViolation, operation, message, cause)
}

// ConditionalTransaction creates and executes one sealed v2 transaction.
func (client *ServerClient) ConditionalTransaction(ctx context.Context, namespace, requestID string, conditions []ConditionalTransactionCondition, mutations []ConditionalTransactionMutation) (ConditionalTransactionResult, error) {
	request, err := NewConditionalTransactionRequest(namespace, requestID, conditions, mutations)
	if err != nil {
		return ConditionalTransactionResult{}, err
	}
	return client.ExecuteConditionalTransaction(ctx, request)
}

// ExecuteConditionalTransaction sends an already sealed v2 request. On any
// acknowledgement loss or malformed acknowledgement it returns
// CodeResultIndeterminate; callers must retain request and use its exact
// receipt lookup rather than retrying with a new request ID or payload.
func (client *ServerClient) ExecuteConditionalTransaction(ctx context.Context, request ConditionalTransactionRequest) (ConditionalTransactionResult, error) {
	built, err := buildClosedConditionalRequest(request)
	if err != nil {
		return ConditionalTransactionResult{}, err
	}
	data, err := client.storage(ctx, "conditional_batch", built.wire, "remote conditional transaction", true)
	if err != nil {
		return ConditionalTransactionResult{}, err
	}
	result, err := decodeConditionalTransactionResult(data, built)
	if err != nil {
		return ConditionalTransactionResult{}, remoteResponseError("remote conditional transaction", "conditional transaction response violated its typed contract", err, true)
	}
	return result, nil
}

// ConditionalTransactionReceipt is a compatibility alias for
// ConditionalTransactionReceiptFor.
// Deprecated: use ConditionalTransactionReceiptFor.
func (client *ServerClient) ConditionalTransactionReceipt(ctx context.Context, request ConditionalTransactionRequest) (ConditionalReceiptLookup, error) {
	return client.ConditionalTransactionReceiptFor(ctx, request)
}

// ConditionalTransactionReceiptFor resolves the final receipt for one exact
// sealed transaction identity without executing another mutation.
func (client *ServerClient) ConditionalTransactionReceiptFor(ctx context.Context, request ConditionalTransactionRequest) (ConditionalReceiptLookup, error) {
	built, err := buildClosedConditionalRequest(request)
	if err != nil {
		return ConditionalReceiptLookup{}, err
	}
	data, err := client.storage(ctx, "conditional_receipt", struct {
		RequestID string `json:"request_id"`
	}{RequestID: built.requestID}, "remote conditional receipt", false)
	if err != nil {
		return ConditionalReceiptLookup{}, err
	}
	return decodeConditionalReceiptLookup(data, built)
}

// ConditionalGet reads one conditionally namespaced key and verifies the
// echoed request identity, lower-bound revision, and response digest.
func (client *ServerClient) ConditionalGet(ctx context.Context, request ConditionalPointReadRequest) (ConditionalPointReadResult, error) {
	wire, err := buildConditionalPointReadRequest(request)
	if err != nil {
		return ConditionalPointReadResult{}, err
	}
	data, err := client.storage(ctx, "conditional_get", wire, "remote conditional point read", false)
	if err != nil {
		return ConditionalPointReadResult{}, err
	}
	return decodeConditionalPointReadResult(data, request)
}

// ConditionalPointRead is a compatibility alias for ConditionalGet.
// Deprecated: use ConditionalGet.
func (client *ServerClient) ConditionalPointRead(ctx context.Context, request ConditionalPointReadRequest) (ConditionalPointReadResult, error) {
	return client.ConditionalGet(ctx, request)
}

// ConditionalSnapshotGet performs one typed, same-MVCC-snapshot multi-key
// read and verifies ordering, lower-bound revision, and response digest.
func (client *ServerClient) ConditionalSnapshotGet(ctx context.Context, request ConditionalSnapshotReadRequest) (ConditionalSnapshotReadResult, error) {
	wire, err := buildConditionalSnapshotReadRequest(request)
	if err != nil {
		return ConditionalSnapshotReadResult{}, err
	}
	data, err := client.storage(ctx, "conditional_snapshot_get", wire, "remote conditional snapshot read", false)
	if err != nil {
		return ConditionalSnapshotReadResult{}, err
	}
	return decodeConditionalSnapshotReadResult(data, request)
}

// ConditionalSnapshotRead is a compatibility alias for ConditionalSnapshotGet.
// Deprecated: use ConditionalSnapshotGet.
func (client *ServerClient) ConditionalSnapshotRead(ctx context.Context, request ConditionalSnapshotReadRequest) (ConditionalSnapshotReadResult, error) {
	return client.ConditionalSnapshotGet(ctx, request)
}

type serverKVSetParams struct {
	Key   string  `json:"key"`
	Value string  `json:"value"`
	TTL   *uint64 `json:"ttl,omitempty"`
}

type serverKVKeyParams struct {
	Key string `json:"key"`
}

func validateServerKV(key string, value *string) error {
	if len(key) == 0 || len(key) > maxServerKVKeyBytes || !utf8.ValidString(key) || strings.ContainsRune(key, '\x00') {
		return newError(CodeInvalidArgument, "remote KV", "key is empty, too large, not UTF-8, or contains NUL", nil)
	}
	if value != nil && (len(*value) > maxServerKVValueBytes || !utf8.ValidString(*value) || strings.ContainsRune(*value, '\x00')) {
		return newError(CodeInvalidArgument, "remote KV", "value is too large, not UTF-8, or contains NUL", nil)
	}
	return nil
}

// KvSet is a typed migration helper for Server KV. It does not provide a
// conditional-v2 receipt; a response loss is reported as indeterminate.
func (client *ServerClient) KvSet(ctx context.Context, key, value string, ttl *uint64) error {
	if err := validateServerKV(key, &value); err != nil {
		return err
	}
	_, err := client.kv(ctx, "set", serverKVSetParams{Key: key, Value: value, TTL: ttl}, "remote KV set", true, false)
	return err
}

// KvGet returns nil for an absent key. Empty and absent values remain distinct.
func (client *ServerClient) KvGet(ctx context.Context, key string) (*string, error) {
	if err := validateServerKV(key, nil); err != nil {
		return nil, err
	}
	data, err := client.kv(ctx, "get", serverKVKeyParams{Key: key}, "remote KV get", false, true)
	if err != nil {
		return nil, err
	}
	var result struct {
		Value json.RawMessage `json:"value"`
	}
	if err := decodeStrictJSON(data, &result); err != nil || len(result.Value) == 0 {
		if err == nil {
			err = fmt.Errorf("value is missing")
		}
		return nil, remoteProtocolError("decode remote KV get", err)
	}
	if bytes.Equal(result.Value, []byte("null")) {
		return nil, nil
	}
	var value string
	if err := decodeStrictJSON(result.Value, &value); err != nil {
		return nil, remoteProtocolError("decode remote KV get", err)
	}
	if err := validateServerKV(key, &value); err != nil {
		return nil, remoteProtocolError("decode remote KV get", err)
	}
	return &value, nil
}

// KvDel deletes key and reports whether it had existed. A response loss is
// indeterminate because the Server may already have processed the deletion.
func (client *ServerClient) KvDel(ctx context.Context, key string) (bool, error) {
	if err := validateServerKV(key, nil); err != nil {
		return false, err
	}
	data, err := client.kv(ctx, "del", serverKVKeyParams{Key: key}, "remote KV delete", true, true)
	if err != nil {
		return false, err
	}
	var result struct {
		Deleted *bool `json:"deleted"`
	}
	if err := decodeStrictJSON(data, &result); err != nil || result.Deleted == nil {
		if err == nil {
			err = fmt.Errorf("deleted is missing")
		}
		return false, remoteResponseError("remote KV delete", "KV delete response violated its typed contract", err, true)
	}
	return *result.Deleted, nil
}

// KvExists reports whether key exists without exposing a Server envelope.
func (client *ServerClient) KvExists(ctx context.Context, key string) (bool, error) {
	if err := validateServerKV(key, nil); err != nil {
		return false, err
	}
	data, err := client.kv(ctx, "exists", serverKVKeyParams{Key: key}, "remote KV exists", false, true)
	if err != nil {
		return false, err
	}
	var result struct {
		Exists *bool `json:"exists"`
	}
	if err := decodeStrictJSON(data, &result); err != nil || result.Exists == nil {
		if err == nil {
			err = fmt.Errorf("exists is missing")
		}
		return false, remoteProtocolError("decode remote KV exists", err)
	}
	return *result.Exists, nil
}

// KvSetNX writes only when key is absent. It is retained for KV migration;
// new durable business mutations should use ExecuteConditionalTransaction.
func (client *ServerClient) KvSetNX(ctx context.Context, key, value string, ttl *uint64) (bool, error) {
	if err := validateServerKV(key, &value); err != nil {
		return false, err
	}
	data, err := client.kv(ctx, "setnx", serverKVSetParams{Key: key, Value: value, TTL: ttl}, "remote KV setnx", true, true)
	if err != nil {
		return false, err
	}
	var result struct {
		Set *bool `json:"set"`
	}
	if err := decodeStrictJSON(data, &result); err != nil || result.Set == nil {
		if err == nil {
			err = fmt.Errorf("set is missing")
		}
		return false, remoteResponseError("remote KV setnx", "KV setnx response violated its typed contract", err, true)
	}
	return *result.Set, nil
}
