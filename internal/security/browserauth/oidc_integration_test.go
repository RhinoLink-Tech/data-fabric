package browserauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

type realAuth struct{ issuer string }

func (a realAuth) Resolve(_ context.Context, issuer, subject string) (string, string, bool, error) {
	return "existing-alice", "tenant-a", issuer == a.issuer && subject == "alice", nil
}
func TestRealSignedOIDCCodeFlow(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	var srv *httptest.Server
	var nonce, challenge string
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]string{"issuer": srv.URL, "jwks_uri": srv.URL + "/jwks", "authorization_endpoint": srv.URL + "/authorize", "token_endpoint": srv.URL + "/token"})
		case "/jwks":
			_ = json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "test-key", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/token":
			if r.ParseForm() != nil || r.Form.Get("code") != "test-code" || oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != challenge {
				http.Error(w, "invalid", 400)
				return
			}
			now := time.Now()
			j := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": srv.URL, "sub": "alice", "aud": "browser-client", "iat": now.Unix(), "exp": now.Add(time.Minute).Unix(), "nonce": nonce})
			j.Header["kid"] = "test-key"
			signed, e := j.SignedString(key)
			if e != nil {
				http.Error(w, "signing failed", 500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "opaque", "token_type": "Bearer", "id_token": signed})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	verifier, e := security.NewOIDCIDTokenVerifier(context.Background(), srv.URL, "browser-client", srv.Client())
	if e != nil {
		t.Fatal(e)
	}
	s := testStore(t)
	conf := oauth2.Config{ClientID: "browser-client", RedirectURL: "https://fabric.example/onboarding/auth/callback", Scopes: []string{"openid"}, Endpoint: oauth2.Endpoint{AuthURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token", AuthStyle: oauth2.AuthStyleInParams}}
	tampered := conf
	tampered.Endpoint.TokenURL = "https://attacker.invalid/token"
	if _, err := NewHandler(s, tampered, verifier, realAuth{srv.URL}, fakeFlows{}, srv.Client(), "https://fabric.example"); err == nil {
		t.Fatal("untrusted token endpoint accepted")
	}
	h, e := NewHandler(s, conf, verifier, realAuth{srv.URL}, fakeFlows{}, srv.Client(), "https://fabric.example")
	if e != nil {
		t.Fatal(e)
	}
	flow, _ := random()
	w := httptest.NewRecorder()
	h.Login(w, httptest.NewRequest("GET", "https://fabric.example/onboarding/auth/login?flow="+flow, nil))
	u, e := url.Parse(w.Header().Get("Location"))
	if e != nil {
		t.Fatal(e)
	}
	q := u.Query()
	nonce = q.Get("nonce")
	challenge = q.Get("code_challenge")
	r := httptest.NewRequest("GET", "https://fabric.example/onboarding/auth/callback?state="+q.Get("state")+"&code=test-code", nil)
	r.AddCookie(w.Result().Cookies()[0])
	res := httptest.NewRecorder()
	h.Callback(res, r)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("signed callback status=%d", res.Code)
	}
	var c *http.Cookie
	for _, x := range res.Result().Cookies() {
		if x.Name == "__Host-df_session" {
			c = x
		}
	}
	if c == nil {
		t.Fatal("missing session")
	}
	sessionReq := httptest.NewRequest("GET", "https://fabric.example/onboarding/session", nil)
	sessionReq.AddCookie(c)
	identity, user, tenant, _, e := h.Principal(sessionReq)
	if e != nil || identity.Subject != "alice" || identity.Issuer != srv.URL || user != "existing-alice" || tenant != "tenant-a" {
		t.Fatal("identity mapping failed")
	}
}
