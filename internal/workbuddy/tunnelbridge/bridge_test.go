package tunnelbridge

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandlerRestrictsPathsAndPreservesAuthorization(t *testing.T) {
	called := 0
	handler := Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.Header.Get("Authorization") != "Bearer secret" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Real-IP") != "" {
			t.Error("unexpected header handling")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct {
		path   string
		status int
	}{
		{"/mcp", 204}, {"/onboarding/auth/callback?code=secret&state=secret", 204},
		{"/.well-known/oauth-protected-resource", 204}, {"/.well-known/oauth-protected-resource/mcp", 204}, {"/.well-known/oauth-protected-resource/other", 404},
		{"/metrics", 404}, {"/internal/replica", 404}, {"/mcp/extra", 404}, {"/onboarding", 404},
	} {
		r := httptest.NewRequest("GET", tc.path, nil)
		r.Header.Set("Authorization", "Bearer secret")
		r.Header.Set("X-Forwarded-For", "forged")
		r.Header.Set("X-Real-IP", "forged")
		r.Header.Set("Forwarded", "forged")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Errorf("%s: got %d, want %d", tc.path, w.Code, tc.status)
		}
	}
	if called != 4 {
		t.Errorf("forwarded %d requests, want 4", called)
	}
}
