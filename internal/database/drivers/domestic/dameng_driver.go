package domestic

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// DaMengConfig holds DaMeng configuration
type DaMengConfig struct {
	Host     string
	Port     int
	Database string
	Username string
	Password string
	Charset  string // UTF8, GB18030
}

// NewDaMengConfig creates a new default DaMeng configuration
func NewDaMengConfig() *DaMengConfig {
	return &DaMengConfig{
		Host:     "localhost",
		Port:     5236,
		Database: "",
		Username: "",
		Password: "",
		Charset:  "UTF8",
	}
}

// DaMengDriver implements Driver interface for DaMeng database
type DaMengDriver struct {
	config *DaMengConfig
}

// NewDaMengDriver creates a new DaMeng driver
func NewDaMengDriver(config *DaMengConfig) (*DaMengDriver, error) {
	if config.Host == "" {
		return nil, fmt.Errorf("host is required")
	}

	return &DaMengDriver{
		config: config,
	}, nil
}

var ErrDaMengDriverUnavailable = errors.New("DaMeng native database/sql driver is not registered in this build")

// Open opens a connection to DaMeng
func (d *DaMengDriver) Open(dsn string) (*sql.DB, error) {
	registered := false
	for _, name := range sql.Drivers() {
		if name == "dameng" {
			registered = true
			break
		}
	}
	if !registered {
		return nil, ErrDaMengDriverUnavailable
	}
	db, err := sql.Open("dameng", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open DaMeng connection: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping DaMeng: %w", err)
	}

	return db, nil
}

// ValidateDSN validates the connection string
func (d *DaMengDriver) ValidateDSN(dsn string) error {
	if dsn == "" {
		return fmt.Errorf("DSN cannot be empty")
	}
	return nil
}

// GetDefaultPort returns the default DaMeng port
func (d *DaMengDriver) GetDefaultPort() int {
	return 5236
}

// BuildDSN builds a connection string from configuration
func (d *DaMengDriver) BuildDSN(config *model.DataSourceConfig) string {
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
		params = append(params, "charset=utf8")
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

// GetDatabaseTypeName returns the database type name
func (d *DaMengDriver) GetDatabaseTypeName() string {
	return "dameng"
}

// TestConnection tests if the connection is working
func (d *DaMengDriver) TestConnection(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return db.PingContext(ctx)
}

// GetDriverName returns the driver name
func (d *DaMengDriver) GetDriverName() string {
	return "dameng"
}

// GetCategory returns the driver category
func (d *DaMengDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryDomesticDatabase
}

// GetCapabilities returns driver capabilities
func (d *DaMengDriver) GetCapabilities() map[string]bool {
	return map[string]bool{
		"SupportsSQL":             true,
		"SupportsTransaction":     true,
		"SupportsSchemaDiscovery": true,
		"SupportsTimeTravel":      false,
		"RequiresTokenRotation":   false,
		"SupportsStreaming":       false,
	}
}

// ConfigureAuth configures authentication
func (d *DaMengDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// QueryWithCharset executes a query with specific charset
func (d *DaMengDriver) QueryWithCharset(ctx context.Context, db *sql.DB, sql string, charset string) (*DaMengQueryResult, error) {
	// Charset handling should be configured via DSN/driver; do not execute Postgres-specific SET CLIENT_ENCODING here.
	_ = charset

	rows, err := db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	defer rows.Close()

	// Read results
	columns, _ := rows.Columns()
	var results []map[string]interface{}

	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, err
		}

		rowMap := make(map[string]interface{})
		for i, col := range columns {
			rowMap[col] = values[i]
		}
		results = append(results, rowMap)
	}

	return &DaMengQueryResult{
		Rows:    results,
		Columns: columns,
		Count:   len(results),
		Charset: charset,
	}, nil
}

// DaMengQueryResult represents DaMeng query results
type DaMengQueryResult struct {
	Rows    []map[string]interface{}
	Columns []string
	Count   int
	Charset string
}

// GetDatabaseInfo retrieves DaMeng database information
func (d *DaMengDriver) GetDatabaseInfo(ctx context.Context, db *sql.DB) (*DaMengDatabaseInfo, error) {
	var version string
	err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version)
	if err != nil {
		return nil, fmt.Errorf("failed to get version: %w", err)
	}

	info := &DaMengDatabaseInfo{
		Version: version,
		Charset: d.config.Charset,
	}

	return info, nil
}

// DaMengDatabaseInfo represents DaMeng database information
type DaMengDatabaseInfo struct {
	Version string
	Charset string
	Mode    string // Enterprise or Standard
}

// adapter to implement database.Driver
type DaMengDBAdapter struct {
	Inner *DaMengDriver
}

func (a *DaMengDBAdapter) Open(dsn string) (*sql.DB, error)            { return a.Inner.Open(dsn) }
func (a *DaMengDBAdapter) ValidateDSN(dsn string) error                { return a.Inner.ValidateDSN(dsn) }
func (a *DaMengDBAdapter) GetDefaultPort() int                         { return a.Inner.GetDefaultPort() }
func (a *DaMengDBAdapter) BuildDSN(cfg *model.DataSourceConfig) string { return a.Inner.BuildDSN(cfg) }
func (a *DaMengDBAdapter) GetDatabaseTypeName() string                 { return a.Inner.GetDatabaseTypeName() }
func (a *DaMengDBAdapter) TestConnection(db *sql.DB) error             { return a.Inner.TestConnection(db) }
func (a *DaMengDBAdapter) GetDriverName() string                       { return a.Inner.GetDriverName() }
func (a *DaMengDBAdapter) GetCategory() drivers.DriverCategory {
	return drivers.CategoryDomesticDatabase
}
func (a *DaMengDBAdapter) GetCapabilities() drivers.DriverCapabilities {
	caps := a.Inner.GetCapabilities()
	return drivers.DriverCapabilities{
		SupportsSQL:             caps["SupportsSQL"],
		SupportsTransaction:     caps["SupportsTransaction"],
		SupportsSchemaDiscovery: caps["SupportsSchemaDiscovery"],
		SupportsTimeTravel:      caps["SupportsTimeTravel"],
		RequiresTokenRotation:   caps["RequiresTokenRotation"],
		SupportsStreaming:       caps["SupportsStreaming"],
	}
}
func (a *DaMengDBAdapter) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	sql, err := trimStatementTerminator(sql)
	if err != nil {
		return "", err
	}

	// Check if query already has pagination clauses
	sqlUpper := strings.ToUpper(sql)
	if (strings.Contains(sqlUpper, " OFFSET ") && strings.Contains(sqlUpper, " ROWS")) ||
		(strings.Contains(sqlUpper, " FETCH FIRST ") && strings.Contains(sqlUpper, " ROWS ONLY")) {
		// For complex queries with existing pagination, add a subquery wrapper
		return fmt.Sprintf("SELECT * FROM (%s) AS batch_query OFFSET %d ROWS FETCH FIRST %d ROWS ONLY", sql, offset, batchSize), nil
	}
	if offset > 0 {
		return fmt.Sprintf("%s OFFSET %d ROWS FETCH FIRST %d ROWS ONLY", sql, offset, batchSize), nil
	}
	return fmt.Sprintf("%s FETCH FIRST %d ROWS ONLY", sql, batchSize), nil
}

func (a *DaMengDBAdapter) ConfigureAuth(authConfig interface{}) error {
	return a.Inner.ConfigureAuth(authConfig)
}

//func init() {
//	// Attempt to register the concrete DaMeng driver with the global registry.
//	cfg := NewDaMengConfig()
//	if drv, err := NewDaMengDriver(cfg); err == nil {
//		//  database.GetDriverRegistry().RegisterDriver(model.DatabaseTypeDaMeng, &DaMengDBAdapter{inner: drv})
//	}
//}
