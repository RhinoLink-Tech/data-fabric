package service

import (
	"context"
	"errors"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

// #9 service-level authorization: every management operation resolves the
// record and applies the caller gates before touching the repository. The
// stub records the scope and writes it observed so tests can assert what the
// service asked the persistence layer for.

type authzRepositoryStub struct {
	repository.DataSourceRepository

	existing      *model.DataSource
	byName        map[string]*model.DataSource // "tenant\x00name" -> source
	created       []*model.DataSource
	updated       []*model.DataSource
	deleted       []string
	statusChanged []string
	getAllScope   *repository.AccessScope
	countScope    *repository.AccessScope
}

func authzFixture() *model.DataSource {
	return &model.DataSource{
		ID: "11111111-1111-4111-8111-111111111111", Name: "warehouse", Type: model.DatabaseTypeMySQL,
		Status: model.DataSourceStatusActive, OwnerID: "alice", TenantID: "acme",
		Config: model.DataSourceConfig{Host: "db", Port: 3306, Database: "a", Username: "u"},
	}
}

func (r *authzRepositoryStub) GetByID(context.Context, string) (*model.DataSource, error) {
	if r.existing == nil {
		return nil, repository.ErrDataSourceNotFound
	}
	// Hand every call a copy so subtests stay independent.
	copy := *r.existing
	return &copy, nil
}

func (r *authzRepositoryStub) GetByName(_ context.Context, tenantID, name string) (*model.DataSource, error) {
	if source, ok := r.byName[tenantID+"\x00"+name]; ok {
		return source, nil
	}
	return nil, repository.ErrDataSourceNotFound
}

func (r *authzRepositoryStub) Create(_ context.Context, ds *model.DataSource) error {
	r.created = append(r.created, ds)
	return nil
}

func (r *authzRepositoryStub) Update(_ context.Context, ds *model.DataSource) error {
	r.updated = append(r.updated, ds)
	return nil
}

func (r *authzRepositoryStub) Delete(_ context.Context, id string) error {
	r.deleted = append(r.deleted, id)
	return nil
}

func (r *authzRepositoryStub) Activate(_ context.Context, id string) error {
	r.statusChanged = append(r.statusChanged, id)
	return nil
}

func (r *authzRepositoryStub) Deactivate(_ context.Context, id string) error {
	r.statusChanged = append(r.statusChanged, id)
	return nil
}

func (r *authzRepositoryStub) GetAll(_ context.Context, scope *repository.AccessScope, _ model.DataSourceStatus, _, _ int) ([]*model.DataSource, int64, error) {
	r.getAllScope = scope
	return nil, 0, nil
}

func (r *authzRepositoryStub) CountByStatus(_ context.Context, scope *repository.AccessScope) (map[model.DataSourceStatus]int64, error) {
	r.countScope = scope
	return map[model.DataSourceStatus]int64{}, nil
}

func newAuthzService(repo repository.DataSourceRepository) DataSourceService {
	return NewDataSourceService(repo, nil, &datasourceConfigValidatorStub{supported: true})
}

func TestCreateDataSourceStampsCallerOwnership(t *testing.T) {
	ctx := context.Background()

	t.Run("authenticated create stamps owner and tenant", func(t *testing.T) {
		repo := &authzRepositoryStub{}
		svc := newAuthzService(repo)
		created, err := svc.CreateDataSource(ctx, &security.Caller{UserID: "alice", TenantID: "acme"}, &CreateDataSourceRequest{
			Name: "warehouse", Type: model.DatabaseTypeMySQL,
			Config: model.DataSourceConfig{Host: "db", Port: 3306},
		})
		if err != nil {
			t.Fatal(err)
		}
		if created.OwnerID != "alice" || created.TenantID != "acme" {
			t.Fatalf("ownership = %q/%q, want alice/acme", created.OwnerID, created.TenantID)
		}
	})

	t.Run("authorization-disabled create stays legacy", func(t *testing.T) {
		repo := &authzRepositoryStub{}
		svc := newAuthzService(repo)
		created, err := svc.CreateDataSource(ctx, nil, &CreateDataSourceRequest{
			Name: "warehouse", Type: model.DatabaseTypeMySQL,
			Config: model.DataSourceConfig{Host: "db", Port: 3306},
		})
		if err != nil {
			t.Fatal(err)
		}
		if !security.IsLegacyDataSource(created) {
			t.Fatalf("disabled-mode create = %+v, want legacy row", created)
		}
	})

	t.Run("name collision is checked within the caller's tenant bucket", func(t *testing.T) {
		repo := &authzRepositoryStub{byName: map[string]*model.DataSource{
			"contoso\x00warehouse": authzFixture(),
		}}
		svc := newAuthzService(repo)
		if _, err := svc.CreateDataSource(ctx, &security.Caller{UserID: "alice", TenantID: "acme"}, &CreateDataSourceRequest{
			Name: "warehouse", Type: model.DatabaseTypeMySQL,
			Config: model.DataSourceConfig{Host: "db", Port: 3306},
		}); err != nil {
			t.Fatalf("same name in another tenant rejected: %v", err)
		}

		repo.byName["acme\x00warehouse"] = authzFixture()
		if _, err := svc.CreateDataSource(ctx, &security.Caller{UserID: "alice", TenantID: "acme"}, &CreateDataSourceRequest{
			Name: "warehouse", Type: model.DatabaseTypeMySQL,
			Config: model.DataSourceConfig{Host: "db", Port: 3306},
		}); !errors.Is(err, repository.ErrDataSourceExists) {
			t.Fatalf("duplicate name within tenant error = %v, want ErrDataSourceExists", err)
		}
	})
}

func TestGetDataSourceEnforcesReadGate(t *testing.T) {
	ctx := context.Background()
	id := authzFixture().ID

	tests := []struct {
		name   string
		caller *security.Caller
		wantOK bool
	}{
		{"owner", &security.Caller{UserID: "alice"}, true},
		{"tenant member", &security.Caller{UserID: "bob", TenantID: "acme"}, true},
		{"admin", &security.Caller{UserID: "root", Roles: []string{security.AdminRole}}, true},
		{"foreign user", &security.Caller{UserID: "eve", TenantID: "contoso"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newAuthzService(&authzRepositoryStub{existing: authzFixture()})
			_, err := svc.GetDataSource(ctx, tt.caller, id)
			if tt.wantOK && err != nil {
				t.Fatalf("GetDataSource() rejected an authorized caller: %v", err)
			}
			if !tt.wantOK && !errors.Is(err, security.ErrDataSourceAccessDenied) {
				t.Fatalf("GetDataSource() error = %v, want ErrDataSourceAccessDenied", err)
			}
		})
	}

	legacy := authzFixture()
	legacy.OwnerID, legacy.TenantID = "", ""
	svc := newAuthzService(&authzRepositoryStub{existing: legacy})
	if _, err := svc.GetDataSource(ctx, &security.Caller{UserID: "eve"}, id); !errors.Is(err, security.ErrDataSourceAccessDenied) {
		t.Fatalf("legacy source read error = %v, want ErrDataSourceAccessDenied", err)
	}
}

func TestUpdateDataSourceWriteGate(t *testing.T) {
	ctx := context.Background()
	id := authzFixture().ID
	newName := "renamed"

	t.Run("owner updates", func(t *testing.T) {
		repo := &authzRepositoryStub{existing: authzFixture()}
		svc := newAuthzService(repo)
		if _, err := svc.UpdateDataSource(ctx, &security.Caller{UserID: "alice"}, id, &UpdateDataSourceRequest{Name: &newName}); err != nil {
			t.Fatal(err)
		}
		if len(repo.updated) != 1 || repo.updated[0].Name != "renamed" {
			t.Fatalf("updates = %+v", repo.updated)
		}
	})

	t.Run("tenant member cannot update", func(t *testing.T) {
		repo := &authzRepositoryStub{existing: authzFixture()}
		svc := newAuthzService(repo)
		_, err := svc.UpdateDataSource(ctx, &security.Caller{UserID: "bob", TenantID: "acme"}, id, &UpdateDataSourceRequest{Name: &newName})
		if !errors.Is(err, security.ErrDataSourceAccessDenied) {
			t.Fatalf("error = %v, want ErrDataSourceAccessDenied", err)
		}
		if len(repo.updated) != 0 {
			t.Fatal("denied update reached the repository")
		}
	})

	legacy := authzFixture()
	legacy.OwnerID, legacy.TenantID = "", ""

	t.Run("non-admin cannot update legacy row", func(t *testing.T) {
		repo := &authzRepositoryStub{existing: legacy}
		svc := newAuthzService(repo)
		_, err := svc.UpdateDataSource(ctx, &security.Caller{UserID: "bob", TenantID: "acme"}, id, &UpdateDataSourceRequest{Name: &newName})
		if !errors.Is(err, security.ErrDataSourceAccessDenied) {
			t.Fatalf("error = %v, want ErrDataSourceAccessDenied", err)
		}
		if len(repo.updated) != 0 {
			t.Fatal("denied legacy update reached the repository")
		}
	})

	t.Run("admin updates legacy without claiming it", func(t *testing.T) {
		repo := &authzRepositoryStub{existing: legacy}
		svc := newAuthzService(repo)
		admin := &security.Caller{UserID: "root", Roles: []string{security.AdminRole}}
		if _, err := svc.UpdateDataSource(ctx, admin, id, &UpdateDataSourceRequest{Name: &newName}); err != nil {
			t.Fatal(err)
		}
		if got := repo.updated[0]; got.OwnerID != "" || got.TenantID != "" {
			t.Fatalf("admin update claimed the row: %+v", got)
		}
	})

}

func TestDataSourceMutationsEnforceWriteGate(t *testing.T) {
	ctx := context.Background()
	id := authzFixture().ID
	legacy := authzFixture()
	legacy.OwnerID, legacy.TenantID = "", ""

	mutations := []struct {
		name string
		call func(DataSourceService, *security.Caller) error
	}{
		{"delete", func(s DataSourceService, c *security.Caller) error { return s.DeleteDataSource(ctx, c, id) }},
		{"activate", func(s DataSourceService, c *security.Caller) error { return s.ActivateDataSource(ctx, c, id) }},
		{"deactivate", func(s DataSourceService, c *security.Caller) error { return s.DeactivateDataSource(ctx, c, id) }},
	}
	callers := []struct {
		name     string
		caller   *security.Caller
		existing *model.DataSource
		wantErr  error
	}{
		{"owner", &security.Caller{UserID: "alice"}, authzFixture(), nil},
		{"admin", &security.Caller{UserID: "root", Roles: []string{security.AdminRole}}, authzFixture(), nil},
		{"tenant member", &security.Caller{UserID: "bob", TenantID: "acme"}, authzFixture(), security.ErrDataSourceAccessDenied},
		{"legacy row non-admin", &security.Caller{UserID: "bob", TenantID: "acme"}, legacy, security.ErrDataSourceAccessDenied},
	}
	for _, mutation := range mutations {
		for _, tt := range callers {
			t.Run(mutation.name+"/"+tt.name, func(t *testing.T) {
				repo := &authzRepositoryStub{existing: tt.existing}
				svc := newAuthzService(repo)
				err := mutation.call(svc, tt.caller)
				if tt.wantErr == nil && err != nil {
					t.Fatalf("authorized mutation rejected: %v", err)
				}
				if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
					t.Fatalf("error = %v, want %v", err, tt.wantErr)
				}
				denied := tt.wantErr != nil
				if denied && (len(repo.deleted) != 0 || len(repo.statusChanged) != 0) {
					t.Fatal("denied mutation reached the repository")
				}
				if !denied && len(repo.deleted) == 0 && len(repo.statusChanged) == 0 {
					t.Fatal("authorized mutation did not reach the repository")
				}
			})
		}
	}
}

func TestListAndStatsDeriveScopeFromCaller(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		caller    *security.Caller
		wantScope *repository.AccessScope
	}{
		{"admin unrestricted", &security.Caller{UserID: "root", Roles: []string{security.AdminRole}}, nil},
		{"authorization disabled unrestricted", nil, nil},
		{"regular caller scoped", &security.Caller{UserID: "alice", TenantID: "acme"}, &repository.AccessScope{OwnerID: "alice", TenantID: "acme"}},
		{"tenantless caller scoped", &security.Caller{UserID: "bob"}, &repository.AccessScope{OwnerID: "bob"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &authzRepositoryStub{}
			svc := newAuthzService(repo)

			if _, err := svc.ListDataSources(ctx, tt.caller, &ListDataSourcesRequest{}); err != nil {
				t.Fatal(err)
			}
			if !sameScope(repo.getAllScope, tt.wantScope) {
				t.Fatalf("list scope = %+v, want %+v", repo.getAllScope, tt.wantScope)
			}

			if _, err := svc.GetDataSourceStats(ctx, tt.caller); err != nil {
				t.Fatal(err)
			}
			if !sameScope(repo.countScope, tt.wantScope) {
				t.Fatalf("stats scope = %+v, want %+v", repo.countScope, tt.wantScope)
			}
		})
	}
}

func sameScope(got, want *repository.AccessScope) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}
	return *got == *want
}
