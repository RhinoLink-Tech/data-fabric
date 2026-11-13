// Package onboardingruntime assembles an opt-in onboarding surface. It is not
// connected to cmd/server until a deployment supplies an enforced egress guard.
package onboardingruntime

import (
	"context"
	"embed"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/mcp"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/browserauth"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	web "github.com/RhinoLink-Tech/data-fabric/internal/workbuddy/onboardinghttp"
	"github.com/google/uuid"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
)

//go:embed ui/index.html ui/onboarding.css ui/onboarding.js
var assets embed.FS

type Options struct {
	Enabled                     bool
	Origin, MCPIssuer, AuditKey string
	CIDRs                       []string
	Ports                       []int
	DB                          *gorm.DB
	Vault                       *security.CredentialVault
	Validator                   serviceValidator
	MCP                         *mcp.MCPServer
	Fallback                    http.Handler
	OAuth                       oauth2.Config
	Verifier                    browserauth.Verifier
	HTTPClient                  *http.Client
	Pool                        *database.ConnectionPool
	Policy                      *database.FixedIPEgressPolicy
}
type serviceValidator interface {
	IsSupportedDataSourceType(model.DatabaseType) bool
	ValidateDataSourceConfiguration(*model.DataSourceConfig, model.DatabaseType) error
}
type Runtime struct {
	Handler http.Handler
	Grants  *onboarding.GrantStore
	stop    context.CancelFunc
	wg      sync.WaitGroup
}

func (r *Runtime) Close() {
	if r != nil && r.stop != nil {
		r.stop()
		r.wg.Wait()
	}
}

// restrictedProbe applies the query pool's immutable policy to the pinned
// destination for BOTH test and creation, including the configured port.
type restrictedProbe struct {
	probe  *web.AllowedProbe
	policy *database.FixedIPEgressPolicy
}

func (p restrictedProbe) Pin(ctx context.Context, t model.DatabaseType, c model.DataSourceConfig) (model.DataSourceConfig, error) {
	pinned, err := p.probe.Pin(ctx, t, c)
	if err != nil {
		return model.DataSourceConfig{}, err
	}
	if err := p.policy.Check(&model.DataSource{Type: t, Config: pinned}); err != nil {
		return model.DataSourceConfig{}, errors.New("address not permitted")
	}
	return pinned, nil
}
func (p restrictedProbe) Test(ctx context.Context, t model.DatabaseType, c model.DataSourceConfig) (bool, error) {
	pinned, err := p.Pin(ctx, t, c)
	if err != nil {
		return false, err
	}
	return p.probe.TestPinned(ctx, t, pinned, p.policy)
}

type grants struct {
	store    *onboarding.GrantStore
	db       *gorm.DB
	vault    *security.CredentialVault
	repo     repository.DataSourceRepository
	auditKey string
}

// Hold the same grant row lock used by revoke for the entire management
// operation, including connection testing. Source mutations use the ordinary
// scoped service and retain its independent ownership and tenant checks.
func (g grants) WithManagementGrant(ctx context.Context, issuer, subject, owner, tenant string, action func(context.Context) error) error {
	if action == nil {
		return onboarding.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var committed func()
	err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := onboarding.LockAndVerifyGrant(ctx, tx, issuer, subject, owner, tenant); err != nil {
			return onboarding.ErrUnavailable
		}
		var actionCtx context.Context
		actionCtx, committed = repository.WithSourceManagementTransaction(ctx, tx)
		return action(actionCtx)
	})
	if err == nil && committed != nil {
		committed()
	}
	return err
}

// RecordManagementSuccess writes only trusted identifiers and fixed operations
// using the same transaction as authorization and mutation.
func (g grants) RecordManagementSuccess(ctx context.Context, owner, tenant, operation, id string) error {
	tx := repository.SourceManagementTransaction(ctx)
	if tx == nil {
		return onboarding.ErrUnavailable
	}
	switch operation {
	case "datasource.rename", "datasource.status", "datasource.delete":
	default:
		return onboarding.ErrUnavailable
	}
	parsed, err := uuid.Parse(id)
	if err != nil {
		return onboarding.ErrUnavailable
	}
	store, err := auditlog.NewStore(tx, g.auditKey, 90)
	if err != nil {
		return err
	}
	return store.Record(ctx, auditlog.Event{ActorID: owner, TenantID: tenant, Transport: "mcp", Operation: operation, DataSourceID: parsed.String(), Decision: auditlog.DecisionAllow, Outcome: "success"})
}

func (g grants) Resolve(ctx context.Context, issuer, subject string) (string, string, bool, error) {
	return g.store.Resolve(ctx, issuer, subject)
}
func (g grants) AuthorizeOnboarding(ctx context.Context, p onboarding.Principal, c *security.Caller, id string) error {
	user, tenant, ok, e := g.store.Resolve(ctx, p.Issuer, p.Subject)
	if e != nil || !ok || c == nil || user != c.UserID || tenant != c.TenantID || tenant != p.Tenant {
		return onboarding.ErrUnavailable
	}
	if id == "" {
		return nil
	}
	ds, e := g.repo.GetByID(ctx, id)
	if e != nil || ds.OwnerID != user || ds.TenantID != tenant {
		return onboarding.ErrUnavailable
	}
	return nil
}

func (g grants) AuthorizedStatus(ctx context.Context, p onboarding.Principal, c *security.Caller, id string, flow *onboarding.Store) (state, source string, err error) {
	if c == nil || c.UserID != p.Subject || c.TenantID != p.Tenant {
		return "", "", onboarding.ErrUnavailable
	}
	err = g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := onboarding.LockAndVerifyGrant(ctx, tx, p.Issuer, p.Subject, c.UserID, c.TenantID); e != nil {
			return e
		}
		var e error
		state, source, e = flow.InspectTx(ctx, tx, id, p)
		if e != nil {
			return e
		}
		if state == "completed" {
			repo, e := repository.NewDataSourceRepository(tx, g.vault)
			if e != nil {
				return e
			}
			ds, e := repo.GetByID(ctx, source)
			if e != nil || ds.OwnerID != c.UserID || ds.TenantID != c.TenantID {
				return onboarding.ErrUnavailable
			}
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}
	return state, source, nil
}

type audited struct {
	delegate mcp.OnboardingCoordinator
	store    *auditlog.Store
}

func (a audited) record(ctx context.Context, p onboarding.Principal, c *security.Caller, op string, ok bool) error {
	actor := ""
	tenant := ""
	if c != nil {
		actor = c.UserID
		tenant = c.TenantID
	}
	decision := auditlog.DecisionDeny
	outcome := "failure"
	if ok {
		decision = auditlog.DecisionAllow
		outcome = "success"
	}
	return a.store.Record(ctx, auditlog.Event{ActorID: actor, TenantID: tenant, Transport: "mcp", Operation: op, Decision: decision, Outcome: outcome})
}
func (a audited) Begin(ctx context.Context, p onboarding.Principal, c *security.Caller) (string, error) {
	v, e := a.delegate.Begin(ctx, p, c)
	if a.record(ctx, p, c, "onboarding.begin", e == nil) != nil {
		return "", onboarding.ErrUnavailable
	}
	return v, e
}
func (a audited) Status(ctx context.Context, p onboarding.Principal, c *security.Caller, id string) (string, string, error) {
	s, v, e := a.delegate.Status(ctx, p, c, id)
	if a.record(ctx, p, c, "onboarding.status", e == nil) != nil {
		return "", "", onboarding.ErrUnavailable
	}
	return s, v, e
}
func New(ctx context.Context, o Options) (*Runtime, error) {
	if !o.Enabled {
		if o.Fallback == nil {
			return nil, errors.New("fallback required")
		}
		return &Runtime{Handler: o.Fallback}, nil
	}
	if o.DB == nil || o.Vault == nil || o.Validator == nil || o.MCP == nil || o.Fallback == nil || o.Verifier == nil || o.HTTPClient == nil || o.Pool == nil || o.Policy == nil || len(strings.TrimSpace(o.AuditKey)) < 32 || o.MCPIssuer == "" || o.Verifier.Issuer() != o.MCPIssuer {
		return nil, errors.New("onboarding dependencies unavailable")
	}
	u, e := url.Parse(o.Origin)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("invalid onboarding origin")
	}
	if !o.Pool.UsesEgressPolicy(o.Policy) {
		return nil, errors.New("egress policy unavailable")
	}
	probe, e := web.NewAllowedProbe(o.CIDRs, o.Ports, 4)
	if e != nil {
		return nil, e
	}
	for _, migration := range []func(*gorm.DB) error{onboarding.Migrate, onboarding.MigrateCompletions, onboarding.MigrateQuota, onboarding.MigrateGrants, browserauth.Migrate} {
		if e = migration(o.DB); e != nil {
			return nil, errors.New("onboarding schema unavailable")
		}
	}
	if !o.DB.Migrator().HasTable(&auditlog.Event{}) {
		return nil, errors.New("audit schema unavailable")
	}
	// SQLite remains available only to isolated tests; real deployments in
	// cmd/server use MySQL and must verify InnoDB before exposing routes.
	if o.DB.Dialector.Name() == "mysql" {
		if e = onboarding.RequireInnoDB(o.DB, onboarding.RequiredTables); e != nil {
			return nil, e
		}
	} else if o.DB.Dialector.Name() != "sqlite" {
		return nil, errors.New("unsupported metadata dialect")
	}
	grantStore, e := onboarding.NewGrantStore(o.DB)
	if e != nil {
		return nil, e
	}
	repo, e := repository.NewDataSourceRepository(o.DB, o.Vault)
	if e != nil {
		return nil, e
	}
	gs := grants{store: grantStore, db: o.DB, vault: o.Vault, repo: repo, auditKey: o.AuditKey}
	flow, e := onboarding.New(o.DB, 10*time.Minute)
	if e != nil {
		return nil, e
	}
	sessions, e := browserauth.New(o.DB, o.Vault, 5*time.Minute, 30*time.Minute)
	if e != nil {
		return nil, e
	}
	login, e := browserauth.NewHandler(sessions, o.OAuth, o.Verifier, gs, flow, o.HTTPClient, o.Origin)
	if e != nil {
		return nil, e
	}
	manager, e := web.New(o.DB, o.Vault, o.Validator, web.BrowserAuth{Handler: login, Origin: o.Origin}, flow, restrictedProbe{probe, o.Policy}, web.TransactionalAudit{FingerprintKey: o.AuditKey}, []model.DatabaseType{model.DatabaseTypeMySQL, model.DatabaseTypePostgreSQL})
	if e != nil {
		return nil, e
	}
	coordinator, e := mcp.NewBoundedOnboardingCoordinator(flow, gs, 4, 64, 4096)
	if e != nil {
		return nil, e
	}
	audit, e := auditlog.NewStore(o.DB, o.AuditKey, 90)
	if e != nil {
		return nil, e
	}
	if e = o.MCP.SetSourceManagementGrant(gs); e != nil {
		return nil, e
	}
	if e = o.MCP.SetOnboardingCoordinator(audited{coordinator, audit}, o.Origin); e != nil {
		return nil, e
	}
	o.MCP.SetSourceTypePolicy(o.Policy, manager.AllowsSourceType)
	runCtx, stop := context.WithCancel(ctx)
	runtime := &Runtime{Grants: grantStore, stop: stop}
	runtime.wg.Add(1)
	go func() {
		defer runtime.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case <-ticker.C:
				_, _ = flow.PurgeBounded(runCtx, 100)
				_, _ = flow.PurgeCompletions(runCtx, 100)
				_ = sessions.PurgeBounded(runCtx, 100)
			}
		}
	}()
	runtime.Handler = route(o.Fallback, login, manager, u.Host)
	return runtime, nil
}
func route(fallback http.Handler, login *browserauth.Handler, manager *web.Handler, originHost string) http.Handler {
	slots := make(chan struct{}, 16)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/onboarding/") {
			fallback.ServeHTTP(w, r)
			return
		}
		if !strings.EqualFold(r.Host, originHost) {
			http.Error(w, "request rejected", 400)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if len(r.URL.RequestURI()) > 8192 {
			http.Error(w, "request rejected", 400)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "service busy", 429)
			return
		}
		switch r.URL.Path {
		case "/onboarding/auth/login":
			login.Login(w, r)
			return
		case "/onboarding/auth/callback":
			login.Callback(w, r)
			return
		case "/onboarding/auth/logout":
			login.Logout(w, r)
			return
		case "/onboarding/session":
			login.Session(w, r)
			return
		case "/onboarding/source-types":
			manager.Types(w, r)
			return
		case "/onboarding/assets/onboarding.js", "/onboarding/assets/onboarding.css":
			if r.Method != "GET" {
				http.Error(w, "method not allowed", 405)
				return
			}
			name := strings.TrimPrefix(r.URL.Path, "/onboarding/assets/")
			b, e := assets.ReadFile("ui/" + name)
			if e != nil {
				http.NotFound(w, r)
				return
			}
			if strings.HasSuffix(name, ".js") {
				w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			} else {
				w.Header().Set("Content-Type", "text/css; charset=utf-8")
			}
			_, _ = w.Write(b)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/onboarding/flows/")
		if path == r.URL.Path {
			http.NotFound(w, r)
			return
		}
		parts := strings.Split(path, "/")
		if len(parts) == 1 && len(parts[0]) == 43 && r.Method == "GET" {
			b, _ := assets.ReadFile("ui/index.html")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(b)
			return
		}
		if len(parts) == 2 && len(parts[0]) == 43 && (parts[1] == "test" || parts[1] == "sources") {
			manager.ServeFlow(w, r, parts[0], parts[1])
			return
		}
		http.NotFound(w, r)
	})
}
