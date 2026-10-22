package object_storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/table_formats"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// S3HudiDriver reads Hudi table metadata from standard S3 table files. It does
// not execute row queries; callers need a configured Hudi query engine for that.
type S3HudiDriver struct {
	config         *S3Config
	client         *S3Client
	metadataReader *S3HudiMetadataReader
}

// NewS3HudiDriver creates an S3-backed Hudi metadata driver.
func NewS3HudiDriver(ctx context.Context, config *S3Config) (*S3HudiDriver, error) {
	if config == nil {
		return nil, fmt.Errorf("S3 configuration is required")
	}
	client, err := NewS3Client(ctx, config)
	if err != nil {
		return nil, err
	}
	return &S3HudiDriver{
		config:         config,
		client:         client,
		metadataReader: NewS3HudiMetadataReaderWithClient(client, config.Bucket),
	}, nil
}

func (d *S3HudiDriver) Open(string) (*sql.DB, error) {
	return nil, fmt.Errorf("S3 Hudi does not provide a standard database connection; configure a Hudi-capable query engine for row queries")
}

func (d *S3HudiDriver) ValidateDSN(dsn string) error {
	if strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("DSN cannot be empty")
	}
	return nil
}

func (d *S3HudiDriver) GetDefaultPort() int { return 443 }

func (d *S3HudiDriver) BuildDSN(config *model.DataSourceConfig) string {
	return fmt.Sprintf("s3://%s", config.Database)
}

func (d *S3HudiDriver) GetDatabaseTypeName() string { return "s3-hudi" }

func (d *S3HudiDriver) TestConnection(*sql.DB) error {
	return fmt.Errorf("use TestConnectionContext instead for S3 Hudi")
}

func (d *S3HudiDriver) TestConnectionContext(ctx context.Context) error {
	if d.metadataReader == nil {
		return fmt.Errorf("S3 Hudi metadata reader is not configured")
	}
	return d.metadataReader.TestConnection(ctx)
}

func (d *S3HudiDriver) GetDriverName() string { return "s3-hudi" }

func (d *S3HudiDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

func (d *S3HudiDriver) GetCapabilities() drivers.DriverCapabilities {
	metadataAvailable := d.metadataReader != nil
	return drivers.DriverCapabilities{
		SupportsSQL:               false,
		SupportsTransaction:       false,
		SupportsSchemaDiscovery:   metadataAvailable,
		SupportsTimeTravel:        false,
		RequiresTokenRotation:     false,
		SupportsStreaming:         false,
		SupportsSnapshotDiscovery: metadataAvailable,
		SupportsIncrementalRead:   false,
	}
}

func (d *S3HudiDriver) ConfigureAuth(interface{}) error { return nil }

// GetTableMetadata retrieves Hudi metadata from hoodie.properties.
func (d *S3HudiDriver) GetTableMetadata(ctx context.Context, tablePath string) (*HudiMetadata, error) {
	if d.metadataReader == nil {
		return nil, fmt.Errorf("S3 Hudi metadata reader is not configured")
	}
	metadata, err := d.metadataReader.GetTableMetadata(ctx, tablePath)
	if err != nil {
		return nil, err
	}
	location := strings.TrimRight(tablePath, "/")
	if !strings.HasPrefix(location, "s3://") {
		location = "s3://" + d.config.Bucket + "/" + strings.TrimLeft(location, "/")
	}
	return &HudiMetadata{
		TableLocation: location,
		Format:        "hudi",
		TableType:     metadata.TableType,
		RecordKey:     metadata.KeyField,
		PartitionPath: strings.Join(metadata.PartitionFields, ","),
	}, nil
}

// QueryTable explicitly reports that this storage adapter cannot execute SQL.
func (d *S3HudiDriver) QueryTable(context.Context, string, string) (*HudiQueryResult, error) {
	return nil, fmt.Errorf("S3 Hudi metadata adapter does not execute row queries; configure a Hudi-capable query engine")
}

// ListPartitions returns partition paths and file statistics from S3 objects.
func (d *S3HudiDriver) ListPartitions(ctx context.Context, tablePath string) ([]table_formats.HudiPartition, error) {
	if d.metadataReader == nil {
		return nil, fmt.Errorf("S3 Hudi metadata reader is not configured")
	}
	return d.metadataReader.ListPartitions(ctx, tablePath)
}

// GetCommits returns completed entries from the classic active Hudi timeline.
func (d *S3HudiDriver) GetCommits(ctx context.Context, tablePath string) ([]table_formats.HudiCommit, error) {
	if d.metadataReader == nil {
		return nil, fmt.Errorf("S3 Hudi metadata reader is not configured")
	}
	return d.metadataReader.GetCommits(ctx, tablePath)
}

// GetSavepoints returns savepoint entries from the classic active Hudi timeline.
func (d *S3HudiDriver) GetSavepoints(ctx context.Context, tablePath string) ([]table_formats.HudiSavepoint, error) {
	if d.metadataReader == nil {
		return nil, fmt.Errorf("S3 Hudi metadata reader is not configured")
	}
	return d.metadataReader.GetSavepoints(ctx, tablePath)
}

// HudiMetadata is the legacy metadata response used by the S3 driver API.
type HudiMetadata struct {
	TableLocation string
	Format        string
	TableType     string
	RecordKey     string
	PartitionPath string
}

// HudiQueryResult is retained for API compatibility; queries now return an
// explicit unsupported error until a query engine is configured.
type HudiQueryResult struct {
	Rows        []map[string]interface{}
	NumRows     int64
	InstantTime string
}

func (d *S3HudiDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	return sql, nil
}
