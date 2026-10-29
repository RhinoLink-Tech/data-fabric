package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OpenWithConfig creates a read-only SQL adapter for the single configured
// Parquet object. S3 Select remains disabled so the gateway uses the same
// bounded SQL subset on every S3-compatible endpoint.
func (d *S3ParquetDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if config == nil {
		return nil, errors.New("s3-parquet data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" {
		return nil, errors.New("s3-parquet bucket is required in database")
	}
	if (strings.TrimSpace(config.Username) == "") != (strings.TrimSpace(config.Password) == "") {
		return nil, errors.New("S3 access key and secret key must be configured together")
	}
	objectKey := configString(config.AdditionalProps, "object_key")
	if objectKey == "" {
		objectKey = strings.TrimSpace(config.Host)
	}
	if objectKey == "" {
		return nil, errors.New("s3-parquet object_key is required in additionalProps or host")
	}
	region := configString(config.AdditionalProps, "region")
	if region == "" {
		region = "us-east-1"
	}
	s3Config := &S3Config{
		Region: region, Bucket: bucket, AccessKey: config.Username, SecretKey: config.Password,
		SessionToken: configString(config.AdditionalProps, "session_token"),
		RoleARN:      configString(config.AdditionalProps, "role_arn"), ExternalID: configString(config.AdditionalProps, "external_id"),
		EndpointURL:    configString(config.AdditionalProps, "endpoint_url"),
		DisableSSL:     configBool(config.AdditionalProps, "disable_ssl", false),
		ForcePathStyle: configBool(config.AdditionalProps, "force_path_style", false),
		MaxRetries:     configInt(config.AdditionalProps, "max_retries", 0),
	}
	configured, err := NewS3ParquetDriver(ctx, &S3ParquetDriverConfig{
		S3Config: s3Config, BatchSize: configInt(config.AdditionalProps, "batch_size", 1000),
		EnableSelectAPI: false, EnablePushdown: false,
	})
	if err != nil {
		return nil, err
	}
	configured.objectKey = objectKey
	configured.tableName, err = S3ParquetTableName(config)
	if err != nil {
		return nil, err
	}
	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "s3-parquet",
		objectKey:  objectKey,
		tableName:  configured.tableName,
		ping: func(ctx context.Context, key string) error {
			_, err := configured.s3Client.GetObjectMetadata(ctx, key)
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
			result, err := configured.Query(ctx, &S3ParquetQuery{Key: key})
			if err != nil {
				return nil, nil, err
			}
			return objectSQLSchemaFromParquet(result.Schema), result.Rows, nil
		},
	}), nil
}

// S3ParquetTableName returns the SQL table identity exposed for a configured object.
func S3ParquetTableName(config *model.DataSourceConfig) (string, error) {
	return parquetObjectTableName(config, "s3-parquet")
}
