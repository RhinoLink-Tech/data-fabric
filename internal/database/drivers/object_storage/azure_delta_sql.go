package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OpenWithConfig exposes one configured Azure Delta table as a read-only SQL table.
func (d *AzureDeltaDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if config == nil {
		return nil, errors.New("azure-delta data source config is required")
	}
	container := strings.TrimSpace(config.Database)
	if container == "" {
		return nil, errors.New("azure-delta container is required in database")
	}
	accountName := strings.TrimSpace(config.Username)
	if accountName == "" {
		return nil, errors.New("azure-delta account name is required in username")
	}
	sasToken := configString(config.AdditionalProps, "sas_token")
	if strings.TrimSpace(config.Password) == "" && strings.TrimSpace(sasToken) == "" {
		return nil, errors.New("azure-delta account key or sas_token is required")
	}
	tablePath := configString(config.AdditionalProps, "table_path")
	if tablePath == "" {
		tablePath = config.Host
	}
	tablePath, err := cleanObjectDeltaTablePath(tablePath)
	if err != nil {
		return nil, fmt.Errorf("azure-delta: %w", err)
	}
	endpoint, err := AzureDeltaEndpointFromConfig(config)
	if err != nil {
		return nil, err
	}
	configured, err := NewAzureDeltaDriver(ctx, &AzureBlobConfig{
		AccountName: accountName, AccountKey: config.Password, SASToken: sasToken,
		ContainerName: container, Endpoint: endpoint,
	})
	if err != nil {
		return nil, err
	}
	configured.tablePath = tablePath
	configured.tableName, err = AzureDeltaTableName(config)
	if err != nil {
		return nil, err
	}
	configured.batchSize = configInt(config.AdditionalProps, "batch_size", 1000)
	if configured.batchSize <= 0 {
		configured.batchSize = 1000
	}
	if configured.batchSize > 10_000 {
		return nil, errors.New("azure-delta batch_size cannot exceed 10000")
	}

	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "azure-delta",
		objectKey:  tablePath,
		tableName:  configured.tableName,
		ping: func(ctx context.Context, key string) error {
			_, err := configured.resolveSnapshot(ctx, key)
			return err
		},
		getSchema: func(ctx context.Context, key string) (*objectSQLSchema, error) {
			snapshot, err := configured.resolveSnapshot(ctx, key)
			if err != nil {
				return nil, err
			}
			schema, err := readObjectDeltaSchema(ctx, snapshot, configured.batchSize, configured.client.GetBlob)
			if err != nil {
				return nil, err
			}
			return objectSQLSchemaFromParquet(schema), nil
		},
		readRows: func(ctx context.Context, key string) (*objectSQLSchema, []map[string]interface{}, error) {
			snapshot, err := configured.resolveSnapshot(ctx, key)
			if err != nil {
				return nil, nil, err
			}
			schema, rows, err := readObjectDeltaRows(ctx, snapshot, configured.batchSize, maxObjectDeltaTableBytes, configured.client.GetBlob)
			if err != nil {
				return nil, nil, err
			}
			return objectSQLSchemaFromParquet(schema), rows, nil
		},
	}), nil
}

func (d *AzureDeltaDriver) resolveSnapshot(ctx context.Context, tablePath string) (*objectDeltaSnapshot, error) {
	if d == nil || d.client == nil {
		return nil, errors.New("Azure Delta driver is not initialized")
	}
	return loadObjectDeltaSnapshot(ctx, tablePath,
		func(ctx context.Context, prefix string, max int) ([]deltaListedObject, error) {
			blobs, err := d.client.ListBlobs(ctx, prefix, max)
			if err != nil {
				return nil, err
			}
			result := make([]deltaListedObject, len(blobs))
			for i, blob := range blobs {
				result[i] = deltaListedObject{key: blob.Name, size: blob.Size}
			}
			return result, nil
		},
		d.client.GetBlob,
	)
}

// AzureDeltaEndpointFromConfig validates and returns an optional Azure Blob endpoint.
func AzureDeltaEndpointFromConfig(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("azure-delta data source config is required")
	}
	endpoint := strings.TrimSpace(configString(config.AdditionalProps, "endpoint_url"))
	if endpoint != "" {
		if err := validateAzureBlobEndpoint(endpoint); err != nil {
			return "", fmt.Errorf("azure-delta: %w", err)
		}
	}
	return endpoint, nil
}

// AzureDeltaTableName returns the SQL table identity for a configured Delta table.
func AzureDeltaTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("azure-delta data source config is required")
	}
	tablePath := configString(config.AdditionalProps, "table_path")
	if tablePath == "" {
		tablePath = config.Host
	}
	tablePath, err := cleanObjectDeltaTablePath(tablePath)
	if err != nil {
		return "", fmt.Errorf("azure-delta: %w", err)
	}
	name := configString(config.AdditionalProps, "table_name")
	if name != "" {
		return name, nil
	}
	return path.Base(tablePath), nil
}
