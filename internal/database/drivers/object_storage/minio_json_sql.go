package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OpenWithConfig normalizes MinIO settings and delegates JSON parsing and SQL
// execution to the shared S3-compatible adapter.
func (d *MinIOJSONDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := minIOJSONDataSourceConfig(config)
	if err != nil {
		return nil, err
	}
	return (&S3JSONDriver{}).openWithConfig(ctx, normalized, "minio-json")
}

func minIOJSONDataSourceConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, error) {
	if config == nil {
		return nil, errors.New("minio-json data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return nil, errors.New("minio-json bucket is required in database")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("minio-json access key and secret key are required")
	}
	if _, err := MinIOJSONTableName(config); err != nil {
		return nil, err
	}
	props := make(map[string]interface{}, len(config.AdditionalProps)+4)
	for key, value := range config.AdditionalProps {
		props[key] = value
	}
	objectKey := configString(props, "object_key")
	if objectKey == "" {
		objectKey = strings.TrimSpace(config.Host)
	}
	if objectKey == "" {
		return nil, errors.New("minio-json object_key is required in additionalProps or host")
	}
	endpoint := configString(props, "endpoint_url")
	if err := validateMinIOJSONEndpoint(endpoint); err != nil {
		return nil, err
	}
	region := configString(props, "region")
	if region == "" {
		region = "us-east-1"
	}
	props["object_key"] = objectKey
	props["endpoint_url"] = endpoint
	props["region"] = region
	props["force_path_style"] = configBool(props, "force_path_style", true)
	normalized := *config
	normalized.Host = objectKey
	normalized.AdditionalProps = props
	return &normalized, nil
}

func validateMinIOJSONEndpoint(endpoint string) error {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("minio-json endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return nil
}

// ValidateMinIOJSONConfig validates the required MinIO endpoint and object settings.
func ValidateMinIOJSONConfig(config *model.DataSourceConfig) error {
	_, err := minIOJSONDataSourceConfig(config)
	return err
}

// MinIOJSONTableName returns the SQL table identity for one configured JSON object.
func MinIOJSONTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "minio-json")
}
