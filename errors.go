/*
 * Copyright (c) 2026 Talon Contributors
 * Author: dark.lijin@gmail.com
 * Licensed under the Talon Community Dual License Agreement.
 * See the LICENSE file in the project root for full license information.
 */

package talon

import (
	"errors"
	"fmt"
)

// ErrorCode is a stable SDK-level error classification. The bundled C ABI
// exposes engine failures as text only, so ErrorEngine deliberately does not
// guess finer-grained constraint or storage error categories from messages.
type ErrorCode string

const (
	ErrorUnknown         ErrorCode = "unknown"
	ErrorClosed          ErrorCode = "closed"
	ErrorInvalidArgument ErrorCode = "invalid_argument"
	ErrorUnsupported     ErrorCode = "unsupported"
	ErrorEncode          ErrorCode = "encode"
	ErrorNativeCall      ErrorCode = "native_call"
	ErrorProtocol        ErrorCode = "protocol"
	ErrorEngine          ErrorCode = "engine"
)

// OperationError describes an SDK-side failure with a stable code.
type OperationError struct {
	Code    ErrorCode
	Op      string
	Message string
	Cause   error
}

func (e *OperationError) Error() string {
	if e.Op == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Op, e.Message)
}

func (e *OperationError) Unwrap() error { return e.Cause }

// ErrorCode returns the stable SDK classification.
func (e *OperationError) ErrorCode() ErrorCode { return e.Code }

// ErrorCode marks native business errors without changing TalonError's
// backward-compatible one-field representation.
func (e *TalonError) ErrorCode() ErrorCode { return ErrorEngine }

// ErrorCodeOf returns the stable classification of err. Wrapped errors are
// traversed. Errors not produced by this SDK are ErrorUnknown.
func ErrorCodeOf(err error) ErrorCode {
	if err == nil {
		return ErrorUnknown
	}
	var coded interface{ ErrorCode() ErrorCode }
	if errors.As(err, &coded) {
		return coded.ErrorCode()
	}
	return ErrorUnknown
}

func operationError(code ErrorCode, op, message string, cause error) error {
	return &OperationError{Code: code, Op: op, Message: message, Cause: cause}
}
