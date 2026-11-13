package mcp

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
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

// A subject may be reused across tenants: require an exact tenant match for
// non-admin owners before delegating the mutation to the existing service.
func (m *MCPServer) canManageSource(ctx context.Context, caller *security.Caller, id string) bool {
	source, err := m.datasourceService.GetDataSource(ctx, caller, id)
	if err != nil || source == nil {
		return false
	}
	return caller.IsAdmin() || (source.OwnerID == caller.UserID && source.TenantID == caller.TenantID)
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
	if !m.canManageSource(ctx, caller, id) {
		auditlog.MarkDecision(ctx, auditlog.DecisionDeny, "source_management_access_denied")
		return mcp.NewToolResultError("source is unavailable or access is denied"), nil
	}
	source, err := m.datasourceService.UpdateDataSource(ctx, caller, id, &service.UpdateDataSourceRequest{Name: &name})
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
	if !m.canManageSource(ctx, caller, id) {
		auditlog.MarkDecision(ctx, auditlog.DecisionDeny, "source_management_access_denied")
		return mcp.NewToolResultError("source is unavailable or access is denied"), nil
	}
	var err error
	if status == "active" {
		err = m.datasourceService.ActivateDataSource(ctx, caller, id)
	} else {
		err = m.datasourceService.DeactivateDataSource(ctx, caller, id)
	}
	if err != nil {
		return mcp.NewToolResultError("source status could not be changed; check access and identifier"), nil
	}
	return mcpTablePolicyJSON(map[string]any{"id": id, "status": status})
}
