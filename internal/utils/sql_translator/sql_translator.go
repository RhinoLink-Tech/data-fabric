package sql_translator

import "context"

// ExternalSQLTranslator delegates conversion to the bundled SQLGlot runtime.
type ExternalSQLTranslator struct {
	supportedDialects []string
	useExternalLib    bool
	runtimeOptions    RuntimeOptions
	pythonPath        string
	scriptPath        string
	concurrent        chan struct{}
}

func NewExternalSQLTranslator(useExternalLib bool) *ExternalSQLTranslator {
	if !useExternalLib {
		return &ExternalSQLTranslator{supportedDialects: supportedDialects(), useExternalLib: false}
	}
	return NewExternalSQLTranslatorWithOptions(DefaultRuntimeOptions())
}

func supportedDialects() []string {
	return []string{
		string(MySQL), string(PostgreSQL), string(Trino), string(Presto), string(OpenLooKeng), string(Spark),
		string(Oracle), string(SQLServer), string(Snowflake), string(SQLite), string(DuckDB), string(Redshift),
	}
}

func (e *ExternalSQLTranslator) Translate(sql, sourceDialect, targetDialect string) (string, error) {
	return e.TranslateContext(context.Background(), sql, sourceDialect, targetDialect)
}

func (e *ExternalSQLTranslator) SupportedDialects() []string {
	return e.supportedDialects
}

func (e *ExternalSQLTranslator) Validate(sql, dialect string) (bool, error) {
	return e.ValidateContext(context.Background(), sql, dialect)
}

func GetTranslator(useExternal bool) SQLTranslator {
	if useExternal {
		return NewExternalSQLTranslator(true)
	}
	return NewMockSQLTranslator()
}
