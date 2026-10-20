//go:build mysql_rehearsal

package repository

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	mysqlDriver "github.com/go-sql-driver/mysql"
	mysqlGorm "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const mysqlRehearsalDSNEnv = "DATA_FABRIC_MYSQL_REHEARSAL_DSN"

var (
	mysqlRehearsalRetainedKey = []byte(strings.Repeat("r", 32))
	mysqlRehearsalCurrentKey  = []byte(strings.Repeat("c", 32))
)

func mysqlRehearsalDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv(mysqlRehearsalDSNEnv)
	if dsn == "" {
		t.Fatalf("%s is required; use the disposable rehearsal script", mysqlRehearsalDSNEnv)
	}
	parsed, err := mysqlDriver.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse rehearsal DSN: %v", err)
	}
	if !strings.HasPrefix(parsed.DBName, "data_fabric_rehearsal_") {
		t.Fatalf("refusing to use database %q: expected a disposable data_fabric_rehearsal_* database", parsed.DBName)
	}
	db, err := gorm.Open(mysqlGorm.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open rehearsal database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("ping rehearsal database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func mysqlRehearsalVault(t *testing.T, active string, keys map[string][]byte) *security.CredentialVault {
	t.Helper()
	vault, err := security.NewCredentialVaultWithKeyring(active, keys)
	if err != nil {
		t.Fatal(err)
	}
	return vault
}

func resetMySQLDataSources(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec("DROP TABLE IF EXISTS data_sources").Error; err != nil {
		t.Fatalf("reset data_sources table: %v", err)
	}
}

func mysqlConfigJSON(t *testing.T, config model.DataSourceConfig) string {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func insertMySQLDataSource(t *testing.T, db *gorm.DB, id, name, owner, tenant string, config model.DataSourceConfig) {
	t.Helper()
	row := model.DataSource{
		ID: id, Name: name, Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive,
		OwnerID: owner, TenantID: tenant, Config: config,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("insert synthetic data source %q: %v", id, err)
	}
}

func requireMySQLTenantIndex(t *testing.T, db *gorm.DB) {
	t.Helper()
	if db.Migrator().HasIndex(&model.DataSource{}, legacyNameIndex) {
		t.Fatalf("legacy global unique index %q is still present", legacyNameIndex)
	}
	if !db.Migrator().HasIndex(&model.DataSource{}, "idx_datasources_tenant_name") {
		t.Fatal("tenant/name unique index is missing")
	}
}

func TestMySQLUpgradePaths(t *testing.T) {
	db := mysqlRehearsalDB(t)
	ctx := context.Background()

	t.Run("v1.2.0 plaintext schema upgrades and is idempotent", func(t *testing.T) {
		resetMySQLDataSources(t, db)
		const historicalDDL = `CREATE TABLE data_sources (
			id char(36) NOT NULL,
			name varchar(255) NOT NULL,
			type enum('mysql','mariadb','postgresql','oracle','iceberg','delta_lake','hudi','snowflake','databricks','redshift','bigquery','s3_parquet','s3_orc','s3_avro','s3_csv','s3_json','minio_parquet','minio_csv','oss_parquet','cos_parquet','azure_blob_parquet','hdfs_avro','hdfs_parquet','hdfs_csv','ozone_parquet','clickhouse','doris','starrocks','druid','oceanbase_mysql','oceanbase_oracle','tidb','tdsql','gaussdb_mysql','gaussdb_postgres','dameng','kingbasees','gbase_8s','gbase_8t','oscar','opengauss','oss_delta','minio_iceberg','azure_delta','ozone_text','ozone_avro','azure_parquet','hdfs_text','hdfs_parquet_compressed','minio_delta','hive','sqlserver','hologres','mongodb','maxcompute') NOT NULL,
			config json NOT NULL,
			status enum('active','inactive','error') DEFAULT 'active',
			created_at datetime(3) DEFAULT NULL,
			updated_at datetime(3) DEFAULT NULL,
			PRIMARY KEY (id),
			UNIQUE KEY idx_data_sources_name (name)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`
		if err := db.Exec(historicalDDL).Error; err != nil {
			t.Fatalf("create v1.2.0-era schema: %v", err)
		}
		legacyConfig := model.DataSourceConfig{
			Host: "legacy-db.invalid", Port: 3306, Database: "synthetic", Username: "synthetic-reader",
			Password:        "rehearsal-v120-password",
			AdditionalProps: map[string]interface{}{"auth": map[string]interface{}{"access_token": "rehearsal-v120-token"}},
		}
		if err := db.Exec("INSERT INTO data_sources (id, name, type, config, status) VALUES (?, ?, ?, ?, ?)",
			"legacy-v120", "legacy-source", "mysql", mysqlConfigJSON(t, legacyConfig), "active").Error; err != nil {
			t.Fatalf("insert v1.2.0 synthetic row: %v", err)
		}

		if err := MigrateDataSourceSchema(db); err != nil {
			t.Fatalf("upgrade v1.2.0 schema: %v", err)
		}
		requireMySQLTenantIndex(t, db)
		for _, column := range []string{"owner_id", "tenant_id"} {
			if !db.Migrator().HasColumn(&model.DataSource{}, column) {
				t.Fatalf("new ownership column %q is missing", column)
			}
		}
		var ownerTenant struct{ OwnerID, TenantID string }
		if err := db.Raw("SELECT owner_id, tenant_id FROM data_sources WHERE id = ?", "legacy-v120").Scan(&ownerTenant).Error; err != nil {
			t.Fatal(err)
		}
		if ownerTenant.OwnerID != "" || ownerTenant.TenantID != "" {
			t.Fatalf("legacy row ownership = %q/%q; expected ownerless admin-only row", ownerTenant.OwnerID, ownerTenant.TenantID)
		}

		vault := mysqlRehearsalVault(t, "release", map[string][]byte{"release": mysqlRehearsalCurrentKey})
		repo, err := NewDataSourceRepository(db, vault)
		if err != nil {
			t.Fatal(err)
		}
		migrated, err := repo.MigrateCredentials(ctx)
		if err != nil || migrated != 1 {
			t.Fatalf("first credential migration = %d, %v; want 1 and nil", migrated, err)
		}
		raw := storedConfig(t, db, "legacy-v120")
		for _, secret := range []string{"rehearsal-v120-password", "rehearsal-v120-token"} {
			if strings.Contains(raw, secret) {
				t.Fatalf("synthetic plaintext credential %q remains in MySQL config", secret)
			}
		}
		legacy, err := repo.GetByID(ctx, "legacy-v120")
		if err != nil || legacy.Config.Password != "rehearsal-v120-password" {
			t.Fatalf("read migrated legacy row = %+v, %v", legacy, err)
		}
		auth := legacy.Config.AdditionalProps["auth"].(map[string]interface{})
		if auth["access_token"] != "rehearsal-v120-token" {
			t.Fatalf("nested legacy token did not round-trip: %#v", auth)
		}

		insertMySQLDataSource(t, db, "tenant-a-same-name", "shared-name", "owner-a", "tenant-a", model.DataSourceConfig{})
		insertMySQLDataSource(t, db, "tenant-b-same-name", "shared-name", "owner-b", "tenant-b", model.DataSourceConfig{})
		if err := db.Create(&model.DataSource{
			ID: "tenant-a-duplicate", Name: "shared-name", Type: model.DatabaseTypeMySQL,
			Status: model.DataSourceStatusActive, OwnerID: "owner-c", TenantID: "tenant-a",
		}).Error; err == nil {
			t.Fatal("same-tenant duplicate name unexpectedly succeeded")
		}

		nonAdminScope := ScopeForCaller(&security.Caller{UserID: "visitor"})
		visible, _, err := repo.GetAll(ctx, nonAdminScope, "", 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range visible {
			if row.ID == "legacy-v120" {
				t.Fatal("ownerless v1.2.0 row is visible to a non-admin")
			}
		}
		adminScope := ScopeForCaller(&security.Caller{UserID: "admin", Roles: []string{security.AdminRole}})
		adminVisible, _, err := repo.GetAll(ctx, adminScope, "", 100, 0)
		if err != nil {
			t.Fatal(err)
		}
		foundLegacy := false
		for _, row := range adminVisible {
			foundLegacy = foundLegacy || row.ID == "legacy-v120"
		}
		if !foundLegacy {
			t.Fatal("administrator cannot see the ownerless v1.2.0 row")
		}

		if err := MigrateDataSourceSchema(db); err != nil {
			t.Fatalf("second schema migration: %v", err)
		}
		if migrated, err := repo.MigrateCredentials(ctx); err != nil || migrated != 0 {
			t.Fatalf("second credential migration = %d, %v; want 0 and nil", migrated, err)
		}
		requireMySQLTenantIndex(t, db)
	})

	t.Run("recent encrypted schema retains and rotates its keyring", func(t *testing.T) {
		resetMySQLDataSources(t, db)
		if err := db.AutoMigrate(&model.DataSource{}); err != nil {
			t.Fatalf("create recent encrypted schema: %v", err)
		}
		oldVault := mysqlRehearsalVault(t, "retained", map[string][]byte{"retained": mysqlRehearsalRetainedKey})
		retainedCiphertext, err := oldVault.EncryptCredentials([]byte("rehearsal-retained-password"))
		if err != nil {
			t.Fatal(err)
		}
		retainedToken, err := oldVault.EncryptCredentials([]byte("rehearsal-retained-token"))
		if err != nil {
			t.Fatal(err)
		}
		currentVault := mysqlRehearsalVault(t, "current", map[string][]byte{
			"retained": mysqlRehearsalRetainedKey, "current": mysqlRehearsalCurrentKey,
		})
		currentCiphertext, err := currentVault.EncryptCredentials([]byte("rehearsal-current-password"))
		if err != nil {
			t.Fatal(err)
		}
		insertMySQLDataSource(t, db, "recent-retained-key", "retained-key-source", "owner-a", "tenant-a",
			model.DataSourceConfig{Host: "recent-db.invalid", Port: 3306, Database: "synthetic", Username: "reader",
				Password: retainedCiphertext, AdditionalProps: map[string]interface{}{"access_token": retainedToken}})
		insertMySQLDataSource(t, db, "recent-current-key", "current-key-source", "owner-b", "tenant-b",
			model.DataSourceConfig{Host: "recent-db.invalid", Port: 3306, Database: "synthetic", Username: "reader",
				Password: currentCiphertext})

		if err := MigrateDataSourceSchema(db); err != nil {
			t.Fatalf("migrate recent schema: %v", err)
		}
		requireMySQLTenantIndex(t, db)
		repo, err := NewDataSourceRepository(db, currentVault)
		if err != nil {
			t.Fatal(err)
		}
		migrated, err := repo.MigrateCredentials(ctx)
		if err != nil || migrated != 1 {
			t.Fatalf("recent credential migration = %d, %v; want 1 and nil", migrated, err)
		}
		if raw := storedConfig(t, db, "recent-retained-key"); !strings.Contains(raw, "dfcred:v1:current:") || strings.Contains(raw, "dfcred:v1:retained:") {
			t.Fatalf("retained-key credentials were not rotated to active key: %s", raw)
		}
		if raw := storedConfig(t, db, "recent-current-key"); !strings.Contains(raw, "dfcred:v1:current:") {
			t.Fatalf("current-key credentials changed unexpectedly: %s", raw)
		}
		got, err := repo.GetByID(ctx, "recent-retained-key")
		if err != nil || got.Config.Password != "rehearsal-retained-password" {
			t.Fatalf("retained-key row read = %+v, %v", got, err)
		}
		auth := got.Config.AdditionalProps["access_token"]
		if auth != "rehearsal-retained-token" {
			t.Fatalf("retained-key nested token = %#v", auth)
		}
		if migrated, err := repo.MigrateCredentials(ctx); err != nil || migrated != 0 {
			t.Fatalf("repeat recent credential migration = %d, %v; want 0 and nil", migrated, err)
		}
	})
}

func TestMySQLFailClosedFixture(t *testing.T) {
	db := mysqlRehearsalDB(t)
	resetMySQLDataSources(t, db)
	if err := db.AutoMigrate(&model.DataSource{}); err != nil {
		t.Fatal(err)
	}
	insertMySQLDataSource(t, db, "a-before-failure", "valid-before-failure", "owner-a", "tenant-a",
		model.DataSourceConfig{Host: "synthetic-db.invalid", Port: 3306, Database: "synthetic", Username: "reader",
			Password: "rehearsal-before-failure-secret"})
	insertMySQLDataSource(t, db, "z-missing-key", "missing-key-source", "owner-a", "tenant-a",
		model.DataSourceConfig{Host: "synthetic-db.invalid", Port: 3306, Database: "synthetic", Username: "reader",
			Password: "dfcred:v1:missing-key:c3ludGhldGlj"})
}

func TestMySQLRestoredRecentFixture(t *testing.T) {
	db := mysqlRehearsalDB(t)
	vault := mysqlRehearsalVault(t, "current", map[string][]byte{
		"retained": mysqlRehearsalRetainedKey, "current": mysqlRehearsalCurrentKey,
	})
	repo, err := NewDataSourceRepository(db, vault)
	if err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{
		"recent-retained-key": "rehearsal-retained-password",
		"recent-current-key":  "rehearsal-current-password",
	} {
		got, err := repo.GetByID(context.Background(), id)
		if err != nil || got.Config.Password != want {
			t.Fatalf("restored row %q = %+v, %v; want password preserved", id, got, err)
		}
	}
	var count int64
	if err := db.Model(&model.DataSource{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("restored data_sources rows = %d, want 2", count)
	}
	requireMySQLTenantIndex(t, db)
}
