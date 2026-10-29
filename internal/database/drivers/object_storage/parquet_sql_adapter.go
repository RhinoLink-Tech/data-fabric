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

type parquetSQLAdapter struct {
	driverName string
	objectKey  string
	tableName  string
	ping       func(context.Context, string) error
	getSchema  func(context.Context, string) (*ParquetSchema, error)
	readRows   func(context.Context, string) (*ParquetSchema, []map[string]interface{}, error)
}

func newParquetSQLDB(adapter *parquetSQLAdapter) *sql.DB {
	return sql.OpenDB(&parquetSQLConnector{adapter: adapter})
}

type parquetSQLConnector struct{ adapter *parquetSQLAdapter }

func (c *parquetSQLConnector) Connect(context.Context) (driver.Conn, error) {
	return &parquetSQLConn{adapter: c.adapter}, nil
}
func (c *parquetSQLConnector) Driver() driver.Driver {
	return parquetSQLDatabaseDriver{connector: c}
}

type parquetSQLDatabaseDriver struct{ connector *parquetSQLConnector }

func (d parquetSQLDatabaseDriver) Open(string) (driver.Conn, error) {
	return d.connector.Connect(context.Background())
}

type parquetSQLConn struct{ adapter *parquetSQLAdapter }

func (c *parquetSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("%s does not support prepared statements", c.adapter.driverName)
}
func (c *parquetSQLConn) Close() error { return nil }
func (c *parquetSQLConn) Begin() (driver.Tx, error) {
	return nil, fmt.Errorf("%s supports read-only SELECT queries", c.adapter.driverName)
}
func (c *parquetSQLConn) Ping(ctx context.Context) error {
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
func (c *parquetSQLConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	result, err := c.adapter.executeSQL(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return &parquetSQLRows{ctx: ctx, columns: result.columns, types: result.types, nullable: result.nullable, rows: result.rows}, nil
}
func (c *parquetSQLConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, fmt.Errorf("%s supports read-only SELECT queries", c.adapter.driverName)
}

type parquetSQLResult struct {
	columns  []string
	types    []string
	nullable []bool
	rows     [][]driver.Value
}

type parquetSQLRows struct {
	ctx      context.Context
	columns  []string
	types    []string
	nullable []bool
	rows     [][]driver.Value
	next     int
}

func (r *parquetSQLRows) Columns() []string { return r.columns }
func (r *parquetSQLRows) Close() error      { return nil }
func (r *parquetSQLRows) Next(dest []driver.Value) error {
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
func (r *parquetSQLRows) ColumnTypeDatabaseTypeName(index int) string {
	if index < 0 || index >= len(r.types) {
		return "VARCHAR"
	}
	return r.types[index]
}
func (r *parquetSQLRows) ColumnTypeNullable(index int) (nullable, ok bool) {
	if index < 0 || index >= len(r.nullable) {
		return true, false
	}
	return r.nullable[index], true
}

func applyParquetBatchPagination(query string, batchSize, offset int64) (string, error) {
	if batchSize <= 0 {
		return "", errors.New("batch size must be positive")
	}
	query = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";"))
	if query == "" {
		return "", errors.New("query is required")
	}
	return fmt.Sprintf("SELECT * FROM (%s) AS __data_fabric_page LIMIT %d OFFSET %d", query, batchSize, maxInt64(offset, 0)), nil
}

func (d *parquetSQLAdapter) executeSQL(ctx context.Context, query string, args []driver.NamedValue) (*parquetSQLResult, error) {
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

func (d *parquetSQLAdapter) evaluateSelect(ctx context.Context, stmt *sqlparser.Select, args []driver.NamedValue, argIndex *int) (*parquetSQLResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if stmt == nil || stmt.With != nil || stmt.Distinct || stmt.GroupBy != nil || stmt.Having != nil || stmt.Lock != sqlparser.NoLock || len(stmt.From) != 1 {
		return nil, fmt.Errorf("%s supports one-object SELECT with WHERE, ORDER BY, LIMIT and OFFSET only", d.driverName)
	}
	if err := validateParquetSQLColumnQualifiers(stmt, d.driverName); err != nil {
		return nil, err
	}
	from, ok := stmt.From[0].(*sqlparser.AliasedTableExpr)
	if !ok || len(from.Partitions) > 0 || len(from.Hints) > 0 {
		return nil, fmt.Errorf("%s supports one configured table or a derived SELECT", d.driverName)
	}
	var rows []map[string]interface{}
	var schema *ParquetSchema
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
		return d.finishSelect(ctx, plan, parquetSchemaFromSQLResult(innerResult), rows, columns)
	default:
		return nil, fmt.Errorf("%s supports a configured table or derived SELECT only", d.driverName)
	}
}

func validateParquetSQLColumnQualifiers(stmt sqlparser.SQLNode, driverName string) error {
	return sqlparser.Walk(func(node sqlparser.SQLNode) (bool, error) {
		column, ok := node.(*sqlparser.ColName)
		if ok && (column.Qualifier.Name.String() != "" || column.Qualifier.Qualifier.String() != "") {
			return false, fmt.Errorf("%s column qualifiers are not supported", driverName)
		}
		return true, nil
	}, stmt)
}

func parquetSchemaFromSQLResult(result *parquetSQLResult) *ParquetSchema {
	schema := &ParquetSchema{Columns: make([]ParquetColumn, len(result.columns))}
	for i, name := range result.columns {
		schema.Columns[i] = ParquetColumn{Name: name, StandardType: strings.ToLower(strings.TrimSpace(result.types[i]))}
	}
	return schema
}

func (d *parquetSQLAdapter) finishSelect(ctx context.Context, plan *s3CSVSelectPlan, schema *ParquetSchema, rows []map[string]interface{}, sourceColumnOverride ...[]string) (*parquetSQLResult, error) {
	if schema == nil {
		return nil, fmt.Errorf("%s returned an empty Parquet schema", d.driverName)
	}
	sourceColumns := make([]string, 0, len(schema.Columns))
	typesByName := make(map[string]string, len(schema.Columns))
	nullableByName := make(map[string]bool, len(schema.Columns))
	for _, column := range schema.Columns {
		sourceColumns = append(sourceColumns, column.Name)
		typesByName[column.Name] = parquetSQLType(column.StandardType)
		nullableByName[column.Name] = column.RepetitionType != "REQUIRED"
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
		result := &parquetSQLResult{columns: []string{name}, types: []string{"BIGINT"}, nullable: []bool{false}}
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
	result := &parquetSQLResult{columns: columns, types: types, nullable: nullable, rows: make([][]driver.Value, 0, len(filtered))}
	for _, row := range filtered {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		values := make([]driver.Value, len(projections))
		for i, projection := range projections {
			values[i] = parquetDriverValue(csvMapValue(row, projection.name))
		}
		result.rows = append(result.rows, values)
	}
	return result, nil
}

func parquetSQLType(standardType string) string {
	switch strings.ToLower(strings.TrimSpace(standardType)) {
	case "boolean":
		return "BOOLEAN"
	case "integer":
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

func parquetDriverValue(value interface{}) driver.Value {
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
