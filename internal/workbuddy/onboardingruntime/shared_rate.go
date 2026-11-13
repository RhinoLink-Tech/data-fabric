package onboardingruntime

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// One fixed database row bounds state and admission across replicas. Caller
// headers and request identifiers are never used; clients share the quota.
type sharedRateWindow struct {
	ID     int   `gorm:"primaryKey"`
	Window int64 `gorm:"not null"`
	Count  int64 `gorm:"not null"`
}

func (sharedRateWindow) TableName() string { return "df_onboarding_rate_limit" }

type sharedRateLimiter struct {
	db      *gorm.DB
	limit   int64
	seconds int64
}

func newSharedRateLimiter(db *gorm.DB, limit, seconds int64) (*sharedRateLimiter, error) {
	if db == nil || db.Dialector == nil || (db.Dialector.Name() != "mysql" && db.Dialector.Name() != "sqlite") || limit < 1 || seconds < 1 {
		return nil, errors.New("shared onboarding rate limiter unavailable")
	}
	if err := db.AutoMigrate(&sharedRateWindow{}); err != nil {
		return nil, err
	}
	if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&sharedRateWindow{ID: 1}).Error; err != nil {
		return nil, err
	}
	return &sharedRateLimiter{db: db, limit: limit, seconds: seconds}, nil
}
func (l *sharedRateLimiter) allow(ctx context.Context) (bool, error) {
	var allowed bool
	err := l.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row sharedRateWindow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", 1).First(&row).Error; err != nil {
			return err
		}
		var now int64
		expr := "SELECT UNIX_TIMESTAMP()"
		if tx.Dialector.Name() == "sqlite" {
			expr = "SELECT CAST(strftime('%s','now') AS INTEGER)"
		}
		if err := tx.Raw(expr).Scan(&now).Error; err != nil {
			return err
		}
		if now <= 0 {
			return errors.New("clock unavailable")
		}
		window := now / l.seconds
		if row.Window > window {
			return errors.New("clock moved backwards")
		}
		if row.Window != window {
			row.Window = window
			row.Count = 0
		}
		if row.Count >= l.limit {
			return nil
		}
		result := tx.Model(&sharedRateWindow{}).Where("id = ?", 1).Updates(map[string]any{"window": row.Window, "count": row.Count + 1})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("rate state unavailable")
		}
		allowed = true
		return nil
	})
	return allowed, err
}

// All onboarding requests, including static assets, share one bounded budget.
// All other routes pass to the fallback without changing existing behavior.
func (l *sharedRateLimiter) wrap(next http.Handler) http.Handler {
	slots := make(chan struct{}, 16)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/onboarding/") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "service busy", http.StatusTooManyRequests)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		allowed, err := l.allow(ctx)
		if err != nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		if !allowed {
			w.Header().Set("Retry-After", strconv.FormatInt(l.seconds, 10))
			http.Error(w, "service busy", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}
