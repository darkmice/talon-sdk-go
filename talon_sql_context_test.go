package talon

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestSQLContextCancellationPreservesUncertainty(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	original := &TalonError{Code: CodeResultIndeterminate, NativeCode: "uncertain", Message: "commit acknowledgement unknown", Cause: errors.New("durability cause")}
	err := sqlContextError(ctx, original)
	if ErrorCodeOf(err) != CodeResultIndeterminate || !errors.Is(err, original) || !errors.Is(err, context.Canceled) {
		t.Fatalf("lost outcome/cause: %v", err)
	}
	if sqlContextError(ctx, nil) != nil {
		t.Fatal("confirmed success replaced with cancellation")
	}
	unrelated := newNativeError("sql", "storage failed", "storage_error")
	if sqlContextError(ctx, unrelated) != unrelated {
		t.Fatal("cancellation masked unrelated storage failure")
	}
	for _, item := range []struct {
		code  string
		cause error
	}{{"cancelled", context.Canceled}, {"deadline_exceeded", context.DeadlineExceeded}} {
		err := sqlContextError(context.Background(), newNativeError("sql", "native interruption", item.code))
		if !errors.Is(err, item.cause) || NativeCodeOf(err) != item.code {
			t.Fatalf("%s: %v", item.code, err)
		}
	}
}

func TestSQLContextCapabilityContract(t *testing.T) {
	policy, base := developmentTestPolicy(t)
	good := base
	good.ABI.RequiredSymbols = append(append([]string(nil), base.ABI.RequiredSymbols...), sqlContextSymbols...)
	good.Features = append(append([]string(nil), base.Features...), "native_sql_context_v1")
	good.Capabilities = append(append([]NativeCapability(nil), base.Capabilities...), NativeCapability{Name: "native_sql_context", Version: 1, Status: "available"})
	if err := validateLocalDevelopmentBuild(good, policy); err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"feature", "capability", "partial_symbols", "unknown_version"} {
		changed := good
		switch part {
		case "feature":
			changed.Features = base.Features
		case "capability":
			changed.Capabilities = base.Capabilities
		case "partial_symbols":
			changed.ABI.RequiredSymbols = good.ABI.RequiredSymbols[:len(good.ABI.RequiredSymbols)-1]
		case "unknown_version":
			changed.Capabilities = append([]NativeCapability(nil), good.Capabilities...)
			changed.Capabilities[len(changed.Capabilities)-1].Version = 2
		}
		if validateLocalDevelopmentBuild(changed, policy) == nil {
			t.Fatalf("accepted %s", part)
		}
	}
}

func TestNativeSQLContextLegacyCapabilityGate(t *testing.T) {
	if os.Getenv("TALON_TEST_SQL_CONTEXT_LEGACY") != "1" {
		t.Skip("explicit pinned old local Core required")
	}
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.RequireCapability("native_sql_context") == nil {
		t.Fatal("expected fixed old artifact without cancellation")
	}
	if _, err := db.QueryResultContext(context.Background(), "CREATE TABLE ctx_legacy(id INT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := db.QueryResultContext(ctx, "INSERT INTO ctx_legacy VALUES(1)"); ErrorCodeOf(err) != CodeCapabilityUnavailable {
		t.Fatalf("bounded call silently fell back: %v", err)
	}
	rows, err := db.QueryResult("SELECT id FROM ctx_legacy")
	if err != nil || len(rows.Rows) != 0 {
		t.Fatalf("rejected call mutated rows: %+v %v", rows, err)
	}
	t.Logf("legacy admission=%s library=%s; cancellable operation fails before native dispatch", db.NativeInfo().Admission, db.NativeInfo().LibrarySHA256)
}

func TestSQLContextVersionedErrorEnvelope(t *testing.T) {
	for _, item := range []struct {
		wire  string
		code  ErrorCode
		cause error
	}{
		{`{"ok":false,"error":"interrupted","code":"cancelled","cause":"cancelled","retryable":false,"transaction_state":"rolled_back_if_owned"}`, CodeNativeUnclassified, context.Canceled},
		{`{"ok":false,"error":"unknown","code":"result_indeterminate","cause":"deadline_exceeded","retryable":false,"transaction_state":"consumed","outcome":"unknown"}`, CodeResultIndeterminate, context.DeadlineExceeded},
	} {
		_, err := decodeSQLContextCommandResult([]byte(item.wire))
		if ErrorCodeOf(err) != item.code || !errors.Is(err, item.cause) {
			t.Fatalf("versioned cause lost: %v", err)
		}
		if _, legacy := decodeCommandResult([]byte(item.wire), "sql", "query"); ErrorCodeOf(legacy) != CodeProtocolViolation {
			t.Fatal("legacy decoder admitted unversioned extension")
		}
	}
	for _, wire := range []string{
		`{"ok":true,"data":[],"cause":"cancelled","retryable":false}`,
		`{"ok":false,"error":"unknown","code":"result_indeterminate","cause":"cancelled","retryable":true,"transaction_state":"consumed","outcome":"unknown"}`,
		`{"ok":false,"error":"unknown","code":"result_indeterminate","cause":"cancelled","retryable":false,"transaction_state":"rolled_back","outcome":"unknown"}`,
	} {
		if _, err := decodeSQLContextCommandResult([]byte(wire)); ErrorCodeOf(err) != CodeProtocolViolation {
			t.Fatalf("invalid context envelope admitted: %v", err)
		}
	}
}

func TestSQLContextCleanupFailureKeepsOriginalInterruption(t *testing.T) {
	wire := `{"ok":false,"code":"result_indeterminate","cause":"cleanup_failed","retryable":false,"error":"cleanup failed","interruption":{"ok":false,"code":"deadline_exceeded","cause":"deadline_exceeded","retryable":false,"transaction_state":"rolled_back_if_owned","error":"original deadline"}}`
	_, err := decodeSQLContextCommandResult([]byte(wire))
	if ErrorCodeOf(err) != CodeResultIndeterminate || NativeCodeOf(err) != "result_indeterminate" || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanup failure masked original cause: %v", err)
	}
}
