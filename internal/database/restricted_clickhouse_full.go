//go:build !datafabric_core

package database

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"net"
	"net/netip"
	"strconv"
	"time"

	clickhouse "github.com/ClickHouse/clickhouse-go/v2"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// restrictedClickHouseOptions is called only after the fixed-IP policy has
// admitted the source. A single numeric address is used for the native
// connection while TLS validates the independently pinned server name.
func restrictedClickHouseOptions(c model.DataSourceConfig, caPEM []byte) (*clickhouse.Options, error) {
	ip, err := netip.ParseAddr(c.Host)
	if err != nil || !ip.Is4() || !c.SSL || c.Port < 1 || c.Port > 65535 || c.TLSServerName == "" {
		return nil, ErrEgressDenied
	}
	tlsConfig := &tls.Config{ServerName: c.TLSServerName, MinVersion: tls.VersionTLS12}
	if c.TLSCA == "custom" {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(caPEM) {
			return nil, ErrEgressDenied
		}
		tlsConfig.RootCAs = roots
	} else if c.TLSCA != "" && c.TLSCA != "system" {
		return nil, ErrEgressDenied
	}
	return &clickhouse.Options{
		Protocol:    clickhouse.Native,
		Addr:        []string{net.JoinHostPort(ip.String(), strconv.Itoa(c.Port))},
		Auth:        clickhouse.Auth{Database: c.Database, Username: c.Username, Password: c.Password},
		TLS:         tlsConfig,
		DialTimeout: 5 * time.Second,
		ReadTimeout: 5 * time.Second,
	}, nil
}

func openRestrictedClickHouse(c model.DataSourceConfig, caPEM []byte) (*sql.DB, error) {
	options, err := restrictedClickHouseOptions(c, caPEM)
	if err != nil {
		return nil, err
	}
	return clickhouse.OpenDB(options), nil
}
