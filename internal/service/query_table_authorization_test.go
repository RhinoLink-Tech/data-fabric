package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/sqlanalysis"
	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type fixedSQLASTAnalyzer struct {
	analysis sqlanalysis.Analysis
	err      error
}

func (a fixedSQLASTAnalyzer) AnalyzeSQLContext(context.Context, string, string) (sqlanalysis.Analysis, error) {
	return a.analysis, a.err
}

func TestValidateAndAuthorizeSQLRejectsAnyUnpermittedTable(t *testing.T) {
	source := &model.DataSource{
		ID: "ds-1", Type: model.DatabaseTypePostgreSQL,
		Config: model.DataSourceConfig{Schema: "public"},
	}
	authorizer := security.NewTableAuthorizer(security.TableAuthorizationConfig{
		Enabled: true,
		DataSources: map[string]security.DataSourceTablePolicy{
			"ds-1": {Rules: []security.TableAccessRule{{
				UserID: "alice", Read: []security.TableScope{{Schema: "public", Table: "orders"}},
			}}},
		},
	})
	service := NewQueryServiceWithTableAuthorizer(nil, nil, false, authorizer, newTestSQLTranslationManager()).(*queryService)
	service.sqlValidator.SetASTAnalyzer(fixedSQLASTAnalyzer{analysis: sqlanalysis.Analysis{
		StatementCount: 1, IsQuery: true,
		Tables: []sqlanalysis.TableReference{
			{Schema: "public", Table: "orders"},
			{Schema: "private", Table: "payroll"},
		},
	}})
	_, err := service.validateAndAuthorizeSQL(context.Background(), &security.Caller{UserID: "alice"}, source, "SELECT 1", security.TablePermissionRead)
	if !errors.Is(err, security.ErrTableAccessDenied) {
		t.Fatalf("validateAndAuthorizeSQL() error = %v, want ErrTableAccessDenied", err)
	}
}

func TestValidateAndAuthorizeSQLRejectsDeniedColumnBeforeExecution(t *testing.T) {
	source := &model.DataSource{ID: "ds-1", Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Schema: "public"}}
	authorizer := security.NewTableAuthorizer(security.TableAuthorizationConfig{Enabled: true, DataSources: map[string]security.DataSourceTablePolicy{
		"ds-1": {Rules: []security.TableAccessRule{{UserID: "alice", Read: []security.TableScope{{Schema: "public", Table: "employees", Columns: []string{"id"}}}}}},
	}})
	service := NewQueryServiceWithTableAuthorizer(nil, nil, false, authorizer, newTestSQLTranslationManager()).(*queryService)
	service.sqlValidator.SetASTAnalyzer(fixedSQLASTAnalyzer{analysis: sqlanalysis.Analysis{
		StatementCount: 1, IsQuery: true,
		Tables:  []sqlanalysis.TableReference{{Schema: "public", Table: "employees"}},
		Columns: []sqlanalysis.ColumnReference{{Schema: "public", Table: "employees", Column: "salary"}},
	}})
	_, err := service.validateAndAuthorizeSQL(context.Background(), &security.Caller{UserID: "alice"}, source, "SELECT salary FROM public.employees", security.TablePermissionRead)
	if !errors.Is(err, security.ErrTableAccessDenied) {
		t.Fatalf("validateAndAuthorizeSQL() error = %v, want denied column before execution", err)
	}
}

func TestValidateAndAuthorizeSQLFailsClosedWithoutAST(t *testing.T) {
	source := &model.DataSource{ID: "ds-1", Type: model.DatabaseTypePostgreSQL}
	authorizer := security.NewTableAuthorizer(security.TableAuthorizationConfig{Enabled: true})
	service := NewQueryServiceWithTableAuthorizer(nil, nil, false, authorizer, sql_translator.NewSQLTranslationManager(false)).(*queryService)
	_, err := service.validateAndAuthorizeSQL(context.Background(), &security.Caller{UserID: "alice"}, source, "SELECT 1", security.TablePermissionRead)
	if !errors.Is(err, security.ErrTableAuthorizationUnavailable) {
		t.Fatalf("validateAndAuthorizeSQL() error = %v, want ErrTableAuthorizationUnavailable", err)
	}
}

func TestFetchNextBatchRechecksTableAuthorization(t *testing.T) {
	source := &model.DataSource{ID: "ds-1", Type: model.DatabaseTypePostgreSQL, OwnerID: "alice", Status: model.DataSourceStatusActive}
	authorizer := security.NewTableAuthorizer(security.TableAuthorizationConfig{Enabled: true})
	service := NewQueryServiceWithTableAuthorizer(
		federationSourceRepository{source: source},
		database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false),
		false,
		authorizer,
		newTestSQLTranslationManager(),
	).(*queryService)
	service.sessionManager.Store(&QuerySession{
		QueryID: "query-1", Slug: "slug-1", Token: "token-1", creator: querySessionPrincipal{userID: "alice"},
		DataSourceID: "ds-1", SQL: "SELECT * FROM public.orders",
		Tables:    []sqlanalysis.TableReference{{Schema: "public", Table: "orders"}},
		TotalRows: 2, FetchedRows: 1, ExpiresAt: time.Now().Add(time.Hour),
	})

	_, err := service.FetchNextBatch(context.Background(), &security.Caller{UserID: "alice"}, "query-1", "slug-1", "token-1", 1)
	if !errors.Is(err, security.ErrTableAccessDenied) {
		t.Fatalf("FetchNextBatch() error = %v, want ErrTableAccessDenied", err)
	}
}

func TestExecuteStreamQueryRejectsUnauthorizedTableBeforeConnection(t *testing.T) {
	source := &model.DataSource{
		ID: "ds-1", Type: model.DatabaseTypePostgreSQL, OwnerID: "alice",
		Status: model.DataSourceStatusActive,
	}
	authorizer := security.NewTableAuthorizer(security.TableAuthorizationConfig{Enabled: true})
	service := NewStreamingServiceWithTableAuthorizer(
		federationSourceRepository{source: source},
		nil,
		authorizer,
		newTestSQLTranslationManager(),
	).(*streamingService)
	service.sqlValidator.SetASTAnalyzer(fixedSQLASTAnalyzer{analysis: sqlanalysis.Analysis{
		StatementCount: 1, IsQuery: true,
		Tables: []sqlanalysis.TableReference{{Schema: "private", Table: "payroll"}},
	}})

	_, _, err := service.ExecuteStreamQuery(context.Background(), &security.Caller{UserID: "alice"}, &model.QueryRequest{
		DataSourceID: source.ID,
		SQL:          "SELECT * FROM private.payroll",
	})
	if !errors.Is(err, security.ErrTableAccessDenied) {
		t.Fatalf("ExecuteStreamQuery() error = %v, want ErrTableAccessDenied before opening a connection", err)
	}
}

func TestFetchNextBatchRechecksColumnPolicy(t *testing.T) {
	source := &model.DataSource{ID: "ds-1", Type: model.DatabaseTypePostgreSQL, OwnerID: "alice", Status: model.DataSourceStatusActive}
	authorizer := security.NewTableAuthorizer(security.TableAuthorizationConfig{Enabled: true, DataSources: map[string]security.DataSourceTablePolicy{
		"ds-1": {Rules: []security.TableAccessRule{{UserID: "alice", Read: []security.TableScope{{Schema: "public", Table: "orders", Columns: []string{"id"}}}}}},
	}})
	service := NewQueryServiceWithTableAuthorizer(
		federationSourceRepository{source: source},
		database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false),
		false,
		authorizer,
		newTestSQLTranslationManager(),
	).(*queryService)
	service.sqlValidator.SetASTAnalyzer(fixedSQLASTAnalyzer{analysis: sqlanalysis.Analysis{
		StatementCount: 1, IsQuery: true,
		Tables:  []sqlanalysis.TableReference{{Schema: "public", Table: "orders"}},
		Columns: []sqlanalysis.ColumnReference{{Schema: "public", Table: "orders", Column: "secret"}},
	}})
	service.sessionManager.Store(&QuerySession{
		QueryID: "column-query", Slug: "column-slug", Token: "column-token", creator: querySessionPrincipal{userID: "alice"},
		DataSourceID: "ds-1", SQL: "SELECT secret FROM public.orders",
		Tables:    []sqlanalysis.TableReference{{Schema: "public", Table: "orders"}},
		TotalRows: 1, FetchedRows: 1, ExpiresAt: time.Now().Add(time.Hour),
	})
	_, err := service.FetchNextBatch(context.Background(), &security.Caller{UserID: "alice"}, "column-query", "column-slug", "column-token", 1)
	if !errors.Is(err, security.ErrTableAccessDenied) {
		t.Fatalf("FetchNextBatch() error = %v, want current column policy denial", err)
	}
}

func TestFetchNextBatchWithStreamingRechecksColumnPolicy(t *testing.T) {
	source := &model.DataSource{ID: "ds-1", Type: model.DatabaseTypePostgreSQL, OwnerID: "alice", Status: model.DataSourceStatusActive}
	authorizer := security.NewTableAuthorizer(security.TableAuthorizationConfig{Enabled: true, DataSources: map[string]security.DataSourceTablePolicy{
		"ds-1": {Rules: []security.TableAccessRule{{UserID: "alice", Read: []security.TableScope{{Schema: "public", Table: "orders", Columns: []string{"id"}}}}}},
	}})
	manager := NewStreamingSessionManager()
	manager.Store(&StreamingSession{
		QueryID: "stream-column-query", Slug: "stream-column-slug", Token: "stream-column-token", creator: querySessionPrincipal{userID: "alice"},
		DataSourceID: "ds-1", SQL: "SELECT secret FROM public.orders",
		Tables:    []sqlanalysis.TableReference{{Schema: "public", Table: "orders"}},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	service := &streamingService{
		datasourceRepo:          federationSourceRepository{source: source},
		activeStreams:           make(map[string]*ActiveStream),
		streamingSessionManager: manager,
		tableAuthorizer:         authorizer,
		sqlValidator:            security.NewSQLValidator(1000),
	}
	service.sqlValidator.SetASTAnalyzer(fixedSQLASTAnalyzer{analysis: sqlanalysis.Analysis{
		StatementCount: 1, IsQuery: true,
		Tables:  []sqlanalysis.TableReference{{Schema: "public", Table: "orders"}},
		Columns: []sqlanalysis.ColumnReference{{Schema: "public", Table: "orders", Column: "secret"}},
	}})
	_, err := service.FetchNextBatchWithStreaming(context.Background(), &security.Caller{UserID: "alice"}, "stream-column-query", "stream-column-slug", "stream-column-token", 1)
	if !errors.Is(err, security.ErrTableAccessDenied) {
		t.Fatalf("FetchNextBatchWithStreaming() error = %v, want current column policy denial", err)
	}
}

func TestFetchNextBatchWithStreamingRechecksTableAuthorization(t *testing.T) {
	source := &model.DataSource{ID: "ds-1", Type: model.DatabaseTypePostgreSQL, OwnerID: "alice", Status: model.DataSourceStatusActive}
	authorizer := security.NewTableAuthorizer(security.TableAuthorizationConfig{Enabled: true})
	manager := NewStreamingSessionManager()
	manager.Store(&StreamingSession{
		QueryID: "stream-query", Slug: "stream-slug", Token: "stream-token", creator: querySessionPrincipal{userID: "alice"},
		DataSourceID: "ds-1", StreamID: "stream-id",
		Tables:    []sqlanalysis.TableReference{{Schema: "public", Table: "orders"}},
		ExpiresAt: time.Now().Add(time.Hour),
	})
	service := &streamingService{
		datasourceRepo:          federationSourceRepository{source: source},
		activeStreams:           make(map[string]*ActiveStream),
		streamingSessionManager: manager,
		tableAuthorizer:         authorizer,
	}

	_, err := service.FetchNextBatchWithStreaming(context.Background(), &security.Caller{UserID: "alice"}, "stream-query", "stream-slug", "stream-token", 1)
	if !errors.Is(err, security.ErrTableAccessDenied) {
		t.Fatalf("FetchNextBatchWithStreaming() error = %v, want ErrTableAccessDenied", err)
	}
}

func TestValidateAndAuthorizeSQLRejectsParserFailure(t *testing.T) {
	source := &model.DataSource{ID: "ds-1", Type: model.DatabaseTypePostgreSQL}
	authorizer := security.NewTableAuthorizer(security.TableAuthorizationConfig{Enabled: true})
	service := NewQueryServiceWithTableAuthorizer(nil, nil, false, authorizer, newTestSQLTranslationManager()).(*queryService)
	service.sqlValidator.SetASTAnalyzer(fixedSQLASTAnalyzer{err: errors.New("parser rejected the statement")})

	_, err := service.validateAndAuthorizeSQL(context.Background(), &security.Caller{UserID: "alice"}, source, "SELECT * FROM ?", security.TablePermissionRead)
	if !errors.Is(err, security.ErrSQLSyntaxError) {
		t.Fatalf("validateAndAuthorizeSQL() error = %v, want ErrSQLSyntaxError", err)
	}
}
func TestFetchNextBatchRevokesAndDeletesSessionAfterPolicyPublication(t *testing.T) {
	dsn := "file:service-policy-revoke-" + uuid.NewString() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	source := &model.DataSource{
		ID: "ds-1", Type: model.DatabaseTypePostgreSQL, OwnerID: "alice",
		Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{Schema: "public"},
	}
	initial := security.TableAuthorizationConfig{Enabled: true, DataSources: map[string]security.DataSourceTablePolicy{
		"ds-1": {Rules: []security.TableAccessRule{{UserID: "alice", Read: []security.TableScope{{Schema: "public", Table: "orders"}}}}},
	}}
	authorizer := security.NewTableAuthorizer(initial)
	manager, err := security.NewTablePolicyManager(db, authorizer, initial, "")
	if err != nil {
		t.Fatal(err)
	}
	service := NewQueryServiceWithTableAuthorizer(
		federationSourceRepository{source: source},
		database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false),
		false,
		authorizer,
		newTestSQLTranslationManager(),
	).(*queryService)
	service.sessionManager.Store(&QuerySession{
		QueryID: "revoked-query", Slug: "revoked-slug", Token: "revoked-token",
		creator: querySessionPrincipal{userID: "alice"}, DataSourceID: source.ID,
		SQL:       "SELECT id FROM public.orders",
		Tables:    []sqlanalysis.TableReference{{Schema: "public", Table: "orders"}},
		TotalRows: 10, FetchedRows: 1, ExpiresAt: time.Now().Add(time.Hour),
	})

	if _, err := service.sessionManager.Get("revoked-query"); err != nil {
		t.Fatalf("continuation session was not seeded: %v", err)
	}
	_, err = manager.Publish(context.Background(), 1, "policy-admin", security.TableAuthorizationConfig{Enabled: true})
	if err != nil {
		t.Fatalf("publish revocation: %v", err)
	}
	ctx := authorizer.PinRequest(context.Background())
	_, err = service.FetchNextBatch(ctx, &security.Caller{UserID: "alice"}, "revoked-query", "revoked-slug", "revoked-token", 1)
	if !errors.Is(err, security.ErrTableAccessDenied) {
		t.Fatalf("continuation after revocation error = %v", err)
	}
	if _, err := service.sessionManager.Get("revoked-query"); err == nil {
		t.Fatal("revoked continuation session was retained")
	}
}
