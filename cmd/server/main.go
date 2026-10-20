package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/controller"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/mcp"
	"github.com/RhinoLink-Tech/data-fabric/internal/middleware"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	"github.com/RhinoLink-Tech/data-fabric/internal/unified_service"

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
		log.Fatal("Failed to load credential encryption keys: ", err)
	}

	// Check if MCP server mode is enabled
	if cfg.MCP.Enabled {
		if cfg.MCP.Transport != "" && cfg.MCP.Transport != "stdio" {
			log.Fatalf("unsupported MCP transport %q: only stdio is currently implemented", cfg.MCP.Transport)
		}
		log.Println("Starting Data Fabric as MCP Server...")
		if err := startMCPServer(cfg, vault); err != nil {
			log.Fatal("Failed to start MCP server:", err)
		}
		return
	}

	// Normal HTTP server mode
	if err := startHTTPServer(cfg, vault); err != nil {
		log.Fatal("Failed to start HTTP server: ", err)
	}
}

func startHTTPServer(cfg *config.Config, vault *security.CredentialVault) error {
	// Fail closed on insecure authentication settings before the metadata
	// database is touched.
	if err := cfg.ValidateSecurity(); err != nil {
		return fmt.Errorf("invalid security configuration: %w", err)
	}

	// Set Gin mode
	if cfg.Server.Mode == "release" {
		gin.SetMode(gin.ReleaseMode)
	}

	// Initialize database connection
	db, err := config.InitDatabase(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	// Auto migrate database schema
	if err := repository.MigrateDataSourceSchema(db); err != nil {
		return fmt.Errorf("database schema migration failed: %w", err)
	}

	// Migrate old plaintext records and rotate any ciphertext before serving requests.
	datasourceRepo, err := repository.NewDataSourceRepository(db, vault)
	if err != nil {
		return fmt.Errorf("failed to initialize data-source repository: %w", err)
	}
	migrated, err := datasourceRepo.MigrateCredentials(context.Background())
	if err != nil {
		return fmt.Errorf("credential migration failed; refusing to start: %w", err)
	}
	if migrated > 0 {
		log.Printf("Encrypted or rotated credentials for %d data sources", migrated)
	}

	// Initialize infrastructure
	connPool := database.NewConnectionPool(security.NewTokenManager(vault), vault, cfg.Security.EnforceReadOnly)
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
	datasourceService := service.NewDataSourceService(datasourceRepo, connPool, healthChecker)

	// Initialize streaming service
	streamingService := service.NewStreamingService(datasourceRepo, connPool)

	// Initialize pagination service (original query service) with streaming support
	paginationService := service.NewQueryService(datasourceRepo, connPool, cfg.Query.PreferStreaming)

	// Initialize unified query service that chooses between pagination and streaming based on config
	unifiedQueryService := unifiedservice.NewUnifiedQueryService(paginationService, streamingService, cfg)

	// Initialize controllers
	datasourceController := controller.NewDataSourceController(datasourceService)
	queryController := controller.NewQueryController(unifiedQueryService)
	databaseController := controller.NewDatabaseController(datasourceRepo, healthChecker)
	healthController := controller.NewHealthController(db)
	sqlTranslationController := controller.NewSQLTranslationController() // Added SQL translation controller

	// Create Gin router
	router := newRouter(routerDeps{
		cfg:                      cfg,
		authMiddleware:           authMiddleware,
		rateLimiter:              rateLimiter,
		healthController:         healthController,
		datasourceController:     datasourceController,
		queryController:          queryController,
		databaseController:       databaseController,
		sqlTranslationController: sqlTranslationController,
	})

	// Start server
	log.Printf("Starting server on port %s", cfg.Server.Port)
	log.Printf("Health check available at: http://localhost:%s/health", cfg.Server.Port)
	log.Printf("API documentation: http://localhost:%s/swagger/index.html", cfg.Server.Port)

	server := newHTTPServer(":"+cfg.Server.Port, router, cfg.Server)
	serverCtx, stopServer := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopServer()

	stopStreaming := startStreamingMaintenance(serverCtx, streamingService)
	defer stopStreaming()

	listenResult := make(chan error, 1)
	go func() {
		listenResult <- server.ListenAndServe()
	}()

	select {
	case err := <-listenResult:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("failed to start server: %w", err)
		}
		return nil
	case <-serverCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		shutdownErr := server.Shutdown(shutdownCtx)
		cancel()
		listenErr := <-listenResult
		if shutdownErr != nil {
			_ = server.Close()
			return fmt.Errorf("failed to shut down HTTP server: %w", shutdownErr)
		}
		if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			return fmt.Errorf("failed to stop HTTP server: %w", listenErr)
		}
		return nil
	}
}

func startStreamingMaintenance(parent context.Context, streamingService service.StreamingService) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		streamingService.StartCleanupRoutine(ctx, time.Minute)
	}()

	return func() {
		cancel()
		<-done
		if err := streamingService.Shutdown(); err != nil {
			log.Printf("failed to close streaming resources during shutdown: %v", err)
		}
	}
}

func startMCPServer(cfg *config.Config, vault *security.CredentialVault) error {
	db, err := config.InitDatabase(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	if err := repository.MigrateDataSourceSchema(db); err != nil {
		return fmt.Errorf("database schema migration failed: %w", err)
	}
	datasourceRepo, err := repository.NewDataSourceRepository(db, vault)
	if err != nil {
		return fmt.Errorf("failed to initialize data-source repository: %w", err)
	}
	migrated, err := datasourceRepo.MigrateCredentials(context.Background())
	if err != nil {
		return fmt.Errorf("credential migration failed; refusing to start: %w", err)
	}
	if migrated > 0 {
		log.Printf("Encrypted or rotated credentials for %d data sources", migrated)
	}

	connPool := database.NewConnectionPool(security.NewTokenManager(vault), vault, cfg.Security.EnforceReadOnly)
	streamingService := service.NewStreamingService(datasourceRepo, connPool)
	stopStreaming := startStreamingMaintenance(context.Background(), streamingService)
	defer stopStreaming()

	paginationService := service.NewQueryService(datasourceRepo, connPool, cfg.Query.PreferStreaming)
	queryService := unifiedservice.NewUnifiedQueryService(paginationService, streamingService, cfg)
	mcpServer := mcp.NewMCPServer(queryService, datasourceRepo)

	log.Printf("Starting MCP server with transport: %s", cfg.MCP.Transport)
	return mcpServer.StartStdio()
}
