package database

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/file_system"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/readonly"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

var (
	sensitiveConnectionParameter = regexp.MustCompile(`(?i)((?:password|passwd|token|secret|credential|access[_-]?key|api[_-]?key)=)[^&\s,;]+`)
	connectionURLPassword        = regexp.MustCompile(`(?i)(https?://[^/\s@]+:)[^@/\s]*@`)
	connectionAuthorization      = regexp.MustCompile(`(?i)(authorization:\s*(?:basic|bearer)\s+)[A-Za-z0-9._~+/-]+=*`)
	sensitiveConnectionKey       = regexp.MustCompile(`(?i)(password|passwd|token|secret|credential|access[_-]?key|api[_-]?key|authorization)`)
)

// redactConnectionError preserves useful connection context while removing configured secrets.
func redactConnectionError(err error, config *model.DataSourceConfig) string {
	if err == nil {
		return ""
	}

	message := err.Error()
	secrets := make([]string, 0)
	if config != nil {
		if config.Password != "" {
			secrets = append(secrets, config.Password)
			if config.Username != "" {
				secrets = append(secrets, base64.StdEncoding.EncodeToString([]byte(config.Username+":"+config.Password)))
			}
		}
		for key, value := range config.AdditionalProps {
			sensitive := model.IsSensitiveDataSourceProperty(key) || sensitiveConnectionKey.MatchString(key)
			collectConnectionSecrets(value, sensitive, &secrets)
		}
	}

	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	for _, secret := range secrets {
		for _, encoded := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret)} {
			if encoded != "" {
				message = strings.ReplaceAll(message, encoded, "[REDACTED]")
			}
		}
	}

	message = connectionURLPassword.ReplaceAllString(message, "$1[REDACTED]@")
	message = sensitiveConnectionParameter.ReplaceAllString(message, "$1[REDACTED]")
	message = connectionAuthorization.ReplaceAllString(message, "$1[REDACTED]")
	return message
}

func collectConnectionSecrets(value interface{}, force bool, secrets *[]string) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			sensitive := force || model.IsSensitiveDataSourceProperty(key) || sensitiveConnectionKey.MatchString(key)
			collectConnectionSecrets(child, sensitive, secrets)
		}
	case []interface{}:
		for _, child := range typed {
			collectConnectionSecrets(child, force, secrets)
		}
	case string:
		if force && typed != "" {
			*secrets = append(*secrets, typed)
		}
	default:
		if force && value != nil {
			secret := fmt.Sprint(value)
			if secret != "" {
				*secrets = append(*secrets, secret)
			}
		}
	}
}

// HealthChecker performs health checks on database connections
type HealthChecker struct {
	connPool *ConnectionPool
	registry *DriverRegistry
}

// NewHealthChecker creates a new HealthChecker instance
func NewHealthChecker(connPool *ConnectionPool) *HealthChecker {
	return &HealthChecker{
		connPool: connPool,
		registry: GetDriverRegistry(),
	}
}

// HealthCheckResult represents the result of a health check
type HealthCheckResult struct {
	DataSourceID string              `json:"dataSourceId"`
	DatabaseType string              `json:"databaseType"`
	Status       string              `json:"status"`
	Message      string              `json:"message,omitempty"`
	Latency      time.Duration       `json:"latency"`
	CheckedAt    time.Time           `json:"checkedAt"`
	ServerInfo   *DetectedServerInfo `json:"serverInfo,omitempty"`
	// ReadOnlyEnforced reports whether the enforced connection actually runs
	// read-only (#20); nil when the type has no read-only session mode or
	// enforcement is disabled.
	ReadOnlyEnforced *bool    `json:"readOnlyEnforced,omitempty"`
	Warnings         []string `json:"warnings,omitempty"`
}

// DatabaseHealthSummary represents a summary of database health
type DatabaseHealthSummary struct {
	Status               string                       `json:"status,omitempty"`
	TotalConnections     int                          `json:"totalConnections"`
	HealthyConnections   int                          `json:"healthyConnections"`
	UnhealthyConnections int                          `json:"unhealthyConnections"`
	Results              []HealthCheckResult          `json:"results"`
	SummaryByType        map[string]TypeHealthSummary `json:"summaryByType"`
	CheckedAt            time.Time                    `json:"checkedAt"`
}

// TypeHealthSummary represents health summary by database type
type TypeHealthSummary struct {
	Total     int `json:"total"`
	Healthy   int `json:"healthy"`
	Unhealthy int `json:"unhealthy"`
}

// CheckDataSourceHealth checks the health of a specific data source
func (hc *HealthChecker) CheckDataSourceHealth(ctx context.Context, dataSource *model.DataSource) (*HealthCheckResult, error) {
	startTime := time.Now()

	result := &HealthCheckResult{
		DataSourceID: dataSource.ID,
		DatabaseType: string(dataSource.Type),
		CheckedAt:    time.Now(),
	}

	// Get driver for the database type
	driver, err := hc.registry.GetDriver(dataSource.Type)
	if err != nil {
		result.Status = "error"
		result.Message = fmt.Sprintf("Driver not available: %s", redactConnectionError(err, &dataSource.Config))
		result.Latency = time.Since(startTime)
		return result, nil
	}
	if checker, ok := driver.(drivers.NativeAPIHealthChecker); ok {
		err = checker.CheckNativeAPIConnection(ctx, &dataSource.Config)
		result.Latency = time.Since(startTime)
		if err != nil {
			result.Status = "unhealthy"
			result.Message = fmt.Sprintf("Connection test failed: %s", redactConnectionError(err, &dataSource.Config))
			return result, nil
		}
		result.Status = "healthy"
		result.Message = "Connection successful"
		return result, nil
	}

	// Get connection from pool
	db, err := hc.connPool.GetConnection(ctx, dataSource)
	if err != nil {
		result.Status = "unhealthy"
		result.Message = fmt.Sprintf("Failed to get connection: %s", redactConnectionError(err, &dataSource.Config))
		result.Latency = time.Since(startTime)
		return result, nil
	}

	// Ping with the caller's deadline so health checks cannot block on a slow source.
	err = db.PingContext(ctx)
	result.Latency = time.Since(startTime)

	if err != nil {
		result.Status = "unhealthy"
		result.Message = fmt.Sprintf("Connection test failed: %s", redactConnectionError(err, &dataSource.Config))
	} else {
		result.Status = "healthy"
		result.Message = "Connection successful"
	}

	return result, nil
}

// GetConnectionPoolStats returns live statistics for currently initialized pools.
func (hc *HealthChecker) GetConnectionPoolStats() map[string]ConnectionStats {
	if hc == nil || hc.connPool == nil {
		return make(map[string]ConnectionStats)
	}
	return hc.connPool.GetStats()
}

// CheckAllConnectionsHealth checks health of all active connections in the pool
func (hc *HealthChecker) CheckAllConnectionsHealth(ctx context.Context) (*DatabaseHealthSummary, error) {
	stats := hc.GetConnectionPoolStats()

	summary := &DatabaseHealthSummary{
		TotalConnections:     len(stats),
		HealthyConnections:   0,
		UnhealthyConnections: 0,
		Results:              make([]HealthCheckResult, 0),
		SummaryByType:        make(map[string]TypeHealthSummary),
		CheckedAt:            time.Now(),
	}

	// Check each connection
	for dataSourceID, connStats := range stats {
		result := HealthCheckResult{
			DataSourceID: dataSourceID,
			Status:       "unknown",
			Latency:      0,
			CheckedAt:    time.Now(),
		}

		if connStats.Healthy {
			result.Status = "healthy"
			result.Message = "Connection is healthy"
			summary.HealthyConnections++
		} else {
			result.Status = "unhealthy"
			result.Message = "Connection is unhealthy"
			summary.UnhealthyConnections++
		}

		summary.Results = append(summary.Results, result)
	}

	summary.Status = healthSummaryStatus(summary.TotalConnections, summary.HealthyConnections, summary.UnhealthyConnections)
	return summary, nil
}

func healthSummaryStatus(total, healthy, unhealthy int) string {
	if total == 0 {
		return "unknown"
	}
	if unhealthy == 0 {
		return "healthy"
	}
	if healthy == 0 {
		return "unhealthy"
	}
	return "degraded"
}

// CheckDataSourceConnectivity tests connectivity to a data source without using pool
func (hc *HealthChecker) CheckDataSourceConnectivity(ctx context.Context, config *model.DataSourceConfig, dbType model.DatabaseType) (*HealthCheckResult, error) {
	startTime := time.Now()

	result := &HealthCheckResult{
		DatabaseType: string(dbType),
		CheckedAt:    time.Now(),
	}

	// Get driver for the database type
	driver, err := hc.registry.GetDriver(dbType)
	if err != nil {
		result.Status = "error"
		result.Message = fmt.Sprintf("Driver not available: %s", redactConnectionError(err, config))
		result.Latency = time.Since(startTime)
		return result, nil
	}

	// Build DSN
	dsn := driver.BuildDSN(config)

	// Validate DSN
	if err := driver.ValidateDSN(dsn); err != nil {
		result.Status = "error"
		result.Message = fmt.Sprintf("Invalid connection string: %s", redactConnectionError(err, config))
		result.Latency = time.Since(startTime)
		return result, nil
	}

	if checker, ok := driver.(drivers.NativeAPIHealthChecker); ok {
		err := checker.CheckNativeAPIConnection(ctx, config)
		result.Latency = time.Since(startTime)
		if err != nil {
			result.Status = "unhealthy"
			result.Message = fmt.Sprintf("Connection test failed: %s", redactConnectionError(err, config))
			return result, nil
		}
		result.Status = "healthy"
		result.Message = "Connection successful"
		return result, nil
	}

	// Config-aware sources can construct a database/sql adapter without putting
	// object-store credentials and options into the DSN.
	configured := false
	var db *sql.DB
	if opener, ok := driver.(interface {
		OpenWithConfig(context.Context, *model.DataSourceConfig) (*sql.DB, error)
	}); ok {
		configured = true
		db, err = opener.OpenWithConfig(ctx, config)
	} else if opener, ok := driver.(drivers.SQLDatabaseOpener); ok {
		db, err = opener.Open(dsn)
	} else {
		err = fmt.Errorf("driver does not provide a SQL connection or native health check")
	}
	if err != nil {
		result.Status = "unhealthy"
		result.Message = fmt.Sprintf("Failed to open connection: %s", redactConnectionError(err, config))
		result.Latency = time.Since(startTime)
		return result, nil
	}
	defer db.Close()

	// Config-aware adapters validate the configured endpoint through PingContext.
	// Legacy drivers keep their existing connectivity probe.
	if configured {
		err = db.PingContext(ctx)
	} else if checker, ok := driver.(drivers.SQLConnectionTester); ok {
		err = checker.TestConnection(db)
	} else {
		err = fmt.Errorf("driver does not provide a SQL connection tester")
	}
	result.Latency = time.Since(startTime)

	if err != nil {
		result.Status = "unhealthy"
		result.Message = fmt.Sprintf("Connection test failed: %s", redactConnectionError(err, config))
		return result, nil
	}

	result.Status = "healthy"
	result.Message = "Connection successful"
	result.ServerInfo = detectServerInfo(ctx, db, dbType, driver.GetCategory())
	hc.verifyReadOnlySession(ctx, result, driver, dsn, config, dbType)
	return result, nil
}

// verifyReadOnlySession probes the enforced connection so a healthy check
// reports whether sessions actually run read-only (#20). The raw
// connectivity test above already passed, so an enforcement failure is
// reported as readOnlyEnforced=false plus a warning instead of an unhealthy
// status; the distinction matters when an engine rejects the read-only init.
func (hc *HealthChecker) verifyReadOnlySession(ctx context.Context, result *HealthCheckResult, driver drivers.Driver, dsn string, config *model.DataSourceConfig, dbType model.DatabaseType) {
	if !readonly.SupportsReadOnlySession(dbType) || !hc.connPool.readOnlyEnforced(config) {
		return
	}

	opener, ok := driver.(drivers.SQLDatabaseOpener)
	if !ok {
		return
	}
	enforced, warnings, err := readonly.VerifySession(ctx, dbType, opener.Open, dsn)
	result.Warnings = append(result.Warnings, warnings...)
	if err != nil {
		result.ReadOnlyEnforced = new(bool)
		result.Warnings = append(result.Warnings, "read-only session verification failed (queries stay blocked by SQL validation): "+redactConnectionError(err, config))
		return
	}
	result.ReadOnlyEnforced = &enforced
	if !enforced {
		result.Warnings = append(result.Warnings, "session read-only flag was not set after enforcement; treat this data source as not read-only")
	}
}

// PeriodicHealthCheck performs periodic health checks
func (hc *HealthChecker) PeriodicHealthCheck(ctx context.Context, interval time.Duration) <-chan *DatabaseHealthSummary {
	results := make(chan *DatabaseHealthSummary)

	go func() {
		defer close(results)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				summary, err := hc.CheckAllConnectionsHealth(ctx)
				if err != nil {
					// Create error summary
					summary = &DatabaseHealthSummary{
						CheckedAt: time.Now(),
						Results: []HealthCheckResult{
							{
								Status:    "error",
								Message:   fmt.Sprintf("Health check failed: %v", err),
								CheckedAt: time.Now(),
							},
						},
					}
				}
				select {
				case results <- summary:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return results
}

// ValidateDataSourceConfiguration validates a data source configuration
func (hc *HealthChecker) ValidateDataSourceConfiguration(config *model.DataSourceConfig, dbType model.DatabaseType) error {
	if config == nil {
		return fmt.Errorf("data source config is required")
	}
	if !hc.IsSupportedDataSourceType(dbType) {
		return fmt.Errorf("unsupported database type: %s", dbType)
	}

	// Get driver for the database type
	driver, err := hc.registry.GetDriver(dbType)
	if err != nil {
		return fmt.Errorf("driver not available for database type %s: %w", dbType, err)
	}

	// Get category to determine validation rules
	category := driver.GetCategory()

	// Validate based on category
	switch category {
	case drivers.CategoryObjectStorage:
		if driver.GetDefaultPort() > 0 {
			if err := applyDriverDefaultPort(config, driver); err != nil {
				return err
			}
		}
		if dbType == model.DatabaseTypeOzoneParquet || dbType == model.DatabaseTypeOzoneORC || dbType == model.DatabaseTypeOzoneText || dbType == model.DatabaseTypeOzoneAvro || dbType == model.DatabaseTypeOzoneJSON || dbType == model.DatabaseTypeOzoneCSV {
			switch dbType {
			case model.DatabaseTypeOzoneText:
				return file_system.ValidateOzoneTextConfig(config)
			case model.DatabaseTypeOzoneAvro:
				return file_system.ValidateOzoneAvroConfig(config)
			case model.DatabaseTypeOzoneJSON:
				return file_system.ValidateOzoneJSONConfig(config)
			case model.DatabaseTypeOzoneCSV:
				return file_system.ValidateOzoneCSVConfig(config)
			case model.DatabaseTypeOzoneORC:
				return file_system.ValidateOzoneORCConfig(config)
			default:
				return file_system.ValidateOzoneParquetConfig(config)
			}
		}
		if dbType == model.DatabaseTypeS3CSV || dbType == model.DatabaseTypeMinIOCSV {
			return hc.validateS3CSVConfig(config)
		}
		if dbType == model.DatabaseTypeS3Avro {
			return hc.validateS3AvroConfig(config)
		}
		if dbType == model.DatabaseTypeMinIOAvro {
			return object_storage.ValidateMinIOAvroConfig(config)
		}
		if dbType == model.DatabaseTypeS3JSON {
			return hc.validateS3JSONConfig(config)
		}
		if dbType == model.DatabaseTypeMinIOJSON {
			return object_storage.ValidateMinIOJSONConfig(config)
		}
		if dbType == model.DatabaseTypeS3Parquet {
			return hc.validateS3ParquetConfig(config)
		}
		if dbType == model.DatabaseTypeS3ORC {
			return hc.validateS3ORCConfig(config)
		}
		if dbType == model.DatabaseTypeS3Delta {
			return object_storage.ValidateS3DeltaConfig(config)
		}
		if dbType == model.DatabaseTypeMinIOORC {
			return object_storage.ValidateMinIOORCConfig(config)
		}
		if dbType == model.DatabaseTypeMinIOParquet {
			return hc.validateMinIOParquetConfig(config)
		}
		if dbType == model.DatabaseTypeMinIOIceberg {
			return hc.validateMinIOIcebergConfig(config)
		}
		if dbType == model.DatabaseTypeAlibabaOSSParquet {
			return hc.validateOSSParquetConfig(config)
		}
		if dbType == model.DatabaseTypeOSSCSV {
			return object_storage.ValidateOSSCSVConfig(config)
		}
		if dbType == model.DatabaseTypeAlibabaOSSJSON {
			return object_storage.ValidateOSSJSONConfig(config)
		}
		if dbType == model.DatabaseTypeAlibabaOSSAvro {
			return object_storage.ValidateOSSAvroConfig(config)
		}
		if dbType == model.DatabaseTypeAlibabaOSSORC {
			return object_storage.ValidateOSSORCConfig(config)
		}
		if dbType == model.DatabaseTypeOSSDelta {
			return hc.validateOSSDeltaConfig(config)
		}
		if dbType == model.DatabaseTypeMinIODelta {
			return hc.validateMinIODeltaConfig(config)
		}
		if dbType == model.DatabaseTypeAzureDelta {
			return hc.validateAzureDeltaConfig(config)
		}
		if dbType == model.DatabaseTypeTencentCOSParquet {
			return hc.validateCOSParquetConfig(config)
		}
		if dbType == model.DatabaseTypeTencentCOSCSV {
			return object_storage.ValidateCOSCSVConfig(config)
		}
		if dbType == model.DatabaseTypeTencentCOSJSON {
			return object_storage.ValidateCOSJSONConfig(config)
		}
		if dbType == model.DatabaseTypeTencentCOSORC {
			return object_storage.ValidateCOSORCConfig(config)
		}
		if dbType == model.DatabaseTypeTencentCOSAvro {
			return object_storage.ValidateCOSAvroConfig(config)
		}
		if dbType == model.DatabaseTypeAzureBlobORC {
			return object_storage.ValidateAzureBlobORCConfig(config)
		}
		if dbType == model.DatabaseTypeAzureBlobAvro {
			return object_storage.ValidateAzureBlobAvroConfig(config)
		}
		if dbType == model.DatabaseTypeAzureBlobCSV {
			return object_storage.ValidateAzureBlobCSVConfig(config)
		}
		if dbType == model.DatabaseTypeAzureBlobJSON {
			return object_storage.ValidateAzureBlobJSONConfig(config)
		}
		if dbType == model.DatabaseTypeAzureBlobParquet || dbType == model.DatabaseTypeAzureParquet {
			return hc.validateAzureParquetConfig(config)
		}
		// Object storage (S3, etc.) requires bucket and region
		return hc.validateObjectStorageConfig(config, driver)
	case drivers.CategoryFileSystem:
		if dbType == model.DatabaseTypeHDFSJSON {
			if err := applyDriverDefaultPort(config, driver); err != nil {
				return err
			}
			return file_system.ValidateHDFSJSONConfig(config)
		}
		if dbType == model.DatabaseTypeHDFSText {
			if err := applyDriverDefaultPort(config, driver); err != nil {
				return err
			}
			return file_system.ValidateHDFSTextConfig(config)
		}
		if dbType == model.DatabaseTypeHDFSCSV {
			if err := applyDriverDefaultPort(config, driver); err != nil {
				return err
			}
			return file_system.ValidateHDFSCSVConfig(config)
		}
		if dbType == model.DatabaseTypeHDFSParquet || dbType == model.DatabaseTypeHDFSParquetCompressed {
			if err := applyDriverDefaultPort(config, driver); err != nil {
				return err
			}
			return file_system.ValidateHDFSParquetConfig(config)
		}
		if dbType == model.DatabaseTypeHDFSAvro || dbType == model.DatabaseTypeHDFSORC {
			if err := applyDriverDefaultPort(config, driver); err != nil {
				return err
			}
			if dbType == model.DatabaseTypeHDFSORC {
				return file_system.ValidateHDFSORCConfig(config)
			}
			return file_system.ValidateHDFSAvroConfig(config)
		}
		// File systems (HDFS/Ozone) use the registered driver's port defaults.
		return hc.validateFileSystemConfig(config, driver)
	case drivers.CategoryTableFormat:
		return hc.validateTableFormatConfig(config, driver)
	case drivers.CategoryNoSQL:
		return hc.validateNoSQLConfig(config, driver)
	default:
		// Standard databases require host, port, database, username
		return hc.validateDatabaseConfig(config, driver)
	}
}

// IsSupportedDataSourceType reports whether the active driver registry can
// create a driver for the requested database type. API handlers and services
// use this instead of maintaining independent allowlists.
func (hc *HealthChecker) IsSupportedDataSourceType(dbType model.DatabaseType) bool {
	return hc != nil && hc.registry != nil && hc.registry.IsSupported(dbType)
}

// validateDatabaseConfig validates standard database configurations
func (hc *HealthChecker) validateDatabaseConfig(config *model.DataSourceConfig, driver drivers.Driver) error {
	// Check required fields
	if strings.TrimSpace(config.Host) == "" {
		return fmt.Errorf("host is required")
	}
	if err := applyDriverDefaultPort(config, driver); err != nil {
		return err
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("database name is required")
	}
	if strings.TrimSpace(config.Username) == "" {
		return fmt.Errorf("username is required")
	}

	// Build and validate DSN
	dsn := driver.BuildDSN(config)
	return driver.ValidateDSN(dsn)
}

func applyDriverDefaultPort(config *model.DataSourceConfig, driver drivers.Driver) error {
	if config.Port < 0 || config.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if config.Port == 0 {
		defaultPort := driver.GetDefaultPort()
		if defaultPort <= 0 || defaultPort > 65535 {
			return fmt.Errorf("port is required because the driver has no valid default")
		}
		config.Port = defaultPort
	}
	return nil
}

func (hc *HealthChecker) validateTableFormatConfig(config *model.DataSourceConfig, driver drivers.Driver) error {
	if strings.TrimSpace(config.Host) == "" {
		return fmt.Errorf("catalog or storage endpoint is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("warehouse or table namespace is required")
	}
	if err := applyDriverDefaultPort(config, driver); err != nil {
		return err
	}
	return driver.ValidateDSN(driver.BuildDSN(config))
}

func (hc *HealthChecker) validateNoSQLConfig(config *model.DataSourceConfig, driver drivers.Driver) error {
	if strings.TrimSpace(config.Host) == "" {
		return fmt.Errorf("host is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("database name is required")
	}
	if err := applyDriverDefaultPort(config, driver); err != nil {
		return err
	}
	if config.Password != "" && strings.TrimSpace(config.Username) == "" {
		return fmt.Errorf("username is required when a password is configured")
	}
	return driver.ValidateDSN(driver.BuildDSN(config))
}

// validateObjectStorageConfig validates object storage configurations
func (hc *HealthChecker) validateObjectStorageConfig(config *model.DataSourceConfig, driver drivers.Driver) error {
	// Object storage requires bucket name and region
	if strings.TrimSpace(config.Database) == "" { // Use Database field for bucket name
		return fmt.Errorf("bucket name is required")
	}

	// Region is required for most object storage providers
	// (stored in Host field for non-SQL sources)
	if strings.TrimSpace(config.Host) == "" {
		return fmt.Errorf("region is required")
	}
	if err := applyDriverDefaultPort(config, driver); err != nil {
		return err
	}

	return driver.ValidateDSN(driver.BuildDSN(config))
}

func (hc *HealthChecker) validateS3CSVConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return fmt.Errorf("data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("bucket name is required in database")
	}
	objectKey := ""
	if value, ok := config.AdditionalProps["object_key"]; ok && value != nil {
		objectKey = strings.TrimSpace(fmt.Sprint(value))
	}
	if objectKey == "" {
		objectKey = strings.TrimSpace(config.Host)
	}
	if objectKey == "" {
		return fmt.Errorf("object_key is required in additionalProps or host")
	}
	return nil
}

func (hc *HealthChecker) validateMinIOParquetConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return fmt.Errorf("data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("bucket name is required in database")
	}
	if _, err := object_storage.MinIOParquetTableName(config); err != nil {
		return err
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return fmt.Errorf("MinIO access key and secret key are required")
	}
	endpoint, _ := config.AdditionalProps["endpoint_url"].(string)
	if strings.TrimSpace(endpoint) == "" {
		return fmt.Errorf("endpoint_url is required in additionalProps")
	}
	return nil
}

func dataSourcePropString(props map[string]interface{}, key string) string {
	if props == nil || props[key] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(props[key]))
}

func (hc *HealthChecker) validateOSSParquetConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return fmt.Errorf("data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("bucket name is required in database")
	}
	objectKey := dataSourcePropString(config.AdditionalProps, "object_key")
	if strings.TrimSpace(objectKey) == "" {
		objectKey = config.Host
	}
	if strings.TrimSpace(objectKey) == "" {
		return fmt.Errorf("object_key is required in additionalProps or host")
	}
	if _, err := object_storage.OSSParquetTableName(config); err != nil {
		return err
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return fmt.Errorf("OSS access key ID and secret are required")
	}
	endpoint := dataSourcePropString(config.AdditionalProps, "endpoint_url")
	if endpoint == "" {
		endpoint = dataSourcePropString(config.AdditionalProps, "endpoint")
	}
	if strings.TrimSpace(endpoint) == "" {
		return fmt.Errorf("endpoint_url is required in additionalProps")
	}
	return nil
}

func (hc *HealthChecker) validateOSSDeltaConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return fmt.Errorf("data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("bucket name is required in database")
	}
	tablePath := dataSourcePropString(config.AdditionalProps, "table_path")
	if tablePath == "" {
		tablePath = config.Host
	}
	if strings.Trim(strings.TrimSpace(tablePath), "/") == "" {
		return fmt.Errorf("table_path is required in additionalProps or host")
	}
	if _, err := object_storage.OSSDeltaTableName(config); err != nil {
		return err
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return fmt.Errorf("OSS access key ID and secret are required")
	}
	if _, err := object_storage.OSSDeltaEndpointFromConfig(config); err != nil {
		return err
	}
	return nil
}

func (hc *HealthChecker) validateMinIODeltaConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return fmt.Errorf("data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("bucket name is required in database")
	}
	tablePath := dataSourcePropString(config.AdditionalProps, "table_path")
	if tablePath == "" {
		tablePath = config.Host
	}
	if strings.Trim(strings.TrimSpace(tablePath), "/") == "" {
		return fmt.Errorf("table_path is required in additionalProps or host")
	}
	if _, err := object_storage.MinIODeltaTableName(config); err != nil {
		return err
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return fmt.Errorf("MinIO access key and secret key are required")
	}
	if _, _, err := object_storage.MinIODeltaEndpointFromConfig(config); err != nil {
		return err
	}
	return nil
}

func (hc *HealthChecker) validateMinIOIcebergConfig(config *model.DataSourceConfig) error {
	return object_storage.ValidateMinIOIcebergConfig(config)
}

func (hc *HealthChecker) validateAzureDeltaConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return fmt.Errorf("data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("container name is required in database")
	}
	tablePath := dataSourcePropString(config.AdditionalProps, "table_path")
	if tablePath == "" {
		tablePath = config.Host
	}
	if strings.Trim(strings.TrimSpace(tablePath), "/") == "" {
		return fmt.Errorf("table_path is required in additionalProps or host")
	}
	if _, err := object_storage.AzureDeltaTableName(config); err != nil {
		return err
	}
	if strings.TrimSpace(config.Username) == "" {
		return fmt.Errorf("Azure Blob account name is required in username")
	}
	if strings.TrimSpace(config.Password) == "" && strings.TrimSpace(dataSourcePropString(config.AdditionalProps, "sas_token")) == "" {
		return fmt.Errorf("Azure Blob account key or sas_token is required")
	}
	if _, err := object_storage.AzureDeltaEndpointFromConfig(config); err != nil {
		return err
	}
	return nil
}

func (hc *HealthChecker) validateCOSParquetConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return fmt.Errorf("data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("bucket name is required in database")
	}
	objectKey := dataSourcePropString(config.AdditionalProps, "object_key")
	if objectKey == "" {
		objectKey = strings.TrimSpace(config.Host)
	}
	if objectKey == "" {
		return fmt.Errorf("object_key is required in additionalProps or host")
	}
	if _, err := object_storage.COSParquetTableName(config); err != nil {
		return err
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return fmt.Errorf("COS SecretId and SecretKey are required")
	}
	if dataSourcePropString(config.AdditionalProps, "region") == "" {
		return fmt.Errorf("region is required in additionalProps")
	}
	endpoint := dataSourcePropString(config.AdditionalProps, "endpoint_url")
	if endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			(parsed.Path != "" && parsed.Path != "/") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
		}
	}
	return nil
}

func (hc *HealthChecker) validateAzureParquetConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return fmt.Errorf("data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("container name is required in database")
	}
	objectKey := dataSourcePropString(config.AdditionalProps, "object_key")
	if objectKey == "" {
		objectKey = strings.TrimSpace(config.Host)
	}
	if objectKey == "" {
		return fmt.Errorf("object_key is required in additionalProps or host")
	}
	if _, err := object_storage.AzureBlobParquetTableName(config); err != nil {
		return err
	}
	if strings.TrimSpace(config.Username) == "" {
		return fmt.Errorf("Azure Blob account name is required in username")
	}
	if strings.TrimSpace(config.Password) == "" && dataSourcePropString(config.AdditionalProps, "sas_token") == "" {
		return fmt.Errorf("Azure Blob account key or sas_token is required")
	}
	endpoint := dataSourcePropString(config.AdditionalProps, "endpoint_url")
	if endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("endpoint_url must be an HTTP(S) URL without credentials, query, or fragment")
		}
	}
	return nil
}

func (hc *HealthChecker) validateS3JSONConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return fmt.Errorf("data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("bucket name is required in database")
	}
	objectKey := dataSourcePropString(config.AdditionalProps, "object_key")
	if strings.TrimSpace(objectKey) == "" {
		objectKey = config.Host
	}
	if strings.TrimSpace(objectKey) == "" {
		return fmt.Errorf("object_key is required in additionalProps or host")
	}
	if _, err := object_storage.S3JSONTableName(config); err != nil {
		return err
	}
	if (strings.TrimSpace(config.Username) == "") != (strings.TrimSpace(config.Password) == "") {
		return fmt.Errorf("S3 access key and secret key must be configured together")
	}
	return nil
}

func (hc *HealthChecker) validateS3AvroConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return fmt.Errorf("data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("bucket name is required in database")
	}
	objectKey := dataSourcePropString(config.AdditionalProps, "object_key")
	if strings.TrimSpace(objectKey) == "" {
		objectKey = config.Host
	}
	if strings.TrimSpace(objectKey) == "" {
		return fmt.Errorf("object_key is required in additionalProps or host")
	}
	if _, err := object_storage.S3AvroTableName(config); err != nil {
		return err
	}
	if (strings.TrimSpace(config.Username) == "") != (strings.TrimSpace(config.Password) == "") {
		return fmt.Errorf("S3 access key and secret key must be configured together")
	}
	return nil
}

func (hc *HealthChecker) validateS3ParquetConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return fmt.Errorf("data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("bucket name is required in database")
	}
	objectKey, _ := config.AdditionalProps["object_key"].(string)
	if strings.TrimSpace(objectKey) == "" {
		objectKey = config.Host
	}
	if strings.TrimSpace(objectKey) == "" {
		return fmt.Errorf("object_key is required in additionalProps or host")
	}
	if _, err := object_storage.S3ParquetTableName(config); err != nil {
		return err
	}
	if (strings.TrimSpace(config.Username) == "") != (strings.TrimSpace(config.Password) == "") {
		return fmt.Errorf("S3 access key and secret key must be configured together")
	}
	return nil
}

// validateS3ORCConfig validates the bucket and object identity before opening
// a read-only ORC source.
func (hc *HealthChecker) validateS3ORCConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return fmt.Errorf("data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return fmt.Errorf("bucket name is required in database")
	}
	objectKey, _ := config.AdditionalProps["object_key"].(string)
	if strings.TrimSpace(objectKey) == "" {
		objectKey = config.Host
	}
	if strings.TrimSpace(objectKey) == "" {
		return fmt.Errorf("object_key is required in additionalProps or host")
	}
	if _, err := object_storage.S3ORCTableName(config); err != nil {
		return err
	}
	if (strings.TrimSpace(config.Username) == "") != (strings.TrimSpace(config.Password) == "") {
		return fmt.Errorf("S3 access key and secret key must be configured together")
	}
	return nil
}

// validateFileSystemConfig validates file system configurations.
func (hc *HealthChecker) validateFileSystemConfig(config *model.DataSourceConfig, driver drivers.Driver) error {
	// HDFS/Ozone require namenode/ozonename address
	if strings.TrimSpace(config.Host) == "" {
		return fmt.Errorf("namenode address is required")
	}
	if err := applyDriverDefaultPort(config, driver); err != nil {
		return err
	}

	// Path is optional (uses root if not specified)
	if config.Database == "" {
		config.Database = "/" // Default to root
	}

	return nil
}

// GetDriverInfo returns information about available database drivers
func (hc *HealthChecker) GetDriverInfo() map[string]DriverInfo {
	info := make(map[string]DriverInfo)
	for _, profile := range hc.GetSupportCatalog() {
		if !profile.Supported {
			continue
		}
		info[profile.Type] = DriverInfo{ConnectorSupport: profile}
	}
	return info
}

// GetSupportCatalog returns the same support profiles used by both database APIs.
func (hc *HealthChecker) GetSupportCatalog() []ConnectorSupport {
	if hc == nil || hc.registry == nil {
		return nil
	}
	return SupportCatalog(hc.registry)
}
