package object_storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// S3DeltaDriver reads bounded, unpartitioned Delta snapshots stored in S3.
type S3DeltaDriver struct {
	config    *S3Config
	client    *S3Client
	tablePath string
	tableName string
	batchSize int
}

func NewS3DeltaDriver(ctx context.Context, config *S3Config) (*S3DeltaDriver, error) {
	if config == nil {
		return nil, fmt.Errorf("S3 Delta configuration is required")
	}
	client, err := NewS3Client(ctx, config)
	if err != nil {
		return nil, err
	}
	return &S3DeltaDriver{config: config, client: client, batchSize: 1000}, nil
}

func (d *S3DeltaDriver) Open(string) (*sql.DB, error) {
	return nil, fmt.Errorf("S3 Delta requires a configured read-only SQL adapter")
}

func (d *S3DeltaDriver) ValidateDSN(dsn string) error {
	if !strings.HasPrefix(dsn, "s3://") {
		return fmt.Errorf("invalid S3 Delta URI")
	}
	bucket, tablePath, err := ParseS3URI(dsn)
	if err != nil {
		return fmt.Errorf("invalid S3 Delta URI: %w", err)
	}
	if strings.TrimSpace(bucket) == "" {
		return fmt.Errorf("S3 Delta URI must include a bucket")
	}
	if _, err := cleanObjectDeltaTablePath(tablePath); err != nil {
		return err
	}
	return nil
}

func (d *S3DeltaDriver) GetDefaultPort() int { return 443 }

func (d *S3DeltaDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "s3://"
	}
	tablePath := configString(config.AdditionalProps, "table_path")
	if tablePath == "" {
		tablePath = strings.TrimSpace(config.Host)
	}
	return fmt.Sprintf("s3://%s/%s", strings.TrimSpace(config.Database), tablePath)
}

func (d *S3DeltaDriver) GetDatabaseTypeName() string { return "s3-delta" }
func (d *S3DeltaDriver) GetDriverName() string       { return "s3-delta" }
func (d *S3DeltaDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (d *S3DeltaDriver) TestConnection(*sql.DB) error {
	return fmt.Errorf("use TestConnectionContext instead")
}
func (d *S3DeltaDriver) TestConnectionContext(ctx context.Context) error {
	if d == nil || d.client == nil {
		return fmt.Errorf("S3 Delta driver is not initialized")
	}
	_, err := d.client.ListObjects(ctx, "", 1)
	if err != nil {
		return fmt.Errorf("failed to test S3 connection: %w", err)
	}
	return nil
}
func (d *S3DeltaDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsSchemaDiscovery: true,
	}
}
func (d *S3DeltaDriver) ConfigureAuth(interface{}) error { return nil }

// GetTableMetadata returns the current complete Delta snapshot metadata.
func (d *S3DeltaDriver) GetTableMetadata(ctx context.Context, tablePath string) (*DeltaMetadata, error) {
	cleanPath, err := cleanObjectDeltaTablePath(tablePath)
	if err != nil {
		return nil, err
	}
	snapshot, err := d.resolveSnapshot(ctx, cleanPath)
	if err != nil {
		return nil, err
	}
	return &DeltaMetadata{
		TableLocation: "s3://" + d.config.Bucket + "/" + cleanPath,
		Format:        "delta",
		Version:       snapshot.version,
	}, nil
}

// DeltaMetadata describes the latest snapshot resolved from the transaction log.
type DeltaMetadata struct {
	TableLocation string
	Format        string
	Version       int64
	PartitionCols []string
}

// QueryTable reads the current Delta snapshot. Historical time travel is not supported.
func (d *S3DeltaDriver) QueryTable(ctx context.Context, tablePath string, version int64) (*DeltaQueryResult, error) {
	snapshot, err := d.resolveSnapshot(ctx, tablePath)
	if err != nil {
		return nil, err
	}
	if version >= 0 && version != snapshot.version {
		return nil, fmt.Errorf("S3 Delta time travel is not supported; current snapshot is version %d", snapshot.version)
	}
	batchSize := d.batchSize
	if batchSize <= 0 {
		batchSize = 1000
	}
	_, rows, err := readObjectDeltaRows(ctx, snapshot, batchSize, maxObjectDeltaTableBytes, d.client.GetObject)
	if err != nil {
		return nil, err
	}
	return &DeltaQueryResult{Rows: rows, NumRows: int64(len(rows)), Version: snapshot.version}, nil
}

type DeltaQueryResult struct {
	Rows    []map[string]interface{}
	NumRows int64
	Version int64
}

func (d *S3DeltaDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}
