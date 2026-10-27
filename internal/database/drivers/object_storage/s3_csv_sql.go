package object_storage

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"vitess.io/vitess/go/vt/sqlparser"
)

var errS3CSVReadOnly = errors.New("s3-csv supports read-only SELECT queries")

type s3CSVConnector struct{ driver *S3CSVDriver }

func (c *s3CSVConnector) Connect(context.Context) (driver.Conn, error) {
	return &s3CSVConn{driver: c.driver}, nil
}
func (c *s3CSVConnector) Driver() driver.Driver { return s3CSVDatabaseDriver{connector: c} }

type s3CSVDatabaseDriver struct{ connector *s3CSVConnector }

func (d s3CSVDatabaseDriver) Open(string) (driver.Conn, error) {
	return d.connector.Connect(context.Background())
}

type s3CSVConn struct{ driver *S3CSVDriver }

func (c *s3CSVConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("s3-csv does not support prepared statements")
}
func (c *s3CSVConn) Close() error              { return nil }
func (c *s3CSVConn) Begin() (driver.Tx, error) { return nil, errS3CSVReadOnly }
func (c *s3CSVConn) Ping(ctx context.Context) error {
	if c.driver.objectKey == "" {
		return errors.New("s3-csv object_key is required")
	}
	_, err := c.driver.s3Client.GetObjectMetadata(ctx, c.driver.objectKey)
	if err != nil {
		return fmt.Errorf("failed to access configured CSV object: %w", err)
	}
	return nil
}
func (c *s3CSVConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	result, err := c.driver.executeSQL(ctx, query, args)
	if err != nil {
		return nil, err
	}
	return &s3CSVRows{ctx: ctx, columns: result.columns, types: result.types, rows: result.rows}, nil
}
func (c *s3CSVConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, errS3CSVReadOnly
}

type s3CSVResult struct {
	columns []string
	types   []string
	rows    [][]driver.Value
}
type s3CSVRows struct {
	ctx            context.Context
	columns, types []string
	rows           [][]driver.Value
	next           int
}

func (r *s3CSVRows) Columns() []string { return r.columns }
func (r *s3CSVRows) Close() error      { return nil }
func (r *s3CSVRows) Next(dest []driver.Value) error {
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
func (r *s3CSVRows) ColumnTypeDatabaseTypeName(index int) string {
	if index < 0 || index >= len(r.types) {
		return "VARCHAR"
	}
	return r.types[index]
}
func (r *s3CSVRows) ColumnTypeNullable(int) (nullable, ok bool) { return true, true }

// OpenWithConfig creates the configured read-only database/sql adapter. The bucket
// comes from Database, the object key from additionalProps.object_key (Host is a
// legacy fallback), and S3 credentials from Username and Password.
func (d *S3CSVDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if config == nil {
		return nil, errors.New("s3-csv data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return nil, errors.New("s3-csv bucket is required in database")
	}
	props := config.AdditionalProps
	objectKey := configString(props, "object_key")
	if objectKey == "" {
		objectKey = strings.TrimSpace(config.Host)
	}
	if objectKey == "" {
		return nil, errors.New("s3-csv object_key is required in additionalProps or host")
	}
	region := configString(props, "region")
	if region == "" {
		region = "us-east-1"
	}
	delimiter := configString(props, "delimiter")
	if delimiter == "" {
		delimiter = ","
	}
	delimiterRunes := []rune(delimiter)
	if len(delimiterRunes) != 1 {
		return nil, errors.New("s3-csv delimiter must be exactly one character")
	}
	quote := configString(props, "quote_char")
	if quote != "" && quote != "\"" {
		return nil, errors.New("s3-csv currently supports the standard double-quote CSV escape character")
	}
	clientConfig := &S3Config{
		Region: region, Bucket: strings.TrimSpace(config.Database), AccessKey: config.Username, SecretKey: config.Password,
		SessionToken: configString(props, "session_token"), RoleARN: configString(props, "role_arn"), ExternalID: configString(props, "external_id"),
		EndpointURL: configString(props, "endpoint_url"), DisableSSL: configBool(props, "disable_ssl", false),
		ForcePathStyle: configBool(props, "force_path_style", false),
		MaxRetries:     configInt(props, "max_retries", 0),
	}
	driverConfig := &S3CSVDriverConfig{
		S3Config: clientConfig, Delimiter: delimiterRunes[0], HasHeader: configBool(props, "has_header", true),
		QuoteChar: '"', SkipHeaderRows: configInt(props, "skip_header_rows", 0), NullValues: configStringSlice(props, "null_values"),
	}
	if driverConfig.SkipHeaderRows < 0 {
		return nil, errors.New("s3-csv skip_header_rows cannot be negative")
	}
	configuredDriver, err := NewS3CSVDriver(ctx, driverConfig)
	if err != nil {
		return nil, err
	}
	configuredDriver.objectKey = objectKey
	configuredDriver.tableName, err = CSVTableName(config)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(&s3CSVConnector{driver: configuredDriver}), nil
}

// CSVTableName returns the single table identity used by the SQL adapter.
// Metadata discovery uses the same rules as connection setup so names cannot drift.
func CSVTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("s3-csv data source config is required")
	}
	name := configString(config.AdditionalProps, "table_name")
	if name == "" {
		key := configString(config.AdditionalProps, "object_key")
		if key == "" {
			key = strings.TrimSpace(config.Host)
		}
		if key == "" {
			return "", errors.New("s3-csv object_key is required in additionalProps or host")
		}
		name = strings.TrimSuffix(path.Base(key), path.Ext(key))
	}
	if name == "" || name == "." {
		return "", errors.New("s3-csv table name could not be inferred; set additionalProps.table_name")
	}
	return name, nil
}

func configString(props map[string]interface{}, key string) string {
	value, ok := props[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
func configBool(props map[string]interface{}, key string, fallback bool) bool {
	value, ok := props[key]
	if !ok || value == nil {
		return fallback
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		if err == nil {
			return parsed
		}
	case float64:
		return v != 0
	case int:
		return v != 0
	}
	return fallback
}
func configInt(props map[string]interface{}, key string, fallback int) int {
	value, ok := props[key]
	if !ok || value == nil {
		return fallback
	}
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return parsed
		}
	}
	return fallback
}
func configStringSlice(props map[string]interface{}, key string) []string {
	value, ok := props[key]
	if !ok || value == nil {
		return nil
	}
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			result = append(result, fmt.Sprint(item))
		}
		return result
	case string:
		if strings.TrimSpace(v) == "" {
			return nil
		}
		return strings.Split(v, ",")
	}
	return nil
}

type s3CSVProjection struct{ name, alias string }
type s3CSVCondition struct {
	column string
	op     sqlparser.ComparisonExprOperator
	value  interface{}
	values []interface{}
	isNull int
}
type s3CSVOrder struct {
	column string
	desc   bool
}
type s3CSVSelectPlan struct {
	projections   []s3CSVProjection
	allColumns    bool
	conditions    []s3CSVCondition
	orderBy       []s3CSVOrder
	limit, offset int
	hasLimit      bool
	count         bool
	countAlias    string
}

func (d *S3CSVDriver) executeSQL(ctx context.Context, query string, args []driver.NamedValue) (*s3CSVResult, error) {
	stmt, err := sqlparser.NewTestParser().Parse(strings.TrimSpace(query))
	if err != nil {
		return nil, fmt.Errorf("s3-csv SQL parse failed: %w", err)
	}
	selectStmt, ok := stmt.(*sqlparser.Select)
	if !ok {
		return nil, errors.New("s3-csv accepts SELECT statements only")
	}
	argIndex := new(int)
	rows, columns, err := d.evaluateSelect(ctx, selectStmt, args, argIndex)
	if err != nil {
		return nil, err
	}
	if *argIndex != len(args) {
		return nil, fmt.Errorf("s3-csv received %d unused SQL arguments", len(args)-*argIndex)
	}
	return &s3CSVResult{columns: columns, types: inferCSVColumnTypes(columns, rows), rows: rows}, nil
}

func (d *S3CSVDriver) evaluateSelect(ctx context.Context, stmt *sqlparser.Select, args []driver.NamedValue, argIndex *int) ([][]driver.Value, []string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if stmt == nil || stmt.With != nil || stmt.Distinct || stmt.GroupBy != nil || stmt.Having != nil || stmt.Lock != sqlparser.NoLock {
		return nil, nil, errors.New("s3-csv supports plain SELECT, WHERE, ORDER BY, LIMIT and OFFSET only")
	}
	if len(stmt.From) != 1 {
		return nil, nil, errors.New("s3-csv queries must read one configured CSV object")
	}
	from, ok := stmt.From[0].(*sqlparser.AliasedTableExpr)
	if !ok || len(from.Partitions) > 0 || len(from.Hints) > 0 {
		return nil, nil, errors.New("s3-csv supports one table reference or derived SELECT")
	}
	rows, sourceColumns, err := d.readSQLSource(ctx, from.Expr, args, argIndex)
	if err != nil {
		return nil, nil, err
	}
	plan, err := buildS3CSVPlan(stmt, args, argIndex)
	if err != nil {
		return nil, nil, err
	}
	if err := validateCSVPlanColumns(sourceColumns, plan); err != nil {
		return nil, nil, err
	}
	filtered, err := applyCSVConditions(ctx, rows, plan.conditions)
	if err != nil {
		return nil, nil, err
	}
	filtered, err = applyCSVOrder(ctx, filtered, plan.orderBy)
	if err != nil {
		return nil, nil, err
	}
	if plan.count {
		countColumn := plan.countAlias
		if countColumn == "" {
			countColumn = "count(*)"
		}
		countRows := []map[string]interface{}{{countColumn: int64(len(filtered))}}
		countRows = applyCSVPage(countRows, plan.offset, plan.limit, plan.hasLimit)
		if len(countRows) == 0 {
			return [][]driver.Value{}, []string{countColumn}, nil
		}
		return [][]driver.Value{{normalizeDriverValue(countRows[0][countColumn])}}, []string{countColumn}, nil
	}
	projections, err := resolveCSVProjections(sourceColumns, plan)
	if err != nil {
		return nil, nil, err
	}
	columns := make([]string, len(projections))
	for i, projection := range projections {
		columns[i] = projection.alias
	}
	filtered = applyCSVPage(filtered, plan.offset, plan.limit, plan.hasLimit)
	results := make([][]driver.Value, 0, len(filtered))
	for _, row := range filtered {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		values := make([]driver.Value, len(columns))
		for i, projection := range projections {
			values[i] = normalizeDriverValue(csvMapValue(row, projection.name))
		}
		results = append(results, values)
	}
	return results, columns, nil
}

func (d *S3CSVDriver) readSQLSource(ctx context.Context, expr sqlparser.SimpleTableExpr, args []driver.NamedValue, argIndex *int) ([]map[string]interface{}, []string, error) {
	switch table := expr.(type) {
	case sqlparser.TableName:
		// Table authorization matches references exactly as written —
		// qualifier and case included. Execution must use the same
		// identity: accepting a qualified or case-variant spelling here
		// would let a query authorized for one identity (public.records,
		// RECORDS) read the single configured table under another. CSV
		// sources have no schemas, so only the exact configured name is
		// addressable.
		if qualifier := table.Qualifier.String(); qualifier != "" {
			return nil, nil, fmt.Errorf("s3-csv table %q must be referenced without a qualifier", qualifier+"."+table.Name.String())
		}
		name := table.Name.String()
		if name == "" || name != d.tableName {
			return nil, nil, fmt.Errorf("s3-csv table %q does not match configured table %q", name, d.tableName)
		}
		result, err := d.Query(ctx, &S3CSVQuery{Key: d.objectKey})
		if err != nil {
			return nil, nil, err
		}
		columns := append([]string(nil), result.Columns...)
		if len(columns) == 0 && len(result.Rows) > 0 {
			for name := range result.Rows[0] {
				columns = append(columns, name)
			}
			sort.Strings(columns)
		}
		return result.Rows, columns, nil
	case *sqlparser.DerivedTable:
		inner, ok := table.Select.(*sqlparser.Select)
		if !ok {
			return nil, nil, errors.New("s3-csv derived tables must contain a SELECT")
		}
		rows, columns, err := d.evaluateSelect(ctx, inner, args, argIndex)
		if err != nil {
			return nil, nil, err
		}
		mapped := make([]map[string]interface{}, len(rows))
		for i, row := range rows {
			mapped[i] = make(map[string]interface{}, len(columns))
			for col, name := range columns {
				mapped[i][name] = row[col]
			}
		}
		return mapped, columns, nil
	default:
		return nil, nil, errors.New("s3-csv supports a named table or derived SELECT only")
	}
}

func buildS3CSVPlan(stmt *sqlparser.Select, args []driver.NamedValue, argIndex *int) (*s3CSVSelectPlan, error) {
	plan := &s3CSVSelectPlan{}
	exprs := stmt.SelectExprs.Exprs
	if len(exprs) == 1 {
		if aliased, ok := exprs[0].(*sqlparser.AliasedExpr); ok && strings.EqualFold(strings.ReplaceAll(sqlparser.String(aliased.Expr), " ", ""), "count(*)") {
			plan.count = true
			plan.countAlias = aliased.As.String()
		}
	}
	if !plan.count {
		if len(exprs) == 1 {
			if _, ok := exprs[0].(*sqlparser.StarExpr); ok {
				plan.allColumns = true
			}
		}
		if !plan.allColumns {
			for _, selectExpr := range exprs {
				aliased, ok := selectExpr.(*sqlparser.AliasedExpr)
				if !ok {
					return nil, errors.New("s3-csv projection supports column names and * only")
				}
				column, ok := aliased.Expr.(*sqlparser.ColName)
				if !ok {
					return nil, errors.New("s3-csv projection supports column names and * only")
				}
				name, alias := column.Name.String(), aliased.As.String()
				if alias == "" {
					alias = name
				}
				plan.projections = append(plan.projections, s3CSVProjection{name: name, alias: alias})
			}
		}
	}
	if stmt.Where != nil {
		if err := collectCSVConditions(stmt.Where.Expr, args, argIndex, &plan.conditions); err != nil {
			return nil, err
		}
	}
	for _, order := range stmt.OrderBy {
		column, ok := order.Expr.(*sqlparser.ColName)
		if !ok {
			return nil, errors.New("s3-csv ORDER BY supports column names only")
		}
		plan.orderBy = append(plan.orderBy, s3CSVOrder{column: column.Name.String(), desc: order.Direction == sqlparser.DescOrder})
	}
	if stmt.Limit != nil {
		if stmt.Limit.Offset != nil {
			value, err := csvNonNegativeInt(stmt.Limit.Offset, args, argIndex)
			if err != nil {
				return nil, err
			}
			plan.offset = value
		}
		if stmt.Limit.Rowcount != nil {
			value, err := csvNonNegativeInt(stmt.Limit.Rowcount, args, argIndex)
			if err != nil {
				return nil, err
			}
			plan.limit = value
			plan.hasLimit = true
		}
	}
	return plan, nil
}

func collectCSVConditions(expr sqlparser.Expr, args []driver.NamedValue, argIndex *int, conditions *[]s3CSVCondition) error {
	switch value := expr.(type) {
	case *sqlparser.AndExpr:
		if err := collectCSVConditions(value.Left, args, argIndex, conditions); err != nil {
			return err
		}
		return collectCSVConditions(value.Right, args, argIndex, conditions)
	case *sqlparser.ComparisonExpr:
		column, ok := value.Left.(*sqlparser.ColName)
		if !ok {
			return errors.New("s3-csv WHERE comparisons require a column on the left")
		}
		condition := s3CSVCondition{column: column.Name.String(), op: value.Operator}
		switch right := value.Right.(type) {
		case sqlparser.ValTuple:
			if value.Operator != sqlparser.InOp && value.Operator != sqlparser.NotInOp {
				return errors.New("s3-csv only supports IN lists for tuple comparisons")
			}
			for _, item := range right {
				parsed, err := csvSQLValue(item, args, argIndex)
				if err != nil {
					return err
				}
				condition.values = append(condition.values, parsed)
			}
		default:
			parsed, err := csvSQLValue(value.Right, args, argIndex)
			if err != nil {
				return err
			}
			condition.value = parsed
		}
		*conditions = append(*conditions, condition)
		return nil
	case *sqlparser.IsExpr:
		column, ok := value.Left.(*sqlparser.ColName)
		if !ok {
			return errors.New("s3-csv IS NULL filters require a column on the left")
		}
		switch value.Right {
		case sqlparser.IsNullOp:
			*conditions = append(*conditions, s3CSVCondition{column: column.Name.String(), isNull: 1})
		case sqlparser.IsNotNullOp:
			*conditions = append(*conditions, s3CSVCondition{column: column.Name.String(), isNull: -1})
		default:
			return errors.New("s3-csv supports IS NULL and IS NOT NULL only")
		}
		return nil
	default:
		return errors.New("s3-csv WHERE supports AND-combined comparisons and NULL checks")
	}
}

func validateCSVPlanColumns(sourceColumns []string, plan *s3CSVSelectPlan) error {
	for _, condition := range plan.conditions {
		if !containsCSVColumn(sourceColumns, condition.column) {
			return fmt.Errorf("s3-csv column %q does not exist", condition.column)
		}
	}
	for _, order := range plan.orderBy {
		if !containsCSVColumn(sourceColumns, order.column) {
			return fmt.Errorf("s3-csv ORDER BY column %q does not exist", order.column)
		}
	}
	return nil
}

func containsCSVColumn(columns []string, target string) bool {
	for _, column := range columns {
		if strings.EqualFold(column, target) {
			return true
		}
	}
	return false
}

func csvSQLValue(expr sqlparser.Expr, args []driver.NamedValue, argIndex *int) (interface{}, error) {
	switch value := expr.(type) {
	case *sqlparser.Literal:
		switch value.Type {
		case sqlparser.StrVal:
			return value.Val, nil
		case sqlparser.IntVal:
			parsed, err := strconv.ParseInt(value.Val, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid integer literal %q", value.Val)
			}
			return parsed, nil
		case sqlparser.DecimalVal, sqlparser.FloatVal:
			parsed, err := strconv.ParseFloat(value.Val, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid numeric literal %q", value.Val)
			}
			return parsed, nil
		default:
			return nil, fmt.Errorf("s3-csv does not support SQL literal type %s", sqlparser.String(value))
		}
	case *sqlparser.NullVal:
		return nil, nil
	case sqlparser.BoolVal:
		return bool(value), nil
	case *sqlparser.Argument:
		if *argIndex < len(args) {
			arg := args[*argIndex]
			(*argIndex)++
			return arg.Value, nil
		}
		return nil, fmt.Errorf("missing SQL argument %q", value.Name)
	case *sqlparser.UnaryExpr:
		operand, err := csvSQLValue(value.Expr, args, argIndex)
		if err != nil {
			return nil, err
		}
		switch value.Operator {
		case sqlparser.UMinusOp:
			switch n := operand.(type) {
			case int64:
				return -n, nil
			case float64:
				return -n, nil
			}
		case sqlparser.UPlusOp:
			return operand, nil
		}
		return nil, errors.New("s3-csv supports numeric unary signs only")
	default:
		return nil, fmt.Errorf("s3-csv does not support SQL value expression %T", expr)
	}
}

func csvNonNegativeInt(expr sqlparser.Expr, args []driver.NamedValue, argIndex *int) (int, error) {
	value, err := csvSQLValue(expr, args, argIndex)
	if err != nil {
		return 0, err
	}
	number, ok := value.(int64)
	if !ok || number < 0 || number > int64(math.MaxInt) {
		return 0, errors.New("s3-csv LIMIT and OFFSET must be non-negative integers")
	}
	return int(number), nil
}

func applyCSVConditions(ctx context.Context, rows []map[string]interface{}, conditions []s3CSVCondition) ([]map[string]interface{}, error) {
	if len(conditions) == 0 {
		return rows, nil
	}
	filtered := make([]map[string]interface{}, 0, len(rows))
	for i, row := range rows {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		matches := true
		for _, condition := range conditions {
			actual, exists := csvMapLookup(row, condition.column)
			if !exists {
				return nil, fmt.Errorf("s3-csv column %q does not exist", condition.column)
			}
			if !matchesCSVCondition(actual, condition) {
				matches = false
				break
			}
		}
		if matches {
			filtered = append(filtered, row)
		}
	}
	return filtered, nil
}
func matchesCSVCondition(actual interface{}, condition s3CSVCondition) bool {
	if condition.isNull != 0 {
		return (actual == nil) == (condition.isNull > 0)
	}
	if condition.op == sqlparser.InOp || condition.op == sqlparser.NotInOp {
		found := false
		for _, expected := range condition.values {
			if actual != nil && expected != nil && compareCSVValues(actual, expected) == 0 {
				found = true
				break
			}
		}
		if condition.op == sqlparser.NotInOp {
			return actual != nil && !found
		}
		return found
	}
	if condition.op == sqlparser.LikeOp || condition.op == sqlparser.NotLikeOp {
		if actual == nil || condition.value == nil {
			return false
		}
		pattern := regexp.QuoteMeta(fmt.Sprint(condition.value))
		pattern = strings.ReplaceAll(strings.ReplaceAll(pattern, "%", ".*"), "_", ".")
		matched, err := regexp.MatchString("(?s)^"+pattern+"$", fmt.Sprint(actual))
		if err != nil {
			return false
		}
		if condition.op == sqlparser.NotLikeOp {
			return !matched
		}
		return matched
	}
	if actual == nil || condition.value == nil {
		return condition.op == sqlparser.NullSafeEqualOp && actual == nil && condition.value == nil
	}
	comparison := compareCSVValues(actual, condition.value)
	switch condition.op {
	case sqlparser.EqualOp, sqlparser.NullSafeEqualOp:
		return comparison == 0
	case sqlparser.NotEqualOp:
		return comparison != 0
	case sqlparser.LessThanOp:
		return comparison < 0
	case sqlparser.LessEqualOp:
		return comparison <= 0
	case sqlparser.GreaterThanOp:
		return comparison > 0
	case sqlparser.GreaterEqualOp:
		return comparison >= 0
	}
	return false
}
func compareCSVValues(left, right interface{}) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return -1
	}
	if right == nil {
		return 1
	}
	leftNumber, leftOK := csvNumber(left)
	rightNumber, rightOK := csvNumber(right)
	if leftOK && rightOK {
		switch {
		case leftNumber < rightNumber:
			return -1
		case leftNumber > rightNumber:
			return 1
		default:
			return 0
		}
	}
	leftText, rightText := fmt.Sprint(left), fmt.Sprint(right)
	switch {
	case leftText < rightText:
		return -1
	case leftText > rightText:
		return 1
	default:
		return 0
	}
}
func csvNumber(value interface{}) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float32:
		return float64(v), true
	case float64:
		return v, true
	case string:
		number, err := strconv.ParseFloat(v, 64)
		return number, err == nil
	default:
		return 0, false
	}
}
func applyCSVOrder(ctx context.Context, rows []map[string]interface{}, orderBy []s3CSVOrder) ([]map[string]interface{}, error) {
	if len(orderBy) == 0 || len(rows) < 2 {
		return rows, nil
	}
	for _, order := range orderBy {
		if _, exists := csvMapLookup(rows[0], order.column); !exists {
			return nil, fmt.Errorf("s3-csv ORDER BY column %q does not exist", order.column)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(rows, func(i, j int) bool {
		for _, order := range orderBy {
			left, _ := csvMapLookup(rows[i], order.column)
			right, _ := csvMapLookup(rows[j], order.column)
			comparison := compareCSVValues(left, right)
			if comparison == 0 {
				continue
			}
			if order.desc {
				return comparison > 0
			}
			return comparison < 0
		}
		return false
	})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
func resolveCSVProjections(sourceColumns []string, plan *s3CSVSelectPlan) ([]s3CSVProjection, error) {
	if plan.allColumns {
		columns := make([]s3CSVProjection, len(sourceColumns))
		for i, name := range sourceColumns {
			columns[i] = s3CSVProjection{name: name, alias: name}
		}
		return columns, nil
	}
	columns := make([]s3CSVProjection, 0, len(plan.projections))
	for _, projection := range plan.projections {
		found := false
		for _, source := range sourceColumns {
			if strings.EqualFold(source, projection.name) {
				columns = append(columns, s3CSVProjection{name: source, alias: projection.alias})
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("s3-csv column %q does not exist", projection.name)
		}
	}
	return columns, nil
}

func csvMapLookup(row map[string]interface{}, column string) (interface{}, bool) {
	if value, ok := row[column]; ok {
		return value, true
	}
	for name, value := range row {
		if strings.EqualFold(name, column) {
			return value, true
		}
	}
	return nil, false
}

func csvMapValue(row map[string]interface{}, column string) interface{} {
	value, _ := csvMapLookup(row, column)
	return value
}
func applyCSVPage(rows []map[string]interface{}, offset, limit int, hasLimit bool) []map[string]interface{} {
	if offset >= len(rows) {
		return []map[string]interface{}{}
	}
	if offset > 0 {
		rows = rows[offset:]
	}
	if hasLimit && limit < len(rows) {
		rows = rows[:limit]
	}
	return rows
}
func normalizeDriverValue(value interface{}) driver.Value {
	switch v := value.(type) {
	case nil, int64, float64, bool, string, []byte, time.Time:
		return v
	case int:
		return int64(v)
	case float32:
		return float64(v)
	default:
		return fmt.Sprint(v)
	}
}
func inferCSVColumnTypes(columns []string, rows [][]driver.Value) []string {
	types := make([]string, len(columns))
	for col := range columns {
		types[col] = "VARCHAR"
		for _, row := range rows {
			if col >= len(row) || row[col] == nil {
				continue
			}
			switch row[col].(type) {
			case int64:
				types[col] = "BIGINT"
			case float64:
				types[col] = "DOUBLE"
			case bool:
				types[col] = "BOOLEAN"
			case time.Time:
				types[col] = "TIMESTAMP"
			}
			break
		}
	}
	return types
}
