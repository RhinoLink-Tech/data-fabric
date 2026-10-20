package config

import (
	"strings"
	"testing"
	"time"
)

func TestValidateSecurity(t *testing.T) {
	longSecret := strings.Repeat("s", 44)
	shortSecret := strings.Repeat("s", 31)

	tests := []struct {
		name    string
		mode    string
		auth    bool
		secret  string
		expiry  time.Duration
		wantErr bool
	}{
		{"release with auth disabled", "release", false, "", 24 * time.Hour, true},
		// The /fetch API always authenticates (#6), so the secret is checked
		// even with security.enable_auth disabled.
		{"debug with auth disabled and no secret", "debug", false, "", 0, true},
		{"test mode with auth disabled no secret", "test", false, "", 0, true},
		{"auth disabled valid secret", "debug", false, longSecret, 0, false},
		{"auth disabled short secret rejected", "debug", false, shortSecret, 24 * time.Hour, true},
		{"auth disabled placeholder secret rejected", "debug", false, "your-secret-key", 24 * time.Hour, true},
		{"release auth enabled empty secret", "release", true, "", 24 * time.Hour, true},
		{"debug auth enabled empty secret", "debug", true, "", 24 * time.Hour, true},
		{"debug auth enabled whitespace secret", "debug", true, "   ", 24 * time.Hour, true},
		{"release auth enabled valid secret", "release", true, longSecret, 24 * time.Hour, false},
		{"debug auth enabled short secret rejected", "debug", true, shortSecret, 24 * time.Hour, true},
		{"release auth enabled short secret rejected", "release", true, shortSecret, 24 * time.Hour, true},
		{"auth enabled zero expiration", "debug", true, longSecret, 0, true},
		{"auth enabled negative expiration", "debug", true, longSecret, -time.Hour, true},
	}

	for _, placeholder := range []string{
		"your-secret-key",
		"your-super-secret-jwt-key-change-in-production",
		"your-jwt-secret",
		"change-me",
		"changeme",
		"secret",
		"YOUR-SECRET-KEY",
	} {
		tests = append(tests, struct {
			name    string
			mode    string
			auth    bool
			secret  string
			expiry  time.Duration
			wantErr bool
		}{"placeholder secret: " + placeholder, "debug", true, placeholder, 24 * time.Hour, true})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				Server: ServerConfig{Mode: tt.mode},
				Security: SecurityConfig{
					EnableAuth:    tt.auth,
					JWTSecret:     tt.secret,
					JWTExpiration: tt.expiry,
				},
			}
			err := cfg.ValidateSecurity()
			if tt.wantErr && err == nil {
				t.Errorf("ValidateSecurity() = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("ValidateSecurity() = %v, want nil", err)
			}
		})
	}
}
