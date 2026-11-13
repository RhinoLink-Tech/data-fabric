package database

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"github.com/lib/pq"
	"net/netip"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

// FixedIPEgressPolicy is an immutable, opt-in deployment policy. It applies to
// EVERY source using this pool, including existing sources and metadata calls.
// Changing a policy requires restarting/draining the process: a previously
// returned *sql.DB or an already running query cannot be revoked by this API.
// The policy admits pinned IPv4 MySQL/PostgreSQL sources; TLS verifies
// the original hostname against system or operator-provisioned trust roots.
type FixedIPEgressPolicy struct {
	prefixes []netip.Prefix
	ports    map[int]struct{}
	caTag    string
	caPEM    []byte
}

var safeDSNPart = regexp.MustCompile(`^[A-Za-z0-9._~-]*$`)
var ErrEgressDenied = errors.New("data source blocked by egress policy")

func NewFixedIPEgressPolicy(cidrs []string, ports []int) (*FixedIPEgressPolicy, error) {
	p := &FixedIPEgressPolicy{ports: make(map[int]struct{})}
	for _, s := range cidrs {
		prefix, err := netip.ParsePrefix(s)
		if err != nil || !prefix.Addr().Is4() {
			return nil, ErrEgressDenied
		}
		p.prefixes = append(p.prefixes, prefix.Masked())
	}
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return nil, ErrEgressDenied
		}
		p.ports[port] = struct{}{}
	}
	if len(p.prefixes) == 0 || len(p.ports) == 0 {
		return nil, ErrEgressDenied
	}
	return p, nil
}

// NewFixedIPEgressPolicyWithCA adds one operator-provisioned custom CA.
// caFile is configured by the administrator, never accepted from a source.
func NewFixedIPEgressPolicyWithCA(cidrs []string, ports []int, caFile string) (*FixedIPEgressPolicy, error) {
	p, err := NewFixedIPEgressPolicy(cidrs, ports)
	if err != nil {
		return nil, err
	}
	if caFile == "" {
		return p, nil
	}
	pem, err := readOperatorCA(caFile)
	if err != nil {
		return nil, ErrEgressDenied
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, ErrEgressDenied
	}
	sum := sha256.Sum256(pem)
	tag := "data-fabric-workbuddy-" + hex.EncodeToString(sum[:])
	if err := pq.RegisterTLSConfig(tag, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}); err != nil {
		return nil, ErrEgressDenied
	}
	p.caTag, p.caPEM = tag, append([]byte(nil), pem...)
	return p, nil
}

// NewRestrictedConnectionPool enforces p on each pool lease and native API
// dispatch. A non-nil policy is mandatory and cannot be reconfigured in place.
func NewRestrictedConnectionPool(registry *DriverRegistry, tm *security.TokenManager, vault *security.CredentialVault, readOnly bool, p *FixedIPEgressPolicy) (*ConnectionPool, error) {
	if p == nil || len(p.prefixes) == 0 || len(p.ports) == 0 {
		return nil, ErrEgressDenied
	}
	cp := NewConnectionPool(registry, tm, vault, readOnly)
	cp.egress = p
	return cp, nil
}

// AllowsSourceType reports only the connector-type constraint. A true result
// does not grant access or imply that a source configuration will pass policy.
func (p *FixedIPEgressPolicy) AllowsSourceType(t model.DatabaseType) bool {
	return p != nil && (t == model.DatabaseTypeMySQL || t == model.DatabaseTypePostgreSQL)
}

func (p *FixedIPEgressPolicy) Check(ds *model.DataSource) error {
	if ds == nil || !p.AllowsSourceType(ds.Type) {
		return ErrEgressDenied
	}
	c := ds.Config
	ip, err := netip.ParseAddr(c.Host)
	if err != nil || !ip.Is4() {
		return ErrEgressDenied
	}
	_, ok := p.ports[c.Port]
	if !ok {
		return ErrEgressDenied
	}
	allowed := false
	for _, prefix := range p.prefixes {
		if prefix.Contains(ip) {
			allowed = true
			break
		}
	}
	if !allowed || c.SSLRootCert != "" || c.Timezone != "" || len(c.AdditionalProps) != 0 {
		return ErrEgressDenied
	}
	if c.SSL {
		if c.SSLMode != "" && c.SSLMode != "verify-full" {
			return ErrEgressDenied
		}
		if c.TLSServerName == "" || !probeHostname.MatchString(c.TLSServerName) || strings.Contains(c.TLSServerName, "..") || len(c.TLSServerName) > 253 {
			return ErrEgressDenied
		}
		if c.TLSCA != "" && c.TLSCA != "system" && (c.TLSCA != "custom" || p.caTag == "") {
			return ErrEgressDenied
		}
	} else if c.TLSServerName != "" || c.TLSCA != "" || (c.SSLMode != "" && c.SSLMode != "disable") {
		return ErrEgressDenied
	}
	if c.Username == "" || c.Database == "" || !safeDSNPart.MatchString(c.Username) || !ValidOnboardingPassword(c.Password) || !safeDSNPart.MatchString(c.Database) {
		return ErrEgressDenied
	}
	return nil
}

// UsesEgressPolicy reports whether this exact immutable policy guards this pool.
// The deployment must also use this pool for every database execution path.
func (cp *ConnectionPool) UsesEgressPolicy(p *FixedIPEgressPolicy) bool {
	return cp != nil && p != nil && cp.egress == p
}

// ValidOnboardingPassword permits punctuation and Unicode without allowing
// control characters or unbounded credential inputs.
func ValidOnboardingPassword(v string) bool {
	if len(v) > 256 || !utf8.ValidString(v) {
		return false
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
