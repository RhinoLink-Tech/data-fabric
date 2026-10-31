package file_system

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const (
	defaultHDFSCSVMaxFileBytes int64 = 16 << 20
	maxHDFSCSVMaxFileBytes     int64 = 64 << 20
)

// HDFSCSVFileClient is the bounded read surface needed by the HDFS CSV adapter.
type HDFSCSVFileClient interface {
	ReadFileLimited(context.Context, string, int64) ([]byte, error)
	GetFileInfo(context.Context, string) (*HDFSFileInfo, error)
	ListFiles(context.Context, string) ([]HDFSFileInfo, error)
	GetCapacity(context.Context) (*HDFSCapacity, error)
	Close() error
}

// HDFSCSVDriver exposes one configured HDFS CSV file through read-only SQL.
type HDFSCSVDriver struct {
	config *HDFSConfig
	client HDFSCSVFileClient
}

type reopenableHDFSCSVFileClient struct {
	mu      sync.Mutex
	cond    *sync.Cond
	client  HDFSCSVFileClient
	factory func(context.Context) (HDFSCSVFileClient, error)
	active  int
	closing bool
}

func newReopenableHDFSCSVFileClient(factory func(context.Context) (HDFSCSVFileClient, error)) *reopenableHDFSCSVFileClient {
	client := &reopenableHDFSCSVFileClient{factory: factory}
	client.cond = sync.NewCond(&client.mu)
	return client
}

func (c *reopenableHDFSCSVFileClient) withClient(ctx context.Context, operation func(HDFSCSVFileClient) error) error {
	c.mu.Lock()
	for c.closing {
		c.cond.Wait()
	}
	if c.client == nil {
		client, err := c.factory(ctx)
		if err != nil {
			c.mu.Unlock()
			return err
		}
		c.client = client
	}
	client := c.client
	c.active++
	c.mu.Unlock()

	err := operation(client)
	c.mu.Lock()
	c.active--
	if c.active == 0 {
		c.cond.Broadcast()
	}
	c.mu.Unlock()
	return err
}

func (c *reopenableHDFSCSVFileClient) ReadFileLimited(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	var data []byte
	err := c.withClient(ctx, func(client HDFSCSVFileClient) error {
		var err error
		data, err = client.ReadFileLimited(ctx, path, maxBytes)
		return err
	})
	return data, err
}
func (c *reopenableHDFSCSVFileClient) GetFileInfo(ctx context.Context, path string) (*HDFSFileInfo, error) {
	var info *HDFSFileInfo
	err := c.withClient(ctx, func(client HDFSCSVFileClient) error {
		var err error
		info, err = client.GetFileInfo(ctx, path)
		return err
	})
	return info, err
}
func (c *reopenableHDFSCSVFileClient) ListFiles(ctx context.Context, path string) ([]HDFSFileInfo, error) {
	var files []HDFSFileInfo
	err := c.withClient(ctx, func(client HDFSCSVFileClient) error {
		var err error
		files, err = client.ListFiles(ctx, path)
		return err
	})
	return files, err
}
func (c *reopenableHDFSCSVFileClient) GetCapacity(ctx context.Context) (*HDFSCapacity, error) {
	var capacity *HDFSCapacity
	err := c.withClient(ctx, func(client HDFSCSVFileClient) error {
		var err error
		capacity, err = client.GetCapacity(ctx)
		return err
	})
	return capacity, err
}
func (c *reopenableHDFSCSVFileClient) Close() error {
	c.mu.Lock()
	for c.closing {
		c.cond.Wait()
	}
	c.closing = true
	for c.active != 0 {
		c.cond.Wait()
	}
	client := c.client
	var err error
	if client != nil {
		err = client.Close()
		c.client = nil
	}
	c.closing = false
	c.cond.Broadcast()
	c.mu.Unlock()
	return err
}

func NewHDFSCSVDriver(ctx context.Context, config *HDFSConfig) (*HDFSCSVDriver, error) {
	if config == nil {
		return nil, errors.New("HDFS CSV configuration is required")
	}
	client, err := NewHDFSClient(ctx, config)
	if err != nil {
		return nil, err
	}
	return &HDFSCSVDriver{config: config, client: client}, nil
}

// NewHDFSCSVDriverWithFileClient injects a file client for embedding and tests.
func NewHDFSCSVDriverWithFileClient(client HDFSCSVFileClient) *HDFSCSVDriver {
	return &HDFSCSVDriver{client: client}
}

func (d *HDFSCSVDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("HDFS CSV requires a configured read-only SQL adapter")
}

func (d *HDFSCSVDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateHDFSCSVConfig(config); err != nil {
		return nil, err
	}
	client := d.client
	if client == nil {
		hdfsConfig, err := HDFSCSVClientConfig(config)
		if err != nil {
			return nil, err
		}
		client = newReopenableHDFSCSVFileClient(func(ctx context.Context) (HDFSCSVFileClient, error) {
			return NewHDFSClient(ctx, hdfsConfig)
		})
	}
	filePath := HDFSCSVFilePath(config)
	tableName, err := HDFSCSVTableName(config)
	if err != nil {
		return nil, err
	}
	maxBytes := HDFSCSVMaxFileBytes(config)
	return object_storage.NewCSVSQLDatabase(object_storage.CSVSQLDatabaseConfig{
		DriverName: "hdfs-csv", ObjectKey: filePath, TableName: tableName,
		Delimiter: HDFSCSVDelimiter(config), HasHeader: hdfsCSVHasHeader(config),
		SkipHeaderRows: hdfsCSVSkipHeaderRows(config), NullValues: hdfsCSVNullValues(config), MaxRows: HDFSCSVMaxRows(),
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := client.GetFileInfo(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect HDFS CSV file: %w", err)
		}
		if info.IsDir {
			return errors.New("configured HDFS CSV path is a directory")
		}
		if info.Size < 0 || info.Size > maxBytes {
			return fmt.Errorf("HDFS CSV file is %d bytes; configured maximum is %d bytes", info.Size, maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := client.ReadFileLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read HDFS CSV file: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, client.Close)
}

func (d *HDFSCSVDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "hdfs" || parsed.Host == "" || parsed.Path == "" || !path.IsAbs(parsed.Path) {
		return errors.New("invalid HDFS CSV URI; expected hdfs://namenode/path/to/file.csv")
	}
	return nil
}

func (d *HDFSCSVDriver) GetDefaultPort() int { return 8020 }

func (d *HDFSCSVDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "hdfs://"
	}
	filePath := HDFSCSVFilePath(config)
	hdfsConfig, err := HDFSCSVClientConfig(config)
	if err != nil || len(hdfsConfig.NameNodes) == 0 {
		return "hdfs://"
	}
	return "hdfs://" + hdfsConfig.NameNodes[0] + filePath
}

func (d *HDFSCSVDriver) GetDatabaseTypeName() string { return "hdfs-csv" }
func (d *HDFSCSVDriver) GetDriverName() string       { return "hdfs-csv" }
func (d *HDFSCSVDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryFileSystem
}
func (d *HDFSCSVDriver) TestConnection(*sql.DB) error {
	return errors.New("use TestConnectionContext instead")
}
func (d *HDFSCSVDriver) TestConnectionContext(ctx context.Context) error {
	if d == nil || d.client == nil {
		return errors.New("HDFS CSV driver is not initialized")
	}
	_, err := d.client.GetCapacity(ctx)
	if err != nil {
		return fmt.Errorf("failed to test HDFS connection: %w", err)
	}
	return nil
}
func (d *HDFSCSVDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (d *HDFSCSVDriver) ConfigureAuth(interface{}) error { return nil }

func (d *HDFSCSVDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	if batchSize <= 0 {
		return "", errors.New("batch size must be positive")
	}
	query = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(query), ";"))
	if query == "" {
		return "", errors.New("query is required")
	}
	if offset < 0 {
		offset = 0
	}
	return fmt.Sprintf("SELECT * FROM (%s) AS __data_fabric_page LIMIT %d OFFSET %d", query, batchSize, offset), nil
}

func (d *HDFSCSVDriver) ListCSVFiles(ctx context.Context, directory string) ([]HDFSFileInfo, error) {
	if d == nil || d.client == nil {
		return nil, errors.New("HDFS CSV driver is not initialized")
	}
	files, err := d.client.ListFiles(ctx, directory)
	if err != nil {
		return nil, err
	}
	result := make([]HDFSFileInfo, 0, len(files))
	for _, file := range files {
		if !file.IsDir && strings.EqualFold(path.Ext(file.Name), ".csv") {
			result = append(result, file)
		}
	}
	return result, nil
}

// QueryCSVFile returns parsed rows for legacy callers; unified SQL uses OpenWithConfig.
func (d *HDFSCSVDriver) QueryCSVFile(ctx context.Context, filePath string, delimiter rune, hasHeader bool) (*HDFSCSVResult, error) {
	if d == nil || d.client == nil {
		return nil, errors.New("HDFS CSV driver is not initialized")
	}
	if delimiter == 0 {
		delimiter = ','
	}
	data, err := d.client.ReadFileLimited(ctx, filePath, defaultHDFSCSVMaxFileBytes)
	if err != nil {
		return nil, fmt.Errorf("read HDFS CSV file: %w", err)
	}
	db, err := object_storage.NewCSVSQLDatabase(object_storage.CSVSQLDatabaseConfig{
		DriverName: "hdfs-csv", ObjectKey: filePath, TableName: "records",
		Delimiter: delimiter, HasHeader: hasHeader, MaxRows: HDFSCSVMaxRows(),
	}, func(ctx context.Context, key string) error {
		_, err := d.client.GetFileInfo(ctx, key)
		return err
	}, func(context.Context, string) ([]byte, error) { return data, nil }, nil)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT * FROM records")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := &HDFSCSVResult{Rows: []map[string]interface{}{}, FilePath: filePath, BytesRead: int64(len(data))}
	for rows.Next() {
		values := make([]interface{}, len(columns))
		dest := make([]interface{}, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		row := make(map[string]interface{}, len(columns))
		for i, column := range columns {
			row[column] = values[i]
		}
		result.Rows = append(result.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result.NumRows = int64(len(result.Rows))
	return result, nil
}

// HDFSCSVClientConfig maps the common source configuration to native HDFS client settings.
func HDFSCSVClientConfig(config *model.DataSourceConfig) (*HDFSConfig, error) {
	if config == nil {
		return nil, errors.New("HDFS CSV data source config is required")
	}
	port := config.Port
	if port <= 0 {
		port = 8020
	}
	addresses, err := hdfsCSVNameNodeAddresses(config, port)
	if err != nil {
		return nil, err
	}
	return &HDFSConfig{NameNodes: addresses, Username: strings.TrimSpace(config.Username)}, nil
}

func ValidateHDFSCSVConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return errors.New("HDFS CSV data source config is required")
	}
	if _, err := HDFSCSVClientConfig(config); err != nil {
		return err
	}
	filePath, err := cleanHDFSCSVPath(HDFSCSVFilePath(config))
	if err != nil {
		return err
	}
	if !strings.EqualFold(path.Ext(filePath), ".csv") {
		return errors.New("HDFS CSV file_path must have a .csv extension")
	}
	if _, err := HDFSCSVTableName(config); err != nil {
		return err
	}
	delimiter := configPropString(config, "delimiter")
	if delimiter != "" && len([]rune(delimiter)) != 1 {
		return errors.New("HDFS CSV delimiter must be exactly one character")
	}
	quote := configPropString(config, "quote_char")
	if quote != "" && quote != "\"" {
		return errors.New("HDFS CSV currently supports the standard double-quote escape character")
	}
	if _, present := configProp(config, "skip_header_rows"); present {
		if _, err := configPropInt64(config, "skip_header_rows"); err != nil {
			return errors.New("HDFS CSV skip_header_rows must be a non-negative integer")
		}
	}
	if hdfsCSVSkipHeaderRows(config) < 0 {
		return errors.New("HDFS CSV skip_header_rows cannot be negative")
	}
	if value := strings.ToLower(configPropString(config, "has_header")); value != "" && value != "true" && value != "false" && value != "1" && value != "0" {
		return errors.New("HDFS CSV has_header must be true or false")
	}
	if _, present := configProp(config, "max_file_bytes"); present {
		if _, err := configPropInt64(config, "max_file_bytes"); err != nil {
			return errors.New("HDFS CSV max_file_bytes must be an integer")
		}
	}
	maxBytes := HDFSCSVMaxFileBytes(config)
	if maxBytes <= 0 || maxBytes > maxHDFSCSVMaxFileBytes {
		return fmt.Errorf("HDFS CSV max_file_bytes must be between 1 and %d", maxHDFSCSVMaxFileBytes)
	}
	return nil
}

func HDFSCSVFilePath(config *model.DataSourceConfig) string {
	if config == nil {
		return ""
	}
	if value := configPropString(config, "file_path"); value != "" {
		return value
	}
	return strings.TrimSpace(config.Database)
}

func HDFSCSVTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("HDFS CSV data source config is required")
	}
	if value := configPropString(config, "table_name"); value != "" {
		return value, nil
	}
	filePath, err := cleanHDFSCSVPath(HDFSCSVFilePath(config))
	if err != nil {
		return "", err
	}
	name := strings.TrimSuffix(path.Base(filePath), path.Ext(filePath))
	if name == "" || name == "." || name == "/" {
		return "", errors.New("HDFS CSV table_name could not be inferred; set additionalProps.table_name")
	}
	return name, nil
}

func HDFSCSVDelimiter(config *model.DataSourceConfig) rune {
	value := configPropString(config, "delimiter")
	if value == "" {
		return ','
	}
	return []rune(value)[0]
}

func HDFSCSVHasHeader(config *model.DataSourceConfig) bool { return hdfsCSVHasHeader(config) }
func HDFSCSVMaxFileBytes(config *model.DataSourceConfig) int64 {
	if config == nil {
		return defaultHDFSCSVMaxFileBytes
	}
	value, present := configProp(config, "max_file_bytes")
	if !present {
		return defaultHDFSCSVMaxFileBytes
	}
	parsed, err := parseHDFSCSVInt64(value)
	if err != nil {
		return 0
	}
	return parsed
}

func hdfsCSVHasHeader(config *model.DataSourceConfig) bool {
	value := strings.ToLower(configPropString(config, "has_header"))
	return value == "" || value == "true" || value == "1"
}
func hdfsCSVSkipHeaderRows(config *model.DataSourceConfig) int {
	value, present := configProp(config, "skip_header_rows")
	if !present {
		return 0
	}
	parsed, err := parseHDFSCSVInt64(value)
	if err != nil || parsed > int64(^uint(0)>>1) {
		return -1
	}
	return int(parsed)
}
func hdfsCSVNullValues(config *model.DataSourceConfig) []string {
	if config == nil || config.AdditionalProps == nil {
		return nil
	}
	switch value := config.AdditionalProps["null_values"].(type) {
	case []string:
		return append([]string(nil), value...)
	case []interface{}:
		result := make([]string, 0, len(value))
		for _, item := range value {
			if item != nil {
				result = append(result, fmt.Sprint(item))
			}
		}
		return result
	case string:
		if strings.TrimSpace(value) != "" {
			return strings.Split(value, ",")
		}
	}
	return nil
}

func configProp(config *model.DataSourceConfig, key string) (interface{}, bool) {
	if config == nil || config.AdditionalProps == nil {
		return nil, false
	}
	value, ok := config.AdditionalProps[key]
	return value, ok && value != nil
}

func configPropString(config *model.DataSourceConfig, key string) string {
	value, ok := configProp(config, key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func configPropInt64(config *model.DataSourceConfig, key string) (int64, error) {
	value, ok := configProp(config, key)
	if !ok {
		return 0, errors.New("property is not set")
	}
	return parseHDFSCSVInt64(value)
}

func parseHDFSCSVInt64(value interface{}) (int64, error) {
	switch value := value.(type) {
	case int:
		return int64(value), nil
	case int8:
		return int64(value), nil
	case int16:
		return int64(value), nil
	case int32:
		return int64(value), nil
	case int64:
		return value, nil
	case uint:
		if uint64(value) > uint64(^uint64(0)>>1) {
			return 0, errors.New("integer overflows int64")
		}
		return int64(value), nil
	case uint8:
		return int64(value), nil
	case uint16:
		return int64(value), nil
	case uint32:
		return int64(value), nil
	case uint64:
		if value > uint64(^uint64(0)>>1) {
			return 0, errors.New("integer overflows int64")
		}
		return int64(value), nil
	case float32:
		f := float64(value)
		if f >= float64(uint64(1)<<63) || f < -float64(uint64(1)<<63) || math.Trunc(f) != f {
			return 0, errors.New("number is not an int64")
		}
		return int64(f), nil
	case float64:
		if value >= float64(uint64(1)<<63) || value < -float64(uint64(1)<<63) || math.Trunc(value) != value {
			return 0, errors.New("number is not an int64")
		}
		return int64(value), nil
	case json.Number:
		return value.Int64()
	case string:
		return strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	default:
		return 0, fmt.Errorf("unsupported integer property type %T", value)
	}
}

func cleanHDFSCSVPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !path.IsAbs(value) || strings.Contains(value, "\\") {
		return "", errors.New("HDFS CSV file_path must be an absolute HDFS path")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." || segment == "." {
			return "", errors.New("HDFS CSV file_path contains an invalid path segment")
		}
	}
	cleaned := path.Clean(value)
	if cleaned == "/" {
		return "", errors.New("HDFS CSV file_path must name a file")
	}
	return cleaned, nil
}

func hdfsCSVNameNodeAddresses(config *model.DataSourceConfig, port int) ([]string, error) {
	var values []string
	if config != nil && config.AdditionalProps != nil {
		switch names := config.AdditionalProps["namenodes"].(type) {
		case string:
			for _, value := range strings.Split(names, ",") {
				if strings.TrimSpace(value) != "" {
					values = append(values, strings.TrimSpace(value))
				}
			}
		case []string:
			values = append(values, names...)
		case []interface{}:
			for _, value := range names {
				if value != nil && strings.TrimSpace(fmt.Sprint(value)) != "" {
					values = append(values, strings.TrimSpace(fmt.Sprint(value)))
				}
			}
		}
	}
	if len(values) == 0 && config != nil && strings.TrimSpace(config.Host) != "" {
		for _, value := range strings.Split(config.Host, ",") {
			values = append(values, strings.TrimSpace(value))
		}
	}
	if len(values) == 0 {
		return nil, errors.New("HDFS CSV namenode host is required")
	}
	addresses := make([]string, 0, len(values))
	for _, value := range values {
		address := strings.TrimSpace(value)
		if address == "" || strings.Contains(address, "://") {
			return nil, fmt.Errorf("invalid HDFS NameNode address %q", value)
		}
		if _, _, err := net.SplitHostPort(address); err == nil {
			addresses = append(addresses, address)
			continue
		}
		if strings.HasPrefix(address, "[") && strings.HasSuffix(address, "]") {
			address = strings.Trim(address, "[]")
		}
		if ip := net.ParseIP(address); ip == nil && strings.Contains(address, ":") {
			return nil, fmt.Errorf("invalid HDFS NameNode address %q", value)
		}
		addresses = append(addresses, net.JoinHostPort(address, strconv.Itoa(port)))
	}
	return addresses, nil
}

func HDFSCSVMaxRows() int { return 100000 }

type HDFSCSVResult struct {
	Rows      []map[string]interface{}
	NumRows   int64
	BytesRead int64
	FilePath  string
}
