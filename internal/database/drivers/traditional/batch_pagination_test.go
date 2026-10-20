package traditional

import "testing"

func TestApplyBatchPaginationPreservesExistingClausesAndDialectSyntax(t *testing.T) {
	tests := []struct {
		name   string
		driver interface {
			ApplyBatchPagination(string, int64, int64) (string, error)
		}
		sql  string
		want string
	}{
		{
			name:   "mysql keeps outer ordering",
			driver: &MySQLDriver{},
			sql:    "SELECT id FROM events ORDER BY id",
			want:   "SELECT id FROM events ORDER BY id LIMIT 25 OFFSET 50",
		},
		{
			name:   "mysql wraps existing limit",
			driver: &MySQLDriver{},
			sql:    "SELECT id FROM events ORDER BY id LIMIT 10",
			want:   "SELECT * FROM (SELECT id FROM events ORDER BY id LIMIT 10) AS batch_query LIMIT 25 OFFSET 50",
		},
		{
			name:   "postgres wraps existing offset",
			driver: &PostgreSQLDriver{},
			sql:    "SELECT id FROM events ORDER BY id OFFSET 5",
			want:   "SELECT * FROM (SELECT id FROM events ORDER BY id OFFSET 5) AS batch_query LIMIT 25 OFFSET 50",
		},
		{
			name:   "oracle appends offset fetch after ordering",
			driver: &OracleDriver{},
			sql:    "SELECT id FROM events ORDER BY id",
			want:   "SELECT id FROM events ORDER BY id OFFSET 50 ROWS FETCH FIRST 25 ROWS ONLY",
		},
		{
			name:   "oracle wraps existing fetch with legal alias syntax",
			driver: &OracleDriver{},
			sql:    "SELECT id FROM events ORDER BY id FETCH FIRST 10 ROWS ONLY",
			want:   "SELECT * FROM (SELECT id FROM events ORDER BY id FETCH FIRST 10 ROWS ONLY) batch_query OFFSET 50 ROWS FETCH FIRST 25 ROWS ONLY",
		},
		{
			name:   "sql server reuses outer ordering",
			driver: NewSQLServerDriver(),
			sql:    "SELECT id FROM events ORDER BY id",
			want:   "SELECT id FROM events ORDER BY id OFFSET 50 ROWS FETCH NEXT 25 ROWS ONLY",
		},
		{
			name:   "sql server adds fallback ordering when absent",
			driver: NewSQLServerDriver(),
			sql:    "SELECT id FROM events",
			want:   "SELECT id FROM events ORDER BY (SELECT NULL) OFFSET 50 ROWS FETCH NEXT 25 ROWS ONLY",
		},
		{
			name:   "sql server ignores nested ordering for the outer query",
			driver: NewSQLServerDriver(),
			sql:    "SELECT * FROM (SELECT TOP 10 id FROM events ORDER BY id) AS recent",
			want:   "SELECT * FROM (SELECT TOP 10 id FROM events ORDER BY id) AS recent ORDER BY (SELECT NULL) OFFSET 50 ROWS FETCH NEXT 25 ROWS ONLY",
		},
		{
			name:   "sql server wraps an existing page",
			driver: NewSQLServerDriver(),
			sql:    "SELECT id FROM events ORDER BY id OFFSET 5 ROWS FETCH NEXT 10 ROWS ONLY",
			want:   "SELECT * FROM (SELECT id FROM events ORDER BY id OFFSET 5 ROWS FETCH NEXT 10 ROWS ONLY) AS batch_query ORDER BY (SELECT NULL) OFFSET 50 ROWS FETCH NEXT 25 ROWS ONLY",
		},
		{
			name:   "sql server wraps an existing top clause",
			driver: NewSQLServerDriver(),
			sql:    "SELECT TOP (10) id FROM events ORDER BY id",
			want:   "SELECT * FROM (SELECT TOP (10) id FROM events ORDER BY id) AS batch_query ORDER BY (SELECT NULL) OFFSET 50 ROWS FETCH NEXT 25 ROWS ONLY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.driver.ApplyBatchPagination(tt.sql, 25, 50)
			if err != nil {
				t.Fatalf("ApplyBatchPagination returned error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ApplyBatchPagination = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApplyBatchPaginationHandlesCTEAndSubqueryShapes(t *testing.T) {
	tests := []struct {
		name   string
		driver interface {
			ApplyBatchPagination(string, int64, int64) (string, error)
		}
		sql  string
		want string
	}{
		{
			name:   "mysql wraps a CTE carrying its own limit",
			driver: &MySQLDriver{},
			sql:    "WITH recent AS (SELECT id FROM events LIMIT 10) SELECT id FROM recent",
			want:   "SELECT * FROM (WITH recent AS (SELECT id FROM events LIMIT 10) SELECT id FROM recent) AS batch_query LIMIT 25 OFFSET 50",
		},
		{
			name:   "postgres wraps a CTE carrying its own offset",
			driver: &PostgreSQLDriver{},
			sql:    "WITH recent AS (SELECT id FROM events ORDER BY id LIMIT 10) SELECT id FROM recent OFFSET 2",
			want:   "SELECT * FROM (WITH recent AS (SELECT id FROM events ORDER BY id LIMIT 10) SELECT id FROM recent OFFSET 2) AS batch_query LIMIT 25 OFFSET 50",
		},
		{
			name:   "oracle paginates a CTE result",
			driver: &OracleDriver{},
			sql:    "WITH recent AS (SELECT id FROM events) SELECT id FROM recent ORDER BY id",
			want:   "WITH recent AS (SELECT id FROM events) SELECT id FROM recent ORDER BY id OFFSET 50 ROWS FETCH FIRST 25 ROWS ONLY",
		},
		{
			name:   "sql server does not treat CTE ordering as outer ordering",
			driver: NewSQLServerDriver(),
			sql:    "WITH recent AS (SELECT TOP 10 id FROM events ORDER BY id) SELECT id FROM recent",
			want:   "WITH recent AS (SELECT TOP 10 id FROM events ORDER BY id) SELECT id FROM recent ORDER BY (SELECT NULL) OFFSET 50 ROWS FETCH NEXT 25 ROWS ONLY",
		},
		{
			name:   "sql server ignores nested pagination when wrapping is considered",
			driver: NewSQLServerDriver(),
			sql:    "WITH recent AS (SELECT id FROM events ORDER BY id OFFSET 0 ROWS FETCH NEXT 10 ROWS ONLY) SELECT id FROM recent",
			want:   "WITH recent AS (SELECT id FROM events ORDER BY id OFFSET 0 ROWS FETCH NEXT 10 ROWS ONLY) SELECT id FROM recent ORDER BY (SELECT NULL) OFFSET 50 ROWS FETCH NEXT 25 ROWS ONLY",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.driver.ApplyBatchPagination(tt.sql, 25, 50)
			if err != nil {
				t.Fatalf("ApplyBatchPagination returned error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ApplyBatchPagination = %q, want %q", got, tt.want)
			}
		})
	}
}
