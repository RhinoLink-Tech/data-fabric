package onboardinghttp

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"io"
	"net/http"
	"strings"
)

type updateBrowserStore interface {
	updateFlowStore
	InspectUpdateTx(ctx context.Context, tx *gorm.DB, id string, p onboarding.Principal, target string) (string, string, error)
}

// ServeUpdateFlow is not registered by default. The embedding deployment must
// supply a browser session, same-origin CSRF checks, a safe egress policy,
// bounded flows and safe pool semantics on every replica.
func (h *Handler) ServeUpdateFlow(w http.ResponseWriter, r *http.Request, store updateBrowserStore, audit updateAudit, pool updateInvalidator, flowID, target, action string) {
	w.Header().Set("Cache-Control", "no-store")
	reject := func(code int) {
		if h.audit.Attempt(r.Context(), h.db, Actor{}, "update", "failure", code) != nil {
			generic(w, 503)
			return
		}
		generic(w, code)
	}
	if r.Method != "POST" || store == nil || audit == nil || pool == nil || (action != "test" && action != "update") {
		reject(400)
		return
	}
	u, e := uuid.Parse(target)
	if e != nil || u.String() != target {
		reject(400)
		return
	}
	a, e := h.auth.Current(r)
	if e != nil || !actorValid(a) || !h.auth.CheckMutation(r, a.CSRF) {
		reject(403)
		return
	}
	var typ model.DatabaseType
	var existing model.DataSourceConfig
	var state string
	e = h.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if e := h.auth.LockAuthorization(r.Context(), tx, a); e != nil {
			return e
		}
		var ds model.DataSource
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "owner_id", "tenant_id", "type").Where("id = ?", target).First(&ds).Error; e != nil || ds.OwnerID != a.UserID || ds.TenantID != a.Tenant {
			return errors.New("source unavailable")
		}
		var source string
		state, source, e = store.InspectUpdateTx(r.Context(), tx, flowID, principal(a), target)
		if e != nil || (state == "completed" && source != target) {
			return errors.New("flow unavailable")
		}
		repo, err := repository.NewDataSourceRepository(tx, h.vault)
		if err != nil {
			return err
		}
		current, err := repo.GetByID(r.Context(), target)
		if err != nil {
			return err
		}
		existing = current.Config
		typ = ds.Type
		return nil
	})
	if e != nil || !h.types[typ] {
		reject(404)
		return
	}
	if state == "completed" && action == "test" {
		reject(409)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var input struct {
		Name   string             `json:"name"`
		Type   model.DatabaseType `json:"type"`
		Config struct {
			Host     string  `json:"host"`
			Port     int     `json:"port"`
			Database string  `json:"database"`
			Schema   string  `json:"schema"`
			Username string  `json:"username"`
			Password string  `json:"password"`
			SSL      *bool   `json:"ssl"`
			TLSCA    *string `json:"tlsCA"`
		} `json:"config"`
	}
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
	cfg := model.DataSourceConfig{Host: input.Config.Host, Port: input.Config.Port, Database: input.Config.Database, Schema: input.Config.Schema, Username: input.Config.Username, Password: input.Config.Password, MaxPoolSize: 2, Timeout: 5}
	if input.Type != typ || !hostPattern.MatchString(cfg.Host) || cfg.Port < 1 || cfg.Port > 65535 || !safeIdentifier.MatchString(cfg.Database) || !safeIdentifier.MatchString(cfg.Username) || (cfg.Schema != "" && !safeIdentifier.MatchString(cfg.Schema)) || !database.ValidOnboardingPassword(cfg.Password) || strings.TrimSpace(input.Name) != "" {
		reject(400)
		return
	}
	cfg.SSL = existing.SSL
	if input.Config.SSL != nil {
		cfg.SSL = *input.Config.SSL
	}
	cfg.TLSCA = existing.TLSCA
	if input.Config.TLSCA != nil {
		cfg.TLSCA = *input.Config.TLSCA
	}
	if existing.SSL && !cfg.SSL {
		reject(422)
		return
	}
	if cfg.TLSCA != "" && cfg.TLSCA != "system" && cfg.TLSCA != "custom" {
		reject(422)
		return
	}
	if !cfg.SSL && cfg.TLSCA != "" {
		reject(422)
		return
	}
	if !h.validator.IsSupportedDataSourceType(typ) || h.validator.ValidateDataSourceConfiguration(&cfg, typ) != nil {
		reject(400)
		return
	}
	if action == "test" {
		ok, err := h.probe.Test(r.Context(), typ, cfg)
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
	pinned, err := h.probe.Pin(r.Context(), typ, cfg)
	if err != nil {
		reject(400)
		return
	}
	id, err := h.applyUpdate(r.Context(), store, audit, pool, a, flowID, target, typ, pinned)
	if err != nil {
		reject(400)
		return
	}
	jsonOut(w, map[string]string{"id": id})
}
