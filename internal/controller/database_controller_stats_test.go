package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/gin-gonic/gin"
)

type connectionPoolStatsProviderStub struct {
	pools map[string]database.ConnectionStats
}

func (s connectionPoolStatsProviderStub) GetConnectionPoolStats() map[string]database.ConnectionStats {
	return s.pools
}

func TestGetConnectionStatsReturnsLivePoolAndAggregateStats(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dc := &DatabaseController{poolStatsProvider: connectionPoolStatsProviderStub{pools: map[string]database.ConnectionStats{
		"postgres-source": {
			OpenConnections: 5,
			InUse:           2,
			Idle:            3,
			WaitCount:       7,
			WaitDuration:    23 * time.Millisecond,
			Healthy:         true,
		},
		"mysql-source": {
			OpenConnections: 3,
			InUse:           1,
			Idle:            2,
			WaitCount:       4,
			WaitDuration:    11 * time.Millisecond,
			Healthy:         false,
		},
	}}}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/database/connections/stats", nil)
	startedAt := time.Now()
	dc.GetConnectionStats(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var payload struct {
		Success bool                        `json:"success"`
		Data    ConnectionPoolStatsResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	stats := payload.Data
	if !payload.Success {
		t.Fatalf("success = false; body: %s", recorder.Body.String())
	}
	if stats.TotalPools != 2 || stats.TotalConnections != 8 || stats.ActiveConnections != 3 || stats.IdleConnections != 5 {
		t.Fatalf("unexpected aggregate pool stats: %+v", stats)
	}
	if stats.WaitCount != 11 || stats.WaitDuration != 34*time.Millisecond {
		t.Fatalf("unexpected wait stats: count=%d duration=%s", stats.WaitCount, stats.WaitDuration)
	}
	if len(stats.Pools) != 2 || stats.Pools["postgres-source"].OpenConnections != 5 || stats.Pools["mysql-source"].Healthy {
		t.Fatalf("per-pool stats were not returned: %+v", stats.Pools)
	}
	if stats.CheckedAt.Before(startedAt) || stats.Message == "" {
		t.Fatalf("response metadata is missing or stale: %+v", stats)
	}
}

func TestGetConnectionStatsReturnsEmptyForUninitializedPools(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dc := &DatabaseController{poolStatsProvider: connectionPoolStatsProviderStub{pools: map[string]database.ConnectionStats{}}}

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/database/connections/stats", nil)
	dc.GetConnectionStats(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var payload struct {
		Data ConnectionPoolStatsResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Pools == nil || len(payload.Data.Pools) != 0 || payload.Data.TotalPools != 0 || payload.Data.TotalConnections != 0 {
		t.Fatalf("unexpected uninitialized pool response: %+v", payload.Data)
	}
}

func TestGetConnectionStatsFailsWhenPoolStatsAreUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dc := NewDatabaseController(nil, nil)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/v1/database/connections/stats", nil)
	dc.GetConnectionStats(ctx)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
}
