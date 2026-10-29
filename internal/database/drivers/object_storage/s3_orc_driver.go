package object_storage

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// S3ORCDriver implements Driver interface for querying ORC files on S3
type S3ORCDriver struct {
	s3Client  *S3Client
	orcReader *ORCReader
	config    *S3ORCDriverConfig
}

// S3ORCDriverConfig holds S3 ORC driver configuration
type S3ORCDriverConfig struct {
	S3Config       *S3Config
	BatchSize      int
	EnablePushdown bool
}

// NewS3ORCDriver creates a new S3 ORC driver
func NewS3ORCDriver(ctx context.Context, config *S3ORCDriverConfig) (*S3ORCDriver, error) {
	if config == nil || config.S3Config == nil {
		return nil, fmt.Errorf("S3 ORC configuration is required")
	}
	s3Client, err := NewS3Client(ctx, config.S3Config)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	orcConfig := &ORCReaderConfig{
		BatchSize:       config.BatchSize,
		EnablePredicate: config.EnablePushdown,
	}

	orcReader := NewORCReader(s3Client, orcConfig)

	return &S3ORCDriver{
		s3Client:  s3Client,
		orcReader: orcReader,
		config:    config,
	}, nil
}

// Open opens a connection (not applicable for S3 ORC)
func (d *S3ORCDriver) Open(dsn string) (*sql.DB, error) {
	return nil, fmt.Errorf("S3 ORC driver does not support standard database connections")
}

// ValidateDSN validates the connection string
func (d *S3ORCDriver) ValidateDSN(dsn string) error {
	bucket, _, err := ParseS3URI(dsn)
	if err != nil {
		return fmt.Errorf("invalid S3 URI: %w", err)
	}

	if bucket == "" {
		return fmt.Errorf("bucket is required in S3 URI")
	}

	return nil
}

// GetDefaultPort returns the default S3 port
func (d *S3ORCDriver) GetDefaultPort() int {
	return 443
}

// BuildDSN builds a connection string from configuration
func (d *S3ORCDriver) BuildDSN(config *model.DataSourceConfig) string {
	return fmt.Sprintf("s3://%s/%s", config.Database, config.Host)
}

// GetDatabaseTypeName returns the database type name
func (d *S3ORCDriver) GetDatabaseTypeName() string {
	return "s3-orc"
}

// TestConnection tests if the connection is working
func (d *S3ORCDriver) TestConnection(db *sql.DB) error {
	return fmt.Errorf("use TestConnectionContext instead")
}

// TestConnectionContext tests the S3 connection
func (d *S3ORCDriver) TestConnectionContext(ctx context.Context) error {
	_, err := d.s3Client.ListObjects(ctx, "", 1)
	if err != nil {
		return fmt.Errorf("failed to list S3 objects: %w", err)
	}
	return nil
}

// GetDriverName returns the driver name
func (d *S3ORCDriver) GetDriverName() string {
	return "s3-orc"
}

// GetCategory returns the driver category
func (d *S3ORCDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

// GetCapabilities returns driver capabilities
func (d *S3ORCDriver) GetCapabilities() drivers.DriverCapabilities {
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
func (d *S3ORCDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// QueryWithPredicate queries ORC file with predicate pushdown
func (d *S3ORCDriver) QueryWithPredicate(ctx context.Context, key string, predicates []ORCPredicate) ([]map[string]interface{}, error) {
	return d.orcReader.FilterORCFile(ctx, key, predicates)
}

// GetSchema retrieves schema for an ORC file
func (d *S3ORCDriver) GetSchema(ctx context.Context, key string) (*ORCSchema, error) {
	return d.orcReader.GetSchemaFromKey(ctx, key)
}

// ApplyBatchPagination adds pagination to SQL query
func (d *S3ORCDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(sql, batchSize, offset)
}

// S3ORCResult contains rows and schema from one configured ORC object.
type S3ORCResult struct {
	Rows   []map[string]interface{}
	Schema *ORCSchema
}

// Query reads one configured ORC object and returns its decoded rows and schema.
func (d *S3ORCDriver) Query(ctx context.Context, key string) (*S3ORCResult, error) {
	if d == nil || d.orcReader == nil {
		return nil, fmt.Errorf("S3 ORC driver is not initialized")
	}
	file, err := d.orcReader.Read(ctx, key)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	rows, err := file.ReadAll()
	if err != nil {
		return nil, err
	}
	schema, err := file.GetSchema()
	if err != nil {
		return nil, err
	}
	return &S3ORCResult{Rows: rows, Schema: schema}, nil
}
