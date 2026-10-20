package metadata

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// cannedRows serves one canned result set through the database/sql driver
// interfaces, keeping the tests on the standard library only.
type cannedRows struct {
	columns []string
	values  [][]driver.Value
	pos     int
}

func (r *cannedRows) Columns() []string { return r.columns }
func (r *cannedRows) Close() error      { return nil }
func (r *cannedRows) Next(dest []driver.Value) error {
	if r.pos >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.pos])
	r.pos++
	return nil
}

type cannedConn struct {
	lookup func(query string) driver.Rows
}

func (c cannedConn) Prepare(query string) (driver.Stmt, error) {
	return cannedStmt{lookup: c.lookup, query: query}, nil
}
func (cannedConn) Close() error              { return nil }
func (cannedConn) Begin() (driver.Tx, error) { return nil, errors.New("transactions unused") }

type cannedStmt struct {
	query  string
	lookup func(string) driver.Rows
}

func (s cannedStmt) Close() error  { return nil }
func (s cannedStmt) NumInput() int { return -1 }
func (s cannedStmt) Query([]driver.Value) (driver.Rows, error) {
	if rows := s.lookup(s.query); rows != nil {
		return rows, nil
	}
	return &cannedRows{}, nil
}
func (cannedStmt) Exec([]driver.Value) (driver.Result, error) {
	return nil, errors.New("exec unused")
}

type cannedConnector struct {
	lookup  func(query string) driver.Rows
	queries *[]string
}

func (c cannedConnector) Connect(context.Context) (driver.Conn, error) {
	return cannedConn{lookup: func(query string) driver.Rows {
		if c.queries != nil {
			*c.queries = append(*c.queries, query)
		}
		return c.lookup(query)
	}}, nil
}
func (cannedConnector) Driver() driver.Driver { return cannedDriver{} }

type cannedDriver struct{}

func (cannedDriver) Open(string) (driver.Conn, error) { return nil, errors.New("open via connector") }

type cannedConnectionProvider struct {
	db *sql.DB
}

func (p *cannedConnectionProvider) GetConnection(context.Context, *model.DataSource) (*sql.DB, error) {
	return p.db, nil
}

// newTableMetadataExtractor wires an extractor to a canned PostgreSQL source:
// two tables ("orders", "customers") where only "orders" has column and
// primary-key details. The returned queries slice records every issued query.
func newTableMetadataExtractor(t *testing.T) (*MetadataExtractor, *[]string) {
	t.Helper()

	var queries []string
	lookup := func(query string) driver.Rows {
		switch {
		case strings.Contains(query, "FROM pg_tables"):
			return &cannedRows{
				columns: []string{"tablename", "tabletype", "schemaname"},
				values: [][]driver.Value{
					{"orders", "TABLE", "public"},
					{"customers", "TABLE", "public"},
				},
			}
		case strings.Contains(query, "information_schema.columns") && strings.Contains(query, "'orders'"):
			return &cannedRows{
				columns: []string{"column_name", "data_type", "is_nullable", "column_default", "comment"},
				values: [][]driver.Value{
					{"id", "integer", "NO", nil, ""},
					{"amount", "numeric", "YES", nil, ""},
				},
			}
		case strings.Contains(query, "pg_index") && !strings.Contains(query, "pg_indexes"):
			return &cannedRows{
				columns: []string{"attname"},
				values:  [][]driver.Value{{"id"}},
			}
		default:
			return &cannedRows{}
		}
	}
	db := sql.OpenDB(cannedConnector{lookup: lookup, queries: &queries})

	source := &model.DataSource{
		ID:   "pg-source",
		Type: model.DatabaseTypePostgreSQL,
	}
	return &MetadataExtractor{
		connPool: &cannedConnectionProvider{db: db},
		sources:  &dataSourceLookupStub{dataSource: source},
		registry: database.NewDriverRegistry(),
	}, &queries
}

// ExtractTableSchema returns the requested table with column details fetched
// only for that table (#184): the sibling table is never described.
func TestExtractTableSchemaReturnsOnlyRequestedTable(t *testing.T) {
	extractor, queries := newTableMetadataExtractor(t)

	table, err := extractor.ExtractTableSchema(context.Background(), "pg-source", "orders")
	if err != nil {
		t.Fatalf("ExtractTableSchema() error = %v", err)
	}

	if table.Name != "orders" || table.Schema != "public" || table.Type != "TABLE" {
		t.Fatalf("table identity = %q/%q/%q, want orders/public/TABLE", table.Name, table.Schema, table.Type)
	}
	if len(table.Columns) != 2 {
		t.Fatalf("columns = %+v, want 2", table.Columns)
	}
	if table.Columns[0].Name != "id" || table.Columns[0].Type != "integer" || table.Columns[0].Nullable {
		t.Errorf("first column = %+v, want id/integer/not nullable", table.Columns[0])
	}
	if table.Columns[1].Name != "amount" || !table.Columns[1].Nullable {
		t.Errorf("second column = %+v, want amount/nullable (information_schema YES)", table.Columns[1])
	}
	if len(table.PrimaryKey) != 1 || table.PrimaryKey[0] != "id" {
		t.Errorf("primary key = %+v, want [id]", table.PrimaryKey)
	}

	for _, query := range *queries {
		if strings.Contains(query, "'customers'") {
			t.Errorf("sibling table was queried too: %s", query)
		}
	}
}

// A table missing from the source fails with ErrTableNotFound instead of a
// guessed schema (#184).
func TestExtractTableSchemaReportsMissingTable(t *testing.T) {
	extractor, _ := newTableMetadataExtractor(t)

	_, err := extractor.ExtractTableSchema(context.Background(), "pg-source", "missing")
	if !errors.Is(err, ErrTableNotFound) {
		t.Fatalf("ExtractTableSchema() error = %v, want ErrTableNotFound", err)
	}
}

// Sources whose driver has no schema discovery fail before a connection is
// attempted, matching ExtractSchema (#184: no guessed schema).
func TestExtractTableSchemaRejectsUnsupportedSourceBeforeOpeningConnection(t *testing.T) {
	sources := &dataSourceLookupStub{dataSource: &model.DataSource{
		ID: "unknown-source", Type: model.DatabaseType("not_registered"),
	}}
	pool := &metadataConnectionProviderStub{}
	extractor := &MetadataExtractor{sources: sources, connPool: pool, registry: database.NewDriverRegistry()}

	_, err := extractor.ExtractTableSchema(context.Background(), sources.dataSource.ID, "orders")
	if !errors.Is(err, ErrSchemaDiscoveryUnsupported) {
		t.Fatalf("ExtractTableSchema() error = %v, want ErrSchemaDiscoveryUnsupported", err)
	}
	if pool.calls != 0 {
		t.Fatalf("connection pool called %d times for unsupported source", pool.calls)
	}
}
