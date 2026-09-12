/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package talon

import (
	"errors"
	"regexp"

	serverapi "github.com/darkmice/talon-sdk-go/server"
)

// ErrorCode is a stable SDK-level error classification. Known, self-attested
// Core machine codes are mapped explicitly; unknown codes remain
// CodeNativeUnclassified. The SDK never derives a code from diagnostic text.
type ErrorCode = serverapi.ErrorCode

const (
	CodeInvalidArgument          = serverapi.CodeInvalidArgument
	CodeDatabaseClosed           = serverapi.CodeDatabaseClosed
	CodeNativeVerification       = serverapi.CodeNativeVerification
	CodeNativeLoad               = serverapi.CodeNativeLoad
	CodeProtocolViolation        = serverapi.CodeProtocolViolation
	CodeNativeUnclassified       = serverapi.CodeNativeUnclassified
	CodeCapabilityUnavailable    = serverapi.CodeCapabilityUnavailable
	CodeResultIndeterminate      = serverapi.CodeResultIndeterminate
	CodeNativeConflict           = serverapi.CodeNativeConflict
	CodeNativeNotFound           = serverapi.CodeNativeNotFound
	CodeNativeNotLeader          = serverapi.CodeNativeNotLeader
	CodeNativeNoQuorum           = serverapi.CodeNativeNoQuorum
	CodeNativeUnavailable        = serverapi.CodeNativeUnavailable
	CodeNativeFenced             = serverapi.CodeNativeFenced
	CodeNativeTimeout            = serverapi.CodeNativeTimeout
	CodeNativePersistence        = serverapi.CodeNativePersistence
	CodeSnapshotNotAvailable     = serverapi.CodeSnapshotNotAvailable
	CodeCorruptStream            = serverapi.CodeCorruptStream
	CodeNativeUnsupportedVersion = serverapi.CodeNativeUnsupportedVersion
	CodeNativeInvalidNamespace   = serverapi.CodeNativeInvalidNamespace
	CodeNativeInvalidRequest     = serverapi.CodeNativeInvalidRequest
	CodeNativeRequestTooLarge    = serverapi.CodeNativeRequestTooLarge
	CodeNativeResultTooLarge     = serverapi.CodeNativeResultTooLarge
	CodeNativeCursorMismatch     = serverapi.CodeNativeCursorMismatch
	CodeNativeCursorUnavailable  = serverapi.CodeNativeCursorUnavailable
	CodeNativeResourceExhausted  = serverapi.CodeNativeResourceExhausted
	CodeNativeCorruptState       = serverapi.CodeNativeCorruptState
	CodeNativeStorage            = serverapi.CodeNativeStorage
	CodeNativeUnsupportedAction  = serverapi.CodeNativeUnsupportedAction
	CodeNativeNotConfigured      = serverapi.CodeNativeNotConfigured
	CodeNativeDecimalOutOfRange  = serverapi.CodeNativeDecimalOutOfRange
)

// TalonError is the public machine-readable error envelope.
type TalonError = serverapi.TalonError

// NativeLeaderHint is validated routing metadata attached to a native error.
type NativeLeaderHint = serverapi.NativeLeaderHint

// ErrorCodeOf returns a stable classification without inspecting error text.
func ErrorCodeOf(err error) ErrorCode {
	return serverapi.ErrorCodeOf(err)
}

// NativeCodeOf returns the Core-provided machine code. It never parses Error().
func NativeCodeOf(err error) string {
	return serverapi.NativeCodeOf(err)
}

func newError(code ErrorCode, operation, message string, cause error) error {
	return &TalonError{Code: code, Operation: operation, Message: message, Cause: cause}
}

func newNativeError(operation, message, nativeCode string) error {
	if nativeCode != "" && !nativeCodePattern.MatchString(nativeCode) {
		return &TalonError{Code: CodeProtocolViolation, Operation: operation, Message: "native returned an invalid machine error code", Cause: errors.New(message)}
	}
	code := CodeNativeUnclassified
	switch nativeCode {
	case "uncertain":
		code = CodeResultIndeterminate
	case "conflict":
		code = CodeNativeConflict
	case "not_found":
		code = CodeNativeNotFound
	case "not_leader":
		code = CodeNativeNotLeader
	case "no_quorum":
		code = CodeNativeNoQuorum
	case "unavailable":
		code = CodeNativeUnavailable
	case "fenced":
		code = CodeNativeFenced
	case "timeout":
		code = CodeNativeTimeout
	case "persistence_failed":
		code = CodeNativePersistence
	case "snapshot_not_available":
		code = CodeSnapshotNotAvailable
	case "corrupt_stream":
		code = CodeCorruptStream
	case "unsupported_version":
		code = CodeNativeUnsupportedVersion
	case "invalid_namespace":
		code = CodeNativeInvalidNamespace
	case "invalid_request":
		code = CodeNativeInvalidRequest
	case "request_too_large":
		code = CodeNativeRequestTooLarge
	case "result_too_large":
		code = CodeNativeResultTooLarge
	case "cursor_mismatch":
		code = CodeNativeCursorMismatch
	case "cursor_unavailable":
		code = CodeNativeCursorUnavailable
	case "resource_exhausted":
		code = CodeNativeResourceExhausted
	case "corrupt_state":
		code = CodeNativeCorruptState
	case "storage_error":
		code = CodeNativeStorage
	case "unsupported_action":
		code = CodeNativeUnsupportedAction
	case "not_configured":
		code = CodeNativeNotConfigured
	case "decimal_out_of_range":
		code = CodeNativeDecimalOutOfRange
	}
	return &TalonError{Code: code, NativeCode: nativeCode, Operation: operation, Message: message}
}

func newNativeResponseError(operation, message, nativeCode string, term *uint64, leaderHint *NativeLeaderHint) error {
	err := newNativeError(operation, message, nativeCode)
	var target *TalonError
	if errors.As(err, &target) {
		if term != nil {
			value := *term
			target.Term = &value
		}
		if leaderHint != nil {
			copy := *leaderHint
			if leaderHint.Address != nil {
				address := *leaderHint.Address
				copy.Address = &address
			}
			target.LeaderHint = &copy
		}
	}
	return err
}

var nativeCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

var (
	// ErrStableNativeErrorCodesUnavailable is an explicit ABI gate. Core's
	// released, signed, self-attested Core bundle must advertise
	// native_error_codes_v1 before callers may rely on it.
	ErrStableNativeErrorCodesUnavailable = &TalonError{
		Code:      CodeCapabilityUnavailable,
		Operation: "native error classification",
		Message:   "the loaded Core bundle does not attest stable machine-readable error codes",
	}
	// ErrStorageConditionalBatchUnavailable prevents SDK-side CAS or lock
	// emulation while storage_conditional_batch_v1 is not a released Core ABI.
	ErrStorageConditionalBatchUnavailable = &TalonError{
		Code:      CodeCapabilityUnavailable,
		Operation: "storage conditional batch",
		Message:   "storage_conditional_batch_v1 is not implemented by this SDK/Core ABI",
	}
)

// Legacy names retained for the JSON-oriented helpers introduced in v0.2.2.
// They map onto the machine-readable classifications above so callers can
// migrate without a second error hierarchy.
const (
	ErrorUnknown         ErrorCode = CodeNativeUnclassified
	ErrorClosed          ErrorCode = CodeDatabaseClosed
	ErrorInvalidArgument ErrorCode = CodeInvalidArgument
	ErrorUnsupported     ErrorCode = CodeCapabilityUnavailable
	ErrorEncode          ErrorCode = CodeProtocolViolation
	ErrorNativeCall      ErrorCode = CodeNativeLoad
	ErrorProtocol        ErrorCode = CodeProtocolViolation
	ErrorEngine          ErrorCode = CodeNativeUnclassified
)

func operationError(code ErrorCode, operation, message string, cause error) error {
	return newError(code, operation, message, cause)
}
