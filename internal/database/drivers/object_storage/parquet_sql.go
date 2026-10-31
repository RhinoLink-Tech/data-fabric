package object_storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"

	"github.com/xitongsys/parquet-go/source"
)

// ParquetSQLDatabaseConfig bounds one Parquet object exposed as a read-only SQL table.
type ParquetSQLDatabaseConfig struct {
	DriverName   string
	ObjectKey    string
	TableName    string
	BatchSize    int
	MaxFileBytes int64
	MaxRows      int
}

// NewParquetSQLDatabase reuses the Parquet decoder and object SQL evaluator for
// filesystems and storage clients that provide bounded object reads.
func NewParquetSQLDatabase(config ParquetSQLDatabaseConfig, ping func(context.Context, string) error, read func(context.Context, string) ([]byte, error), close func() error) (*sql.DB, error) {
	if config.DriverName == "" || config.ObjectKey == "" || config.TableName == "" {
		return nil, errors.New("Parquet SQL adapter requires driver name, object key, and table name")
	}
	if ping == nil || read == nil {
		return nil, errors.New("Parquet SQL adapter requires ping and read callbacks")
	}
	if config.MaxFileBytes <= 0 || config.MaxRows <= 0 {
		return nil, errors.New("Parquet SQL adapter requires positive file and row limits")
	}
	if config.BatchSize <= 0 {
		config.BatchSize = 1000
	}

	reader := NewParquetReaderWithFileFactory(&ParquetReaderConfig{BatchSize: config.BatchSize}, func(ctx context.Context, key string) (source.ParquetFile, error) {
		data, err := read(ctx, key)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > config.MaxFileBytes {
			return nil, fmt.Errorf("Parquet file is %d bytes; configured maximum is %d bytes", len(data), config.MaxFileBytes)
		}
		return newBytesParquetFile(data), nil
	})

	openFile := func(ctx context.Context, key string) (*ParquetFileReader, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := reader.Read(ctx, key)
		if err != nil {
			return nil, err
		}
		if file.GetNumRows() > config.MaxRows {
			_ = file.Close()
			return nil, fmt.Errorf("Parquet file exceeds configured row limit of %d", config.MaxRows)
		}
		return file, nil
	}

	getSchema := func(ctx context.Context, key string) (*objectSQLSchema, error) {
		file, err := openFile(ctx, key)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		schema, err := file.GetSchema()
		if err != nil {
			return nil, err
		}
		return objectSQLSchemaFromParquet(schema), nil
	}

	readRows := func(ctx context.Context, key string) (*objectSQLSchema, []map[string]interface{}, error) {
		file, err := openFile(ctx, key)
		if err != nil {
			return nil, nil, err
		}
		defer file.Close()
		schema, err := file.GetSchema()
		if err != nil {
			return nil, nil, err
		}
		rows := make([]map[string]interface{}, 0, file.GetNumRows())
		for len(rows) < file.GetNumRows() {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			batchSize := config.BatchSize
			if remaining := file.GetNumRows() - len(rows); batchSize > remaining {
				batchSize = remaining
			}
			batch, err := file.ReadNumRows(batchSize)
			if err != nil {
				return nil, nil, err
			}
			if len(batch) == 0 {
				break
			}
			rows = append(rows, batch...)
		}
		if len(rows) != file.GetNumRows() {
			return nil, nil, fmt.Errorf("Parquet reader returned %d rows; footer declares %d", len(rows), file.GetNumRows())
		}
		return objectSQLSchemaFromParquet(schema), rows, nil
	}

	return newObjectSQLDB(&objectSQLAdapter{
		driverName: config.DriverName,
		objectKey:  config.ObjectKey,
		tableName:  config.TableName,
		ping:       ping,
		getSchema:  getSchema,
		readRows:   readRows,
		close:      close,
	}), nil
}

type bytesParquetFile struct {
	data   []byte
	reader *bytes.Reader
}

func newBytesParquetFile(data []byte) *bytesParquetFile {
	copied := append([]byte(nil), data...)
	return &bytesParquetFile{data: copied, reader: bytes.NewReader(copied)}
}

func (f *bytesParquetFile) Read(data []byte) (int, error) { return f.reader.Read(data) }
func (f *bytesParquetFile) Seek(offset int64, whence int) (int64, error) {
	return f.reader.Seek(offset, whence)
}
func (*bytesParquetFile) Write([]byte) (int, error) {
	return 0, errors.New("Parquet SQL file source is read-only")
}
func (*bytesParquetFile) Create(string) (source.ParquetFile, error) {
	return nil, errors.New("Parquet SQL file source is read-only")
}
func (f *bytesParquetFile) Open(string) (source.ParquetFile, error) {
	return newBytesParquetFile(f.data), nil
}
func (*bytesParquetFile) Close() error { return nil }

var _ io.Seeker = (*bytesParquetFile)(nil)
var _ io.Reader = (*bytesParquetFile)(nil)
var _ io.Writer = (*bytesParquetFile)(nil)
var _ io.Closer = (*bytesParquetFile)(nil)
var _ source.ParquetFile = (*bytesParquetFile)(nil)
