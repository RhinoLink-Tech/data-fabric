package config

import (
	"strings"
	"testing"
	"time"
)

func validStartupConfig() Config {
	return Config{
		Server: ServerConfig{
			Port:                "8080",
			Mode:                "debug",
			ReadHeaderTimeout:   10 * time.Second,
			ReadTimeout:         30 * time.Second,
			WriteTimeout:        time.Minute,
			IdleTimeout:         time.Minute,
			MaxRequestBodyBytes: 1024,
		},
		Database: DatabaseConfig{
			Host:     "localhost",
			Port:     "3306",
			Database: "data_fabric",
			Username: "data_fabric",
		},
		Security: SecurityConfig{
			JWTSecret:       strings.Repeat("s", 32),
			JWTExpiration:   time.Hour,
			EnableAuth:      true,
			EnforceReadOnly: true,
		},
		Query: QueryConfig{ExecutionMode: "auto"},
		Audit: AuditConfig{Enabled: true, RetentionDays: 90},
		MCP: MCPConfig{
			Transport: MCPTransportStdio,
			Address:   "127.0.0.1:8081",
		},
	}
}

func TestValidateStartupHTTPConfiguration(t *testing.T) {
	cfg := validStartupConfig()
	if err := cfg.ValidateStartup(); err != nil {
		t.Fatalf("ValidateStartup() error = %v", err)
	}
}

func TestValidateStartupRequiresAuditInReleaseAndNonnegativeRetention(t *testing.T) {
	t.Run("release cannot disable audit", func(t *testing.T) {
		cfg := validStartupConfig()
		cfg.Server.Mode = "release"
		cfg.Audit.Enabled = false
		if err := cfg.ValidateStartup(); err == nil || !strings.Contains(err.Error(), "audit.enabled") {
			t.Fatalf("ValidateStartup() error = %v, want audit.enabled release error", err)
		}
	})
	t.Run("retention cannot be negative", func(t *testing.T) {
		cfg := validStartupConfig()
		cfg.Audit.RetentionDays = -1
		if err := cfg.ValidateStartup(); err == nil || !strings.Contains(err.Error(), "audit.retention_days") {
			t.Fatalf("ValidateStartup() error = %v, want audit.retention_days error", err)
		}
	})
}

func TestValidateStartupRejectsMissingDatabaseSettings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*DatabaseConfig)
		want   string
	}{
		{"host", func(c *DatabaseConfig) { c.Host = " " }, "database.host"},
		{"database", func(c *DatabaseConfig) { c.Database = "" }, "database.database"},
		{"username", func(c *DatabaseConfig) { c.Username = "" }, "database.username"},
		{"port", func(c *DatabaseConfig) { c.Port = "not-a-port" }, "database.port"},
		{"port out of range", func(c *DatabaseConfig) { c.Port = "65536" }, "database.port"},
		{"port zero", func(c *DatabaseConfig) { c.Port = "0" }, "database.port"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validStartupConfig()
			tc.mutate(&cfg.Database)
			err := cfg.ValidateStartup()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateStartup() error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestValidateStartupRejectsInvalidHTTPSettings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"invalid server port", func(c *Config) { c.Server.Port = "70000" }, "server.port"},
		{"zero server port", func(c *Config) { c.Server.Port = "0" }, "server.port"},
		{"invalid mode", func(c *Config) { c.Server.Mode = "production" }, "server.mode"},
		{"negative timeout", func(c *Config) { c.Server.ReadTimeout = -time.Second }, "server.read_timeout"},
		{"zero body limit", func(c *Config) { c.Server.MaxRequestBodyBytes = 0 }, "server.max_request_body_bytes"},
		{"invalid query execution mode", func(c *Config) { c.Query.ExecutionMode = "background" }, "query.execution_mode"},
		{"negative query session TTL", func(c *Config) { c.Query.SessionTTLSeconds = -1 }, "query.session_ttl_seconds"},
		{"oversized query session TTL", func(c *Config) { c.Query.SessionTTLSeconds = 1801 }, "query.session_ttl_seconds"},
		{"negative per-tenant query limit", func(c *Config) { c.Query.MaxConcurrentQueriesPerTenant = -1 }, "query.max_concurrent_queries_per_tenant"},
		{"negative tenant query rate", func(c *Config) { c.Query.RateLimitPerMinutePerTenant = -1 }, "query.rate_limit_per_minute_per_tenant"},
		{"negative tenant query burst", func(c *Config) { c.Query.RateLimitBurstPerTenant = -1 }, "query.rate_limit_burst_per_tenant"},
		{"incomplete tenant query rate", func(c *Config) { c.Query.RateLimitPerMinutePerTenant = 120 }, "must both be zero"},
		{"negative query result rows", func(c *Config) { c.Query.MaxResultRows = -1 }, "query.max_result_rows"},
		{"oversized query result bytes", func(c *Config) { c.Query.MaxResultBytes = 1 << 31 }, "query.max_result_bytes"},
		{"query duration above session TTL", func(c *Config) { c.Query.MaxDurationSeconds = 400; c.Query.SessionTTLSeconds = 300 }, "cannot exceed query.session_ttl_seconds"},
		{"missing JWT secret", func(c *Config) { c.Security.JWTSecret = "" }, "security.jwt_secret"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validStartupConfig()
			tc.mutate(&cfg)
			err := cfg.ValidateStartup()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateStartup() error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

func TestValidateStartupAcceptsSupportedExecutionModes(t *testing.T) {
	for _, mode := range []string{"auto", "streaming", "pagination"} {
		t.Run(mode, func(t *testing.T) {
			cfg := validStartupConfig()
			cfg.Query.ExecutionMode = mode
			if err := cfg.ValidateStartup(); err != nil {
				t.Fatalf("ValidateStartup() error = %v", err)
			}
		})
	}
}

func TestValidateStartupMCPStdioDoesNotRequireHTTPSettings(t *testing.T) {
	cfg := validStartupConfig()
	cfg.MCP.Enabled = true
	cfg.MCP.Transport = MCPTransportStdio
	cfg.Server.Port = "invalid"
	cfg.Security.JWTSecret = ""

	if err := cfg.ValidateStartup(); err != nil {
		t.Fatalf("ValidateStartup() error = %v, want MCP stdio to skip HTTP-only validation", err)
	}
}

func TestValidateStartupRemoteMCPRequiresSecurity(t *testing.T) {
	cfg := validStartupConfig()
	cfg.MCP.Enabled = true
	cfg.MCP.Transport = MCPTransportStreamableHTTP
	cfg.MCP.Address = "127.0.0.1:8081"
	cfg.Security.JWTSecret = ""

	if err := cfg.ValidateStartup(); err == nil || !strings.Contains(err.Error(), "security.jwt_secret") {
		t.Fatalf("ValidateStartup() error = %v, want remote MCP security error", err)
	}
}

func TestValidateStartupRemoteMCPRejectsInvalidServerMode(t *testing.T) {
	cfg := validStartupConfig()
	cfg.MCP.Enabled = true
	cfg.MCP.Transport = MCPTransportStreamableHTTP
	cfg.MCP.Address = "127.0.0.1:8081"
	cfg.Server.Mode = "production"

	if err := cfg.ValidateStartup(); err == nil || !strings.Contains(err.Error(), "server.mode") {
		t.Fatalf("ValidateStartup() error = %v, want remote MCP mode validation error", err)
	}
}

func TestSQLTranslationConfigValidation(t *testing.T) {
	valid := SQLTranslationConfig{
		Enabled: true, Timeout: 5 * time.Second, MaxConcurrent: 4,
		MaxSQLBytes: 1 << 20, MaxOutputBytes: 1 << 20,
		PythonPath: "python3", ScriptPath: "./scripts/sql_translate.py",
	}
	tests := []struct {
		name   string
		mutate func(*SQLTranslationConfig)
		want   string
	}{
		{"timeout", func(c *SQLTranslationConfig) { c.Timeout = 0 }, "sql_translation.timeout"},
		{"concurrency", func(c *SQLTranslationConfig) { c.MaxConcurrent = 33 }, "sql_translation.max_concurrent"},
		{"SQL bytes", func(c *SQLTranslationConfig) { c.MaxSQLBytes = 0 }, "sql_translation.max_sql_bytes"},
		{"output bytes", func(c *SQLTranslationConfig) { c.MaxOutputBytes = 17 << 20 }, "sql_translation.max_output_bytes"},
		{"python path", func(c *SQLTranslationConfig) { c.PythonPath = " " }, "sql_translation.python_path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			tc.mutate(&cfg)
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate() error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
	if err := (SQLTranslationConfig{}).Validate(); err != nil {
		t.Fatalf("disabled SQL translation should not require a runtime: %v", err)
	}
}

func TestValidateStartupReleaseRemoteMCPRequiresOIDC(t *testing.T) {
	cfg := validStartupConfig()
	cfg.Server.Mode = "release"
	cfg.MCP.Enabled = true
	cfg.MCP.Transport = MCPTransportStreamableHTTP
	cfg.MCP.Address = "127.0.0.1:8081"
	if err := cfg.ValidateStartup(); err == nil || !strings.Contains(err.Error(), "mcp.auth_mode must be oidc") {
		t.Fatalf("ValidateStartup() error = %v, want release remote MCP OIDC requirement", err)
	}
}

func TestValidateStartupOIDCRemoteMCPDoesNotRequireLocalJWTSecret(t *testing.T) {
	cfg := validStartupConfig()
	cfg.Server.Mode = "release"
	cfg.MCP.Enabled = true
	cfg.MCP.Transport = MCPTransportStreamableHTTP
	cfg.MCP.Address = "127.0.0.1:8081"
	cfg.MCP.AuthMode = MCPAuthModeOIDC
	cfg.MCP.OIDCIssuer = "https://identity.example/tenant"
	cfg.MCP.OIDCAudience = "data-fabric-mcp"
	cfg.MCP.OIDCRequiredScopes = "data-fabric.query"
	cfg.MCP.OIDCTenantClaim = "tenant"
	cfg.MCP.OIDCRolesClaim = "roles"
	cfg.MCP.OIDCUsernameClaim = "preferred_username"
	cfg.MCP.OIDCJWKSRefreshInterval = time.Minute
	cfg.Security.JWTSecret = ""
	if err := cfg.ValidateStartup(); err != nil {
		t.Fatalf("ValidateStartup() error = %v, want OIDC to replace the local MCP JWT secret", err)
	}
}

func TestValidateStartupRESTDrainConfiguration(t *testing.T) {
	cfg := validStartupConfig()
	cfg.Routing.Enabled = true
	cfg.Routing.PolicyRevision = "policy-v1"
	if err := cfg.ValidateStartup(); err != nil || cfg.Routing.DrainTimeout() != 120*time.Second {
		t.Fatalf("default drain timeout = %v, error = %v", cfg.Routing.DrainTimeout(), err)
	}
	for _, seconds := range []int{-1, 1801} {
		cfg.Routing.DrainTimeoutSeconds = seconds
		if err := cfg.ValidateStartup(); err == nil || !strings.Contains(err.Error(), "routing.drain_timeout_seconds") {
			t.Fatalf("drain timeout %d: error = %v", seconds, err)
		}
	}
	cfg.Routing.DrainTimeoutSeconds = 1800
	cfg.MCP.Enabled = true
	cfg.MCP.Transport = MCPTransportStreamableHTTP
	cfg.MCP.Address = "127.0.0.1:8081"
	if err := cfg.ValidateStartup(); err != nil {
		t.Fatalf("remote MCP routing configuration: error = %v", err)
	}
	cfg.MCP.Transport = MCPTransportStdio
	if err := cfg.ValidateStartup(); err == nil || !strings.Contains(err.Error(), "requires the streamable-http transport") {
		t.Fatalf("stdio routing enabled: error = %v", err)
	}
}
