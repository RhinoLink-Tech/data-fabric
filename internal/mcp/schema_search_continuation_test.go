package mcp

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

func searchPage(t *testing.T, server *MCPServer, caller *security.Caller, source, query string, extra map[string]any) schemaSearchResponse {
	t.Helper()
	ctx, request := schemaSearchRequest(caller, source, query, extra)
	result, err := server.handleSearchSchema(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	return schemaSearchResult(t, result)
}
func TestSchemaSearchContinuesPastScanLimitWithoutDeniedTables(t *testing.T) {
	source := activeOwnedSource()
	source.TenantID = "team"
	metadataSource := &schemaSearchMetadataStub{tables: map[string]*metadata.TableSchema{}}
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("table_%03d", i)
		metadataSource.identities = append(metadataSource.identities, metadata.TableIdentity{Name: name, Schema: "public", Type: "TABLE"})
		metadataSource.tables[name] = &metadata.TableSchema{Name: name, Schema: "public"}
	}
	metadataSource.identities = append(metadataSource.identities, metadata.TableIdentity{Name: "needle_orders", Schema: "public", Type: "TABLE"}, metadata.TableIdentity{Name: "needle_secret", Schema: "private", Type: "TABLE"})
	// Ensure the matching public table sorts after the first 100 permitted entries.
	metadataSource.identities[100].Name = "zz_needle_orders"
	policy := security.NewTableAuthorizer(security.TableAuthorizationConfig{Enabled: true, DataSources: map[string]security.DataSourceTablePolicy{source.ID: {Rules: []security.TableAccessRule{{UserID: "alice", Metadata: []security.TableScope{{Schema: "public", Table: "*"}}}}}}})
	server := NewMCPServerWithTableAuthorizer(nil, describeTableRepo{source: source}, metadataSource, policy)
	caller := &security.Caller{UserID: "alice", TenantID: "team"}
	first := searchPage(t, server, caller, source.ID, "needle", nil)
	if len(first.Matches) != 0 || !first.Truncated || first.NextCursor == "" || len(metadataSource.calls) != 100 {
		t.Fatalf("first page %+v calls %d", first, len(metadataSource.calls))
	}
	second := searchPage(t, server, caller, source.ID, "needle", map[string]any{"cursor": first.NextCursor})
	if len(second.Matches) != 1 || second.Matches[0].Table != "zz_needle_orders" || second.NextCursor != "" || second.Truncated {
		t.Fatalf("second page %+v", second)
	}
	raw, _ := json.Marshal(second)
	if strings.Contains(string(raw), "needle_secret") {
		t.Fatal("denied table leaked")
	}
}

func TestSchemaSearchContinuationBindsInputsAndInventory(t *testing.T) {
	source := activeOwnedSource()
	source.TenantID = "team"
	meta := &schemaSearchMetadataStub{identities: []metadata.TableIdentity{{Name: "orders_1", Schema: "public"}, {Name: "orders_2", Schema: "public"}, {Name: "orders_3", Schema: "public"}}}
	server := NewMCPServer(nil, describeTableRepo{source: source}, meta)
	caller := &security.Caller{UserID: "alice", TenantID: "team"}
	first := searchPage(t, server, caller, source.ID, "orders", map[string]any{"limit": float64(1)})
	if len(first.Matches) != 1 || first.NextCursor == "" {
		t.Fatalf("first page %+v", first)
	}
	second := searchPage(t, server, caller, source.ID, "orders", map[string]any{"limit": float64(1), "cursor": first.NextCursor})
	third := searchPage(t, server, caller, source.ID, "orders", map[string]any{"limit": float64(1), "cursor": second.NextCursor})
	if first.Matches[0].Table != "orders_1" || second.Matches[0].Table != "orders_2" || third.Matches[0].Table != "orders_3" || third.NextCursor != "" {
		t.Fatalf("unexpected pages %+v %+v %+v", first, second, third)
	}
	for _, tc := range []struct {
		caller *security.Caller
		query  string
		args   map[string]any
	}{
		{caller, "orderx", map[string]any{"limit": float64(1), "cursor": first.NextCursor}},
		{caller, "orders", map[string]any{"limit": float64(2), "cursor": first.NextCursor}},
		{&security.Caller{UserID: "bob", TenantID: "team"}, "orders", map[string]any{"limit": float64(1), "cursor": first.NextCursor}},
		{caller, "orders", map[string]any{"limit": float64(1), "cursor": first.NextCursor + "x"}},
	} {
		ctx, req := schemaSearchRequest(tc.caller, source.ID, tc.query, tc.args)
		result, err := server.handleSearchSchema(ctx, req)
		if err != nil || !result.IsError {
			t.Fatalf("accepted changed input: %+v %v", result, err)
		}
	}
	meta.identities = append(meta.identities, metadata.TableIdentity{Name: "orders_4", Schema: "public"})
	ctx, req := schemaSearchRequest(caller, source.ID, "orders", map[string]any{"limit": float64(1), "cursor": first.NextCursor})
	result, err := server.handleSearchSchema(ctx, req)
	if err != nil || !result.IsError {
		t.Fatal("accepted changed table inventory")
	}
}

func TestSchemaSearchContinuationRejectsExpiryAndRestart(t *testing.T) {
	source := activeOwnedSource()
	caller := &security.Caller{UserID: "alice"}
	meta := &schemaSearchMetadataStub{identities: []metadata.TableIdentity{{Name: "orders_1"}, {Name: "orders_2"}}}
	server := NewMCPServer(nil, describeTableRepo{source: source}, meta)
	first := searchPage(t, server, caller, source.ID, "orders", map[string]any{"limit": float64(1)})
	other := NewMCPServer(nil, describeTableRepo{source: source}, meta)
	ctx, req := schemaSearchRequest(caller, source.ID, "orders", map[string]any{"limit": float64(1), "cursor": first.NextCursor})
	result, err := other.handleSearchSchema(ctx, req)
	if err != nil || !result.IsError {
		t.Fatal("accepted cursor across restart")
	}
	raw, err := base64.RawURLEncoding.DecodeString(first.NextCursor)
	if err != nil {
		t.Fatal(err)
	}
	var cursor schemaSearchCursor
	if err = json.Unmarshal(raw, &cursor); err != nil {
		t.Fatal(err)
	}
	cursor.Expires = time.Now().Add(-time.Minute).Unix()
	raw, _ = json.Marshal(cursor)
	ctx, req = schemaSearchRequest(caller, source.ID, "orders", map[string]any{"limit": float64(1), "cursor": base64.RawURLEncoding.EncodeToString(raw)})
	result, err = server.handleSearchSchema(ctx, req)
	if err != nil || !result.IsError {
		t.Fatal("accepted expired cursor")
	}
}
