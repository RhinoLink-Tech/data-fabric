package file_system

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/writer"
)

type hdfsParquetFixtureRow struct {
	Label    string `parquet:"name=label, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Quantity int32  `parquet:"name=quantity, type=INT32"`
	Enabled  bool   `parquet:"name=enabled, type=BOOLEAN"`
}

func createHDFSParquetFixture(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	parquetWriter, err := writer.NewParquetWriterFromWriter(&output, new(hdfsParquetFixtureRow), 1)
	if err != nil {
		t.Fatalf("create Parquet writer: %v", err)
	}
	parquetWriter.CompressionType = parquet.CompressionCodec_UNCOMPRESSED
	for _, row := range []hdfsParquetFixtureRow{
		{Label: "one", Quantity: 1, Enabled: false},
		{Label: "two", Quantity: 2, Enabled: true},
		{Label: "three", Quantity: 3, Enabled: true},
	} {
		if err := parquetWriter.Write(row); err != nil {
			t.Fatalf("write Parquet row: %v", err)
		}
	}
	if err := parquetWriter.WriteStop(); err != nil {
		t.Fatalf("finish Parquet writer: %v", err)
	}
	return append([]byte(nil), output.Bytes()...)
}

func TestHDFSParquetOpenWithConfigRunsReadOnlySQL(t *testing.T) {
	data := createHDFSParquetFixture(t)
	fixture := &hdfsCSVFileClientFixture{data: data, info: HDFSFileInfo{Name: "events.parquet", Size: int64(len(data))}}
	config := hdfsCSVTestConfig(nil)
	config.Database = "/warehouse/events.parquet"
	db, err := NewHDFSParquetDriverWithFileClient(fixture).OpenWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT label, quantity FROM events WHERE quantity >= 2 ORDER BY quantity DESC LIMIT 1")
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	if !rows.Next() {
		t.Fatalf("QueryContext() returned no row; rows.Err() = %v", rows.Err())
	}
	var label string
	var quantity int64
	if err := rows.Scan(&label, &quantity); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if label != "three" || quantity != 3 {
		t.Fatalf("query row = (%q, %d), want (three, 3)", label, quantity)
	}
	if rows.Next() {
		t.Fatal("query returned more than one row")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err() = %v", err)
	}
	_ = rows.Close()

	var count int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events WHERE enabled = true").Scan(&count); err != nil {
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

func TestHDFSParquetQueryPropagatesCancellationAndReadErrors(t *testing.T) {
	data := createHDFSParquetFixture(t)
	fixture := &hdfsCSVFileClientFixture{data: data, info: HDFSFileInfo{Name: "events.parquet", Size: int64(len(data))}}
	config := hdfsCSVTestConfig(nil)
	config.Database = "/warehouse/events.parquet"
	db, err := NewHDFSParquetDriverWithFileClient(fixture).OpenWithConfig(context.Background(), config)
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

func TestParquetSQLDatabaseEnforcesDecodedRowLimit(t *testing.T) {
	data := createHDFSParquetFixture(t)
	db, err := object_storage.NewParquetSQLDatabase(object_storage.ParquetSQLDatabaseConfig{
		DriverName: "hdfs-parquet", ObjectKey: "/warehouse/events.parquet", TableName: "events",
		BatchSize: 1, MaxFileBytes: int64(len(data)), MaxRows: 1,
	}, func(context.Context, string) error { return nil }, func(context.Context, string) ([]byte, error) {
		return append([]byte(nil), data...), nil
	}, nil)
	if err != nil {
		t.Fatalf("NewParquetSQLDatabase() error = %v", err)
	}
	defer db.Close()
	if _, err := db.QueryContext(context.Background(), "SELECT * FROM events"); err == nil || !strings.Contains(err.Error(), "row limit") {
		t.Fatalf("QueryContext() error = %v, want row-limit rejection", err)
	}
}

func TestValidateHDFSParquetConfigRejectsUnsafeOrUnsupportedSettings(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*model.DataSourceConfig)
	}{
		{name: "CSV path", mutate: func(c *model.DataSourceConfig) { c.Database = "/warehouse/events.csv" }},
		{name: "relative path", mutate: func(c *model.DataSourceConfig) { c.Database = "warehouse/events.parquet" }},
		{name: "traversal path", mutate: func(c *model.DataSourceConfig) { c.Database = "/warehouse/../secret.parquet" }},
		{name: "oversized file setting", mutate: func(c *model.DataSourceConfig) {
			c.AdditionalProps["max_file_bytes"] = float64(maxHDFSCSVMaxFileBytes + 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := hdfsCSVTestConfig(nil)
			config.Database = "/warehouse/events.parquet"
			tc.mutate(config)
			if err := ValidateHDFSParquetConfig(config); err == nil {
				t.Fatal("ValidateHDFSParquetConfig() succeeded for invalid configuration")
			}
		})
	}
}

func TestHDFSParquetTableNameIsDerivedFromConfiguredFile(t *testing.T) {
	config := hdfsCSVTestConfig(nil)
	config.Database = "/warehouse/sales/events.parquet"
	name, err := HDFSParquetTableName(config)
	if err != nil {
		t.Fatalf("HDFSParquetTableName() error = %v", err)
	}
	if name != "events" {
		t.Fatalf("HDFSParquetTableName() = %q, want events", name)
	}
}
