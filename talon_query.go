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
	"math"
)

// unwrapCell 将 talon SQL 引擎返回的类型标签值解包为 Go 原生值。
//
// talon 序列化的单元格格式：
//
//	{"Text": "hello"}        → string
//	{"Integer": 1234.0}      → int64  (JSON 数字解析为 float64，需转换)
//	{"Float": 3.14}          → float64
//	{"Boolean": true}        → bool
//	"Null"                   → nil   (裸字符串，不是 map)
//
// 若输入本就不是 map[string]interface{}（防御性），原样返回，
// 但裸字符串 "Null" 会被识别并转换为 nil。
func unwrapCell(v interface{}) interface{} {
	// talon 将 NULL 序列化为裸字符串 "Null"
	if s, ok := v.(string); ok && s == "Null" {
		return nil
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	if val, exists := m["Text"]; exists {
		return val
	}
	if val, exists := m["Integer"]; exists {
		// JSON 解析后数字为 float64，转换为 int64
		switch n := val.(type) {
		case float64:
			return int64(n)
		case int64:
			return n
		case int:
			return int64(n)
		}
		return val
	}
	if val, exists := m["Float"]; exists {
		return val
	}
	if val, exists := m["Boolean"]; exists {
		return val
	}
	if _, exists := m["Null"]; exists {
		return nil
	}
	// 未知标签，原样返回
	return v
}

// Row 是一行查询结果，单元格已从 talon 的类型标签 map 解包为 Go 原生值。
type Row []interface{}

// Value returns a cell with bounds checking. Query and QueryParams guarantee
// that every returned value has already passed strict Talon Value decoding.
func (r Row) Value(i int) (interface{}, error) {
	if i < 0 || i >= len(r) {
		return nil, operationError(ErrorInvalidArgument, "row.value", fmt.Sprintf("列索引 %d 越界", i), nil)
	}
	return r[i], nil
}

// GetString is the strict counterpart to the backward-compatible Str method.
func (r Row) GetString(i int) (string, error) {
	v, err := r.Value(i)
	if err != nil {
		return "", err
	}
	s, ok := v.(string)
	if !ok {
		return "", operationError(ErrorProtocol, "row.string", fmt.Sprintf("列 %d 是 %T，不是 string", i, v), nil)
	}
	return s, nil
}

// GetInt64 is the strict counterpart to the backward-compatible Int method.
func (r Row) GetInt64(i int) (int64, error) {
	v, err := r.Value(i)
	if err != nil {
		return 0, err
	}
	n, ok := v.(int64)
	if !ok {
		return 0, operationError(ErrorProtocol, "row.int64", fmt.Sprintf("列 %d 是 %T，不是 int64", i, v), nil)
	}
	return n, nil
}

// GetBool is the strict counterpart to the backward-compatible Bool method.
func (r Row) GetBool(i int) (bool, error) {
	v, err := r.Value(i)
	if err != nil {
		return false, err
	}
	b, ok := v.(bool)
	if !ok {
		return false, operationError(ErrorProtocol, "row.bool", fmt.Sprintf("列 %d 是 %T，不是 bool", i, v), nil)
	}
	return b, nil
}

// Str 返回第 i 列的字符串值。非字符串或越界返回 ""。
func (r Row) Str(i int) string {
	if i < 0 || i >= len(r) {
		return ""
	}
	s, ok := r[i].(string)
	if !ok {
		return ""
	}
	return s
}

// Int 返回第 i 列的整数值。非数字或越界返回 0。
func (r Row) Int(i int) int64 {
	if i < 0 || i >= len(r) {
		return 0
	}
	switch v := r[i].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int32:
		return int64(v)
	}
	return 0
}

// Float 返回第 i 列的浮点值。非数字或越界返回 0。
func (r Row) Float(i int) float64 {
	if i < 0 || i >= len(r) {
		return 0
	}
	switch v := r[i].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	case int:
		return float64(v)
	}
	return 0
}

// Bool 返回第 i 列的布尔值。非布尔或越界返回 false。
func (r Row) Bool(i int) bool {
	if i < 0 || i >= len(r) {
		return false
	}
	b, ok := r[i].(bool)
	if !ok {
		return false
	}
	return b
}

// IsNull 报告第 i 列是否为 NULL（或越界）。
func (r Row) IsNull(i int) bool {
	if i < 0 || i >= len(r) {
		return true
	}
	return r[i] == nil
}

// Query 执行 SQL 并返回解包后的行集。
// 每个单元格已从 talon 类型标签 map 解包为 Go 原生值（string/int64/float64/bool/nil）。
func (db *DB) Query(sql string) ([]Row, error) {
	return db.QueryParams(sql)
}

// Timestamp, Date, Time, Vector and GeoPoint preserve Talon's non-primitive
// Value tags without collapsing them into ambiguous Go numbers or slices.
type Timestamp int64
type Date int32
type Time int64
type Vector []float32
type GeoPoint struct{ Latitude, Longitude float64 }

func decodeRows(rawRows []json.RawMessage) ([]Row, error) {
	rows := make([]Row, len(rawRows))
	for rowIndex, rawRow := range rawRows {
		var rawCells []json.RawMessage
		if err := decodeStrictJSON(rawRow, &rawCells); err != nil {
			return nil, operationError(ErrorProtocol, "sql.rows", fmt.Sprintf("第 %d 行不是合法数组", rowIndex), err)
		}
		row := make(Row, len(rawCells))
		for columnIndex, rawCell := range rawCells {
			cell, err := decodeCell(rawCell)
			if err != nil {
				return nil, operationError(ErrorProtocol, "sql.rows", fmt.Sprintf("第 %d 行第 %d 列无效", rowIndex, columnIndex), err)
			}
			row[columnIndex] = cell
		}
		rows[rowIndex] = row
	}
	return rows, nil
}

func decodeCell(raw json.RawMessage) (interface{}, error) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte(`"Null"`)) {
		return nil, nil
	}
	var tagged map[string]json.RawMessage
	if err := decodeStrictJSON(trimmed, &tagged); err != nil {
		return nil, fmt.Errorf("Talon Value 必须是单标签对象或 \"Null\": %w", err)
	}
	if len(tagged) != 1 {
		return nil, fmt.Errorf("Talon Value 必须恰有一个类型标签")
	}
	for tag, payload := range tagged {
		switch tag {
		case "Text":
			var value string
			if err := decodeStrictJSON(payload, &value); err != nil {
				return nil, err
			}
			return value, nil
		case "Integer":
			var value int64
			if err := decodeStrictJSON(payload, &value); err != nil {
				return nil, err
			}
			return value, nil
		case "Float":
			var value float64
			if err := decodeStrictJSON(payload, &value); err != nil {
				return nil, err
			}
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, fmt.Errorf("Float 非有限值")
			}
			return value, nil
		case "Boolean":
			var value bool
			if err := decodeStrictJSON(payload, &value); err != nil {
				return nil, err
			}
			return value, nil
		case "Blob":
			var value []byte
			if err := decodeStrictJSON(payload, &value); err != nil {
				return nil, err
			}
			return value, nil
		case "Jsonb":
			var value interface{}
			if err := decodeStrictJSON(payload, &value); err != nil {
				return nil, err
			}
			return json.RawMessage(append([]byte(nil), payload...)), nil
		case "Vector":
			var value []float32
			if err := decodeStrictJSON(payload, &value); err != nil {
				return nil, err
			}
			for _, n := range value {
				if math.IsNaN(float64(n)) || math.IsInf(float64(n), 0) {
					return nil, fmt.Errorf("Vector 含非有限值")
				}
			}
			return Vector(value), nil
		case "Timestamp":
			var value int64
			if err := decodeStrictJSON(payload, &value); err != nil {
				return nil, err
			}
			return Timestamp(value), nil
		case "GeoPoint":
			var value [2]float64
			if err := decodeStrictJSON(payload, &value); err != nil {
				return nil, err
			}
			if math.IsNaN(value[0]) || math.IsInf(value[0], 0) || math.IsNaN(value[1]) || math.IsInf(value[1], 0) {
				return nil, fmt.Errorf("GeoPoint 含非有限值")
			}
			return GeoPoint{Latitude: value[0], Longitude: value[1]}, nil
		case "Date":
			var value int32
			if err := decodeStrictJSON(payload, &value); err != nil {
				return nil, err
			}
			return Date(value), nil
		case "Time":
			var value int64
			if err := decodeStrictJSON(payload, &value); err != nil {
				return nil, err
			}
			return Time(value), nil
		default:
			return nil, fmt.Errorf("未知 Talon Value 标签 %q", tag)
		}
	}
	panic("unreachable")
}
