package traditional

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/go-sql-driver/mysql"
)

const mysqlTLSRootCertParam = "datafabricsslrootcert"

// MySQLDriver implements Driver for MySQL/MariaDB
type MySQLDriver struct{}

func (qs *MySQLDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {

	sql = strings.TrimSpace(sql)

	// Check if query already has LIMIT or OFFSET
	sqlUpper := strings.ToUpper(sql)
	if strings.Contains(sqlUpper, " LIMIT ") || strings.Contains(sqlUpper, " OFFSET ") {
		// For complex queries with existing pagination, add a subquery wrapper
		return fmt.Sprintf("SELECT * FROM (%s) AS batch_query LIMIT %d OFFSET %d", sql, batchSize, offset), nil

	}
	// Use standard LIMIT/OFFSET for most databases
	if offset > 0 {
		return fmt.Sprintf("%s LIMIT %d OFFSET %d", sql, batchSize, offset), nil
	}
	return fmt.Sprintf("%s LIMIT %d", sql, batchSize), nil
}
func (d *MySQLDriver) Open(dsn string) (*sql.DB, error) {
	if err := d.ValidateDSN(dsn); err != nil {
		return nil, fmt.Errorf("invalid MySQL connection configuration: %w", err)
	}

	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid MySQL connection string")
	}
	rootCertPath := config.Params[mysqlTLSRootCertParam]
	delete(config.Params, mysqlTLSRootCertParam)
	if rootCertPath == "" {
		return sql.Open("mysql", dsn)
	}

	certificate, err := os.ReadFile(rootCertPath)
	if err != nil {
		return nil, fmt.Errorf("MySQL sslRootCert must refer to a readable PEM CA certificate")
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(certificate) {
		return nil, fmt.Errorf("MySQL sslRootCert must contain at least one valid PEM CA certificate")
	}
	config.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    rootCAs,
	}
	connector, err := mysql.NewConnector(config)
	if err != nil {
		return nil, fmt.Errorf("invalid MySQL TLS configuration")
	}
	return sql.OpenDB(connector), nil
}

func (d *MySQLDriver) ValidateDSN(dsn string) error {
	if strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("DSN cannot be empty")
	}
	config, err := mysql.ParseDSN(dsn)
	if err != nil {
		return fmt.Errorf("invalid MySQL DSN")
	}
	if config.TLSConfig != "" && config.TLSConfig != "true" && config.TLSConfig != "false" {
		return fmt.Errorf("MySQL TLS requires verified tls=true; insecure modes and custom TLS names are unsupported")
	}
	rootCertPath := config.Params[mysqlTLSRootCertParam]
	if rootCertPath == "" {
		return nil
	}
	if config.TLSConfig != "true" {
		return fmt.Errorf("MySQL sslRootCert requires ssl=true")
	}
	certificate, err := os.ReadFile(rootCertPath)
	if err != nil {
		return fmt.Errorf("MySQL sslRootCert must refer to a readable PEM CA certificate")
	}
	rootCAs := x509.NewCertPool()
	if !rootCAs.AppendCertsFromPEM(certificate) {
		return fmt.Errorf("MySQL sslRootCert must contain at least one valid PEM CA certificate")
	}
	return nil
}

func (d *MySQLDriver) GetDefaultPort() int {
	return 3306
}

func (d *MySQLDriver) BuildDSN(config *model.DataSourceConfig) string {
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s",
		config.Username,
		config.Password,
		config.Host,
		config.Port,
		config.Database,
	)

	// Add parameters
	params := []string{}
	if config.SSL {
		params = append(params, "tls=true")
	}
	if config.SSLRootCert != "" {
		params = append(params, mysqlTLSRootCertParam+"="+url.QueryEscape(config.SSLRootCert))
	}
	if config.Timezone != "" {
		params = append(params, "loc="+config.Timezone)
	}

	if len(params) > 0 {
		dsn += "?"
		for i, param := range params {
			if i > 0 {
				dsn += "&"
			}
			dsn += param
		}
	}

	return dsn
}

func (d *MySQLDriver) GetDatabaseTypeName() string {
	return "mysql"
}

func (d *MySQLDriver) TestConnection(db *sql.DB) error {
	return db.Ping()
}

func (d *MySQLDriver) GetDriverName() string {
	return "mysql"
}

func (d *MySQLDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryRelational
}

func (d *MySQLDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     true,
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       true,
	}
}

func (d *MySQLDriver) ConfigureAuth(authConfig interface{}) error {
	// MySQL uses basic auth, no special configuration
	return nil
}
