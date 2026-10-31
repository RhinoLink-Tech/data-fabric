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

func TestFetchQueryRoutesAzureDeltaThroughGateway(t *testing.T) {
	data := parquetObjectQueryTestData(t)
	const (
		container = "demo-container"
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
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("comp") == "list" {
			prefix := r.URL.Query().Get("prefix")
			if prefix != tablePath+"/_delta_log/" || r.URL.Query().Get("sig") != "fixture" {
				http.Error(w, "unexpected Azure list request", http.StatusBadRequest)
				return
			}
			xml := fmt.Sprintf(
				"<?xml version=\"1.0\" encoding=\"utf-8\"?><EnumerationResults ServiceEndpoint=\"%s/\" ContainerName=\"%s\"><Prefix>%s</Prefix><MaxResults>1000</MaxResults><Blobs><Blob><Name>%s</Name><Properties><Creation-Time>2026-10-29T00:00:00Z</Creation-Time><Last-Modified>2026-10-29T00:00:00Z</Last-Modified><Etag>&quot;azure-delta-fixture&quot;</Etag><Content-Length>%d</Content-Length><Content-Type>application/json</Content-Type><BlobType>BlockBlob</BlobType><LeaseStatus>unlocked</LeaseStatus></Properties></Blob></Blobs><NextMarker></NextMarker></EnumerationResults>",
				server.URL, container, prefix, logKey, len(logBody),
			)
			w.Header().Set("Content-Type", "application/xml")
			w.Header().Set("Content-Length", strconv.Itoa(len(xml)))
			w.Header().Set("x-ms-version", "2023-11-03")
			_, _ = w.Write([]byte(xml))
			return
		}
		if r.URL.Query().Get("sig") != "fixture" {
			http.Error(w, "missing SAS query token", http.StatusForbidden)
			return
		}
		key := strings.TrimPrefix(r.URL.Path, "/"+container+"/")
		var content []byte
		switch key {
		case logKey:
			content = []byte(logBody)
		case dataKey:
			content = data
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", "\"azure-delta-fixture\"")
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("x-ms-blob-type", "BlockBlob")
		w.Header().Set("x-ms-request-id", "fixture-request")
		w.Header().Set("x-ms-version", "2023-11-03")
		_, _ = w.Write(content)
	}))
	defer server.Close()

	source := &model.DataSource{
		ID: "azure-delta-query-service-test", Type: model.DatabaseTypeAzureDelta,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host: tablePath, Database: container, Username: "testaccount",
			AdditionalProps: map[string]interface{}{
				"table_path": tablePath, "table_name": "events", "endpoint_url": server.URL,
				"sas_token": "sv=2023-11-03&sig=fixture",
			},
		},
	}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer func() { _ = pool.CloseAll() }()
	checker := database.NewHealthChecker(pool)
	if err := checker.ValidateDataSourceConfiguration(&source.Config, source.Type); err != nil {
		t.Fatalf("validate Azure Delta config: %v", err)
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
		t.Fatal("FetchQuery() accepted an Azure Delta write")
	}
}
