package table_formats

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/iceberg-go"
	"github.com/apache/iceberg-go/catalog/rest"
	"github.com/apache/iceberg-go/table"
	"vitess.io/vitess/go/vt/sqlparser"
)

const (
	icebergDefaultMaxRows = 10000
	icebergMaxOffsetRows  = 100000
	icebergSQLReadOnly    = "Iceberg SQL adapter accepts read-only SELECT queries only"
)

var errIcebergReadOnly = errors.New(icebergSQLReadOnly)

// icebergSQLConfig contains the catalog connection settings without putting
// credentials into a DSN or query string.
type icebergSQLConfig struct {
	uri        string
	warehouse  string
	namespace  string
	token      string
	credential string
	props      iceberg.Properties
	maxRows    int64
}

type icebergSQLConnector struct{ config icebergSQLConfig }

func (c *icebergSQLConnector) Connect(ctx context.Context) (driver.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	options := []rest.Option{rest.WithWarehouseLocation(c.config.warehouse)}
	if c.config.token != "" {
		options = append(options, rest.WithOAuthToken(c.config.token))
	} else if c.config.credential != "" {
		options = append(options, rest.WithCredential(c.config.credential))
	}
	if len(c.config.props) != 0 {
		options = append(options, rest.WithAdditionalProps(c.config.props))
	}
	catalog, err := rest.NewCatalog(ctx, "data-fabric", c.config.uri, options...)
	if err != nil {
		return nil, fmt.Errorf("connect to Iceberg REST catalog: %w", err)
	}
	return &icebergSQLConn{catalog: catalog, config: c.config}, nil
}

func (c *icebergSQLConnector) Driver() driver.Driver { return icebergSQLDatabaseDriver{connector: c} }

type icebergSQLDatabaseDriver struct{ connector *icebergSQLConnector }

func (d icebergSQLDatabaseDriver) Open(string) (driver.Conn, error) {
	return d.connector.Connect(context.Background())
}

type icebergSQLConn struct {
	catalog *rest.Catalog
	config  icebergSQLConfig
	closed  bool
}

func (c *icebergSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Iceberg adapter does not support prepared statements; use QueryContext parameters")
}

func (c *icebergSQLConn) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	return c.catalog.Close()
}

func (c *icebergSQLConn) Begin() (driver.Tx, error) { return nil, errIcebergReadOnly }

func (c *icebergSQLConn) Ping(ctx context.Context) error {
	if c.closed {
		return driver.ErrBadConn
	}
	_, err := c.catalog.ListNamespaces(ctx, nil)
	if err != nil {
		return fmt.Errorf("Iceberg REST catalog health check failed: %w", err)
	}
	return nil
}

func (c *icebergSQLConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, errIcebergReadOnly
}

func (c *icebergSQLConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if c.closed {
		return nil, driver.ErrBadConn
	}
	plan, err := parseIcebergSQL(query, args, c.config.namespace, c.config.maxRows, false)
	if err != nil {
		return nil, err
	}
	identifier := make(table.Identifier, 0, len(plan.namespace)+1)
	identifier = append(identifier, plan.namespace...)
	identifier = append(identifier, plan.table)
	tbl, err := c.catalog.LoadTable(ctx, identifier)
	if err != nil {
		return nil, fmt.Errorf("load Iceberg table %s: %w", strings.Join(identifier, "."), err)
	}

	projection := plan.projections
	if plan.count {
		projection = nil
	} else if !plan.allColumns {
		for i := range projection {
			field, ok := tbl.Schema().FindFieldByName(projection[i].source)
			if !ok {
				field, ok = tbl.Schema().FindFieldByNameCaseInsensitive(projection[i].source)
			}
			if !ok {
				return nil, fmt.Errorf("Iceberg column %q does not exist in table %s", projection[i].source, strings.Join(identifier, "."))
			}
			projection[i].source = field.Name
		}
	}

	columns := make([]string, 0)
	columnTypes := make([]string, 0)
	if plan.count {
		columns = []string{plan.countColumn}
		columnTypes = []string{"BIGINT"}
	} else if plan.allColumns {
		for _, field := range tbl.Schema().Fields() {
			columns = append(columns, field.Name)
			columnTypes = append(columnTypes, icebergSQLTypeName(field.Type))
			projection = append(projection, icebergProjection{source: field.Name, output: field.Name})
		}
	} else {
		for _, field := range projection {
			columns = append(columns, field.output)
			metadataField, _ := tbl.Schema().FindFieldByName(field.source)
			columnTypes = append(columnTypes, icebergSQLTypeName(metadataField.Type))
		}
	}
	if plan.countNoRows {
		return &icebergSQLRows{ctx: ctx, columns: columns, columnTypes: columnTypes, closed: true}, nil
	}
	if plan.count && plan.hasLimit && plan.limit == 0 {
		return &icebergSQLRows{ctx: ctx, columns: columns, columnTypes: columnTypes, count: true, countReady: true}, nil
	}
	if plan.limit == 0 && !plan.count {
		return &icebergSQLRows{ctx: ctx, columns: columns, columnTypes: columnTypes, closed: true}, nil
	}

	scanOptions := []table.ScanOption{table.WithMaxConcurrency(1), table.WithArrowBatchSize(256), table.WithCaseSensitive(false)}
	if plan.filter != nil {
		scanOptions = append(scanOptions, table.WithRowFilter(plan.filter))
	}
	selected := make([]string, 0, len(projection))
	seen := make(map[string]struct{}, len(projection))
	for _, field := range projection {
		if _, exists := seen[field.source]; exists {
			continue
		}
		seen[field.source] = struct{}{}
		selected = append(selected, field.source)
	}
	if plan.count {
		fields := tbl.Schema().Fields()
		if len(fields) > 0 {
			scanOptions = append(scanOptions, table.WithSelectedFields(fields[0].Name))
		}
	} else if !plan.allColumns {
		scanOptions = append(scanOptions, table.WithSelectedFields(selected...))
	}
	scan := tbl.Scan(scanOptions...)
	if plan.hasLimit {
		scan.UseRowLimit(plan.offset + plan.limit)
	}
	_, records, err := scan.ToArrowRecords(ctx)
	if err != nil {
		_ = scan.Close()
		return nil, fmt.Errorf("plan Iceberg scan: %w", err)
	}
	next, stop := iter.Pull2(records)
	rows := &icebergSQLRows{
		ctx: ctx, columns: columns, columnTypes: columnTypes, projections: projection, offset: plan.offset,
		count: plan.count, countOffset: plan.offset, nextBatch: next, stop: stop, scan: scan,
	}
	if plan.count && plan.hasLimit {
		rows.countLimit = plan.limit
	}
	return rows, nil
}

type icebergProjection struct{ source, output string }

type icebergSelectPlan struct {
	namespace   []string
	table       string
	projections []icebergProjection
	allColumns  bool
	filter      iceberg.BooleanExpression
	limit       int64
	offset      int64
	hasLimit    bool
	count       bool
	countNoRows bool
	countColumn string
}

func parseIcebergSQL(query string, args []driver.NamedValue, defaultNamespace string, maxRows int64, allowUnbounded bool) (*icebergSelectPlan, error) {
	stmt, err := sqlparser.NewTestParser().Parse(strings.TrimSpace(query))
	if err != nil {
		return nil, fmt.Errorf("Iceberg SQL parse failed: %w", err)
	}
	selectStmt, ok := stmt.(*sqlparser.Select)
	if !ok {
		return nil, errIcebergReadOnly
	}
	if count, countColumn := icebergCountSelect(selectStmt); count {
		if len(selectStmt.From) != 1 {
			return nil, errors.New("Iceberg COUNT(*) requires one table or a single-table SELECT subquery")
		}
		from, ok := selectStmt.From[0].(*sqlparser.AliasedTableExpr)
		if !ok || len(from.Partitions) != 0 || len(from.Hints) != 0 {
			return nil, errors.New("Iceberg COUNT(*) supports one table or a single-table SELECT subquery")
		}
		if derived, ok := from.Expr.(*sqlparser.DerivedTable); ok {
			if selectStmt.With != nil || selectStmt.Distinct || selectStmt.Where != nil || selectStmt.GroupBy != nil ||
				selectStmt.Having != nil || selectStmt.OrderBy != nil || len(selectStmt.Windows) != 0 ||
				selectStmt.Limit != nil || selectStmt.Into != nil || selectStmt.Lock != sqlparser.NoLock {
				return nil, errors.New("Iceberg COUNT(*) wrapper supports only a single SELECT subquery")
			}
			inner, ok := derived.Select.(*sqlparser.Select)
			if !ok {
				return nil, errors.New("Iceberg COUNT(*) only supports a SELECT subquery")
			}
			plan, err := parseIcebergSelect(inner, args, defaultNamespace, maxRows, true)
			if err != nil {
				return nil, err
			}
			plan.count, plan.countColumn = true, countColumn
			return plan, nil
		}
		countStmt := *selectStmt
		countStmt.SelectExprs = &sqlparser.SelectExprs{Exprs: []sqlparser.SelectExpr{&sqlparser.StarExpr{}}}
		plan, err := parseIcebergSelect(&countStmt, args, defaultNamespace, maxRows, true)
		if err != nil {
			return nil, err
		}
		// LIMIT on an aggregate applies to its one output row, not the rows
		// consumed by COUNT(*).
		plan.countNoRows = (plan.hasLimit && plan.limit == 0) || plan.offset > 0
		plan.hasLimit = false
		plan.offset = 0
		plan.allColumns = false
		plan.projections = nil
		plan.count, plan.countColumn = true, countColumn
		return plan, nil
	}
	return parseIcebergSelect(selectStmt, args, defaultNamespace, maxRows, allowUnbounded)
}

func icebergCountSelect(stmt *sqlparser.Select) (bool, string) {
	if stmt.SelectExprs == nil || len(stmt.SelectExprs.Exprs) != 1 {
		return false, ""
	}
	aliased, ok := stmt.SelectExprs.Exprs[0].(*sqlparser.AliasedExpr)
	if !ok || !strings.EqualFold(strings.ReplaceAll(sqlparser.String(aliased.Expr), " ", ""), "count(*)") {
		return false, ""
	}
	name := aliased.As.String()
	if name == "" {
		name = "count(*)"
	}
	return true, name
}

func parseIcebergSelect(stmt *sqlparser.Select, args []driver.NamedValue, defaultNamespace string, maxRows int64, allowUnbounded bool) (*icebergSelectPlan, error) {
	if stmt.With != nil || stmt.Distinct || stmt.GroupBy != nil || stmt.Having != nil || stmt.Into != nil || stmt.Lock != sqlparser.NoLock || len(stmt.Windows) != 0 {
		return nil, errors.New("Iceberg SQL supports read-only single-table SELECT with projection, AND filters and LIMIT/OFFSET")
	}
	if len(stmt.From) != 1 {
		return nil, errors.New("Iceberg SQL queries must reference exactly one table")
	}
	from, ok := stmt.From[0].(*sqlparser.AliasedTableExpr)
	if !ok || len(from.Partitions) != 0 || len(from.Hints) != 0 {
		return nil, errors.New("Iceberg SQL supports one table reference")
	}
	tableName, ok := from.Expr.(sqlparser.TableName)
	if !ok || tableName.Name.String() == "" {
		return nil, errors.New("Iceberg SQL supports a named table reference")
	}
	namespace := defaultNamespace
	if qualifier := tableName.Qualifier.String(); qualifier != "" {
		namespace = qualifier
	}
	if strings.TrimSpace(namespace) == "" {
		return nil, errors.New("Iceberg namespace is required; qualify the table as namespace.table or set the data source schema")
	}
	plan := &icebergSelectPlan{namespace: strings.Split(namespace, "."), table: tableName.Name.String()}
	if stmt.SelectExprs == nil || len(stmt.SelectExprs.Exprs) == 0 {
		return nil, errors.New("Iceberg SQL SELECT list cannot be empty")
	}
	for _, expr := range stmt.SelectExprs.Exprs {
		switch value := expr.(type) {
		case *sqlparser.StarExpr:
			if len(stmt.SelectExprs.Exprs) != 1 {
				return nil, errors.New("Iceberg SQL does not support mixing * with other projections")
			}
			plan.allColumns = true
		case *sqlparser.AliasedExpr:
			column, ok := value.Expr.(*sqlparser.ColName)
			if !ok {
				return nil, errors.New("Iceberg SQL projection supports column references only")
			}
			name, err := icebergColumnName(column, from.As.String(), tableName.Name.String())
			if err != nil {
				return nil, err
			}
			output := value.As.String()
			if output == "" {
				output = name
			}
			plan.projections = append(plan.projections, icebergProjection{source: name, output: output})
		default:
			return nil, errors.New("Iceberg SQL projection supports column references and * only")
		}
	}
	if stmt.OrderBy != nil && len(stmt.OrderBy) > 0 {
		return nil, errors.New("Iceberg SQL does not support ORDER BY yet; sort the result in a query engine")
	}
	argIndex := new(int)
	if stmt.Where != nil {
		filter, err := icebergWhereFilter(stmt.Where.Expr, args, argIndex, from.As.String(), tableName.Name.String())
		if err != nil {
			return nil, err
		}
		plan.filter = filter
	}
	if stmt.Limit != nil {
		plan.hasLimit = true
		limit, err := icebergIntegerExpression(stmt.Limit.Rowcount, args, argIndex)
		if err != nil {
			return nil, fmt.Errorf("invalid Iceberg LIMIT: %w", err)
		}
		plan.limit = limit
		if stmt.Limit.Offset != nil {
			offset, err := icebergIntegerExpression(stmt.Limit.Offset, args, argIndex)
			if err != nil {
				return nil, fmt.Errorf("invalid Iceberg OFFSET: %w", err)
			}
			plan.offset = offset
		}
		if plan.limit < 0 || plan.offset < 0 {
			return nil, errors.New("Iceberg LIMIT and OFFSET cannot be negative")
		}
		if !allowUnbounded && plan.limit > maxRows {
			return nil, fmt.Errorf("Iceberg LIMIT %d exceeds the configured maximum of %d rows", plan.limit, maxRows)
		}
		if plan.offset > icebergMaxOffsetRows || plan.limit > icebergMaxOffsetRows-plan.offset {
			return nil, fmt.Errorf("Iceberg OFFSET plus LIMIT exceeds the scan bound of %d rows", icebergMaxOffsetRows)
		}
	}
	if *argIndex != len(args) {
		return nil, fmt.Errorf("Iceberg query received %d unused SQL arguments", len(args)-*argIndex)
	}
	if !allowUnbounded && !plan.hasLimit {
		return nil, errors.New("Iceberg queries require LIMIT to keep table scans bounded")
	}
	return plan, nil
}

func icebergColumnName(column *sqlparser.ColName, alias, table string) (string, error) {
	if column == nil || column.Name.String() == "" {
		return "", errors.New("Iceberg SQL column name is required")
	}
	qualifier := column.Qualifier.Name.String()
	if qualifier != "" && !strings.EqualFold(qualifier, alias) && !strings.EqualFold(qualifier, table) {
		return "", fmt.Errorf("Iceberg SQL column qualifier %q does not match the selected table", qualifier)
	}
	return column.Name.String(), nil
}

func icebergWhereFilter(expr sqlparser.Expr, args []driver.NamedValue, argIndex *int, alias, table string) (iceberg.BooleanExpression, error) {
	switch value := expr.(type) {
	case *sqlparser.AndExpr:
		left, err := icebergWhereFilter(value.Left, args, argIndex, alias, table)
		if err != nil {
			return nil, err
		}
		right, err := icebergWhereFilter(value.Right, args, argIndex, alias, table)
		if err != nil {
			return nil, err
		}
		return iceberg.NewAnd(left, right), nil
	case *sqlparser.IsExpr:
		column, ok := value.Left.(*sqlparser.ColName)
		if !ok {
			return nil, errors.New("Iceberg WHERE supports IS NULL checks on columns only")
		}
		name, err := icebergColumnName(column, alias, table)
		if err != nil {
			return nil, err
		}
		switch value.Right {
		case sqlparser.IsNullOp:
			return iceberg.IsNull(iceberg.Reference(name)), nil
		case sqlparser.IsNotNullOp:
			return iceberg.NotNull(iceberg.Reference(name)), nil
		default:
			return nil, errors.New("Iceberg WHERE supports IS NULL and IS NOT NULL only")
		}
	case *sqlparser.ComparisonExpr:
		if value.Modifier != sqlparser.Missing || value.Escape != nil {
			return nil, errors.New("Iceberg WHERE does not support comparison modifiers")
		}
		column, ok := value.Left.(*sqlparser.ColName)
		operator := value.Operator
		literalExpr := value.Right
		if !ok {
			column, ok = value.Right.(*sqlparser.ColName)
			if !ok {
				return nil, errors.New("Iceberg WHERE comparisons must compare a column with a literal or parameter")
			}
			literalExpr = value.Left
			operator = icebergFlipComparison(operator)
		}
		name, err := icebergColumnName(column, alias, table)
		if err != nil {
			return nil, err
		}
		literal, err := icebergSQLValue(literalExpr, args, argIndex)
		if err != nil {
			return nil, err
		}
		if literal == nil {
			return nil, errors.New("Iceberg WHERE comparisons with NULL must use IS NULL or IS NOT NULL")
		}
		op := icebergComparisonOperation(operator)
		if op == nil {
			return nil, fmt.Errorf("Iceberg WHERE operator %q is not supported", sqlparser.String(value))
		}
		return icebergPredicate(*op, iceberg.Reference(name), literal)
	default:
		return nil, errors.New("Iceberg WHERE supports AND-combined comparisons and NULL checks only")
	}
}

func icebergPredicate(operation iceberg.Operation, reference iceberg.UnboundTerm, value any) (iceberg.BooleanExpression, error) {
	var literal iceberg.Literal
	switch typed := value.(type) {
	case bool:
		literal = iceberg.NewLiteral(typed)
	case int:
		literal = iceberg.NewLiteral(int64(typed))
	case int8:
		literal = iceberg.NewLiteral(int64(typed))
	case int16:
		literal = iceberg.NewLiteral(int64(typed))
	case int32:
		literal = iceberg.NewLiteral(int64(typed))
	case int64:
		literal = iceberg.NewLiteral(typed)
	case float32:
		literal = iceberg.NewLiteral(float64(typed))
	case float64:
		literal = iceberg.NewLiteral(typed)
	case string:
		literal = iceberg.NewLiteral(typed)
	case []byte:
		literal = iceberg.NewLiteral(typed)
	default:
		return nil, fmt.Errorf("Iceberg WHERE parameter type %T is not supported", value)
	}
	return iceberg.LiteralPredicate(operation, reference, literal), nil
}

func icebergFlipComparison(operator sqlparser.ComparisonExprOperator) sqlparser.ComparisonExprOperator {
	switch operator {
	case sqlparser.LessThanOp:
		return sqlparser.GreaterThanOp
	case sqlparser.GreaterThanOp:
		return sqlparser.LessThanOp
	case sqlparser.LessEqualOp:
		return sqlparser.GreaterEqualOp
	case sqlparser.GreaterEqualOp:
		return sqlparser.LessEqualOp
	default:
		return operator
	}
}

func icebergComparisonOperation(operator sqlparser.ComparisonExprOperator) *iceberg.Operation {
	var operation iceberg.Operation
	switch operator {
	case sqlparser.EqualOp:
		operation = iceberg.OpEQ
	case sqlparser.NotEqualOp:
		operation = iceberg.OpNEQ
	case sqlparser.LessThanOp:
		operation = iceberg.OpLT
	case sqlparser.LessEqualOp:
		operation = iceberg.OpLTEQ
	case sqlparser.GreaterThanOp:
		operation = iceberg.OpGT
	case sqlparser.GreaterEqualOp:
		operation = iceberg.OpGTEQ
	default:
		return nil
	}
	return &operation
}

func icebergIntegerExpression(expr sqlparser.Expr, args []driver.NamedValue, argIndex *int) (int64, error) {
	value, err := icebergSQLValue(expr, args, argIndex)
	if err != nil {
		return 0, err
	}
	switch number := value.(type) {
	case int64:
		return number, nil
	case int:
		return int64(number), nil
	default:
		return 0, fmt.Errorf("expected an integer, got %T", value)
	}
}

func icebergSQLValue(expr sqlparser.Expr, args []driver.NamedValue, argIndex *int) (any, error) {
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
			return nil, fmt.Errorf("Iceberg SQL does not support literal type %s", sqlparser.String(value))
		}
	case *sqlparser.NullVal:
		return nil, nil
	case sqlparser.BoolVal:
		return bool(value), nil
	case *sqlparser.Argument:
		if *argIndex >= len(args) {
			return nil, fmt.Errorf("missing Iceberg SQL argument %q", value.Name)
		}
		arg := args[*argIndex]
		*argIndex++
		return arg.Value, nil
	case *sqlparser.UnaryExpr:
		operand, err := icebergSQLValue(value.Expr, args, argIndex)
		if err != nil {
			return nil, err
		}
		switch value.Operator {
		case sqlparser.UMinusOp:
			switch number := operand.(type) {
			case int64:
				if number == math.MinInt64 {
					return nil, errors.New("integer literal is out of range")
				}
				return -number, nil
			case float64:
				return -number, nil
			}
		case sqlparser.UPlusOp:
			return operand, nil
		}
		return nil, errors.New("Iceberg SQL supports unary signs on numeric literals only")
	default:
		return nil, fmt.Errorf("Iceberg SQL does not support WHERE value %s", sqlparser.String(expr))
	}
}

type icebergSQLRows struct {
	ctx           context.Context
	columns       []string
	projections   []icebergProjection
	offset        int64
	seen          int64
	count         bool
	countReady    bool
	countEmitted  bool
	countOffset   int64
	countLimit    int64
	countValue    int64
	current       arrow.RecordBatch
	rowIndex      int64
	nextBatch     func() (arrow.RecordBatch, error, bool)
	stop          func()
	scan          *table.Scan
	closed        bool
	columnIndexes []int
	columnTypes   []string
}

func (r *icebergSQLRows) Columns() []string { return r.columns }

func (r *icebergSQLRows) Close() error { return r.finish() }

func (r *icebergSQLRows) finish() error {
	if r.closed {
		return nil
	}
	r.closed = true
	if r.current != nil {
		r.current.Release()
		r.current = nil
	}
	if r.stop != nil {
		r.stop()
		r.stop = nil
	}
	if r.scan != nil {
		err := r.scan.Close()
		r.scan = nil
		return err
	}
	return nil
}

func (r *icebergSQLRows) Next(dest []driver.Value) error {
	if r.closed {
		return io.EOF
	}
	if err := r.ctx.Err(); err != nil {
		_ = r.finish()
		return err
	}
	if r.count {
		return r.nextCount(dest)
	}
	for {
		if r.current != nil && r.rowIndex < r.current.NumRows() {
			if r.seen < r.offset {
				r.rowIndex++
				r.seen++
				continue
			}
			if err := r.mapBatchColumns(); err != nil {
				_ = r.finish()
				return err
			}
			for i, sourceIndex := range r.columnIndexes {
				value, err := icebergArrowDriverValue(r.current.Column(sourceIndex), int(r.rowIndex))
				if err != nil {
					_ = r.finish()
					return fmt.Errorf("convert Iceberg result column %q: %w", r.columns[i], err)
				}
				dest[i] = value
			}
			r.rowIndex++
			r.seen++
			return nil
		}
		if r.current != nil {
			r.current.Release()
			r.current = nil
		}
		batch, err, ok := r.nextBatch()
		if !ok {
			_ = r.finish()
			return io.EOF
		}
		if err != nil {
			if batch != nil {
				batch.Release()
			}
			_ = r.finish()
			return fmt.Errorf("read Iceberg data files: %w", err)
		}
		r.current = batch
		r.rowIndex = 0
		r.columnIndexes = nil
	}
}

func (r *icebergSQLRows) nextCount(dest []driver.Value) error {
	if r.countReady {
		if r.countEmitted {
			_ = r.finish()
			return io.EOF
		}
		r.countEmitted = true
		if len(dest) != 0 {
			dest[0] = r.countValue
		}
		return nil
	}
	for {
		if err := r.ctx.Err(); err != nil {
			_ = r.finish()
			return err
		}
		batch, err, ok := r.nextBatch()
		if !ok {
			r.countReady = true
			break
		}
		if err != nil {
			if batch != nil {
				batch.Release()
			}
			_ = r.finish()
			return fmt.Errorf("count Iceberg rows: %w", err)
		}
		rowCount := batch.NumRows()
		if r.countOffset > 0 {
			skipped := min(rowCount, r.countOffset)
			r.countOffset -= skipped
			rowCount -= skipped
		}
		if r.countLimit > 0 {
			remaining := r.countLimit - r.countValue
			rowCount = min(rowCount, remaining)
		}
		r.countValue += rowCount
		batch.Release()
	}
	r.countReady = true
	r.countEmitted = true
	if len(dest) != 0 {
		dest[0] = r.countValue
	}
	return nil
}

func (r *icebergSQLRows) mapBatchColumns() error {
	if r.columnIndexes != nil {
		return nil
	}
	fields := r.current.Schema().Fields()
	byName := make(map[string]int, len(fields))
	for i, field := range fields {
		byName[field.Name] = i
	}
	r.columnIndexes = make([]int, len(r.projections))
	for i, projection := range r.projections {
		index, ok := byName[projection.source]
		if !ok {
			return fmt.Errorf("Iceberg scan did not return projected column %q", projection.source)
		}
		r.columnIndexes[i] = index
	}
	return nil
}

func icebergSQLTypeName(typ iceberg.Type) string {
	if typ == nil {
		return "UNKNOWN"
	}
	switch typ.Type() {
	case "boolean":
		return "BOOLEAN"
	case "int":
		return "INTEGER"
	case "long":
		return "BIGINT"
	case "float":
		return "REAL"
	case "double":
		return "DOUBLE"
	case "date":
		return "DATE"
	case "time":
		return "TIME"
	case "timestamp", "timestamp_ns":
		return "TIMESTAMP"
	case "timestamptz", "timestamptz_ns":
		return "TIMESTAMP WITH TIME ZONE"
	case "string":
		return "VARCHAR"
	case "uuid":
		return "UUID"
	case "binary", "fixed":
		return "VARBINARY"
	case "decimal":
		return "DECIMAL"
	default:
		return "JSON"
	}
}

func (r *icebergSQLRows) ColumnTypeDatabaseTypeName(index int) string {
	if index >= 0 && index < len(r.columnTypes) {
		return r.columnTypes[index]
	}
	return "UNKNOWN"
}

func (r *icebergSQLRows) ColumnTypeNullable(int) (nullable, ok bool) { return true, true }

func icebergArrowDriverValue(values arrow.Array, row int) (driver.Value, error) {
	if values.IsNull(row) {
		return nil, nil
	}
	value := array.ValueAsAny(values, row)
	switch v := value.(type) {
	case nil:
		return nil, nil
	case bool, float64, string, time.Time:
		return v, nil
	case int:
		return int64(v), nil
	case int8:
		return int64(v), nil
	case int16:
		return int64(v), nil
	case int32:
		if _, isDate := values.DataType().(*arrow.Date32Type); isDate {
			return time.Unix(int64(v)*86400, 0).UTC(), nil
		}
		return int64(v), nil
	case int64:
		if _, isDate := values.DataType().(*arrow.Date64Type); isDate {
			return time.UnixMilli(v).UTC(), nil
		}
		return v, nil
	case arrow.Date32:
		return time.Unix(int64(v)*86400, 0).UTC(), nil
	case arrow.Date64:
		return time.UnixMilli(int64(v)).UTC(), nil
	case arrow.Timestamp:
		if typ, ok := values.DataType().(*arrow.TimestampType); ok {
			return v.ToTime(typ.Unit), nil
		}
		return int64(v), nil
	case float32:
		return float64(v), nil
	case []byte:
		return append([]byte(nil), v...), nil
	case uint8:
		return int64(v), nil
	case uint16:
		return int64(v), nil
	case uint32:
		return int64(v), nil
	case uint64:
		if v <= math.MaxInt64 {
			return int64(v), nil
		}
		return strconv.FormatUint(v, 10), nil
	default:
		encoded, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("unsupported Arrow value %T: %w", value, err)
		}
		return string(encoded), nil
	}
}

// OpenWithConfig opens the read-only SQL adapter that executes bounded scans
// against Iceberg's current table snapshot via its standard REST catalog API.
func (d *IcebergDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	settings, err := icebergConfigFromDataSource(config)
	if err != nil {
		return nil, err
	}
	clientConfig := &IcebergConfig{BaseURL: settings.uri, Warehouse: settings.warehouse}
	if settings.token != "" {
		clientConfig.Auth = &IcebergAuth{Credential: settings.token, Type: "bearer"}
	}
	client, err := NewIcebergRESTClient(clientConfig)
	if err != nil {
		return nil, err
	}
	d.config, d.restClient = clientConfig, client
	d.sqlConfig = &settings
	if d.parser == nil {
		d.parser = NewIcebergMetadataParser()
	}
	return sql.OpenDB(&icebergSQLConnector{config: settings}), nil
}

func icebergConfigFromDataSource(config *model.DataSourceConfig) (icebergSQLConfig, error) {
	if config == nil {
		return icebergSQLConfig{}, errors.New("Iceberg data source config is required")
	}
	props := config.AdditionalProps
	uri := icebergConfigString(props, "catalog_uri")
	if uri == "" {
		host := strings.TrimSpace(config.Host)
		if host == "" {
			return icebergSQLConfig{}, errors.New("Iceberg REST catalog host is required")
		}
		scheme := "http"
		if config.SSL {
			scheme = "https"
		}
		if strings.Contains(host, "://") {
			parsed, err := url.Parse(host)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return icebergSQLConfig{}, errors.New("Iceberg catalog_uri must be a valid HTTP or HTTPS URL")
			}
			if parsed.Port() == "" && config.Port > 0 {
				parsed.Host = net.JoinHostPort(parsed.Hostname(), strconv.Itoa(config.Port))
			}
			uri = strings.TrimRight(parsed.String(), "/")
		} else {
			port := config.Port
			if port <= 0 {
				port = 8181
			}
			uri = fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(host, strconv.Itoa(port)))
		}
	}
	parsedURI, err := url.Parse(uri)
	if err != nil || parsedURI.Host == "" || (parsedURI.Scheme != "http" && parsedURI.Scheme != "https") {
		return icebergSQLConfig{}, errors.New("Iceberg catalog_uri must be a valid HTTP or HTTPS URL")
	}
	warehouse := icebergConfigString(props, "warehouse")
	if warehouse == "" {
		warehouse = strings.TrimSpace(config.Database)
	}
	if warehouse == "" {
		return icebergSQLConfig{}, errors.New("Iceberg warehouse is required in database or additionalProps.warehouse")
	}
	namespace := icebergConfigString(props, "namespace")
	if namespace == "" {
		namespace = strings.TrimSpace(config.Schema)
	}
	credential := icebergConfigString(props, "credential")
	if credential == "" && strings.TrimSpace(config.Username) != "" && config.Password != "" {
		credential = strings.TrimSpace(config.Username) + ":" + config.Password
	}
	allowedProperties := iceberg.Properties{}
	for key, value := range props {
		if value == nil || !(strings.HasPrefix(key, "s3.") || strings.HasPrefix(key, "rest.")) {
			continue
		}
		allowedProperties[key] = strings.TrimSpace(fmt.Sprint(value))
	}
	if authURI := icebergConfigString(props, "oauth2-server-uri"); authURI != "" {
		allowedProperties["oauth2-server-uri"] = authURI
	}
	maxRows := int64(icebergConfigInt(props, "max_rows", icebergDefaultMaxRows))
	if maxRows < 1 || maxRows > icebergMaxOffsetRows {
		return icebergSQLConfig{}, fmt.Errorf("Iceberg max_rows must be between 1 and %d", icebergMaxOffsetRows)
	}
	return icebergSQLConfig{
		uri: strings.TrimRight(uri, "/"), warehouse: warehouse, namespace: namespace,
		token: icebergConfigString(props, "token"), credential: credential,
		props: allowedProperties, maxRows: maxRows,
	}, nil
}

func icebergConfigString(props map[string]interface{}, key string) string {
	if props == nil || props[key] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(props[key]))
}

func icebergConfigInt(props map[string]interface{}, key string, fallback int) int {
	if props == nil || props[key] == nil {
		return fallback
	}
	switch value := props[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

// ApplyBatchPagination adds a bounded SQL page using the same LIMIT/OFFSET
// syntax accepted by this adapter.
func (d *IcebergDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	if batchSize <= 0 {
		batchSize = 1000
	}
	if batchSize > icebergDefaultMaxRows {
		return "", fmt.Errorf("Iceberg batch size cannot exceed %d rows", icebergDefaultMaxRows)
	}
	if offset < 0 || offset > icebergMaxOffsetRows || batchSize > icebergMaxOffsetRows-offset {
		return "", fmt.Errorf("Iceberg offset and batch size must be non-negative and total no more than %d rows", icebergMaxOffsetRows)
	}
	stmt, err := sqlparser.NewTestParser().Parse(strings.TrimSpace(query))
	if err != nil {
		return "", fmt.Errorf("Iceberg SQL parse failed: %w", err)
	}
	order, ok := stmt.(sqlparser.OrderAndLimit)
	if !ok {
		return "", errors.New("Iceberg pagination requires a SELECT statement")
	}
	order.SetLimit(&sqlparser.Limit{
		Offset:   &sqlparser.Literal{Type: sqlparser.IntVal, Val: strconv.FormatInt(offset, 10)},
		Rowcount: &sqlparser.Literal{Type: sqlparser.IntVal, Val: strconv.FormatInt(batchSize, 10)},
	})
	return sqlparser.String(stmt), nil
}

var _ driver.Connector = (*icebergSQLConnector)(nil)
var _ driver.QueryerContext = (*icebergSQLConn)(nil)
var _ driver.Pinger = (*icebergSQLConn)(nil)
var _ driver.Rows = (*icebergSQLRows)(nil)
