package repository

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func testRepository(t *testing.T, vault *security.CredentialVault) (*gorm.DB, DataSourceRepository) {
	t.Helper()
	name := strings.ReplaceAll(t.Name(), "/", "_")
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null default '', tenant_id text not null default '', created_at datetime, updated_at datetime, UNIQUE (tenant_id, name))").Error; err != nil {
		t.Fatal(err)
	}
	repo, err := NewDataSourceRepository(db, vault)
	if err != nil {
		t.Fatal(err)
	}
	return db, repo
}

func vaultForTest(t *testing.T) *security.CredentialVault {
	t.Helper()
	vault, err := security.NewCredentialVaultWithKeyring("key-1", map[string][]byte{"key-1": []byte(strings.Repeat("a", 32))})
	if err != nil {
		t.Fatal(err)
	}
	return vault
}

func storedConfig(t *testing.T, db *gorm.DB, id string) string {
	t.Helper()
	var raw string
	if err := db.Raw("SELECT config FROM data_sources WHERE id = ?", id).Scan(&raw).Error; err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDataSourceRepositoryEncryptsAtRestAndDecryptsOnRead(t *testing.T) {
	vault := vaultForTest(t)
	db, repo := testRepository(t, vault)
	ds := &model.DataSource{
		Name: "warehouse", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{Host: "db.local", Port: 3306, Database: "analytics", Username: "reader", Password: "db-password",
			AdditionalProps: map[string]interface{}{"api_token": "source-token", "auth": map[string]interface{}{"client_secret": "client-secret", "region": "west"}}},
	}
	if err := repo.Create(context.Background(), ds); err != nil {
		t.Fatal(err)
	}
	if ds.Config.Password != "db-password" {
		t.Fatal("Create mutated caller config")
	}
	raw := storedConfig(t, db, ds.ID)
	for _, secret := range []string{"db-password", "source-token", "client-secret"} {
		if strings.Contains(raw, secret) {
			t.Errorf("plaintext %q persisted: %s", secret, raw)
		}
	}
	if !strings.Contains(raw, "dfcred:v1:key-1:") {
		t.Fatalf("ciphertext envelope missing: %s", raw)
	}
	got, err := repo.GetByID(context.Background(), ds.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Password != "db-password" || got.Config.AdditionalProps["api_token"] != "source-token" {
		t.Fatalf("secrets did not round trip: %+v", got.Config)
	}
	auth := got.Config.AdditionalProps["auth"].(map[string]interface{})
	if auth["client_secret"] != "client-secret" || auth["region"] != "west" {
		t.Fatalf("nested values did not round trip: %#v", auth)
	}
}

func TestMigrateCredentialsEncryptsLegacyPlaintext(t *testing.T) {
	vault := vaultForTest(t)
	db, repo := testRepository(t, vault)
	ds := model.DataSource{
		ID: "legacy-1", Name: "legacy", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{Host: "db", Port: 3306, Database: "a", Username: "u", Password: "legacy-password",
			AdditionalProps: map[string]interface{}{"auth": map[string]interface{}{"access_token": "legacy-token"}}},
	}
	if err := db.Create(&ds).Error; err != nil {
		t.Fatal(err)
	}
	count, err := repo.MigrateCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migrated = %d", count)
	}
	raw := storedConfig(t, db, ds.ID)
	if strings.Contains(raw, "legacy-password") || strings.Contains(raw, "legacy-token") {
		t.Fatalf("plaintext remains: %s", raw)
	}
	got, err := repo.GetByID(context.Background(), ds.ID)
	if err != nil {
		t.Fatal(err)
	}
	auth := got.Config.AdditionalProps["auth"].(map[string]interface{})
	if got.Config.Password != "legacy-password" || auth["access_token"] != "legacy-token" {
		t.Fatalf("migration round trip failed: %+v", got.Config)
	}
}

func TestMigrateCredentialsRotatesOldKey(t *testing.T) {
	oldKey, newKey := []byte(strings.Repeat("o", 32)), []byte(strings.Repeat("n", 32))
	oldVault, err := security.NewCredentialVaultWithKeyring("old", map[string][]byte{"old": oldKey})
	if err != nil {
		t.Fatal(err)
	}
	db, oldRepo := testRepository(t, oldVault)
	ds := &model.DataSource{Name: "rotation", Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{Host: "db", Port: 3306, Database: "a", Username: "u", Password: "rotate-password"}}
	if err := oldRepo.Create(context.Background(), ds); err != nil {
		t.Fatal(err)
	}
	newVault, err := security.NewCredentialVaultWithKeyring("new", map[string][]byte{"old": oldKey, "new": newKey})
	if err != nil {
		t.Fatal(err)
	}
	newRepo, err := NewDataSourceRepository(db, newVault)
	if err != nil {
		t.Fatal(err)
	}
	count, err := newRepo.MigrateCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migrated = %d", count)
	}
	raw := storedConfig(t, db, ds.ID)
	if !strings.Contains(raw, "dfcred:v1:new:") || strings.Contains(raw, "dfcred:v1:old:") {
		t.Fatalf("rotation failed: %s", raw)
	}
	got, err := newRepo.GetByID(context.Background(), ds.ID)
	if err != nil || got.Config.Password != "rotate-password" {
		t.Fatalf("read after rotation = %+v, %v", got, err)
	}
}

func TestMigrateCredentialsFailsClosedAndRollsBack(t *testing.T) {
	vault := vaultForTest(t)
	db, repo := testRepository(t, vault)
	valid := model.DataSource{ID: "a-legacy", Name: "legacy", Type: model.DatabaseTypeMySQL,
		Config: model.DataSourceConfig{Host: "db", Port: 3306, Database: "a", Username: "u", Password: "plain-secret"}}
	bad := model.DataSource{ID: "z-bad", Name: "bad", Type: model.DatabaseTypeMySQL,
		Config: model.DataSourceConfig{Host: "db", Port: 3306, Database: "b", Username: "u", Password: "dfcred:v1:missing:YWJj"}}
	if err := db.Create(&valid).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&bad).Error; err != nil {
		t.Fatal(err)
	}
	_, err := repo.MigrateCredentials(context.Background())
	if err == nil || !errors.Is(err, security.ErrUnknownCredentialKey) {
		t.Fatalf("migration error = %v", err)
	}
	if raw := storedConfig(t, db, valid.ID); !strings.Contains(raw, "plain-secret") {
		t.Fatalf("transaction did not roll back: %s", raw)
	}
	if _, err := repo.GetByID(context.Background(), valid.ID); !errors.Is(err, ErrPlaintextCredential) {
		t.Fatalf("legacy read error = %v", err)
	}
}

func TestDataSourceMutationsReturnNotFoundForMissingRecords(t *testing.T) {
	ctx := context.Background()
	vault := vaultForTest(t)
	_, repo := testRepository(t, vault)

	missing := &model.DataSource{
		ID: "missing", Name: "missing", Type: model.DatabaseTypePostgreSQL,
		Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{Host: "db", Port: 5432, Database: "analytics", Username: "reader"},
	}
	mutations := []struct {
		name string
		call func() error
	}{
		{name: "update", call: func() error { return repo.Update(ctx, missing) }},
		{name: "delete", call: func() error { return repo.Delete(ctx, missing.ID) }},
		{name: "activate", call: func() error { return repo.Activate(ctx, missing.ID) }},
		{name: "deactivate", call: func() error { return repo.Deactivate(ctx, missing.ID) }},
		{name: "set error", call: func() error { return repo.SetError(ctx, missing.ID) }},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			if err := mutation.call(); !errors.Is(err, ErrDataSourceNotFound) {
				t.Fatalf("mutation error = %v, want ErrDataSourceNotFound", err)
			}
		})
	}
}

func TestDataSourceMutationsAcceptExistingRecordsAndDoNotRecreateOnUpdate(t *testing.T) {
	ctx := context.Background()
	vault := vaultForTest(t)
	db, repo := testRepository(t, vault)
	dataSource := &model.DataSource{
		Name: "existing", Type: model.DatabaseTypePostgreSQL, Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{Host: "db", Port: 5432, Database: "analytics", Username: "reader"},
	}
	if err := repo.Create(ctx, dataSource); err != nil {
		t.Fatal(err)
	}

	// Simulate a driver that reports zero changed rows for an idempotent update.
	if err := repo.(*dataSourceRepository).checkMutationResult(ctx, dataSource.ID, &gorm.DB{}); err != nil {
		t.Fatalf("zero-row mutation for existing source: %v", err)
	}
	if err := repo.Activate(ctx, dataSource.ID); err != nil {
		t.Fatalf("idempotent activate: %v", err)
	}
	if err := repo.Deactivate(ctx, dataSource.ID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if err := repo.Deactivate(ctx, dataSource.ID); err != nil {
		t.Fatalf("idempotent deactivate: %v", err)
	}
	if err := repo.SetError(ctx, dataSource.ID); err != nil {
		t.Fatalf("set error: %v", err)
	}

	dataSource.Name = "updated"
	if err := repo.Update(ctx, dataSource); err != nil {
		t.Fatalf("update existing source: %v", err)
	}
	updated, err := repo.GetByID(ctx, dataSource.ID)
	if err != nil || updated.Name != "updated" {
		t.Fatalf("updated source = %+v, %v", updated, err)
	}

	if err := db.Where("id = ?", dataSource.ID).Delete(&model.DataSource{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, dataSource); !errors.Is(err, ErrDataSourceNotFound) {
		t.Fatalf("update after concurrent deletion = %v, want ErrDataSourceNotFound", err)
	}
	var count int64
	if err := db.Model(&model.DataSource{}).Where("id = ?", dataSource.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("update recreated deleted row; count = %d", count)
	}
}
