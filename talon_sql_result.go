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
	if params == nil {
		params = []Value{}
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
	var wire struct {
		ProtocolVersion uint64              `json:"protocol_version"`
		Columns         []string            `json:"columns"`
		Rows            [][]json.RawMessage `json:"rows"`
		AffectedRows    json.RawMessage     `json:"affected_rows"`
		LastInsertID    json.RawMessage     `json:"last_insert_id"`
	}
	if err := decodeStrictJSON(data, &wire); err != nil {
		return SQLResult{}, newError(CodeProtocolViolation, "sql result", "invalid native SQL result", err)
	}
	if wire.ProtocolVersion != 2 || wire.Columns == nil || wire.Rows == nil || wire.AffectedRows == nil || wire.LastInsertID == nil {
		return SQLResult{}, newError(CodeProtocolViolation, "sql result", "native SQL result omitted required v2 fields", nil)
	}
	result := SQLResult{Columns: wire.Columns, Rows: make([]Row, len(wire.Rows))}
	if err := json.Unmarshal(wire.AffectedRows, &result.AffectedRows); err != nil {
		return SQLResult{}, newError(CodeProtocolViolation, "sql result", "invalid affected_rows", err)
	}
	if err := json.Unmarshal(wire.LastInsertID, &result.LastInsertID); err != nil {
		return SQLResult{}, newError(CodeProtocolViolation, "sql result", "invalid last_insert_id", err)
	}
	for i, row := range wire.Rows {
		if len(row) != len(wire.Columns) {
			return SQLResult{}, newError(CodeProtocolViolation, "sql result", fmt.Sprintf("row %d width differs from columns", i), nil)
		}
		result.Rows[i] = make(Row, len(row))
		for j, cell := range row {
			// The strict pass above already checked every nested JSON key and
			// value, including this tagged cell.
			value, err := decodeCellValidated(cell)
			if err != nil {
				return SQLResult{}, newError(CodeProtocolViolation, "sql result", fmt.Sprintf("row %d column %d is invalid", i, j), err)
			}
			result.Rows[i][j] = value
		}
	}
	return result, nil
}
