package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	defaultAzureBlobCSVMaxFileBytes int64 = 16 << 20
	maxAzureBlobCSVMaxFileBytes     int64 = 64 << 20
	azureBlobCSVMaxRows                   = 100000
)

// AzureBlobCSVDriver exposes one bounded Azure Blob CSV object as a read-only SQL table.
type AzureBlobCSVDriver struct{}

func (*AzureBlobCSVDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("Azure Blob CSV requires a configured read-only SQL adapter")
}

func (*AzureBlobCSVDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	normalized, err := NormalizeAzureBlobCSVConfig(config)
	if err != nil {
		return nil, err
	}
	objectKey := AzureBlobCSVObjectKey(normalized)
	tableName, err := AzureBlobCSVTableName(normalized)
	if err != nil {
		return nil, err
	}
	endpoint := configString(normalized.AdditionalProps, "endpoint_url")
	client, err := NewAzureBlobClient(ctx, &AzureBlobConfig{
		AccountName: strings.TrimSpace(normalized.Username), AccountKey: normalized.Password,
		SASToken:      configString(normalized.AdditionalProps, "sas_token"),
		ContainerName: strings.TrimSpace(normalized.Database), Endpoint: endpoint,
	})
	if err != nil {
		return nil, fmt.Errorf("create Azure Blob CSV client: %w", err)
	}
	maxBytes, _ := azureBlobCSVMaxFileBytes(normalized)
	return NewCSVSQLDatabase(CSVSQLDatabaseConfig{
		DriverName: "azure-blob-csv", ObjectKey: objectKey, TableName: tableName,
		Delimiter: AzureBlobCSVDelimiter(normalized), HasHeader: AzureBlobCSVHasHeader(normalized),
		SkipHeaderRows: AzureBlobCSVSkipHeaderRows(normalized), NullValues: configStringSlice(normalized.AdditionalProps, "null_values"),
		MaxRows: azureBlobCSVMaxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		metadata, err := client.GetBlobMetadata(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect Azure Blob CSV object: %w", err)
		}
		if metadata == nil || metadata.ContentLength < 0 || metadata.ContentLength > maxBytes {
			return fmt.Errorf("Azure Blob CSV object exceeds configured maximum of %d bytes or has an invalid size", maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.GetBlobLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read Azure Blob CSV object: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, nil)
}

func (*AzureBlobCSVDriver) GetDefaultPort() int         { return 443 }
func (*AzureBlobCSVDriver) GetDatabaseTypeName() string { return "azure_blob_csv" }
func (*AzureBlobCSVDriver) GetDriverName() string       { return "azure-blob-csv" }
func (*AzureBlobCSVDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryObjectStorage
}
func (*AzureBlobCSVDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*AzureBlobCSVDriver) ConfigureAuth(interface{}) error { return nil }
func (*AzureBlobCSVDriver) TestConnection(db *sql.DB) error {
	if db == nil {
		return errors.New("Azure Blob CSV SQL database is required for a connection check")
	}
	return db.Ping()
}
func (*AzureBlobCSVDriver) TestConnectionContext(context.Context) error {
	return errors.New("Azure Blob CSV connectivity must be checked with PingContext on the database opened from its configuration")
}
func (*AzureBlobCSVDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}

func (*AzureBlobCSVDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "azure-csv" || strings.TrimSpace(parsed.Host) == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("invalid Azure Blob CSV URI; expected azure-csv://container/path/file.csv")
	}
	key, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil || !validAzureBlobCSVObjectKey(key) {
		return errors.New("invalid Azure Blob CSV URI; expected a safe relative .csv object key")
	}
	return nil
}

func (*AzureBlobCSVDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "azure-csv://"
	}
	return (&url.URL{Scheme: "azure-csv", Host: strings.TrimSpace(config.Database), Path: "/" + AzureBlobCSVObjectKey(config)}).String()
}

// NormalizeAzureBlobCSVConfig validates the single-object Azure Blob CSV connection profile.
func NormalizeAzureBlobCSVConfig(config *model.DataSourceConfig) (*model.DataSourceConfig, error) {
	if config == nil {
		return nil, errors.New("azure-blob-csv data source config is required")
	}
	container := strings.TrimSpace(config.Database)
	if container == "" || container == "." || container == ".." || strings.ContainsAny(container, "/\\") {
		return nil, errors.New("azure-blob-csv container must be one path-safe name in database")
	}
	if strings.TrimSpace(config.Username) == "" {
		return nil, errors.New("azure-blob-csv account name is required in username")
	}
	sasToken := configString(config.AdditionalProps, "sas_token")
	if sasToken == "" && strings.TrimSpace(config.Password) == "" {
		return nil, errors.New("azure-blob-csv account key or sas_token is required")
	}
	if sasToken != "" {
		parsed, err := url.ParseQuery(strings.TrimPrefix(sasToken, "?"))
		if err != nil || len(parsed) == 0 {
			return nil, errors.New("azure-blob-csv sas_token must be a valid query string")
		}
	}
	if !validAzureBlobCSVObjectKey(AzureBlobCSVObjectKey(config)) {
		return nil, errors.New("azure-blob-csv object_key must be a safe relative .csv object key")
	}
	endpoint := configString(config.AdditionalProps, "endpoint_url")
	if endpoint != "" {
		if err := validateAzureBlobEndpoint(endpoint); err != nil {
			return nil, err
		}
	}
	if _, err := azureBlobCSVMaxFileBytes(config); err != nil {
		return nil, err
	}
	delimiter := configString(config.AdditionalProps, "delimiter")
	if delimiter != "" && (!utf8.ValidString(delimiter) || len([]rune(delimiter)) != 1) {
		return nil, errors.New("azure-blob-csv delimiter must be exactly one character")
	}
	quote := configString(config.AdditionalProps, "quote_char")
	if quote != "" && quote != "\"" {
		return nil, errors.New("azure-blob-csv currently supports the standard double-quote CSV escape character")
	}
	if _, err := azureBlobCSVNonNegativeInt(config.AdditionalProps, "skip_header_rows"); err != nil {
		return nil, fmt.Errorf("azure-blob-csv skip_header_rows must be a non-negative integer: %w", err)
	}
	if value := strings.ToLower(configString(config.AdditionalProps, "has_header")); value != "" && value != "true" && value != "false" && value != "1" && value != "0" {
		return nil, errors.New("azure-blob-csv has_header must be true or false")
	}
	normalized := *config
	normalized.AdditionalProps = make(map[string]interface{}, len(config.AdditionalProps))
	for key, value := range config.AdditionalProps {
		normalized.AdditionalProps[key] = value
	}
	return &normalized, nil
}

func ValidateAzureBlobCSVConfig(config *model.DataSourceConfig) error {
	_, err := NormalizeAzureBlobCSVConfig(config)
	return err
}

func azureBlobCSVMaxFileBytes(config *model.DataSourceConfig) (int64, error) {
	if config == nil {
		return defaultAzureBlobCSVMaxFileBytes, nil
	}
	value, exists := config.AdditionalProps["max_file_bytes"]
	if !exists || value == nil {
		return defaultAzureBlobCSVMaxFileBytes, nil
	}
	parsed, err := parseAzureBlobConfigInt64(value)
	if err != nil {
		return 0, errors.New("azure-blob-csv max_file_bytes must be an integer")
	}
	if parsed <= 0 || parsed > maxAzureBlobCSVMaxFileBytes {
		return 0, fmt.Errorf("azure-blob-csv max_file_bytes must be between 1 and %d", maxAzureBlobCSVMaxFileBytes)
	}
	return parsed, nil
}

func azureBlobCSVNonNegativeInt(props map[string]interface{}, key string) (int, error) {
	value, exists := props[key]
	if !exists || value == nil {
		return 0, nil
	}
	parsed, err := parseAzureBlobConfigInt64(value)
	if err != nil || parsed < 0 || uint64(parsed) > uint64(^uint(0)>>1) {
		return 0, errors.New("invalid value")
	}
	return int(parsed), nil
}

func parseAzureBlobConfigInt64(value interface{}) (int64, error) {
	switch number := value.(type) {
	case int:
		return int64(number), nil
	case int64:
		return number, nil
	case int32:
		return int64(number), nil
	case uint:
		if uint64(number) > uint64(^uint64(0)>>1) {
			return 0, errors.New("integer overflows int64")
		}
		return int64(number), nil
	case uint64:
		if number > uint64(^uint64(0)>>1) {
			return 0, errors.New("integer overflows int64")
		}
		return int64(number), nil
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number ||
			number < math.MinInt64 || number >= float64(math.MaxInt64) {
			return 0, errors.New("value is not an int64 integer")
		}
		return int64(number), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(number), 10, 64)
	default:
		return 0, errors.New("unsupported value")
	}
}

func validAzureBlobCSVObjectKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" || path.IsAbs(key) || strings.Contains(key, "\\") || !strings.EqualFold(path.Ext(key), ".csv") {
		return false
	}
	for _, segment := range strings.Split(key, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func AzureBlobCSVObjectKey(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if key := configString(config.AdditionalProps, "object_key"); key != "" {
		return key
	}
	return strings.TrimSpace(config.Host)
}

func AzureBlobCSVTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "azure-blob-csv")
}

func AzureBlobCSVMaxFileBytes(config *model.DataSourceConfig) int64 {
	limit, _ := azureBlobCSVMaxFileBytes(config)
	return limit
}

func AzureBlobCSVDelimiter(config *model.DataSourceConfig) rune {
	value := configString(config.AdditionalProps, "delimiter")
	if value == "" {
		return ','
	}
	return []rune(value)[0]
}

func AzureBlobCSVHasHeader(config *model.DataSourceConfig) bool {
	value := strings.ToLower(configString(config.AdditionalProps, "has_header"))
	return value == "" || value == "true" || value == "1"
}

func AzureBlobCSVSkipHeaderRows(config *model.DataSourceConfig) int {
	value, err := azureBlobCSVNonNegativeInt(config.AdditionalProps, "skip_header_rows")
	if err != nil {
		return -1
	}
	return value
}
