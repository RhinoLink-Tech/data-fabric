package file_system

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/linkedin/goavro/v2"
)

const hdfsAvroFixtureSchema = "{\"type\":\"record\",\"name\":\"Event\",\"fields\":[{\"name\":\"id\",\"type\":\"long\"},{\"name\":\"name\",\"type\":[\"null\",\"string\"],\"default\":null},{\"name\":\"active\",\"type\":\"boolean\"}]}"

func createHDFSAvroFixture(t *testing.T) []byte {
	t.Helper()
	codec, err := goavro.NewCodec(hdfsAvroFixtureSchema)
	if err != nil {
		t.Fatalf("create Avro codec: %v", err)
	}
	var output bytes.Buffer
	writer, err := goavro.NewOCFWriter(goavro.OCFConfig{W: &output, Codec: codec, CompressionName: "null"})
	if err != nil {
		t.Fatalf("create Avro OCF writer: %v", err)
	}
	records := []interface{}{
		map[string]interface{}{"id": int64(1), "name": goavro.Union("string", "Ada"), "active": false},
		map[string]interface{}{"id": int64(2), "name": goavro.Union("null", nil), "active": true},
		map[string]interface{}{"id": int64(3), "name": goavro.Union("string", "Mei"), "active": true},
	}
	if err := writer.Append(records); err != nil {
		t.Fatalf("write Avro OCF fixture: %v", err)
	}
	return append([]byte(nil), output.Bytes()...)
}

func TestHDFSAvroOpenWithConfigRunsReadOnlySQL(t *testing.T) {
	data := createHDFSAvroFixture(t)
	fixture := &hdfsCSVFileClientFixture{data: data, info: HDFSFileInfo{Name: "events.avro", Size: int64(len(data))}}
	config := hdfsCSVTestConfig(nil)
	config.Database = "/warehouse/events.avro"
	db, err := NewHDFSAvroDriverWithFileClient(fixture).OpenWithConfig(context.Background(), config)
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
	_ = rows.Close()

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
}

func TestHDFSAvroQueryPropagatesCancellationAndReadErrors(t *testing.T) {
	data := createHDFSAvroFixture(t)
	fixture := &hdfsCSVFileClientFixture{data: data, info: HDFSFileInfo{Name: "events.avro", Size: int64(len(data))}}
	config := hdfsCSVTestConfig(nil)
	config.Database = "/warehouse/events.avro"
	db, err := NewHDFSAvroDriverWithFileClient(fixture).OpenWithConfig(context.Background(), config)
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

func TestHDFSAvroOpenRejectsOversizedFile(t *testing.T) {
	fixture := &hdfsCSVFileClientFixture{
		data: []byte("small"), info: HDFSFileInfo{Name: "events.avro", Size: defaultHDFSCSVMaxFileBytes + 1},
	}
	config := hdfsCSVTestConfig(nil)
	config.Database = "/warehouse/events.avro"
	db, err := NewHDFSAvroDriverWithFileClient(fixture).OpenWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	if err := db.PingContext(context.Background()); err == nil || !strings.Contains(err.Error(), "configured maximum") {
		t.Fatalf("PingContext() error = %v, want file-size rejection", err)
	}
	_ = db.Close()
}

func TestAvroSQLDatabaseEnforcesDecodedRecordLimit(t *testing.T) {
	data := createHDFSAvroFixture(t)
	db, err := object_storage.NewAvroSQLDatabase(object_storage.AvroSQLDatabaseConfig{
		DriverName: "hdfs-avro", ObjectKey: "/warehouse/events.avro", TableName: "events",
		BatchSize: 1, MaxFileBytes: int64(len(data)), MaxRows: 1,
	}, func(context.Context, string) error { return nil }, func(context.Context, string) ([]byte, error) {
		return append([]byte(nil), data...), nil
	}, nil)
	if err != nil {
		t.Fatalf("NewAvroSQLDatabase() error = %v", err)
	}
	defer db.Close()
	if _, err := db.QueryContext(context.Background(), "SELECT * FROM events"); err == nil || !strings.Contains(err.Error(), "row limit") {
		t.Fatalf("QueryContext() error = %v, want record-limit rejection", err)
	}
}

func TestValidateHDFSAvroConfigRejectsUnsafeOrUnsupportedSettings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*model.DataSourceConfig)
	}{
		{name: "Parquet path", mutate: func(c *model.DataSourceConfig) { c.Database = "/warehouse/events.parquet" }},
		{name: "relative path", mutate: func(c *model.DataSourceConfig) { c.Database = "warehouse/events.avro" }},
		{name: "traversal path", mutate: func(c *model.DataSourceConfig) { c.Database = "/warehouse/../secret.avro" }},
		{name: "oversized file setting", mutate: func(c *model.DataSourceConfig) {
			c.AdditionalProps["max_file_bytes"] = float64(maxHDFSCSVMaxFileBytes + 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := hdfsCSVTestConfig(nil)
			config.Database = "/warehouse/events.avro"
			tc.mutate(config)
			if err := ValidateHDFSAvroConfig(config); err == nil {
				t.Fatal("ValidateHDFSAvroConfig() succeeded for invalid configuration")
			}
		})
	}
}
