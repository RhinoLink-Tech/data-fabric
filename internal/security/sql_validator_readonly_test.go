package security

import (
	"errors"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// TestReadOnlyDefenseBlocksWritesAndDangerousFunctions locks the
// application-layer defense required by #20: typical DML/DDL, stored
// procedures, and dangerous functions must never reach a data source, while
// ordinary read-only SELECTs keep passing.
func TestReadOnlyDefenseBlocksWritesAndDangerousFunctions(t *testing.T) {
	validator := NewSQLValidator(10000)

	mustReject := []struct {
		name string
		sql  string
	}{
		// DML
		{"insert", "INSERT INTO orders (id) VALUES (1)"},
		{"update", "UPDATE orders SET status = 'paid' WHERE id = 1"},
		{"delete", "DELETE FROM orders WHERE id = 1"},
		{"merge", "MERGE INTO orders o USING staged s ON o.id = s.id WHEN MATCHED THEN UPDATE SET o.status = s.status"},
		{"replace", "REPLACE INTO orders (id) VALUES (1)"},

		// DDL and privileges
		{"create table", "CREATE TABLE evil (id INT)"},
		{"drop table", "DROP TABLE orders"},
		{"drop index", "DROP INDEX idx_orders"},
		{"alter table", "ALTER TABLE orders ADD COLUMN note TEXT"},
		{"truncate", "TRUNCATE TABLE orders"},
		{"grant", "GRANT ALL ON orders TO public"},
		{"revoke", "REVOKE SELECT ON orders FROM analyst"},

		// Stored procedures and stacked statements
		{"call procedure", "CALL refresh_reports()"},
		{"exec", "EXEC sp_refresh_reports"},
		{"execute statement", "EXECUTE immediate_drop"},
		{"stacked statements", "SELECT 1; DROP TABLE orders"},
		{"stacked statements with repeated letters", "SELECT id FROM aaaccounts; DROP TABLE aaaccounts"},
		{"dangerous function with repeated identifier", "SELECT pg_sleep(1) FROM aaaccounts"},

		// Dangerous functions reachable from a SELECT (#20)
		{"mysql load_file", "SELECT LOAD_FILE('/etc/passwd')"},
		{"mysql into outfile", "SELECT * FROM orders INTO OUTFILE '/tmp/leak'"},
		{"mysql into dumpfile", "SELECT * FROM orders INTO DUMPFILE '/tmp/leak'"},
		{"postgres pg_read_file", "SELECT pg_read_file('/etc/passwd')"},
		{"postgres pg_read_binary_file", "SELECT pg_read_binary_file('/etc/passwd')"},
		{"postgres pg_ls_dir", "SELECT pg_ls_dir('/etc')"},
		{"postgres lo_import", "SELECT lo_import('/etc/passwd')"},
		{"postgres lo_export", "SELECT lo_export(12345, '/tmp/leak')"},
		{"postgres dblink", "SELECT * FROM dblink('host=evil dbname=x', 'SELECT secret') AS t(s text)"},
		{"postgres pg_sleep", "SELECT pg_sleep(60)"},
	}

	for _, tt := range mustReject {
		t.Run("reject "+tt.name, func(t *testing.T) {
			err := validator.ValidateStatementForDataSource(tt.sql, model.DatabaseTypeMySQL)
			if err == nil {
				t.Errorf("ValidateStatementForDataSource(%q) = nil, want rejection", tt.sql)
				return
			}
			if !errors.Is(err, ErrNotSelectQuery) && !errors.Is(err, ErrDangerousKeyword) &&
				!errors.Is(err, ErrSQLInjection) && !errors.Is(err, ErrSQLSyntaxError) {
				t.Errorf("ValidateStatementForDataSource(%q) = %v, want a read-only sentinel error", tt.sql, err)
			}
		})
	}

	mustPass := []struct {
		name string
		sql  string
	}{
		{"plain select", "SELECT * FROM orders LIMIT 10"},
		{"projection and filter", "SELECT id, status FROM orders WHERE amount > 100 ORDER BY created_at DESC"},
		{"join with group by", "SELECT c.name, COUNT(*) FROM orders o JOIN customers c ON o.customer_id = c.id GROUP BY c.name"},
		// Column and alias names that contain blocked words must not trip
		// the word-boundary checks.
		{"column named update_time", "SELECT update_time, created_by FROM orders"},
		{"column named dblink_id", "SELECT dblink_id FROM links"},
		{"alias named created", "SELECT id AS created FROM orders"},
	}

	for _, tt := range mustPass {
		t.Run("allow "+tt.name, func(t *testing.T) {
			if err := validator.ValidateStatementForDataSource(tt.sql, model.DatabaseTypeMySQL); err != nil {
				t.Errorf("ValidateStatementForDataSource(%q) = %v, want nil", tt.sql, err)
			}
		})
	}
}
