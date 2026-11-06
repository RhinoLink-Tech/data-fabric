package replicarouting

import (
	"encoding/base64"
	"errors"
	"io"
	"net"
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

func TestIngressRoutesRESTContinuationAcrossTwoHTTPOwners(t *testing.T) {
	key := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("z", 32)))
	ring, err := ParseKeyRing("acceptance-site", `{"active":"`+key+`"}`)
	if err != nil {
		t.Fatal(err)
	}
	ownerA, err := ring.NewOwner("active", REST, "rest-a")
	if err != nil {
		t.Fatal(err)
	}
	ownerB, err := ring.NewOwner("active", REST, "rest-b")
	if err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Minute)
	slugA, err := ownerA.Mark("query-a", expiry)
	if err != nil {
		t.Fatal(err)
	}
	slugB, err := ownerB.Mark("query-b", expiry)
	if err != nil {
		t.Fatal(err)
	}
	var aHits, bHits atomic.Int32
	var failA atomic.Bool
	newOwner := func(slot string, hits *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if r.Method == http.MethodPost && r.URL.Path == "/api/v1/query" {
				slug := slugA
				if slot == "rest-b" {
					slug = slugB
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"owner":"`+slot+`","rows":["synthetic-1","synthetic-2"],"nextUri":"`+fetchPath+`/query-`+strings.TrimPrefix(slot, "rest-")+`/`+slug+`/token"}`)
				return
			}
			if strings.HasPrefix(r.URL.Path, fetchPath+"/") && r.Method == http.MethodGet {
				if slot == "rest-a" && failA.Load() {
					hijacker, ok := w.(http.Hijacker)
					if !ok {
						http.Error(w, "unexpected writer", http.StatusInternalServerError)
						return
					}
					conn, _, hijackErr := hijacker.Hijack()
					if hijackErr == nil {
						_ = conn.Close()
					}
					return
				}
				fields := strings.Split(strings.TrimPrefix(r.URL.Path, fetchPath+"/"), "/")
				if len(fields) != 3 {
					http.NotFound(w, r)
					return
				}
				verifiedSlot, verifyErr := ring.Verify(fields[1], REST, fields[0], time.Now())
				if verifyErr != nil || verifiedSlot != slot {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"owner":"`+slot+`","rows":["synthetic-3"]}`)
				return
			}
			http.NotFound(w, r)
		}))
	}
	serverA := newOwner("rest-a", &aHits)
	serverB := newOwner("rest-b", &bHits)
	t.Cleanup(serverA.Close)
	t.Cleanup(serverB.Close)
	cfg := Upstreams{REST: map[string]Upstream{
		"rest-a": {URL: serverA.URL, AcceptNew: true},
		"rest-b": {URL: serverB.URL},
	}}
	ingress, err := NewIngress(ring, cfg)
	if err != nil {
		t.Fatal(err)
	}
	public := httptest.NewServer(ingress)
	t.Cleanup(public.Close)
	conn, loopbackErr := net.DialTimeout("tcp", public.Listener.Addr().String(), time.Second)
	if loopbackErr != nil {
		t.Skipf("local TCP loopback is unavailable: %v", loopbackErr)
	}
	_ = conn.Close()
	client := public.Client()
	send := func(method, path string) (*http.Response, string) {
		t.Helper()
		resp, err := client.Do(mustRequest(t, method, public.URL+path))
		if err != nil {
			t.Fatalf("request through ingress: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp, string(body)
	}

	resp, body := send(http.MethodPost, "/api/v1/query")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"owner":"rest-a"`) {
		t.Fatalf("first query reached the wrong owner: %d %s", resp.StatusCode, body)
	}
	if err := ingress.Reload(Upstreams{REST: map[string]Upstream{
		"rest-a": {URL: serverA.URL},
		"rest-b": {URL: serverB.URL, AcceptNew: true},
	}}); err != nil {
		t.Fatal(err)
	}
	resp, body = send(http.MethodPost, "/api/v1/query")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"owner":"rest-b"`) {
		t.Fatalf("new query did not move to owner B: %d %s", resp.StatusCode, body)
	}
	resp, body = send(http.MethodGet, fetchPath+"/query-a/"+slugA+"/token")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"owner":"rest-a"`) || !strings.Contains(body, "synthetic-3") {
		t.Fatalf("continuation did not stay on owner A: %d %s", resp.StatusCode, body)
	}
	beforeA, beforeB := aHits.Load(), bHits.Load()
	resp, body = send(http.MethodGet, fetchPath+"/query-other/"+slugA+"/token")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "QUERY_SESSION_UNAVAILABLE") {
		t.Fatalf("mismatched query marker status/body = %d %q", resp.StatusCode, body)
	}
	if aHits.Load() != beforeA || bHits.Load() != beforeB {
		t.Fatal("mismatched marker reached an owner")
	}

	failA.Store(true)
	beforeA, beforeB = aHits.Load(), bHits.Load()
	resp, body = send(http.MethodGet, fetchPath+"/query-a/"+slugA+"/token")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "QUERY_SESSION_UNAVAILABLE") || aHits.Load() != beforeA+1 || bHits.Load() != beforeB {
		t.Fatalf("failed continuation status/hits A/B = %d/%d/%d, want 404/one/unchanged", resp.StatusCode, aHits.Load()-beforeA, bHits.Load()-beforeB)
	}
}

func mustRequest(t *testing.T, method, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	return req
}
