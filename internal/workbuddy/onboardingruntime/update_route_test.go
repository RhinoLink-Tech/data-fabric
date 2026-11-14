package onboardingruntime

import (
	"github.com/RhinoLink-Tech/data-fabric/internal/workbuddy/onboardinghttp"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpdateRouteIsExplicitAndHostBound(t *testing.T) {
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(418) })
	route := routeUpdate(fallback, nil, nil, onboardinghttp.TransactionalAudit{}, nil, "example.test")
	path := "/onboarding/flows/" + strings.Repeat("A", 43) + "/update/34ee7a59-e839-4de8-b278-afc920c54136"
	req := httptest.NewRequest("GET", "https://example.test"+path, nil)
	w := httptest.NewRecorder()
	route.ServeHTTP(w, req)
	if w.Code != 200 || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("update page unavailable", w.Code)
	}
	req = httptest.NewRequest("GET", "https://other.test"+path, nil)
	w = httptest.NewRecorder()
	route.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatal("wrong host accepted", w.Code)
	}
	req = httptest.NewRequest("GET", "https://example.test/onboarding/session", nil)
	w = httptest.NewRecorder()
	route.ServeHTTP(w, req)
	if w.Code != 418 {
		t.Fatal("existing route intercepted", w.Code)
	}
}
