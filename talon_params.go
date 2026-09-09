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
	"strconv"
)

// NativeSQLBindSupported reports whether this SDK exposes parameterized SQL
// through the typed native ABI.
const NativeSQLBindSupported = true

// ExecParams executes SQL with typed parameters and never interpolates caller
// data into SQL text.
func (db *DB) ExecParams(sql string, params ...interface{}) error {
	values, err := encodeParamValues(params)
	if err != nil {
		return err
	}
	return db.Exec(sql, values...)
}

// QueryParams executes parameterized SQL through the typed native ABI.
func (db *DB) QueryParams(sql string, params ...interface{}) ([]Row, error) {
	values, err := encodeParamValues(params)
	if err != nil {
		return nil, err
	}
	return db.Query(sql, values...)
}

func encodeParamValues(params []interface{}) ([]Value, error) {
	values := make([]Value, len(params))
	for index, param := range params {
		value, err := encodeParamValue(param)
		if err != nil {
			return nil, newError(CodeInvalidArgument, "sql.bind", fmt.Sprintf("parameter %d is invalid", index), err)
		}
		values[index] = value
	}
	return values, nil
}

func encodeParamValue(value interface{}) (Value, error) {
	switch typed := value.(type) {
	case nil:
		return NullValue(), nil
	case string:
		return TextValue(typed)
	case bool:
		return BooleanValue(typed), nil
	case int:
		return IntegerValue(int64(typed)), nil
	case int8:
		return IntegerValue(int64(typed)), nil
	case int16:
		return IntegerValue(int64(typed)), nil
	case int32:
		return IntegerValue(int64(typed)), nil
	case int64:
		return IntegerValue(typed), nil
	case uint:
		if uint64(typed) > math.MaxInt64 {
			return Value{}, fmt.Errorf("uint value exceeds Talon INTEGER range")
		}
		return IntegerValue(int64(typed)), nil
	case uint8:
		return IntegerValue(int64(typed)), nil
	case uint16:
		return IntegerValue(int64(typed)), nil
	case uint32:
		return IntegerValue(int64(typed)), nil
	case uint64:
		if typed > math.MaxInt64 {
			return Value{}, fmt.Errorf("uint64 value exceeds Talon INTEGER range")
		}
		return IntegerValue(int64(typed)), nil
	case float32:
		return FloatValue(float64(typed))
	case float64:
		return FloatValue(typed)
	case []byte:
		return BlobValue(typed), nil
	case json.RawMessage:
		return JSONValue(typed)
	default:
		return Value{}, fmt.Errorf("Go type %T has no stable Talon Value mapping", value)
	}
}

// decodeCell decodes the JSON tagged-value representation used by the
// high-level execute protocol. The binary Query API remains the production
// path; this helper is retained for strict protocol tests and compatibility.
func decodeCell(raw json.RawMessage) (Value, error) {
	var text string
	if bytes.Equal(bytes.TrimSpace(raw), []byte(`"Null"`)) {
		return NullValue(), nil
	}
	var object map[string]json.RawMessage
	if err := decodeStrictJSON(raw, &object); err != nil || len(object) != 1 {
		return Value{}, fmt.Errorf("invalid tagged value")
	}
	for tag, payload := range object {
		switch tag {
		case "Integer":
			var number json.Number
			if err := json.Unmarshal(payload, &number); err != nil {
				return Value{}, fmt.Errorf("invalid integer payload")
			}
			value, err := strconv.ParseInt(number.String(), 10, 64)
			if err != nil {
				return Value{}, fmt.Errorf("invalid integer payload")
			}
			return IntegerValue(value), nil
		case "Float":
			var value float64
			if err := json.Unmarshal(payload, &value); err != nil {
				return Value{}, fmt.Errorf("invalid float payload")
			}
			return FloatValue(value)
		case "Text":
			if err := json.Unmarshal(payload, &text); err != nil {
				return Value{}, fmt.Errorf("invalid text payload")
			}
			return TextValue(text)
		case "Boolean":
			var value bool
			if err := json.Unmarshal(payload, &value); err != nil {
				return Value{}, fmt.Errorf("invalid boolean payload")
			}
			return BooleanValue(value), nil
		default:
			return Value{}, fmt.Errorf("unknown tagged value")
		}
	}
	return Value{}, fmt.Errorf("invalid tagged value")
}
