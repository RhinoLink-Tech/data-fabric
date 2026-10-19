package controller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/gin-gonic/gin"
)

type databaseHealthDataSourceListerStub struct {
	sources []*model.DataSource
	total   int64
	err     error
	status  model.DataSourceStatus
	calls   int
}

func (s *databaseHealthDataSourceListerStub) GetAll(_ context.Context, status model.DataSourceStatus, limit, offset int) ([]*model.DataSource, int64, error) {
	s.calls++
	s.status = status
	if s.err != nil {
		return nil, 0, s.err
	}
	if offset >= len(s.sources) {
		return nil, s.total, nil
	}
	end := offset + limit
	if end > len(s.sources) {
		end = len(s.sources)
	}
	return s.sources[offset:end], s.total, nil
}

func TestGetDatabaseHealthProbesConfiguredSourcesWithDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sources := &databaseHealthDataSourceListerStub{
		sources: []*model.DataSource{
			{ID: "healthy-source", Type: model.DatabaseTypePostgreSQL},
			{ID: "unhealthy-source", Type: model.DatabaseTypeMySQL},
		},
		total: 2,
	}
	deadlines := make(chan time.Time, 2)
	dc := &DatabaseController{
		datasourceRepo: sources,
		healthProbe: func(ctx context.Context, source *model.DataSource) (*database.HealthCheckResult, error) {
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Error("health probe context has no deadline")
			} else {
				deadlines <- deadline
			}
			if source.ID == "unhealthy-source" {
				return &database.HealthCheckResult{
					DataSourceID: source.ID,
					DatabaseType: string(source.Type),
					Status:       "unhealthy",
					Message:      "connection failed: password=do-not-leak",
				}, nil
			}
			return &database.HealthCheckResult{
				DataSourceID: source.ID,
				DatabaseType: string(source.Type),
				Status:       "healthy",
			}, nil
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/database/health", nil)
	startedAt := time.Now()
	dc.GetDatabaseHealth(ctx)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d; body: %s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "do-not-leak") {
		t.Fatalf("health response leaked a driver error: %s", recorder.Body.String())
	}
	var payload struct {
		Success   bool                           `json:"success"`
		Data      database.DatabaseHealthSummary `json:"data"`
		Timestamp time.Time                      `json:"timestamp"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Success || payload.Data.Status != "degraded" || payload.Data.TotalConnections != 2 || payload.Data.HealthyConnections != 1 || payload.Data.UnhealthyConnections != 1 {
		t.Fatalf("unexpected health summary: %+v", payload)
	}
	if payload.Data.Results[1].Message != "Health check failed" {
		t.Fatalf("unhealthy result message = %q, want sanitized message", payload.Data.Results[1].Message)
	}
	if payload.Data.CheckedAt.Before(startedAt) || payload.Data.CheckedAt.After(time.Now()) || payload.Timestamp.Before(startedAt) {
		t.Fatalf("health timestamps were not generated at response time: %+v", payload)
	}
	if sources.status != model.DataSourceStatusActive {
		t.Fatalf("queried status = %q, want active sources", sources.status)
	}
	if len(deadlines) != 2 {
		t.Fatalf("deadline-aware probes = %d, want 2", len(deadlines))
	}
	for range 2 {
		deadline := <-deadlines
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > databaseHealthTimeout {
			t.Fatalf("probe deadline remaining = %s, want within %s", remaining, databaseHealthTimeout)
		}
	}
}

func TestGetDatabaseHealthReturnsHealthyForReachableSources(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sources := &databaseHealthDataSourceListerStub{
		sources: []*model.DataSource{{ID: "source", Type: model.DatabaseTypePostgreSQL}},
		total:   1,
	}
	dc := &DatabaseController{
		datasourceRepo: sources,
		healthProbe: func(_ context.Context, source *model.DataSource) (*database.HealthCheckResult, error) {
			return &database.HealthCheckResult{DataSourceID: source.ID, DatabaseType: string(source.Type), Status: "healthy"}, nil
		},
	}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/database/health", nil)
	dc.GetDatabaseHealth(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var payload struct {
		Data database.DatabaseHealthSummary `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Status != "healthy" || payload.Data.HealthyConnections != 1 || payload.Data.UnhealthyConnections != 0 {
		t.Fatalf("unexpected health summary: %+v", payload.Data)
	}
}

func TestGetDatabaseHealthDoesNotExposeRepositoryErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sources := &databaseHealthDataSourceListerStub{err: errors.New("database password=do-not-leak")}
	dc := &DatabaseController{datasourceRepo: sources, healthProbe: func(context.Context, *model.DataSource) (*database.HealthCheckResult, error) {
		t.Fatal("health probe must not run when source loading fails")
		return nil, nil
	}}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/database/health", nil)
	dc.GetDatabaseHealth(ctx)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(recorder.Body.String(), "do-not-leak") {
		t.Fatalf("health response leaked repository error: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "timestamp") {
		t.Fatalf("health failure response has no timestamp: %s", recorder.Body.String())
	}
}
