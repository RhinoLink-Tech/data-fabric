package mcp

import (
	"context"
	"encoding/json"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"testing"
)

type catalogResult struct {
	SourceTypes []struct {
		Type     string
		Category string
		Format   string
		Status   string
		Verified bool `json:"end_to_end_verified"`
	} `json:"source_types"`
	Count int
}

func TestListSourceTypesReflectsRegistryAndFilters(t *testing.T) {
	srv := NewMCPServer(nil, nil, nil)
	read := func(args map[string]any) catalogResult {
		t.Helper()
		result, err := srv.handleListSourceTypes(context.Background(), callToolRequest("list_source_types", args))
		if err != nil || result.IsError {
			t.Fatalf("catalog: %v, %+v", err, result)
		}
		var c catalogResult
		if err = json.Unmarshal([]byte(resultText(t, result)), &c); err != nil {
			t.Fatal(err)
		}
		if c.Count != len(c.SourceTypes) {
			t.Fatal("incorrect count")
		}
		return c
	}
	all := read(nil)
	seen := map[string]bool{}
	for _, x := range all.SourceTypes {
		if x.Category == "" || x.Status == "" || seen[x.Type] {
			t.Fatalf("invalid entry: %+v", x)
		}
		seen[x.Type] = true
	}
	if len(seen) != len(database.GetDriverRegistry().ListDrivers()) {
		t.Fatal("catalog differs from runtime registry")
	}
	for _, name := range []string{"postgresql", "mysql", "s3_csv", "s3_parquet", "s3_json"} {
		if !seen[name] {
			t.Fatalf("missing %s", name)
		}
	}
	filtered := read(map[string]any{"category": "object_storage", "format": "csv"})
	found := false
	for _, x := range filtered.SourceTypes {
		if x.Category != "object_storage" || x.Format != "csv" {
			t.Fatalf("bad filter %+v", x)
		}
		if x.Type == "s3_csv" {
			found = true
		}
	}
	if !found {
		t.Fatal("CSV filter omitted s3_csv")
	}
	empty := read(map[string]any{"format": "unknown"})
	if empty.Count != 0 {
		t.Fatal("unknown filter should return empty")
	}
	result, err := srv.handleListSourceTypes(context.Background(), callToolRequest("list_source_types", map[string]any{"format": 123}))
	if err != nil || !result.IsError {
		t.Fatal("invalid filter accepted")
	}
}
func TestCatalogDetailsAndDeltaFormat(t *testing.T) {
	srv := NewMCPServer(nil, nil, nil)
	invoke := func(args map[string]any) []map[string]any {
		t.Helper()
		res, err := srv.handleListSourceTypes(context.Background(), callToolRequest("list_source_types", args))
		if err != nil || res.IsError {
			t.Fatalf("tool error: %v", err)
		}
		var data struct{ Types []map[string]any }
		// Read the response through a map so the test uses the actual public keys.
		var payload map[string]json.RawMessage
		if err = json.Unmarshal([]byte(resultText(t, res)), &payload); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(payload["source_types"], &data.Types); err != nil {
			t.Fatal(err)
		}
		return data.Types
	}
	delta := invoke(map[string]any{"format": "delta"})
	names := map[string]bool{}
	for _, item := range delta {
		if item["format"] != "delta" {
			t.Fatalf("unexpected delta entry: %+v", item)
		}
		names[item["type"].(string)] = true
	}
	for _, name := range []string{"delta_lake", "s3_delta"} {
		if !names[name] {
			t.Fatalf("missing %s in delta results", name)
		}
	}
	got := invoke(map[string]any{"type": "s3_csv", "include_details": true})
	if len(got) != 1 {
		t.Fatalf("type filter produced %d entries", len(got))
	}
	var expected database.ConnectorSupport
	for _, item := range database.SupportCatalog(database.GetDriverRegistry()) {
		if item.Type == "s3_csv" {
			expected = item
			break
		}
	}
	if expected.Type == "" {
		t.Fatal("s3_csv not present in shared catalog")
	}
	for key, want := range map[string]any{"version_policy": expected.VersionPolicy, "limitations": expected.Limitations, "operations": expected.Operations, "authentication_disclosure": expected.AuthenticationDisclosure, "authentication_methods": expected.AuthenticationMethods, "compatibility_mode": expected.CompatibilityMode} {
		// Compare canonical JSON, including nil/empty distinctions.
		a, _ := json.Marshal(got[0][key])
		b, _ := json.Marshal(want)
		if string(a) != string(b) {
			t.Errorf("%s did not preserve catalog value: got %s, want %s", key, a, b)
		}
	}
}
func TestCatalogCompactByDefaultAndExplicitDetails(t *testing.T) {
	srv := NewMCPServer(nil, nil, nil)
	response := func(args map[string]any) string {
		t.Helper()
		res, err := srv.handleListSourceTypes(context.Background(), callToolRequest("list_source_types", args))
		if err != nil || res.IsError {
			t.Fatalf("catalog error: %v", err)
		}
		return resultText(t, res)
	}
	compact := response(nil)
	detailed := response(map[string]any{"include_details": true})
	if len(compact)*2 >= len(detailed) {
		t.Fatalf("compact response %d bytes is not less than half of detailed %d bytes", len(compact), len(detailed))
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(compact), &obj); err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	if err := json.Unmarshal(obj["source_types"], &items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		for _, key := range []string{"limitations", "operations", "authentication_disclosure", "version_policy"} {
			if _, found := item[key]; found {
				t.Fatalf("compact entry includes %s", key)
			}
		}
	}
	// The flag is optional and false must match omission exactly.
	if response(map[string]any{"include_details": false}) != compact {
		t.Fatal("explicit false differs from default")
	}
	bad, err := srv.handleListSourceTypes(context.Background(), callToolRequest("list_source_types", map[string]any{"include_details": "true"}))
	if err != nil || !bad.IsError {
		t.Fatal("string include_details accepted")
	}
	t.Logf("catalog sizes: compact=%d detailed=%d bytes", len(compact), len(detailed))
}
