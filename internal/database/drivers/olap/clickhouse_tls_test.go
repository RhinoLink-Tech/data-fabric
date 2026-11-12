package olap

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
)

func TestClickHouseBuildDSNEncodesCredentialsAndTLS(t *testing.T) {
	rootCertPath := writeClickHouseTestCA(t)
	config := &model.DataSourceConfig{
		Host:        "clickhouse.example",
		Port:        9440,
		Database:    "analytics",
		Username:    "reader@team",
		Password:    "p@ss:/?#word",
		SSL:         true,
		SSLRootCert: rootCertPath,
	}
	driver := &ClickHouseDriver{}
	dsn := driver.BuildDSN(config)
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN for generated TLS configuration: %v", err)
	}

	parsedURL, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse generated DSN: %v", err)
	}
	username := parsedURL.User.Username()
	password, _ := parsedURL.User.Password()
	if username != config.Username || password != config.Password {
		t.Fatalf("generated credentials = (%q, %q), want (%q, %q)", username, password, config.Username, config.Password)
	}
	if got := parsedURL.Query().Get("secure"); got != "true" {
		t.Fatalf("secure = %q, want true", got)
	}
	if got := parsedURL.Query().Get("skip_verify"); got != "false" {
		t.Fatalf("skip_verify = %q, want false", got)
	}
	if got := parsedURL.Query().Get(clickHouseTLSRootCertParam); got != rootCertPath {
		t.Fatalf("root certificate path = %q, want %q", got, rootCertPath)
	}

	options, parsedRootCert, err := parseClickHouseDSN(dsn)
	if err != nil {
		t.Fatalf("parse generated ClickHouse DSN: %v", err)
	}
	if options.TLS == nil || options.TLS.InsecureSkipVerify {
		t.Fatalf("generated TLS options = %+v, want verified TLS", options.TLS)
	}
	if parsedRootCert != rootCertPath {
		t.Fatalf("parsed root certificate path = %q, want %q", parsedRootCert, rootCertPath)
	}
	if _, exists := options.Settings[clickHouseTLSRootCertParam]; exists {
		t.Fatal("Data Fabric root certificate path leaked into ClickHouse server settings")
	}
}

func TestClickHouseTLSConfigurationValidation(t *testing.T) {
	rootCertPath := writeClickHouseTestCA(t)
	driver := &ClickHouseDriver{}

	for _, test := range []struct {
		name    string
		dsn     string
		wantErr bool
	}{
		{
			name: "verified TLS",
			dsn:  "clickhouse://reader@localhost:9440/default?secure=true&skip_verify=false",
		},
		{
			name: "plaintext compatibility",
			dsn:  "clickhouse://reader@localhost:9000/default",
		},
		{
			name:    "private CA requires TLS",
			dsn:     "clickhouse://reader@localhost:9000/default?" + clickHouseTLSRootCertParam + "=" + url.QueryEscape(rootCertPath),
			wantErr: true,
		},
		{
			name:    "reject disabled certificate verification",
			dsn:     "clickhouse://reader@localhost:9440/default?secure=true&skip_verify=true",
			wantErr: true,
		},
		{
			name:    "reject verification bypass without TLS",
			dsn:     "clickhouse://reader@localhost:9000/default?skip_verify=true",
			wantErr: true,
		},
		{
			name:    "reject unreadable CA path",
			dsn:     "clickhouse://reader@localhost:9440/default?secure=true&" + clickHouseTLSRootCertParam + "=" + url.QueryEscape(filepath.Join(t.TempDir(), "missing.pem")),
			wantErr: true,
		},
		{
			name:    "reject malformed CA bundle",
			dsn:     "clickhouse://reader@localhost:9440/default?secure=true&" + clickHouseTLSRootCertParam + "=" + url.QueryEscape(writeClickHouseInvalidCA(t)),
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := driver.ValidateDSN(test.dsn)
			if test.wantErr && err == nil {
				t.Fatal("ValidateDSN succeeded, want an error")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("ValidateDSN: %v", err)
			}
		})
	}
}

func writeClickHouseTestCA(t *testing.T) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("generate CA serial: %v", err)
	}
	now := time.Now()
	certificate := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Data Fabric ClickHouse Test CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create CA certificate: %v", err)
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write CA certificate: %v", err)
	}
	return path
}

func writeClickHouseInvalidCA(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "invalid-ca.pem")
	if err := os.WriteFile(path, []byte("not a PEM certificate"), 0o600); err != nil {
		t.Fatalf("write invalid CA fixture: %v", err)
	}
	return path
}
