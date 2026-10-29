package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestFetchQueryRoutesOSSDeltaThroughGateway(t *testing.T) {
	data := parquetObjectQueryTestData(t)
	const (
		tablePath = "delta/events"
		logKey    = tablePath + "/_delta_log/00000000000000000000.json"
		dataKey   = tablePath + "/part-00000.parquet"
	)
	logBody := fmt.Sprintf(
		"{\"protocol\":{\"minReaderVersion\":1,\"minWriterVersion\":2}}\n"+
			"{\"metaData\":{\"id\":\"service-fixture\",\"format\":{\"provider\":\"parquet\",\"options\":{}},\"schemaString\":\"{}\",\"partitionColumns\":[],\"configuration\":{}}}\n"+
			"{\"add\":{\"path\":\"part-00000.parquet\",\"size\":%d,\"partitionValues\":{},\"modificationTime\":1,\"dataChange\":true}}\n",
		len(data),
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if prefix := r.URL.Query().Get("prefix"); prefix != "" {
			if prefix != tablePath+"/_delta_log/" {
				http.Error(w, "unexpected list prefix", http.StatusBadRequest)
				return
			}
			xml := fmt.Sprintf("<ListBucketResult><Name>demo-bucket</Name><Prefix>%s</Prefix><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated><Contents><Key>%s</Key><Size>%d</Size></Contents></ListBucketResult>", prefix, logKey, len(logBody))
			w.Header().Set("Content-Type", "application/xml")
			w.Header().Set("Content-Length", strconv.Itoa(len(xml)))
			_, _ = w.Write([]byte(xml))
			return
		}
		switch strings.TrimPrefix(r.URL.Path, "/demo-bucket/") {
		case logKey:
			w.Header().Set("Content-Length", strconv.Itoa(len(logBody)))
			_, _ = w.Write([]byte(logBody))
		case dataKey:
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	source := &model.DataSource{
		ID: "oss-delta-query-service-test", Type: model.DatabaseTypeOSSDelta,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: tablePath, Database: "demo-bucket", Username: "test-access-key", Password: "test-secret",
			AdditionalProps: map[string]interface{}{
				"table_path": tablePath, "table_name": "events", "endpoint_url": server.URL,
				"force_path_style": true,
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer func() { _ = pool.CloseAll() }()
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate OSS Delta config: %v", err)
	}
	queryService := NewQueryService(federationSourceRepository{source: source}, pool, false, newTestSQLTranslationManager())
	response, err := queryService.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: source.ID,
		SQL:          "SELECT id, name FROM events WHERE active = true ORDER BY id DESC",
		Type:         2,
		BatchSize:    1,
	})
	if err != nil {
		t.Fatalf("FetchQuery() error = %v", err)
	}
	if response.TotalCount != 2 || len(response.Entries) != 1 || response.NextURI == "" {
		t.Fatalf("response total=%d entries=%d next=%q, want first of two rows", response.TotalCount, len(response.Entries), response.NextURI)
	}
	assertS3CSVServiceRows(t, response.Entries, []int64{3}, []string{"Grace"})
	next, err := queryService.FetchNextBatch(context.Background(), nil, response.QueryID, response.Slug, response.Token, 1)
	if err != nil {
		t.Fatalf("FetchNextBatch() error = %v", err)
	}
	if next.TotalCount != 2 || len(next.Entries) != 1 || next.NextURI != "" {
		t.Fatalf("next response total=%d entries=%d next=%q, want final row", next.TotalCount, len(next.Entries), next.NextURI)
	}
	assertS3CSVServiceRows(t, next.Entries, []int64{1}, []string{"Ada"})
	if _, err := queryService.FetchQuery(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: source.ID, SQL: "DELETE FROM events", Type: 1, BatchSize: 10,
	}); err == nil {
		t.Fatal("FetchQuery() accepted an OSS Delta write")
	}
}
