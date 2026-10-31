package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// AvroSQLDatabaseConfig bounds one Avro OCF exposed as a read-only SQL table.
type AvroSQLDatabaseConfig struct {
	DriverName   string
	ObjectKey    string
	TableName    string
	BatchSize    int
	MaxFileBytes int64
	MaxRows      int
}

// NewAvroSQLDatabase reuses the Avro OCF decoder and object SQL evaluator for
// filesystems and storage clients that provide bounded reads.
func NewAvroSQLDatabase(config AvroSQLDatabaseConfig, ping func(context.Context, string) error, read AvroObjectReadFunc, close func() error) (*sql.DB, error) {
	if config.DriverName == "" || config.ObjectKey == "" || config.TableName == "" {
		return nil, errors.New("Avro SQL adapter requires driver name, object key, and table name")
	}
	if ping == nil || read == nil {
		return nil, errors.New("Avro SQL adapter requires ping and read callbacks")
	}
	if config.MaxFileBytes <= 0 || config.MaxRows <= 0 {
		return nil, errors.New("Avro SQL adapter requires positive file and row limits")
	}
	if config.BatchSize <= 0 {
		config.BatchSize = 1000
	}

	reader := NewAvroReaderWithReadFunc(&AvroReaderConfig{BatchSize: config.BatchSize}, func(ctx context.Context, key string) ([]byte, error) {
		data, err := read(ctx, key)
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > config.MaxFileBytes {
			return nil, fmt.Errorf("Avro OCF file is %d bytes; configured maximum is %d bytes", len(data), config.MaxFileBytes)
		}
		return data, nil
	})

	getSchema := func(ctx context.Context, key string) (*objectSQLSchema, error) {
		file, err := reader.Read(ctx, key)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		schema, err := file.GetSchema()
		if err != nil {
			return nil, err
		}
		return objectSQLSchemaFromAvro(schema)
	}

	readRows := func(ctx context.Context, key string) (*objectSQLSchema, []map[string]interface{}, error) {
		file, err := reader.Read(ctx, key)
		if err != nil {
			return nil, nil, err
		}
		defer file.Close()
		schema, err := file.GetSchema()
		if err != nil {
			return nil, nil, err
		}
		rows, err := file.ReadNumRows(config.MaxRows + 1)
		if err != nil {
			return nil, nil, err
		}
		if len(rows) > config.MaxRows {
			return nil, nil, fmt.Errorf("Avro OCF file exceeds configured row limit of %d", config.MaxRows)
		}
		objectSchema, err := objectSQLSchemaFromAvro(schema)
		if err != nil {
			return nil, nil, err
		}
		return objectSchema, rows, nil
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
