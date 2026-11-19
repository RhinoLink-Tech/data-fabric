package table_formats

import (
	"context"
	"strings"
	"testing"
)

type latestChangesMetadataReader struct {
	commits []HudiCommit
	calls   int
}

func (r *latestChangesMetadataReader) TestConnection(context.Context) error { return nil }
func (r *latestChangesMetadataReader) GetTableMetadata(context.Context, string) (*HudiTableMetadata, error) {
	return nil, nil
}
func (r *latestChangesMetadataReader) ListPartitions(context.Context, string) ([]HudiPartition, error) {
	return nil, nil
}
func (r *latestChangesMetadataReader) GetCommits(context.Context, string) ([]HudiCommit, error) {
	r.calls++
	return r.commits, nil
}
func (r *latestChangesMetadataReader) GetSavepoints(context.Context, string) ([]HudiSavepoint, error) {
	return nil, nil
}

func TestHudiGetLatestChangesValidatesCommitCount(t *testing.T) {
	reader := &latestChangesMetadataReader{
		commits: []HudiCommit{
			{CommitTime: "20240101153045000"},
			{CommitTime: "20240101153045123"},
			{CommitTime: "20240101153045234"},
		},
	}
	driver, err := NewHudiDriver(&HudiConfig{MetadataReader: reader})
	if err != nil {
		t.Fatal(err)
	}
	query := NewHudiIncrementalQuery(driver)

	for _, test := range []struct {
		name       string
		count      int
		wantErr    string
		wantReader int
	}{
		{name: "negative", count: -1, wantErr: "greater than zero", wantReader: 0},
		{name: "zero", count: 0, wantErr: "greater than zero", wantReader: 0},
		{name: "insufficient commits", count: 3, wantErr: "not enough commits", wantReader: 1},
		{name: "excessively large count", count: int(^uint(0) >> 1), wantErr: "not enough commits", wantReader: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader.calls = 0
			_, err := query.GetLatestChanges(context.Background(), "table", test.count)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("GetLatestChanges error = %v, want substring %q", err, test.wantErr)
			}
			if reader.calls != test.wantReader {
				t.Fatalf("metadata reader called %d times, want %d", reader.calls, test.wantReader)
			}
		})
	}
}

func TestHudiGetLatestChangesUsesLatestInstantsRegardlessOfReaderOrder(t *testing.T) {
	reader := &latestChangesMetadataReader{
		commits: []HudiCommit{
			{CommitTime: "20240101153045234"},
			{CommitTime: "20240101153045000"},
			{CommitTime: "20240101153045123"},
		},
	}
	executor := &fakeHudiIncrementalExecutor{result: &HudiQueryResult{}}
	driver, err := NewHudiDriver(&HudiConfig{
		MetadataReader:      reader,
		IncrementalExecutor: executor,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := NewHudiIncrementalQuery(driver).GetLatestChanges(context.Background(), "table", 1); err != nil {
		t.Fatalf("GetLatestChanges returned error: %v", err)
	}
	if executor.options.BeginInstantTime != "20240101153045123" {
		t.Errorf("begin instant = %q, want second-latest instant", executor.options.BeginInstantTime)
	}
	if executor.options.EndInstantTime != "20240101153045234" {
		t.Errorf("end instant = %q, want latest instant", executor.options.EndInstantTime)
	}
	if reader.commits[0].CommitTime != "20240101153045234" {
		t.Errorf("GetLatestChanges mutated metadata reader order")
	}
}
