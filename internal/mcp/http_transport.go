package mcp

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/mark3labs/mcp-go/server"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

type callerContextKey struct{}

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
	transport := server.NewStreamableHTTPServer(m.server)
	mux := http.NewServeMux()
	mux.Handle("/mcp", authenticateMCPRequests(tokenValidator, transport))
	return mux
}

func authenticateMCPRequests(tokenValidator MCPTokenValidator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tokenValidator == nil {
			writeMCPUnauthorized(w)
			return
		}
		token, err := security.ExtractBearerToken(r.Header.Get("Authorization"))
		if err != nil {
			writeMCPUnauthorized(w)
			return
		}
		claims, err := tokenValidator.ValidateMCPToken(r.Context(), token)
		if err != nil || claims == nil || claims.UserID == "" {
			writeMCPUnauthorized(w)
			return
		}

		caller := security.CallerFromClaims(claims)
		ctx := context.WithValue(r.Context(), callerContextKey{}, caller)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
