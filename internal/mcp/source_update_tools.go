package mcp

import (
	"context"
	"errors"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
)

// SourceUpdateCoordinator is responsible for current source ownership and grant
// checks, durable quotas, source-bound intent and transactional auditing.
type SourceUpdateCoordinator interface {
	BeginUpdate(context.Context, onboarding.Principal, *security.Caller, string) (string, error)
	UpdateStatus(context.Context, onboarding.Principal, *security.Caller, string, string) (string, error)
}

// SetSourceUpdateCoordinator is opt-in and intentionally not registered by
// runtime until browser authentication, egress, lease consistency and actual
// client validation are independently complete.
func (m *MCPServer) SetSourceUpdateCoordinator(c SourceUpdateCoordinator) error {
	if m == nil || c == nil || m.updateCoordinator != nil || m.onboardingCoordinator == nil || m.onboardingBaseURL == "" {
		return errors.New("source update unavailable")
	}
	m.updateCoordinator = c
	m.addTool("begin_data_source_update", mcp.NewTool("begin_data_source_update", mcp.WithDescription("Begin editing connection settings through a separately authenticated browser. Never provide database credentials in chat or tool arguments."), mcp.WithString("datasource_id", mcp.Required())), m.handleBeginSourceUpdate)
	m.addTool("get_data_source_update_status", mcp.NewTool("get_data_source_update_status", mcp.WithDescription("Check the status of an authorized source update."), mcp.WithString("flow_id", mcp.Required()), mcp.WithString("datasource_id", mcp.Required())), m.handleSourceUpdateStatus)
	return nil
}
func validUpdateSource(id string) bool { u, e := uuid.Parse(id); return e == nil && u.String() == id }
func (m *MCPServer) handleBeginSourceUpdate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p, caller, ok := m.onboardingActor(ctx)
	args := req.GetArguments()
	id, valid := args["datasource_id"].(string)
	if !ok || m.updateCoordinator == nil || len(args) != 1 || !valid || !validUpdateSource(id) {
		return mcp.NewToolResultError("source update unavailable"), nil
	}
	flow, e := m.updateCoordinator.BeginUpdate(ctx, p, caller, id)
	if e != nil || !validOnboardingID(flow) {
		return mcp.NewToolResultError("source update unavailable"), nil
	}
	return mcpTablePolicyJSON(map[string]any{"flow_id": flow, "datasource_id": id, "url": m.onboardingBaseURL + "/onboarding/flows/" + flow + "/update/" + id, "message": "Sign in separately and enter connection settings in your browser. This link does not grant access."})
}
func (m *MCPServer) handleSourceUpdateStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	p, caller, ok := m.onboardingActor(ctx)
	args := req.GetArguments()
	id, a := args["datasource_id"].(string)
	flow, b := args["flow_id"].(string)
	if !ok || m.updateCoordinator == nil || len(args) != 2 || !a || !b || !validUpdateSource(id) || !validOnboardingID(flow) {
		return mcp.NewToolResultError("source update unavailable"), nil
	}
	state, e := m.updateCoordinator.UpdateStatus(ctx, p, caller, flow, id)
	if e != nil || (state != "pending" && state != "completed") {
		return mcp.NewToolResultError("source update unavailable"), nil
	}
	return mcpTablePolicyJSON(map[string]any{"status": state, "datasource_id": id})
}
