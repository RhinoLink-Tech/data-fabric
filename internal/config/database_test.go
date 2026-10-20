package config

import (
	"testing"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
)

// TestMetadataDBConfigPreservesSpecialCharacterCredentials locks #143: the
// metadata DSN is no longer built by string interpolation, so reserved DSN
// characters in credentials survive verbatim instead of corrupting the
// connection settings.
func TestMetadataDBConfigPreservesSpecialCharacterCredentials(t *testing.T) {
	cfg := &Config{Database: DatabaseConfig{
		Host:     "db.internal",
		Port:     "3307",
		Database: "gateway_db",
		Username: "user@name",
		Password: "p@ss:word/?sp=ec&ial+",
		SSL:      "false",
	}}

	driverConfig, err := metadataDBConfig(cfg)
	if err != nil {
		t.Fatalf("metadataDBConfig() error = %v", err)
	}

	if driverConfig.User != cfg.Database.Username {
		t.Errorf("User = %q, want %q", driverConfig.User, cfg.Database.Username)
	}
	if driverConfig.Passwd != cfg.Database.Password {
		t.Errorf("Passwd = %q, want %q", driverConfig.Passwd, cfg.Database.Password)
	}
	if driverConfig.Net != "tcp" || driverConfig.Addr != "db.internal:3307" {
		t.Errorf("address = %q(%q), want tcp(db.internal:3307)", driverConfig.Net, driverConfig.Addr)
	}
	if driverConfig.DBName != "gateway_db" {
		t.Errorf("DBName = %q, want gateway_db", driverConfig.DBName)
	}
	if driverConfig.Params["charset"] != "utf8mb4" {
		t.Errorf("charset param = %q, want utf8mb4", driverConfig.Params["charset"])
	}
	if !driverConfig.ParseTime {
		t.Error("ParseTime = false, want true (replaces the old parseTime=True DSN param)")
	}
	if driverConfig.Loc != time.Local {
		t.Errorf("Loc = %v, want time.Local (replaces the old loc=Local DSN param)", driverConfig.Loc)
	}
	if driverConfig.TLSConfig != "" {
		t.Errorf("TLSConfig = %q, want empty for ssl=false", driverConfig.TLSConfig)
	}
}

// TestMetadataDBConfigRoundTripsThroughDSN proves the driver itself can
// represent the configuration: FormatDSN followed by ParseDSN (the parse
// gorm's DSN string path would do) restores every field. This is the
// no-running-MySQL unit test the #143 acceptance asks for.
func TestMetadataDBConfigRoundTripsThroughDSN(t *testing.T) {
	cfg := &Config{Database: DatabaseConfig{
		Host:     "localhost",
		Port:     "3306",
		Database: "gateway_db",
		Username: "user@name",
		Password: "p@ss:word/?sp=ec&ial+",
		SSL:      "true",
	}}

	driverConfig, err := metadataDBConfig(cfg)
	if err != nil {
		t.Fatalf("metadataDBConfig() error = %v", err)
	}

	parsed, err := gomysql.ParseDSN(driverConfig.FormatDSN())
	if err != nil {
		t.Fatalf("ParseDSN(FormatDSN()) error = %v", err)
	}
	if parsed.User != driverConfig.User || parsed.Passwd != driverConfig.Passwd {
		t.Errorf("credentials did not round-trip: got %q/%q, want %q/%q",
			parsed.User, parsed.Passwd, driverConfig.User, driverConfig.Passwd)
	}
	if parsed.Net != driverConfig.Net || parsed.Addr != driverConfig.Addr {
		t.Errorf("address did not round-trip: got %q(%q), want %q(%q)",
			parsed.Net, parsed.Addr, driverConfig.Net, driverConfig.Addr)
	}
	if parsed.DBName != driverConfig.DBName {
		t.Errorf("DBName did not round-trip: got %q, want %q", parsed.DBName, driverConfig.DBName)
	}
	if parsed.TLSConfig != "true" {
		t.Errorf("TLSConfig did not round-trip: got %q, want true", parsed.TLSConfig)
	}
}

// TestMetadataTLSConfig pins this version's database.ssl semantics (#143):
// plaintext default, verified TLS, TLS without verification for self-signed
// servers — and startup failure for anything else.
func TestMetadataTLSConfig(t *testing.T) {
	tests := []struct {
		name string
		ssl  string
		want string
		err  bool
	}{
		{name: "empty is plaintext", ssl: "", want: ""},
		{name: "false is plaintext", ssl: "false", want: ""},
		{name: "FALSE is plaintext", ssl: "FALSE", want: ""},
		{name: "padded false is plaintext", ssl: " false ", want: ""},
		{name: "true enables verified TLS", ssl: "true", want: "true"},
		{name: "TRUE enables verified TLS", ssl: "TRUE", want: "true"},
		{name: "skip-verify enables unverified TLS", ssl: "skip-verify", want: "skip-verify"},
		{name: "preferred is unsupported", ssl: "preferred", err: true},
		{name: "verify-ca is unsupported", ssl: "verify-ca", err: true},
		{name: "custom tls name is unsupported", ssl: "my-ca", err: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := metadataTLSConfig(tt.ssl)
			if tt.err {
				if err == nil {
					t.Fatalf("metadataTLSConfig(%q) = %q, want error", tt.ssl, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("metadataTLSConfig(%q) error = %v", tt.ssl, err)
			}
			if got != tt.want {
				t.Errorf("metadataTLSConfig(%q) = %q, want %q", tt.ssl, got, tt.want)
			}
		})
	}
}

// TestDatabaseValidateRejectsUnsupportedSSL keeps the startup guard honest:
// the value fails validation before any connection is attempted.
func TestDatabaseValidateRejectsUnsupportedSSL(t *testing.T) {
	cfg := DatabaseConfig{Host: "localhost", Port: "3306", Database: "db", Username: "user", SSL: "preferred"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() accepted an unsupported database.ssl value")
	}
}
