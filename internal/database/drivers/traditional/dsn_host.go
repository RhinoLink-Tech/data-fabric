package traditional

import (
	"golang.org/x/net/idna"
	"net"
	"net/netip"
	"strings"
)

// normalizeDSNHost supports the legacy PostgreSQL URL-escaped zone while
// treating MySQL zone identifiers literally. For PostgreSQL, use an
// unbracketed host if the literal zone starts with 25 to avoid ambiguity.
func normalizeDSNHost(host string, postgres bool) (string, bool) {
	if host == "" {
		return "", false
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = host[1 : len(host)-1]
		if postgres && strings.Contains(host, "%25") {
			host = strings.Replace(host, "%25", "%", 1)
		}
	}
	if len(host) > 253 {
		return "", false
	}
	if net.ParseIP(host) != nil {
		return host, true
	}
	if addr, err := netip.ParseAddr(host); err == nil && addr.Is6() && addr.Zone() != "" {
		for _, r := range addr.Zone() {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
				return "", false
			}
		}
		return host, true
	}
	if strings.ContainsFunc(host, func(r rune) bool { return r > 127 }) {
		var err error
		host, err = idna.Lookup.ToASCII(host)
		if err != nil || len(host) > 253 {
			return "", false
		}
	}
	for _, r := range host {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_') {
			return "", false
		}
	}
	return host, host != ""
}
