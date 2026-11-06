package reference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

type sourceRecord struct {
	ID string `json:"id"`
}

func (env *Environment) registerSources(ctx context.Context) error {
	for _, def := range sourceDefinitions {
		body := map[string]any{
			"name": "reference-" + def.key, "type": def.key,
			"config": map[string]any{
				"host": def.service, "port": def.port, "database": "sample", "schema": def.schema,
				"username": "sample_reader", "password": env.stack.env[def.readerEnv],
			},
		}
		var created sourceRecord
		if err := env.rest(ctx, "app-a", http.MethodPost, "/api/v1/datasources", body, http.StatusCreated, &created); err != nil {
			return fmt.Errorf("register %s: %w", def.key, err)
		}
		if created.ID == "" {
			return fmt.Errorf("register %s returned an empty ID", def.key)
		}
		env.Sources[def.key] = Source{ID: created.ID, Type: def.key, Schema: def.schema}
	}
	return nil
}

// EnsureTenantBSource creates a deterministic source for a second tenant
// only when the routed acceptance matrix reaches its principal-binding case.
func (env *Environment) EnsureTenantBSource(ctx context.Context, sourceKey string) error {
	if _, ok := env.TenantBSources[sourceKey]; ok {
		return nil
	}
	var definition *sourceDefinition
	for index := range sourceDefinitions {
		if sourceDefinitions[index].key == sourceKey {
			definition = &sourceDefinitions[index]
			break
		}
	}
	if definition == nil {
		return fmt.Errorf("unknown reference source")
	}
	body := map[string]any{
		"name": "reference-" + definition.key + "-tenant-b", "type": definition.key,
		"config": map[string]any{
			"host": definition.service, "port": definition.port, "database": "sample", "schema": definition.schema,
			"username": "sample_reader", "password": env.stack.env[definition.readerEnv],
		},
	}
	var created sourceRecord
	if err := env.rest(ctx, "app-a-tenant-b", http.MethodPost, "/api/v1/datasources", body, http.StatusCreated, &created); err != nil {
		return fmt.Errorf("register tenant-b %s: %w", sourceKey, err)
	}
	if created.ID == "" {
		return fmt.Errorf("register tenant-b %s returned an empty ID", sourceKey)
	}
	env.TenantBSources[sourceKey] = Source{ID: created.ID, Type: definition.key, Schema: definition.schema}
	if err := env.publishManagedPolicy(ctx); err != nil {
		return fmt.Errorf("publish tenant-b source policy: %w", err)
	}
	return nil
}

func (env *Environment) verifyReuse(ctx context.Context) error {
	var restSources struct {
		DataSources []sourceRecord `json:"dataSources"`
	}
	if err := env.rest(ctx, "app-b", http.MethodGet, "/api/v1/datasources", nil, http.StatusOK, &restSources); err != nil {
		return err
	}
	if err := env.verifySources(restSources.DataSources); err != nil {
		return fmt.Errorf("REST reuse: %w", err)
	}

	mcpClient, err := env.openMCP(ctx, "app-b")
	if err != nil {
		return err
	}
	defer mcpClient.Close()
	var mcpSources struct {
		DataSources []sourceRecord `json:"data_sources"`
	}
	if err := mcpCall(ctx, mcpClient, "list_data_sources", nil, false, &mcpSources); err != nil {
		return err
	}
	if err := env.verifySources(mcpSources.DataSources); err != nil {
		return fmt.Errorf("MCP reuse: %w", err)
	}

	for _, def := range sourceDefinitions {
		source := env.Sources[def.key]
		query := "SELECT id, amount FROM " + source.Schema + ".orders WHERE id = 1"
		var result struct {
			Entries [][]json.RawMessage `json:"entries"`
		}
		restQuery := map[string]any{"dataSourceId": source.ID, "sql": query, "type": 1, "batch_size": 10}
		if err := env.rest(ctx, "app-b", http.MethodPost, "/api/v1/fetch", restQuery, http.StatusOK, &result); err != nil {
			return fmt.Errorf("REST query on %s: %w", def.key, err)
		}
		if !expectedOrder(result.Entries) {
			return fmt.Errorf("REST query on %s returned unexpected rows", def.key)
		}
		mcpQuery := map[string]any{"datasource_id": source.ID, "sql": query, "batch_size": 10}
		result.Entries = nil
		if err := mcpCall(ctx, mcpClient, "execute_sql_query", mcpQuery, false, &result); err != nil {
			return fmt.Errorf("MCP query on %s: %w", def.key, err)
		}
		if !expectedOrder(result.Entries) {
			return fmt.Errorf("MCP query on %s returned unexpected rows", def.key)
		}
	}
	return nil
}

func (env *Environment) verifySources(records []sourceRecord) error {
	if len(records) != len(env.Sources) {
		return fmt.Errorf("unexpected number of visible sources: %d", len(records))
	}
	ids := make(map[string]bool, len(records))
	for _, record := range records {
		ids[record.ID] = true
	}
	for key, source := range env.Sources {
		if !ids[source.ID] {
			return fmt.Errorf("registered %s source is missing", key)
		}
	}
	return nil
}

func (env *Environment) verifyDenials(ctx context.Context) error {
	source := env.Sources["mysql"]
	allowedQuery := "SELECT id FROM sample.orders WHERE id = 1"
	restrictedQuery := "SELECT customer FROM sample.orders WHERE id = 1"
	for _, sample := range []struct{ identity, sql string }{
		{"app-c", allowedQuery},
		{"app-b", restrictedQuery},
	} {
		body := map[string]any{"dataSourceId": source.ID, "sql": sample.sql, "type": 1}
		if err := env.rest(ctx, sample.identity, http.MethodPost, "/api/v1/fetch", body, http.StatusForbidden, nil); err != nil {
			return fmt.Errorf("REST denial for %s: %w", sample.identity, err)
		}
		mcpClient, err := env.openMCP(ctx, sample.identity)
		if err != nil {
			return err
		}
		err = mcpCall(ctx, mcpClient, "execute_sql_query", map[string]any{"datasource_id": source.ID, "sql": sample.sql}, true, nil)
		mcpClient.Close()
		if err != nil {
			return fmt.Errorf("MCP denial for %s: %w", sample.identity, err)
		}
	}
	badToken, err := env.MintMCP("app-b", func(claims jwt.MapClaims) {
		claims["aud"] = "unrelated-service"
	})
	if err != nil {
		return err
	}
	probe, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": mcp.LATEST_PROTOCOL_VERSION,
			"clientInfo":      map[string]string{"name": "reference-invalid-audience", "version": "1"},
			"capabilities":    map[string]any{},
		},
	})
	if err != nil {
		return fmt.Errorf("encode MCP invalid-audience probe: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.MCPURL, bytes.NewReader(probe))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+badToken)
	resp, err := env.mcpHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("MCP invalid-audience probe: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("MCP invalid-audience status %d, expected 401", resp.StatusCode)
	}
	return nil
}

func (env *Environment) rest(ctx context.Context, identity, method, path string, request any, status int, response any) error {
	var body io.Reader
	if request != nil {
		data, err := json.Marshal(request)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, env.APIURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+env.Identities[identity].RESTToken)
	if request != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	httpClient := &http.Client{Timeout: 20 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != status {
		return fmt.Errorf("unexpected REST %s %s status %d, expected %d", method, path, resp.StatusCode, status)
	}
	if response == nil {
		return nil
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode REST response: %w", err)
	}
	if !envelope.Success {
		return fmt.Errorf("REST response did not indicate success")
	}
	return json.Unmarshal(envelope.Data, response)
}

func (env *Environment) mcpHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   20 * time.Second,
		Transport: &http.Transport{TLSClientConfig: env.TLSConfig.Clone()},
	}
}

func (env *Environment) openMCP(ctx context.Context, identity string) (*client.Client, error) {
	c, err := client.NewStreamableHttpClient(env.MCPURL, transport.WithHTTPBasicClient(env.mcpHTTPClient()),
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + env.Identities[identity].MCPToken}))
	if err != nil {
		return nil, fmt.Errorf("create MCP client: %w", err)
	}
	if err := c.Start(ctx); err != nil {
		c.Close()
		return nil, fmt.Errorf("start MCP client: %w", err)
	}
	_, err = c.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: identity, Version: "reference"},
	}})
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("initialize MCP client for %s: %w", identity, err)
	}
	return c, nil
}

func mcpCall(ctx context.Context, c *client.Client, name string, args map[string]any, wantError bool, response any) error {
	result, err := c.CallTool(ctx, mcp.CallToolRequest{Params: mcp.CallToolParams{Name: name, Arguments: args}})
	if err != nil {
		return fmt.Errorf("call MCP %s: %w", name, err)
	}
	if result.IsError != wantError {
		return fmt.Errorf("MCP %s isError=%t, expected %t", name, result.IsError, wantError)
	}
	if response == nil {
		return nil
	}
	if len(result.Content) != 1 {
		return fmt.Errorf("MCP %s returned unexpected content", name)
	}
	text, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		return fmt.Errorf("MCP %s did not return JSON text", name)
	}
	return json.Unmarshal([]byte(text.Text), response)
}

func expectedOrder(rows [][]json.RawMessage) bool {
	if len(rows) != 1 || len(rows[0]) != 2 || string(rows[0][0]) != "1" {
		return false
	}
	amount, err := strconv.ParseFloat(string(bytes.Trim(rows[0][1], `"`)), 64)
	return err == nil && amount == 250
}
