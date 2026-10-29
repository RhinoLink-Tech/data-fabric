package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OpenWithConfig creates a read-only SQL adapter for one configured S3 Avro OCF.
func (d *S3AvroDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if config == nil {
		return nil, errors.New("s3-avro data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" {
		return nil, errors.New("s3-avro bucket is required in database")
	}
	if (strings.TrimSpace(config.Username) == "") != (strings.TrimSpace(config.Password) == "") {
		return nil, errors.New("S3 access key and secret key must be configured together")
	}
	objectKey := configString(config.AdditionalProps, "object_key")
	if objectKey == "" {
		objectKey = strings.TrimSpace(config.Host)
	}
	if objectKey == "" {
		return nil, errors.New("s3-avro object_key is required in additionalProps or host")
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
	configured, err := NewS3AvroDriver(ctx, &S3AvroDriverConfig{
		S3Config: s3Config, BatchSize: configInt(config.AdditionalProps, "batch_size", 1000),
	})
	if err != nil {
		return nil, err
	}
	tableName, err := S3AvroTableName(config)
	if err != nil {
		return nil, err
	}
	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "s3-avro",
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
			return objectSQLSchemaFromAvro(schema)
		},
		readRows: func(ctx context.Context, key string) (*objectSQLSchema, []map[string]interface{}, error) {
			result, err := configured.Query(ctx, key)
			if err != nil {
				return nil, nil, err
			}
			schema, err := objectSQLSchemaFromAvro(result.Schema)
			if err != nil {
				return nil, nil, err
			}
			return schema, result.Rows, nil
		},
	}), nil
}

func objectSQLSchemaFromAvro(schema *AvroSchema) (*objectSQLSchema, error) {
	if schema == nil || len(schema.Fields) == 0 {
		return nil, errors.New("s3-avro returned an empty record schema")
	}
	result := &objectSQLSchema{Columns: make([]objectSQLColumn, len(schema.Fields))}
	for i, field := range schema.Fields {
		result.Columns[i] = objectSQLColumn{
			Name: field.Name, StandardType: field.Type, Nullable: field.Nullable,
		}
	}
	return result, nil
}

// S3AvroTableName returns the SQL table identity for one configured OCF object.
func S3AvroTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "s3-avro")
}
