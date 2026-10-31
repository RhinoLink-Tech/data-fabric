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

const hdfsAvroMaxRows = 100000

// HDFSAvroDriver exposes one configured Avro Object Container File through read-only SQL.
type HDFSAvroDriver struct {
	config     *HDFSConfig
	client     *HDFSClient
	fileClient HDFSCSVFileClient
}

func NewHDFSAvroDriver(ctx context.Context, config *HDFSConfig) (*HDFSAvroDriver, error) {
	if config == nil {
		return nil, errors.New("HDFS Avro configuration is required")
	}
	client, err := NewHDFSClient(ctx, config)
	if err != nil {
		return nil, err
	}
	return &HDFSAvroDriver{config: config, client: client, fileClient: client}, nil
}

// NewHDFSAvroDriverWithFileClient injects a bounded HDFS file client for tests and embedding.
func NewHDFSAvroDriverWithFileClient(client HDFSCSVFileClient) *HDFSAvroDriver {
	return &HDFSAvroDriver{fileClient: client}
}

func (*HDFSAvroDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("HDFS Avro requires a configured read-only SQL adapter")
}

func (d *HDFSAvroDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateHDFSAvroConfig(config); err != nil {
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
	tableName, err := HDFSAvroTableName(config)
	if err != nil {
		return nil, err
	}
	maxBytes := HDFSCSVMaxFileBytes(config)
	return object_storage.NewAvroSQLDatabase(object_storage.AvroSQLDatabaseConfig{
		DriverName: "hdfs-avro", ObjectKey: filePath, TableName: tableName,
		BatchSize: 1000, MaxFileBytes: maxBytes, MaxRows: hdfsAvroMaxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := client.GetFileInfo(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect HDFS Avro OCF file: %w", err)
		}
		if info == nil || info.IsDir {
			return errors.New("configured HDFS Avro path is not a file")
		}
		if info.Size < 0 || info.Size > maxBytes {
			return fmt.Errorf("HDFS Avro OCF file is %d bytes; configured maximum is %d bytes", info.Size, maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := client.ReadFileLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read HDFS Avro OCF file: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, client.Close)
}

func (*HDFSAvroDriver) GetDefaultPort() int { return 8020 }
func (d *HDFSAvroDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "hdfs://"
	}
	hdfsConfig, err := HDFSCSVClientConfig(config)
	if err != nil || len(hdfsConfig.NameNodes) == 0 {
		return "hdfs://"
	}
	return (&url.URL{Scheme: "hdfs", Host: hdfsConfig.NameNodes[0], Path: HDFSCSVFilePath(config)}).String()
}
func (*HDFSAvroDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "hdfs" || parsed.Host == "" || !path.IsAbs(parsed.Path) || !strings.EqualFold(path.Ext(parsed.Path), ".avro") {
		return errors.New("invalid HDFS Avro URI; expected hdfs://namenode/path/to/file.avro")
	}
	return nil
}
func (*HDFSAvroDriver) GetDatabaseTypeName() string { return "hdfs-avro" }
func (*HDFSAvroDriver) GetDriverName() string       { return "hdfs-avro" }
func (*HDFSAvroDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryFileSystem
}
func (*HDFSAvroDriver) TestConnection(*sql.DB) error {
	return errors.New("use TestConnectionContext instead")
}
func (d *HDFSAvroDriver) TestConnectionContext(ctx context.Context) error {
	client := d.fileClient
	if client == nil && d.client != nil {
		client = d.client
	}
	if client == nil {
		return errors.New("HDFS Avro driver is not initialized")
	}
	if _, err := client.GetCapacity(ctx); err != nil {
		return fmt.Errorf("failed to test HDFS connection: %w", err)
	}
	return nil
}
func (*HDFSAvroDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*HDFSAvroDriver) ConfigureAuth(interface{}) error { return nil }

func (*HDFSAvroDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
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

func (d *HDFSAvroDriver) ListAvroFiles(ctx context.Context, directory string) ([]HDFSFileInfo, error) {
	client := d.fileClient
	if client == nil && d.client != nil {
		client = d.client
	}
	if client == nil {
		return nil, errors.New("HDFS Avro driver is not initialized")
	}
	files, err := client.ListFiles(ctx, directory)
	if err != nil {
		return nil, err
	}
	result := make([]HDFSFileInfo, 0, len(files))
	for _, file := range files {
		if !file.IsDir && strings.EqualFold(path.Ext(file.Name), ".avro") {
			result = append(result, file)
		}
	}
	return result, nil
}

// QueryAvroFile returns parsed rows for legacy callers; unified SQL uses OpenWithConfig.
func (d *HDFSAvroDriver) QueryAvroFile(ctx context.Context, filePath string) (*HDFSAvroResult, error) {
	client := d.fileClient
	if client == nil && d.client != nil {
		client = d.client
	}
	if client == nil {
		return nil, errors.New("HDFS Avro driver is not initialized")
	}
	data, err := client.ReadFileLimited(ctx, filePath, defaultHDFSCSVMaxFileBytes)
	if err != nil {
		return nil, fmt.Errorf("read HDFS Avro OCF file: %w", err)
	}
	tableName := strings.TrimSuffix(path.Base(filePath), path.Ext(filePath))
	if tableName == "" || tableName == "." {
		tableName = "records"
	}
	db, err := object_storage.NewAvroSQLDatabase(object_storage.AvroSQLDatabaseConfig{
		DriverName: "hdfs-avro", ObjectKey: filePath, TableName: tableName,
		BatchSize: 1000, MaxFileBytes: defaultHDFSCSVMaxFileBytes, MaxRows: hdfsAvroMaxRows,
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
	result := &HDFSAvroResult{Rows: []map[string]interface{}{}, FilePath: filePath, BytesRead: int64(len(data))}
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

func (d *HDFSAvroDriver) GetCapacity(ctx context.Context) (*HDFSCapacity, error) {
	client := d.fileClient
	if client == nil && d.client != nil {
		client = d.client
	}
	if client == nil {
		return nil, errors.New("HDFS Avro driver is not initialized")
	}
	return client.GetCapacity(ctx)
}

func ValidateHDFSAvroConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return errors.New("HDFS Avro data source config is required")
	}
	if _, err := HDFSCSVClientConfig(config); err != nil {
		return err
	}
	filePath, err := cleanHDFSCSVPath(HDFSCSVFilePath(config))
	if err != nil {
		return err
	}
	if !strings.EqualFold(path.Ext(filePath), ".avro") {
		return errors.New("HDFS Avro file_path must have an .avro extension")
	}
	if _, err := HDFSAvroTableName(config); err != nil {
		return err
	}
	if _, present := configProp(config, "max_file_bytes"); present {
		if _, err := configPropInt64(config, "max_file_bytes"); err != nil {
			return errors.New("HDFS Avro max_file_bytes must be an integer")
		}
	}
	maxBytes := HDFSCSVMaxFileBytes(config)
	if maxBytes <= 0 || maxBytes > maxHDFSCSVMaxFileBytes {
		return fmt.Errorf("HDFS Avro max_file_bytes must be between 1 and %d", maxHDFSCSVMaxFileBytes)
	}
	return nil
}

func HDFSAvroTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("HDFS Avro data source config is required")
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
		return "", errors.New("HDFS Avro table_name could not be inferred; set additionalProps.table_name")
	}
	return name, nil
}

func HDFSAvroMaxRows() int { return hdfsAvroMaxRows }

type HDFSAvroResult struct {
	Rows      []map[string]interface{}
	NumRows   int64
	BytesRead int64
	FilePath  string
}
