// Package browserauth implements shared, one-use OIDC state and browser sessions.
// It does not register routes or grant management permissions.
package browserauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var ErrUnavailable = errors.New("browser authentication unavailable")

type loginRecord struct {
	ID        string `gorm:"primaryKey;size:64"`
	Binding   string `gorm:"size:64;not null"`
	FlowKey   string `gorm:"size:64;not null;index"`
	Encrypted string `gorm:"not null"`
	Expires   int64  `gorm:"not null;index"`
}

func (loginRecord) TableName() string { return "df_browser_login_states" }

type sessionRecord struct {
	ID        string `gorm:"primaryKey;size:64"`
	Encrypted string `gorm:"not null"`
	Expires   int64  `gorm:"not null;index"`
}

func (sessionRecord) TableName() string { return "df_browser_sessions" }

type loginSecret struct{ Verifier, Nonce, FlowID string }

// Identity is authentication only, not a tenant membership or management grant.
type Identity struct {
	Issuer, Subject string
	ExpiresAt       time.Time
}
type sessionSecret struct {
	Identity Identity
	CSRF     string
}
type quotaGate struct {
	ID string `gorm:"primaryKey;size:32"`
}

func (quotaGate) TableName() string { return "df_browser_auth_quota" }

type Store struct {
	db                   *gorm.DB
	vault                *security.CredentialVault
	loginTTL, sessionTTL time.Duration
}

func New(db *gorm.DB, vault *security.CredentialVault, loginTTL, sessionTTL time.Duration) (*Store, error) {
	if db == nil || db.Dialector == nil || vault == nil || loginTTL < time.Second || loginTTL > 10*time.Minute || sessionTTL < time.Second || sessionTTL > time.Hour {
		return nil, ErrUnavailable
	}
	if db.Dialector.Name() != "mysql" && db.Dialector.Name() != "sqlite" {
		return nil, ErrUnavailable
	}
	return &Store{db, vault, loginTTL, sessionTTL}, nil
}

// Migrate is additive; deploy schema before enabling this package.
func Migrate(db *gorm.DB) error {
	if db == nil {
		return ErrUnavailable
	}
	if err := db.AutoMigrate(&loginRecord{}, &sessionRecord{}, &quotaGate{}); err != nil {
		return err
	}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&quotaGate{ID: "global"}).Error
}
func now(db *gorm.DB) (int64, error) {
	var q string
	switch db.Dialector.Name() {
	case "mysql":
		q = "(UNIX_TIMESTAMP() * 1000 + MICROSECOND(CURRENT_TIMESTAMP(3)) DIV 1000)"
	case "sqlite":
		q = "CAST((julianday('now') - 2440587.5) * 86400000 AS INTEGER)"
	default:
		return 0, ErrUnavailable
	}
	var ms int64
	err := db.Raw("SELECT " + q).Scan(&ms).Error
	return ms, err
}
func random() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func hashKey(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func hash(v string) (string, error) {
	b, e := base64.RawURLEncoding.DecodeString(v)
	if e != nil || len(b) != 32 || base64.RawURLEncoding.EncodeToString(b) != v {
		return "", ErrUnavailable
	}
	h := sha256.Sum256([]byte(v))
	return hex.EncodeToString(h[:]), nil
}
func (s *Store) seal(v any) (string, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return "", ErrUnavailable
	}
	return s.vault.EncryptCredentials(b)
}
func (s *Store) unseal(v string, out any) error {
	b, e := s.vault.DecryptCredentials(v)
	if e != nil {
		return ErrUnavailable
	}
	if json.Unmarshal(b, out) != nil {
		return ErrUnavailable
	}
	return nil
}

// capacity serializes bounded cleanup and insertion across all replicas using one shared DB row.
func capacity(tx *gorm.DB, model any, limit int64, ms int64) error {
	var gate quotaGate
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", "global").First(&gate).Error; err != nil {
		return err
	}
	var ids []string
	if err := tx.Model(model).Where("expires <= ?", ms).Limit(128).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) > 0 {
		if err := tx.Where("id IN ?", ids).Delete(model).Error; err != nil {
			return err
		}
	}
	var count int64
	if err := tx.Model(model).Count(&count).Error; err != nil {
		return err
	}
	if count >= limit {
		return ErrUnavailable
	}
	return nil
}

// Begin returns state, browser binding, nonce and a PKCE challenge. The caller
// must place binding in a Secure HttpOnly SameSite=Lax cookie, never in a URL.
func (s *Store) Begin(ctx context.Context) (state, binding, nonce, challenge string, err error) {
	return s.BeginFlow(ctx, "")
}

func (s *Store) BeginFlow(ctx context.Context, flowID string) (state, binding, nonce, challenge string, err error) {
	if flowID != "" {
		if _, e := hash(flowID); e != nil {
			err = ErrUnavailable
			return
		}
	}
	state, err = random()
	if err != nil {
		return
	}
	binding, err = random()
	if err != nil {
		return
	}
	nonce, err = random()
	if err != nil {
		return
	}
	verifier, e := random()
	if e != nil {
		err = e
		return
	}
	id, _ := hash(state)
	bh, _ := hash(binding)
	flowKey := hashKey(flowID)
	enc, e := s.seal(loginSecret{Verifier: verifier, Nonce: nonce, FlowID: flowID})
	if e != nil {
		err = e
		return
	}
	ms, e := now(s.db.WithContext(ctx))
	if e != nil {
		err = e
		return
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := capacity(tx, &loginRecord{}, 2048, ms); e != nil {
			return e
		}
		var n int64
		if e := tx.Model(&loginRecord{}).Where("flow_key = ? AND expires > ?", flowKey, ms).Count(&n).Error; e != nil {
			return e
		}
		if n >= 3 {
			return ErrUnavailable
		}
		return tx.Create(&loginRecord{ID: id, Binding: bh, FlowKey: flowKey, Encrypted: enc, Expires: ms + s.loginTTL.Milliseconds()}).Error
	})
	challenge = oauth2.S256ChallengeFromVerifier(verifier)
	return
}

// Consume is atomic and single-use, including failure after consumption. The
// caller must only exchange a code after this succeeds, then verify ID token.
func (s *Store) Consume(ctx context.Context, state, binding string) (verifier, nonce string, err error) {
	verifier, nonce, _, err = s.ConsumeFlow(ctx, state, binding)
	return
}

func (s *Store) ConsumeFlow(ctx context.Context, state, binding string) (verifier, nonce, flowID string, err error) {
	id, e := hash(state)
	if e != nil {
		return "", "", "", ErrUnavailable
	}
	bh, e := hash(binding)
	if e != nil {
		return "", "", "", ErrUnavailable
	}
	var secret loginSecret
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row loginRecord
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", id).First(&row).Error; e != nil {
			return ErrUnavailable
		}
		ms, e := now(tx)
		if e != nil {
			return e
		}
		if row.Expires <= ms || subtle.ConstantTimeCompare([]byte(row.Binding), []byte(bh)) != 1 {
			return ErrUnavailable
		}
		if e = s.unseal(row.Encrypted, &secret); e != nil {
			return e
		}
		res := tx.Where("id = ? AND binding = ?", id, bh).Delete(&loginRecord{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return ErrUnavailable
		}
		return nil
	})
	if err != nil {
		return "", "", "", ErrUnavailable
	}
	return secret.Verifier, secret.Nonce, secret.FlowID, nil
}

// CreateSession accepts only an already verified identity. It does not select a
// tenant or grant scope; a trusted authorization provider must be consulted on
// every management request. The cookie must not outlive ID-token expiration.
func (s *Store) CreateSession(ctx context.Context, identity Identity) (cookie, csrf string, expires time.Time, err error) {
	if strings.TrimSpace(identity.Issuer) == "" || strings.TrimSpace(identity.Subject) == "" || identity.ExpiresAt.IsZero() {
		err = ErrUnavailable
		return
	}
	cookie, err = random()
	if err != nil {
		return
	}
	csrf, err = random()
	if err != nil {
		return
	}
	id, _ := hash(cookie)
	ms, e := now(s.db.WithContext(ctx))
	if e != nil {
		err = e
		return
	}
	end := ms + s.sessionTTL.Milliseconds()
	if identity.ExpiresAt.UnixMilli() < end {
		end = identity.ExpiresAt.UnixMilli()
	}
	if end <= ms {
		err = ErrUnavailable
		return
	}
	enc, e := s.seal(sessionSecret{Identity: identity, CSRF: csrf})
	if e != nil {
		err = e
		return
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := capacity(tx, &sessionRecord{}, 4096, ms); e != nil {
			return e
		}
		return tx.Create(&sessionRecord{ID: id, Encrypted: enc, Expires: end}).Error
	})
	expires = time.UnixMilli(end)
	return
}

// Authenticate validates the local session and its expiry only. It does not
// query the identity provider for token revocation or back-channel logout.
// Management authorization must be resolved separately on every request.
func (s *Store) Authenticate(ctx context.Context, cookie string) (identity Identity, csrf string, err error) {
	id, e := hash(cookie)
	if e != nil {
		return identity, "", ErrUnavailable
	}
	var row sessionRecord
	if e = s.db.WithContext(ctx).Where("id = ?", id).First(&row).Error; e != nil {
		return identity, "", ErrUnavailable
	}
	ms, e := now(s.db.WithContext(ctx))
	if e != nil || row.Expires <= ms {
		return identity, "", ErrUnavailable
	}
	var secret sessionSecret
	if s.unseal(row.Encrypted, &secret) != nil {
		return identity, "", ErrUnavailable
	}
	return secret.Identity, secret.CSRF, nil
}
func (s *Store) Revoke(ctx context.Context, cookie string) error {
	id, e := hash(cookie)
	if e != nil {
		return ErrUnavailable
	}
	return s.db.WithContext(ctx).Where("id = ?", id).Delete(&sessionRecord{}).Error
}

// CheckMutation requires an exact, configured HTTPS origin, a session-bound
// CSRF token and a non-safe method. Proxy host headers are never trusted.
func CheckMutation(r *http.Request, origin, expected string) bool {
	if r == nil || !strings.HasPrefix(origin, "https://") || r.Header.Get("Origin") != origin || r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" || expected == "" {
		return false
	}
	got := r.Header.Get("X-CSRF-Token")
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}
func SessionCookie(value string, expiry time.Time) *http.Cookie {
	return &http.Cookie{Name: "__Host-df_session", Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: expiry}
}
func BindingCookie(value string, expiry time.Time) *http.Cookie {
	return &http.Cookie{Name: "__Host-df_login", Value: value, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, Expires: expiry}
}

// PurgeBounded deletes at most limit expired login states and limit expired
// browser sessions under the shared capacity lock, using database time.
func (s *Store) PurgeBounded(ctx context.Context, limit int) error {
	if s == nil || limit < 1 || limit > 1000 {
		return ErrUnavailable
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var gate quotaGate
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", "global").First(&gate).Error; err != nil {
			return err
		}
		ms, err := now(tx)
		if err != nil {
			return err
		}
		for _, table := range []any{&loginRecord{}, &sessionRecord{}} {
			var ids []string
			if err = tx.Model(table).Where("expires <= ?", ms).Limit(limit).Pluck("id", &ids).Error; err != nil {
				return err
			}
			if len(ids) > 0 {
				if err = tx.Where("id IN ? AND expires <= ?", ids, ms).Delete(table).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}
