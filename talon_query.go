/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package talon

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
	raw, err := db.SQL(sql)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, len(raw))
	for i, rawRow := range raw {
		row := make(Row, len(rawRow))
		for j, cell := range rawRow {
			row[j] = unwrapCell(cell)
		}
		rows[i] = row
	}
	return rows, nil
}
