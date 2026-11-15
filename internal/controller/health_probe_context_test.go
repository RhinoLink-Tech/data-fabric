package controller

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// probeConnector hands out connections whose Ping blocks until the context
// it receives is done, reporting that context's error on observed. When
// immediate is set, Ping succeeds without blocking.
type probeConnector struct {
	entered   chan struct{}
	observed  chan error
	immediate bool
}

func (c probeConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return probeConn(c), nil
}

func (c probeConnector) Driver() sqldriver.Driver { return probeDriver{} }

type probeDriver struct{}

func (probeDriver) Open(string) (sqldriver.Conn, error) { return nil, errors.New("unused") }

type probeConn probeConnector

func (c probeConn) Prepare(string) (sqldriver.Stmt, error) { return nil, errors.New("unused") }
func (c probeConn) Close() error                           { return nil }
func (c probeConn) Begin() (sqldriver.Tx, error)           { return nil, errors.New("unused") }

func (c probeConn) Ping(ctx context.Context) error {
	if c.immediate {
		return nil
	}
	select {
	case c.entered <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		c.observed <- ctx.Err()
		return ctx.Err()
	case <-time.After(10 * time.Second):
		c.observed <- nil
		return nil
	}
}

func TestHealthProbeHonorsRequestCancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	connector := probeConnector{entered: make(chan struct{}, 1), observed: make(chan error, 1)}
	// Open a normal gorm DB (its dialector queries the version at startup),
	// then route the health probe through the synthetic driver.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	probeDB := sql.OpenDB(connector)
	defer probeDB.Close()
	db.ConnPool = probeDB
	db.Statement.ConnPool = probeDB

	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest("GET", "/health", nil).WithContext(reqCtx)

	done := make(chan struct{})
	go func() {
		defer close(done)
		NewHealthController(db).HealthCheck(ginCtx)
	}()

	select {
	case <-connector.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("probe never started")
	}
	cancel()

	select {
	case got := <-connector.observed:
		if !errors.Is(got, context.Canceled) {
			t.Fatalf("probe ctx err = %v, want context canceled", got)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("probe did not observe cancellation")
	}
	<-done
	if recorder.Code != 503 {
		t.Fatalf("HTTP status = %d, want 503", recorder.Code)
	}
}

// newProbeHealthDB returns a gorm DB whose health probe goes through connector.
func newProbeHealthDB(t *testing.T, connector probeConnector) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	probeDB := sql.OpenDB(connector)
	t.Cleanup(func() { probeDB.Close() })
	db.ConnPool = probeDB
	db.Statement.ConnPool = probeDB
	return db
}

func TestHealthProbeHealthyReturns200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := newProbeHealthDB(t, probeConnector{immediate: true})
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	ginCtx.Request = httptest.NewRequest("GET", "/health", nil)

	NewHealthController(db).HealthCheck(ginCtx)

	if recorder.Code != 200 {
		t.Fatalf("HTTP status = %d, want 200; body: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"status":"healthy"`) {
		t.Fatalf("body = %s, want healthy status", recorder.Body.String())
	}
}

func TestHealthProbeDeadlineReturnsUnhealthy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	connector := probeConnector{entered: make(chan struct{}, 1), observed: make(chan error, 1)}
	db := newProbeHealthDB(t, connector)
	recorder := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(recorder)
	// Background request context: only the probe's own deadline can end the ping.
	ginCtx.Request = httptest.NewRequest("GET", "/health", nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		NewHealthController(db).HealthCheck(ginCtx)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("health check did not return after probe deadline")
	}

	select {
	case got := <-connector.observed:
		if !errors.Is(got, context.DeadlineExceeded) {
			t.Fatalf("probe ctx err = %v, want deadline exceeded", got)
		}
	default:
		t.Fatal("probe did not observe its deadline")
	}
	if recorder.Code != 503 {
		t.Fatalf("HTTP status = %d, want 503; body: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"status":"unhealthy"`) {
		t.Fatalf("body = %s, want unhealthy status", recorder.Body.String())
	}
}
