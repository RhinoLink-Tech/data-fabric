package object_storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// OpenWithConfig creates a read-only SQL adapter for one configured S3 JSON
// object. The gateway parser is used consistently across S3-compatible stores.
func (d *S3JSONDriver) OpenWithConfig(ctx context.Context, config *model.DataSourceConfig) (*sql.DB, error) {
	if config == nil {
		return nil, errors.New("s3-json data source config is required")
	}
	bucket := strings.TrimSpace(config.Database)
	if bucket == "" {
		return nil, errors.New("s3-json bucket is required in database")
	}
	if (strings.TrimSpace(config.Username) == "") != (strings.TrimSpace(config.Password) == "") {
		return nil, errors.New("S3 access key and secret key must be configured together")
	}
	objectKey := configString(config.AdditionalProps, "object_key")
	if objectKey == "" {
		objectKey = strings.TrimSpace(config.Host)
	}
	if objectKey == "" {
		return nil, errors.New("s3-json object_key is required in additionalProps or host")
	}
	region := configString(config.AdditionalProps, "region")
	if region == "" {
		region = "us-east-1"
	}
	configured, err := NewS3JSONDriver(ctx, &S3JSONDriverConfig{
		S3Config: &S3Config{
			Region: region, Bucket: bucket, AccessKey: config.Username, SecretKey: config.Password,
			SessionToken: configString(config.AdditionalProps, "session_token"),
			RoleARN:      configString(config.AdditionalProps, "role_arn"), ExternalID: configString(config.AdditionalProps, "external_id"),
			EndpointURL:    configString(config.AdditionalProps, "endpoint_url"),
			DisableSSL:     configBool(config.AdditionalProps, "disable_ssl", false),
			ForcePathStyle: configBool(config.AdditionalProps, "force_path_style", false),
			MaxRetries:     configInt(config.AdditionalProps, "max_retries", 0),
		},
		FlattenNested: configBool(config.AdditionalProps, "flatten_nested", false),
		DetectTypes:   true,
	})
	if err != nil {
		return nil, err
	}
	tableName, err := S3JSONTableName(config)
	if err != nil {
		return nil, err
	}
	return newObjectSQLDB(&objectSQLAdapter{
		driverName: "s3-json",
		objectKey:  objectKey,
		tableName:  tableName,
		ping: func(ctx context.Context, key string) error {
			_, err := configured.s3Client.GetObjectMetadata(ctx, key)
			return err
		},
		getSchema: func(ctx context.Context, key string) (*objectSQLSchema, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			schema, err := configured.DetectSchema(ctx, key)
			if err != nil {
				return nil, err
			}
			return objectSQLSchemaFromJSON(schema)
		},
		readRows: func(ctx context.Context, key string) (*objectSQLSchema, []map[string]interface{}, error) {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			result, err := configured.queryWithParser(ctx, &S3JSONQuery{Key: key})
			if err != nil {
				return nil, nil, err
			}
			schema, err := objectSQLSchemaFromJSON(result.Schema)
			if err != nil {
				return nil, nil, err
			}
			normalizeJSONSQLRows(result.Rows, result.Schema)
			return schema, result.Rows, nil
		},
	}), nil
}

func objectSQLSchemaFromJSON(schema *JSONSchema) (*objectSQLSchema, error) {
	if schema == nil || len(schema.Fields) == 0 {
		return nil, errors.New("s3-json object has no fields")
	}
	result := &objectSQLSchema{Columns: make([]objectSQLColumn, len(schema.Fields))}
	for i, field := range schema.Fields {
		standardType := ""
		switch strings.ToLower(strings.TrimSpace(field.Type)) {
		case "boolean":
			standardType = "boolean"
		case "integer":
			standardType = "bigint"
		case "float", "double":
			standardType = "double"
		case "string":
			standardType = "string"
		default:
			return nil, fmt.Errorf("s3-json field %q has unsupported SQL type %q; use top-level scalar JSON fields", field.Name, field.Type)
		}
		result.Columns[i] = objectSQLColumn{
			Name: field.Name, StandardType: standardType, Nullable: field.Nullable,
		}
	}
	return result, nil
}

func normalizeJSONSQLRows(rows []map[string]interface{}, schema *JSONSchema) {
	if schema == nil {
		return
	}
	doubleFields := make(map[string]struct{})
	for _, field := range schema.Fields {
		if field.Type == "double" || field.Type == "float" {
			doubleFields[field.Name] = struct{}{}
		}
	}
	for _, row := range rows {
		for field := range doubleFields {
			if integer, ok := row[field].(int64); ok {
				row[field] = float64(integer)
			}
		}
	}
}

// S3JSONTableName returns the SQL table identity for one configured JSON object.
func S3JSONTableName(config *model.DataSourceConfig) (string, error) {
	return objectSQLTableName(config, "s3-json")
}
