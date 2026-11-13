package onboardingruntime

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSharedOnboardingRateAppliesOnlyToOnboarding(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	l, err := newSharedRateLimiter(db, 2, 60)
	if err != nil {
		t.Fatal(err)
	}
	h := l.wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	check := func(path string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "https://example.test"+path, nil))
		if w.Code != want {
			t.Fatalf("%s: got %d want %d", path, w.Code, want)
		}
	}
	check("/onboarding/auth/login", 204)
	check("/onboarding/session", 204)
	check("/onboarding/auth/callback", 429)
	check("/mcp", 204)
	if err = db.Model(&sharedRateWindow{}).Where("id = ?", 1).Update("window", 0).Error; err != nil {
		t.Fatal(err)
	}
	check("/onboarding/source-types", 204)
	if err = db.Migrator().DropTable(&sharedRateWindow{}); err != nil {
		t.Fatal(err)
	}
	check("/onboarding/session", 503)
	check("/mcp", 204)
}
