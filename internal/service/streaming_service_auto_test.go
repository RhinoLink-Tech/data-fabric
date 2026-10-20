package service

import (
	"context"
	"errors"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

type streamingCapabilityRepository struct {
	repository.DataSourceRepository
	source *model.DataSource
	err    error
}

func (r streamingCapabilityRepository) GetByID(context.Context, string) (*model.DataSource, error) {
	return r.source, r.err
}

type streamingCapabilityChecker interface {
	SupportsStreaming(context.Context, *security.Caller, string) (bool, error)
}

func TestSupportsStreamingUsesDriverCapabilityAndAccessGates(t *testing.T) {
	_ = database.GetDriverRegistry()
	caller := &security.Caller{UserID: "alice", TenantID: "acme"}
	tests := []struct {
		name      string
		dbType    model.DatabaseType
		status    model.DataSourceStatus
		caller    *security.Caller
		want      bool
		wantError error
	}{
		{name: "mysql supports streaming", dbType: model.DatabaseTypeMySQL, status: model.DataSourceStatusActive, caller: caller, want: true},
		{name: "maxcompute does not support streaming", dbType: model.DatabaseTypeMaxCompute, status: model.DataSourceStatusActive, caller: caller, want: false},
		{name: "foreign tenant is denied before capability lookup", dbType: model.DatabaseTypeMySQL, status: model.DataSourceStatusActive, caller: &security.Caller{UserID: "eve", TenantID: "other"}, wantError: security.ErrDataSourceAccessDenied},
		{name: "inactive sources cannot be selected", dbType: model.DatabaseTypeMySQL, status: model.DataSourceStatusInactive, caller: caller, wantError: ErrDataSourceInactive},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := streamingCapabilityRepository{source: &model.DataSource{
				ID: "source-1", Type: tt.dbType, Status: tt.status, OwnerID: "alice", TenantID: "acme",
			}}
			streaming := NewStreamingService(repo, nil).(streamingCapabilityChecker)
			got, err := streaming.SupportsStreaming(context.Background(), tt.caller, "source-1")
			if tt.wantError != nil {
				if !errors.Is(err, tt.wantError) {
					t.Fatalf("SupportsStreaming() error = %v, want %v", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("SupportsStreaming() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("SupportsStreaming() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSupportsStreamingPropagatesMissingSource(t *testing.T) {
	streaming := NewStreamingService(streamingCapabilityRepository{err: repository.ErrDataSourceNotFound}, nil).(streamingCapabilityChecker)
	if _, err := streaming.SupportsStreaming(context.Background(), security.SystemCaller(), "missing"); !errors.Is(err, repository.ErrDataSourceNotFound) {
		t.Fatalf("SupportsStreaming() error = %v, want data source not found", err)
	}
}
