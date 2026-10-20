package config

import (
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	gomysql "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// InitDatabase initializes the database connection with GORM
func InitDatabase(cfg *Config) (*gorm.DB, error) {
	driverConfig, err := metadataDBConfig(cfg)
	if err != nil {
		return nil, err
	}

	// Configure GORM logger
	var gormLogger logger.Interface
	switch cfg.Logging.Level {
	case "debug":
		gormLogger = logger.Default.LogMode(logger.Info)
	case "info":
		gormLogger = logger.Default.LogMode(logger.Warn)
	case "warn":
		gormLogger = logger.Default.LogMode(logger.Error)
	case "error":
		gormLogger = logger.Default.LogMode(logger.Silent)
	default:
		gormLogger = logger.Default.LogMode(logger.Warn)
	}

	db, err := gorm.Open(mysql.New(mysql.Config{DSNConfig: driverConfig}), &gorm.Config{
		Logger: gormLogger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Test the connection
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get underlying sql.DB: %w", err)
	}

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Println("Database connection established successfully")
	return db, nil
}

// metadataDBConfig builds the go-sql-driver configuration for the metadata
// database. Handing the driver config to GORM via DSNConfig (#143) replaces
// DSN string interpolation, so credentials containing reserved DSN
// characters — '@', ':', '/', '?' — are represented correctly, and database.ssl
// finally reaches the connection as a TLS setting.
func metadataDBConfig(cfg *Config) (*gomysql.Config, error) {
	tlsConfig, err := metadataTLSConfig(cfg.Database.SSL)
	if err != nil {
		return nil, err
	}

	driverConfig := gomysql.NewConfig()
	driverConfig.User = cfg.Database.Username
	driverConfig.Passwd = cfg.Database.Password
	driverConfig.Net = "tcp"
	driverConfig.Addr = net.JoinHostPort(cfg.Database.Host, cfg.Database.Port)
	driverConfig.DBName = cfg.Database.Database
	driverConfig.Params = map[string]string{"charset": "utf8mb4"}
	driverConfig.ParseTime = true
	driverConfig.Loc = time.Local
	driverConfig.TLSConfig = tlsConfig
	return driverConfig, nil
}

// metadataTLSConfig maps database.ssl to the MySQL driver's TLS settings for
// the metadata store. This version's semantics:
//   - "" or "false": no TLS (the behavior before the setting was consumed)
//   - "true": TLS required; the server certificate is verified against the
//     system roots and the connected hostname
//   - "skip-verify": TLS required without certificate verification, for
//     servers with self-signed certificates — the connection is encrypted,
//     but the server's identity is not proven
//
// Any other value fails startup instead of silently degrading to plaintext
// or to an unverified connection.
func metadataTLSConfig(ssl string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(ssl)) {
	case "", "false":
		return "", nil
	case "true":
		return "true", nil
	case "skip-verify":
		return "skip-verify", nil
	default:
		return "", fmt.Errorf("database.ssl must be false, true, or skip-verify (got %q)", ssl)
	}
}
