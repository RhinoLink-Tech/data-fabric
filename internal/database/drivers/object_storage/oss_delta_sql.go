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

// OpenWithConfig exposes one OSS Delta table as a read-only database/sql table.
func (d *OSSDeltaDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if config == nil {
		return nil, errors.New("oss-delta data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" {
		return nil, errors.New("oss-delta bucket is required in database")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("oss-delta access key ID and secret are required")
	}
	tablePath := configString(config.AdditionalProps, "table_path")
	if tablePath == "" {
		tablePath = config.Host
	}
	tablePath, err := cleanObjectDeltaTablePath(tablePath)
	if err != nil {
		return nil, fmt.Errorf("oss-delta: %w", err)
	}
	endpoint, err := OSSDeltaEndpointFromConfig(config)
	if err != nil {
		return nil, err
	}
	securityToken := configString(config.AdditionalProps, "session_token")
	if securityToken == "" {
		securityToken = configString(config.AdditionalProps, "security_token")
	}
	configured, err := NewOSSDeltaDriver(ctx, &OSSConfig{
		Endpoint: endpoint, AccessKeyID: config.Username, AccessKeySecret: config.Password,
		BucketName: bucket, SecurityToken: securityToken,
		ForcePathStyle: configBool(config.AdditionalProps, "force_path_style", false),
	})
	if err != nil {
		return nil, err
	}
	configured.tablePath = tablePath
	configured.tableName, err = OSSDeltaTableName(config)
	if err != nil {
		return nil, err
	}
	configured.batchSize = configInt(config.AdditionalProps, "batch_size", 1000)
	if configured.batchSize <= 0 {
		configured.batchSize = 1000
	}
	if configured.batchSize > 10_000 {
		return nil, errors.New("oss-delta batch_size cannot exceed 10000")
	}

	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "oss-delta",
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

func (d *OSSDeltaDriver) resolveSnapshot(ctx context.Context, tablePath string) (*objectDeltaSnapshot, error) {
	if d == nil || d.client == nil {
		return nil, errors.New("OSS Delta driver is not initialized")
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

// OSSDeltaEndpointFromConfig returns the validated OSS endpoint for a Delta source.
func OSSDeltaEndpointFromConfig(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("oss-delta data source config is required")
	}
	return ossEndpointFromConfig(config)
}

// OSSDeltaTableName returns the SQL table identity for a configured Delta table.
func OSSDeltaTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("oss-delta data source config is required")
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
		return "", errors.New("oss-delta table_path is required in additionalProps or host")
	}
	return strings.TrimSuffix(path.Base(tablePath), path.Ext(tablePath)), nil
}

func (d *OSSDeltaDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}
