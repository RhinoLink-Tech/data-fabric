package service

import (
	"context"
	"fmt"
	"log"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"

	"github.com/google/uuid"
)

type DataSourceService interface {
	CreateDataSource(ctx context.Context, caller *security.Caller, req *CreateDataSourceRequest) (*model.DataSource, error)
	GetDataSource(ctx context.Context, caller *security.Caller, id string) (*model.DataSource, error)
	GetDataSourceByName(ctx context.Context, caller *security.Caller, name string) (*model.DataSource, error)
	ListDataSources(ctx context.Context, caller *security.Caller, req *ListDataSourcesRequest) (*ListDataSourcesResponse, error)
	UpdateDataSource(ctx context.Context, caller *security.Caller, id string, req *UpdateDataSourceRequest) (*model.DataSource, error)
	DeleteDataSource(ctx context.Context, caller *security.Caller, id string) error
	ActivateDataSource(ctx context.Context, caller *security.Caller, id string) error
	DeactivateDataSource(ctx context.Context, caller *security.Caller, id string) error
	GetDataSourceStats(ctx context.Context, caller *security.Caller) (*DataSourceStatsResponse, error)
}

// ConnectionInvalidator closes the pooled connections of a data source. The
// connection pool implements it; tests substitute a fake.
type ConnectionInvalidator interface {
	InvalidateDataSource(dataSourceID string) error
}

type DataSourceConfigValidator interface {
	IsSupportedDataSourceType(dbType model.DatabaseType) bool
	ValidateDataSourceConfiguration(config *model.DataSourceConfig, dbType model.DatabaseType) error
}

type dataSourceService struct {
	repo            repository.DataSourceRepository
	invalids        ConnectionInvalidator
	configValidator DataSourceConfigValidator
}

type CreateDataSourceRequest struct {
	Name   string                 `json:"name" validate:"required,min=1,max=255"`
	Type   model.DatabaseType     `json:"type" validate:"required"`
	Config model.DataSourceConfig `json:"config" validate:"required"`
}

type UpdateDataSourceRequest struct {
	Name   *string                 `json:"name,omitempty" validate:"omitempty,min=1,max=255"`
	Config *model.DataSourceConfig `json:"config,omitempty"`
	Status *model.DataSourceStatus `json:"status,omitempty"`
}

type ListDataSourcesRequest struct {
	Status model.DataSourceStatus `json:"status,omitempty"`
	Limit  int                    `json:"limit,omitempty" validate:"omitempty,min=1,max=100"`
	Offset int                    `json:"offset,omitempty" validate:"omitempty,min=0"`
}

type ListDataSourcesResponse struct {
	DataSources []*model.DataSource `json:"dataSources"`
	Total       int64               `json:"total"`
	Limit       int                 `json:"limit"`
	Offset      int                 `json:"offset"`
}

type DataSourceStatsResponse struct {
	Total    int64                            `json:"total"`
	ByStatus map[model.DataSourceStatus]int64 `json:"byStatus"`
}

// NewDataSourceService creates a new instance of DataSourceService.
func NewDataSourceService(repo repository.DataSourceRepository, invalidator ConnectionInvalidator, configValidator DataSourceConfigValidator) DataSourceService {
	return &dataSourceService{
		repo:            repo,
		invalids:        invalidator,
		configValidator: configValidator,
	}
}

// invalidatePool evicts the pooled connections of a data source. It is called
// after the repository write succeeded, so a cleanup failure must not fail
// the already-persisted operation — it is surfaced in the log instead, while
// the pool entry itself is dropped either way (#117).
func (s *dataSourceService) invalidatePool(dataSourceID string) {
	if s.invalids == nil {
		return
	}
	if err := s.invalids.InvalidateDataSource(dataSourceID); err != nil {
		log.Printf("data source %s updated but its pooled connections failed to close: %v", dataSourceID, err)
	}
}

func (s *dataSourceService) validateDataSourceConfig(config *model.DataSourceConfig, dbType model.DatabaseType) error {
	if s.configValidator == nil {
		return fmt.Errorf("%w: validator unavailable", ErrInvalidDataSourceConfig)
	}
	if !s.configValidator.IsSupportedDataSourceType(dbType) {
		return fmt.Errorf("%w: %s", ErrUnsupportedDataSourceType, dbType)
	}
	if err := s.configValidator.ValidateDataSourceConfiguration(config, dbType); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidDataSourceConfig, err)
	}
	return nil
}

func (s *dataSourceService) CreateDataSource(ctx context.Context, caller *security.Caller, req *CreateDataSourceRequest) (*model.DataSource, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: request is required", ErrInvalidDataSourceConfig)
	}
	config := req.Config
	if err := s.validateDataSourceConfig(&config, req.Type); err != nil {
		return nil, err
	}

	// Name uniqueness is per tenant (#9): only a same-name source in the
	// caller's bucket collides. Callers without a tenant claim share the
	// default bucket.
	tenantID := ""
	if caller != nil {
		tenantID = caller.TenantID
	}
	if existing, _ := s.repo.GetByName(ctx, tenantID, req.Name); existing != nil {
		return nil, repository.ErrDataSourceExists
	}

	// Create new data source with ownership stamped from the caller. Rows
	// created while authorization is disabled stay legacy (empty fields).
	dataSource := &model.DataSource{
		Name:   req.Name,
		Type:   req.Type,
		Config: config,
		Status: model.DataSourceStatusActive,
	}
	security.StampCaller(dataSource, caller)

	if err := s.repo.Create(ctx, dataSource); err != nil {
		return nil, fmt.Errorf("failed to create data source: %w", err)
	}

	return dataSource, nil
}

func (s *dataSourceService) GetDataSource(ctx context.Context, caller *security.Caller, id string) (*model.DataSource, error) {
	// Validate UUID format
	if _, err := uuid.Parse(id); err != nil {
		return nil, repository.ErrInvalidUUID
	}

	dataSource, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get data source: %w", err)
	}
	if !caller.CanRead(dataSource) {
		return nil, security.ErrDataSourceAccessDenied
	}

	return dataSource, nil
}

func (s *dataSourceService) GetDataSourceByName(ctx context.Context, caller *security.Caller, name string) (*model.DataSource, error) {
	if name == "" {
		return nil, fmt.Errorf("name cannot be empty")
	}

	tenantID := ""
	if caller != nil {
		tenantID = caller.TenantID
	}
	dataSource, err := s.repo.GetByName(ctx, tenantID, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get data source by name: %w", err)
	}
	if !caller.CanRead(dataSource) {
		return nil, security.ErrDataSourceAccessDenied
	}

	return dataSource, nil
}

func (s *dataSourceService) ListDataSources(ctx context.Context, caller *security.Caller, req *ListDataSourcesRequest) (*ListDataSourcesResponse, error) {
	// Set default values
	if req.Limit == 0 {
		req.Limit = 20
	}
	if req.Limit > 100 {
		req.Limit = 100
	}

	dataSources, total, err := s.repo.GetAll(ctx, repository.ScopeForCaller(caller), req.Status, req.Limit, req.Offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list data sources: %w", err)
	}

	return &ListDataSourcesResponse{
		DataSources: dataSources,
		Total:       total,
		Limit:       req.Limit,
		Offset:      req.Offset,
	}, nil
}

func preserveDataSourceSecrets(existing, incoming model.DataSourceConfig) model.DataSourceConfig {
	if incoming.Password == "" {
		incoming.Password = existing.Password
	}
	incoming.AdditionalProps = mergeAdditionalProperties(existing.AdditionalProps, incoming.AdditionalProps)
	return incoming
}

func mergeAdditionalProperties(existing, incoming map[string]interface{}) map[string]interface{} {
	if incoming == nil {
		return cloneAdditionalProperties(existing)
	}

	merged := cloneAdditionalProperties(incoming)
	for key, oldValue := range existing {
		newValue, exists := merged[key]
		if model.IsSensitiveDataSourceProperty(key) {
			if !exists {
				merged[key] = oldValue
			}
			continue
		}

		oldNested, oldIsMap := oldValue.(map[string]interface{})
		newNested, newIsMap := newValue.(map[string]interface{})
		if oldIsMap && newIsMap {
			merged[key] = mergeAdditionalProperties(oldNested, newNested)
		}
	}
	return merged
}

func cloneAdditionalProperties(source map[string]interface{}) map[string]interface{} {
	if source == nil {
		return nil
	}
	clone := make(map[string]interface{}, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func (s *dataSourceService) UpdateDataSource(ctx context.Context, caller *security.Caller, id string, req *UpdateDataSourceRequest) (*model.DataSource, error) {
	if req == nil {
		return nil, fmt.Errorf("%w: request is required", ErrInvalidDataSourceConfig)
	}

	// Validate UUID format
	if _, err := uuid.Parse(id); err != nil {
		return nil, repository.ErrInvalidUUID
	}

	// Get existing data source
	dataSource, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get data source: %w", err)
	}

	// Only the owner or an administrator may change a stored data source.
	// Legacy rows are admin-only because their original owner is unknown.
	if !caller.CanWrite(dataSource) {
		return nil, security.ErrDataSourceAccessDenied
	}

	// Update fields if provided
	if req.Name != nil {
		dataSource.Name = *req.Name
	}
	if req.Config != nil {
		dataSource.Config = preserveDataSourceSecrets(dataSource.Config, *req.Config)
	}
	if req.Status != nil {
		dataSource.Status = *req.Status
	}

	if err := s.validateDataSourceConfig(&dataSource.Config, dataSource.Type); err != nil {
		return nil, err
	}

	if err := s.repo.Update(ctx, dataSource); err != nil {
		return nil, fmt.Errorf("failed to update data source: %w", err)
	}

	// Connection-relevant changes (config, status) must not keep serving from
	// a pool built with the old settings (#117). Name-only updates don't
	// affect connections.
	if req.Config != nil || req.Status != nil {
		s.invalidatePool(id)
	}

	return dataSource, nil
}

// authorizeWrite resolves the record and enforces the #9 write gate shared by
// delete/activate/deactivate. Legacy rows are accessible only to administrators.
func (s *dataSourceService) authorizeWrite(ctx context.Context, caller *security.Caller, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return repository.ErrInvalidUUID
	}
	dataSource, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("failed to get data source: %w", err)
	}
	if !caller.CanWrite(dataSource) {
		return security.ErrDataSourceAccessDenied
	}
	return nil
}

func (s *dataSourceService) DeleteDataSource(ctx context.Context, caller *security.Caller, id string) error {
	if err := s.authorizeWrite(ctx, caller, id); err != nil {
		return err
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("failed to delete data source: %w", err)
	}

	// A deleted source's connections must be closed, not just unreachable
	// through the registry (#117).
	s.invalidatePool(id)

	return nil
}

func (s *dataSourceService) ActivateDataSource(ctx context.Context, caller *security.Caller, id string) error {
	if err := s.authorizeWrite(ctx, caller, id); err != nil {
		return err
	}

	if err := s.repo.Activate(ctx, id); err != nil {
		return fmt.Errorf("failed to activate data source: %w", err)
	}

	// Connections pooled before deactivation may predate configuration fixes
	// made while the source was down; start clean (#117).
	s.invalidatePool(id)

	return nil
}

func (s *dataSourceService) DeactivateDataSource(ctx context.Context, caller *security.Caller, id string) error {
	if err := s.authorizeWrite(ctx, caller, id); err != nil {
		return err
	}

	if err := s.repo.Deactivate(ctx, id); err != nil {
		return fmt.Errorf("failed to deactivate data source: %w", err)
	}

	// Deactivation means the source must stop consuming database connections
	// immediately, not just reject new queries (#117).
	s.invalidatePool(id)

	return nil
}

func (s *dataSourceService) GetDataSourceStats(ctx context.Context, caller *security.Caller) (*DataSourceStatsResponse, error) {
	counts, err := s.repo.CountByStatus(ctx, repository.ScopeForCaller(caller))
	if err != nil {
		return nil, fmt.Errorf("failed to get data source stats: %w", err)
	}

	// Calculate total
	total := int64(0)
	for _, count := range counts {
		total += count
	}

	return &DataSourceStatsResponse{
		Total:    total,
		ByStatus: counts,
	}, nil
}
