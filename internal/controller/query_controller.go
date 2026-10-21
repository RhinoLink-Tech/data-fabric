package controller

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	queryservice "github.com/RhinoLink-Tech/data-fabric/internal/service"
	unifiedservice "github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
	"github.com/RhinoLink-Tech/data-fabric/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

type QueryController struct {
	queryService unifiedservice.UnifiedQueryService // Changed to use unified service
	validator    *validator.Validate
}

func NewQueryController(unifiedQueryService unifiedservice.UnifiedQueryService) *QueryController { // Changed parameter type
	return &QueryController{
		queryService: unifiedQueryService,
		validator:    validator.New(),
	}
}

// FetchQuery godoc
// @Summary Execute a batch SQL query
// @Description Executes a SQL query and returns batch data with pagination support.
// This endpoint will attempt to open a streaming cursor on the backend when possible
// and return the first batch of rows. If streaming is established, the response
// includes a `nextUri` for continuing the stream via `/fetch/{query_id}/{slug}/{token}`.
// @Tags queries
// @Accept json
// @Produce json
// @Param request body model.FetchQueryRequest true "Fetch query request"
// @Success 200 {object} response.StandardResponse{data=model.FetchQueryResponse}
// @Failure 400 {object} response.StandardResponse
// @Failure 422 {object} response.StandardResponse
// @Failure 500 {object} response.StandardResponse
// @Router /api/v1/fetch [post]
func (qc *QueryController) FetchQuery(c *gin.Context) {
	correlationID := qc.getCorrelationID(c)
	caller := security.CallerFromGin(c)

	var req model.FetchQueryRequest
	// Bind with UseNumber so large integer parameters keep their exact
	// digits instead of degrading to float64 (#21); the service normalizes
	// json.Number into driver-bindable values.
	if err := decodeFetchQueryRequest(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, response.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body.",
			"",
			correlationID,
		))
		return
	}

	// Apply defaults
	if req.BatchSize <= 0 {
		req.BatchSize = 10000
	}
	if req.BatchSize > 10000 {
		req.BatchSize = 10000
	}
	if req.Timeout <= 0 {
		req.Timeout = 60
	}
	if req.Timeout > 1800 {
		req.Timeout = 1800
	}

	// Validate request
	if err := qc.validator.Struct(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, response.ErrorResponse(
			"VALIDATION_ERROR",
			"Query request validation failed.",
			"",
			correlationID,
		))
		return
	}

	// Create context with timeout
	ctx := c.Request.Context()
	var cancel context.CancelFunc
	if req.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.Timeout)*time.Second)
		defer cancel()
	}

	// Carry the authenticated identity so the query history records who ran
	// the query and never mixes tenants (#103)
	ctx = queryservice.WithQueryUser(ctx, requestingUser(c))

	// Execute fetch query using unified service
	result, err := qc.queryService.FetchQuery(ctx, caller, &req)
	if err != nil {
		qc.writeQueryError(c, caller, err)
		return
	}

	// Return response
	c.JSON(http.StatusOK, response.SuccessResponse(result, correlationID))
}

// FetchNextBatch godoc
// @Summary Fetch next batch of data
// @Description Fetches the next batch of data using the query ID, slug, and token.
// This endpoint retrieves subsequent batches for sessions created by POST
// `/fetch`. It supports streaming cursors and offset-based pagination.
// @Tags queries
// @Accept json
// @Produce json
// @Param query_id path string true "Query ID"
// @Param slug path string true "Query slug"
// @Param token path string true "Query token"
// @Param batch_size query int false "Batch size (1-10000, default 10000)"
// @Success 200 {object} response.StandardResponse{data=model.FetchQueryResponse}
// @Failure 400 {object} response.StandardResponse
// @Failure 404 {object} response.StandardResponse
// @Failure 403 {object} response.StandardResponse
// @Failure 500 {object} response.StandardResponse
// @Router /api/v1/fetch/{query_id}/{slug}/{token} [get]
func (qc *QueryController) FetchNextBatch(c *gin.Context) {
	correlationID := qc.getCorrelationID(c)

	// Extract path parameters
	queryID := c.Param("query_id")
	slug := c.Param("slug")
	token := c.Param("token")

	if queryID == "" || slug == "" || token == "" {
		c.JSON(http.StatusBadRequest, response.ErrorResponse(
			"INVALID_PARAMETERS",
			"Query ID, slug, and token are required",
			"",
			correlationID,
		))
		return
	}

	// Parse batch size
	batchSize := 10000
	if bs := c.Query("batch_size"); bs != "" {
		if size, err := strconv.Atoi(bs); err == nil && size > 0 && size <= 10000 {
			batchSize = size
		}
	}

	// Create context
	ctx := c.Request.Context()

	// Fetch next batch using unified service
	result, err := qc.queryService.FetchNextBatch(ctx, security.CallerFromGin(c), queryID, slug, token, batchSize)
	if err != nil {
		qc.writeQueryError(c, security.CallerFromGin(c), err)
		return
	}

	c.JSON(http.StatusOK, response.SuccessResponse(result, correlationID))
}

// ValidateQuery godoc
// @Summary Validate a SQL query
// @Description Validates a SQL query without executing it
// @Tags queries
// @Accept json
// @Produce json
// @Param request body model.QueryRequest true "Query validation request"
// @Success 200 {object} response.StandardResponse
// @Failure 400 {object} response.StandardResponse
// @Failure 403 {object} response.StandardResponse
// @Failure 404 {object} response.StandardResponse
// @Failure 422 {object} response.StandardResponse
// @Router /api/v1/query/validate [post]
func (qc *QueryController) ValidateQuery(c *gin.Context) {
	correlationID := qc.getCorrelationID(c)

	var req model.QueryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, response.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body.",
			"",
			correlationID,
		))
		return
	}

	// Apply defaults to request
	req.ApplyDefaults()

	// Validate request structure
	if err := qc.validator.Struct(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, response.ErrorResponse(
			"VALIDATION_ERROR",
			"Query request validation failed.",
			"",
			correlationID,
		))
		return
	}

	// Validate query using unified service
	err := qc.queryService.ValidateQuery(c.Request.Context(), security.CallerFromGin(c), &req)
	if err != nil {
		if errors.Is(err, repository.ErrDataSourceNotFound) || errors.Is(err, security.ErrDataSourceAccessDenied) {
			qc.writeQueryError(c, security.CallerFromGin(c), err)
			return
		}
		slog.ErrorContext(c.Request.Context(), "query validation failed", "correlation_id", correlationID, "error", err)
		c.JSON(http.StatusOK, response.SuccessResponse(map[string]interface{}{
			"valid":   false,
			"error":   queryservice.SafeQueryErrorMessage(err),
			"queryId": qc.generateQueryID(),
		}, correlationID))
		return
	}

	c.JSON(http.StatusOK, response.SuccessResponse(map[string]interface{}{
		"valid":   true,
		"queryId": qc.generateQueryID(),
	}, correlationID))
}

// GetQueryStats godoc
// @Summary Get query statistics
// @Description Returns statistics about query executions
// @Tags queries
// @Produce json
// @Success 200 {object} response.StandardResponse{data=model.QueryStats}
// @Router /api/v1/query/stats [get]
func (qc *QueryController) GetQueryStats(c *gin.Context) {
	correlationID := qc.getCorrelationID(c)

	stats, err := qc.queryService.GetQueryStats(c.Request.Context())
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "query statistics request failed", "correlation_id", correlationID, "error", err)
		c.JSON(http.StatusInternalServerError, response.ErrorResponse(
			"STATS_ERROR",
			"Failed to get query statistics.",
			"",
			correlationID,
		))
		return
	}

	c.JSON(http.StatusOK, response.SuccessResponse(stats, correlationID))
}

// GetQueryHistory godoc
// @Summary Get recent query history
// @Description Returns the caller's recent queries with bounded pagination. Records are scoped to the authenticated user; an optional dataSourceId filters further.
// @Tags queries
// @Produce json
// @Param limit query int false "Maximum number of items to return (default: 100, max: 1000)"
// @Param offset query int false "Number of items to skip (default: 0)"
// @Param dataSourceId query string false "Only return queries executed against this data source"
// @Success 200 {object} response.StandardResponse
// @Router /api/v1/query/history [get]
func (qc *QueryController) GetQueryHistory(c *gin.Context) {
	correlationID := qc.getCorrelationID(c)

	// Parse query parameters
	limit := 100
	offset := 0

	if limitStr := c.Query("limit"); limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
			if limit > 1000 {
				limit = 1000
			}
		}
	}

	if offsetStr := c.Query("offset"); offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	// The history is scoped to the authenticated caller; records from other
	// users are never visible (#103).
	filter := queryservice.QueryHistoryFilter{
		User:         requestingUser(c),
		DataSourceID: c.Query("dataSourceId"),
		Limit:        limit,
		Offset:       offset,
	}
	page, err := qc.queryService.GetQueryHistory(c.Request.Context(), filter)
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "query history request failed", "correlation_id", correlationID, "error", err)
		c.JSON(http.StatusInternalServerError, response.ErrorResponse(
			"HISTORY_ERROR",
			"Failed to get query history.",
			"",
			correlationID,
		))
		return
	}

	history := map[string]interface{}{
		"queries": page.Records,
		"total":   page.Total,
		"limit":   page.Limit,
		"offset":  page.Offset,
	}

	c.JSON(http.StatusOK, response.SuccessResponse(history, correlationID))
}

// Helper methods

// requestingUser identifies the caller for per-user scoping such as query
// history (#103). Falls back from the JWT user ID to the username; callers
// that carry neither (MCP stdio) get the shared "anonymous" bucket.
func requestingUser(c *gin.Context) string {
	if user, ok := security.GetUserID(c); ok && user != "" {
		return user
	}
	if user, ok := security.GetUsername(c); ok && user != "" {
		return user
	}
	return "anonymous"
}

// writeQueryError maps a query failure to its HTTP response. Non-admin
// callers get the same 403 for missing and forbidden data sources (#9), so
// existence cannot be probed through /fetch either.
func (qc *QueryController) writeQueryError(c *gin.Context, caller *security.Caller, err error) {
	slog.ErrorContext(c.Request.Context(), "query request failed", "correlation_id", qc.getCorrelationID(c), "error", err)
	if (errors.Is(err, repository.ErrDataSourceNotFound) || errors.Is(err, security.ErrDataSourceAccessDenied)) && caller != nil && !caller.IsAdmin() {
		c.JSON(http.StatusForbidden, response.ErrorResponse(
			"DATASOURCE_ACCESS_DENIED",
			"You do not have access to this data source",
			"",
			qc.getCorrelationID(c),
		))
		return
	}
	if errors.Is(err, security.ErrDataSourceAccessDenied) && caller != nil && caller.IsAdmin() {
		c.JSON(http.StatusInternalServerError, response.ErrorResponse(
			"QUERY_ENGINE_ERROR",
			"The data-source query failed. Check the service logs.",
			"",
			qc.getCorrelationID(c),
		))
		return
	}
	status, code := queryFailureStatusAndCode(err)
	c.JSON(status, response.ErrorResponse(
		code,
		queryservice.SafeQueryErrorMessage(err),
		"",
		qc.getCorrelationID(c),
	))
}

func (qc *QueryController) getCorrelationID(c *gin.Context) string {
	if correlationID, exists := c.Get("correlation_id"); exists {
		if id, ok := correlationID.(string); ok {
			return id
		}
	}
	return ""
}

func (qc *QueryController) generateQueryID() string {
	// Generate a simple query ID for tracking
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func (qc *QueryController) generateSlug() string {
	// Generate a slug for query identification
	return uuid.New().String()[:8]
}

func (qc *QueryController) generateToken() string {
	// Generate a token for authorization
	return uuid.New().String()
}

// decodeFetchQueryRequest binds the fetch request body with UseNumber, so
// numeric parameters keep their exact digits as json.Number instead of
// becoming float64 (#21). The service layer converts them into
// driver-bindable values.
func decodeFetchQueryRequest(c *gin.Context, req *model.FetchQueryRequest) error {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.UseNumber()
	return decoder.Decode(req)
}

func queryFailureStatusAndCode(err error) (int, string) {
	switch {
	case errors.Is(err, repository.ErrDataSourceNotFound):
		return http.StatusNotFound, "DATASOURCE_NOT_FOUND"
	case errors.Is(err, security.ErrDataSourceAccessDenied):
		return http.StatusForbidden, "DATASOURCE_ACCESS_DENIED"
	case errors.Is(err, security.ErrTableAccessDenied):
		return http.StatusForbidden, "TABLE_ACCESS_DENIED"
	case errors.Is(err, queryservice.ErrDataSourceInactive):
		// The source exists but its current state forbids execution (#118)
		return http.StatusConflict, "DATASOURCE_INACTIVE"
	case errors.Is(err, queryservice.ErrQuerySessionUnavailable), errors.Is(err, queryservice.ErrInvalidQuerySession):
		return http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE"
	case errors.Is(err, queryservice.ErrExactCountUnavailable):
		// The statement is valid but its shape cannot be counted exactly (#23)
		return http.StatusUnprocessableEntity, "COUNT_UNSUPPORTED"
	case errors.Is(err, security.ErrNotSelectQuery), errors.Is(err, security.ErrSQLInjection),
		errors.Is(err, security.ErrSQLSyntaxError), errors.Is(err, security.ErrDangerousKeyword),
		errors.Is(err, security.ErrEmptyQuery), errors.Is(err, security.ErrQueryTooLong):
		return http.StatusBadRequest, "INVALID_QUERY"
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "QUERY_TIMEOUT"
	case errors.Is(err, context.Canceled):
		return http.StatusRequestTimeout, "QUERY_CANCELLED"
	default:
		return http.StatusBadGateway, "QUERY_ENGINE_ERROR"
	}
}
