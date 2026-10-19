package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
)

type dataSourceLookupStub struct {
	dataSource  *model.DataSource
	err         error
	requestedID string
}

func (s *dataSourceLookupStub) GetByID(_ context.Context, id string) (*model.DataSource, error) {
	s.requestedID = id
	return s.dataSource, s.err
}

type metadataConnectionProviderStub struct {
	dataSource *model.DataSource
	err        error
	calls      int
}

func (s *metadataConnectionProviderStub) GetConnection(_ context.Context, dataSource *model.DataSource) (*sql.DB, error) {
	s.calls++
	s.dataSource = dataSource
	return nil, s.err
}

func TestExtractSchemaLoadsRequestedDataSource(t *testing.T) {
	connectionErr := errors.New("connection refused")
	source := &model.DataSource{
		ID: "stored-source-id", Type: model.DatabaseTypePostgreSQL,
		Config: model.DataSourceConfig{Host: "pg.internal", Port: 5432, Database: "analytics", Username: "reader"},
	}
	sources := &dataSourceLookupStub{dataSource: source}
	pool := &metadataConnectionProviderStub{err: connectionErr}
	extractor := &MetadataExtractor{
		connPool: pool,
		sources:  sources,
		registry: database.NewDriverRegistry(),
	}

	_, _, err := extractor.ExtractSchema(context.Background(), "requested-source-id")
	if !errors.Is(err, connectionErr) {
		t.Fatalf("ExtractSchema() error = %v, want wrapped connection error", err)
	}
	if sources.requestedID != "requested-source-id" {
		t.Fatalf("repository was asked for %q, want requested ID", sources.requestedID)
	}
	if pool.calls != 1 || pool.dataSource != source {
		t.Fatalf("connection source = %#v, calls = %d; want loaded data source", pool.dataSource, pool.calls)
	}
	if pool.dataSource.ID != "stored-source-id" || pool.dataSource.Type != model.DatabaseTypePostgreSQL || pool.dataSource.Config.Host != "pg.internal" {
		t.Fatalf("connection pool received placeholder source: %+v", pool.dataSource)
	}
}

func TestExtractSchemaReturnsNotFoundFromRepository(t *testing.T) {
	sources := &dataSourceLookupStub{err: fmt.Errorf("lookup failed: %w", repository.ErrDataSourceNotFound)}
	pool := &metadataConnectionProviderStub{}
	extractor := &MetadataExtractor{sources: sources, connPool: pool, registry: database.NewDriverRegistry()}

	_, _, err := extractor.ExtractSchema(context.Background(), "missing-source")
	if !errors.Is(err, repository.ErrDataSourceNotFound) {
		t.Fatalf("ExtractSchema() error = %v, want repository.ErrDataSourceNotFound", err)
	}
	if pool.calls != 0 {
		t.Fatalf("connection pool called %d times for a missing source", pool.calls)
	}
}

func TestExtractSchemaRejectsUnsupportedSourceBeforeOpeningConnection(t *testing.T) {
	sources := &dataSourceLookupStub{dataSource: &model.DataSource{
		ID: "unknown-source", Type: model.DatabaseType("not_registered"),
	}}
	pool := &metadataConnectionProviderStub{}
	extractor := &MetadataExtractor{sources: sources, connPool: pool, registry: database.NewDriverRegistry()}

	_, _, err := extractor.ExtractSchema(context.Background(), sources.dataSource.ID)
	if !errors.Is(err, ErrSchemaDiscoveryUnsupported) {
		t.Fatalf("ExtractSchema() error = %v, want ErrSchemaDiscoveryUnsupported", err)
	}
	if pool.calls != 0 {
		t.Fatalf("connection pool called %d times for unsupported source", pool.calls)
	}
}

func TestExtractSchemaRequiresRepository(t *testing.T) {
	extractor := &MetadataExtractor{registry: database.NewDriverRegistry()}
	_, _, err := extractor.ExtractSchema(context.Background(), "source-id")
	if !errors.Is(err, ErrMetadataRepositoryRequired) {
		t.Fatalf("ExtractSchema() error = %v, want ErrMetadataRepositoryRequired", err)
	}
}

func TestExtractSchemaRequiresConnectionPool(t *testing.T) {
	sources := &dataSourceLookupStub{dataSource: &model.DataSource{
		ID: "source-id", Type: model.DatabaseTypePostgreSQL,
	}}
	extractor := &MetadataExtractor{sources: sources, registry: database.NewDriverRegistry()}
	_, _, err := extractor.ExtractSchema(context.Background(), "source-id")
	if !errors.Is(err, ErrMetadataConnectionPoolRequired) {
		t.Fatalf("ExtractSchema() error = %v, want ErrMetadataConnectionPoolRequired", err)
	}
}

func TestExtractSchemaTreatsEmptyRepositoryResultAsNotFound(t *testing.T) {
	sources := &dataSourceLookupStub{}
	pool := &metadataConnectionProviderStub{}
	extractor := &MetadataExtractor{sources: sources, connPool: pool, registry: database.NewDriverRegistry()}
	_, _, err := extractor.ExtractSchema(context.Background(), "missing-source")
	if !errors.Is(err, repository.ErrDataSourceNotFound) {
		t.Fatalf("ExtractSchema() error = %v, want repository.ErrDataSourceNotFound", err)
	}
	if pool.calls != 0 {
		t.Fatalf("connection pool called %d times for an empty repository result", pool.calls)
	}
}
