package service

import (
	"context"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/google/uuid"
)

// ScopedSourceManagement is the remote MCP write path. Its authorization is
// enforced by the mutation itself rather than by a preceding read.
type ScopedSourceManagement interface {
	RenameSourceScoped(context.Context, *security.Caller, string, string) (*repository.SourceMutationResult, error)
	SetSourceStatusScoped(context.Context, *security.Caller, string, model.DataSourceStatus) error
}

func (s *dataSourceService) atomicManagement(caller *security.Caller, id string) (repository.AtomicSourceManagement, repository.SourceWriteScope, error) {
	if caller == nil || caller.UserID == "" {
		return nil, repository.SourceWriteScope{}, security.ErrDataSourceAccessDenied
	}
	if _, err := uuid.Parse(id); err != nil {
		return nil, repository.SourceWriteScope{}, repository.ErrInvalidUUID
	}
	repo, ok := s.repo.(repository.AtomicSourceManagement)
	if !ok {
		return nil, repository.SourceWriteScope{}, security.ErrDataSourceAccessDenied
	}
	return repo, repository.SourceWriteScope{OwnerID: caller.UserID, TenantID: caller.TenantID, Admin: caller.IsAdmin()}, nil
}
func (s *dataSourceService) RenameSourceScoped(ctx context.Context, caller *security.Caller, id, name string) (*repository.SourceMutationResult, error) {
	repo, scope, err := s.atomicManagement(caller, id)
	if err != nil {
		return nil, err
	}
	return repo.RenameSourceScoped(ctx, scope, id, name)
}
func (s *dataSourceService) SetSourceStatusScoped(ctx context.Context, caller *security.Caller, id string, status model.DataSourceStatus) error {
	if status != model.DataSourceStatusActive && status != model.DataSourceStatusInactive {
		return ErrInvalidDataSourceConfig
	}
	repo, scope, err := s.atomicManagement(caller, id)
	if err != nil {
		return err
	}
	if err = repo.SetSourceStatusScoped(ctx, scope, id, status); err != nil {
		return err
	}
	if !repository.AfterSourceManagementCommit(ctx, func() { s.invalidatePool(id) }) {
		s.invalidatePool(id)
	}
	return nil
}

// ScopedSourceDeletion deletes only the named source and invalidates its pool.
type ScopedSourceDeletion interface {
	DeleteSourceScoped(context.Context, *security.Caller, string, string) error
}

func (s *dataSourceService) DeleteSourceScoped(ctx context.Context, caller *security.Caller, id, expectedName string) error {
	repo, scope, err := s.atomicManagement(caller, id)
	if err != nil {
		return err
	}
	if err = repo.DeleteSourceScoped(ctx, scope, id, expectedName); err != nil {
		return err
	}
	if !repository.AfterSourceManagementCommit(ctx, func() { s.invalidatePool(id) }) {
		s.invalidatePool(id)
	}
	return nil
}
