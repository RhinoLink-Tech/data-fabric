package object_storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"vitess.io/vitess/go/vt/sqlparser"
)

type objectSQLAdapter struct {
	driverName string
	objectKey  string
	tableName  string
	ping       func(context.Context, string) error
	getSchema  func(context.Context, string) (*objectSQLSchema, error)
	readRows   func(context.Context, string) (*objectSQLSchema, []map[string]interface{}, error)
}

func newObjectSQLDB(adapter *objectSQLAdapter) *sql.DB {
	return sql.OpenDB(&objectSQLConnector{adapter: adapter})
}

type objectSQLConnector struct{ adapter *objectSQLAdapter }

func (c *objectSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &objectSQLConn{adapter: c.adapter}, nil
}
func (c *objectSQLConnector) Driver() driver.Driver {
	return objectSQLDatabaseDriver{connector: c}
}

type objectSQLDatabaseDriver struct{ connector *objectSQLConnector }

func (d objectSQLDatabaseDriver) Open(string) (driver.Conn, error) {
	return d.connector.Connect(context.Background())
}

type objectSQLConn struct{ adapter *objectSQLAdapter }

func (c *objectSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("%s does not support prepared statements", c.adapter.driverName)
}
func (c *objectSQLConn) Close() error { return nil }
func (c *objectSQLConn) Begin() (driver.Tx, error) {
	return nil, fmt.Errorf("%s supports read-only SELECT queries", c.adapter.driverName)
}
func (c *objectSQLConn) Ping(ctx context.Context) error {
	if c.adapter.objectKey == "" {
		return fmt.Errorf("%s object_key is required", c.adapter.driverName)
	}
	if c.adapter.ping == nil {
		return fmt.Errorf("%s object access is not configured", c.adapter.driverName)
	}
	if err := c.adapter.ping(ctx, c.adapter.objectKey); err != nil {
		return fmt.Errorf("failed to access configured Parquet object: %w", err)
	}
	return nil
}
func (c *objectSQLConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	result, err := c.adapter.executeSQL(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return &objectSQLRows{ctx: ctx, columns: result.columns, types: result.types, nullable: result.nullable, rows: result.rows}, nil
}
func (c *objectSQLConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, fmt.Errorf("%s supports read-only SELECT queries", c.adapter.driverName)
}

type objectSQLResult struct {
	columns  []string
	types    []string
	nullable []bool
	rows     [][]driver.Value
}

type objectSQLRows struct {
	ctx      context.Context
	columns  []string
	types    []string
	nullable []bool
	rows     [][]driver.Value
	next     int
}

func (r *objectSQLRows) Columns() []string { return r.columns }
func (r *objectSQLRows) Close() error      { return nil }
func (r *objectSQLRows) Next(dest []driver.Value) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if r.next >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.next])
	r.next++
	return nil
}
func (r *objectSQLRows) ColumnTypeDatabaseTypeName(index int) string {
	if index < 0 || index >= len(r.types) {
		return "VARCHAR"
	}
	return r.types[index]
}
func (r *objectSQLRows) ColumnTypeNullable(index int) (nullable, ok bool) {
	if index < 0 || index >= len(r.nullable) {
		return true, false
	}
	return r.nullable[index], true
}

func applyObjectBatchPagination(query string, batchSize, offset int64) (string, error) {
	if batchSize <= 0 {
		return "", errors.New("batch size must be positive")
	}
	query = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";"))
	if query == "" {
		return "", errors.New("query is required")
	}
	return fmt.Sprintf("SELECT * FROM (%s) AS __data_fabric_page LIMIT %d OFFSET %d", query, batchSize, maxInt64(offset, 0)), nil
}

func (d *objectSQLAdapter) executeSQL(ctx context.Context, query string, args []driver.NamedValue) (*objectSQLResult, error) {
	stmt, err := sqlparser.NewTestParser().Parse(strings.TrimSpace(query))
	if err != nil {
		return nil, fmt.Errorf("%s SQL parse failed: %w", d.driverName, err)
	}
	selectStmt, ok := stmt.(*sqlparser.Select)
	if !ok {
		return nil, fmt.Errorf("%s accepts SELECT statements only", d.driverName)
	}
	argIndex := new(int)
	result, err := d.evaluateSelect(ctx, selectStmt, args, argIndex)
	if err != nil {
		return nil, err
	}
	if *argIndex != len(args) {
		return nil, fmt.Errorf("%s received %d unused SQL arguments", d.driverName, len(args)-*argIndex)
	}
	return result, nil
}

func (d *objectSQLAdapter) evaluateSelect(ctx context.Context, stmt *sqlparser.Select, args []driver.NamedValue, argIndex *int) (*objectSQLResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if stmt == nil || stmt.With != nil || stmt.Distinct || stmt.GroupBy != nil || stmt.Having != nil || stmt.Lock != sqlparser.NoLock || len(stmt.From) != 1 {
		return nil, fmt.Errorf("%s supports one-object SELECT with WHERE, ORDER BY, LIMIT and OFFSET only", d.driverName)
	}
	if err := validateObjectSQLColumnQualifiers(stmt, d.driverName); err != nil {
		return nil, err
	}
	from, ok := stmt.From[0].(*sqlparser.AliasedTableExpr)
	if !ok || len(from.Partitions) > 0 || len(from.Hints) > 0 {
		return nil, fmt.Errorf("%s supports one configured table or a derived SELECT", d.driverName)
	}
	var rows []map[string]interface{}
	var schema *objectSQLSchema
	switch table := from.Expr.(type) {
	case sqlparser.TableName:
		if from.As.String() != "" {
			return nil, fmt.Errorf("%s table aliases are not supported", d.driverName)
		}
		if table.Qualifier.String() != "" || table.Name.String() != d.tableName {
			return nil, fmt.Errorf("%s table %q does not match configured table %q", d.driverName, sqlparser.String(table), d.tableName)
		}
		// Parse the plan first so LIMIT 0 can expose schema metadata without
		// reading any row groups from the configured object.
		plan, err := buildS3CSVPlan(stmt, args, argIndex)
		if err != nil {
			return nil, err
		}
		if plan.hasLimit && plan.limit == 0 {
			if d.getSchema == nil {
				return nil, fmt.Errorf("%s schema discovery is not configured", d.driverName)
			}
			schema, err = d.getSchema(ctx, d.objectKey)
			if err != nil {
				return nil, err
			}
			rows = []map[string]interface{}{}
		} else {
			if d.readRows == nil {
				return nil, fmt.Errorf("%s object reader is not configured", d.driverName)
			}
			schema, rows, err = d.readRows(ctx, d.objectKey)
			if err != nil {
				return nil, err
			}
		}
		return d.finishSelect(ctx, plan, schema, rows)
	case *sqlparser.DerivedTable:
		inner, ok := table.Select.(*sqlparser.Select)
		if !ok {
			return nil, fmt.Errorf("%s derived tables must contain a SELECT", d.driverName)
		}
		innerResult, err := d.evaluateSelect(ctx, inner, args, argIndex)
		if err != nil {
			return nil, err
		}
		rows = make([]map[string]interface{}, len(innerResult.rows))
		for i, values := range innerResult.rows {
			rows[i] = make(map[string]interface{}, len(innerResult.columns))
			for columnIndex, column := range innerResult.columns {
				rows[i][column] = values[columnIndex]
			}
		}
		columns := append([]string(nil), innerResult.columns...)
		plan, err := buildS3CSVPlan(stmt, args, argIndex)
		if err != nil {
			return nil, err
		}
		return d.finishSelect(ctx, plan, objectSQLSchemaFromResult(innerResult), rows, columns)
	default:
		return nil, fmt.Errorf("%s supports a configured table or derived SELECT only", d.driverName)
	}
}

func validateObjectSQLColumnQualifiers(stmt sqlparser.SQLNode, driverName string) error {
	return sqlparser.Walk(func(node sqlparser.SQLNode) (bool, error) {
		column, ok := node.(*sqlparser.ColName)
		if ok && (column.Qualifier.Name.String() != "" || column.Qualifier.Qualifier.String() != "") {
			return false, fmt.Errorf("%s column qualifiers are not supported", driverName)
		}
		return true, nil
	}, stmt)
}

func objectSQLSchemaFromResult(result *objectSQLResult) *objectSQLSchema {
	if result == nil {
		return nil
	}
	schema := &objectSQLSchema{Columns: make([]objectSQLColumn, len(result.columns))}
	for i, name := range result.columns {
		nullable := true
		if i < len(result.nullable) {
			nullable = result.nullable[i]
		}
		schema.Columns[i] = objectSQLColumn{Name: name, StandardType: strings.ToLower(strings.TrimSpace(result.types[i])), Nullable: nullable}
	}
	return schema
}

type objectSQLSchema struct {
	Columns []objectSQLColumn
}

type objectSQLColumn struct {
	Name         string
	StandardType string
	Nullable     bool
}

func objectSQLSchemaFromParquet(schema *ParquetSchema) *objectSQLSchema {
	if schema == nil {
		return nil
	}
	result := &objectSQLSchema{Columns: make([]objectSQLColumn, len(schema.Columns))}
	for i, column := range schema.Columns {
		result.Columns[i] = objectSQLColumn{
			Name: column.Name, StandardType: column.StandardType,
			Nullable: column.RepetitionType != "REQUIRED",
		}
	}
	return result
}

func (d *objectSQLAdapter) finishSelect(ctx context.Context, plan *s3CSVSelectPlan, schema *objectSQLSchema, rows []map[string]interface{}, sourceColumnOverride ...[]string) (*objectSQLResult, error) {
	if schema == nil {
		return nil, fmt.Errorf("%s returned an empty object schema", d.driverName)
	}
	sourceColumns := make([]string, 0, len(schema.Columns))
	typesByName := make(map[string]string, len(schema.Columns))
	nullableByName := make(map[string]bool, len(schema.Columns))
	for _, column := range schema.Columns {
		sourceColumns = append(sourceColumns, column.Name)
		typesByName[column.Name] = objectSQLType(column.StandardType)
		nullableByName[column.Name] = column.Nullable
	}
	if len(sourceColumnOverride) > 0 {
		sourceColumns = sourceColumnOverride[0]
		for _, column := range sourceColumns {
			if typesByName[column] == "" {
				typesByName[column] = "VARCHAR"
				nullableByName[column] = true
			}
		}
	}
	if err := validateCSVPlanColumns(sourceColumns, plan); err != nil {
		return nil, err
	}
	filtered, err := applyCSVConditions(ctx, rows, plan.conditions)
	if err != nil {
		return nil, err
	}
	filtered, err = applyCSVOrder(ctx, filtered, plan.orderBy)
	if err != nil {
		return nil, err
	}
	if plan.count {
		name := plan.countAlias
		if name == "" {
			name = "count(*)"
		}
		countRows := applyCSVPage([]map[string]interface{}{{name: int64(len(filtered))}}, plan.offset, plan.limit, plan.hasLimit)
		result := &objectSQLResult{columns: []string{name}, types: []string{"BIGINT"}, nullable: []bool{false}}
		if len(countRows) > 0 {
			result.rows = [][]driver.Value{{int64(len(filtered))}}
		}
		return result, nil
	}
	projections, err := resolveCSVProjections(sourceColumns, plan)
	if err != nil {
		return nil, err
	}
	columns := make([]string, len(projections))
	types := make([]string, len(projections))
	nullable := make([]bool, len(projections))
	for i, projection := range projections {
		columns[i] = projection.alias
		types[i] = typesByName[projection.name]
		nullable[i] = nullableByName[projection.name]
	}
	filtered = applyCSVPage(filtered, plan.offset, plan.limit, plan.hasLimit)
	result := &objectSQLResult{columns: columns, types: types, nullable: nullable, rows: make([][]driver.Value, 0, len(filtered))}
	for _, row := range filtered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		values := make([]driver.Value, len(projections))
		for i, projection := range projections {
			values[i] = objectSQLDriverValue(csvMapValue(row, projection.name))
		}
		result.rows = append(result.rows, values)
	}
	return result, nil
}

func objectSQLType(standardType string) string {
	switch strings.ToLower(strings.TrimSpace(standardType)) {
	case "boolean":
		return "BOOLEAN"
	case "integer", "int", "smallint", "tinyint":
		return "INTEGER"
	case "bigint":
		return "BIGINT"
	case "float":
		return "REAL"
	case "double":
		return "DOUBLE"
	case "decimal":
		return "DECIMAL"
	case "date":
		return "DATE"
	case "time":
		return "TIME"
	case "timestamp":
		return "TIMESTAMP"
	case "json":
		return "JSON"
	case "uuid":
		return "UUID"
	case "binary":
		return "VARBINARY"
	default:
		return "VARCHAR"
	}
}

func objectSQLDriverValue(value interface{}) driver.Value {
	switch v := value.(type) {
	case nil, int64, float64, bool, string, []byte, time.Time:
		return v
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int:
		return int64(v)
	case uint8:
		return int64(v)
	case uint16:
		return int64(v)
	case uint32:
		return int64(v)
	case uint64:
		if v <= uint64(^uint64(0)>>1) {
			return int64(v)
		}
		return fmt.Sprint(v)
	case float32:
		return float64(v)
	default:
		return fmt.Sprint(v)
	}
}
