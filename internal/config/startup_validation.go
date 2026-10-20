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
	if err := c.SQLTranslation.Validate(); err != nil {
		return err
	}

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
				return c.ValidateMCPReleaseSecurity()
			}
			return c.ValidateSecurity()
		}
		return nil
	}

	if err := c.Server.Validate(); err != nil {
		return err
	}
	return c.ValidateSecurity()
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
	return validatePort("database.port", c.Port)
}

func (c ServerConfig) Validate() error {
	if err := validateServerMode(c.Mode); err != nil {
		return err
	}
	if err := validatePort("server.port", c.Port); err != nil {
		return err
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
