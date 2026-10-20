package sql_translator

import (
	"context"
	"fmt"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/sqlanalysis"
)

// SQLTranslationManager manages SQL dialect translation and the shared enable policy.
type SQLTranslationManager struct {
	translator SQLTranslator
	enabled    bool
}

// NewSQLTranslationManager preserves the simple constructor used by existing callers.
// Production servers should create one shared manager from their runtime configuration.
func NewSQLTranslationManager(useExternal bool) *SQLTranslationManager {
	options := DefaultRuntimeOptions()
	options.Enabled = true
	if useExternal {
		return NewRuntimeSQLTranslationManager(options)
	}
	return &SQLTranslationManager{
		translator: NewMockSQLTranslator(),
		enabled:    true,
	}
}

// NewRuntimeSQLTranslationManager creates a manager backed by the bounded SQLGlot process.
func NewRuntimeSQLTranslationManager(options RuntimeOptions) *SQLTranslationManager {
	options = normalizeRuntimeOptions(options)
	return &SQLTranslationManager{
		translator: NewExternalSQLTranslatorWithOptions(options),
		enabled:    options.Enabled,
	}
}

func (stm *SQLTranslationManager) Enabled() bool {
	return stm != nil && stm.enabled
}

// TranslateQuery translates SQL while honoring the configured per-request timeout,
// concurrency limit, input size, and output size.
func (stm *SQLTranslationManager) TranslateQuery(sql, sourceDialect, targetDialect string) (string, error) {
	return stm.TranslateQueryContext(context.Background(), sql, sourceDialect, targetDialect)
}

func (stm *SQLTranslationManager) TranslateQueryContext(ctx context.Context, sql, sourceDialect, targetDialect string) (string, error) {
	if !stm.Enabled() {
		return "", ErrSQLTranslationDisabled
	}
	if sql == "" {
		return "", fmt.Errorf("empty SQL query")
	}
	if sourceDialect == "" {
		return "", fmt.Errorf("empty source dialect")
	}
	if targetDialect == "" {
		return "", fmt.Errorf("empty target dialect")
	}

	sourceDialect = strings.ToLower(strings.TrimSpace(sourceDialect))
	targetDialect = strings.ToLower(strings.TrimSpace(targetDialect))
	if sourceDialect == targetDialect {
		return sql, nil
	}

	if translator, ok := stm.translator.(contextSQLTranslator); ok {
		return translator.TranslateContext(ctx, sql, sourceDialect, targetDialect)
	}
	return stm.translator.Translate(sql, sourceDialect, targetDialect)
}

func (stm *SQLTranslationManager) GetSupportedDialects() []string {
	if !stm.Enabled() || stm.translator == nil {
		return nil
	}
	return stm.translator.SupportedDialects()
}

func (stm *SQLTranslationManager) ValidateSQL(sql, dialect string) (bool, error) {
	return stm.ValidateSQLContext(context.Background(), sql, dialect)
}

func (stm *SQLTranslationManager) ValidateSQLContext(ctx context.Context, sql, dialect string) (bool, error) {
	if !stm.Enabled() {
		return false, ErrSQLTranslationDisabled
	}
	if translator, ok := stm.translator.(contextSQLTranslator); ok {
		return translator.ValidateContext(ctx, sql, dialect)
	}
	return stm.translator.Validate(sql, dialect)
}

// SupportsASTAnalysis reports whether the configured runtime can parse SQL.
// This capability remains available when user-facing translation is disabled.
func (stm *SQLTranslationManager) SupportsASTAnalysis() bool {
	if stm == nil || stm.translator == nil {
		return false
	}
	_, ok := stm.translator.(contextSQLAnalyzer)
	return ok
}

// AnalyzeSQLContext parses SQL with the requested dialect. Read-only validation
// uses this regardless of the user-facing translation setting.
func (stm *SQLTranslationManager) AnalyzeSQLContext(ctx context.Context, sql, dialect string) (sqlanalysis.Analysis, error) {
	if stm == nil || stm.translator == nil {
		return sqlanalysis.Analysis{}, fmt.Errorf("SQL AST analyzer is not configured")
	}
	analyzer, ok := stm.translator.(contextSQLAnalyzer)
	if !ok {
		return sqlanalysis.Analysis{}, fmt.Errorf("SQL AST analysis requires the SQLGlot runtime")
	}
	if strings.TrimSpace(dialect) == "" {
		return sqlanalysis.Analysis{}, fmt.Errorf("empty SQL dialect")
	}
	return analyzer.AnalyzeContext(ctx, sql, strings.ToLower(strings.TrimSpace(dialect)))
}

// ValidateRuntime checks the configured interpreter, script, and SQLGlot import
// before the server opens its metadata database.
func (stm *SQLTranslationManager) ValidateRuntime() error {
	translator, ok := stm.translator.(*ExternalSQLTranslator)
	if !ok {
		return nil
	}
	return translator.ValidateRuntime()
}

func (stm *SQLTranslationManager) TranslateFromMySQL(sql, targetDialect string) (string, error) {
	return stm.TranslateQuery(sql, string(MySQL), targetDialect)
}

func (stm *SQLTranslationManager) TranslateFromTrino(sql, targetDialect string) (string, error) {
	return stm.TranslateQuery(sql, string(Trino), targetDialect)
}

func (stm *SQLTranslationManager) TranslateToMySQL(sql, sourceDialect string) (string, error) {
	return stm.TranslateQuery(sql, sourceDialect, string(MySQL))
}

func (stm *SQLTranslationManager) TranslateToTrino(sql, sourceDialect string) (string, error) {
	return stm.TranslateQuery(sql, sourceDialect, string(Trino))
}
