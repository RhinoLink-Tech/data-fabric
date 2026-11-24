package domestic

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"

	_ "github.com/go-sql-driver/mysql"
)

// GBaseDriver implements Driver interface for GBase 8s
type GBaseDriver struct {
	config *GBaseConfig
}

// GBaseUnsupportedDriver keeps registry entries for discovery while rejecting
// an unverified MySQL-wire connection for a distinct GBase product.
type GBaseUnsupportedDriver struct {
	GBaseDriver
	Variant string
}

func (d *GBaseUnsupportedDriver) Open(_ string) (*sql.DB, error) {
	return nil, fmt.Errorf("%s connection is unavailable: a verified native GBase driver is not configured", d.Variant)
}
func (d *GBaseUnsupportedDriver) GetDatabaseTypeName() string { return d.Variant }
func (d *GBaseUnsupportedDriver) GetDriverName() string       { return d.Variant + "-unavailable" }
func (d *GBaseUnsupportedDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{}
}

// GBaseConfig holds GBase configuration
type GBaseConfig struct {
	Host              string
	Port              int
	Database          string
	Username          string
	Password          string
	Charset           string // UTF8, GB18030
	CompatibilityMode string // INFORMIX, ORACLE
}

// NewGBaseDriver creates a new GBase driver
func NewGBaseDriver(config *GBaseConfig) (*GBaseDriver, error) {
	if config.Host == "" {
		return nil, fmt.Errorf("host is required")
	}

	return &GBaseDriver{
		config: config,
	}, nil
}

// Open opens a connection to GBase (MySQL-compatible)
func (d *GBaseDriver) Open(dsn string) (*sql.DB, error) {
	if err := d.ValidateDSN(dsn); err != nil {
		return nil, err
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open GBase connection: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping GBase: %w", err)
	}

	return db, nil
}

// ValidateDSN validates the connection string
func (d *GBaseDriver) ValidateDSN(dsn string) error {
	if dsn == unsupportedDomesticTLSDSN {
		return fmt.Errorf("requested TLS configuration is unsupported by this driver")
	}
	if dsn == "" {
		return fmt.Errorf("DSN cannot be empty")
	}
	return nil
}

// GetDefaultPort returns the default GBase port
func (d *GBaseDriver) GetDefaultPort() int {
	return 9088
}

// BuildDSN builds a connection string from configuration.
//
// The driver registry hands back a zero-value GBaseDriver (d.config == nil);
// dereferencing d.config here panicked every registry-sourced BuildDSN call.
// The DSN is built from the supplied *model.DataSourceConfig, and the
// constructor-only charset is consulted defensively.
func (d *GBaseDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if unsupportedDomesticTLS(config, "") {
		return unsupportedDomesticTLSDSN
	}

	charset := "utf8"
	if d.config != nil && d.config.Charset != "" {
		charset = d.config.Charset
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?charset=%s",
		config.Username, config.Password, config.Host, config.Port, config.Database, charset)

	return dsn
}

// GetDatabaseTypeName returns the database type name
func (d *GBaseDriver) GetDatabaseTypeName() string {
	return "gbase"
}

// TestConnection tests if the connection is working
func (d *GBaseDriver) TestConnection(db *sql.DB) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return db.PingContext(ctx)
}

// GetDriverName returns the driver name
func (d *GBaseDriver) GetDriverName() string {
	return "gbase-mysql"
}

// GetCategory returns the driver category
func (d *GBaseDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryRelational
}

// GetCapabilities returns driver capabilities
func (d *GBaseDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     true,
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       false,
	}
}

// ConfigureAuth configures authentication
func (d *GBaseDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// GetDatabaseInfo retrieves GBase database information
func (d *GBaseDriver) GetDatabaseInfo(ctx context.Context, db *sql.DB) (*GBaseDatabaseInfo, error) {
	var version string
	err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version)
	if err != nil {
		return nil, fmt.Errorf("failed to get version: %w", err)
	}

	info := &GBaseDatabaseInfo{
		Version:           version,
		CompatibilityMode: d.config.CompatibilityMode,
	}

	return info, nil
}

// GBaseDatabaseInfo represents GBase database information
type GBaseDatabaseInfo struct {
	Version           string
	CompatibilityMode string
	Edition           string // Enterprise, Standard
}

// ExecuteInformixCompatQuery executes query in Informix compatibility mode
func (d *GBaseDriver) ExecuteInformixCompatQuery(ctx context.Context, db *sql.DB, sql string) (*GBaseQueryResult, error) {
	rows, err := db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("failed to execute Informix compat query: %w", err)
	}
	defer rows.Close()

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

	return &GBaseQueryResult{
		Rows:              results,
		Columns:           columns,
		Count:             len(results),
		CompatibilityMode: "INFORMIX",
	}, nil
}

// GBaseQueryResult represents query results
type GBaseQueryResult struct {
	Rows              []map[string]interface{}
	Columns           []string
	Count             int
	CompatibilityMode string
}

// GetFragmentInfo retrieves fragment information (GBase partitioning)
func (d *GBaseDriver) GetFragmentInfo(ctx context.Context, db *sql.DB, tableName string) (*GBaseFragmentInfo, error) {
	sql := `
		SELECT TABNAME, FRAGTYPE, FRAGKEY,
		       NFRAGS, STRATEGY
		FROM SYSTABLES
		WHERE TABNAME = ?
	`

	info := &GBaseFragmentInfo{
		TableName: tableName,
	}

	err := db.QueryRowContext(ctx, sql, tableName).Scan(
		&info.TableName,
		&info.FragmentType,
		&info.FragmentKey,
		&info.NumFragments,
		&info.Strategy,
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get fragment info: %w", err)
	}

	return info, nil
}

// ApplyBatchPagination applies pagination to a SQL query for batch processing
func (d *GBaseDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	sql = strings.TrimSpace(sql)

	// Check if query already has LIMIT or OFFSET
	sqlUpper := strings.ToUpper(sql)
	if strings.Contains(sqlUpper, " LIMIT ") || strings.Contains(sqlUpper, " OFFSET ") {
		// For complex queries with existing pagination, add a subquery wrapper
		return fmt.Sprintf("SELECT * FROM (%s) AS batch_query LIMIT %d OFFSET %d", sql, batchSize, offset), nil
	}
	// Use standard LIMIT/OFFSET for MySQL-compatible databases
	if offset > 0 {
		return fmt.Sprintf("%s LIMIT %d OFFSET %d", sql, batchSize, offset), nil
	}
	return fmt.Sprintf("%s LIMIT %d", sql, batchSize), nil
}

// GBaseFragmentInfo represents fragment information
type GBaseFragmentInfo struct {
	TableName    string
	FragmentType string // ROUND-ROBIN, HASH, EXPRESSION
	FragmentKey  string
	NumFragments int
	Strategy     string
}
