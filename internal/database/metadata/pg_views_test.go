package metadata

import (
	"context"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// PostgreSQL ordinary views live in pg_views, not pg_tables. Discovery must
// list them with their VIEW type and describe them by name.
func TestPostgresViewsAreListedAndDescribed(t *testing.T) {
	extractor := newLookupExtractor(t, model.DatabaseTypePostgreSQL, func(query string) driver.Rows {
		switch {
		case strings.Contains(query, "pg_tables") && strings.Contains(query, "pg_views"):
			return &cannedRows{
				columns: []string{"tablename", "tabletype", "schemaname"},
				values: [][]driver.Value{
					{"orders", "TABLE", "public"},
					{"sales_summary", "VIEW", "public"},
				},
			}
		case strings.Contains(query, "information_schema.columns") && strings.Contains(query, "'sales_summary'"):
			return &cannedRows{
				columns: []string{"column_name", "data_type", "is_nullable", "column_default", "comment"},
				values:  [][]driver.Value{{"region", "text", "YES", nil, ""}},
			}
		default:
			return &cannedRows{}
		}
	})

	tables, err := extractor.ListTables(context.Background(), "source-under-test")
	if err != nil {
		t.Fatalf("ListTables() error = %v", err)
	}
	if len(tables) != 2 || tables[1].Name != "sales_summary" || tables[1].Schema != "public" || tables[1].Type != "VIEW" {
		t.Fatalf("ListTables() = %+v, want public.orders TABLE and public.sales_summary VIEW", tables)
	}

	view, err := extractor.ExtractTableSchemaInSchema(context.Background(), "source-under-test", "public", "sales_summary")
	if err != nil {
		t.Fatalf("ExtractTableSchemaInSchema() error = %v", err)
	}
	if view.Type != "VIEW" || view.Schema != "public" || len(view.Columns) != 1 || view.Columns[0].Name != "region" {
		t.Fatalf("view schema = %+v, want public.sales_summary VIEW with column region", view)
	}
}

// A view and a table may share a name in different schemas. Each must be
// listed separately and resolved by its exact schema; an unqualified name is
// ambiguous rather than silently bound to one of them.
func TestPostgresViewWithSameNameInAnotherSchemaResolvesBySchema(t *testing.T) {
	extractor := newLookupExtractor(t, model.DatabaseTypePostgreSQL, func(query string) driver.Rows {
		switch {
		case strings.Contains(query, "pg_tables") && strings.Contains(query, "pg_views"):
			return &cannedRows{
				columns: []string{"tablename", "tabletype", "schemaname"},
				values: [][]driver.Value{
					{"sales_summary", "TABLE", "public"},
					{"sales_summary", "VIEW", "reporting"},
				},
			}
		case strings.Contains(query, "information_schema.columns") && strings.Contains(query, "'sales_summary'"):
			return &cannedRows{
				columns: []string{"column_name", "data_type", "is_nullable", "column_default", "comment"},
				values:  [][]driver.Value{{"region", "text", "YES", nil, ""}},
			}
		default:
			return &cannedRows{}
		}
	})

	tables, err := extractor.ListTables(context.Background(), "source-under-test")
	if err != nil {
		t.Fatalf("ListTables() error = %v", err)
	}
	if len(tables) != 2 || tables[1].Schema != "reporting" || tables[1].Type != "VIEW" {
		t.Fatalf("ListTables() = %+v, want public.sales_summary TABLE and reporting.sales_summary VIEW", tables)
	}

	view, err := extractor.ExtractTableSchemaInSchema(context.Background(), "source-under-test", "reporting", "sales_summary")
	if err != nil {
		t.Fatalf("ExtractTableSchemaInSchema(reporting) error = %v", err)
	}
	if view.Type != "VIEW" || view.Schema != "reporting" {
		t.Fatalf("reporting view = %+v, want reporting.sales_summary VIEW", view)
	}

	table, err := extractor.ExtractTableSchemaInSchema(context.Background(), "source-under-test", "public", "sales_summary")
	if err != nil {
		t.Fatalf("ExtractTableSchemaInSchema(public) error = %v", err)
	}
	if table.Type != "TABLE" || table.Schema != "public" {
		t.Fatalf("public table = %+v, want public.sales_summary TABLE", table)
	}

	if _, err := extractor.ExtractTableSchemaInSchema(context.Background(), "source-under-test", "", "sales_summary"); !errors.Is(err, ErrAmbiguousTable) {
		t.Fatalf("unqualified sales_summary error = %v, want ErrAmbiguousTable", err)
	}
}

// A view that does not exist in the catalog is reported as not found, even
// when a table with other names is present.
func TestPostgresAbsentViewReportsNotFound(t *testing.T) {
	extractor := newLookupExtractor(t, model.DatabaseTypePostgreSQL, func(query string) driver.Rows {
		if strings.Contains(query, "pg_tables") && strings.Contains(query, "pg_views") {
			return &cannedRows{
				columns: []string{"tablename", "tabletype", "schemaname"},
				values:  [][]driver.Value{{"orders", "TABLE", "public"}},
			}
		}
		return &cannedRows{}
	})

	_, err := extractor.ExtractTableSchemaInSchema(context.Background(), "source-under-test", "public", "missing_view")
	if !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("ExtractTableSchemaInSchema() error = %v, want ErrTableNotFound", err)
	}
}
