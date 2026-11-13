package database

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
)

// lib/pq captures PQGO_DEBUG during package initialization. Keep an
// independent startup snapshot so unsetting the variable later cannot enable
// a credential-bearing probe in a process with protocol logging enabled.
var postgresProbeDebugAtInit = os.Getenv("PQGO_DEBUG") == "1" || unsafeInitialPostgresDebug()

// Linux exposes the exec-time environment even after os.Unsetenv. If it is
// unavailable or unusually large, fail closed rather than guess pq's mode.
func unsafeInitialPostgresDebug() bool {
	return unsafePostgresDebugFromEnvironment(runtime.GOOS, func(path string) (io.ReadCloser, error) { return os.Open(path) })
}
func unsafePostgresDebugFromEnvironment(goos string, open func(string) (io.ReadCloser, error)) bool {
	if goos != "linux" {
		return true
	}
	f, err := open("/proc/self/environ")
	if err != nil {
		return true
	}
	defer f.Close()
	const limit = 1 << 20
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(data) > limit {
		return true
	}
	for _, entry := range bytes.Split(data, []byte{0}) {
		if bytes.Equal(entry, []byte("PQGO_DEBUG=1")) {
			return true
		}
	}
	return false
}

var probeHostname = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

// CheckStoredSourceConnectivity probes only plain TCP MySQL and PostgreSQL.
// It builds driver configuration from a fixed allowlist, never from untrusted
// URL/DSN fragments, and never downgrades a source configured for TLS.
func (hc *HealthChecker) CheckStoredSourceConnectivity(ctx context.Context, source *model.DataSource) error {
	if hc.connPool != nil && hc.connPool.egress != nil {
		return hc.connPool.egress.CheckRestrictedSourceConnectivity(ctx, source)
	}
	if source == nil || (source.Type != model.DatabaseTypePostgreSQL && source.Type != model.DatabaseTypeMySQL) {
		return fmt.Errorf("unsupported source type")
	}
	if source.Type == model.DatabaseTypePostgreSQL && (postgresProbeDebugAtInit || os.Getenv("PQGO_DEBUG") == "1") {
		return fmt.Errorf("postgres probe is disabled when protocol debug is enabled")
	}
	c := source.Config
	if c.SSL || c.SSLRootCert != "" || (c.SSLMode != "" && c.SSLMode != "disable") {
		return fmt.Errorf("TLS probes are unavailable")
	}
	if c.Host == "" || (net.ParseIP(c.Host) == nil && !probeHostname.MatchString(c.Host)) || c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("invalid host or port")
	}
	var db *sql.DB
	if source.Type == model.DatabaseTypePostgreSQL {
		// NewConfig normally inherits PG* environment settings, including service
		// files and certificate paths. Override all file-bearing options and disable
		// service lookup before it parses the configuration. Password is explicit,
		// even when empty, so lib/pq never falls back to ~/.pgpass.
		unavailable := filepath.Join(os.DevNull, "data-fabric-probe-unavailable")
		// Keyword DSNs preserve explicitly empty service/password fields,
		// whereas pq's URL parser drops empty values and revives PGSERVICE.
		quote := func(v string) string { return "'" + strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(v) + "'" }
		dsn := "host=" + quote(c.Host) + " port=" + quote(fmt.Sprint(c.Port)) +
			" dbname=" + quote(c.Database) + " user=" + quote(c.Username) + " password=" + quote(c.Password) +
			" service='' sslmode='disable' passfile=" + quote(unavailable) +
			" sslcert=" + quote(unavailable) + " sslkey=" + quote(unavailable) + " sslrootcert=" + quote(unavailable) + " connect_timeout='3'"
		cfg, err := pq.NewConfig(dsn)
		if err != nil {
			return err
		}
		// Drop ambient settings that could alter target, authentication, or startup.
		cfg.Host = c.Host
		cfg.Hostaddr = netip.Addr{}
		cfg.Port = uint16(c.Port)
		cfg.Multi = nil
		cfg.Database = c.Database
		cfg.User = c.Username
		cfg.Password = c.Password
		cfg.Service = ""
		cfg.ServiceFile = unavailable
		cfg.Passfile = unavailable
		cfg.SSLMode = pq.SSLModeDisable
		cfg.SSLRootCert = unavailable
		cfg.SSLCert = unavailable
		cfg.SSLKey = unavailable
		cfg.Options = ""
		cfg.Runtime = nil
		cfg.ConnectTimeout = 3 * time.Second
		connector, err := pq.NewConnectorConfig(cfg)
		if err != nil {
			return err
		}
		connector.Dialer(probeDialer{deadline: time.Now().Add(3 * time.Second)})
		db = sql.OpenDB(connector)
	} else {
		cfg := mysql.NewConfig()
		cfg.User = c.Username
		cfg.Passwd = c.Password
		cfg.Net = "tcp"
		cfg.Addr = net.JoinHostPort(c.Host, fmt.Sprint(c.Port))
		cfg.DBName = c.Database
		cfg.Timeout = 3 * time.Second
		cfg.ReadTimeout = 3 * time.Second
		cfg.WriteTimeout = 3 * time.Second
		connector, err := mysql.NewConnector(cfg)
		if err != nil {
			return err
		}
		db = sql.OpenDB(connector)
	}
	defer db.Close()
	return db.PingContext(ctx)
}

type probeDialer struct{ deadline time.Time }

func (d probeDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}
func (d probeDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return d.DialContext(ctx, network, address)
}
func (d probeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	ctx, cancel := context.WithDeadline(ctx, d.deadline)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	wrapped := &probeConnection{Conn: conn, deadline: d.deadline}
	if err = wrapped.SetDeadline(d.deadline); err != nil {
		conn.Close()
		return nil, err
	}
	return wrapped, nil
}

type probeConnection struct {
	net.Conn
	deadline time.Time
}

func (c *probeConnection) SetDeadline(deadline time.Time) error {
	if deadline.IsZero() || deadline.After(c.deadline) {
		deadline = c.deadline
	}
	return c.Conn.SetDeadline(deadline)
}
