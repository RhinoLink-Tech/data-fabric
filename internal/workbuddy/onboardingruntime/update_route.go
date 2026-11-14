package onboardingruntime

import (
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	web "github.com/RhinoLink-Tech/data-fabric/internal/workbuddy/onboardinghttp"
	"net/http"
	"strings"
)

// routeUpdate is only connected by the internal, default-off experiment.
// Advisory locks do not provide fencing; it must not be enabled for external
// use before actual operation leases and loss handling are independently reviewed.
func routeUpdate(next http.Handler, manager *web.Handler, flow *onboarding.Store, audit web.TransactionalAudit, pool *database.ConnectionPool, originHost string) http.Handler {
	slots := make(chan struct{}, 16)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.URL.Path, "/onboarding/flows/")
		if raw == r.URL.Path {
			next.ServeHTTP(w, r)
			return
		}
		parts := strings.Split(raw, "/")
		if len(parts) < 3 || parts[1] != "update" {
			next.ServeHTTP(w, r)
			return
		}
		if !strings.EqualFold(r.Host, originHost) || len(r.URL.RequestURI()) > 8192 {
			http.Error(w, "request rejected", 400)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "service busy", 429)
			return
		}
		if len(parts) == 3 && len(parts[0]) == 43 && r.Method == "GET" {
			b, e := assets.ReadFile("ui/index.html")
			if e != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(b)
			return
		}
		if len(parts) == 4 && len(parts[0]) == 43 && (parts[3] == "test" || parts[3] == "save") {
			action := parts[3]
			if action == "save" {
				action = "update"
			}
			manager.ServeUpdateFlow(w, r, flow, audit, pool, parts[0], parts[2], action)
			return
		}
		http.NotFound(w, r)
	})
}
