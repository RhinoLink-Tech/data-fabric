package olap

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// DruidDriver implements Driver interface for Apache Druid.
type DruidDriver struct {
	config     *DruidConfig
	restClient *DruidRESTClient
}

// DruidConfig holds Druid configuration
type DruidConfig struct {
	BrokerURL      string
	CoordinatorURL string
	OverlordURL    string
	Username       string
	Password       string
	Timeout        time.Duration
	UseSQL         bool // Use SQL API or native query
}

// NewDruidDriver creates a new Druid driver
func NewDruidDriver(config *DruidConfig) (*DruidDriver, error) {
	if config == nil || strings.TrimSpace(config.BrokerURL) == "" {
		return nil, fmt.Errorf("broker URL is required")
	}
	if err := validateDruidEndpoint(config.BrokerURL); err != nil {
		return nil, err
	}

	restClient := NewDruidRESTClient(config.BrokerURL)
	restClient.SetBasicAuth(config.Username, config.Password)

	return &DruidDriver{
		config:     config,
		restClient: restClient,
	}, nil
}

// Open opens a read-only database/sql adapter over the Druid SQL HTTP API.
// The shared query service is built around database/sql, so this makes the
// REST protocol available through the same connection-pool path as SQL drivers.
func (d *DruidDriver) Open(dsn string) (*sql.DB, error) {
	if err := d.ValidateDSN(dsn); err != nil {
		return nil, err
	}
	return sql.OpenDB(&druidConnector{client: NewDruidRESTClient(dsn)}), nil
}

// ValidateDSN validates the connection string
func (d *DruidDriver) ValidateDSN(dsn string) error {
	return validateDruidEndpoint(dsn)
}

// GetDefaultPort returns the default Druid port
func (d *DruidDriver) GetDefaultPort() int {
	return 8082 // Broker port
}

// BuildDSN builds a connection string from configuration
func (d *DruidDriver) BuildDSN(config *model.DataSourceConfig) string {
	endpoint, err := druidEndpointFromConfig(config)
	if err != nil {
		return ""
	}
	return endpoint
}

// GetDatabaseTypeName returns the database type name
func (d *DruidDriver) GetDatabaseTypeName() string {
	return "druid"
}

// TestConnection checks the SQL adapter's status probe.
func (d *DruidDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("Druid database handle is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}

// TestConnectionContext tests the Druid connection
func (d *DruidDriver) TestConnectionContext(ctx context.Context) error {
	if d.restClient == nil {
		return fmt.Errorf("Druid REST client is not configured")
	}
	status, err := d.restClient.GetClusterStatus(ctx)
	if err != nil {
		return fmt.Errorf("failed to get cluster status: %w", err)
	}

	if status.Version == "" {
		return fmt.Errorf("invalid cluster status")
	}

	return nil
}

// OpenWithConfig creates the configured REST-to-database/sql adapter used by
// the unified query service. Credentials stay outside the DSN and are used only
// for optional HTTP basic authentication.
func (d *DruidDriver) OpenWithConfig(_ context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	endpoint, err := druidEndpointFromConfig(config)
	if err != nil {
		return nil, err
	}
	client := NewDruidRESTClient(endpoint)
	client.SetBasicAuth(config.Username, config.Password)
	return sql.OpenDB(&druidConnector{client: client}), nil
}

// GetDriverName returns the driver name
func (d *DruidDriver) GetDriverName() string {
	return "druid-rest"
}

// GetCategory returns the driver category
func (d *DruidDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryOLAP
}

// GetCapabilities returns driver capabilities
func (d *DruidDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     false,
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       false,
	}
}

// ConfigureAuth configures authentication
func (d *DruidDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// ApplyBatchPagination applies pagination to SQL query for batch processing
func (d *DruidDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", sql, batchSize, offset), nil
}

// ExecuteSQL executes a SQL query via Druid SQL API
func (d *DruidDriver) ExecuteSQL(ctx context.Context, sql string) (*DruidQueryResult, error) {
	if d.restClient == nil {
		return nil, fmt.Errorf("Druid REST client is not configured")
	}
	return d.restClient.ExecuteSQLQuery(ctx, sql)
}

// GetDatasources retrieves list of datasources
func (d *DruidDriver) GetDatasources(ctx context.Context) ([]string, error) {
	if d.restClient == nil {
		return nil, fmt.Errorf("Druid REST client is not configured")
	}
	return d.restClient.GetDatasources(ctx)
}

// GetDatasourceMetadata retrieves metadata for a datasource
func (d *DruidDriver) GetDatasourceMetadata(ctx context.Context, datasource string) (*DruidDatasourceMetadata, error) {
	if d.restClient == nil {
		return nil, fmt.Errorf("Druid REST client is not configured")
	}
	return d.restClient.GetDatasourceMetadata(ctx, datasource)
}

// ExecuteTimeTravelQuery executes a time-travel query
func (d *DruidDriver) ExecuteTimeTravelQuery(ctx context.Context, sql string, timestamp string) (*DruidQueryResult, error) {
	if d.restClient == nil {
		return nil, fmt.Errorf("Druid REST client is not configured")
	}
	// Druid uses intervals for time travel
	timeTravelSQL := fmt.Sprintf("%s WHERE __time >= TIMESTAMP '%s' AND __time < TIMESTAMP '%s' + INTERVAL 1 DAY",
		sql, timestamp, timestamp)

	return d.restClient.ExecuteSQLQuery(ctx, timeTravelSQL)
}

// GetClusterInfo retrieves cluster information
func (d *DruidDriver) GetClusterInfo(ctx context.Context) (*DruidClusterInfo, error) {
	if d.restClient == nil || d.config == nil {
		return nil, fmt.Errorf("Druid REST client is not configured")
	}
	status, err := d.restClient.GetClusterStatus(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster status: %w", err)
	}

	info := &DruidClusterInfo{
		Version:        status.Version,
		BrokerURL:      d.config.BrokerURL,
		CoordinatorURL: d.config.CoordinatorURL,
		OverlordURL:    d.config.OverlordURL,
	}

	return info, nil
}

// DruidClusterInfo represents cluster information
type DruidClusterInfo struct {
	Version        string
	BrokerURL      string
	CoordinatorURL string
	OverlordURL    string
}

func druidEndpointFromConfig(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", fmt.Errorf("Druid data source config is required")
	}
	host := strings.TrimSpace(config.Host)
	if host == "" {
		return "", fmt.Errorf("Druid host is required")
	}
	port := config.Port
	if port == 0 {
		port = (&DruidDriver{}).GetDefaultPort()
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("Druid port must be between 1 and 65535")
	}

	scheme := "http"
	if config.SSL {
		scheme = "https"
	}
	if config.AdditionalProps != nil {
		if value, ok := config.AdditionalProps["scheme"]; ok && value != nil {
			configured := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
			if configured != "" {
				scheme = configured
			}
		}
	}
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("Druid scheme must be http or https")
	}

	if strings.Contains(host, "://") {
		endpoint, err := url.Parse(host)
		if err != nil {
			return "", fmt.Errorf("invalid Druid host URL: %w", err)
		}
		if endpoint.Port() == "" {
			endpoint.Host = net.JoinHostPort(endpoint.Hostname(), strconv.Itoa(port))
		}
		if err := validateDruidEndpoint(endpoint.String()); err != nil {
			return "", err
		}
		return strings.TrimRight(endpoint.String(), "/"), nil
	}
	if hostname, configuredPort, err := net.SplitHostPort(host); err == nil {
		parsedPort, parseErr := strconv.Atoi(configuredPort)
		if parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
			return "", fmt.Errorf("Druid router port must be between 1 and 65535")
		}
		if config.Port != 0 && config.Port != parsedPort {
			return "", fmt.Errorf("Druid host port %d conflicts with configured port %d", parsedPort, config.Port)
		}
		endpoint := (&url.URL{Scheme: scheme, Host: net.JoinHostPort(hostname, configuredPort)}).String()
		if err := validateDruidEndpoint(endpoint); err != nil {
			return "", err
		}
		return endpoint, nil
	}
	if strings.ContainsAny(host, "/?#") {
		return "", fmt.Errorf("Druid host must not include a path, query, or fragment")
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}

	endpoint := (&url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(port))}).String()
	if err := validateDruidEndpoint(endpoint); err != nil {
		return "", err
	}
	return endpoint, nil
}

func validateDruidEndpoint(dsn string) error {
	endpoint, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil {
		return fmt.Errorf("invalid Druid connection URL: %w", err)
	}
	if endpoint.Scheme != "http" && endpoint.Scheme != "https" {
		return fmt.Errorf("Druid connection URL scheme must be http or https")
	}
	if endpoint.Hostname() == "" {
		return fmt.Errorf("Druid router host is required")
	}
	if endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Opaque != "" {
		return fmt.Errorf("Druid connection URL must contain only scheme, host, optional port, and base path")
	}
	if port := endpoint.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return fmt.Errorf("Druid router port must be between 1 and 65535")
		}
	}
	return nil
}
