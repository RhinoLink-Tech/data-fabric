package traditional

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/go-sql-driver/mysql"
)

func TestMySQLBuildDSNMapsTLSConfiguration(t *testing.T) {
	driver := &MySQLDriver{}
	base := &model.DataSourceConfig{
		Host: "localhost", Port: 3306, Database: "analytics", Username: "reader",
	}

	plaintext, err := mysql.ParseDSN(driver.BuildDSN(base))
	if err != nil {
		t.Fatal(err)
	}
	if plaintext.TLSConfig != "" {
		t.Fatalf("TLS config = %q, want disabled by default", plaintext.TLSConfig)
	}

	tlsConfig := *base
	tlsConfig.SSL = true
	encrypted, err := mysql.ParseDSN(driver.BuildDSN(&tlsConfig))
	if err != nil {
		t.Fatal(err)
	}
	if encrypted.TLSConfig != "true" {
		t.Fatalf("TLS config = %q, want verified tls=true", encrypted.TLSConfig)
	}
	if err := driver.ValidateDSN(driver.BuildDSN(&tlsConfig)); err != nil {
		t.Fatalf("ValidateDSN with verified TLS: %v", err)
	}
}

func TestMySQLBuildDSNUsesPrivateCACertificate(t *testing.T) {
	driver := &MySQLDriver{}
	caPath := writeMySQLTestCA(t)
	config := &model.DataSourceConfig{
		Host: "localhost", Port: 3306, Database: "analytics", Username: "reader",
		SSL: true, SSLRootCert: caPath,
	}
	dsn := driver.BuildDSN(config)
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.TLSConfig != "true" {
		t.Fatalf("TLS config = %q, want true", parsed.TLSConfig)
	}
	if got := parsed.Params[mysqlTLSRootCertParam]; got != caPath {
		t.Fatalf("CA path = %q, want %q", got, caPath)
	}
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN with private CA: %v", err)
	}
}

func TestMySQLValidateDSNRejectsInsecureOrInvalidTLS(t *testing.T) {
	driver := &MySQLDriver{}
	caPath := writeMySQLTestCA(t)
	invalidCAPath := filepath.Join(t.TempDir(), "invalid-ca.pem")
	if err := os.WriteFile(invalidCAPath, []byte("not a PEM CA"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		dsn  string
	}{
		{name: "skip certificate verification", dsn: "reader@tcp(localhost:3306)/analytics?tls=skip-verify"},
		{name: "allow plaintext fallback", dsn: "reader@tcp(localhost:3306)/analytics?tls=preferred"},
		{name: "CA without TLS", dsn: "reader@tcp(localhost:3306)/analytics?" + mysqlTLSRootCertParam + "=" + url.QueryEscape(caPath)},
		{name: "missing CA file", dsn: "reader@tcp(localhost:3306)/analytics?tls=true&" + mysqlTLSRootCertParam + "=" + url.QueryEscape(caPath+".missing")},
		{name: "invalid CA content", dsn: "reader@tcp(localhost:3306)/analytics?tls=true&" + mysqlTLSRootCertParam + "=" + url.QueryEscape(invalidCAPath)},
		{name: "unknown TLS mode", dsn: "reader@tcp(localhost:3306)/analytics?tls=custom-name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := driver.ValidateDSN(test.dsn); err == nil {
				t.Fatalf("ValidateDSN(%q) succeeded; want an error", test.dsn)
			}
		})
	}
}

func writeMySQLTestCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Data Fabric MySQL Contract CA"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "mysql ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
