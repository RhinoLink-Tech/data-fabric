package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
)

// verifiedOnboardingIdentity is derived only from an already validated HTTP access token.
// It is not set by stdio or from model-provided arguments.
type verifiedOnboardingIdentity struct{ Issuer, Subject, Tenant string }

// OnboardingCoordinator must independently enforce durable per-principal and
// tenant quotas, bounded expiry cleanup, and audit. Status must re-check current
// permission and existence of a completed source; a flow ID never grants access.
// It must not return configuration, database credentials, or raw driver errors.
type OnboardingCoordinator interface {
	Begin(context.Context, onboarding.Principal, *security.Caller) (string, error)
	Status(context.Context, onboarding.Principal, *security.Caller, string) (state, sourceID string, err error)
}

// SetOnboardingCoordinator is deliberately opt-in and is not wired to the
// application until the browser session, authorization, quota, and audit paths
// exist. baseURL is an operator-configured HTTPS origin, not user-controlled.
func (m *MCPServer) SetOnboardingCoordinator(c OnboardingCoordinator, baseURL string) error {
	if m == nil || c == nil || m.onboardingCoordinator != nil {
		return errors.New("onboarding unavailable")
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.RawPath != "" || u.Opaque != "" || strings.TrimSpace(baseURL) != baseURL {
		return errors.New("onboarding requires an HTTPS origin")
	}
	m.onboardingBaseURL = strings.TrimSuffix(u.String(), "/")
	m.onboardingCoordinator = c
	m.addTool("begin_data_source_onboarding", mcp.NewTool("begin_data_source_onboarding", mcp.WithDescription("Begin adding a source through a separately authenticated browser. Requires source management permission. Enter credentials only in the browser, never in chat or tool arguments.")), m.handleBeginOnboarding)
	m.addTool("get_data_source_onboarding_status", mcp.NewTool("get_data_source_onboarding_status", mcp.WithDescription("Check the status of a source setup you initiated, subject to current authorization."), mcp.WithString("flow_id", mcp.Required())), m.handleOnboardingStatus)
	return nil
}

func (m *MCPServer) onboardingActor(ctx context.Context) (onboarding.Principal, *security.Caller, bool) {
	caller := m.managementCaller(ctx)
	identity, ok := ctx.Value(verifiedIdentityContextKey{}).(verifiedOnboardingIdentity)
	if !ok || caller == nil || identity.Issuer == "" || identity.Subject == "" || identity.Tenant == "" || identity.Subject != caller.UserID || identity.Tenant != caller.TenantID {
		return onboarding.Principal{}, nil, false
	}
	return onboarding.Principal{Issuer: identity.Issuer, Subject: identity.Subject, Tenant: identity.Tenant}, caller, true
}
func validOnboardingID(id string) bool {
	b, e := base64.RawURLEncoding.DecodeString(id)
	return e == nil && len(b) == 32 && base64.RawURLEncoding.EncodeToString(b) == id
}

func (m *MCPServer) handleBeginOnboarding(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	actor, caller, ok := m.onboardingActor(ctx)
	if !ok || m.onboardingCoordinator == nil {
		return mcp.NewToolResultError("source onboarding permission required"), nil
	}
	if len(req.GetArguments()) != 0 {
		return mcp.NewToolResultError("no arguments are accepted; enter credentials in the browser"), nil
	}
	id, err := m.onboardingCoordinator.Begin(ctx, actor, caller)
	if err != nil || !validOnboardingID(id) {
		return mcp.NewToolResultError("source onboarding is unavailable"), nil
	}
	return mcpTablePolicyJSON(map[string]any{"flow_id": id, "url": m.onboardingBaseURL + "/onboarding/flows/" + id, "message": "Sign in separately and enter credentials in your browser. The link does not grant access."})
}
func (m *MCPServer) handleOnboardingStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	actor, caller, ok := m.onboardingActor(ctx)
	if !ok || m.onboardingCoordinator == nil {
		return mcp.NewToolResultError("source onboarding permission required"), nil
	}
	args := req.GetArguments()
	id, ok := args["flow_id"].(string)
	if !ok || len(args) != 1 || !validOnboardingID(id) {
		return mcp.NewToolResultError("invalid onboarding flow"), nil
	}
	state, source, err := m.onboardingCoordinator.Status(ctx, actor, caller, id)
	if err != nil || (state != "pending" && state != "completed") || (state == "pending" && source != "") {
		return mcp.NewToolResultError("source onboarding is unavailable"), nil
	}
	if state == "completed" {
		parsed, e := uuid.Parse(source)
		if e != nil || parsed.String() != source {
			return mcp.NewToolResultError("source onboarding is unavailable"), nil
		}
		return mcpTablePolicyJSON(map[string]any{"status": state, "datasource_id": source})
	}
	return mcpTablePolicyJSON(map[string]any{"status": state})
}
