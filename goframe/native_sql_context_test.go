package goframe

import (
	"context"
	"database/sql/driver"
	"errors"
	talon "github.com/darkmice/talon-sdk-go"
	"testing"
)

func TestNativeTransactionContextAndCleanupOwnership(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fake := &fakeNative{}
	conn := &nativeConn{db: fake}
	tx, err := conn.BeginTx(ctx, driver.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	fake.execHook = func(actual context.Context, sql string) error {
		if actual != ctx || sql != "ROLLBACK" {
			t.Fatalf("lost transaction context: %s", sql)
		}
		return actual.Err()
	}
	err = tx.Rollback()
	if !errors.Is(err, context.Canceled) || conn.IsValid() || fake.closed != 1 {
		t.Fatalf("cleanup err=%v valid=%v close=%d", err, conn.IsValid(), fake.closed)
	}
}

func TestNativeCommitUnknownIsNotRetriedAndSessionDiscarded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	original := &talon.TalonError{Code: talon.CodeResultIndeterminate, NativeCode: "uncertain", Message: "unknown commit"}
	fake := &fakeNative{}
	conn := &nativeConn{db: fake}
	tx, err := conn.BeginTx(ctx, driver.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fake.execHook = func(actual context.Context, sql string) error {
		if actual != ctx || sql != "COMMIT" {
			t.Fatalf("unexpected terminal %s", sql)
		}
		cancel()
		return errors.Join(original, ctx.Err())
	}
	err = tx.Commit()
	if talon.ErrorCodeOf(err) != talon.CodeResultIndeterminate || !errors.Is(err, original) || !errors.Is(err, context.Canceled) || conn.IsValid() || fake.closed != 1 {
		t.Fatalf("unknown envelope or cleanup lost: %v", err)
	}
	if len(fake.execCalls) != 2 {
		t.Fatalf("commit replayed/rollback falsely claimed: %v", fake.execCalls)
	}
}

func TestNativeFailedBeginDiscardsUntrackedSession(t *testing.T) {
	cause := &talon.TalonError{Code: talon.CodeProtocolViolation, Message: "response omitted result"}
	fake := &fakeNative{execError: cause}
	conn := &nativeConn{db: fake}
	if _, err := conn.BeginTx(context.Background(), driver.TxOptions{}); !errors.Is(err, cause) {
		t.Fatalf("lost begin cause: %v", err)
	}
	if conn.IsValid() || fake.closed != 1 {
		t.Fatal("failed BEGIN left untracked session in pool")
	}
}
