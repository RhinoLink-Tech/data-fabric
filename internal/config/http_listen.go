package config

import (
	"net"
	"strings"
)

// HTTPListenAddress builds the HTTP listener address from server.host and
// server.port (#127). An empty host binds every interface, which keeps
// container deployments working; IPv6 hosts are bracketed by net.JoinHostPort.
func (c Config) HTTPListenAddress() string {
	return net.JoinHostPort(strings.TrimSpace(c.Server.Host), strings.TrimSpace(c.Server.Port))
}
