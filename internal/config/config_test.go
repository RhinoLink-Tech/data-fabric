package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// resetViper isolates each test from the global viper state that Load
// accumulates across calls (a previously read config file stays cached).
func resetViper(t *testing.T) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
}

func TestSecretsFromEnv(t *testing.T) {
	resetViper(t)
	t.Setenv("DATABASE_USERNAME", "env-user")
	t.Setenv("DATABASE_PASSWORD", "env-pass")
	t.Setenv("JWT_SECRET", "env-secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Database.Username != "env-user" {
		t.Errorf("database.username = %q, want %q", cfg.Database.Username, "env-user")
	}
	if cfg.Database.Password != "env-pass" {
		t.Errorf("database.password = %q, want %q", cfg.Database.Password, "env-pass")
	}
	if cfg.Security.JWTSecret != "env-secret" {
		t.Errorf("security.jwt_secret = %q, want %q", cfg.Security.JWTSecret, "env-secret")
	}
}

func TestSecretsFromEnvOverrideConfigFile(t *testing.T) {
	resetViper(t)

	dir := t.TempDir()
	content := "server:\n  port: \"9999\"\ndatabase:\n  password: \"file-pass\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to temp dir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(wd) })

	t.Setenv("DATABASE_PASSWORD", "env-pass")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Port != "9999" {
		t.Errorf("server.port = %q, want %q (config file was not read)", cfg.Server.Port, "9999")
	}
	if cfg.Database.Password != "env-pass" {
		t.Errorf("database.password = %q, want %q (env must win over the file)", cfg.Database.Password, "env-pass")
	}
}

func TestConfigEnvironmentVariablesOverrideAllSections(t *testing.T) {
	resetViper(t)

	values := map[string]string{
		"SERVER_PORT":                    "9081",
		"SERVER_MODE":                    "test",
		"SERVER_HOST":                    "127.0.0.1",
		"SERVER_READ_HEADER_TIMEOUT":     "12s",
		"SERVER_READ_TIMEOUT":            "45s",
		"SERVER_WRITE_TIMEOUT":           "32m",
		"SERVER_IDLE_TIMEOUT":            "90s",
		"SERVER_MAX_REQUEST_BODY_BYTES":  "2048",
		"DATABASE_HOST":                  "db.internal",
		"DATABASE_PORT":                  "5432",
		"DATABASE_DATABASE":              "analytics",
		"DATABASE_NAME":                  "legacy-analytics",
		"DATABASE_USERNAME":              "env-user",
		"DATABASE_PASSWORD":              "env-pass",
		"DATABASE_SSL":                   "require",
		"SECURITY_JWT_SECRET":            strings.Repeat("s", 32),
		"JWT_SECRET":                     strings.Repeat("j", 32),
		"SECURITY_JWT_EXPIRATION":        "12h",
		"SECURITY_RATE_LIMIT_PER_MINUTE": "123",
		"SECURITY_RATE_LIMIT_BURST":      "24",
		"SECURITY_ENABLE_AUTH":           "false",
		"SECURITY_ENABLE_RATE_LIMIT":     "false",
		"SECURITY_ENFORCE_READ_ONLY":     "false",
		"LOGGING_LEVEL":                  "debug",
		"LOGGING_FORMAT":                 "text",
		"QUERY_PREFER_STREAMING":         "false",
		"QUERY_EXECUTION_MODE":           "pagination",
		"MCP_ENABLED":                    "true",
		"MCP_TRANSPORT":                  "stdio",
		"MCP_ADDRESS":                    "127.0.0.1:9090",
		"MCP_TLS_CERT_FILE":              "/tmp/mcp.crt",
		"MCP_TLS_KEY_FILE":               "/tmp/mcp.key",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}

	dir := t.TempDir()
	content := "server:\n  port: \"8000\"\ndatabase:\n  host: file-host\n  database: file-db\n"
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir to temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Server.Port != "9081" || cfg.Server.Mode != "test" || cfg.Server.Host != "127.0.0.1" {
		t.Errorf("server settings = %+v, want environment overrides", cfg.Server)
	}
	if cfg.Server.ReadHeaderTimeout != 12*time.Second || cfg.Server.ReadTimeout != 45*time.Second ||
		cfg.Server.WriteTimeout != 32*time.Minute || cfg.Server.IdleTimeout != 90*time.Second ||
		cfg.Server.MaxRequestBodyBytes != 2048 {
		t.Errorf("server limits = %+v, want environment overrides", cfg.Server)
	}
	if cfg.Database.Host != "db.internal" || cfg.Database.Port != "5432" ||
		cfg.Database.Database != "analytics" || cfg.Database.Username != "env-user" ||
		cfg.Database.Password != "env-pass" || cfg.Database.SSL != "require" {
		t.Errorf("database settings = %+v, want environment overrides", cfg.Database)
	}
	if cfg.Security.JWTSecret != strings.Repeat("s", 32) || cfg.Security.JWTExpiration != 12*time.Hour ||
		cfg.Security.RateLimitPerMinute != 123 || cfg.Security.RateLimitBurst != 24 ||
		cfg.Security.EnableAuth || cfg.Security.EnableRateLimit || cfg.Security.EnforceReadOnly {
		t.Errorf("security settings = %+v, want environment overrides", cfg.Security)
	}
	if cfg.Logging.Level != "debug" || cfg.Logging.Format != "text" {
		t.Errorf("logging settings = %+v, want environment overrides", cfg.Logging)
	}
	if cfg.Query.PreferStreaming || cfg.Query.ExecutionMode != "pagination" {
		t.Errorf("query settings = %+v, want environment overrides", cfg.Query)
	}
	if !cfg.MCP.Enabled || cfg.MCP.Transport != "stdio" || cfg.MCP.Address != "127.0.0.1:9090" ||
		cfg.MCP.TLSCertFile != "/tmp/mcp.crt" || cfg.MCP.TLSKeyFile != "/tmp/mcp.key" {
		t.Errorf("MCP settings = %+v, want environment overrides", cfg.MCP)
	}
}

func TestLegacyEnvironmentVariableAliases(t *testing.T) {
	resetViper(t)
	t.Setenv("DATABASE_DATABASE", "")
	t.Setenv("DATABASE_NAME", "legacy_db")
	t.Setenv("SECURITY_JWT_SECRET", "")
	t.Setenv("JWT_SECRET", strings.Repeat("j", 32))

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Database.Database != "legacy_db" {
		t.Errorf("database.database = %q, want legacy alias %q", cfg.Database.Database, "legacy_db")
	}
	if cfg.Security.JWTSecret != strings.Repeat("j", 32) {
		t.Errorf("security.jwt_secret did not use JWT_SECRET compatibility alias")
	}
}

func TestLoadRejectsMalformedDurationEnvironmentValue(t *testing.T) {
	resetViper(t)
	t.Setenv("SERVER_READ_TIMEOUT", "not-a-duration")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "unmarshaling config") {
		t.Errorf("Load() error = %v, want a typed configuration decode error", err)
	}
}

func TestEnvironmentOverridesAreAppliedBeforeSecurityValidation(t *testing.T) {
	resetViper(t)
	t.Setenv("SERVER_MODE", "release")
	t.Setenv("SECURITY_JWT_SECRET", strings.Repeat("s", 32))
	t.Setenv("SECURITY_ENABLE_AUTH", "false")
	t.Setenv("SECURITY_ENFORCE_READ_ONLY", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := cfg.ValidateSecurity(); err == nil || !strings.Contains(err.Error(), "security.enable_auth") {
		t.Errorf("ValidateSecurity() error = %v, want release-mode auth error from environment override", err)
	}
}

func TestHTTPServerTimeoutDefaults(t *testing.T) {
	resetViper(t)
	setDefaults()

	var server ServerConfig
	if err := viper.UnmarshalKey("server", &server); err != nil {
		t.Fatalf("UnmarshalKey(server) error = %v", err)
	}

	if server.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("read_header_timeout = %s, want 10s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != 30*time.Second {
		t.Errorf("read_timeout = %s, want 30s", server.ReadTimeout)
	}
	if server.WriteTimeout != 31*time.Minute {
		t.Errorf("write_timeout = %s, want 31m", server.WriteTimeout)
	}
	if server.IdleTimeout != 120*time.Second {
		t.Errorf("idle_timeout = %s, want 120s", server.IdleTimeout)
	}
	if server.MaxRequestBodyBytes != 1<<20 {
		t.Errorf("max_request_body_bytes = %d, want %d", server.MaxRequestBodyBytes, 1<<20)
	}
}

func TestHTTPServerSettingsCanBeOverridden(t *testing.T) {
	resetViper(t)
	setDefaults()
	viper.Set("server.read_timeout", "45s")
	viper.Set("server.max_request_body_bytes", 2048)

	var server ServerConfig
	if err := viper.UnmarshalKey("server", &server); err != nil {
		t.Fatalf("UnmarshalKey(server) error = %v", err)
	}
	if server.ReadTimeout != 45*time.Second {
		t.Errorf("read_timeout = %s, want 45s", server.ReadTimeout)
	}
	if server.MaxRequestBodyBytes != 2048 {
		t.Errorf("max_request_body_bytes = %d, want 2048", server.MaxRequestBodyBytes)
	}
}
