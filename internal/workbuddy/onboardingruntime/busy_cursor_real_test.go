package onboardingruntime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// These are disposable test databases created by test-workbuddy-bounded-pool.sh.
func TestBusyFreshnessKeepsRealPaginationAndCursor(t *testing.T) {
	dsn, addr := os.Getenv("DF_BOUNDED_META_DSN"), os.Getenv("DF_BOUNDED_PG_ENDPOINT")
	if dsn == "" || addr == "" {
		t.Skip("isolated synthetic databases required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	host, ps, e := net.SplitHostPort(addr)
	if e != nil {
		t.Fatal(e)
	}
	port, e := strconv.Atoi(ps)
	if e != nil {
		t.Fatal(e)
	}
	pg, e := sql.Open("postgres", fmt.Sprintf("host=%s port=%d user=synthetic password=synthetic dbname=synthetic sslmode=disable", host, port))
	if e != nil {
		t.Fatal(e)
	}
	defer pg.Close()
	if _, e = pg.ExecContext(ctx, "CREATE TABLE df_busy_cursor_test (id integer primary key)"); e != nil {
		t.Fatal(e)
	}
	defer pg.Exec("DROP TABLE IF EXISTS df_busy_cursor_test")
	if _, e = pg.ExecContext(ctx, "INSERT INTO df_busy_cursor_test SELECT generate_series(1,20)"); e != nil {
		t.Fatal(e)
	}
	db, e := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if e != nil {
		t.Fatal(e)
	}
	sqlDB, e := db.DB()
	if e != nil {
		t.Fatal(e)
	}
	defer sqlDB.Close()
	if e = repository.MigrateDataSourceSchema(db); e != nil {
		t.Fatal(e)
	}
	vault, e := security.NewCredentialVaultWithKeyring("test", map[string][]byte{"test": []byte(strings.Repeat("a", 32))})
	if e != nil {
		t.Fatal(e)
	}
	repo, e := repository.NewDataSourceRepository(db, vault)
	if e != nil {
		t.Fatal(e)
	}
	source := &model.DataSource{Name: "busy-cursor-source", Type: model.DatabaseTypePostgreSQL, OwnerID: "alice", TenantID: "tenant", Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{Host: host, Port: port, Username: "synthetic", Password: "synthetic", Database: "synthetic"}}
	if e = repo.Create(ctx, source); e != nil {
		t.Fatal(e)
	}
	policy, e := database.NewFixedIPEgressPolicy([]string{"127.0.0.0/8"}, []int{port})
	if e != nil {
		t.Fatal(e)
	}
	pool, e := database.NewRestrictedConnectionPool(nil, nil, vault, true, policy)
	if e != nil {
		t.Fatal(e)
	}
	defer pool.Stop()
	guard, e := startUpdateGuard(ctx, db, vault, pool)
	if e != nil {
		t.Fatal(e)
	}
	defer guard.Close()
	query, streams := service.NewGatewayQueryServices(repo, pool, false, nil, nil, nil, time.Minute, nil)
	defer query.Shutdown()
	defer streams.Shutdown()
	closer := streams.(interface{ CloseDataSourceStreams(string) int })
	var invalidations atomic.Int32
	pool.SetDataSourceInvalidationHandler(func(id string) { invalidations.Add(1); closer.CloseDataSourceStreams(id) })
	caller := &security.Caller{UserID: "alice", TenantID: "tenant"}
	req := &model.FetchQueryRequest{DataSourceID: source.ID, SQL: "SELECT id FROM df_busy_cursor_test ORDER BY id", BatchSize: 2, Type: 1}
	page, e := query.FetchQuery(ctx, caller, req)
	if e != nil || page.NextURI == "" {
		t.Fatalf("initial page: %v", e)
	}
	req.Type = 2
	stream, e := streams.FetchWithStreaming(ctx, caller, req)
	if e != nil || stream.NextURI == "" {
		t.Fatalf("initial stream: %v", e)
	}
	// Cancel one caller before lock checking; the live sessions belong to
	// another caller and must not be invalidated.
	canceled, cancelCaller := context.WithCancel(ctx)
	cancelCaller()
	if _, e = pool.GetConnection(canceled, source); !errors.Is(e, context.Canceled) {
		t.Fatal("caller cancellation not preserved", e)
	}
	if invalidations.Load() != 0 {
		t.Fatal("canceled lock check invalidated shared pool")
	}
	// Pause the actual MySQL repository lookup after the lease check, then
	// cancel only this caller. This is the former metadata-read failure path.
	entered, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	if e = db.Callback().Query().Before("gorm:query").Register("test:cancel_source_lookup", func(tx *gorm.DB) {
		if tx.Statement.Table == "data_sources" {
			once.Do(func() { close(entered); <-resume })
		}
	}); e != nil {
		t.Fatal(e)
	}
	lookupCtx, cancelLookup := context.WithCancel(ctx)
	lookupResult := make(chan error, 1)
	go func() {
		for {
			_, err := pool.GetConnection(lookupCtx, source)
			if !errors.Is(err, database.ErrSourceCheckBusy) {
				lookupResult <- err
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("metadata lookup not reached")
	}
	cancelLookup()
	close(resume)
	if e = <-lookupResult; !errors.Is(e, context.Canceled) {
		t.Fatal("metadata cancellation not preserved", e)
	}
	if e = db.Callback().Query().Remove("test:cancel_source_lookup"); e != nil {
		t.Fatal(e)
	}
	if invalidations.Load() != 0 {
		t.Fatal("canceled metadata lookup invalidated shared pool")
	}
	stop := make(chan struct{})
	var watcher sync.WaitGroup
	watcher.Add(1)
	go func() {
		defer watcher.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = guard.lease.Check(ctx)
			}
		}
	}()
	var wg sync.WaitGroup
	var busy, unexpected atomic.Int32
	for i := 0; i < 300; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := pool.GetConnection(ctx, source)
			if errors.Is(err, database.ErrSourceCheckBusy) {
				busy.Add(1)
			} else if err != nil {
				unexpected.Add(1)
			}
		}()
	}
	// Exercise the stored-source health probe through the same contended guard.
	var probes sync.WaitGroup
	for i := 0; i < 15; i++ {
		probes.Add(1)
		go func() {
			defer probes.Done()
			err := database.NewHealthChecker(pool).CheckStoredSourceConnectivity(ctx, source)
			if errors.Is(err, database.ErrSourceCheckBusy) {
				busy.Add(1)
			} else if err != nil {
				unexpected.Add(1)
			}
		}()
	}
	// Competing continuations may return a retryable busy error, but must not
	// destroy their sessions or make the previous continuation token unusable.
	pNext, pErr := query.FetchNextBatch(ctx, caller, page.QueryID, page.Slug, page.Token, 2)
	if pErr == nil {
		page = pNext
	} else if !errors.Is(pErr, database.ErrSourceCheckBusy) {
		t.Fatalf("busy pagination error: %v", pErr)
	}
	sNext, sErr := streams.FetchNextBatchWithStreaming(ctx, caller, stream.QueryID, stream.Slug, stream.Token, 2)
	if sErr == nil {
		stream = sNext
	} else if !errors.Is(sErr, database.ErrSourceCheckBusy) {
		t.Fatalf("busy stream error: %v", sErr)
	}
	probes.Wait()
	wg.Wait()
	close(stop)
	watcher.Wait()
	if busy.Load() == 0 || unexpected.Load() != 0 {
		t.Fatalf("contention: busy=%d unexpected=%d", busy.Load(), unexpected.Load())
	}
	if invalidations.Load() != 0 {
		t.Fatalf("busy checks invalidated %d live streams", invalidations.Load())
	}
	var next *model.FetchQueryResponse
	for i := 0; i < 20; i++ {
		next, e = query.FetchNextBatch(ctx, caller, page.QueryID, page.Slug, page.Token, 2)
		if !errors.Is(e, database.ErrSourceCheckBusy) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if e != nil || len(next.Entries) != 2 {
		t.Fatalf("pagination continuation failed: %v", e)
	}
	for i := 0; i < 20; i++ {
		next, e = streams.FetchNextBatchWithStreaming(ctx, caller, stream.QueryID, stream.Slug, stream.Token, 2)
		if !errors.Is(e, database.ErrSourceCheckBusy) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if e != nil || len(next.Entries) != 2 {
		t.Fatalf("stream continuation failed: %v", e)
	}
	// A genuinely stale configuration remains fail-closed and invalidates.
	changed := *source
	changed.Config.Password = "different"
	for i := 0; i < 20; i++ {
		_, e = pool.GetConnection(ctx, &changed)
		if !errors.Is(e, database.ErrSourceCheckBusy) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if e == nil || errors.Is(e, database.ErrSourceCheckBusy) {
		t.Fatalf("stale source accepted: %v", e)
	}
	if invalidations.Load() == 0 {
		t.Fatal("genuinely stale source did not invalidate")
	}
}
