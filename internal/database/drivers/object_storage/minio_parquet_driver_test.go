package object_storage

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/xitongsys/parquet-go/parquet"
	"github.com/xitongsys/parquet-go/reader"
	"github.com/xitongsys/parquet-go/schema"
	"github.com/xitongsys/parquet-go/writer"
)

type minIOParquetTestRow struct {
	Name      string  `parquet:"name=name, type=BYTE_ARRAY, convertedtype=UTF8, encoding=PLAIN_DICTIONARY"`
	Count     int32   `parquet:"name=count, type=INT32"`
	Enabled   bool    `parquet:"name=enabled, type=BOOLEAN"`
	Score     float64 `parquet:"name=score, type=DOUBLE"`
	Nullable  *int32  `parquet:"name=nullable_count, type=INT32"`
	EventTime int64   `parquet:"name=event_time, type=INT64, convertedtype=TIMESTAMP_MICROS"`
}

func createMinIOParquetTestData(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	parquetWriter, err := writer.NewParquetWriterFromWriter(&output, new(minIOParquetTestRow), 1)
	if err != nil {
		t.Fatalf("create Parquet writer: %v", err)
	}
	parquetWriter.CompressionType = parquet.CompressionCodec_UNCOMPRESSED
	value := int32(17)
	rows := []minIOParquetTestRow{
		{Name: "first", Count: 3, Enabled: true, Score: 1.5, Nullable: nil, EventTime: 1_700_000_000_000_000},
		{Name: "second", Count: 9, Enabled: false, Score: 2.25, Nullable: &value, EventTime: 1_700_000_001_000_000},
	}
	for _, row := range rows {
		if err := parquetWriter.Write(row); err != nil {
			t.Fatalf("write Parquet row: %v", err)
		}
	}
	if err := parquetWriter.WriteStop(); err != nil {
		t.Fatalf("finish Parquet writer: %v", err)
	}
	return append([]byte(nil), output.Bytes()...)
}

func newMinIOParquetTestClient(t *testing.T, data []byte) (*MinIOClient, *atomic.Int64, string) {
	t.Helper()
	var rangeRequests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-bucket/events.parquet" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", "\"test-etag\"")
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("Accept-Ranges", "bytes")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		start, end := int64(0), int64(len(data))-1
		if rangeHeader := r.Header.Get("Range"); rangeHeader != "" {
			rangeRequests.Add(1)
			if _, err := fmt.Sscanf(rangeHeader, "bytes=%d-%d", &start, &end); err != nil {
				http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
				return
			}
			if start < 0 || end < start || start >= int64(len(data)) {
				http.Error(w, "range outside object", http.StatusRequestedRangeNotSatisfiable)
				return
			}
			if end >= int64(len(data)) {
				end = int64(len(data)) - 1
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
			w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
			w.WriteHeader(http.StatusPartialContent)
		}
		_, _ = w.Write(data[start : end+1])
	}))
	t.Cleanup(server.Close)
	client, err := NewMinIOClient(context.Background(), &MinIOConfig{
		Endpoint:  strings.TrimPrefix(server.URL, "http://"),
		AccessKey: "test-access",
		SecretKey: "test-secret",
		Region:    "us-east-1",
		Bucket:    "test-bucket",
	})
	if err != nil {
		t.Fatalf("create MinIO client: %v", err)
	}
	return client, &rangeRequests, server.URL
}

func TestMinIOParquetQueryReadsRealRowsAndNulls(t *testing.T) {
	data := createMinIOParquetTestData(t)
	client, rangeRequests, _ := newMinIOParquetTestClient(t, data)
	driver := &MinIOParquetDriver{minioClient: client}

	result, err := driver.Query(context.Background(), &MinIOParquetQuery{Key: "events.parquet"})
	if err != nil {
		t.Fatalf("query MinIO Parquet object: %v", err)
	}
	if result.NumRows != 2 || len(result.Rows) != 2 {
		t.Fatalf("expected two decoded rows, got count=%d rows=%#v", result.NumRows, result.Rows)
	}
	if result.Schema == nil || len(result.Schema.Columns) != 6 {
		t.Fatalf("expected six columns in result schema, got %#v", result.Schema)
	}
	if result.BytesRead <= 0 {
		t.Errorf("expected ranged object reads to report transferred bytes, got %d", result.BytesRead)
	}
	if got := result.Rows[0]["name"]; got != "first" {
		t.Errorf("first row name = %#v, want %q", got, "first")
	}
	if got := result.Rows[0]["count"]; got != int32(3) {
		t.Errorf("first row count = %#v (%T), want int32(3)", got, got)
	}
	if got := result.Rows[0]["nullable_count"]; got != nil {
		t.Errorf("first row nullable count = %#v, want nil", got)
	}
	if got := result.Rows[1]["nullable_count"]; got != int32(17) {
		t.Errorf("second row nullable count = %#v, want int32(17)", got)
	}
	if got, ok := result.Rows[0]["event_time"].(time.Time); !ok || !got.Equal(time.UnixMicro(1_700_000_000_000_000).UTC()) {
		t.Errorf("event_time = %#v (%T), want timestamp", result.Rows[0]["event_time"], result.Rows[0]["event_time"])
	}
	if rangeRequests.Load() == 0 {
		t.Error("expected Parquet reader to use ranged MinIO GET requests")
	}
}

func TestMinIOParquetReaderMapsSchemaAndRemainingRows(t *testing.T) {
	client, _, _ := newMinIOParquetTestClient(t, createMinIOParquetTestData(t))
	minioReader := NewMinIOParquetReader(client, "events.parquet")
	fileReader, err := minioReader.Read(context.Background())
	if err != nil {
		t.Fatalf("open Parquet object: %v", err)
	}
	defer fileReader.Close()

	first, err := fileReader.ReadNumRows(1)
	if err != nil {
		t.Fatalf("read first batch: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("first batch has %d rows, want 1", len(first))
	}
	remaining, err := fileReader.ReadAll()
	if err != nil {
		t.Fatalf("read remaining rows: %v", err)
	}
	if len(remaining) != 1 || remaining[0]["name"] != "second" {
		t.Fatalf("remaining rows = %#v, want second row", remaining)
	}
	metadata, err := fileReader.GetSchema()
	if err != nil {
		t.Fatalf("read Parquet schema: %v", err)
	}
	if len(metadata.Columns) != 6 {
		t.Fatalf("schema has %d columns, want 6: %#v", len(metadata.Columns), metadata.Columns)
	}
	if metadata.Columns[0].StandardType != "string" || metadata.Columns[1].StandardType != "integer" ||
		metadata.Columns[4].StandardType != "integer" || metadata.Columns[4].RepetitionType != "OPTIONAL" ||
		metadata.Columns[5].StandardType != "timestamp" {
		t.Errorf("unexpected Parquet type/nullability mapping: %#v", metadata.Columns)
	}
	driver := &MinIOParquetDriver{}
	stdSchema := driver.ConvertToStandardSchema(metadata)
	if len(stdSchema.Columns) != 6 || !stdSchema.Columns[4].Nullable ||
		stdSchema.Columns[1].Type != "integer" || stdSchema.Columns[5].Type != "timestamp" {
		t.Errorf("unexpected standardized schema: %#v", stdSchema)
	}
}

func TestMinIOParquetReaderReportsInvalidAndUnsupportedSchemas(t *testing.T) {
	client, _, _ := newMinIOParquetTestClient(t, []byte("not a Parquet file"))
	if _, err := NewMinIOParquetReader(client, "events.parquet").Read(context.Background()); err == nil {
		t.Fatal("expected invalid Parquet data to return an error")
	}

	root := parquet.NewSchemaElement()
	root.Name = "root"
	root.NumChildren = int32Ptr(1)
	root.RepetitionType = parquet.FieldRepetitionTypePtr(parquet.FieldRepetitionType_REQUIRED)
	group := parquet.NewSchemaElement()
	group.Name = "nested"
	group.NumChildren = int32Ptr(1)
	group.RepetitionType = parquet.FieldRepetitionTypePtr(parquet.FieldRepetitionType_REQUIRED)
	leaf := parquet.NewSchemaElement()
	leaf.Name = "value"
	leaf.Type = parquet.TypePtr(parquet.Type_INT32)
	leaf.RepetitionType = parquet.FieldRepetitionTypePtr(parquet.FieldRepetitionType_REQUIRED)
	handler := schema.NewSchemaHandlerFromSchemaList([]*parquet.SchemaElement{root, group, leaf})
	if _, err := parquetColumns(&reader.ParquetReader{SchemaHandler: handler}); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Fatalf("nested schema error = %v, want explicit unsupported nested schema", err)
	}
}

func int32Ptr(value int32) *int32 { return &value }

func TestMinIOParquetQueryProjectsColumns(t *testing.T) {
	client, _, _ := newMinIOParquetTestClient(t, createMinIOParquetTestData(t))
	driver := &MinIOParquetDriver{minioClient: client}
	result, err := driver.Query(context.Background(), &MinIOParquetQuery{
		Key:     "events.parquet",
		Columns: []string{"name", "count"},
		Limit:   1,
	})
	if err != nil {
		t.Fatalf("query projected columns: %v", err)
	}
	if len(result.Rows) != 1 || len(result.Rows[0]) != 2 {
		t.Fatalf("projected result = %#v, want one row with two columns", result.Rows)
	}
	if result.Rows[0]["name"] != "first" || result.Rows[0]["count"] != int32(3) {
		t.Errorf("unexpected projected values: %#v", result.Rows[0])
	}
	if _, ok := result.Rows[0]["enabled"]; ok {
		t.Errorf("unselected column was returned: %#v", result.Rows[0])
	}
	if result.Schema == nil || len(result.Schema.Columns) != 2 ||
		result.Schema.Columns[0].Name != "name" || result.Schema.Columns[1].Name != "count" {
		t.Errorf("projected schema = %#v, want name and count", result.Schema)
	}

	if _, err := driver.Query(context.Background(), &MinIOParquetQuery{
		Key:     "events.parquet",
		Columns: []string{"missing"},
	}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("unknown column error = %v, want an explicit column error", err)
	}

	filtered, err := driver.Query(context.Background(), &MinIOParquetQuery{
		Key:     "events.parquet",
		Filters: map[string]interface{}{"enabled": false},
		Offset:  0,
	})
	if err != nil {
		t.Fatalf("query filtered rows: %v", err)
	}
	if len(filtered.Rows) != 1 || filtered.Rows[0]["name"] != "second" {
		t.Errorf("filtered rows = %#v, want only second row", filtered.Rows)
	}
}

func TestMinIOParquetConfigOpensReadOnlySQLAdapter(t *testing.T) {
	_, _, endpoint := newMinIOParquetTestClient(t, createMinIOParquetTestData(t))
	config := &model.DataSourceConfig{
		Host: "events.parquet", Database: "test-bucket", Username: "test-access", Password: "test-secret",
		AdditionalProps: map[string]interface{}{
			"endpoint_url": endpoint, "object_key": "events.parquet", "table_name": "events", "region": "us-east-1",
		},
	}
	if err := (&MinIOParquetDriver{}).ValidateDSN("minio://test-bucket/events.parquet"); err != nil {
		t.Fatalf("validate MinIO Parquet DSN: %v", err)
	}
	for _, invalidEndpoint := range []string{"ftp://minio.internal:9000", "http://minio.internal:9000/path", "http://user:secret@minio.internal:9000"} {
		invalidConfig := *config
		invalidConfig.AdditionalProps = map[string]interface{}{
			"endpoint_url": invalidEndpoint, "object_key": "events.parquet", "table_name": "events",
		}
		if _, err := (&MinIOParquetDriver{}).OpenWithConfig(context.Background(), &invalidConfig); err == nil {
			t.Errorf("OpenWithConfig() accepted invalid endpoint %q", invalidEndpoint)
		}
	}
	db, err := (&MinIOParquetDriver{}).OpenWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open MinIO Parquet SQL adapter: %v", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(context.Background(), "SELECT name AS person, count FROM events WHERE count >= ? ORDER BY count DESC LIMIT 1 OFFSET 1", int32(3))
	if err != nil {
		t.Fatalf("query configured Parquet table: %v", err)
	}
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"person", "count"}) {
		t.Fatalf("query columns = %v, error = %v", columns, err)
	}
	if !rows.Next() {
		t.Fatalf("expected one paginated row, rows error: %v", rows.Err())
	}
	var name string
	var count int64
	if err := rows.Scan(&name, &count); err != nil {
		t.Fatalf("scan configured Parquet result: %v", err)
	}
	if name != "first" || count != 3 {
		t.Fatalf("row = (%q, %d), want (first, 3)", name, count)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected extra row or iteration error: %v", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close query rows: %v", err)
	}

	var total int64
	if err := db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM events WHERE enabled = true").Scan(&total); err != nil || total != 1 {
		t.Fatalf("count result = %d, error = %v; want 1", total, err)
	}

	schemaRows, err := db.QueryContext(context.Background(), "SELECT * FROM events LIMIT 0")
	if err != nil {
		t.Fatalf("inspect configured Parquet schema: %v", err)
	}
	types, err := schemaRows.ColumnTypes()
	if err != nil || len(types) != 6 || types[0].DatabaseTypeName() != "VARCHAR" || types[1].DatabaseTypeName() != "INTEGER" {
		t.Fatalf("Parquet column types = %v, error = %v", types, err)
	}
	if schemaRows.Next() || schemaRows.Err() != nil {
		t.Fatalf("LIMIT 0 returned data or failed: %v", schemaRows.Err())
	}
	_ = schemaRows.Close()

	if _, err := db.ExecContext(context.Background(), "DELETE FROM events"); err == nil || !strings.Contains(err.Error(), "read-only SELECT") {
		t.Fatalf("ExecContext() error = %v, want read-only rejection", err)
	}
	if _, err := db.QueryContext(context.Background(), "DELETE FROM events"); err == nil {
		t.Fatal("QueryContext() accepted a write statement")
	}
	for _, query := range []string{
		"SELECT events.name FROM events",
		"SELECT name FROM events AS e",
		"SELECT name FROM other_table",
	} {
		if _, err := db.QueryContext(context.Background(), query); err == nil {
			t.Errorf("QueryContext() accepted unsupported table identity in %q", query)
		}
	}
}

func TestParquetStandardTypeMapsPhysicalAndLogicalTypes(t *testing.T) {
	tests := []struct {
		name      string
		physical  parquet.Type
		converted *parquet.ConvertedType
		want      string
	}{
		{name: "boolean", physical: parquet.Type_BOOLEAN, want: "boolean"},
		{name: "int32", physical: parquet.Type_INT32, want: "integer"},
		{name: "int64", physical: parquet.Type_INT64, want: "bigint"},
		{name: "legacy int96 timestamp", physical: parquet.Type_INT96, want: "timestamp"},
		{name: "float", physical: parquet.Type_FLOAT, want: "float"},
		{name: "double", physical: parquet.Type_DOUBLE, want: "double"},
		{name: "unannotated byte array", physical: parquet.Type_BYTE_ARRAY, want: "binary"},
		{name: "fixed binary", physical: parquet.Type_FIXED_LEN_BYTE_ARRAY, want: "binary"},
		{name: "utf8", physical: parquet.Type_BYTE_ARRAY, converted: parquet.ConvertedTypePtr(parquet.ConvertedType_UTF8), want: "string"},
		{name: "date", physical: parquet.Type_INT32, converted: parquet.ConvertedTypePtr(parquet.ConvertedType_DATE), want: "date"},
		{name: "decimal", physical: parquet.Type_INT64, converted: parquet.ConvertedTypePtr(parquet.ConvertedType_DECIMAL), want: "decimal"},
		{name: "time micros", physical: parquet.Type_INT64, converted: parquet.ConvertedTypePtr(parquet.ConvertedType_TIME_MICROS), want: "time"},
		{name: "timestamp millis", physical: parquet.Type_INT64, converted: parquet.ConvertedTypePtr(parquet.ConvertedType_TIMESTAMP_MILLIS), want: "timestamp"},
		{name: "narrow integer", physical: parquet.Type_INT32, converted: parquet.ConvertedTypePtr(parquet.ConvertedType_INT_16), want: "integer"},
		{name: "unsigned bigint", physical: parquet.Type_INT64, converted: parquet.ConvertedTypePtr(parquet.ConvertedType_UINT_64), want: "bigint"},
		{name: "json", physical: parquet.Type_BYTE_ARRAY, converted: parquet.ConvertedTypePtr(parquet.ConvertedType_JSON), want: "json"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			element := &parquet.SchemaElement{Type: parquet.TypePtr(test.physical), ConvertedType: test.converted}
			got, err := parquetStandardType(element)
			if err != nil {
				t.Fatalf("map type: %v", err)
			}
			if got != test.want {
				t.Errorf("mapped type = %q, want %q", got, test.want)
			}
		})
	}

	decimal, err := parquetValue(reflect.ValueOf(int64(12345)), ParquetColumn{
		StandardType: "decimal",
		Precision:    8,
		Scale:        2,
	}, &parquet.SchemaElement{})
	if err != nil {
		t.Fatalf("map decimal value: %v", err)
	}
	if decimal != "123.45" {
		t.Errorf("decimal value = %#v, want %q", decimal, "123.45")
	}
	timeValue, err := parquetValue(reflect.ValueOf(int64(3_723_000_000)), ParquetColumn{StandardType: "time"}, &parquet.SchemaElement{
		Type:          parquet.TypePtr(parquet.Type_INT64),
		ConvertedType: parquet.ConvertedTypePtr(parquet.ConvertedType_TIME_MICROS),
	})
	if err != nil || timeValue != "01:02:03.000000" {
		t.Errorf("time value = %#v, error %v, want 01:02:03.000000", timeValue, err)
	}
	unsigned, err := parquetValue(reflect.ValueOf(int64(-1)), ParquetColumn{StandardType: "bigint"}, &parquet.SchemaElement{
		Type:          parquet.TypePtr(parquet.Type_INT64),
		ConvertedType: parquet.ConvertedTypePtr(parquet.ConvertedType_UINT_64),
	})
	if err != nil || unsigned != uint64(^uint64(0)) {
		t.Errorf("unsigned integer = %#v, error %v, want MaxUint64", unsigned, err)
	}
}
