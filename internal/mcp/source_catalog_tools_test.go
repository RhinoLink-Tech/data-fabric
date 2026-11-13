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
	got := invoke(map[string]any{"type": "s3_csv"})
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
