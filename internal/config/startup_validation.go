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

	if c.MCP.Enabled {
		if err := c.MCP.Validate(); err != nil {
			return err
		}
		if strings.TrimSpace(c.MCP.Transport) == MCPTransportStreamableHTTP {
			if err := validateServerMode(c.Server.Mode); err != nil {
				return err
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
