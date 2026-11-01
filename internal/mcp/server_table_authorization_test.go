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

func TestListObjectCSVTablesWithAndWithoutTablePolicy(t *testing.T) {
	source := activeOwnedSource()
	source.Type = model.DatabaseTypeMinIOCSV
	metadataSource := &policyTableMetadataStub{identities: []metadata.TableIdentity{{Name: "records", Type: "TABLE"}}}
	for _, tc := range []struct {
		name      string
		policy    *security.TableAuthorizer
		wantCount int
	}{
		{name: "policy disabled", wantCount: 1},
		{name: "metadata allowed", policy: security.NewTableAuthorizer(security.TableAuthorizationConfig{
			Enabled: true, DataSources: map[string]security.DataSourceTablePolicy{
				source.ID: {Rules: []security.TableAccessRule{{UserID: "alice", Metadata: []security.TableScope{{Schema: "*", Table: "records"}}}}},
			},
		}), wantCount: 1},
		{name: "metadata denied", policy: security.NewTableAuthorizer(security.TableAuthorizationConfig{Enabled: true}), wantCount: 0},
	} {
		for _, dbType := range []model.DatabaseType{model.DatabaseTypeMinIOCSV, model.DatabaseTypeS3CSV} {
			t.Run(string(dbType)+"/"+tc.name, func(t *testing.T) {
				source.Type = dbType
				server := NewMCPServerWithTableAuthorizer(nil, describeTableRepo{source: source}, metadataSource, tc.policy)
				ctx, request := tablePolicyRequest(&security.Caller{UserID: "alice"}, "list_tables", map[string]any{"datasource_id": source.ID})
				result, err := server.handleListTables(ctx, request)
				if err != nil || result.IsError {
					t.Fatalf("list tables result=%+v, error=%v", result, err)
				}
				var payload model.FetchQueryResponse
				if err := json.Unmarshal([]byte(describeTableText(t, result)), &payload); err != nil {
					t.Fatalf("decode list tables: %v", err)
				}
				if payload.TotalCount != tc.wantCount || len(payload.Entries) != tc.wantCount {
					t.Fatalf("list tables = %+v, want %d entries", payload, tc.wantCount)
				}
				if tc.wantCount > 0 && (*payload.Entries[0])[0] != "records" {
					t.Fatalf("unexpected table name: %+v", payload.Entries)
				}
			})
		}
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

func TestDescribeTableHidesRestrictedColumnsAndRelatedMetadata(t *testing.T) {
	source := activeOwnedSource()
	metadataSource := &stubTableDescriber{table: &metadata.TableSchema{
		Name: "orders", Schema: "public",
		Columns:     []metadata.ColumnSchema{{Name: "id", Type: "integer"}, {Name: "customer_email", Type: "text"}, {Name: "total", Type: "numeric"}},
		PrimaryKey:  []string{"id"},
		Indexes:     []metadata.IndexSchema{{Name: "orders_email_idx", Columns: []string{"customer_email"}}, {Name: "orders_id_idx", Columns: []string{"id"}}},
		ForeignKeys: []metadata.ForeignKeySchema{{Name: "orders_customer_fk", Columns: []string{"customer_email"}, RefTable: "customers", RefColumns: []string{"email"}}},
		Properties:  map[string]interface{}{"partition_column": "customer_email"},
	}}
	authorizer := security.NewTableAuthorizer(security.TableAuthorizationConfig{Enabled: true, DataSources: map[string]security.DataSourceTablePolicy{
		source.ID: {Rules: []security.TableAccessRule{{UserID: "alice", Metadata: []security.TableScope{{Schema: "public", Table: "orders", Columns: []string{"id", "total"}}}}}},
	}})
	server := NewMCPServerWithTableAuthorizer(nil, describeTableRepo{source: source}, metadataSource, authorizer)
	ctx, request := tablePolicyRequest(&security.Caller{UserID: "alice"}, "describe_table", map[string]any{
		"datasource_id": source.ID, "table_name": "orders", "schema": "public",
	})
	result, err := server.handleDescribeTable(ctx, request)
	if err != nil || result.IsError {
		t.Fatalf("describe result=%+v, error=%v", result, err)
	}
	var payload struct {
		Table metadata.TableSchema `json:"table"`
	}
	if err := json.Unmarshal([]byte(describeTableText(t, result)), &payload); err != nil {
		t.Fatalf("decode description: %v", err)
	}
	if len(payload.Table.Columns) != 2 || payload.Table.Columns[0].Name != "id" || payload.Table.Columns[1].Name != "total" {
		t.Fatalf("visible columns = %+v", payload.Table.Columns)
	}
	if len(payload.Table.Indexes) != 1 || payload.Table.Indexes[0].Name != "" || len(payload.Table.Indexes[0].Columns) != 1 || payload.Table.Indexes[0].Columns[0] != "id" {
		t.Fatalf("visible indexes = %+v", payload.Table.Indexes)
	}
	if len(payload.Table.PrimaryKey) != 1 || payload.Table.PrimaryKey[0] != "id" || len(payload.Table.ForeignKeys) != 0 || payload.Table.Properties != nil {
		t.Fatalf("restricted metadata leaked through keys/properties: %+v", payload.Table)
	}
	body := describeTableText(t, result)
	if strings.Contains(body, "customer_email") || strings.Contains(body, "orders_email_idx") || strings.Contains(body, "customers") || strings.Contains(body, "orders_id_idx") {
		t.Fatalf("restricted metadata leaked in response: %s", body)
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
