package browserauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type unavailableRecoveryFlow struct{}

func (unavailableRecoveryFlow) Exists(context.Context, string) (bool, error) {
	return false, errors.New("synthetic backend failure")
}

func TestInvalidLoginFlowOffersSafeRecovery(t *testing.T) {
	validShape, err := random()
	if err != nil {
		t.Fatal(err)
	}
	s := testStore(t)
	cases := []struct {
		name, method, path string
		unavailable        bool
	}{
		{"missing", http.MethodGet, "/onboarding/auth/login", false},
		{"malformed", http.MethodGet, "/onboarding/auth/login?flow=secret-marker", false},
		{"duplicate", http.MethodGet, "/onboarding/auth/login?flow=" + validShape + "&flow=" + validShape, false},
		{"wrong method", http.MethodPost, "/onboarding/auth/login?flow=" + validShape, false},
		{"unknown or expired", http.MethodGet, "/onboarding/auth/login?flow=" + validShape, false},
		{"store unavailable", http.MethodGet, "/onboarding/auth/login?flow=" + validShape, true},
	}
	var expected string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{store: s, flows: invalidFlow{}}
			if tc.unavailable {
				h.flows = unavailableRecoveryFlow{}
			}
			w := httptest.NewRecorder()
			h.Login(w, httptest.NewRequest(tc.method, "https://fabric.example"+tc.path, nil))
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status=%d", w.Code)
			}
			body := w.Body.String()
			if !strings.Contains(body, "WorkBuddy") || !strings.Contains(body, "重新开始") {
				t.Fatal("missing actionable recovery guidance")
			}
			if strings.Contains(body, validShape) || strings.Contains(body, "secret-marker") || strings.Contains(body, "synthetic backend failure") {
				t.Fatal("response disclosed input or backend details")
			}
			if expected == "" {
				expected = body
			} else if body != expected {
				t.Fatal("different failures must have the same response")
			}
			if w.Header().Get("Set-Cookie") != "" || w.Header().Get("Location") != "" {
				t.Fatal("invalid flow created a login session or redirect")
			}
			if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("missing security headers")
			}
		})
	}
	var count int64
	if err := s.db.Model(&loginRecord{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatal("invalid flow allocated login state")
	}
}
