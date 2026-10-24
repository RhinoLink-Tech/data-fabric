package table_formats

import (
	"context"
	"fmt"
	"time"
)

// DeltaTimeTravel provides time travel functionality for Delta Lake tables
type DeltaTimeTravel struct {
	driver *DeltaDriver
}

// NewDeltaTimeTravel creates a new time travel handler
func NewDeltaTimeTravel(driver *DeltaDriver) *DeltaTimeTravel {
	return &DeltaTimeTravel{driver: driver}
}

// TimeTravelQuery represents a time travel query
type DeltaTimeTravelQuery struct {
	TablePath string
	Timestamp *time.Time
	Version   *int64
}

// QueryForVersion builds a query for a specific version
func (tt *DeltaTimeTravel) QueryForVersion(ctx context.Context, req *DeltaTimeTravelQuery) (string, error) {
	if req.Version == nil && req.Timestamp == nil {
		return "", fmt.Errorf("must specify either version or timestamp")
	}

	var version int64
	var err error

	if req.Version != nil {
		version = *req.Version
	} else if req.Timestamp != nil {
		version, err = tt.driver.GetVersionAtTime(ctx, req.TablePath, *req.Timestamp)
		if err != nil {
			return "", err
		}
	}

	// Delta Lake time travel syntax
	return tt.driver.parser.BuildTimeTravelQuery(req.TablePath, version), nil
}

// GetVersionAtTimestamp retrieves version information for a specific timestamp
func (tt *DeltaTimeTravel) GetVersionAtTimestamp(ctx context.Context, tablePath string, timestamp time.Time) (*DeltaCommitInfo, error) {
	version, err := tt.driver.GetVersionAtTime(ctx, tablePath, timestamp)
	if err != nil {
		return nil, err
	}

	return tt.driver.GetVersionInfo(ctx, tablePath, version)
}

// GetLatestVersion retrieves the latest version
func (tt *DeltaTimeTravel) GetLatestVersion(ctx context.Context, tablePath string) (*DeltaCommitInfo, error) {
	version, err := tt.driver.GetLatestVersion(ctx, tablePath)
	if err != nil {
		return nil, err
	}

	return tt.driver.GetVersionInfo(ctx, tablePath, version)
}

// GetVersionHistory retrieves version history
func (tt *DeltaTimeTravel) GetVersionHistory(ctx context.Context, tablePath string, limit int) ([]DeltaCommitInfo, error) {
	return tt.driver.DescribeHistory(ctx, tablePath, limit)
}

// GetCommitTimeline returns a timeline of commits
func (tt *DeltaTimeTravel) GetCommitTimeline(ctx context.Context, tablePath string) ([]CommitTimelineEntry, error) {
	commits, err := tt.driver.GetHistory(ctx, tablePath)
	if err != nil {
		return nil, err
	}

	return tt.driver.parser.GetCommitTimeline(commits), nil
}

// GetVersionBeforeTime retrieves the latest version before a given time
func (tt *DeltaTimeTravel) GetVersionBeforeTime(ctx context.Context, tablePath string, timestamp time.Time) (*DeltaCommitInfo, error) {
	return tt.GetVersionAtTimestamp(ctx, tablePath, timestamp)
}

// GetVersionsInRange retrieves versions within a time range
func (tt *DeltaTimeTravel) GetVersionsInRange(ctx context.Context, tablePath string, startTime, endTime time.Time) ([]DeltaCommitInfo, error) {
	commits, err := tt.driver.GetHistory(ctx, tablePath)
	if err != nil {
		return nil, err
	}

	var result []DeltaCommitInfo
	for _, commit := range commits {
		if (commit.Timestamp.Equal(startTime) || commit.Timestamp.After(startTime)) &&
			(commit.Timestamp.Equal(endTime) || commit.Timestamp.Before(endTime)) {
			result = append(result, commit)
		}
	}

	return result, nil
}

// CompareVersions compares two versions
func (tt *DeltaTimeTravel) CompareVersions(ctx context.Context, tablePath string, version1, version2 int64) (*VersionComparison, error) {
	commit1, err := tt.driver.GetVersionInfo(ctx, tablePath, version1)
	if err != nil {
		return nil, err
	}

	commit2, err := tt.driver.GetVersionInfo(ctx, tablePath, version2)
	if err != nil {
		return nil, err
	}

	comparison := &VersionComparison{
		Version1:   version1,
		Version2:   version2,
		Timestamp1: commit1.Timestamp,
		Timestamp2: commit2.Timestamp,
		Operation1: commit1.Operation,
		Operation2: commit2.Operation,
		User1:      commit1.UserName,
		User2:      commit2.UserName,
	}

	return comparison, nil
}

// VersionComparison represents comparison between two versions
type VersionComparison struct {
	Version1   int64
	Version2   int64
	Timestamp1 time.Time
	Timestamp2 time.Time
	Operation1 string
	Operation2 string
	User1      string
	User2      string
}

// RestoreToVersion restores a table to a specific version
func (tt *DeltaTimeTravel) RestoreToVersion(ctx context.Context, tablePath string, version int64) error {
	// Delta Lake restore syntax: RESTORE TABLE table_name TO VERSION AS OF version
	return tt.driver.UpdateTableVersion(ctx, tablePath, version)
}

// IsVersionExists checks if a version exists
func (tt *DeltaTimeTravel) IsVersionExists(ctx context.Context, tablePath string, version int64) bool {
	_, err := tt.driver.GetVersionInfo(ctx, tablePath, version)
	return err == nil
}

// GetOperationStatistics calculates statistics by operation type
func (tt *DeltaTimeTravel) GetOperationStatistics(ctx context.Context, tablePath string) (map[string]int64, error) {
	commits, err := tt.driver.GetHistory(ctx, tablePath)
	if err != nil {
		return nil, err
	}

	return tt.driver.parser.GetOperationStatistics(commits), nil
}

// GetActiveVersions returns active versions within retention period
func (tt *DeltaTimeTravel) GetActiveVersions(ctx context.Context, tablePath string, retention time.Duration) ([]int64, error) {
	commits, err := tt.driver.GetHistory(ctx, tablePath)
	if err != nil {
		return nil, err
	}

	return tt.driver.parser.GetActiveVersions(commits, retention), nil
}

// ValidateTimeTravelQuery validates a Delta Lake time travel query
func (tt *DeltaTimeTravel) ValidateTimeTravelQuery(sql string) error {
	// Delta Lake supports:
	// - VERSION AS OF
	// - TIMESTAMP AS OF

	timeTravelKeywords := []string{
		"VERSION AS OF",
		"TIMESTAMP AS OF",
		"@v", // Short syntax: table@v1
		"@t", // Short syntax: table@timestamp
	}

	for _, keyword := range timeTravelKeywords {
		if containsSQL(sql, keyword) {
			return nil
		}
	}

	return fmt.Errorf("not a Delta Lake time travel query")
}

func containsSQL(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > len(substr) && containsSubstringSQL(s, substr)))
}

func containsSubstringSQL(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// GetVersionSummary returns a summary of a version
func (tt *DeltaTimeTravel) GetVersionSummary(ctx context.Context, tablePath string, version int64) (*VersionSummary, error) {
	commit, err := tt.driver.GetVersionInfo(ctx, tablePath, version)
	if err != nil {
		return nil, err
	}

	summary := &VersionSummary{
		Version:    commit.Version,
		Timestamp:  commit.Timestamp,
		User:       commit.UserName,
		Operation:  commit.Operation,
		Parameters: commit.OperationParameters,
		Note:       commit.Note,
	}

	return summary, nil
}

// VersionSummary represents a version summary
type VersionSummary struct {
	Version    int64
	Timestamp  time.Time
	User       string
	Operation  string
	Parameters map[string]interface{}
	Note       string
}

// GetEarliestVersion retrieves the earliest version
func (tt *DeltaTimeTravel) GetEarliestVersion(ctx context.Context, tablePath string) (*DeltaCommitInfo, error) {
	commits, err := tt.driver.GetHistory(ctx, tablePath)
	if err != nil {
		return nil, err
	}

	if len(commits) == 0 {
		return nil, fmt.Errorf("no versions available")
	}

	// Commits come from DESCRIBE HISTORY (newest-first); the earliest is last.
	return &commits[len(commits)-1], nil
}

// GetVersionCount returns the total number of versions
func (tt *DeltaTimeTravel) GetVersionCount(ctx context.Context, tablePath string) (int, error) {
	commits, err := tt.driver.GetHistory(ctx, tablePath)
	if err != nil {
		return 0, err
	}

	return len(commits), nil
}

// GetVersionsByOperation retrieves versions filtered by operation type
func (tt *DeltaTimeTravel) GetVersionsByOperation(ctx context.Context, tablePath string, operation string) ([]DeltaCommitInfo, error) {
	commits, err := tt.driver.GetHistory(ctx, tablePath)
	if err != nil {
		return nil, err
	}

	var result []DeltaCommitInfo
	for _, commit := range commits {
		if commit.Operation == operation {
			result = append(result, commit)
		}
	}

	return result, nil
}

// GetVersionsByUser retrieves versions filtered by user
func (tt *DeltaTimeTravel) GetVersionsByUser(ctx context.Context, tablePath string, username string) ([]DeltaCommitInfo, error) {
	commits, err := tt.driver.GetHistory(ctx, tablePath)
	if err != nil {
		return nil, err
	}

	var result []DeltaCommitInfo
	for _, commit := range commits {
		if commit.UserName == username {
			result = append(result, commit)
		}
	}

	return result, nil
}
