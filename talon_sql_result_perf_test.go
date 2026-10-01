package talon

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// BenchmarkNativeSQLResultDecode isolates the SDK's cost for a 100-row,
// two-integer-column result like the GoFrame GROUP BY fixture.
func BenchmarkNativeSQLResultDecode(b *testing.B) {
	var payload strings.Builder
	payload.WriteString(`{"protocol_version":2,"columns":["group_id","n"],"rows":[`)
	for group := 0; group < 100; group++ {
		if group != 0 {
			payload.WriteByte(',')
		}
		fmt.Fprintf(&payload, `[{"Integer":%d},{"Integer":100}]`, group)
	}
	payload.WriteString(`],"affected_rows":null,"last_insert_id":null}`)
	raw := json.RawMessage(payload.String())
	b.SetBytes(int64(len(raw)))
	b.Run("sql_result", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := decodeSQLResult(raw); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("single_strict_pass", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			var result json.RawMessage
			if err := decodeStrictJSON(raw, &result); err != nil {
				b.Fatal(err)
			}
		}
	})
	for name, cell := range map[string]json.RawMessage{
		"integer_cell": json.RawMessage(`{"Integer":12345}`),
		"text_cell":    json.RawMessage(`{"Text":"sample"}`),
		"json_cell":    json.RawMessage(`{"Jsonb":{"x":1}}`),
	} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := decodeCellValidated(cell); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
