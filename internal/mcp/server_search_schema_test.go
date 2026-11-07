package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

type schemaSearchMetadataStub struct {
	identities []metadata.TableIdentity
	tables     map[string]*metadata.TableSchema
	calls      []string
	listErr    error
	schemaErr  error
}

func (s *schemaSearchMetadataStub) ListTables(context.Context, string) ([]metadata.TableIdentity, error) {
	return s.identities, s.listErr
}

func (s *schemaSearchMetadataStub) ExtractTableSchema(_ context.Context, sourceID, tableName string) (*metadata.TableSchema, error) {
	s.calls = append(s.calls, sourceID+":"+tableName)
	if s.schemaErr != nil {
		return nil, s.schemaErr
	}
	table, ok := s.tables[tableName]
	if !ok {
		return nil, metadata.ErrTableNotFound
	}
	return table, nil
}

func (s *schemaSearchMetadataStub) ExtractTableSchemaInSchema(_ context.Context, sourceID, schemaName, tableName string) (*metadata.TableSchema, error) {
	s.calls = append(s.calls, sourceID+":"+schemaName+":"+tableName)
	if s.schemaErr != nil {
		return nil, s.schemaErr
	}
	table, ok := s.tables[tableName]
	if !ok {
		return nil, metadata.ErrTableNotFound
	}
	return table, nil
}

func schemaSearchRequest(caller *security.Caller, sourceID, query string, extra map[string]any) (context.Context, mcp.CallToolRequest) {
	args := map[string]any{"datasource_id": sourceID, "query": query}
	for name, value := range extra {
		args[name] = value
	}
	return tablePolicyRequest(caller, "search_schema", args)
}

func schemaSearchResult(t *testing.T, result *mcp.CallToolResult) schemaSearchResponse {
	t.Helper()
	if result == nil || result.IsError {
		t.Fatalf("schema search returned tool error: %+v", result)
	}
	var response schemaSearchResponse
	if err := json.Unmarshal([]byte(describeTableText(t, result)), &response); err != nil {
		t.Fatalf("decode schema search result: %v", err)
	}
	return response
}

func TestSearchSchemaFindsColumnCommentsWithBoundedStableResults(t *testing.T) {
	source := activeOwnedSource()
	source.Config.Schema = "public"
	metadataSource := &schemaSearchMetadataStub{
		identities: []metadata.TableIdentity{
			{Name: "orders", Schema: "public", Type: "TABLE"},
			{Name: "order_items", Schema: "public", Type: "TABLE"},
		},
		tables: map[string]*metadata.TableSchema{
			"orders":      {Name: "orders", Schema: "public", Columns: []metadata.ColumnSchema{{Name: "amount", Comment: "Billing amount"}}},
			"order_items": {Name: "order_items", Schema: "public", Columns: []metadata.ColumnSchema{{Name: "order_id", Comment: "Billing reference"}}},
		},
	}
	server := NewMCPServer(nil, describeTableRepo{source: source}, metadataSource)
	ctx, request := schemaSearchRequest(&security.Caller{UserID: "alice"}, source.ID, "BILLING", nil)

	result, err := server.handleSearchSchema(ctx, request)
	if err != nil {
		t.Fatalf("handleSearchSchema() error = %v", err)
	}
	body := describeTableText(t, result)
	response := schemaSearchResult(t, result)
	if response.DataSourceID != source.ID || response.Query != "BILLING" || len(response.Matches) != 2 ||
		!response.Ambiguous || response.Truncated {
		t.Fatalf("search response = %+v", response)
	}
	if response.Matches[0].Table != "order_items" || response.Matches[1].Table != "orders" {
		t.Fatalf("matches are not in stable schema/table order: %+v", response.Matches)
	}
	for _, match := range response.Matches {
		if match.Schema != "public" || len(match.MatchedFields) != 1 || match.MatchedFields[0] != "column_comment" ||
			len(match.Columns) != 1 {
			t.Fatalf("match identity/fields = %+v", match)
		}
	}
	if strings.Contains(body, "Billing amount") || strings.Contains(body, "Billing reference") {
		t.Fatalf("comment text was returned instead of only match metadata: %s", body)
	}
}

func TestSearchSchemaFiltersTablesAndHiddenColumnComments(t *testing.T) {
	source := activeOwnedSource()
	source.Config.Schema = "public"
	metadataSource := &schemaSearchMetadataStub{
		identities: []metadata.TableIdentity{
			{Name: "orders", Schema: "public", Type: "TABLE"},
			{Name: "payroll", Schema: "private", Type: "TABLE"},
		},
		tables: map[string]*metadata.TableSchema{
			"orders": {
				Name: "orders", Schema: "public",
				Columns: []metadata.ColumnSchema{
					{Name: "id", Comment: "Public order identifier"},
					{Name: "internal_note", Comment: "Secret payroll notes"},
				},
			},
			"payroll": {Name: "payroll", Schema: "private", Columns: []metadata.ColumnSchema{{Name: "salary", Comment: "Secret payroll notes"}}},
		},
	}
	authorizer := security.NewTableAuthorizer(security.TableAuthorizationConfig{
		Enabled: true,
		DataSources: map[string]security.DataSourceTablePolicy{
			source.ID: {Rules: []security.TableAccessRule{{
				UserID: "alice",
				Metadata: []security.TableScope{{
					Schema: "public", Table: "orders", Columns: []string{"id"},
				}},
			}}},
		},
	})
	server := NewMCPServerWithTableAuthorizer(nil, describeTableRepo{source: source}, metadataSource, authorizer)

	ctx, request := schemaSearchRequest(&security.Caller{UserID: "alice"}, source.ID, "secret", nil)
	result, err := server.handleSearchSchema(ctx, request)
	if err != nil {
		t.Fatalf("handleSearchSchema() error = %v", err)
	}
	response := schemaSearchResult(t, result)
	body := describeTableText(t, result)
	if len(response.Matches) != 0 || response.Ambiguous || strings.Contains(strings.ToLower(body), "payroll") ||
		strings.Contains(strings.ToLower(body), "internal_note") || strings.Contains(strings.ToLower(body), "salary") {
		t.Fatalf("restricted metadata leaked: %+v %s", response, body)
	}
	if len(metadataSource.calls) != 1 || metadataSource.calls[0] != source.ID+":public:orders" {
		t.Fatalf("metadata reads = %v, want only the permitted table", metadataSource.calls)
	}

	ctx, request = schemaSearchRequest(&security.Caller{UserID: "alice"}, source.ID, "identifier", nil)
	result, err = server.handleSearchSchema(ctx, request)
	if err != nil {
		t.Fatalf("handleSearchSchema() visible-column query error = %v", err)
	}
	response = schemaSearchResult(t, result)
	if len(response.Matches) != 1 || response.Matches[0].Table != "orders" ||
		len(response.Matches[0].Columns) != 1 || response.Matches[0].Columns[0] != "id" {
		t.Fatalf("visible column search = %+v", response)
	}
}

func TestSearchSchemaBoundsResultCountAndFlagsTruncation(t *testing.T) {
	source := activeOwnedSource()
	identities := make([]metadata.TableIdentity, 105)
	for i := range identities {
		identities[i] = metadata.TableIdentity{Name: fmt.Sprintf("table_%03d", i), Schema: "public", Type: "TABLE"}
	}
	metadataSource := &schemaSearchMetadataStub{identities: identities}
	server := NewMCPServer(nil, describeTableRepo{source: source}, metadataSource)
	ctx, request := schemaSearchRequest(
		&security.Caller{UserID: "alice"}, source.ID, "table", map[string]any{"limit": float64(2)},
	)

	result, err := server.handleSearchSchema(ctx, request)
	if err != nil {
		t.Fatalf("handleSearchSchema() error = %v", err)
	}
	response := schemaSearchResult(t, result)
	if len(response.Matches) != 2 || !response.Truncated || !response.Ambiguous ||
		response.Matches[0].Table != "table_000" || response.Matches[1].Table != "table_001" {
		t.Fatalf("bounded response = %+v", response)
	}
	if len(metadataSource.calls) != 0 {
		t.Fatalf("name search unexpectedly read table schemas: %v", metadataSource.calls)
	}
}

func TestSearchSchemaRejectsInvalidBoundsAndUnsupportedMetadata(t *testing.T) {
	source := activeOwnedSource()
	server := NewMCPServer(nil, describeTableRepo{source: source}, &stubTableDescriber{})
	for _, tc := range []struct {
		name  string
		query string
		extra map[string]any
		want  string
	}{
		{name: "empty query", query: " ", want: "query must contain"},
		{name: "query too short", query: "a", want: "query must contain"},
		{name: "limit too large", query: "orders", extra: map[string]any{"limit": float64(21)}, want: "limit must be between"},
		{name: "no table lister", query: "orders", want: "not supported for this server configuration"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, request := schemaSearchRequest(&security.Caller{UserID: "alice"}, source.ID, tc.query, tc.extra)
			result, err := server.handleSearchSchema(ctx, request)
			if err != nil || result == nil || !result.IsError {
				t.Fatalf("result=%+v error=%v, want tool error", result, err)
			}
			body := describeTableText(t, result)
			if !strings.Contains(body, tc.want) {
				t.Fatalf("error = %q, want %q", body, tc.want)
			}
		})
	}

	unsupported := &schemaSearchMetadataStub{identities: []metadata.TableIdentity{{Name: "orders"}}, listErr: metadata.ErrSchemaDiscoveryUnsupported}
	server = NewMCPServer(nil, describeTableRepo{source: source}, unsupported)
	ctx, request := schemaSearchRequest(&security.Caller{UserID: "alice"}, source.ID, "orders", nil)
	result, err := server.handleSearchSchema(ctx, request)
	if err != nil || result == nil || !result.IsError ||
		!strings.Contains(describeTableText(t, result), "schema search is not supported for data source type") {
		t.Fatalf("unsupported metadata result=%+v error=%v", result, err)
	}
}

var _ repository.DataSourceRepository = describeTableRepo{}
var _ TableLister = (*schemaSearchMetadataStub)(nil)
var _ TableDescriber = (*schemaSearchMetadataStub)(nil)
