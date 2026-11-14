package database

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestRestrictedTLSRealDatabases(t *testing.T) {
	ca, badCA := os.Getenv("DF_TLS_CA"), os.Getenv("DF_TLS_BAD_CA")
	if ca == "" || badCA == "" {
		t.Skip("isolated TLS integration is not configured")
	}
	// Deliberately hostile lib/pq fallback and environment; only the explicitly
	// configured trust root, host, port and protocol may affect the connector.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PGHOST", "evil.invalid")
	t.Setenv("PGHOSTADDR", "169.254.169.254")
	t.Setenv("PGPORT", "1")
	t.Setenv("PGSERVICE", "evil")
	t.Setenv("PGSERVICEFILE", "/dev/null")
	t.Setenv("PGSSLMODE", "disable")
	t.Setenv("PGSSLROOTCERT", "/dev/null")
	t.Setenv("PGSSLCERT", "/dev/null")
	t.Setenv("PGSSLKEY", "/dev/null")
	t.Setenv("PGOPTIONS", "-c default_transaction_read_only=off")
	t.Setenv("PGTARGETSESSIONATTRS", "standby")
	t.Setenv("PGSSLNEGOTIATION", "direct")
	if err := os.Mkdir(filepath.Join(home, ".postgresql"), 0700); err != nil {
		t.Fatal(err)
	}
	copyRoot := func(path string) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(home, ".postgresql", "root.crt"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, env, user string
		typ             model.DatabaseType
	}{
		{"postgres", "DF_TLS_POSTGRES", "postgres", model.DatabaseTypePostgreSQL},
		{"mysql", "DF_TLS_MYSQL", "root", model.DatabaseTypeMySQL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			host, ps, err := net.SplitHostPort(os.Getenv(tc.env))
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(ps)
			if err != nil {
				t.Fatal(err)
			}
			p, err := NewFixedIPEgressPolicyWithCA([]string{"127.0.0.1/32"}, []int{port}, ca)
			if err != nil {
				t.Fatal(err)
			}
			pool, err := NewRestrictedConnectionPool(nil, nil, nil, true, p)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.CloseAll()
			d := &model.DataSource{ID: "tls-synthetic", Type: tc.typ, Config: model.DataSourceConfig{Host: host, Port: port, Database: "synthetic", Username: tc.user, Password: "SyntheT1c@:/?&=测试", SSL: true, TLSServerName: "db.example.test", TLSCA: "custom"}}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			copyRoot(badCA)
			if err = p.CheckRestrictedSourceConnectivity(ctx, d); err != nil {
				t.Fatalf("probe: %v", err)
			}
			db, err := pool.GetConnection(ctx, d)
			if err != nil {
				t.Fatalf("pool: %v", err)
			}
			var n int
			if err = db.QueryRowContext(ctx, "SELECT 1").Scan(&n); err != nil || n != 1 {
				t.Fatalf("query %d %v", n, err)
			}
			if _, err = db.ExecContext(ctx, "CREATE TABLE should_fail (id INT)"); err == nil {
				t.Fatal("read-only session allowed write")
			}
			wrong := *d
			wrong.Config.TLSServerName = "wrong.example.test"
			if err = p.CheckRestrictedSourceConnectivity(ctx, &wrong); err == nil {
				t.Fatal("incorrect server hostname accepted")
			}
			bad, err := NewFixedIPEgressPolicyWithCA([]string{"127.0.0.1/32"}, []int{port}, badCA)
			if err != nil {
				t.Fatal(err)
			}
			copyRoot(ca)
			if err = bad.CheckRestrictedSourceConnectivity(ctx, d); err == nil {
				t.Fatal("incorrect CA accepted")
			}
			wrong = *d
			wrong.Config.Port = port + 1
			if err = p.CheckRestrictedSourceConnectivity(ctx, &wrong); !errors.Is(err, ErrEgressDenied) {
				t.Fatalf("unlisted port: %v", err)
			}
			wrong = *d
			wrong.Config.Host = "169.254.169.254"
			if err = p.CheckRestrictedSourceConnectivity(ctx, &wrong); !errors.Is(err, ErrEgressDenied) {
				t.Fatalf("disallowed IP: %v", err)
			}
			wrong = *d
			wrong.Config.SSLRootCert = "/etc/passwd"
			if err = p.CheckRestrictedSourceConnectivity(ctx, &wrong); !errors.Is(err, ErrEgressDenied) {
				t.Fatalf("file path: %v", err)
			}
			fmt.Printf("%s TLS hostname, CA, credentials, read-only and egress checks passed\n", tc.name)
		})
	}
}
