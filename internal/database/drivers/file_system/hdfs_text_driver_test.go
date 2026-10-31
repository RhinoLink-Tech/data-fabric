package file_system

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestHDFSTextOpenWithConfigExposesPhysicalLinesAsReadOnlySQL(t *testing.T) {
	data := []byte("first\r\n\r\nlast")
	fixture := &hdfsCSVFileClientFixture{data: data, info: HDFSFileInfo{Name: "events.log", Size: int64(len(data))}}
	config := hdfsCSVTestConfig(nil)
	config.Database = "/warehouse/events.log"
	db, err := NewHDFSTextDriverWithFileClient(fixture).OpenWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT line_number, line FROM events ORDER BY line_number DESC LIMIT 2")
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("query returned no first row; rows.Err() = %v", rows.Err())
	}
	var lineNumber int64
	var line string
	if err := rows.Scan(&lineNumber, &line); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if lineNumber != 3 || line != "last" {
		t.Fatalf("first row = (%d, %q), want (3, last)", lineNumber, line)
	}
	if !rows.Next() {
		t.Fatalf("query returned no blank row; rows.Err() = %v", rows.Err())
	}
	if err := rows.Scan(&lineNumber, &line); err != nil {
		t.Fatalf("Scan() blank line error = %v", err)
	}
	if lineNumber != 2 || line != "" {
		t.Fatalf("blank row = (%d, %q), want (2, empty)", lineNumber, line)
	}
	if rows.Next() {
		t.Fatal("query returned more than two rows")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() = %v", err)
	}

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events").Scan(&count); err != nil {
		t.Fatalf("COUNT(*) error = %v", err)
	}
	if count != 3 {
		t.Fatalf("COUNT(*) = %d, want 3", count)
	}
	if _, err := db.ExecContext(context.Background(), "DELETE FROM events"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("ExecContext() error = %v, want read-only rejection", err)
	}
	if fixture.readLimit != defaultHDFSCSVMaxFileBytes {
		t.Fatalf("read limit = %d, want default %d", fixture.readLimit, defaultHDFSCSVMaxFileBytes)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestHDFSTextQueryPropagatesCancellationAndReadErrors(t *testing.T) {
	data := []byte("first\nsecond")
	fixture := &hdfsCSVFileClientFixture{data: data, info: HDFSFileInfo{Name: "events.txt", Size: int64(len(data))}}
	config := hdfsCSVTestConfig(nil)
	config.Database = "/warehouse/events.txt"
	db, err := NewHDFSTextDriverWithFileClient(fixture).OpenWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	defer db.Close()
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

func TestHDFSTextOpenRejectsOversizedFile(t *testing.T) {
	fixture := &hdfsCSVFileClientFixture{
		data: []byte("small"), info: HDFSFileInfo{Name: "events.txt", Size: defaultHDFSCSVMaxFileBytes + 1},
	}
	config := hdfsCSVTestConfig(nil)
	config.Database = "/warehouse/events.txt"
	db, err := NewHDFSTextDriverWithFileClient(fixture).OpenWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	if err := db.PingContext(context.Background()); err == nil || !strings.Contains(err.Error(), "configured maximum") {
		t.Fatalf("PingContext() error = %v, want file-size rejection", err)
	}
	_ = db.Close()
}

func TestTextSQLDatabaseRejectsInvalidUTF8AndExcessLines(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{name: "invalid UTF-8", data: []byte{0xff}, want: "valid UTF-8"},
		{name: "line limit", data: []byte("one\ntwo\nthree"), want: "line limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := object_storage.NewTextSQLDatabase(object_storage.TextSQLDatabaseConfig{
				DriverName: "hdfs-text", ObjectKey: "/warehouse/events.txt", TableName: "events",
				MaxFileBytes: 1024, MaxRows: 2,
			}, func(context.Context, string) error { return nil }, func(context.Context, string) ([]byte, error) {
				return tc.data, nil
			}, nil)
			if err != nil {
				t.Fatalf("NewTextSQLDatabase() error = %v", err)
			}
			defer db.Close()
			if _, err := db.QueryContext(context.Background(), "SELECT * FROM events"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("QueryContext() error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestValidateHDFSTextConfigRejectsUnsafeOrUnsupportedSettings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*model.DataSourceConfig)
	}{
		{name: "CSV path", mutate: func(c *model.DataSourceConfig) { c.Database = "/warehouse/events.csv" }},
		{name: "relative path", mutate: func(c *model.DataSourceConfig) { c.Database = "warehouse/events.txt" }},
		{name: "traversal path", mutate: func(c *model.DataSourceConfig) { c.Database = "/warehouse/../secret.txt" }},
		{name: "oversized limit", mutate: func(c *model.DataSourceConfig) {
			c.AdditionalProps["max_file_bytes"] = float64(maxHDFSCSVMaxFileBytes + 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := hdfsCSVTestConfig(nil)
			config.Database = "/warehouse/events.txt"
			tc.mutate(config)
			if err := ValidateHDFSTextConfig(config); err == nil {
				t.Fatal("ValidateHDFSTextConfig() succeeded for invalid configuration")
			}
		})
	}
}
