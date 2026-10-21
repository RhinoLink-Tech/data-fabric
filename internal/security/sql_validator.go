package security

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/sqlanalysis"

	"vitess.io/vitess/go/vt/sqlparser"
)

var (
	ErrNotSelectQuery        = errors.New("only SELECT queries are allowed")
	ErrSQLSyntaxError        = errors.New("SQL syntax error")
	ErrDangerousKeyword      = errors.New("dangerous SQL keyword detected")
	ErrSQLInjection          = errors.New("potential SQL injection detected")
	ErrEmptyQuery            = errors.New("query cannot be empty")
	ErrQueryTooLong          = errors.New("query exceeds maximum length")
	ErrUnsupportedSQLDialect = errors.New("SQL dialect is not supported for read-only validation")
)

// SQLASTAnalyzer parses a statement using a concrete SQL dialect and returns
// security-relevant AST facts.
type SQLASTAnalyzer interface {
	AnalyzeSQLContext(context.Context, string, string) (sqlanalysis.Analysis, error)
}

// SQLValidator validates SQL queries for security
type SQLValidator struct {
	maxQueryLength int
	astAnalyzer    SQLASTAnalyzer
}

// NewSQLValidator creates a new SQLValidator instance
func NewSQLValidator(maxQueryLength int) *SQLValidator {
	if maxQueryLength <= 0 {
		maxQueryLength = 10000 // Default max query length
	}
	return &SQLValidator{
		maxQueryLength: maxQueryLength,
	}
}

// ValidateStatement validates that the SQL statement is safe and read-only
func (sv *SQLValidator) ValidateStatement(sql string) error {
	return sv.validateStatement(sql, false)
}

// ValidateStatementForDataSource validates a read-only statement using the
// identifier forms supported by the target data source.
func (sv *SQLValidator) ValidateStatementForDataSource(sql string, dbType model.DatabaseType) error {
	return sv.ValidateStatementForDataSourceContext(context.Background(), sql, dbType)
}

// SetASTAnalyzer configures the dialect-aware parser used for data-source
// validation. A nil analyzer keeps the legacy parser for isolated callers.
func (sv *SQLValidator) SetASTAnalyzer(analyzer SQLASTAnalyzer) {
	sv.astAnalyzer = analyzer
}

// SupportsASTAnalysis reports whether this validator can return table
// references from a dialect-aware parser.
func (sv *SQLValidator) SupportsASTAnalysis() bool {
	return sv != nil && sv.astAnalyzer != nil
}

// ValidateStatementForDataSourceContext validates one read-only query using the
// target source dialect and respects request cancellation and parser timeouts.
func (sv *SQLValidator) ValidateStatementForDataSourceContext(ctx context.Context, sql string, dbType model.DatabaseType) error {
	if sv.astAnalyzer != nil {
		if _, ok := SQLDialectForDataSource(dbType); !ok {
			return ErrUnsupportedSQLDialect
		}
		// The in-process Vitess parser covers common read-only SQL without
		// starting Python for every request. Keep SQLGlot as the dialect-aware
		// fallback for syntax the fast parser cannot parse; table authorization
		// still requests full AST facts explicitly.
		if err := sv.validateStatement(sql, supportsThreePartCatalogNames(dbType)); err == nil {
			return nil
		} else if errors.Is(err, ErrEmptyQuery) || errors.Is(err, ErrQueryTooLong) || errors.Is(err, ErrNotSelectQuery) {
			// These results are definitive: the input is empty/too large, or the
			// parser identified a non-query statement. Text heuristics can have
			// dialect-specific false positives, so let SQLGlot check those.
			return err
		}
		return sv.validateStatementWithAST(ctx, sql, dbType)
	}
	return sv.validateStatement(sql, supportsThreePartCatalogNames(dbType))
}

func (sv *SQLValidator) validateStatementWithAST(ctx context.Context, sql string, dbType model.DatabaseType) error {
	_, err := sv.AnalyzeStatementForDataSourceContext(ctx, sql, dbType)
	return err
}

// AnalyzeStatementForDataSourceContext validates a read-only statement and
// returns the same dialect-aware AST facts used for table authorization.
func (sv *SQLValidator) AnalyzeStatementForDataSourceContext(ctx context.Context, sql string, dbType model.DatabaseType) (sqlanalysis.Analysis, error) {
	if sv.astAnalyzer == nil {
		return sqlanalysis.Analysis{}, ErrUnsupportedSQLDialect
	}
	if err := sv.basicValidation(sql); err != nil {
		return sqlanalysis.Analysis{}, err
	}
	dialect, ok := SQLDialectForDataSource(dbType)
	if !ok {
		return sqlanalysis.Analysis{}, ErrUnsupportedSQLDialect
	}

	analysis, err := sv.astAnalyzer.AnalyzeSQLContext(ctx, sv.normalizeSQL(sql), dialect)
	if err != nil {
		if ctx.Err() != nil {
			return sqlanalysis.Analysis{}, ctx.Err()
		}
		return sqlanalysis.Analysis{}, ErrSQLSyntaxError
	}
	if analysis.StatementCount != 1 {
		return sqlanalysis.Analysis{}, ErrSQLSyntaxError
	}
	if !analysis.IsQuery || analysis.HasMutation {
		return sqlanalysis.Analysis{}, ErrNotSelectQuery
	}
	if analysis.HasSelectInto || analysis.HasLocking {
		return sqlanalysis.Analysis{}, ErrDangerousKeyword
	}

	dangerousFunctions := map[string]struct{}{
		"LOAD_FILE": {}, "PG_READ_FILE": {}, "PG_READ_BINARY_FILE": {}, "PG_LS_DIR": {},
		"LO_IMPORT": {}, "LO_EXPORT": {}, "DBLINK": {}, "DBLINK_CONNECT": {}, "PG_SLEEP": {},
		"PG_ADVISORY_LOCK": {}, "PG_ADVISORY_XACT_LOCK": {}, "PG_ADVISORY_UNLOCK": {},
		"PG_ADVISORY_UNLOCK_ALL": {}, "PG_NOTIFY": {}, "PG_TERMINATE_BACKEND": {},
		"PG_CANCEL_BACKEND": {}, "PG_CREATE_RESTORE_POINT": {}, "NEXTVAL": {}, "SETVAL": {},
		"SLEEP": {}, "BENCHMARK": {}, "GET_LOCK": {}, "RELEASE_LOCK": {}, "RELEASE_ALL_LOCKS": {},
		"SYS_EXEC": {}, "XP_CMDSHELL": {}, "XP_READERRORLOG": {}, "SP_OACREATE": {},
		"OPENROWSET": {}, "OPENQUERY": {}, "UTL_FILE": {}, "UTL_HTTP": {}, "UTL_INADDR": {},
		"DBMS_SCHEDULER": {}, "DBMS_LOCK.SLEEP": {},
	}
	for _, function := range analysis.Functions {
		if _, denied := dangerousFunctions[strings.ToUpper(function)]; denied {
			return sqlanalysis.Analysis{}, ErrDangerousKeyword
		}
	}
	return analysis, nil
}

// SQLDialectForDataSource maps connector types to the SQLGlot grammar used for
// parsing. Compatibility engines share their closest supported SQL grammar.
func SQLDialectForDataSource(dbType model.DatabaseType) (string, bool) {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB, model.DatabaseTypeTiDB,
		model.DatabaseTypeOceanBaseMySQL, model.DatabaseTypeTDSQL, model.DatabaseTypeGaussDBMySQL,
		model.DatabaseTypeGBase8s, model.DatabaseTypeGBase8t, model.DatabaseTypeMongoDB:
		return "mysql", true
	case model.DatabaseTypePostgreSQL, model.DatabaseTypeGaussDBPostgres, model.DatabaseTypeKingbaseES,
		model.DatabaseTypeOpenGauss, model.DatabaseTypeHologres:
		return "postgres", true
	case model.DatabaseTypeOracle, model.DatabaseTypeOceanBaseOracle, model.DatabaseTypeDaMeng,
		model.DatabaseTypeOscar:
		return "oracle", true
	case model.DatabaseTypeTrino:
		return "trino", true
	case model.DatabaseTypePresto, model.DatabaseTypeOpenLooKeng:
		return "presto", true
	case model.DatabaseTypeSQLServer:
		return "sqlserver", true
	case model.DatabaseTypeSnowflake:
		return "snowflake", true
	case model.DatabaseTypeDatabricks:
		return "databricks", true
	case model.DatabaseTypeRedshift:
		return "redshift", true
	case model.DatabaseTypeBigQuery:
		return "bigquery", true
	case model.DatabaseTypeApacheHive:
		return "hive", true
	case model.DatabaseTypeMaxCompute:
		return "maxcompute", true
	case model.DatabaseTypeClickHouse:
		return "clickhouse", true
	case model.DatabaseTypeApacheDoris:
		return "doris", true
	case model.DatabaseTypeStarRocks:
		return "starrocks", true
	case model.DatabaseTypeApacheDruid:
		return "druid", true
	case model.DatabaseTypeApacheIceberg, model.DatabaseTypeDeltaLake, model.DatabaseTypeApacheHudi,
		model.DatabaseTypeOSSDelta, model.DatabaseTypeMinIOIceberg, model.DatabaseTypeAzureDelta,
		model.DatabaseTypeMinIODelta:
		return "spark", true
	case model.DatabaseTypeS3Parquet, model.DatabaseTypeS3ORC, model.DatabaseTypeS3Avro,
		model.DatabaseTypeS3CSV, model.DatabaseTypeS3JSON, model.DatabaseTypeMinIOParquet,
		model.DatabaseTypeMinIOCSV, model.DatabaseTypeAlibabaOSSParquet, model.DatabaseTypeTencentCOSParquet,
		model.DatabaseTypeAzureBlobParquet, model.DatabaseTypeAzureParquet,
		model.DatabaseTypeOzoneParquet, model.DatabaseTypeOzoneText, model.DatabaseTypeOzoneAvro:
		return "mysql", true
	default:
		return "", false
	}
}

func (sv *SQLValidator) validateStatement(sql string, supportsThreePartCatalogNames bool) error {
	// Basic checks
	if err := sv.basicValidation(sql); err != nil {
		return err
	}

	// Normalize SQL first (removes trailing semicolon and whitespace)
	normalizedSQL := sv.normalizeSQL(sql)

	// Mask string literals before the text heuristics below run: the checks
	// judge SQL structure, and with literal contents replaced by a placeholder
	// they can no longer flag keywords, comments, or patterns that appear
	// inside what is user-controlled data (#19).
	maskedSQL := maskStringLiterals(normalizedSQL)

	// Parse SQL statement (use normalized SQL). Vitess accepts schema.table but
	// not Trino's catalog.schema.table form, so retry with a validation-only
	// copy that removes catalog prefixes. The SQL used for execution is intact.
	stmt, err := sv.parseSQL(normalizedSQL)
	if err != nil && supportsThreePartCatalogNames {
		validationSQL := stripCatalogPrefixes(normalizedSQL)
		if validationSQL != normalizedSQL {
			stmt, err = sv.parseSQL(validationSQL)
		}
	}
	if err != nil {
		return ErrSQLSyntaxError
	}

	// Check if it's a SELECT statement
	if !sv.isSelectStatement(stmt) {
		return ErrNotSelectQuery
	}

	// Check for dangerous keywords (masked SQL: keywords inside literals are
	// data, not structure)
	if err := sv.checkDangerousKeywords(maskedSQL); err != nil {
		return err
	}

	// Check for potential SQL injection patterns (masked SQL for the pattern
	// scan; the original for character-level signals)
	if err := sv.checkSQLInjection(normalizedSQL, maskedSQL); err != nil {
		return err
	}

	return nil
}

func supportsThreePartCatalogNames(dbType model.DatabaseType) bool {
	switch dbType {
	case model.DatabaseTypeTrino, model.DatabaseTypePresto, model.DatabaseTypeOpenLooKeng:
		return true
	default:
		return false
	}
}

// stripCatalogPrefixes returns a validation-only SQL copy with dotted
// identifier chains reduced to their final schema/table (or field) pair.
// Quoted strings are skipped, and quoted identifiers are treated as one
// component. For example, catalog.schema.table becomes schema.table.
func stripCatalogPrefixes(sql string) string {
	type token struct {
		start int
		ident bool
		dot   bool
	}

	tokens := make([]token, 0, len(sql)/4)
	for i := 0; i < len(sql); {
		c := sql[i]
		if isSQLSpace(c) {
			i++
			continue
		}

		if c == 39 {
			// String contents are not identifier paths.
			i++
			for i < len(sql) {
				if sql[i] == 92 && i+1 < len(sql) {
					i += 2
					continue
				}
				if sql[i] == 39 {
					if i+1 < len(sql) && sql[i+1] == 39 {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			tokens = append(tokens, token{start: i})
			continue
		}

		if c == 34 || c == 96 {
			quote := c
			start := i
			i++
			for i < len(sql) {
				if sql[i] == quote {
					if i+1 < len(sql) && sql[i+1] == quote {
						i += 2
						continue
					}
					i++
					break
				}
				i++
			}
			tokens = append(tokens, token{start: start, ident: true})
			continue
		}

		if isSQLIdentifierStart(c) {
			start := i
			i++
			for i < len(sql) && isSQLIdentifierPart(sql[i]) {
				i++
			}
			tokens = append(tokens, token{start: start, ident: true})
			continue
		}

		if c == '.' {
			tokens = append(tokens, token{start: i, dot: true})
		} else {
			tokens = append(tokens, token{start: i})
		}
		i++
	}

	var out strings.Builder
	lastWritten := 0
	for i := 0; i < len(tokens); {
		if !tokens[i].ident {
			i++
			continue
		}
		chain := []token{tokens[i]}
		j := i
		for j+2 < len(tokens) && tokens[j+1].dot && tokens[j+2].ident {
			chain = append(chain, tokens[j+2])
			j += 2
		}
		if len(chain) > 2 {
			keepFrom := chain[len(chain)-2].start
			chainStart := chain[0].start
			out.WriteString(sql[lastWritten:chainStart])
			lastWritten = keepFrom
			i = j + 1
			continue
		}
		i++
	}
	out.WriteString(sql[lastWritten:])
	return out.String()
}

func isSQLSpace(c byte) bool {
	return c == ' ' || c == 9 || c == 10 || c == 13 || c == 12
}

func isSQLIdentifierStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isSQLIdentifierPart(c byte) bool {
	return isSQLIdentifierStart(c) || c >= '0' && c <= '9' || c == '$'
}

// IsReadOnly checks if the SQL statement is read-only
func (sv *SQLValidator) IsReadOnly(sql string) (bool, error) {
	stmt, err := sv.parseSQL(sql)
	if err != nil {
		return false, ErrSQLSyntaxError
	}
	return sv.isSelectStatement(stmt), nil
}

// ParseSQL parses SQL statement using vitess parser
func (sv *SQLValidator) ParseSQL(sql string) (sqlparser.Statement, error) {
	return sv.parseSQL(sql)
}

// Private methods

func (sv *SQLValidator) basicValidation(sql string) error {
	// Check if query is empty
	if strings.TrimSpace(sql) == "" {
		return ErrEmptyQuery
	}

	// Check query length
	if len(sql) > sv.maxQueryLength {
		return ErrQueryTooLong
	}

	// Comment patterns are rejected in checkSQLInjection on literal-masked
	// SQL: a "--" inside a string literal is data, not a comment (#19).

	return nil
}

func (sv *SQLValidator) parseSQL(sql string) (sqlparser.Statement, error) {
	// Normalize the SQL string
	normalizedSQL := sv.normalizeSQL(sql)

	// Parse using vitess SQL parser
	parser := sqlparser.NewTestParser()
	return parser.Parse(normalizedSQL)
}

func (sv *SQLValidator) isSelectStatement(stmt sqlparser.Statement) bool {
	switch stmt.(type) {
	case *sqlparser.Select:
		return true
	case *sqlparser.Union:
		return true
	default:
		return false
	}
}

func (sv *SQLValidator) checkDangerousKeywords(sql string) error {
	// List of dangerous keywords that should not be in SELECT queries
	dangerousKeywords := []string{
		"DROP", "DELETE", "UPDATE", "INSERT", "CREATE", "ALTER",
		"TRUNCATE", "REPLACE", "MERGE", "GRANT", "REVOKE",
		"COMMIT", "ROLLBACK", "SAVEPOINT", "RELEASE", "TRANSACTION",
		"ATTACH", "DETACH", "COPY", "EXPORT", "IMPORT", "USE",
		"INTO", "LOAD_FILE", "INTO OUTFILE", "INTO DUMPFILE",
		"EXEC", "EXECUTE", "CALL", "DO",
		"FOR SHARE", "FOR KEY SHARE", "LOCK IN SHARE MODE",
	}

	// Functions with filesystem access, side effects, or outbound
	// connections that can be invoked from a SELECT (#20). Word boundaries
	// keep identifiers such as a dblink_id column from matching.
	dangerousFunctions := []string{
		"PG_READ_FILE", "PG_READ_BINARY_FILE", "PG_LS_DIR",
		"LO_IMPORT", "LO_EXPORT", "DBLINK", "DBLINK_CONNECT", "PG_SLEEP",
		"PG_ADVISORY_LOCK", "PG_ADVISORY_XACT_LOCK", "PG_ADVISORY_UNLOCK",
		"PG_ADVISORY_UNLOCK_ALL", "PG_NOTIFY", "PG_TERMINATE_BACKEND",
		"PG_CANCEL_BACKEND", "PG_CREATE_RESTORE_POINT", "NEXTVAL", "SETVAL",
		"SLEEP", "BENCHMARK", "GET_LOCK", "RELEASE_LOCK", "RELEASE_ALL_LOCKS",
		"SYS_EXEC", "XP_CMDSHELL", "XP_READERRORLOG", "SP_OACREATE",
		"OPENROWSET", "OPENQUERY", "UTL_FILE", "UTL_HTTP", "UTL_INADDR",
		"DBMS_SCHEDULER", "DBMS_LOCK[.]SLEEP",
	}

	// Convert to uppercase for comparison
	upperSQL := strings.ToUpper(sql)

	for _, keyword := range append(dangerousKeywords, dangerousFunctions...) {
		// Use word boundaries to avoid false positives
		pattern := `\b` + keyword + `\b`
		matched, _ := regexp.MatchString(pattern, upperSQL)
		if matched {
			return ErrDangerousKeyword
		}
	}

	return nil
}

// checkSQLInjection scans for injection-shaped structure. The pattern scan
// runs on maskedSQL — string literals replaced by "?" — so literal contents
// cannot look like comments, semicolons, or write statements (#19); sql keeps
// the original text for character-level signals.
func (sv *SQLValidator) checkSQLInjection(sql, maskedSQL string) error {
	// Check for suspicious patterns
	// Note: sql should already be normalized (trailing semicolon removed)
	suspiciousPatterns := []string{
		// SQL comment patterns (escaped: a bare "/*" would quantify the
		// slash and match everything, "*/" would be an invalid regex)
		"--", "\\/\\*", "\\*\\/",
		// Multiple statements (check for multiple semicolons - should not exist after normalization)
		";.*;",       // Multiple semicolons indicate multiple statements
		";\\s+[a-z]", // Content after semicolon (another statement)
		// Comparing two string literals ('1'='1') is the masked form of the
		// classic tautology probe; real columns never become placeholders.
		"\\?\\s*=\\s*\\?",
		// Or patterns with suspicious operators (injection patterns)
		"\\bor\\s+1\\s*=\\s*1\\b",  // OR 1=1 (injection pattern)
		"\\band\\s+1\\s*=\\s*1\\b", // AND 1=1 (injection pattern)
		// Common injection patterns
		"\\bor\\s+true\\b", "\\band\\s+true\\b",
		// Waitfor delays (common in injection testing)
		"waitfor\\s+delay",
		// Exec/Execute patterns (dangerous)
		"\\bexec\\s*\\(", "\\bexecute\\s*\\(",
		// Drop/Delete/Update/Insert patterns (should be caught by read-only check, but extra safety)
		"\\bdrop\\s+table\\b", "\\bdelete\\s+from\\b", "\\bupdate\\s+.*\\s+set\\b", "\\binsert\\s+into\\b",
	}

	lowerSQL := strings.ToLower(maskedSQL)

	for _, pattern := range suspiciousPatterns {
		matched, _ := regexp.MatchString(pattern, lowerSQL)
		if matched {
			return ErrSQLInjection
		}
	}

	// Check for unusual character sequences
	if sv.hasSuspiciousCharacters(sql, maskedSQL) {
		return ErrSQLInjection
	}

	return nil
}

// hasSuspiciousCharacters reports character-level anomalies. Non-printable
// bytes are counted on the original sql — control characters are suspicious
// even inside literals. The repeated-character heuristic runs on maskedSQL
// and letters only: runs like "000" in 1000 or 0.0001 are ordinary numeric
// literals, not obfuscation (#19).
func (sv *SQLValidator) hasSuspiciousCharacters(sql, maskedSQL string) bool {
	// Count non-printable characters
	suspiciousCount := 0
	for _, r := range sql {
		if !unicode.IsPrint(r) && !unicode.IsSpace(r) {
			suspiciousCount++
			if suspiciousCount > 5 {
				return true
			}
		}
	}

	// Check for repeated characters (might indicate obfuscation)
	// Only check for 3+ consecutive identical letters outside literals
	if len(maskedSQL) > 10 {
		for i := 0; i < len(maskedSQL)-2; i++ {
			char := maskedSQL[i]
			isLetter := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
			if isLetter &&
				maskedSQL[i] == maskedSQL[i+1] && maskedSQL[i+1] == maskedSQL[i+2] {
				return true
			}
		}
	}

	return false
}

func (sv *SQLValidator) normalizeSQL(sql string) string {
	// Remove leading/trailing whitespace
	sql = strings.TrimSpace(sql)

	// Remove trailing semicolon if present
	if strings.HasSuffix(sql, ";") {
		sql = strings.TrimSuffix(sql, ";")
		sql = strings.TrimSpace(sql)
	}

	return sql
}

// maskStringLiterals replaces every single-quoted string literal, including
// its delimiters, with a "?" placeholder. The validator's text heuristics
// judge SQL structure, and masked literals can no longer match keywords,
// comments, or injection patterns that live in user-controlled data (#19).
// Quoted identifiers ("…" / `…`) are left intact: they name objects, and
// masking them would make two different columns indistinguishable to the
// literal-comparison probe. Escapes follow the MySQL syntax the vitess
// parser behind the AST gate uses: ” doubling and backslash escapes.
func maskStringLiterals(sql string) string {
	hasLiteral := false
	for i := 0; i < len(sql); i++ {
		if sql[i] == '\'' {
			hasLiteral = true
			break
		}
	}
	if !hasLiteral {
		return sql
	}

	var out strings.Builder
	out.Grow(len(sql))
	for i := 0; i < len(sql); {
		if sql[i] != '\'' {
			out.WriteByte(sql[i])
			i++
			continue
		}
		// Skip the whole literal and emit one placeholder. An unterminated
		// literal consumes the rest of the input; the parser rejects the
		// original anyway.
		i++
		for i < len(sql) {
			if sql[i] == '\\' && i+1 < len(sql) {
				i += 2
				continue
			}
			if sql[i] == '\'' {
				if i+1 < len(sql) && sql[i+1] == '\'' {
					i += 2 // escaped quote inside the literal
					continue
				}
				i++ // closing delimiter
				break
			}
			i++
		}
		out.WriteByte('?')
	}
	return out.String()
}

// GetTableName extracts table name from SELECT statement
func (sv *SQLValidator) GetTableName(sql string) (string, error) {
	stmt, err := sv.parseSQL(sql)
	if err != nil {
		return "", err
	}

	selectStmt, ok := stmt.(*sqlparser.Select)
	if !ok {
		return "", fmt.Errorf("not a SELECT statement")
	}

	// Extract table name from FROM clause
	if selectStmt.From != nil {
		if tableExpr, ok := selectStmt.From[0].(*sqlparser.AliasedTableExpr); ok {
			if tableName, ok := tableExpr.Expr.(*sqlparser.TableName); ok {
				return tableName.Name.String(), nil
			}
		}
	}

	return "", fmt.Errorf("table name not found")
}

// CheckQueryComplexity assesses query complexity
func (sv *SQLValidator) CheckQueryComplexity(sql string) (complexity int, err error) {
	stmt, err := sv.parseSQL(sql)
	if err != nil {
		return 0, err
	}

	switch s := stmt.(type) {
	case *sqlparser.Select:
		return sv.calculateSelectComplexity(s), nil
	case *sqlparser.Union:
		// Union queries are more complex
		var leftComplexity, rightComplexity int
		if leftSelect, ok := s.Left.(*sqlparser.Select); ok {
			leftComplexity = sv.calculateSelectComplexity(leftSelect)
		}
		if rightSelect, ok := s.Right.(*sqlparser.Select); ok {
			rightComplexity = sv.calculateSelectComplexity(rightSelect)
		}
		return leftComplexity + rightComplexity + 10, nil
	default:
		return 0, fmt.Errorf("unsupported statement type")
	}
}

func (sv *SQLValidator) calculateSelectComplexity(selectStmt *sqlparser.Select) int {
	complexity := 0

	// Base complexity
	complexity += 1

	// Check for JOINs
	if selectStmt.From != nil && len(selectStmt.From) > 1 {
		complexity += len(selectStmt.From) * 5
	}

	// Check for WHERE clause
	if selectStmt.Where != nil {
		complexity += 3
	}

	// Check for GROUP BY
	if selectStmt.GroupBy != nil {
		complexity += 2
	}

	// Check for HAVING
	if selectStmt.Having != nil {
		complexity += 2
	}

	// Check for ORDER BY
	if selectStmt.OrderBy != nil {
		complexity += 2
	}

	// Check for LIMIT
	if selectStmt.Limit != nil {
		complexity += 1
	}

	return complexity
}

// =============================================================================
// Data Source-Specific Validation (Phase 1 Extensions)
// =============================================================================

// DataSourceSQLConfig holds SQL dialect configuration for a data source type
type DataSourceSQLConfig struct {
	SupportsTimeTravel      bool
	SupportsWindowFunctions bool
	SupportsArrays          bool
	SupportsStructs         bool
	SupportsJSON            bool
	IdentifierQuote         string // e.g., `"` for PostgreSQL, "`" for MySQL
	TimeTravelSyntax        string // e.g., "FOR SYSTEM_TIME AS OF", "TABLE"
}

// GetDataSourceSQLConfig returns SQL configuration for a data source type
func GetDataSourceSQLConfig(dbType model.DatabaseType) DataSourceSQLConfig {
	switch dbType {
	case model.DatabaseTypeTrino, model.DatabaseTypePresto, model.DatabaseTypeOpenLooKeng:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      false,
			SupportsWindowFunctions: true,
			SupportsArrays:          true,
			SupportsStructs:         true,
			SupportsJSON:            true,
			IdentifierQuote:         `"`,
			TimeTravelSyntax:        "",
		}
	// Data Lake Table Formats
	case model.DatabaseTypeApacheIceberg:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      true,
			SupportsWindowFunctions: true,
			SupportsArrays:          true,
			SupportsStructs:         true,
			SupportsJSON:            true,
			IdentifierQuote:         `"`,
			TimeTravelSyntax:        "FOR SYSTEM_TIME AS OF",
		}
	case model.DatabaseTypeDeltaLake:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      true,
			SupportsWindowFunctions: true,
			SupportsArrays:          true,
			SupportsStructs:         true,
			SupportsJSON:            true,
			IdentifierQuote:         "`",
			TimeTravelSyntax:        "VERSION AS OF",
		}
	case model.DatabaseTypeApacheHudi:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      true,
			SupportsWindowFunctions: true,
			SupportsArrays:          true,
			SupportsStructs:         true,
			SupportsJSON:            true,
			IdentifierQuote:         "`",
			TimeTravelSyntax:        " TIMESTAMP AS OF",
		}

	// Cloud Warehouses
	case model.DatabaseTypeSnowflake:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      true,
			SupportsWindowFunctions: true,
			SupportsArrays:          true,
			SupportsStructs:         true,
			SupportsJSON:            true,
			IdentifierQuote:         `"`,
			TimeTravelSyntax:        "AT",
		}
	case model.DatabaseTypeDatabricks:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      true,
			SupportsWindowFunctions: true,
			SupportsArrays:          true,
			SupportsStructs:         true,
			SupportsJSON:            true,
			IdentifierQuote:         "`",
			TimeTravelSyntax:        "VERSION AS OF",
		}
	case model.DatabaseTypeBigQuery:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      true,
			SupportsWindowFunctions: true,
			SupportsArrays:          true,
			SupportsStructs:         true,
			SupportsJSON:            true,
			IdentifierQuote:         "`",
			TimeTravelSyntax:        "FOR SYSTEM_TIME AS OF",
		}
	case model.DatabaseTypeRedshift:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      false,
			SupportsWindowFunctions: true,
			SupportsArrays:          false,
			SupportsStructs:         false,
			SupportsJSON:            true,
			IdentifierQuote:         `"`,
			TimeTravelSyntax:        "",
		}

	// OLAP Engines
	case model.DatabaseTypeClickHouse:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      false,
			SupportsWindowFunctions: true,
			SupportsArrays:          true,
			SupportsStructs:         true,
			SupportsJSON:            true,
			IdentifierQuote:         "`",
			TimeTravelSyntax:        "",
		}
	case model.DatabaseTypeApacheDoris, model.DatabaseTypeStarRocks, model.DatabaseTypeApacheDruid:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      false,
			SupportsWindowFunctions: true,
			SupportsArrays:          false,
			SupportsStructs:         false,
			SupportsJSON:            true,
			IdentifierQuote:         "`",
			TimeTravelSyntax:        "",
		}

	// Object Storage - No SQL, API-based
	case model.DatabaseTypeS3Parquet, model.DatabaseTypeS3ORC, model.DatabaseTypeS3Avro,
		model.DatabaseTypeS3CSV, model.DatabaseTypeS3JSON:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      false,
			SupportsWindowFunctions: false,
			SupportsArrays:          false,
			SupportsStructs:         false,
			SupportsJSON:            false,
			IdentifierQuote:         "",
			TimeTravelSyntax:        "",
		}

	// Domestic Databases
	case model.DatabaseTypeOceanBaseMySQL, model.DatabaseTypeOceanBaseOracle, model.DatabaseTypeTiDB:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      false,
			SupportsWindowFunctions: true,
			SupportsArrays:          false,
			SupportsStructs:         false,
			SupportsJSON:            true,
			IdentifierQuote:         "`",
			TimeTravelSyntax:        "",
		}

	// HDFS types have no registered query path and must not inherit a SQL dialect.
	case model.DatabaseTypeHDFSAvro, model.DatabaseTypeHDFSParquet, model.DatabaseTypeHDFSCSV,
		model.DatabaseTypeHDFSText, model.DatabaseTypeHDFSParquetCompressed:
		return DataSourceSQLConfig{}

	// File Systems
	case model.DatabaseTypeOzoneParquet:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      false,
			SupportsWindowFunctions: false,
			SupportsArrays:          false,
			SupportsStructs:         false,
			SupportsJSON:            false,
			IdentifierQuote:         "",
			TimeTravelSyntax:        "",
		}

	// Relational Databases (existing)
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      false,
			SupportsWindowFunctions: true,
			SupportsArrays:          false,
			SupportsStructs:         false,
			SupportsJSON:            true,
			IdentifierQuote:         "`",
			TimeTravelSyntax:        "",
		}
	case model.DatabaseTypePostgreSQL:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      false,
			SupportsWindowFunctions: true,
			SupportsArrays:          true,
			SupportsStructs:         false,
			SupportsJSON:            true,
			IdentifierQuote:         `"`,
			TimeTravelSyntax:        "",
		}
	case model.DatabaseTypeOracle:
		return DataSourceSQLConfig{
			SupportsTimeTravel:      false,
			SupportsWindowFunctions: true,
			SupportsArrays:          true,
			SupportsStructs:         true,
			SupportsJSON:            true,
			IdentifierQuote:         `"`,
			TimeTravelSyntax:        "",
		}

	default:
		// Default to MySQL-like configuration
		return DataSourceSQLConfig{
			SupportsTimeTravel:      false,
			SupportsWindowFunctions: true,
			SupportsArrays:          false,
			SupportsStructs:         false,
			SupportsJSON:            true,
			IdentifierQuote:         "`",
			TimeTravelSyntax:        "",
		}
	}
}

// ValidateDataSourceSyntax validates SQL syntax for a specific data source type
func (sv *SQLValidator) ValidateDataSourceSyntax(sql string, dbType model.DatabaseType) error {
	return sv.ValidateDataSourceSyntaxContext(context.Background(), sql, dbType)
}

// ValidateDataSourceSyntaxContext validates SQL with request cancellation and
// then applies connector feature checks.
func (sv *SQLValidator) ValidateDataSourceSyntaxContext(ctx context.Context, sql string, dbType model.DatabaseType) error {
	config := GetDataSourceSQLConfig(dbType)

	// Basic validation first
	if err := sv.ValidateStatementForDataSourceContext(ctx, sql, dbType); err != nil {
		return err
	}
	if sv.astAnalyzer != nil {
		// The configured dialect parser already checked the target grammar.
		// Keep substring-based feature heuristics out of this production path;
		// they can misread comments, literals, and quoted identifiers.
		return nil
	}

	// Validate data source-specific syntax
	return sv.validateDataSourceSpecificSyntax(sql, dbType, config)
}

// validateDataSourceSpecificSyntax validates data source-specific SQL features
func (sv *SQLValidator) validateDataSourceSpecificSyntax(sql string, dbType model.DatabaseType, config DataSourceSQLConfig) error {
	sqlUpper := strings.ToUpper(sql)

	// Check for time travel syntax if not supported
	if config.TimeTravelSyntax == "" && sv.hasTimeTravelSyntax(sqlUpper) {
		return fmt.Errorf("time travel queries not supported for %s", dbType)
	}

	// Check for unsupported complex types
	if !config.SupportsArrays && sv.hasArraySyntax(sql) {
		return fmt.Errorf("array types not supported for %s", dbType)
	}

	if !config.SupportsStructs && sv.hasStructSyntax(sql) {
		return fmt.Errorf("struct types not supported for %s", dbType)
	}

	return nil
}

// hasTimeTravelSyntax checks if SQL contains time travel syntax
func (sv *SQLValidator) hasTimeTravelSyntax(sqlUpper string) bool {
	timeTravelPatterns := []string{
		"FOR SYSTEM_TIME AS OF",
		"VERSION AS OF",
		"TIMESTAMP AS OF",
		"AT TIMESTAMP",
		"FOR SYSTEM TIME",
	}

	for _, pattern := range timeTravelPatterns {
		if strings.Contains(sqlUpper, pattern) {
			return true
		}
	}

	return false
}

// hasArraySyntax checks if SQL contains array syntax
func (sv *SQLValidator) hasArraySyntax(sql string) bool {
	// Check for ARRAY type or bracket notation
	arrayPatterns := []string{
		"ARRAY[",
		"ARRAY<",
		"::ARRAY",
		"[", // Could be array subscript
	}

	upperSQL := strings.ToUpper(sql)
	for _, pattern := range arrayPatterns {
		if strings.Contains(upperSQL, pattern) || strings.Contains(sql, pattern) {
			return true
		}
	}

	return false
}

// hasStructSyntax checks if SQL contains struct syntax
func (sv *SQLValidator) hasStructSyntax(sql string) bool {
	structPatterns := []string{
		"STRUCT<",
		"ROW(",
		"::STRUCT",
	}

	upperSQL := strings.ToUpper(sql)
	for _, pattern := range structPatterns {
		if strings.Contains(upperSQL, pattern) {
			return true
		}
	}

	return false
}

// SupportsQueryType checks if a data source supports a specific query type
func (sv *SQLValidator) SupportsQueryType(dbType model.DatabaseType, queryType string) bool {
	config := GetDataSourceSQLConfig(dbType)

	switch strings.ToUpper(queryType) {
	case "TIMETRAVEL":
		return config.SupportsTimeTravel
	case "WINDOW":
		return config.SupportsWindowFunctions
	case "ARRAY":
		return config.SupportsArrays
	case "STRUCT":
		return config.SupportsStructs
	case "JSON":
		return config.SupportsJSON
	default:
		return true
	}
}

// SanitizeSQLForDataSource sanitizes SQL for a specific data source
func (sv *SQLValidator) SanitizeSQLForDataSource(sql string, dbType model.DatabaseType) (string, error) {
	config := GetDataSourceSQLConfig(dbType)

	// Validate first
	if err := sv.ValidateDataSourceSyntax(sql, dbType); err != nil {
		return "", err
	}

	// Normalize identifiers (quote them properly)
	sanitized := sv.normalizeIdentifiers(sql, config.IdentifierQuote)

	return sanitized, nil
}

// normalizeIdentifiers normalizes SQL identifiers for the data source
func (sv *SQLValidator) normalizeIdentifiers(sql, quoteChar string) string {
	if quoteChar == "" {
		return sql
	}

	// This is a simplified implementation
	// In production, you would use the SQL parser to properly identify and quote identifiers
	normalized := sql

	// Replace unquoted identifiers with quoted ones (simplified)
	// This is a placeholder - proper implementation requires AST parsing

	return normalized
}
