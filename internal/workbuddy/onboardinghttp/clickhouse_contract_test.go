//go:build !datafabric_core

package onboardinghttp

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/security/onboarding"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClickHouseBrowserRestrictedTLSContract(t *testing.T) {
	portText, ca := os.Getenv("DATA_FABRIC_CLICKHOUSE_TLS_CONTRACT_PORT"), os.Getenv("DATA_FABRIC_CLICKHOUSE_TLS_ROOT_CA")
	if portText == "" || ca == "" {
		t.Skip("ClickHouse TLS contract fixture not configured")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := database.NewFixedIPEgressPolicyWithClickHouse([]string{"127.0.0.1/32"}, []int{port}, ca, false, false, false, true)
	if err != nil {
		t.Fatal(err)
	}
	probe, err := NewAllowedProbe([]string{"127.0.0.1/32"}, []int{port}, 2)
	if err != nil {
		t.Fatal(err)
	}
	probe.resolver = loopbackResolver{}
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "meta.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec(`CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null default '', tenant_id text not null default '', created_at datetime, updated_at datetime, UNIQUE (tenant_id, name))`).Error; err != nil {
		t.Fatal(err)
	}
	if err = onboarding.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err = onboarding.MigrateCompletions(db); err != nil {
		t.Fatal(err)
	}
	flow, err := onboarding.New(db, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	principal := onboarding.Principal{Issuer: "https://issuer.test", Subject: "alice", Tenant: "a"}
	id, err := flow.Begin(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	vault, err := security.NewCredentialVaultWithKeyring("test", map[string][]byte{"test": []byte(strings.Repeat("a", 32))})
	if err != nil {
		t.Fatal(err)
	}
	auth := &testAuth{actor: Actor{Issuer: principal.Issuer, Subject: principal.Subject, Tenant: principal.Tenant, UserID: "alice", CSRF: "csrf"}, allow: true, csrf: true}
	handler, err := New(db, vault, database.NewHealthChecker(nil), auth, testStore{flow, id, principal}, actualTLSProbe{probe, policy}, &testAudit{}, []model.DatabaseType{model.DatabaseTypeClickHouse})
	if err != nil {
		t.Fatal(err)
	}
	password := os.Getenv("DATA_FABRIC_CLICKHOUSE_TLS_CONTRACT_PASSWORD")
	input := map[string]any{"name": "orders", "type": "clickhouse", "config": map[string]any{"host": "127.0.0.1", "port": port, "database": os.Getenv("DATA_FABRIC_CLICKHOUSE_TLS_CONTRACT_DATABASE"), "username": os.Getenv("DATA_FABRIC_CLICKHOUSE_TLS_CONTRACT_USERNAME"), "password": password, "ssl": true, "tlsCA": "custom"}}
	invoke := func(action string) *httptest.ResponseRecorder {
		b, _ := json.Marshal(input)
		w := httptest.NewRecorder()
		handler.ServeFlow(w, httptest.NewRequest("POST", "https://example.test", bytes.NewReader(b)), id, action)
		return w
	}
	cfg := input["config"].(map[string]any)
	cfg["password"] = "wrong-password"
	if w := invoke("test"); w.Code == 200 {
		t.Fatal("bad password accepted")
	}
	cfg["password"] = password
	cfg["host"] = "wrong.example.test"
	if w := invoke("test"); w.Code == 200 {
		t.Fatal("bad TLS hostname accepted")
	}
	cfg["host"] = "127.0.0.1"
	if w := invoke("test"); w.Code != 200 || strings.Contains(w.Body.String(), password) {
		t.Fatalf("valid test failed or leaked: %d", w.Code)
	}
	auth.allow = false
	if w := invoke("sources"); w.Code != 403 {
		t.Fatal("unauthorized creation accepted")
	}
	auth.allow = true
	w := invoke("sources")
	if w.Code != 200 || strings.Contains(w.Body.String(), password) {
		t.Fatalf("creation failed or leaked: %d", w.Code)
	}
	var response struct {
		ID string `json:"id"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.ID == "" {
		t.Fatal("missing created source")
	}
	repo, err := repository.NewDataSourceRepository(db, vault)
	if err != nil {
		t.Fatal(err)
	}
	source, err := repo.GetByID(context.Background(), response.ID)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := database.NewRestrictedConnectionPool(nil, nil, vault, true, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.CloseAll()
	conn, err := pool.GetConnection(context.Background(), source)
	if err != nil {
		t.Fatalf("created source connection: %v", err)
	}
	var n int
	if err = conn.QueryRow("SELECT 1").Scan(&n); err != nil || n != 1 {
		t.Fatalf("created source query: %v", err)
	}
	rows, err := conn.Query("SELECT id, status FROM browser_orders ORDER BY id")
	if err != nil {
		t.Fatalf("order query failed: %v", err)
	}
	defer rows.Close()
	want := []struct {
		id     int
		status string
	}{{1, "shipped"}, {2, "new"}, {3, "shipped"}}
	index := 0
	for rows.Next() {
		var id int
		var status string
		if index >= len(want) || rows.Scan(&id, &status) != nil || id != want[index].id || status != want[index].status {
			t.Fatal("unexpected order result")
		}
		index++
	}
	if err = rows.Err(); err != nil || index != len(want) {
		t.Fatal("incomplete order results")
	}
	metadata, err := conn.Query("SELECT name FROM system.columns WHERE database = currentDatabase() AND table = 'browser_orders' ORDER BY position")
	if err != nil {
		t.Fatalf("order metadata query failed: %v", err)
	}
	defer metadata.Close()
	var columns []string
	for metadata.Next() {
		var name string
		if err = metadata.Scan(&name); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, name)
	}
	if err = metadata.Err(); err != nil || len(columns) != 2 || columns[0] != "id" || columns[1] != "status" {
		t.Fatal("unexpected order metadata")
	}
	if _, err = conn.Exec("CREATE TABLE should_fail_browser (id UInt64) ENGINE = Memory"); !clickhouseAccessDenied(err) {
		t.Fatalf("source account did not reject DDL with a privilege error: %v", err)
	}
	// The database account must independently deny writes even without the
	// gateway's read-only session initializer.
	unwrapped, err := policy.OpenRestrictedDatabase(source, false)
	if err != nil {
		t.Fatal(err)
	}
	defer unwrapped.Close()
	if _, err = unwrapped.Exec("INSERT INTO browser_orders (id,status) VALUES (4,'new')"); !clickhouseAccessDenied(err) {
		t.Fatalf("source account did not reject DML with a privilege error: %v", err)
	}
}

func TestClickHouseProductionValidator(t *testing.T) {
	validator := database.NewHealthChecker(nil)
	if !validator.IsSupportedDataSourceType(model.DatabaseTypeClickHouse) {
		t.Skip("ClickHouse driver not present in this build")
	}
	cfg := model.DataSourceConfig{Host: "localhost", Port: 3306, Database: "orders", Username: "reader", Password: "synthetic", SSL: true, TLSCA: "custom", MaxPoolSize: 2, Timeout: 5}
	if err := validator.ValidateDataSourceConfiguration(&cfg, model.DatabaseTypeClickHouse); err != nil {
		t.Fatalf("ClickHouse configuration rejected: %v", err)
	}
}

func clickhouseAccessDenied(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not enough privileges")
}
