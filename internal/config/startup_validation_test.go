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
