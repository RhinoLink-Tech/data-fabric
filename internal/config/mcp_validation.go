package config

import (
	"fmt"
	"net"
	"strings"
)

const (
	MCPTransportStdio          = "stdio"
	MCPTransportStreamableHTTP = "streamable-http"
)

// Validate checks the selected MCP transport and prevents an unencrypted
// remote listener from being exposed. Plain HTTP is limited to loopback so a
// TLS reverse proxy can terminate HTTPS in front of the process.
func (c MCPConfig) Validate() error {
	transport := strings.TrimSpace(c.Transport)
	if transport == "" {
		transport = MCPTransportStdio
	}
	if transport == MCPTransportStdio {
		return nil
	}
	if transport != MCPTransportStreamableHTTP {
		return fmt.Errorf("unsupported MCP transport %q: use %q or %q", transport, MCPTransportStdio, MCPTransportStreamableHTTP)
	}

	certConfigured := strings.TrimSpace(c.TLSCertFile) != ""
	keyConfigured := strings.TrimSpace(c.TLSKeyFile) != ""
	if certConfigured != keyConfigured {
		return fmt.Errorf("mcp.tls_cert_file and mcp.tls_key_file must be configured together")
	}

	address := strings.TrimSpace(c.Address)
	if address == "" {
		return fmt.Errorf("mcp.address is required for the Streamable HTTP transport")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid mcp.address %q: %w", address, err)
	}
	if err := validatePort("mcp.address port", port); err != nil {
		return err
	}
	if !certConfigured && !isLoopbackHost(host) {
		return fmt.Errorf("MCP Streamable HTTP without direct TLS must bind to loopback; expose it through an HTTPS reverse proxy or configure both TLS files")
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
