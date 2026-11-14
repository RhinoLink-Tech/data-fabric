package browserauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"golang.org/x/oauth2"
)

// Verifier must verify signed ID tokens, including issuer, audience and nonce.
type Verifier interface {
	Issuer() string
	Verify(context.Context, string, string) (*security.OIDCBrowserIdentity, error)
}

// Authorizer is a trusted server-side policy check; it MUST check current
// tenant membership and source-management permission on every request.
type Authorizer = onboarding.IdentityResolver
type FlowChecker interface {
	Exists(context.Context, string) (bool, error)
}

type Handler struct {
	flows    FlowChecker
	store    *Store
	oauth    oauth2.Config
	verifier Verifier
	auth     Authorizer
	client   *http.Client
	origin   string
}

func NewHandler(store *Store, config oauth2.Config, verifier Verifier, auth Authorizer, flows FlowChecker, client *http.Client, origin string) (*Handler, error) {
	if store == nil || verifier == nil || auth == nil || flows == nil || client == nil || config.ClientID == "" {
		return nil, ErrUnavailable
	}
	hasOpenID := false
	for _, scope := range config.Scopes {
		if scope == "openid" {
			hasOpenID = true
		}
	}
	if !hasOpenID {
		return nil, ErrUnavailable
	}
	u, e := url.Parse(origin)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, ErrUnavailable
	}
	callback, e := url.Parse(config.RedirectURL)
	if e != nil || callback.Scheme != "https" || callback.Host != u.Host || callback.Path != "/onboarding/auth/callback" || callback.RawQuery != "" || callback.Fragment != "" || callback.User != nil {
		return nil, ErrUnavailable
	}
	for _, v := range []string{config.Endpoint.AuthURL, config.Endpoint.TokenURL} {
		x, e := url.Parse(v)
		if e != nil || x.Scheme != "https" || x.Host == "" || x.User != nil || x.Fragment != "" {
			return nil, ErrUnavailable
		}
	}
	copyClient := *client
	copyClient.Timeout = 10 * time.Second
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	issuer := verifier.Issuer()
	iu, err := url.Parse(issuer)
	if err != nil || iu.Scheme != "https" || iu.Host == "" || iu.User != nil || iu.RawQuery != "" || iu.Fragment != "" {
		return nil, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(issuer, "/")+"/.well-known/openid-configuration", nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	res, err := copyClient.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, ErrUnavailable
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 262145))
	if err != nil || len(body) > 262144 {
		return nil, ErrUnavailable
	}
	var discovery struct {
		Issuer                string `json:"issuer"`
		AuthorizationEndpoint string `json:"authorization_endpoint"`
		TokenEndpoint         string `json:"token_endpoint"`
	}
	if json.Unmarshal(body, &discovery) != nil || discovery.Issuer != issuer || discovery.AuthorizationEndpoint != config.Endpoint.AuthURL || discovery.TokenEndpoint != config.Endpoint.TokenURL {
		return nil, ErrUnavailable
	}
	return &Handler{store: store, oauth: config, verifier: verifier, auth: auth, flows: flows, client: &copyClient, origin: origin}, nil
}
func noCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}
func reject(w http.ResponseWriter) {
	http.Error(w, "authentication unavailable", http.StatusUnauthorized)
}

// Login begins code+PKCE login for a single opaque onboarding flow.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	if r.Method != "GET" || len(r.URL.Query()["flow"]) != 1 {
		http.Error(w, "invalid request", 400)
		return
	}
	id := r.URL.Query().Get("flow")
	if _, e := hash(id); e != nil {
		http.Error(w, "invalid request", 400)
		return
	}
	exists, err := h.flows.Exists(r.Context(), id)
	if err != nil || !exists {
		http.Error(w, "invalid request", 400)
		return
	}
	state, binding, nonce, challenge, e := h.store.BeginFlow(r.Context(), id)
	if e != nil {
		http.Error(w, "authentication unavailable", 503)
		return
	}
	http.SetCookie(w, BindingCookie(binding, time.Now().Add(h.store.loginTTL)))
	target := h.oauth.AuthCodeURL(state, oauth2.SetAuthURLParam("nonce", nonce), oauth2.SetAuthURLParam("code_challenge", challenge), oauth2.SetAuthURLParam("code_challenge_method", "S256"))
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// Callback consumes state before exchanging the code; a failed exchange
// requires a fresh login. No code, verifier or token is written to logs.
func (h *Handler) Callback(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	if r.Method != "GET" || len(r.URL.Query()["state"]) != 1 || len(r.URL.Query()["code"]) != 1 {
		reject(w)
		return
	}
	c, e := r.Cookie("__Host-df_login")
	if e != nil {
		reject(w)
		return
	}
	http.SetCookie(w, BindingCookie("", time.Unix(1, 0)))
	verifier, nonce, flow, e := h.store.ConsumeFlow(r.Context(), r.URL.Query().Get("state"), c.Value)
	if e != nil {
		reject(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, h.client)
	tok, e := h.oauth.Exchange(ctx, r.URL.Query().Get("code"), oauth2.SetAuthURLParam("code_verifier", verifier))
	if e != nil {
		reject(w)
		return
	}
	raw, ok := tok.Extra("id_token").(string)
	if !ok {
		reject(w)
		return
	}
	verified, e := h.verifier.Verify(ctx, raw, nonce)
	if e != nil || verified == nil {
		reject(w)
		return
	}
	userID, tenant, allowed, e := h.auth.Resolve(ctx, verified.Issuer, verified.Subject)
	if e != nil || !allowed || strings.TrimSpace(userID) == "" || strings.TrimSpace(tenant) == "" {
		reject(w)
		return
	}
	cookie, _, exp, e := h.store.CreateSession(ctx, Identity{Issuer: verified.Issuer, Subject: verified.Subject, ExpiresAt: verified.ExpiresAt})
	if e != nil {
		reject(w)
		return
	}
	http.SetCookie(w, SessionCookie(cookie, exp))
	http.Redirect(w, r, "/onboarding/flows/"+flow, http.StatusSeeOther)
}

// Principal always resolves current management permission again; identity
// cannot supply the tenant or access grant from browser input.
func (h *Handler) Principal(r *http.Request) (Identity, string, string, string, error) {
	c, e := r.Cookie("__Host-df_session")
	if e != nil {
		return Identity{}, "", "", "", ErrUnavailable
	}
	id, csrf, e := h.store.Authenticate(r.Context(), c.Value)
	if e != nil {
		return Identity{}, "", "", "", ErrUnavailable
	}
	userID, tenant, allowed, e := h.auth.Resolve(r.Context(), id.Issuer, id.Subject)
	if e != nil || !allowed || strings.TrimSpace(userID) == "" || strings.TrimSpace(tenant) == "" {
		return Identity{}, "", "", "", ErrUnavailable
	}
	return id, userID, tenant, csrf, nil
}
func (h *Handler) Session(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	if r.Method != "GET" {
		http.Error(w, "method not allowed", 405)
		return
	}
	_, _, _, csrf, e := h.Principal(r)
	if e != nil {
		reject(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "csrf_token": csrf})
}
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	noCache(w)
	if r.Method != "POST" {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, _, _, csrf, e := h.Principal(r)
	if e != nil || !CheckMutation(r, h.origin, csrf) {
		reject(w)
		return
	}
	c, e := r.Cookie("__Host-df_session")
	if e != nil || h.store.Revoke(r.Context(), c.Value) != nil {
		reject(w)
		return
	}
	http.SetCookie(w, SessionCookie("", time.Unix(1, 0)))
	w.WriteHeader(http.StatusNoContent)
}
