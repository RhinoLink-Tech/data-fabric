package mcp

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	"github.com/mark3labs/mcp-go/mcp"
)

const sourceManagementScope = "data-fabric.sources.manage"

// SetDataSourceManagementService enables non-secret lifecycle tools on the
// authenticated remote transport. It is intentionally not wired for stdio.
func (m *MCPServer) SetDataSourceManagementService(s service.DataSourceService) {
	if m == nil || s == nil || m.datasourceService != nil {
		return
	}
	m.datasourceService = s
	m.addTool("delete_data_source", mcp.NewTool("delete_data_source",
		mcp.WithDescription("Permanently remove an owned source after explicit user confirmation. Requires data-fabric.sources.manage; confirmation_name must exactly match the source name."),
		mcp.WithString("datasource_id", mcp.Required()), mcp.WithString("confirmation_name", mcp.Required())), m.handleDeleteDataSource)
	m.addTool("rename_data_source", mcp.NewTool("rename_data_source",
		mcp.WithDescription("Rename a source owned by the caller. Requires data-fabric.sources.manage; source configuration and credentials are unchanged."),
		mcp.WithString("datasource_id", mcp.Required()), mcp.WithString("name", mcp.Required())), m.handleRenameDataSource)
	m.addTool("set_data_source_status", mcp.NewTool("set_data_source_status",
		mcp.WithDescription("Activate or deactivate a source owned by the caller. Requires data-fabric.sources.manage."),
		mcp.WithString("datasource_id", mcp.Required()), mcp.WithString("status", mcp.Required())), m.handleSetDataSourceStatus)
}

func (m *MCPServer) managementCaller(ctx context.Context) *security.Caller {
	caller, _ := ctx.Value(callerContextKey{}).(*security.Caller)
	if caller == nil || strings.TrimSpace(caller.UserID) == "" {
		return nil
	}
	for _, scope := range caller.Scopes {
		if scope == sourceManagementScope {
			return caller
		}
	}
	return nil
}
func managementArgs(args map[string]any, second string) (string, string, bool) {
	if len(args) != 2 {
		return "", "", false
	}
	id, ok := args["datasource_id"].(string)
	if !ok || strings.TrimSpace(id) == "" {
		return "", "", false
	}
	value, ok := args[second].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return "", "", false
	}
	return id, value, true
}

func (m *MCPServer) handleRenameDataSource(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	caller := m.managementCaller(ctx)
	if caller == nil {
		auditlog.MarkDecision(ctx, auditlog.DecisionDeny, "source_management_scope_required")
		return mcp.NewToolResultError("source management permission required"), nil
	}
	id, name, ok := managementArgs(req.GetArguments(), "name")
	if !ok || name != strings.TrimSpace(name) || utf8.RuneCountInString(name) > 255 {
		return mcp.NewToolResultError("provide datasource_id and a name of 1 to 255 characters without surrounding whitespace"), nil
	}
	mutator, ok := m.datasourceService.(service.ScopedSourceManagement)
	if !ok {
		return mcp.NewToolResultError("source management unavailable"), nil
	}
	source, err := mutator.RenameSourceScoped(ctx, caller, id, name)
	if errors.Is(err, repository.ErrDataSourceNotFound) || errors.Is(err, security.ErrDataSourceAccessDenied) {
		auditlog.MarkDecision(ctx, auditlog.DecisionDeny, "source_management_access_denied")
		return mcp.NewToolResultError("source is unavailable or access is denied"), nil
	}
	if err != nil || source == nil {
		return mcp.NewToolResultError("source could not be renamed; check access, identifier and name availability"), nil
	}
	return mcpTablePolicyJSON(map[string]any{"id": source.ID, "name": source.Name, "status": source.Status})
}
func (m *MCPServer) handleSetDataSourceStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	caller := m.managementCaller(ctx)
	if caller == nil {
		auditlog.MarkDecision(ctx, auditlog.DecisionDeny, "source_management_scope_required")
		return mcp.NewToolResultError("source management permission required"), nil
	}
	id, status, ok := managementArgs(req.GetArguments(), "status")
	if !ok || (status != "active" && status != "inactive") {
		return mcp.NewToolResultError("provide datasource_id and status active or inactive"), nil
	}
	mutator, ok := m.datasourceService.(service.ScopedSourceManagement)
	if !ok {
		return mcp.NewToolResultError("source management unavailable"), nil
	}
	err := mutator.SetSourceStatusScoped(ctx, caller, id, model.DataSourceStatus(status))
	if errors.Is(err, repository.ErrDataSourceNotFound) || errors.Is(err, security.ErrDataSourceAccessDenied) {
		auditlog.MarkDecision(ctx, auditlog.DecisionDeny, "source_management_access_denied")
		return mcp.NewToolResultError("source is unavailable or access is denied"), nil
	}
	if err != nil {
		return mcp.NewToolResultError("source status could not be changed; check access and identifier"), nil
	}
	return mcpTablePolicyJSON(map[string]any{"id": id, "status": status})
}

// SourceConnectivityTester receives decrypted configuration inside the server.
type SourceConnectivityTester interface {
	CheckStoredSourceConnectivity(context.Context, *model.DataSource) error
}

func (m *MCPServer) SetSourceConnectivityTester(t SourceConnectivityTester) {
	if m == nil || t == nil || m.datasourceService == nil || m.sourceConnectivityTester != nil {
		return
	}
	m.sourceConnectivityTester = t
	m.sourceConnectivitySlots = make(chan struct{}, 4)
	m.addTool("test_data_source_connection", mcp.NewTool("test_data_source_connection",
		mcp.WithDescription("Test a stored MySQL or PostgreSQL source without exposing configuration. Requires data-fabric.sources.manage and source ownership."),
		mcp.WithString("datasource_id", mcp.Required())), m.handleTestDataSourceConnection)
}
func (m *MCPServer) handleDeleteDataSource(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	caller := m.managementCaller(ctx)
	if caller == nil {
		auditlog.MarkDecision(ctx, auditlog.DecisionDeny, "source_management_scope_required")
		return mcp.NewToolResultError("source management permission required"), nil
	}
	id, name, ok := managementArgs(req.GetArguments(), "confirmation_name")
	if !ok || utf8.RuneCountInString(name) > 255 {
		return mcp.NewToolResultError("provide datasource_id and exact confirmation_name"), nil
	}
	mutator, ok := m.datasourceService.(service.ScopedSourceDeletion)
	if !ok {
		return mcp.NewToolResultError("source deletion unavailable"), nil
	}
	err := mutator.DeleteSourceScoped(ctx, caller, id, name)
	if errors.Is(err, repository.ErrDataSourceNotFound) || errors.Is(err, security.ErrDataSourceAccessDenied) {
		auditlog.MarkDecision(ctx, auditlog.DecisionDeny, "source_management_access_denied")
		return mcp.NewToolResultError("source is unavailable, confirmation does not match, or access is denied"), nil
	}
	if err != nil {
		return mcp.NewToolResultError("source could not be deleted"), nil
	}
	return mcpTablePolicyJSON(map[string]any{"id": id, "deleted": true})
}
func (m *MCPServer) handleTestDataSourceConnection(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	caller := m.managementCaller(ctx)
	if caller == nil {
		auditlog.MarkDecision(ctx, auditlog.DecisionDeny, "source_management_scope_required")
		return mcp.NewToolResultError("source management permission required"), nil
	}
	args := req.GetArguments()
	id, ok := args["datasource_id"].(string)
	if !ok || len(args) != 1 || strings.TrimSpace(id) == "" {
		return mcp.NewToolResultError("provide only datasource_id"), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	select {
	case m.sourceConnectivitySlots <- struct{}{}:
		defer func() { <-m.sourceConnectivitySlots }()
	default:
		return mcp.NewToolResultError("connection testing is busy; retry later"), nil
	}
	source, err := m.datasourceService.GetDataSource(ctx, caller, id)
	if err != nil || source == nil || (!caller.IsAdmin() && (source.OwnerID != caller.UserID || source.TenantID != caller.TenantID)) {
		auditlog.MarkDecision(ctx, auditlog.DecisionDeny, "source_management_access_denied")
		return mcp.NewToolResultError("source is unavailable or access is denied"), nil
	}
	if source.Type != model.DatabaseTypePostgreSQL && source.Type != model.DatabaseTypeMySQL {
		return mcp.NewToolResultError("connection testing supports MySQL and PostgreSQL only"), nil
	}
	// Certificate files can be special devices or FIFOs, so synchronous file
	// reads are not covered by the network deadline.
	if source.Config.SSL || source.Config.SSLRootCert != "" ||
		(source.Config.SSLMode != "" && source.Config.SSLMode != "disable") {
		return mcp.NewToolResultError("connection testing for TLS sources is not supported"), nil
	}
	if err = m.sourceConnectivityTester.CheckStoredSourceConnectivity(ctx, source); err != nil {
		return mcp.NewToolResultError("connection test failed; check source configuration and network"), nil
	}
	return mcpTablePolicyJSON(map[string]any{"id": id, "connected": true})
}
