package metadata

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/file_system"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/writer"
)

type hdfsParquetMetadataRow struct {
	Name     string `parquet:"name=name, type=BYTE_ARRAY, convertedtype=UTF8"`
	Quantity int32  `parquet:"name=quantity, type=INT32"`
}

type hdfsParquetMetadataClient struct{ data []byte }

func (f *hdfsParquetMetadataClient) ReadFileLimited(_ context.Context, _ string, maxBytes int64) ([]byte, error) {
	if int64(len(f.data)) > maxBytes {
		return nil, context.DeadlineExceeded
	}
	return append([]byte(nil), f.data...), nil
}
func (f *hdfsParquetMetadataClient) GetFileInfo(context.Context, string) (*file_system.HDFSFileInfo, error) {
	return &file_system.HDFSFileInfo{Name: "events.parquet", Size: int64(len(f.data))}, nil
}
func (*hdfsParquetMetadataClient) ListFiles(context.Context, string) ([]file_system.HDFSFileInfo, error) {
	return nil, nil
}
func (*hdfsParquetMetadataClient) GetCapacity(context.Context) (*file_system.HDFSCapacity, error) {
	return &file_system.HDFSCapacity{}, nil
}
func (*hdfsParquetMetadataClient) Close() error { return nil }

func TestHDFSParquetAdapterSupportsUnifiedMetadataDiscovery(t *testing.T) {
	ctx := context.Background()
	var output bytes.Buffer
	parquetWriter, err := writer.NewParquetWriterFromWriter(&output, new(hdfsParquetMetadataRow), 1)
	if err != nil {
		t.Fatalf("create Parquet writer: %v", err)
	}
	parquetWriter.CompressionType = parquet.CompressionCodec_UNCOMPRESSED
	if err := parquetWriter.Write(hdfsParquetMetadataRow{Name: "Ada", Quantity: 3}); err != nil {
		t.Fatalf("write Parquet fixture: %v", err)
	}
	if err := parquetWriter.WriteStop(); err != nil {
		t.Fatalf("finish Parquet fixture: %v", err)
	}

	source := &model.DataSource{
		ID: "hdfs-events", Type: model.DatabaseTypeHDFSParquet,
		Config: model.DataSourceConfig{
			Host: "namenode.local", Port: 8020, Database: "/warehouse/events.parquet", Username: "hdfs",
			AdditionalProps: map[string]interface{}{"table_name": "events"},
		},
	}
	registry := database.NewDriverRegistry()
	registry.RegisterDriver(model.DatabaseTypeHDFSParquet, file_system.NewHDFSParquetDriverWithFileClient(
		&hdfsParquetMetadataClient{data: append([]byte(nil), output.Bytes()...)},
	))
	pool := database.NewConnectionPool(registry, nil, nil, false)
	defer func() { _ = pool.CloseAll() }()
	extractor := &MetadataExtractor{
		connPool: pool,
		sources:  &dataSourceLookupStub{dataSource: source},
		registry: registry,
	}

	tables, err := extractor.ListTables(ctx, source.ID)
	if err != nil {
		t.Fatalf("ListTables() error = %v", err)
	}
	if len(tables) != 1 || tables[0].Name != "events" {
		t.Fatalf("ListTables() = %+v, want events", tables)
	}
	table, err := extractor.ExtractTableSchema(ctx, source.ID, "events")
	if err != nil {
		t.Fatalf("ExtractTableSchema() error = %v", err)
	}
	if len(table.Columns) != 2 || table.Columns[0].Name != "name" || table.Columns[1].Name != "quantity" {
		t.Fatalf("ExtractTableSchema() columns = %+v, want name and quantity", table.Columns)
	}
	schema, _, err := extractor.ExtractSchema(ctx, source.ID)
	if err != nil {
		t.Fatalf("ExtractSchema() error = %v", err)
	}
	if len(schema.Tables) != 1 || schema.Tables["events"] == nil {
		t.Fatalf("ExtractSchema() tables = %+v, want events", schema.Tables)
	}
}

func TestHDFSCompressedParquetAdapterSupportsUnifiedMetadataDiscovery(t *testing.T) {
	ctx := context.Background()
	var output bytes.Buffer
	parquetWriter, err := writer.NewParquetWriterFromWriter(&output, new(hdfsParquetMetadataRow), 1)
	if err != nil {
		t.Fatalf("create compressed Parquet writer: %v", err)
	}
	parquetWriter.CompressionType = parquet.CompressionCodec_SNAPPY
	if err := parquetWriter.Write(hdfsParquetMetadataRow{Name: "Ada", Quantity: 3}); err != nil {
		t.Fatalf("write compressed Parquet fixture: %v", err)
	}
	if err := parquetWriter.WriteStop(); err != nil {
		t.Fatalf("finish compressed Parquet fixture: %v", err)
	}

	source := &model.DataSource{
		ID: "hdfs-compressed-events", Type: model.DatabaseTypeHDFSParquetCompressed,
		Config: model.DataSourceConfig{
			Host: "namenode.local", Port: 8020, Database: "/warehouse/events.parquet", Username: "hdfs",
			AdditionalProps: map[string]interface{}{"table_name": "events"},
		},
	}
	registry := database.NewDriverRegistry()
	registry.RegisterDriver(model.DatabaseTypeHDFSParquetCompressed, file_system.NewHDFSParquetCompressionDriverWithFileClient(
		&hdfsParquetMetadataClient{data: append([]byte(nil), output.Bytes()...)},
	))
	pool := database.NewConnectionPool(registry, nil, nil, false)
	defer func() { _ = pool.CloseAll() }()
	extractor := &MetadataExtractor{
		connPool: pool,
		sources:  &dataSourceLookupStub{dataSource: source},
		registry: registry,
	}

	tables, err := extractor.ListTables(ctx, source.ID)
	if err != nil {
		t.Fatalf("ListTables() error = %v", err)
	}
	if len(tables) != 1 || tables[0].Name != "events" {
		t.Fatalf("ListTables() = %+v, want events", tables)
	}
	table, err := extractor.ExtractTableSchema(ctx, source.ID, "events")
	if err != nil {
		t.Fatalf("ExtractTableSchema() error = %v", err)
	}
	if len(table.Columns) != 2 || table.Columns[0].Name != "name" || table.Columns[1].Name != "quantity" {
		t.Fatalf("ExtractTableSchema() columns = %+v, want name and quantity", table.Columns)
	}
}

func TestOzoneParquetAdapterSupportsUnifiedMetadataDiscovery(t *testing.T) {
	ctx := context.Background()
	var output bytes.Buffer
	parquetWriter, err := writer.NewParquetWriterFromWriter(&output, new(hdfsParquetMetadataRow), 1)
	if err != nil {
		t.Fatalf("create Parquet writer: %v", err)
	}
	parquetWriter.CompressionType = parquet.CompressionCodec_SNAPPY
	if err := parquetWriter.Write(hdfsParquetMetadataRow{Name: "Ada", Quantity: 3}); err != nil {
		t.Fatalf("write Parquet fixture: %v", err)
	}
	if err := parquetWriter.WriteStop(); err != nil {
		t.Fatalf("finish Parquet fixture: %v", err)
	}
	data := append([]byte(nil), output.Bytes()...)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/analytics/warehouse/events.parquet" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer server.Close()

	source := &model.DataSource{
		ID: "ozone-events", Type: model.DatabaseTypeOzoneParquet,
		Config: model.DataSourceConfig{
			Host: "warehouse/events.parquet", Database: "analytics", Username: "access-key", Password: "secret-key",
			AdditionalProps: map[string]interface{}{
				"endpoint_url": server.URL, "object_key": "warehouse/events.parquet", "table_name": "events",
			},
		},
	}
	registry := database.NewDriverRegistry()
	registry.RegisterDriver(model.DatabaseTypeOzoneParquet, &file_system.OzoneParquetDriver{})
	pool := database.NewConnectionPool(registry, nil, nil, false)
	defer func() { _ = pool.CloseAll() }()
	extractor := &MetadataExtractor{
		connPool: pool,
		sources:  &dataSourceLookupStub{dataSource: source},
		registry: registry,
	}

	tables, err := extractor.ListTables(ctx, source.ID)
	if err != nil {
		t.Fatalf("ListTables() error = %v", err)
	}
	if len(tables) != 1 || tables[0].Name != "events" {
		t.Fatalf("ListTables() = %+v, want events", tables)
	}
	table, err := extractor.ExtractTableSchema(ctx, source.ID, "events")
	if err != nil {
		t.Fatalf("ExtractTableSchema() error = %v", err)
	}
	if len(table.Columns) != 2 || table.Columns[0].Name != "name" || table.Columns[1].Name != "quantity" {
		t.Fatalf("ExtractTableSchema() columns = %+v, want name and quantity", table.Columns)
	}
	schema, _, err := extractor.ExtractSchema(ctx, source.ID)
	if err != nil {
		t.Fatalf("ExtractSchema() error = %v", err)
	}
	if len(schema.Tables) != 1 || schema.Tables["events"] == nil {
		t.Fatalf("ExtractSchema() tables = %+v, want events", schema.Tables)
	}
}
