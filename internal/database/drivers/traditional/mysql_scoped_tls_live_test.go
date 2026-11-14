package traditional

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/readonly"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestScopedMySQLTLSRealDatabase(t *testing.T) {
	ca := os.Getenv("DF_SCOPED_TLS_CA")
	good, bad := os.Getenv("DF_SCOPED_TLS_GOOD_PORT"), os.Getenv("DF_SCOPED_TLS_BAD_PORT")
	if ca == "" || good == "" || bad == "" {
		t.Skip("isolated IPv6 TLS MySQL unavailable")
	}
	for _, tc := range []struct {
		name, port string
		expect     bool
	}{{"matching IP SAN", good, true}, {"wrong IP SAN", bad, false}} {
		t.Run(tc.name, func(t *testing.T) {
			port, err := strconv.Atoi(tc.port)
			if err != nil {
				t.Fatal(err)
			}
			d := &MySQLDriver{}
			dsn := d.BuildDSN(&model.DataSourceConfig{Host: "::1%lo", Port: port, Database: "synthetic", Username: "root", Password: "synthetic-pass", SSL: true, SSLRootCert: ca})
			for _, ro := range []bool{false, true} {
				var err error
				db, e := d.Open(dsn)
				if ro {
					if db != nil {
						db.Close()
					}
					db, _, e = readonly.OpenDB(model.DatabaseTypeMySQL, dsn)
				}
				if e != nil {
					t.Fatal("connector construction failed", e)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err = db.PingContext(ctx)
				cancel()
				if (err == nil) != tc.expect {
					t.Fatalf("readonly=%v unexpected verified TLS outcome: %v", ro, err)
				}
				if err == nil && ro {
					var value int
					if err = db.QueryRow("SELECT 1").Scan(&value); err != nil || value != 1 {
						t.Fatal("read-only query failed")
					}
				}
				db.Close()
			}
		})
	}
}
