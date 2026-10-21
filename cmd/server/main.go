package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/controller"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/mcp"
	"github.com/RhinoLink-Tech/data-fabric/internal/middleware"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
	"github.com/RhinoLink-Tech/data-fabric/internal/unified_service"
	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"

	"github.com/gin-gonic/gin"
	_ "github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

func main() {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		log.Fatal("Failed to load configuration:", err)
	}
	if err := cfg.ValidateStartup(); err != nil {
		log.Fatal("Invalid startup configuration: ", err)
	}

	vault, err := security.LoadCredentialVaultFromEnv()
	if err != nil {
		log.Fatal("Failed to load credential encryption keys: ", err)
	}

	// Check if MCP server mode is enabled
	if cfg.MCP.Enabled {
		if err := cfg.MCP.Validate(); err != nil {
			log.Fatal("Invalid MCP configuration: ", err)
		}
		log.Printf("Starting Data Fabric MCP server with %s transport...", cfg.MCP.Transport)
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
	// Validate settings before connecting to the metadata database.
	if err := cfg.ValidateStartup(); err != nil {
		return fmt.Errorf("invalid startup configuration: %w", err)
	}

	translationManager, err := newSQLTranslationManager(cfg)
	if err != nil {
		return err
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
	auditStore, err := initializeAuditStore(db, cfg)
	if err != nil {
		return err
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
	// Registered before the streaming maintenance defers so it unwinds after
	// them: cursors close first, then pooled connections and the metadata
	// database (#32).
	defer closeGatewayResources(db, connPool)
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
	tableAuthorizer := security.NewTableAuthorizer(cfg.TableAuthorization)

	// Initialize streaming and pagination services with one shared table policy.
	streamingService := service.NewStreamingServiceWithTableAuthorizer(datasourceRepo, connPool, tableAuthorizer, translationManager)
	paginationService := service.NewQueryServiceWithTableAuthorizer(datasourceRepo, connPool, cfg.Query.PreferStreaming, tableAuthorizer, translationManager)

	// Initialize unified query service that chooses between pagination and streaming based on config
	unifiedQueryService := unifiedservice.NewUnifiedQueryService(paginationService, streamingService, cfg)

	// Initialize controllers
	datasourceController := controller.NewDataSourceController(datasourceService)
	queryController := controller.NewQueryController(unifiedQueryService)
	databaseController := controller.NewDatabaseController(datasourceRepo, healthChecker)
	healthController := controller.NewHealthController(db)
	sqlTranslationController := controller.NewSQLTranslationController(translationManager)
	var auditController *controller.AuditController
	if auditStore != nil {
		auditController = controller.NewAuditController(auditStore)
	}

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
		auditController:          auditController,
		auditRecorder:            auditStore,
	})

	// Bind the listener before anything else starts so an invalid or occupied
	// bind address fails startup with the actual address in the error (#127).
	listenAddr := cfg.HTTPListenAddress()
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("failed to bind HTTP listener %s: %w", listenAddr, err)
	}

	log.Printf("Starting server on %s", listenAddr)
	log.Printf("Health check available at: http://localhost:%s/health", cfg.Server.Port)
	log.Printf("API documentation: http://localhost:%s/swagger/index.html", cfg.Server.Port)

	server := newHTTPServer(listener.Addr().String(), router, cfg.Server)
	serverCtx, stopServer := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopServer()

	stopStreaming := startStreamingMaintenance(serverCtx, streamingService)
	defer stopStreaming()
	stopAuditCleanup := startAuditRetentionMaintenance(serverCtx, auditStore)
	defer stopAuditCleanup()

	listenResult := make(chan error, 1)
	go func() {
		listenResult <- server.Serve(listener)
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

// metadataDB is the handle shutdown needs from the metadata database; the
// narrow interface keeps the helper testable without a live gorm instance.
type metadataDB interface{ DB() (*sql.DB, error) }

func initializeAuditStore(db *gorm.DB, cfg *config.Config) (*auditlog.Store, error) {
	if cfg == nil || !cfg.Audit.Enabled {
		return nil, nil
	}
	if err := auditlog.Migrate(db); err != nil {
		return nil, fmt.Errorf("audit schema migration failed: %w", err)
	}
	store, err := auditlog.NewStore(db, cfg.Security.JWTSecret, cfg.Audit.RetentionDays)
	if err != nil {
		return nil, err
	}
	if _, err := store.PruneExpired(context.Background()); err != nil {
		return nil, fmt.Errorf("audit retention cleanup failed: %w", err)
	}
	return store, nil
}

func startAuditRetentionMaintenance(parent context.Context, store *auditlog.Store) func() {
	if store == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cleanupCtx, cleanupCancel := context.WithTimeout(ctx, 10*time.Second)
				if _, err := store.PruneExpired(cleanupCtx); err != nil {
					log.Printf("failed to prune expired audit events: %v", err)
				}
				cleanupCancel()
			}
		}
	}()
	return func() { cancel(); <-done }
}

// closeGatewayResources releases the pooled data-source connections and the
// metadata database during process shutdown, after in-flight requests have
// drained. It is best-effort: failures are logged, never fatal, so one
// stubborn connection cannot block the rest of the cleanup (#32).
func closeGatewayResources(db metadataDB, pool *database.ConnectionPool) {
	if pool != nil {
		if err := pool.CloseAll(); err != nil {
			log.Printf("failed to close data-source connection pools during shutdown: %v", err)
		}
	}
	if db != nil {
		sqlDB, err := db.DB()
		if err != nil {
			log.Printf("failed to resolve metadata database handle during shutdown: %v", err)
			return
		}
		if err := sqlDB.Close(); err != nil {
			log.Printf("failed to close metadata database during shutdown: %v", err)
		}
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
	if err := cfg.ValidateStartup(); err != nil {
		return fmt.Errorf("invalid startup configuration: %w", err)
	}

	translationManager, err := newSQLTranslationManager(cfg)
	if err != nil {
		return err
	}

	var tokenValidator mcp.MCPTokenValidator
	if cfg.MCP.Transport == config.MCPTransportStreamableHTTP {
		tokenValidator, err = newMCPTokenValidator(context.Background(), cfg)
		if err != nil {
			return fmt.Errorf("failed to initialize remote MCP token validation: %w", err)
		}
	}

	db, err := config.InitDatabase(cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	if err := repository.MigrateDataSourceSchema(db); err != nil {
		return fmt.Errorf("database schema migration failed: %w", err)
	}
	auditStore, err := initializeAuditStore(db, cfg)
	if err != nil {
		return err
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
	// Unwinds after the streaming maintenance defer below: cursors close
	// first, then pooled connections and the metadata database (#32).
	defer closeGatewayResources(db, connPool)
	tableAuthorizer := security.NewTableAuthorizer(cfg.TableAuthorization)
	streamingService := service.NewStreamingServiceWithTableAuthorizer(datasourceRepo, connPool, tableAuthorizer, translationManager)
	paginationService := service.NewQueryServiceWithTableAuthorizer(datasourceRepo, connPool, cfg.Query.PreferStreaming, tableAuthorizer, translationManager)
	queryService := unifiedservice.NewUnifiedQueryService(paginationService, streamingService, cfg)
	metadataExtractor := metadata.NewMetadataExtractor(connPool, datasourceRepo)
	mcpServer := mcp.NewMCPServerWithTableAuthorizer(queryService, datasourceRepo, metadataExtractor, tableAuthorizer, translationManager)
	mcpServer.SetAuditRecorder(auditStore)

	if cfg.MCP.Transport == config.MCPTransportStreamableHTTP {
		// The remote entrance gets the same security budgets as the HTTP API:
		// per-client rate limiting from the shared config, plus body and
		// concurrency caps so one client cannot exhaust the gateway before
		// authentication runs (#185, #186).
		rateLimiter := middleware.NewRateLimiter(middleware.RateLimiterConfig{
			RPM:             cfg.Security.RateLimitPerMinute,
			Burst:           cfg.Security.RateLimitBurst,
			CleanupInterval: 5 * time.Minute,
		})
		limits := mcp.RemoteLimits{
			MaxBodyBytes:  cfg.Server.MaxRequestBodyBytes,
			MaxConcurrent: cfg.MCP.MaxConcurrentRequests,
		}
		log.Printf("Remote MCP entrance budgets: body %d bytes, %d concurrent requests, rate %d/min",
			limits.EffectiveBodyBytes(), limits.EffectiveConcurrent(), cfg.Security.RateLimitPerMinute)
		handler := mcpServer.StreamableHTTPHandlerWithLimits(tokenValidator, rateLimiter, limits)
		return serveMCPHTTP(cfg, handler, streamingService, db, connPool, auditStore)
	}

	stopStreaming := startStreamingMaintenance(context.Background(), streamingService)
	defer stopStreaming()
	stopAuditCleanup := startAuditRetentionMaintenance(context.Background(), auditStore)
	defer stopAuditCleanup()
	log.Println("Starting MCP server with transport: stdio")
	return mcpServer.StartStdio()
}

func newMCPTokenValidator(ctx context.Context, cfg *config.Config) (mcp.MCPTokenValidator, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration is required")
	}
	if strings.EqualFold(strings.TrimSpace(cfg.MCP.AuthMode), config.MCPAuthModeOIDC) {
		oidcConfig := security.OIDCTokenConfig{
			Issuer:              cfg.MCP.OIDCIssuer,
			Audience:            cfg.MCP.OIDCAudience,
			RequiredScopes:      strings.Fields(cfg.MCP.OIDCRequiredScopes),
			ScopeClaim:          cfg.MCP.OIDCScopeClaim,
			TenantClaim:         cfg.MCP.OIDCTenantClaim,
			RolesClaim:          cfg.MCP.OIDCRolesClaim,
			UsernameClaim:       cfg.MCP.OIDCUsernameClaim,
			JWKSRefreshInterval: cfg.MCP.OIDCJWKSRefreshInterval,
		}
		return security.NewOIDCTokenValidator(ctx, oidcConfig)
	}
	return security.NewJWTManager(cfg.Security.JWTSecret, cfg.Security.JWTExpiration), nil
}

func serveMCPHTTP(cfg *config.Config, handler http.Handler, streamingService service.StreamingService, db metadataDB, connPool *database.ConnectionPool, auditStore *auditlog.Store) error {
	// Unwinds after the streaming maintenance defer below: cursors close
	// first, then pooled connections and the metadata database (#32).
	defer closeGatewayResources(db, connPool)
	server := &http.Server{
		Addr:              cfg.MCP.Address,
		Handler:           handler,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		// WriteTimeout stays unset: streamable-HTTP responses may be SSE
		// streams held open beyond any fixed write deadline.
	}
	serverCtx, stopServer := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopServer()
	stopStreaming := startStreamingMaintenance(serverCtx, streamingService)
	defer stopStreaming()
	stopAuditCleanup := startAuditRetentionMaintenance(serverCtx, auditStore)
	defer stopAuditCleanup()

	listenResult := make(chan error, 1)
	go func() {
		if cfg.MCP.TLSCertFile != "" {
			listenResult <- server.ListenAndServeTLS(cfg.MCP.TLSCertFile, cfg.MCP.TLSKeyFile)
			return
		}
		listenResult <- server.ListenAndServe()
	}()

	if cfg.MCP.TLSCertFile != "" {
		log.Printf("MCP Streamable HTTP listening with TLS on %s/mcp", cfg.MCP.Address)
	} else {
		log.Printf("MCP Streamable HTTP listening on %s/mcp; terminate TLS at an HTTPS reverse proxy", cfg.MCP.Address)
	}

	select {
	case err := <-listenResult:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("failed to start MCP HTTP server: %w", err)
		}
		return nil
	case <-serverCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		shutdownErr := server.Shutdown(shutdownCtx)
		cancel()
		listenErr := <-listenResult
		if shutdownErr != nil {
			_ = server.Close()
			return fmt.Errorf("failed to shut down MCP HTTP server: %w", shutdownErr)
		}
		if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			return fmt.Errorf("failed to stop MCP HTTP server: %w", listenErr)
		}
		return nil
	}
}

func newSQLTranslationManager(cfg *config.Config) (*sql_translator.SQLTranslationManager, error) {
	c := cfg.SQLTranslation
	options := sql_translator.SQLTranslationRuntimeOptionsFromConfig(
		c.Enabled, c.PythonPath, c.ScriptPath, c.PythonModulePath,
		c.Timeout, c.MaxConcurrent, c.MaxSQLBytes, c.MaxOutputBytes,
	)
	manager := sql_translator.NewRuntimeSQLTranslationManager(options)
	if err := manager.ValidateRuntime(); err != nil {
		return nil, fmt.Errorf("SQL translation runtime validation failed: %w", err)
	}
	return manager, nil
}
