package security

import (
	"context"
	"errors"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/sqlanalysis"
)

func TestTableAuthorizerSeparatesReadAndMetadataPermissions(t *testing.T) {
	source := &model.DataSource{ID: "ds-1", Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Schema: "public"}}
	caller := &Caller{UserID: "alice", TenantID: "analytics"}
	authorizer := NewTableAuthorizer(TableAuthorizationConfig{
		Enabled: true,
		DataSources: map[string]DataSourceTablePolicy{
			"ds-1": {Rules: []TableAccessRule{{
				UserID:   "alice",
				Read:     []TableScope{{Schema: "public", Table: "orders"}},
				Metadata: []TableScope{{Schema: "public", Table: "customers"}},
			}}},
		},
	})
	orders := sqlanalysis.TableReference{Table: "orders", Unqualified: true}
	customers := sqlanalysis.TableReference{Schema: "public", Table: "customers"}

	if err := authorizer.AuthorizeReferences(context.Background(), caller, source, []sqlanalysis.TableReference{orders}, TablePermissionRead); err != nil {
		t.Fatalf("read grant rejected: %v", err)
	}
	if err := authorizer.AuthorizeReferences(context.Background(), caller, source, []sqlanalysis.TableReference{customers}, TablePermissionMetadata); err != nil {
		t.Fatalf("metadata grant rejected: %v", err)
	}
	if err := authorizer.AuthorizeReferences(context.Background(), caller, source, []sqlanalysis.TableReference{customers}, TablePermissionRead); !errors.Is(err, ErrTableAccessDenied) {
		t.Fatalf("metadata-only grant allowed query: %v", err)
	}
	if err := authorizer.AuthorizeReferences(context.Background(), caller, source, []sqlanalysis.TableReference{orders}, TablePermissionMetadata); !errors.Is(err, ErrTableAccessDenied) {
		t.Fatalf("read-only grant allowed metadata: %v", err)
	}
}

func TestTableAuthorizerDeniesByDefaultAndUnresolvedReferences(t *testing.T) {
	source := &model.DataSource{ID: "ds-1", Type: model.DatabaseTypePostgreSQL}
	caller := &Caller{UserID: "alice"}
	authorizer := NewTableAuthorizer(TableAuthorizationConfig{
		Enabled: true,
		DataSources: map[string]DataSourceTablePolicy{
			"ds-1": {Rules: []TableAccessRule{{UserID: "alice", Read: []TableScope{{Schema: "public", Table: "*"}}}}},
		},
	})

	if err := authorizer.AuthorizeReferences(context.Background(), caller, source, []sqlanalysis.TableReference{{Table: "orders", Unqualified: true}}, TablePermissionRead); !errors.Is(err, ErrTableAccessDenied) {
		t.Fatalf("unqualified table was not denied: %v", err)
	}
	if err := authorizer.AuthorizeReferences(context.Background(), caller, &model.DataSource{ID: "unknown"}, []sqlanalysis.TableReference{{Schema: "public", Table: "orders"}}, TablePermissionRead); !errors.Is(err, ErrTableAccessDenied) {
		t.Fatalf("missing datasource policy was not denied: %v", err)
	}
	if err := authorizer.AuthorizeAnalysis(context.Background(), caller, source, sqlanalysis.Analysis{
		Tables:           []sqlanalysis.TableReference{{Schema: "public", Table: "orders"}},
		UnresolvedTables: true,
	}, TablePermissionRead); !errors.Is(err, ErrTableAccessDenied) {
		t.Fatalf("unresolved source was not denied: %v", err)
	}
}

func TestTableAuthorizerExplicitWildcardsAndAdminOverride(t *testing.T) {
	source := &model.DataSource{ID: "ds-1"}
	admin := &Caller{UserID: "root", Roles: []string{AdminRole}}
	tables := []sqlanalysis.TableReference{{Catalog: "warehouse", Schema: "reports", Table: "daily"}}

	wildcard := NewTableAuthorizer(TableAuthorizationConfig{Enabled: true, DataSources: map[string]DataSourceTablePolicy{
		"ds-1": {Rules: []TableAccessRule{{UserID: "analyst", Read: []TableScope{{Catalog: "*", Schema: "*", Table: "daily"}}}}},
	}})
	if err := wildcard.AuthorizeReferences(context.Background(), &Caller{UserID: "analyst"}, source, []sqlanalysis.TableReference{{Table: "daily", Unqualified: true}}, TablePermissionRead); err != nil {
		t.Fatalf("explicit schema wildcard rejected unqualified table: %v", err)
	}
	if err := wildcard.AuthorizeReferences(context.Background(), admin, source, tables, TablePermissionRead); !errors.Is(err, ErrTableAccessDenied) {
		t.Fatalf("admin bypassed a policy without admin_override: %v", err)
	}

	override := NewTableAuthorizer(TableAuthorizationConfig{Enabled: true, DataSources: map[string]DataSourceTablePolicy{
		"ds-1": {AdminOverride: true},
	}})
	if err := override.AuthorizeReferences(context.Background(), admin, source, tables, TablePermissionMetadata); err != nil {
		t.Fatalf("configured admin override rejected: %v", err)
	}
}

func TestTableAuthorizationConfigValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		config  TableAuthorizationConfig
		wantErr bool
	}{
		{name: "disabled ignored", config: TableAuthorizationConfig{}},
		{name: "missing subject", config: TableAuthorizationConfig{Enabled: true, DataSources: map[string]DataSourceTablePolicy{"ds": {Rules: []TableAccessRule{{Read: []TableScope{{Table: "*"}}}}}}}, wantErr: true},
		{name: "missing table scope", config: TableAuthorizationConfig{Enabled: true, DataSources: map[string]DataSourceTablePolicy{"ds": {Rules: []TableAccessRule{{UserID: "alice", Read: []TableScope{{Schema: "public"}}}}}}}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.config.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestTableAuthorizerResolvesPrestoFamilyDefaults(t *testing.T) {
	for _, dbType := range []model.DatabaseType{model.DatabaseTypeTrino, model.DatabaseTypePresto, model.DatabaseTypeOpenLooKeng} {
		t.Run(string(dbType), func(t *testing.T) {
			source := &model.DataSource{
				ID: "query-engine", Type: dbType,
				Config: model.DataSourceConfig{Database: "lake", Schema: "analytics"},
			}
			authorizer := NewTableAuthorizer(TableAuthorizationConfig{
				Enabled: true,
				DataSources: map[string]DataSourceTablePolicy{
					source.ID: {Rules: []TableAccessRule{{
						UserID: "alice",
						Read:   []TableScope{{Catalog: "lake", Schema: "analytics", Table: "orders"}},
					}}},
				},
			})
			err := authorizer.AuthorizeReferences(context.Background(), &Caller{UserID: "alice"}, source, []sqlanalysis.TableReference{{
				Table: "orders", Unqualified: true,
			}}, TablePermissionRead)
			if err != nil {
				t.Fatalf("configured default catalog/schema were not applied: %v", err)
			}
		})
	}
}

func TestTableAuthorizerEnforcesTenantBoundary(t *testing.T) {
	source := &model.DataSource{ID: "tenant-shared-ds", Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Schema: "public"}}
	authorizer := NewTableAuthorizer(TableAuthorizationConfig{
		Enabled: true,
		DataSources: map[string]DataSourceTablePolicy{
			source.ID: {Rules: []TableAccessRule{{
				TenantID: "analytics", Read: []TableScope{{Schema: "public", Table: "orders"}},
			}}},
		},
	})
	table := []sqlanalysis.TableReference{{Table: "orders", Unqualified: true}}
	if err := authorizer.AuthorizeReferences(context.Background(), &Caller{UserID: "alice", TenantID: "analytics"}, source, table, TablePermissionRead); err != nil {
		t.Fatalf("matching tenant was denied: %v", err)
	}
	if err := authorizer.AuthorizeReferences(context.Background(), &Caller{UserID: "bob", TenantID: "finance"}, source, table, TablePermissionRead); !errors.Is(err, ErrTableAccessDenied) {
		t.Fatalf("cross-tenant caller was allowed: %v", err)
	}
}
