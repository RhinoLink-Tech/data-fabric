package database

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/readonly"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
)

// OpenRestrictedDatabase constructs structured connectors with a pinned destination.
// TLS authenticates the original hostname independently of the IP dial target.
func (p *FixedIPEgressPolicy) OpenRestrictedDatabase(source *model.DataSource, enforceReadOnly bool) (*sql.DB, error) {
	return p.openRestrictedDatabase(source, enforceReadOnly, false)
}

func (p *FixedIPEgressPolicy) openRestrictedDatabase(source *model.DataSource, enforceReadOnly, probe bool) (*sql.DB, error) {
	if err := p.Check(source); err != nil {
		return nil, err
	}
	c := source.Config
	if source.Type == model.DatabaseTypePostgreSQL {
		if postgresProbeDebugAtInit || os.Getenv("PQGO_DEBUG") == "1" {
			return nil, fmt.Errorf("postgres debug not permitted")
		}
		unavailable := filepath.Join(os.DevNull, "data-fabric-unavailable")
		quote := func(v string) string { return "'" + strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(v) + "'" }
		dsn := "host=" + quote(c.Host) + " port=" + quote(fmt.Sprint(c.Port)) + " dbname=" + quote(c.Database) + " user=" + quote(c.Username) + " password=" + quote(c.Password) + " service='' sslmode='disable' sslnegotiation='postgres' passfile=" + quote(unavailable) + " sslcert=" + quote(unavailable) + " sslkey=" + quote(unavailable) + " sslrootcert=" + quote(unavailable)
		cfg, err := pq.NewConfig(dsn)
		if err != nil {
			return nil, err
		}
		cfg.Host, cfg.Hostaddr = c.Host, netip.Addr{}
		cfg.Port, cfg.Multi = uint16(c.Port), nil
		cfg.Database, cfg.User, cfg.Password = c.Database, c.Username, c.Password
		cfg.Service, cfg.ServiceFile, cfg.Passfile = "", unavailable, unavailable
		cfg.SSLMode, cfg.SSLRootCert, cfg.SSLCert, cfg.SSLKey = pq.SSLModeDisable, unavailable, unavailable, unavailable
		cfg.Options, cfg.Runtime = "", nil
		cfg.ConnectTimeout = 5 * time.Second
		cfg.SSLNegotiation = pq.SSLNegotiationPostgres
		// NewConfig must preserve its private "password supplied" bit. Clear
		// every remaining environment-derived parameter after parsing.
		cfg.ApplicationName, cfg.FallbackApplicationName = "", ""
		cfg.SSLMinProtocolVersion, cfg.SSLMaxProtocolVersion = "", ""
		cfg.KrbSrvname, cfg.KrbSpn = "", ""
		cfg.ClientEncoding, cfg.Datestyle, cfg.TZ, cfg.Geqo = "", "", "", ""
		cfg.TargetSessionAttrs, cfg.LoadBalanceHosts = "", ""
		cfg.MinProtocolVersion, cfg.MaxProtocolVersion = "3.0", "3.0"

		if enforceReadOnly {
			cfg.Runtime = map[string]string{readonly.PostgresReadOnlyDSNParam: readonly.PostgresReadOnlyDSNValue}
		}
		if c.SSL {
			cfg.Host, cfg.Hostaddr = c.TLSServerName, netip.MustParseAddr(c.Host)
			cfg.SSLMode, cfg.SSLRootCert, cfg.SSLSNI = pq.SSLModeVerifyFull, "system", true
			if c.TLSCA == "custom" {
				// lib/pq clones this immutable, process-registered tls.Config.
				// No path from a user or later file read can alter the trust roots.
				cfg.SSLMode = pq.SSLMode("pqgo-" + p.caTag)
				cfg.SSLRootCert = ""
			}
		}
		connector, err := pq.NewConnectorConfig(cfg)
		if err != nil {
			return nil, err
		}
		if probe {
			connector.Dialer(probeDialer{deadline: time.Now().Add(5 * time.Second)})
		}
		return sql.OpenDB(connector), nil
	}
	cfg := mysql.NewConfig()
	cfg.User, cfg.Passwd, cfg.Net = c.Username, c.Password, "tcp"
	cfg.Addr, cfg.DBName = net.JoinHostPort(c.Host, fmt.Sprint(c.Port)), c.Database
	cfg.Timeout, cfg.ReadTimeout, cfg.WriteTimeout = 5*time.Second, 5*time.Second, 5*time.Second
	if c.SSL {
		cfg.TLS = &tls.Config{ServerName: c.TLSServerName, MinVersion: tls.VersionTLS12}
		if c.TLSCA == "custom" {
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM(p.caPEM) {
				return nil, ErrEgressDenied
			}
			cfg.TLS.RootCAs = roots
		}
	}
	connector, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, err
	}
	if enforceReadOnly {
		return sql.OpenDB(readonly.WrapMySQLConnector(connector)), nil
	}
	return sql.OpenDB(connector), nil
}

func (p *FixedIPEgressPolicy) CheckRestrictedSourceConnectivity(ctx context.Context, source *model.DataSource) error {
	db, err := p.openRestrictedDatabase(source, false, true)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}
