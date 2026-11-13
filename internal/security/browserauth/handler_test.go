package browserauth

import (
	"context"
	"encoding/json"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"golang.org/x/oauth2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type fakeVerifier struct{ nonce, issuer string }

func (v *fakeVerifier) Issuer() string { return v.issuer }

func (v *fakeVerifier) Verify(_ context.Context, token, nonce string) (*security.OIDCBrowserIdentity, error) {
	if token != "signed-id-token" || nonce != v.nonce {
		return nil, ErrUnavailable
	}
	return &security.OIDCBrowserIdentity{Issuer: "https://issuer.example", Subject: "alice", ExpiresAt: time.Now().Add(time.Minute)}, nil
}

type fakeFlows struct{}

func (fakeFlows) Exists(context.Context, string) (bool, error) { return true, nil }

type fakeAuth struct{ allow bool }

func (a *fakeAuth) Resolve(_ context.Context, iss, sub string) (string, string, bool, error) {
	return "canonical-alice", "tenant-1", a.allow && iss == "https://issuer.example" && sub == "alice", nil
}
func TestCodeLoginAndRevocation(t *testing.T) {
	var challenge, providerURL string
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": providerURL, "authorization_endpoint": providerURL + "/authorize", "token_endpoint": providerURL + "/token"})
			return
		}
		if r.URL.Path != "/token" {
			http.NotFound(w, r)
			return
		}
		if r.ParseForm() != nil || r.Form.Get("code") != "test-code" || oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != challenge {
			http.Error(w, "bad exchange", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access","token_type":"bearer","id_token":"signed-id-token"}`))
	}))
	defer provider.Close()
	providerURL = provider.URL
	s := testStore(t)
	v := &fakeVerifier{issuer: providerURL}
	a := &fakeAuth{true}
	conf := oauth2.Config{ClientID: "client", Endpoint: oauth2.Endpoint{AuthURL: provider.URL + "/authorize", TokenURL: provider.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}, RedirectURL: "https://fabric.example/onboarding/auth/callback", Scopes: []string{"openid"}}
	h, e := NewHandler(s, conf, v, a, fakeFlows{}, provider.Client(), "https://fabric.example")
	if e != nil {
		t.Fatal(e)
	}
	id, _ := random()
	r := httptest.NewRequest("GET", "https://fabric.example/onboarding/auth/login?flow="+id, nil)
	w := httptest.NewRecorder()
	h.Login(w, r)
	if w.Code != 303 {
		t.Fatal(w.Code)
	}
	u, e := url.Parse(w.Header().Get("Location"))
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	challenge = q.Get("code_challenge")
	v.nonce = q.Get("nonce")
	if q.Get("code_challenge_method") != "S256" || challenge == "" || v.nonce == "" {
		t.Fatal("missing PKCE or nonce")
	}
	binding := w.Result().Cookies()[0]
	callback := httptest.NewRequest("GET", "https://fabric.example/onboarding/auth/callback?state="+q.Get("state")+"&code=test-code", nil)
	callback.AddCookie(binding)
	out := httptest.NewRecorder()
	h.Callback(out, callback)
	if out.Code != 303 || out.Header().Get("Location") != "/onboarding/flows/"+id {
		t.Fatalf("callback %d", out.Code)
	}
	var session *http.Cookie
	for _, c := range out.Result().Cookies() {
		if c.Name == "__Host-df_session" {
			session = c
		}
	}
	if session == nil || !session.Secure || !session.HttpOnly {
		t.Fatal("missing secure session")
	}
	again := httptest.NewRecorder()
	h.Callback(again, callback)
	if again.Code != 401 {
		t.Fatal("state replay accepted")
	}
	get := httptest.NewRequest("GET", "https://fabric.example/onboarding/session", nil)
	get.AddCookie(session)
	out = httptest.NewRecorder()
	h.Session(out, get)
	if out.Code != 200 {
		t.Fatal(out.Code)
	}
	var data map[string]any
	if json.Unmarshal(out.Body.Bytes(), &data) != nil || data["csrf_token"] == "" {
		t.Fatal("csrf missing")
	}
	a.allow = false
	out = httptest.NewRecorder()
	h.Session(out, get)
	if out.Code != 401 {
		t.Fatal("revoked authorization still accepted")
	}
	if strings.Contains(out.Body.String(), "alice") {
		t.Fatal("identity leaked")
	}
}

type invalidFlow struct{}

func (invalidFlow) Exists(context.Context, string) (bool, error) { return false, nil }
func TestUnrecognizedFlowCannotAllocate(t *testing.T) {
	s := testStore(t)
	conf := oauth2.Config{ClientID: "client", RedirectURL: "https://fabric.example/onboarding/auth/callback", Scopes: []string{"openid"}, Endpoint: oauth2.Endpoint{AuthURL: "https://issuer.example/auth", TokenURL: "https://issuer.example/token"}}
	h := &Handler{store: s, oauth: conf, verifier: &fakeVerifier{}, auth: &fakeAuth{true}, flows: invalidFlow{}, client: http.DefaultClient, origin: "https://fabric.example"}
	var e error
	if e != nil {
		t.Fatal(e)
	}
	flow, _ := random()
	w := httptest.NewRecorder()
	h.Login(w, httptest.NewRequest("GET", "https://fabric.example/onboarding/auth/login?flow="+flow, nil))
	if w.Code != http.StatusBadRequest {
		t.Fatal(w.Code)
	}
	var count int64
	if e = s.db.Model(&loginRecord{}).Count(&count).Error; e != nil || count != 0 {
		t.Fatal("unknown flow created login state")
	}
}
