/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */
// Package talon 提供 Talon 数据库的 Go SDK。
//
// 通过 cgo 动态加载经过 talon-native-manifest-v1 验证的 Core 原生库。
package talon

/*
#include <stdlib.h>
#include "native_loader.h"
*/
import "C"
import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"unicode/utf8"
	"unsafe"
)

// DB 是 Talon 数据库客户端（嵌入式模式）。
type DB struct {
	mu         sync.RWMutex
	handle     *C.TalonSDKHandle
	nativeInfo NativeInfo
}

// OpenOptions contains trusted native-runtime configuration.
type OpenOptions struct {
	Native NativePolicy
}

var (
	nativeLoadMu sync.Mutex
	loadedNative *verifiedNative
)

// Open opens a database using the fail-closed native policy from environment.
// Missing or incomplete trust configuration is an error; there is no unsigned
// library fallback.
func Open(path string) (*DB, error) {
	policy, err := NativePolicyFromEnvironment()
	if err != nil {
		return nil, newError(CodeNativeVerification, "open", "native trust policy is incomplete", err)
	}
	return OpenWithOptions(path, OpenOptions{Native: policy})
}

// OpenWithOptions verifies the signed native bundle before dlopen and before a
// Core database handle is created.
func OpenWithOptions(path string, options OpenOptions) (*DB, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 || !utf8.ValidString(path) {
		return nil, newError(CodeInvalidArgument, "open", "database path is empty, contains NUL, or is not UTF-8", nil)
	}
	verified, err := ensureNativeLoaded(options.Native)
	if err != nil {
		return nil, err
	}
	cs := C.CString(path)
	defer C.free(unsafe.Pointer(cs))
	var errorCode [128]C.char
	h := C.talon_sdk_open(cs, &errorCode[0], C.size_t(len(errorCode)))
	if h == nil {
		return nil, nativeFailure("open", fmt.Sprintf("failed to open database at %s", path), C.GoString(&errorCode[0]))
	}
	return &DB{handle: h, nativeInfo: verified.Info}, nil
}

func ensureNativeLoaded(policy NativePolicy) (*verifiedNative, error) {
	nativeLoadMu.Lock()
	defer nativeLoadMu.Unlock()
	policyHash := hashNativePolicy(policy)
	if loadedNative != nil {
		if loadedNative.policyHash != policyHash {
			return nil, newError(CodeNativeVerification, "load native library", "a different native trust policy is already active in this process", nil)
		}
		return loadedNative, nil
	}
	verified, err := verifyNativeBundle(policy)
	if err != nil {
		return nil, newError(CodeNativeVerification, "verify native bundle", "native bundle verification failed", err)
	}
	libraryPath := C.CString(verified.LibraryPath)
	rc := C.talon_sdk_load(libraryPath)
	C.free(unsafe.Pointer(libraryPath))
	if rc != 0 {
		message := "dynamic loader rejected the verified native library"
		if diagnostic := C.talon_sdk_loader_error(); diagnostic != nil {
			message += ": " + C.GoString(diagnostic)
		}
		_ = removeVerifiedNative(verified)
		return nil, newError(CodeNativeLoad, "load native library", message, nil)
	}
	if err := attestLoadedNative(verified); err != nil {
		C.talon_sdk_unload()
		_ = removeVerifiedNative(verified)
		return nil, newError(CodeNativeVerification, "attest loaded native library", "Core runtime identity does not match the signed artifact manifest", err)
	}
	loadedNative = verified
	return verified, nil
}

func removeVerifiedNative(verified *verifiedNative) error {
	if verified == nil || verified.tempDir == "" {
		return nil
	}
	return os.RemoveAll(verified.tempDir)
}

func attestLoadedNative(verified *verifiedNative) error {
	var output *C.char
	var errorCode [128]C.char
	if C.talon_sdk_build_manifest(&output, &errorCode[0], C.size_t(len(errorCode))) != 0 {
		return fmt.Errorf("talon_build_manifest failed with machine code %q", C.GoString(&errorCode[0]))
	}
	if output == nil {
		return fmt.Errorf("talon_build_manifest returned a null result")
	}
	defer C.talon_sdk_free_string(output)
	data, err := boundedCString(output, maxManifestBytes)
	if err != nil {
		return fmt.Errorf("read Core build manifest: %w", err)
	}
	build, err := verifyCoreBuildIdentity(data, verified)
	if err != nil {
		return err
	}
	if err := verifyRequiredRuntimeCapabilities(build, verified.requiredCapabilities); err != nil {
		return err
	}
	for _, symbol := range verified.Manifest.ABI.RequiredSymbols {
		name := C.CString(symbol)
		present := C.talon_sdk_has_symbol(name) != 0
		C.free(unsafe.Pointer(name))
		if !present {
			return fmt.Errorf("loaded Core omitted required symbol %q", symbol)
		}
	}
	verified.Info.Features = append([]string(nil), build.Features...)
	verified.Info.Capabilities = cloneNativeCapabilities(build.Capabilities)
	return nil
}

func boundedCString(value *C.char, maxLength int) ([]byte, error) {
	if value == nil || maxLength < 0 {
		return nil, fmt.Errorf("C string pointer or bound is invalid")
	}
	var length C.size_t
	if C.talon_sdk_bounded_strlen(value, C.size_t(maxLength), &length) != 0 {
		return nil, fmt.Errorf("C string is not NUL-terminated within %d bytes", maxLength)
	}
	if uint64(length) > uint64(maxLength) || uint64(length) > math.MaxInt32 {
		return nil, fmt.Errorf("C string exceeds the Go copy bound")
	}
	return C.GoBytes(unsafe.Pointer(value), C.int(length)), nil
}

// Close 关闭数据库。
func (db *DB) Close() {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.handle != nil {
		C.talon_sdk_close(db.handle)
		db.handle = nil
	}
}

// Persist 刷盘。
func (db *DB) Persist() error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.handle == nil {
		return newError(CodeDatabaseClosed, "persist", "database is closed", nil)
	}
	var errorCode [128]C.char
	if C.talon_sdk_persist(db.handle, &errorCode[0], C.size_t(len(errorCode))) != 0 {
		return nativeFailure("persist", "native persist failed", C.GoString(&errorCode[0]))
	}
	return nil
}

type cmdResult struct {
	OK         *bool           `json:"ok"`
	Data       json.RawMessage `json:"data"`
	Code       string          `json:"code"`
	Error      string          `json:"error"`
	Term       *uint64         `json:"term"`
	LeaderHint json.RawMessage `json:"leader_hint"`
}

// execute 执行通用命令，返回 data 原始 JSON。
func (db *DB) execute(module, action string, params interface{}) (json.RawMessage, error) {
	return db.executeWithBounds(module, action, params, maxNativeJSONRequestBytes, maxNativeJSONResultBytes)
}

func (db *DB) executeWithBounds(module, action string, params interface{}, requestLimit, resultLimit int) (json.RawMessage, error) {
	if requestLimit <= 0 || resultLimit <= 0 {
		return nil, newError(CodeInvalidArgument, "execute", "native JSON bounds must be positive", nil)
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.handle == nil {
		return nil, newError(CodeDatabaseClosed, "execute", "database is closed", nil)
	}
	if params == nil {
		params = map[string]interface{}{}
	}
	cmd := map[string]interface{}{
		"module": module, "action": action, "params": params,
	}
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		return nil, operationError(ErrorEncode, module+"."+action, "无法编码 talon_execute 请求", err)
	}
	if len(cmdBytes) > requestLimit {
		return nil, newError(CodeInvalidArgument, "execute", "native JSON request exceeds the SDK bound", nil)
	}
	cs := C.CString(string(cmdBytes))
	defer C.free(unsafe.Pointer(cs))
	var outPtr *C.char
	var errorCode [128]C.char
	rc := C.talon_sdk_execute(db.handle, cs, &outPtr, &errorCode[0], C.size_t(len(errorCode)))
	if rc != 0 {
		return nil, nativeFailure("execute", "talon_execute failed", C.GoString(&errorCode[0]))
	}
	if outPtr == nil {
		return nil, newError(CodeProtocolViolation, "execute", "talon_execute returned a null result", nil)
	}
	defer C.talon_sdk_free_string(outPtr)
	outBytes, err := boundedCString(outPtr, resultLimit)
	if err != nil {
		return nil, newError(CodeProtocolViolation, "execute", "native JSON response exceeds the SDK bound or is unterminated", err)
	}
	var result cmdResult
	if err := decodeStrictJSON(outBytes, &result); err != nil {
		return nil, newError(CodeProtocolViolation, "execute", "invalid native JSON response", err)
	}
	if result.OK == nil {
		return nil, newError(CodeProtocolViolation, "execute", "native JSON response omitted ok", nil)
	}
	if !*result.OK {
		if len(result.Data) != 0 {
			return nil, newError(CodeProtocolViolation, "execute", "failed native response included data", nil)
		}
		msg := result.Error
		if msg == "" {
			msg = "native operation failed without a diagnostic"
		}
		leaderHint, err := decodeNativeLeaderHint(result.LeaderHint)
		if err != nil {
			return nil, newError(CodeProtocolViolation, "execute", "native error response included an invalid leader_hint", err)
		}
		return nil, newNativeResponseError("execute", msg, result.Code, result.Term, leaderHint)
	}
	if len(result.Data) == 0 {
		return nil, newError(CodeProtocolViolation, "execute", "successful native response omitted data", nil)
	}
	if result.Code != "" || result.Error != "" || result.Term != nil || len(result.LeaderHint) != 0 {
		return nil, newError(CodeProtocolViolation, "execute", "successful native response mixed error fields into its envelope", nil)
	}
	if len(result.Data) == 0 || bytes.Equal(bytes.TrimSpace(result.Data), []byte("null")) {
		return nil, operationError(ErrorProtocol, module+"."+action, "talon_execute 成功响应缺少 data 字段", nil)
	}
	return result.Data, nil
}

func decodeNativeLeaderHint(data json.RawMessage) (*NativeLeaderHint, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	var hint NativeLeaderHint
	if err := decodeStrictJSON(data, &hint); err != nil {
		return nil, err
	}
	if strings.TrimSpace(hint.NodeID) == "" || hint.Address == nil || strings.TrimSpace(*hint.Address) == "" {
		return nil, fmt.Errorf("leader_hint requires non-empty node_id and address")
	}
	return &hint, nil
}

// NativeInfo returns the verified signed identity used by this DB.
func (db *DB) NativeInfo() NativeInfo {
	info := db.nativeInfo
	info.Features = append([]string(nil), db.nativeInfo.Features...)
	info.Capabilities = cloneNativeCapabilities(db.nativeInfo.Capabilities)
	info.Gates = make(map[string]CapabilityGate, len(db.nativeInfo.Gates))
	for name, gate := range db.nativeInfo.Gates {
		info.Gates[name] = gate
	}
	return info
}

func cloneNativeCapabilities(capabilities []NativeCapability) []NativeCapability {
	result := make([]NativeCapability, len(capabilities))
	for index, capability := range capabilities {
		result[index] = capability
		if capability.Reason != nil {
			reason := *capability.Reason
			result[index].Reason = &reason
		}
		if capability.Limits != nil {
			result[index].Limits = &NativeCapabilityLimits{
				MaxValueBytes:                  cloneUint64Pointer(capability.Limits.MaxValueBytes),
				MaxAggregateCommandBytes:       cloneUint64Pointer(capability.Limits.MaxAggregateCommandBytes),
				MaxCompactReceiptBytes:         cloneUint64Pointer(capability.Limits.MaxCompactReceiptBytes),
				MaxRequestBytes:                cloneUint64Pointer(capability.Limits.MaxRequestBytes),
				MaxResponseBytes:               cloneUint64Pointer(capability.Limits.MaxResponseBytes),
				MaxSnapshotValueBytes:          cloneUint64Pointer(capability.Limits.MaxSnapshotValueBytes),
				MaxSnapshotAggregateValueBytes: cloneUint64Pointer(capability.Limits.MaxSnapshotAggregateValueBytes),
				present:                        capability.Limits.present,
			}
		}
	}
	return result
}

// CapabilityGate returns the signed gate state without treating it as an
// implemented SDK operation.
func (db *DB) CapabilityGate(name string) (CapabilityGate, bool) {
	gate, ok := db.nativeInfo.Gates[name]
	return gate, ok
}

// RequireStableNativeErrorCodes is the explicit error-ABI gate. Diagnostic
// text is intentionally insufficient.
func (db *DB) RequireStableNativeErrorCodes() error {
	if containsString(db.nativeInfo.Features, "native_error_codes_v1") {
		return nil
	}
	return ErrStableNativeErrorCodesUnavailable
}

// RequireCapability enforces both the signed Core feature gate and this SDK's
// implementation gate. No capability is inferred from a tag or symbol text.
func (db *DB) RequireCapability(name string) error {
	if gate, ok := db.nativeInfo.Gates[name]; ok && gate.Status != "available" {
		return newError(CodeCapabilityUnavailable, "require capability", fmt.Sprintf("%s is %s: %s", name, gate.Status, gate.Reason), nil)
	}
	if name == "storage_conditional_batch_v1" {
		return ErrStorageConditionalBatchUnavailable
	}
	var coreCapability *NativeCapability
	for index := range db.nativeInfo.Capabilities {
		if db.nativeInfo.Capabilities[index].Name == name {
			coreCapability = &db.nativeInfo.Capabilities[index]
			break
		}
	}
	if coreCapability == nil {
		return newError(CodeCapabilityUnavailable, "require capability", fmt.Sprintf("loaded Core did not attest capability %q", name), nil)
	}
	if coreCapability.Status != "available" {
		reason := ""
		if coreCapability.Reason != nil {
			reason = ": " + *coreCapability.Reason
		}
		return newError(CodeCapabilityUnavailable, "require capability", fmt.Sprintf("%s is %s%s", name, coreCapability.Status, reason), nil)
	}
	if name == "native_conditional_transaction_v2" && containsString(db.nativeInfo.Features, name) && containsString(db.nativeInfo.Features, "conditional_transaction_command_digest_v1") {
		return nil
	}
	if name == compactConditionalCapability && coreCapability.Version == conditionalTransactionCompactVersion && validCompactConditionalLimits(coreCapability.Limits) && containsString(db.nativeInfo.Features, compactConditionalCapability) && containsString(db.nativeInfo.Features, compactReceiptFeature) && containsString(db.nativeInfo.Features, "conditional_transaction_command_digest_v1") {
		return nil
	}
	if name == "storage_conditional_point_read" && coreCapability.Version == conditionalPointReadVersion && containsString(db.nativeInfo.Features, "storage_conditional_point_read_v1") {
		return nil
	}
	if name == "storage_conditional_snapshot_read" && coreCapability.Version == conditionalSnapshotReadVersion && containsString(db.nativeInfo.Features, "storage_conditional_snapshot_read_v1") {
		return nil
	}
	if name == "revision_stream" && coreCapability.Version == revisionStreamVersion && containsString(db.nativeInfo.Features, "revision_stream_v1") && containsString(db.nativeInfo.Features, "revision_stream_v2_mmr_proof") {
		return nil
	}
	return newError(CodeCapabilityUnavailable, "require capability", fmt.Sprintf("capability %q is not implemented by this SDK", name), nil)
}

func nativeFailure(operation, fallback, nativeCode string) error {
	return newNativeError(operation, fallback, nativeCode)
}

// Stats 获取引擎统计信息。
func (db *DB) Stats() (map[string]interface{}, error) {
	data, err := db.execute("stats", "", nil)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	return m, json.Unmarshal(data, &m)
}

// SQL 执行 SQL 语句。
func (db *DB) SQL(query string) ([][]interface{}, error) {
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	out := make([][]interface{}, len(rows))
	for rowIndex, row := range rows {
		out[rowIndex] = make([]interface{}, len(row))
		for columnIndex, value := range row {
			out[rowIndex][columnIndex] = value.GoValue()
		}
	}
	return out, nil
}

// ── KV ──

// KvSet 设置键值。
func (db *DB) KvSet(key, value string, ttl *uint64) error {
	p := map[string]interface{}{"key": key, "value": value}
	if ttl != nil {
		p["ttl"] = *ttl
	}
	_, err := db.execute("kv", "set", p)
	return err
}

// KvGet 获取值。
func (db *DB) KvGet(key string) (*string, error) {
	data, err := db.execute("kv", "get", map[string]string{"key": key})
	if err != nil {
		return nil, err
	}
	var out struct {
		Value *string `json:"value"`
	}
	return out.Value, json.Unmarshal(data, &out)
}

// KvDel 删除键。
func (db *DB) KvDel(key string) (bool, error) {
	data, err := db.execute("kv", "del", map[string]string{"key": key})
	if err != nil {
		return false, err
	}
	var out struct {
		Deleted bool `json:"deleted"`
	}
	return out.Deleted, json.Unmarshal(data, &out)
}

// KvExists 检查键是否存在。
func (db *DB) KvExists(key string) (bool, error) {
	data, err := db.execute("kv", "exists", map[string]string{"key": key})
	if err != nil {
		return false, err
	}
	var out struct {
		Exists bool `json:"exists"`
	}
	return out.Exists, json.Unmarshal(data, &out)
}

// KvIncr 原子自增。
func (db *DB) KvIncr(key string) (int64, error) {
	data, err := db.execute("kv", "incr", map[string]string{"key": key})
	if err != nil {
		return 0, err
	}
	var out struct {
		Value int64 `json:"value"`
	}
	return out.Value, json.Unmarshal(data, &out)
}

// KvKeys 前缀扫描。
func (db *DB) KvKeys(prefix string) ([]string, error) {
	data, err := db.execute("kv", "keys", map[string]string{"prefix": prefix})
	if err != nil {
		return nil, err
	}
	var out struct {
		Keys []string `json:"keys"`
	}
	return out.Keys, json.Unmarshal(data, &out)
}

// KvMset 批量设置键值。
func (db *DB) KvMset(keys, values []string) error {
	_, err := db.execute("kv", "mset", map[string]interface{}{"keys": keys, "values": values})
	return err
}

// KvMget 批量获取值。
func (db *DB) KvMget(keys []string) ([]*string, error) {
	data, err := db.execute("kv", "mget", map[string]interface{}{"keys": keys})
	if err != nil {
		return nil, err
	}
	var out struct {
		Values []interface{} `json:"values"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	result := make([]*string, len(out.Values))
	for i, v := range out.Values {
		if s, ok := v.(string); ok {
			result[i] = &s
		}
	}
	return result, nil
}

// KvKeysMatch glob 模式匹配（支持 * 和 ?）。
func (db *DB) KvKeysMatch(pattern string) ([]string, error) {
	data, err := db.execute("kv", "keys_match", map[string]string{"pattern": pattern})
	if err != nil {
		return nil, err
	}
	var out struct {
		Keys []string `json:"keys"`
	}
	return out.Keys, json.Unmarshal(data, &out)
}

// KvExpire 设置 TTL。
func (db *DB) KvExpire(key string, seconds uint64) error {
	_, err := db.execute("kv", "expire", map[string]interface{}{"key": key, "seconds": seconds})
	return err
}

// KvTtl 查询剩余 TTL。
func (db *DB) KvTtl(key string) (*uint64, error) {
	data, err := db.execute("kv", "ttl", map[string]string{"key": key})
	if err != nil {
		return nil, err
	}
	var out struct {
		TTL *uint64 `json:"ttl"`
	}
	return out.TTL, json.Unmarshal(data, &out)
}

// KvIncrBy 按 delta 自增。
func (db *DB) KvIncrBy(key string, delta int64) (int64, error) {
	data, err := db.execute("kv", "incrby", map[string]interface{}{"key": key, "delta": delta})
	if err != nil {
		return 0, err
	}
	var out struct {
		Value int64 `json:"value"`
	}
	return out.Value, json.Unmarshal(data, &out)
}

// KvDecrBy 按 delta 自减。
func (db *DB) KvDecrBy(key string, delta int64) (int64, error) {
	data, err := db.execute("kv", "decrby", map[string]interface{}{"key": key, "delta": delta})
	if err != nil {
		return 0, err
	}
	var out struct {
		Value int64 `json:"value"`
	}
	return out.Value, json.Unmarshal(data, &out)
}

// KvSetNX 仅在 key 不存在时写入，返回是否成功写入。
func (db *DB) KvSetNX(key, value string, ttl *uint64) (bool, error) {
	p := map[string]interface{}{"key": key, "value": value}
	if ttl != nil {
		p["ttl"] = *ttl
	}
	data, err := db.execute("kv", "setnx", p)
	if err != nil {
		return false, err
	}
	var out struct {
		Set bool `json:"set"`
	}
	return out.Set, json.Unmarshal(data, &out)
}

// KvKeysLimit 分页前缀扫描（亿级安全）。
func (db *DB) KvKeysLimit(prefix string, offset, limit uint64) ([]string, error) {
	data, err := db.execute("kv", "keys_limit", map[string]interface{}{
		"prefix": prefix, "offset": offset, "limit": limit,
	})
	if err != nil {
		return nil, err
	}
	var out struct {
		Keys []string `json:"keys"`
	}
	return out.Keys, json.Unmarshal(data, &out)
}

// KvScanLimit 分页 KV 扫描，返回原始 JSON。
func (db *DB) KvScanLimit(prefix string, offset, limit uint64) (json.RawMessage, error) {
	return db.execute("kv", "scan_limit", map[string]interface{}{
		"prefix": prefix, "offset": offset, "limit": limit,
	})
}

// KvCount 获取 Key 总数。
func (db *DB) KvCount() (uint64, error) {
	data, err := db.execute("kv", "count", nil)
	if err != nil {
		return 0, err
	}
	var out struct {
		Count uint64 `json:"count"`
	}
	return out.Count, json.Unmarshal(data, &out)
}

// ── Vector ──

// VectorInsert 插入向量。
func (db *DB) VectorInsert(name string, id uint64, vector []float32) error {
	_, err := db.execute("vector", "insert", map[string]interface{}{
		"name": name, "id": id, "vector": vector,
	})
	return err
}

// VectorSearch 向量搜索，返回原始 JSON。
func (db *DB) VectorSearch(name string, vector []float32, k int, metric string) (json.RawMessage, error) {
	if metric == "" {
		metric = "cosine"
	}
	return db.execute("vector", "search", map[string]interface{}{
		"name": name, "vector": vector, "k": k, "metric": metric,
	})
}

// VectorDelete 删除向量。
func (db *DB) VectorDelete(name string, id uint64) error {
	_, err := db.execute("vector", "delete", map[string]interface{}{
		"name": name, "id": id,
	})
	return err
}

// VectorCount 获取向量数量。
func (db *DB) VectorCount(name string) (uint64, error) {
	data, err := db.execute("vector", "count", map[string]interface{}{"name": name})
	if err != nil {
		return 0, err
	}
	var out struct {
		Count uint64 `json:"count"`
	}
	return out.Count, json.Unmarshal(data, &out)
}

// VectorBatchInsert 批量插入向量。
func (db *DB) VectorBatchInsert(name string, items []map[string]interface{}) (uint64, error) {
	data, err := db.execute("vector", "batch_insert", map[string]interface{}{
		"name": name, "items": items,
	})
	if err != nil {
		return 0, err
	}
	var out struct {
		Inserted uint64 `json:"inserted"`
	}
	return out.Inserted, json.Unmarshal(data, &out)
}

// VectorBatchSearch 批量向量搜索。
func (db *DB) VectorBatchSearch(name string, vectors [][]float32, k int, metric string) (json.RawMessage, error) {
	if metric == "" {
		metric = "cosine"
	}
	return db.execute("vector", "batch_search", map[string]interface{}{
		"name": name, "vectors": vectors, "k": k, "metric": metric,
	})
}

// VectorSetEfSearch 设置向量索引运行时搜索宽度 ef_search。
func (db *DB) VectorSetEfSearch(name string, efSearch int) error {
	_, err := db.execute("vector", "set_ef_search", map[string]interface{}{
		"name": name, "ef_search": efSearch,
	})
	return err
}

// ── Cluster ──

// ClusterStatus 查询集群状态（角色/LSN/从节点列表）。
func (db *DB) ClusterStatus() (map[string]interface{}, error) {
	data, err := db.execute("cluster", "status", nil)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	return m, json.Unmarshal(data, &m)
}

// ClusterRole 查询当前集群角色。
func (db *DB) ClusterRole() (string, error) {
	data, err := db.execute("cluster", "role", nil)
	if err != nil {
		return "", err
	}
	var out struct {
		Role interface{} `json:"role"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	switch v := out.Role.(type) {
	case string:
		return v, nil
	default:
		b, _ := json.Marshal(v)
		return string(b), nil
	}
}

// ClusterPromote 将 Replica 提升为 Primary。
func (db *DB) ClusterPromote() error {
	_, err := db.execute("cluster", "promote", nil)
	return err
}

// ClusterReplicas 查询从节点列表。
func (db *DB) ClusterReplicas() (json.RawMessage, error) {
	return db.execute("cluster", "replicas", nil)
}

// ── Ops ──

// DatabaseStats 获取数据库全局统计信息。
func (db *DB) DatabaseStats() (map[string]interface{}, error) {
	data, err := db.execute("database_stats", "", nil)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	return m, json.Unmarshal(data, &m)
}

// HealthCheck 执行健康检查。
func (db *DB) HealthCheck() (map[string]interface{}, error) {
	data, err := db.execute("health_check", "", nil)
	if err != nil {
		return nil, err
	}
	var m map[string]interface{}
	return m, json.Unmarshal(data, &m)
}
