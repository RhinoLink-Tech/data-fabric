package model

import (
	"net/url"
	"strings"
)

// PublicDataSourceHost removes URI userinfo, query parameters and fragments.
// Query parameter names are not a reliable indicator of whether their values
// are secrets, so all query values are hidden. Non-URL hosts are returned
// unchanged unless they contain URL delimiters that could conceal a secret.
func PublicDataSourceHost(host string) string {
	if !strings.ContainsAny(host, "@?#") {
		return host
	}
	u, err := url.Parse(host)
	if err != nil || u.Scheme == "" || u.Host == "" || u.Opaque != "" {
		return "[redacted]"
	}
	if u.User != nil {
		u.User = url.User("redacted")
	}
	if u.RawQuery != "" || u.ForceQuery {
		u.RawQuery = "redacted"
		u.ForceQuery = false
	}
	if u.Fragment != "" || u.RawFragment != "" {
		u.Fragment = "redacted"
		u.RawFragment = ""
	}
	return u.String()
}

// ContainsRedactedHostMarker identifies the placeholders emitted by
// PublicDataSourceHost. It must not be used as an authorization decision.
func ContainsRedactedHostMarker(host string) bool {
	if host == "[redacted]" {
		return true
	}
	u, err := url.Parse(host)
	if err != nil {
		return strings.Contains(host, "[redacted]")
	}
	if u.User != nil && u.User.Username() == "redacted" {
		return true
	}
	if _, ok := u.Query()["redacted"]; ok {
		return true
	}
	return u.Fragment == "redacted"
}
