package onboardingruntime

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This test kills its own lease session. Only a disposable MySQL server is safe.
func TestMySQLUpdateGuardLossStopsRequestsAndPools(t *testing.T) {
	dsn := os.Getenv("DF_SINGLETON_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("disposable MySQL not supplied")
	}
	db, e := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	vault, e := security.NewCredentialVaultWithKeyring("test", map[string][]byte{"test": []byte(strings.Repeat("a", 32))})
	if e != nil {
		t.Fatal(e)
	}
	policy, e := database.NewFixedIPEgressPolicy([]string{"127.0.0.1/32"}, []int{5432})
	if e != nil {
		t.Fatal(e)
	}
	pool, e := database.NewRestrictedConnectionPool(nil, nil, vault, false, policy)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sqlDB, e := db.DB()
	if e != nil {
		t.Fatal(e)
	}
	sqlDB.SetMaxOpenConns(1)
	if _, e = startUpdateGuard(ctx, db, vault, pool); e == nil {
		t.Fatal("single metadata connection accepted")
	}
	sqlDB.SetMaxOpenConns(2)
	guard, e := startUpdateGuard(ctx, db, vault, pool)
	if e != nil {
		t.Fatal(e)
	}
	defer guard.Close()
	next := guard.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	invoke := func() int {
		w := httptest.NewRecorder()
		next.ServeHTTP(w, httptest.NewRequest("GET", "http://example.test/", nil))
		return w.Code
	}
	eventual := func() int {
		deadline := time.Now().Add(2 * time.Second)
		for {
			code := invoke()
			if code != 429 || time.Now().After(deadline) {
				return code
			}
			time.Sleep(time.Millisecond)
		}
	}
	if eventual() != 204 {
		t.Fatal("live lease denied")
	}
	// All requests are unauthenticated at this boundary. Flood them together:
	// overload may return 429 but must never be mistaken for permanent lock loss.
	var wg sync.WaitGroup
	var denied, ok, other atomic.Int32
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch invoke() {
			case 204:
				ok.Add(1)
			case 429:
				denied.Add(1)
			default:
				other.Add(1)
			}
		}()
	}
	wg.Wait()
	if other.Load() != 0 || ok.Load() == 0 {
		t.Fatalf("flood returned unexpected status: ok=%d busy=%d other=%d", ok.Load(), denied.Load(), other.Load())
	}
	if eventual() != 204 {
		t.Fatal("flood permanently disabled guard")
	}
	pool2, e := database.NewRestrictedConnectionPool(nil, nil, vault, false, policy)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = startUpdateGuard(ctx, db, vault, pool2); e == nil {
		t.Fatal("second guard acquired lease")
	}
	var holder int64
	if e = db.Raw("SELECT IS_USED_LOCK(?)", "data-fabric-workbuddy-update-v1").Scan(&holder).Error; e != nil {
		t.Fatal(e)
	}
	if e = db.Exec(fmt.Sprintf("KILL CONNECTION %d", holder)).Error; e != nil {
		t.Fatal(e)
	}
	if eventual() != 503 {
		t.Fatal("lost lock request accepted")
	}
	if _, e = pool.GetConnection(ctx, &model.DataSource{ID: "old", Type: model.DatabaseTypeMySQL}); e == nil {
		t.Fatal("pool reopened after loss")
	}
	guard2, e := startUpdateGuard(ctx, db, vault, pool2)
	if e != nil {
		t.Fatal(e)
	}
	guard2.Close()
}

func TestUpdateMetadataConnectionCapacity(t *testing.T) {
	db, e := sql.Open("mysql", "synthetic:synthetic@tcp(127.0.0.1:1)/synthetic")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	for _, tc := range []struct {
		max    int
		reject bool
	}{{0, false}, {1, true}, {2, false}, {10, false}} {
		db.SetMaxOpenConns(tc.max)
		if err := requireUpdateMetadataCapacity(db); (err != nil) != tc.reject {
			t.Fatalf("capacity %d, error %v", tc.max, err)
		}
	}
	if requireUpdateMetadataCapacity(nil) == nil {
		t.Fatal("nil database accepted")
	}
}
