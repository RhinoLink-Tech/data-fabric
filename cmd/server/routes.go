package main

import (
	"nexus-gateway/internal/config"
	"nexus-gateway/internal/controller"
	"nexus-gateway/internal/middleware"
	"nexus-gateway/internal/security"

	"github.com/gin-gonic/gin"
)

// routerDeps carries everything newRouter needs to wire the HTTP API.
// Production wiring lives in main.go; route-level tests build it with stub
// controllers because authentication decisions abort before handlers run.
type routerDeps struct {
	cfg                  *config.Config
	authMiddleware       *security.AuthMiddleware
	rateLimiter          *middleware.RateLimiter
	healthController     *controller.HealthController
	datasourceController *controller.DataSourceController
	queryController      *controller.QueryController
	databaseController   *controller.DatabaseController
}

// newRouter assembles the HTTP API. Setting the gin mode is the caller's job
// so tests can pick TestMode independently of the configured server mode.
func newRouter(deps routerDeps) *gin.Engine {
	router := gin.New()

	// Add middleware
	router.Use(gin.Logger())
	router.Use(gin.Recovery())
	router.Use(middleware.Cors())
	router.Use(middleware.CorrelationID())

	// Authenticate optional JWTs before rate limiting so verified users share
	// a per-user bucket. Invalid or absent credentials fall back to the peer IP.
	// With enable_auth disabled this stays unregistered and everyone is
	// bucketed by IP — pre-existing behavior, kept as is.
	if deps.cfg.Security.EnableAuth && deps.cfg.Security.EnableRateLimit {
		router.Use(deps.authMiddleware.OptionalAuth())
	}

	// Add rate limiting if enabled
	if deps.cfg.Security.EnableRateLimit {
		router.Use(deps.rateLimiter.RateLimit())
	}

	// Health check endpoint (always available)
	router.GET("/health", deps.healthController.HealthCheck)

	// API v1 group
	api := router.Group("/api/v1")

	// Public endpoints (no authentication required)
	public := api.Group("")
	{
		public.GET("/health", deps.healthController.HealthCheck)
		public.GET("/database/types", deps.databaseController.GetDatabaseTypes)
		public.GET("/database/compatibility", deps.databaseController.GetDatabaseCompatibility)
	}

	// Endpoints gated by the security.enable_auth switch (management API)
	auth := api.Group("")
	if deps.cfg.Security.EnableAuth {
		auth.Use(deps.authMiddleware.RequireAuth())
	}
	{
		// Data source endpoints
		datasources := auth.Group("/datasources")
		{
			datasources.POST("", deps.datasourceController.CreateDataSource)
			datasources.GET("", deps.datasourceController.ListDataSources)
			datasources.GET("/stats", deps.datasourceController.GetDataSourceStats)
			datasources.GET("/:id", deps.datasourceController.GetDataSource)
			datasources.PUT("/:id", deps.datasourceController.UpdateDataSource)
			datasources.DELETE("/:id", deps.datasourceController.DeleteDataSource)
			datasources.POST("/:id/activate", deps.datasourceController.ActivateDataSource)
			datasources.POST("/:id/deactivate", deps.datasourceController.DeactivateDataSource)
		}

		// Fetch endpoints (batch data retrieval). Always authenticated (#6):
		// /fetch executes SQL against backends, so it stays locked even when
		// security.enable_auth is disabled — the switch only gates the
		// management API above. Subsequent GET batches require both a valid
		// Bearer token and the per-session path token.
		fetch := api.Group("/fetch")
		fetch.Use(deps.authMiddleware.RequireAuth())
		{
			fetch.POST("", deps.queryController.FetchQuery)
			fetch.GET("/:query_id/:slug/:token", deps.queryController.FetchNextBatch)
		}

		// Database management endpoints
		database := auth.Group("/database")
		{
			database.POST("/test-connection", deps.databaseController.TestDataSourceConnection)
			database.POST("/validate-config", deps.databaseController.ValidateDataSourceConfig)
			database.GET("/connections/stats", deps.databaseController.GetConnectionStats)
			database.GET("/health", deps.databaseController.GetDatabaseHealth)
		}
	}

	// Keep the legacy internal gateway endpoints for existing clients.
	internalAPI := router.Group("/api/internal/v2")
	internalFetch := internalAPI.Group("/fetch")
	internalFetch.POST("", deps.queryController.InternalFetchQuery)
	internalFetch.GET("/:query_id/:slug/:token", deps.queryController.InternalFetchNextBatch)

	return router
}
