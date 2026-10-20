package security

import (
	"context"
	"errors"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/sqlanalysis"
)

type stubSQLASTAnalyzer struct {
	analysis sqlanalysis.Analysis
	err      error
	dialect  string
	calls    int
}

func (s *stubSQLASTAnalyzer) AnalyzeSQLContext(_ context.Context, _ string, dialect string) (sqlanalysis.Analysis, error) {
	s.calls++
	s.dialect = dialect
	return s.analysis, s.err
}

func TestValidateStatementForDataSourceUsesASTFacts(t *testing.T) {
	tests := []struct {
		name        string
		analysis    sqlanalysis.Analysis
		analyzerErr error
		wantError   error
	}{
		{
			name: "accepts valid query with SQL-looking comment and text",
			analysis: sqlanalysis.Analysis{
				StatementCount: 1,
				IsQuery:        true,
			},
		},
		{
			name: "rejects multiple statements",
			analysis: sqlanalysis.Analysis{
				StatementCount: 2,
				IsQuery:        false,
			},
			wantError: ErrSQLSyntaxError,
		},
		{
			name: "rejects nested mutation",
			analysis: sqlanalysis.Analysis{
				StatementCount: 1,
				IsQuery:        true,
				HasMutation:    true,
			},
			wantError: ErrNotSelectQuery,
		},
		{
			name: "rejects select into",
			analysis: sqlanalysis.Analysis{
				StatementCount: 1,
				IsQuery:        true,
				HasSelectInto:  true,
			},
			wantError: ErrDangerousKeyword,
		},
		{
			name: "rejects locking query",
			analysis: sqlanalysis.Analysis{
				StatementCount: 1,
				IsQuery:        true,
				HasLocking:     true,
			},
			wantError: ErrDangerousKeyword,
		},
		{
			name: "rejects dangerous function",
			analysis: sqlanalysis.Analysis{
				StatementCount: 1,
				IsQuery:        true,
				Functions:      []string{"pg_read_file"},
			},
			wantError: ErrDangerousKeyword,
		},
		{
			name:        "fails closed on parser errors",
			analysis:    sqlanalysis.Analysis{},
			analyzerErr: errors.New("parser details should not escape"),
			wantError:   ErrSQLSyntaxError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analyzer := &stubSQLASTAnalyzer{analysis: tt.analysis, err: tt.analyzerErr}
			validator := NewSQLValidator(10000)
			validator.SetASTAnalyzer(analyzer)
			err := validator.ValidateStatementForDataSourceContext(context.Background(), "SELECT 'DROP -- text' /* hint */", model.DatabaseTypePostgreSQL)
			if tt.wantError == nil {
				if err != nil {
					t.Fatalf("validation error = %v, want nil", err)
				}
			} else if !errors.Is(err, tt.wantError) {
				t.Fatalf("validation error = %v, want %v", err, tt.wantError)
			}
			if analyzer.calls != 1 || analyzer.dialect != "postgres" {
				t.Fatalf("analyzer called %d times with dialect %q, want one call with postgres", analyzer.calls, analyzer.dialect)
			}
		})
	}
}

func TestValidateStatementForDataSourceFailsClosedWhenDialectIsUnknown(t *testing.T) {
	analyzer := &stubSQLASTAnalyzer{analysis: sqlanalysis.Analysis{StatementCount: 1, IsQuery: true}}
	validator := NewSQLValidator(10000)
	validator.SetASTAnalyzer(analyzer)
	if err := validator.ValidateStatementForDataSource("SELECT 1", model.DatabaseTypeMongoDB); !errors.Is(err, ErrUnsupportedSQLDialect) {
		t.Fatalf("validation error = %v, want unsupported dialect", err)
	}
	if analyzer.calls != 0 {
		t.Fatalf("analyzer called %d times for an unsupported dialect", analyzer.calls)
	}
}

func TestValidateDataSourceSyntaxUsesASTInsteadOfSubstringHeuristics(t *testing.T) {
	analyzer := &stubSQLASTAnalyzer{
		analysis: sqlanalysis.Analysis{StatementCount: 1, IsQuery: true},
	}
	validator := NewSQLValidator(10000)
	validator.SetASTAnalyzer(analyzer)
	if err := validator.ValidateDataSourceSyntaxContext(context.Background(), "SELECT 'ARRAY[' AS label", model.DatabaseTypeMySQL); err != nil {
		t.Fatalf("ValidateDataSourceSyntaxContext() = %v, want valid literal", err)
	}
}

func TestSQLDialectForDataSourceCompatibilityMap(t *testing.T) {
	tests := map[model.DatabaseType]string{
		model.DatabaseTypeMySQL:       "mysql",
		model.DatabaseTypePostgreSQL:  "postgres",
		model.DatabaseTypeOracle:      "oracle",
		model.DatabaseTypeSQLServer:   "sqlserver",
		model.DatabaseTypeTrino:       "trino",
		model.DatabaseTypeOpenLooKeng: "presto",
		model.DatabaseTypeBigQuery:    "bigquery",
		model.DatabaseTypeClickHouse:  "clickhouse",
		model.DatabaseTypeStarRocks:   "starrocks",
	}
	for dbType, want := range tests {
		got, ok := SQLDialectForDataSource(dbType)
		if !ok || got != want {
			t.Errorf("SQLDialectForDataSource(%s) = %q, %v; want %q, true", dbType, got, ok, want)
		}
	}
}
