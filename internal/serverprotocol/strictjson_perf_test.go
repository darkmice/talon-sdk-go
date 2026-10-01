package serverprotocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// BenchmarkStrictJSONGroupResult isolates validation of the GROUP BY result
// shape used by the SDK's native SQL decoder benchmark.
func BenchmarkStrictJSONGroupResult(b *testing.B) {
	var payload strings.Builder
	payload.WriteString(`{"protocol_version":2,"columns":["group_id","n"],"rows":[`)
	for group := 0; group < 100; group++ {
		if group != 0 {
			payload.WriteByte(',')
		}
		fmt.Fprintf(&payload, `[{"Integer":%d},{"Integer":100}]`, group)
	}
	payload.WriteString(`],"affected_rows":null,"last_insert_id":null}`)
	raw := []byte(payload.String())
	b.Run("duplicate_check", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if err := rejectDuplicateJSONKeys(raw); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("json_unmarshal", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var value json.RawMessage
			if err := json.Unmarshal(raw, &value); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkDuplicateKeyScannerSmall(b *testing.B) {
	raw := []byte(`{"ok":true,"data":{"value":123}}`)
	for name, check := range map[string]func([]byte) error{
		"new":    rejectDuplicateJSONKeys,
		"legacy": legacyDuplicateKeyCheck,
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if err := check(raw); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
