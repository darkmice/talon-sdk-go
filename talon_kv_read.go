package talon

/*
#include "native_loader.h"
*/
import "C"

import (
	"encoding/binary"
	"fmt"
	"math"
	"unsafe"
)

const (
	nativeKVReadGet    = 1
	nativeKVReadMGet   = 2
	nativeKVReadExists = 3
	nativeKVReadType   = 4
	nativeKVReadTTL    = 5
	nativeKVMaxRequest = 1 << 20
	nativeKVMaxReply   = 64 << 20
	nativeKVMaxKeys    = 4096
	nativeKVMaxKey     = 64 << 10
	nativeKVMaxValue   = 16 << 20
)

// KVBytes preserves the distinction between a missing key and an empty value.
// Value may contain arbitrary bytes, including NUL and invalid UTF-8.
type KVBytes struct {
	Value   []byte
	Present bool
}

// KVSetBytes writes raw bytes through the existing native KV ABI. TTL is in
// whole seconds; a nonpositive value means no expiry. This is a string-keyspace
// operation, not Redis SET with its option and millisecond semantics.
func (db *DB) KVSetBytes(key, value []byte, ttlSeconds int64) error {
	if len(key) > nativeKVMaxKey || len(value) > nativeKVMaxValue {
		return newError(CodeInvalidArgument, "native KV SET", "key or value exceeds the native bound", nil)
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.handle == nil {
		return newError(CodeDatabaseClosed, "native KV SET", "database is closed", nil)
	}
	var empty byte
	keyPtr, valuePtr := &empty, &empty
	if len(key) != 0 {
		keyPtr = &key[0]
	}
	if len(value) != 0 {
		valuePtr = &value[0]
	}
	var errorCode [128]C.char
	if C.talon_sdk_kv_set(
		db.handle,
		(*C.uint8_t)(unsafe.Pointer(keyPtr)), C.size_t(len(key)),
		(*C.uint8_t)(unsafe.Pointer(valuePtr)), C.size_t(len(value)),
		C.int64_t(ttlSeconds), &errorCode[0], C.size_t(len(errorCode)),
	) != 0 {
		return nativeFailure("native KV SET", "talon_kv_set failed", C.GoString(&errorCode[0]))
	}
	return nil
}

func encodeNativeKVKeys(keys [][]byte, multiple bool) ([]byte, error) {
	if !multiple && len(keys) != 1 {
		return nil, newError(CodeInvalidArgument, "native KV read", "exactly one key is required", nil)
	}
	if len(keys) > nativeKVMaxKeys {
		return nil, newError(CodeInvalidArgument, "native KV read", "key count exceeds 4096", nil)
	}
	size := 0
	if multiple {
		size = 4
	}
	for _, key := range keys {
		if len(key) > nativeKVMaxKey || size > nativeKVMaxRequest-4-len(key) {
			return nil, newError(CodeInvalidArgument, "native KV read", "key or request exceeds the native bound", nil)
		}
		size += 4 + len(key)
	}
	request := make([]byte, size)
	offset := 0
	if multiple {
		binary.LittleEndian.PutUint32(request, uint32(len(keys)))
		offset = 4
	}
	for _, key := range keys {
		binary.LittleEndian.PutUint32(request[offset:], uint32(len(key)))
		offset += 4
		copy(request[offset:], key)
		offset += len(key)
	}
	return request, nil
}

func (db *DB) nativeKVRead(operation uint32, request []byte) ([]byte, error) {
	if err := db.RequireCapability("native_kv_read"); err != nil {
		return nil, err
	}
	db.mu.RLock()
	defer db.mu.RUnlock()
	if db.handle == nil {
		return nil, newError(CodeDatabaseClosed, "native KV read", "database is closed", nil)
	}
	if len(request) == 0 || len(request) > nativeKVMaxRequest {
		return nil, newError(CodeInvalidArgument, "native KV read", "request exceeds the native bound", nil)
	}
	var output *C.uint8_t
	var outputLen C.size_t
	var errorCode [128]C.char
	result := C.talon_sdk_kv_read_v1(
		db.handle,
		C.uint32_t(operation),
		(*C.uint8_t)(unsafe.Pointer(&request[0])),
		C.size_t(len(request)),
		&output,
		&outputLen,
		&errorCode[0],
		C.size_t(len(errorCode)),
	)
	if output != nil {
		defer C.talon_sdk_free_bytes(output, outputLen)
	}
	if result != 0 {
		return nil, nativeFailure("native KV read", "talon_kv_read_v1 failed", C.GoString(&errorCode[0]))
	}
	if output == nil || uint64(outputLen) > nativeKVMaxReply || uint64(outputLen) > math.MaxInt32 {
		return nil, newError(CodeProtocolViolation, "native KV read", "native reply is null or exceeds the SDK bound", nil)
	}
	return C.GoBytes(unsafe.Pointer(output), C.int(outputLen)), nil
}

func parseNativeKVValue(data []byte, offset *int) (KVBytes, error) {
	if *offset >= len(data) {
		return KVBytes{}, fmt.Errorf("truncated KV value tag")
	}
	tag := data[*offset]
	*offset++
	if tag == 0 {
		return KVBytes{}, nil
	}
	if tag != 1 || len(data)-*offset < 4 {
		return KVBytes{}, fmt.Errorf("invalid or truncated KV value header")
	}
	size := uint64(binary.LittleEndian.Uint32(data[*offset:]))
	*offset += 4
	if size > uint64(len(data)-*offset) {
		return KVBytes{}, fmt.Errorf("truncated KV value")
	}
	value := append([]byte{}, data[*offset:*offset+int(size)]...)
	*offset += int(size)
	return KVBytes{Value: value, Present: true}, nil
}

func parseNativeKVGet(data []byte) (KVBytes, error) {
	offset := 0
	value, err := parseNativeKVValue(data, &offset)
	if err != nil || offset != len(data) {
		return KVBytes{}, newError(CodeProtocolViolation, "native KV GET", "invalid native reply", err)
	}
	return value, nil
}

func parseNativeKVMGet(data []byte, want int) ([]KVBytes, error) {
	if len(data) < 4 || binary.LittleEndian.Uint32(data) != uint32(want) {
		return nil, newError(CodeProtocolViolation, "native KV MGET", "native reply count differs from request", nil)
	}
	offset := 4
	values := make([]KVBytes, want)
	for i := range values {
		value, err := parseNativeKVValue(data, &offset)
		if err != nil {
			return nil, newError(CodeProtocolViolation, "native KV MGET", "invalid native value", err)
		}
		values[i] = value
	}
	if offset != len(data) {
		return nil, newError(CodeProtocolViolation, "native KV MGET", "native reply has trailing bytes", nil)
	}
	return values, nil
}

// KVGetBytes reads one string-keyspace value through Core's binary native ABI.
func (db *DB) KVGetBytes(key []byte) (KVBytes, error) {
	request, err := encodeNativeKVKeys([][]byte{key}, false)
	if err != nil {
		return KVBytes{}, err
	}
	data, err := db.nativeKVRead(nativeKVReadGet, request)
	if err != nil {
		return KVBytes{}, err
	}
	return parseNativeKVGet(data)
}

// KVMGetBytes preserves request order, missing keys, and arbitrary value bytes.
func (db *DB) KVMGetBytes(keys [][]byte) ([]KVBytes, error) {
	request, err := encodeNativeKVKeys(keys, true)
	if err != nil {
		return nil, err
	}
	data, err := db.nativeKVRead(nativeKVReadMGet, request)
	if err != nil {
		return nil, err
	}
	return parseNativeKVMGet(data, len(keys))
}

// KVExistsBytes counts every supplied key, including duplicates.
func (db *DB) KVExistsBytes(keys [][]byte) (int64, error) {
	request, err := encodeNativeKVKeys(keys, true)
	if err != nil {
		return 0, err
	}
	data, err := db.nativeKVRead(nativeKVReadExists, request)
	if err != nil {
		return 0, err
	}
	if len(data) != 8 {
		return 0, newError(CodeProtocolViolation, "native KV EXISTS", "invalid native reply length", nil)
	}
	count := int64(binary.LittleEndian.Uint64(data))
	if count < 0 || count > int64(len(keys)) {
		return 0, newError(CodeProtocolViolation, "native KV EXISTS", "invalid native key count", nil)
	}
	return count, nil
}

// KVTypeBytes reports "none" or "string" for the native string keyspace.
func (db *DB) KVTypeBytes(key []byte) (string, error) {
	request, err := encodeNativeKVKeys([][]byte{key}, false)
	if err != nil {
		return "", err
	}
	data, err := db.nativeKVRead(nativeKVReadType, request)
	if err != nil {
		return "", err
	}
	if len(data) != 1 {
		return "", newError(CodeProtocolViolation, "native KV TYPE", "invalid native reply length", nil)
	}
	switch data[0] {
	case 0:
		return "none", nil
	case 1:
		return "string", nil
	default:
		return "", newError(CodeProtocolViolation, "native KV TYPE", "unknown native key type", nil)
	}
}

// KVTTLBytes returns remaining whole seconds, -1 without expiry, or -2 if absent.
func (db *DB) KVTTLBytes(key []byte) (int64, error) {
	request, err := encodeNativeKVKeys([][]byte{key}, false)
	if err != nil {
		return 0, err
	}
	data, err := db.nativeKVRead(nativeKVReadTTL, request)
	if err != nil {
		return 0, err
	}
	if len(data) != 8 {
		return 0, newError(CodeProtocolViolation, "native KV TTL", "invalid native reply length", nil)
	}
	ttl := int64(binary.LittleEndian.Uint64(data))
	if ttl < -2 {
		return 0, newError(CodeProtocolViolation, "native KV TTL", "invalid native TTL", nil)
	}
	return ttl, nil
}
