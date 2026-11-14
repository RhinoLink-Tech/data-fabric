package mysqlutil

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql/driver"
	"errors"
	"net"
	"net/netip"
	"os"

	"github.com/go-sql-driver/mysql"
)

const RootCertParam = "datafabricsslrootcert"

// Connector preserves the scoped address for dialing while using its IP
// without the local interface zone for X.509 verification.
func Connector(cfg *mysql.Config) (driver.Connector, error) {
	if cfg == nil {
		return nil, errors.New("invalid MySQL connection configuration")
	}
	cfg = cfg.Clone()
	root := cfg.Params[RootCertParam]
	delete(cfg.Params, RootCertParam)
	if root != "" && (cfg.TLSConfig != "true" || cfg.TLS == nil || cfg.TLS.InsecureSkipVerify) {
		return nil, errors.New("MySQL sslRootCert requires verified tls=true")
	}
	if cfg.TLS != nil && !cfg.TLS.InsecureSkipVerify {
		host, _, splitErr := net.SplitHostPort(cfg.Addr)
		addr, parseErr := netip.ParseAddr(host)
		if splitErr == nil && parseErr == nil && addr.Zone() != "" {
			cfg.TLS.ServerName = addr.WithZone("").String()
		}
	}
	if root != "" {
		pem, err := os.ReadFile(root)
		if err != nil {
			return nil, errors.New("MySQL sslRootCert must refer to a readable PEM CA certificate")
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(pem) {
			return nil, errors.New("MySQL sslRootCert must contain at least one valid PEM CA certificate")
		}
		cfg.TLS = cfg.TLS.Clone()
		cfg.TLS.MinVersion = tls.VersionTLS12
		cfg.TLS.RootCAs = roots
	}
	c, err := mysql.NewConnector(cfg)
	if err != nil {
		return nil, errors.New("invalid MySQL connection configuration")
	}
	return c, nil
}
