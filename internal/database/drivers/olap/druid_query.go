package olap

import (
	"context"
	"fmt"
)

// DruidQueryHandler handles Druid query operations
type DruidQueryHandler struct {
	client *DruidRESTClient
}

// NewDruidQueryHandler creates a new query handler
func NewDruidQueryHandler(client *DruidRESTClient) *DruidQueryHandler {
	return &DruidQueryHandler{
		client: client,
	}
}

// ExecuteTimeseriesQuery executes a timeseries query
func (h *DruidQueryHandler) ExecuteTimeseriesQuery(ctx context.Context, datasource string, granularity string, intervals []string) (*DruidQueryResult, error) {
	sql := fmt.Sprintf(`
		SELECT TIME_FLOOR(__time, '%s') AS timestamp, COUNT(*) AS count
		FROM %s
		WHERE __time IN (%s)
		GROUP BY TIME_FLOOR(__time, '%s')
		ORDER BY timestamp
	`, granularity, datasource, formatIntervals(intervals), granularity)

	return h.client.ExecuteSQLQuery(ctx, sql)
}

// ExecuteTopNQuery executes a TopN query
func (h *DruidQueryHandler) ExecuteTopNQuery(ctx context.Context, datasource, dimension string, metric string, threshold int) (*DruidQueryResult, error) {
	sql := fmt.Sprintf(`
		SELECT %s, COUNT(*) AS count
		FROM %s
		GROUP BY %s
		ORDER BY %s DESC
		LIMIT %d
	`, dimension, datasource, dimension, metric, threshold)

	return h.client.ExecuteSQLQuery(ctx, sql)
}

// ExecuteGroupByQuery executes a GroupBy query
func (h *DruidQueryHandler) ExecuteGroupByQuery(ctx context.Context, datasource string, dimensions []string, granularity string) (*DruidQueryResult, error) {
	dimSelect := formatDimensions(dimensions)
	sql := fmt.Sprintf(`
		SELECT TIME_FLOOR(__time, '%s') AS timestamp, %s, COUNT(*) AS count
		FROM %s
		GROUP BY TIME_FLOOR(__time, '%s'), %s
		ORDER BY timestamp
	`, granularity, dimSelect, datasource, granularity, dimSelect)

	return h.client.ExecuteSQLQuery(ctx, sql)
}

// ExecuteScanQuery executes a Scan query for fast data retrieval
func (h *DruidQueryHandler) ExecuteScanQuery(ctx context.Context, datasource string, limit int) (*DruidQueryResult, error) {
	sql := fmt.Sprintf(`
		SELECT *
		FROM %s
		LIMIT %d
	`, datasource, limit)

	return h.client.ExecuteSQLQuery(ctx, sql)
}

// ExecuteSearchQuery executes a Search query
func (h *DruidQueryHandler) ExecuteSearchQuery(ctx context.Context, datasource, dimension, query string) (*DruidQueryResult, error) {
	sql := fmt.Sprintf(`
		SELECT %s, COUNT(*) AS count
		FROM %s
		WHERE %s LIKE '%%%s%%'
		GROUP BY %s
		ORDER BY count DESC
	`, dimension, datasource, dimension, query, dimension)

	return h.client.ExecuteSQLQuery(ctx, sql)
}

// ExecuteTimeBoundaryQuery executes a TimeBoundary query
func (h *DruidQueryHandler) ExecuteTimeBoundaryQuery(ctx context.Context, datasource string) (*DruidTimeBoundaryResult, error) {
	sql := fmt.Sprintf(`
		SELECT MIN(__time) AS min_time, MAX(__time) AS max_time
		FROM %s
	`, datasource)

	result, err := h.client.ExecuteSQLQuery(ctx, sql)
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return nil, fmt.Errorf("no rows returned for datasource %q", datasource)
	}

	return &DruidTimeBoundaryResult{
		MinTime: result.Rows[0]["min_time"],
		MaxTime: result.Rows[0]["max_time"],
	}, nil
}

// DruidTimeBoundaryResult represents time boundary result
type DruidTimeBoundaryResult struct {
	MinTime interface{}
	MaxTime interface{}
}

// ExecuteSegmentMetadataQuery executes a SegmentMetadata query
func (h *DruidQueryHandler) ExecuteSegmentMetadataQuery(ctx context.Context, datasource string) (*DruidSegmentMetadata, error) {
	sql := fmt.Sprintf(`
		SELECT COUNT(*) AS num_rows, COUNT(DISTINCT __time) AS num_timestamps
		FROM %s
	`, datasource)

	result, err := h.client.ExecuteSQLQuery(ctx, sql)
	if err != nil {
		return nil, err
	}
	if len(result.Rows) == 0 {
		return nil, fmt.Errorf("no rows returned for datasource %q", datasource)
	}

	return &DruidSegmentMetadata{
		NumRows:       result.Rows[0]["num_rows"],
		NumTimestamps: result.Rows[0]["num_timestamps"],
	}, nil
}

// DruidSegmentMetadata represents segment metadata
type DruidSegmentMetadata struct {
	NumRows       interface{}
	NumTimestamps interface{}
}

// formatIntervals formats intervals for Druid queries
func formatIntervals(intervals []string) string {
	result := ""
	for i, interval := range intervals {
		if i > 0 {
			result += ", "
		}
		result += "'" + interval + "'"
	}
	return result
}

// formatDimensions formats dimensions for Druid queries
func formatDimensions(dimensions []string) string {
	result := ""
	for i, dim := range dimensions {
		if i > 0 {
			result += ", "
		}
		result += dim
	}
	return result
}
