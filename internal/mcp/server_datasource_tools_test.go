package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
)

// secretMarker is planted in every credential-bearing field of the fake
// source; responses must never contain it. A unique-looking value avoids
// false passes from words that legitimately appear in responses.
const secretMarker = "marker-secret-do-not-print-9f3a"

// callToolRequest builds a tool call the way the transport layer would.
func callToolRequest(name string, arguments map[string]any) mcp.CallToolRequest {
	request := mcp.CallToolRequest{}
	request.Params.Name = name
	if arguments != nil {
		request.Params.Arguments = arguments
	}
	return request
}

// listingRepoStub backs the data-source listing/detail tools. GetAll and
// GetByID are the only repository methods the handlers call.
type listingRepoStub struct {
	repository.DataSourceRepository
	sources []*model.DataSource
	listErr error
	getErr  error
}

func (r listingRepoStub) GetAll(context.Context, *repository.AccessScope, model.DataSourceStatus, int, int) ([]*model.DataSource, int64, error) {
	if r.listErr != nil {
		return nil, 0, r.listErr
	}
	return r.sources, int64(len(r.sources)), nil
}

func (r listingRepoStub) GetByID(_ context.Context, id string) (*model.DataSource, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	for _, source := range r.sources {
		if source.ID == id {
			return source, nil
		}
	}
	return nil, repository.ErrDataSourceNotFound
}

func sourceWithSecrets() *model.DataSource {
	return &model.DataSource{
		ID:     "ds-1",
		Name:   "orders-db",
		Type:   model.DatabaseTypeMySQL,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{
			Host:     "db-internal",
			Port:     3306,
			Database: "sample",
			Username: "marker-user-do-not-print",
			Password: secretMarker,
			AdditionalProps: map[string]interface{}{
				"api_key": secretMarker,
			},
		},
	}
}

func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	return describeTableText(t, result)
}

// list_data_sources succeeds, includes the allowlisted connection fields, and
// omits every credential-bearing field: password, username, and additional
// properties (#181).
func TestListDataSourcesOmitsStoredCredentials(t *testing.T) {
	srv := NewMCPServer(nil, listingRepoStub{sources: []*model.DataSource{sourceWithSecrets()}}, &stubTableDescriber{})

	result, err := srv.handleListDataSources(context.Background(), callToolRequest("list_data_sources", nil))
	if err != nil {
		t.Fatalf("handleListDataSources() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("handleListDataSources() reported tool error: %+v", result.Content)
	}
	text := resultText(t, result)
	for _, absent := range []string{secretMarker, "marker-user-do-not-print", "api_key", "password", "username"} {
		if strings.Contains(text, absent) {
			t.Errorf("response leaks %q: %s", absent, text)
		}
	}
	var payload struct {
		Count       int `json:"count"`
		DataSources []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
			Host string `json:"host"`
			Port int    `json:"port"`
		} `json:"data_sources"`
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		t.Fatalf("decode response %q: %v", text, err)
	}
	if payload.Count != 1 || len(payload.DataSources) != 1 {
		t.Fatalf("count = %d, sources = %d, want 1/1", payload.Count, len(payload.DataSources))
	}
	source := payload.DataSources[0]
	if source.ID != "ds-1" || source.Name != "orders-db" || source.Type != "mysql" || source.Host != "db-internal" || source.Port != 3306 {
		t.Errorf("listed source = %+v, want the allowlisted connection fields", source)
	}
}

// get_data_source_info shares the same allowlist: detail responses carry the
// connection fields but never the stored credentials (#181).
func TestGetDataSourceInfoOmitsStoredCredentials(t *testing.T) {
	srv := NewMCPServer(nil, listingRepoStub{sources: []*model.DataSource{sourceWithSecrets()}}, &stubTableDescriber{})

	result, err := srv.handleGetDataSourceInfo(context.Background(), callToolRequest("get_data_source_info", map[string]any{"datasource_id": "ds-1"}))
	if err != nil {
		t.Fatalf("handleGetDataSourceInfo() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("handleGetDataSourceInfo() reported tool error: %+v", result.Content)
	}
	text := resultText(t, result)
	for _, absent := range []string{secretMarker, "marker-user-do-not-print", "api_key"} {
		if strings.Contains(text, absent) {
			t.Errorf("response leaks %q: %s", absent, text)
		}
	}
	if !strings.Contains(text, `"id":"ds-1"`) || !strings.Contains(text, `"database":"sample"`) {
		t.Errorf("response is missing allowlisted fields: %s", text)
	}
}

// An empty repository yields a successful empty listing, not an error (#181).
func TestListDataSourcesEmpty(t *testing.T) {
	srv := NewMCPServer(nil, listingRepoStub{}, &stubTableDescriber{})

	result, err := srv.handleListDataSources(context.Background(), callToolRequest("list_data_sources", nil))
	if err != nil {
		t.Fatalf("handleListDataSources() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("handleListDataSources() reported tool error: %+v", result.Content)
	}
	var payload struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(resultText(t, result)), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.Count != 0 {
		t.Fatalf("count = %d, want 0", payload.Count)
	}
}

// A missing data source is a tool-level error (isError: true with stable
// text), not a transport-level JSON-RPC error — the client can act on it
// (#181 failure channels).
func TestGetDataSourceInfoNotFoundIsToolError(t *testing.T) {
	srv := NewMCPServer(nil, listingRepoStub{}, &stubTableDescriber{})

	result, err := srv.handleGetDataSourceInfo(context.Background(), callToolRequest("get_data_source_info", map[string]any{"datasource_id": "missing"}))
	if err != nil {
		t.Fatalf("handleGetDataSourceInfo() error = %v, want a tool-level error result", err)
	}
	if !result.IsError {
		t.Fatalf("result is a success response, want isError: %s", resultText(t, result))
	}
	if text := resultText(t, result); !strings.Contains(text, "data source not found or access denied") {
		t.Fatalf("tool error text = %q, want the stable not-found message", text)
	}
}

// A repository failure on listing is a transport-level error (returned error,
// no result) — the other failure channel, reserved for gateway-side faults
// (#181).
func TestListDataSourcesRepoFailureIsTransportError(t *testing.T) {
	srv := NewMCPServer(nil, listingRepoStub{listErr: errors.New("metadata db down")}, &stubTableDescriber{})

	result, err := srv.handleListDataSources(context.Background(), callToolRequest("list_data_sources", nil))
	if err == nil {
		t.Fatalf("handleListDataSources() succeeded despite repository failure: %+v", result)
	}
	if result != nil {
		t.Fatalf("repository failure produced both channels: err = %v, result = %+v", err, result)
	}
}
