package reference

import (
	"fmt"
	"os"
	"path/filepath"
)

// The temporary directory is private to the current user; container-readable
// files are mounted individually. Passwords are randomly generated per run.
func (s *composeStack) writeTrinoCatalogs() error {
	catalogs := []struct{ name, connector, url, password string }{
		{"mysql", "mysql", "jdbc:mysql://sample-source:3306", s.env["SAMPLE_READER_PASSWORD"]},
		{"postgresql", "postgresql", "jdbc:postgresql://sample-postgres:5432/sample", s.env["DF_REFERENCE_POSTGRES_READER_PASSWORD"]},
	}
	for _, c := range catalogs {
		config := fmt.Sprintf("connector.name=%s\nconnection-url=%s\nconnection-user=sample_reader\nconnection-password=%s\n", c.connector, c.url, c.password)
		if err := os.WriteFile(filepath.Join(s.directory, c.name+".properties"), []byte(config), 0o644); err != nil {
			return fmt.Errorf("write temporary Trino catalog %s: %w", c.name, err)
		}
	}
	return nil
}
