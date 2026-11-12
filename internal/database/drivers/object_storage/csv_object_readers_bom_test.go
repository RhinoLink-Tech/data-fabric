package object_storage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestCSVObjectReadersPreserveHeaderNames(t *testing.T) {
	const (
		bucket   = "fixture-bucket"
		plainKey = "orders.csv"
		bomKey   = "orders-bom.csv"
		csvData  = "\"order_id\";\"label\"\n101;front\uFEFFend\n202;second\n"
	)
	objects := map[string]string{
		"/" + bucket + "/" + plainKey: csvData,
		"/" + bucket + "/" + bomKey:   "\xEF\xBB\xBF" + csvData,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, exists := objects[r.URL.Path]
		if !exists {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("ETag", "\"fixture\"")
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		switch r.Method {
		case http.MethodGet:
			_, _ = io.WriteString(w, data)
		case http.MethodHead:
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)

	ctx := t.Context()
	s3Driver, err := NewS3CSVDriver(ctx, &S3CSVDriverConfig{
		S3Config: &S3Config{
			Region: "us-east-1", Bucket: bucket, EndpointURL: server.URL, ForcePathStyle: true,
			AccessKey: "fixture-key", SecretKey: "fixture-secret",
		},
		Delimiter: ';', HasHeader: true,
	})
	if err != nil {
		t.Fatalf("open S3 CSV fixture: %v", err)
	}
	minioDriver, err := NewMinIOCSVDriver(ctx, &MinIOCSVDriverConfig{
		MinIOConfig: &MinIOConfig{
			Region: "us-east-1", Bucket: bucket, Endpoint: strings.TrimPrefix(server.URL, "http://"),
			AccessKey: "fixture-key", SecretKey: "fixture-secret",
		},
		Delimiter: ';', HasHeader: true,
	})
	if err != nil {
		t.Fatalf("open MinIO CSV fixture: %v", err)
	}
	readers := []struct {
		name   string
		detect func(context.Context, string) (*DetectedSchema, error)
		query  func(context.Context, string) ([]map[string]any, error)
		stream func(context.Context, string, func(map[string]any) error) error
	}{
		{
			name: "S3", detect: s3Driver.DetectSchema, stream: s3Driver.StreamQuery,
			query: func(ctx context.Context, key string) ([]map[string]any, error) {
				result, err := s3Driver.Query(ctx, &S3CSVQuery{Key: key})
				if err != nil {
					return nil, err
				}
				return result.Rows, nil
			},
		},
		{
			name: "MinIO", detect: minioDriver.DetectSchema, stream: minioDriver.StreamQuery,
			query: func(ctx context.Context, key string) ([]map[string]any, error) {
				result, err := minioDriver.Query(ctx, &MinIOCSVQuery{Key: key})
				if err != nil {
					return nil, err
				}
				return result.Rows, nil
			},
		},
	}
	want := []map[string]any{
		{"order_id": int64(101), "label": "front\uFEFFend"},
		{"order_id": int64(202), "label": "second"},
	}
	for _, reader := range readers {
		for _, key := range []string{plainKey, bomKey} {
			t.Run(reader.name+"/"+key, func(t *testing.T) {
				schema, err := reader.detect(t.Context(), key)
				if err != nil {
					t.Fatalf("detect schema: %v", err)
				}
				if len(schema.Columns) != 2 || schema.Columns[0].Name != "order_id" || schema.Columns[1].Name != "label" {
					t.Fatalf("unexpected schema columns: %+v", schema.Columns)
				}
				rows, err := reader.query(t.Context(), key)
				if err != nil || !reflect.DeepEqual(rows, want) {
					t.Errorf("query rows = %v, error = %v, want %v", rows, err, want)
				}
				var streamed []map[string]any
				err = reader.stream(t.Context(), key, func(row map[string]any) error {
					streamed = append(streamed, row)
					return nil
				})
				if err != nil || !reflect.DeepEqual(streamed, want) {
					t.Errorf("stream rows = %v, error = %v, want %v", streamed, err, want)
				}
			})
		}
	}
}
