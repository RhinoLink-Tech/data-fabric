package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/file_system"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
)

const maxListedTableIdentities = 1000

var (
	ErrMetadataRepositoryRequired     = errors.New("data source repository is required")
	ErrMetadataConnectionPoolRequired = errors.New("metadata connection pool is required")
	ErrMetadataDriverRegistryRequired = errors.New("metadata driver registry is required")
	ErrSchemaDiscoveryUnsupported     = errors.New("schema discovery is not supported")
	ErrTableNotFound                  = errors.New("table not found")
	ErrAmbiguousTable                 = errors.New("table name is ambiguous")
)

type dataSourceLookup interface {
	GetByID(ctx context.Context, id string) (*model.DataSource, error)
}

type metadataConnectionProvider interface {
	GetConnection(ctx context.Context, dataSource *model.DataSource) (*sql.DB, error)
}

// MetadataExtractor extracts schema metadata from data sources
type MetadataExtractor struct {
	connPool metadataConnectionProvider
	sources  dataSourceLookup
	registry *database.DriverRegistry
}

// NewMetadataExtractor creates a metadata extractor backed by the source repository.
func NewMetadataExtractor(connPool *database.ConnectionPool, sources repository.DataSourceRepository) *MetadataExtractor {
	return &MetadataExtractor{
		connPool: connPool,
		sources:  sources,
		registry: database.GetDriverRegistry(),
	}
}

// ExtractSchema extracts the complete schema for a data source.
func (e *MetadataExtractor) ExtractSchema(ctx context.Context, dataSourceID string) (*DataSourceSchema, string, error) {
	dataSource, db, err := e.resolveConnection(ctx, dataSourceID)
	if err != nil {
		return nil, "", err
	}

	// Extract tables
	var tables map[string]*TableSchema
	if isCSVObjectSource(dataSource.Type) {
		var table *TableSchema
		table, err = extractCSVTable(ctx, db, &dataSource.Config, dataSource.Type)
		if err == nil {
			tables = map[string]*TableSchema{table.Name: table}
		}
	} else if isLineTextSource(dataSource.Type) || isHDFSCSVSource(dataSource.Type) || isHDFSParquetSource(dataSource.Type) || isParquetObjectSource(dataSource.Type) || isORCObjectSource(dataSource.Type) || isAvroObjectSource(dataSource.Type) || isJSONSQLSource(dataSource.Type) || isDeltaObjectSource(dataSource.Type) || isIcebergRESTSource(dataSource.Type) || isMinIOORCSource(dataSource.Type) {
		var table *TableSchema
		table, err = extractObjectSQLTable(ctx, db, &dataSource.Config, dataSource.Type)
		if err == nil && isIcebergRESTSource(dataSource.Type) {
			table.Schema, err = icebergRESTNamespace(dataSource.Type, &dataSource.Config)
		}
		if err == nil {
			tables = map[string]*TableSchema{table.Name: table}
		}
	} else {
		tables, err = e.extractTables(ctx, db, dataSource.Type)
	}
	if err != nil {
		return nil, "", fmt.Errorf("failed to extract tables: %w", err)
	}

	schema := &DataSourceSchema{
		Tables: tables,
	}

	// Generate schema version (hash-based or timestamp-based)
	version := e.generateSchemaVersion(schema)

	return schema, version, nil
}

// ExtractTableSchema extracts the schema of one table without scanning every
// table in the data source: the table list is read once, and column, index,
// and primary-key details are fetched only for the requested table (#184).
func (e *MetadataExtractor) ExtractTableSchema(ctx context.Context, dataSourceID, tableName string) (*TableSchema, error) {
	return e.ExtractTableSchemaInSchema(ctx, dataSourceID, "", tableName)
}

// ExtractTableSchemaInSchema describes a table only after resolving its exact
// schema. An empty schema accepts a unique table name and rejects ambiguity.
func (e *MetadataExtractor) ExtractTableSchemaInSchema(ctx context.Context, dataSourceID, requestedSchema, tableName string) (*TableSchema, error) {
	dataSource, db, err := e.resolveConnection(ctx, dataSourceID)
	if err != nil {
		return nil, err
	}
	if isCSVObjectSource(dataSource.Type) {
		name, err := csvTableNameForSource(dataSource.Type, &dataSource.Config)
		if err != nil {
			return nil, err
		}
		if requestedSchema != "" || tableName != name {
			return nil, fmt.Errorf("%w: %q in data source %s", ErrTableNotFound, tableName, dataSourceID)
		}
		return extractCSVTable(ctx, db, &dataSource.Config, dataSource.Type)
	}
	if isIcebergRESTSource(dataSource.Type) {
		name, err := objectSQLTableNameForSource(dataSource.Type, &dataSource.Config)
		if err != nil {
			return nil, err
		}
		namespace, err := icebergRESTNamespace(dataSource.Type, &dataSource.Config)
		if err != nil {
			return nil, err
		}
		if tableName != name || (requestedSchema != "" && requestedSchema != namespace) {
			return nil, fmt.Errorf("%w: %q in data source %s", ErrTableNotFound, tableName, dataSourceID)
		}
		table, err := extractObjectSQLTable(ctx, db, &dataSource.Config, dataSource.Type)
		if err != nil {
			return nil, err
		}
		table.Schema = namespace
		return table, nil
	}
	if isLineTextSource(dataSource.Type) || isHDFSCSVSource(dataSource.Type) || isHDFSParquetSource(dataSource.Type) || isParquetObjectSource(dataSource.Type) || isORCObjectSource(dataSource.Type) || isAvroObjectSource(dataSource.Type) || isJSONSQLSource(dataSource.Type) || isDeltaObjectSource(dataSource.Type) || isMinIOORCSource(dataSource.Type) {
		name, err := objectSQLTableNameForSource(dataSource.Type, &dataSource.Config)
		if err != nil {
			return nil, err
		}
		if requestedSchema != "" || tableName != name {
			return nil, fmt.Errorf("%w: %q in data source %s", ErrTableNotFound, tableName, dataSourceID)
		}
		return extractObjectSQLTable(ctx, db, &dataSource.Config, dataSource.Type)
	}

	schemaName, tableType, found, err := e.findTable(ctx, db, dataSource.Type, requestedSchema, tableName)
	if err != nil {
		return nil, fmt.Errorf("failed to look up table %q: %w", tableName, err)
	}
	if !found {
		return nil, fmt.Errorf("%w: %q in data source %s", ErrTableNotFound, tableName, dataSourceID)
	}

	columns, err := e.extractColumns(ctx, db, dataSource.Type, tableName, schemaName)
	if err != nil {
		return nil, fmt.Errorf("failed to extract columns for table %q: %w", tableName, err)
	}

	// Indexes and primary keys are supplementary: keep serving the schema
	// when the engine cannot provide them, like full-schema extraction does.
	indexes, err := e.extractIndexes(ctx, db, dataSource.Type, tableName, schemaName)
	if err != nil {
		log.Printf("data source %s: indexes for table %q unavailable, returning none: %v", dataSourceID, tableName, err)
		indexes = []IndexSchema{}
	}
	pk, err := e.extractPrimaryKey(ctx, db, dataSource.Type, tableName, schemaName)
	if err != nil {
		pk = []string{}
	}

	return &TableSchema{
		Name:       tableName,
		Schema:     schemaName,
		Type:       tableType,
		Columns:    columns,
		PrimaryKey: pk,
		Indexes:    indexes,
	}, nil
}

// resolveConnection loads the data source, verifies that its driver supports
// schema discovery, and opens a pooled connection. Unsupported sources fail
// before a connection is attempted, so placeholder settings never produce a
// misleading connection error.
func (e *MetadataExtractor) resolveConnection(ctx context.Context, dataSourceID string) (*model.DataSource, *sql.DB, error) {
	if e.sources == nil {
		return nil, nil, ErrMetadataRepositoryRequired
	}
	dataSource, err := e.sources.GetByID(ctx, dataSourceID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load data source %q: %w", dataSourceID, err)
	}
	if dataSource == nil {
		return nil, nil, fmt.Errorf("failed to load data source %q: %w", dataSourceID, repository.ErrDataSourceNotFound)
	}
	if e.registry == nil {
		return nil, nil, ErrMetadataDriverRegistryRequired
	}
	driver, err := e.registry.GetDriver(dataSource.Type)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrSchemaDiscoveryUnsupported, dataSource.Type)
	}
	if !driver.GetCapabilities().SupportsSchemaDiscovery {
		return nil, nil, fmt.Errorf("%w: %s", ErrSchemaDiscoveryUnsupported, dataSource.Type)
	}
	if e.connPool == nil {
		return nil, nil, ErrMetadataConnectionPoolRequired
	}

	db, err := e.connPool.GetConnection(ctx, dataSource)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get connection for data source %q: %w", dataSourceID, err)
	}
	return dataSource, db, nil
}

// findTable scans the table list for one table name and reports its schema
// name and table type without extracting details for the other tables.
func (e *MetadataExtractor) findTable(ctx context.Context, db *sql.DB, dbType model.DatabaseType, requestedSchema, tableName string) (schemaName, tableType string, found bool, err error) {
	rows, err := db.QueryContext(ctx, e.getTableQuery(dbType))
	if err != nil {
		return "", "", false, err
	}
	defer rows.Close()

	for rows.Next() {
		var name, kind string
		var schema sql.NullString
		if err := e.scanTableRow(rows, &name, &kind, &schema); err != nil {
			// A scan failure must surface as an error, not masquerade as a
			// missing table (#184 review: swallowed errors made real tables
			// unfindable).
			return "", "", false, fmt.Errorf("failed to scan table row: %w", err)
		}
		if name != tableName || (requestedSchema != "" && requestedSchema != schema.String) {
			continue
		}
		if found && schemaName != schema.String {
			return "", "", false, ErrAmbiguousTable
		}
		schemaName, tableType, found = schema.String, kind, true
	}
	return schemaName, tableType, found, rows.Err()
}

// ListTables returns only table identities, without reading column or index
// metadata. Callers can filter the result before exposing names to users.
func (e *MetadataExtractor) ListTables(ctx context.Context, dataSourceID string) ([]TableIdentity, error) {
	dataSource, db, err := e.resolveConnection(ctx, dataSourceID)
	if err != nil {
		return nil, err
	}
	if isCSVObjectSource(dataSource.Type) {
		name, err := csvTableNameForSource(dataSource.Type, &dataSource.Config)
		if err != nil {
			return nil, err
		}
		return []TableIdentity{{Name: name, Type: "TABLE"}}, nil
	}
	if isLineTextSource(dataSource.Type) || isHDFSCSVSource(dataSource.Type) || isHDFSParquetSource(dataSource.Type) || isParquetObjectSource(dataSource.Type) || isORCObjectSource(dataSource.Type) || isAvroObjectSource(dataSource.Type) || isJSONSQLSource(dataSource.Type) || isDeltaObjectSource(dataSource.Type) || isIcebergRESTSource(dataSource.Type) || isMinIOORCSource(dataSource.Type) {
		name, err := objectSQLTableNameForSource(dataSource.Type, &dataSource.Config)
		if err != nil {
			return nil, err
		}
		namespace := ""
		if isIcebergRESTSource(dataSource.Type) {
			namespace, err = icebergRESTNamespace(dataSource.Type, &dataSource.Config)
			if err != nil {
				return nil, err
			}
		}
		return []TableIdentity{{Name: name, Schema: namespace, Type: "TABLE"}}, nil
	}
	rows, err := db.QueryContext(ctx, e.getTableQuery(dataSource.Type))
	if err != nil {
		return nil, fmt.Errorf("failed to query table identities: %w", err)
	}
	defer rows.Close()

	var tables []TableIdentity
	for rows.Next() {
		var name, kind string
		var schema sql.NullString
		if err := e.scanTableRow(rows, &name, &kind, &schema); err != nil {
			return nil, fmt.Errorf("failed to scan table identity: %w", err)
		}
		tables = append(tables, TableIdentity{Name: name, Schema: schema.String, Type: kind})
		if len(tables) == maxListedTableIdentities {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read table identities: %w", err)
	}
	return tables, nil
}

func isHDFSCSVSource(dbType model.DatabaseType) bool {
	return dbType == model.DatabaseTypeHDFSCSV
}

func isLineTextSource(dbType model.DatabaseType) bool {
	return dbType == model.DatabaseTypeHDFSText || dbType == model.DatabaseTypeOzoneText
}

func isHDFSParquetSource(dbType model.DatabaseType) bool {
	return dbType == model.DatabaseTypeHDFSParquet || dbType == model.DatabaseTypeHDFSParquetCompressed
}

func isCSVObjectSource(dbType model.DatabaseType) bool {
	return dbType == model.DatabaseTypeMinIOCSV || dbType == model.DatabaseTypeS3CSV || dbType == model.DatabaseTypeGCSCSV || dbType == model.DatabaseTypeOzoneCSV || dbType == model.DatabaseTypeOSSCSV || dbType == model.DatabaseTypeTencentCOSCSV || dbType == model.DatabaseTypeAzureBlobCSV
}

func csvTableNameForSource(dbType model.DatabaseType, config *model.DataSourceConfig) (string, error) {
	switch dbType {
	case model.DatabaseTypeOzoneCSV:
		return file_system.OzoneCSVTableName(config)
	case model.DatabaseTypeOSSCSV:
		return object_storage.OSSCSVTableName(config)
	case model.DatabaseTypeTencentCOSCSV:
		return object_storage.COSCSVTableName(config)
	case model.DatabaseTypeAzureBlobCSV:
		return object_storage.AzureBlobCSVTableName(config)
	case model.DatabaseTypeGCSCSV:
		return object_storage.GCSCSVTableName(config)
	default:
		return object_storage.CSVTableName(config)
	}
}

func isParquetObjectSource(dbType model.DatabaseType) bool {
	return dbType == model.DatabaseTypeMinIOParquet || dbType == model.DatabaseTypeS3Parquet || dbType == model.DatabaseTypeAlibabaOSSParquet || dbType == model.DatabaseTypeTencentCOSParquet || dbType == model.DatabaseTypeAzureBlobParquet || dbType == model.DatabaseTypeAzureParquet || dbType == model.DatabaseTypeOzoneParquet
}

func isORCObjectSource(dbType model.DatabaseType) bool {
	return dbType == model.DatabaseTypeS3ORC || dbType == model.DatabaseTypeHDFSORC || dbType == model.DatabaseTypeAzureBlobORC || dbType == model.DatabaseTypeAlibabaOSSORC || dbType == model.DatabaseTypeTencentCOSORC || dbType == model.DatabaseTypeOzoneORC
}

func isAvroObjectSource(dbType model.DatabaseType) bool {
	return dbType == model.DatabaseTypeS3Avro || dbType == model.DatabaseTypeMinIOAvro || dbType == model.DatabaseTypeHDFSAvro || dbType == model.DatabaseTypeOzoneAvro || dbType == model.DatabaseTypeAzureBlobAvro || dbType == model.DatabaseTypeAlibabaOSSAvro || dbType == model.DatabaseTypeTencentCOSAvro
}

func isJSONSQLSource(dbType model.DatabaseType) bool {
	return dbType == model.DatabaseTypeHDFSJSON || dbType == model.DatabaseTypeS3JSON || dbType == model.DatabaseTypeMinIOJSON || dbType == model.DatabaseTypeOzoneJSON || dbType == model.DatabaseTypeAzureBlobJSON || dbType == model.DatabaseTypeTencentCOSJSON || dbType == model.DatabaseTypeAlibabaOSSJSON
}

func isDeltaObjectSource(dbType model.DatabaseType) bool {
	return dbType == model.DatabaseTypeOSSDelta || dbType == model.DatabaseTypeS3Delta || dbType == model.DatabaseTypeMinIODelta || dbType == model.DatabaseTypeAzureDelta || dbType == model.DatabaseTypeOzoneDelta || dbType == model.DatabaseTypeTencentCOSDelta
}

func isIcebergRESTSource(dbType model.DatabaseType) bool {
	return dbType == model.DatabaseTypeMinIOIceberg || dbType == model.DatabaseTypeTencentCOSIceberg
}

func icebergRESTNamespace(dbType model.DatabaseType, config *model.DataSourceConfig) (string, error) {
	switch dbType {
	case model.DatabaseTypeTencentCOSIceberg:
		return object_storage.COSIcebergNamespace(config)
	default:
		return object_storage.MinIOIcebergNamespace(config)
	}
}

func icebergRESTTableName(dbType model.DatabaseType, config *model.DataSourceConfig) (string, error) {
	switch dbType {
	case model.DatabaseTypeTencentCOSIceberg:
		return object_storage.COSIcebergTableName(config)
	default:
		return object_storage.MinIOIcebergTableName(config)
	}
}

func isMinIOORCSource(dbType model.DatabaseType) bool {
	return dbType == model.DatabaseTypeMinIOORC
}

func objectSQLTableNameForSource(dbType model.DatabaseType, config *model.DataSourceConfig) (string, error) {
	switch dbType {
	case model.DatabaseTypeHDFSJSON:
		return file_system.HDFSJSONTableName(config)
	case model.DatabaseTypeOzoneJSON:
		return file_system.OzoneJSONTableName(config)
	case model.DatabaseTypeHDFSText:
		return file_system.HDFSTextTableName(config)
	case model.DatabaseTypeOzoneText:
		return file_system.OzoneTextTableName(config)
	case model.DatabaseTypeHDFSCSV:
		return file_system.HDFSCSVTableName(config)
	case model.DatabaseTypeHDFSParquet, model.DatabaseTypeHDFSParquetCompressed:
		return file_system.HDFSParquetTableName(config)
	case model.DatabaseTypeHDFSAvro:
		return file_system.HDFSAvroTableName(config)
	case model.DatabaseTypeHDFSORC:
		return file_system.HDFSORCTableName(config)
	case model.DatabaseTypeMinIOParquet:
		return object_storage.MinIOParquetTableName(config)
	case model.DatabaseTypeS3Parquet:
		return object_storage.S3ParquetTableName(config)
	case model.DatabaseTypeAlibabaOSSParquet:
		return object_storage.OSSParquetTableName(config)
	case model.DatabaseTypeTencentCOSParquet:
		return object_storage.COSParquetTableName(config)
	case model.DatabaseTypeAzureBlobORC:
		return object_storage.AzureBlobORCTableName(config)
	case model.DatabaseTypeAzureBlobAvro:
		return object_storage.AzureBlobAvroTableName(config)
	case model.DatabaseTypeAzureBlobJSON:
		return object_storage.AzureBlobJSONTableName(config)
	case model.DatabaseTypeTencentCOSJSON:
		return object_storage.COSJSONTableName(config)
	case model.DatabaseTypeAlibabaOSSJSON:
		return object_storage.OSSJSONTableName(config)
	case model.DatabaseTypeAzureBlobParquet, model.DatabaseTypeAzureParquet:
		return object_storage.AzureBlobParquetTableName(config)
	case model.DatabaseTypeOzoneParquet:
		return file_system.OzoneParquetTableName(config)
	case model.DatabaseTypeOzoneORC:
		return file_system.OzoneORCTableName(config)
	case model.DatabaseTypeOzoneAvro:
		return file_system.OzoneAvroTableName(config)
	case model.DatabaseTypeS3ORC:
		return object_storage.S3ORCTableName(config)
	case model.DatabaseTypeAlibabaOSSORC:
		return object_storage.OSSORCTableName(config)
	case model.DatabaseTypeTencentCOSORC:
		return object_storage.COSORCTableName(config)
	case model.DatabaseTypeS3Avro:
		return object_storage.S3AvroTableName(config)
	case model.DatabaseTypeAlibabaOSSAvro:
		return object_storage.OSSAvroTableName(config)
	case model.DatabaseTypeTencentCOSAvro:
		return object_storage.COSAvroTableName(config)
	case model.DatabaseTypeMinIOAvro:
		return object_storage.MinIOAvroTableName(config)
	case model.DatabaseTypeS3JSON:
		return object_storage.S3JSONTableName(config)
	case model.DatabaseTypeMinIOJSON:
		return object_storage.MinIOJSONTableName(config)
	case model.DatabaseTypeOSSDelta:
		return object_storage.OSSDeltaTableName(config)
	case model.DatabaseTypeS3Delta:
		return object_storage.S3DeltaTableName(config)
	case model.DatabaseTypeMinIODelta:
		return object_storage.MinIODeltaTableName(config)
	case model.DatabaseTypeAzureDelta:
		return object_storage.AzureDeltaTableName(config)
	case model.DatabaseTypeOzoneDelta:
		return object_storage.OzoneDeltaTableName(config)
	case model.DatabaseTypeTencentCOSDelta:
		return object_storage.COSDeltaTableName(config)
	case model.DatabaseTypeMinIOIceberg, model.DatabaseTypeTencentCOSIceberg:
		return icebergRESTTableName(dbType, config)
	case model.DatabaseTypeMinIOORC:
		return object_storage.MinIOORCTableName(config)
	default:
		return "", fmt.Errorf("object SQL metadata is not supported for %s", dbType)
	}
}

// extractCSVTable asks the configured adapter for column metadata. CSV has no
// declared schema, so the reported types are inferred from the object using
// the same parser and rules as SELECT queries; no row values are returned.
func extractCSVTable(ctx context.Context, db *sql.DB, config *model.DataSourceConfig, dbType model.DatabaseType) (*TableSchema, error) {
	name, err := csvTableNameForSource(dbType, config)
	if err != nil {
		return nil, err
	}
	// Quote the configured identifier, including embedded quote characters.
	query := "SELECT * FROM `" + strings.ReplaceAll(name, "`", "``") + "`"
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect CSV table %q: %w", name, err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect CSV columns: %w", err)
	}
	columns := make([]ColumnSchema, len(types))
	for i, col := range types {
		nullable, known := col.Nullable()
		if !known {
			nullable = true
		}
		columns[i] = ColumnSchema{Name: col.Name(), Type: col.DatabaseTypeName(), Nullable: nullable}
	}
	return &TableSchema{Name: name, Type: "TABLE", Columns: columns}, nil
}

// extractObjectSQLTable obtains the configured object's schema through
// LIMIT 0. Format adapters may decode records when schema inference requires it.
func extractObjectSQLTable(ctx context.Context, db *sql.DB, config *model.DataSourceConfig, dbType model.DatabaseType) (*TableSchema, error) {
	name, err := objectSQLTableNameForSource(dbType, config)
	if err != nil {
		return nil, err
	}
	query := "SELECT * FROM `" + strings.ReplaceAll(name, "`", "``") + "` LIMIT 0"
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect flat object table %q: %w", name, err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect object columns: %w", err)
	}
	columns := make([]ColumnSchema, len(types))
	for i, col := range types {
		nullable, known := col.Nullable()
		if !known {
			nullable = true
		}
		columns[i] = ColumnSchema{Name: col.Name(), Type: col.DatabaseTypeName(), Nullable: nullable}
	}
	return &TableSchema{Name: name, Type: "TABLE", Columns: columns}, nil
}

// ValidateSchema validates that the cached schema is still valid
func (e *MetadataExtractor) ValidateSchema(ctx context.Context, dataSourceID string) error {
	// TODO: Implement schema validation by checking table count or checksums
	return nil
}

// extractTables extracts all tables from the data source
func (e *MetadataExtractor) extractTables(ctx context.Context, db *sql.DB, dbType model.DatabaseType) (map[string]*TableSchema, error) {
	tables := make(map[string]*TableSchema)

	// Query to get all tables
	tableQuery := e.getTableQuery(dbType)

	rows, err := db.QueryContext(ctx, tableQuery)
	if err != nil {
		return nil, fmt.Errorf("failed to query tables: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var tableName, tableType string
		var tableSchema sql.NullString

		// Scan and column failures must surface: swallowing them reported a
		// partial schema as complete success (#108).
		if err := e.scanTableRow(rows, &tableName, &tableType, &tableSchema); err != nil {
			return nil, fmt.Errorf("failed to scan table row: %w", err)
		}

		// Extract columns for this table
		columns, err := e.extractColumns(ctx, db, dbType, tableName, tableSchema.String)
		if err != nil {
			return nil, fmt.Errorf("failed to extract columns for table %q: %w", tableName, err)
		}

		// Extract indexes
		indexes, err := e.extractIndexes(ctx, db, dbType, tableName, tableSchema.String)
		if err != nil {
			indexes = []IndexSchema{} // Continue without indexes
		}

		// Extract primary key
		pk, err := e.extractPrimaryKey(ctx, db, dbType, tableName, tableSchema.String)
		if err != nil {
			pk = []string{} // Continue without primary key
		}

		tables[tableName] = &TableSchema{
			Name:       tableName,
			Schema:     tableSchema.String,
			Type:       tableType,
			Columns:    columns,
			PrimaryKey: pk,
			Indexes:    indexes,
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate table rows: %w", err)
	}

	return tables, nil
}

// extractColumns extracts column information for a table
func (e *MetadataExtractor) extractColumns(ctx context.Context, db *sql.DB, dbType model.DatabaseType, tableName, schemaName string) ([]ColumnSchema, error) {
	query := e.getColumnQuery(dbType, tableName, schemaName)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []ColumnSchema
	for rows.Next() {
		var colName, colType string
		var isNullable bool
		var colDefault sql.NullString
		var colComment sql.NullString

		if err := e.scanColumnRow(rows, dbType, &colName, &colType, &isNullable, &colDefault, &colComment); err != nil {
			return nil, fmt.Errorf("failed to scan column row: %w", err)
		}

		columns = append(columns, ColumnSchema{
			Name:     colName,
			Type:     colType,
			Nullable: isNullable,
			Default:  colDefault.String,
			Comment:  colComment.String,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate column rows: %w", err)
	}

	return columns, nil
}

// extractIndexes extracts index information for a table
func (e *MetadataExtractor) extractIndexes(ctx context.Context, db *sql.DB, dbType model.DatabaseType, tableName, schemaName string) ([]IndexSchema, error) {
	// ClickHouse primary keys and data-skipping indexes do not map to the
	// portable IndexSchema contract. Querying information_schema for them also
	// defeats the deliberately minimal system-table grants used by browser
	// onboarding, so report this supplemental metadata as unavailable.
	if dbType == model.DatabaseTypeClickHouse {
		return []IndexSchema{}, nil
	}
	query := e.getIndexQuery(dbType, tableName, schemaName)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	readRow, err := indexRowReader(dbType, rows)
	if err != nil {
		return nil, err
	}

	var indexes []IndexSchema
	indexMap := make(map[string]*IndexSchema)
	parts := make(map[string][]indexPart)

	for rows.Next() {
		indexName, columnName, seq, isUnique, err := readRow(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan index row: %w", err)
		}

		parts[indexName] = append(parts[indexName], indexPart{column: columnName, seq: seq})
		if _, exists := indexMap[indexName]; !exists {
			indexMap[indexName] = &IndexSchema{
				Name:   indexName,
				Unique: isUnique,
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate index rows: %w", err)
	}

	for name, idx := range indexMap {
		// Composite columns follow Seq_in_index, not the order the rows arrive in.
		p := parts[name]
		sort.SliceStable(p, func(i, j int) bool { return p[i].seq < p[j].seq })
		for _, part := range p {
			idx.Columns = append(idx.Columns, part.column)
		}
		indexes = append(indexes, *idx)
	}

	return indexes, nil
}

// indexRowReader returns a function that reads one index row as
// (indexName, columnName, isUnique). MySQL/MariaDB SHOW INDEX returns 14-15
// columns that vary by server version, so its fields are located by name rather
// than by position. Other dialects return exactly the three scanned columns.
// indexPart is one column of an index, with its position within the index.
type indexPart struct {
	column string
	seq    int64
}

func indexRowReader(dbType model.DatabaseType, rows *sql.Rows) (func(*sql.Rows) (string, string, int64, bool, error), error) {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB:
		cols, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("failed to read index result columns: %w", err)
		}
		pos := make(map[string]int, len(cols))
		for i, name := range cols {
			pos[name] = i
		}
		keyPos, hasKey := pos["Key_name"]
		colPos, hasCol := pos["Column_name"]
		nonUniquePos, hasNonUnique := pos["Non_unique"]
		seqPos, hasSeq := pos["Seq_in_index"]
		if !hasKey || !hasCol || !hasNonUnique || !hasSeq {
			return nil, fmt.Errorf("SHOW INDEX result lacks Key_name, Column_name, Non_unique or Seq_in_index: %v", cols)
		}
		return func(rows *sql.Rows) (string, string, int64, bool, error) {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				return "", "", 0, false, err
			}
			seq, err := strconv.ParseInt(indexValueString(vals[seqPos]), 10, 64)
			if err != nil {
				return "", "", 0, false, fmt.Errorf("invalid Seq_in_index %q: %w", indexValueString(vals[seqPos]), err)
			}
			isUnique := indexValueString(vals[nonUniquePos]) == "0"
			return indexValueString(vals[keyPos]), indexValueString(vals[colPos]), seq, isUnique, nil
		}, nil
	default:
		// Only the three scanned columns exist; row order is the column order.
		return func(rows *sql.Rows) (string, string, int64, bool, error) {
			var indexName, columnName string
			var isUnique bool
			err := rows.Scan(&indexName, &columnName, &isUnique)
			return indexName, columnName, 0, isUnique, err
		}, nil
	}
}

// indexValueString renders a driver value from SHOW INDEX, which may arrive as
// []byte (text protocol) or an integer or string depending on the driver.
func indexValueString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}

// extractPrimaryKey extracts primary key information for a table
func (e *MetadataExtractor) extractPrimaryKey(ctx context.Context, db *sql.DB, dbType model.DatabaseType, tableName, schemaName string) ([]string, error) {
	// See extractIndexes: ClickHouse ordering keys are not relational primary
	// keys, and browser onboarding does not require unrelated information_schema
	// permissions merely to describe table columns.
	if dbType == model.DatabaseTypeClickHouse {
		return []string{}, nil
	}
	query := e.getPrimaryKeyQuery(dbType, tableName, schemaName)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pkColumns []string
	for rows.Next() {
		var columnName string
		if err := rows.Scan(&columnName); err != nil {
			return nil, fmt.Errorf("failed to scan primary-key row: %w", err)
		}
		pkColumns = append(pkColumns, columnName)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate primary-key rows: %w", err)
	}

	return pkColumns, nil
}

// =============================================================================
// Database-specific queries
// =============================================================================

func escapeSQLStringLiteral(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func postgresRegclassLiteral(schemaName, tableName string) string {
	quoteIdentifier := func(value string) string {
		return "\"" + strings.ReplaceAll(value, "\"", "\"\"") + "\""
	}
	qualifiedName := quoteIdentifier(tableName)
	if schemaName != "" {
		qualifiedName = quoteIdentifier(schemaName) + "." + qualifiedName
	}
	return "'" + escapeSQLStringLiteral(qualifiedName) + "'"
}

// mysqlIdentifier wraps a MySQL/MariaDB table name in backticks, doubling any
// embedded backtick, so a user-supplied name cannot break out of the identifier
// position in SHOW COLUMNS / SHOW INDEX.
func mysqlIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func (e *MetadataExtractor) getTableQuery(dbType model.DatabaseType) string {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB, model.DatabaseTypeApacheDoris, model.DatabaseTypeStarRocks:
		// information_schema instead of SHOW TABLE STATUS: the result aligns
		// with the three-column scan and reports the real schema and type
		// (SHOW TABLE STATUS would feed the row count into the schema column).
		return "SELECT table_name, table_type, table_schema FROM information_schema.tables WHERE table_schema = DATABASE()"
	case model.DatabaseTypeClickHouse:
		// ClickHouse's information_schema compatibility views are not usable by
		// every least-privilege native-protocol account. system.tables is the
		// supported metadata catalog and stays scoped to the configured database.
		// The expression preserves the extractor's TABLE/VIEW contract without
		// exposing temporary objects. Do not depend on is_dropped here: it is not
		// a portable system.tables column across the pinned ClickHouse baseline.
		return "SELECT name, if(engine IN ('View', 'MaterializedView', 'LiveView', 'WindowView'), 'VIEW', 'TABLE'), database FROM system.tables WHERE database = currentDatabase() AND is_temporary = 0"
	case model.DatabaseTypePostgreSQL:
		// Ordinary views live in pg_views, separate from pg_tables. Materialized
		// views are not included: they are a distinct relation kind.
		return "SELECT tablename, 'TABLE' as tabletype, schemaname FROM pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema') " +
			"UNION ALL SELECT viewname, 'VIEW' as tabletype, schemaname FROM pg_views WHERE schemaname NOT IN ('pg_catalog', 'information_schema')"
	case model.DatabaseTypeOracle:
		return "SELECT table_name, 'TABLE', owner FROM all_tables WHERE owner NOT IN ('SYS', 'SYSTEM')"
	case model.DatabaseTypeSnowflake, model.DatabaseTypeBigQuery, model.DatabaseTypeRedshift:
		// Warehouse-specific queries - to be implemented. The column list must
		// match the three-column scanTableRow contract; a two-column select
		// failed every row's scan and masqueraded as an empty schema (#108).
		return "SELECT table_name, table_type, table_schema FROM information_schema.tables"
	default:
		// Default to information_schema
		return "SELECT table_name, table_type, table_schema FROM information_schema.tables"
	}
}

func (e *MetadataExtractor) getColumnQuery(dbType model.DatabaseType, tableName, schemaName string) string {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB, model.DatabaseTypeApacheDoris, model.DatabaseTypeStarRocks:
		return fmt.Sprintf("SHOW COLUMNS FROM %s", mysqlIdentifier(tableName))
	case model.DatabaseTypePostgreSQL, model.DatabaseTypeOpenGauss:
		return fmt.Sprintf("SELECT column_name, data_type, is_nullable, column_default, '' FROM information_schema.columns WHERE table_name = '%s' AND table_schema = '%s'", escapeSQLStringLiteral(tableName), escapeSQLStringLiteral(schemaName))
	case model.DatabaseTypeOracle:
		return fmt.Sprintf("SELECT column_name, data_type, nullable, data_default, '' FROM all_tab_columns WHERE table_name = '%s' AND owner = '%s'", escapeSQLStringLiteral(tableName), escapeSQLStringLiteral(schemaName))
	case model.DatabaseTypeClickHouse:
		// ClickHouse keeps column metadata in system.columns rather than the
		// portable information_schema shape. Return the same five columns that
		// scanColumnRow expects, with a conservative Nullable(...) detection.
		return fmt.Sprintf("SELECT name, type, if(positionCaseInsensitive(type, 'Nullable(') > 0, 'YES', 'NO'), nullIf(default_expression, ''), comment FROM system.columns WHERE database = currentDatabase() AND table = '%s'", escapeSQLStringLiteral(tableName))
	default:
		return fmt.Sprintf("SELECT column_name, data_type, is_nullable, column_default, '' FROM information_schema.columns WHERE table_name = '%s'", escapeSQLStringLiteral(tableName))
	}
}

func (e *MetadataExtractor) getIndexQuery(dbType model.DatabaseType, tableName, schemaName string) string {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB:
		return fmt.Sprintf("SHOW INDEX FROM %s", mysqlIdentifier(tableName))
	case model.DatabaseTypePostgreSQL:
		return fmt.Sprintf("SELECT i.indexname, a.attname, i.indexdef LIKE '%%UNIQUE%%' FROM pg_indexes i JOIN pg_attribute a ON a.attname = ANY(string_to_array(replace(regexp_replace(i.indexdef, '.*\\((.*)\\).*', '\\1'), '\"', ''), ', ')) WHERE i.tablename = '%s' AND i.schemaname = '%s'", escapeSQLStringLiteral(tableName), escapeSQLStringLiteral(schemaName))
	default:
		return fmt.Sprintf("SELECT index_name, column_name, not non_unique FROM information_schema.statistics WHERE table_name = '%s'", escapeSQLStringLiteral(tableName))
	}
}

func (e *MetadataExtractor) getPrimaryKeyQuery(dbType model.DatabaseType, tableName, schemaName string) string {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB:
		return fmt.Sprintf("SELECT column_name FROM information_schema.key_column_usage WHERE table_name = '%s' AND constraint_name = 'PRIMARY'", escapeSQLStringLiteral(tableName))
	case model.DatabaseTypePostgreSQL:
		return fmt.Sprintf("SELECT a.attname FROM pg_index i JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey) WHERE i.indrelid = %s::regclass AND i.indisprimary", postgresRegclassLiteral(schemaName, tableName))
	default:
		return fmt.Sprintf("SELECT column_name FROM information_schema.key_column_usage WHERE table_name = '%s' AND constraint_name LIKE '%%PRIMARY%%'", escapeSQLStringLiteral(tableName))
	}
}

// =============================================================================
// Row scanning helpers
// =============================================================================

func (e *MetadataExtractor) scanTableRow(rows *sql.Rows, tableName, tableType *string, tableSchema *sql.NullString) error {
	// Every getTableQuery variant — including the MySQL/MariaDB
	// information_schema query — returns (table_name, table_type,
	// table_schema), so one three-column scan serves all dialects.
	if err := rows.Scan(tableName, tableType, tableSchema); err != nil {
		return err
	}
	return nil
}

func (e *MetadataExtractor) scanColumnRow(rows *sql.Rows, dbType model.DatabaseType, colName, colType *string, isNullable *bool, colDefault, colComment *sql.NullString) error {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB, model.DatabaseTypeApacheDoris, model.DatabaseTypeStarRocks:
		var field, typ, null, key, extra string
		// SHOW COLUMNS reports NULL for columns without a default; scanning
		// that into a plain string fails and dropped the whole row (#201
		// review).
		var defaultVal sql.NullString
		if err := rows.Scan(&field, &typ, &null, &key, &defaultVal, &extra); err != nil {
			return err
		}
		*colName = field
		*colType = typ
		*isNullable = strings.ToUpper(null) == "YES"
		*colDefault = defaultVal
		*colComment = sql.NullString{Valid: false}
	default:
		// Catalog queries return strings: information_schema uses YES/NO
		// while Oracle's ALL_TAB_COLUMNS uses Y/N.
		var nullable string
		if err := rows.Scan(colName, colType, &nullable, colDefault, colComment); err != nil {
			return err
		}
		nullableFlag := "YES"
		if dbType == model.DatabaseTypeOracle {
			nullableFlag = "Y"
		}
		*isNullable = strings.EqualFold(nullable, nullableFlag)
	}
	return nil
}

// generateSchemaVersion generates a version identifier for the schema
func (e *MetadataExtractor) generateSchemaVersion(schema *DataSourceSchema) string {
	// Simple version based on timestamp
	// In production, use a hash of table names, columns, and counts
	return fmt.Sprintf("v%d", time.Now().Unix())
}

// GetCachedSchemaWithRefresh returns cached schema or refreshes if expired
func (e *MetadataExtractor) GetCachedSchemaWithRefresh(ctx context.Context, cache *SchemaCache, dataSourceID string) (*DataSourceSchema, error) {
	return cache.Refresh(ctx, dataSourceID, e)
}

// ExtractTableCount returns the number of tables in a data source
func (e *MetadataExtractor) ExtractTableCount(ctx context.Context, dataSourceID string) (int, error) {
	schema, _, err := e.ExtractSchema(ctx, dataSourceID)
	if err != nil {
		return 0, err
	}
	return len(schema.Tables), nil
}
