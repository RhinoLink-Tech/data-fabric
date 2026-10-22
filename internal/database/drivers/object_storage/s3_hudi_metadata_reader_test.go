package object_storage

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/table_formats"
)

type fakeS3HudiMetadataClient struct {
	objects []S3Object
	files   map[string][]byte
}

func (f *fakeS3HudiMetadataClient) ListObjects(_ context.Context, prefix string, maxKeys int32) ([]S3Object, error) {
	objects := make([]S3Object, 0)
	for _, object := range f.objects {
		if strings.HasPrefix(object.Key, prefix) {
			objects = append(objects, object)
			if maxKeys > 0 && int32(len(objects)) >= maxKeys {
				break
			}
		}
	}
	return objects, nil
}

func (f *fakeS3HudiMetadataClient) ListObjectsAll(_ context.Context, prefix string) ([]S3Object, error) {
	return f.ListObjects(context.Background(), prefix, 0)
}

func (f *fakeS3HudiMetadataClient) GetObject(_ context.Context, key string) ([]byte, error) {
	data, ok := f.files[key]
	if !ok {
		return nil, fmt.Errorf("not found: %s", key)
	}
	return data, nil
}

func newFakeS3HudiReader() *S3HudiMetadataReader {
	const properties = `hoodie.table.name=orders
hoodie.table.type=COPY_ON_WRITE
hoodie.table.version=6
hoodie.table.recordkey.fields=id
hoodie.table.partition.fields=dt,region
hoodie.table.timeline.path=timeline
hoodie.table.create.schema={"type":"record","name":"orders","fields":[{"name":"id","type":"long"},{"name":"note","type":["null","string"]}]}
`
	return NewS3HudiMetadataReaderWithClient(&fakeS3HudiMetadataClient{
		files: map[string][]byte{"warehouse/orders/.hoodie/hoodie.properties": []byte(properties)},
		objects: []S3Object{
			{Key: "warehouse/orders/.hoodie/timeline/20260101000000.commit"},
			{Key: "warehouse/orders/.hoodie/timeline/20260102000000.deltacommit"},
			{Key: "warehouse/orders/.hoodie/timeline/20260103000000.savepoint"},
			{Key: "warehouse/orders/.hoodie/timeline/20260104000000.commit.inflight"},
			{Key: "warehouse/orders/dt=2026-01-01/region=cn/part-1.parquet", Size: 100},
			{Key: "warehouse/orders/dt=2026-01-01/region=cn/part-2.parquet", Size: 200},
			{Key: "warehouse/orders/dt=2026-01-02/region=us/part-3.log.1_0-0-0", Size: 50},
			{Key: "warehouse/orders/_SUCCESS", Size: 0},
		},
	}, "test-bucket")
}

func TestS3HudiMetadataReaderReadsPropertiesAndActiveTimeline(t *testing.T) {
	reader := newFakeS3HudiReader()
	metadata, err := reader.GetTableMetadata(context.Background(), "s3://test-bucket/warehouse/orders")
	if err != nil {
		t.Fatalf("GetTableMetadata returned error: %v", err)
	}
	if metadata.TableName != "orders" || metadata.TableType != "COPY_ON_WRITE" || metadata.KeyField != "id" {
		t.Fatalf("unexpected metadata: %#v", metadata)
	}
	if metadata.Properties["hoodie.table.timeline.path"] != "timeline" {
		t.Fatalf("timeline path was not preserved: %#v", metadata.Properties)
	}
	if metadata.RecordFields[0].Type != "BIGINT" || metadata.RecordFields[1].Nullable != true {
		t.Fatalf("Avro schema was not parsed: %#v", metadata.RecordFields)
	}

	commits, err := reader.GetCommits(context.Background(), "warehouse/orders")
	if err != nil {
		t.Fatalf("GetCommits returned error: %v", err)
	}
	if len(commits) != 2 || commits[0].ActionType != "commit" || commits[1].ActionType != "deltacommit" {
		t.Fatalf("unexpected commits (pending action should be excluded): %#v", commits)
	}
	savepoints, err := reader.GetSavepoints(context.Background(), "warehouse/orders")
	if err != nil {
		t.Fatalf("GetSavepoints returned error: %v", err)
	}
	if len(savepoints) != 1 || savepoints[0].SavepointTime != "20260103000000" {
		t.Fatalf("unexpected savepoints: %#v", savepoints)
	}
}

func TestS3HudiMetadataReaderListsDataPartitions(t *testing.T) {
	partitions, err := newFakeS3HudiReader().ListPartitions(context.Background(), "warehouse/orders")
	if err != nil {
		t.Fatalf("ListPartitions returned error: %v", err)
	}
	if len(partitions) != 2 {
		t.Fatalf("got %d partitions, want 2: %#v", len(partitions), partitions)
	}
	if partitions[0].Path != "dt=2026-01-01/region=cn" || partitions[0].FileCount != 2 || partitions[0].SizeBytes != 300 {
		t.Fatalf("unexpected first partition: %#v", partitions[0])
	}
	if partitions[1].Values["dt"] != "2026-01-02" || partitions[1].Values["region"] != "us" || partitions[1].FileCount != 1 {
		t.Fatalf("unexpected second partition: %#v", partitions[1])
	}
}

func TestS3HudiMetadataReaderRejectsUnsafePathsAndNewTimelineLayout(t *testing.T) {
	reader := newFakeS3HudiReader()
	for _, value := range []string{"../other", "s3://different-bucket/warehouse/orders"} {
		if _, err := reader.GetTableMetadata(context.Background(), value); err == nil {
			t.Errorf("GetTableMetadata(%q) unexpectedly succeeded", value)
		}
	}

	client := &fakeS3HudiMetadataClient{
		files: map[string][]byte{"warehouse/new/.hoodie/hoodie.properties": []byte(
			"hoodie.table.name=new\nhoodie.table.type=MERGE_ON_READ\nhoodie.table.version=8\n",
		)},
	}
	if _, err := NewS3HudiMetadataReaderWithClient(client, "test-bucket").GetTableMetadata(context.Background(), "warehouse/new"); err == nil {
		t.Fatal("format version 8 should be rejected by the classic timeline reader")
	}
}

var _ table_formats.HudiMetadataReader = (*S3HudiMetadataReader)(nil)
