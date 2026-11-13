package traditional

import (
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	_ "github.com/lib/pq"
)

// PostgreSQLDriver implements Driver for PostgreSQL
type PostgreSQLDriver struct{}

func (d *PostgreSQLDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
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
func (d *PostgreSQLDriver) Open(dsn string) (*sql.DB, error) {
	return sql.Open("postgres", dsn)
}

func (d *PostgreSQLDriver) ValidateDSN(dsn string) error {
	if strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("DSN cannot be empty")
	}
	parsed, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("invalid PostgreSQL DSN")
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return fmt.Errorf("PostgreSQL DSN must use the postgres or postgresql scheme")
	}

	query := parsed.Query()
	mode := query.Get("sslmode")
	switch mode {
	case "", "disable", "require", "verify-ca", "verify-full":
	default:
		return fmt.Errorf("unsupported PostgreSQL sslMode; use disable, require, verify-ca, or verify-full")
	}

	rootCert := query.Get("sslrootcert")
	if rootCert == "" {
		return nil
	}
	if mode != "verify-ca" && mode != "verify-full" {
		return fmt.Errorf("PostgreSQL sslRootCert requires sslMode verify-ca or verify-full")
	}
	file, err := os.Open(rootCert)
	if err != nil {
		return fmt.Errorf("PostgreSQL sslRootCert must refer to a readable certificate file")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("PostgreSQL sslRootCert must refer to a readable certificate file")
	}
	return nil
}

func (d *PostgreSQLDriver) GetDefaultPort() int {
	return 5432
}

func (d *PostgreSQLDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil || !safeDSNHost(config.Host) || config.Port < 1 || config.Port > 65535 {
		return ""
	}
	sslMode := config.SSLMode
	if sslMode == "" {
		if config.SSL {
			sslMode = "require"
		} else {
			sslMode = "disable"
		}
	}
	params := url.Values{"sslmode": {sslMode}}
	if config.SSLRootCert != "" {
		params.Set("sslrootcert", config.SSLRootCert)
	}
	if config.Timezone != "" {
		params.Set("TimeZone", config.Timezone)
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(config.Username, config.Password), Host: net.JoinHostPort(strings.Trim(config.Host, "[]"), strconv.Itoa(config.Port)), Path: "/" + config.Database, RawPath: "/" + url.PathEscape(config.Database), RawQuery: params.Encode()}
	return u.String()
}

func (d *PostgreSQLDriver) GetDatabaseTypeName() string {
	return "postgresql"
}

func (d *PostgreSQLDriver) TestConnection(db *sql.DB) error {
	return db.Ping()
}

func (d *PostgreSQLDriver) GetDriverName() string {
	return "postgres"
}

func (d *PostgreSQLDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryRelational
}

func (d *PostgreSQLDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     true,
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       true,
	}
}

func (d *PostgreSQLDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}
