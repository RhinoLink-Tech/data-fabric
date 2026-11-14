package traditional

import (
	"net"
	"net/netip"
	"strings"
)

// normalizeDSNHost accepts a bracketed URI-escaped IPv6 zone as in legacy
// PostgreSQL URLs, while passing ordinary raw hostnames through unchanged.
func normalizeDSNHost(host string) string {
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
		host = strings.Replace(host, "%25", "%", 1)
	}
	return host
}

// safeDSNHost excludes syntax delimiters that can change the driver target.
func safeDSNHost(host string) bool {
	if host == "" {
		return false
	}
	host = normalizeDSNHost(host)
	if len(host) > 253 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	// Scoped IPv6 addresses are valid connection targets on local networks.
	// Limit zone characters so the host cannot introduce DSN delimiters.
	if addr, err := netip.ParseAddr(host); err == nil && addr.Is6() && addr.Zone() != "" {
		for _, r := range addr.Zone() {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
				return false
			}
		}
		return true
	}
	for _, r := range host {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
