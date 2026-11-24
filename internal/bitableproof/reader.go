// Package bitableproof is an isolated synthetic prototype. It is not a
// registered connector and must not be used for real provider credentials.
package bitableproof

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"time"
)

var (
	ErrInvalid      = errors.New("invalid prototype request")
	ErrContinuation = errors.New("continuation unavailable")
	ErrPermission   = errors.New("provider permission denied")
	ErrRateLimited  = errors.New("provider rate limited")
	ErrUnavailable  = errors.New("provider unavailable")
	ErrBudget       = errors.New("result budget exceeded")
)

// Scope is the authorization context and fixed query. Empty values are invalid.
type Scope struct{ Caller, Tenant, Source, Table, View string }

// Record intentionally exposes only the approved synthetic fields.
// Nil means absent or unknown, never an inferred zero or empty value.
type Record struct {
	ID        string  `json:"record_id"`
	Status    *string `json:"status"`
	CreatedAt *int64  `json:"created_at"`
}

type Page struct {
	Records   []Record `json:"records"`
	Next      string   `json:"next,omitempty"`
	Complete  bool     `json:"complete"`
	Truncated bool     `json:"truncated"`
}

type Limits struct {
	PageSize, MaxPages, MaxRows, MaxBytes, MaxSessions int
	Duration, SessionTTL                               time.Duration
}
type session struct {
	scope             Scope
	cursor            string
	pages, rows, size int
	deadline, expires time.Time
}
type Reader struct {
	endpoint *url.URL
	client   *http.Client
	limits   Limits
	mu       sync.Mutex
	sessions map[string]session
}

// New creates a local-fake-only reader. The endpoint must be a numeric loopback
// address; redirects and non-loopback destinations are forbidden.
func New(raw string, lim Limits) (*Reader, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/records" {
		return nil, ErrInvalid
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || port == "" {
		return nil, ErrInvalid
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() {
		return nil, ErrInvalid
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 || lim.PageSize < 1 || lim.PageSize > 100 || lim.MaxPages < 1 || lim.MaxPages > 100 || lim.MaxRows < 1 || lim.MaxRows > 10000 || lim.MaxBytes < 256 || lim.MaxBytes > 1048576 || lim.MaxSessions < 1 || lim.MaxSessions > 1000 || lim.Duration <= 0 || lim.Duration > time.Minute || lim.SessionTTL <= 0 || lim.SessionTTL > time.Minute {
		return nil, ErrInvalid
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: lim.Duration}).DialContext, DisableKeepAlives: true}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Reader{endpoint: u, client: client, limits: lim, sessions: make(map[string]session)}, nil
}

func validScope(s Scope) bool {
	for _, v := range []string{s.Caller, s.Tenant, s.Source, s.Table, s.View} {
		if v == "" || len(v) > 128 {
			return false
		}
	}
	return true
}

// Read returns one page. A token is single-use and is bound to all Scope
// fields. Expiry or any provider failure invalidates that token; callers can
// restart and must not treat an incomplete result as a complete summary.
func (r *Reader) Read(ctx context.Context, scope Scope, token string) (Page, error) {
	if r == nil || ctx == nil || !validScope(scope) {
		return Page{}, ErrInvalid
	}
	now := time.Now()
	state := session{scope: scope, deadline: now.Add(r.limits.Duration), expires: now.Add(r.limits.SessionTTL)}
	if token != "" {
		r.mu.Lock()
		stored, ok := r.sessions[token]
		if ok && stored.scope == scope {
			delete(r.sessions, token)
		}
		r.mu.Unlock()
		if !ok || stored.scope != scope || !now.Before(stored.expires) || !now.Before(stored.deadline) {
			return Page{}, ErrContinuation
		}
		state = stored
	}
	remaining := r.limits.MaxRows - state.rows
	if remaining <= 0 || state.pages >= r.limits.MaxPages {
		return Page{}, ErrBudget
	}
	pageSize := r.limits.PageSize
	if pageSize > remaining {
		pageSize = remaining
	}
	child, cancel := context.WithDeadline(ctx, state.deadline)
	defer cancel()
	page, err := r.fetch(child, scope, state.cursor, pageSize)
	if err != nil {
		return Page{}, err
	}
	encoded, err := json.Marshal(page.Records)
	if err != nil || state.size+len(encoded) > r.limits.MaxBytes {
		return Page{}, ErrBudget
	}
	state.pages++
	state.rows += len(page.Records)
	state.size += len(encoded)
	if !page.more {
		return Page{Records: page.Records, Complete: true}, nil
	}
	if state.pages >= r.limits.MaxPages || state.rows >= r.limits.MaxRows || state.size >= r.limits.MaxBytes {
		return Page{Records: page.Records, Truncated: true}, nil
	}
	state.cursor = page.cursor
	state.expires = time.Now().Add(r.limits.SessionTTL)
	if state.expires.After(state.deadline) {
		state.expires = state.deadline
	}
	random := make([]byte, 32)
	if _, err = rand.Read(random); err != nil {
		return Page{}, ErrUnavailable
	}
	next := base64.RawURLEncoding.EncodeToString(random)
	r.mu.Lock()
	for key, old := range r.sessions {
		if !time.Now().Before(old.expires) || !time.Now().Before(old.deadline) {
			delete(r.sessions, key)
		}
	}
	if len(r.sessions) >= r.limits.MaxSessions {
		r.mu.Unlock()
		return Page{}, ErrBudget
	}
	if _, exists := r.sessions[next]; exists {
		r.mu.Unlock()
		return Page{}, ErrUnavailable
	}
	r.sessions[next] = state
	r.mu.Unlock()
	return Page{Records: page.Records, Next: next}, nil
}

type providerResponse struct {
	Records []Record `json:"records"`
	More    *bool    `json:"has_more"`
	Cursor  string   `json:"page_token"`
}
type fetched struct {
	Records []Record
	more    bool
	cursor  string
}

func (r *Reader) fetch(ctx context.Context, scope Scope, cursor string, size int) (fetched, error) {
	u := *r.endpoint
	q := url.Values{"table": {scope.Table}, "view": {scope.View}, "page_size": {strconv.Itoa(size)}}
	if cursor != "" {
		q.Set("page_token", cursor)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fetched{}, ErrUnavailable
	}
	res, err := r.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return fetched{}, ctx.Err()
		}
		return fetched{}, ErrUnavailable
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusUnauthorized:
		return fetched{}, ErrPermission
	case http.StatusTooManyRequests:
		return fetched{}, ErrRateLimited
	default:
		return fetched{}, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, int64(r.limits.MaxBytes)+1))
	if err != nil {
		if ctx.Err() != nil {
			return fetched{}, ctx.Err()
		}
		return fetched{}, ErrUnavailable
	}
	if len(body) > r.limits.MaxBytes {
		return fetched{}, ErrBudget
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	var p providerResponse
	if err = dec.Decode(&p); err != nil {
		return fetched{}, ErrUnavailable
	}
	var extra any
	if err = dec.Decode(&extra); err != io.EOF {
		return fetched{}, ErrUnavailable
	}
	if p.Records == nil || p.More == nil || len(p.Records) > size || len(p.Cursor) > 1024 || *p.More != (p.Cursor != "") || (*p.More && p.Cursor == cursor) {
		return fetched{}, ErrUnavailable
	}
	for _, v := range p.Records {
		if v.ID == "" || len(v.ID) > 256 || (v.Status != nil && len(*v.Status) > 4096) {
			return fetched{}, ErrUnavailable
		}
	}
	return fetched{Records: p.Records, more: *p.More, cursor: p.Cursor}, nil
}
