package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OpenWithConfig exposes one configured Alibaba OSS Parquet object as a
// read-only SQL table through database/sql.
func (d *OSSParquetDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if config == nil {
		return nil, errors.New("oss-parquet data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" {
		return nil, errors.New("oss-parquet bucket is required in database")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("oss-parquet access key ID and secret are required")
	}
	objectKey := configString(config.AdditionalProps, "object_key")
	if objectKey == "" {
		objectKey = strings.TrimSpace(config.Host)
	}
	if objectKey == "" {
		return nil, errors.New("oss-parquet object_key is required in additionalProps or host")
	}
	endpoint, err := ossEndpointFromConfig(config)
	if err != nil {
		return nil, err
	}
	securityToken := configString(config.AdditionalProps, "session_token")
	if securityToken == "" {
		securityToken = configString(config.AdditionalProps, "security_token")
	}
	configured, err := NewOSSParquetDriver(ctx, &OSSParquetDriverConfig{
		OSSConfig: &OSSConfig{
			Endpoint: endpoint, AccessKeyID: config.Username, AccessKeySecret: config.Password,
			BucketName: bucket, SecurityToken: securityToken,
			ForcePathStyle: configBool(config.AdditionalProps, "force_path_style", false),
		},
		BatchSize: configInt(config.AdditionalProps, "batch_size", 1000),
	})
	if err != nil {
		return nil, err
	}
	configured.objectKey = objectKey
	configured.tableName, err = OSSParquetTableName(config)
	if err != nil {
		return nil, err
	}

	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "oss-parquet",
		objectKey:  objectKey,
		tableName:  configured.tableName,
		ping: func(ctx context.Context, key string) error {
			_, err := configured.ossClient.GetObjectMetadata(ctx, key)
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

func ossEndpointFromConfig(config *model.DataSourceConfig) (string, error) {
	raw := configString(config.AdditionalProps, "endpoint_url")
	if raw == "" {
		raw = configString(config.AdditionalProps, "endpoint")
	}
	if raw == "" {
		return "", errors.New("oss-parquet endpoint_url is required in additionalProps")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("oss-parquet endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return parsed.String(), nil
}

// OSSParquetTableName returns the SQL table identity exposed for a configured object.
func OSSParquetTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "oss-parquet")
}

func (d *OSSParquetDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}
