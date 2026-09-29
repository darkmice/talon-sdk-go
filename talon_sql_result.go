package talon

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SQLResult is one native SQL execution, including empty result columns and
// effects captured before the Core engine lock is released.
type SQLResult struct {
	Columns      []string
	Rows         []Row
	AffectedRows *uint64
	LastInsertID *int64
}

// QueryResult requires the Core v2 result capability and executes exactly one
// statement on this DB's native session. A nil effect denotes non-DML SQL.
func (db *DB) QueryResult(sql string, params ...Value) (SQLResult, error) {
	if strings.TrimSpace(sql) == "" {
		return SQLResult{}, newError(CodeInvalidArgument, "sql result", "SQL is empty", nil)
	}
	if err := db.RequireCapability("native_sql_result"); err != nil {
		return SQLResult{}, err
	}
	data, err := db.execute("sql", "query", map[string]interface{}{
		"sql": sql, "bind": params, "protocol_version": 2,
	})
	if err != nil {
		return SQLResult{}, err
	}
	return decodeSQLResult(data)
}

func decodeSQLResult(data json.RawMessage) (SQLResult, error) {
	var fields map[string]json.RawMessage
	if err := decodeStrictJSON(data, &fields); err != nil {
		return SQLResult{}, newError(CodeProtocolViolation, "sql result", "invalid native SQL result", err)
	}
	for _, key := range []string{"protocol_version", "columns", "rows", "affected_rows", "last_insert_id"} {
		if _, ok := fields[key]; !ok {
			return SQLResult{}, newError(CodeProtocolViolation, "sql result", "native SQL result omitted "+key, nil)
		}
	}
	var wire struct {
		ProtocolVersion uint64              `json:"protocol_version"`
		Columns         []string            `json:"columns"`
		Rows            [][]json.RawMessage `json:"rows"`
		AffectedRows    *uint64             `json:"affected_rows"`
		LastInsertID    *int64              `json:"last_insert_id"`
	}
	if err := decodeStrictJSON(data, &wire); err != nil {
		return SQLResult{}, newError(CodeProtocolViolation, "sql result", "invalid native SQL result", err)
	}
	if wire.ProtocolVersion != 2 || wire.Columns == nil || wire.Rows == nil {
		return SQLResult{}, newError(CodeProtocolViolation, "sql result", "native SQL result omitted required v2 fields", nil)
	}
	result := SQLResult{Columns: wire.Columns, Rows: make([]Row, len(wire.Rows)), AffectedRows: wire.AffectedRows, LastInsertID: wire.LastInsertID}
	for i, row := range wire.Rows {
		if len(row) != len(wire.Columns) {
			return SQLResult{}, newError(CodeProtocolViolation, "sql result", fmt.Sprintf("row %d width differs from columns", i), nil)
		}
		result.Rows[i] = make(Row, len(row))
		for j, cell := range row {
			value, err := decodeCell(cell)
			if err != nil {
				return SQLResult{}, newError(CodeProtocolViolation, "sql result", fmt.Sprintf("row %d column %d is invalid", i, j), err)
			}
			result.Rows[i][j] = value
		}
	}
	return result, nil
}
