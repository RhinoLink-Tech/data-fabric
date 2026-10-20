package main

import (
	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/controller"
	"github.com/RhinoLink-Tech/data-fabric/internal/middleware"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"

	"github.com/gin-gonic/gin"
)

// routerDeps carries everything newRouter needs to wire the HTTP API.
// Production wiring lives in main.go; route-level tests build it with stub
// controllers because authentication decisions abort before handlers run.
type routerDeps struct {
	cfg                      *config.Config
	authMiddleware           *security.AuthMiddleware
	rateLimiter              *middleware.RateLimiter
	healthController         *controller.HealthController
	datasourceController     *controller.DataSourceController
	queryController          *controller.QueryController
	databaseController       *controller.DatabaseController
	sqlTranslationController *controller.SQLTranslationController
}

// newRouter assembles the HTTP API. Setting the gin mode is the caller's job
// so tests can pick TestMode independently of the configured server mode.
func newRouter(deps routerDeps) *gin.Engine {
	middleware.InitMetrics()
	router := gin.New()

	// Add middleware
	router.Use(gin.Logger())
	router.Use(gin.Recovery())
	router.Use(middleware.Cors())
	router.Use(middleware.CorrelationID())
	router.Use(middleware.PrometheusMiddleware())

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

	// Metrics expose operational details, so scraping always requires a
	// standard Bearer JWT with the admin role, independent of the management
	// API authentication switch.
	metricsHandler := gin.WrapH(middleware.PrometheusHandler())
	router.GET("/metrics", deps.authMiddleware.RequireAnyRole(security.AdminRole), metricsHandler)

	// API v1 group
	api := router.Group("/api/v1")
	jsonRequestLimit := middleware.RequestBodyLimit(deps.cfg.Server.MaxRequestBodyBytes)

	// Public endpoints (no authentication required)
	public := api.Group("")
	{
		public.GET("/health", deps.healthController.HealthCheck)
		public.GET("/database/types", deps.databaseController.GetDatabaseTypes)
		public.GET("/database/compatibility", deps.databaseController.GetDatabaseCompatibility)
	}

	// SQL translation endpoints (no authentication required)
	sqlGroup := api.Group("/sql")
	{
		sqlGroup.POST("/translate", jsonRequestLimit, deps.sqlTranslationController.TranslateSQL)
		sqlGroup.GET("/dialects", deps.sqlTranslationController.GetSupportedDialects)
		sqlGroup.POST("/validate", jsonRequestLimit, deps.sqlTranslationController.ValidateSQL)
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
			datasources.POST("", jsonRequestLimit, deps.datasourceController.CreateDataSource)
			datasources.GET("", deps.datasourceController.ListDataSources)
			datasources.GET("/stats", deps.datasourceController.GetDataSourceStats)
			datasources.GET("/:id", deps.datasourceController.GetDataSource)
			datasources.PUT("/:id", jsonRequestLimit, deps.datasourceController.UpdateDataSource)
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
			fetch.POST("", jsonRequestLimit, deps.queryController.FetchQuery)
			fetch.GET("/:query_id/:slug/:token", deps.queryController.FetchNextBatch)
		}

		// Query endpoints (validation, statistics, history). Same policy as
		// the fetch group — always authenticated regardless of the
		// security.enable_auth switch (#102) — because they resolve data
		// sources by ID and expose operational data. The handlers already
		// existed; the routes they document were never registered.
		query := api.Group("/query")
		query.Use(deps.authMiddleware.RequireAuth())
		{
			query.POST("/validate", jsonRequestLimit, deps.queryController.ValidateQuery)
			query.GET("/stats", deps.queryController.GetQueryStats)
			query.GET("/history", deps.queryController.GetQueryHistory)
		}

		// Database management endpoints
		database := auth.Group("/database")
		{
			database.POST("/test-connection", jsonRequestLimit, deps.databaseController.TestDataSourceConnection)
			database.POST("/validate-config", jsonRequestLimit, deps.databaseController.ValidateDataSourceConfig)
			// Pool stats and the fleet health probe enumerate every stored
			// data source (names, types, hosts, connectivity), so they are
			// admin-only (#9). With enable_auth disabled there is no identity
			// to check and the endpoints stay open, matching the switch's
			// documented meaning for the management API.
			if deps.cfg.Security.EnableAuth {
				database.GET("/connections/stats", deps.authMiddleware.RequireAnyRole(security.AdminRole), deps.databaseController.GetConnectionStats)
				database.GET("/health", deps.authMiddleware.RequireAnyRole(security.AdminRole), deps.databaseController.GetDatabaseHealth)
			} else {
				database.GET("/connections/stats", deps.databaseController.GetConnectionStats)
				database.GET("/health", deps.databaseController.GetDatabaseHealth)
			}
		}
	}

	return router
}
