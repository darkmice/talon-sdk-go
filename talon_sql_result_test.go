package talon

import (
	"encoding/json"
	"math/big"
	"reflect"
	"testing"
)

func TestNativeSQLValueJSONRoundTrip(t *testing.T) {
	text, _ := TextValue("hello")
	float, _ := FloatValue(1.25)
	jsonValue, _ := JSONValue([]byte(`{"x":1}`))
	vector, _ := VectorValue([]float32{1, 2})
	geo, _ := GeoPointValue(10, 20)
	timeValue, _ := TimeValue(123)
	decimal, _ := DecimalValue(big.NewInt(-1230), 2)
	values := []Value{NullValue(), IntegerValue(7), float, text, BlobValue([]byte{0, 255}), BooleanValue(true), jsonValue, vector, TimestampValue(123), geo, DateValue(-1), timeValue, decimal}
	for _, original := range values {
		wire, err := json.Marshal(original)
		if err != nil {
			t.Fatalf("kind %d marshal: %v", original.Kind(), err)
		}
		decoded, err := decodeCell(wire)
		if err != nil {
			t.Fatalf("kind %d decode %s: %v", original.Kind(), wire, err)
		}
		if !reflect.DeepEqual(original.GoValue(), decoded.GoValue()) {
			t.Fatalf("kind %d: original=%v decoded=%v", original.Kind(), original.GoValue(), decoded.GoValue())
		}
	}
}

func TestNativeSQLValueRejectsInvalidDecimalAndBlob(t *testing.T) {
	for _, wire := range []string{`{"Decimal":1.2}`, `{"Decimal":"1e3"}`, `{"Blob":[256]}`, `{"Blob":[-1]}`} {
		if _, err := decodeCell(json.RawMessage(wire)); err == nil {
			t.Fatalf("accepted %s", wire)
		}
	}
}

func TestNativeSQLResultRejectsMissingOrWrongMetadata(t *testing.T) {
	for _, payload := range []string{
		`{"protocol_version":1,"columns":[],"rows":[],"affected_rows":null,"last_insert_id":null}`,
		`{"protocol_version":2,"columns":null,"rows":[],"affected_rows":null,"last_insert_id":null}`,
		`{"protocol_version":2,"columns":["a"],"rows":[[]],"affected_rows":null,"last_insert_id":null}`,
		`{"protocol_version":2,"columns":["a"],"rows":[[{"Unknown":1}]],"affected_rows":null,"last_insert_id":null}`,
	} {
		if _, err := decodeSQLResult(json.RawMessage(payload)); err == nil {
			t.Fatalf("accepted %s", payload)
		}
	}
	result, err := decodeSQLResult(json.RawMessage(`{"protocol_version":2,"columns":["id"],"rows":[],"affected_rows":null,"last_insert_id":null}`))
	if err != nil || len(result.Columns) != 1 || result.Columns[0] != "id" || len(result.Rows) != 0 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func TestNativeSQLResultFailsBeforeDispatchWithoutCapability(t *testing.T) {
	db := &DB{}
	if _, err := db.QueryResult("SELECT 1"); err == nil {
		t.Fatal("QueryResult accepted Core without native_sql_result capability")
	}
}
