package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/server"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/middleware"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

type strictTenantContextKey struct{}

// StrictTenantHTTPHandler enables tenant isolation for all requests in an
// explicitly configured deployment. It must wrap the authenticated MCP handler.
func StrictTenantHTTPHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), strictTenantContextKey{}, true)))
	})
}

type callerContextKey struct{}
type verifiedIdentityContextKey struct{}
type auditCorrelationIDKey struct{}
type auditTransportKey struct{}

func callerFromContext(ctx context.Context) *security.Caller {
	if caller, ok := ctx.Value(callerContextKey{}).(*security.Caller); ok && caller != nil {
		return caller
	}
	// stdio is a trusted local process and retains its administrator-equivalent
	// behavior. Every HTTP request is authenticated before reaching MCP tools.
	return security.SystemCaller()
}

func queryUserFromContext(ctx context.Context) string {
	caller := callerFromContext(ctx)
	if caller.UserID == security.SystemUserID {
		return "mcp"
	}
	return caller.UserID
}

// MCPTokenValidator validates bearer tokens for the remote MCP transport.
type MCPTokenValidator interface {
	ValidateMCPToken(context.Context, string) (*security.Claims, error)
}

// StreamableHTTPHandler exposes MCP over Streamable HTTP and propagates the
// validated bearer-token identity to tool handlers.
func (m *MCPServer) StreamableHTTPHandler(tokenValidator MCPTokenValidator) http.Handler {
	transport := server.NewStreamableHTTPServer(m.server, server.WithStreamableHTTPLogger(safeMCPTransportLogger()))
	mux := http.NewServeMux()
	mux.Handle("/mcp", authenticateMCPRequests(tokenValidator, transport, m.auditRecorder))
	mux.Handle("/metrics", m.adminMetricsHandler(tokenValidator))
	return mux
}

func (m *MCPServer) adminMetricsHandler(tokenValidator MCPTokenValidator) http.Handler {
	metrics := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller, _ := r.Context().Value(callerContextKey{}).(*security.Caller)
		if caller == nil || !caller.IsAdmin() {
			writeRemoteError(w, http.StatusForbidden, "forbidden", "administrator role required")
			return
		}
		middleware.PrometheusHandler().ServeHTTP(w, r)
	})
	return authenticateMCPRequests(tokenValidator, metrics, m.auditRecorder)
}

func authenticateMCPRequests(tokenValidator MCPTokenValidator, next http.Handler, recorders ...auditlog.Recorder) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := r.Header.Get("X-Correlation-ID")
		if correlationID == "" {
			correlationID = uuid.NewString()
		}
		w.Header().Set("X-Correlation-ID", correlationID)
		ctx := context.WithValue(r.Context(), auditCorrelationIDKey{}, correlationID)
		ctx = context.WithValue(ctx, auditTransportKey{}, "mcp-http")
		r = r.WithContext(ctx)
		var recorder auditlog.Recorder
		if len(recorders) > 0 {
			recorder = recorders[0]
		}
		if tokenValidator == nil {
			recordMCPAuthFailure(recorder, r.Context(), correlationID, "token_validator_unavailable")
			writeMCPUnauthorized(w)
			return
		}
		token, err := security.ExtractBearerToken(r.Header.Get("Authorization"))
		if err != nil {
			recordMCPAuthFailure(recorder, r.Context(), correlationID, "missing_or_invalid_bearer_token")
			writeMCPUnauthorized(w)
			return
		}
		claims, err := tokenValidator.ValidateMCPToken(r.Context(), token)
		if err != nil || claims == nil || claims.UserID == "" {
			recordMCPAuthFailure(recorder, r.Context(), correlationID, "token_validation_failed")
			writeMCPUnauthorized(w)
			return
		}

		caller := security.CallerFromClaims(claims)
		caller.StrictTenantIsolation, _ = r.Context().Value(strictTenantContextKey{}).(bool)
		recordMCPAuthSuccess(recorder, r.Context(), correlationID, caller)
		ctx = context.WithValue(r.Context(), callerContextKey{}, caller)
		ctx = context.WithValue(ctx, verifiedIdentityContextKey{}, verifiedOnboardingIdentity{Issuer: claims.Issuer, Subject: claims.Subject, Tenant: claims.TenantID})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func recordMCPAuthFailure(recorder auditlog.Recorder, ctx context.Context, correlationID, reason string) {
	if recorder == nil {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := recorder.Record(writeCtx, auditlog.Event{
		CorrelationID: correlationID, Transport: "mcp-http", Operation: "mcp.authenticate",
		Decision: auditlog.DecisionDeny, ReasonCode: reason, Outcome: "denied",
		StatusCode: http.StatusUnauthorized, OccurredAt: time.Now().UTC(),
	}); err != nil {
		middleware.RecordAuditPersistenceFailure("mcp-http")
		slog.ErrorContext(writeCtx, "failed to persist MCP authentication audit event", "correlation_id", correlationID, "error", err)
	}
}

func recordMCPAuthSuccess(recorder auditlog.Recorder, ctx context.Context, correlationID string, caller *security.Caller) {
	if recorder == nil || caller == nil {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := recorder.Record(writeCtx, auditlog.Event{
		CorrelationID: correlationID, ActorID: caller.UserID, TenantID: caller.TenantID,
		Transport: "mcp-http", Operation: "mcp.authenticate", Decision: auditlog.DecisionAllow,
		ReasonCode: "token_validated", Outcome: "success", StatusCode: http.StatusOK,
		OccurredAt: time.Now().UTC(),
	}); err != nil {
		middleware.RecordAuditPersistenceFailure("mcp-http")
		slog.ErrorContext(writeCtx, "failed to persist MCP authentication audit event", "correlation_id", correlationID, "error", err)
	}
}

func writeMCPUnauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":   "unauthorized",
		"message": "a valid Bearer token is required",
	})
}
