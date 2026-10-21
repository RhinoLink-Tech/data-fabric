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
		QueryID: "query-1", Slug: "slug-1", Token: "token-1", CreatorID: "alice",
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

func TestFetchNextBatchWithStreamingRechecksTableAuthorization(t *testing.T) {
	source := &model.DataSource{ID: "ds-1", Type: model.DatabaseTypePostgreSQL, OwnerID: "alice", Status: model.DataSourceStatusActive}
	authorizer := security.NewTableAuthorizer(security.TableAuthorizationConfig{Enabled: true})
	manager := NewStreamingSessionManager()
	manager.Store(&StreamingSession{
		QueryID: "stream-query", Slug: "stream-slug", Token: "stream-token", CreatorID: "alice",
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
