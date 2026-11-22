//go:build !datafabric_core

package database

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/readonly"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	mysql "github.com/go-sql-driver/mysql"
)

func TestGaussDBRegistryUsesSeparateProtocols(t *testing.T) {
	registry := NewDriverRegistry()
	my, err := registry.GetDriver(model.DatabaseTypeGaussDBMySQL)
	if err != nil {
		t.Fatal(err)
	}
	pg, err := registry.GetDriver(model.DatabaseTypeGaussDBPostgres)
	if err != nil {
		t.Fatal(err)
	}
	if my.GetDriverName() != "gaussdb-mysql" || pg.GetDriverName() != "gaussdb-postgres" {
		t.Fatalf("wrong driver names: %s %s", my.GetDriverName(), pg.GetDriverName())
	}
	cfg := &model.DataSourceConfig{Host: "127.0.0.1", Port: 3306, Database: "orders", Username: "app", Password: "p@ss/w:ord"}
	dsn := my.BuildDSN(cfg)
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("MySQL DSN failed parsing: %v", err)
	}
	if parsed.Passwd != cfg.Password || parsed.DBName != cfg.Database {
		t.Fatal("MySQL DSN did not preserve connection settings")
	}
	if readonly.ModeFor(model.DatabaseTypeGaussDBMySQL) != readonly.ModeUnsupported {
		t.Fatal("unverified GaussDB read-only session must not be claimed")
	}
	if readonly.ModeFor(model.DatabaseTypeGaussDBPostgres) != readonly.ModeDSNParam {
		t.Fatal("PostgreSQL session behavior changed")
	}
	if _, applied := readonly.ApplyDSN(model.DatabaseTypeGaussDBMySQL, dsn); applied {
		t.Fatal("PostgreSQL session parameter applied to MySQL DSN")
	}
	cfg.SSL = true
	if err := my.ValidateDSN(my.BuildDSN(cfg)); err == nil || !strings.Contains(err.Error(), "TLS") {
		t.Fatalf("TLS must fail closed: %v", err)
	}
	if _, err := my.(drivers.SQLDatabaseOpener).Open(my.BuildDSN(cfg)); err == nil {
		t.Fatal("TLS sentinel must not be opened")
	}
}

// This exercises the MySQL wire path against an isolated MySQL fixture. It is
// not evidence of compatibility with an actual GaussDB server.
func TestGaussDBMySQLWireAgainstFixture(t *testing.T) {
	raw := os.Getenv("DF_GAUSSDB_MYSQL_FIXTURE_DSN")
	if raw == "" {
		t.Skip("set DF_GAUSSDB_MYSQL_FIXTURE_DSN to an isolated MySQL fixture")
	}
	fixture, err := mysql.ParseDSN(raw)
	if err != nil {
		t.Fatal("invalid fixture DSN")
	}
	host, portText, err := net.SplitHostPort(fixture.Addr)
	if err != nil {
		t.Fatal("invalid fixture address")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal("invalid fixture port")
	}
	cfg := &model.DataSourceConfig{Host: host, Port: port, Database: fixture.DBName, Username: fixture.User, Password: fixture.Passwd}
	pool := NewConnectionPool(nil, nil, nil, true)
	dsn, err := pool.getConnectUrl(model.DatabaseTypeGaussDBMySQL, cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := pool.openConfiguredDatabase(ctx, model.DatabaseTypeGaussDBMySQL, dsn, cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var result int
	if err := db.QueryRowContext(ctx, "SELECT 42").Scan(&result); err != nil || result != 42 {
		t.Fatalf("MySQL-wire query failed: %v", err)
	}
	query, err := pool.ApplyBatchPagination(model.DatabaseTypeGaussDBMySQL, "SELECT id FROM orders WHERE id > ? ORDER BY id", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, query, 0).Scan(&result); err != nil || result != 2 {
		t.Fatalf("MySQL-wire parameter and pagination failed: %v", err)
	}
	// The fixture account has no write grant. Do not infer server-side session
	// enforcement from this test or from protocol compatibility.
	if _, err := db.ExecContext(ctx, "CREATE TABLE forbidden_write (id int)"); err == nil {
		t.Fatal("fixture account unexpectedly has write access")
	}
}
