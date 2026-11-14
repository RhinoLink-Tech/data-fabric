package traditional

import (
	"context"
	"database/sql"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestLiveStructuredDSNWithSpecialPassword(t *testing.T) {
	for _, tc := range []struct {
		name, portEnv string
		build         func(*model.DataSourceConfig) string
		open          func(string) (*sql.DB, error)
	}{
		{"postgres", "DF_DSN_TEST_POSTGRES_PORT", (&PostgreSQLDriver{}).BuildDSN, (&PostgreSQLDriver{}).Open},
		{"mysql", "DF_DSN_TEST_MYSQL_PORT", (&MySQLDriver{}).BuildDSN, (&MySQLDriver{}).Open},
	} {
		t.Run(tc.name, func(t *testing.T) {
			portText := os.Getenv(tc.portEnv)
			if portText == "" {
				t.Skip("isolated DB not configured")
			}
			port, err := strconv.Atoi(portText)
			if err != nil {
				t.Fatal(err)
			}
			c := &model.DataSourceConfig{Host: "127.0.0.1", Port: port, Database: "synthetic", Username: "tester", Password: "p@ss:/q+"}
			db, err := tc.open(tc.build(c))
			if err != nil {
				t.Fatal("opening failed")
			}
			defer db.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			for {
				err = db.PingContext(ctx)
				if err == nil {
					break
				}
				if ctx.Err() != nil {
					t.Fatal("synthetic DB connection failed")
				}
				time.Sleep(200 * time.Millisecond)
			}
			var value int
			if err = db.QueryRowContext(ctx, "SELECT 1").Scan(&value); err != nil || value != 1 {
				t.Fatal("synthetic query failed")
			}
		})
	}
}
