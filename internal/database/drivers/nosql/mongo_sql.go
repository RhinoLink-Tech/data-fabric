package nosql

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"vitess.io/vitess/go/vt/sqlparser"
)

var errMongoReadOnly = errors.New("MongoDB query adapter accepts SELECT statements only")

type mongoSQLConn struct {
	client    *mongo.Client
	database  *mongo.Database
	connector *mongoSQLConnector
	closed    bool
}

func (c *mongoSQLConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("MongoDB adapter does not support prepared statements; use QueryContext parameters")
}
func (c *mongoSQLConn) Close() error {
	if c.closed {
		return nil
	}
	c.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if c.connector != nil {
		return c.connector.release(ctx, c.client)
	}
	return c.client.Disconnect(ctx)
}
func (c *mongoSQLConn) Begin() (driver.Tx, error) {
	return nil, errors.New("MongoDB SQL adapter does not expose transactions")
}
func (c *mongoSQLConn) Ping(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return c.client.Ping(ctx, readpref.Primary())
}
func (c *mongoSQLConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return nil, errMongoReadOnly
}
func (c *mongoSQLConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	stmt, err := sqlparser.NewTestParser().Parse(strings.TrimSpace(query))
	if err != nil {
		return nil, fmt.Errorf("MongoDB SQL parse failed: %w", err)
	}
	selectStmt, ok := stmt.(*sqlparser.Select)
	if !ok {
		return nil, errMongoReadOnly
	}
	argIndex := new(int)
	plan, err := compileMongoSelect(selectStmt, args, argIndex)
	if err != nil {
		return nil, err
	}
	if *argIndex != len(args) {
		return nil, fmt.Errorf("MongoDB query received %d unused SQL arguments", len(args)-*argIndex)
	}
	return c.execute(ctx, plan)
}

type mongoProjection struct{ source, output string }
type mongoSelectPlan struct {
	collection string
	filter     bson.D
	projection []mongoProjection
	allColumns bool
	sort       bson.D
	skip       int64
	limit      int64
	hasLimit   bool
	count      bool
	countAlias string
	countInner *mongoSelectPlan
}
type mongoCondition struct {
	field       string
	op          sqlparser.ComparisonExprOperator
	value       interface{}
	values      []interface{}
	isNull      int
	alwaysFalse bool
}

func compileMongoSelect(stmt *sqlparser.Select, args []driver.NamedValue, argIndex *int) (*mongoSelectPlan, error) {
	if stmt == nil || stmt.With != nil || stmt.Distinct || stmt.GroupBy != nil || stmt.Having != nil ||
		stmt.Lock != sqlparser.NoLock || stmt.Into != nil || stmt.SQLCalcFoundRows || stmt.HighPriority ||
		stmt.StraightJoinHint || stmt.SQLSmallResult || stmt.SQLBigResult || stmt.SQLBufferResult ||
		stmt.Cache != nil || len(stmt.Windows) > 0 {
		return nil, errors.New("MongoDB SQL supports plain SELECT, WHERE, ORDER BY, LIMIT and OFFSET only")
	}
	if len(stmt.From) != 1 {
		return nil, errors.New("MongoDB SQL queries must reference one collection")
	}
	from, ok := stmt.From[0].(*sqlparser.AliasedTableExpr)
	if !ok || len(from.Partitions) > 0 || len(from.Hints) > 0 {
		return nil, errors.New("MongoDB SQL supports one collection reference or derived SELECT only")
	}
	qualifier := from.As.String()
	if source, ok := from.Expr.(sqlparser.TableName); ok {
		if qualifier == "" {
			qualifier = source.Name.String()
		}
	}
	count, countAlias, all, projections, err := mongoSelectList(stmt.SelectExprs, qualifier)
	if err != nil {
		return nil, err
	}

	switch table := from.Expr.(type) {
	case sqlparser.TableName:
		if table.Qualifier.String() != "" || table.Name.String() == "" {
			return nil, errors.New("MongoDB SQL collection names must be unqualified; the data source selects the database")
		}
		plan := &mongoSelectPlan{
			collection: table.Name.String(), filter: bson.D{},
			allColumns: all, projection: projections,
		}
		if count {
			plan.count = true
			plan.countAlias = countAlias
		}
		if stmt.Where != nil {
			conditions, err := mongoWhereConditions(stmt.Where.Expr, args, argIndex, qualifier)
			if err != nil {
				return nil, err
			}
			plan.filter = mongoFilter(conditions)
		}
		if err := mongoOrderBy(stmt.OrderBy, plan, qualifier); err != nil {
			return nil, err
		}
		if err := mongoPagination(stmt.Limit, args, argIndex, plan); err != nil {
			return nil, err
		}
		return plan, nil

	case *sqlparser.DerivedTable:
		inner, ok := table.Select.(*sqlparser.Select)
		if !ok {
			return nil, errors.New("MongoDB SQL derived tables must contain a SELECT")
		}
		innerPlan, err := compileMongoSelect(inner, args, argIndex)
		if err != nil {
			return nil, err
		}
		if count {
			plan := &mongoSelectPlan{
				collection: innerPlan.collection, filter: innerPlan.filter,
				count: true, countAlias: countAlias, countInner: innerPlan,
			}
			if stmt.Where != nil || len(stmt.OrderBy) > 0 {
				return nil, errors.New("MongoDB SQL count wrappers do not accept outer WHERE or ORDER BY clauses")
			}
			if err := mongoPagination(stmt.Limit, args, argIndex, plan); err != nil {
				return nil, err
			}
			return plan, nil
		}
		if !all || len(projections) > 0 {
			return nil, errors.New("MongoDB SQL derived tables support SELECT * or COUNT(*) only")
		}
		if stmt.Where != nil || len(stmt.OrderBy) > 0 {
			return nil, errors.New("MongoDB SQL pagination wrappers do not accept outer WHERE or ORDER BY clauses")
		}
		if err := composeMongoPagination(stmt.Limit, args, argIndex, innerPlan); err != nil {
			return nil, err
		}
		return innerPlan, nil

	default:
		return nil, errors.New("MongoDB SQL supports a collection name or derived SELECT only")
	}
}

func mongoSelectList(selectExprs *sqlparser.SelectExprs, allowedQualifier string) (count bool, countAlias string, all bool, projections []mongoProjection, err error) {
	if selectExprs == nil {
		return false, "", false, nil, errors.New("MongoDB SQL SELECT list is required")
	}
	exprs := selectExprs.Exprs
	if len(exprs) == 1 {
		if aliased, ok := exprs[0].(*sqlparser.AliasedExpr); ok && strings.EqualFold(strings.ReplaceAll(sqlparser.String(aliased.Expr), " ", ""), "count(*)") {
			return true, aliased.As.String(), false, nil, nil
		}
		if _, ok := exprs[0].(*sqlparser.StarExpr); ok {
			return false, "", true, nil, nil
		}
	}
	for _, selectExpr := range exprs {
		aliased, ok := selectExpr.(*sqlparser.AliasedExpr)
		if !ok {
			return false, "", false, nil, errors.New("MongoDB SQL projection supports column names and * only")
		}
		column, ok := aliased.Expr.(*sqlparser.ColName)
		if !ok {
			return false, "", false, nil, errors.New("MongoDB SQL projection supports column names and * only")
		}
		name, err := mongoColumnName(column, allowedQualifier)
		if err != nil {
			return false, "", false, nil, err
		}
		alias := aliased.As.String()
		if alias == "" {
			alias = name
		}
		projections = append(projections, mongoProjection{source: name, output: alias})
	}
	if len(projections) == 0 {
		return false, "", false, nil, errors.New("MongoDB SQL SELECT list is required")
	}
	return false, "", false, projections, nil
}

func mongoWhereConditions(expr sqlparser.Expr, args []driver.NamedValue, argIndex *int, allowedQualifier string) ([]mongoCondition, error) {
	var conditions []mongoCondition
	if err := collectMongoConditions(expr, args, argIndex, allowedQualifier, &conditions); err != nil {
		return nil, err
	}
	return conditions, nil
}

func collectMongoConditions(expr sqlparser.Expr, args []driver.NamedValue, argIndex *int, allowedQualifier string, conditions *[]mongoCondition) error {
	switch value := expr.(type) {
	case *sqlparser.AndExpr:
		if err := collectMongoConditions(value.Left, args, argIndex, allowedQualifier, conditions); err != nil {
			return err
		}
		return collectMongoConditions(value.Right, args, argIndex, allowedQualifier, conditions)
	case *sqlparser.ComparisonExpr:
		column, ok := value.Left.(*sqlparser.ColName)
		if !ok {
			return errors.New("MongoDB SQL WHERE comparisons require a column on the left")
		}
		field, err := mongoColumnName(column, allowedQualifier)
		if err != nil {
			return err
		}
		condition := mongoCondition{field: field, op: value.Operator}
		switch condition.op {
		case sqlparser.EqualOp, sqlparser.NotEqualOp, sqlparser.LessThanOp, sqlparser.LessEqualOp,
			sqlparser.GreaterThanOp, sqlparser.GreaterEqualOp, sqlparser.InOp, sqlparser.NotInOp,
			sqlparser.LikeOp, sqlparser.NotLikeOp:
		default:
			return fmt.Errorf("MongoDB SQL comparison operator %v is not supported", condition.op)
		}
		if condition.field == "" {
			return errors.New("MongoDB SQL WHERE column is required")
		}
		if tuple, ok := value.Right.(sqlparser.ValTuple); ok {
			if value.Operator != sqlparser.InOp && value.Operator != sqlparser.NotInOp {
				return errors.New("MongoDB SQL tuple values are supported only with IN and NOT IN")
			}
			for _, item := range tuple {
				parsed, err := mongoSQLValue(item, args, argIndex)
				if err != nil {
					return err
				}
				if parsed == nil {
					return errors.New("MongoDB SQL IN lists cannot contain NULL")
				}
				condition.values = append(condition.values, parsed)
			}
		} else {
			if value.Operator == sqlparser.InOp || value.Operator == sqlparser.NotInOp {
				return errors.New("MongoDB SQL IN and NOT IN require a parenthesized list of values")
			}
			parsed, err := mongoSQLValue(value.Right, args, argIndex)
			if err != nil {
				return err
			}
			condition.value = parsed
			condition.alwaysFalse = parsed == nil
		}
		*conditions = append(*conditions, condition)
		return nil
	case *sqlparser.IsExpr:
		column, ok := value.Left.(*sqlparser.ColName)
		if !ok {
			return errors.New("MongoDB SQL IS NULL filters require a column on the left")
		}
		field, err := mongoColumnName(column, allowedQualifier)
		if err != nil {
			return err
		}
		switch value.Right {
		case sqlparser.IsNullOp:
			*conditions = append(*conditions, mongoCondition{field: field, isNull: 1})
		case sqlparser.IsNotNullOp:
			*conditions = append(*conditions, mongoCondition{field: field, isNull: -1})
		default:
			return errors.New("MongoDB SQL supports IS NULL and IS NOT NULL only")
		}
		return nil
	default:
		return errors.New("MongoDB SQL WHERE supports AND-combined comparisons and NULL checks only")
	}
}

func mongoSQLValue(expr sqlparser.Expr, args []driver.NamedValue, argIndex *int) (interface{}, error) {
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
			return nil, fmt.Errorf("MongoDB SQL does not support literal type %s", sqlparser.String(value))
		}
	case *sqlparser.NullVal:
		return nil, nil
	case sqlparser.BoolVal:
		return bool(value), nil
	case *sqlparser.Argument:
		if *argIndex >= len(args) {
			return nil, fmt.Errorf("missing SQL argument %q", value.Name)
		}
		arg := args[*argIndex]
		(*argIndex)++
		return arg.Value, nil
	case *sqlparser.UnaryExpr:
		operand, err := mongoSQLValue(value.Expr, args, argIndex)
		if err != nil {
			return nil, err
		}
		switch value.Operator {
		case sqlparser.UMinusOp:
			switch number := operand.(type) {
			case int64:
				return -number, nil
			case float64:
				return -number, nil
			}
		case sqlparser.UPlusOp:
			return operand, nil
		}
		return nil, errors.New("MongoDB SQL supports numeric unary signs only")
	default:
		return nil, fmt.Errorf("MongoDB SQL does not support value expression %T", expr)
	}
}

func mongoColumnName(column *sqlparser.ColName, allowedQualifier string) (string, error) {
	if column == nil || column.Name.String() == "" {
		return "", errors.New("MongoDB SQL column is required")
	}
	qualifier := column.Qualifier.Name.String()
	if qualifier != "" && !strings.EqualFold(qualifier, allowedQualifier) {
		return "", fmt.Errorf("MongoDB SQL column qualifier %q does not match collection %q", qualifier, allowedQualifier)
	}
	return column.Name.String(), nil
}

func mongoFalseFilter() bson.D {
	return bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{1, 0}}}}}
}

func mongoFilter(conditions []mongoCondition) bson.D {
	if len(conditions) == 0 {
		return bson.D{}
	}
	clauses := make(bson.A, 0, len(conditions))
	for _, condition := range conditions {
		if condition.alwaysFalse {
			clauses = append(clauses, mongoFalseFilter())
			continue
		}
		var predicate interface{}
		switch {
		case condition.isNull > 0:
			predicate = bson.D{{Key: condition.field, Value: nil}}
		case condition.isNull < 0:
			predicate = bson.D{{Key: condition.field, Value: bson.D{{Key: "$exists", Value: true}, {Key: "$ne", Value: nil}}}}
		case condition.op == sqlparser.InOp:
			predicate = bson.D{{Key: condition.field, Value: bson.D{{Key: "$in", Value: bson.A(condition.values)}}}}
		case condition.op == sqlparser.NotInOp:
			predicate = bson.D{{Key: condition.field, Value: bson.D{{Key: "$exists", Value: true}, {Key: "$nin", Value: bson.A(condition.values)}}}}
		case condition.op == sqlparser.LikeOp || condition.op == sqlparser.NotLikeOp:
			pattern := regexp.QuoteMeta(fmt.Sprint(condition.value))
			pattern = strings.ReplaceAll(strings.ReplaceAll(pattern, "%", ".*"), "_", ".")
			regex := primitive.Regex{Pattern: "^" + pattern + "$", Options: "s"}
			if condition.op == sqlparser.NotLikeOp {
				predicate = bson.D{{Key: condition.field, Value: bson.D{{Key: "$exists", Value: true}, {Key: "$not", Value: regex}}}}
			} else {
				predicate = bson.D{{Key: condition.field, Value: regex}}
			}
		default:
			operator := ""
			switch condition.op {
			case sqlparser.EqualOp:
			case sqlparser.NotEqualOp:
				operator = "$ne"
			case sqlparser.LessThanOp:
				operator = "$lt"
			case sqlparser.LessEqualOp:
				operator = "$lte"
			case sqlparser.GreaterThanOp:
				operator = "$gt"
			case sqlparser.GreaterEqualOp:
				operator = "$gte"
			default:
				clauses = append(clauses, mongoFalseFilter())
				continue
			}
			if operator == "" {
				predicate = bson.D{{Key: condition.field, Value: condition.value}}
			} else {
				predicate = bson.D{{Key: condition.field, Value: bson.D{{Key: operator, Value: condition.value}}}}
			}
		}
		clauses = append(clauses, predicate)
	}
	if len(clauses) == 0 {
		return mongoFalseFilter()
	}
	if len(clauses) == 1 {
		if filter, ok := clauses[0].(bson.D); ok {
			return filter
		}
	}
	return bson.D{{Key: "$and", Value: clauses}}
}

func mongoOrderBy(orderBy sqlparser.OrderBy, plan *mongoSelectPlan, allowedQualifier string) error {
	for _, order := range orderBy {
		column, ok := order.Expr.(*sqlparser.ColName)
		if !ok {
			return errors.New("MongoDB SQL ORDER BY supports column names only")
		}
		field, err := mongoColumnName(column, allowedQualifier)
		if err != nil {
			return err
		}
		for _, projection := range plan.projection {
			if strings.EqualFold(field, projection.output) {
				field = projection.source
				break
			}
		}
		direction := int32(1)
		if order.Direction == sqlparser.DescOrder {
			direction = -1
		}
		plan.sort = append(plan.sort, bson.E{Key: field, Value: direction})
	}
	return nil
}

func mongoPagination(limit *sqlparser.Limit, args []driver.NamedValue, argIndex *int, plan *mongoSelectPlan) error {
	if limit == nil {
		return nil
	}
	if limit.Rowcount != nil {
		rowCount, err := mongoNonNegativeInt(limit.Rowcount, args, argIndex)
		if err != nil {
			return err
		}
		plan.limit, plan.hasLimit = rowCount, true
	}
	if limit.Offset != nil {
		offset, err := mongoNonNegativeInt(limit.Offset, args, argIndex)
		if err != nil {
			return err
		}
		plan.skip = offset
	}
	return nil
}

func composeMongoPagination(limit *sqlparser.Limit, args []driver.NamedValue, argIndex *int, plan *mongoSelectPlan) error {
	outer := &mongoSelectPlan{}
	if err := mongoPagination(limit, args, argIndex, outer); err != nil {
		return err
	}
	if outer.skip > math.MaxInt64-plan.skip {
		return errors.New("MongoDB SQL OFFSET exceeds the supported range")
	}
	plan.skip += outer.skip
	if outer.hasLimit {
		remaining := outer.limit
		if plan.hasLimit {
			if outer.skip >= plan.limit {
				remaining = 0
			} else if innerRemaining := plan.limit - outer.skip; remaining > innerRemaining {
				remaining = innerRemaining
			}
		}
		plan.limit, plan.hasLimit = remaining, true
	} else if plan.hasLimit && outer.skip > 0 {
		if outer.skip >= plan.limit {
			plan.limit = 0
		} else {
			plan.limit -= outer.skip
		}
	}
	return nil
}

func mongoNonNegativeInt(expr sqlparser.Expr, args []driver.NamedValue, argIndex *int) (int64, error) {
	value, err := mongoSQLValue(expr, args, argIndex)
	if err != nil {
		return 0, err
	}
	number, ok := value.(int64)
	if !ok || number < 0 {
		return 0, errors.New("MongoDB SQL LIMIT and OFFSET must be non-negative integers")
	}
	return number, nil
}

func (c *mongoSQLConn) execute(ctx context.Context, plan *mongoSelectPlan) (driver.Rows, error) {
	if plan.count {
		if plan.skip > 0 || (plan.hasLimit && plan.limit == 0) {
			return &mongoSQLRows{ctx: ctx, columns: []string{mongoCountColumn(plan)}, types: []string{"BIGINT"}}, nil
		}
		count, err := c.countPlan(ctx, plan)
		if err != nil {
			return nil, err
		}
		rows := [][]driver.Value{}
		if plan.skip == 0 && (!plan.hasLimit || plan.limit > 0) {
			rows = append(rows, []driver.Value{count})
		}
		return &mongoSQLRows{ctx: ctx, columns: []string{mongoCountColumn(plan)}, types: []string{"BIGINT"}, countRows: rows}, nil
	}
	if plan.hasLimit && plan.limit == 0 {
		columns := mongoPlanColumns(plan, nil)
		types := make([]string, len(columns))
		for i := range types {
			types[i] = "UNKNOWN"
		}
		return &mongoSQLRows{ctx: ctx, columns: columns, types: types}, nil
	}
	findOptions := options.Find()
	if plan.skip > 0 {
		findOptions.SetSkip(plan.skip)
	}
	if plan.hasLimit {
		findOptions.SetLimit(plan.limit)
	}
	if len(plan.sort) > 0 {
		findOptions.SetSort(plan.sort)
	}
	if !plan.allColumns && len(plan.projection) > 0 {
		findOptions.SetProjection(mongoProjectionDocument(plan.projection))
	}
	cursor, err := c.database.Collection(plan.collection).Find(ctx, plan.filter, findOptions)
	if err != nil {
		return nil, fmt.Errorf("MongoDB find failed: %w", err)
	}
	rows := &mongoSQLRows{ctx: ctx, cursor: cursor, projections: append([]mongoProjection(nil), plan.projection...), columns: mongoPlanColumns(plan, nil)}
	if cursor.Next(ctx) {
		var first bson.D
		if err := cursor.Decode(&first); err != nil {
			_ = cursor.Close(context.Background())
			return nil, fmt.Errorf("decode MongoDB document: %w", err)
		}
		rows.pending, rows.hasPending = first, true
		if plan.allColumns {
			rows.columns = mongoDocumentColumns(first)
			rows.projections = mongoIdentityProjections(rows.columns)
		}
	} else if err := cursor.Err(); err != nil {
		_ = cursor.Close(context.Background())
		return nil, fmt.Errorf("read MongoDB cursor: %w", err)
	}
	rows.types = mongoColumnTypes(rows.columns, rows.projections, rows.pending)
	return rows, nil
}

func mongoCountColumn(plan *mongoSelectPlan) string {
	if plan.countAlias != "" {
		return plan.countAlias
	}
	return "count(*)"
}

func (c *mongoSQLConn) countPlan(ctx context.Context, plan *mongoSelectPlan) (int64, error) {
	countPlan := plan
	if plan.countInner != nil {
		countPlan = plan.countInner
		if countPlan.count {
			if countPlan.skip > 0 || (countPlan.hasLimit && countPlan.limit == 0) {
				return 0, nil
			}
			return 1, nil
		}
	}
	countOptions := options.Count()
	if plan.countInner != nil {
		if countPlan.skip > 0 {
			countOptions.SetSkip(countPlan.skip)
		}
		if countPlan.hasLimit {
			if countPlan.limit == 0 {
				return 0, nil
			}
			countOptions.SetLimit(countPlan.limit)
		}
	}
	count, err := c.database.Collection(countPlan.collection).CountDocuments(ctx, countPlan.filter, countOptions)
	if err != nil {
		return 0, fmt.Errorf("MongoDB count failed: %w", err)
	}
	return count, nil
}

func mongoPlanColumns(plan *mongoSelectPlan, doc bson.D) []string {
	if plan.count {
		return []string{mongoCountColumn(plan)}
	}
	if plan.allColumns {
		return mongoDocumentColumns(doc)
	}
	columns := make([]string, len(plan.projection))
	for i, projection := range plan.projection {
		columns[i] = projection.output
	}
	return columns
}

func mongoProjectionDocument(projections []mongoProjection) bson.D {
	projection := make(bson.D, 0, len(projections)+1)
	seen := make(map[string]struct{}, len(projections))
	includeID := false
	for _, column := range projections {
		if _, exists := seen[column.source]; exists {
			continue
		}
		seen[column.source] = struct{}{}
		projection = append(projection, bson.E{Key: column.source, Value: 1})
		if column.source == "_id" {
			includeID = true
		}
	}
	if !includeID {
		projection = append(projection, bson.E{Key: "_id", Value: 0})
	}
	return projection
}

type mongoSQLRows struct {
	ctx         context.Context
	cursor      *mongo.Cursor
	columns     []string
	types       []string
	projections []mongoProjection
	pending     bson.D
	hasPending  bool
	countRows   [][]driver.Value
	countIndex  int
}

func (r *mongoSQLRows) Columns() []string { return r.columns }
func (r *mongoSQLRows) Close() error {
	if r.cursor == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return r.cursor.Close(ctx)
}
func (r *mongoSQLRows) ColumnTypeDatabaseTypeName(index int) string {
	if index < 0 || index >= len(r.types) {
		return "UNKNOWN"
	}
	return r.types[index]
}
func (r *mongoSQLRows) ColumnTypeNullable(int) (nullable, ok bool) { return true, true }
func (r *mongoSQLRows) Next(dest []driver.Value) error {
	if err := r.ctx.Err(); err != nil {
		return err
	}
	if r.countRows != nil {
		if r.countIndex >= len(r.countRows) {
			return io.EOF
		}
		copy(dest, r.countRows[r.countIndex])
		r.countIndex++
		return nil
	}
	var doc bson.D
	if r.hasPending {
		doc, r.hasPending = r.pending, false
	} else {
		if r.cursor == nil || !r.cursor.Next(r.ctx) {
			if r.cursor != nil && r.cursor.Err() != nil {
				return r.cursor.Err()
			}
			return io.EOF
		}
		if err := r.cursor.Decode(&doc); err != nil {
			return err
		}
	}
	for i, projection := range r.projections {
		if i >= len(dest) {
			break
		}
		value, _ := mongoDocumentPathValue(doc, projection.source)
		dest[i] = mongoDriverValue(value)
	}
	return nil
}

func mongoDocumentColumns(doc bson.D) []string {
	columns := make([]string, len(doc))
	for i, field := range doc {
		columns[i] = field.Key
	}
	return columns
}

func mongoIdentityProjections(columns []string) []mongoProjection {
	projections := make([]mongoProjection, len(columns))
	for i, column := range columns {
		projections[i] = mongoProjection{source: column, output: column}
	}
	return projections
}

func mongoColumnTypes(columns []string, projections []mongoProjection, doc bson.D) []string {
	types := make([]string, len(columns))
	for i := range columns {
		types[i] = "UNKNOWN"
		if i < len(projections) {
			if value, ok := mongoDocumentPathValue(doc, projections[i].source); ok {
				types[i] = mongoDatabaseType(value)
			}
		}
	}
	return types
}

func mongoDatabaseType(value interface{}) string {
	switch value.(type) {
	case nil:
		return "UNKNOWN"
	case primitive.Decimal128:
		return "DECIMAL"
	case int32:
		return "INTEGER"
	case int64, int:
		return "BIGINT"
	case float32, float64:
		return "DOUBLE"
	case bool:
		return "BOOLEAN"
	case time.Time, primitive.DateTime:
		return "TIMESTAMP"
	case []byte, primitive.Binary:
		return "VARBINARY"
	case bson.D, bson.M, bson.A:
		return "JSON"
	default:
		return "VARCHAR"
	}
}

func mongoDocumentPathValue(doc bson.D, path string) (interface{}, bool) {
	var current interface{} = doc
	for _, part := range strings.Split(path, ".") {
		found := false
		switch value := current.(type) {
		case bson.D:
			for _, item := range value {
				if item.Key == part {
					current, found = item.Value, true
					break
				}
			}
		case bson.M:
			current, found = value[part]
		}
		if !found {
			return nil, false
		}
	}
	return current, true
}

func mongoDriverValue(value interface{}) driver.Value {
	switch v := value.(type) {
	case nil:
		return nil
	case int:
		return int64(v)
	case int8:
		return int64(v)
	case int16:
		return int64(v)
	case int32:
		return int64(v)
	case int64, float64, bool, string, []byte, time.Time:
		return v
	case float32:
		return float64(v)
	case primitive.DateTime:
		return v.Time()
	case primitive.ObjectID:
		return v.Hex()
	case primitive.Decimal128:
		return v.String()
	case primitive.Binary:
		return append([]byte(nil), v.Data...)
	default:
		encoded, err := bson.MarshalExtJSON(v, false, false)
		if err != nil {
			return fmt.Sprint(v)
		}
		return string(encoded)
	}
}
