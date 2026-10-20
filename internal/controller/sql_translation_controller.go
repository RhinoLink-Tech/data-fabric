package controller

import (
	"net/http"

	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"
	"github.com/RhinoLink-Tech/data-fabric/pkg/response"

	"github.com/gin-gonic/gin"
)

// SQLTranslationController handles SQL dialect translation requests
type SQLTranslationController struct {
	translator *sql_translator.SQLTranslationManager
}

// SQLTranslationRequest represents a request to translate SQL from one dialect to another
type SQLTranslationRequest struct {
	SQL           string `json:"sql" binding:"required"`
	SourceDialect string `json:"sourceDialect" binding:"required"`
	TargetDialect string `json:"targetDialect" binding:"required"`
}

// SQLTranslationResponse represents the response for SQL translation
type SQLTranslationResponse struct {
	OriginalSQL   string `json:"originalSql"`
	TranslatedSQL string `json:"translatedSql"`
	SourceDialect string `json:"sourceDialect"`
	TargetDialect string `json:"targetDialect"`
	Success       bool   `json:"success"`
	Error         string `json:"error,omitempty"`
}

// NewSQLTranslationController creates a new SQL translation controller
func NewSQLTranslationController(translators ...*sql_translator.SQLTranslationManager) *SQLTranslationController {
	translator := sql_translator.NewSQLTranslationManager(true)
	if len(translators) > 0 && translators[0] != nil {
		translator = translators[0]
	}
	return &SQLTranslationController{translator: translator}
}

func (stc *SQLTranslationController) translationEnabled(c *gin.Context) bool {
	if stc.translator != nil && stc.translator.Enabled() {
		return true
	}
	c.JSON(http.StatusServiceUnavailable, response.ErrorResponse(
		"SQL_TRANSLATION_DISABLED", "SQL translation is disabled by server configuration", "", getCorrelationID(c),
	))
	return false
}

// TranslateSQL translates SQL from one dialect to another
// @Summary Translate SQL from one dialect to another
// @Description Translates SQL from a source dialect to a target dialect
// @Tags sql-translation
// @Accept json
// @Produce json
// @Param request body SQLTranslationRequest true "SQL translation request"
// @Success 200 {object} response.StandardResponse{data=SQLTranslationResponse}
// @Failure 400 {object} response.StandardResponse
// @Failure 500 {object} response.StandardResponse
// @Router /api/v1/sql/translate [post]
func (stc *SQLTranslationController) TranslateSQL(c *gin.Context) {
	correlationID := getCorrelationID(c)
	if !stc.translationEnabled(c) {
		return
	}

	var req SQLTranslationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, response.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body: "+err.Error(),
			"",
			correlationID,
		))
		return
	}

	// Validate dialects
	supportedDialects := stc.translator.GetSupportedDialects()
	sourceSupported := false
	targetSupported := false

	for _, dialect := range supportedDialects {
		if dialect == req.SourceDialect {
			sourceSupported = true
		}
		if dialect == req.TargetDialect {
			targetSupported = true
		}
	}

	if !sourceSupported {
		c.JSON(http.StatusBadRequest, response.ErrorResponse(
			"UNSUPPORTED_SOURCE_DIALECT",
			"Source dialect '"+req.SourceDialect+"' is not supported",
			"",
			correlationID,
		))
		return
	}

	if !targetSupported {
		c.JSON(http.StatusBadRequest, response.ErrorResponse(
			"UNSUPPORTED_TARGET_DIALECT",
			"Target dialect '"+req.TargetDialect+"' is not supported",
			"",
			correlationID,
		))
		return
	}

	// Translate the SQL
	translatedSQL, err := stc.translator.TranslateQueryContext(c.Request.Context(), req.SQL, req.SourceDialect, req.TargetDialect)
	if err != nil {
		c.JSON(http.StatusInternalServerError, response.ErrorResponse(
			"TRANSLATION_ERROR",
			"Failed to translate SQL: "+err.Error(),
			"",
			correlationID,
		))
		return
	}

	responseData := SQLTranslationResponse{
		OriginalSQL:   req.SQL,
		TranslatedSQL: translatedSQL,
		SourceDialect: req.SourceDialect,
		TargetDialect: req.TargetDialect,
		Success:       true,
	}

	c.JSON(http.StatusOK, response.SuccessResponse(responseData, correlationID))
}

// GetSupportedDialects returns a list of supported SQL dialects
// @Summary Get supported SQL dialects
// @Description Returns a list of all supported SQL dialects
// @Tags sql-translation
// @Produce json
// @Success 200 {object} response.StandardResponse{data=[]string}
// @Router /api/v1/sql/dialects [get]
func (stc *SQLTranslationController) GetSupportedDialects(c *gin.Context) {
	correlationID := getCorrelationID(c)
	if !stc.translationEnabled(c) {
		return
	}

	supportedDialects := stc.translator.GetSupportedDialects()

	c.JSON(http.StatusOK, response.SuccessResponse(supportedDialects, correlationID))
}

// ValidateSQL validates SQL against a specific dialect
// @Summary Validate SQL against a dialect
// @Description Validates if SQL is syntactically correct for a specific dialect
// @Tags sql-translation
// @Accept json
// @Produce json
// @Param request body struct{SQL string "json:\"sql\""; Dialect string "json:\"dialect\""} true "SQL validation request"
// @Success 200 {object} response.StandardResponse{data=map[string]interface{}}
// @Failure 400 {object} response.StandardResponse
// @Router /api/v1/sql/validate [post]
func (stc *SQLTranslationController) ValidateSQL(c *gin.Context) {
	correlationID := getCorrelationID(c)
	if !stc.translationEnabled(c) {
		return
	}

	var req struct {
		SQL     string `json:"sql" binding:"required"`
		Dialect string `json:"dialect" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, response.ErrorResponse(
			"INVALID_REQUEST",
			"Invalid request body: "+err.Error(),
			"",
			correlationID,
		))
		return
	}

	// Validate dialect
	supportedDialects := stc.translator.GetSupportedDialects()
	dialectSupported := false

	for _, dialect := range supportedDialects {
		if dialect == req.Dialect {
			dialectSupported = true
			break
		}
	}

	if !dialectSupported {
		c.JSON(http.StatusBadRequest, response.ErrorResponse(
			"UNSUPPORTED_DIALECT",
			"Dialect '"+req.Dialect+"' is not supported",
			"",
			correlationID,
		))
		return
	}

	// Validate the SQL
	isValid, err := stc.translator.ValidateSQLContext(c.Request.Context(), req.SQL, req.Dialect)
	if err != nil {
		c.JSON(http.StatusInternalServerError, response.ErrorResponse(
			"VALIDATION_ERROR",
			"Failed to validate SQL: "+err.Error(),
			"",
			correlationID,
		))
		return
	}

	validationResult := map[string]interface{}{
		"sql":     req.SQL,
		"dialect": req.Dialect,
		"valid":   isValid,
	}

	c.JSON(http.StatusOK, response.SuccessResponse(validationResult, correlationID))
}

// getCorrelationID extracts the correlation ID from the context
func getCorrelationID(c *gin.Context) string {
	if correlationID, exists := c.Get("correlation_id"); exists {
		if id, ok := correlationID.(string); ok {
			return id
		}
	}
	return ""
}
