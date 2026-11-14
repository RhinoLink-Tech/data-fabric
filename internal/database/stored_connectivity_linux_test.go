package database

import (
	"context"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"net"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestProbeIgnoresInjectedFilePathsAndAmbientPGSettings(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "never-read")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	// Any accidental service, pgpass or certificate read would block indefinitely.
	for _, key := range []string{"PGSERVICEFILE", "PGSSLCERT", "PGSSLKEY", "PGSSLROOTCERT", "PGPASSFILE"} {
		t.Setenv(key, fifo)
	}
	t.Setenv("PGSERVICE", "unexpected-service")
	for _, trial := range []struct {
		name          string
		kind          model.DatabaseType
		emptyPassword bool
	}{
		{"postgresql", model.DatabaseTypePostgreSQL, false},
		{"postgresql-empty-password", model.DatabaseTypePostgreSQL, true},
		{"mysql", model.DatabaseTypeMySQL, false},
	} {
		kind := trial.kind
		t.Run(trial.name, func(t *testing.T) {
			if kind == model.DatabaseTypePostgreSQL && postgresProbeDebugAtInit {
				t.Skip("PostgreSQL probe is fail-closed on this platform")
			}
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			accepted := make(chan bool, 1)
			go func() {
				c, e := l.Accept()
				if e != nil {
					accepted <- false
					return
				}
				c.Close()
				accepted <- true
			}()
			addr := l.Addr().(*net.TCPAddr)
			config := model.DataSourceConfig{Host: "127.0.0.1", Port: addr.Port, Database: "sample?sslrootcert=" + fifo, Username: "reader\u0027 service=evil sslcert=" + fifo, Password: "safe\\x service=bad" + fifo + " sslrootcert=x", Timezone: "UTC&sslrootcert=" + fifo + "&sslcert=" + fifo + "&sslkey=" + fifo + "&datafabricsslrootcert=" + fifo}
			if trial.emptyPassword {
				config.Password = ""
			}
			source := &model.DataSource{Type: kind, Config: config}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			start := time.Now()
			if err := NewHealthChecker(nil).CheckStoredSourceConnectivity(ctx, source); err == nil {
				t.Fatal("closed server reported healthy")
			}
			if time.Since(start) > time.Second {
				t.Fatal("probe blocked on an injected file")
			}
			select {
			case ok := <-accepted:
				if !ok {
					t.Fatal("listener was not reached")
				}
			case <-time.After(time.Second):
				t.Fatal("probe did not attempt allowed host")
			}
		})
	}
}
func TestProbeRejectsTLSInsteadOfDowngrading(t *testing.T) {
	for _, kind := range []model.DatabaseType{model.DatabaseTypePostgreSQL, model.DatabaseTypeMySQL} {
		source := &model.DataSource{Type: kind, Config: model.DataSourceConfig{Host: "127.0.0.1", Port: 5432, SSL: true}}
		if err := NewHealthChecker(nil).CheckStoredSourceConnectivity(context.Background(), source); err == nil {
			t.Fatal("TLS probe accepted")
		}
		source.Config.SSL = false
		source.Config.SSLMode = "require"
		if err := NewHealthChecker(nil).CheckStoredSourceConnectivity(context.Background(), source); err == nil {
			t.Fatal("TLS mode probe accepted")
		}
	}
}
