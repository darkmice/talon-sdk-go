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
	// decodeStrictJSON already rejected invalid UTF-8. Validate syntax once,
	// then scan only object keys. Decoder.Token boxes every scalar in large
	// result sets and allocates for every nested cell.
	if !json.Valid(data) {
		return fmt.Errorf("invalid JSON")
	}
	scanner := duplicateKeyScanner{data: data}
	if err := scanner.visit(0); err != nil {
		return err
	}
	scanner.skipSpace()
	if scanner.offset != len(data) {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}

type duplicateKeyScanner struct {
	data   []byte
	offset int
}

func (scanner *duplicateKeyScanner) skipSpace() {
	for scanner.offset < len(scanner.data) {
		switch scanner.data[scanner.offset] {
		case ' ', '\n', '\r', '\t':
			scanner.offset++
		default:
			return
		}
	}
}

// skipString assumes json.Valid has already checked escapes and termination.
func (scanner *duplicateKeyScanner) skipString() (start, end int, escaped bool) {
	start = scanner.offset
	scanner.offset++ // opening quote
	for scanner.offset < len(scanner.data) {
		ch := scanner.data[scanner.offset]
		scanner.offset++
		if ch == '\\' {
			escaped = true
			scanner.offset++ // escaped byte; json.Valid checked the sequence
		} else if ch == '"' {
			return start, scanner.offset, escaped
		}
	}
	return start, scanner.offset, escaped
}

func (scanner *duplicateKeyScanner) visit(depth int) error {
	if depth > 128 {
		return fmt.Errorf("JSON nesting exceeds 128 levels")
	}
	scanner.skipSpace()
	switch scanner.data[scanner.offset] {
	case '{':
		scanner.offset++
		scanner.skipSpace()
		if scanner.data[scanner.offset] == '}' {
			scanner.offset++
			return nil
		}
		seen := make(map[string]struct{})
		for {
			start, end, escaped := scanner.skipString()
			var key string
			if escaped {
				// JSON escapes (including surrogate pairs) must be
				// normalized before comparing keys.
				if err := json.Unmarshal(scanner.data[start:end], &key); err != nil {
					return err
				}
			} else {
				key = string(scanner.data[start+1 : end-1])
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON key %q", key)
			}
			seen[key] = struct{}{}
			scanner.skipSpace()
			scanner.offset++ // colon
			if err := scanner.visit(depth + 1); err != nil {
				return err
			}
			scanner.skipSpace()
			if scanner.data[scanner.offset] == '}' {
				scanner.offset++
				return nil
			}
			scanner.offset++ // comma
			scanner.skipSpace()
		}
	case '[':
		scanner.offset++
		scanner.skipSpace()
		if scanner.data[scanner.offset] == ']' {
			scanner.offset++
			return nil
		}
		for {
			if err := scanner.visit(depth + 1); err != nil {
				return err
			}
			scanner.skipSpace()
			if scanner.data[scanner.offset] == ']' {
				scanner.offset++
				return nil
			}
			scanner.offset++ // comma
		}
	case '"':
		scanner.skipString()
	default:
		// The syntax pass has established the primitive boundary.
		for scanner.offset < len(scanner.data) {
			switch scanner.data[scanner.offset] {
			case ' ', '\n', '\r', '\t', ',', '}', ']':
				return nil
			default:
				scanner.offset++
			}
		}
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
