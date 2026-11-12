package mcp

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

func TestListTablesUsesConfiguredPostgresSchema(t *testing.T) {
	tests := []struct{ name, schema, want string }{
		{"configured", "sales", "sales"},
		{"default", "", "public"},
		{"quoted schema stays a bound value", "Sales' OR true --", "Sales' OR true --"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			source := activeOwnedSource()
			source.Config.Schema = tc.schema
			query := &stubQueryService{}
			server := NewMCPServer(query, describeTableRepo{source: source}, &stubTableDescriber{})
			ctx, request := tablePolicyRequest(&security.Caller{UserID: "alice"}, "list_tables", map[string]any{"datasource_id": source.ID})
			result, err := server.handleListTables(ctx, request)
			if err != nil || result.IsError {
				t.Fatalf("list result = %+v, err = %v", result, err)
			}
			if query.captured == nil {
				t.Fatal("missing query")
			}
			if query.captured.SQL != "SELECT table_name FROM information_schema.tables WHERE table_schema = $1" {
				t.Fatalf("unexpected SQL %q", query.captured.SQL)
			}
			if !reflect.DeepEqual(query.captured.Parameters, []interface{}{tc.want}) {
				t.Fatalf("parameters = %#v, want schema %q", query.captured.Parameters, tc.want)
			}
		})
	}
}

func TestListTablesKeepsMySQLDiscoveryUnchanged(t *testing.T) {
	source := activeOwnedSource()
	source.Type = model.DatabaseTypeMySQL
	query := &stubQueryService{}
	server := NewMCPServer(query, describeTableRepo{source: source}, &stubTableDescriber{})
	ctx, request := tablePolicyRequest(&security.Caller{UserID: "alice"}, "list_tables", map[string]any{"datasource_id": source.ID})
	result, err := server.handleListTables(ctx, request)
	if err != nil || result.IsError {
		t.Fatalf("list result = %+v, err = %v", result, err)
	}
	if query.captured.SQL != "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()" || len(query.captured.Parameters) != 0 {
		t.Fatalf("unexpected query: %+v", query.captured)
	}
}

func TestListTablesPostgresPolicyStillFiltersIdentities(t *testing.T) {
	source := activeOwnedSource()
	source.Config.Schema = "sales"
	identities := &policyTableMetadataStub{identities: []metadata.TableIdentity{
		{Name: "orders", Schema: "public", Type: "TABLE"},
		{Name: "sales_orders", Schema: "sales", Type: "TABLE"},
	}}
	server := tablePolicyServer(source, identities)
	ctx, req := tablePolicyRequest(&security.Caller{UserID: "alice"}, "list_tables", map[string]any{"datasource_id": source.ID})
	result, err := server.handleListTables(ctx, req)
	if err != nil || result.IsError {
		t.Fatalf("list result = %+v, err = %v", result, err)
	}
	var response model.FetchQueryResponse
	if err := json.Unmarshal([]byte(describeTableText(t, result)), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Entries) != 1 || (*response.Entries[0])[0] != "orders" {
		t.Fatalf("policy-filtered identities = %+v", response.Entries)
	}
}
