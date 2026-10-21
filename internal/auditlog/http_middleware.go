package auditlog

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/gin-gonic/gin"
)

const RowCountContextKey = "audit_rows_returned"

type fingerprinter interface{ Fingerprint(string) string }

// HTTPMiddleware records API access outcomes after the request completes.
// It reads at most maxBodyBytes for fingerprinting and restores those bytes
// together with the unread body before the route handler runs.
func HTTPMiddleware(recorder Recorder, maxBodyBytes int64) gin.HandlerFunc {
	if maxBodyBytes <= 0 {
		maxBodyBytes = 1 << 20
	}
	return func(c *gin.Context) {
		if recorder == nil || !strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Next()
			return
		}
		started := time.Now()
		captureCtx := WithCapture(c.Request.Context())
		c.Request = c.Request.WithContext(captureCtx)
		fingerprint, datasourceID, tables := captureRequestMetadata(c.Request, maxBodyBytes, recorder)
		c.Next()
		tables = append(tables, CapturedTables(captureCtx)...)

		decision, outcome, reason := DecisionAllow, "success", "request_completed"
		if c.Writer.Status() == http.StatusUnauthorized || c.Writer.Status() == http.StatusForbidden {
			decision, outcome = DecisionDeny, "denied"
			reason = "http_" + strconv.Itoa(c.Writer.Status())
		} else if c.Writer.Status() >= http.StatusBadRequest {
			decision, outcome, reason = DecisionError, "failed", "http_"+strconv.Itoa(c.Writer.Status())
		}
		actorID, tenantID := "", ""
		if caller := security.CallerFromGin(c); caller != nil {
			actorID, tenantID = caller.UserID, caller.TenantID
		}
		if id := c.Param("id"); datasourceID == "" && id != "" {
			datasourceID = id
		}
		correlationID, _ := c.Get("correlation_id")
		correlation, _ := correlationID.(string)
		operation := c.Request.Method + " " + c.FullPath()
		if c.FullPath() == "" {
			operation = c.Request.Method + " /unmatched"
		}
		rows := int64(0)
		if value, ok := c.Get(RowCountContextKey); ok {
			if count, ok := value.(int64); ok {
				rows = count
			}
			if count, ok := value.(int); ok {
				rows = int64(count)
			}
		}
		event := Event{
			CorrelationID: correlation, ActorID: actorID, TenantID: tenantID,
			Transport: "rest", Operation: operation, DataSourceID: datasourceID,
			Tables: tables, QueryFingerprint: fingerprint, Decision: decision,
			ReasonCode: reason, Outcome: outcome, StatusCode: c.Writer.Status(),
			DurationMS: time.Since(started).Milliseconds(), RowsReturned: rows,
			OccurredAt: started.UTC(),
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
		defer cancel()
		if err := recorder.Record(ctx, event); err != nil {
			slog.ErrorContext(ctx, "failed to persist audit event", "correlation_id", correlation, "error", err)
		}
	}
}

func captureRequestMetadata(request *http.Request, maxBodyBytes int64, recorder Recorder) (string, string, []string) {
	if request == nil || request.Body == nil || request.ContentLength > maxBodyBytes {
		return "", "", nil
	}
	original := request.Body
	body, err := io.ReadAll(io.LimitReader(original, maxBodyBytes+1))
	request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), original))
	if err != nil || int64(len(body)) > maxBodyBytes {
		return "", "", nil
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return "", "", nil
	}
	datasourceID := firstString(payload, "dataSourceId", "datasourceId", "data_source_id", "datasource_id")
	query := firstString(payload, "sql", "query")
	tables := []string{}
	if table := firstString(payload, "table_name", "tableName", "table"); table != "" {
		tables = append(tables, table)
	}
	fingerprint := ""
	if f, ok := recorder.(fingerprinter); ok {
		fingerprint = f.Fingerprint(query)
	}
	return fingerprint, datasourceID, tables
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
