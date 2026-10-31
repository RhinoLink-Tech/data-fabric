package file_system

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

type hdfsCSVFileClientFixture struct {
	data       []byte
	info       HDFSFileInfo
	readErr    error
	statErr    error
	readLimit  int64
	readCalls  int
	closeCalls int
}

func (f *hdfsCSVFileClientFixture) ReadFileLimited(_ context.Context, _ string, maxBytes int64) ([]byte, error) {
	f.readCalls++
	f.readLimit = maxBytes
	if f.readErr != nil {
		return nil, f.readErr
	}
	if int64(len(f.data)) > maxBytes {
		return nil, errors.New("fixture file exceeds limit")
	}
	return append([]byte(nil), f.data...), nil
}
func (f *hdfsCSVFileClientFixture) GetFileInfo(context.Context, string) (*HDFSFileInfo, error) {
	if f.statErr != nil {
		return nil, f.statErr
	}
	info := f.info
	return &info, nil
}
func (*hdfsCSVFileClientFixture) ListFiles(context.Context, string) ([]HDFSFileInfo, error) {
	return nil, nil
}
func (*hdfsCSVFileClientFixture) GetCapacity(context.Context) (*HDFSCapacity, error) {
	return &HDFSCapacity{}, nil
}
func (f *hdfsCSVFileClientFixture) Close() error {
	f.closeCalls++
	return nil
}

func TestHDFSCSVOpenWithConfigRunsReadOnlySQLAgainstOneFile(t *testing.T) {
	fixture := &hdfsCSVFileClientFixture{
		data: []byte("id,name,active\n1,Ada,true\n2,Lin,false\n3,Mei,true\n"),
		info: HDFSFileInfo{Name: "events.csv", Size: 54},
	}
	driver := NewHDFSCSVDriverWithFileClient(fixture)
	db, err := driver.OpenWithConfig(context.Background(), hdfsCSVTestConfig(nil))
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}

	rows, err := db.QueryContext(context.Background(), "SELECT id, name FROM events WHERE id >= 2 ORDER BY id DESC LIMIT 1")
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("QueryContext() returned no row; rows.Err() = %v", rows.Err())
	}
	var id int64
	var name string
	if err := rows.Scan(&id, &name); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if id != 3 || name != "Mei" {
		t.Fatalf("query row = (%d, %q), want (3, Mei)", id, name)
	}
	if rows.Next() {
		t.Fatal("query returned more than one row")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() = %v", err)
	}

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events WHERE active = true").Scan(&count); err != nil {
		t.Fatalf("COUNT(*) error = %v", err)
	}
	if count != 2 {
		t.Fatalf("COUNT(*) = %d, want 2", count)
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
	if fixture.closeCalls != 1 {
		t.Fatalf("file client Close() called %d times, want once", fixture.closeCalls)
	}
}

func TestReopenableHDFSCSVFileClientReopensAfterDatabaseConnectionClose(t *testing.T) {
	first := &hdfsCSVFileClientFixture{info: HDFSFileInfo{Name: "events.csv", Size: 10}}
	second := &hdfsCSVFileClientFixture{info: HDFSFileInfo{Name: "events.csv", Size: 10}}
	clients := []HDFSCSVFileClient{first, second}
	created := 0
	client := newReopenableHDFSCSVFileClient(func(context.Context) (HDFSCSVFileClient, error) {
		result := clients[created]
		created++
		return result, nil
	})

	if _, err := client.GetFileInfo(context.Background(), "/warehouse/events.csv"); err != nil {
		t.Fatalf("first GetFileInfo() error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	if _, err := client.GetFileInfo(context.Background(), "/warehouse/events.csv"); err != nil {
		t.Fatalf("second GetFileInfo() error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if created != 2 || first.closeCalls != 1 || second.closeCalls != 1 {
		t.Fatalf("created=%d, close calls=(%d,%d), want two clients each closed once", created, first.closeCalls, second.closeCalls)
	}
}

func TestHDFSCSVOpenWithConfigRejectsDirectoryAndOversizedFile(t *testing.T) {
	for _, tc := range []struct {
		name string
		info HDFSFileInfo
	}{
		{name: "directory", info: HDFSFileInfo{Name: "events.csv", IsDir: true}},
		{name: "oversized file", info: HDFSFileInfo{Name: "events.csv", Size: defaultHDFSCSVMaxFileBytes + 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &hdfsCSVFileClientFixture{data: []byte("id\n1\n"), info: tc.info}
			db, err := NewHDFSCSVDriverWithFileClient(fixture).OpenWithConfig(context.Background(), hdfsCSVTestConfig(nil))
			if err != nil {
				t.Fatalf("OpenWithConfig() error = %v", err)
			}
			err = db.PingContext(context.Background())
			if err == nil {
				t.Fatal("PingContext() succeeded for an invalid file")
			}
			_ = db.Close()
		})
	}
}

func TestValidateHDFSCSVConfigAcceptsJSONNumbersAndBounds(t *testing.T) {
	config := hdfsCSVTestConfig(map[string]interface{}{
		"max_file_bytes":   64.0 * 1024 * 1024,
		"skip_header_rows": float64(1),
	})
	if err := ValidateHDFSCSVConfig(config); err != nil {
		t.Fatalf("ValidateHDFSCSVConfig() error = %v", err)
	}
	if got := HDFSCSVMaxFileBytes(config); got != maxHDFSCSVMaxFileBytes {
		t.Fatalf("HDFSCSVMaxFileBytes() = %d, want %d", got, maxHDFSCSVMaxFileBytes)
	}
}

func TestValidateHDFSCSVConfigRejectsUnsafeOrUnsupportedSettings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*model.DataSourceConfig)
	}{
		{name: "relative path", mutate: func(c *model.DataSourceConfig) { c.Database = "warehouse/events.csv" }},
		{name: "traversal path", mutate: func(c *model.DataSourceConfig) { c.Database = "/warehouse/../secret.csv" }},
		{name: "not csv", mutate: func(c *model.DataSourceConfig) { c.Database = "/warehouse/events.parquet" }},
		{name: "multi-character delimiter", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["delimiter"] = "||" }},
		{name: "negative header skip", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["skip_header_rows"] = float64(-1) }},
		{name: "fractional row skip", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["skip_header_rows"] = 1.5 }},
		{name: "oversized limit", mutate: func(c *model.DataSourceConfig) {
			c.AdditionalProps["max_file_bytes"] = float64(maxHDFSCSVMaxFileBytes + 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := hdfsCSVTestConfig(nil)
			tc.mutate(config)
			if err := ValidateHDFSCSVConfig(config); err == nil {
				t.Fatal("ValidateHDFSCSVConfig() succeeded for invalid configuration")
			}
		})
	}
}

func hdfsCSVTestConfig(additional map[string]interface{}) *model.DataSourceConfig {
	props := map[string]interface{}{"table_name": "events"}
	for key, value := range additional {
		props[key] = value
	}
	return &model.DataSourceConfig{
		Host: "namenode.local", Port: 8020, Database: "/warehouse/events.csv", Username: "hdfs",
		AdditionalProps: props,
	}
}
