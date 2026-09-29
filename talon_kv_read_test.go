package talon

import (
	"bytes"
	"testing"
)

func TestNativeKVReadV1BinaryCodec(t *testing.T) {
	request, err := encodeNativeKVKeys([][]byte{{0, 0xff}, {}, {0, 0xff}}, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{3, 0, 0, 0, 2, 0, 0, 0, 0, 0xff, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0xff}
	if !bytes.Equal(request, want) {
		t.Fatalf("request = %v, want %v", request, want)
	}
	reply := []byte{3, 0, 0, 0, 1, 3, 0, 0, 0, 0, 0xff, 0, 0, 1, 0, 0, 0, 0}
	values, err := parseNativeKVMGet(reply, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 || !values[0].Present || !bytes.Equal(values[0].Value, []byte{0, 0xff, 0}) || values[1].Present || !values[2].Present || values[2].Value == nil || len(values[2].Value) != 0 {
		t.Fatalf("MGET values = %#v", values)
	}
	if _, err := parseNativeKVMGet(reply, 2); err == nil {
		t.Fatal("mismatched MGET count was accepted")
	}
}

func TestNativeKVReadV1RejectsMalformedReplies(t *testing.T) {
	for _, data := range [][]byte{
		{}, {2}, {0, 99}, {1}, {1, 5, 0, 0, 0, 1}, {1, 0, 0, 0, 0, 9},
	} {
		if _, err := parseNativeKVGet(data); err == nil {
			t.Errorf("malformed GET reply %v was accepted", data)
		}
	}
	if _, err := encodeNativeKVKeys([][]byte{make([]byte, nativeKVMaxKey+1)}, false); err == nil {
		t.Fatal("oversized key was accepted")
	}
	if _, err := encodeNativeKVKeys(make([][]byte, nativeKVMaxKeys+1), true); err == nil {
		t.Fatal("oversized key count was accepted")
	}
}

func TestNativeKVReadV1GateAndABISets(t *testing.T) {
	old := append([]string(nil), sdkRequiredSymbols...)
	newSymbols := append(append([]string(nil), old...), "talon_kv_read_v1")
	if !supportedNativeSymbolSet(old) || !supportedNativeSymbolSet(newSymbols) || supportedNativeSymbolSet(append(newSymbols, "surprise_symbol")) {
		t.Fatal("exact old/new native ABI symbol sets were not enforced")
	}
	db := &DB{nativeInfo: NativeInfo{
		Features:     []string{"native_kv_read_v1"},
		Capabilities: []NativeCapability{{Name: "native_kv_read", Version: 1, Status: "gated"}},
	}}
	if err := db.RequireCapability("native_kv_read"); err == nil {
		t.Fatal("gated Core capability was accepted")
	}
	db.nativeInfo.Capabilities[0].Status = "available"
	if err := db.RequireCapability("native_kv_read"); err != nil {
		t.Fatalf("available versioned capability was rejected: %v", err)
	}
	db.nativeInfo.Features = nil
	if err := db.RequireCapability("native_kv_read"); err == nil {
		t.Fatal("capability without matching feature was accepted")
	}
}
