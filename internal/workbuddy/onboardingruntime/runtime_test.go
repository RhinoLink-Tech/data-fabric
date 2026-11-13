package onboardingruntime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	web "github.com/RhinoLink-Tech/data-fabric/internal/workbuddy/onboardinghttp"
)

func TestDisabledNeverEnablesRoutes(t *testing.T) {
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(418) })
	rt, err := New(context.Background(), Options{Fallback: fallback})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	w := httptest.NewRecorder()
	rt.Handler.ServeHTTP(w, httptest.NewRequest("GET", "https://example.test/onboarding/session", nil))
	if w.Code != 418 {
		t.Fatalf("got %d", w.Code)
	}
	if _, err = New(context.Background(), Options{Enabled: true, Fallback: fallback}); err == nil {
		t.Fatal("unconfigured enabled runtime accepted")
	}
}
func TestRestrictedProbeChecksPortAtCreationAndTest(t *testing.T) {
	policy, err := database.NewFixedIPEgressPolicy([]string{"127.0.0.0/8"}, []int{5432})
	if err != nil {
		t.Fatal(err)
	}
	probe, err := web.NewAllowedProbe([]string{"127.0.0.0/8"}, []int{5432}, 1)
	if err != nil {
		t.Fatal(err)
	}
	p := restrictedProbe{probe, policy}
	c := model.DataSourceConfig{Host: "127.0.0.1", Port: 5432, Username: "reader", Database: "orders"}
	if _, err = p.Pin(context.Background(), model.DatabaseTypePostgreSQL, c); err != nil {
		t.Fatal(err)
	}
	c.Port = 80
	if _, err = p.Pin(context.Background(), model.DatabaseTypePostgreSQL, c); err == nil {
		t.Fatal("disallowed port accepted on create")
	}
	if _, err = p.Test(context.Background(), model.DatabaseTypePostgreSQL, c); err == nil {
		t.Fatal("disallowed port accepted by probe")
	}
	c.Port = 5432
	c.Host = "192.0.2.1"
	if _, err = p.Pin(context.Background(), model.DatabaseTypePostgreSQL, c); err == nil {
		t.Fatal("disallowed address accepted")
	}
}
func TestRoutingProtectsOriginAndStaticAssets(t *testing.T) {
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(418) })
	h := route(fallback, nil, nil, "example.test")
	for _, tc := range []struct {
		method, path string
		code         int
	}{{"GET", "/onboarding/assets/onboarding.js", 200}, {"POST", "/onboarding/assets/onboarding.js", 405}, {"GET", "/onboarding/flows/" + strings.Repeat("a", 43), 200}, {"GET", "/onboarding/assets/../../etc/passwd", 404}, {"GET", "/mcp", 418}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, "https://example.test"+tc.path, nil))
		if w.Code != tc.code {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
		if tc.code == 200 && (w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Security-Policy") == "") {
			t.Fatal("missing browser security headers")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://attacker.test/onboarding/assets/onboarding.js", nil))
	if w.Code != 400 {
		t.Fatal("wrong host accepted")
	}
}
