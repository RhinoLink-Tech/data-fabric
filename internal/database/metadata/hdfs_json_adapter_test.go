package metadata

import (
	"context"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/file_system"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

type hdfsJSONMetadataFixture struct{ data []byte }

func (f *hdfsJSONMetadataFixture) ReadFileLimited(_ context.Context, _ string, maxBytes int64) ([]byte, error) {
	if int64(len(f.data)) > maxBytes {
		return nil, context.DeadlineExceeded
	}
	return append([]byte(nil), f.data...), nil
}
func (f *hdfsJSONMetadataFixture) GetFileInfo(context.Context, string) (*file_system.HDFSFileInfo, error) {
	return &file_system.HDFSFileInfo{Name: "events.jsonl", Size: int64(len(f.data))}, nil
}
func (*hdfsJSONMetadataFixture) ListFiles(context.Context, string) ([]file_system.HDFSFileInfo, error) {
	return nil, nil
}
func (*hdfsJSONMetadataFixture) GetCapacity(context.Context) (*file_system.HDFSCapacity, error) {
	return &file_system.HDFSCapacity{}, nil
}
func (*hdfsJSONMetadataFixture) Close() error { return nil }

func TestHDFSJSONAdapterSupportsUnifiedQueryMetadata(t *testing.T) {
	ctx := context.Background()
	source := &model.DataSource{
		ID: "hdfs-json-events", Type: model.DatabaseTypeHDFSJSON,
		Config: model.DataSourceConfig{
			Host: "namenode.local", Port: 8020, Database: "/warehouse/events.jsonl", Username: "hdfs",
			AdditionalProps: map[string]interface{}{},
		},
	}
	registry := database.NewDriverRegistry()
	registry.RegisterDriver(model.DatabaseTypeHDFSJSON, file_system.NewHDFSJSONDriverWithFileClient(
		&hdfsJSONMetadataFixture{data: []byte("{\"id\":1,\"name\":\"Ada\"}\n{\"id\":2,\"name\":\"Lin\"}\n")},
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
		t.Fatalf("ListTables() = %+v, want inferred events table", tables)
	}
	table, err := extractor.ExtractTableSchema(ctx, source.ID, "events")
	if err != nil {
		t.Fatalf("ExtractTableSchema() error = %v", err)
	}
	if len(table.Columns) != 2 || table.Columns[0].Name != "id" || table.Columns[1].Name != "name" {
		t.Fatalf("ExtractTableSchema() columns = %+v, want id and name", table.Columns)
	}
}
