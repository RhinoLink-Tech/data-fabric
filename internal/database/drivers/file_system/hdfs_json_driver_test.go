package file_system

import (
	"context"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestHDFSJSONOpenWithConfigQueriesSupportedDocumentForms(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		data string
	}{
		{name: "object", path: "/warehouse/events.json", data: `{"id":1,"name":"Ada","active":true}`},
		{name: "array", path: "/warehouse/events.json", data: `[{"id":1,"name":"Ada","active":true},{"id":2,"name":"Lin","active":false},{"id":3,"name":"Mei","active":true}]`},
		{name: "json lines", path: "/warehouse/events.jsonl", data: "{\"id\":1,\"name\":\"Ada\",\"active\":true}\n{\"id\":2,\"name\":\"Lin\",\"active\":false}\n{\"id\":3,\"name\":\"Mei\",\"active\":true}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &hdfsCSVFileClientFixture{
				data: []byte(tc.data), info: HDFSFileInfo{Name: "events" + ".json", Size: int64(len(tc.data))},
			}
			config := hdfsJSONTestConfig(nil)
			config.Database = tc.path
			db, err := NewHDFSJSONDriverWithFileClient(fixture).OpenWithConfig(context.Background(), config)
			if err != nil {
				t.Fatalf("OpenWithConfig() error = %v", err)
			}
			defer db.Close()

			if err := db.PingContext(context.Background()); err != nil {
				t.Fatalf("PingContext() error = %v", err)
			}
			rows, err := db.QueryContext(context.Background(), "SELECT id, name FROM events ORDER BY id DESC LIMIT 1")
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
			wantID, wantName := int64(3), "Mei"
			if tc.name == "object" {
				wantID, wantName = 1, "Ada"
			}
			if id != wantID || name != wantName {
				t.Fatalf("query row = (%d, %q), want (%d, %s)", id, name, wantID, wantName)
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
			wantCount := int64(2)
			if tc.name == "object" {
				wantCount = 1
			}
			if count != wantCount {
				t.Fatalf("COUNT(*) = %d, want %d", count, wantCount)
			}
			if _, err := db.ExecContext(context.Background(), "DELETE FROM events"); err == nil || !strings.Contains(err.Error(), "read-only") {
				t.Fatalf("ExecContext() error = %v, want read-only rejection", err)
			}
			if fixture.readLimit != defaultHDFSCSVMaxFileBytes {
				t.Fatalf("read limit = %d, want default %d", fixture.readLimit, defaultHDFSCSVMaxFileBytes)
			}
		})
	}
}

func TestHDFSJSONHonorsConfiguredFileByteLimit(t *testing.T) {
	data := []byte(`{"id":1}`)
	fixture := &hdfsCSVFileClientFixture{
		data: data, info: HDFSFileInfo{Name: "events.json", Size: int64(len(data))},
	}
	config := hdfsJSONTestConfig(map[string]interface{}{"max_file_bytes": float64(64)})
	db, err := NewHDFSJSONDriverWithFileClient(fixture).OpenWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	defer db.Close()
	if _, err := db.QueryContext(context.Background(), "SELECT id FROM events"); err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	if fixture.readLimit != 64 {
		t.Fatalf("read limit = %d, want configured limit 64", fixture.readLimit)
	}
}

func TestHDFSJSONListsOnlySupportedFileExtensions(t *testing.T) {
	fixture := &hdfsCSVFileClientFixture{files: []HDFSFileInfo{
		{Name: "events.json"}, {Name: "events.jsonl"}, {Name: "events.ndjson"}, {Name: "events.jsonlines"},
		{Name: "events.csv"}, {Name: "directory.json", IsDir: true},
	}}
	files, err := NewHDFSJSONDriverWithFileClient(fixture).ListJSONFiles(context.Background(), "/warehouse")
	if err != nil {
		t.Fatalf("ListJSONFiles() error = %v", err)
	}
	if len(files) != 4 {
		t.Fatalf("ListJSONFiles() returned %d entries, want four supported JSON files: %+v", len(files), files)
	}
}

func TestHDFSJSONOpenWithConfigRejectsDirectoriesAndOversizedFiles(t *testing.T) {
	for _, tc := range []struct {
		name string
		info HDFSFileInfo
	}{
		{name: "directory", info: HDFSFileInfo{Name: "events.json", IsDir: true}},
		{name: "oversized file", info: HDFSFileInfo{Name: "events.json", Size: defaultHDFSCSVMaxFileBytes + 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &hdfsCSVFileClientFixture{data: []byte(`{"id":1}`), info: tc.info}
			db, err := NewHDFSJSONDriverWithFileClient(fixture).OpenWithConfig(context.Background(), hdfsJSONTestConfig(nil))
			if err != nil {
				t.Fatalf("OpenWithConfig() error = %v", err)
			}
			if err := db.PingContext(context.Background()); err == nil {
				t.Fatal("PingContext() succeeded for an invalid file")
			}
			_ = db.Close()
		})
	}
}

func TestHDFSJSONRejectsNestedValuesThatCannotBecomeSQLColumns(t *testing.T) {
	fixture := &hdfsCSVFileClientFixture{
		data: []byte(`{"id":1,"details":{"city":"Paris"}}`),
		info: HDFSFileInfo{Name: "events.json", Size: 37},
	}
	db, err := NewHDFSJSONDriverWithFileClient(fixture).OpenWithConfig(context.Background(), hdfsJSONTestConfig(nil))
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	defer db.Close()
	if _, err := db.QueryContext(context.Background(), "SELECT id FROM events"); err == nil || !strings.Contains(err.Error(), "top-level scalar JSON fields") {
		t.Fatalf("QueryContext() error = %v, want nested-field rejection", err)
	}
}

func TestValidateHDFSJSONConfigRejectsUnsupportedInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*model.DataSourceConfig)
	}{
		{name: "relative path", mutate: func(c *model.DataSourceConfig) { c.Database = "warehouse/events.json" }},
		{name: "path traversal", mutate: func(c *model.DataSourceConfig) { c.Database = "/warehouse/../secret.json" }},
		{name: "unsupported extension", mutate: func(c *model.DataSourceConfig) { c.Database = "/warehouse/events.csv" }},
		{name: "oversized limit", mutate: func(c *model.DataSourceConfig) {
			c.AdditionalProps["max_file_bytes"] = float64(maxHDFSCSVMaxFileBytes + 1)
		}},
		{name: "invalid limit", mutate: func(c *model.DataSourceConfig) { c.AdditionalProps["max_file_bytes"] = "large" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := hdfsJSONTestConfig(nil)
			tc.mutate(config)
			if err := ValidateHDFSJSONConfig(config); err == nil {
				t.Fatal("ValidateHDFSJSONConfig() succeeded for invalid configuration")
			}
		})
	}
}

func TestHDFSJSONTableNameIsInferredFromFileName(t *testing.T) {
	config := hdfsJSONTestConfig(nil)
	config.AdditionalProps = nil
	got, err := HDFSJSONTableName(config)
	if err != nil || got != "events" {
		t.Fatalf("HDFSJSONTableName() = %q, %v; want events", got, err)
	}
}

func TestHDFSJSONLegacyQueryJSONFileDecodesRecords(t *testing.T) {
	fixture := &hdfsCSVFileClientFixture{
		data: []byte("{\"id\":1}\n{\"id\":2}\n"),
		info: HDFSFileInfo{Name: "events.jsonl", Size: 18},
	}
	result, err := NewHDFSJSONDriverWithFileClient(fixture).QueryJSONFile(context.Background(), "/warehouse/events.jsonl")
	if err != nil {
		t.Fatalf("QueryJSONFile() error = %v", err)
	}
	if result.NumRows != 2 || result.Rows[1]["id"] != int64(2) || result.FilePath != "/warehouse/events.jsonl" {
		t.Fatalf("QueryJSONFile() result = %+v, want two decoded records", result)
	}
}

func hdfsJSONTestConfig(additional map[string]interface{}) *model.DataSourceConfig {
	props := map[string]interface{}{"table_name": "events"}
	for key, value := range additional {
		props[key] = value
	}
	return &model.DataSourceConfig{
		Host: "namenode.local", Port: 8020, Database: "/warehouse/events.json", Username: "hdfs",
		AdditionalProps: props,
	}
}
