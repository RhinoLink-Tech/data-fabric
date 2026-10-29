package object_storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"vitess.io/vitess/go/vt/sqlparser"
)

var errMinIOParquetReadOnly = errors.New("minio-parquet supports read-only SELECT queries")

type minIOParquetConnector struct{ source *MinIOParquetDriver }

func (c *minIOParquetConnector) Connect(context.Context) (driver.Conn, error) {
	return &minIOParquetConn{source: c.source}, nil
}
func (c *minIOParquetConnector) Driver() driver.Driver {
	return minIOParquetDatabaseDriver{connector: c}
}

type minIOParquetDatabaseDriver struct{ connector *minIOParquetConnector }

func (d minIOParquetDatabaseDriver) Open(string) (driver.Conn, error) {
	return d.connector.Connect(context.Background())
}

type minIOParquetConn struct{ source *MinIOParquetDriver }

func (c *minIOParquetConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("minio-parquet does not support prepared statements")
}
func (c *minIOParquetConn) Close() error              { return nil }
func (c *minIOParquetConn) Begin() (driver.Tx, error) { return nil, errMinIOParquetReadOnly }
func (c *minIOParquetConn) Ping(ctx context.Context) error {
	if c.source.objectKey == "" {
		return errors.New("minio-parquet object_key is required")
	}
	if _, err := c.source.minioClient.GetObjectMetadata(ctx, c.source.objectKey); err != nil {
		return fmt.Errorf("failed to access configured Parquet object: %w", err)
	}
	return nil
}
func (c *minIOParquetConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	result, err := c.source.executeSQL(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return &minIOParquetRows{ctx: ctx, columns: result.columns, types: result.types, nullable: result.nullable, rows: result.rows}, nil
}
func (c *minIOParquetConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, errMinIOParquetReadOnly
}

type minIOParquetResult struct {
	columns  []string
	types    []string
	nullable []bool
	rows     [][]driver.Value
}

type minIOParquetRows struct {
	ctx      context.Context
	columns  []string
	types    []string
	nullable []bool
	rows     [][]driver.Value
	next     int
}

func (r *minIOParquetRows) Columns() []string { return r.columns }
func (r *minIOParquetRows) Close() error      { return nil }
func (r *minIOParquetRows) Next(dest []driver.Value) error {
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
func (r *minIOParquetRows) ColumnTypeDatabaseTypeName(index int) string {
	if index < 0 || index >= len(r.types) {
		return "VARCHAR"
	}
	return r.types[index]
}
func (r *minIOParquetRows) ColumnTypeNullable(index int) (nullable, ok bool) {
	if index < 0 || index >= len(r.nullable) {
		return true, false
	}
	return r.nullable[index], true
}

// OpenWithConfig builds the configured, read-only SQL adapter. The bucket is
// stored in database, the object key is read from additionalProps.object_key
// (with host as a legacy fallback), and endpoint_url must be an HTTP(S) URL.
func (d *MinIOParquetDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if config == nil {
		return nil, errors.New("minio-parquet data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" {
		return nil, errors.New("minio-parquet bucket is required in database")
	}
	objectKey := configString(config.AdditionalProps, "object_key")
	if objectKey == "" {
		objectKey = strings.TrimSpace(config.Host)
	}
	if objectKey == "" {
		return nil, errors.New("minio-parquet object_key is required in additionalProps or host")
	}
	endpoint, secure, err := minIOEndpointFromConfig(config)
	if err != nil {
		return nil, err
	}
	region := configString(config.AdditionalProps, "region")
	if region == "" {
		region = "us-east-1"
	}
	minioConfig := &MinIOConfig{
		Endpoint: endpoint, AccessKey: config.Username, SecretKey: config.Password,
		Region: region, Secure: secure, Token: configString(config.AdditionalProps, "session_token"),
		Bucket: bucket,
	}
	configured, err := NewMinIOParquetDriver(ctx, &MinIOParquetDriverConfig{MinIOConfig: minioConfig, BatchSize: configInt(config.AdditionalProps, "batch_size", 1000)})
	if err != nil {
		return nil, err
	}
	configured.objectKey = objectKey
	configured.tableName, err = MinIOParquetTableName(config)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(&minIOParquetConnector{source: configured}), nil
}

func minIOEndpointFromConfig(config *model.DataSourceConfig) (string, bool, error) {
	raw := configString(config.AdditionalProps, "endpoint_url")
	if raw == "" {
		return "", false, errors.New("minio-parquet endpoint_url is required in additionalProps")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || (parsed.Path != "" && parsed.Path != "/") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false, errors.New("minio-parquet endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return parsed.Host, parsed.Scheme == "https", nil
}

// MinIOParquetTableName returns the one SQL table identity exposed for a
// configured Parquet object.
func MinIOParquetTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("minio-parquet data source config is required")
	}
	name := configString(config.AdditionalProps, "table_name")
	if name == "" {
		key := configString(config.AdditionalProps, "object_key")
		if key == "" {
			key = strings.TrimSpace(config.Host)
		}
		if key == "" {
			return "", errors.New("minio-parquet object_key is required in additionalProps or host")
		}
		name = strings.TrimSuffix(path.Base(key), path.Ext(key))
	}
	if name == "" || name == "." {
		return "", errors.New("minio-parquet table name could not be inferred; set additionalProps.table_name")
	}
	return name, nil
}

func (d *MinIOParquetDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	if batchSize <= 0 {
		return "", errors.New("batch size must be positive")
	}
	query = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";"))
	if query == "" {
		return "", errors.New("query is required")
	}
	return fmt.Sprintf("SELECT * FROM (%s) AS __data_fabric_page LIMIT %d OFFSET %d", query, batchSize, maxInt64(offset, 0)), nil
}

func (d *MinIOParquetDriver) executeSQL(ctx context.Context, query string, args []driver.NamedValue) (*minIOParquetResult, error) {
	stmt, err := sqlparser.NewTestParser().Parse(strings.TrimSpace(query))
	if err != nil {
		return nil, fmt.Errorf("minio-parquet SQL parse failed: %w", err)
	}
	selectStmt, ok := stmt.(*sqlparser.Select)
	if !ok {
		return nil, errors.New("minio-parquet accepts SELECT statements only")
	}
	argIndex := new(int)
	result, err := d.evaluateSelect(ctx, selectStmt, args, argIndex)
	if err != nil {
		return nil, err
	}
	if *argIndex != len(args) {
		return nil, fmt.Errorf("minio-parquet received %d unused SQL arguments", len(args)-*argIndex)
	}
	return result, nil
}

func (d *MinIOParquetDriver) evaluateSelect(ctx context.Context, stmt *sqlparser.Select, args []driver.NamedValue, argIndex *int) (*minIOParquetResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if stmt == nil || stmt.With != nil || stmt.Distinct || stmt.GroupBy != nil || stmt.Having != nil || stmt.Lock != sqlparser.NoLock || len(stmt.From) != 1 {
		return nil, errors.New("minio-parquet supports one-object SELECT with WHERE, ORDER BY, LIMIT and OFFSET only")
	}
	if err := validateMinIOParquetColumnQualifiers(stmt); err != nil {
		return nil, err
	}
	from, ok := stmt.From[0].(*sqlparser.AliasedTableExpr)
	if !ok || len(from.Partitions) > 0 || len(from.Hints) > 0 {
		return nil, errors.New("minio-parquet supports one configured table or a derived SELECT")
	}
	var rows []map[string]interface{}
	var schema *ParquetSchema
	switch table := from.Expr.(type) {
	case sqlparser.TableName:
		if from.As.String() != "" {
			return nil, errors.New("minio-parquet table aliases are not supported")
		}
		if table.Qualifier.String() != "" || table.Name.String() != d.tableName {
			return nil, fmt.Errorf("minio-parquet table %q does not match configured table %q", sqlparser.String(table), d.tableName)
		}
		// Parse the plan first so LIMIT 0 can expose schema metadata without
		// reading any row groups from the configured object.
		plan, err := buildS3CSVPlan(stmt, args, argIndex)
		if err != nil {
			return nil, err
		}
		if plan.hasLimit && plan.limit == 0 {
			schema, err = d.GetSchema(ctx, d.objectKey)
			if err != nil {
				return nil, err
			}
			rows = []map[string]interface{}{}
		} else {
			queryResult, queryErr := d.Query(ctx, &MinIOParquetQuery{Key: d.objectKey})
			if queryErr != nil {
				return nil, queryErr
			}
			schema, rows = queryResult.Schema, queryResult.Rows
		}
		return d.finishSelect(ctx, plan, schema, rows)
	case *sqlparser.DerivedTable:
		inner, ok := table.Select.(*sqlparser.Select)
		if !ok {
			return nil, errors.New("minio-parquet derived tables must contain a SELECT")
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
		return nil, errors.New("minio-parquet supports a configured table or derived SELECT only")
	}
}

func validateMinIOParquetColumnQualifiers(stmt sqlparser.SQLNode) error {
	return sqlparser.Walk(func(node sqlparser.SQLNode) (bool, error) {
		column, ok := node.(*sqlparser.ColName)
		if ok && (column.Qualifier.Name.String() != "" || column.Qualifier.Qualifier.String() != "") {
			return false, errors.New("minio-parquet column qualifiers are not supported")
		}
		return true, nil
	}, stmt)
}

func parquetSchemaFromSQLResult(result *minIOParquetResult) *ParquetSchema {
	schema := &ParquetSchema{Columns: make([]ParquetColumn, len(result.columns))}
	for i, name := range result.columns {
		schema.Columns[i] = ParquetColumn{Name: name, StandardType: strings.ToLower(strings.TrimSpace(result.types[i]))}
	}
	return schema
}

func (d *MinIOParquetDriver) finishSelect(ctx context.Context, plan *s3CSVSelectPlan, schema *ParquetSchema, rows []map[string]interface{}, sourceColumnOverride ...[]string) (*minIOParquetResult, error) {
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
		result := &minIOParquetResult{columns: []string{name}, types: []string{"BIGINT"}, nullable: []bool{false}}
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
	result := &minIOParquetResult{columns: columns, types: types, nullable: nullable, rows: make([][]driver.Value, 0, len(filtered))}
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
