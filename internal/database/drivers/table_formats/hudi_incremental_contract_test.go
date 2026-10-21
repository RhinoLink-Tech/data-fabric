package table_formats

import (
	"context"
	"strings"
	"testing"
)

type fakeHudiIncrementalExecutor struct {
	options HudiIncrementalReadOptions
	result  *HudiQueryResult
	err     error
	called  bool
}

func (f *fakeHudiIncrementalExecutor) ReadIncremental(_ context.Context, options HudiIncrementalReadOptions) (*HudiQueryResult, error) {
	f.options = options
	f.called = true
	return f.result, f.err
}

func TestParseHudiProperties(t *testing.T) {
	properties := strings.Join([]string{
		"# standard hoodie.properties fields",
		"hoodie.table.name=customers",
		"hoodie.table.type=COPY_ON_WRITE",
		"hoodie.table.version=6",
		"hoodie.table.cdc.enabled=true",
		"hoodie.table.recordkey.fields=id, tenant_id",
		"hoodie.table.partition.fields=region, date",
		"hoodie.table.create.schema={\x22type\x22:\x22record\x22,\x22name\x22:\x22customers\x22,\x22fields\x22:[{\x22name\x22:\x22id\x22,\x22type\x22:\x22long\x22},{\x22name\x22:\x22name\x22,\x22type\x22:[\x22null\x22,\x22string\x22],\x22default\x22:null},{\x22name\x22:\x22tags\x22,\x22type\x22:{\x22type\x22:\x22array\x22,\x22items\x22:\x22string\x22}}]}",
	}, "\n")

	metadata, err := NewHudiMetadataParser().ParseHudiProperties([]byte(properties))
	if err != nil {
		t.Fatalf("ParseHudiProperties returned error: %v", err)
	}
	if metadata.TableName != "customers" || metadata.TableType != "COPY_ON_WRITE" || metadata.HudiVersion != "6" {
		t.Fatalf("unexpected table metadata: %#v", metadata)
	}
	if metadata.KeyField != "id,tenant_id" {
		t.Fatalf("KeyField = %q", metadata.KeyField)
	}
	if len(metadata.PartitionFields) != 2 || metadata.PartitionFields[0] != "region" {
		t.Fatalf("unexpected partition fields: %#v", metadata.PartitionFields)
	}
	if len(metadata.RecordFields) != 3 {
		t.Fatalf("got %d record fields, want 3", len(metadata.RecordFields))
	}
	if metadata.RecordFields[1].Type != "VARCHAR" || !metadata.RecordFields[1].Nullable {
		t.Fatalf("nullable union type was not parsed: %#v", metadata.RecordFields[1])
	}
	if metadata.RecordFields[2].Type != "ARRAY" {
		t.Fatalf("nested Avro array type = %q", metadata.RecordFields[2].Type)
	}
}

func TestBuildHudiIncrementalReadOptions(t *testing.T) {
	request := &IncrementalQueryRequest{
		BasePath:          "s3://bucket/warehouse/customers",
		BeginInstantTime:  "20240101153045000",
		EndInstantTime:    "20240101153045123",
		QueryType:         "incremental",
		IncrementalFormat: "cdc",
		SelectedFields:    []string{"id", "payload.name"},
		PageSize:          40,
		PageToken:         "opaque-engine-cursor",
	}
	options, err := BuildHudiIncrementalReadOptions(nil, request)
	if err != nil {
		t.Fatalf("BuildHudiIncrementalReadOptions returned error: %v", err)
	}
	if options.IncrementalFormat != "cdc" || options.BeginInstantTime != request.BeginInstantTime || options.EndInstantTime != request.EndInstantTime {
		t.Fatalf("range or format changed: %#v", options)
	}
	if options.PageSize != 40 || options.PageToken != request.PageToken || len(options.SelectedFields) != 2 {
		t.Fatalf("pagination/projection changed: %#v", options)
	}
}

func TestHudiCDCRequiresEnabledCopyOnWriteTable(t *testing.T) {
	request := &IncrementalQueryRequest{
		BasePath:          "table",
		BeginInstantTime:  "20240101153045000",
		EndInstantTime:    "20240101153045123",
		IncrementalFormat: "cdc",
		TableType:         "MERGE_ON_READ",
	}
	if _, err := BuildHudiIncrementalReadOptions(nil, request); err == nil {
		t.Fatal("CDC for Merge-on-Read should be rejected")
	}
	request.TableType = "COPY_ON_WRITE"
	if _, err := BuildHudiIncrementalReadOptions(nil, request); err != nil {
		t.Fatalf("CDC for Copy-on-Write should be accepted: %v", err)
	}
}

type staticHudiMetadataReader struct {
	metadata *HudiTableMetadata
}

func (r staticHudiMetadataReader) TestConnection(context.Context) error { return nil }
func (r staticHudiMetadataReader) GetTableMetadata(context.Context, string) (*HudiTableMetadata, error) {
	return r.metadata, nil
}
func (r staticHudiMetadataReader) ListPartitions(context.Context, string) ([]HudiPartition, error) {
	return nil, nil
}
func (r staticHudiMetadataReader) GetCommits(context.Context, string) ([]HudiCommit, error) {
	return nil, nil
}
func (r staticHudiMetadataReader) GetSavepoints(context.Context, string) ([]HudiSavepoint, error) {
	return nil, nil
}

func TestHudiIncrementalCDCRequiresEnabledCopyOnWriteMetadata(t *testing.T) {
	for _, test := range []struct {
		name     string
		metadata *HudiTableMetadata
		wantErr  bool
	}{
		{name: "CDC disabled", metadata: &HudiTableMetadata{TableType: "COPY_ON_WRITE"}, wantErr: true},
		{name: "MOR table", metadata: &HudiTableMetadata{TableType: "MERGE_ON_READ", CDCEnabled: true}, wantErr: true},
		{name: "enabled COW", metadata: &HudiTableMetadata{TableType: "COPY_ON_WRITE", CDCEnabled: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := &fakeHudiIncrementalExecutor{result: &HudiQueryResult{}}
			driver, err := NewHudiDriver(&HudiConfig{
				MetadataReader:      staticHudiMetadataReader{metadata: test.metadata},
				IncrementalExecutor: executor,
			})
			if err != nil {
				t.Fatal(err)
			}
			request := &IncrementalQueryRequest{
				BasePath: "table", BeginInstantTime: "20240101153045000",
				EndInstantTime: "20240101153045123", IncrementalFormat: "cdc",
			}
			_, err = NewHudiIncrementalQuery(driver).QueryIncremental(context.Background(), request)
			if (err != nil) != test.wantErr {
				t.Fatalf("QueryIncremental error = %v, wantErr %v", err, test.wantErr)
			}
			if executor.called == test.wantErr {
				t.Fatalf("engine called=%v, want call=%v", executor.called, !test.wantErr)
			}
		})
	}
}

func TestBuildHudiIncrementalReadOptionsRejectsInvalidRequests(t *testing.T) {
	valid := IncrementalQueryRequest{
		BasePath:         "table",
		BeginInstantTime: "20240101153045000",
		EndInstantTime:   "20240101153045123",
	}
	tests := []struct {
		name string
		edit func(*IncrementalQueryRequest)
	}{
		{name: "end instant required", edit: func(r *IncrementalQueryRequest) { r.EndInstantTime = "" }},
		{name: "reversed range", edit: func(r *IncrementalQueryRequest) { r.EndInstantTime = r.BeginInstantTime }},
		{name: "unsafe field", edit: func(r *IncrementalQueryRequest) { r.SelectedFields = []string{"id; DROP TABLE t"} }},
		{name: "unsupported format", edit: func(r *IncrementalQueryRequest) { r.IncrementalFormat = "unknown" }},
		{name: "oversized page", edit: func(r *IncrementalQueryRequest) { r.PageSize = 10001 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			test.edit(&request)
			if _, err := BuildHudiIncrementalReadOptions(nil, &request); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestHudiIncrementalQueryUsesEngineContractAndReturnsCursor(t *testing.T) {
	executor := &fakeHudiIncrementalExecutor{
		result: &HudiQueryResult{
			Schema:        HudiResultSchema{Fields: []HudiResultField{{Name: "id", Type: "long"}}},
			Rows:          [][]interface{}{{"42"}},
			NextPageToken: "next-page",
		},
	}
	driver, err := NewHudiDriver(&HudiConfig{IncrementalExecutor: executor})
	if err != nil {
		t.Fatalf("NewHudiDriver returned error: %v", err)
	}
	request := &IncrementalQueryRequest{
		BasePath:         "table",
		BeginInstantTime: "20240101153045000",
		EndInstantTime:   "20240101153045123",
		PageToken:        "current-page",
		PageSize:         3,
	}
	result, err := NewHudiIncrementalQuery(driver).QueryIncremental(context.Background(), request)
	if err != nil {
		t.Fatalf("QueryIncremental returned error: %v", err)
	}
	if executor.options.EndInstantTime != request.EndInstantTime || executor.options.PageToken != request.PageToken || executor.options.PageSize != 3 {
		t.Fatalf("engine did not receive pinned request state: %#v", executor.options)
	}
	if result.NextPageToken != "next-page" || result.EndTime != request.EndInstantTime || len(result.Data) != 1 {
		t.Fatalf("incremental result lost cursor/state: %#v", result)
	}
	capabilities := driver.GetCapabilities()
	if !capabilities.SupportsIncrementalRead || capabilities.SupportsSQL || capabilities.SupportsTransaction || capabilities.SupportsStreaming {
		t.Fatalf("Hudi capabilities overclaim unsupported operations: %#v", capabilities)
	}
}

func TestHudiIncrementalQueryRejectsInvalidEnginePagination(t *testing.T) {
	for _, test := range []struct {
		name   string
		rows   [][]interface{}
		input  string
		output string
	}{
		{name: "oversized page", rows: [][]interface{}{{1}, {2}}},
		{name: "repeated token", rows: [][]interface{}{{1}}, input: "cursor", output: "cursor"},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := &fakeHudiIncrementalExecutor{
				result: &HudiQueryResult{Rows: test.rows, NextPageToken: test.output},
			}
			driver, err := NewHudiDriver(&HudiConfig{IncrementalExecutor: executor})
			if err != nil {
				t.Fatal(err)
			}
			request := &IncrementalQueryRequest{
				BasePath: "table", BeginInstantTime: "20240101153045000",
				EndInstantTime: "20240101153045123", PageSize: 1, PageToken: test.input,
			}
			if _, err := NewHudiIncrementalQuery(driver).QueryIncremental(context.Background(), request); err == nil {
				t.Fatal("expected invalid page to be rejected")
			}
		})
	}
}

func TestHudiIncrementalQueryDoesNotInventAQueryProtocol(t *testing.T) {
	driver, err := NewHudiDriver(&HudiConfig{})
	if err != nil {
		t.Fatal(err)
	}
	request := &IncrementalQueryRequest{
		BasePath:         "table",
		BeginInstantTime: "20240101153045000",
		EndInstantTime:   "20240101153045123",
	}
	if _, err := NewHudiIncrementalQuery(driver).QueryIncremental(context.Background(), request); err == nil {
		t.Fatal("expected explicit missing query-engine error")
	}
}
