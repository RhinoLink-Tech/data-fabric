package metadata

import (
	"context"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/file_system"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

type hdfsTextMetadataClient struct{ data []byte }

func (f *hdfsTextMetadataClient) ReadFileLimited(_ context.Context, _ string, maxBytes int64) ([]byte, error) {
	if int64(len(f.data)) > maxBytes {
		return nil, context.DeadlineExceeded
	}
	return append([]byte(nil), f.data...), nil
}
func (f *hdfsTextMetadataClient) GetFileInfo(context.Context, string) (*file_system.HDFSFileInfo, error) {
	return &file_system.HDFSFileInfo{Name: "application.log", Size: int64(len(f.data))}, nil
}
func (*hdfsTextMetadataClient) ListFiles(context.Context, string) ([]file_system.HDFSFileInfo, error) {
	return nil, nil
}
func (*hdfsTextMetadataClient) GetCapacity(context.Context) (*file_system.HDFSCapacity, error) {
	return &file_system.HDFSCapacity{}, nil
}
func (*hdfsTextMetadataClient) Close() error { return nil }

func TestHDFSTextAdapterSupportsUnifiedMetadataDiscovery(t *testing.T) {
	ctx := context.Background()
	source := &model.DataSource{
		ID: "hdfs-application-log", Type: model.DatabaseTypeHDFSText,
		Config: model.DataSourceConfig{
			Host: "namenode.local", Port: 8020, Database: "/warehouse/application.log", Username: "hdfs",
			AdditionalProps: map[string]interface{}{"table_name": "application_log"},
		},
	}
	registry := database.NewDriverRegistry()
	registry.RegisterDriver(model.DatabaseTypeHDFSText, file_system.NewHDFSTextDriverWithFileClient(&hdfsTextMetadataClient{
		data: []byte("ready\ncomplete"),
	}))
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
	if len(tables) != 1 || tables[0].Name != "application_log" {
		t.Fatalf("ListTables() = %+v, want application_log", tables)
	}
	table, err := extractor.ExtractTableSchema(ctx, source.ID, "application_log")
	if err != nil {
		t.Fatalf("ExtractTableSchema() error = %v", err)
	}
	if len(table.Columns) != 2 || table.Columns[0].Name != "line_number" || table.Columns[1].Name != "line" {
		t.Fatalf("ExtractTableSchema() columns = %+v, want line_number and line", table.Columns)
	}
	schema, _, err := extractor.ExtractSchema(ctx, source.ID)
	if err != nil {
		t.Fatalf("ExtractSchema() error = %v", err)
	}
	if len(schema.Tables) != 1 || schema.Tables["application_log"] == nil {
		t.Fatalf("ExtractSchema() tables = %+v, want application_log", schema.Tables)
	}
}
