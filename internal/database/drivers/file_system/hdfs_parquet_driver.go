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

const hdfsParquetMaxRows = 100000

// HDFSParquetDriver exposes one configured HDFS Parquet file through read-only SQL.
type HDFSParquetDriver struct {
	config     *HDFSConfig
	client     *HDFSClient
	fileClient HDFSCSVFileClient
}

func NewHDFSParquetDriver(ctx context.Context, config *HDFSConfig) (*HDFSParquetDriver, error) {
	if config == nil {
		return nil, errors.New("HDFS Parquet configuration is required")
	}
	client, err := NewHDFSClient(ctx, config)
	if err != nil {
		return nil, err
	}
	return &HDFSParquetDriver{config: config, client: client, fileClient: client}, nil
}

// NewHDFSParquetDriverWithFileClient injects a bounded HDFS file client for tests and embedding.
func NewHDFSParquetDriverWithFileClient(client HDFSCSVFileClient) *HDFSParquetDriver {
	return &HDFSParquetDriver{fileClient: client}
}

func (d *HDFSParquetDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("HDFS Parquet requires a configured read-only SQL adapter")
}

func (d *HDFSParquetDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	return d.openWithConfig(ctx, config, "hdfs-parquet")
}

func (d *HDFSParquetDriver) openWithConfig(ctx context.Context, config *model.DataSourceConfig, driverName string) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateHDFSParquetConfig(config); err != nil {
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
	tableName, err := HDFSParquetTableName(config)
	if err != nil {
		return nil, err
	}
	maxBytes := HDFSCSVMaxFileBytes(config)
	return object_storage.NewParquetSQLDatabase(object_storage.ParquetSQLDatabaseConfig{
		DriverName: driverName, ObjectKey: filePath, TableName: tableName,
		BatchSize: 1000, MaxFileBytes: maxBytes, MaxRows: hdfsParquetMaxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := client.GetFileInfo(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect HDFS Parquet file: %w", err)
		}
		if info == nil || info.IsDir {
			return errors.New("configured HDFS Parquet path is not a file")
		}
		if info.Size < 0 || info.Size > maxBytes {
			return fmt.Errorf("HDFS Parquet file is %d bytes; configured maximum is %d bytes", info.Size, maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := client.ReadFileLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read HDFS Parquet file: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, client.Close)
}

func (d *HDFSParquetDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "hdfs" || parsed.Host == "" || !path.IsAbs(parsed.Path) || !strings.EqualFold(path.Ext(parsed.Path), ".parquet") {
		return errors.New("invalid HDFS Parquet URI; expected hdfs://namenode/path/to/file.parquet")
	}
	return nil
}

func (*HDFSParquetDriver) GetDefaultPort() int { return 8020 }

func (d *HDFSParquetDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "hdfs://"
	}
	hdfsConfig, err := HDFSCSVClientConfig(config)
	if err != nil || len(hdfsConfig.NameNodes) == 0 {
		return "hdfs://"
	}
	return (&url.URL{Scheme: "hdfs", Host: hdfsConfig.NameNodes[0], Path: HDFSCSVFilePath(config)}).String()
}

func (*HDFSParquetDriver) GetDatabaseTypeName() string { return "hdfs-parquet" }
func (*HDFSParquetDriver) GetDriverName() string       { return "hdfs-parquet" }
func (*HDFSParquetDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryFileSystem
}
func (*HDFSParquetDriver) TestConnection(*sql.DB) error {
	return errors.New("use TestConnectionContext instead")
}
func (d *HDFSParquetDriver) TestConnectionContext(ctx context.Context) error {
	client := d.fileClient
	if client == nil && d.client != nil {
		client = d.client
	}
	if client == nil {
		return errors.New("HDFS Parquet driver is not initialized")
	}
	if _, err := client.GetCapacity(ctx); err != nil {
		return fmt.Errorf("failed to test HDFS connection: %w", err)
	}
	return nil
}
func (*HDFSParquetDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*HDFSParquetDriver) ConfigureAuth(interface{}) error { return nil }

func (*HDFSParquetDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
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

func (d *HDFSParquetDriver) ListParquetFiles(ctx context.Context, directory string) ([]HDFSFileInfo, error) {
	client := d.fileClient
	if client == nil && d.client != nil {
		client = d.client
	}
	if client == nil {
		return nil, errors.New("HDFS Parquet driver is not initialized")
	}
	files, err := client.ListFiles(ctx, directory)
	if err != nil {
		return nil, err
	}
	result := make([]HDFSFileInfo, 0, len(files))
	for _, file := range files {
		if !file.IsDir && strings.EqualFold(path.Ext(file.Name), ".parquet") {
			result = append(result, file)
		}
	}
	return result, nil
}

// QueryParquetFile returns parsed rows for legacy callers; unified SQL uses OpenWithConfig.
func (d *HDFSParquetDriver) QueryParquetFile(ctx context.Context, filePath string) (*HDFSParquetResult, error) {
	client := d.fileClient
	if client == nil && d.client != nil {
		client = d.client
	}
	if client == nil {
		return nil, errors.New("HDFS Parquet driver is not initialized")
	}
	data, err := client.ReadFileLimited(ctx, filePath, defaultHDFSCSVMaxFileBytes)
	if err != nil {
		return nil, fmt.Errorf("read HDFS Parquet file: %w", err)
	}
	tableName := strings.TrimSuffix(path.Base(filePath), path.Ext(filePath))
	if tableName == "" || tableName == "." {
		tableName = "records"
	}
	db, err := object_storage.NewParquetSQLDatabase(object_storage.ParquetSQLDatabaseConfig{
		DriverName: "hdfs-parquet", ObjectKey: filePath, TableName: tableName,
		BatchSize: 1000, MaxFileBytes: defaultHDFSCSVMaxFileBytes, MaxRows: hdfsParquetMaxRows,
	}, func(ctx context.Context, key string) error {
		_, err := client.GetFileInfo(ctx, key)
		return err
	}, func(context.Context, string) ([]byte, error) { return data, nil }, nil)
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
	result := &HDFSParquetResult{Rows: []map[string]interface{}{}, FilePath: filePath, BytesRead: int64(len(data))}
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

func (d *HDFSParquetDriver) GetBlockLocations(ctx context.Context, filePath string) ([]HDFSBlockLocation, error) {
	if d.client == nil {
		return nil, errors.New("HDFS block-location client is not initialized")
	}
	return d.client.GetBlockLocations(ctx, filePath)
}
func (d *HDFSParquetDriver) GetCapacity(ctx context.Context) (*HDFSCapacity, error) {
	client := d.fileClient
	if client == nil && d.client != nil {
		client = d.client
	}
	if client == nil {
		return nil, errors.New("HDFS Parquet driver is not initialized")
	}
	return client.GetCapacity(ctx)
}

func ValidateHDFSParquetConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return errors.New("HDFS Parquet data source config is required")
	}
	if _, err := HDFSCSVClientConfig(config); err != nil {
		return err
	}
	filePath, err := cleanHDFSCSVPath(HDFSCSVFilePath(config))
	if err != nil {
		return err
	}
	if !strings.EqualFold(path.Ext(filePath), ".parquet") {
		return errors.New("HDFS Parquet file_path must have a .parquet extension")
	}
	if _, err := HDFSParquetTableName(config); err != nil {
		return err
	}
	if _, present := configProp(config, "max_file_bytes"); present {
		if _, err := configPropInt64(config, "max_file_bytes"); err != nil {
			return errors.New("HDFS Parquet max_file_bytes must be an integer")
		}
	}
	maxBytes := HDFSCSVMaxFileBytes(config)
	if maxBytes <= 0 || maxBytes > maxHDFSCSVMaxFileBytes {
		return fmt.Errorf("HDFS Parquet max_file_bytes must be between 1 and %d", maxHDFSCSVMaxFileBytes)
	}
	return nil
}

func HDFSParquetTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("HDFS Parquet data source config is required")
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
		return "", errors.New("HDFS Parquet table_name could not be inferred; set additionalProps.table_name")
	}
	return name, nil
}

func HDFSParquetMaxRows() int { return hdfsParquetMaxRows }

type HDFSParquetResult struct {
	Rows      []map[string]interface{}
	NumRows   int64
	BytesRead int64
	FilePath  string
}
