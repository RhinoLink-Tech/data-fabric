package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/mark3labs/mcp-go/mcp"
)

const maxMCPTablePolicyBytes = 1 << 20

func (m *MCPServer) registerTablePolicyTools() {
	statusTool := mcp.NewTool("get_table_policy",
		mcp.WithDescription("Read the current table/column policy, active revision, and replica freshness. Set revision to inspect an older version for rollback. Administrator role required."),
		mcp.WithNumber("revision", mcp.Description("Optional historical revision to inspect; omit for the active policy.")))
	m.addTool("get_table_policy", statusTool, m.handleGetTablePolicy)

	validateTool := mcp.NewTool("validate_table_policy",
		mcp.WithDescription("Validate a complete enabled table/column policy without publishing it. Administrator role required."),
		mcp.WithString("policy_json", mcp.Required(), mcp.Description("Complete TableAuthorizationConfig JSON object.")))
	m.addTool("validate_table_policy", validateTool, m.handleValidateTablePolicy)

	publishTool := mcp.NewTool("publish_table_policy",
		mcp.WithDescription("Atomically publish a validated table/column policy using compare-and-swap. A rollback is a new publication containing the previous policy. Administrator role required."),
		mcp.WithNumber("expected_revision", mcp.Required(), mcp.Description("Current revision returned by get_table_policy.")),
		mcp.WithString("policy_json", mcp.Required(), mcp.Description("Complete TableAuthorizationConfig JSON object.")))
	m.addTool("publish_table_policy", publishTool, m.handlePublishTablePolicy)

	historyTool := mcp.NewTool("list_table_policy_publications",
		mcp.WithDescription("List recent policy publication outcomes, including publisher and revision. Administrator role required."),
		mcp.WithNumber("limit", mcp.Description("Maximum entries from 1 to 100; default 50.")))
	m.addTool("list_table_policy_publications", historyTool, m.handleListTablePolicyPublications)
}

func (m *MCPServer) tablePolicyAdmin(ctx context.Context) (*security.Caller, error) {
	caller := callerFromContext(ctx)
	if caller == nil || !caller.IsAdmin() {
		return nil, fmt.Errorf("administrator role required")
	}
	if m == nil || m.tablePolicyManager == nil {
		return nil, fmt.Errorf("managed table policy is unavailable")
	}
	return caller, nil
}

func decodeMCPTablePolicy(args map[string]any) (security.TableAuthorizationConfig, error) {
	raw, ok := args["policy_json"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return security.TableAuthorizationConfig{}, fmt.Errorf("policy_json is required")
	}
	if len(raw) > maxMCPTablePolicyBytes {
		return security.TableAuthorizationConfig{}, fmt.Errorf("policy_json exceeds 1 MiB")
	}
	var policy security.TableAuthorizationConfig
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return security.TableAuthorizationConfig{}, fmt.Errorf("policy_json must be a valid policy object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return security.TableAuthorizationConfig{}, fmt.Errorf("policy_json must contain one JSON value")
	}
	return policy, nil
}

func mcpTablePolicyJSON(value any) (*mcp.CallToolResult, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode table policy response: %w", err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(string(encoded))}}, nil
}

func (m *MCPServer) handleGetTablePolicy(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if _, err := m.tablePolicyAdmin(ctx); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	snapshot := m.tablePolicyManager.CurrentSnapshot()
	if raw, exists := request.GetArguments()["revision"]; exists {
		revision, ok := mcpPolicyRevision(raw)
		if !ok || revision <= 0 {
			return mcp.NewToolResultError("revision must be a positive integer"), nil
		}
		historical, err := m.tablePolicyManager.GetRevision(ctx, revision)
		if err != nil {
			return mcp.NewToolResultError("table policy revision was not found"), nil
		}
		snapshot = historical
	}
	return mcpTablePolicyJSON(map[string]any{
		"status":   m.tablePolicyManager.Status(),
		"policy":   snapshot.Config,
		"revision": snapshot.Revision,
	})
}

func (m *MCPServer) handleValidateTablePolicy(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if _, err := m.tablePolicyAdmin(ctx); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	policy, err := decodeMCPTablePolicy(request.GetArguments())
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if err := m.tablePolicyManager.Validate(policy); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	return mcpTablePolicyJSON(map[string]any{
		"valid": true, "expectedRevision": m.tablePolicyManager.Status().Revision,
		"propagationWindow": security.TablePolicyPropagationWindow.String(),
	})
}

func (m *MCPServer) handlePublishTablePolicy(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	caller, err := m.tablePolicyAdmin(ctx)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	args := request.GetArguments()
	expectedRevision, ok := mcpPolicyRevision(args["expected_revision"])
	if !ok || expectedRevision <= 0 {
		return mcp.NewToolResultError("expected_revision must be a positive integer"), nil
	}
	policy, err := decodeMCPTablePolicy(args)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	result, err := m.tablePolicyManager.Publish(ctx, expectedRevision, caller.UserID, policy)
	if err != nil {
		switch {
		case errors.Is(err, security.ErrTablePolicyRevisionConflict):
			return mcp.NewToolResultError("table policy revision conflict; read the current revision and retry"), nil
		case errors.Is(err, security.ErrTablePolicyInvalid):
			return mcp.NewToolResultError(err.Error()), nil
		case errors.Is(err, security.ErrTablePolicyAuditUnavailable):
			return mcp.NewToolResultError("policy publication audit could not be recorded; no policy was activated"), nil
		default:
			return mcp.NewToolResultError("policy publication failed; no success was reported"), nil
		}
	}
	return mcpTablePolicyJSON(result)
}

func (m *MCPServer) handleListTablePolicyPublications(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if _, err := m.tablePolicyAdmin(ctx); err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	limit := 50
	if raw, exists := request.GetArguments()["limit"]; exists {
		value, ok := mcpPolicyRevision(raw)
		if !ok {
			return mcp.NewToolResultError("limit must be an integer between 1 and 100"), nil
		}
		limit = int(value)
	}
	if limit < 1 || limit > 100 {
		return mcp.NewToolResultError("limit must be an integer between 1 and 100"), nil
	}
	events, err := m.tablePolicyManager.Publications(ctx, limit)
	if err != nil {
		return mcp.NewToolResultError("table policy publication history is unavailable"), nil
	}
	return mcpTablePolicyJSON(map[string]any{"publications": events})
}

func mcpPolicyRevision(value any) (int64, bool) {
	switch number := value.(type) {
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number >= float64(math.MaxInt64) || number < float64(math.MinInt64) {
			return 0, false
		}
		return int64(number), true
	case int:
		return int64(number), true
	case int64:
		return number, true
	case json.Number:
		parsed, err := number.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}
