package metadata

import (
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
		model.DatabaseTypeOracle,
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
