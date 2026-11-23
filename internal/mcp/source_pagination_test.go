package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestListDataSourcesPagedAndScoped(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:source-paging?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE TABLE data_sources (id text primary key, name text, type text, config json not null, status text, owner_id text not null, tenant_id text not null, created_at datetime, updated_at datetime)").Error; err != nil {
		t.Fatal(err)
	}
	vault, err := security.NewCredentialVaultWithKeyring("test-key", map[string][]byte{"test-key": []byte(strings.Repeat("a", 32))})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := repository.NewDataSourceRepository(db, vault)
	if err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sources := make([]model.DataSource, 1002)
	for i := range sources {
		tenant := "team-a"
		if i == 1001 {
			tenant = "team-b"
		}
		sources[i] = model.DataSource{ID: fmt.Sprintf("%04d", i), Name: fmt.Sprintf("source-%04d", i), Type: model.DatabaseTypeMySQL, Status: model.DataSourceStatusActive, OwnerID: "owner-" + tenant, TenantID: tenant, CreatedAt: stamp, UpdatedAt: stamp, Config: model.DataSourceConfig{Host: "db", Port: 3306}}
		if i == 0 {
			sources[i].OwnerID = "reader"
		}
	}
	if err := db.CreateInBatches(sources, 100).Error; err != nil {
		t.Fatal(err)
	}
	srv := NewMCPServer(nil, repo, &stubTableDescriber{})
	ctx := context.WithValue(context.Background(), callerContextKey{}, &security.Caller{UserID: "reader", TenantID: "team-a", StrictTenantIsolation: true})
	offset := 0
	seen := map[string]bool{}
	for page := 0; page < 2; page++ {
		result, err := srv.handleListDataSources(ctx, callToolRequest("list_data_sources", map[string]any{"offset": offset}))
		if err != nil || result.IsError {
			t.Fatalf("page %d: %v %+v", page, err, result)
		}
		var payload struct {
			DataSources []struct {
				ID            string `json:"id"`
				OwnedByCaller *bool  `json:"owned_by_caller"`
			} `json:"data_sources"`
			Count      int  `json:"count"`
			Total      int  `json:"total"`
			HasMore    bool `json:"has_more"`
			NextOffset int  `json:"next_offset"`
		}
		if err := json.Unmarshal([]byte(resultText(t, result)), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Total != 1001 || payload.Count != len(payload.DataSources) {
			t.Fatalf("unexpected page: %+v", payload)
		}
		for _, source := range payload.DataSources {
			if source.ID == "1001" || seen[source.ID] {
				t.Fatalf("hidden or duplicate ID: %s", source.ID)
			}
			if source.OwnedByCaller == nil || *source.OwnedByCaller != (source.ID == "0000") {
				t.Fatalf("incorrect ownership on source %s", source.ID)
			}
			seen[source.ID] = true
		}
		if page == 0 && (!payload.HasMore || payload.NextOffset != 1000) {
			t.Fatalf("missing continuation: %+v", payload)
		}
		if page == 1 && (payload.HasMore || payload.Count != 1) {
			t.Fatalf("unexpected final page: %+v", payload)
		}
		offset = payload.NextOffset
	}
	if len(seen) != 1001 {
		t.Fatalf("saw %d sources", len(seen))
	}
}

func TestListDataSourcesRejectsInvalidPageArguments(t *testing.T) {
	srv := NewMCPServer(nil, listingRepoStub{}, &stubTableDescriber{})
	for _, args := range []map[string]any{{"limit": 0}, {"limit": 1001}, {"limit": 1.5}, {"limit": "2"}, {"offset": -1}, {"offset": 2147482648.0}} {
		result, err := srv.handleListDataSources(context.Background(), callToolRequest("list_data_sources", args))
		if err != nil || result == nil || !result.IsError {
			t.Fatalf("args %v: result=%+v err=%v", args, result, err)
		}
	}
}

// A continuation must never point beyond the largest supported offset.
type extremeSourcePageRepo struct {
	repository.DataSourceRepository
}

func (extremeSourcePageRepo) GetAll(context.Context, *repository.AccessScope, model.DataSourceStatus, int, int) ([]*model.DataSource, int64, error) {
	return []*model.DataSource{{ID: "last"}}, 2147483000, nil
}
func TestListDataSourcesRejectsUnusableContinuation(t *testing.T) {
	srv := NewMCPServer(nil, extremeSourcePageRepo{}, &stubTableDescriber{})
	result, err := srv.handleListDataSources(context.Background(), callToolRequest("list_data_sources", map[string]any{"offset": 2147482647}))
	if err != nil || result == nil || !result.IsError {
		t.Fatalf("expected bounded-range tool error, got %+v %v", result, err)
	}
}
