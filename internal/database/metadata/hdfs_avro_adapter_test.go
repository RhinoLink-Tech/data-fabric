package metadata

import (
	"bytes"
	"context"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/file_system"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/linkedin/goavro/v2"
)

const hdfsAvroMetadataSchema = "{\"type\":\"record\",\"name\":\"Event\",\"fields\":[{\"name\":\"id\",\"type\":\"long\"},{\"name\":\"name\",\"type\":[\"null\",\"string\"],\"default\":null}]}"

type hdfsAvroMetadataClient struct{ data []byte }

func (f *hdfsAvroMetadataClient) ReadFileLimited(_ context.Context, _ string, maxBytes int64) ([]byte, error) {
	if int64(len(f.data)) > maxBytes {
		return nil, context.DeadlineExceeded
	}
	return append([]byte(nil), f.data...), nil
}
func (f *hdfsAvroMetadataClient) GetFileInfo(context.Context, string) (*file_system.HDFSFileInfo, error) {
	return &file_system.HDFSFileInfo{Name: "events.avro", Size: int64(len(f.data))}, nil
}
func (*hdfsAvroMetadataClient) ListFiles(context.Context, string) ([]file_system.HDFSFileInfo, error) {
	return nil, nil
}
func (*hdfsAvroMetadataClient) GetCapacity(context.Context) (*file_system.HDFSCapacity, error) {
	return &file_system.HDFSCapacity{}, nil
}
func (*hdfsAvroMetadataClient) Close() error { return nil }

func TestHDFSAvroAdapterSupportsUnifiedMetadataDiscovery(t *testing.T) {
	ctx := context.Background()
	codec, err := goavro.NewCodec(hdfsAvroMetadataSchema)
	if err != nil {
		t.Fatalf("create Avro codec: %v", err)
	}
	var output bytes.Buffer
	writer, err := goavro.NewOCFWriter(goavro.OCFConfig{W: &output, Codec: codec, CompressionName: "null"})
	if err != nil {
		t.Fatalf("create Avro OCF writer: %v", err)
	}
	if err := writer.Append([]interface{}{map[string]interface{}{"id": int64(1), "name": goavro.Union("string", "Ada")}}); err != nil {
		t.Fatalf("write Avro fixture: %v", err)
	}

	source := &model.DataSource{
		ID: "hdfs-events", Type: model.DatabaseTypeHDFSAvro,
		Config: model.DataSourceConfig{
			Host: "namenode.local", Port: 8020, Database: "/warehouse/events.avro", Username: "hdfs",
			AdditionalProps: map[string]interface{}{"table_name": "events"},
		},
	}
	registry := database.NewDriverRegistry()
	registry.RegisterDriver(model.DatabaseTypeHDFSAvro, file_system.NewHDFSAvroDriverWithFileClient(
		&hdfsAvroMetadataClient{data: append([]byte(nil), output.Bytes()...)},
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
	if len(table.Columns) != 2 || table.Columns[0].Name != "id" || table.Columns[1].Name != "name" {
		t.Fatalf("ExtractTableSchema() columns = %+v, want id and name", table.Columns)
	}
	schema, _, err := extractor.ExtractSchema(ctx, source.ID)
	if err != nil {
		t.Fatalf("ExtractSchema() error = %v", err)
	}
	if len(schema.Tables) != 1 || schema.Tables["events"] == nil {
		t.Fatalf("ExtractSchema() tables = %+v, want events", schema.Tables)
	}
}
