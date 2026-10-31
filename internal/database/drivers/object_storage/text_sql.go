package object_storage

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"unicode/utf8"
)

// TextSQLDatabaseConfig bounds one line-oriented UTF-8 text file exposed as a
// read-only SQL table.
type TextSQLDatabaseConfig struct {
	DriverName   string
	ObjectKey    string
	TableName    string
	MaxFileBytes int64
	MaxRows      int
}

// NewTextSQLDatabase exposes each physical line from one bounded UTF-8 file as
// line_number and line columns. LF and CRLF delimiters are supported.
func NewTextSQLDatabase(config TextSQLDatabaseConfig, ping func(context.Context, string) error, read func(context.Context, string) ([]byte, error), close func() error) (*sql.DB, error) {
	if config.DriverName == "" || config.ObjectKey == "" || config.TableName == "" {
		return nil, errors.New("text SQL adapter requires driver name, object key, and table name")
	}
	if ping == nil || read == nil {
		return nil, errors.New("text SQL adapter requires ping and read callbacks")
	}
	if config.MaxFileBytes <= 0 || config.MaxRows <= 0 || config.MaxFileBytes >= int64(^uint(0)>>1) {
		return nil, errors.New("text SQL adapter requires positive, representable file and row limits")
	}

	schema := &objectSQLSchema{Columns: []objectSQLColumn{
		{Name: "line_number", StandardType: "bigint", Nullable: false},
		{Name: "line", StandardType: "string", Nullable: false},
	}}
	getSchema := func(context.Context, string) (*objectSQLSchema, error) {
		return schema, nil
	}
	readRows := func(ctx context.Context, key string) (*objectSQLSchema, []map[string]interface{}, error) {
		data, err := read(ctx, key)
		if err != nil {
			return nil, nil, err
		}
		if int64(len(data)) > config.MaxFileBytes {
			return nil, nil, fmt.Errorf("text file is %d bytes; configured maximum is %d bytes", len(data), config.MaxFileBytes)
		}
		if !utf8.Valid(data) {
			return nil, nil, errors.New("text file is not valid UTF-8")
		}

		scanner := bufio.NewScanner(bytes.NewReader(data))
		scanner.Buffer(make([]byte, 64*1024), int(config.MaxFileBytes)+1)
		scanner.Split(scanLFTextLines)
		rows := make([]map[string]interface{}, 0, 128)
		var lineNumber int64
		for scanner.Scan() {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			if len(rows) >= config.MaxRows {
				return nil, nil, fmt.Errorf("text file exceeds configured line limit of %d", config.MaxRows)
			}
			lineNumber++
			rows = append(rows, map[string]interface{}{
				"line_number": lineNumber,
				"line":        scanner.Text(),
			})
		}
		if err := scanner.Err(); err != nil {
			return nil, nil, fmt.Errorf("scan text file lines: %w", err)
		}
		return schema, rows, nil
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

func scanLFTextLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if index := bytes.IndexByte(data, '\n'); index >= 0 {
		line := data[:index]
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		return index + 1, line, nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
