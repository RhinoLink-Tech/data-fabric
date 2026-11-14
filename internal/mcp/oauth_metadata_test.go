package mcp

import (
	"encoding/json"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestProtectedResourceMetadataDiscovery(t *testing.T) {
	const resource = "https://fabric.example/mcp"
	m, err := NewProtectedResourceMetadata(resource, "https://identity.example/realm", resource)
	if err != nil {
		t.Fatal(err)
	}
	handler := m.Wrap(authenticateMCPRequests(nil, http.NotFoundHandler()))
	for _, path := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource"} {
		req := httptest.NewRequest(http.MethodGet, "https://fabric.example"+path, nil)
		req.Header.Set("X-Forwarded-Host", "attacker.example")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		var body map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["resource"] != resource {
			t.Fatalf("invalid metadata %d %s", w.Code, w.Body.String())
		}
		servers := body["authorization_servers"].([]any)
		if len(servers) != 1 || servers[0] != "https://identity.example/realm" {
			t.Fatal("wrong issuer")
		}
	}
	req := httptest.NewRequest(http.MethodPost, resource, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") != "Bearer resource_metadata="+strconv.Quote("https://fabric.example/.well-known/oauth-protected-resource/mcp") {
		t.Fatalf("challenge mismatch: %d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
	req = httptest.NewRequest(http.MethodGet, "https://attacker.example/.well-known/oauth-protected-resource/mcp", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 400 || strings.Contains(w.Body.String(), "attacker.example") {
		t.Fatal("untrusted host accepted")
	}
	req = httptest.NewRequest(http.MethodPost, "https://fabric.example/.well-known/oauth-protected-resource/mcp", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != 405 {
		t.Fatal("metadata method not rejected")
	}
}

func TestProtectedResourceMetadataRejectsMisconfiguration(t *testing.T) {
	for _, x := range []struct{ resource, issuer, audience string }{
		{"http://fabric.example/mcp", "https://id.example", "http://fabric.example/mcp"},
		{"https://fabric.example/mcp?token=x", "https://id.example", "https://fabric.example/mcp?token=x"},
		{"https://fabric.example/mcp", "https://id.example", "data-fabric-mcp"},
		{"https://fabric.example/mcp", "http://id.example", "https://fabric.example/mcp"},
		{"https://user@fabric.example/mcp", "https://id.example", "https://user@fabric.example/mcp"},
	} {
		if _, err := NewProtectedResourceMetadata(x.resource, x.issuer, x.audience); err == nil {
			t.Fatalf("accepted unsafe config %+v", x)
		}
	}
}

func TestOAuthDiscoveryValidatesHostBeforeLoopbackNormalization(t *testing.T) {
	m, err := NewProtectedResourceMetadata("https://fabric.example/mcp", "https://issuer.example", "https://fabric.example/mcp")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewMCPServer(nil, nil, nil).StreamableHTTPHandler(staticMCPTokenValidator{claims: &security.Claims{UserID: "synthetic"}})
	server := httptest.NewServer(m.Wrap(handler))
	defer server.Close()
	body := "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":\"2025-03-26\",\"capabilities\":{},\"clientInfo\":{\"name\":\"test\",\"version\":\"1\"}}}"
	call := func(host string) int {
		req, e := http.NewRequest("POST", server.URL+"/mcp", strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		req.Host = host
		req.Header.Set("Authorization", "Bearer trusted-access-token")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("X-Forwarded-Host", "fabric.example")
		res, e := server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	if got := call("fabric.example"); got != 200 {
		t.Fatalf("valid Host through real loopback listener returned %d", got)
	}
	if got := call("attacker.example"); got != 400 {
		t.Fatalf("rebind Host returned %d", got)
	}
}
