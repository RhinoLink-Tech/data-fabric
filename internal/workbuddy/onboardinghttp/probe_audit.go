package onboardinghttp

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"gorm.io/gorm"
)

// AllowedProbe pins every dial to a resolved address in an explicit operator
// allowlist. An empty list is an error; untrusted browser values cannot expand it.
// TLS probing requires the same immutable policy as the query pool.
type ipResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type AllowedProbe struct {
	prefixes []netip.Prefix
	ports    map[int]struct{}
	slots    chan struct{}
	resolver ipResolver
}

func NewAllowedProbe(cidrs []string, ports []int, concurrency int) (*AllowedProbe, error) {
	if len(cidrs) == 0 || len(ports) == 0 || concurrency < 1 || concurrency > 16 {
		return nil, errors.New("explicit probe allowlist and concurrency required")
	}
	p := &AllowedProbe{slots: make(chan struct{}, concurrency), resolver: net.DefaultResolver, ports: make(map[int]struct{})}
	for _, port := range ports {
		if port < 1 || port > 65535 {
			return nil, errors.New("invalid probe port")
		}
		p.ports[port] = struct{}{}
	}
	for _, raw := range cidrs {
		prefix, e := netip.ParsePrefix(strings.TrimSpace(raw))
		if e != nil || prefix.Bits() == 0 {
			return nil, errors.New("invalid probe allowlist")
		}
		p.prefixes = append(p.prefixes, prefix.Masked())
	}
	return p, nil
}

// Pin independently resolves each create attempt and preserves the certificate name.
func (p *AllowedProbe) Pin(ctx context.Context, t model.DatabaseType, c model.DataSourceConfig) (model.DataSourceConfig, error) {
	if p == nil || len(p.prefixes) == 0 || (t != model.DatabaseTypeMySQL && t != model.DatabaseTypePostgreSQL) {
		return model.DataSourceConfig{}, errors.New("network policy unavailable")
	}
	if _, ok := p.ports[c.Port]; !ok {
		return model.DataSourceConfig{}, errors.New("port not permitted")
	}
	if c.SSLRootCert != "" || c.TLSServerName != "" || (c.SSLMode != "" && c.SSLMode != "disable") || (!c.SSL && c.TLSCA != "") {
		return model.DataSourceConfig{}, errors.New("invalid TLS settings")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	host := strings.Trim(c.Host, "[]")
	ips, err := p.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return model.DataSourceConfig{}, errors.New("address resolution failed")
	}
	var chosen netip.Addr
	for _, ip := range ips {
		ip = ip.Unmap()
		if !ip.Is4() {
			continue
		}
		for _, prefix := range p.prefixes {
			if prefix.Contains(ip) {
				chosen = ip
				break
			}
		}
		if chosen.IsValid() {
			break
		}
	}
	if !chosen.IsValid() {
		return model.DataSourceConfig{}, errors.New("address not permitted")
	}
	if c.SSL {
		c.TLSServerName = host
	}
	c.Host = chosen.String()
	return c, nil
}
func (p *AllowedProbe) Test(ctx context.Context, t model.DatabaseType, c model.DataSourceConfig) (bool, error) {
	if p == nil || len(p.prefixes) == 0 {
		return false, errors.New("probe unavailable")
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		return false, errors.New("probe busy")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pinned, err := p.Pin(ctx, t, c)
	if err != nil {
		return false, errors.New("probe unavailable")
	}
	if pinned.SSL {
		return false, errors.New("TLS requires restricted policy")
	}
	hc := database.NewHealthChecker(nil)
	if err = hc.CheckStoredSourceConnectivity(ctx, &model.DataSource{Type: t, Config: pinned}); err != nil {
		return false, errors.New("probe failed")
	}
	return true, nil
}

// TransactionalAudit records identifiers only using the same transaction as
// creation. Configure a stable, non-empty fingerprint key for auditlog.Store.
type TransactionalAudit struct{ FingerprintKey string }

func (a TransactionalAudit) Created(ctx context.Context, tx *gorm.DB, actor Actor, id string) error {
	if tx == nil || len(strings.TrimSpace(a.FingerprintKey)) < 32 {
		return errors.New("audit unavailable")
	}
	store, e := auditlog.NewStore(tx, a.FingerprintKey, 90)
	if e != nil {
		return e
	}
	return store.Record(ctx, auditlog.Event{ActorID: actor.UserID, TenantID: actor.Tenant, Transport: "workbuddy_browser", Operation: "datasource.create", DataSourceID: id, Decision: auditlog.DecisionAllow, Outcome: "success", StatusCode: 200})
}

// Attempt records only fixed action, status and trusted identifiers; never
// include request bodies, source hosts, errors or credential-bearing values.
func (a TransactionalAudit) Attempt(ctx context.Context, db *gorm.DB, actor Actor, action, outcome string, status int) error {
	if db == nil || len(strings.TrimSpace(a.FingerprintKey)) < 32 {
		return errors.New("audit unavailable")
	}
	switch action {
	case "sources":
		action = "datasource.create"
	case "test":
		action = "datasource.test"
	case "recover":
		action = "datasource.recover"
	default:
		action = "datasource.unknown"
	}
	decision := auditlog.DecisionAllow
	if outcome != "success" {
		decision = auditlog.DecisionDeny
		outcome = "failure"
	}
	store, e := auditlog.NewStore(db, a.FingerprintKey, 90)
	if e != nil {
		return e
	}
	return store.Record(ctx, auditlog.Event{ActorID: actor.UserID, TenantID: actor.Tenant, Transport: "workbuddy_browser", Operation: action, Decision: decision, Outcome: outcome, StatusCode: status})
}

// TestPinned uses the same immutable policy and exact IP as the query pool.
func (p *AllowedProbe) TestPinned(ctx context.Context, t model.DatabaseType, c model.DataSourceConfig, policy *database.FixedIPEgressPolicy) (bool, error) {
	if p == nil || policy == nil {
		return false, errors.New("probe unavailable")
	}
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	default:
		return false, errors.New("probe busy")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := policy.CheckRestrictedSourceConnectivity(ctx, &model.DataSource{Type: t, Config: c}); err != nil {
		return false, errors.New("probe failed")
	}
	return true, nil
}
