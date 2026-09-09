/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package talon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
)

// JSONValue explicitly binds a Go JSON value as Talon JSONB. The bytes must
// contain exactly one valid JSON value.
type JSONValue json.RawMessage

type taggedBindValue map[string]interface{}

// NativeSQLBindSupported reports whether this module's bundled native
// artifacts consume talon_execute params.bind. The bundled darwin-arm64 and
// linux-amd64 artifacts are built from the pinned Talon Core revision recorded
// in native-manifest.json and pass the parameter-binding conformance tests.
const NativeSQLBindSupported = true

// ExecParams executes a parameterized SQL statement through talon_execute's
// params.bind path. Placeholders are positional question marks. Supported Go
// values are nil, strings, booleans, signed integers, uint values that fit in
// int64, finite floats, []byte (BLOB), json.RawMessage and JSONValue (JSONB).
func (db *DB) ExecParams(sql string, params ...interface{}) error {
	if len(params) > 0 && !NativeSQLBindSupported {
		return operationError(ErrorUnsupported, "sql.bind", "随附 Talon v0.1.0 native library 不消费 talon_execute params.bind", nil)
	}
	_, err := db.sqlParams(sql, params)
	return err
}

// QueryParams executes parameterized SQL and strictly decodes Talon's tagged
// Value rows. Unknown tags, malformed numeric values and unexpected response
// fields are protocol errors rather than silently coerced values.
func (db *DB) QueryParams(sql string, params ...interface{}) ([]Row, error) {
	if len(params) > 0 && !NativeSQLBindSupported {
		return nil, operationError(ErrorUnsupported, "sql.bind", "随附 Talon v0.1.0 native library 不消费 talon_execute params.bind", nil)
	}
	result, err := db.sqlParams(sql, params)
	if err != nil {
		return nil, err
	}
	return decodeRows(result.Rows)
}

type sqlWireResult struct {
	Rows    []json.RawMessage `json:"rows"`
	Columns []string          `json:"columns,omitempty"`
}

func (db *DB) sqlParams(sql string, params []interface{}) (*sqlWireResult, error) {
	bind, err := encodeBindValues(params)
	if err != nil {
		return nil, err
	}
	wireParams := map[string]interface{}{"sql": sql, "bind": bind}
	data, err := db.execute("sql", "", wireParams)
	if err != nil {
		return nil, err
	}
	var result sqlWireResult
	if err := decodeStrictJSON(data, &result); err != nil {
		return nil, operationError(ErrorProtocol, "sql", "SQL 响应 data 结构无效", err)
	}
	if result.Rows == nil {
		return nil, operationError(ErrorProtocol, "sql", "SQL 响应缺少 rows 字段", nil)
	}
	return &result, nil
}

func encodeBindValues(params []interface{}) ([]interface{}, error) {
	values := make([]interface{}, len(params))
	for i, param := range params {
		value, err := encodeBindValue(param)
		if err != nil {
			return nil, operationError(ErrorInvalidArgument, "sql.bind", fmt.Sprintf("参数 %d 不受支持", i), err)
		}
		values[i] = value
	}
	return values, nil
}

func encodeBindValue(value interface{}) (interface{}, error) {
	if value == nil {
		return "Null", nil
	}
	switch v := value.(type) {
	case string:
		return taggedBindValue{"Text": v}, nil
	case bool:
		return taggedBindValue{"Boolean": v}, nil
	case int:
		return taggedBindValue{"Integer": int64(v)}, nil
	case int8:
		return taggedBindValue{"Integer": int64(v)}, nil
	case int16:
		return taggedBindValue{"Integer": int64(v)}, nil
	case int32:
		return taggedBindValue{"Integer": int64(v)}, nil
	case int64:
		return taggedBindValue{"Integer": v}, nil
	case uint:
		if uint64(v) > math.MaxInt64 {
			return nil, fmt.Errorf("uint 值超出 Talon INTEGER 范围")
		}
		return taggedBindValue{"Integer": int64(v)}, nil
	case uint8:
		return taggedBindValue{"Integer": int64(v)}, nil
	case uint16:
		return taggedBindValue{"Integer": int64(v)}, nil
	case uint32:
		return taggedBindValue{"Integer": int64(v)}, nil
	case uint64:
		if v > math.MaxInt64 {
			return nil, fmt.Errorf("uint64 值超出 Talon INTEGER 范围")
		}
		return taggedBindValue{"Integer": int64(v)}, nil
	case float32:
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nil, fmt.Errorf("Talon FLOAT 不接受 NaN 或 Infinity")
		}
		return taggedBindValue{"Float": float64(v)}, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, fmt.Errorf("Talon FLOAT 不接受 NaN 或 Infinity")
		}
		return taggedBindValue{"Float": v}, nil
	case []byte:
		return taggedBindValue{"Blob": v}, nil
	case json.RawMessage:
		return encodeJSONBind(v)
	case JSONValue:
		return encodeJSONBind(json.RawMessage(v))
	default:
		return nil, fmt.Errorf("Go 类型 %s 没有稳定的 Talon Value 映射", reflect.TypeOf(value))
	}
}

func encodeJSONBind(raw json.RawMessage) (interface{}, error) {
	var value interface{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("无效 JSONB: %w", err)
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("JSONB 包含尾随内容")
	}
	return taggedBindValue{"Jsonb": value}, nil
}

func decodeStrictJSON(raw []byte, destination interface{}) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("尾随 JSON")
	}
	return nil
}
