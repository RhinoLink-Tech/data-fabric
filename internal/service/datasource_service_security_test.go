package service

import (
	"context"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
)

type updateDataSourceRepositoryStub struct {
	repository.DataSourceRepository
	dataSource *model.DataSource
	updated    *model.DataSource
}

func (r *updateDataSourceRepositoryStub) GetByID(context.Context, string) (*model.DataSource, error) {
	return r.dataSource, nil
}
func (r *updateDataSourceRepositoryStub) Update(_ context.Context, dataSource *model.DataSource) error {
	r.updated = dataSource
	return nil
}

func TestUpdateDataSourcePreservesOmittedSecrets(t *testing.T) {
	id := "e8c3cfb8-21dd-4f29-a197-7a616e0165ba"
	for _, tt := range []struct {
		name            string
		newPassword     string
		wantPassword    string
		wantAccessToken string
	}{
		{name: "omitted password preserves existing", wantPassword: "old-password", wantAccessToken: "old-token"},
		{name: "provided password replaces existing", newPassword: "new-password", wantPassword: "new-password", wantAccessToken: "old-token"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			existing := &model.DataSource{
				ID:   id,
				Type: model.DatabaseTypeTrino,
				Config: model.DataSourceConfig{
					Host: "old-host", Database: "lakehouse", Username: "gateway", SSL: true,
					Password: "old-password",
					AdditionalProps: map[string]interface{}{
						"source":       "old-source",
						"access_token": "old-token",
						"auth": map[string]interface{}{
							"client_secret": "old-client-secret",
							"region":        "old-region",
						},
					},
				},
			}
			repo := &updateDataSourceRepositoryStub{dataSource: existing}
			svc := NewDataSourceService(repo, nil, database.NewHealthChecker(nil))
			req := &UpdateDataSourceRequest{
				Config: &model.DataSourceConfig{
					Host: "new-host", Database: "lakehouse", Username: "gateway", SSL: true,
					Password: tt.newPassword,
					AdditionalProps: map[string]interface{}{
						"source": "new-source",
						"auth": map[string]interface{}{
							"region": "new-region",
						},
					},
				},
			}

			updated, err := svc.UpdateDataSource(context.Background(), nil, id, req)
			if err != nil {
				t.Fatalf("UpdateDataSource() error = %v", err)
			}
			if updated.Config.Password != tt.wantPassword {
				t.Errorf("password = %q, want %q", updated.Config.Password, tt.wantPassword)
			}
			if got := updated.Config.AdditionalProps["access_token"]; got != tt.wantAccessToken {
				t.Errorf("access token = %v, want %q", got, tt.wantAccessToken)
			}
			auth, ok := updated.Config.AdditionalProps["auth"].(map[string]interface{})
			if !ok {
				t.Fatalf("auth property has type %T", updated.Config.AdditionalProps["auth"])
			}
			if got := auth["client_secret"]; got != "old-client-secret" {
				t.Errorf("client secret = %v, want preserved value", got)
			}
			if got := auth["region"]; got != "new-region" {
				t.Errorf("region = %v, want new-region", got)
			}
			if got := updated.Config.AdditionalProps["source"]; got != "new-source" {
				t.Errorf("source = %v, want new-source", got)
			}
		})
	}
}
