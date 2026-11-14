package onboardingruntime

import (
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Run only with an isolated throwaway MySQL schema.
func TestSharedOnboardingRateAcrossReplicasMySQL(t *testing.T) {
	dsn := os.Getenv("DF_WB_E2E_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL required")
	}
	db1, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	db2, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	l1, err := newSharedRateLimiter(db1, 8, 3600)
	if err != nil {
		t.Fatal(err)
	}
	l2, err := newSharedRateLimiter(db2, 8, 3600)
	if err != nil {
		t.Fatal(err)
	}
	if err = db1.Model(&sharedRateWindow{}).Where("id = ?", 1).Updates(map[string]any{"window": 0, "count": 0}).Error; err != nil {
		t.Fatal(err)
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handlers := []http.Handler{l1.wrap(next), l2.wrap(next)}
	var successes atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := httptest.NewRequest("GET", "https://example.test/onboarding/auth/login?flow=opaque", nil)
			req.Header.Set("X-Forwarded-For", string(rune(i+32)))
			w := httptest.NewRecorder()
			handlers[i%2].ServeHTTP(w, req)
			switch w.Code {
			case http.StatusNoContent:
				successes.Add(1)
			case http.StatusTooManyRequests:
			case http.StatusServiceUnavailable:
			default:
				t.Errorf("unexpected HTTP %d", w.Code)
			}
		}(i)
	}
	wg.Wait()
	if successes.Load() != 8 {
		t.Fatalf("shared admission %d, want 8", successes.Load())
	}
	for _, h := range handlers {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", "https://example.test/onboarding/auth/login", nil))
		if r.Code != 429 {
			t.Fatalf("limit bypass: %d", r.Code)
		}
	}
	var count int64
	if err = db1.Model(&sharedRateWindow{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("unbounded rows: %d %v", count, err)
	}
	// Reset the server-side window rather than waiting for wall-clock boundaries.
	if err = db1.Model(&sharedRateWindow{}).Where("id = ?", 1).Update("window", 0).Error; err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handlers[1].ServeHTTP(w, httptest.NewRequest("GET", "https://example.test/onboarding/session", nil))
	if w.Code != 204 {
		t.Fatalf("did not recover: %d", w.Code)
	}
	// A backwards database clock must not reset and bypass the rate budget.
	if err = db1.Model(&sharedRateWindow{}).Where("id = ?", 1).Update("window", int64(999999999999)).Error; err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handlers[0].ServeHTTP(w, httptest.NewRequest("GET", "https://example.test/onboarding/auth/login", nil))
	if w.Code != 503 {
		t.Fatalf("backwards clock not rejected: %d", w.Code)
	}
	// Fault must deny the onboarding route while unrelated fallback remains usable.
	if err = db1.Migrator().DropTable(&sharedRateWindow{}); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	handlers[0].ServeHTTP(w, httptest.NewRequest("GET", "https://example.test/onboarding/auth/login", nil))
	if w.Code != 503 {
		t.Fatalf("failure did not close: %d", w.Code)
	}
	w = httptest.NewRecorder()
	handlers[0].ServeHTTP(w, httptest.NewRequest("GET", "https://example.test/mcp", nil))
	if w.Code != 204 {
		t.Fatalf("fallback affected: %d", w.Code)
	}
}
