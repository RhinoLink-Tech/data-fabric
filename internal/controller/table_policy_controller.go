package controller

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/gin-gonic/gin"
)

const maxTablePolicyRequestBytes = 1 << 20

type TablePolicyController struct {
	manager *security.TablePolicyManager
}

func NewTablePolicyController(manager *security.TablePolicyManager) *TablePolicyController {
	return &TablePolicyController{manager: manager}
}

func (tc *TablePolicyController) Status() security.TablePolicyStatus {
	if tc == nil || tc.manager == nil {
		return security.TablePolicyStatus{}
	}
	return tc.manager.Status()
}

func (tc *TablePolicyController) Get(c *gin.Context) {
	if tc == nil || tc.manager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "TABLE_POLICY_UNAVAILABLE"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"status": tc.manager.Status(),
		"policy": tc.manager.CurrentSnapshot().Config,
	})
}

func (tc *TablePolicyController) GetRevision(c *gin.Context) {
	if tc == nil || tc.manager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "TABLE_POLICY_UNAVAILABLE"})
		return
	}
	revision, err := strconv.ParseInt(c.Param("revision"), 10, 64)
	if err != nil || revision <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_POLICY_REVISION"})
		return
	}
	snapshot, err := tc.manager.GetRevision(c.Request.Context(), revision)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"code": "TABLE_POLICY_REVISION_NOT_FOUND"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, snapshot)
}

func (tc *TablePolicyController) Publications(c *gin.Context) {
	if tc == nil || tc.manager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "TABLE_POLICY_UNAVAILABLE"})
		return
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	events, err := tc.manager.Publications(c.Request.Context(), limit)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "TABLE_POLICY_HISTORY_UNAVAILABLE"})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"publications": events})
}

type tablePolicyRequest struct {
	Policy security.TableAuthorizationConfig `json:"policy"`
}

type tablePolicyPublishRequest struct {
	ExpectedRevision int64                             `json:"expectedRevision"`
	Policy           security.TableAuthorizationConfig `json:"policy"`
}

func decodeTablePolicyRequest(c *gin.Context, dst any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxTablePolicyRequestBytes)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func (tc *TablePolicyController) Validate(c *gin.Context) {
	if tc == nil || tc.manager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "TABLE_POLICY_UNAVAILABLE"})
		return
	}
	var req tablePolicyRequest
	if err := decodeTablePolicyRequest(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_POLICY_REQUEST", "message": "Request body must contain a valid policy object."})
		return
	}
	if err := tc.manager.Validate(req.Policy); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "TABLE_POLICY_INVALID", "message": safePolicyValidationError(err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"valid": true, "expectedRevision": tc.manager.Status().Revision,
		"propagationWindow": security.TablePolicyPropagationWindow.String(),
	})
}

func (tc *TablePolicyController) Publish(c *gin.Context) {
	if tc == nil || tc.manager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "TABLE_POLICY_UNAVAILABLE"})
		return
	}
	var req tablePolicyPublishRequest
	if err := decodeTablePolicyRequest(c, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_POLICY_REQUEST", "message": "Request body must contain expectedRevision and policy."})
		return
	}
	if req.ExpectedRevision <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"code": "INVALID_EXPECTED_REVISION", "message": "expectedRevision must be a positive integer."})
		return
	}
	publisher := "unknown"
	if caller := security.CallerFromGin(c); caller != nil && strings.TrimSpace(caller.UserID) != "" {
		publisher = caller.UserID
	}
	result, err := tc.manager.Publish(c.Request.Context(), req.ExpectedRevision, publisher, req.Policy)
	if err == nil {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, result)
		return
	}
	switch {
	case errors.Is(err, security.ErrTablePolicyRevisionConflict):
		c.JSON(http.StatusConflict, gin.H{"code": "TABLE_POLICY_REVISION_CONFLICT", "publication": result})
	case errors.Is(err, security.ErrTablePolicyInvalid):
		c.JSON(http.StatusBadRequest, gin.H{"code": "TABLE_POLICY_INVALID", "message": safePolicyValidationError(err), "publication": result})
	case errors.Is(err, security.ErrTablePolicyAuditUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "TABLE_POLICY_AUDIT_UNAVAILABLE", "auditRecorded": false})
	default:
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": "TABLE_POLICY_PUBLICATION_UNAVAILABLE", "auditRecorded": false})
	}
}

func safePolicyValidationError(err error) string {
	if err == nil {
		return "Policy validation failed."
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}
