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

func (d *MinIODeltaDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if config == nil {
		return nil, errors.New("minio-delta data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" {
		return nil, errors.New("minio-delta bucket is required in database")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("minio-delta access key and secret key are required")
	}
	tablePath := configString(config.AdditionalProps, "table_path")
	if tablePath == "" {
		tablePath = config.Host
	}
	tablePath, err := cleanObjectDeltaTablePath(tablePath)
	if err != nil {
		return nil, fmt.Errorf("minio-delta: %w", err)
	}
	endpoint, secure, err := minIODeltaEndpointFromConfig(config)
	if err != nil {
		return nil, err
	}
	region := configString(config.AdditionalProps, "region")
	if region == "" {
		region = "us-east-1"
	}
	configured, err := NewMinIODeltaDriver(ctx, &MinIOConfig{
		Endpoint: endpoint, AccessKey: config.Username, SecretKey: config.Password,
		Region: region, Secure: secure, Token: configString(config.AdditionalProps, "session_token"),
		Bucket: bucket,
	})
	if err != nil {
		return nil, err
	}
	configured.tablePath = tablePath
	configured.tableName, err = MinIODeltaTableName(config)
	if err != nil {
		return nil, err
	}
	configured.batchSize = configInt(config.AdditionalProps, "batch_size", 1000)
	if configured.batchSize <= 0 {
		configured.batchSize = 1000
	}
	if configured.batchSize > 10_000 {
		return nil, errors.New("minio-delta batch_size cannot exceed 10000")
	}

	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "minio-delta",
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

func (d *MinIODeltaDriver) resolveSnapshot(ctx context.Context, tablePath string) (*objectDeltaSnapshot, error) {
	if d == nil || d.client == nil {
		return nil, errors.New("MinIO Delta driver is not initialized")
	}
	return loadObjectDeltaSnapshot(ctx, tablePath,
		func(ctx context.Context, prefix string, max int) ([]deltaListedObject, error) {
			objects, err := d.client.ListObjects(ctx, prefix, max)
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

func minIODeltaEndpointFromConfig(config *model.DataSourceConfig) (string, bool, error) {
	if config == nil {
		return "", false, errors.New("minio-delta data source config is required")
	}
	raw := configString(config.AdditionalProps, "endpoint_url")
	if raw == "" {
		return "", false, errors.New("minio-delta endpoint_url is required in additionalProps")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", false, errors.New("minio-delta endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return parsed.Host, parsed.Scheme == "https", nil
}

func MinIODeltaEndpointFromConfig(config *model.DataSourceConfig) (string, bool, error) {
	return minIODeltaEndpointFromConfig(config)
}

func MinIODeltaTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("minio-delta data source config is required")
	}
	name := configString(config.AdditionalProps, "table_name")
	if name != "" {
		return name, nil
	}
	tablePath := configString(config.AdditionalProps, "table_path")
	if tablePath == "" {
		tablePath = config.Host
	}
	if tablePath == "" {
		return "", errors.New("minio-delta table_path is required in additionalProps or host")
	}
	return strings.TrimSuffix(path.Base(tablePath), path.Ext(tablePath)), nil
}
