package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"nexus-gateway/internal/model"
	"nexus-gateway/internal/repository"
)

type DataSourceService interface {
	CreateDataSource(ctx context.Context, req *CreateDataSourceRequest) (*model.DataSource, error)
	GetDataSource(ctx context.Context, id string) (*model.DataSource, error)
	GetDataSourceByName(ctx context.Context, name string) (*model.DataSource, error)
	ListDataSources(ctx context.Context, req *ListDataSourcesRequest) (*ListDataSourcesResponse, error)
	UpdateDataSource(ctx context.Context, id string, req *UpdateDataSourceRequest) (*model.DataSource, error)
	DeleteDataSource(ctx context.Context, id string) error
	ActivateDataSource(ctx context.Context, id string) error
	DeactivateDataSource(ctx context.Context, id string) error
	GetDataSourceStats(ctx context.Context) (*DataSourceStatsResponse, error)
}

type dataSourceService struct {
	repo repository.DataSourceRepository
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

// NewDataSourceService creates a new instance of DataSourceService
func NewDataSourceService(repo repository.DataSourceRepository) DataSourceService {
	return &dataSourceService{
		repo: repo,
	}
}

func (s *dataSourceService) CreateDataSource(ctx context.Context, req *CreateDataSourceRequest) (*model.DataSource, error) {
	// Check if data source with the same name already exists
	if existing, _ := s.repo.GetByName(ctx, req.Name); existing != nil {
		return nil, repository.ErrDataSourceExists
	}

	// Create new data source
	dataSource := &model.DataSource{
		Name:   req.Name,
		Type:   req.Type,
		Config: req.Config,
		Status: model.DataSourceStatusActive,
	}

	if err := s.repo.Create(ctx, dataSource); err != nil {
		return nil, fmt.Errorf("failed to create data source: %w", err)
	}

	return dataSource, nil
}

func (s *dataSourceService) GetDataSource(ctx context.Context, id string) (*model.DataSource, error) {
	// Validate UUID format
	if _, err := uuid.Parse(id); err != nil {
		return nil, repository.ErrInvalidUUID
	}

	dataSource, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get data source: %w", err)
	}

	return dataSource, nil
}

func (s *dataSourceService) GetDataSourceByName(ctx context.Context, name string) (*model.DataSource, error) {
	if name == "" {
		return nil, fmt.Errorf("name cannot be empty")
	}

	dataSource, err := s.repo.GetByName(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to get data source by name: %w", err)
	}

	return dataSource, nil
}

func (s *dataSourceService) ListDataSources(ctx context.Context, req *ListDataSourcesRequest) (*ListDataSourcesResponse, error) {
	// Set default values
	if req.Limit == 0 {
		req.Limit = 20
	}
	if req.Limit > 100 {
		req.Limit = 100
	}

	dataSources, total, err := s.repo.GetAll(ctx, req.Status, req.Limit, req.Offset)
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

func (s *dataSourceService) UpdateDataSource(ctx context.Context, id string, req *UpdateDataSourceRequest) (*model.DataSource, error) {
	// Validate UUID format
	if _, err := uuid.Parse(id); err != nil {
		return nil, repository.ErrInvalidUUID
	}

	// Get existing data source
	dataSource, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get data source: %w", err)
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

	if err := s.repo.Update(ctx, dataSource); err != nil {
		return nil, fmt.Errorf("failed to update data source: %w", err)
	}

	return dataSource, nil
}

func (s *dataSourceService) DeleteDataSource(ctx context.Context, id string) error {
	// Validate UUID format
	if _, err := uuid.Parse(id); err != nil {
		return repository.ErrInvalidUUID
	}

	if err := s.repo.Delete(ctx, id); err != nil {
		return fmt.Errorf("failed to delete data source: %w", err)
	}

	return nil
}

func (s *dataSourceService) ActivateDataSource(ctx context.Context, id string) error {
	// Validate UUID format
	if _, err := uuid.Parse(id); err != nil {
		return repository.ErrInvalidUUID
	}

	if err := s.repo.Activate(ctx, id); err != nil {
		return fmt.Errorf("failed to activate data source: %w", err)
	}

	return nil
}

func (s *dataSourceService) DeactivateDataSource(ctx context.Context, id string) error {
	// Validate UUID format
	if _, err := uuid.Parse(id); err != nil {
		return repository.ErrInvalidUUID
	}

	if err := s.repo.Deactivate(ctx, id); err != nil {
		return fmt.Errorf("failed to deactivate data source: %w", err)
	}

	return nil
}

func (s *dataSourceService) GetDataSourceStats(ctx context.Context) (*DataSourceStatsResponse, error) {
	counts, err := s.repo.CountByStatus(ctx)
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
