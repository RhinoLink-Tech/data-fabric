package controller

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"nexus-gateway/internal/model"
	"nexus-gateway/internal/service"
	"nexus-gateway/internal/utils"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
)

type DataSourceController struct {
	service   service.DataSourceService
	validator *validator.Validate
}

type Response struct {
	Success       bool        `json:"success"`
	Data          interface{} `json:"data,omitempty"`
	Error         *ErrorInfo  `json:"error,omitempty"`
	Message       string      `json:"message,omitempty"`
	CorrelationID string      `json:"correlationId"`
}

type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type DataSourceResponse struct {
	ID        string                   `json:"id"`
	Name      string                   `json:"name"`
	Type      model.DatabaseType       `json:"type"`
	Config    DataSourceConfigResponse `json:"config"`
	Status    model.DataSourceStatus   `json:"status"`
	CreatedAt time.Time                `json:"created_at"`
	UpdatedAt time.Time                `json:"updated_at"`
}

type DataSourceConfigResponse struct {
	Host            string                 `json:"host"`
	Port            int                    `json:"port"`
	Database        string                 `json:"database"`
	Schema          string                 `json:"schema,omitempty"`
	Username        string                 `json:"username"`
	SSL             bool                   `json:"ssl"`
	Timeout         int                    `json:"timeout"`
	MaxPoolSize     int                    `json:"maxPoolSize"`
	IdleTimeout     int                    `json:"idleTimeout"`
	MaxLifetime     int                    `json:"maxLifetime"`
	Timezone        string                 `json:"timezone"`
	AdditionalProps map[string]interface{} `json:"additionalProps,omitempty"`
}

type DataSourceListResponse struct {
	DataSources []*DataSourceResponse `json:"dataSources"`
	Total       int64                 `json:"total"`
	Limit       int                   `json:"limit"`
	Offset      int                   `json:"offset"`
}

func toDataSourceResponse(dataSource *model.DataSource) *DataSourceResponse {
	if dataSource == nil {
		return nil
	}
	config := dataSource.Config
	return &DataSourceResponse{
		ID:        dataSource.ID,
		Name:      dataSource.Name,
		Type:      dataSource.Type,
		Status:    dataSource.Status,
		CreatedAt: dataSource.CreatedAt,
		UpdatedAt: dataSource.UpdatedAt,
		Config: DataSourceConfigResponse{
			Host:            config.Host,
			Port:            config.Port,
			Database:        config.Database,
			Schema:          config.Schema,
			Username:        config.Username,
			SSL:             config.SSL,
			Timeout:         config.Timeout,
			MaxPoolSize:     config.MaxPoolSize,
			IdleTimeout:     config.IdleTimeout,
			MaxLifetime:     config.MaxLifetime,
			Timezone:        config.Timezone,
			AdditionalProps: redactDataSourceProperties(config.AdditionalProps),
		},
	}
}

func toDataSourceListResponse(result *service.ListDataSourcesResponse) DataSourceListResponse {
	if result == nil {
		return DataSourceListResponse{}
	}
	dataSources := make([]*DataSourceResponse, 0, len(result.DataSources))
	for _, dataSource := range result.DataSources {
		dataSources = append(dataSources, toDataSourceResponse(dataSource))
	}
	return DataSourceListResponse{
		DataSources: dataSources,
		Total:       result.Total,
		Limit:       result.Limit,
		Offset:      result.Offset,
	}
}

func redactDataSourceProperties(properties map[string]interface{}) map[string]interface{} {
	if properties == nil {
		return nil
	}
	safe := make(map[string]interface{}, len(properties))
	for key, value := range properties {
		if model.IsSensitiveDataSourceProperty(key) {
			continue
		}
		safe[key] = redactNestedProperties(value)
	}
	return safe
}

func redactNestedProperties(value interface{}) interface{} {
	switch value := value.(type) {
	case map[string]interface{}:
		return redactDataSourceProperties(value)
	case map[string]string:
		safe := make(map[string]interface{}, len(value))
		for key, item := range value {
			if !model.IsSensitiveDataSourceProperty(key) {
				safe[key] = item
			}
		}
		return safe
	case []interface{}:
		safe := make([]interface{}, len(value))
		for i, item := range value {
			safe[i] = redactNestedProperties(item)
		}
		return safe
	default:
		return value
	}
}

func NewDataSourceController(service service.DataSourceService) *DataSourceController {
	return &DataSourceController{
		service:   service,
		validator: validator.New(),
	}
}

// CreateDataSource godoc
// @Summary Create a new data source
// @Description Creates a new data source with the provided configuration
// @Tags datasources
// @Accept json
// @Produce json
// @Param request body service.CreateDataSourceRequest true "Create data source request"
// @Success 201 {object} Response{data=DataSourceResponse}
// @Failure 400 {object} Response
// @Failure 409 {object} Response
// @Router /api/v1/datasources [post]
func (dc *DataSourceController) CreateDataSource(c *gin.Context) {
	var req service.CreateDataSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		appErr := utils.NewValidationError("Invalid request body", err.Error())
		dc.sendError(c, utils.GetErrorStatus(appErr), appErr.Code, appErr.Message)
		return
	}

	if err := dc.validator.Struct(&req); err != nil {
		appErr := utils.NewValidationError("Validation failed", err.Error())
		dc.sendError(c, utils.GetErrorStatus(appErr), appErr.Code, appErr.Message)
		return
	}

	dataSource, err := dc.service.CreateDataSource(c.Request.Context(), &req)
	if err != nil {
		if err == service.ErrDataSourceExists {
			appErr := utils.NewConflictError("data source", "Data source with this name already exists")
			dc.sendError(c, utils.GetErrorStatus(appErr), appErr.Code, appErr.Message)
			return
		}
		appErr := utils.NewDatabaseError(err, "Failed to create data source")
		dc.sendError(c, utils.GetErrorStatus(appErr), appErr.Code, appErr.Message)
		return
	}

	c.JSON(http.StatusCreated, Response{
		Success:       true,
		Data:          toDataSourceResponse(dataSource),
		CorrelationID: dc.getCorrelationID(c),
	})
}

// GetDataSource godoc
// @Summary Get a data source by ID
// @Description Retrieves a data source by its UUID
// @Tags datasources
// @Produce json
// @Param id path string true "Data source UUID"
// @Success 200 {object} Response{data=DataSourceResponse}
// @Failure 400 {object} Response
// @Failure 404 {object} Response
// @Router /api/v1/datasources/{id} [get]
func (dc *DataSourceController) GetDataSource(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		dc.sendError(c, http.StatusBadRequest, "MISSING_ID", "Data source ID is required")
		return
	}

	dataSource, err := dc.service.GetDataSource(c.Request.Context(), id)
	if err != nil {
		if err == service.ErrDataSourceNotFound {
			dc.sendError(c, http.StatusNotFound, "DATASOURCE_NOT_FOUND", "Data source not found")
			return
		}
		if err == service.ErrInvalidUUID {
			dc.sendError(c, http.StatusBadRequest, "INVALID_ID", "Invalid data source ID format")
			return
		}
		dc.sendError(c, http.StatusInternalServerError, "GET_FAILED", "Failed to get data source")
		return
	}

	c.JSON(http.StatusOK, Response{
		Success:       true,
		Data:          toDataSourceResponse(dataSource),
		CorrelationID: dc.getCorrelationID(c),
	})
}

// ListDataSources godoc
// @Summary List data sources
// @Description Retrieves a paginated list of data sources with optional filtering
// @Tags datasources
// @Produce json
// @Param status query string false "Filter by status (active, inactive, error)"
// @Param limit query int false "Maximum number of items to return (default: 20, max: 100)"
// @Param offset query int false "Number of items to skip (default: 0)"
// @Success 200 {object} Response{data=DataSourceListResponse}
// @Router /api/v1/datasources [get]
func (dc *DataSourceController) ListDataSources(c *gin.Context) {
	req := &service.ListDataSourcesRequest{}

	// Parse query parameters
	if statusStr := c.Query("status"); statusStr != "" {
		req.Status = model.DataSourceStatus(statusStr)
	}

	if limitStr := c.Query("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil {
			req.Limit = limit
		}
	}

	if offsetStr := c.Query("offset"); offsetStr != "" {
		if offset, err := strconv.Atoi(offsetStr); err == nil {
			req.Offset = offset
		}
	}

	if err := dc.validator.Struct(req); err != nil {
		dc.sendError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	response, err := dc.service.ListDataSources(c.Request.Context(), req)
	if err != nil {
		dc.sendError(c, http.StatusInternalServerError, "LIST_FAILED", "Failed to list data sources")
		return
	}

	c.JSON(http.StatusOK, Response{
		Success:       true,
		Data:          toDataSourceListResponse(response),
		CorrelationID: dc.getCorrelationID(c),
	})
}

// UpdateDataSource godoc
// @Summary Update a data source
// @Description Updates an existing data source
// @Tags datasources
// @Accept json
// @Produce json
// @Param id path string true "Data source UUID"
// @Param request body service.UpdateDataSourceRequest true "Update data source request"
// @Success 200 {object} Response{data=DataSourceResponse}
// @Failure 400 {object} Response
// @Failure 404 {object} Response
// @Router /api/v1/datasources/{id} [put]
func (dc *DataSourceController) UpdateDataSource(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		dc.sendError(c, http.StatusBadRequest, "MISSING_ID", "Data source ID is required")
		return
	}

	var req service.UpdateDataSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dc.sendError(c, http.StatusBadRequest, "INVALID_REQUEST", "Invalid request body: "+err.Error())
		return
	}

	if err := dc.validator.Struct(&req); err != nil {
		dc.sendError(c, http.StatusBadRequest, "VALIDATION_ERROR", err.Error())
		return
	}

	dataSource, err := dc.service.UpdateDataSource(c.Request.Context(), id, &req)
	if err != nil {
		if err == service.ErrDataSourceNotFound {
			dc.sendError(c, http.StatusNotFound, "DATASOURCE_NOT_FOUND", "Data source not found")
			return
		}
		if err == service.ErrInvalidUUID {
			dc.sendError(c, http.StatusBadRequest, "INVALID_ID", "Invalid data source ID format")
			return
		}
		dc.sendError(c, http.StatusInternalServerError, "UPDATE_FAILED", "Failed to update data source")
		return
	}

	c.JSON(http.StatusOK, Response{
		Success:       true,
		Data:          toDataSourceResponse(dataSource),
		CorrelationID: dc.getCorrelationID(c),
	})
}

// DeleteDataSource godoc
// @Summary Delete a data source
// @Description Deletes a data source by its UUID
// @Tags datasources
// @Produce json
// @Param id path string true "Data source UUID"
// @Success 200 {object} Response
// @Failure 400 {object} Response
// @Failure 404 {object} Response
// @Router /api/v1/datasources/{id} [delete]
func (dc *DataSourceController) DeleteDataSource(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		dc.sendError(c, http.StatusBadRequest, "MISSING_ID", "Data source ID is required")
		return
	}

	err := dc.service.DeleteDataSource(c.Request.Context(), id)
	if err != nil {
		if err == service.ErrDataSourceNotFound {
			dc.sendError(c, http.StatusNotFound, "DATASOURCE_NOT_FOUND", "Data source not found")
			return
		}
		if err == service.ErrInvalidUUID {
			dc.sendError(c, http.StatusBadRequest, "INVALID_ID", "Invalid data source ID format")
			return
		}
		dc.sendError(c, http.StatusInternalServerError, "DELETE_FAILED", "Failed to delete data source")
		return
	}

	c.JSON(http.StatusOK, Response{
		Success:       true,
		Message:       "Data source deleted successfully",
		CorrelationID: dc.getCorrelationID(c),
	})
}

// ActivateDataSource godoc
// @Summary Activate a data source
// @Description Sets a data source status to active
// @Tags datasources
// @Produce json
// @Param id path string true "Data source UUID"
// @Success 200 {object} Response
// @Failure 400 {object} Response
// @Failure 404 {object} Response
// @Router /api/v1/datasources/{id}/activate [post]
func (dc *DataSourceController) ActivateDataSource(c *gin.Context) {
	dc.changeDataSourceStatus(c, "activate", dc.service.ActivateDataSource)
}

// DeactivateDataSource godoc
// @Summary Deactivate a data source
// @Description Sets a data source status to inactive
// @Tags datasources
// @Produce json
// @Param id path string true "Data source UUID"
// @Success 200 {object} Response
// @Failure 400 {object} Response
// @Failure 404 {object} Response
// @Router /api/v1/datasources/{id}/deactivate [post]
func (dc *DataSourceController) DeactivateDataSource(c *gin.Context) {
	dc.changeDataSourceStatus(c, "deactivate", dc.service.DeactivateDataSource)
}

// GetDataSourceStats godoc
// @Summary Get data source statistics
// @Description Retrieves statistics about data sources
// @Tags datasources
// @Produce json
// @Success 200 {object} Response{data=service.DataSourceStatsResponse}
// @Router /api/v1/datasources/stats [get]
func (dc *DataSourceController) GetDataSourceStats(c *gin.Context) {
	stats, err := dc.service.GetDataSourceStats(c.Request.Context())
	if err != nil {
		dc.sendError(c, http.StatusInternalServerError, "STATS_FAILED", "Failed to get data source statistics")
		return
	}

	c.JSON(http.StatusOK, Response{
		Success:       true,
		Data:          stats,
		CorrelationID: dc.getCorrelationID(c),
	})
}

// Helper methods

func (dc *DataSourceController) changeDataSourceStatus(c *gin.Context, action string, statusFunc func(ctx context.Context, id string) error) {
	id := c.Param("id")
	if id == "" {
		dc.sendError(c, http.StatusBadRequest, "MISSING_ID", "Data source ID is required")
		return
	}

	err := statusFunc(c.Request.Context(), id)
	if err != nil {
		if err == service.ErrDataSourceNotFound {
			dc.sendError(c, http.StatusNotFound, "DATASOURCE_NOT_FOUND", "Data source not found")
			return
		}
		if err == service.ErrInvalidUUID {
			dc.sendError(c, http.StatusBadRequest, "INVALID_ID", "Invalid data source ID format")
			return
		}
		dc.sendError(c, http.StatusInternalServerError, action+"_FAILED", "Failed to "+action+" data source")
		return
	}

	c.JSON(http.StatusOK, Response{
		Success:       true,
		Message:       "Data source " + action + "d successfully",
		CorrelationID: dc.getCorrelationID(c),
	})
}

func (dc *DataSourceController) sendError(c *gin.Context, statusCode int, code, message string) {
	c.JSON(statusCode, Response{
		Success: false,
		Error: &ErrorInfo{
			Code:    code,
			Message: message,
		},
		CorrelationID: dc.getCorrelationID(c),
	})
}

func (dc *DataSourceController) getCorrelationID(c *gin.Context) string {
	if correlationID, exists := c.Get("correlation_id"); exists {
		if id, ok := correlationID.(string); ok {
			return id
		}
	}
	return ""
}
