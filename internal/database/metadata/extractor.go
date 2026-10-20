package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
)

var (
	ErrMetadataRepositoryRequired     = errors.New("data source repository is required")
	ErrMetadataConnectionPoolRequired = errors.New("metadata connection pool is required")
	ErrMetadataDriverRegistryRequired = errors.New("metadata driver registry is required")
	ErrSchemaDiscoveryUnsupported     = errors.New("schema discovery is not supported")
	ErrTableNotFound                  = errors.New("table not found")
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
	tables, err := e.extractTables(ctx, db, dataSource.Type)
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
	dataSource, db, err := e.resolveConnection(ctx, dataSourceID)
	if err != nil {
		return nil, err
	}

	schemaName, tableType, found, err := e.findTable(ctx, db, dataSource.Type, tableName)
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
func (e *MetadataExtractor) findTable(ctx context.Context, db *sql.DB, dbType model.DatabaseType, tableName string) (schemaName, tableType string, found bool, err error) {
	rows, err := db.QueryContext(ctx, e.getTableQuery(dbType))
	if err != nil {
		return "", "", false, err
	}
	defer rows.Close()

	for rows.Next() {
		var name, kind string
		var schema sql.NullString
		if err := e.scanTableRow(rows, dbType, &name, &kind, &schema); err != nil {
			continue
		}
		if name == tableName {
			return schema.String, kind, true, nil
		}
	}
	return "", "", false, rows.Err()
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

		// Scan based on database type
		if err := e.scanTableRow(rows, dbType, &tableName, &tableType, &tableSchema); err != nil {
			continue
		}

		// Extract columns for this table
		columns, err := e.extractColumns(ctx, db, dbType, tableName, tableSchema.String)
		if err != nil {
			continue
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
			continue
		}

		columns = append(columns, ColumnSchema{
			Name:     colName,
			Type:     colType,
			Nullable: isNullable,
			Default:  colDefault.String,
			Comment:  colComment.String,
		})
	}

	return columns, nil
}

// extractIndexes extracts index information for a table
func (e *MetadataExtractor) extractIndexes(ctx context.Context, db *sql.DB, dbType model.DatabaseType, tableName, schemaName string) ([]IndexSchema, error) {
	query := e.getIndexQuery(dbType, tableName, schemaName)

	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var indexes []IndexSchema
	indexMap := make(map[string]*IndexSchema)

	for rows.Next() {
		var indexName, columnName string
		var isUnique bool

		if err := rows.Scan(&indexName, &columnName, &isUnique); err != nil {
			continue
		}

		if idx, exists := indexMap[indexName]; exists {
			idx.Columns = append(idx.Columns, columnName)
		} else {
			indexMap[indexName] = &IndexSchema{
				Name:    indexName,
				Columns: []string{columnName},
				Unique:  isUnique,
			}
		}
	}

	for _, idx := range indexMap {
		indexes = append(indexes, *idx)
	}

	return indexes, nil
}

// extractPrimaryKey extracts primary key information for a table
func (e *MetadataExtractor) extractPrimaryKey(ctx context.Context, db *sql.DB, dbType model.DatabaseType, tableName, schemaName string) ([]string, error) {
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
			continue
		}
		pkColumns = append(pkColumns, columnName)
	}

	return pkColumns, nil
}

// =============================================================================
// Database-specific queries
// =============================================================================

func (e *MetadataExtractor) getTableQuery(dbType model.DatabaseType) string {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB:
		// information_schema instead of SHOW TABLE STATUS: the result aligns
		// with the three-column scan and reports the real schema and type
		// (SHOW TABLE STATUS would feed the row count into the schema column).
		return "SELECT table_name, table_type, table_schema FROM information_schema.tables WHERE table_schema = DATABASE()"
	case model.DatabaseTypePostgreSQL:
		return "SELECT tablename, 'TABLE' as tabletype, schemaname FROM pg_tables WHERE schemaname NOT IN ('pg_catalog', 'information_schema')"
	case model.DatabaseTypeOracle:
		return "SELECT table_name, 'TABLE', owner FROM all_tables WHERE owner NOT IN ('SYS', 'SYSTEM')"
	case model.DatabaseTypeSnowflake, model.DatabaseTypeBigQuery, model.DatabaseTypeRedshift:
		// Warehouse-specific queries - to be implemented
		return "SELECT table_name, table_type FROM information_schema.tables"
	default:
		// Default to information_schema
		return "SELECT table_name, table_type, table_schema FROM information_schema.tables"
	}
}

func (e *MetadataExtractor) getColumnQuery(dbType model.DatabaseType, tableName, schemaName string) string {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB:
		return fmt.Sprintf("SHOW COLUMNS FROM %s", tableName)
	case model.DatabaseTypePostgreSQL:
		return fmt.Sprintf("SELECT column_name, data_type, is_nullable, column_default, '' FROM information_schema.columns WHERE table_name = '%s' AND table_schema = '%s'", tableName, schemaName)
	case model.DatabaseTypeOracle:
		return fmt.Sprintf("SELECT column_name, data_type, nullable, data_default, '' FROM all_tab_columns WHERE table_name = '%s'", tableName)
	default:
		return fmt.Sprintf("SELECT column_name, data_type, is_nullable, column_default, '' FROM information_schema.columns WHERE table_name = '%s'", tableName)
	}
}

func (e *MetadataExtractor) getIndexQuery(dbType model.DatabaseType, tableName, schemaName string) string {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB:
		return fmt.Sprintf("SHOW INDEX FROM %s", tableName)
	case model.DatabaseTypePostgreSQL:
		return fmt.Sprintf("SELECT i.indexname, a.attname, i.indexdef LIKE '%%UNIQUE%%' FROM pg_indexes i JOIN pg_attribute a ON a.attname = ANY(string_to_array(replace(regexp_replace(i.indexdef, '.*\\((.*)\\).*', '\\1'), '\"', ''), ', ')) WHERE i.tablename = '%s' AND i.schemaname = '%s'", tableName, schemaName)
	default:
		return fmt.Sprintf("SELECT index_name, column_name, not non_unique FROM information_schema.statistics WHERE table_name = '%s'", tableName)
	}
}

func (e *MetadataExtractor) getPrimaryKeyQuery(dbType model.DatabaseType, tableName, schemaName string) string {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB:
		return fmt.Sprintf("SELECT column_name FROM information_schema.key_column_usage WHERE table_name = '%s' AND constraint_name = 'PRIMARY'", tableName)
	case model.DatabaseTypePostgreSQL:
		return fmt.Sprintf("SELECT a.attname FROM pg_index i JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey) WHERE i.indrelid = '%s'::regclass AND i.indisprimary", tableName)
	default:
		return fmt.Sprintf("SELECT column_name FROM information_schema.key_column_usage WHERE table_name = '%s' AND constraint_name LIKE '%%PRIMARY%%'", tableName)
	}
}

// =============================================================================
// Row scanning helpers
// =============================================================================

func (e *MetadataExtractor) scanTableRow(rows *sql.Rows, dbType model.DatabaseType, tableName, tableType *string, tableSchema *sql.NullString) error {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB:
		var name, engine, rowFormat string
		var comment sql.NullString
		if err := rows.Scan(&name, &engine, &rowFormat, &comment, &tableSchema, &tableType, &comment); err != nil {
			return err
		}
		*tableName = name
		*tableType = "TABLE"
	case model.DatabaseTypePostgreSQL:
		if err := rows.Scan(tableName, tableType, tableSchema); err != nil {
			return err
		}
	default:
		if err := rows.Scan(tableName, tableType, tableSchema); err != nil {
			return err
		}
	}
	return nil
}

func (e *MetadataExtractor) scanColumnRow(rows *sql.Rows, dbType model.DatabaseType, colName, colType *string, isNullable *bool, colDefault, colComment *sql.NullString) error {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB:
		var field, typ, null, key, defaultVal, extra string
		if err := rows.Scan(&field, &typ, &null, &key, &defaultVal, &extra); err != nil {
			return err
		}
		*colName = field
		*colType = typ
		*isNullable = strings.ToUpper(null) == "YES"
		*colDefault = sql.NullString{String: defaultVal, Valid: defaultVal != ""}
		*colComment = sql.NullString{Valid: false}
	default:
		// information_schema reports nullability as the strings "YES"/"NO";
		// scanning that into a bool always fails and silently drops the
		// column, so scan the string and convert (#184).
		var nullable string
		if err := rows.Scan(colName, colType, &nullable, colDefault, colComment); err != nil {
			return err
		}
		*isNullable = strings.EqualFold(nullable, "YES")
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
