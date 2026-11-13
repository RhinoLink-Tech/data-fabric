package service

import (
	"context"
	"errors"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"strconv"
	"testing"
)

func TestPreserveRedactedHostOnEdit(t *testing.T) {
	existing := model.DataSourceConfig{Host: "https://user:pass@example.test/objects?token=secret"}
	redacted := model.PublicDataSourceHost(existing.Host)
	got := preserveDataSourceSecrets(existing, model.DataSourceConfig{Host: redacted})
	if got.Host != existing.Host {
		t.Fatal("editing the redacted representation destroyed the stored host")
	}
	got = preserveDataSourceSecrets(existing, model.DataSourceConfig{Host: "https://other.example.test/data"})
	if got.Host != "https://other.example.test/data" {
		t.Fatal("explicit replacement host was not saved")
	}
}

func TestRedactedHostRoundTripsThroughScopedUpdate(t *testing.T) {
	ctx := context.Background()
	repo, source := transactionFixture(t)
	const original = "https://urluser:urlpassword@example.test/path?sig=urltoken"
	source.Config.Host = original
	if err := repo.Update(ctx, source); err != nil {
		t.Fatal(err)
	}
	svc := NewDataSourceService(repo, nil, &datasourceConfigValidatorStub{supported: true})
	caller := &security.Caller{UserID: "alice", TenantID: "a"}
	update := source.Config
	update.Host = model.PublicDataSourceHost(original)
	if _, err := svc.UpdateDataSource(ctx, caller, source.ID, &UpdateDataSourceRequest{Config: &update}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, source.ID)
	if err != nil || got.Config.Host != original {
		t.Fatalf("update lost URL credentials: %v", err)
	}
}

func TestRedactedHostDoesNotOverwriteConcurrentRotation(t *testing.T) {
	ctx := context.Background()
	repo, source := transactionFixture(t)
	oldHost := "postgres://user:old-secret@example.test/path?sig=old-token"
	newHost := "postgres://user:new-secret@example.test/path?sig=new-token"
	source.Config.Host = oldHost
	if err := repo.Update(ctx, source); err != nil {
		t.Fatal(err)
	}
	oldPublic := model.PublicDataSourceHost(oldHost)
	wrapper := &beforeLockedUpdate{DataSourceRepository: repo, before: func() {
		current, err := repo.GetByID(ctx, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		current.Config.Host = newHost
		if err := repo.Update(ctx, current); err != nil {
			t.Fatal(err)
		}
	}}
	svc := NewDataSourceService(wrapper, nil, &datasourceConfigValidatorStub{supported: true})
	update := source.Config
	update.Host = oldPublic
	_, err := svc.UpdateDataSource(ctx, &security.Caller{UserID: "alice", TenantID: "a"}, source.ID, &UpdateDataSourceRequest{Config: &update})
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, source.ID)
	if err != nil || got.Config.Host != newHost {
		t.Fatalf("concurrent host rotation was overwritten: %v", err)
	}
}

func TestUpdateRejectsEditedRedactedHostMarker(t *testing.T) {
	for i, replacement := range []string{
		"https://redacted@other.example/path?redacted",
		"https://user:pass@other.example/path?redacted&foo=bar",
		"https://other.example/path#redacted",
		"[redacted]",
	} {
		t.Run("case_"+strconv.Itoa(i), func(t *testing.T) {
			ctx := context.Background()
			repo, source := transactionFixture(t)
			const original = "https://user:pass@original.example/path?sig=secret"
			source.Config.Host = original
			if err := repo.Update(ctx, source); err != nil {
				t.Fatal(err)
			}
			svc := NewDataSourceService(repo, nil, &datasourceConfigValidatorStub{supported: true})
			update := source.Config
			update.Host = replacement
			_, err := svc.UpdateDataSource(ctx, &security.Caller{UserID: "alice", TenantID: "a"}, source.ID, &UpdateDataSourceRequest{Config: &update})
			if !errors.Is(err, ErrInvalidDataSourceConfig) {
				t.Fatalf("expected rejection: %v", err)
			}
			got, err := repo.GetByID(ctx, source.ID)
			if err != nil || got.Config.Host != original {
				t.Fatalf("rejected update changed host: %v", err)
			}
		})
	}
}

func TestStaleRedactedHostCannotUndoConcurrentEndpointChange(t *testing.T) {
	ctx := context.Background()
	repo, source := transactionFixture(t)
	oldHost := "https://user:old@example.test/path?sig=old"
	newHost := "https://user:new@other.example.test/path?sig=new"
	source.Config.Host = oldHost
	if err := repo.Update(ctx, source); err != nil {
		t.Fatal(err)
	}
	wrapper := &beforeLockedUpdate{DataSourceRepository: repo, before: func() {
		current, err := repo.GetByID(ctx, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		current.Config.Host = newHost
		if err := repo.Update(ctx, current); err != nil {
			t.Fatal(err)
		}
	}}
	svc := NewDataSourceService(wrapper, nil, &datasourceConfigValidatorStub{supported: true})
	update := source.Config
	update.Host = model.PublicDataSourceHost(oldHost)
	_, err := svc.UpdateDataSource(ctx, &security.Caller{UserID: "alice", TenantID: "a"}, source.ID, &UpdateDataSourceRequest{Config: &update})
	if !errors.Is(err, ErrInvalidDataSourceConfig) {
		t.Fatalf("stale update should be rejected: %v", err)
	}
	got, err := repo.GetByID(ctx, source.ID)
	if err != nil || got.Config.Host != newHost {
		t.Fatalf("stale update changed host: %v", err)
	}
}
