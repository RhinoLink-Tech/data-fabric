package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OpenWithConfig exposes one configured Tencent COS Parquet object as a
// read-only SQL table through database/sql.
func (d *COSParquetDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if config == nil {
		return nil, errors.New("cos-parquet data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" {
		return nil, errors.New("cos-parquet bucket is required in database")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("cos-parquet secret ID and secret key are required")
	}
	objectKey := configString(config.AdditionalProps, "object_key")
	if objectKey == "" {
		objectKey = strings.TrimSpace(config.Host)
	}
	if objectKey == "" {
		return nil, errors.New("cos-parquet object_key is required in additionalProps or host")
	}
	region := configString(config.AdditionalProps, "region")
	if region == "" {
		return nil, errors.New("cos-parquet region is required in additionalProps.region")
	}
	endpoint := configString(config.AdditionalProps, "endpoint_url")
	if endpoint != "" {
		if err := validateCOSEndpoint(endpoint); err != nil {
			return nil, err
		}
	}
	configured, err := NewCOSParquetDriver(ctx, &COSParquetDriverConfig{
		COSConfig: &COSConfig{
			SecretID: config.Username, SecretKey: config.Password, Region: region,
			BucketName: bucket, HTTPS: configBool(config.AdditionalProps, "https", true),
			EndpointURL: endpoint,
		},
		BatchSize: configInt(config.AdditionalProps, "batch_size", 1000),
	})
	if err != nil {
		return nil, err
	}
	configured.objectKey = objectKey
	configured.tableName, err = COSParquetTableName(config)
	if err != nil {
		return nil, err
	}

	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "cos-parquet",
		objectKey:  objectKey,
		tableName:  configured.tableName,
		ping: func(ctx context.Context, key string) error {
			_, err := configured.cosClient.GetObjectMetadata(ctx, key)
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

func validateCOSEndpoint(endpoint string) error {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("cos-parquet endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return nil
}

// COSParquetTableName returns the SQL table identity exposed for a configured object.
func COSParquetTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "cos-parquet")
}

func (d *COSParquetDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}
