/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package talon

import (
	"testing"
)

// ── unwrapCell 单元测试 ──

func TestUnwrapCell_Text(t *testing.T) {
	got := unwrapCell(map[string]interface{}{"Text": "hello"})
	if got != "hello" {
		t.Errorf("Text: got %v, want hello", got)
	}
}

func TestUnwrapCell_Integer(t *testing.T) {
	got := unwrapCell(map[string]interface{}{"Integer": float64(42)})
	v, ok := got.(int64)
	if !ok || v != 42 {
		t.Errorf("Integer: got %v (%T), want int64(42)", got, got)
	}
}

func TestUnwrapCell_IntegerNegative(t *testing.T) {
	got := unwrapCell(map[string]interface{}{"Integer": float64(-7)})
	v, ok := got.(int64)
	if !ok || v != -7 {
		t.Errorf("Integer negative: got %v (%T), want int64(-7)", got, got)
	}
}

func TestUnwrapCell_Float(t *testing.T) {
	got := unwrapCell(map[string]interface{}{"Float": float64(3.14)})
	v, ok := got.(float64)
	if !ok || v != 3.14 {
		t.Errorf("Float: got %v, want 3.14", got)
	}
}

func TestUnwrapCell_Boolean_True(t *testing.T) {
	got := unwrapCell(map[string]interface{}{"Boolean": true})
	v, ok := got.(bool)
	if !ok || !v {
		t.Errorf("Boolean true: got %v, want true", got)
	}
}

func TestUnwrapCell_Boolean_False(t *testing.T) {
	got := unwrapCell(map[string]interface{}{"Boolean": false})
	v, ok := got.(bool)
	if !ok || v {
		t.Errorf("Boolean false: got %v, want false", got)
	}
}

func TestUnwrapCell_Null_BareString(t *testing.T) {
	// talon 将 NULL 序列化为裸字符串 "Null"
	got := unwrapCell("Null")
	if got != nil {
		t.Errorf("Null bare string: got %v, want nil", got)
	}
}

func TestUnwrapCell_Null_Map(t *testing.T) {
	// 防御性：map{"Null": nil} 形式也应解包为 nil
	got := unwrapCell(map[string]interface{}{"Null": nil})
	if got != nil {
		t.Errorf("Null map: got %v, want nil", got)
	}
}

func TestUnwrapCell_NonMap_String(t *testing.T) {
	got := unwrapCell("bare string")
	if got != "bare string" {
		t.Errorf("non-map string: got %v, want bare string", got)
	}
}

func TestUnwrapCell_NonMap_Int(t *testing.T) {
	got := unwrapCell(int64(99))
	if got != int64(99) {
		t.Errorf("non-map int64: got %v, want 99", got)
	}
}

func TestUnwrapCell_NonMap_Nil(t *testing.T) {
	got := unwrapCell(nil)
	if got != nil {
		t.Errorf("non-map nil: got %v, want nil", got)
	}
}

func TestUnwrapCell_UnknownTag(t *testing.T) {
	orig := map[string]interface{}{"UnknownTag": "value"}
	got := unwrapCell(orig)
	gotMap, ok := got.(map[string]interface{})
	if !ok {
		t.Errorf("unknown tag: expected map[string]interface{}, got %T", got)
		return
	}
	if gotMap["UnknownTag"] != "value" {
		t.Errorf("unknown tag: expected map preserved, got %v", gotMap)
	}
}

// ── Row 访问器单元测试 ──

func makeRow() Row {
	return Row{
		"hello",       // 0: string
		int64(42),     // 1: int64
		float64(3.14), // 2: float64
		true,          // 3: bool
		nil,           // 4: nil
	}
}

func TestRow_Str(t *testing.T) {
	r := makeRow()
	if got := r.Str(0); got != "hello" {
		t.Errorf("Str(0): got %q, want hello", got)
	}
}

func TestRow_Str_WrongType(t *testing.T) {
	r := makeRow()
	if got := r.Str(1); got != "" {
		t.Errorf("Str(1) wrong type: got %q, want empty", got)
	}
}

func TestRow_Str_OutOfBounds(t *testing.T) {
	r := makeRow()
	if got := r.Str(99); got != "" {
		t.Errorf("Str(99) OOB: got %q, want empty", got)
	}
}

func TestRow_Str_NegativeIndex(t *testing.T) {
	r := makeRow()
	if got := r.Str(-1); got != "" {
		t.Errorf("Str(-1): got %q, want empty", got)
	}
}

func TestRow_Int(t *testing.T) {
	r := makeRow()
	if got := r.Int(1); got != 42 {
		t.Errorf("Int(1): got %d, want 42", got)
	}
}

func TestRow_Int_FromFloat64(t *testing.T) {
	r := Row{float64(100)}
	if got := r.Int(0); got != 100 {
		t.Errorf("Int from float64: got %d, want 100", got)
	}
}

func TestRow_Int_WrongType(t *testing.T) {
	r := makeRow()
	if got := r.Int(0); got != 0 {
		t.Errorf("Int(0) wrong type: got %d, want 0", got)
	}
}

func TestRow_Int_OutOfBounds(t *testing.T) {
	r := makeRow()
	if got := r.Int(99); got != 0 {
		t.Errorf("Int(99) OOB: got %d, want 0", got)
	}
}

func TestRow_Float(t *testing.T) {
	r := makeRow()
	if got := r.Float(2); got != 3.14 {
		t.Errorf("Float(2): got %f, want 3.14", got)
	}
}

func TestRow_Float_FromInt64(t *testing.T) {
	r := makeRow()
	if got := r.Float(1); got != 42.0 {
		t.Errorf("Float from int64: got %f, want 42.0", got)
	}
}

func TestRow_Float_WrongType(t *testing.T) {
	r := makeRow()
	if got := r.Float(0); got != 0 {
		t.Errorf("Float(0) wrong type: got %f, want 0", got)
	}
}

func TestRow_Float_OutOfBounds(t *testing.T) {
	r := makeRow()
	if got := r.Float(99); got != 0 {
		t.Errorf("Float(99) OOB: got %f, want 0", got)
	}
}

func TestRow_Bool(t *testing.T) {
	r := makeRow()
	if got := r.Bool(3); !got {
		t.Errorf("Bool(3): got false, want true")
	}
}

func TestRow_Bool_WrongType(t *testing.T) {
	r := makeRow()
	if got := r.Bool(0); got {
		t.Errorf("Bool(0) wrong type: got true, want false")
	}
}

func TestRow_Bool_OutOfBounds(t *testing.T) {
	r := makeRow()
	if got := r.Bool(99); got {
		t.Errorf("Bool(99) OOB: got true, want false")
	}
}

func TestRow_IsNull_True(t *testing.T) {
	r := makeRow()
	if !r.IsNull(4) {
		t.Errorf("IsNull(4): want true")
	}
}

func TestRow_IsNull_False(t *testing.T) {
	r := makeRow()
	if r.IsNull(0) {
		t.Errorf("IsNull(0): want false")
	}
}

func TestRow_IsNull_OutOfBounds(t *testing.T) {
	r := makeRow()
	if !r.IsNull(99) {
		t.Errorf("IsNull(99) OOB: want true")
	}
}

// ── Query 集成测试 ──

func TestQuery_CreateInsertSelect(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	// 创建表
	_, err = db.SQL("CREATE TABLE test_items (id INTEGER PRIMARY KEY, name TEXT, score REAL, active BOOLEAN)")
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	// 插入几行
	inserts := []string{
		"INSERT INTO test_items (id, name, score, active) VALUES (1, 'alpha', 1.5, true)",
		"INSERT INTO test_items (id, name, score, active) VALUES (2, 'beta', 2.7, false)",
		"INSERT INTO test_items (id, name, score, active) VALUES (3, 'gamma', 0.0, true)",
	}
	for _, ins := range inserts {
		_, err = db.SQL(ins)
		if err != nil {
			t.Fatalf("INSERT: %v", err)
		}
	}

	// 查询
	rows, err := db.Query("SELECT id, name, score, active FROM test_items ORDER BY id")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	// 验证第一行
	r := rows[0]
	if r.Int(0) != 1 {
		t.Errorf("row[0].Int(0): got %d, want 1", r.Int(0))
	}
	if r.Str(1) != "alpha" {
		t.Errorf("row[0].Str(1): got %q, want alpha", r.Str(1))
	}
	if r.Float(2) != 1.5 {
		t.Errorf("row[0].Float(2): got %f, want 1.5", r.Float(2))
	}
	if !r.Bool(3) {
		t.Errorf("row[0].Bool(3): want true")
	}

	// 验证第二行
	r2 := rows[1]
	if r2.Int(0) != 2 {
		t.Errorf("row[1].Int(0): got %d, want 2", r2.Int(0))
	}
	if r2.Str(1) != "beta" {
		t.Errorf("row[1].Str(1): got %q, want beta", r2.Str(1))
	}
	if r2.Bool(3) {
		t.Errorf("row[1].Bool(3): want false")
	}
}

func TestQuery_NullValues(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	_, err = db.SQL("CREATE TABLE nullable_tbl (id INTEGER, val TEXT)")
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}
	_, err = db.SQL("INSERT INTO nullable_tbl VALUES (1, NULL)")
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}

	rows, err := db.Query("SELECT id, val FROM nullable_tbl")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Int(0) != 1 {
		t.Errorf("id: got %d, want 1", r.Int(0))
	}
	if !r.IsNull(1) {
		t.Errorf("val should be NULL, got %v (type %T)", r[1], r[1])
	}
}

func TestQuery_EmptyResult(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	_, err = db.SQL("CREATE TABLE empty_tbl (id INTEGER)")
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	rows, err := db.Query("SELECT id FROM empty_tbl")
	if err != nil {
		t.Fatalf("Query empty: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}
