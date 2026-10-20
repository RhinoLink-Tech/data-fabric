package controller

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/readonly"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/pkg/response"
	"github.com/gin-gonic/gin"
)

const (
	databaseHealthTimeout      = 5 * time.Second
	databaseHealthPageSize     = 100
	databaseHealthProbeWorkers = 8
)

type databaseHealthDataSourceLister interface {
	GetAll(ctx context.Context, scope *repository.AccessScope, status model.DataSourceStatus, limit, offset int) ([]*model.DataSource, int64, error)
}

type connectionPoolStatsProvider interface {
	GetConnectionPoolStats() map[string]database.ConnectionStats
}

type DatabaseController struct {
	datasourceRepo    databaseHealthDataSourceLister
	healthChecker     *database.HealthChecker
	healthProbe       func(context.Context, *model.DataSource) (*database.HealthCheckResult, error)
	poolStatsProvider connectionPoolStatsProvider
}

func NewDatabaseController(datasourceRepo repository.DataSourceRepository, healthChecker *database.HealthChecker) *DatabaseController {
	controller := &DatabaseController{
		datasourceRepo: datasourceRepo,
		healthChecker:  healthChecker,
	}
	if healthChecker != nil {
		controller.healthProbe = healthChecker.CheckDataSourceHealth
		controller.poolStatsProvider = healthChecker
	}
	return controller
}

// GetDatabaseTypes godoc
// @Summary Get supported database types
// @Description Returns a list of supported database types with their information
// @Tags database
// @Produce json
// @Success 200 {object} response.StandardResponse
// @Router /api/v1/database/types [get]
func (dc *DatabaseController) GetDatabaseTypes(c *gin.Context) {
	correlationID := dc.getCorrelationID(c)

	driverInfo := dc.healthChecker.GetDriverInfo()

	c.JSON(http.StatusOK, response.SuccessResponse(driverInfo, correlationID))
}

// TestDataSourceConnection godoc
// @Summary Test data source connection
// @Description Tests connectivity to a data source without adding it to the pool
// @Tags database
// @Accept json
// @Produce json
// @Param request body model.DataSourceConfig true "Data source configuration"
// @Param type query string true "Registered database type (see /api/v1/database/types)"
// @Success 200 {object} response.StandardResponse{data=database.HealthCheckResult}
// @Failure 400 {object} response.StandardResponse
// @Router /api/v1/database/test-connection [post]
func (dc *DatabaseController) TestDataSourceConnection(c *gin.Context) {
	correlationID := dc.getCorrelationID(c)

	// Get database type from query parameter
	dbTypeStr := c.Query("type")
	if dbTypeStr == "" {
		c.JSON(http.StatusBadRequest, response.ErrorResponse(
			"MISSING_TYPE",
			"Database type is required",
			"",
			correlationID,
		))
		return
	}

	// Validate the type against the active driver registry. This keeps the
	// endpoint aligned with the same driver set used by connection services.
	dbType := model.DatabaseType(dbTypeStr)
	if !dc.healthChecker.IsSupportedDataSourceType(dbType) {
		c.JSON(http.StatusBadRequest, response.ErrorResponse(
			"INVALID_TYPE",
			fmt.Sprintf("Unsupported database type: %s", dbType),
			"",
			correlationID,
		))
		return
	}

	var config model.DataSourceConfig
	if err := c.ShouldBindJSON(&config); err != nil {
		c.JSON(http.StatusBadRequest, response.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body: "+err.Error(),
			"",
			correlationID,
		))
		return
	}

	// Validate configuration
	if err := dc.healthChecker.ValidateDataSourceConfiguration(&config, dbType); err != nil {
		c.JSON(http.StatusBadRequest, response.ErrorResponse(
			"INVALID_CONFIG",
			"Invalid configuration: "+err.Error(),
			"",
			correlationID,
		))
		return
	}

	// Test connectivity
	result, err := dc.healthChecker.CheckDataSourceConnectivity(c.Request.Context(), &config, dbType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, response.ErrorResponse(
			"CONNECTION_TEST_FAILED",
			"Failed to test connection: "+err.Error(),
			"",
			correlationID,
		))
		return
	}

	c.JSON(http.StatusOK, response.SuccessResponse(result, correlationID))
}

// ValidateDataSourceConfig godoc
// @Summary Validate data source configuration
// @Description Validates a data source configuration without testing the connection
// @Tags database
// @Accept json
// @Produce json
// @Param request body ValidateConfigRequest true "Validation request"
// @Success 200 {object} response.StandardResponse
// @Failure 400 {object} response.StandardResponse
// @Router /api/v1/database/validate-config [post]
func (dc *DatabaseController) ValidateDataSourceConfig(c *gin.Context) {
	correlationID := dc.getCorrelationID(c)

	var req ValidateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, response.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body: "+err.Error(),
			"",
			correlationID,
		))
		return
	}

	// Validate the type against the active driver registry. This keeps the
	// endpoint aligned with the same driver set used by connection services.
	dbType := model.DatabaseType(req.Type)
	if !dc.healthChecker.IsSupportedDataSourceType(dbType) {
		c.JSON(http.StatusBadRequest, response.ErrorResponse(
			"INVALID_TYPE",
			fmt.Sprintf("Unsupported database type: %s", dbType),
			"",
			correlationID,
		))
		return
	}

	// Validate configuration
	if err := dc.healthChecker.ValidateDataSourceConfiguration(&req.Config, dbType); err != nil {
		c.JSON(http.StatusOK, response.SuccessResponse(map[string]interface{}{
			"valid":  false,
			"errors": []string{err.Error()},
		}, correlationID))
		return
	}

	c.JSON(http.StatusOK, response.SuccessResponse(map[string]interface{}{
		"valid":    true,
		"warnings": dc.getConfigWarnings(&req.Config, dbType),
	}, correlationID))
}

// GetConnectionStats godoc
// @Summary Get connection pool statistics
// @Description Returns live statistics for initialized connection pools. Configured data sources without an initialized pool are omitted; when no pools exist, the pools map and aggregate counters are empty or zero.
// @Tags database
// @Produce json
// @Success 200 {object} response.StandardResponse{data=ConnectionPoolStatsResponse}
// @Failure 503 {object} response.StandardResponse
// @Router /api/v1/database/connections/stats [get]
func (dc *DatabaseController) GetConnectionStats(c *gin.Context) {
	correlationID := dc.getCorrelationID(c)
	if dc.poolStatsProvider == nil {
		c.JSON(http.StatusServiceUnavailable, response.ErrorResponse(
			"CONNECTION_STATS_UNAVAILABLE",
			"Connection pool statistics are unavailable",
			"Connection pool statistics dependency is not configured",
			correlationID,
		))
		return
	}

	pools := dc.poolStatsProvider.GetConnectionPoolStats()
	if pools == nil {
		pools = make(map[string]database.ConnectionStats)
	}
	stats := ConnectionPoolStatsResponse{
		Message:   "Statistics for initialized connection pools",
		Pools:     pools,
		CheckedAt: time.Now(),
	}
	for _, pool := range pools {
		stats.TotalPools++
		stats.TotalConnections += pool.OpenConnections
		stats.ActiveConnections += pool.InUse
		stats.IdleConnections += pool.Idle
		stats.WaitCount += pool.WaitCount
		stats.WaitDuration += pool.WaitDuration
	}
	c.JSON(http.StatusOK, response.SuccessResponse(stats, correlationID))
}

// GetDatabaseHealth godoc
// @Summary Get database health status
// @Description Returns health status of all database connections
// @Tags database
// @Produce json
// @Success 200 {object} response.StandardResponse{data=database.DatabaseHealthSummary}
// @Router /api/v1/database/health [get]
func (dc *DatabaseController) GetDatabaseHealth(c *gin.Context) {
	correlationID := dc.getCorrelationID(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), databaseHealthTimeout)
	defer cancel()

	if dc.datasourceRepo == nil || dc.healthProbe == nil {
		c.JSON(http.StatusServiceUnavailable, response.ErrorResponse(
			"DATABASE_HEALTH_UNAVAILABLE",
			"Database health check is unavailable",
			"Required health-check dependencies are not configured",
			correlationID,
		))
		return
	}

	sources, err := dc.loadActiveDataSources(ctx)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, response.ErrorResponse(
			"DATABASE_HEALTH_UNAVAILABLE",
			"Database health check is unavailable",
			"Configured data sources could not be loaded",
			correlationID,
		))
		return
	}

	results := dc.probeDataSources(ctx, sources)
	summary := &database.DatabaseHealthSummary{
		TotalConnections: len(sources),
		Results:          results,
		SummaryByType:    make(map[string]database.TypeHealthSummary),
		CheckedAt:        time.Now(),
	}
	for i, result := range results {
		typeName := result.DatabaseType
		if typeName == "" && i < len(sources) && sources[i] != nil {
			typeName = string(sources[i].Type)
		}
		typeSummary := summary.SummaryByType[typeName]
		typeSummary.Total++
		if result.Status == "healthy" {
			summary.HealthyConnections++
			typeSummary.Healthy++
		} else {
			summary.UnhealthyConnections++
			typeSummary.Unhealthy++
		}
		summary.SummaryByType[typeName] = typeSummary
	}
	summary.Status = databaseHealthStatus(summary.TotalConnections, summary.HealthyConnections, summary.UnhealthyConnections)

	if summary.Status == "unhealthy" || summary.Status == "degraded" {
		resp := response.SuccessResponse(summary, correlationID)
		resp.Success = false
		resp.Error = &response.ErrorInfo{
			Code:    "DATABASE_UNHEALTHY",
			Message: "One or more configured data sources are unhealthy",
		}
		c.JSON(http.StatusServiceUnavailable, resp)
		return
	}
	c.JSON(http.StatusOK, response.SuccessResponse(summary, correlationID))
}

func (dc *DatabaseController) loadActiveDataSources(ctx context.Context) ([]*model.DataSource, error) {
	var sources []*model.DataSource
	for offset := 0; ; {
		// The health probe is admin-only at the route layer (#9) and sees the
		// whole fleet; an unscoped read is intended here.
		page, total, err := dc.datasourceRepo.GetAll(ctx, nil, model.DataSourceStatusActive, databaseHealthPageSize, offset)
		if err != nil {
			return nil, err
		}
		sources = append(sources, page...)
		if len(sources) >= int(total) || len(page) == 0 {
			return sources, nil
		}
		offset += len(page)
	}
}

func (dc *DatabaseController) probeDataSources(ctx context.Context, sources []*model.DataSource) []database.HealthCheckResult {
	results := make([]database.HealthCheckResult, len(sources))
	if len(sources) == 0 {
		return results
	}

	workerCount := databaseHealthProbeWorkers
	if len(sources) < workerCount {
		workerCount = len(sources)
	}
	jobs := make(chan int)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for index := range jobs {
				source := sources[index]
				checkedAt := time.Now()
				if source == nil {
					results[index] = database.HealthCheckResult{Status: "unhealthy", Message: "Health check failed", CheckedAt: checkedAt}
					continue
				}
				if err := ctx.Err(); err != nil {
					results[index] = database.HealthCheckResult{DataSourceID: source.ID, DatabaseType: string(source.Type), Status: "unhealthy", Message: "Health check timed out", CheckedAt: checkedAt}
					continue
				}
				result, err := dc.healthProbe(ctx, source)
				if err != nil || result == nil {
					results[index] = database.HealthCheckResult{DataSourceID: source.ID, DatabaseType: string(source.Type), Status: "unhealthy", Message: "Health check failed", CheckedAt: checkedAt}
					continue
				}
				results[index] = *result
				results[index].DataSourceID = source.ID
				results[index].DatabaseType = string(source.Type)
				if results[index].Status == "healthy" {
					results[index].Message = "Connection successful"
				} else {
					results[index].Status = "unhealthy"
					results[index].Message = "Health check failed"
				}
				if results[index].CheckedAt.IsZero() {
					results[index].CheckedAt = checkedAt
				}
			}
		}()
	}
	for index := range sources {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	return results
}

func databaseHealthStatus(total, healthy, unhealthy int) string {
	if total == 0 {
		return "unknown"
	}
	if unhealthy == 0 {
		return "healthy"
	}
	if healthy == 0 {
		return "unhealthy"
	}
	return "degraded"
}

// GetDatabaseCompatibility godoc
// @Summary Get database compatibility information
// @Description Returns compatibility information for different database types
// @Tags database
// @Produce json
// @Success 200 {object} response.StandardResponse
// @Router /api/v1/database/compatibility [get]
func (dc *DatabaseController) GetDatabaseCompatibility(c *gin.Context) {
	correlationID := dc.getCorrelationID(c)

	compatibility := map[string]DatabaseCompatibility{
		"mysql": {
			Supported:   true,
			Version:     "5.7+",
			Features:    []string{"SELECT", "JOIN", "GROUP BY", "ORDER BY", "LIMIT"},
			Limitations: []string{"No DML/DDL operations", "No stored procedures"},
			Recommended: true,
		},
		"trino": {
			Supported:   true,
			Version:     "372+",
			Features:    []string{"Unified SQL entry", "Cross-catalog queries", "SELECT", "JOIN", "GROUP BY", "ORDER BY", "LIMIT", "Window functions"},
			Limitations: []string{"Requires a configured Trino coordinator and catalogs", "Connector capabilities vary by catalog"},
			Recommended: true,
		},
		"presto": {
			Supported:   true,
			Features:    []string{"Unified SQL entry", "Cross-catalog queries", "SELECT", "JOIN", "GROUP BY", "ORDER BY", "LIMIT"},
			Limitations: []string{"Requires a configured Presto coordinator and catalogs", "Connector capabilities vary by catalog"},
			Recommended: true,
		},
		"openlookeng": {
			Supported:   true,
			Features:    []string{"Unified SQL entry", "Cross-catalog queries", "SELECT", "JOIN", "GROUP BY", "ORDER BY", "LIMIT"},
			Limitations: []string{"Requires a configured OpenLooKeng coordinator and catalogs", "Connector capabilities vary by catalog"},
			Recommended: true,
		},
		"mariadb": {
			Supported:   true,
			Version:     "10.2+",
			Features:    []string{"SELECT", "JOIN", "GROUP BY", "ORDER BY", "LIMIT"},
			Limitations: []string{"No DML/DDL operations", "No stored procedures"},
			Recommended: true,
		},
		"postgresql": {
			Supported:   true,
			Version:     "9.6+",
			Features:    []string{"SELECT", "JOIN", "GROUP BY", "ORDER BY", "LIMIT", "Window functions"},
			Limitations: []string{"No DML/DDL operations", "No stored procedures"},
			Recommended: true,
		},
		"oracle": {
			Supported:   true,
			Version:     "12c+",
			Features:    []string{"SELECT", "JOIN", "GROUP BY", "ORDER BY"},
			Limitations: []string{"No DML/DDL operations", "No stored procedures", "Limited pagination support"},
			Recommended: true,
		},
	}

	c.JSON(http.StatusOK, response.SuccessResponse(compatibility, correlationID))
}

// Request/Response types

type ValidateConfigRequest struct {
	Type   string                 `json:"type" validate:"required"`
	Config model.DataSourceConfig `json:"config" validate:"required"`
}

// ConnectionPoolStatsResponse is a snapshot of initialized database/sql pools.
// Data sources that have not created a pool are omitted from Pools.
type ConnectionPoolStatsResponse struct {
	Message           string                              `json:"message"`
	TotalPools        int                                 `json:"totalPools"`
	TotalConnections  int                                 `json:"totalConnections"`
	ActiveConnections int                                 `json:"activeConnections"`
	IdleConnections   int                                 `json:"idleConnections"`
	WaitCount         int64                               `json:"waitCount"`
	WaitDuration      time.Duration                       `json:"waitDuration"`
	Pools             map[string]database.ConnectionStats `json:"pools"`
	CheckedAt         time.Time                           `json:"checkedAt"`
}

type DatabaseCompatibility struct {
	Supported   bool     `json:"supported"`
	Version     string   `json:"version"`
	Features    []string `json:"features"`
	Limitations []string `json:"limitations"`
	Recommended bool     `json:"recommended"`
}

// Helper methods

func (dc *DatabaseController) getCorrelationID(c *gin.Context) string {
	if correlationID, exists := c.Get("correlation_id"); exists {
		if id, ok := correlationID.(string); ok {
			return id
		}
	}
	return ""
}

func (dc *DatabaseController) getConfigWarnings(config *model.DataSourceConfig, dbType model.DatabaseType) []string {
	warnings := []string{}

	// Check for potential configuration issues
	if config.MaxPoolSize > 50 {
		warnings = append(warnings, "Large connection pool size may impact performance")
	}

	if config.Timeout > 60 {
		warnings = append(warnings, "Long timeout may mask performance issues")
	}

	if !config.SSL && dbType != model.DatabaseTypeOracle {
		warnings = append(warnings, "SSL is disabled - connection will not be encrypted")
	}

	// Read-only enforcement advisories (#20)
	if !readonly.SupportsReadOnlySession(dbType) {
		warnings = append(warnings, fmt.Sprintf("%s has no database-side read-only session mode; queries are restricted by SQL validation only - use a least-privilege read-only database account (docs/readonly-access.md)", dbType))
	}
	if disabled, ok := readOnlyOverrideDisabled(config); ok && disabled {
		warnings = append(warnings, "read-only session enforcement is disabled for this data source; queries are restricted by SQL validation only")
	}

	return warnings
}

// readOnlyOverrideDisabled reports whether the per-data-source opt-out in
// additionalProps.enforce_read_only disables database-side read-only
// enforcement (#20).
func readOnlyOverrideDisabled(config *model.DataSourceConfig) (disabled bool, present bool) {
	override, ok := config.AdditionalProps["enforce_read_only"]
	if !ok {
		return false, false
	}
	switch value := override.(type) {
	case bool:
		return !value, true
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "false", "off", "0":
			return true, true
		}
	}
	return false, true
}
