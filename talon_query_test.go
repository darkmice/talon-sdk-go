package talon

import (
	"encoding/binary"
	"math"
	"math/big"
	"testing"
)

func mustText(t *testing.T, value string) Value {
	t.Helper()
	result, err := TextValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func mustFloat(t *testing.T, value float64) Value {
	t.Helper()
	result, err := FloatValue(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestStrictWireRoundTripAllValueKinds(t *testing.T) {
	jsonValue, err := JSONValue([]byte(`{"answer":42}`))
	if err != nil {
		t.Fatal(err)
	}
	vector, err := VectorValue([]float32{1.25, -2.5})
	if err != nil {
		t.Fatal(err)
	}
	geo, err := GeoPointValue(35.6762, 139.6503)
	if err != nil {
		t.Fatal(err)
	}
	timeValue, err := TimeValue(1234)
	if err != nil {
		t.Fatal(err)
	}
	decimal, err := DecimalValue(big.NewInt(-12300), 4)
	if err != nil {
		t.Fatal(err)
	}
	values := []Value{NullValue(), IntegerValue(-7), mustFloat(t, 3.5), mustText(t, "hello"), BlobValue([]byte{0, 1, 2}), BooleanValue(true), jsonValue, vector, TimestampValue(1700000000), geo, DateValue(-1), timeValue, decimal}
	wire := make([]byte, 8)
	binary.LittleEndian.PutUint32(wire[:4], 1)
	binary.LittleEndian.PutUint32(wire[4:8], uint32(len(values)))
	for _, value := range values {
		wire, err = appendWireValue(wire, value)
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := decodeRows(wire)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || len(rows[0]) != len(values) {
		t.Fatalf("decoded shape = %d x %d", len(rows), len(rows[0]))
	}
	for index := range values {
		if rows[0][index].Kind() != values[index].Kind() {
			t.Fatalf("value %d kind = %d, want %d", index, rows[0][index].Kind(), values[index].Kind())
		}
	}
}

func TestDecimalValueExactRoundTripAndBounds(t *testing.T) {
	max := new(big.Int).Sub(new(big.Int).Exp(big.NewInt(10), big.NewInt(38), nil), big.NewInt(1))
	for _, coefficient := range []*big.Int{big.NewInt(0), big.NewInt(-1), max, new(big.Int).Neg(max)} {
		value, err := DecimalValue(coefficient, 38)
		if err != nil {
			t.Fatalf("DecimalValue(%s): %v", coefficient, err)
		}
		wire, err := appendWireValue(nil, value)
		if err != nil {
			t.Fatal(err)
		}
		decoded, position, err := decodeWireValue(wire, 0)
		if err != nil || position != len(wire) {
			t.Fatalf("decode decimal: position=%d error=%v", position, err)
		}
		actual, scale, ok := decoded.Decimal()
		if !ok || actual.Cmp(coefficient) != 0 || scale != 38 {
			t.Fatalf("decimal roundtrip = (%v,%d,%v), want (%v,38,true)", actual, scale, ok, coefficient)
		}
		actual.SetInt64(7)
		again, _, _ := decoded.Decimal()
		if again.Cmp(coefficient) != 0 {
			t.Fatal("Decimal getter leaked mutable coefficient state")
		}
	}
	tooPrecise := new(big.Int).Exp(big.NewInt(10), big.NewInt(38), nil)
	if _, err := DecimalValue(tooPrecise, 0); err == nil {
		t.Fatal("39-digit decimal coefficient was accepted")
	}
	if _, err := DecimalValue(big.NewInt(1), 39); err == nil {
		t.Fatal("decimal scale above 38 was accepted")
	}
	if _, err := DecimalValue(nil, 0); err == nil {
		t.Fatal("nil decimal coefficient was accepted")
	}
	minusOne, _ := DecimalValue(big.NewInt(-1), 0)
	wire, _ := appendWireValue(nil, minusOne)
	for index, value := range wire[1:17] {
		if value != 0xff {
			t.Fatalf("negative i128 byte %d = %x, want ff", index, value)
		}
	}
	if _, _, err := decodeWireValue([]byte{byte(KindDecimal), 1}, 0); err == nil {
		t.Fatal("truncated decimal was accepted")
	}
	invalidScale := append([]byte{byte(KindDecimal)}, make([]byte, 17)...)
	invalidScale[len(invalidScale)-1] = 39
	if _, _, err := decodeWireValue(invalidScale, 0); err == nil {
		t.Fatal("decimal with invalid scale was accepted")
	}
}

func TestStrictWireDecoderRejectsMalformedValues(t *testing.T) {
	tests := []struct {
		name string
		wire []byte
	}{
		{name: "unknown tag", wire: []byte{1, 0, 0, 0, 1, 0, 0, 0, 99}},
		{name: "truncated integer", wire: []byte{1, 0, 0, 0, 1, 0, 0, 0, byte(KindInteger), 1}},
		{name: "non canonical bool", wire: []byte{1, 0, 0, 0, 1, 0, 0, 0, byte(KindBoolean), 2}},
		{name: "trailing bytes", wire: []byte{1, 0, 0, 0, 1, 0, 0, 0, byte(KindNull), 0}},
		{name: "inconsistent empty shape", wire: []byte{0, 0, 0, 0, 1, 0, 0, 0}},
		{name: "impossible allocation shape", wire: []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := decodeRows(test.wire); err == nil {
				t.Fatal("malformed wire was accepted")
			}
		})
	}

	nonFinite := make([]byte, 17)
	binary.LittleEndian.PutUint32(nonFinite[:4], 1)
	binary.LittleEndian.PutUint32(nonFinite[4:8], 1)
	nonFinite[8] = byte(KindFloat)
	binary.LittleEndian.PutUint64(nonFinite[9:], math.Float64bits(math.NaN()))
	if _, err := decodeRows(nonFinite); err == nil {
		t.Fatal("NaN wire value was accepted")
	}
	oversizedCell := make([]byte, 13)
	binary.LittleEndian.PutUint32(oversizedCell[:4], 1)
	binary.LittleEndian.PutUint32(oversizedCell[4:8], 1)
	oversizedCell[8] = byte(KindBlob)
	binary.LittleEndian.PutUint32(oversizedCell[9:], maxWireCellBytes+1)
	if _, err := decodeRows(oversizedCell); err == nil {
		t.Fatal("oversized cell declaration was accepted")
	}
}

func TestValueConstructorsRejectAmbiguousInputs(t *testing.T) {
	if _, err := FloatValue(math.Inf(1)); err == nil {
		t.Fatal("infinite float was accepted")
	}
	if _, err := JSONValue([]byte(`{"x":1,"x":2}`)); err == nil {
		t.Fatal("duplicate JSON key was accepted")
	}
	if _, err := VectorValue(nil); err == nil {
		t.Fatal("empty vector was accepted")
	}
	if _, err := GeoPointValue(91, 0); err == nil {
		t.Fatal("invalid latitude was accepted")
	}
	if _, err := TimeValue(maxTimeNanos + 1); err == nil {
		t.Fatal("invalid time was accepted")
	}
}

func TestRowAccessorsDoNotCoerceNumericTypes(t *testing.T) {
	row := Row{IntegerValue(42), mustFloat(t, 42), mustText(t, "42")}
	if row.Int(0) != 42 || row.Int(1) != 0 || row.Int(2) != 0 {
		t.Fatalf("integer accessor coerced a non-integer: %#v", row)
	}
	if row.Float(1) != 42 || row.Float(0) != 0 {
		t.Fatalf("float accessor coerced an integer: %#v", row)
	}
}

func TestParameterizedExecAndQuery(t *testing.T) {
	db := openTestDB(t)
	if err := db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT, score REAL, active BOOLEAN)"); err != nil {
		t.Fatal(err)
	}
	attack := "x'); DROP TABLE items; --"
	if err := db.Exec("INSERT INTO items (id, name, score, active) VALUES (?, ?, ?, ?)", IntegerValue(1), mustText(t, attack), mustFloat(t, 1.5), BooleanValue(true)); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query("SELECT id, name, score, active FROM items WHERE id = ?", IntegerValue(1))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Int(0) != 1 || rows[0].Str(1) != attack || rows[0].Float(2) != 1.5 || !rows[0].Bool(3) {
		t.Fatalf("unexpected parameterized result: %#v", rows)
	}
	if _, err := db.Query("SELECT COUNT(*) FROM items"); err != nil {
		t.Fatalf("table was altered by parameter content: %v", err)
	}
}

func TestNativeFailureIsNotClassifiedFromText(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Query("SELECT * FROM definitely_missing_table")
	if ErrorCodeOf(err) != CodeNativeUnclassified {
		t.Fatalf("native error code = %q, error = %v", ErrorCodeOf(err), err)
	}
}
