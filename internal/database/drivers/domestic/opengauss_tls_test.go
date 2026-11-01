package domestic

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestOpenGaussBuildDSNTLSConfiguration(t *testing.T) {
	rootCert := filepath.Join(t.TempDir(), "private ca.crt")
	if err := os.WriteFile(rootCert, []byte("test certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name         string
		config       model.DataSourceConfig
		driverMode   string
		wantMode     string
		wantRootCert string
	}{
		{
			name:     "legacy SSL false disables TLS",
			config:   model.DataSourceConfig{},
			wantMode: "disable",
		},
		{
			name: "legacy SSL true requires encryption",
			config: model.DataSourceConfig{
				SSL: true,
			},
			wantMode: "require",
		},
		{
			name: "explicit sslMode overrides legacy boolean",
			config: model.DataSourceConfig{
				SSL:     true,
				SSLMode: "verify-ca",
			},
			wantMode: "verify-ca",
		},
		{
			name: "verified TLS carries CA path",
			config: model.DataSourceConfig{
				SSL:         true,
				SSLMode:     "verify-full",
				SSLRootCert: rootCert,
			},
			wantMode:     "verify-full",
			wantRootCert: rootCert,
		},
		{
			name:       "constructor mode remains a fallback",
			driverMode: "verify-full",
			wantMode:   "verify-full",
		},
		{
			name: "data source mode takes precedence over constructor mode",
			config: model.DataSourceConfig{
				SSLMode: "verify-ca",
			},
			driverMode: "verify-full",
			wantMode:   "verify-ca",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := test.config
			if config.Host == "" {
				config.Host = "localhost"
			}
			if config.Port == 0 {
				config.Port = 5432
			}
			if config.Database == "" {
				config.Database = "analytics"
			}
			if config.Username == "" {
				config.Username = "reader"
			}
			driver := &OpenGaussDriver{}
			if test.driverMode != "" {
				driver.config = &OpenGaussConfig{SSLMode: test.driverMode}
			}

			dsn := driver.BuildDSN(&config)
			parsed, err := url.Parse(dsn)
			if err != nil {
				t.Fatalf("parse DSN: %v", err)
			}
			if got := parsed.Query().Get("sslmode"); got != test.wantMode {
				t.Fatalf("sslmode = %q, want %q", got, test.wantMode)
			}
			if got := parsed.Query().Get("sslrootcert"); got != test.wantRootCert {
				t.Fatalf("sslrootcert = %q, want %q", got, test.wantRootCert)
			}
			if err := driver.ValidateDSN(dsn); err != nil {
				t.Fatalf("ValidateDSN: %v", err)
			}
		})
	}
}

func TestOpenGaussBuildDSNEscapesConnectionValues(t *testing.T) {
	config := &model.DataSourceConfig{
		Host:     "2001:db8::1",
		Port:     5432,
		Database: "analytics archive",
		Username: "reader@example",
		Password: "p@ss:/?#",
	}
	dsn := (&OpenGaussDriver{}).BuildDSN(config)
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	if got := parsed.Host; got != "[2001:db8::1]:5432" {
		t.Fatalf("host = %q, want [2001:db8::1]:5432", got)
	}
	if got := parsed.Path; got != "/analytics archive" {
		t.Fatalf("database path = %q, want %q", got, "/analytics archive")
	}
	username := parsed.User.Username()
	password, ok := parsed.User.Password()
	if username != config.Username || !ok || password != config.Password {
		t.Fatalf("parsed credentials = (%q, %q, %t), want (%q, %q, true)", username, password, ok, config.Username, config.Password)
	}
}

func TestOpenGaussValidateDSNRejectsInvalidTLSConfiguration(t *testing.T) {
	driver := &OpenGaussDriver{}
	rootCert := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(rootCert, []byte("test certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		dsn  string
	}{
		{name: "unsupported mode", dsn: "postgres://reader@localhost/db?sslmode=prefer"},
		{name: "CA with encryption-only mode", dsn: "postgres://reader@localhost/db?sslmode=require&sslrootcert=" + url.QueryEscape(rootCert)},
		{name: "CA with plaintext mode", dsn: "postgres://reader@localhost/db?sslmode=disable&sslrootcert=" + url.QueryEscape(rootCert)},
		{name: "missing CA file", dsn: "postgres://reader@localhost/db?sslmode=verify-full&sslrootcert=" + url.QueryEscape(rootCert+".missing")},
		{name: "wrong DSN scheme", dsn: "mysql://reader@localhost/db?sslmode=verify-full"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := driver.ValidateDSN(test.dsn); err == nil {
				t.Fatalf("ValidateDSN(%q) succeeded; want configuration error", test.dsn)
			}
		})
	}
}
