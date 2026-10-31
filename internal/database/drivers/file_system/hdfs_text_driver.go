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

const hdfsTextMaxRows = 100000

// HDFSTextDriver exposes one configured UTF-8 text file as a read-only SQL table.
type HDFSTextDriver struct {
	config     *HDFSConfig
	client     *HDFSClient
	fileClient HDFSCSVFileClient
}

func NewHDFSTextDriver(ctx context.Context, config *HDFSConfig) (*HDFSTextDriver, error) {
	if config == nil {
		return nil, errors.New("HDFS text configuration is required")
	}
	client, err := NewHDFSClient(ctx, config)
	if err != nil {
		return nil, err
	}
	return &HDFSTextDriver{config: config, client: client, fileClient: client}, nil
}

// NewHDFSTextDriverWithFileClient injects a bounded HDFS file client for tests and embedding.
func NewHDFSTextDriverWithFileClient(client HDFSCSVFileClient) *HDFSTextDriver {
	return &HDFSTextDriver{fileClient: client}
}

func (*HDFSTextDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("HDFS text requires a configured read-only SQL adapter")
}

func (d *HDFSTextDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateHDFSTextConfig(config); err != nil {
		return nil, err
	}
	client := d.boundedFileClient()
	if client == nil {
		hdfsConfig, err := HDFSCSVClientConfig(config)
		if err != nil {
			return nil, err
		}
		client = newReopenableHDFSCSVFileClient(func(ctx context.Context) (HDFSCSVFileClient, error) {
			return NewHDFSClient(ctx, hdfsConfig)
		})
	}
	filePath := HDFSTextFilePath(config)
	tableName, err := HDFSTextTableName(config)
	if err != nil {
		return nil, err
	}
	maxBytes := HDFSCSVMaxFileBytes(config)
	return object_storage.NewTextSQLDatabase(object_storage.TextSQLDatabaseConfig{
		DriverName: "hdfs-text", ObjectKey: filePath, TableName: tableName,
		MaxFileBytes: maxBytes, MaxRows: hdfsTextMaxRows,
	}, func(ctx context.Context, key string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := client.GetFileInfo(ctx, key)
		if err != nil {
			return fmt.Errorf("inspect HDFS text file: %w", err)
		}
		if info == nil || info.IsDir {
			return errors.New("configured HDFS text path is not a file")
		}
		if info.Size < 0 || info.Size > maxBytes {
			return fmt.Errorf("HDFS text file is %d bytes; configured maximum is %d bytes", info.Size, maxBytes)
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := client.ReadFileLimited(ctx, key, maxBytes)
		if err != nil {
			return nil, fmt.Errorf("read HDFS text file: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	}, client.Close)
}

func (d *HDFSTextDriver) boundedFileClient() HDFSCSVFileClient {
	if d == nil {
		return nil
	}
	if d.fileClient != nil {
		return d.fileClient
	}
	if d.client != nil {
		return d.client
	}
	return nil
}

func (d *HDFSTextDriver) ApplyBatchPagination(query string, batchSize, offset int64) (string, error) {
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

func (d *HDFSTextDriver) ValidateDSN(dsn string) error {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme != "hdfs" || parsed.Host == "" || !path.IsAbs(parsed.Path) {
		return errors.New("invalid HDFS text URI; expected hdfs://namenode/path/to/file.txt or .log")
	}
	cleaned, err := cleanHDFSTextPath(parsed.Path)
	if err != nil || cleaned != parsed.Path || !validHDFSTextExtension(cleaned) {
		return errors.New("invalid HDFS text URI; expected hdfs://namenode/path/to/file.txt or .log")
	}
	return nil
}

func (*HDFSTextDriver) GetDefaultPort() int { return 8020 }
func (d *HDFSTextDriver) BuildDSN(config *model.DataSourceConfig) string {
	if config == nil {
		return "hdfs://"
	}
	hdfsConfig, err := HDFSCSVClientConfig(config)
	if err != nil || len(hdfsConfig.NameNodes) == 0 {
		return "hdfs://"
	}
	return (&url.URL{Scheme: "hdfs", Host: hdfsConfig.NameNodes[0], Path: HDFSTextFilePath(config)}).String()
}
func (*HDFSTextDriver) GetDatabaseTypeName() string { return "hdfs-text" }
func (*HDFSTextDriver) GetDriverName() string       { return "hdfs-text" }
func (*HDFSTextDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryFileSystem
}
func (*HDFSTextDriver) TestConnection(*sql.DB) error {
	return errors.New("use TestConnectionContext instead")
}
func (d *HDFSTextDriver) TestConnectionContext(ctx context.Context) error {
	client := d.boundedFileClient()
	if client == nil {
		return errors.New("HDFS text driver is not initialized")
	}
	if _, err := client.GetCapacity(ctx); err != nil {
		return fmt.Errorf("failed to test HDFS connection: %w", err)
	}
	return nil
}
func (*HDFSTextDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (*HDFSTextDriver) ConfigureAuth(interface{}) error { return nil }

func (d *HDFSTextDriver) ListTextFiles(ctx context.Context, directory string) ([]HDFSFileInfo, error) {
	client := d.boundedFileClient()
	if client == nil {
		return nil, errors.New("HDFS text driver is not initialized")
	}
	files, err := client.ListFiles(ctx, directory)
	if err != nil {
		return nil, err
	}
	result := make([]HDFSFileInfo, 0, len(files))
	for _, file := range files {
		if !file.IsDir && validHDFSTextExtension(file.Name) {
			result = append(result, file)
		}
	}
	return result, nil
}

// QueryTextFile preserves the legacy line-oriented result while applying the
// same byte and line bounds as the SQL adapter.
func (d *HDFSTextDriver) QueryTextFile(ctx context.Context, filePath string) (*HDFSTextResult, error) {
	client := d.boundedFileClient()
	if client == nil {
		return nil, errors.New("HDFS text driver is not initialized")
	}
	cleaned, err := cleanHDFSTextPath(filePath)
	if err != nil {
		return nil, err
	}
	if !validHDFSTextExtension(cleaned) {
		return nil, errors.New("HDFS text path must end in .txt or .log")
	}
	const tableName = "records"
	maxBytes := defaultHDFSCSVMaxFileBytes
	var bytesRead int64
	db, err := object_storage.NewTextSQLDatabase(object_storage.TextSQLDatabaseConfig{
		DriverName: "hdfs-text", ObjectKey: cleaned, TableName: tableName,
		MaxFileBytes: maxBytes, MaxRows: hdfsTextMaxRows,
	}, func(ctx context.Context, key string) error {
		info, err := client.GetFileInfo(ctx, key)
		if err != nil {
			return err
		}
		if info == nil || info.IsDir || info.Size < 0 || info.Size > maxBytes {
			return errors.New("configured HDFS text path is not a bounded file")
		}
		return nil
	}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := client.ReadFileLimited(ctx, key, maxBytes)
		if err == nil {
			bytesRead = int64(len(data))
		}
		return data, err
	}, nil)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT line FROM records ORDER BY line_number")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := &HDFSTextResult{Rows: []string{}, FilePath: cleaned}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return nil, err
		}
		result.Rows = append(result.Rows, line)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result.LineCount = len(result.Rows)
	result.BytesRead = bytesRead
	return result, nil
}

func ValidateHDFSTextConfig(config *model.DataSourceConfig) error {
	if config == nil {
		return errors.New("HDFS text data source config is required")
	}
	if _, err := HDFSCSVClientConfig(config); err != nil {
		return err
	}
	filePath, err := cleanHDFSTextPath(HDFSTextFilePath(config))
	if err != nil {
		return err
	}
	if !validHDFSTextExtension(filePath) {
		return errors.New("HDFS text file_path must end in .txt or .log")
	}
	if _, err := HDFSTextTableName(config); err != nil {
		return err
	}
	if _, present := configProp(config, "max_file_bytes"); present {
		if _, err := configPropInt64(config, "max_file_bytes"); err != nil {
			return errors.New("HDFS text max_file_bytes must be an integer")
		}
	}
	maxBytes := HDFSCSVMaxFileBytes(config)
	if maxBytes <= 0 || maxBytes > maxHDFSCSVMaxFileBytes {
		return fmt.Errorf("HDFS text max_file_bytes must be between 1 and %d", maxHDFSCSVMaxFileBytes)
	}
	return nil
}

func HDFSTextFilePath(config *model.DataSourceConfig) string { return HDFSCSVFilePath(config) }

func HDFSTextTableName(config *model.DataSourceConfig) (string, error) {
	if config == nil {
		return "", errors.New("HDFS text data source config is required")
	}
	if name := configPropString(config, "table_name"); name != "" {
		return name, nil
	}
	filePath, err := cleanHDFSTextPath(HDFSTextFilePath(config))
	if err != nil {
		return "", err
	}
	name := strings.TrimSuffix(path.Base(filePath), path.Ext(filePath))
	if name == "" || name == "." || name == "/" {
		return "", errors.New("HDFS text table_name could not be inferred; set additionalProps.table_name")
	}
	return name, nil
}

func HDFSTextMaxRows() int { return hdfsTextMaxRows }

func cleanHDFSTextPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !path.IsAbs(value) || strings.Contains(value, "\\") {
		return "", errors.New("HDFS text file_path must be an absolute HDFS path")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "." || segment == ".." {
			return "", errors.New("HDFS text file_path contains an invalid path segment")
		}
	}
	cleaned := path.Clean(value)
	if cleaned == "/" {
		return "", errors.New("HDFS text file_path must name a file")
	}
	return cleaned, nil
}

func validHDFSTextExtension(filePath string) bool {
	extension := strings.ToLower(path.Ext(filePath))
	return extension == ".txt" || extension == ".log"
}

type HDFSTextResult struct {
	Rows      []string
	LineCount int
	BytesRead int64
	FilePath  string
}
