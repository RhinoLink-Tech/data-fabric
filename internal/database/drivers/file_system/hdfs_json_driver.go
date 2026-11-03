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

const hdfsJSONMaxRecords = 100000

// HDFSJSONDriver exposes one bounded JSON document through read-only SQL.
type HDFSJSONDriver struct {
	client     *HDFSClient
	fileClient HDFSCSVFileClient
}

func NewHDFSJSONDriver(ctx context.Context, config *HDFSConfig) (*HDFSJSONDriver, error) {
	if config == nil {
		return nil, errors.New("HDFS JSON configuration is required")
	}
	client, err := NewHDFSClient(ctx, config)
	if err != nil {
		return nil, err
	}
	return &HDFSJSONDriver{client: client, fileClient: client}, nil
}

// NewHDFSJSONDriverWithFileClient injects a bounded file client for tests and embedding.
func NewHDFSJSONDriverWithFileClient(client HDFSCSVFileClient) *HDFSJSONDriver {
	return &HDFSJSONDriver{fileClient: client}
}

func (*HDFSJSONDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("HDFS JSON requires a configured read-only SQL adapter")
}

func (d *HDFSJSONDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateHDFSJSONConfig(config); err != nil {
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
	tableName, err := HDFSJSONTableName(config)
	if err != nil {
		return nil, err
	}
	maxBytes := HDFSCSVMaxFileBytes(config)
	return object_storage.NewJSONSQLDatabase(object_storage.JSONSQLDatabaseConfig{
		DriverName: "hdfs-json", ObjectKey: filePath, TableName: tableName,
		MaxFileBytes: maxBytes, MaxRecords: hdfsJSONMaxRecords,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := client.GetFileInfo(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect HDFS JSON file: %w", err)
		}
		if info == nil || info.IsDir {
			return errors.New("configured HDFS JSON path is not a file")
		}
		if info.Size < 0 || info.Size > maxBytes {
			return fmt.Errorf("HDFS JSON file is %d bytes; configured maximum is %d bytes", info.Size, maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string, limit int64) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := client.ReadFileLimited(ctx, key, limit)
		if err != nil {
			return nil, fmt.Errorf("read HDFS JSON file: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, client.Close)
}

func (d *HDFSJSONDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "hdfs://"
	}
	hdfsConfig, err := HDFSCSVClientConfig(config)
	if err != nil || len(hdfsConfig.NameNodes) == 0 {
		return "hdfs://"
	}
	return (&url.URL{Scheme: "hdfs", Host: hdfsConfig.NameNodes[0], Path: HDFSCSVFilePath(config)}).String()
}

func (*HDFSJSONDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "hdfs" || parsed.Host == "" || !path.IsAbs(parsed.Path) || !isHDFSJSONExtension(path.Ext(parsed.Path)) {
		return errors.New("invalid HDFS JSON URI; expected hdfs://namenode/path/to/file.json")
	}
	return nil
}

func (*HDFSJSONDriver) GetDefaultPort() int                 { return 8020 }
func (*HDFSJSONDriver) GetDatabaseTypeName() string         { return "hdfs-json" }
func (*HDFSJSONDriver) GetDriverName() string               { return "hdfs-json" }
func (*HDFSJSONDriver) GetCategory() drivers.DriverCategory { return drivers.CategoryFileSystem }
func (*HDFSJSONDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*HDFSJSONDriver) ConfigureAuth(interface{}) error { return nil }
func (*HDFSJSONDriver) TestConnection(*sql.DB) error {
	return errors.New("use TestConnectionContext instead")
}
func (d *HDFSJSONDriver) TestConnectionContext(ctx context.Context) error {
	client := d.fileClient
	if client == nil && d.client != nil {
		client = d.client
	}
	if client == nil {
		return errors.New("HDFS JSON driver is not initialized")
	}
	if _, err := client.GetCapacity(ctx); err != nil {
		return fmt.Errorf("failed to test HDFS connection: %w", err)
	}
	return nil
}

func (d *HDFSJSONDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
	return object_storage.ApplyJSONBatchPagination(query, batchSize, offset)
}

func (d *HDFSJSONDriver) fileClientForOperation() (HDFSCSVFileClient, error) {
	if d.fileClient != nil {
		return d.fileClient, nil
	}
	if d.client != nil {
		return d.client, nil
	}
	return nil, errors.New("HDFS JSON driver is not initialized")
}

func (d *HDFSJSONDriver) ListJSONFiles(ctx context.Context, directory string) ([]HDFSFileInfo, error) {
	client, err := d.fileClientForOperation()
	if err != nil {
		return nil, err
	}
	files, err := client.ListFiles(ctx, directory)
	if err != nil {
		return nil, err
	}
	result := make([]HDFSFileInfo, 0, len(files))
	for _, file := range files {
		if !file.IsDir && isHDFSJSONExtension(path.Ext(file.Name)) {
			result = append(result, file)
		}
	}
	return result, nil
}

// QueryJSONFile returns decoded records for legacy callers; unified SQL uses OpenWithConfig.
func (d *HDFSJSONDriver) QueryJSONFile(ctx context.Context, filePath string) (*HDFSJSONResult, error) {
	client, err := d.fileClientForOperation()
	if err != nil {
		return nil, err
	}
	safePath, err := cleanHDFSCSVPath(filePath)
	if err != nil {
		return nil, err
	}
	data, err := client.ReadFileLimited(ctx, safePath, maxHDFSCSVMaxFileBytes)
	if err != nil {
		return nil, fmt.Errorf("read HDFS JSON file: %w", err)
	}
	parsed, _, err := object_storage.NewJSONParser(&object_storage.JSONParserConfig{
		DetectTypes: true, MaxRecords: hdfsJSONMaxRecords,
	}).ParseJSONDataContext(ctx, data)
	if err != nil {
		return nil, fmt.Errorf("parse HDFS JSON file: %w", err)
	}
	return &HDFSJSONResult{
		Rows: parsed.Data, NumRows: int64(len(parsed.Data)), BytesRead: int64(len(data)), FilePath: safePath,
	}, nil
}

func ValidateHDFSJSONConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return errors.New("HDFS JSON data source config is required")
	}
	if _, err := HDFSCSVClientConfig(config); err != nil {
		return err
	}
	filePath, err := cleanHDFSCSVPath(HDFSCSVFilePath(config))
	if err != nil {
		return err
	}
	if !isHDFSJSONExtension(path.Ext(filePath)) {
		return errors.New("HDFS JSON file_path must have a .json, .jsonl, .ndjson, or .jsonlines extension")
	}
	if _, err := HDFSJSONTableName(config); err != nil {
		return err
	}
	if _, present := configProp(config, "max_file_bytes"); present {
		if _, err := configPropInt64(config, "max_file_bytes"); err != nil {
			return errors.New("HDFS JSON max_file_bytes must be an integer")
		}
	}
	maxBytes := HDFSCSVMaxFileBytes(config)
	if maxBytes <= 0 || maxBytes > maxHDFSCSVMaxFileBytes {
		return fmt.Errorf("HDFS JSON max_file_bytes must be between 1 and %d", maxHDFSCSVMaxFileBytes)
	}
	return nil
}

func HDFSJSONTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("HDFS JSON data source config is required")
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
		return "", errors.New("HDFS JSON table_name could not be inferred; set additionalProps.table_name")
	}
	return name, nil
}

func isHDFSJSONExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case ".json", ".jsonl", ".ndjson", ".jsonlines":
		return true
	default:
		return false
	}
}

type HDFSJSONResult struct {
	Rows      []map[string]interface{}
	NumRows   int64
	BytesRead int64
	FilePath  string
}
