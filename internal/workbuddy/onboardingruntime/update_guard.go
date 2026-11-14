package onboardingruntime

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"reflect"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/workbuddy/singleton"
	"gorm.io/gorm"
)

// updateGuard is an internal experiment, wired only when EnableUpdates is set.
// It requires one MySQL primary, no unrestricted writers, no native executors,
// bounded in-flight requests, and independent review of all pool call sites.
type updateGuard struct {
	lease  *singleton.Lease
	cancel context.CancelFunc
	pool   *database.ConnectionPool
	done   chan struct{}
}

// The lock permanently occupies a DB connection, so a finite pool must
// leave at least one connection for metadata and authorization operations.
func requireUpdateMetadataCapacity(db *sql.DB) error {
	if db == nil {
		return errors.New("metadata database unavailable")
	}
	max := db.Stats().MaxOpenConnections
	if max > 0 && max < 2 {
		return errors.New("source updates require at least two metadata database connections")
	}
	return nil
}

func startUpdateGuard(ctx context.Context, db *gorm.DB, vault *security.CredentialVault, pool *database.ConnectionPool) (*updateGuard, error) {
	if db == nil || db.Dialector.Name() != "mysql" || vault == nil || pool == nil {
		return nil, errors.New("update guard unavailable")
	}
	sqlDB, e := db.DB()
	if e != nil {
		return nil, e
	}
	if e = requireUpdateMetadataCapacity(sqlDB); e != nil {
		return nil, e
	}
	// A fixed MySQL-server-wide name is deliberately conservative: distinct
	// metadata schemas on the same server cannot concurrently enable updates.
	lease, e := singleton.Acquire(ctx, sqlDB, "data-fabric-workbuddy-update-v1", func() { _ = pool.Stop() })
	if e != nil {
		return nil, e
	}
	repo, e := repository.NewDataSourceRepository(db, vault)
	if e != nil {
		lease.Close()
		return nil, e
	}
	e = pool.SetBoundedSourceFreshness(30*time.Second, func(ctx context.Context, snapshot *model.DataSource) error {
		if snapshot == nil {
			return errors.New("source unavailable")
		}
		if e := lease.Check(ctx); e != nil {
			if errors.Is(e, singleton.ErrBusy) {
				return database.ErrSourceCheckBusy
			}
			if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
				return e
			}
			return errors.New("source unavailable")
		}
		current, e := repo.GetByID(ctx, snapshot.ID)
		if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
			return e
		}
		if e != nil || current.ID != snapshot.ID || current.Type != snapshot.Type || current.Status != snapshot.Status || current.OwnerID != snapshot.OwnerID || current.TenantID != snapshot.TenantID || !reflect.DeepEqual(current.Config, snapshot.Config) {
			return errors.New("source unavailable")
		}
		return nil
	})
	if e != nil {
		lease.Close()
		return nil, e
	}
	run, cancel := context.WithCancel(ctx)
	g := &updateGuard{lease: lease, cancel: cancel, pool: pool, done: make(chan struct{})}
	go func() { defer close(g.done); _ = lease.Watch(run, time.Second, 2*time.Second); _ = pool.Stop() }()
	return g, nil
}
func (g *updateGuard) Close() {
	if g != nil {
		g.cancel()
		<-g.done
		_ = g.lease.Close()
	}
}

// Wrap checks the live lease before each accepted request. In-flight work may
// finish, while the on-loss callback stops the SQL pools; this is not fencing.
func (g *updateGuard) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if g == nil || g.lease == nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		err := g.lease.Check(r.Context())
		if errors.Is(err, singleton.ErrBusy) {
			http.Error(w, "service busy", http.StatusTooManyRequests)
			return
		}
		if err != nil {
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}
