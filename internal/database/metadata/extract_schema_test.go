package metadata

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// newSchemaExtractor wires a PostgreSQL extractor to a caller-supplied canned
// lookup so each test controls exactly one broken result set.
func newSchemaExtractor(lookup func(query string) driver.Rows) *MetadataExtractor {
	return &MetadataExtractor{
		connPool: &cannedConnectionProvider{db: sql.OpenDB(cannedConnector{lookup: lookup})},
		sources:  &dataSourceLookupStub{dataSource: &model.DataSource{ID: "pg-source", Type: model.DatabaseTypePostgreSQL}},
		registry: database.NewDriverRegistry(),
	}
}

// A scan failure on the table list must fail schema extraction instead of
// silently dropping tables and reporting the remainder as the complete schema
// (#108).
func TestExtractSchemaFailsOnTableScanError(t *testing.T) {
	extractor := newSchemaExtractor(func(query string) driver.Rows {
		if strings.Contains(query, "pg_tables") {
			// Two columns where the scan expects three: every row fails.
			return &cannedRows{
				columns: []string{"tablename", "tabletype"},
				values:  [][]driver.Value{{"orders", "TABLE"}},
			}
		}
		return &cannedRows{}
	})

	schema, _, err := extractor.ExtractSchema(context.Background(), "pg-source")
	if err == nil || !strings.Contains(err.Error(), "failed to scan table row") {
		t.Fatalf("ExtractSchema() error = %v, want a table-scan failure", err)
	}
	if schema != nil {
		t.Errorf("ExtractSchema() returned a partial schema %v alongside the error", schema)
	}
}

// A scan failure on a table's columns must fail extraction for that table —
// and the whole schema — instead of omitting the table (#108).
func TestExtractSchemaFailsOnColumnScanError(t *testing.T) {
	extractor := newSchemaExtractor(func(query string) driver.Rows {
		switch {
		case strings.Contains(query, "pg_tables"):
			return &cannedRows{
				columns: []string{"tablename", "tabletype", "schemaname"},
				values:  [][]driver.Value{{"orders", "TABLE", "public"}},
			}
		case strings.Contains(query, "information_schema.columns"):
			// Four columns where the scan expects five.
			return &cannedRows{
				columns: []string{"column_name", "data_type", "is_nullable", "column_default"},
				values:  [][]driver.Value{{"id", "integer", "NO", nil}},
			}
		}
		return &cannedRows{}
	})

	_, _, err := extractor.ExtractSchema(context.Background(), "pg-source")
	if err == nil || !strings.Contains(err.Error(), `failed to extract columns for table "orders"`) {
		t.Fatalf("ExtractSchema() error = %v, want a column-extraction failure for orders", err)
	}
}

// A connection drop while iterating the table list must surface as an error,
// not as a schema containing only the rows read before the drop (#108).
func TestExtractSchemaFailsWhenTableIterationDrops(t *testing.T) {
	dropErr := errors.New("connection reset by peer")
	extractor := newSchemaExtractor(func(query string) driver.Rows {
		if strings.Contains(query, "pg_tables") {
			return &cannedRows{
				columns: []string{"tablename", "tabletype", "schemaname"},
				values: [][]driver.Value{
					{"orders", "TABLE", "public"},
					{"customers", "TABLE", "public"},
				},
				failAt:  1,
				failErr: dropErr,
			}
		}
		return &cannedRows{}
	})

	_, _, err := extractor.ExtractSchema(context.Background(), "pg-source")
	if err == nil || !strings.Contains(err.Error(), "failed to iterate table rows") {
		t.Fatalf("ExtractSchema() error = %v, want an iteration failure", err)
	}
}

// The single-table path shares the same honesty: a mid-iteration drop on the
// column list fails describe instead of returning truncated columns (#108).
func TestExtractTableSchemaFailsWhenColumnIterationDrops(t *testing.T) {
	dropErr := errors.New("connection reset by peer")
	extractor := newSchemaExtractor(func(query string) driver.Rows {
		switch {
		case strings.Contains(query, "pg_tables"):
			return &cannedRows{
				columns: []string{"tablename", "tabletype", "schemaname"},
				values:  [][]driver.Value{{"orders", "TABLE", "public"}},
			}
		case strings.Contains(query, "information_schema.columns"):
			return &cannedRows{
				columns: []string{"column_name", "data_type", "is_nullable", "column_default", "comment"},
				values: [][]driver.Value{
					{"id", "integer", "NO", nil, ""},
					{"amount", "numeric", "YES", nil, ""},
				},
				failAt:  1,
				failErr: dropErr,
			}
		}
		return &cannedRows{}
	})

	table, err := extractor.ExtractTableSchema(context.Background(), "pg-source", "orders")
	if err == nil || !strings.Contains(err.Error(), "failed to iterate column rows") {
		t.Fatalf("ExtractTableSchema() error = %v, want a column iteration failure", err)
	}
	if table != nil {
		t.Errorf("ExtractTableSchema() returned a truncated table %v alongside the error", table)
	}
}
