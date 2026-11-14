package controller

import (
	"fmt"
	"net/http"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/buildinfo"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type HealthResponse struct {
	Status         string            `json:"status"`
	Timestamp      time.Time         `json:"timestamp"`
	Service        string            `json:"service"`
	Version        string            `json:"version"`
	SourceRevision string            `json:"source_revision"`
	Database       DatabaseStatus    `json:"database"`
	Connections    map[string]string `json:"connections"`
}

type DatabaseStatus struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type HealthController struct {
	db *gorm.DB
}

func NewHealthController(db *gorm.DB) *HealthController {
	return &HealthController{
		db: db,
	}
}

func (hc *HealthController) HealthCheck(c *gin.Context) {
	response := HealthResponse{
		Status:         "healthy",
		Timestamp:      time.Now(),
		Service:        "data-fabric",
		Version:        buildinfo.Version,
		SourceRevision: buildinfo.SourceRevision,
		Connections:    make(map[string]string),
	}

	// Check database connection
	sqlDB, err := hc.db.DB()
	if err != nil {
		response.Status = "unhealthy"
		response.Database = DatabaseStatus{
			Status:  "disconnected",
			Message: "Failed to get database instance",
		}
	} else if err := sqlDB.Ping(); err != nil {
		response.Status = "unhealthy"
		response.Database = DatabaseStatus{
			Status:  "disconnected",
			Message: "Database ping failed: " + err.Error(),
		}
	} else {
		stats := sqlDB.Stats()
		response.Database = DatabaseStatus{
			Status:  "connected",
			Message: "Database connection healthy",
		}
		response.Connections["database_open_connections"] = fmt.Sprintf("%d", stats.OpenConnections)
		response.Connections["database_in_use"] = fmt.Sprintf("%d", stats.InUse)
		response.Connections["database_idle"] = fmt.Sprintf("%d", stats.Idle)
	}

	// Set HTTP status based on health
	statusCode := http.StatusOK
	if response.Status == "unhealthy" {
		statusCode = http.StatusServiceUnavailable
	}

	c.JSON(statusCode, response)
}
