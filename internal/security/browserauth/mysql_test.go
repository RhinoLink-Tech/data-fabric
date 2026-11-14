package browserauth

import (
	"context"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"os"
	"sync"
	"testing"
	"time"
)

func TestMySQLConcurrentConsumption(t *testing.T) {
	dsn := os.Getenv("DF_TEST_BROWSER_MYSQL_DSN")
	if dsn == "" {
		t.Skip("isolated MySQL DSN not set")
	}
	db, e := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	if e = Migrate(db); e != nil {
		t.Fatal(e)
	}
	v, e := security.NewCredentialVault(make([]byte, 32))
	if e != nil {
		t.Fatal(e)
	}
	s, e := New(db, v, time.Minute, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	db2, e := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if e != nil {
		t.Fatal(e)
	}
	s2, e := New(db2, v, time.Minute, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	cookie, csrf, _, e := s.CreateSession(context.Background(), Identity{Issuer: "https://issuer.test", Subject: "alice", ExpiresAt: time.Now().Add(time.Minute)})
	if e != nil {
		t.Fatal(e)
	}
	id2, csrf2, e := s2.Authenticate(context.Background(), cookie)
	if e != nil || id2.Subject != "alice" || csrf2 != csrf {
		t.Fatal("shared session failed")
	}
	flow, _ := random()
	var starts int
	var startMu sync.Mutex
	var startWg sync.WaitGroup
	for i := 0; i < 12; i++ {
		startWg.Add(1)
		go func(n int) {
			defer startWg.Done()
			st := s
			if n%2 == 0 {
				st = s2
			}
			if _, _, _, _, err := st.BeginFlow(context.Background(), flow); err == nil {
				startMu.Lock()
				starts++
				startMu.Unlock()
			}
		}(i)
	}
	startWg.Wait()
	if starts != 3 {
		t.Fatalf("per-flow limit accepted %d", starts)
	}
	other, _ := random()
	if _, _, _, _, e := s.BeginFlow(context.Background(), other); e != nil {
		t.Fatal("other flow starved", e)
	}
	state, binding, _, _, e := s.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	success := 0
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			store := s
			if index%2 == 0 {
				store = s2
			}
			_, _, err := store.Consume(context.Background(), state, binding)
			if err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("state consumed %d times", success)
	}
}
