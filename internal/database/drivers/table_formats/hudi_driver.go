package table_formats

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/common"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// Using common schema types from drivers/common package

// HudiDriver implements the Driver interface for Apache Hudi tables
type HudiMetadataReader interface {
	TestConnection(context.Context) error
	GetTableMetadata(context.Context, string) (*HudiTableMetadata, error)
	ListPartitions(context.Context, string) ([]HudiPartition, error)
	GetCommits(context.Context, string) ([]HudiCommit, error)
	GetSavepoints(context.Context, string) ([]HudiSavepoint, error)
}

// HudiDriver exposes Hudi metadata and incremental-read contracts through
// explicitly supplied storage and query-engine adapters.
type HudiDriver struct {
	metadataReader      HudiMetadataReader
	parser              *HudiMetadataParser
	config              *HudiConfig
	incrementalExecutor HudiIncrementalExecutor
}

// NewHudiDriver creates a new Hudi driver
func NewHudiDriver(config *HudiConfig) (*HudiDriver, error) {
	if config == nil {
		return nil, fmt.Errorf("Hudi configuration is required")
	}
	return &HudiDriver{
		metadataReader:      config.MetadataReader,
		parser:              NewHudiMetadataParser(),
		config:              config,
		incrementalExecutor: config.IncrementalExecutor,
	}, nil
}

// Open opens a connection (not applicable for Hudi which uses compute engines)
func (d *HudiDriver) Open(dsn string) (*sql.DB, error) {
	return nil, fmt.Errorf("Hudi tables require a configured query-engine adapter; Apache Hudi has no generic SQL connection protocol")
}

// ValidateDSN validates the connection string
func (d *HudiDriver) ValidateDSN(dsn string) error {
	if dsn == "" {
		return fmt.Errorf("DSN cannot be empty")
	}
	return nil
}

// GetDefaultPort returns the default port
func (d *HudiDriver) GetDefaultPort() int {
	return 0 // Hudi is a table format, not a network service with a standard port.
}

// BuildDSN builds a connection string from configuration
func (d *HudiDriver) BuildDSN(config *model.DataSourceConfig) string {
	return fmt.Sprintf("hudi://%s:%d/%s", config.Host, config.Port, config.Database)
}

// GetDatabaseTypeName returns the database type name
func (d *HudiDriver) GetDatabaseTypeName() string {
	return "hudi"
}

// TestConnection tests the connection
func (d *HudiDriver) TestConnection(db *sql.DB) error {
	return fmt.Errorf("use TestConnectionREST instead for Hudi")
}

// TestConnectionREST tests REST API connectivity
func (d *HudiDriver) TestConnectionREST(ctx context.Context) error {
	if d.metadataReader != nil {
		return d.metadataReader.TestConnection(ctx)
	}
	if d.incrementalExecutor != nil {
		return nil
	}
	return fmt.Errorf("configure a Hudi metadata reader or query-engine adapter")
}

// GetDriverName returns the driver name
func (d *HudiDriver) GetDriverName() string {
	return "hudi-engine-adapter"
}

// GetCategory returns the driver category
func (d *HudiDriver) GetCategory() drivers.DriverCategory {
	return drivers.CategoryTableFormat
}

// GetCapabilities returns driver capabilities
func (d *HudiDriver) GetCapabilities() drivers.DriverCapabilities {
	return drivers.DriverCapabilities{
		SupportsSQL:               false,
		SupportsTransaction:       false,
		SupportsSchemaDiscovery:   d.metadataReader != nil,
		SupportsTimeTravel:        false,
		RequiresTokenRotation:     false,
		SupportsStreaming:         false,
		SupportsSnapshotDiscovery: d.metadataReader != nil,
		SupportsIncrementalRead:   d.incrementalExecutor != nil,
	}
}

// ConfigureAuth configures authentication
func (d *HudiDriver) ConfigureAuth(authConfig interface{}) error {
	return nil
}

// =============================================================================
// Apache Hudi-Specific Methods
// =============================================================================

func (d *HudiDriver) validateIncrementalFormat(ctx context.Context, options HudiIncrementalReadOptions) error {
	if options.IncrementalFormat != "cdc" || d.metadataReader == nil {
		// Without a storage reader, the query engine must validate table type and
		// CDC enablement before it returns records.
		return nil
	}
	metadata, err := d.metadataReader.GetTableMetadata(ctx, options.BasePath)
	if err != nil {
		return fmt.Errorf("validate Hudi CDC table metadata: %w", err)
	}
	if metadata == nil {
		return fmt.Errorf("validate Hudi CDC table metadata: storage reader returned no metadata")
	}
	if ParseHudiTableType(metadata.TableType) != string(CopyOnWriteTableType) {
		return fmt.Errorf("Hudi CDC reads require a Copy-on-Write table")
	}
	if !metadata.CDCEnabled {
		return fmt.Errorf("Hudi CDC reads require hoodie.table.cdc.enabled=true")
	}
	return nil
}

func (d *HudiDriver) readTableMetadata(ctx context.Context, basePath string) (*HudiTableMetadata, error) {
	if d.metadataReader == nil {
		return nil, fmt.Errorf("Hudi metadata discovery requires a storage metadata reader")
	}
	return d.metadataReader.GetTableMetadata(ctx, basePath)
}

func (d *HudiDriver) listPartitions(ctx context.Context, basePath string) ([]HudiPartition, error) {
	if d.metadataReader == nil {
		return nil, fmt.Errorf("Hudi partition discovery requires a storage metadata reader")
	}
	return d.metadataReader.ListPartitions(ctx, basePath)
}

func (d *HudiDriver) readCommits(ctx context.Context, basePath string) ([]HudiCommit, error) {
	if d.metadataReader == nil {
		return nil, fmt.Errorf("Hudi timeline discovery requires a storage metadata reader")
	}
	return d.metadataReader.GetCommits(ctx, basePath)
}

func (d *HudiDriver) readSavepoints(ctx context.Context, basePath string) ([]HudiSavepoint, error) {
	if d.metadataReader == nil {
		return nil, fmt.Errorf("Hudi savepoint discovery requires a storage metadata reader")
	}
	return d.metadataReader.GetSavepoints(ctx, basePath)
}

// ListPartitions lists all partitions for a table
func (d *HudiDriver) ListPartitions(ctx context.Context, basePath string) ([]HudiPartition, error) {
	return d.listPartitions(ctx, basePath)
}

// GetTableMetadata retrieves table metadata
func (d *HudiDriver) GetTableMetadata(ctx context.Context, basePath string) (*HudiTableMetadata, error) {
	return d.readTableMetadata(ctx, basePath)
}

// GetTableSchema retrieves table schema in standard format
func (d *HudiDriver) GetTableSchema(ctx context.Context, basePath, tableName string) (*common.TableSchema, error) {
	metadata, err := d.readTableMetadata(ctx, basePath)
	if err != nil {
		return nil, err
	}

	databaseSchema, err := d.parser.ParseTableMetadata(metadata, tableName)
	if err != nil {
		return nil, err
	}

	tableSchema, exists := databaseSchema.Tables[tableName]
	if !exists {
		return nil, fmt.Errorf("table not found in schema: %s", tableName)
	}

	return tableSchema, nil
}

// QueryTable executes a SQL query
func (d *HudiDriver) QueryTable(ctx context.Context, sql string) (*HudiQueryResult, error) {
	return nil, fmt.Errorf("Apache Hudi has no generic SQL REST endpoint; execute SQL through a configured query-engine adapter")
}

// GetCommits retrieves commit timeline
func (d *HudiDriver) GetCommits(ctx context.Context, basePath string) ([]HudiCommit, error) {
	return d.readCommits(ctx, basePath)
}

// GetSavepoints retrieves savepoints
func (d *HudiDriver) GetSavepoints(ctx context.Context, basePath string) ([]HudiSavepoint, error) {
	return d.readSavepoints(ctx, basePath)
}

// GetStatistics retrieves table statistics
func (d *HudiDriver) GetStatistics(ctx context.Context, basePath string) (*HudiTableStatistics, error) {
	commits, err := d.readCommits(ctx, basePath)
	if err != nil {
		return nil, err
	}

	partitions, err := d.listPartitions(ctx, basePath)
	if err != nil {
		return nil, err
	}

	// Timeline files identify completed instants but do not contain enough
	// information to estimate row-level insert/update/delete counts.
	partitionStats := d.parser.GetPartitionStats(partitions)

	return &HudiTableStatistics{
		RecordStats:    nil,
		PartitionStats: partitionStats,
		CommitCount:    len(commits),
	}, nil
}

// HudiTableStatistics contains table-level statistics
type HudiTableStatistics struct {
	RecordStats    *HudiRecordStats
	PartitionStats *PartitionStats
	CommitCount    int
}

// GetTableType determines if table is COW or MOR
func (d *HudiDriver) GetTableType(ctx context.Context, basePath string) (string, error) {
	metadata, err := d.readTableMetadata(ctx, basePath)
	if err != nil {
		return "", err
	}

	return d.parser.GetTableType(metadata), nil
}

// GetCommitAtTime retrieves commit for a specific timestamp
func (d *HudiDriver) GetCommitAtTime(ctx context.Context, basePath string, timestamp time.Time) (*HudiCommit, error) {
	commits, err := d.readCommits(ctx, basePath)
	if err != nil {
		return nil, err
	}

	return d.parser.GetCommitAtTime(commits, timestamp)
}

// GetLatestCommit retrieves the latest commit
func (d *HudiDriver) GetLatestCommit(ctx context.Context, basePath string) (*HudiCommit, error) {
	commits, err := d.readCommits(ctx, basePath)
	if err != nil {
		return nil, err
	}

	return d.parser.GetLatestCommit(commits)
}

// GetCommitTimeline retrieves commit timeline
func (d *HudiDriver) GetCommitTimeline(ctx context.Context, basePath string) ([]CommitTimelineEntry, error) {
	commits, err := d.readCommits(ctx, basePath)
	if err != nil {
		return nil, err
	}

	return d.parser.GetCommitTimeline(commits), nil
}

// SupportsIncrementalQuery checks if incremental queries are supported
func (d *HudiDriver) SupportsIncrementalQuery(ctx context.Context, basePath string) (bool, error) {
	metadata, err := d.readTableMetadata(ctx, basePath)
	if err != nil {
		return false, err
	}

	return d.parser.SupportsIncrementalQuery(metadata), nil
}

// GetRecordStats calculates record statistics
func (d *HudiDriver) GetRecordStats(ctx context.Context, basePath string) (*HudiRecordStats, error) {
	commits, err := d.readCommits(ctx, basePath)
	if err != nil {
		return nil, err
	}

	return d.parser.CalculateRecordStats(commits), nil
}

// GetPartitionSpec returns partition specification
func (d *HudiDriver) GetPartitionSpec(ctx context.Context, basePath string) ([]string, error) {
	metadata, err := d.readTableMetadata(ctx, basePath)
	if err != nil {
		return nil, err
	}

	return d.parser.GetPartitionSpec(metadata), nil
}

// GetOperationTimeline retrieves operation timeline
func (d *HudiDriver) GetOperationTimeline(ctx context.Context, basePath string) ([]OperationEvent, error) {
	commits, err := d.readCommits(ctx, basePath)
	if err != nil {
		return nil, err
	}

	return d.parser.GetOperationTimeline(commits), nil
}

// RollbackToInstant rolls back to a specific instant
func (d *HudiDriver) RollbackToInstant(ctx context.Context, basePath, instantTime string) error {
	// Hudi rollback requires executing rollback action
	// This would typically be done through Spark or REST API
	return fmt.Errorf("rollback not yet implemented")
}

// Savepoint creates a savepoint
func (d *HudiDriver) Savepoint(ctx context.Context, basePath, instantTime, user string) error {
	// Savepoint creation requires executing savepoint action
	return fmt.Errorf("savepoint creation not yet implemented")
}

// RestoreToSavepoint restores to a savepoint
func (d *HudiDriver) RestoreToSavepoint(ctx context.Context, basePath, savepointTime string) error {
	// Restore requires executing restore action
	return fmt.Errorf("restore to savepoint not yet implemented")
}

// CompactTable performs compaction (for MOR tables)
func (d *HudiDriver) CompactTable(ctx context.Context, basePath string) error {
	return fmt.Errorf("compaction not yet implemented")
}

// ClusterTable performs clustering
func (d *HudiDriver) ClusterTable(ctx context.Context, basePath string) error {
	return fmt.Errorf("clustering not yet implemented")
}

// GetCommitInfo retrieves detailed commit information
func (d *HudiDriver) GetCommitInfo(ctx context.Context, basePath, commitTime string) (*HudiCommit, error) {
	commits, err := d.readCommits(ctx, basePath)
	if err != nil {
		return nil, err
	}

	for _, commit := range commits {
		if commit.CommitTime == commitTime {
			return &commit, nil
		}
	}

	return nil, fmt.Errorf("commit not found: %s", commitTime)
}

// GetSavepointInfo retrieves savepoint information
func (d *HudiDriver) GetSavepointInfo(ctx context.Context, basePath, savepointTime string) (*HudiSavepoint, error) {
	savepoints, err := d.readSavepoints(ctx, basePath)
	if err != nil {
		return nil, err
	}

	for _, sp := range savepoints {
		if sp.SavepointTime == savepointTime {
			return &sp, nil
		}
	}

	return nil, fmt.Errorf("savepoint not found: %s", savepointTime)
}

// ValidateTableConfig validates Hudi table configuration
func (d *HudiDriver) ValidateTableConfig(config map[string]string) error {
	return d.parser.ValidateHudiTableConfig(config)
}

// GetCompressionCodec returns the compression codec
func (d *HudiDriver) GetCompressionCodec(ctx context.Context, basePath string) (string, error) {
	metadata, err := d.readTableMetadata(ctx, basePath)
	if err != nil {
		return "", err
	}

	return d.parser.GetCompressionCodec(metadata), nil
}

// ApplyBatchPagination applies pagination to a SQL query
func (d *HudiDriver) ApplyBatchPagination(sql string, batchSize, offset int64) (string, error) {
	// Hudi doesn't support direct pagination via SQL, but we can implement it using LIMIT and OFFSET
	// when integrated with compute engines like Spark, Flink, etc.
	if batchSize <= 0 {
		batchSize = 1000 // Default batch size
	}

	// For Hudi, we append LIMIT and OFFSET to the query
	// Note: This assumes the query doesn't already have LIMIT clause
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", sql, batchSize, offset), nil
}
