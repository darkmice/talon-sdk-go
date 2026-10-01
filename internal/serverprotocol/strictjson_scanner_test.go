package serverprotocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"
)

func TestDuplicateKeyScannerPreservesJSONSemantics(t *testing.T) {
	cases := []string{
		`{"a":1,"a":2}`,
		`{"a":1,"\u0061":2}`,
		`{"\ud800":1,"�":2}`,
		`{"\ud83d\ude00":1,"😀":2}`,
		`{"nested":{"x":1,"x":2}}`,
		`[{"a":1},{"a":2}]`,
		`{"a":"escaped \\" , } []","b":[1,true,null,{"c":3}]}`,
		`{"a\\b":1,"a\\b":2}`,
		` { "a" : [ { "b" : 1 } ], "c" : 2 } `,
		`null`,
		`[1,2,3]`,
		`{`,
		`{"a":1} true`,
		`{"a":01}`,
		strings.Repeat("[", 129) + `0` + strings.Repeat("]", 129),
	}
	for _, input := range cases {
		assertDuplicateScannerMatchesLegacy(t, input)
	}
	random := rand.New(rand.NewSource(42))
	for i := 0; i < 3000; i++ {
		assertDuplicateScannerMatchesLegacy(t, randomJSONForDuplicateTest(random, 0))
	}
}

func assertDuplicateScannerMatchesLegacy(t *testing.T, input string) {
	t.Helper()
	got := rejectDuplicateJSONKeys([]byte(input))
	legacy := legacyDuplicateKeyCheck([]byte(input))
	if (got == nil) != (legacy == nil) {
		t.Fatalf("duplicate scan differs for %q: new=%v legacy=%v", input, got, legacy)
	}
	if (got != nil && strings.Contains(got.Error(), "duplicate JSON key")) !=
		(legacy != nil && strings.Contains(legacy.Error(), "duplicate JSON key")) {
		t.Fatalf("duplicate classification differs for %q: new=%v legacy=%v", input, got, legacy)
	}
}

func randomJSONForDuplicateTest(random *rand.Rand, depth int) string {
	if depth == 4 {
		return []string{`0`, `-12.5e2`, `true`, `null`, `"text"`}[random.Intn(5)]
	}
	switch random.Intn(5) {
	case 0, 1:
		keys := []string{`"a"`, `"b"`, `"\u0061"`, `"x\\y"`, `"�"`, `"\ud800"`}
		count := random.Intn(5)
		parts := make([]string, count)
		for i := range parts {
			parts[i] = keys[random.Intn(len(keys))] + `:` + randomJSONForDuplicateTest(random, depth+1)
		}
		return `{` + strings.Join(parts, `,`) + `}`
	case 2:
		count := random.Intn(5)
		parts := make([]string, count)
		for i := range parts {
			parts[i] = randomJSONForDuplicateTest(random, depth+1)
		}
		return `[` + strings.Join(parts, `,`) + `]`
	default:
		return []string{`0`, `-12.5e2`, `true`, `null`, `"text"`}[random.Intn(5)]
	}
}

// This is the previous token-based duplicate check, kept as a differential
// oracle for the allocation-optimized scanner.
func legacyDuplicateKeyCheck(data []byte) error {
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
