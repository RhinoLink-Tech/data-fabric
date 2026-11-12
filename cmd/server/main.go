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
	"sync"
	"syscall"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/auditlog"
	"github.com/RhinoLink-Tech/data-fabric/internal/config"
	"github.com/RhinoLink-Tech/data-fabric/internal/controller"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/metadata"
	"github.com/RhinoLink-Tech/data-fabric/internal/mcp"
	"github.com/RhinoLink-Tech/data-fabric/internal/middleware"
	"github.com/RhinoLink-Tech/data-fabric/internal/replicarouting"
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

func newTablePolicyManager(db *gorm.DB, cfg *config.Config, authorizer *security.TableAuthorizer) (*security.TablePolicyManager, error) {
	if cfg == nil {
		return nil, nil
	}
	manager, err := security.NewTablePolicyManager(db, authorizer, cfg.TableAuthorization, cfg.Routing.PolicyRevision)
	if err != nil {
		return nil, fmt.Errorf("initialize table policy manager: %w", err)
	}
	return manager, nil
}

func configureManagedTablePolicyMetrics(manager *security.TablePolicyManager) {
	middleware.SetManagedTablePolicyStatusProvider(nil)
	if manager == nil {
		return
	}
	manager.SetSyncFailureObserver(middleware.RecordManagedTablePolicySyncFailure)
	middleware.SetManagedTablePolicyStatusProvider(func() middleware.ManagedTablePolicyStatus {
		status := manager.Status()
		return middleware.ManagedTablePolicyStatus{
			Configured: status.Revision != "",
			Fresh:      status.Fresh,
			LastSync:   status.LastSyncAt,
		}
	})
}

func newRouteOwner(cfg config.RoutingConfig, pool string) (*replicarouting.Owner, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	keys, err := replicarouting.ParseKeyRing(cfg.DeploymentID, os.Getenv("DATA_FABRIC_ROUTING_KEYS"))
	if err != nil {
		return nil, err
	}
	return keys.NewOwner(os.Getenv("DATA_FABRIC_ROUTING_ACTIVE_KEY_ID"), pool, cfg.OwnerSlot)
}

func startHTTPServer(cfg *config.Config, vault *security.CredentialVault) error {
	// Validate settings before connecting to the metadata database.
	if err := cfg.ValidateStartup(); err != nil {
		return fmt.Errorf("invalid startup configuration: %w", err)
	}

	routing, err := newRouteOwner(cfg.Routing, replicarouting.REST)
	if err != nil {
		return err
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
	tokenManager := security.NewTokenManager(vault)
	connPool := database.NewConnectionPool(database.GetDriverRegistry(), tokenManager, vault, cfg.Security.EnforceReadOnly)
	tokenStore, ok := datasourceRepo.(security.TokenStore)
	if !ok {
		return fmt.Errorf("data-source repository does not support encrypted access-token storage")
	}
	connPool.SetTokenStore(tokenStore)
	connPool.StartTokenRotation(context.Background())
	// Query sessions and cursors are closed before connection pools and the
	// metadata database unwind (#32).
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
	tablePolicyManager, err := newTablePolicyManager(db, cfg, tableAuthorizer)
	if err != nil {
		return err
	}
	configureManagedTablePolicyMetrics(tablePolicyManager)
	if tablePolicyManager != nil {
		tablePolicyManager.Start(context.Background())
		defer tablePolicyManager.Stop()
	}

	// Initialize streaming and pagination services with one shared table policy.
	queryStats := service.NewQueryStatsTracker()
	queryAdmissionLimiter := service.NewQueryAdmissionLimiterWithRateLimit(
		cfg.Query.MaxConcurrentQueries,
		cfg.Query.MaxConcurrentQueriesPerTenant,
		cfg.Query.RateLimitPerMinutePerTenant,
		cfg.Query.RateLimitBurstPerTenant,
	)
	middleware.SetQueryAdmissionCapacityProvider(queryAdmissionLimiter.CapacitySnapshot)
	queryBudgets := service.QueryBudgetLimits{
		MaxRows:     cfg.Query.MaxResultRows,
		MaxBytes:    cfg.Query.MaxResultBytes,
		MaxDuration: time.Duration(cfg.Query.MaxDurationSeconds) * time.Second,
	}
	paginationService, streamingService := service.NewGatewayQueryServicesWithBudgets(datasourceRepo, connPool, cfg.Query.PreferStreaming,
		tableAuthorizer, queryStats, queryAdmissionLimiter, time.Duration(cfg.Query.SessionTTLSeconds)*time.Second, queryBudgets, translationManager)
	bindDataSourceStreamInvalidation(connPool, streamingService)
	var stopStreaming func()
	var queryOwner *unifiedservice.OwnerLifecycle
	var queryCleanup sync.Once
	shutdownQueryServices := func() {
		queryCleanup.Do(func() {
			if stopStreaming != nil {
				stopStreaming()
			} else if err := streamingService.Shutdown(); err != nil {
				log.Printf("failed to close streaming resources during shutdown: %v", err)
			}
			paginationService.Shutdown()
		})
	}
	defer shutdownQueryServices()
	if cfg.Routing.Enabled {
		queryOwner = unifiedservice.NewOwnerLifecycle(cfg.Routing.DrainTimeout(), func(now time.Time) int {
			return paginationService.LiveSessionCount(now) + streamingService.LiveSessionCount(now)
		}, shutdownQueryServices)
		queryOwner.SetIdentity(routing.Slot(), routing.BootID())
	}
	unifiedQueryService := unifiedservice.NewUnifiedQueryServiceWithOwner(paginationService, streamingService, cfg, queryOwner, routing)

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
	var tablePolicyController *controller.TablePolicyController
	if tablePolicyManager != nil {
		tablePolicyController = controller.NewTablePolicyController(tablePolicyManager)
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
		tableAuthorizer:          tableAuthorizer,
		tablePolicyController:    tablePolicyController,
		queryOwner:               queryOwner,
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

	stopStreaming = startStreamingMaintenance(serverCtx, streamingService)
	if queryOwner != nil {
		defer queryOwner.Shutdown()
	}
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
		if queryOwner != nil {
			queryOwner.Shutdown()
		}
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

func bindDataSourceStreamInvalidation(pool *database.ConnectionPool, streams service.StreamingService) {
	closer, ok := streams.(interface {
		CloseDataSourceStreams(string) int
	})
	if !ok {
		return
	}
	pool.SetDataSourceInvalidationHandler(func(dataSourceID string) {
		if closed := closer.CloseDataSourceStreams(dataSourceID); closed > 0 {
			log.Printf("closed %d active stream(s) after data source %s changed", closed, dataSourceID)
		}
	})
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
	routing, err := newRouteOwner(cfg.Routing, replicarouting.MCP)
	if err != nil {
		return err
	}

	translationManager, err := newSQLTranslationManager(cfg)
	if err != nil {
		return err
	}

	var tokenValidator mcp.MCPTokenValidator
	if strings.TrimSpace(cfg.MCP.Transport) == config.MCPTransportStreamableHTTP {
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

	tokenManager := security.NewTokenManager(vault)
	connPool := database.NewConnectionPool(database.GetDriverRegistry(), tokenManager, vault, cfg.Security.EnforceReadOnly)
	tokenStore, ok := datasourceRepo.(security.TokenStore)
	if !ok {
		return fmt.Errorf("data-source repository does not support encrypted access-token storage")
	}
	connPool.SetTokenStore(tokenStore)
	connPool.StartTokenRotation(context.Background())
	// The query lifecycle cleanup below runs before connection pools and the
	// metadata database are closed (#32).
	defer closeGatewayResources(db, connPool)
	tableAuthorizer := security.NewTableAuthorizer(cfg.TableAuthorization)
	tablePolicyManager, err := newTablePolicyManager(db, cfg, tableAuthorizer)
	if err != nil {
		return err
	}
	configureManagedTablePolicyMetrics(tablePolicyManager)
	if tablePolicyManager != nil {
		tablePolicyManager.Start(context.Background())
		defer tablePolicyManager.Stop()
	}
	queryStats := service.NewQueryStatsTracker()
	queryAdmissionLimiter := service.NewQueryAdmissionLimiterWithRateLimit(
		cfg.Query.MaxConcurrentQueries,
		cfg.Query.MaxConcurrentQueriesPerTenant,
		cfg.Query.RateLimitPerMinutePerTenant,
		cfg.Query.RateLimitBurstPerTenant,
	)
	middleware.SetQueryAdmissionCapacityProvider(queryAdmissionLimiter.CapacitySnapshot)
	queryBudgets := service.QueryBudgetLimits{
		MaxRows:     cfg.Query.MaxResultRows,
		MaxBytes:    cfg.Query.MaxResultBytes,
		MaxDuration: time.Duration(cfg.Query.MaxDurationSeconds) * time.Second,
	}
	paginationService, streamingService := service.NewGatewayQueryServicesWithBudgets(datasourceRepo, connPool, cfg.Query.PreferStreaming,
		tableAuthorizer, queryStats, queryAdmissionLimiter, time.Duration(cfg.Query.SessionTTLSeconds)*time.Second, queryBudgets, translationManager)
	bindDataSourceStreamInvalidation(connPool, streamingService)
	var routedTransport *mcp.OwnerTransport
	var stopStreaming func()
	var queryOwner *unifiedservice.OwnerLifecycle
	var queryCleanup sync.Once
	shutdownQueryServices := func() {
		queryCleanup.Do(func() {
			if routedTransport != nil {
				routedTransport.Close()
			}
			if stopStreaming != nil {
				stopStreaming()
			} else if err := streamingService.Shutdown(); err != nil {
				log.Printf("failed to close streaming resources during shutdown: %v", err)
			}
			paginationService.Shutdown()
		})
	}
	if routing != nil {
		queryOwner = unifiedservice.NewOwnerLifecycle(cfg.Routing.DrainTimeout(), func(now time.Time) int {
			pending := paginationService.LiveSessionCount(now) + streamingService.LiveSessionCount(now)
			if routedTransport != nil {
				pending += routedTransport.LiveSessionCount()
			}
			return pending
		}, shutdownQueryServices)
		queryOwner.SetIdentity(routing.Slot(), routing.BootID())
	}
	defer func() {
		if queryOwner != nil {
			queryOwner.Shutdown()
		} else {
			shutdownQueryServices()
		}
	}()
	queryService := unifiedservice.NewUnifiedQueryServiceWithOwner(paginationService, streamingService, cfg, queryOwner, routing)
	metadataExtractor := metadata.NewMetadataExtractor(connPool, datasourceRepo)
	mcpServer := mcp.NewMCPServerWithTableAuthorizer(queryService, datasourceRepo, metadataExtractor, tableAuthorizer, translationManager)
	mcpServer.SetAuditRecorder(auditStore)
	mcpServer.SetTablePolicyManager(tablePolicyManager)

	if strings.TrimSpace(cfg.MCP.Transport) == config.MCPTransportStreamableHTTP {
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
		var handler http.Handler
		if routing == nil {
			handler = mcpServer.StreamableHTTPHandlerWithLimits(tokenValidator, rateLimiter, limits)
		} else {
			routedTransport, err = mcp.NewOwnerTransport(mcp.OwnerTransportOptions{
				Owner: routing, TTL: cfg.MCP.RoutedSessionTTL(), MaxSessions: cfg.MCP.RoutedSessionCapacity(),
				BeginInitialize: queryOwner.BeginInitialization,
				BindSession:     replicarouting.WithTransportSession,
				EndSession: func(sessionID string) {
					paginationService.EndTransportSession(sessionID)
					streamingService.EndTransportSession(sessionID)
				},
			})
			if err != nil {
				return fmt.Errorf("failed to initialize routed MCP sessions: %w", err)
			}
			handler = mcpServer.StreamableHTTPHandlerWithOwnerAdmin(
				tokenValidator, rateLimiter, limits, routedTransport, queryOwner, cfg.Routing.PolicyRevision, tablePolicyManager,
			)
		}
		return serveMCPHTTP(cfg, handler, streamingService, auditStore, queryOwner, func(stop func()) {
			stopStreaming = stop
		})
	}

	stopStreaming = startStreamingMaintenance(context.Background(), streamingService)
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

func observeMCPHTTP(handler http.Handler) http.Handler {
	router := gin.New()
	router.Use(middleware.AccessLog(log.Writer()), middleware.Recovery(log.Writer()), middleware.PrometheusMiddleware())
	for _, path := range []string{"/mcp", "/internal/replica", "/internal/replica/drain", "/metrics"} {
		router.Any(path, gin.WrapH(handler))
	}
	router.NoRoute(gin.WrapH(handler))
	return router
}

func serveMCPHTTP(cfg *config.Config, handler http.Handler, streamingService service.StreamingService, auditStore *auditlog.Store, queryOwner *unifiedservice.OwnerLifecycle, setMaintenanceStop func(func())) error {
	middleware.InitMetrics()
	handler = observeMCPHTTP(handler)
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
	if setMaintenanceStop != nil {
		setMaintenanceStop(stopStreaming)
	}
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
