package repository

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Uses only an explicitly supplied disposable MySQL database. CI may omit it.
func TestScopedManagementMySQLConcurrentContract(t *testing.T) {
	dsn := os.Getenv("DF_ATOMIC_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("DF_ATOMIC_TEST_MYSQL_DSN not supplied")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	err = db.Exec("CREATE TABLE data_sources (id varchar(36) primary key, name varchar(255), type varchar(80), config json not null, status varchar(20), owner_id varchar(255) not null default '', tenant_id varchar(255) not null default '', created_at datetime(6), updated_at datetime(6), UNIQUE KEY uniq_tenant_name (tenant_id, name)) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci").Error
	if err != nil {
		t.Fatalf("isolated test table required: %v", err)
	}
	t.Cleanup(func() { db.Exec("DROP TABLE data_sources") })
	repo, err := NewDataSourceRepository(db, vaultForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source := &model.DataSource{Name: "orders", Type: model.DatabaseTypeMySQL, OwnerID: "alice", TenantID: "a", Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{Host: "db", Port: 3306, Database: "d", Username: "u", Password: "password", AdditionalProps: map[string]interface{}{"access_token": "old"}}}
	if err = repo.Create(ctx, source); err != nil {
		t.Fatal(err)
	}
	scope := SourceWriteScope{OwnerID: "alice", TenantID: "a"}
	mutator := repo.(AtomicSourceManagement)
	tokens := repo.(interface {
		CompareAndSwapAccessToken(context.Context, string, string, string, time.Time) (bool, error)
	})
	for i := 0; i < 20; i++ {
		// Restore the round, then race three independent operations on one row.
		current, err := repo.GetByID(ctx, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		current.Name = "orders"
		current.Status = model.DataSourceStatusActive
		current.Config.AdditionalProps["access_token"] = "old"
		if err = repo.Update(ctx, current); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		errs := make(chan error, 3)
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			<-start
			_, e := mutator.RenameSourceScoped(ctx, scope, source.ID, "renamed")
			errs <- e
		}()
		go func() {
			defer wg.Done()
			<-start
			errs <- mutator.SetSourceStatusScoped(ctx, scope, source.ID, model.DataSourceStatusInactive)
		}()
		go func() {
			defer wg.Done()
			<-start
			ok, e := tokens.CompareAndSwapAccessToken(ctx, source.ID, "old", "new", time.Now().Add(time.Hour))
			if e == nil && !ok {
				e = errTokenNotUpdated{}
			}
			errs <- e
		}()
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		got, err := repo.GetByID(ctx, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "renamed" || got.Status != model.DataSourceStatusInactive || got.Config.Password != "password" || got.Config.AdditionalProps["access_token"] != "new" {
			t.Fatalf("concurrent mutation lost data, round %d", i)
		}
	}
	if err := mutator.SetSourceStatusScoped(ctx, scope, source.ID, model.DataSourceStatusInactive); err != nil {
		t.Fatalf("idempotent status: %v", err)
	}
	if _, err := mutator.RenameSourceScoped(ctx, SourceWriteScope{OwnerID: "alice", TenantID: "other"}, source.ID, "forbidden"); !errors.Is(err, ErrDataSourceNotFound) {
		t.Fatalf("cross tenant: %v", err)
	}
	// MySQL identity columns may use a case/accent-insensitive collation;
	// exact authentication claims must not inherit those comparison rules.
	for _, other := range []SourceWriteScope{
		{OwnerID: "ALICE", TenantID: "a"},
		{OwnerID: "alice ", TenantID: "a"},
		{OwnerID: "alice", TenantID: "A"},
		{OwnerID: "alice", TenantID: "a "},
	} {
		if _, err := mutator.RenameSourceScoped(ctx, other, source.ID, "forbidden"); !errors.Is(err, ErrDataSourceNotFound) {
			t.Fatalf("non-exact identity accepted: %+v: %v", other, err)
		}
		if err := mutator.SetSourceStatusScoped(ctx, other, source.ID, model.DataSourceStatusActive); !errors.Is(err, ErrDataSourceNotFound) {
			t.Fatalf("non-exact identity accepted: %+v: %v", other, err)
		}
	}
	unicodeSource := &model.DataSource{Name: "unicode-source", Type: model.DatabaseTypeMySQL, OwnerID: "\u00e9", TenantID: "\u00e9", Config: model.DataSourceConfig{Host: "db", Port: 3306}}
	if err = repo.Create(ctx, unicodeSource); err != nil {
		t.Fatal(err)
	}
	for _, other := range []SourceWriteScope{{OwnerID: "e", TenantID: "\u00e9"}, {OwnerID: "\u00e9", TenantID: "e"}, {OwnerID: "e\u0301", TenantID: "\u00e9"}} {
		if _, err := mutator.RenameSourceScoped(ctx, other, unicodeSource.ID, "forbidden"); !errors.Is(err, ErrDataSourceNotFound) {
			t.Fatalf("unicode identity accepted: %+v: %v", other, err)
		}
	}
	second := &model.DataSource{Name: "collision", Type: model.DatabaseTypeMySQL, OwnerID: "alice", TenantID: "a", Config: model.DataSourceConfig{Host: "db", Port: 3306}}
	if err = repo.Create(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err = mutator.RenameSourceScoped(ctx, scope, source.ID, "collision"); !errors.Is(err, ErrDataSourceExists) {
		t.Fatalf("unique collision: %v", err)
	}
	for _, tc := range []struct {
		who  SourceWriteScope
		name string
	}{
		{SourceWriteScope{OwnerID: "ALICE", TenantID: "a"}, "renamed"},
		{SourceWriteScope{OwnerID: "alice", TenantID: "A"}, "renamed"},
		{scope, "RENAMED"}, {scope, "renamed "},
		{scope, "orders"},
	} {
		if err = mutator.DeleteSourceScoped(ctx, tc.who, source.ID, tc.name); !errors.Is(err, ErrDataSourceNotFound) {
			t.Fatalf("nonexact delete accepted: %v", err)
		}
	}
	if err = mutator.DeleteSourceScoped(ctx, scope, source.ID, "renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.GetByID(ctx, source.ID); !errors.Is(err, ErrDataSourceNotFound) {
		t.Fatal("deleted source remained")
	}
}

type errTokenNotUpdated struct{}

func (errTokenNotUpdated) Error() string { return "token update lost compare-and-swap" }
