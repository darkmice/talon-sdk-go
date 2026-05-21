/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */
// Package talon 提供 Talon 数据库的 Go SDK。
//
// 通过 cgo 封装 talon_execute C ABI。预编译原生库（include/ 和 lib/）随
// 模块一起发布，go get 后即可直接编译，无需额外下载步骤。
package talon

/*
#include <stdlib.h>
#include "include/talon.h"
*/
import "C"
import (
	"encoding/json"
	"fmt"
	"unsafe"
)

// TalonError 表示 Talon 操作错误。
type TalonError struct {
	Message string
}

func (e *TalonError) Error() string { return e.Message }

// DB 是 Talon 数据库客户端（嵌入式模式）。
type DB struct {
	handle *C.TalonHandle
}

// Open 打开数据库。
func Open(path string) (*DB, error) {
	cs := C.CString(path)
	defer C.free(unsafe.Pointer(cs))
	h := C.talon_open(cs)
	if h == nil {
		return nil, &TalonError{fmt.Sprintf("无法打开数据库: %s", path)}
	}
	return &DB{handle: h}, nil
}

// Close 关闭数据库。
func (db *DB) Close() {
	if db.handle != nil {
		C.talon_close(db.handle)
		db.handle = nil
	}
}

// Persist 刷盘。
func (db *DB) Persist() error {
	if db.handle == nil {
		return &TalonError{"数据库已关闭"}
	}
	if C.talon_persist(db.handle) != 0 {
		return &TalonError{"persist 失败"}
	}
	return nil
}

type cmdResult struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error string          `json:"error"`
}

// execute 执行通用命令，返回 data 原始 JSON。
func (db *DB) execute(module, action string, params interface{}) (json.RawMessage, error) {
	if db.handle == nil {
		return nil, &TalonError{"数据库已关闭"}
	}
	if params == nil {
		params = map[string]interface{}{}
	}
	cmd := map[string]interface{}{
		"module": module, "action": action, "params": params,
	}
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		return nil, err
	}
	cs := C.CString(string(cmdBytes))
	defer C.free(unsafe.Pointer(cs))
	var outPtr *C.char
	rc := C.talon_execute(db.handle, cs, &outPtr)
	if rc != 0 {
		return nil, &TalonError{"talon_execute 调用失败"}
	}
	if outPtr == nil {
		return nil, &TalonError{"talon_execute 返回空指针"}
	}
	defer C.talon_free_string(outPtr)
	outStr := C.GoString(outPtr)
	var result cmdResult
	if err := json.Unmarshal([]byte(outStr), &result); err != nil {
		return nil, err
	}
	if !result.OK {
		msg := result.Error
		if msg == "" {
			msg = "未知错误"
		}
		return nil, &TalonError{msg}
	}
	return result.Data, nil
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
	data, err := db.execute("sql", "", map[string]string{"sql": query})
	if err != nil {
		return nil, err
	}
	var out struct {
		Rows [][]interface{} `json:"rows"`
	}
	return out.Rows, json.Unmarshal(data, &out)
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
