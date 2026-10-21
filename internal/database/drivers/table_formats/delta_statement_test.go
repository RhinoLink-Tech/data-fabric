package table_formats

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newDeltaTestClient(t *testing.T, handler http.HandlerFunc) (*DeltaRESTClient, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	client, err := NewDeltaRESTClient(&DeltaConfig{
		BaseURL: server.URL, WarehouseID: "warehouse-1", Timeout: 3 * time.Second,
		Auth: &DeltaAuth{Token: "test-token", TokenType: "bearer"},
	})
	if err != nil {
		server.Close()
		t.Fatalf("NewDeltaRESTClient returned error: %v", err)
	}
	return client, server.Close
}

func TestDeltaStatementExecutionPollsAndCollectsChunks(t *testing.T) {
	polls := 0
	client, closeServer := newDeltaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/2.0/sql/statements":
			var request map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode request: %v", err)
			}
			if request["warehouse_id"] != "warehouse-1" || request["disposition"] != "INLINE" || request["format"] != "JSON_ARRAY" || request["wait_timeout"] != "0s" {
				t.Errorf("statement request has wrong API contract: %#v", request)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"statement_id": "statement-1",
				"status":       map[string]interface{}{"state": "PENDING"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/2.0/sql/statements/statement-1":
			polls++
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"statement_id": "statement-1",
				"status":       map[string]interface{}{"state": "SUCCEEDED"},
				"manifest": map[string]interface{}{
					"schema": map[string]interface{}{"columns": []interface{}{map[string]interface{}{"name": "version"}, map[string]interface{}{"name": "timestamp"}}},
				},
				"result": map[string]interface{}{
					"data_array":               [][]interface{}{{float64(2), "2026-10-21T00:00:00Z"}},
					"next_chunk_index":         1,
					"next_chunk_internal_link": "",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/2.0/sql/statements/statement-1/result/chunks/1":
			if r.URL.Query().Get("warehouse_id") != "warehouse-1" {
				t.Errorf("chunk request did not preserve warehouse ID: %s", r.URL.RawQuery)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"data_array": [][]interface{}{{float64(1), "2026-10-20T00:00:00Z"}},
			})
		default:
			http.NotFound(w, r)
		}
	})
	defer closeServer()

	result, err := client.QueryTable(context.Background(), "", "SELECT 1")
	if err != nil {
		t.Fatalf("QueryTable returned error: %v", err)
	}
	if polls != 1 || len(result.Manifest.Data) != 2 {
		t.Fatalf("poll count=%d, rows=%d", polls, len(result.Manifest.Data))
	}
	if result.Manifest.Data[0][0] != float64(2) || result.Manifest.Data[1][0] != float64(1) {
		t.Fatalf("result chunks were not concatenated in order: %#v", result.Manifest.Data)
	}
}

func TestDeltaHistoryUsesStatementExecutionAndParsesRows(t *testing.T) {
	client, closeServer := newDeltaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var request map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&request)
		statement, _ := request["statement"].(string)
		q := string(rune(96))
		expected := "DESCRIBE HISTORY " + q + "catalog" + q + "." + q + "schema" + q + "." + q + "table" + q + " LIMIT 1000"
		if !strings.Contains(statement, expected) {
			t.Errorf("unsafe or unexpected history statement: %q", statement)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"statement_id": "history-1",
			"status":       map[string]interface{}{"state": "SUCCEEDED"},
			"manifest": map[string]interface{}{
				"schema": map[string]interface{}{"columns": []interface{}{
					map[string]interface{}{"name": "version"},
					map[string]interface{}{"name": "timestamp"},
					map[string]interface{}{"name": "userId"},
					map[string]interface{}{"name": "userName"},
					map[string]interface{}{"name": "operation"},
					map[string]interface{}{"name": "operationParameters"},
				}},
			},
			"result": map[string]interface{}{
				"data_array": [][]interface{}{{float64(9), "2026-10-21T00:00:00Z", "u-1", "Feng", "WRITE", map[string]interface{}{"mode": "append"}}},
			},
		})
	})
	defer closeServer()

	commits, err := client.GetTableHistory(context.Background(), "catalog.schema.table")
	if err != nil {
		t.Fatalf("GetTableHistory returned error: %v", err)
	}
	if len(commits) != 1 || commits[0].Version != 9 || commits[0].Operation != "WRITE" || commits[0].UserId != "u-1" {
		t.Fatalf("unexpected history: %#v", commits)
	}
	if commits[0].Timestamp.IsZero() || commits[0].OperationParameters["mode"] != "append" {
		t.Fatalf("history metadata was not decoded: %#v", commits[0])
	}
}

func TestDeltaHistoryIdentifierQuotesComponents(t *testing.T) {
	got, err := deltaHistoryIdentifier("catalog.schema.a b")
	if err != nil {
		t.Fatal(err)
	}
	q := string(rune(96))
	expected := q + "catalog" + q + "." + q + "schema" + q + "." + q + "a b" + q
	if got != expected {
		t.Fatalf("identifier = %q", got)
	}
	if _, err := deltaHistoryIdentifier("catalog..table"); err == nil {
		t.Fatal("expected empty identifier component to be rejected")
	}
}

func TestDeltaStatementRejectsCrossOriginChunkURL(t *testing.T) {
	client := &DeltaRESTClient{baseURL: "https://workspace.example"}
	if _, err := client.deltaChunkURL("statement", "warehouse", nil, "https://attacker.example/chunk"); err == nil {
		t.Fatal("expected cross-origin chunk URL to be rejected")
	}
}
