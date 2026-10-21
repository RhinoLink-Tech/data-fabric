package table_formats

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestBuildDeltaChangeDataFeedStatement(t *testing.T) {
	end := int64(11)
	statement, err := BuildDeltaChangeDataFeedStatement("main", "sales", "order`items", 2, &end)
	if err != nil {
		t.Fatalf("BuildDeltaChangeDataFeedStatement returned error: %v", err)
	}
	tick := string(rune(96))
	want := "SELECT * FROM table_changes('" + tick + "main" + tick + "." + tick + "sales" + tick + "." + tick + "order" + tick + tick + "items" + tick + "', 2, 11)"
	if statement != want {
		t.Fatalf("statement = %q, want %q", statement, want)
	}
	if _, err := BuildDeltaChangeDataFeedStatement("", "sales", "orders", 0, nil); err == nil {
		t.Fatal("missing catalog should be rejected")
	}
	if _, err := BuildDeltaChangeDataFeedStatement("main", "sales", "orders", -1, nil); err == nil {
		t.Fatal("negative start version should be rejected")
	}
	tooEarly := int64(2)
	if _, err := BuildDeltaChangeDataFeedStatement("main", "sales", "orders", 3, &tooEarly); err == nil {
		t.Fatal("end version before start should be rejected")
	}
}

func TestDeltaChangeDataFeedUsesInclusiveVersionRange(t *testing.T) {
	var statement string
	client, closeServer := newDeltaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/2.0/sql/statements" {
			http.NotFound(w, r)
			return
		}
		var request map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		statement, _ = request["statement"].(string)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"statement_id": "cdf-1",
			"status":       map[string]interface{}{"state": "SUCCEEDED"},
			"manifest": map[string]interface{}{
				"schema": map[string]interface{}{"columns": []interface{}{map[string]interface{}{"name": "_change_type"}}},
			},
			"result": map[string]interface{}{
				"data_array": [][]interface{}{{"insert"}},
			},
		})
	})
	defer closeServer()

	end := int64(9)
	result, err := client.ReadChangeDataFeed(context.Background(), "", "main", "sales", "orders", 4, &end)
	if err != nil {
		t.Fatalf("ReadChangeDataFeed returned error: %v", err)
	}
	tick := string(rune(96))
	if !strings.Contains(statement, "table_changes('"+tick+"main"+tick+"."+tick+"sales"+tick+"."+tick+"orders"+tick+"', 4, 9)") {
		t.Fatalf("unexpected CDF statement: %q", statement)
	}
	if len(result.Manifest.Data) != 1 || result.Manifest.Data[0][0] != "insert" {
		t.Fatalf("unexpected CDF result: %#v", result.Manifest.Data)
	}
}

func TestDeltaIncrementalCapabilityRequiresWarehouse(t *testing.T) {
	withoutWarehouse := &DeltaDriver{config: &DeltaConfig{BaseURL: "https://workspace.example"}}
	if withoutWarehouse.GetCapabilities().SupportsIncrementalRead {
		t.Fatal("incremental read should require a Databricks SQL Warehouse")
	}
	withWarehouse := &DeltaDriver{config: &DeltaConfig{BaseURL: "https://workspace.example", WarehouseID: "warehouse-1"}}
	if !withWarehouse.GetCapabilities().SupportsIncrementalRead {
		t.Fatal("configured Databricks SQL Warehouse should expose incremental reads")
	}
}

func TestDeltaStatementRejectsTruncatedInlineResult(t *testing.T) {
	client, closeServer := newDeltaTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"statement_id": "truncated-1",
			"status":       map[string]interface{}{"state": "SUCCEEDED"},
			"manifest":     map[string]interface{}{"truncated": true},
			"result":       map[string]interface{}{"data_array": [][]interface{}{{"partial"}}},
		})
	})
	defer closeServer()
	if _, err := client.QueryTable(context.Background(), "", "SELECT 1"); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("QueryTable error = %v, want a truncation error", err)
	}
}
