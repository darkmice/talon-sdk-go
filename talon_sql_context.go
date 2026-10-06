package talon

/*
#include <stdlib.h>
#include "native_loader.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unsafe"
)

// QueryResultContext executes synchronously on the native session. Cancellable
// contexts require native_sql_context v1; there is no detached-query fallback.
// Cancellation is cooperative, not a hard bound on filesystem or OS calls.
// A confirmed successful native result wins over a concurrent cancellation.
func (db *DB) QueryResultContext(ctx context.Context, sql string, params ...Value) (SQLResult, error) {
	if ctx == nil {
		return SQLResult{}, newError(CodeInvalidArgument, "sql context", "context is nil", nil)
	}
	if err := ctx.Err(); err != nil {
		return SQLResult{}, err
	}
	return db.queryResultContext(ctx, sql, false, params...)
}

// ExecContext executes a statement without result rows on the native session.
func (db *DB) ExecContext(ctx context.Context, sql string, params ...Value) error {
	result, err := db.QueryResultContext(ctx, sql, params...)
	if err != nil {
		return err
	}
	if len(result.Rows) != 0 {
		return newError(CodeProtocolViolation, "exec context", "statement returned rows; use QueryResultContext", nil)
	}
	return nil
}

// SQLRollbackContext always dispatches cleanup even if ctx is already cancelled.
// Core must finish cleanup before reporting cancellation; callers must wait for
// return. Unlike queries, skipping an expired rollback would leave a transaction.
func (db *DB) SQLRollbackContext(ctx context.Context) error {
	if ctx == nil {
		return newError(CodeInvalidArgument, "rollback context", "context is nil", nil)
	}
	_, err := db.queryResultContext(ctx, "ROLLBACK", true)
	return err
}

func (db *DB) queryResultContext(ctx context.Context, sql string, cleanup bool, params ...Value) (SQLResult, error) {
	if strings.TrimSpace(sql) == "" {
		return SQLResult{}, newError(CodeInvalidArgument, "sql context", "SQL is empty", nil)
	}
	if err := db.RequireCapability("native_sql_result"); err != nil {
		return SQLResult{}, err
	}
	if err := db.RequireCapability("native_sql_context"); err != nil {
		// Existing signed runtimes remain usable for truly non-cancellable calls.
		if ctx.Done() == nil {
			return db.QueryResult(sql, params...)
		}
		return SQLResult{}, err
	}
	if params == nil {
		params = []Value{}
	}
	request, err := json.Marshal(map[string]interface{}{"module": "sql", "action": "query", "params": map[string]interface{}{"sql": sql, "bind": params, "protocol_version": 2}})
	if err != nil {
		return SQLResult{}, newError(CodeInvalidArgument, "sql context", "encode SQL request", err)
	}
	if len(request) > maxNativeJSONRequestBytes {
		return SQLResult{}, newError(CodeInvalidArgument, "sql context", "native JSON request exceeds the SDK bound", nil)
	}
	data, err := db.executeSQLContext(ctx, request, cleanup)
	if err != nil {
		return SQLResult{}, err
	}
	return decodeSQLResult(data)
}

func (db *DB) executeSQLContext(ctx context.Context, request []byte, cleanup bool) (json.RawMessage, error) {
	// Close waits for native completion. A waiting query can observe its deadline
	// while acquiring the handle lease; rollback must acquire it to finish cleanup.
	if cleanup {
		db.mu.RLock()
	} else {
		for !db.mu.TryRLock() {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Millisecond):
			}
		}
	}
	defer db.mu.RUnlock()
	if db.handle == nil {
		return nil, newError(CodeDatabaseClosed, "sql context", "database is closed", nil)
	}
	if !cleanup {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	timeout := uint64(0)
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			timeout = 1
		} else {
			timeout = uint64(remaining / time.Millisecond)
			if remaining%time.Millisecond != 0 {
				timeout++
			}
		}
	}
	token := C.talon_sdk_sql_context_new(C.uint64_t(timeout))
	if token == nil {
		return nil, newError(CodeNativeUnavailable, "sql context", "native SQL context could not be allocated", nil)
	}
	// The watcher ONLY signals the token. SQL itself stays on this goroutine.
	// Join the watcher before freeing C memory, including the cancellation race.
	stop, joined := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-ctx.Done():
			// The Core deadline is rounded up to whole milliseconds. Signalling
			// cancel at the slightly earlier Go deadline would incorrectly latch
			// "cancelled" as the first cause. Let its monotonic deadline expire.
			if !errors.Is(ctx.Err(), context.DeadlineExceeded) || timeout == 0 {
				C.talon_sdk_sql_context_cancel(token)
			}
		case <-stop:
		}
	}()
	defer func() { close(stop); <-joined; C.talon_sdk_sql_context_free(token) }()
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && timeout > 0 {
			// Expired rollback still dispatches cleanup with an expired token.
			// 0 means no deadline in ABI v1, so allow its 1ms deadline to elapse.
			time.Sleep(time.Millisecond)
		} else {
			C.talon_sdk_sql_context_cancel(token)
		}
	}
	disarmTest := armSQLContextTest(ctx, unsafe.Pointer(token))
	defer disarmTest()
	command := C.CString(string(request))
	defer C.free(unsafe.Pointer(command))
	var output *C.char
	var code [128]C.char
	rc := C.talon_sdk_execute_sql_context(db.handle, command, token, &output, &code[0], C.size_t(len(code)))
	if output != nil {
		defer C.talon_sdk_free_string(output)
	}
	if rc != 0 {
		return nil, sqlContextError(ctx, nativeFailure("sql context", "native SQL context call failed", C.GoString(&code[0])))
	}
	if output == nil {
		return nil, newError(CodeProtocolViolation, "sql context", "native SQL context returned a null result", nil)
	}
	wire, err := boundedCString(output, maxNativeJSONResultBytes)
	if err != nil {
		return nil, newError(CodeProtocolViolation, "sql context", "native SQL response exceeds bound or is unterminated", err)
	}
	data, err := decodeSQLContextCommandResult(wire)
	return data, sqlContextError(ctx, err)
}

func sqlContextError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	switch NativeCodeOf(err) {
	case "cancelled":
		if errors.Is(err, context.Canceled) {
			return err
		}
		return errors.Join(err, context.Canceled)
	case "deadline_exceeded":
		if errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return errors.Join(err, context.DeadlineExceeded)
	}
	// Keep uncertain as the primary classification. A deadline does not turn an
	// indeterminate commit into a known rollback and must never trigger replay.
	if ErrorCodeOf(err) == CodeResultIndeterminate && ctx.Err() != nil && !errors.Is(err, ctx.Err()) {
		return errors.Join(err, ctx.Err())
	}
	return err
}

// Context v1 adds typed outcome/cause fields to the ordinary command envelope.
// Validate this versioned extension before passing the base envelope to the
// shared decoder; legacy SQL/other modules do not accept these extra fields.
func decodeSQLContextCommandResult(data []byte) (json.RawMessage, error) {
	var wire struct {
		cmdResult
		Cause            string          `json:"cause"`
		Retryable        *bool           `json:"retryable"`
		TransactionState string          `json:"transaction_state"`
		Outcome          string          `json:"outcome"`
		Interruption     json.RawMessage `json:"interruption"`
	}
	if err := decodeStrictJSON(data, &wire); err != nil {
		return nil, newError(CodeProtocolViolation, "sql context", "invalid context response", err)
	}
	extended := wire.Cause != "" || wire.Retryable != nil || wire.TransactionState != "" || wire.Outcome != "" || len(wire.Interruption) != 0
	if extended {
		if wire.OK == nil || *wire.OK || wire.Retryable == nil || *wire.Retryable {
			return nil, newError(CodeProtocolViolation, "sql context", "invalid context outcome fields", nil)
		}
		switch wire.Code {
		case "cancelled", "deadline_exceeded":
			if wire.Cause != wire.Code || wire.TransactionState != "rolled_back_if_owned" || wire.Outcome != "" || len(wire.Interruption) != 0 {
				return nil, newError(CodeProtocolViolation, "sql context", "invalid interruption outcome", nil)
			}
		case "result_indeterminate":
			if wire.Cause != "cancelled" && wire.Cause != "deadline_exceeded" && wire.Cause != "cleanup_failed" {
				return nil, newError(CodeProtocolViolation, "sql context", "invalid indeterminate cause", nil)
			}
			if wire.Cause != "cleanup_failed" && (wire.TransactionState != "consumed" || wire.Outcome != "unknown" || len(wire.Interruption) != 0) {
				return nil, newError(CodeProtocolViolation, "sql context", "invalid indeterminate outcome", nil)
			}
		default:
			return nil, newError(CodeProtocolViolation, "sql context", "unexpected context outcome extension", nil)
		}
	}
	var interruption error
	if wire.Cause == "cleanup_failed" {
		if wire.TransactionState != "" || wire.Outcome != "" || len(wire.Interruption) == 0 {
			return nil, newError(CodeProtocolViolation, "sql context", "invalid cleanup failure outcome", nil)
		}
		var original struct {
			Code  string `json:"code"`
			Cause string `json:"cause"`
		}
		if json.Unmarshal(wire.Interruption, &original) != nil || (original.Code != "cancelled" && original.Code != "deadline_exceeded" && original.Code != "result_indeterminate") || (original.Cause != "cancelled" && original.Cause != "deadline_exceeded") {
			return nil, newError(CodeProtocolViolation, "sql context", "invalid cleanup interruption cause", nil)
		}
		_, interruption = decodeSQLContextCommandResult(wire.Interruption)
		if interruption == nil || ErrorCodeOf(interruption) == CodeProtocolViolation {
			return nil, newError(CodeProtocolViolation, "sql context", "invalid cleanup interruption envelope", interruption)
		}
	}
	var base map[string]json.RawMessage
	if err := json.Unmarshal(data, &base); err != nil {
		return nil, newError(CodeProtocolViolation, "sql context", "invalid context base", err)
	}
	for _, field := range []string{"cause", "retryable", "transaction_state", "outcome", "interruption"} {
		delete(base, field)
	}
	normalized, err := json.Marshal(base)
	if err != nil {
		return nil, newError(CodeProtocolViolation, "sql context", "encode context base", err)
	}
	result, err := decodeCommandResult(normalized, "sql", "query")
	if err != nil && interruption != nil {
		err = errors.Join(err, interruption)
	}
	if err != nil {
		switch wire.Cause {
		case "cancelled":
			err = errors.Join(err, context.Canceled)
		case "deadline_exceeded":
			err = errors.Join(err, context.DeadlineExceeded)
		}
	}
	return result, err
}
