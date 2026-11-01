package file_system

import (
	"context"
	"database/sql"
	"errors"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// HDFSParquetCompressionDriver exposes Parquet files using the same page-codec
// decoding and bounded read-only SQL path as HDFSParquetDriver.
type HDFSParquetCompressionDriver struct {
	*HDFSParquetDriver
}

func NewHDFSParquetCompressionDriver(ctx context.Context, config *HDFSConfig) (*HDFSParquetCompressionDriver, error) {
	parquetDriver, err := NewHDFSParquetDriver(ctx, config)
	if err != nil {
		return nil, err
	}
	return &HDFSParquetCompressionDriver{HDFSParquetDriver: parquetDriver}, nil
}

// NewHDFSParquetCompressionDriverWithFileClient injects a bounded HDFS file client for tests and embedding.
func NewHDFSParquetCompressionDriverWithFileClient(client HDFSCSVFileClient) *HDFSParquetCompressionDriver {
	return &HDFSParquetCompressionDriver{HDFSParquetDriver: NewHDFSParquetDriverWithFileClient(client)}
}

func (d *HDFSParquetCompressionDriver) baseDriver() *HDFSParquetDriver {
	if d == nil || d.HDFSParquetDriver == nil {
		return &HDFSParquetDriver{}
	}
	return d.HDFSParquetDriver
}

func (*HDFSParquetCompressionDriver) Open(string) (*sql.DB, error) {
	return nil, errors.New("HDFS compressed Parquet requires a configured read-only SQL adapter")
}

func (d *HDFSParquetCompressionDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	return d.baseDriver().openWithConfig(ctx, config, "hdfs-parquet-compressed")
}

func (*HDFSParquetCompressionDriver) GetDatabaseTypeName() string { return "hdfs-parquet-compressed" }
func (*HDFSParquetCompressionDriver) GetDriverName() string       { return "hdfs-parquet-compressed" }
func (*HDFSParquetCompressionDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryFileSystem
}
func (*HDFSParquetCompressionDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{SupportsSQL: true, SupportsSchemaDiscovery: true}
}
func (d *HDFSParquetCompressionDriver) TestConnection(db *sql.DB) error {
	return d.TestConnectionContext(context.Background())
}
func (d *HDFSParquetCompressionDriver) TestConnectionContext(ctx context.Context) error {
	return d.baseDriver().TestConnectionContext(ctx)
}
func (d *HDFSParquetCompressionDriver) ListCompressedParquetFiles(ctx context.Context, directory string) ([]HDFSFileInfo, error) {
	return d.baseDriver().ListParquetFiles(ctx, directory)
}
func (d *HDFSParquetCompressionDriver) QueryCompressedParquetFile(ctx context.Context, filePath string) (*HDFSParquetResult, error) {
	return d.baseDriver().QueryParquetFile(ctx, filePath)
}
