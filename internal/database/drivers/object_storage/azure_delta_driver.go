package object_storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// AzureDeltaDriver reads bounded, unpartitioned Delta snapshots stored in Azure Blob Storage.
type AzureDeltaDriver struct {
	config    *AzureBlobConfig
	client    *AzureBlobClient
	tablePath string
	tableName string
	batchSize int
}

// NewAzureDeltaDriver creates an Azure Delta reader.
func NewAzureDeltaDriver(ctx context.Context, config *AzureBlobConfig) (*AzureDeltaDriver, error) {
	client, err := NewAzureBlobClient(ctx, config)
	if err != nil {
		return nil, err
	}
	return &AzureDeltaDriver{config: config, client: client, batchSize: 1000}, nil
}

func (d *AzureDeltaDriver) Open(string) (*sql.DB, error) {
	return nil, fmt.Errorf("Azure Delta requires a configured read-only SQL adapter")
}

func (d *AzureDeltaDriver) ValidateDSN(dsn string) error {
	if !strings.HasPrefix(dsn, "azure://") {
		return fmt.Errorf("invalid Azure Delta URI: %s", dsn)
	}
	container, tablePath, found := strings.Cut(strings.TrimPrefix(dsn, "azure://"), "/")
	if !found || strings.TrimSpace(container) == "" {
		return fmt.Errorf("Azure Delta URI must include a container and table path")
	}
	if _, err := cleanObjectDeltaTablePath(tablePath); err != nil {
		return err
	}
	return nil
}

func (d *AzureDeltaDriver) GetDefaultPort() int { return 443 }

func (d *AzureDeltaDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "azure://"
	}
	tablePath := configString(config.AdditionalProps, "table_path")
	if tablePath == "" {
		tablePath = config.Host
	}
	return fmt.Sprintf("azure://%s/%s", config.Database, tablePath)
}

func (d *AzureDeltaDriver) GetDatabaseTypeName() string { return "azure-delta" }
func (d *AzureDeltaDriver) GetDriverName() string       { return "azure-delta" }
func (d *AzureDeltaDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}

func (d *AzureDeltaDriver) TestConnection(*sql.DB) error {
	return fmt.Errorf("use TestConnectionContext instead")
}

func (d *AzureDeltaDriver) TestConnectionContext(ctx context.Context) error {
	if d == nil || d.client == nil {
		return fmt.Errorf("Azure Delta driver is not initialized")
	}
	_, err := d.client.ListBlobs(ctx, "", 1)
	if err != nil {
		return fmt.Errorf("failed to test Azure Blob connection: %w", err)
	}
	return nil
}

func (d *AzureDeltaDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:             true,
		SupportsTransaction:     false,
		SupportsSchemaDiscovery: true,
		SupportsTimeTravel:      false,
		RequiresTokenRotation:   false,
		SupportsStreaming:       false,
	}
}

func (d *AzureDeltaDriver) ConfigureAuth(interface{}) error { return nil }

func (d *AzureDeltaDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}
