package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

type policyTableMetadataStub struct {
	identities []metadata.TableIdentity
	calls      int
	gotSchema  string
	gotTable   string
}

func (s *policyTableMetadataStub) ListTables(context.Context, string) ([]metadata.TableIdentity, error) {
	return s.identities, nil
}

func (s *policyTableMetadataStub) ExtractTableSchema(_ context.Context, _, tableName string) (*metadata.TableSchema, error) {
	s.calls++
	s.gotTable = tableName
	return &metadata.TableSchema{Name: tableName}, nil
}

func (s *policyTableMetadataStub) ExtractTableSchemaInSchema(_ context.Context, _, schemaName, tableName string) (*metadata.TableSchema, error) {
	s.calls++
	s.gotSchema = schemaName
	s.gotTable = tableName
	return &metadata.TableSchema{Name: tableName, Schema: schemaName}, nil
}

func tablePolicyServer(source *model.DataSource, metadataSource *policyTableMetadataStub) *MCPServer {
	policy := security.NewTableAuthorizer(security.TableAuthorizationConfig{
		Enabled: true,
		DataSources: map[string]security.DataSourceTablePolicy{
			source.ID: {Rules: []security.TableAccessRule{{
				UserID:   "alice",
				Metadata: []security.TableScope{{Schema: "public", Table: "orders"}},
			}}},
		},
	})
	return NewMCPServerWithTableAuthorizer(nil, describeTableRepo{source: source}, metadataSource, policy)
}

func tablePolicyRequest(caller *security.Caller, tool string, args map[string]any) (context.Context, mcp.CallToolRequest) {
	request := mcp.CallToolRequest{}
	request.Params.Name = tool
	request.Params.Arguments = args
	return context.WithValue(context.Background(), callerContextKey{}, caller), request
}

func TestListTablesHidesUnauthorizedNames(t *testing.T) {
	source := activeOwnedSource()
	metadataSource := &policyTableMetadataStub{identities: []metadata.TableIdentity{
		{Name: "orders", Schema: "public", Type: "TABLE"},
		{Name: "payroll", Schema: "private", Type: "TABLE"},
	}}
	server := tablePolicyServer(source, metadataSource)

	ctx, request := tablePolicyRequest(&security.Caller{UserID: "alice"}, "list_tables", map[string]any{"datasource_id": source.ID})
	result, err := server.handleListTables(ctx, request)
	if err != nil {
		t.Fatalf("handleListTables() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", result.Content[0].(mcp.TextContent).Text)
	}
	body := result.Content[0].(mcp.TextContent).Text
	if !strings.Contains(body, "orders") || strings.Contains(body, "payroll") {
		t.Fatalf("visible table list = %s, want orders only", body)
	}
	var payload model.FetchQueryResponse
	if err := json.Unmarshal([]byte(body), &payload); err != nil || len(payload.Entries) != 1 || (*payload.Entries[0])[0] != "orders" || payload.TotalCount != 1 {
		t.Fatalf("filtered table payload = %+v, decode error = %v", payload, err)
	}
}

func TestDescribeTableAuthorizesBeforeReadingSchema(t *testing.T) {
	source := activeOwnedSource()
	metadataSource := &policyTableMetadataStub{}
	server := tablePolicyServer(source, metadataSource)
	caller := &security.Caller{UserID: "alice"}

	ctx, deniedRequest := tablePolicyRequest(caller, "describe_table", map[string]any{
		"datasource_id": source.ID, "table_name": "payroll", "schema": "private",
	})
	denied, err := server.handleDescribeTable(ctx, deniedRequest)
	if err != nil {
		t.Fatalf("denied handleDescribeTable() error = %v", err)
	}
	if !denied.IsError || metadataSource.calls != 0 {
		t.Fatalf("denied description result error=%v metadata calls=%d; want denial before extraction", denied.IsError, metadataSource.calls)
	}

	ctx, allowedRequest := tablePolicyRequest(caller, "describe_table", map[string]any{
		"datasource_id": source.ID, "table_name": "orders", "schema": "public",
	})
	allowed, err := server.handleDescribeTable(ctx, allowedRequest)
	if err != nil {
		t.Fatalf("allowed handleDescribeTable() error = %v", err)
	}
	if allowed.IsError || metadataSource.calls != 1 || metadataSource.gotSchema != "public" || metadataSource.gotTable != "orders" {
		t.Fatalf("allowed result error=%v calls=%d schema=%q table=%q", allowed.IsError, metadataSource.calls, metadataSource.gotSchema, metadataSource.gotTable)
	}
}

func TestListTablesAuthorizesConfiguredTrinoCatalog(t *testing.T) {
	source := &model.DataSource{
		ID: "trino-source", OwnerID: "alice", Status: model.DataSourceStatusActive,
		Type:   model.DatabaseTypeTrino,
		Config: model.DataSourceConfig{Database: "lake", Schema: "analytics"},
	}
	metadataSource := &policyTableMetadataStub{identities: []metadata.TableIdentity{
		{Name: "orders", Schema: "analytics", Type: "TABLE"},
	}}
	authorizer := security.NewTableAuthorizer(security.TableAuthorizationConfig{
		Enabled: true,
		DataSources: map[string]security.DataSourceTablePolicy{
			source.ID: {Rules: []security.TableAccessRule{{
				UserID:   "alice",
				Metadata: []security.TableScope{{Catalog: "lake", Schema: "analytics", Table: "orders"}},
			}}},
		},
	})
	server := NewMCPServerWithTableAuthorizer(nil, describeTableRepo{source: source}, metadataSource, authorizer)
	ctx, request := tablePolicyRequest(&security.Caller{UserID: "alice"}, "list_tables", map[string]any{"datasource_id": source.ID})

	result, err := server.handleListTables(ctx, request)
	if err != nil {
		t.Fatalf("handleListTables() error = %v", err)
	}
	if result.IsError || !strings.Contains(result.Content[0].(mcp.TextContent).Text, "orders") {
		t.Fatalf("configured default catalog table was not listed: %+v", result)
	}
}
