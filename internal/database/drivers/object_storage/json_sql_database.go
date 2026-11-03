package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type JSONSQLDatabaseConfig struct {
	DriverName   string
	ObjectKey    string
	TableName    string
	MaxFileBytes int64
	MaxRecords   int
}

// NewJSONSQLDatabase exposes one bounded JSON object, array, or JSON Lines
// document through the shared read-only SQL adapter.
func NewJSONSQLDatabase(config JSONSQLDatabaseConfig, ping func(context.Context, string) error, readFile func(context.Context, string, int64) ([]byte, error), closeClient func() error) (*sql.DB, error) {
	config.DriverName = strings.TrimSpace(config.DriverName)
	config.ObjectKey = strings.TrimSpace(config.ObjectKey)
	config.TableName = strings.TrimSpace(config.TableName)
	if config.DriverName == "" || config.ObjectKey == "" || config.TableName == "" {
		return nil, errors.New("JSON SQL driver name, object key, and table name are required")
	}
	if config.MaxFileBytes <= 0 || config.MaxRecords <= 0 {
		return nil, errors.New("JSON SQL file and record limits must be positive")
	}
	if ping == nil || readFile == nil {
		return nil, errors.New("JSON SQL object access callbacks are required")
	}

	parser := NewJSONParser(&JSONParserConfig{DetectTypes: true, MaxRecords: config.MaxRecords})
	parse := func(ctx context.Context, key string) (*objectSQLSchema, []map[string]interface{}, error) {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		data, err := readFile(ctx, key, config.MaxFileBytes)
		if err != nil {
			return nil, nil, err
		}
		if int64(len(data)) > config.MaxFileBytes {
			return nil, nil, fmt.Errorf("%s object is %d bytes; configured maximum is %d bytes", config.DriverName, len(data), config.MaxFileBytes)
		}
		parsed, _, err := parser.ParseJSONDataContext(ctx, data)
		if err != nil {
			return nil, nil, fmt.Errorf("parse %s JSON: %w", config.DriverName, err)
		}
		if len(parsed.Data) > config.MaxRecords {
			return nil, nil, fmt.Errorf("%s record limit exceeded: maximum %d", config.DriverName, config.MaxRecords)
		}
		schema, err := objectSQLSchemaFromJSON(&parsed.Schema, config.DriverName)
		if err != nil {
			return nil, nil, fmt.Errorf("%s schema: %w", config.DriverName, err)
		}
		normalizeJSONSQLRows(parsed.Data, &parsed.Schema)
		return schema, parsed.Data, nil
	}

	return newObjectSQLDB(&objectSQLAdapter{
		driverName: config.DriverName,
		objectKey:  config.ObjectKey,
		tableName:  config.TableName,
		ping:       ping,
		getSchema: func(ctx context.Context, key string) (*objectSQLSchema, error) {
			schema, _, err := parse(ctx, key)
			return schema, err
		},
		readRows: func(ctx context.Context, key string) (*objectSQLSchema, []map[string]interface{}, error) {
			return parse(ctx, key)
		},
		close: closeClient,
	}), nil
}

// ApplyJSONBatchPagination wraps a read-only JSON SQL query in an outer page.
func ApplyJSONBatchPagination(query string, batchSize, offset int64) (string, error) {
	return applyObjectBatchPagination(query, batchSize, offset)
}
