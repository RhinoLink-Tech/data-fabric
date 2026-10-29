package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OpenWithConfig creates a read-only SQL adapter for one configured S3 ORC object.
func (d *S3ORCDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if config == nil {
		return nil, errors.New("s3-orc data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" {
		return nil, errors.New("s3-orc bucket is required in database")
	}
	if (strings.TrimSpace(config.Username) == "") != (strings.TrimSpace(config.Password) == "") {
		return nil, errors.New("S3 access key and secret key must be configured together")
	}
	objectKey := configString(config.AdditionalProps, "object_key")
	if objectKey == "" {
		objectKey = strings.TrimSpace(config.Host)
	}
	if objectKey == "" {
		return nil, errors.New("s3-orc object_key is required in additionalProps or host")
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
	configured, err := NewS3ORCDriver(ctx, &S3ORCDriverConfig{
		S3Config: s3Config, BatchSize: configInt(config.AdditionalProps, "batch_size", 1000),
		EnablePushdown: false,
	})
	if err != nil {
		return nil, err
	}
	tableName, err := S3ORCTableName(config)
	if err != nil {
		return nil, err
	}
	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "s3-orc",
		objectKey:  objectKey,
		tableName:  tableName,
		ping: func(ctx context.Context, key string) error {
			_, err := configured.s3Client.GetObjectMetadata(ctx, key)
			return err
		},
		getSchema: func(ctx context.Context, key string) (*objectSQLSchema, error) {
			schema, err := configured.GetSchema(ctx, key)
			if err != nil {
				return nil, err
			}
			return objectSQLSchemaFromORC(schema)
		},
		readRows: func(ctx context.Context, key string) (*objectSQLSchema, []map[string]interface{}, error) {
			result, err := configured.Query(ctx, key)
			if err != nil {
				return nil, nil, err
			}
			schema, err := objectSQLSchemaFromORC(result.Schema)
			if err != nil {
				return nil, nil, err
			}
			return schema, result.Rows, nil
		},
	}), nil
}

func objectSQLSchemaFromORC(schema *ORCSchema) (*objectSQLSchema, error) {
	if schema == nil {
		return nil, errors.New("s3-orc returned an empty schema")
	}
	result := &objectSQLSchema{Columns: make([]objectSQLColumn, len(schema.Columns))}
	for i, column := range schema.Columns {
		result.Columns[i] = objectSQLColumn{
			Name: column.Name, StandardType: column.Type, Nullable: column.Nullable,
		}
	}
	return result, nil
}

// S3ORCTableName returns the SQL table identity for one configured ORC object.
func S3ORCTableName(config *model.DataSourceConfig) (string, error) {
	return parquetObjectTableName(config, "s3-orc")
}
