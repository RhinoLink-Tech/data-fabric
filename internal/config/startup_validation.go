package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ValidateStartup rejects invalid settings before opening the metadata database.
// MCP stdio does not serve the HTTP API, so it does not require HTTP settings or
// a JWT signing secret.
func (c Config) ValidateStartup() error {
	if err := c.Database.Validate(); err != nil {
		return err
	}
	if err := c.Query.Validate(); err != nil {
		return err
	}
	if c.Routing.DrainTimeoutSeconds < 0 || c.Routing.DrainTimeoutSeconds > 1800 {
		return fmt.Errorf("routing.drain_timeout_seconds must be between 0 and 1800")
	}
	if c.Routing.Enabled && (strings.TrimSpace(c.Routing.PolicyRevision) == "" || len(c.Routing.PolicyRevision) > 64) {
		return fmt.Errorf("routing.policy_revision is required and must be at most 64 bytes")
	}
	if c.Routing.Enabled && c.MCP.Enabled {
		transport := strings.TrimSpace(c.MCP.Transport)
		if transport == "" {
			transport = MCPTransportStdio
		}
		if transport != MCPTransportStreamableHTTP {
			return fmt.Errorf("routing.enabled with mcp.enabled requires the streamable-http transport")
		}
		if c.MCP.RoutedSessionTTLSeconds < 0 || c.MCP.RoutedSessionTTLSeconds > maxRoutedSessionTTLSeconds {
			return fmt.Errorf("mcp.routed_session_ttl_seconds must be between 0 and %d", maxRoutedSessionTTLSeconds)
		}
		if c.MCP.MaxRoutedSessions < 0 || c.MCP.MaxRoutedSessions > maxRoutedSessions {
			return fmt.Errorf("mcp.max_routed_sessions must be between 0 and %d", maxRoutedSessions)
		}
	}
	if err := c.SQLTranslation.Validate(); err != nil {
		return err
	}
	// Validate the active table policy after opening the metadata DB. A managed
	// DB revision is authoritative and may intentionally supersede stale local YAML.
	if c.MCP.Enabled {
		if err := c.MCP.Validate(); err != nil {
			return err
		}
		if strings.TrimSpace(c.MCP.Transport) == MCPTransportStreamableHTTP {
			if err := validateServerMode(c.Server.Mode); err != nil {
				return err
			}
			if c.Server.Mode == "release" && !strings.EqualFold(strings.TrimSpace(c.MCP.AuthMode), MCPAuthModeOIDC) {
				return fmt.Errorf("mcp.auth_mode must be oidc for remote MCP in release mode")
			}
			if strings.EqualFold(strings.TrimSpace(c.MCP.AuthMode), MCPAuthModeOIDC) {
				if err := c.ValidateMCPReleaseSecurity(); err != nil {
					return err
				}
			} else if err := c.ValidateSecurity(); err != nil {
				return err
			}
		}
		return c.validateAuditSettings()
	}

	if err := c.Server.Validate(); err != nil {
		return err
	}
	if err := c.ValidateSecurity(); err != nil {
		return err
	}
	return c.validateAuditSettings()
}

func (c Config) validateAuditSettings() error {
	if c.Audit.RetentionDays < 0 {
		return fmt.Errorf("audit.retention_days cannot be negative")
	}
	if c.Server.Mode == "release" && !c.Audit.Enabled {
		return fmt.Errorf("audit.enabled cannot be disabled in release mode")
	}
	return nil
}

func (c DatabaseConfig) Validate() error {
	required := []struct {
		name  string
		value string
	}{
		{name: "database.host", value: c.Host},
		{name: "database.database", value: c.Database},
		{name: "database.username", value: c.Username},
	}
	for _, field := range required {
		if strings.TrimSpace(field.value) == "" {
			return fmt.Errorf("%s is required", field.name)
		}
	}
	if err := validatePort("database.port", c.Port); err != nil {
		return err
	}
	// Unsupported database.ssl values fail before the metadata connection is
	// attempted, in every transport mode (#143).
	_, err := metadataTLSConfig(c.SSL)
	return err
}

func (c ServerConfig) Validate() error {
	if err := validateServerMode(c.Mode); err != nil {
		return err
	}
	if err := validatePort("server.port", c.Port); err != nil {
		return err
	}
	// A bind host may be an IP address (v4/v6) or a hostname resolved by the
	// listener; whitespace is never part of either (#127). Everything else —
	// unparseable addresses, unknown names, bad ports — is rejected when the
	// listener is opened at startup.
	if strings.ContainsAny(c.Host, " \t\r\n") {
		return fmt.Errorf("server.host must be an IP address or hostname, not %q", c.Host)
	}
	timeouts := []struct {
		name  string
		value time.Duration
	}{
		{name: "server.read_header_timeout", value: c.ReadHeaderTimeout},
		{name: "server.read_timeout", value: c.ReadTimeout},
		{name: "server.write_timeout", value: c.WriteTimeout},
		{name: "server.idle_timeout", value: c.IdleTimeout},
	}
	for _, timeout := range timeouts {
		if timeout.value <= 0 {
			return fmt.Errorf("%s must be positive", timeout.name)
		}
	}
	if c.MaxRequestBodyBytes <= 0 {
		return fmt.Errorf("server.max_request_body_bytes must be positive")
	}
	return nil
}

func (c QueryConfig) Validate() error {
	if c.SessionTTLSeconds < 0 || c.SessionTTLSeconds > maxQuerySessionTTLSeconds {
		return fmt.Errorf("query.session_ttl_seconds must be between 0 and %d", maxQuerySessionTTLSeconds)
	}
	if c.MaxConcurrentQueriesPerTenant < 0 {
		return fmt.Errorf("query.max_concurrent_queries_per_tenant cannot be negative")
	}
	if c.RateLimitPerMinutePerTenant < 0 {
		return fmt.Errorf("query.rate_limit_per_minute_per_tenant cannot be negative")
	}
	if c.RateLimitBurstPerTenant < 0 {
		return fmt.Errorf("query.rate_limit_burst_per_tenant cannot be negative")
	}
	if (c.RateLimitPerMinutePerTenant == 0) != (c.RateLimitBurstPerTenant == 0) {
		return fmt.Errorf("query.rate_limit_per_minute_per_tenant and query.rate_limit_burst_per_tenant must both be zero to disable query rate limiting")
	}
	switch strings.TrimSpace(c.ExecutionMode) {
	case "auto", "streaming", "pagination":
		return nil
	default:
		return fmt.Errorf("query.execution_mode must be auto, streaming, or pagination")
	}
}

func validatePort(name, value string) error {
	port, err := strconv.ParseUint(strings.TrimSpace(value), 10, 16)
	if err != nil || port == 0 {
		return fmt.Errorf("%s must be a port between 1 and 65535", name)
	}
	return nil
}

func validateServerMode(mode string) error {
	switch strings.TrimSpace(mode) {
	case "debug", "test", "release":
		return nil
	default:
		return fmt.Errorf("server.mode must be debug, test, or release")
	}
}

func (c SQLTranslationConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Timeout <= 0 || c.Timeout > time.Minute {
		return fmt.Errorf("sql_translation.timeout must be positive and at most 1m")
	}
	if c.MaxConcurrent < 1 || c.MaxConcurrent > 32 {
		return fmt.Errorf("sql_translation.max_concurrent must be between 1 and 32")
	}
	if c.MaxSQLBytes < 1 || c.MaxSQLBytes > 16<<20 {
		return fmt.Errorf("sql_translation.max_sql_bytes must be between 1 and 16777216")
	}
	if c.MaxOutputBytes < 1 || c.MaxOutputBytes > 16<<20 {
		return fmt.Errorf("sql_translation.max_output_bytes must be between 1 and 16777216")
	}
	for name, value := range map[string]string{
		"sql_translation.python_path": c.PythonPath,
		"sql_translation.script_path": c.ScriptPath,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required when translation is enabled", name)
		}
	}
	return nil
}

func (c Config) ValidateMCPReleaseSecurity() error {
	if c.Server.Mode == "release" && !c.Security.EnableAuth {
		return fmt.Errorf("security.enable_auth cannot be disabled in release mode")
	}
	if c.Server.Mode == "release" && !c.Security.EnforceReadOnly {
		return fmt.Errorf("security.enforce_read_only cannot be disabled in release mode")
	}
	return nil
}
