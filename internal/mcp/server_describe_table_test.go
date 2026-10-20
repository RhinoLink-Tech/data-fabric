package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

type describeTableRepo struct {
	repository.DataSourceRepository
	source *model.DataSource
	err    error
}

func (r describeTableRepo) GetByID(context.Context, string) (*model.DataSource, error) {
	return r.source, r.err
}

type stubTableDescriber struct {
	table       *metadata.TableSchema
	err         error
	calls       int
	gotSourceID string
	gotTable    string
}

func (s *stubTableDescriber) ExtractTableSchema(_ context.Context, dataSourceID, tableName string) (*metadata.TableSchema, error) {
	s.calls++
	s.gotSourceID = dataSourceID
	s.gotTable = tableName
	return s.table, s.err
}

func describeTableServer(source *model.DataSource, describer TableDescriber) *MCPServer {
	return NewMCPServer(nil, describeTableRepo{source: source}, describer)
}

func describeTableRequest(t *testing.T, caller *security.Caller, dataSourceID, tableName string) (context.Context, mcp.CallToolRequest) {
	t.Helper()

	request := mcp.CallToolRequest{}
	request.Params.Name = "describe_table"
	request.Params.Arguments = map[string]any{
		"datasource_id": dataSourceID,
		"table_name":    tableName,
	}
	return context.WithValue(context.Background(), callerContextKey{}, caller), request
}

func describeTableText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()

	if len(result.Content) != 1 {
		t.Fatalf("content items = %d, want 1", len(result.Content))
	}
	content, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Fatalf("content type = %T, want mcp.TextContent", result.Content[0])
	}
	return content.Text
}

func activeOwnedSource() *model.DataSource {
	return &model.DataSource{
		ID:      "ds-1",
		OwnerID: "alice",
		Status:  model.DataSourceStatusActive,
		Type:    model.DatabaseTypePostgreSQL,
	}
}

// The owner of an active source gets the table schema with the data source
// and table identified in the result (#184).
func TestDescribeTableReturnsSchemaForOwner(t *testing.T) {
	describer := &stubTableDescriber{table: &metadata.TableSchema{
		Name:   "orders",
		Schema: "public",
		Type:   "TABLE",
		Columns: []metadata.ColumnSchema{
			{Name: "id", Type: "integer", Nullable: false},
			{Name: "amount", Type: "numeric", Nullable: true},
		},
		PrimaryKey: []string{"id"},
	}}
	server := describeTableServer(activeOwnedSource(), describer)

	ctx, request := describeTableRequest(t, &security.Caller{UserID: "alice"}, "ds-1", "orders")
	result, err := server.handleDescribeTable(ctx, request)
	if err != nil {
		t.Fatalf("handleDescribeTable() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %s", describeTableText(t, result))
	}

	var payload struct {
		DataSourceID string `json:"datasource_id"`
		Table        struct {
			Name    string `json:"name"`
			Schema  string `json:"schema"`
			Columns []struct {
				Name     string `json:"name"`
				Type     string `json:"type"`
				Nullable bool   `json:"nullable"`
			} `json:"columns"`
			PrimaryKey []string `json:"primaryKey"`
		} `json:"table"`
	}
	if err := json.Unmarshal([]byte(describeTableText(t, result)), &payload); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}
	if payload.DataSourceID != "ds-1" || payload.Table.Name != "orders" || payload.Table.Schema != "public" {
		t.Fatalf("identity = %s/%s/%s, want ds-1/orders/public", payload.DataSourceID, payload.Table.Name, payload.Table.Schema)
	}
	if len(payload.Table.Columns) != 2 || payload.Table.Columns[1].Nullable != true {
		t.Fatalf("columns = %+v, want two columns with the second nullable", payload.Table.Columns)
	}
	if len(payload.Table.PrimaryKey) != 1 || payload.Table.PrimaryKey[0] != "id" {
		t.Fatalf("primaryKey = %+v, want [id]", payload.Table.PrimaryKey)
	}
	if describer.calls != 1 || describer.gotSourceID != "ds-1" || describer.gotTable != "orders" {
		t.Fatalf("describer called %d times with %s/%s", describer.calls, describer.gotSourceID, describer.gotTable)
	}
}

// A missing table is reported clearly and never answered with a guessed
// schema (#184).
func TestDescribeTableReportsMissingTable(t *testing.T) {
	describer := &stubTableDescriber{err: fmt.Errorf("%w: %q in data source %s", metadata.ErrTableNotFound, "orders", "ds-1")}
	server := describeTableServer(activeOwnedSource(), describer)

	ctx, request := describeTableRequest(t, &security.Caller{UserID: "alice"}, "ds-1", "orders")
	result, err := server.handleDescribeTable(ctx, request)
	if err != nil {
		t.Fatalf("handleDescribeTable() error = %v", err)
	}
	if !result.IsError {
		t.Fatal("missing table must be a tool error")
	}
	if text := describeTableText(t, result); !strings.Contains(text, "not found") {
		t.Fatalf("error text = %q, want a not-found message", text)
	}
}

// Drivers without schema discovery fail clearly instead of guessing (#184).
func TestDescribeTableReportsUnsupportedDriver(t *testing.T) {
	describer := &stubTableDescriber{err: fmt.Errorf("%w: %s", metadata.ErrSchemaDiscoveryUnsupported, "not_registered")}
	server := describeTableServer(activeOwnedSource(), describer)

	ctx, request := describeTableRequest(t, &security.Caller{UserID: "alice"}, "ds-1", "orders")
	result, err := server.handleDescribeTable(ctx, request)
	if err != nil {
		t.Fatalf("handleDescribeTable() error = %v", err)
	}
	if !result.IsError {
		t.Fatal("unsupported driver must be a tool error")
	}
	if text := describeTableText(t, result); !strings.Contains(text, "not supported") {
		t.Fatalf("error text = %q, want an unsupported message", text)
	}
}

// Foreign callers cannot probe data sources: missing and forbidden IDs stay
// indistinguishable, and metadata extraction is never attempted (#9).
func TestDescribeTableDeniesForeignCaller(t *testing.T) {
	describer := &stubTableDescriber{}
	server := describeTableServer(activeOwnedSource(), describer)

	ctx, request := describeTableRequest(t, &security.Caller{UserID: "bob"}, "ds-1", "orders")
	result, err := server.handleDescribeTable(ctx, request)
	if err != nil {
		t.Fatalf("handleDescribeTable() error = %v", err)
	}
	if !result.IsError || describeTableText(t, result) != "data source not found or access denied" {
		t.Fatalf("result = %+v, want access denied", result)
	}
	if describer.calls != 0 {
		t.Fatalf("describer called %d times for a denied caller", describer.calls)
	}
}

// Metadata extraction queries the source itself, so inactive sources are
// rejected like query execution (#118).
func TestDescribeTableRejectsInactiveSource(t *testing.T) {
	source := activeOwnedSource()
	source.Status = model.DataSourceStatusInactive
	describer := &stubTableDescriber{}
	server := describeTableServer(source, describer)

	ctx, request := describeTableRequest(t, &security.Caller{UserID: "alice"}, "ds-1", "orders")
	result, err := server.handleDescribeTable(ctx, request)
	if err != nil {
		t.Fatalf("handleDescribeTable() error = %v", err)
	}
	if !result.IsError {
		t.Fatal("inactive source must be a tool error")
	}
	if text := describeTableText(t, result); !strings.Contains(text, "not active") {
		t.Fatalf("error text = %q, want an inactive message", text)
	}
	if describer.calls != 0 {
		t.Fatalf("describer called %d times for an inactive source", describer.calls)
	}
}

// Unexpected extraction failures return a safe message without internal
// details, matching the query tools' error redaction (#29).
func TestDescribeTableRedactsInternalErrors(t *testing.T) {
	describer := &stubTableDescriber{err: errors.New("dial tcp 10.0.0.5:5432: connection refused")}
	server := describeTableServer(activeOwnedSource(), describer)

	ctx, request := describeTableRequest(t, &security.Caller{UserID: "alice"}, "ds-1", "orders")
	result, err := server.handleDescribeTable(ctx, request)
	if err != nil {
		t.Fatalf("handleDescribeTable() error = %v", err)
	}
	if !result.IsError {
		t.Fatal("extraction failure must be a tool error")
	}
	if text := describeTableText(t, result); strings.Contains(text, "10.0.0.5") {
		t.Fatalf("error text leaked internal details: %q", text)
	}
}

// A server without metadata extraction keeps the handler total: it reports
// the missing capability instead of panicking.
func TestDescribeTableWithoutDescriber(t *testing.T) {
	server := describeTableServer(activeOwnedSource(), nil)

	ctx, request := describeTableRequest(t, &security.Caller{UserID: "alice"}, "ds-1", "orders")
	result, err := server.handleDescribeTable(ctx, request)
	if err != nil {
		t.Fatalf("handleDescribeTable() error = %v", err)
	}
	if !result.IsError {
		t.Fatal("missing describer must be a tool error")
	}
	if text := describeTableText(t, result); !strings.Contains(text, "not available") {
		t.Fatalf("error text = %q, want a capability message", text)
	}
}
