package table_formats

import (
	"context"
	"fmt"
	"time"
)

// IcebergTimeTravel provides time travel functionality for Iceberg tables
type IcebergTimeTravel struct {
	driver *IcebergDriver
}

// NewIcebergTimeTravel creates a new time travel handler
func NewIcebergTimeTravel(driver *IcebergDriver) *IcebergTimeTravel {
	return &IcebergTimeTravel{driver: driver}
}

// TimeTravelQuery represents a time travel query
type TimeTravelQuery struct {
	Namespace   string
	Table       string
	Timestamp   *time.Time
	SnapshotID  *int64
	VersionAsOf int64 // For Spark-style VERSION AS OF
}

// QueryForSnapshot builds a query for a specific snapshot
func (tt *IcebergTimeTravel) QueryForSnapshot(ctx context.Context, req *TimeTravelQuery) (string, error) {
	if req.SnapshotID == nil && req.Timestamp == nil && req.VersionAsOf == 0 {
		return "", fmt.Errorf("must specify either snapshot_id, timestamp, or version")
	}

	var snapshotID int64
	var err error

	// Determine snapshot ID
	if req.SnapshotID != nil {
		snapshotID = *req.SnapshotID
	} else if req.Timestamp != nil {
		snapshotID, err = tt.driver.GetSnapshotAtTime(ctx, req.Namespace, req.Table, *req.Timestamp)
		if err != nil {
			return "", err
		}
	} else if req.VersionAsOf > 0 {
		snapshotID = req.VersionAsOf
	}

	// Build time travel query
	// Iceberg supports different syntax depending on the compute engine
	return tt.driver.parser.BuildTimeTravelQuery(req.Table, snapshotID), nil
}

// GetSnapshotAtTimestamp retrieves snapshot information for a specific timestamp
func (tt *IcebergTimeTravel) GetSnapshotAtTimestamp(ctx context.Context, namespace, table string, timestamp time.Time) (*IcebergSnapshot, error) {
	snapshotID, err := tt.driver.GetSnapshotAtTime(ctx, namespace, table, timestamp)
	if err != nil {
		return nil, err
	}

	return tt.driver.GetSnapshotInfo(ctx, namespace, table, snapshotID)
}

// GetLatestSnapshot retrieves the latest snapshot
func (tt *IcebergTimeTravel) GetLatestSnapshot(ctx context.Context, namespace, table string) (*IcebergSnapshot, error) {
	metadata, err := tt.driver.restClient.LoadTable(ctx, namespace, table)
	if err != nil {
		return nil, err
	}

	latestID, err := tt.driver.parser.GetLatestSnapshotID(metadata)
	if err != nil {
		return nil, err
	}

	return tt.driver.GetSnapshotInfo(ctx, namespace, table, latestID)
}

// GetSnapshotHistory retrieves snapshot history
func (tt *IcebergTimeTravel) GetSnapshotHistory(ctx context.Context, namespace, table string, limit int) ([]IcebergSnapshot, error) {
	metadata, err := tt.driver.restClient.LoadTable(ctx, namespace, table)
	if err != nil {
		return nil, err
	}

	snapshots := metadata.Snapshots

	// Apply limit if specified
	if limit > 0 && len(snapshots) > limit {
		// Return most recent snapshots
		start := len(snapshots) - limit
		if start < 0 {
			start = 0
		}
		snapshots = snapshots[start:]
	}

	return snapshots, nil
}

// GetSchemaAtSnapshot retrieves the schema a snapshot was written with.
func (tt *IcebergTimeTravel) GetSchemaAtSnapshot(ctx context.Context, namespace, table string, snapshotID int64) (*IcebergSchema, error) {
	snapshot, err := tt.driver.GetSnapshotInfo(ctx, namespace, table, snapshotID)
	if err != nil {
		return nil, err
	}

	metadata, err := tt.driver.restClient.LoadTable(ctx, namespace, table)
	if err != nil {
		return nil, err
	}

	// A snapshot records which schema version it was written with. Resolve that
	// snapshot's schema-id against the table's schema history instead of
	// silently handing back the current schema, which misrepresents the data.
	for i := range metadata.Schemas {
		if metadata.Schemas[i].SchemaID == snapshot.SchemaID {
			return &metadata.Schemas[i], nil
		}
	}

	return nil, fmt.Errorf("Iceberg snapshot %d references schema %d, which was not found in the schema history", snapshotID, snapshot.SchemaID)
}

// CompareSnapshots compares two snapshots and returns differences
func (tt *IcebergTimeTravel) CompareSnapshots(ctx context.Context, namespace, table string, snapshotID1, snapshotID2 int64) (*SnapshotComparison, error) {
	snap1, err := tt.driver.GetSnapshotInfo(ctx, namespace, table, snapshotID1)
	if err != nil {
		return nil, err
	}

	snap2, err := tt.driver.GetSnapshotInfo(ctx, namespace, table, snapshotID2)
	if err != nil {
		return nil, err
	}

	comparison := &SnapshotComparison{
		SnapshotID1: snapshotID1,
		SnapshotID2: snapshotID2,
		Timestamp1:  time.UnixMilli(snap1.TimestampMs),
		Timestamp2:  time.UnixMilli(snap2.TimestampMs),
	}

	// Compare summary
	comparison.Operation1 = snap1.Summary.Operation
	comparison.Operation2 = snap2.Summary.Operation

	// Check if one is parent of the other
	if snap1.ParentSnapshotID != nil && *snap1.ParentSnapshotID == snapshotID2 {
		comparison.Relationship = "child"
	} else if snap2.ParentSnapshotID != nil && *snap2.ParentSnapshotID == snapshotID1 {
		comparison.Relationship = "parent"
	} else {
		comparison.Relationship = "unrelated"
	}

	return comparison, nil
}

// SnapshotComparison represents the comparison between two snapshots
type SnapshotComparison struct {
	SnapshotID1  int64
	SnapshotID2  int64
	Timestamp1   time.Time
	Timestamp2   time.Time
	Operation1   string
	Operation2   string
	Relationship string // "parent", "child", "unrelated"
}

// GetSnapshotsInRange retrieves snapshots within a time range
func (tt *IcebergTimeTravel) GetSnapshotsInRange(ctx context.Context, namespace, table string, startTime, endTime time.Time) ([]IcebergSnapshot, error) {
	metadata, err := tt.driver.restClient.LoadTable(ctx, namespace, table)
	if err != nil {
		return nil, err
	}

	var result []IcebergSnapshot
	startMs := startTime.UnixMilli()
	endMs := endTime.UnixMilli()

	for _, snapshot := range metadata.Snapshots {
		if snapshot.TimestampMs >= startMs && snapshot.TimestampMs <= endMs {
			result = append(result, snapshot)
		}
	}

	return result, nil
}

// GetSnapshotBeforeTime retrieves the latest snapshot before a given time
func (tt *IcebergTimeTravel) GetSnapshotBeforeTime(ctx context.Context, namespace, table string, timestamp time.Time) (*IcebergSnapshot, error) {
	snapshotID, err := tt.driver.GetSnapshotAtTime(ctx, namespace, table, timestamp)
	if err != nil {
		return nil, err
	}

	return tt.driver.GetSnapshotInfo(ctx, namespace, table, snapshotID)
}

// IsSnapshotExists checks if a snapshot exists
func (tt *IcebergTimeTravel) IsSnapshotExists(ctx context.Context, namespace, table string, snapshotID int64) bool {
	_, err := tt.driver.GetSnapshotInfo(ctx, namespace, table, snapshotID)
	return err == nil
}

// GetChangelog retrieves changelog between snapshots
func (tt *IcebergTimeTravel) GetChangelog(ctx context.Context, namespace, table string, startSnapshot, endSnapshot int64) ([]ChangeLogEntry, error) {
	// Changelog would need to be computed by comparing manifest files
	// This is a placeholder implementation
	return []ChangeLogEntry{}, fmt.Errorf("changelog computation not yet implemented")
}

// ChangeLogEntry represents a change between snapshots
type ChangeLogEntry struct {
	SnapshotID  int64
	Operation   string
	FilePath    string
	RecordCount int64
}

// ValidateTimeTravelQuery validates a time travel query
func (tt *IcebergTimeTravel) ValidateTimeTravelQuery(sql string) error {
	// Check for time travel syntax
	// Iceberg supports:
	// - VERSION AS OF (Spark)
	// - FOR SYSTEM_TIME AS OF (Trino)
	// - TIMESTAMP AS OF (some engines)

	// This is a simple validation - in production, use proper SQL parsing
	timeTravelKeywords := []string{
		"VERSION AS OF",
		"FOR SYSTEM_TIME AS OF",
		"TIMESTAMP AS OF",
		"AT TIMESTAMP",
		"AT SNAPSHOT",
	}

	for _, keyword := range timeTravelKeywords {
		if contains(sql, keyword) {
			return nil // Valid time travel syntax
		}
	}

	return fmt.Errorf("not a time travel query")
}

// contains is a simple string contains helper
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > len(substr) && containsSubstring(s, substr)))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
