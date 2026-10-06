//go:build !talon_sql_context_test

package talon

import (
	"context"
	"unsafe"
)

func armSQLContextTest(context.Context, unsafe.Pointer) func() { return func() {} }
