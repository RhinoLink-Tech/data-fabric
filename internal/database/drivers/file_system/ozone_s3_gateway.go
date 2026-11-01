package file_system

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	defaultOzoneS3MaxFileBytes int64 = 16 << 20
	maxOzoneS3MaxFileBytes     int64 = 64 << 20
	ozoneS3MaxRows                   = 100000

	defaultOzoneParquetMaxFileBytes = defaultOzoneS3MaxFileBytes
	maxOzoneParquetMaxFileBytes     = maxOzoneS3MaxFileBytes
	ozoneParquetMaxRows             = ozoneS3MaxRows
	defaultOzoneTextMaxFileBytes    = defaultOzoneS3MaxFileBytes
	maxOzoneTextMaxFileBytes        = maxOzoneS3MaxFileBytes
	ozoneTextMaxRows                = ozoneS3MaxRows
	defaultOzoneAvroMaxFileBytes    = defaultOzoneS3MaxFileBytes
	maxOzoneAvroMaxFileBytes        = maxOzoneS3MaxFileBytes
	ozoneAvroMaxRows                = ozoneS3MaxRows
)

func normalizeOzoneS3ObjectConfig(config *model.DataSourceConfig, format string, validKey func(string) bool) (*model.DataSourceConfig, string, error) {
	if config == nil {
		return nil, "", fmt.Errorf("Ozone %s data source config is required", format)
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" {
		return nil, "", errors.New("Ozone S3 bucket is required in database")
	}
	if strings.ContainsAny(bucket, "/\\") || bucket == "." || bucket == ".." {
		return nil, "", errors.New("Ozone S3 bucket must be one path-safe bucket name")
	}
	key := OzoneS3ObjectKey(config)
	if !validKey(key) {
		return nil, "", fmt.Errorf("Ozone %s object_key must be a safe relative object key with a supported extension", format)
	}
	if strings.TrimSpace(config.Username) == "" || strings.TrimSpace(config.Password) == "" {
		return nil, "", errors.New("Ozone S3 access key and secret key are required")
	}
	if _, present := configProp(config, "max_file_bytes"); present {
		if _, err := configPropInt64(config, "max_file_bytes"); err != nil {
			return nil, "", fmt.Errorf("Ozone %s max_file_bytes must be an integer", format)
		}
	}
	maxBytes := OzoneS3MaxFileBytes(config)
	if maxBytes <= 0 || maxBytes > maxOzoneS3MaxFileBytes {
		return nil, "", fmt.Errorf("Ozone %s max_file_bytes must be between 1 and %d", format, maxOzoneS3MaxFileBytes)
	}

	rawEndpoint := configPropString(config, "endpoint_url")
	if rawEndpoint == "" {
		return nil, "", errors.New("Ozone S3 Gateway endpoint_url is required in additionalProps")
	}
	endpoint, err := url.Parse(rawEndpoint)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || (endpoint.Path != "" && endpoint.Path != "/") || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, "", errors.New("Ozone S3 Gateway endpoint_url must be an HTTP(S) origin without credentials, path, query, or fragment")
	}
	port := config.Port
	if port < 0 {
		return nil, "", errors.New("Ozone S3 Gateway port must be between 1 and 65535")
	}
	if port == 0 {
		port = 9878
	}
	if port > 65535 {
		return nil, "", errors.New("Ozone S3 Gateway port must be between 1 and 65535")
	}
	if endpoint.Port() == "" {
		endpoint.Host = net.JoinHostPort(endpoint.Hostname(), strconv.Itoa(port))
	} else {
		parsedPort, parseErr := strconv.Atoi(endpoint.Port())
		if parseErr != nil || parsedPort < 1 || parsedPort > 65535 {
			return nil, "", errors.New("Ozone S3 Gateway endpoint port must be between 1 and 65535")
		}
	}
	endpoint.Path = ""
	endpoint.RawPath = ""

	props := make(map[string]interface{}, len(config.AdditionalProps)+3)
	for name, value := range config.AdditionalProps {
		props[name] = value
	}
	props["endpoint_url"] = endpoint.String()
	if strings.TrimSpace(fmt.Sprint(props["region"])) == "" || props["region"] == nil {
		props["region"] = "us-east-1"
	}
	props["force_path_style"] = true
	normalized := *config
	normalized.AdditionalProps = props
	return &normalized, endpoint.String(), nil
}

func newOzoneS3Client(ctx context.Context, config *model.DataSourceConfig, endpoint string) (*object_storage.S3Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	region := configPropString(config, "region")
	if region == "" {
		region = "us-east-1"
	}
	return object_storage.NewS3Client(ctx, &object_storage.S3Config{
		Region: region, Bucket: strings.TrimSpace(config.Database),
		AccessKey: config.Username, SecretKey: config.Password,
		SessionToken: configPropString(config, "session_token"),
		EndpointURL:  endpoint, ForcePathStyle: true,
	})
}

func OzoneS3ObjectKey(config *model.DataSourceConfig) string {
	if value := configPropString(config, "object_key"); value != "" {
		return value
	}
	if config == nil {
		return ""
	}
	return strings.TrimSpace(config.Host)
}

func OzoneS3TableName(config *model.DataSourceConfig, format string) (string, error) {
	if config == nil {
		return "", fmt.Errorf("Ozone %s data source config is required", format)
	}
	if name := configPropString(config, "table_name"); name != "" {
		return name, nil
	}
	key := OzoneS3ObjectKey(config)
	if key == "" {
		return "", fmt.Errorf("Ozone %s object_key is required in additionalProps or host", format)
	}
	name := strings.TrimSuffix(path.Base(key), path.Ext(key))
	if name == "" || name == "." {
		return "", fmt.Errorf("Ozone %s table_name could not be inferred; set additionalProps.table_name", format)
	}
	return name, nil
}

func OzoneS3MaxFileBytes(config *model.DataSourceConfig) int64 {
	if _, present := configProp(config, "max_file_bytes"); present {
		parsed, err := configPropInt64(config, "max_file_bytes")
		if err != nil {
			return 0
		}
		return parsed
	}
	return defaultOzoneS3MaxFileBytes
}

func validOzoneS3ObjectKey(key string, extensions ...string) bool {
	key = strings.TrimSpace(key)
	if key == "" || path.IsAbs(key) || strings.Contains(key, "\\") {
		return false
	}
	extension := strings.ToLower(path.Ext(key))
	validExtension := false
	for _, allowed := range extensions {
		if extension == allowed {
			validExtension = true
			break
		}
	}
	if !validExtension {
		return false
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}
