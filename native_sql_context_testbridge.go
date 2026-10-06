//go:build talon_sql_context_test

package talon

/*
#cgo CFLAGS: -DTALON_SQL_CONTEXT_TEST
#include "native_loader.h"
int talon_sdk_test_context_arm(TalonSDKSqlContext *ctx, uint32_t stage);
uint32_t talon_sdk_test_context_entered(TalonSDKSqlContext *ctx);
int talon_sdk_test_context_release(TalonSDKSqlContext *ctx);
*/
import "C"

import (
	"context"
	"fmt"
	"sync"
	"unsafe"
)

type nativeSQLContextTestKey struct{}

// NativeSQLContextTestGate is compiled only for the explicit native test tag.
// Its token-specific latch requires a separate sql-context-test-seam Core build.
type NativeSQLContextTestGate struct {
	mu    sync.Mutex
	token *C.TalonSDKSqlContext
	stage uint32
	err   error
}

func NativeSQLContextTestContext(ctx context.Context, stage uint32) (context.Context, *NativeSQLContextTestGate) {
	gate := &NativeSQLContextTestGate{stage: stage}
	return context.WithValue(ctx, nativeSQLContextTestKey{}, gate), gate
}
func armSQLContextTest(ctx context.Context, token unsafe.Pointer) func() {
	gate, _ := ctx.Value(nativeSQLContextTestKey{}).(*NativeSQLContextTestGate)
	if gate == nil {
		return func() {}
	}
	gate.mu.Lock()
	if C.talon_sdk_test_context_arm((*C.TalonSDKSqlContext)(token), C.uint32_t(gate.stage)) != 0 {
		gate.err = fmt.Errorf("native test seam unavailable")
	} else {
		gate.token = (*C.TalonSDKSqlContext)(token)
	}
	gate.mu.Unlock()
	return func() { gate.mu.Lock(); gate.token = nil; gate.mu.Unlock() }
}
func (gate *NativeSQLContextTestGate) Entered() (bool, error) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.err != nil {
		return false, gate.err
	}
	if gate.token == nil {
		return false, nil
	}
	return uint32(C.talon_sdk_test_context_entered(gate.token)) == gate.stage, nil
}
func (gate *NativeSQLContextTestGate) Release() {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.token != nil {
		C.talon_sdk_test_context_release(gate.token)
	}
}
