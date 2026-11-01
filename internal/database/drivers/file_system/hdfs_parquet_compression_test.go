package file_system

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/writer"
)

func createHDFSCompressedParquetFixture(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	parquetWriter, err := writer.NewParquetWriterFromWriter(&output, new(hdfsParquetFixtureRow), 1)
	if err != nil {
		t.Fatalf("create compressed Parquet writer: %v", err)
	}
	parquetWriter.CompressionType = parquet.CompressionCodec_SNAPPY
	for _, row := range []hdfsParquetFixtureRow{
		{Label: "one", Quantity: 1, Enabled: false},
		{Label: "two", Quantity: 2, Enabled: true},
		{Label: "three", Quantity: 3, Enabled: true},
	} {
		if err := parquetWriter.Write(row); err != nil {
			t.Fatalf("write compressed Parquet fixture: %v", err)
		}
	}
	if err := parquetWriter.WriteStop(); err != nil {
		t.Fatalf("finish compressed Parquet fixture: %v", err)
	}
	return append([]byte(nil), output.Bytes()...)
}

func TestHDFSParquetCompressionOpenWithConfigRunsReadOnlySQL(t *testing.T) {
	data := createHDFSCompressedParquetFixture(t)
	fixture := &hdfsCSVFileClientFixture{data: data, info: HDFSFileInfo{Name: "events.parquet", Size: int64(len(data))}}
	config := hdfsCSVTestConfig(nil)
	config.Database = "/warehouse/events.parquet"
	driver := NewHDFSParquetCompressionDriverWithFileClient(fixture)
	if driver.GetDatabaseTypeName() != "hdfs-parquet-compressed" || !driver.GetCapabilities().SupportsSQL {
		t.Fatalf("compressed Parquet driver identity/capabilities = %q/%+v", driver.GetDatabaseTypeName(), driver.GetCapabilities())
	}
	db, err := driver.OpenWithConfig(context.Background(), config)
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
	if err := db.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestValidateHDFSParquetCompressedConfigReusesParquetFileContract(t *testing.T) {
	config := hdfsCSVTestConfig(nil)
	config.Database = "/warehouse/events.parquet"
	if err := ValidateHDFSParquetConfig(config); err != nil {
		t.Fatalf("ValidateHDFSParquetConfig() error = %v", err)
	}
	config.Database = "/warehouse/events.parquet.gz"
	if err := ValidateHDFSParquetConfig(config); err == nil {
		t.Fatal("externally gzip-wrapped Parquet file was accepted")
	}
	if err := NewHDFSParquetCompressionDriverWithFileClient(&hdfsCSVFileClientFixture{}).ValidateDSN("hdfs://namenode.local/warehouse/events.parquet.gz"); err == nil {
		t.Fatal("externally gzip-wrapped Parquet DSN was accepted")
	}
}
