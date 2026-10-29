package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OpenWithConfig builds a config-aware, read-only SQL adapter for one MinIO
// Parquet object.
func (d *MinIOParquetDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if config == nil {
		return nil, errors.New("minio-parquet data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" {
		return nil, errors.New("minio-parquet bucket is required in database")
	}
	objectKey := configString(config.AdditionalProps, "object_key")
	if objectKey == "" {
		objectKey = strings.TrimSpace(config.Host)
	}
	if objectKey == "" {
		return nil, errors.New("minio-parquet object_key is required in additionalProps or host")
	}
	endpoint, secure, err := minIOEndpointFromConfig(config)
	if err != nil {
		return nil, err
	}
	region := configString(config.AdditionalProps, "region")
	if region == "" {
		region = "us-east-1"
	}
	minioConfig := &MinIOConfig{
		Endpoint: endpoint, AccessKey: config.Username, SecretKey: config.Password,
		Region: region, Secure: secure, Token: configString(config.AdditionalProps, "session_token"),
		Bucket: bucket,
	}
	configured, err := NewMinIOParquetDriver(ctx, &MinIOParquetDriverConfig{MinIOConfig: minioConfig, BatchSize: configInt(config.AdditionalProps, "batch_size", 1000)})
	if err != nil {
		return nil, err
	}
	configured.objectKey = objectKey
	configured.tableName, err = MinIOParquetTableName(config)
	if err != nil {
		return nil, err
	}
	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "minio-parquet",
		objectKey:  objectKey,
		tableName:  configured.tableName,
		ping: func(ctx context.Context, key string) error {
			_, err := configured.minioClient.GetObjectMetadata(ctx, key)
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
			result, err := configured.Query(ctx, &MinIOParquetQuery{Key: key})
			if err != nil {
				return nil, nil, err
			}
			return objectSQLSchemaFromParquet(result.Schema), result.Rows, nil
		},
	}), nil
}

func minIOEndpointFromConfig(config *model.DataSourceConfig) (string, bool, error) {
	raw := configString(config.AdditionalProps, "endpoint_url")
	if raw == "" {
		return "", false, errors.New("minio-parquet endpoint_url is required in additionalProps")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || (parsed.Path != "" && parsed.Path != "/") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false, errors.New("minio-parquet endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return parsed.Host, parsed.Scheme == "https", nil
}

// MinIOParquetTableName returns the SQL table identity exposed for a configured object.
func MinIOParquetTableName(config *model.DataSourceConfig) (string, error) {
	return parquetObjectTableName(config, "minio-parquet")
}

func parquetObjectTableName(config *model.DataSourceConfig, sourceName string) (string, error) {
	if config == nil {
		return "", errors.New(sourceName + " data source config is required")
	}
	name := configString(config.AdditionalProps, "table_name")
	if name == "" {
		key := configString(config.AdditionalProps, "object_key")
		if key == "" {
			key = strings.TrimSpace(config.Host)
		}
		if key == "" {
			return "", errors.New(sourceName + " object_key is required in additionalProps or host")
		}
		name = strings.TrimSuffix(path.Base(key), path.Ext(key))
	}
	if name == "" || name == "." {
		return "", errors.New(sourceName + " table name could not be inferred; set additionalProps.table_name")
	}
	return name, nil
}

func (d *MinIOParquetDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}
