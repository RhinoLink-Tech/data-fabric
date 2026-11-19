package warehouses

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// MaxComputeDriver implements the Driver interface for Alibaba Cloud MaxCompute
type MaxComputeDriver struct {
	base *drivers.DriverBase
}

// NewMaxComputeDriver creates a new MaxCompute driver instance
func NewMaxComputeDriver() *MaxComputeDriver {
	return &MaxComputeDriver{
		base: drivers.NewDriverBase(model.DatabaseTypeMaxCompute, drivers.CategoryWarehouse),
	}
}

// ApplyBatchPagination applies pagination to SQL query for batch processing
func (md *MaxComputeDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	// MaxCompute doesn't support traditional LIMIT/OFFSET in all contexts
	// However, we can apply standard LIMIT clause for supported scenarios
	return fmt.Sprintf("%s LIMIT %d", sql, batchSize+offset), nil
}

// ErrMaxComputeQueryUnavailable is returned until actual SQL results can be
// fetched and decoded. It is deliberately returned before submitting a task.
var ErrMaxComputeQueryUnavailable = errors.New("MaxCompute SQL query execution is unavailable: result retrieval is not implemented")

// Open fails closed rather than returning a connection with placeholder rows.
func (md *MaxComputeDriver) Open(_ string) (*sql.DB, error) {
	return nil, ErrMaxComputeQueryUnavailable
}

// ValidateDSN validates the MaxCompute connection string
func (md *MaxComputeDriver) ValidateDSN(dsn string) error {
	// Parse DSN and validate its format
	parsedURL, err := url.Parse(dsn)
	if err != nil {
		return fmt.Errorf("invalid DSN format: %v", err)
	}

	// Check if it's using maxcompute scheme
	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme != "maxcompute" && scheme != "odps" {
		return fmt.Errorf("invalid scheme in DSN: %s, expected 'maxcompute' or 'odps'", scheme)
	}

	if parsedURL.Host == "" {
		return fmt.Errorf("missing host in DSN")
	}

	if parsedURL.User == nil {
		return fmt.Errorf("missing access ID in DSN")
	}

	password, ok := parsedURL.User.Password()
	if !ok {
		return fmt.Errorf("missing access key in DSN")
	}

	if password == "" {
		return fmt.Errorf("empty access key in DSN")
	}

	// Check for required project parameter
	if parsedURL.Query().Get("project") == "" {
		return fmt.Errorf("missing project parameter in DSN")
	}

	return nil
}

// GetDefaultPort returns the default port for MaxCompute
func (md *MaxComputeDriver) GetDefaultPort() int {
	return 80 // Default MaxCompute HTTP port
}

// BuildDSN builds a MaxCompute connection string from configuration
func (md *MaxComputeDriver) BuildDSN(config *model.DataSourceConfig) string {
	// Build MaxCompute connection string
	// Format: maxcompute://accessId:accessKey@endpoint/api?project=projectName
	dsn := fmt.Sprintf("maxcompute://%s:%s@%s:%d?project=%s",
		config.Username,                  // Access ID
		url.QueryEscape(config.Password), // Access Key
		config.Host,                      // Endpoint
		config.Port,                      // Port
		config.Database,                  // Project name
	)

	// Add additional properties
	if config.AdditionalProps != nil {
		params := url.Values{}
		for k, v := range config.AdditionalProps {
			params.Add(k, fmt.Sprintf("%v", v))
		}
		// Append additional params to the DSN
		if params.Encode() != "" {
			dsn += "&" + params.Encode()
		}
	}

	return dsn
}

// GetDatabaseTypeName returns the database type name
func (md *MaxComputeDriver) GetDatabaseTypeName() string {
	return md.base.GetDatabaseTypeName()
}

// TestConnection reports unavailable while query-based health checks are unsupported.
func (md *MaxComputeDriver) TestConnection(_ *sql.DB) error {
	return ErrMaxComputeQueryUnavailable
}

// GetDriverName returns the underlying SQL driver name for MaxCompute
func (md *MaxComputeDriver) GetDriverName() string {
	return "maxcompute"
}

// GetCategory returns the driver category
func (md *MaxComputeDriver) GetCategory() drivers.DriverCategory {
	return md.base.GetCategory()
}

// GetCapabilities returns driver capabilities
func (md *MaxComputeDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             false, // Result retrieval is not implemented
		SupportsTransaction:     false, // MaxCompute doesn't support traditional ACID transactions
		SupportsSchemaDiscovery: false, // SQL-based schema discovery is unavailable
		SupportsTimeTravel:      false, // MaxCompute doesn't have built-in time travel
		RequiresTokenRotation:   false, // No working authentication path is exposed
		SupportsStreaming:       false, // Primarily batch processing
	}
}

// ConfigureAuth configures authentication for MaxCompute (supports access key authentication)
func (md *MaxComputeDriver) ConfigureAuth(authConfig interface{}) error {
	// MaxCompute uses Access ID and Access Key for authentication
	// Implementation would depend on specific authConfig provided
	// This is a placeholder for future extension

	return nil
}
