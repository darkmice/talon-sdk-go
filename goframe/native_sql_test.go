package goframe

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"math/big"
	"reflect"
	"testing"
	"time"

	talon "github.com/darkmice/talon-sdk-go"
	"github.com/gogf/gf/v2/database/gdb"
)

type fakeNative struct {
	queryCalls []string
	contexts   []context.Context
	closed     int
	execHook   func(context.Context, string) error
	execCalls  []string
	result     talon.SQLResult
	capability error
	execError  error
	queryError error
}

func (f *fakeNative) QueryResultContext(ctx context.Context, sql string, _ ...talon.Value) (talon.SQLResult, error) {
	f.queryCalls = append(f.queryCalls, sql)
	f.contexts = append(f.contexts, ctx)
	return f.result, f.queryError
}

func TestNativeInsertIdRequiresIntegerSinglePrimaryKey(t *testing.T) {
	affected, id := uint64(1), int64(42)
	fake := &fakeNative{result: talon.SQLResult{AffectedRows: &affected, LastInsertID: &id}}
	conn := &nativeConn{db: fake}
	result, err := conn.ExecContext(context.Background(), "INSERT INTO items (name) VALUES (?)", []driver.NamedValue{{Ordinal: 1, Value: "item"}})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("RowsAffected=%d error=%v", n, err)
	}
	if id, err := result.LastInsertId(); err != nil || id != 42 {
		t.Fatalf("LastInsertId=%d error=%v", id, err)
	}
	fake.result.LastInsertID = nil
	result, err = conn.ExecContext(context.Background(), "INSERT INTO items (name) VALUES (?)", []driver.NamedValue{{Ordinal: 1, Value: "item"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := result.LastInsertId(); !errors.Is(err, ErrResultMetadataUnavailable) {
		t.Fatalf("composite key LastInsertId error=%v", err)
	}
}

func TestGoFrameInsertVariantsUseTalonNativeSyntax(t *testing.T) {
	affected := uint64(0)
	fake := &fakeNative{result: talon.SQLResult{AffectedRows: &affected}}
	conn := &nativeConn{db: fake}
	for _, input := range []string{
		"INSERT IGNORE INTO items(id) VALUES(?)",
		"REPLACE INTO items(id) VALUES(?)",
	} {
		if _, err := conn.ExecContext(context.Background(), input, []driver.NamedValue{{Ordinal: 1, Value: int64(1)}}); err != nil {
			t.Fatal(err)
		}
	}
	if fake.queryCalls[0] != "INSERT OR IGNORE INTO items(id) VALUES(?)" {
		t.Fatalf("ignore SQL=%q", fake.queryCalls[0])
	}
	if fake.queryCalls[1] != "INSERT OR REPLACE INTO items(id) VALUES(?)" {
		t.Fatalf("replace SQL=%q", fake.queryCalls[1])
	}
}
func (f *fakeNative) ExecContext(ctx context.Context, sql string, _ ...talon.Value) error {
	f.execCalls = append(f.execCalls, sql)
	f.contexts = append(f.contexts, ctx)
	if f.execHook != nil {
		return f.execHook(ctx, sql)
	}
	return f.execError
}
func (f *fakeNative) SQLRollbackContext(ctx context.Context) error {
	return f.ExecContext(ctx, "ROLLBACK")
}
func (f *fakeNative) Close()                         { f.closed++ }
func (f *fakeNative) RequireCapability(string) error { return f.capability }

func TestNativeQueryUsesDescribedColumns(t *testing.T) {
	id, _ := talon.TextValue("one")
	fake := &fakeNative{result: talon.SQLResult{Columns: []string{"id", "name"}, Rows: []talon.Row{{talon.IntegerValue(1), id}}}}
	conn := &nativeConn{db: fake}
	rows, err := conn.QueryContext(context.Background(), "SELECT id, name FROM items WHERE id = ?", []driver.NamedValue{{Ordinal: 1, Value: int64(1)}})
	if err != nil {
		t.Fatal(err)
	}
	if got := rows.Columns(); !reflect.DeepEqual(got, []string{"id", "name"}) {
		t.Fatalf("columns=%v", got)
	}
	values := make([]driver.Value, 2)
	if err := rows.Next(values); err != nil || values[0] != int64(1) || values[1] != "one" {
		t.Fatalf("values=%v err=%v", values, err)
	}
	if err := rows.Next(values); !errors.Is(err, io.EOF) {
		t.Fatalf("Next error=%v", err)
	}
	if got := fake.queryCalls; !reflect.DeepEqual(got, []string{"SELECT id, name FROM items WHERE id = ?"}) {
		t.Fatalf("native calls=%v", got)
	}
}

func TestNativeQueryUsesCoreColumnsForJoin(t *testing.T) {
	fake := &fakeNative{result: talon.SQLResult{Columns: []string{"id"}, Rows: []talon.Row{}}}
	conn := &nativeConn{db: fake}
	rows, err := conn.QueryContext(context.Background(), "SELECT a.id FROM a JOIN b ON a.id=b.id", nil)
	if err != nil || !reflect.DeepEqual(rows.Columns(), []string{"id"}) {
		t.Fatalf("columns=%v error=%v", rows.Columns(), err)
	}
	if len(fake.queryCalls) != 1 {
		t.Fatalf("query calls: %v", fake.queryCalls)
	}
}

func TestNativeExecReturnsCoreAffectedRows(t *testing.T) {
	affected := uint64(0)
	fake := &fakeNative{result: talon.SQLResult{AffectedRows: &affected}}
	conn := &nativeConn{db: fake}
	result, err := conn.ExecContext(context.Background(), "DELETE FROM items WHERE id=?", []driver.NamedValue{{Ordinal: 1, Value: int64(1)}})
	if err != nil || len(fake.queryCalls) != 1 {
		t.Fatalf("Exec result=%v error=%v calls=%v", result, err, fake.queryCalls)
	}
	if fake.queryCalls[0] != "DELETE FROM items WHERE id=?" {
		t.Fatalf("SQL=%q", fake.queryCalls[0])
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 0 {
		t.Fatalf("RowsAffected=%d error=%v", affected, err)
	}
	if _, err := conn.ExecContext(context.Background(), "DELETE FROM items; DELETE FROM items", nil); err == nil {
		t.Fatal("multi-statement mutation was accepted")
	}
	if len(fake.queryCalls) != 1 {
		t.Fatalf("unsafe mutation dispatched: %v", fake.queryCalls)
	}
	if _, err := conn.ExecContext(context.Background(), "INSERT INTO items(id) VALUES(?) ON DUPLICATE KEY UPDATE id=VALUES(id)", []driver.NamedValue{{Ordinal: 1, Value: int64(1)}}); err == nil {
		t.Fatal("MySQL upsert was accepted")
	}
	if len(fake.queryCalls) != 1 {
		t.Fatalf("upsert dispatched before rejection: %v", fake.queryCalls)
	}
}

func TestNativeTransactionUsesSameHandle(t *testing.T) {
	fake := new(fakeNative)
	conn := &nativeConn{db: fake}
	tx, err := conn.BeginTx(context.Background(), driver.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fake.execCalls, []string{"BEGIN", "ROLLBACK"}) {
		t.Fatalf("calls=%v", fake.execCalls)
	}
	fake.capability = errors.New("old native artifact")
	if _, err := conn.Begin(); !errors.Is(err, ErrTransactionsUnavailable) {
		t.Fatalf("Begin error=%v", err)
	}
	if len(fake.execCalls) != 2 {
		t.Fatalf("BEGIN dispatched without capability: %v", fake.execCalls)
	}
}

func TestNativeExactDecimalProjection(t *testing.T) {
	value, err := talon.DecimalValue(big.NewInt(-1230), 2)
	if err != nil {
		t.Fatal(err)
	}
	got, err := sqlValue(value)
	if err != nil || got != "-12.30" {
		t.Fatalf("decimal=%v error=%v", got, err)
	}
}

func TestNativeTimeParameterUsesTalonMilliseconds(t *testing.T) {
	instant := time.Unix(1_700_000_000, 123_000_000)
	value, err := convertParam(instant)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := value.Timestamp()
	if !ok || got != instant.UnixMilli() {
		t.Fatalf("timestamp=%d valid=%t", got, ok)
	}
}

func TestGoFrameNativeConfiguration(t *testing.T) {
	db, err := gdb.New(gdb.ConfigNode{Type: DriverName, Name: "/tmp/talon-goframe-test"})
	if err != nil {
		t.Fatal(err)
	}
	if left, right := db.GetChars(); left != "`" || right != "`" {
		t.Fatalf("quote=%q %q", left, right)
	}
	if _, err := (&Driver{}).Open(&gdb.ConfigNode{Name: "relative/path"}); err == nil {
		t.Fatal("relative path accepted")
	}
	if _, err := (&Driver{}).Open(&gdb.ConfigNode{Name: "/tmp/talon", Host: "127.0.0.1"}); err == nil {
		t.Fatal("server config accepted")
	}
}

func TestNativeSaveUsesTalonConflictSyntax(t *testing.T) {
	db, err := gdb.New(gdb.ConfigNode{Type: DriverName, Name: "/tmp/talon-goframe-save-test"})
	if err != nil {
		t.Fatal(err)
	}
	d := &Driver{Core: db.GetCore()}
	statement, err := d.FormatUpsert([]string{"id", "name"}, nil, gdb.DoInsertOption{
		InsertOption: gdb.InsertOptionSave,
		OnConflict:   []string{"id"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if statement != "ON CONFLICT (`id`) DO UPDATE SET `name`=EXCLUDED.`name`" {
		t.Fatalf("upsert=%q", statement)
	}
}

func TestNativeTransactionErrorContract(t *testing.T) {
	cause := errors.New("capability evidence")
	capability := &talon.TalonError{Code: talon.CodeCapabilityUnavailable, Cause: cause}
	fake := &fakeNative{capability: capability}
	conn := &nativeConn{db: fake}
	_, err := conn.Begin()
	var typed *talon.TalonError
	if !errors.Is(err, ErrTransactionsUnavailable) || !errors.Is(err, cause) || !errors.As(err, &typed) || talon.ErrorCodeOf(err) != talon.CodeCapabilityUnavailable {
		t.Fatalf("capability error chain lost: %v", err)
	}
	if len(fake.execCalls) != 0 {
		t.Fatal("BEGIN sent despite missing capability")
	}
	busy := &talon.TalonError{Code: talon.CodeNativeUnclassified, NativeCode: "busy", Cause: cause}
	fake.capability, fake.execError = nil, busy
	if _, err := conn.Begin(); err != busy || talon.NativeCodeOf(err) != "busy" || talon.ErrorCodeOf(err) == talon.CodeResultIndeterminate {
		t.Fatalf("BEGIN refusal changed classification: %v", err)
	}
	fake.execError = nil
	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	uncertain := &talon.TalonError{Code: talon.CodeResultIndeterminate, NativeCode: "uncertain", Cause: cause}
	fake.execError = uncertain
	if err := tx.Commit(); err != uncertain || !errors.Is(err, cause) || talon.NativeCodeOf(err) != "uncertain" {
		t.Fatalf("COMMIT lost uncertainty: %v", err)
	}
	protocol := &talon.TalonError{Code: talon.CodeProtocolViolation, Cause: cause}
	fake.execError = protocol
	err = tx.Commit()
	if talon.ErrorCodeOf(err) != talon.CodeResultIndeterminate || !errors.Is(err, protocol) || !errors.Is(err, cause) {
		t.Fatalf("malformed COMMIT response must retain cause and signal uncertainty: %v", err)
	}
	fake.execError = busy
	if err := tx.Commit(); err != busy {
		t.Fatalf("explicit Core refusal was reclassified: %v", err)
	}
	if len(fake.execCalls) != 5 {
		t.Fatalf("unexpected retry or cleanup call: %v", fake.execCalls)
	}
}

func TestNativeMutationResultCannotProveOutcome(t *testing.T) {
	for _, tc := range []struct {
		name       string
		result     talon.SQLResult
		queryError error
	}{
		{name: "missing metadata"},
		{name: "overflow metadata", result: func() talon.SQLResult { n := uint64(1) << 63; return talon.SQLResult{AffectedRows: &n} }()},
		{name: "malformed response", queryError: &talon.TalonError{Code: talon.CodeProtocolViolation}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeNative{result: tc.result, queryError: tc.queryError}
			conn := &nativeConn{db: fake}
			result, err := conn.ExecContext(context.Background(), "INSERT INTO items VALUES (?)", []driver.NamedValue{{Ordinal: 1, Value: 1}})
			if result != nil || talon.ErrorCodeOf(err) != talon.CodeResultIndeterminate || !errors.Is(err, &talon.TalonError{Code: talon.CodeProtocolViolation}) {
				t.Fatalf("mutation result=%v error=%v", result, err)
			}
			if len(fake.queryCalls) != 1 {
				t.Fatalf("mutation retried: %v", fake.queryCalls)
			}
		})
	}
}
