package file_system

import (
	"context"
	"errors"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createHDFSORCFixture(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "object_storage", "testdata", "orc_split_elim.orc"))
	if err != nil {
		t.Fatalf("read ORC fixture: %v", err)
	}
	return data
}
func TestHDFSORCOpenWithConfigRunsReadOnlySQL(t *testing.T) {
	data := createHDFSORCFixture(t)
	fixture := &hdfsCSVFileClientFixture{data: data, info: HDFSFileInfo{Name: "events.orc", Size: int64(len(data))}}
	config := hdfsCSVTestConfig(nil)
	config.Database = "/warehouse/events.orc"
	db, err := NewHDFSORCDriverWithFileClient(fixture).OpenWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT userid, string1 FROM events WHERE userid = 2 ORDER BY string1 LIMIT 1")
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	if !rows.Next() {
		t.Fatalf("QueryContext() returned no row; rows.Err() = %v", rows.Err())
	}
	var id int64
	var value string
	if err := rows.Scan(&id, &value); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if id != 2 || value != "foo" {
		t.Fatalf("query row = (%d, %q), want (2, foo)", id, value)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("query returned an extra row or iteration error: %v", rows.Err())
	}
	_ = rows.Close()
	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events").Scan(&count); err != nil {
		t.Fatalf("COUNT(*) error = %v", err)
	}
	if count != 25000 {
		t.Fatalf("COUNT(*) = %d, want 25000", count)
	}
	if _, err := db.ExecContext(context.Background(), "DELETE FROM events"); err == nil || !strings.Contains(err.Error(), "read-only SELECT") {
		t.Fatalf("ExecContext() error = %v, want read-only rejection", err)
	}
	if fixture.readLimit != defaultHDFSCSVMaxFileBytes {
		t.Fatalf("read limit = %d, want default %d", fixture.readLimit, defaultHDFSCSVMaxFileBytes)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
func TestHDFSORCQueryPropagatesCancellationAndReadErrors(t *testing.T) {
	data := createHDFSORCFixture(t)
	fixture := &hdfsCSVFileClientFixture{data: data, info: HDFSFileInfo{Name: "events.orc", Size: int64(len(data))}}
	config := hdfsCSVTestConfig(nil)
	config.Database = "/warehouse/events.orc"
	db, err := NewHDFSORCDriverWithFileClient(fixture).OpenWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.QueryContext(ctx, "SELECT * FROM events"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled QueryContext() error = %v, want context.Canceled", err)
	}
	fixture.readErr = errors.New("fixture read failed")
	if _, err := db.QueryContext(context.Background(), "SELECT * FROM events"); err == nil || !strings.Contains(err.Error(), "fixture read failed") {
		t.Fatalf("QueryContext() error = %v, want fixture read failure", err)
	}
}
func TestHDFSORCOpenRejectsOversizedFile(t *testing.T) {
	fixture := &hdfsCSVFileClientFixture{data: []byte("small"), info: HDFSFileInfo{Name: "events.orc", Size: defaultHDFSCSVMaxFileBytes + 1}}
	config := hdfsCSVTestConfig(nil)
	config.Database = "/warehouse/events.orc"
	db, err := NewHDFSORCDriverWithFileClient(fixture).OpenWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	if err := db.PingContext(context.Background()); err == nil || !strings.Contains(err.Error(), "configured maximum") {
		t.Fatalf("PingContext() error = %v, want size rejection", err)
	}
	_ = db.Close()
}
func TestORCSQLDatabaseEnforcesDecodedRecordLimit(t *testing.T) {
	data := createHDFSORCFixture(t)
	db, err := object_storage.NewORCSQLDatabase(object_storage.ORCSQLDatabaseConfig{DriverName: "hdfs-orc", ObjectKey: "/warehouse/events.orc", TableName: "events", BatchSize: 1, MaxFileBytes: int64(len(data)), MaxRows: 1},
		func(context.Context, string) error { return nil },
		func(context.Context, string) ([]byte, error) { return append([]byte(nil), data...), nil }, nil)
	if err != nil {
		t.Fatalf("NewORCSQLDatabase() error = %v", err)
	}
	defer db.Close()
	if _, err := db.QueryContext(context.Background(), "SELECT * FROM events"); err == nil || !strings.Contains(err.Error(), "row limit") {
		t.Fatalf("QueryContext() error = %v, want record-limit rejection", err)
	}
}
func TestValidateHDFSORCConfigRejectsUnsafeOrUnsupportedSettings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*model.DataSourceConfig)
	}{
		{"Parquet path", func(c *model.DataSourceConfig) { c.Database = "/warehouse/events.parquet" }},
		{"relative path", func(c *model.DataSourceConfig) { c.Database = "warehouse/events.orc" }},
		{"traversal path", func(c *model.DataSourceConfig) { c.Database = "/warehouse/../secret.orc" }},
		{"oversized file setting", func(c *model.DataSourceConfig) {
			c.AdditionalProps["max_file_bytes"] = float64(maxHDFSCSVMaxFileBytes + 1)
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := hdfsCSVTestConfig(nil)
			c.Database = "/warehouse/events.orc"
			tc.mutate(c)
			if err := ValidateHDFSORCConfig(c); err == nil {
				t.Fatal("ValidateHDFSORCConfig() succeeded for invalid configuration")
			}
		})
	}
}

func TestHDFSORCLegacyFileAPIsUseBoundedORCReader(t *testing.T) {
	data := createHDFSORCFixture(t)
	fixture := &hdfsCSVFileClientFixture{
		data: data,
		info: HDFSFileInfo{Name: "events.orc", Size: int64(len(data))},
		files: []HDFSFileInfo{
			{Name: "events.orc"},
			{Name: "archive/events.ORC"},
			{Name: "folder.orc", IsDir: true},
			{Name: "notes.txt"},
		},
	}
	driver := NewHDFSORCDriverWithFileClient(fixture)
	files, err := driver.ListORCFiles(context.Background(), "/warehouse")
	if err != nil {
		t.Fatalf("ListORCFiles() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("ListORCFiles() returned %d entries, want two ORC files: %+v", len(files), files)
	}
	if _, err := driver.ListORCFiles(context.Background(), "/warehouse/../private"); err == nil {
		t.Fatal("ListORCFiles() accepted a traversal path")
	}
	result, err := driver.QueryORCFile(context.Background(), "/warehouse/events.orc")
	if err != nil {
		t.Fatalf("QueryORCFile() error = %v", err)
	}
	if result.NumRows != 25000 || result.BytesRead != int64(len(data)) || result.FilePath != "/warehouse/events.orc" {
		t.Fatalf("QueryORCFile() metadata = %+v, want 25,000 rows and bounded fixture size", result)
	}
	if len(result.Rows) != 25000 {
		t.Fatalf("QueryORCFile() rows = %d, want 25,000", len(result.Rows))
	}
}
