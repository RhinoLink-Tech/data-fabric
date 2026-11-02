package traditional

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/denisenkom/go-mssqldb/msdsn"
)

func TestSQLServerBuildDSNMapsTLSConfiguration(t *testing.T) {
	driver := NewSQLServerDriver()
	caPath := writeSQLServerTestCA(t)
	config := &model.DataSourceConfig{
		Host: "localhost", Port: 1433, Database: "analytics", Username: "reader",
		Password: "p@ss:/? #", SSL: true, SSLRootCert: caPath,
		AdditionalProps: map[string]interface{}{"TrustServerCertificate": true, "Encrypt": false, "DATABASE": "unexpected"},
	}

	dsn := driver.BuildDSN(config)
	parsed, params, err := msdsn.Parse(dsn)
	if err != nil {
		t.Fatalf("parse SQL Server DSN: %v", err)
	}
	if parsed.Host != config.Host || parsed.Port != uint64(config.Port) ||
		parsed.Database != config.Database || parsed.User != config.Username || parsed.Password != config.Password {
		t.Fatalf("parsed SQL Server configuration does not match the input")
	}
	if parsed.Encryption != msdsn.EncryptionRequired || parsed.TLSConfig == nil || parsed.TLSConfig.InsecureSkipVerify {
		t.Fatal("private-CA SQL Server DSN must require encryption and verify the server certificate")
	}
	if params["certificate"] != caPath {
		t.Fatalf("certificate = %q, want %q", params["certificate"], caPath)
	}
	if params["trustservercertificate"] != "false" {
		t.Fatalf("trustservercertificate = %q, want false", params["trustservercertificate"])
	}
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN with private CA: %v", err)
	}

	plain := *config
	plain.SSL = false
	plain.SSLRootCert = ""
	plain.AdditionalProps = map[string]interface{}{"encrypt": true}
	parsed, _, err = msdsn.Parse(driver.BuildDSN(&plain))
	if err != nil {
		t.Fatalf("parse plaintext SQL Server DSN: %v", err)
	}
	if parsed.Encryption != msdsn.EncryptionOff {
		t.Fatalf("plaintext encryption mode = %v, want disabled", parsed.Encryption)
	}
}

func TestSQLServerValidateDSNRejectsInvalidTLSConfiguration(t *testing.T) {
	driver := NewSQLServerDriver()
	caPath := writeSQLServerTestCA(t)
	invalidCAPath := filepath.Join(t.TempDir(), "invalid-ca.pem")
	if err := os.WriteFile(invalidCAPath, []byte("not a PEM certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		config *model.DataSourceConfig
		mutate func(string) string
	}{
		{
			name: "CA with TLS disabled",
			config: &model.DataSourceConfig{
				Host: "localhost", Port: 1433, Database: "analytics", Username: "reader",
				SSLRootCert: caPath,
			},
		},
		{
			name: "missing CA file",
			config: &model.DataSourceConfig{
				Host: "localhost", Port: 1433, Database: "analytics", Username: "reader",
				SSL: true, SSLRootCert: caPath + ".missing",
			},
		},
		{
			name: "invalid CA PEM",
			config: &model.DataSourceConfig{
				Host: "localhost", Port: 1433, Database: "analytics", Username: "reader",
				SSL: true, SSLRootCert: invalidCAPath,
			},
		},
		{
			name: "certificate validation bypass",
			config: &model.DataSourceConfig{
				Host: "localhost", Port: 1433, Database: "analytics", Username: "reader",
				SSL: true, SSLRootCert: caPath,
			},
			mutate: func(dsn string) string {
				return strings.Replace(dsn, "trustservercertificate=false", "trustservercertificate=true", 1)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dsn := driver.BuildDSN(test.config)
			if test.mutate != nil {
				dsn = test.mutate(dsn)
			}
			if err := driver.ValidateDSN(dsn); err == nil {
				t.Fatalf("ValidateDSN(%q) succeeded; want an error", dsn)
			}
		})
	}
}

func writeSQLServerTestCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Data Fabric SQL Server Test CA"},
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
	path := filepath.Join(t.TempDir(), "sql server test ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
