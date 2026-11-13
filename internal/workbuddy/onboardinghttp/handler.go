// Package onboardinghttp contains an opt-in browser onboarding handler. It is
// not registered by default; callers must provide trusted auth, audit and a
// network-restricted probe, and complete independent security review first.
package onboardinghttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	"gorm.io/gorm"
)

type Actor struct{ Issuer, Subject, Tenant, UserID, CSRF string }

// Auth must revalidate the browser session and resolve current source-management
// permission on EVERY request, using the same canonical mapping as MCP.
type Auth interface {
	Current(*http.Request) (Actor, error)
	CheckMutation(*http.Request, string) bool
	LockAuthorization(context.Context, *gorm.DB, Actor) error
}
type Store interface {
	Inspect(context.Context, string, onboarding.Principal) (string, string, error)
	ApplyResult(context.Context, string, onboarding.Principal, func(*gorm.DB) (string, error)) (string, error)
}

// Probe must enforce a deployment-owned network allowlist with DNS-rebinding
// protection and bounded time/resources; callers must not wire a generic dialer.
type Probe interface {
	Test(context.Context, model.DatabaseType, model.DataSourceConfig) (bool, error)
	Pin(context.Context, model.DatabaseType, model.DataSourceConfig) (model.DataSourceConfig, error)
}

// Audit must write a credential-free event on tx in the same transaction.
type Audit interface {
	Created(context.Context, *gorm.DB, Actor, string) error
	Attempt(context.Context, *gorm.DB, Actor, string, string, int) error
}
type Handler struct {
	db        *gorm.DB
	vault     *security.CredentialVault
	validator service.DataSourceConfigValidator
	auth      Auth
	store     Store
	probe     Probe
	audit     Audit
	types     map[model.DatabaseType]bool
}

func New(db *gorm.DB, vault *security.CredentialVault, validator service.DataSourceConfigValidator, auth Auth, store Store, probe Probe, audit Audit, allowed []model.DatabaseType) (*Handler, error) {
	if db == nil || vault == nil || validator == nil || auth == nil || store == nil || probe == nil || audit == nil {
		return nil, errors.New("onboarding dependencies required")
	}
	types := map[model.DatabaseType]bool{}
	for _, t := range allowed {
		if t != model.DatabaseTypeMySQL && t != model.DatabaseTypePostgreSQL {
			return nil, errors.New("unsupported onboarding source type")
		}
		types[t] = true
	}
	if len(types) == 0 {
		return nil, errors.New("onboarding types required")
	}
	return &Handler{db, vault, validator, auth, store, probe, audit, types}, nil
}

// sourceInput is deliberately narrower than the general REST model: it cannot
// supply connection pool sizes, DSNs, filesystem paths, arbitrary TLS names or extras.
type sourceInput struct {
	Name   string             `json:"name"`
	Type   model.DatabaseType `json:"type"`
	Config struct {
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Database string `json:"database"`
		Schema   string `json:"schema"`
		Username string `json:"username"`
		Password string `json:"password"`
		SSL      bool   `json:"ssl"`
		TLSCA    string `json:"tlsCA"`
	} `json:"config"`
}

var hostPattern = regexp.MustCompile(`^[a-zA-Z0-9.\-_:\[\]]{1,253}$`)

// Bound identifiers; both username and database must be non-empty to avoid driver environment fallbacks.
var safeIdentifier = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,128}$`)

func actorValid(a Actor) bool {
	return a.Issuer != "" && a.Subject != "" && a.Tenant != "" && a.UserID != "" && a.CSRF != ""
}
func principal(a Actor) onboarding.Principal {
	return onboarding.Principal{Issuer: a.Issuer, Subject: a.Subject, Tenant: a.Tenant}
}
func generic(w http.ResponseWriter, code int) { http.Error(w, http.StatusText(code), code) }
func jsonOut(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// AllowsSourceType reports the wizard type gate, not authorization, network, or connectivity.
func (h *Handler) AllowsSourceType(t model.DatabaseType) bool {
	return h != nil && h.types[t] && h.validator.IsSupportedDataSourceType(t)
}

// Types exposes ONLY deployment-approved onboarding connectors after auth.
func (h *Handler) Types(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" {
		generic(w, 405)
		return
	}
	a, e := h.auth.Current(r)
	if e != nil || !actorValid(a) {
		generic(w, 403)
		return
	}
	list := []map[string]any{}
	for _, t := range []model.DatabaseType{model.DatabaseTypePostgreSQL, model.DatabaseTypeMySQL} {
		if h.AllowsSourceType(t) {
			list = append(list, map[string]any{"type": t, "name": string(t), "enabled": true})
		}
	}
	jsonOut(w, list)
}
func (h *Handler) ServeFlow(w http.ResponseWriter, r *http.Request, id, action string) {
	w.Header().Set("Cache-Control", "no-store")
	var auditActor Actor
	reject := func(code int) {
		if h.audit.Attempt(r.Context(), h.db, auditActor, action, "failure", code) != nil {
			generic(w, 503)
			return
		}
		generic(w, code)
	}
	if r.Method != "POST" {
		reject(405)
		return
	}
	a, e := h.auth.Current(r)
	if e != nil || !actorValid(a) || !h.auth.CheckMutation(r, a.CSRF) {
		reject(403)
		return
	}
	auditActor = a
	p := principal(a)
	state, source, e := h.store.Inspect(r.Context(), id, p)
	if e != nil {
		reject(404)
		return
	}
	if state == "completed" {
		if action != "sources" {
			reject(409)
			return
		}
		h.returnExisting(w, r, a, source, true)
		return
	}
	if state != "pending" {
		reject(404)
		return
	}
	if action != "sources" && action != "test" {
		reject(404)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var input sourceInput
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil {
		reject(400)
		return
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		reject(400)
		return
	}
	req := service.CreateDataSourceRequest{Name: strings.TrimSpace(input.Name), Type: input.Type, Config: model.DataSourceConfig{Host: input.Config.Host, Port: input.Config.Port, Database: input.Config.Database, Schema: input.Config.Schema, Username: input.Config.Username, Password: input.Config.Password, SSL: input.Config.SSL, TLSCA: input.Config.TLSCA, MaxPoolSize: 2, Timeout: 5}}
	c := req.Config
	if !safeIdentifier.MatchString(c.Database) || !safeIdentifier.MatchString(c.Username) || (c.Schema != "" && !safeIdentifier.MatchString(c.Schema)) || !database.ValidOnboardingPassword(c.Password) {
		reject(400)
		return
	}
	if !h.types[req.Type] || strings.TrimSpace(req.Name) == "" || len(req.Name) > 255 || !hostPattern.MatchString(c.Host) || c.Port < 1 || c.Port > 65535 || len(c.AdditionalProps) != 0 || strings.Contains(strings.ToUpper(c.SSLRootCert), "PRIVATE KEY") {
		reject(400)
		return
	}
	// Trust anchors are operator-selected aliases, never user-supplied paths.
	if c.SSLRootCert != "" || (c.TLSCA != "" && c.TLSCA != "system" && c.TLSCA != "custom") || (!c.SSL && c.TLSCA != "") {
		reject(422)
		return
	}
	if !h.validator.IsSupportedDataSourceType(req.Type) || h.validator.ValidateDataSourceConfiguration(&c, req.Type) != nil {
		reject(400)
		return
	}
	if action == "test" {
		ok, err := h.probe.Test(r.Context(), req.Type, c)
		if err != nil {
			reject(502)
			return
		}
		outcome := "failure"
		if ok {
			outcome = "success"
		}
		if h.audit.Attempt(r.Context(), h.db, a, "test", outcome, 200) != nil {
			generic(w, 503)
			return
		}
		jsonOut(w, map[string]bool{"connected": ok})
		return
	}
	// Resolve and pin again at creation; a prior successful test grants no access.
	pinned, err := h.probe.Pin(r.Context(), req.Type, c)
	if err != nil {
		reject(400)
		return
	}
	req.Config = pinned
	caller := &security.Caller{UserID: a.UserID, TenantID: a.Tenant, Scopes: []string{"data-fabric.sources.manage"}}
	createdNow := false
	newID, err := h.store.ApplyResult(r.Context(), id, p, func(tx *gorm.DB) (string, error) {
		if e := h.auth.LockAuthorization(r.Context(), tx, a); e != nil {
			return "", e
		}
		repo, e := repository.NewDataSourceRepository(tx, h.vault)
		if e != nil {
			return "", e
		}
		svc := service.NewDataSourceService(repo, nil, h.validator)
		created, e := svc.CreateDataSource(r.Context(), caller, &req)
		if e != nil {
			return "", e
		}
		if e = h.audit.Created(r.Context(), tx, a, created.ID); e != nil {
			return "", e
		}
		createdNow = true
		return created.ID, nil
	})
	if err != nil {
		reject(400)
		return
	}
	h.returnExisting(w, r, a, newID, !createdNow)
}
func (h *Handler) returnExisting(w http.ResponseWriter, r *http.Request, a Actor, id string, recovery bool) {
	var sourceID string
	err := h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if e := h.auth.LockAuthorization(r.Context(), tx, a); e != nil {
			return e
		}
		repo, e := repository.NewDataSourceRepository(tx, h.vault)
		if e != nil {
			return e
		}
		ds, e := repo.GetByID(r.Context(), id)
		if e != nil || ds.OwnerID != a.UserID || ds.TenantID != a.Tenant {
			return errors.New("source unavailable")
		}
		if recovery {
			if e = h.audit.Attempt(r.Context(), tx, a, "recover", "success", 200); e != nil {
				return e
			}
		}
		sourceID = ds.ID
		return nil
	})
	if err != nil {
		if recovery && h.audit.Attempt(r.Context(), h.db, a, "recover", "failure", 404) != nil {
			generic(w, 503)
			return
		}
		generic(w, 404)
		return
	}
	jsonOut(w, map[string]string{"id": sourceID})
}
