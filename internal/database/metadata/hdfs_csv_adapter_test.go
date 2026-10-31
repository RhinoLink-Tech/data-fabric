package metadata

import (
	"context"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/file_system"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

type hdfsCSVMetadataFixture struct{ data []byte }

func (f *hdfsCSVMetadataFixture) ReadFileLimited(_ context.Context, _ string, maxBytes int64) ([]byte, error) {
	if int64(len(f.data)) > maxBytes {
		return nil, context.DeadlineExceeded
	}
	return append([]byte(nil), f.data...), nil
}
func (*hdfsCSVMetadataFixture) GetFileInfo(context.Context, string) (*file_system.HDFSFileInfo, error) {
	return &file_system.HDFSFileInfo{Name: "events.csv", Size: 32}, nil
}
func (*hdfsCSVMetadataFixture) ListFiles(context.Context, string) ([]file_system.HDFSFileInfo, error) {
	return nil, nil
}
func (*hdfsCSVMetadataFixture) GetCapacity(context.Context) (*file_system.HDFSCapacity, error) {
	return &file_system.HDFSCapacity{}, nil
}
func (*hdfsCSVMetadataFixture) Close() error { return nil }

func TestHDFSCSVAdapterSupportsUnifiedQueryMetadata(t *testing.T) {
	ctx := context.Background()
	source := &model.DataSource{
		ID: "hdfs-events", Type: model.DatabaseTypeHDFSCSV,
		Config: model.DataSourceConfig{
			Host: "namenode.local", Port: 8020, Database: "/warehouse/events.csv", Username: "hdfs",
			AdditionalProps: map[string]interface{}{"table_name": "events", "has_header": true},
		},
	}
	registry := database.NewDriverRegistry()
	registry.RegisterDriver(model.DatabaseTypeHDFSCSV, file_system.NewHDFSCSVDriverWithFileClient(
		&hdfsCSVMetadataFixture{data: []byte("id,name\n1,Ada\n2,Lin\n")},
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
	if len(tables) != 1 || tables[0].Name != "events" || tables[0].Type != "TABLE" {
		t.Fatalf("ListTables() = %+v, want the configured events table", tables)
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
