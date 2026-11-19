//go:build datafabric_core

package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCoreMCPSourceCatalogMatchesBuild(t *testing.T) {
	server := NewMCPServer(nil, nil, &stubTableDescriber{})
	result, err := server.handleListSourceTypes(context.Background(), callToolRequest("list_source_types", nil))
	if err != nil || result.IsError {
		t.Fatalf("catalog result %+v error %v", result, err)
	}
	var body struct {
		SourceTypes []struct {
			Type string `json:"type"`
		} `json:"source_types"`
	}
	if err = json.Unmarshal([]byte(resultText(t, result)), &body); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"mysql": true, "mariadb": true, "postgresql": true, "trino": true}
	if len(body.SourceTypes) != len(allowed) {
		t.Fatalf("unexpected catalog: %s", resultText(t, result))
	}
	for _, entry := range body.SourceTypes {
		if !allowed[entry.Type] {
			t.Fatalf("unexpected type %q", entry.Type)
		}
	}
	for _, name := range []string{"list_data_sources", "execute_sql_query", "fetch_next_batch", "translate_sql", "list_supported_dialects"} {
		if _, ok := server.server.ListTools()[name]; !ok {
			t.Fatalf("missing MCP tool %s", name)
		}
	}
}
