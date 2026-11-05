package object_storage

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func cosIcebergDataSourceConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, error) {
	if config == nil {
		return nil, errors.New("Tencent COS Iceberg data source config is required")
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
		return nil, errors.New("cos-iceberg catalog_uri must be an HTTP(S) URL without credentials, query, or fragment")
	}
	warehouse := configString(props, "warehouse")
	if warehouse == "" {
		warehouse = strings.TrimSpace(config.Database)
	}
	if warehouse == "" {
		return nil, errors.New("cos-iceberg warehouse is required in database or additionalProps.warehouse")
	}
	namespace, err := COSIcebergNamespace(config)
	if err != nil {
		return nil, err
	}
	if _, err := COSIcebergTableName(config); err != nil {
		return nil, err
	}
	region := configString(props, "region")
	if region == "" {
		region = configString(props, "s3.region")
	}
	if region == "" {
		return nil, errors.New("cos-iceberg region is required in additionalProps.region")
	}
	for _, char := range region {
		if !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-') {
			return nil, errors.New("cos-iceberg region contains invalid characters")
		}
	}
	endpoint := configString(props, "endpoint_url")
	if endpoint == "" {
		endpoint = configString(props, "s3.endpoint")
	}
	if endpoint == "" {
		endpoint = "https://cos." + region + ".myqcloud.com"
	}
	if err := validateCOSIcebergEndpoint(endpoint); err != nil {
		return nil, err
	}
	accessKey := strings.TrimSpace(config.Username)
	secretKey := strings.TrimSpace(config.Password)
	if accessKey == "" || secretKey == "" {
		return nil, errors.New("cos-iceberg SecretId and SecretKey are required in username and password")
	}

	props["catalog_uri"] = strings.TrimRight(catalogURI, "/")
	props["warehouse"] = warehouse
	props["namespace"] = namespace
	props["s3.endpoint"] = endpoint
	props["s3.access-key-id"] = accessKey
	props["s3.secret-access-key"] = secretKey
	props["s3.region"] = region
	sessionToken := configString(props, "session_token")
	if sessionToken == "" {
		sessionToken = configString(props, "s3.session-token")
	}
	if sessionToken != "" {
		props["s3.session-token"] = sessionToken
	}
	forcePathStyle := configBool(props, "force_path_style", false)
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

func validateCOSIcebergEndpoint(endpoint string) error {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return errors.New("cos-iceberg endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	return nil
}

// ValidateCOSIcebergConfig checks the REST catalog and COS storage settings.
func ValidateCOSIcebergConfig(config *model.DataSourceConfig) error {
	_, err := cosIcebergDataSourceConfig(config)
	return err
}

// COSIcebergTableName returns the single SQL table exposed by a COS Iceberg source.
func COSIcebergTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("cos-iceberg data source config is required")
	}
	name := configString(config.AdditionalProps, "table_name")
	if name == "" {
		return "", errors.New("cos-iceberg table_name is required in additionalProps")
	}
	if !isIcebergIdentifier(name) {
		return "", fmt.Errorf("cos-iceberg table_name %q must be an unqualified SQL identifier", name)
	}
	return name, nil
}

// COSIcebergNamespace returns the configured Iceberg namespace.
func COSIcebergNamespace(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("cos-iceberg data source config is required")
	}
	namespace := configString(config.AdditionalProps, "namespace")
	if namespace == "" {
		namespace = strings.TrimSpace(config.Schema)
	}
	if namespace == "" {
		return "", errors.New("cos-iceberg namespace is required in schema or additionalProps.namespace")
	}
	for _, part := range strings.Split(namespace, ".") {
		if !isIcebergIdentifier(part) {
			return "", fmt.Errorf("cos-iceberg namespace %q must contain unqualified SQL identifiers", namespace)
		}
	}
	return namespace, nil
}

func isIcebergIdentifier(value string) bool {
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
