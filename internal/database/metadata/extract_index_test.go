package metadata

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// SHOW INDEX returns 15 columns on MySQL 8.4 and 14 on MariaDB. Each result must
// still yield every index with its uniqueness and ordered component columns,
// instead of failing the scan and reporting an empty index list (#530).
func TestExtractTableSchemaKeepsMySQLShowIndexRows(t *testing.T) {
	mysqlCols := []string{"Table", "Non_unique", "Key_name", "Seq_in_index", "Column_name", "Collation", "Cardinality", "Sub_part", "Packed", "Null", "Index_type", "Comment", "Index_comment", "Visible", "Expression"}
	mariaCols := mysqlCols[:14]

	showIndexRows := func(columns []string, empty bool) *cannedRows {
		row := func(nonUnique, keyName, seq, colName string) []driver.Value {
			full := []driver.Value{"orders", nonUnique, keyName, seq, colName, "A", int64(10), nil, nil, "", "BTREE", "", "", "YES", nil}
			return full[:len(columns)]
		}
		if empty {
			return &cannedRows{columns: columns}
		}
		return &cannedRows{
			columns: columns,
			values: [][]driver.Value{
				row("0", "PRIMARY", "1", "id"),
				row("1", "ix_customer", "1", "customer_id"),
				// Composite rows arrive out of Seq_in_index order on purpose.
				row("0", "uq_pair", "2", "code"),
				row("0", "uq_pair", "1", "region"),
			},
		}
	}

	for _, tc := range []struct {
		name    string
		typ     model.DatabaseType
		columns []string
		empty   bool
	}{
		{"MySQL", model.DatabaseTypeMySQL, mysqlCols, false},
		{"MariaDB", model.DatabaseTypeMariaDB, mariaCols, false},
		{"MySQL no indexes", model.DatabaseTypeMySQL, mysqlCols, true},
		{"MariaDB no indexes", model.DatabaseTypeMariaDB, mariaCols, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extractor := newLookupExtractor(t, tc.typ, func(query string) driver.Rows {
				switch {
				case strings.Contains(query, "table_schema = DATABASE()"):
					return &cannedRows{
						columns: []string{"table_name", "table_type", "table_schema"},
						values:  [][]driver.Value{{"orders", "BASE TABLE", "sample"}},
					}
				case strings.Contains(query, "SHOW COLUMNS FROM `orders`"):
					return &cannedRows{
						columns: []string{"Field", "Type", "Null", "Key", "Default", "Extra"},
						values:  [][]driver.Value{{"id", "bigint", "NO", "PRI", nil, ""}},
					}
				case strings.Contains(query, "SHOW INDEX FROM `orders`"):
					return showIndexRows(tc.columns, tc.empty)
				default:
					return &cannedRows{}
				}
			})

			table, err := extractor.ExtractTableSchema(context.Background(), "source-under-test", "orders")
			if err != nil {
				t.Fatalf("ExtractTableSchema() error = %v", err)
			}

			got := map[string]IndexSchema{}
			for _, idx := range table.Indexes {
				got[idx.Name] = idx
			}
			if tc.empty {
				if len(table.Indexes) != 0 {
					t.Fatalf("indexes = %+v, want none for a table without indexes", table.Indexes)
				}
				return
			}
			if len(got) != 3 {
				t.Fatalf("indexes = %+v, want PRIMARY, ix_customer and uq_pair", table.Indexes)
			}
			if idx := got["ix_customer"]; idx.Unique || len(idx.Columns) != 1 || idx.Columns[0] != "customer_id" {
				t.Errorf("ix_customer = %+v, want non-unique on [customer_id]", idx)
			}
			if idx := got["uq_pair"]; !idx.Unique || len(idx.Columns) != 2 || idx.Columns[0] != "region" || idx.Columns[1] != "code" {
				t.Errorf("uq_pair = %+v, want unique on [region code] in Seq_in_index order", idx)
			}
		})
	}
}
