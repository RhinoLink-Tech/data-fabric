package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	queryservice "github.com/RhinoLink-Tech/data-fabric/internal/service"
	"github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"
)

// MCPServer represents the MCP server that exposes Data Fabric functionality
type MCPServer struct {
	unifiedQueryService unifiedservice.UnifiedQueryService
	datasourceRepo      repository.DataSourceRepository
	sqlTranslator       *sql_translator.SQLTranslationManager // Added SQL translator
	server              *server.MCPServer
}

// NewMCPServer creates a new MCP server instance
func NewMCPServer(unifiedQueryService unifiedservice.UnifiedQueryService, datasourceRepo repository.DataSourceRepository) *MCPServer {
	s := server.NewMCPServer(
		"Data Fabric MCP Server",
		"1.0.0",
	)

	mcpServer := &MCPServer{
		unifiedQueryService: unifiedQueryService,
		datasourceRepo:      datasourceRepo,
		sqlTranslator:       sql_translator.NewSQLTranslationManager(true), // Initialize SQL translator
		server:              s,
	}

	// Register tools that will be available to LLMs
	mcpServer.registerTools()

	return mcpServer
}

// registerTools registers all available tools with the MCP server
func (m *MCPServer) registerTools() {
	// Tool to list available data sources
	listDataSourcesTool := mcp.NewTool("list_data_sources",
		mcp.WithDescription("List all configured data sources in Data Fabric"))
	m.server.AddTool(listDataSourcesTool, m.handleListDataSources)

	// Tool to execute SQL queries against data sources
	executeSQLQueryTool := mcp.NewTool("execute_sql_query",
		mcp.WithDescription("Execute a SQL query against a specified data source"),
		mcp.WithString("datasource_id", mcp.Required(), mcp.Description("The ID of the data source to query")),
		mcp.WithString("sql", mcp.Required(), mcp.Description("The SQL query to execute")),
		mcp.WithString("source_dialect", mcp.Description("Source SQL dialect for translation (e.g., mysql, trino, presto, openlookeng, postgres)")),
		mcp.WithNumber("batch_size", mcp.Description("Number of records to return per batch, default 10000")),
		mcp.WithNumber("timeout", mcp.Description("Query timeout in seconds, default 60")))
	m.server.AddTool(executeSQLQueryTool, m.handleExecuteSQLQuery)

	fetchNextBatchTool := mcp.NewTool("fetch_next_batch",
		mcp.WithDescription("Fetch the next page from a previous execute_sql_query call"),
		mcp.WithString("query_id", mcp.Required(), mcp.Description("Query ID returned by execute_sql_query")),
		mcp.WithString("slug", mcp.Required(), mcp.Description("Query slug returned by execute_sql_query")),
		mcp.WithString("token", mcp.Required(), mcp.Description("Current pagination token")),
		mcp.WithNumber("batch_size", mcp.Description("Number of records to return, default 10000")))
	m.server.AddTool(fetchNextBatchTool, m.handleFetchNextBatch)

	// Tool to get information about a specific data source
	getDataSourceInfoTool := mcp.NewTool("get_data_source_info",
		mcp.WithDescription("Get detailed information about a specific data source"),
		mcp.WithString("datasource_id", mcp.Required(), mcp.Description("The ID of the data source to get info for")))
	m.server.AddTool(getDataSourceInfoTool, m.handleGetDataSourceInfo)

	// Tool to validate a SQL query against a data source
	validateSQLQueryTool := mcp.NewTool("validate_sql_query",
		mcp.WithDescription("Validate a SQL query without executing it"),
		mcp.WithString("datasource_id", mcp.Required(), mcp.Description("The ID of the data source to validate against")),
		mcp.WithString("sql", mcp.Required(), mcp.Description("The SQL query to validate")),
		mcp.WithString("source_dialect", mcp.Description("Source SQL dialect for translation (e.g., mysql, trino, presto, openlookeng, postgres)")))
	m.server.AddTool(validateSQLQueryTool, m.handleValidateSQLQuery)

	// Tool to list tables in a specific data source
	listTablesTool := mcp.NewTool("list_tables",
		mcp.WithDescription("List all tables in a specific data source"),
		mcp.WithString("datasource_id", mcp.Required(), mcp.Description("The ID of the data source to list tables from")))
	m.server.AddTool(listTablesTool, m.handleListTables)

	// Tool to translate SQL between dialects
	translateSQLTool := mcp.NewTool("translate_sql",
		mcp.WithDescription("Translate SQL from one dialect to another"),
		mcp.WithString("sql", mcp.Required(), mcp.Description("The SQL to translate")),
		mcp.WithString("source_dialect", mcp.Required(), mcp.Description("Source SQL dialect (e.g., mysql, trino, presto, openlookeng, postgres)")),
		mcp.WithString("target_dialect", mcp.Required(), mcp.Description("Target SQL dialect (e.g., mysql, trino, presto, openlookeng, postgres)")))
	m.server.AddTool(translateSQLTool, m.handleTranslateSQL)

	// Tool to list supported SQL dialects
	listDialectsTool := mcp.NewTool("list_supported_dialects",
		mcp.WithDescription("List all supported SQL dialects"))
	m.server.AddTool(listDialectsTool, m.handleListSupportedDialects)
}

// handleListDataSources handles the list_data_sources tool call
func (m *MCPServer) handleListDataSources(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Restrict remote callers to their own and tenant-shared data sources.
	caller := callerFromContext(ctx)
	scope := repository.ScopeForCaller(caller)
	dataSources, _, err := m.datasourceRepo.GetAll(ctx, scope, "", 1000, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to list data sources: %w", err)
	}

	var result []map[string]interface{}
	for _, ds := range dataSources {
		dsMap := map[string]interface{}{
			"id":         ds.ID,
			"name":       ds.Name,
			"type":       ds.Type,
			"status":     ds.Status, // Use Status instead of Description/Enabled
			"host":       ds.Config.Host,
			"port":       ds.Config.Port,
			"database":   ds.Config.Database,
			"created_at": ds.CreatedAt,
			"updated_at": ds.UpdatedAt,
		}
		result = append(result, dsMap)
	}

	response := map[string]interface{}{
		"data_sources": result,
		"count":        len(result),
	}

	jsonResp, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(string(jsonResp)),
		},
	}, nil
}

// handleExecuteSQLQuery handles the execute_sql_query tool call
func (m *MCPServer) handleExecuteSQLQuery(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract parameters from the request
	datasourceID := mcp.ParseString(request, "datasource_id", "")
	sql := mcp.ParseString(request, "sql", "")
	sourceDialect := mcp.ParseString(request, "source_dialect", "") // Optional source dialect
	batchSize := int(mcp.ParseInt(request, "batch_size", 10000))
	timeout := int(mcp.ParseInt(request, "timeout", 60))
	if batchSize <= 0 {
		batchSize = 10000
	} else if batchSize > 10000 {
		batchSize = 10000
	}
	if timeout <= 0 {
		timeout = 60
	} else if timeout > 1800 {
		timeout = 1800
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	// The query service performs dialect translation before execution.
	// Keep the original SQL here so it is not translated twice.
	// Prepare the fetch query request
	req := &model.FetchQueryRequest{
		DataSourceID:  datasourceID,
		SQL:           sql,
		SourceDialect: sourceDialect,
		Type:          2,
		BatchSize:     batchSize,
		Timeout:       timeout,
	}

	// Attribute remote query history to the authenticated user; stdio stays in
	// the shared local "mcp" bucket.
	ctx = queryservice.WithQueryUser(ctx, queryUserFromContext(ctx))
	result, err := m.unifiedQueryService.FetchQuery(ctx, callerFromContext(ctx), req)
	if err != nil {
		return mcp.NewToolResultError(queryservice.SafeQueryErrorMessage(err)), nil
	}

	// Convert result to JSON for response
	jsonResp, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(string(jsonResp)),
		},
	}, nil
}

// handleFetchNextBatch handles the fetch_next_batch tool call.
func (m *MCPServer) handleFetchNextBatch(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	queryID := mcp.ParseString(request, "query_id", "")
	slug := mcp.ParseString(request, "slug", "")
	token := mcp.ParseString(request, "token", "")
	batchSize := int(mcp.ParseInt(request, "batch_size", 10000))
	if batchSize <= 0 || batchSize > 10000 {
		batchSize = 10000
	}

	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	result, err := m.unifiedQueryService.FetchNextBatch(ctx, callerFromContext(ctx), queryID, slug, token, batchSize)
	if err != nil {
		return mcp.NewToolResultError(queryservice.SafeQueryErrorMessage(err)), nil
	}
	jsonResp, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(string(jsonResp))},
	}, nil
}

// handleGetDataSourceInfo handles the get_data_source_info tool call
func (m *MCPServer) handleGetDataSourceInfo(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract parameters from the request
	datasourceID := mcp.ParseString(request, "datasource_id", "")

	// Get the data source from the repository
	dataSource, err := m.datasourceRepo.GetByID(ctx, datasourceID)
	caller := callerFromContext(ctx)
	if err != nil || !caller.CanRead(dataSource) {
		// Keep missing and forbidden IDs indistinguishable to non-admin users.
		return mcp.NewToolResultError("data source not found or access denied"), nil
	}

	info := map[string]interface{}{
		"id":         dataSource.ID,
		"name":       dataSource.Name,
		"type":       dataSource.Type,
		"status":     dataSource.Status, // Use Status instead of Description/Enabled
		"host":       dataSource.Config.Host,
		"port":       dataSource.Config.Port,
		"database":   dataSource.Config.Database,
		"created_at": dataSource.CreatedAt,
		"updated_at": dataSource.UpdatedAt,
	}

	jsonResp, err := json.Marshal(info)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(string(jsonResp)),
		},
	}, nil
}

// handleValidateSQLQuery handles the validate_sql_query tool call
func (m *MCPServer) handleValidateSQLQuery(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract parameters from the request
	datasourceID := mcp.ParseString(request, "datasource_id", "")
	sql := mcp.ParseString(request, "sql", "")
	sourceDialect := mcp.ParseString(request, "source_dialect", "") // Optional source dialect

	// The query service translates the SQL for validation when a source dialect is set.
	// Create a basic query request for validation
	req := &model.QueryRequest{
		DataSourceID:  datasourceID,
		SQL:           sql,
		SourceDialect: sourceDialect, // Pass the source dialect for reference
	}

	// Validate the query using the unified query service
	err := m.unifiedQueryService.ValidateQuery(ctx, callerFromContext(ctx), req)
	if err != nil {
		response := map[string]interface{}{
			"valid": false,
			"error": queryservice.SafeQueryErrorMessage(err),
		}

		jsonResp, err := json.Marshal(response)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal response: %w", err)
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.NewTextContent(string(jsonResp)),
			},
		}, nil
	}

	response := map[string]interface{}{
		"valid": true,
		"sql":   sql,
	}

	jsonResp, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(string(jsonResp)),
		},
	}, nil
}

// handleListTables handles the list_tables tool call
func (m *MCPServer) handleListTables(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract parameters from the request
	datasourceID := mcp.ParseString(request, "datasource_id", "")

	// First, get the data source to determine its type
	dataSource, err := m.datasourceRepo.GetByID(ctx, datasourceID)
	caller := callerFromContext(ctx)
	if err != nil || !caller.CanRead(dataSource) {
		return mcp.NewToolResultError("data source not found or access denied"), nil
	}

	// For now, we'll execute a query to list tables based on the database type
	// This is a simplified approach - in a real implementation, you'd want more robust table discovery
	var sql string
	dbType := dataSource.Type
	switch dbType {
	case "mysql", "mariadb":
		sql = "SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE()"
	case "postgres", "postgresql":
		sql = "SELECT table_name FROM information_schema.tables WHERE table_schema='public'"
	case "oracle":
		sql = "SELECT table_name FROM user_tables"
	case "sqlserver":
		sql = "SELECT TABLE_NAME FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_TYPE = 'BASE TABLE'"
	default:
		// Generic approach - try to use information_schema which is common among many databases
		sql = "SELECT table_name FROM information_schema.tables WHERE table_schema NOT IN ('information_schema', 'pg_catalog', 'sys')"
	}

	req := &model.FetchQueryRequest{
		DataSourceID: datasourceID,
		SQL:          sql,
		BatchSize:    1000, // Limit the number of tables returned
		Timeout:      30,
	}

	// Attribute remote query history to the authenticated user; stdio stays in
	// the shared local "mcp" bucket.
	ctx = queryservice.WithQueryUser(ctx, queryUserFromContext(ctx))
	result, err := m.unifiedQueryService.FetchQuery(ctx, caller, req)
	if err != nil {
		return mcp.NewToolResultError(queryservice.SafeQueryErrorMessage(err)), nil
	}

	// Convert result to JSON for response
	jsonResp, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(string(jsonResp)),
		},
	}, nil
}

// handleTranslateSQL handles the translate_sql tool call
func (m *MCPServer) handleTranslateSQL(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Extract parameters from the request
	sql := mcp.ParseString(request, "sql", "")
	sourceDialect := mcp.ParseString(request, "source_dialect", "")
	targetDialect := mcp.ParseString(request, "target_dialect", "")

	if sql == "" {
		return nil, fmt.Errorf("SQL parameter is required")
	}
	if sourceDialect == "" {
		return nil, fmt.Errorf("source_dialect parameter is required")
	}
	if targetDialect == "" {
		return nil, fmt.Errorf("target_dialect parameter is required")
	}

	// Translate the SQL
	translatedSQL, err := m.sqlTranslator.TranslateQuery(sql, sourceDialect, targetDialect)
	if err != nil {
		return nil, fmt.Errorf("failed to translate SQL: %w", err)
	}

	response := map[string]interface{}{
		"original_sql":   sql,
		"source_dialect": sourceDialect,
		"target_dialect": targetDialect,
		"translated_sql": translatedSQL,
	}

	jsonResp, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(string(jsonResp)),
		},
	}, nil
}

// handleListSupportedDialects handles the list_supported_dialects tool call
func (m *MCPServer) handleListSupportedDialects(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Get supported dialects
	supportedDialects := m.sqlTranslator.GetSupportedDialects()

	response := map[string]interface{}{
		"supported_dialects": supportedDialects,
		"count":              len(supportedDialects),
	}

	jsonResp, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.NewTextContent(string(jsonResp)),
		},
	}, nil
}

// StartStdio starts the MCP server using stdio transport
func (m *MCPServer) StartStdio() error {
	return server.ServeStdio(m.server)
}

// Stop stops the MCP server
func (m *MCPServer) Stop() {
	// MCP server doesn't expose a stop method in the current API
	// This is a placeholder for future implementation
}
