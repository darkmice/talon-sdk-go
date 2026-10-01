// Package goframe registers a GoFrame v2 gdb driver backed by Talon's
// embedded, signed native SDK. It does not use Talon Server or PgWire.
package goframe

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/gogf/gf/v2/database/gdb"
)

const DriverName = "talon"

var (
	identifier                   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	ErrResultMetadataUnavailable = errors.New("talon native SQL did not report a generated insert ID")
	ErrTransactionsUnavailable   = errors.New("loaded Talon Core lacks native SQL session capability")
)

type Driver struct{ *gdb.Core }

func init() {
	if err := gdb.Register(DriverName, &Driver{}); err != nil {
		panic(err)
	}
}

func (d *Driver) New(core *gdb.Core, _ *gdb.ConfigNode) (gdb.DB, error) {
	// GoFrame applies its own pool defaults after Driver.Open. Set the limit on
	// the Core as well, or its default (unlimited) silently overrides Open's cap.
	core.SetMaxOpenConnCount(1)
	core.SetMaxIdleConnCount(1)
	return &Driver{Core: core}, nil
}

func (d *Driver) GetChars() (string, string) { return "`", "`" }

// DoInsert supplies Talon's explicit conflict target for GoFrame Save.
func (d *Driver) DoInsert(ctx context.Context, link gdb.Link, table string, list gdb.List, option gdb.DoInsertOption) (sql.Result, error) {
	if option.InsertOption == gdb.InsertOptionSave && len(option.OnConflict) == 0 {
		fields, err := d.TableFields(ctx, table)
		if err != nil {
			return nil, err
		}
		for _, field := range fields {
			if field.Key == "PRI" {
				option.OnConflict = append(option.OnConflict, field.Name)
			}
		}
		sort.Slice(option.OnConflict, func(i, j int) bool { return fields[option.OnConflict[i]].Index < fields[option.OnConflict[j]].Index })
		if len(option.OnConflict) == 0 {
			return nil, errors.New("talon: Save requires a primary key or explicit OnConflict")
		}
	}
	return d.Core.DoInsert(ctx, link, table, list, option)
}

// FormatUpsert emits Talon's native ON CONFLICT syntax.
func (d *Driver) FormatUpsert(columns []string, _ gdb.List, option gdb.DoInsertOption) (string, error) {
	if len(option.OnConflict) == 0 {
		return "", errors.New("talon: Save requires a conflict target")
	}
	quoted := func(names []string) (string, error) {
		result := make([]string, len(names))
		for i, name := range names {
			if !identifier.MatchString(name) {
				return "", fmt.Errorf("talon: invalid column identifier %q", name)
			}
			result[i] = "`" + name + "`"
		}
		return strings.Join(result, ","), nil
	}
	target, err := quoted(option.OnConflict)
	if err != nil {
		return "", err
	}
	assignments := make([]string, 0, len(columns))
	if option.OnDuplicateStr != "" {
		assignments = append(assignments, option.OnDuplicateStr)
	} else if len(option.OnDuplicateMap) != 0 {
		keys := make([]string, 0, len(option.OnDuplicateMap))
		for key := range option.OnDuplicateMap {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, target := range keys {
			source := fmt.Sprint(option.OnDuplicateMap[target])
			if !identifier.MatchString(target) || !identifier.MatchString(source) {
				return "", errors.New("talon: OnDuplicate map requires column names; use Raw with Talon SQL for expressions")
			}
			assignments = append(assignments, "`"+target+"`=EXCLUDED.`"+source+"`")
		}
	} else {
		for _, column := range columns {
			if !identifier.MatchString(column) {
				return "", fmt.Errorf("talon: invalid column identifier %q", column)
			}
			conflictColumn := false
			for _, target := range option.OnConflict {
				conflictColumn = conflictColumn || strings.EqualFold(target, column)
			}
			if conflictColumn || d.IsSoftCreatedFieldName(column) {
				continue
			}
			assignments = append(assignments, "`"+column+"`=EXCLUDED.`"+column+"`")
		}
	}
	if len(assignments) == 0 {
		return "", errors.New("talon: Save has no updatable columns")
	}
	return "ON CONFLICT (" + target + ") DO UPDATE SET " + strings.Join(assignments, ","), nil
}

// Open uses ConfigNode.Name as an absolute local database path. Native trust
// policy is read and enforced by talon.Open, before the Core library is loaded.
func (d *Driver) Open(node *gdb.ConfigNode) (*sql.DB, error) {
	if node == nil || !filepath.IsAbs(node.Name) {
		return nil, errors.New("talon: ConfigNode.Name must be an absolute native database path")
	}
	if node.Host != "" || node.Port != "" || node.User != "" || node.Pass != "" || node.Link != "" || node.Extra != "" {
		return nil, errors.New("talon: native adapter accepts only Type and Name, not server connection fields")
	}
	db := sql.OpenDB(nativeConnector{path: node.Name})
	// Each sql.Conn owns one native handle and its SQL session. Keep pool use
	// bounded to one handle until native session concurrency is exercised E2E.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

// Tables and TableFields read Talon's native schema commands, not a server
// catalog. Native SQL has no named-schema selector.
func (d *Driver) Tables(ctx context.Context, schema ...string) ([]string, error) {
	if err := checkSchema(schema); err != nil {
		return nil, err
	}
	db, err := d.Master()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "SHOW TABLES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var table, kind string
		if err := rows.Scan(&table, &kind); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}

func (d *Driver) TableFields(ctx context.Context, table string, schema ...string) (map[string]*gdb.TableField, error) {
	if err := checkSchema(schema); err != nil {
		return nil, err
	}
	// GoFrame passes its own quoted table name to DoInsert when Model.Save
	// needs the primary key. Strip only one complete pair of driver quotes;
	// the identifier check below still rejects expressions and schemas.
	if strings.HasPrefix(table, "`") && strings.HasSuffix(table, "`") && len(table) >= 2 {
		table = table[1 : len(table)-1]
	}
	if !identifier.MatchString(table) {
		return nil, fmt.Errorf("talon: invalid table identifier %q", table)
	}
	db, err := d.Master()
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, "DESCRIBE `"+table+"`")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := make(map[string]*gdb.TableField)
	for rows.Next() {
		var name, kind, primary, nullable, foreign, comment string
		var defaultValue sql.NullString
		if err := rows.Scan(&name, &kind, &primary, &nullable, &defaultValue, &foreign, &comment); err != nil {
			return nil, err
		}
		if name == "" {
			return nil, errors.New("talon: DESCRIBE returned an empty column name")
		}
		key := ""
		if strings.EqualFold(primary, "YES") {
			key = "PRI"
		}
		field := &gdb.TableField{Index: len(fields), Name: name, Type: kind,
			Null: strings.EqualFold(nullable, "YES"), Key: key, Comment: comment}
		if defaultValue.Valid {
			field.Default = defaultValue.String
		}
		fields[name] = field
	}
	return fields, rows.Err()
}

func checkSchema(schema []string) error {
	for _, name := range schema {
		if name != "" {
			return errors.New("talon: named schemas are unsupported")
		}
	}
	return nil
}
