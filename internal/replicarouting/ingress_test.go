package replicarouting

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIngressKeepsContinuationsOnOwner(t *testing.T) {
	key := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	ring, err := ParseKeyRing("site", `{"active":"`+key+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := ring.NewOwner("active", REST, "a")
	if err != nil {
		t.Fatal(err)
	}
	mcpOwner, err := ring.NewOwner("active", MCP, "a")
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Minute)
	slug, err := owner.Mark("query-id", expiry)
	if err != nil {
		t.Fatal(err)
	}
	mcpSession, err := mcpOwner.Mark("session-id", expiry)
	if err != nil {
		t.Fatal(err)
	}
	var aHits, bHits atomic.Int32
	var dropA atomic.Bool
	aURL := "http://owner-a:8080"
	bURL := "http://owner-b:8080"
	transport := testRoundTrip(func(r *http.Request) (*http.Response, error) {
		body := "A"
		switch r.URL.Host {
		case "owner-a:8080":
			aHits.Add(1)
			if dropA.Load() {
				return nil, io.EOF
			}
		case "owner-b:8080":
			bHits.Add(1)
			body = "B"
		default:
			return nil, errors.New("unexpected upstream")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Cache-Control": []string{"public, max-age=120"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})
	cfg := Upstreams{
		REST: map[string]Upstream{"a": {URL: aURL, AcceptNew: true}, "b": {URL: bURL}},
		MCP:  map[string]Upstream{"a": {URL: aURL, AcceptNew: true}, "b": {URL: bURL}},
	}
	ingress, err := NewIngress(ring, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ingress.newRequests, ingress.continuations = transport, transport
	if err := ingress.Reload(cfg); err != nil {
		t.Fatal(err)
	}
	send := func(method, uri, session string, status int, body string) http.Header {
		t.Helper()
		req := httptest.NewRequest(method, uri, nil)
		if session != "" {
			req.Header.Set(sessionHeader, session)
		}
		resp := httptest.NewRecorder()
		ingress.ServeHTTP(resp, req)
		if resp.Code != status || !strings.Contains(resp.Body.String(), body) {
			t.Errorf("got %d %q, want %d containing %q", resp.Code, resp.Body.String(), status, body)
		}
		return resp.Header()
	}
	send(http.MethodPost, fetchPath, "", http.StatusOK, "A")
	send(http.MethodPost, mcpPath, "", http.StatusOK, "A")
	for _, pool := range []map[string]Upstream{cfg.REST, cfg.MCP} {
		pool["a"] = Upstream{URL: aURL}
		pool["b"] = Upstream{URL: bURL, AcceptNew: true}
	}
	if err := ingress.Reload(cfg); err != nil {
		t.Fatal(err)
	}
	send(http.MethodPost, fetchPath, "", http.StatusOK, "B")
	send(http.MethodPost, mcpPath, "", http.StatusOK, "B")
	nextURI := fetchPath + "/query-id/" + slug + "/token"
	if cache := send(http.MethodGet, nextURI, "", http.StatusOK, "A").Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("continuation is cacheable: %q", cache)
	}
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		send(method, mcpPath, mcpSession, http.StatusOK, "A")
	}
	before := aHits.Load() + bHits.Load()
	send(http.MethodGet, fetchPath+"/different/"+slug+"/token", "", 404, "QUERY_SESSION_UNAVAILABLE")
	send(http.MethodGet, fetchPath+"//"+slug+"/token", "", 404, "QUERY_SESSION_UNAVAILABLE")
	send(http.MethodGet, mcpPath, slug, 404, "Not Found")
	send(http.MethodGet, mcpPath, "", 400, "required")
	send(http.MethodPost, "/api/v1/../../internal/replica/drain", "", 404, "404")
	send(http.MethodPost, "/internal/replica/drain", "", 404, "404")
	if got := aHits.Load() + bHits.Load(); got != before {
		t.Fatalf("invalid requests reached an owner: before %d, after %d", before, got)
	}

	bad := Upstreams{REST: map[string]Upstream{"b": {URL: bURL + "/internal/replica"}}}
	if err := ingress.Reload(bad); err == nil {
		t.Fatal("reload accepted an upstream path")
	}
	send(http.MethodPost, fetchPath, "", http.StatusOK, "B")
	dropA.Store(true)
	aBefore, bBefore := aHits.Load(), bHits.Load()
	send(http.MethodGet, nextURI, "", 404, "QUERY_SESSION_UNAVAILABLE")
	if aHits.Load() != aBefore+1 || bHits.Load() != bBefore {
		t.Fatal("failed continuation was retried or went to the wrong owner")
	}
}

type testRoundTrip func(*http.Request) (*http.Response, error)

func (f testRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
