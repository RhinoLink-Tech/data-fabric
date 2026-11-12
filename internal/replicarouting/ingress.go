package replicarouting

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/pkg/response"
)

const (
	maxIngressConcurrentRequests = 128
	fetchPath                    = "/api/v1/fetch"
	mcpPath                      = "/mcp"
	sessionHeader                = "Mcp-Session-Id"
)

// Upstream maps an owner slot to its operator-configured private URL.
type Upstream struct {
	URL       string `json:"url"`
	AcceptNew bool   `json:"accept_new"`
}

type Upstreams struct {
	REST map[string]Upstream `json:"rest_upstreams"`
	MCP  map[string]Upstream `json:"mcp_upstreams"`
}

type proxyPool struct {
	fresh  []*httputil.ReverseProxy
	owners map[string]*httputil.ReverseProxy
	next   atomic.Uint64
}

type routes struct {
	rest, mcp *proxyPool
}

// Ingress proxies only the public endpoints. An operator can update the
// allowlist without discarding live continuations on retained slots.
type Ingress struct {
	ring          *KeyRing
	newRequests   http.RoundTripper
	continuations http.RoundTripper
	active        atomic.Pointer[routes]
	inFlight      chan struct{}
}

func NewIngress(ring *KeyRing, config Upstreams) (*Ingress, error) {
	if ring == nil {
		return nil, errors.New("routing verification keys are required")
	}
	i := &Ingress{
		ring: ring, newRequests: newTransport(false), continuations: newTransport(true),
		inFlight: make(chan struct{}, maxIngressConcurrentRequests),
	}
	if err := i.Reload(config); err != nil {
		return nil, err
	}
	return i, nil
}

func (i *Ingress) Reload(config Upstreams) error {
	rest, err := i.buildPool(REST, config.REST)
	if err != nil {
		return err
	}
	mcp, err := i.buildPool(MCP, config.MCP)
	if err != nil {
		return err
	}
	i.active.Store(&routes{rest: rest, mcp: mcp})
	return nil
}

func (i *Ingress) buildPool(pool string, config map[string]Upstream) (*proxyPool, error) {
	if len(config) > 64 {
		return nil, fmt.Errorf("%s pool must contain at most 64 slots", pool)
	}
	slots := make([]string, 0, len(config))
	for slot := range config {
		slots = append(slots, slot)
	}
	sort.Strings(slots)
	p := &proxyPool{owners: make(map[string]*httputil.ReverseProxy, len(slots))}
	for _, slot := range slots {
		entry := config[slot]
		if !validLabel(slot, 32) {
			return nil, fmt.Errorf("%s pool has an invalid slot", pool)
		}
		target, err := url.Parse(entry.URL)
		if err != nil || target.Hostname() == "" || !privateOriginHost(target.Hostname()) ||
			(target.Scheme != "http" && target.Scheme != "https") ||
			target.User != nil || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" ||
			(target.Path != "" && target.Path != "/") || target.RawPath != "" {
			return nil, fmt.Errorf("%s slot %q needs a private HTTP(S) origin", pool, slot)
		}
		p.owners[slot] = newProxy(target, pool, true, i.continuations)
		if entry.AcceptNew {
			p.fresh = append(p.fresh, newProxy(target, pool, false, i.newRequests))
		}
	}
	return p, nil
}

func privateOriginHost(host string) bool {
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate()
	}
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	if name == "" || strings.ContainsAny(name, "%[]") {
		return false
	}
	labels := strings.Split(name, ".")
	for _, label := range labels {
		if len(label) == 0 || len(label) > 63 ||
			!isLowerAlphaNumeric(label[0]) || !isLowerAlphaNumeric(label[len(label)-1]) {
			return false
		}
		for _, char := range label {
			if !isLowerAlphaNumeric(byte(char)) && char != '-' {
				return false
			}
		}
	}
	if len(labels) == 1 {
		// Single-label hosts are resolved only inside an operator's private
		// service network, such as a Compose or Kubernetes namespace.
		return true
	}
	for _, suffix := range []string{".internal", ".local", ".localhost", ".svc", ".cluster.local"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

func isLowerAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func newTransport(continuation bool) *http.Transport {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: 30 * time.Minute,
	}
	if continuation {
		// A continuation GET advances the cursor. Avoid Transport retrying
		// a replayable request after a reused connection or HTTP/2 failure.
		transport.DisableKeepAlives = true
		transport.Protocols = new(http.Protocols)
		transport.Protocols.SetHTTP1(true)
	}
	return transport
}

func newProxy(target *url.URL, pool string, continuation bool, transport http.RoundTripper) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport: transport,
		ErrorLog:  log.New(io.Discard, "", 0),
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			// A client cannot choose the identity used by downstream rate
			// limiting through a forged forwarding header.
			r.Out.Header.Del("X-Forwarded-For")
			r.Out.Header.Del("X-Real-IP")
			r.SetXForwarded()
		},
		ModifyResponse: func(r *http.Response) error {
			if continuation {
				r.Header.Set("Cache-Control", "no-store")
			}
			if r.StatusCode >= 300 && r.StatusCode < 400 && r.Header.Get("Location") != "" {
				return errors.New("upstream redirect")
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			if continuation {
				unavailable(w, pool)
				return
			}
			noOwner(w, pool)
		},
	}
}

func (i *Ingress) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	recorder := &ingressResponseWriter{ResponseWriter: w}
	defer func() {
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		log.Printf("ingress request method=%s route=%s status=%d elapsed=%s peer=%s",
			r.Method, ingressRouteTemplate(r.URL.Path), status, time.Since(started), requestPeer(r.RemoteAddr))
	}()
	w = recorder
	w.Header().Set("Cache-Control", "no-store")
	select {
	case i.inFlight <- struct{}{}:
		defer func() { <-i.inFlight }()
	default:
		w.Header().Set("Retry-After", "1")
		if r.URL.Path == mcpPath {
			w.Header().Set("Cache-Control", "no-store")
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		} else {
			writeError(w, REST, http.StatusServiceUnavailable, "QUERY_ROUTER_BUSY", "query ingress is busy")
		}
		return
	}
	pools := i.active.Load()
	uri := r.URL.Path
	if uri == mcpPath {
		i.serveMCP(w, r, pools.mcp)
		return
	}
	if strings.HasPrefix(uri, fetchPath+"/") {
		i.serveFetch(w, r, pools.rest)
		return
	}
	if r.URL.RawPath == "" && path.Clean(uri) == uri && !strings.Contains(uri, "\\") &&
		(uri == "/health" || strings.HasPrefix(uri, "/api/v1/")) {
		pools.rest.serveFresh(w, r, REST)
		return
	}
	http.NotFound(w, r)
}

func (i *Ingress) serveFetch(w http.ResponseWriter, r *http.Request, pool *proxyPool) {
	fields := strings.Split(strings.TrimPrefix(r.URL.Path, fetchPath+"/"), "/")
	if r.Method != http.MethodGet || r.URL.RawPath != "" || len(fields) != 3 || !validLabel(fields[0], 64) || !validLabel(fields[2], 64) {
		unavailable(w, REST)
		return
	}
	i.serveContinuation(w, r, pool, REST, fields[1], fields[0])
}

func (i *Ingress) serveMCP(w http.ResponseWriter, r *http.Request, pool *proxyPool) {
	if r.URL.RawPath != "" {
		unavailable(w, MCP)
		return
	}
	headers := r.Header.Values(sessionHeader)
	switch r.Method {
	case http.MethodGet, http.MethodDelete:
		if len(headers) == 0 {
			http.Error(w, "MCP session header required", http.StatusBadRequest)
			return
		}
	case http.MethodPost:
		if len(headers) == 0 {
			pool.serveFresh(w, r, MCP)
			return
		}
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if len(headers) != 1 {
		unavailable(w, MCP)
		return
	}
	i.serveContinuation(w, r, pool, MCP, headers[0], "")
}

func (i *Ingress) serveContinuation(w http.ResponseWriter, r *http.Request, pool *proxyPool, transport, marker, id string) {
	slot, err := i.ring.Verify(marker, transport, id, time.Now())
	proxy := pool.owners[slot]
	if err != nil || proxy == nil {
		unavailable(w, transport)
		return
	}
	proxy.ServeHTTP(w, r)
}

func (p *proxyPool) serveFresh(w http.ResponseWriter, r *http.Request, pool string) {
	if len(p.fresh) == 0 {
		noOwner(w, pool)
		return
	}
	selected := (p.next.Add(1) - 1) % uint64(len(p.fresh))
	p.fresh[selected].ServeHTTP(w, r)
}

type ingressResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *ingressResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *ingressResponseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *ingressResponseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *ingressResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (w *ingressResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func ingressRouteTemplate(pathname string) string {
	switch {
	case pathname == mcpPath:
		return mcpPath
	case pathname == fetchPath:
		return fetchPath
	case strings.HasPrefix(pathname, fetchPath+"/"):
		return fetchPath + "/:query_id/:slug/:token"
	case pathname == "/health":
		return "/health"
	case strings.HasPrefix(pathname, "/api/v1/"):
		return "/api/v1/*"
	default:
		return "/not-found"
	}
}

func requestPeer(remote string) string {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return "-"
	}
	return host
}

func unavailable(w http.ResponseWriter, pool string) {
	writeError(w, pool, http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE", "query session is unavailable")
}

func noOwner(w http.ResponseWriter, pool string) {
	writeError(w, pool, http.StatusServiceUnavailable, "QUERY_OWNER_UNAVAILABLE", "no query owner is available")
}

func writeError(w http.ResponseWriter, pool string, status int, code, message string) {
	w.Header().Set("Cache-Control", "no-store")
	if pool == MCP {
		http.Error(w, http.StatusText(status), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response.ErrorResponse(code, message, "", ""))
}
