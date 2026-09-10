package talon

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestNativeErrorCodesAreValidatedAndClosed(t *testing.T) {
	known := newNativeError("write", "conflict", "conflict")
	if ErrorCodeOf(known) != CodeNativeConflict || NativeCodeOf(known) != "conflict" {
		t.Fatalf("known machine code mapping = (%q,%q)", ErrorCodeOf(known), NativeCodeOf(known))
	}
	indeterminate := newNativeError("write", "possibly applied", "uncertain")
	if ErrorCodeOf(indeterminate) != CodeResultIndeterminate {
		t.Fatalf("uncertain machine code = %q", ErrorCodeOf(indeterminate))
	}
	unknown := newNativeError("write", "future", "future_code")
	if ErrorCodeOf(unknown) != CodeNativeUnclassified || NativeCodeOf(unknown) != "future_code" {
		t.Fatalf("unknown machine code mapping = (%q,%q)", ErrorCodeOf(unknown), NativeCodeOf(unknown))
	}
	stableNativeCodes := map[string]ErrorCode{
		"unsupported_version":    CodeNativeUnsupportedVersion,
		"invalid_namespace":      CodeNativeInvalidNamespace,
		"invalid_request":        CodeNativeInvalidRequest,
		"request_too_large":      CodeNativeRequestTooLarge,
		"result_too_large":       CodeNativeResultTooLarge,
		"cursor_mismatch":        CodeNativeCursorMismatch,
		"cursor_unavailable":     CodeNativeCursorUnavailable,
		"resource_exhausted":     CodeNativeResourceExhausted,
		"snapshot_not_available": CodeSnapshotNotAvailable,
		"corrupt_state":          CodeNativeCorruptState,
		"storage_error":          CodeNativeStorage,
		"unsupported_action":     CodeNativeUnsupportedAction,
		"not_configured":         CodeNativeNotConfigured,
	}
	for nativeCode, expected := range stableNativeCodes {
		err := newNativeError("conditional get", "definitive native error", nativeCode)
		if ErrorCodeOf(err) != expected || NativeCodeOf(err) != nativeCode {
			t.Fatalf("stable native code %q = (%q,%q)", nativeCode, ErrorCodeOf(err), NativeCodeOf(err))
		}
	}
	for _, invalid := range []string{"UPPER", "has.dot", "1leading", "has-hyphen", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"} {
		err := newNativeError("write", "diagnostic", invalid)
		if ErrorCodeOf(err) != CodeProtocolViolation || NativeCodeOf(err) != "" {
			t.Fatalf("invalid machine code %q was exposed as (%q,%q)", invalid, ErrorCodeOf(err), NativeCodeOf(err))
		}
	}
}

func TestNativeResponseErrorPreservesValidatedRoutingMetadata(t *testing.T) {
	term := uint64(9_007_199_254_740_993)
	address := "127.0.0.1:9000"
	err := newNativeResponseError("write", "not leader", "not_leader", &term, &NativeLeaderHint{NodeID: "n1", Address: &address})
	var typed *TalonError
	if !errors.As(err, &typed) || typed.Term == nil || *typed.Term != term || typed.LeaderHint == nil || typed.LeaderHint.NodeID != "n1" {
		t.Fatalf("routing metadata was lost: %#v", typed)
	}
	if _, err := decodeNativeLeaderHint(json.RawMessage(`{"node_id":"n1","address":"127.0.0.1:9000","extra":true}`)); err == nil {
		t.Fatal("unknown leader_hint field was accepted")
	}
}
