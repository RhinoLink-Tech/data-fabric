package controller

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/pkg/response"
	"github.com/gin-gonic/gin"
)

type AuditController struct{ store *auditlog.Store }

func NewAuditController(store *auditlog.Store) *AuditController {
	return &AuditController{store: store}
}

// ListEvents returns a bounded, filterable page. Route registration always
// requires a verified admin role, including when management auth is disabled.
func (ac *AuditController) ListEvents(c *gin.Context) {
	limit, err := optionalInt(c.Query("limit"), 100)
	if err != nil || limit < 1 || limit > 500 {
		c.JSON(http.StatusBadRequest, response.ErrorResponse("INVALID_AUDIT_FILTER", "limit must be between 1 and 500", "", auditCorrelationID(c)))
		return
	}
	offset, err := optionalInt(c.Query("offset"), 0)
	if err != nil || offset < 0 {
		c.JSON(http.StatusBadRequest, response.ErrorResponse("INVALID_AUDIT_FILTER", "offset must be zero or greater", "", auditCorrelationID(c)))
		return
	}
	filter := auditlog.Filter{
		ActorID: c.Query("actor_id"), TenantID: c.Query("tenant_id"),
		Transport: c.Query("transport"), Operation: c.Query("operation"), DataSourceID: c.Query("data_source_id"),
		Decision: c.Query("decision"), CorrelationID: c.Query("correlation_id"),
		Limit: limit, Offset: offset,
	}
	if filter.Decision != "" && filter.Decision != auditlog.DecisionAllow && filter.Decision != auditlog.DecisionDeny && filter.Decision != auditlog.DecisionError {
		c.JSON(http.StatusBadRequest, response.ErrorResponse("INVALID_AUDIT_FILTER", "decision must be allow, deny, or error", "", auditCorrelationID(c)))
		return
	}
	if raw := c.Query("from"); raw != "" {
		parsed, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, response.ErrorResponse("INVALID_AUDIT_FILTER", "from must be RFC3339", "", auditCorrelationID(c)))
			return
		}
		filter.From = &parsed
	}
	if raw := c.Query("to"); raw != "" {
		parsed, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, response.ErrorResponse("INVALID_AUDIT_FILTER", "to must be RFC3339", "", auditCorrelationID(c)))
			return
		}
		filter.To = &parsed
	}
	page, err := ac.store.List(c.Request.Context(), filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, response.ErrorResponse("AUDIT_QUERY_FAILED", "Unable to read audit events", "", auditCorrelationID(c)))
		return
	}
	c.JSON(http.StatusOK, response.SuccessResponse(page, auditCorrelationID(c)))
}

func optionalInt(raw string, fallback int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	return strconv.Atoi(raw)
}

func auditCorrelationID(c *gin.Context) string {
	value, _ := c.Get("correlation_id")
	id, _ := value.(string)
	return id
}
