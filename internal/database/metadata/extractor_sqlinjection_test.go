package metadata

import (
	"context"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// TestSQLInjectionResistantColumnQuery verifies that tableName/schemaName are
// escaped as inert literals in getColumnQuery, so user-supplied values cannot
// break out of the string-literal context and inject SQL.
func TestSQLInjectionResistantColumnQuery(t *testing.T) {
	e := &MetadataExtractor{}
	malicious := "a'; DROP TABLE x;--"

	// String-literal branches: the single quote must be escaped/doubled so the
	// raw payload never appears verbatim.
	literalTypes := []model.DatabaseType{
		model.DatabaseTypePostgreSQL,
		model.DatabaseTypeOpenGauss,
		model.DatabaseTypeOracle,
		model.DatabaseTypeClickHouse,
		model.DatabaseTypeTrino, // default branch
	}
	for _, dbType := range literalTypes {
		got := e.getColumnQuery(dbType, malicious, malicious)
		if strings.Contains(got, malicious) {
			t.Errorf("%v: getColumnQuery contained unescaped payload %q in query: %s", dbType, malicious, got)
		}
	}

	// Identifier branches (SHOW COLUMNS FROM): the entire name must be confined
	// to a single backtick-quoted identifier token.
	identifierTypes := []model.DatabaseType{
		model.DatabaseTypeMySQL,
		model.DatabaseTypeMariaDB,
		model.DatabaseTypeApacheDoris,
		model.DatabaseTypeStarRocks,
	}
	for _, dbType := range identifierTypes {
		want := "SHOW COLUMNS FROM `" + malicious + "`"
		if got := e.getColumnQuery(dbType, malicious, malicious); got != want {
			t.Errorf("%v: getColumnQuery identifier not backtick-confined: got %q, want %q", dbType, got, want)
		}
	}
}

// TestSQLInjectionResistantIndexQuery verifies the same escaping property for
// getIndexQuery across all branches.
func TestSQLInjectionResistantIndexQuery(t *testing.T) {
	e := &MetadataExtractor{}
	malicious := "x' UNION SELECT 1,2,3--"

	literalTypes := []model.DatabaseType{
		model.DatabaseTypePostgreSQL,
		model.DatabaseTypeTrino, // default branch
	}
	for _, dbType := range literalTypes {
		got := e.getIndexQuery(dbType, malicious, malicious)
		if strings.Contains(got, malicious) {
			t.Errorf("%v: getIndexQuery contained unescaped payload %q in query: %s", dbType, malicious, got)
		}
	}

	identifierTypes := []model.DatabaseType{
		model.DatabaseTypeMySQL,
		model.DatabaseTypeMariaDB,
	}
	for _, dbType := range identifierTypes {
		want := "SHOW INDEX FROM `" + malicious + "`"
		if got := e.getIndexQuery(dbType, malicious, malicious); got != want {
			t.Errorf("%v: getIndexQuery identifier not backtick-confined: got %q, want %q", dbType, got, want)
		}
	}
}

// TestSQLInjectionResistantPrimaryKeyQuery verifies the same escaping property
// for getPrimaryKeyQuery across all branches.
func TestSQLInjectionResistantPrimaryKeyQuery(t *testing.T) {
	e := &MetadataExtractor{}
	malicious := "t' OR '1'='1"

	cases := []struct {
		dbType model.DatabaseType
	}{
		{model.DatabaseTypePostgreSQL},
		{model.DatabaseTypeMySQL},
		{model.DatabaseTypeMariaDB},
		{model.DatabaseTypeTrino}, // default branch
	}

	for _, tc := range cases {
		got := e.getPrimaryKeyQuery(tc.dbType, malicious, malicious)
		if strings.Contains(got, malicious) {
			t.Errorf("%v: getPrimaryKeyQuery contained unescaped payload %q in query: %s", tc.dbType, malicious, got)
		}
	}
}

// TestMySQLShowColumnsQuotesIdentifier ensures SHOW COLUMNS/SHOW INDEX treat the
// table name as a quoted identifier rather than splicing it raw.
func TestMySQLShowColumnsQuotesIdentifier(t *testing.T) {
	e := &MetadataExtractor{}
	want := "SHOW COLUMNS FROM `orders`"
	if got := e.getColumnQuery(model.DatabaseTypeMySQL, "orders", ""); got != want {
		t.Errorf("getColumnQuery(MySQL): got %q, want %q", got, want)
	}
	want = "SHOW INDEX FROM `orders`"
	if got := e.getIndexQuery(model.DatabaseTypeMySQL, "orders", ""); got != want {
		t.Errorf("getIndexQuery(MySQL): got %q, want %q", got, want)
	}
}

func TestOpenGaussColumnQueryScopesSchema(t *testing.T) {
	e := &MetadataExtractor{}
	query := e.getColumnQuery(model.DatabaseTypeOpenGauss, "orders", "tenant_a")
	if !strings.Contains(query, "table_name = 'orders'") || !strings.Contains(query, "table_schema = 'tenant_a'") {
		t.Fatalf("openGauss column query did not constrain schema: %s", query)
	}
}

func TestClickHouseMetadataQueriesUseSystemCatalog(t *testing.T) {
	e := &MetadataExtractor{}
	tableQuery := e.getTableQuery(model.DatabaseTypeClickHouse)
	if !strings.Contains(tableQuery, "FROM system.tables") ||
		!strings.Contains(tableQuery, "database = currentDatabase()") ||
		strings.Contains(tableQuery, "information_schema") ||
		strings.Contains(tableQuery, "is_dropped") {
		t.Fatalf("ClickHouse table query is not least-privilege system catalog discovery: %s", tableQuery)
	}
	columnQuery := e.getColumnQuery(model.DatabaseTypeClickHouse, "orders", "ignored")
	if !strings.Contains(columnQuery, "FROM system.columns") ||
		!strings.Contains(columnQuery, "database = currentDatabase()") ||
		!strings.Contains(columnQuery, "table = 'orders'") ||
		strings.Contains(columnQuery, "information_schema") {
		t.Fatalf("ClickHouse column query is not database-scoped system catalog discovery: %s", columnQuery)
	}
}

// ClickHouse's ordering and data-skipping indexes are not portable relational
// indexes. Browser onboarding grants only the exact system metadata needed for
// tables and columns, so supplemental discovery must not fall back to
// information_schema.
func TestClickHouseSupplementalMetadataDoesNotRequireDatabaseQueries(t *testing.T) {
	e := &MetadataExtractor{}
	indexes, err := e.extractIndexes(context.Background(), nil, model.DatabaseTypeClickHouse, "orders", "")
	if err != nil || len(indexes) != 0 {
		t.Fatalf("extractIndexes() = %#v, %v; want empty indexes without a query", indexes, err)
	}
	primaryKey, err := e.extractPrimaryKey(context.Background(), nil, model.DatabaseTypeClickHouse, "orders", "")
	if err != nil || len(primaryKey) != 0 {
		t.Fatalf("extractPrimaryKey() = %#v, %v; want no primary key without a query", primaryKey, err)
	}
}
