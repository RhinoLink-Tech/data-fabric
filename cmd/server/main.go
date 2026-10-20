package main

import (
	"context"
	"log"
	"time"

	"nexus-gateway/internal/config"
	"nexus-gateway/internal/controller"
	"nexus-gateway/internal/database"
	"nexus-gateway/internal/middleware"
	"nexus-gateway/internal/model"
	"nexus-gateway/internal/repository"
	"nexus-gateway/internal/security"
	"nexus-gateway/internal/service"
	"nexus-gateway/internal/unified_service"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("Failed to load configuration:", err)
	}

	vault, err := security.LoadCredentialVaultFromEnv()
	if err != nil {
		log.Fatal("Failed to load credential encryption keys:", err)
	}
	if err := cfg.ValidateSecurity(); err != nil {
		log.Fatal("Invalid security configuration:", err)
	}

	// Set Gin mode
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Initialize database connection
	db, err := config.InitDatabase(cfg)
	if err != nil {
		log.Fatal("Failed to initialize database:", err)
	}

	// The maintenance candidate does not change metadata columns. Create the
	// schema for a clean install, but avoid rerunning legacy GORM DDL against
	// existing v1.2.0 installations; that path can try to drop a missing index.
	if !db.Migrator().HasTable(&model.DataSource{}) {
		if err := db.AutoMigrate(&model.DataSource{}); err != nil {
			log.Fatalf("Database schema initialization failed: %v", err)
		}
	}

	datasourceRepo, err := repository.NewDataSourceRepository(db, vault)
	if err != nil {
		log.Fatalf("Failed to initialize data-source repository: %v", err)
	}
	if migrated, err := datasourceRepo.MigrateCredentials(context.Background()); err != nil {
		log.Fatalf("Credential migration failed; refusing to start: %v", err)
	} else if migrated > 0 {
		log.Printf("Encrypted or rotated credentials for %d data sources", migrated)
	}

	// Initialize infrastructure
	connPool := database.NewConnectionPool(security.NewTokenManager(vault), vault)
	healthChecker := database.NewHealthChecker(connPool)

	// Initialize security
	jwtManager := security.NewJWTManager(cfg.Security.JWTSecret, cfg.Security.JWTExpiration)
	authMiddleware := security.NewAuthMiddleware(jwtManager)

	// Initialize rate limiting
	rateLimitConfig := middleware.RateLimiterConfig{
		RPM:             cfg.Security.RateLimitPerMinute,
		Burst:           cfg.Security.RateLimitBurst,
		CleanupInterval: 5 * time.Minute,
	}
	rateLimiter := middleware.NewRateLimiter(rateLimitConfig)

	// Initialize services
	datasourceService := service.NewDataSourceService(datasourceRepo)

	// Initialize streaming service
	streamingService := service.NewStreamingService(datasourceRepo, connPool)

	// Initialize pagination service (original query service) with streaming support
	paginationService := service.NewQueryService(datasourceRepo, connPool, cfg.Query.PreferStreaming)

	// Initialize unified query service that chooses between pagination and streaming based on config
	unifiedQueryService := unifiedservice.NewUnifiedQueryService(paginationService, streamingService, cfg)
	queryService := (service.QueryService)(unifiedQueryService)

	// Initialize controllers
	datasourceController := controller.NewDataSourceController(datasourceService)
	queryController := controller.NewQueryController(queryService)
	databaseController := controller.NewDatabaseController(datasourceRepo, healthChecker)
	healthController := controller.NewHealthController(db)

	// Create Gin router
	router := newRouter(routerDeps{
		cfg:                  cfg,
		authMiddleware:       authMiddleware,
		rateLimiter:          rateLimiter,
		healthController:     healthController,
		datasourceController: datasourceController,
		queryController:      queryController,
		databaseController:   databaseController,
	})

	// Start server
	log.Printf("Starting server on port %s", cfg.Server.Port)
	log.Printf("Health check available at: http://localhost:%s/health", cfg.Server.Port)
	log.Printf("API documentation: http://localhost:%s/swagger/index.html", cfg.Server.Port)

	if err := router.Run(":" + cfg.Server.Port); err != nil {
		log.Fatal("Failed to start server:", err)
	}
}
