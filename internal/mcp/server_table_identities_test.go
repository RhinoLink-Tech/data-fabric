package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/mark3labs/mcp-go/mcp"
)

func TestListTablesPreservesOnlyAuthorizedSchemaIdentities(t *testing.T) {
	source := activeOwnedSource()
	metadataSource := &policyTableMetadataStub{identities: []metadata.TableIdentity{
		{Name: "orders", Schema: "sales", Type: "TABLE"},
		{Name: "orders", Schema: "archive", Type: "VIEW"},
		{Name: "orders", Schema: "private", Type: "TABLE"},
	}}
	policy := security.NewTableAuthorizer(security.TableAuthorizationConfig{
		Enabled: true, DataSources: map[string]security.DataSourceTablePolicy{
			source.ID: {Rules: []security.TableAccessRule{{UserID: "alice", Metadata: []security.TableScope{{Schema: "sales", Table: "orders"}, {Schema: "archive", Table: "orders"}}}}},
		},
	})
	server := NewMCPServerWithTableAuthorizer(nil, describeTableRepo{source: source}, metadataSource, policy)
	caller := &security.Caller{UserID: "alice"}
	ctx, request := tablePolicyRequest(caller, "list_tables", map[string]any{"datasource_id": source.ID})
	result, err := server.handleListTables(ctx, request)
	if err != nil || result.IsError {
		t.Fatalf("list failed: %v", err)
	}
	body := result.Content[0].(mcp.TextContent).Text
	var payload struct {
		model.FetchQueryResponse
		Identities []metadata.TableIdentity `json:"table_identities"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.TotalCount != 2 || len(payload.Entries) != 2 || len(payload.Identities) != 2 ||
		(*payload.Entries[0])[0] != "orders" || payload.Identities[0].Schema != "sales" ||
		payload.Identities[1].Schema != "archive" || payload.Identities[1].Type != "VIEW" || strings.Contains(body, "private") {
		t.Fatalf("unexpected filtered metadata: %s", body)
	}
	ctx, describe := tablePolicyRequest(caller, "describe_table", map[string]any{
		"datasource_id": source.ID, "table_name": payload.Identities[0].Name, "schema": payload.Identities[0].Schema,
	})
	description, err := server.handleDescribeTable(ctx, describe)
	if err != nil || description.IsError || metadataSource.gotSchema != "sales" || metadataSource.gotTable != "orders" {
		t.Fatalf("selected identity was not described correctly: %v", err)
	}
}

func TestListTablesPreservesSchemaLessObjectIdentity(t *testing.T) {
	source := activeOwnedSource()
	source.Type = model.DatabaseTypeMinIOCSV
	metadataSource := &policyTableMetadataStub{identities: []metadata.TableIdentity{{Name: "records", Type: "TABLE"}}}
	server := NewMCPServerWithTableAuthorizer(nil, describeTableRepo{source: source}, metadataSource, nil)
	ctx, request := tablePolicyRequest(&security.Caller{UserID: "alice"}, "list_tables", map[string]any{"datasource_id": source.ID})
	result, err := server.handleListTables(ctx, request)
	if err != nil || result.IsError {
		t.Fatalf("list failed: %v", err)
	}
	body := result.Content[0].(mcp.TextContent).Text
	var payload struct {
		Identities []metadata.TableIdentity `json:"table_identities"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Identities) != 1 || payload.Identities[0].Name != "records" || payload.Identities[0].Schema != "" {
		t.Fatalf("unexpected object identity: %s", body)
	}
}
