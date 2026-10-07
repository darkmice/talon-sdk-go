package goframe

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strings"
	"time"

	talon "github.com/darkmice/talon-sdk-go"
)

type nativeConnector struct{ path string }

func (c nativeConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db, err := talon.Open(c.path)
	if err != nil {
		return nil, err
	}
	return &nativeConn{db: db}, nil
}
func (c nativeConnector) Driver() driver.Driver { return nativeDriver{} }

type nativeDriver struct{}

func (nativeDriver) Open(path string) (driver.Conn, error) {
	db, err := talon.Open(path)
	if err != nil {
		return nil, err
	}
	return &nativeConn{db: db}, nil
}

type nativeQueryDB interface {
	QueryResult(string, ...talon.Value) (talon.SQLResult, error)
	Exec(string, ...talon.Value) error
	RequireCapability(string) error
	Close()
}

type nativeConn struct{ db nativeQueryDB }

func (c *nativeConn) Close() error { c.db.Close(); return nil }
func (c *nativeConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *nativeConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if opts.ReadOnly || opts.Isolation != driver.IsolationLevel(0) {
		return nil, errors.New("talon: requested transaction options are unsupported")
	}
	if err := c.db.RequireCapability("native_sql_session"); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTransactionsUnavailable, err)
	}
	if err := c.db.Exec("BEGIN"); err != nil {
		return nil, err
	}
	return &nativeTx{db: c.db}, nil
}

type nativeTx struct{ db nativeQueryDB }

func (t *nativeTx) Commit() error {
	return mutationOutcomeError("COMMIT", t.db.Exec("COMMIT"))
}
func (t *nativeTx) Rollback() error { return t.db.Exec("ROLLBACK") }

// A malformed response after dispatch cannot prove the write outcome. Preserve
// the protocol cause and native codes, but expose uncertainty to retry callers.
// Core rejections (including busy at BEGIN) keep their original classification.
func mutationOutcomeError(operation string, err error) error {
	if talon.ErrorCodeOf(err) == talon.CodeProtocolViolation {
		return &talon.TalonError{Code: talon.CodeResultIndeterminate, NativeCode: talon.NativeCodeOf(err),
			Operation: operation, Message: "native SQL response could not prove the mutation outcome", Cause: err}
	}
	return err
}

func invalidMutationResult(operation, message string) error {
	return mutationOutcomeError(operation, &talon.TalonError{
		Code: talon.CodeProtocolViolation, Operation: operation, Message: message,
	})
}
func (c *nativeConn) Prepare(query string) (driver.Stmt, error) {
	return &nativeStmt{conn: c, query: query}, nil
}
func (c *nativeConn) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := c.db.QueryResult("SHOW TABLES")
	return err
}
func (c *nativeConn) CheckNamedValue(value *driver.NamedValue) error {
	if value.Name != "" {
		return errors.New("talon: named SQL parameters are unsupported")
	}
	return nil // convertParam validates the closed Talon Value set before dispatch.
}

func (c *nativeConn) QueryContext(ctx context.Context, statement string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	params, err := convertParams(args)
	if err != nil {
		return nil, err
	}
	resultSet, err := c.db.QueryResult(statement, params...)
	if err != nil {
		return nil, err
	}
	result := &nativeRows{columns: resultSet.Columns, values: resultSet.Rows}
	for _, row := range resultSet.Rows {
		if len(row) != len(resultSet.Columns) {
			return nil, fmt.Errorf("talon: native row width %d does not match %d columns", len(row), len(resultSet.Columns))
		}
	}
	return result, nil
}

func (c *nativeConn) ExecContext(ctx context.Context, statement string, args []driver.NamedValue) (driver.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	upper := strings.ToUpper(strings.TrimSpace(statement))
	for _, prefix := range []string{"SELECT ", "SHOW ", "DESCRIBE ", "EXPLAIN ", "WITH ", "BEGIN", "COMMIT", "ROLLBACK"} {
		if strings.HasPrefix(upper, prefix) {
			return nil, errors.New("talon: query or transaction SQL cannot be sent through Exec")
		}
	}
	if strings.Contains(upper, "RETURNING") {
		return nil, errors.New("talon: use Query for SQL with RETURNING")
	}
	params, err := convertParams(args)
	if err != nil {
		return nil, err
	}
	mutationSQL := normalizeMutationSQL(statement)
	if mutation, ok := mutationKind(mutationSQL); ok {
		resultSet, err := c.db.QueryResult(mutationSQL, params...)
		if err != nil {
			return nil, mutationOutcomeError(mutation, err)
		}
		if resultSet.AffectedRows == nil {
			return nil, invalidMutationResult(mutation, "Core omitted affected rows")
		}
		if *resultSet.AffectedRows > math.MaxInt64 {
			return nil, invalidMutationResult(mutation, "affected row count exceeds database/sql range")
		}
		result := nativeResult{affected: int64(*resultSet.AffectedRows)}
		if resultSet.LastInsertID != nil {
			result.lastInsertID = *resultSet.LastInsertID
			result.hasInsertID = true
		}
		return result, nil
	}
	if len(strings.Fields(statement)) == 0 {
		return nil, errors.New("talon: empty SQL")
	}
	verb := strings.ToUpper(strings.Fields(statement)[0])
	if verb != "CREATE" && verb != "ALTER" && verb != "DROP" && verb != "TRUNCATE" {
		return nil, errors.New("talon: unsupported Exec statement shape")
	}
	trimmed := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(statement), ";"))
	if strings.Contains(trimmed, ";") {
		return nil, errors.New("talon: Exec accepts one statement at a time")
	}
	if _, err := c.db.QueryResult(statement, params...); err != nil {
		return nil, mutationOutcomeError(verb, err)
	}
	return nativeResult{}, nil
}

func normalizeMutationSQL(statement string) string {
	trimmed := strings.TrimSpace(statement)
	upper := strings.ToUpper(trimmed)
	if strings.HasPrefix(upper, "INSERT IGNORE INTO ") {
		return "INSERT OR IGNORE INTO " + trimmed[len("INSERT IGNORE INTO "):]
	}
	if strings.HasPrefix(upper, "REPLACE INTO ") {
		return "INSERT OR REPLACE INTO " + trimmed[len("REPLACE INTO "):]
	}
	return trimmed
}

// mutationKind rejects multi-statement and result-bearing Exec calls before
// dispatch. Talon Core parses the accepted statement and owns write effects.
func mutationKind(statement string) (string, bool) {
	sql := strings.TrimSpace(statement)
	sql = strings.TrimSpace(strings.TrimSuffix(sql, ";"))
	if strings.Contains(sql, ";") || strings.Contains(strings.ToUpper(sql), "RETURNING") {
		return "", false
	}
	fields := strings.Fields(sql)
	if len(fields) < 3 {
		return "", false
	}
	upper := strings.ToUpper(sql)
	switch strings.ToUpper(fields[0]) {
	case "INSERT":
		if strings.Contains(upper, " ON DUPLICATE ") {
			return "", false
		}
		return "INSERT", true
	case "UPDATE":
		return "UPDATE", true
	case "DELETE":
		return "DELETE", true
	default:
		return "", false
	}
}

type nativeResult struct {
	affected     int64
	lastInsertID int64
	hasInsertID  bool
}

func (r nativeResult) RowsAffected() (int64, error) { return r.affected, nil }
func (r nativeResult) LastInsertId() (int64, error) {
	if !r.hasInsertID {
		return 0, ErrResultMetadataUnavailable
	}
	return r.lastInsertID, nil
}

type nativeStmt struct {
	conn  *nativeConn
	query string
}

func (*nativeStmt) Close() error  { return nil }
func (*nativeStmt) NumInput() int { return -1 }
func (s *nativeStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), positionalArgs(args))
}
func (s *nativeStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), positionalArgs(args))
}
func (s *nativeStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return s.conn.ExecContext(ctx, s.query, args)
}
func (s *nativeStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return s.conn.QueryContext(ctx, s.query, args)
}
func positionalArgs(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, value := range args {
		out[i] = driver.NamedValue{Ordinal: i + 1, Value: value}
	}
	return out
}

func convertParams(args []driver.NamedValue) ([]talon.Value, error) {
	params := make([]talon.Value, len(args))
	for i, arg := range args {
		if arg.Name != "" {
			return nil, errors.New("talon: named SQL parameters are unsupported")
		}
		value, err := convertParam(arg.Value)
		if err != nil {
			return nil, fmt.Errorf("talon: parameter %d: %w", i+1, err)
		}
		params[i] = value
	}
	return params, nil
}
func convertParam(input interface{}) (talon.Value, error) {
	switch value := input.(type) {
	case talon.Value:
		return value, nil
	case nil:
		return talon.NullValue(), nil
	case int64:
		return talon.IntegerValue(value), nil
	case int:
		return talon.IntegerValue(int64(value)), nil
	case bool:
		return talon.BooleanValue(value), nil
	case float64:
		return talon.FloatValue(value)
	case string:
		return talon.TextValue(value)
	case []byte:
		return talon.BlobValue(value), nil
	case time.Time:
		return talon.TimestampValue(value.UnixMilli()), nil
	case driver.Valuer:
		converted, err := value.Value()
		if err != nil {
			return talon.Value{}, err
		}
		return convertParam(converted)
	default:
		return talon.Value{}, fmt.Errorf("Go type %T has no exact Talon SQL mapping", input)
	}
}

type nativeRows struct {
	columns []string
	values  []talon.Row
	next    int
}

func (r *nativeRows) Columns() []string { return append([]string(nil), r.columns...) }
func (r *nativeRows) Close() error      { r.values = nil; return nil }
func (r *nativeRows) Next(dest []driver.Value) error {
	if r.next >= len(r.values) {
		return io.EOF
	}
	for column, value := range r.values[r.next] {
		converted, err := sqlValue(value)
		if err != nil {
			return fmt.Errorf("talon: row %d column %d: %w", r.next, column, err)
		}
		dest[column] = converted
	}
	r.next++
	return nil
}

func sqlValue(value talon.Value) (driver.Value, error) {
	switch value.Kind() {
	case talon.KindNull:
		return nil, nil
	case talon.KindInteger:
		n, _ := value.Integer()
		return n, nil
	case talon.KindFloat:
		n, _ := value.Float64()
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return nil, errors.New("non-finite float")
		}
		return n, nil
	case talon.KindText:
		s, _ := value.String()
		return s, nil
	case talon.KindBlob:
		b, _ := value.Blob()
		return b, nil
	case talon.KindBoolean:
		b, _ := value.Boolean()
		return b, nil
	case talon.KindJSON:
		b, _ := value.JSON()
		return []byte(b), nil
	case talon.KindTimestamp:
		n, _ := value.Timestamp()
		return n, nil
	case talon.KindDate:
		n, _ := value.Date()
		return int64(n), nil
	case talon.KindTime:
		n, _ := value.Time()
		return n, nil
	case talon.KindDecimal:
		coefficient, scale, _ := value.Decimal()
		return decimalText(coefficient, scale), nil
	case talon.KindVector:
		vector, _ := value.Vector()
		b, err := json.Marshal(vector)
		return string(b), err
	case talon.KindGeoPoint:
		point, _ := value.GeoPoint()
		b, err := json.Marshal(point)
		return string(b), err
	default:
		return nil, fmt.Errorf("unsupported native value kind %d", value.Kind())
	}
}

func decimalText(coefficient *big.Int, scale uint8) string {
	negative := coefficient.Sign() < 0
	digits := new(big.Int).Abs(coefficient).String()
	if int(scale) >= len(digits) {
		digits = strings.Repeat("0", int(scale)+1-len(digits)) + digits
	}
	if scale > 0 {
		pos := len(digits) - int(scale)
		digits = digits[:pos] + "." + digits[pos:]
	}
	if negative {
		return "-" + digits
	}
	return digits
}
