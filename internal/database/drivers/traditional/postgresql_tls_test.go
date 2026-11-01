package traditional

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestPostgreSQLBuildDSNPreservesLegacyTLSModes(t *testing.T) {
	driver := &PostgreSQLDriver{}
	config := &model.DataSourceConfig{Host: "localhost", Port: 5432, Database: "analytics", Username: "reader"}

	for _, test := range []struct {
		name string
		ssl  bool
		want string
	}{
		{name: "TLS disabled by default", want: "disable"},
		{name: "legacy ssl true remains encryption only", ssl: true, want: "require"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config.SSL = test.ssl
			config.SSLMode = ""
			config.SSLRootCert = ""
			parsed, err := url.Parse(driver.BuildDSN(config))
			if err != nil {
				t.Fatal(err)
			}
			if got := parsed.Query().Get("sslmode"); got != test.want {
				t.Fatalf("sslmode = %q, want %q", got, test.want)
			}
		})
	}
}

func TestPostgreSQLBuildAndValidateDSNWithVerifiedTLS(t *testing.T) {
	caPath := filepath.Join(t.TempDir(), "postgres ca.crt")
	if err := os.WriteFile(caPath, []byte("test certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := &model.DataSourceConfig{
		Host:        "localhost",
		Port:        5432,
		Database:    "analytics",
		Username:    "reader",
		SSL:         true,
		SSLMode:     "verify-full",
		SSLRootCert: caPath,
	}
	driver := &PostgreSQLDriver{}
	dsn := driver.BuildDSN(config)
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Query().Get("sslmode"); got != "verify-full" {
		t.Fatalf("sslmode = %q, want verify-full", got)
	}
	if got := parsed.Query().Get("sslrootcert"); got != caPath {
		t.Fatalf("sslrootcert = %q, want configured CA path", got)
	}
	if err := driver.ValidateDSN(dsn); err != nil {
		t.Fatalf("ValidateDSN() error = %v", err)
	}
}

func TestPostgreSQLValidateDSNRejectsInvalidTLSConfiguration(t *testing.T) {
	driver := &PostgreSQLDriver{}
	caPath := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(caPath, []byte("test certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dsn := range []string{
		"postgres://reader@localhost/analytics?sslmode=allow",
		"postgres://reader@localhost/analytics?sslmode=disable&sslrootcert=" + url.QueryEscape(caPath),
		"postgres://reader@localhost/analytics?sslmode=verify-full&sslrootcert=" + url.QueryEscape(caPath+".missing"),
	} {
		if err := driver.ValidateDSN(dsn); err == nil {
			t.Errorf("ValidateDSN(%q) succeeded; want TLS configuration error", dsn)
		}
	}
}
