package mcp

import (
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStrictTenantHTTPPropagation(t *testing.T) {
	validator := staticMCPTokenValidator{claims: &security.Claims{UserID: "alice", TenantID: "team"}}
	for _, strict := range []bool{false, true} {
		var seen *security.Caller
		inner := authenticateMCPRequests(validator, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen = callerFromContext(r.Context())
			w.WriteHeader(204)
		}))
		var handler http.Handler = inner
		if strict {
			handler = StrictTenantHTTPHandler(inner)
		}
		r := httptest.NewRequest("POST", "https://example.test/mcp", nil)
		r.Header.Set("Authorization", "Bearer trusted-access-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 204 || seen == nil || seen.StrictTenantIsolation != strict {
			t.Fatalf("strict=%v status=%d caller=%v", strict, w.Code, seen)
		}
	}
}
