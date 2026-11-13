package service

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Only run on an explicitly provided disposable MySQL schema.
func TestRESTUpdateMySQLConcurrentContract(t *testing.T) {
	dsn := os.Getenv("DF_ATOMIC_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("DF_ATOMIC_TEST_MYSQL_DSN not supplied")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE data_sources (id varchar(36) primary key, name varchar(255), type varchar(80), config json not null, status varchar(20), owner_id varchar(255) not null default '', tenant_id varchar(255) not null default '', created_at datetime(6), updated_at datetime(6), UNIQUE KEY uniq_tenant_name (tenant_id, name))").Error; err != nil {
		t.Fatalf("isolated test table required: %v", err)
	}
	t.Cleanup(func() { db.Exec("DROP TABLE data_sources") })
	vault, err := security.NewCredentialVaultWithKeyring("key", map[string][]byte{"key": []byte(strings.Repeat("a", 32))})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := repository.NewDataSourceRepository(db, vault)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source := &model.DataSource{Name: "orders", Type: model.DatabaseTypeMySQL, OwnerID: "alice", TenantID: "a", Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{Host: "db", Port: 3306, Database: "orders", Username: "reader", Password: "password", AdditionalProps: map[string]interface{}{"access_token": "old"}}}
	if err = repo.Create(ctx, source); err != nil {
		t.Fatal(err)
	}
	svc := NewDataSourceService(repo, nil, &datasourceConfigValidatorStub{supported: true})
	mutator := repo.(repository.AtomicSourceManagement)
	tokens := repo.(interface {
		CompareAndSwapAccessToken(context.Context, string, string, string, time.Time) (bool, error)
	})
	for i := 0; i < 20; i++ {
		current, e := repo.GetByID(ctx, source.ID)
		if e != nil {
			t.Fatal(e)
		}
		current.Name = "orders"
		current.Status = model.DataSourceStatusActive
		current.Config.Host = "db"
		current.Config.AdditionalProps["access_token"] = "old"
		if e = repo.Update(ctx, current); e != nil {
			t.Fatal(e)
		}
		start := make(chan struct{})
		errs := make(chan error, 3)
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			<-start
			_, e := svc.UpdateDataSource(ctx, &security.Caller{UserID: "alice", TenantID: "a"}, source.ID, &UpdateDataSourceRequest{Config: &model.DataSourceConfig{Host: "new-host", Port: 3306, Database: "orders", Username: "reader"}})
			errs <- e
		}()
		go func() {
			defer wg.Done()
			<-start
			errs <- mutator.SetSourceStatusScoped(ctx, repository.SourceWriteScope{OwnerID: "alice", TenantID: "a"}, source.ID, model.DataSourceStatusInactive)
		}()
		go func() {
			defer wg.Done()
			<-start
			ok, e := tokens.CompareAndSwapAccessToken(ctx, source.ID, "old", "new", time.Now().Add(time.Hour))
			if e == nil && !ok {
				e = tokenRotationFailed{}
			}
			errs <- e
		}()
		close(start)
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		got, e := repo.GetByID(ctx, source.ID)
		if e != nil {
			t.Fatal(e)
		}
		if got.Status != model.DataSourceStatusInactive || got.Config.Host != "new-host" || got.Config.Password != "password" || got.Config.AdditionalProps["access_token"] != "new" {
			t.Fatalf("concurrent REST update lost data at round %d", i)
		}
	}
}

type tokenRotationFailed struct{}

func (tokenRotationFailed) Error() string { return "token rotation did not succeed" }

// Observes the actual database lock: token rotation cannot finish until a
// config update's authorized merge commits, then must preserve both changes.
type pauseLockedUpdate struct {
	repository.DataSourceRepository
	locked  chan struct{}
	release chan struct{}
}

func (r *pauseLockedUpdate) UpdateSourceLocked(ctx context.Context, id string, fields []string, fn func(*model.DataSource) error) (*model.DataSource, error) {
	return r.DataSourceRepository.(repository.LockedSourceUpdater).UpdateSourceLocked(ctx, id, fields, func(ds *model.DataSource) error {
		close(r.locked)
		<-r.release
		return fn(ds)
	})
}
func TestRESTConfigLocksMySQLTokenRotation(t *testing.T) {
	dsn := os.Getenv("DF_ATOMIC_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("DF_ATOMIC_TEST_MYSQL_DSN not supplied")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE data_sources (id varchar(36) primary key, name varchar(255), type varchar(80), config json not null, status varchar(20), owner_id varchar(255) not null default '', tenant_id varchar(255) not null default '', created_at datetime(6), updated_at datetime(6), UNIQUE KEY uniq_tenant_name (tenant_id, name))").Error; err != nil {
		t.Fatalf("isolated test table required: %v", err)
	}
	t.Cleanup(func() { db.Exec("DROP TABLE data_sources") })
	vault, err := security.NewCredentialVaultWithKeyring("key", map[string][]byte{"key": []byte(strings.Repeat("a", 32))})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := repository.NewDataSourceRepository(db, vault)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	source := &model.DataSource{Name: "orders", Type: model.DatabaseTypeMySQL, OwnerID: "alice", TenantID: "a", Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{Host: "db", Port: 3306, Database: "orders", Username: "reader", Password: "password", AdditionalProps: map[string]interface{}{"access_token": "old"}}}
	if err = repo.Create(ctx, source); err != nil {
		t.Fatal(err)
	}
	wrapper := &pauseLockedUpdate{DataSourceRepository: repo, locked: make(chan struct{}), release: make(chan struct{})}
	svc := NewDataSourceService(wrapper, nil, &datasourceConfigValidatorStub{supported: true})
	finishedUpdate := make(chan error, 1)
	go func() {
		_, e := svc.UpdateDataSource(ctx, &security.Caller{UserID: "alice", TenantID: "a"}, source.ID, &UpdateDataSourceRequest{Config: &model.DataSourceConfig{Host: "updated", Port: 3306, Database: "orders", Username: "reader"}})
		finishedUpdate <- e
	}()
	select {
	case <-wrapper.locked:
	case <-time.After(5 * time.Second):
		t.Fatal("update did not acquire row lock")
	}
	tokenStore := repo.(interface {
		CompareAndSwapAccessToken(context.Context, string, string, string, time.Time) (bool, error)
	})
	started := make(chan struct{})
	finishedToken := make(chan error, 1)
	go func() {
		close(started)
		ok, e := tokenStore.CompareAndSwapAccessToken(ctx, source.ID, "old", "new", time.Now().Add(time.Hour))
		if e == nil && !ok {
			e = tokenRotationFailed{}
		}
		finishedToken <- e
	}()
	<-started
	select {
	case e := <-finishedToken:
		close(wrapper.release)
		t.Fatalf("token rotation bypassed row lock: %v", e)
	case <-time.After(200 * time.Millisecond):
	}
	close(wrapper.release)
	select {
	case e := <-finishedUpdate:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("update did not finish")
	}
	select {
	case e := <-finishedToken:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rotation did not finish")
	}
	got, err := repo.GetByID(ctx, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Host != "updated" || got.Config.AdditionalProps["access_token"] != "new" {
		t.Fatal("config or rotated credential was lost")
	}
}
