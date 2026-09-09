package talon

import (
	"encoding/json"
	"math"
	"testing"
)

func TestEncodeParamValueRejectsUnsupportedAndNonFinite(t *testing.T) {
	if _, err := encodeParamValue(struct{ Value string }{Value: "x"}); err == nil {
		t.Fatal("unsupported struct was accepted")
	}
	if _, err := encodeParamValue(math.NaN()); err == nil {
		t.Fatal("NaN was accepted")
	}
	if _, err := encodeParamValue(json.RawMessage(`{"broken":`)); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
}

func TestRowStrictAccessors(t *testing.T) {
	textValue, err := TextValue("text")
	if err != nil {
		t.Fatal(err)
	}
	row := Row{textValue, IntegerValue(7), BooleanValue(true), NullValue()}
	if value, err := row.GetString(0); err != nil || value != "text" {
		t.Fatalf("GetString = %q, %v", value, err)
	}
	if value, err := row.GetInt64(1); err != nil || value != 7 {
		t.Fatalf("GetInt64 = %d, %v", value, err)
	}
	if value, err := row.GetBool(2); err != nil || !value {
		t.Fatalf("GetBool = %v, %v", value, err)
	}
	if _, err := row.GetInt64(0); ErrorCodeOf(err) != ErrorProtocol {
		t.Fatalf("wrong-kind error code = %q", ErrorCodeOf(err))
	}
	if _, err := row.GetString(9); ErrorCodeOf(err) != ErrorInvalidArgument {
		t.Fatalf("bounds error code = %q", ErrorCodeOf(err))
	}
}
