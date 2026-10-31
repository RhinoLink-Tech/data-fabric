package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OpenWithConfig normalizes MinIO settings and delegates ORC decoding and SQL
// execution to the shared S3-compatible ORC adapter.
func (d *MinIOORCDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := minIOORCDataSourceConfig(config)
	if err != nil {
		return nil, err
	}
	return (&S3ORCDriver{}).openWithConfig(ctx, normalized, "minio-orc")
}

func minIOORCDataSourceConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, error) {
	if config == nil {
		return nil, errors.New("minio-orc data source config is required")
	}
	if strings.TrimSpace(config.Database) == "" {
		return nil, errors.New("minio-orc bucket is required in database")
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("minio-orc access key and secret key are required")
	}
	if _, err := MinIOORCTableName(config); err != nil {
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
		return nil, errors.New("minio-orc object_key is required in additionalProps or host")
	}
	endpoint := configString(props, "endpoint_url")
	if err := validateMinIOORCEndpoint(endpoint); err != nil {
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

func validateMinIOORCEndpoint(endpoint string) error {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("minio-orc endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return nil
}

// ValidateMinIOORCConfig validates the required MinIO endpoint and object settings.
func ValidateMinIOORCConfig(config *model.DataSourceConfig) error {
	_, err := minIOORCDataSourceConfig(config)
	return err
}

// MinIOORCTableName returns the SQL table identity for one configured ORC object.
func MinIOORCTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "minio-orc")
}
