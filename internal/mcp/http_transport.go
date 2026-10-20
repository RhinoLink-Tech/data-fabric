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

// StreamableHTTPHandler exposes the MCP tools using the MCP Streamable HTTP
// transport. It requires a valid Authorization: Bearer JWT on every HTTP
// method and propagates the validated identity to tool handlers.
func (m *MCPServer) StreamableHTTPHandler(jwtManager *security.JWTManager) http.Handler {
	transport := server.NewStreamableHTTPServer(m.server)
	mux := http.NewServeMux()
	mux.Handle("/mcp", authenticateMCPRequests(jwtManager, transport))
	return mux
}

func authenticateMCPRequests(jwtManager *security.JWTManager, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if jwtManager == nil {
			writeMCPUnauthorized(w)
			return
		}
		token, err := jwtManager.ExtractTokenFromHeader(r.Header.Get("Authorization"))
		if err != nil {
			writeMCPUnauthorized(w)
			return
		}
		claims, err := jwtManager.ValidateToken(token)
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
