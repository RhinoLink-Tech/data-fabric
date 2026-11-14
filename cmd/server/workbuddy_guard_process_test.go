package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	"github.com/RhinoLink-Tech/data-fabric/internal/workbuddy/singleton"
	_ "github.com/go-sql-driver/mysql"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const guardTestKey = "df-guard-process-synthetic"

func TestWorkBuddyGuardProcessHelper(t *testing.T) {
	if os.Getenv("DF_GUARD_CHILD") != "1" {
		return
	}
	db, e := sql.Open("mysql", os.Getenv("DF_GUARD_TEST_MYSQL_DSN"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	lease, e := singleton.Acquire(context.Background(), db, guardTestKey, func() {})
	if e != nil {
		t.Fatal(e)
	}
	defer lease.Close()
	guard := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if lease.Check(r.Context()) != nil {
				http.Error(w, "unavailable", 503)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	onboarding := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mcp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	cfg := &config.Config{}
	cfg.MCP.Address = os.Getenv("DF_GUARD_CHILD_ADDR")
	streaming := service.NewStreamingService(nil, nil)
	if e = serveMCPHTTP(cfg, mcp, streaming, nil, nil, nil, true, func(next http.Handler) http.Handler { return workbuddyHTTPOverlay(next, onboarding, guard) }); e != nil {
		t.Fatal(e)
	}
}
func TestWorkBuddyMainMuxGuardProcessLoss(t *testing.T) {
	dsn := os.Getenv("DF_GUARD_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("disposable mysql not supplied")
	}
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := l.Addr().String()
	l.Close()
	bridge, e := net.Listen("tcp", "127.0.0.1:8082")
	if e != nil {
		t.Skip("bridge port unavailable")
	}
	bridge.Close()
	child := exec.Command(os.Args[0], "-test.run=^TestWorkBuddyGuardProcessHelper$")
	child.Env = append(os.Environ(), "DF_GUARD_CHILD=1", "DF_GUARD_CHILD_ADDR="+address)
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { _ = child.Process.Kill(); _ = child.Wait() }()
	httpClient := &http.Client{Timeout: time.Second}
	get := func(url string) int {
		r, e := httpClient.Get(url)
		if e != nil {
			return 0
		}
		defer r.Body.Close()
		return r.StatusCode
	}
	urls := []string{"http://" + address + "/mcp", "http://" + address + "/onboarding/session", "http://127.0.0.1:8082/mcp", "http://127.0.0.1:8082/onboarding/session"}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if get(urls[0]) == 204 && get(urls[2]) == 204 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, u := range urls {
		if got := get(u); got != 204 {
			t.Fatalf("listener not ready %s %d", u, got)
		}
	}
	db, e := sql.Open("mysql", dsn)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var holder int64
	if e = db.QueryRow("SELECT IS_USED_LOCK(?)", guardTestKey).Scan(&holder); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(fmt.Sprintf("KILL CONNECTION %d", holder)); e != nil {
		t.Fatal(e)
	}
	for _, u := range urls {
		if got := get(u); got != 503 {
			t.Fatalf("lost lock allowed %s %d", u, got)
		}
	}
	if strings.Contains(address, "0.0.0.0") {
		t.Fatal("not loopback")
	}
}
