package olap

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestDruidConfiguredAdapterProvidesReadOnlySQLRows(t *testing.T) {
	var received DruidSQLRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "gateway" || password != "test-only-password" {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/status":
			_, _ = fmt.Fprint(w, `{"version":"31.0.0"}`)
		case "/druid/v2/sql":
			if r.Method != http.MethodPost {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if received.Query != "SELECT id, name, active, amount, nullable FROM records WHERE id = ?" ||
				received.ResultFormat != "array" || !received.Header || !received.SQLTypesHeader {
				http.Error(w, "unexpected SQL request", http.StatusBadRequest)
				return
			}
			w.Header().Set("X-Druid-SQL-Query-Id", "contract-query")
			_, _ = fmt.Fprint(w, `[["id","name","active","amount","nullable"],["BIGINT","VARCHAR","BOOLEAN","DOUBLE","VARCHAR"],[7,"Ada",true,3.5,null]]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	endpoint := mustParseDruidTestURL(t, server.URL)
	druidDriver := &DruidDriver{}
	db, err := druidDriver.OpenWithConfig(context.Background(), &model.DataSourceConfig{
		Host: endpoint.Hostname(), Port: mustDruidTestPort(t, endpoint), Username: "gateway", Password: "test-only-password",
	})
	if err != nil {
		t.Fatalf("OpenWithConfig() error = %v", err)
	}
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}

	rows, err := db.QueryContext(context.Background(), "SELECT id, name, active, amount, nullable FROM records WHERE id = ?", 7)
	if err != nil {
		t.Fatalf("QueryContext() error = %v", err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatalf("ColumnTypes() error = %v", err)
	}
	if len(columns) != 5 || columns[0].Name() != "id" || columns[0].DatabaseTypeName() != "BIGINT" || columns[3].DatabaseTypeName() != "DOUBLE" {
		t.Fatalf("unexpected columns: %#v", columns)
	}
	if !rows.Next() {
		t.Fatal("expected one Druid result row")
	}
	var id int64
	var name string
	var active bool
	var amount float64
	var nullable sql.NullString
	if err := rows.Scan(&id, &name, &active, &amount, &nullable); err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if id != 7 || name != "Ada" || !active || amount != 3.5 || nullable.Valid {
		t.Fatalf("scanned row = (%d, %q, %t, %v, %+v)", id, name, active, amount, nullable)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("Rows.Err() = %v", err)
	}
	if len(received.Parameters) != 1 || received.Parameters[0].Type != "BIGINT" || received.Parameters[0].Value.(float64) != 7 {
		t.Fatalf("parameters = %#v, want one BIGINT value 7", received.Parameters)
	}

	if _, err := db.ExecContext(context.Background(), "INSERT INTO records VALUES (8)"); !errors.Is(err, errDruidReadOnly) {
		t.Fatalf("ExecContext(write) error = %v, want read-only rejection", err)
	}
	if _, err := db.QueryContext(context.Background(), "DELETE FROM records"); !errors.Is(err, errDruidReadOnly) {
		t.Fatalf("QueryContext(write) error = %v, want read-only rejection", err)
	}
}

func TestDruidRESTClientMetadataAndCancellationUseProtocolStatusCodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/druid/coordinator/v1/datasources":
			_, _ = fmt.Fprint(w, `["events"]`)
		case "/druid/coordinator/v1/datasources/events":
			_, _ = fmt.Fprint(w, `{"name":"events","segments":2,"columns":["__time","user"]}`)
		case "/druid/v2/sql/query-id":
			if r.Method != http.MethodDelete {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewDruidRESTClient(server.URL)
	datasources, err := client.GetDatasources(context.Background())
	if err != nil || len(datasources) != 1 || datasources[0] != "events" {
		t.Fatalf("GetDatasources() = %#v, %v", datasources, err)
	}
	metadata, err := client.GetDatasourceMetadata(context.Background(), "events")
	if err != nil || metadata.Name != "events" || metadata.Segments != 2 {
		t.Fatalf("GetDatasourceMetadata() = %#v, %v", metadata, err)
	}
	if err := client.CancelQuery(context.Background(), "query-id"); err != nil {
		t.Fatalf("CancelQuery() error = %v", err)
	}
}

func TestDruidEndpointAndReadOnlyValidation(t *testing.T) {
	druidDriver := &DruidDriver{}
	if got := druidDriver.BuildDSN(&model.DataSourceConfig{Host: "router.internal", Port: 8088, SSL: true}); got != "https://router.internal:8088" {
		t.Fatalf("BuildDSN() = %q", got)
	}
	for _, dsn := range []string{"", "tcp://router:8082", "http://user:password@router:8082", "http://router:8082?token=secret"} {
		if err := druidDriver.ValidateDSN(dsn); err == nil {
			t.Errorf("ValidateDSN(%q) succeeded, want error", dsn)
		}
	}
	for _, query := range []string{"SELECT 1", " /* comment */ SELECT 1;", "EXPLAIN PLAN FOR SELECT 1"} {
		if !isDruidReadOnlyQuery(query) {
			t.Errorf("isDruidReadOnlyQuery(%q) = false, want true", query)
		}
	}
	for _, query := range []string{"INSERT INTO x VALUES (1)", "SELECT 1; DELETE FROM x", "WITH x AS (SELECT 1) SELECT * FROM x"} {
		if isDruidReadOnlyQuery(query) {
			t.Errorf("isDruidReadOnlyQuery(%q) = true, want false", query)
		}
	}
	parameters, err := druidParameters([]driver.NamedValue{{Ordinal: 1, Value: time.Unix(0, 0)}, {Ordinal: 2, Value: []byte("text")}})
	if err != nil || len(parameters) != 2 || parameters[0].Type != "TIMESTAMP" || parameters[1].Value != "text" {
		t.Fatalf("druidParameters() = %#v, %v", parameters, err)
	}
}

func TestDruidSQLRowsPreserveDuplicateColumnPositions(t *testing.T) {
	result, err := parseDruidSQLRows([]byte(`[["id","id"],["BIGINT","BIGINT"],[1,2]]`))
	if err != nil {
		t.Fatalf("parseDruidSQLRows() error = %v", err)
	}
	if len(result.Values) != 1 || len(result.Values[0]) != 2 || result.Values[0][0].(json.Number).String() != "1" || result.Values[0][1].(json.Number).String() != "2" {
		t.Fatalf("ordered values = %#v, want both duplicate-name positions", result.Values)
	}
}

func mustParseDruidTestURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func mustDruidTestPort(t *testing.T, endpoint *url.URL) int {
	t.Helper()
	port, err := strconv.Atoi(endpoint.Port())
	if err != nil {
		t.Fatal(err)
	}
	return port
}
