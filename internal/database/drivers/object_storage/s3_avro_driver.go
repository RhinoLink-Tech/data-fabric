package object_storage

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// S3AvroDriver implements Driver interface for querying Avro files on S3
type S3AvroDriver struct {
	s3Client   *S3Client
	avroReader *AvroReader
	config     *S3AvroDriverConfig
}

// S3AvroDriverConfig holds S3 Avro driver configuration
type S3AvroDriverConfig struct {
	S3Config  *S3Config
	BatchSize int
}

// NewS3AvroDriver creates a new S3 Avro driver
func NewS3AvroDriver(ctx context.Context, config *S3AvroDriverConfig) (*S3AvroDriver, error) {
	if config == nil || config.S3Config == nil {
		return nil, fmt.Errorf("S3 Avro configuration is required")
	}
	s3Client, err := NewS3Client(ctx, config.S3Config)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 client: %w", err)
	}

	avroConfig := &AvroReaderConfig{
		BatchSize: config.BatchSize,
	}

	avroReader := NewAvroReader(s3Client, avroConfig)

	return &S3AvroDriver{
		s3Client:   s3Client,
		avroReader: avroReader,
		config:     config,
	}, nil
}

// Open opens a connection (not applicable for S3 Avro)
func (d *S3AvroDriver) Open(dsn string) (*sql.DB, error) {
	return nil, fmt.Errorf("S3 Avro driver does not support standard database connections")
}

// ValidateDSN validates the connection string
func (d *S3AvroDriver) ValidateDSN(dsn string) error {
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
func (d *S3AvroDriver) GetDefaultPort() int {
	return 443
}

// BuildDSN builds a connection string from configuration
func (d *S3AvroDriver) BuildDSN(config *model.DataSourceConfig) string {
	return fmt.Sprintf("s3://%s/%s", config.Database, config.Host)
}

// GetDatabaseTypeName returns the database type name
func (d *S3AvroDriver) GetDatabaseTypeName() string {
	return "s3-avro"
}

// TestConnection tests if the connection is working
func (d *S3AvroDriver) TestConnection(db *sql.DB) error {
	return fmt.Errorf("use TestConnectionContext instead")
}

// TestConnectionContext tests the S3 connection
func (d *S3AvroDriver) TestConnectionContext(ctx context.Context) error {
	_, err := d.s3Client.ListObjects(ctx, "", 1)
	if err != nil {
		return fmt.Errorf("failed to list S3 objects: %w", err)
	}
	return nil
}

// GetDriverName returns the driver name
func (d *S3AvroDriver) GetDriverName() string {
	return "s3-avro"
}

// GetCategory returns the driver category
func (d *S3AvroDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

// GetCapabilities returns driver capabilities
func (d *S3AvroDriver) GetCapabilities() drivers.DriverCapabilities {
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
func (d *S3AvroDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// Query executes a query against Avro files
func (d *S3AvroDriver) Query(ctx context.Context, key string) (*S3AvroResult, error) {
	if d == nil || d.avroReader == nil {
		return nil, fmt.Errorf("S3 Avro driver is not initialized")
	}
	reader, err := d.avroReader.Read(ctx, key)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	schema, err := reader.GetSchema()
	if err != nil {
		return nil, err
	}
	return &S3AvroResult{Rows: rows, NumRows: int64(len(rows)), Schema: schema}, nil
}

// S3AvroResult represents query results
type S3AvroResult struct {
	Rows    []map[string]interface{}
	NumRows int64
	Schema  *AvroSchema
}

// GetSchema retrieves schema for an Avro file
func (d *S3AvroDriver) GetSchema(ctx context.Context, key string) (*AvroSchema, error) {
	reader, err := d.avroReader.Read(ctx, key)
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	return reader.GetSchema()
}

// ListAvroFiles lists Avro files in a prefix
func (d *S3AvroDriver) ListAvroFiles(ctx context.Context, prefix string) ([]S3Object, error) {
	return d.s3Client.ListFilesByExtension(ctx, prefix, ".avro")
}

// ApplyBatchPagination adds pagination to SQL query
func (d *S3AvroDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(sql, batchSize, offset)
}
