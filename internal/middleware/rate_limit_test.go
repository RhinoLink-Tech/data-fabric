package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/gin-gonic/gin"
)

func TestGetClientIDUsesVerifiedUserAndPeerIP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.10:54321"
	request.Header.Set("X-API-Key", "attacker-controlled-key")
	request.Header.Set("X-Forwarded-For", "203.0.113.77")
	ctx.Request = request

	limiter := &RateLimiter{}
	if got, want := limiter.getClientID(ctx), "ip:192.0.2.10"; got != want {
		t.Fatalf("unauthenticated client ID = %q, want %q", got, want)
	}

	ctx.Set("user_id", "user-123")
	if got, want := limiter.getClientID(ctx), "user:user-123"; got != want {
		t.Fatalf("authenticated client ID = %q, want %q", got, want)
	}
}

func TestRateLimitDoesNotCreateBucketsFromAPIKeyHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := NewRateLimiter(RateLimiterConfig{
		RPM:             60,
		Burst:           10,
		CleanupInterval: time.Hour,
	})
	router := gin.New()
	router.Use(limiter.RateLimit())
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	for _, apiKey := range []string{"first-unverified-key", "second-unverified-key"} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = "192.0.2.10:54321"
		request.Header.Set("X-API-Key", apiKey)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
		}
	}

	if got := limiter.GetStats().ActiveClients; got != 1 {
		t.Fatalf("active rate-limit buckets = %d, want 1", got)
	}
}

func TestRateLimitUsesVerifiedJWTIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jwtManager := security.NewJWTManager("test-secret", time.Hour)
	authMiddleware := security.NewAuthMiddleware(jwtManager)
	limiter := NewRateLimiter(RateLimiterConfig{
		RPM:             60,
		Burst:           10,
		CleanupInterval: time.Hour,
	})
	router := gin.New()
	router.Use(authMiddleware.OptionalAuth(), limiter.RateLimit())
	router.GET("/", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	for _, userID := range []string{"user-1", "user-2"} {
		token, err := jwtManager.GenerateToken(userID, "", userID, nil)
		if err != nil {
			t.Fatalf("generate JWT for %s: %v", userID, err)
		}
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = "192.0.2.10:54321"
		request.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
		}
	}

	if got := limiter.GetStats().ActiveClients; got != 2 {
		t.Fatalf("active rate-limit buckets = %d, want 2 for distinct authenticated users", got)
	}
}
