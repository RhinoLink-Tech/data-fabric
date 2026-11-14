package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/linkedin/goavro/v2"
)

func TestS3AvroBinaryAndFloatAcrossPagination(t *testing.T) {
	const schema = `{"type":"record","name":"Record","fields":[{"name":"id","type":"long"},{"name":"payload","type":["null","bytes"],"default":null},{"name":"score","type":"float"}]}`
	codec, err := goavro.NewCodec(schema)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer, err := goavro.NewOCFWriter(goavro.OCFConfig{W: &output, Codec: codec, CompressionName: "null", SyncMarker: [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16}})
	if err != nil {
		t.Fatal(err)
	}
	records := []interface{}{
		map[string]interface{}{"id": int64(1), "payload": goavro.Union("bytes", []byte{0xff, 0x00}), "score": float32(1.25)},
		map[string]interface{}{"id": int64(2), "payload": goavro.Union("bytes", []byte{}), "score": float32(2.5)},
		map[string]interface{}{"id": int64(3), "payload": nil, "score": float32(3.75)},
	}
	if err := writer.Append(records); err != nil {
		t.Fatal(err)
	}
	data := output.Bytes()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/demo-bucket/data.avro" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", `"binary-fixture"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		switch r.Method {
		case http.MethodHead:
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			_, _ = w.Write(data)
		default:
			http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	source := &model.DataSource{ID: "s3-avro-binary-pagination-test", Type: model.DatabaseTypeS3Avro, Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{Host: "data.avro", Database: "demo-bucket", Username: "test-access", Password: "test-secret",
			AdditionalProps: map[string]interface{}{"object_key": "data.avro", "table_name": "records", "region": "us-east-1", "endpoint_url": server.URL, "force_path_style": true}}}
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	defer func() { _ = pool.CloseAll() }()
	ctx := context.Background()
	connection, err := pool.GetConnection(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	db := connection
	const query = "SELECT payload, score FROM records ORDER BY id"
	queries := []string{
		query,
		"SELECT payload, score FROM (SELECT id, payload, score FROM (SELECT id, payload, score FROM records) AS inner1) AS inner2 ORDER BY id",
	}
	for _, directQuery := range queries {
		direct, err := db.QueryContext(ctx, directQuery)
		if err != nil {
			t.Fatalf("direct query %q: %v", directQuery, err)
		}
		types, err := direct.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(types) != 2 || types[0].DatabaseTypeName() != "VARBINARY" || types[1].DatabaseTypeName() != "REAL" {
			t.Fatalf("direct query %q types = %v", directQuery, types)
		}
		if !direct.Next() {
			t.Fatalf("direct query returned no rows: %v", direct.Err())
		}
		var directBytes []byte
		var directScore float64
		if err := direct.Scan(&directBytes, &directScore); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(directBytes, []byte{0xff, 0x00}) || directScore != 1.25 {
			t.Fatalf("direct row = %x, %v", directBytes, directScore)
		}
		if err := direct.Close(); err != nil {
			t.Fatal(err)
		}
	}

	qs := NewQueryService(federationSourceRepository{source: source}, pool, false, newTestSQLTranslationManager())
	page, err := qs.FetchQuery(ctx, nil, &model.FetchQueryRequest{DataSourceID: source.ID, SQL: query, Type: 1, BatchSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	expectedPayloads := []interface{}{"/wA=", "", nil}
	expectedScores := []float64{1.25, 2.5, 3.75}
	for i := range expectedPayloads {
		if len(page.Columns) != 2 || page.Columns[0].Type != model.TypeBinary || page.Columns[1].Type != model.TypeFloat {
			t.Fatalf("page %d column types = %+v", i+1, page.Columns)
		}
		if len(page.Entries) != 1 || page.Entries[0] == nil || len(*page.Entries[0]) != 2 {
			t.Fatalf("page %d entries = %#v", i+1, page.Entries)
		}
		row := *page.Entries[0]
		if row[0] != expectedPayloads[i] {
			t.Fatalf("page %d binary = %#v, want %#v", i+1, row[0], expectedPayloads[i])
		}
		var score float64
		switch v := row[1].(type) {
		case float32:
			score = float64(v)
		case float64:
			score = v
		default:
			t.Fatalf("page %d score type = %T", i+1, row[1])
		}
		if score != expectedScores[i] {
			t.Fatalf("page %d score = %v, want %v", i+1, score, expectedScores[i])
		}
		wire, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 && (!strings.Contains(string(wire), `"/wA="`) || strings.Contains(string(wire), "�")) {
			t.Fatalf("first page wire JSON = %s", wire)
		}
		if i == 1 && !strings.Contains(string(wire), `["",2.5]`) {
			t.Fatalf("empty binary wire JSON = %s", wire)
		}
		if i == 2 && !strings.Contains(string(wire), `[null,3.75]`) {
			t.Fatalf("NULL binary wire JSON = %s", wire)
		}
		if i < len(expectedPayloads)-1 {
			if page.NextURI == "" {
				t.Fatalf("page %d missing continuation", i+1)
			}
			page, err = qs.FetchNextBatch(ctx, nil, page.QueryID, page.Slug, page.Token, 1)
			if err != nil {
				t.Fatalf("continuation %d: %v", i+2, err)
			}
		}
	}
	if page.NextURI != "" {
		t.Fatalf("final page unexpectedly has continuation %q", page.NextURI)
	}
}
