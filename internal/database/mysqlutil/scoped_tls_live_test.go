package mysqlutil

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// The explicit certificate identity may intentionally differ from the dial IP.
// A connector must preserve it, even when the dial target has an IPv6 zone.
func TestScopedTLSExplicitIdentityRealDatabase(t *testing.T) {
	ca, good, bad := os.Getenv("DF_SCOPED_TLS_CA"), os.Getenv("DF_SCOPED_TLS_GOOD_PORT"), os.Getenv("DF_SCOPED_TLS_BAD_PORT")
	if ca == "" || good == "" || bad == "" {
		t.Skip("isolated IPv6 TLS MySQL unavailable")
	}
	pem, err := os.ReadFile(ca)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		t.Fatal("invalid synthetic CA")
	}
	const name = "df-scoped-explicit-test"
	if err = mysql.RegisterTLSConfig(name, &tls.Config{RootCAs: roots, ServerName: "::2", MinVersion: tls.VersionTLS12}); err != nil {
		t.Fatal(err)
	}
	defer mysql.DeregisterTLSConfig(name)
	for _, tc := range []struct {
		name, port string
		expect     bool
	}{{"different SAN is rejected", good, false}, {"explicit SAN is accepted", bad, true}} {
		t.Run(tc.name, func(t *testing.T) {
			dsn := "root:synthetic-pass@tcp([::1%lo]:" + tc.port + ")/synthetic?tls=" + name
			cfg, err := mysql.ParseDSN(dsn)
			if err != nil {
				t.Fatal(err)
			}
			connector, err := Connector(cfg)
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(connector)
			defer db.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = db.PingContext(ctx)
			if (err == nil) != tc.expect {
				t.Fatalf("wrong explicit-name verification result: %v", err)
			}
		})
	}
}
