package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/table_formats"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OpenWithConfig adapts MinIO storage properties to the shared Iceberg REST SQL driver.
func (d *MinIOIcebergDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := minIOIcebergDataSourceConfig(config)
	if err != nil {
		return nil, err
	}
	icebergDriver := &table_formats.IcebergDriver{}
	return icebergDriver.OpenWithConfig(ctx, normalized)
}

func minIOIcebergDataSourceConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, error) {
	if config == nil {
		return nil, errors.New("minio-iceberg data source config is required")
	}
	props := make(map[string]interface{}, len(config.AdditionalProps)+12)
	for key, value := range config.AdditionalProps {
		props[key] = value
	}
	catalogURI := configString(props, "catalog_uri")
	if catalogURI == "" {
		catalogURI = strings.TrimSpace(config.Host)
	}
	parsedCatalog, err := url.Parse(catalogURI)
	if err != nil || parsedCatalog.Host == "" || (parsedCatalog.Scheme != "http" && parsedCatalog.Scheme != "https") ||
		parsedCatalog.User != nil || parsedCatalog.RawQuery != "" || parsedCatalog.Fragment != "" {
		return nil, errors.New("minio-iceberg catalog_uri must be an HTTP(S) URL without credentials, query, or fragment")
	}
	warehouse := configString(props, "warehouse")
	if warehouse == "" {
		warehouse = strings.TrimSpace(config.Database)
	}
	if warehouse == "" {
		return nil, errors.New("minio-iceberg warehouse is required in database or additionalProps.warehouse")
	}
	namespace, err := MinIOIcebergNamespace(config)
	if err != nil {
		return nil, err
	}
	if _, err := MinIOIcebergTableName(config); err != nil {
		return nil, err
	}
	endpoint := configString(props, "endpoint_url")
	if endpoint == "" {
		endpoint = configString(props, "s3.endpoint")
	}
	if err := validateMinIOIcebergEndpoint(endpoint); err != nil {
		return nil, err
	}
	accessKey := strings.TrimSpace(config.Username)
	secretKey := strings.TrimSpace(config.Password)
	if accessKey == "" || secretKey == "" {
		return nil, errors.New("minio-iceberg access key and secret key are required in username and password")
	}

	props["catalog_uri"] = strings.TrimRight(catalogURI, "/")
	props["warehouse"] = warehouse
	props["namespace"] = namespace
	props["s3.endpoint"] = endpoint
	props["s3.access-key-id"] = accessKey
	props["s3.secret-access-key"] = secretKey
	region := configString(props, "region")
	if region == "" {
		region = configString(props, "s3.region")
	}
	if region == "" {
		region = "us-east-1"
	}
	props["s3.region"] = region
	sessionToken := configString(props, "session_token")
	if sessionToken == "" {
		sessionToken = configString(props, "s3.session-token")
	}
	if sessionToken != "" {
		props["s3.session-token"] = sessionToken
	}
	forcePathStyle := configBool(props, "force_path_style", true)
	if _, exists := props["s3.force-virtual-addressing"]; !exists {
		props["s3.force-virtual-addressing"] = strconv.FormatBool(!forcePathStyle)
	}
	if catalogToken := configString(props, "catalog_token"); configString(props, "token") == "" && catalogToken != "" {
		props["token"] = catalogToken
	}
	if catalogCredential := configString(props, "catalog_credential"); configString(props, "credential") == "" && catalogCredential != "" {
		props["credential"] = catalogCredential
	}

	normalized := *config
	normalized.Host = strings.TrimRight(catalogURI, "/")
	normalized.Database = warehouse
	normalized.Schema = namespace
	normalized.Username = ""
	normalized.Password = ""
	normalized.AdditionalProps = props
	return &normalized, nil
}

func validateMinIOIcebergEndpoint(endpoint string) error {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("minio-iceberg endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return nil
}

// ValidateMinIOIcebergConfig checks required REST catalog and MinIO storage settings.
func ValidateMinIOIcebergConfig(config *model.DataSourceConfig) error {
	_, err := minIOIcebergDataSourceConfig(config)
	return err
}

// MinIOIcebergTableName returns the single SQL table exposed by a MinIO Iceberg source.
func MinIOIcebergTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("minio-iceberg data source config is required")
	}
	name := configString(config.AdditionalProps, "table_name")
	if name == "" {
		return "", errors.New("minio-iceberg table_name is required in additionalProps")
	}
	if !isMinIOIcebergIdentifier(name) {
		return "", fmt.Errorf("minio-iceberg table_name %q must be an unqualified SQL identifier", name)
	}
	return name, nil
}

// MinIOIcebergNamespace returns the configured Iceberg namespace.
func MinIOIcebergNamespace(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("minio-iceberg data source config is required")
	}
	namespace := configString(config.AdditionalProps, "namespace")
	if namespace == "" {
		namespace = strings.TrimSpace(config.Schema)
	}
	if namespace == "" {
		return "", errors.New("minio-iceberg namespace is required in schema or additionalProps.namespace")
	}
	for _, part := range strings.Split(namespace, ".") {
		if !isMinIOIcebergIdentifier(part) {
			return "", fmt.Errorf("minio-iceberg namespace %q must contain unqualified SQL identifiers", namespace)
		}
	}
	return namespace, nil
}

func isMinIOIcebergIdentifier(value string) bool {
	for index, char := range value {
		if index == 0 {
			if char != '_' && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') {
				return false
			}
			continue
		}
		if char != '_' && char != '$' && (char < 'A' || char > 'Z') &&
			(char < 'a' || char > 'z') && (char < '0' || char > '9') {
			return false
		}
	}
	return value != ""
}
