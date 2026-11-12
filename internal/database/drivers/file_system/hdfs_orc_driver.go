package file_system

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

const hdfsORCMaxRows = 100000

// HDFSORCDriver exposes one configured ORC Object Container File through read-only SQL.
type HDFSORCDriver struct {
	config     *HDFSConfig
	client     *HDFSClient
	fileClient HDFSCSVFileClient
}

func NewHDFSORCDriver(ctx context.Context, config *HDFSConfig) (*HDFSORCDriver, error) {
	if config == nil {
		return nil, errors.New("HDFS ORC configuration is required")
	}
	client, err := NewHDFSClient(ctx, config)
	if err != nil {
		return nil, err
	}
	return &HDFSORCDriver{config: config, client: client, fileClient: client}, nil
}

// NewHDFSORCDriverWithFileClient injects a bounded HDFS file client for tests and embedding.
func NewHDFSORCDriverWithFileClient(client HDFSCSVFileClient) *HDFSORCDriver {
	return &HDFSORCDriver{fileClient: client}
}

func (*HDFSORCDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("HDFS ORC requires a configured read-only SQL adapter")
}

func (d *HDFSORCDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateHDFSORCConfig(config); err != nil {
		return nil, err
	}
	client := d.fileClient
	if client == nil && d.client != nil {
		client = d.client
	}
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
	tableName, err := HDFSORCTableName(config)
	if err != nil {
		return nil, err
	}
	maxBytes := HDFSCSVMaxFileBytes(config)
	return object_storage.NewORCSQLDatabase(object_storage.ORCSQLDatabaseConfig{
		DriverName: "hdfs-orc", ObjectKey: filePath, TableName: tableName,
		BatchSize: 1000, MaxFileBytes: maxBytes, MaxRows: hdfsORCMaxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := client.GetFileInfo(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect HDFS ORC file: %w", err)
		}
		if info == nil || info.IsDir {
			return errors.New("configured HDFS ORC path is not a file")
		}
		if info.Size < 0 || info.Size > maxBytes {
			return fmt.Errorf("HDFS ORC file is %d bytes; configured maximum is %d bytes", info.Size, maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := client.ReadFileLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read HDFS ORC file: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, client.Close)
}

func (*HDFSORCDriver) GetDefaultPort() int { return 8020 }
func (d *HDFSORCDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "hdfs://"
	}
	hdfsConfig, err := HDFSCSVClientConfig(config)
	if err != nil || len(hdfsConfig.NameNodes) == 0 {
		return "hdfs://"
	}
	return (&url.URL{Scheme: "hdfs", Host: hdfsConfig.NameNodes[0], Path: HDFSCSVFilePath(config)}).String()
}
func (*HDFSORCDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "hdfs" || parsed.Host == "" || !path.IsAbs(parsed.Path) || !strings.EqualFold(path.Ext(parsed.Path), ".orc") {
		return errors.New("invalid HDFS ORC URI; expected hdfs://namenode/path/to/file.orc")
	}
	return nil
}
func (*HDFSORCDriver) GetDatabaseTypeName() string { return "hdfs-orc" }
func (*HDFSORCDriver) GetDriverName() string       { return "hdfs-orc" }
func (*HDFSORCDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryFileSystem
}
func (*HDFSORCDriver) TestConnection(*sql.DB) error {
	return errors.New("use TestConnectionContext instead")
}
func (d *HDFSORCDriver) TestConnectionContext(ctx context.Context) error {
	client := d.fileClient
	if client == nil && d.client != nil {
		client = d.client
	}
	if client == nil {
		return errors.New("HDFS ORC driver is not initialized")
	}
	if _, err := client.GetCapacity(ctx); err != nil {
		return fmt.Errorf("failed to test HDFS connection: %w", err)
	}
	return nil
}
func (*HDFSORCDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*HDFSORCDriver) ConfigureAuth(interface{}) error { return nil }

func (*HDFSORCDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
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

func (d *HDFSORCDriver) boundedFileClient() HDFSCSVFileClient {
	if d.fileClient != nil {
		return d.fileClient
	}
	return d.client
}

func (d *HDFSORCDriver) GetCapacity(ctx context.Context) (*HDFSCapacity, error) {
	client := d.boundedFileClient()
	if client == nil {
		return nil, errors.New("HDFS ORC driver is not initialized")
	}
	return client.GetCapacity(ctx)
}

// ListORCFiles lists only non-directory ORC paths returned by the HDFS file client.
func (d *HDFSORCDriver) ListORCFiles(ctx context.Context, directory string) ([]HDFSFileInfo, error) {
	client := d.boundedFileClient()
	if client == nil {
		return nil, errors.New("HDFS ORC driver is not initialized")
	}
	cleanDirectory, err := cleanHDFSCSVPath(directory)
	if err != nil {
		return nil, err
	}
	files, err := client.ListFiles(ctx, cleanDirectory)
	if err != nil {
		return nil, err
	}
	result := make([]HDFSFileInfo, 0, len(files))
	for _, file := range files {
		if !file.IsDir && strings.EqualFold(path.Ext(file.Name), ".orc") {
			result = append(result, file)
		}
	}
	return result, nil
}

// QueryORCFile preserves the legacy row-result API while using the shared ORC SQL adapter.
func (d *HDFSORCDriver) QueryORCFile(ctx context.Context, filePath string) (*HDFSORCResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	client := d.boundedFileClient()
	if client == nil {
		return nil, errors.New("HDFS ORC driver is not initialized")
	}
	cleanPath, err := cleanHDFSCSVPath(filePath)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(path.Ext(cleanPath), ".orc") {
		return nil, errors.New("HDFS ORC file_path must have an .orc extension")
	}
	maxBytes := defaultHDFSCSVMaxFileBytes
	info, err := client.GetFileInfo(ctx, cleanPath)
	if err != nil {
		return nil, fmt.Errorf("inspect HDFS ORC file: %w", err)
	}
	if info == nil || info.IsDir {
		return nil, errors.New("configured HDFS ORC path is not a file")
	}
	if info.Size < 0 || info.Size > maxBytes {
		return nil, fmt.Errorf("HDFS ORC file is %d bytes; configured maximum is %d bytes", info.Size, maxBytes)
	}
	data, err := client.ReadFileLimited(ctx, cleanPath, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("read HDFS ORC file: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("HDFS ORC file is %d bytes; configured maximum is %d bytes", len(data), maxBytes)
	}
	tableName := strings.TrimSuffix(path.Base(cleanPath), path.Ext(cleanPath))
	db, err := object_storage.NewORCSQLDatabase(object_storage.ORCSQLDatabaseConfig{
		DriverName: "hdfs-orc", ObjectKey: cleanPath, TableName: tableName,
		BatchSize: 1000, MaxFileBytes: maxBytes, MaxRows: hdfsORCMaxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		_, err := client.GetFileInfo(ctx, key)
		return err
	}, func(ctx context.Context, key string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, nil)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	quote := string(rune(96))
	quotedTable := quote + strings.ReplaceAll(tableName, quote, quote+quote) + quote
	rows, err := db.QueryContext(ctx, "SELECT * FROM "+quotedTable)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	result := &HDFSORCResult{Rows: []map[string]interface{}{}, FilePath: cleanPath, BytesRead: int64(len(data))}
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

func ValidateHDFSORCConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return errors.New("HDFS ORC data source config is required")
	}
	if _, err := HDFSCSVClientConfig(config); err != nil {
		return err
	}
	filePath, err := cleanHDFSCSVPath(HDFSCSVFilePath(config))
	if err != nil {
		return err
	}
	if !strings.EqualFold(path.Ext(filePath), ".orc") {
		return errors.New("HDFS ORC file_path must have an .orc extension")
	}
	if _, err := HDFSORCTableName(config); err != nil {
		return err
	}
	if _, present := configProp(config, "max_file_bytes"); present {
		if _, err := configPropInt64(config, "max_file_bytes"); err != nil {
			return errors.New("HDFS ORC max_file_bytes must be an integer")
		}
	}
	maxBytes := HDFSCSVMaxFileBytes(config)
	if maxBytes <= 0 || maxBytes > maxHDFSCSVMaxFileBytes {
		return fmt.Errorf("HDFS ORC max_file_bytes must be between 1 and %d", maxHDFSCSVMaxFileBytes)
	}
	return nil
}

func HDFSORCTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("HDFS ORC data source config is required")
	}
	if name := configPropString(config, "table_name"); name != "" {
		return name, nil
	}
	filePath, err := cleanHDFSCSVPath(HDFSCSVFilePath(config))
	if err != nil {
		return "", err
	}
	name := strings.TrimSuffix(path.Base(filePath), path.Ext(filePath))
	if name == "" || name == "." || name == "/" {
		return "", errors.New("HDFS ORC table_name could not be inferred; set additionalProps.table_name")
	}
	return name, nil
}

func HDFSORCMaxRows() int { return hdfsORCMaxRows }

// HDFSORCResult preserves the legacy result envelope for HDFS ORC callers.
type HDFSORCResult struct {
	Rows      []map[string]interface{}
	NumRows   int64
	BytesRead int64
	FilePath  string
}
