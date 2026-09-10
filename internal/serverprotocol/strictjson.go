/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package serverprotocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxNativeJSONRequestBytes      = 32 << 20
	maxNativeJSONResultBytes       = 16 << 20
	revisionStreamInternalKeyspace = "__revision_stream_v1__"
)

var shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func decodeStrictJSON(data []byte, destination interface{}) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("JSON is not valid UTF-8")
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if generic, ok := destination.(*interface{}); ok {
		if err := validateJSONNumbers(*generic); err != nil {
			return err
		}
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func validateJSONNumbers(value interface{}) error {
	switch typed := value.(type) {
	case json.Number:
		text := typed.String()
		if strings.ContainsAny(text, ".eE") {
			parsed, err := strconv.ParseFloat(text, 64)
			if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
				return fmt.Errorf("JSON number %q is outside the finite f64 range", text)
			}
			return nil
		}
		if strings.HasPrefix(text, "-") {
			if _, err := strconv.ParseInt(text, 10, 64); err != nil {
				return fmt.Errorf("JSON integer %q is outside the i64 range", text)
			}
			return nil
		}
		if _, err := strconv.ParseUint(text, 10, 64); err != nil {
			return fmt.Errorf("JSON integer %q is outside the u64 range", text)
		}
	case []interface{}:
		for _, item := range typed {
			if err := validateJSONNumbers(item); err != nil {
				return err
			}
		}
	case map[string]interface{}:
		for _, item := range typed {
			if err := validateJSONNumbers(item); err != nil {
				return err
			}
		}
	}
	return nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var visit func(int) error
	visit = func(depth int) error {
		if depth > 128 {
			return fmt.Errorf("JSON nesting exceeds 128 levels")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("object key is not a string")
				}
				if _, duplicate := seen[key]; duplicate {
					return fmt.Errorf("duplicate JSON key %q", key)
				}
				seen[key] = struct{}{}
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return fmt.Errorf("unterminated JSON object")
			}
		case '[':
			for decoder.More() {
				if err := visit(depth + 1); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return fmt.Errorf("unterminated JSON array")
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", delim)
		}
		return nil
	}
	if err := visit(0); err != nil {
		return err
	}
	if token, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON token %v", token)
		}
		return err
	}
	return nil
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

func cloneUint64Pointer(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
