package metadata

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// ALL_TAB_COLUMNS.NULLABLE is Y/N, not YES/NO: a nullable Oracle column was
// reported as not nullable (#529).
func TestExtractTableSchemaOracleNullability(t *testing.T) {
	var queries []string
	lookup := func(query string) driver.Rows {
		switch {
		case strings.Contains(query, "FROM all_tables"):
			return &cannedRows{
				columns: []string{"table_name", "table_type", "owner"},
				values:  [][]driver.Value{{"orders", "TABLE", "APP"}},
			}
		case strings.Contains(query, "all_tab_columns"):
			return &cannedRows{
				columns: []string{"column_name", "data_type", "nullable", "data_default", "comment"},
				values: [][]driver.Value{
					{"ID", "NUMBER", "N", nil, ""},
					{"NOTE", "VARCHAR2", "Y", nil, ""},
				},
			}
		default:
			return &cannedRows{}
		}
	}
	db := sql.OpenDB(cannedConnector{lookup: lookup, queries: &queries})

	source := &model.DataSource{ID: "oracle-source", Type: model.DatabaseTypeOracle}
	extractor := &MetadataExtractor{
		connPool: &cannedConnectionProvider{db: db},
		sources:  &dataSourceLookupStub{dataSource: source},
		registry: database.NewDriverRegistry(),
	}

	table, err := extractor.ExtractTableSchema(context.Background(), "oracle-source", "orders")
	if err != nil {
		t.Fatalf("ExtractTableSchema() error = %v", err)
	}
	if len(table.Columns) != 2 {
		t.Fatalf("columns = %+v, want 2", table.Columns)
	}
	if table.Columns[0].Name != "ID" || table.Columns[0].Nullable {
		t.Errorf("ID column = %+v, want not nullable (Oracle N)", table.Columns[0])
	}
	if table.Columns[1].Name != "NOTE" || !table.Columns[1].Nullable {
		t.Errorf("NOTE column = %+v, want nullable (Oracle Y)", table.Columns[1])
	}
}
