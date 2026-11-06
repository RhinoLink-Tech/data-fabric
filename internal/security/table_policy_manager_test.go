package security

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/sqlanalysis"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTablePolicyPublicationPropagatesAndPinsRequests(t *testing.T) {
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared"
	db1 := openTablePolicyTestDB(t, dsn)
	db2 := openTablePolicyTestDB(t, dsn)
	initial := testManagedTablePolicy(true)
	authorizer1 := NewTableAuthorizer(initial)
	manager1, err := newTablePolicyManager(db1, authorizer1, initial, "test", 10*time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("create first policy manager: %v", err)
	}
	authorizer2 := NewTableAuthorizer(TableAuthorizationConfig{Enabled: true})
	manager2, err := newTablePolicyManager(db2, authorizer2, TableAuthorizationConfig{Enabled: true}, "test", 10*time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("create second policy manager: %v", err)
	}
	manager2.Start(context.Background())
	defer manager2.Stop()

	source := &model.DataSource{ID: "sales", Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Schema: "public"}}
	table := []sqlanalysis.TableReference{{Schema: "public", Table: "orders"}}
	allowedCaller := &Caller{UserID: "alice", TenantID: "tenant-a"}
	crossTenantCaller := &Caller{UserID: "alice", TenantID: "tenant-b"}

	if err := authorizer1.AuthorizeReferences(context.Background(), allowedCaller, source, table, TablePermissionRead); err != nil {
		t.Fatalf("initial grant rejected: %v", err)
	}
	if err := authorizer1.AuthorizeReferences(context.Background(), crossTenantCaller, source, table, TablePermissionRead); !errors.Is(err, ErrTableAccessDenied) {
		t.Fatalf("cross-tenant access was not denied: %v", err)
	}

	inFlight1 := authorizer1.PinRequest(context.Background())
	inFlight2 := authorizer2.PinRequest(context.Background())
	result, err := manager1.Publish(context.Background(), 1, "admin-a", testManagedTablePolicy(false))
	if err != nil {
		t.Fatalf("publish revocation: %v", err)
	}
	if result.Revision != "2" || result.Outcome != "activated" || !result.AuditRecorded {
		t.Fatalf("unexpected publication result: %+v", result)
	}

	// Requests that began before publication finish against their pinned version.
	for _, request := range []struct {
		name       string
		authorizer *TableAuthorizer
		ctx        context.Context
	}{{"publisher", authorizer1, inFlight1}, {"peer", authorizer2, inFlight2}} {
		if err := request.authorizer.AuthorizeReferences(request.ctx, allowedCaller, source, table, TablePermissionRead); err != nil {
			t.Errorf("%s request lost its pinned grant: %v", request.name, err)
		}
	}
	// The publishing replica rejects new work immediately after activation.
	if err := authorizer1.AuthorizeReferences(authorizer1.PinRequest(context.Background()), allowedCaller, source, table, TablePermissionRead); !errors.Is(err, ErrTableAccessDenied) {
		t.Fatalf("new request on publisher was not revoked: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for manager2.Status().Revision != "2" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if status := manager2.Status(); status.Revision != "2" || !status.Fresh {
		t.Fatalf("peer did not receive fresh revision 2: %+v", status)
	}
	// Snapshot captured on the peer before its poll remains consistent; the
	// next request uses the new revision and sees the revocation.
	if err := authorizer2.AuthorizeReferences(inFlight2, allowedCaller, source, table, TablePermissionRead); err != nil {
		t.Fatalf("peer request mixed in the newly published version: %v", err)
	}
	if err := authorizer2.AuthorizeReferences(authorizer2.PinRequest(context.Background()), allowedCaller, source, table, TablePermissionRead); !errors.Is(err, ErrTableAccessDenied) {
		t.Fatalf("peer did not apply revocation: %v", err)
	}
}

func TestInvalidPolicyPublicationRetainsActiveVersionAndRecordsOutcome(t *testing.T) {
	db := openTablePolicyTestDB(t, "file:"+uuid.NewString()+"?mode=memory&cache=shared")
	initial := testManagedTablePolicy(true)
	authorizer := NewTableAuthorizer(initial)
	manager, err := newTablePolicyManager(db, authorizer, initial, "", 10*time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("create policy manager: %v", err)
	}
	invalid := TableAuthorizationConfig{
		Enabled: true,
		DataSources: map[string]DataSourceTablePolicy{
			"sales": {Rules: []TableAccessRule{{UserID: "alice", Read: []TableScope{{}}}}},
		},
	}
	result, err := manager.Publish(context.Background(), 1, "admin-b", invalid)
	if !errors.Is(err, ErrTablePolicyInvalid) {
		t.Fatalf("invalid update error = %v", err)
	}
	if !result.AuditRecorded || result.Outcome != "rejected" {
		t.Fatalf("invalid update outcome was not recorded: %+v", result)
	}
	if status := manager.Status(); status.Revision != "1" || !status.Fresh {
		t.Fatalf("invalid update replaced the active revision: %+v", status)
	}
	publications, err := manager.Publications(context.Background(), 10)
	if err != nil {
		t.Fatalf("read publication history: %v", err)
	}
	if len(publications) < 2 || publications[0].Outcome != "rejected" || publications[0].PublishedBy != "admin-b" {
		t.Fatalf("invalid publication is missing from history: %+v", publications)
	}
	source := &model.DataSource{ID: "sales", Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Schema: "public"}}
	if err := authorizer.AuthorizeReferences(context.Background(), &Caller{UserID: "alice", TenantID: "tenant-a"}, source,
		[]sqlanalysis.TableReference{{Schema: "public", Table: "orders"}}, TablePermissionRead); err != nil {
		t.Fatalf("last valid policy was not retained: %v", err)
	}
}

func TestStaleTablePolicyReplicaFailsClosed(t *testing.T) {
	db := openTablePolicyTestDB(t, "file:"+uuid.NewString()+"?mode=memory&cache=shared")
	policy := testManagedTablePolicy(true)
	authorizer := NewTableAuthorizer(policy)
	_, err := newTablePolicyManager(db, authorizer, policy, "", 10*time.Millisecond, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("create policy manager: %v", err)
	}
	authorizer.lastSyncNS.Store(time.Now().Add(-time.Second).UnixNano())
	ctx := authorizer.PinRequest(context.Background())
	source := &model.DataSource{ID: "sales", Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Schema: "public"}}
	err = authorizer.AuthorizeReferences(ctx, &Caller{UserID: "alice", TenantID: "tenant-a"}, source,
		[]sqlanalysis.TableReference{{Schema: "public", Table: "orders"}}, TablePermissionRead)
	if !errors.Is(err, ErrTablePolicyUnavailable) {
		t.Fatalf("stale policy did not fail closed: %v", err)
	}
}

func openTablePolicyTestDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite policy db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func testManagedTablePolicy(grant bool) TableAuthorizationConfig {
	policy := TableAuthorizationConfig{Enabled: true, DataSources: map[string]DataSourceTablePolicy{}}
	if grant {
		policy.DataSources["sales"] = DataSourceTablePolicy{Rules: []TableAccessRule{{
			UserID: "alice", TenantID: "tenant-a",
			Read: []TableScope{{Schema: "public", Table: "orders"}},
		}}}
	}
	return policy
}
func TestManagedTablePolicySurvivesDisabledNodeStartupConfig(t *testing.T) {
	db := openTablePolicyTestDB(t, "file:"+uuid.NewString()+"?mode=memory&cache=shared")
	initial := testManagedTablePolicy(true)
	manager, err := NewTablePolicyManager(db, NewTableAuthorizer(initial), initial, "routing-v1")
	if err != nil {
		t.Fatalf("initialize policy manager: %v", err)
	}
	if _, err := manager.Publish(context.Background(), 1, "admin", testManagedTablePolicy(false)); err != nil {
		t.Fatalf("publish revision 2: %v", err)
	}

	disabledAuthorizer := NewTableAuthorizer(TableAuthorizationConfig{})
	reloaded, err := NewTablePolicyManager(db, disabledAuthorizer, TableAuthorizationConfig{}, "")
	if err != nil {
		t.Fatalf("load managed policy with disabled local YAML: %v", err)
	}
	if reloaded == nil || !disabledAuthorizer.Enabled() || reloaded.Status().Revision != "2" {
		t.Fatalf("managed policy was disabled by node YAML: manager=%v status=%+v", reloaded != nil, reloaded.Status())
	}

	invalidLocalPolicy := TableAuthorizationConfig{Enabled: true, DataSources: map[string]DataSourceTablePolicy{
		"broken": {Rules: []TableAccessRule{{UserID: "alice", Read: []TableScope{{}}}}},
	}}
	invalidLocalAuthorizer := NewTableAuthorizer(invalidLocalPolicy)
	loaded, err := NewTablePolicyManager(db, invalidLocalAuthorizer, invalidLocalPolicy, "stale-local-config")
	if err != nil {
		t.Fatalf("managed DB head should supersede invalid local YAML: %v", err)
	}
	if loaded == nil || loaded.Status().Revision != "2" || invalidLocalAuthorizer.current.Load().SourceLabel != "routing-v1" {
		t.Fatalf("invalid local YAML replaced or blocked managed head: manager=%v status=%+v", loaded != nil, loaded.Status())
	}
}

func TestPublishedPolicyPreservesExplicitEmptyColumnAllowlist(t *testing.T) {
	db := openTablePolicyTestDB(t, "file:"+uuid.NewString()+"?mode=memory&cache=shared")
	initial := testManagedTablePolicy(true)
	authorizer := NewTableAuthorizer(initial)
	manager, err := newTablePolicyManager(db, authorizer, initial, "", 10*time.Millisecond, time.Second)
	if err != nil {
		t.Fatalf("create policy manager: %v", err)
	}
	policy := TableAuthorizationConfig{
		Enabled: true,
		DataSources: map[string]DataSourceTablePolicy{
			"sales": {Rules: []TableAccessRule{{
				UserID: "alice", TenantID: "tenant-a",
				Read: []TableScope{{Schema: "public", Table: "orders", Columns: []string{}}},
			}}},
		},
	}
	if _, err := manager.Publish(context.Background(), 1, "admin", policy); err != nil {
		t.Fatalf("publish empty column allowlist: %v", err)
	}
	historical, err := manager.GetRevision(context.Background(), 2)
	if err != nil {
		t.Fatalf("read published revision: %v", err)
	}
	columns := historical.Config.DataSources["sales"].Rules[0].Read[0].Columns
	if columns == nil || len(columns) != 0 {
		t.Fatalf("published empty allowlist was not preserved: %#v", columns)
	}
	analysis := sqlanalysis.Analysis{
		Tables:  []sqlanalysis.TableReference{{Schema: "public", Table: "orders"}},
		Columns: []sqlanalysis.ColumnReference{{Schema: "public", Table: "orders", Column: "id"}},
	}
	source := &model.DataSource{ID: "sales", Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Schema: "public"}}
	caller := &Caller{UserID: "alice", TenantID: "tenant-a"}
	if err := authorizer.AuthorizeAnalysis(authorizer.PinRequest(context.Background()), caller, source, analysis, TablePermissionRead); !errors.Is(err, ErrTableAccessDenied) {
		t.Fatalf("empty allowlist granted a column after publication: %v", err)
	}
}
