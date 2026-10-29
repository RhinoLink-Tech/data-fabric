package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OpenWithConfig exposes one configured Azure Blob Parquet object as a
// read-only SQL table through database/sql.
func (d *AzureBlobParquetDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if config == nil {
		return nil, errors.New("azure-parquet data source config is required")
	}
	container := strings.TrimSpace(config.Database)
	if container == "" {
		return nil, errors.New("azure-parquet container is required in database")
	}
	accountName := strings.TrimSpace(config.Username)
	if accountName == "" {
		return nil, errors.New("azure-parquet account name is required in username")
	}
	objectKey := configString(config.AdditionalProps, "object_key")
	if objectKey == "" {
		objectKey = strings.TrimSpace(config.Host)
	}
	if objectKey == "" {
		return nil, errors.New("azure-parquet object_key is required in additionalProps or host")
	}
	sasToken := configString(config.AdditionalProps, "sas_token")
	if strings.TrimSpace(config.Password) == "" && sasToken == "" {
		return nil, errors.New("azure-parquet account key or sas_token is required")
	}
	endpoint := configString(config.AdditionalProps, "endpoint_url")
	if endpoint != "" {
		if err := validateAzureBlobEndpoint(endpoint); err != nil {
			return nil, err
		}
	}

	configured, err := NewAzureBlobParquetDriver(ctx, &AzureBlobParquetDriverConfig{
		AzureConfig: &AzureBlobConfig{
			AccountName: accountName, AccountKey: config.Password, SASToken: sasToken,
			ContainerName: container, Endpoint: endpoint,
		},
		BatchSize: configInt(config.AdditionalProps, "batch_size", 1000),
	})
	if err != nil {
		return nil, err
	}
	configured.objectKey = objectKey
	configured.tableName, err = AzureBlobParquetTableName(config)
	if err != nil {
		return nil, err
	}

	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "azure-parquet",
		objectKey:  objectKey,
		tableName:  configured.tableName,
		ping: func(ctx context.Context, key string) error {
			_, err := configured.azureClient.GetBlobMetadata(ctx, key)
			return err
		},
		getSchema: func(ctx context.Context, key string) (*objectSQLSchema, error) {
			schema, err := configured.GetSchema(ctx, key)
			if err != nil {
				return nil, err
			}
			return objectSQLSchemaFromParquet(schema), nil
		},
		readRows: func(ctx context.Context, key string) (*objectSQLSchema, []map[string]interface{}, error) {
			result, err := configured.Query(ctx, key)
			if err != nil {
				return nil, nil, err
			}
			return objectSQLSchemaFromParquet(result.Schema), result.Rows, nil
		},
	}), nil
}

func validateAzureBlobEndpoint(endpoint string) error {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("azure-parquet endpoint_url must be an HTTP(S) URL without credentials, query, or fragment")
	}
	return nil
}

// AzureBlobParquetTableName returns the SQL table identity exposed for a
// configured Azure Blob Parquet object.
func AzureBlobParquetTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "azure-parquet")
}

func (d *AzureBlobParquetDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}
