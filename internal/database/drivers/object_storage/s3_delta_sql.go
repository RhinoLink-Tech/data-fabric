package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OpenWithConfig exposes one configured S3 Delta table through the read-only
// object SQL adapter and shared transaction-log snapshot resolver.
func (d *S3DeltaDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateS3DeltaConfig(config); err != nil {
		return nil, err
	}
	bucket := strings.TrimSpace(config.Database)
	tablePath := configString(config.AdditionalProps, "table_path")
	if tablePath == "" {
		tablePath = strings.TrimSpace(config.Host)
	}
	cleanPath, err := cleanObjectDeltaTablePath(tablePath)
	if err != nil {
		return nil, err
	}
	region := configString(config.AdditionalProps, "region")
	if region == "" {
		region = "us-east-1"
	}
	configured, err := NewS3DeltaDriver(ctx, &S3Config{
		Region: region, Bucket: bucket, AccessKey: strings.TrimSpace(config.Username), SecretKey: config.Password,
		SessionToken: configString(config.AdditionalProps, "session_token"),
		RoleARN:      configString(config.AdditionalProps, "role_arn"), ExternalID: configString(config.AdditionalProps, "external_id"),
		EndpointURL:    configString(config.AdditionalProps, "endpoint_url"),
		DisableSSL:     configBool(config.AdditionalProps, "disable_ssl", false),
		ForcePathStyle: configBool(config.AdditionalProps, "force_path_style", configString(config.AdditionalProps, "endpoint_url") != ""),
		MaxRetries:     configInt(config.AdditionalProps, "max_retries", 0),
	})
	if err != nil {
		return nil, err
	}
	configured.tablePath = cleanPath
	configured.tableName, err = S3DeltaTableName(config)
	if err != nil {
		return nil, err
	}
	configured.batchSize = configInt(config.AdditionalProps, "batch_size", 1000)
	if configured.batchSize <= 0 {
		configured.batchSize = 1000
	}
	if configured.batchSize > 10_000 {
		return nil, errors.New("s3-delta batch_size cannot exceed 10000")
	}

	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "s3-delta",
		objectKey:  cleanPath,
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
			schema, err := readObjectDeltaSchema(ctx, snapshot, configured.batchSize, configured.client.GetObject)
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
			schema, rows, err := readObjectDeltaRows(ctx, snapshot, configured.batchSize, maxObjectDeltaTableBytes, configured.client.GetObject)
			if err != nil {
				return nil, nil, err
			}
			return objectSQLSchemaFromParquet(schema), rows, nil
		},
	}), nil
}

func (d *S3DeltaDriver) resolveSnapshot(ctx context.Context, tablePath string) (*objectDeltaSnapshot, error) {
	if d == nil || d.client == nil {
		return nil, errors.New("S3 Delta driver is not initialized")
	}
	return loadObjectDeltaSnapshot(ctx, tablePath,
		func(ctx context.Context, prefix string, max int) ([]deltaListedObject, error) {
			objects, err := d.client.ListObjects(ctx, prefix, int32(max))
			if err != nil {
				return nil, err
			}
			result := make([]deltaListedObject, len(objects))
			for i, object := range objects {
				result[i] = deltaListedObject{key: object.Key, size: object.Size}
			}
			return result, nil
		},
		d.client.GetObject,
	)
}

// ValidateS3DeltaConfig validates the bucket, Delta table path, and S3 options.
func ValidateS3DeltaConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return errors.New("s3-delta data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return errors.New("s3-delta bucket is required in database")
	}
	if (strings.TrimSpace(config.Username) == "") != (strings.TrimSpace(config.Password) == "") {
		return errors.New("S3 access key and secret key must be configured together")
	}
	if configString(config.AdditionalProps, "session_token") != "" && strings.TrimSpace(config.Username) == "" {
		return errors.New("S3 session_token requires an access key and secret key")
	}
	if configString(config.AdditionalProps, "external_id") != "" && configString(config.AdditionalProps, "role_arn") == "" {
		return errors.New("S3 external_id requires role_arn")
	}
	if _, err := S3DeltaTableName(config); err != nil {
		return err
	}
	tablePath := configString(config.AdditionalProps, "table_path")
	if tablePath == "" {
		tablePath = config.Host
	}
	if _, err := cleanObjectDeltaTablePath(tablePath); err != nil {
		return fmt.Errorf("s3-delta: %w", err)
	}
	if endpoint := configString(config.AdditionalProps, "endpoint_url"); endpoint != "" {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
			return errors.New("s3-delta endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
		}
	}
	batchSize := configInt(config.AdditionalProps, "batch_size", 1000)
	if batchSize > 10_000 {
		return errors.New("s3-delta batch_size cannot exceed 10000")
	}
	return nil
}

// S3DeltaTableName returns the SQL table identity for one configured Delta table.
func S3DeltaTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("s3-delta data source config is required")
	}
	name := configString(config.AdditionalProps, "table_name")
	if name != "" {
		return name, nil
	}
	tablePath := configString(config.AdditionalProps, "table_path")
	if tablePath == "" {
		tablePath = strings.TrimSpace(config.Host)
	}
	if tablePath == "" {
		return "", errors.New("s3-delta table_path is required in additionalProps or host")
	}
	return strings.TrimSuffix(path.Base(tablePath), path.Ext(tablePath)), nil
}
