package repository

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

func TestCredentialAuditChecksRetainedKeysWithoutWriting(t *testing.T) {
	oldKey := []byte(strings.Repeat("o", 32))
	newKey := []byte(strings.Repeat("n", 32))
	oldVault, err := security.NewCredentialVaultWithKeyring("old", map[string][]byte{"old": oldKey})
	if err != nil {
		t.Fatal(err)
	}
	db, repo := testRepository(t, oldVault)
	source := &model.DataSource{ID: "old-source", Name: "orders", Type: model.DatabaseTypeMySQL,
		Config: model.DataSourceConfig{Password: "synthetic-password", AdditionalProps: map[string]interface{}{
			"auth": map[string]interface{}{"access_token": "synthetic-token"},
		}}}
	if err := repo.Create(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	before := storedConfig(t, db, source.ID)
	complete, err := security.NewCredentialVaultWithKeyring("new", map[string][]byte{"old": oldKey, "new": newKey})
	if err != nil {
		t.Fatal(err)
	}
	count, err := AuditStoredDataSourceCredentials(context.Background(), db, complete)
	if err != nil || count != 1 {
		t.Fatalf("audit count=%d error=%v", count, err)
	}
	if got := storedConfig(t, db, source.ID); got != before {
		t.Fatal("audit changed persisted credentials")
	}
	missing, err := security.NewCredentialVaultWithKeyring("new", map[string][]byte{"new": newKey})
	if err != nil {
		t.Fatal(err)
	}
	count, err = AuditStoredDataSourceCredentials(context.Background(), db, missing)
	if count != 0 || !errors.Is(err, ErrCredentialAudit) {
		t.Fatalf("missing historical key was not rejected: count=%d error=%v", count, err)
	}
	if strings.Contains(err.Error(), "synthetic") || strings.Contains(err.Error(), "old-source") {
		t.Fatal("audit error disclosed source details")
	}
	if got := storedConfig(t, db, source.ID); got != before {
		t.Fatal("failed audit changed persisted credentials")
	}
}

func TestCredentialAuditRejectsPlaintextCorruptionAndUnavailableDatabase(t *testing.T) {
	for _, value := range []string{"synthetic-plaintext", "dfcred:v1:key-1:invalid"} {
		t.Run(value, func(t *testing.T) {
			vault := vaultForTest(t)
			db, _ := testRepository(t, vault)
			source := &model.DataSource{ID: "invalid", Name: "invalid", Type: model.DatabaseTypeMySQL,
				Config: model.DataSourceConfig{Password: value}}
			if err := db.Create(source).Error; err != nil {
				t.Fatal(err)
			}
			before := storedConfig(t, db, source.ID)
			if count, err := AuditStoredDataSourceCredentials(context.Background(), db, vault); count != 0 || !errors.Is(err, ErrCredentialAudit) {
				t.Fatalf("invalid ciphertext accepted: count=%d error=%v", count, err)
			}
			if storedConfig(t, db, source.ID) != before {
				t.Fatal("audit modified the invalid record")
			}
		})
	}
	if _, err := AuditStoredDataSourceCredentials(context.Background(), nil, vaultForTest(t)); !errors.Is(err, ErrCredentialAudit) {
		t.Fatalf("nil database accepted: %v", err)
	}
}

func TestCredentialAuditChecksBeyondFirstBatch(t *testing.T) {
	vault := vaultForTest(t)
	db, repo := testRepository(t, vault)
	for i := 0; i < 101; i++ {
		source := &model.DataSource{ID: fmt.Sprintf("source-%03d", i), Name: fmt.Sprintf("source-%03d", i),
			Type: model.DatabaseTypeMySQL, Config: model.DataSourceConfig{Password: "synthetic"}}
		if err := repo.Create(context.Background(), source); err != nil {
			t.Fatal(err)
		}
	}
	if count, err := AuditStoredDataSourceCredentials(context.Background(), db, vault); err != nil || count != 101 {
		t.Fatalf("audit count=%d error=%v", count, err)
	}
	bad := &model.DataSource{ID: "source-101", Name: "source-101", Type: model.DatabaseTypeMySQL,
		Config: model.DataSourceConfig{Password: "dfcred:v1:missing:c3ludGhldGlj"}}
	if err := db.Create(bad).Error; err != nil {
		t.Fatal(err)
	}
	if count, err := AuditStoredDataSourceCredentials(context.Background(), db, vault); count != 0 || !errors.Is(err, ErrCredentialAudit) {
		t.Fatalf("invalid later record accepted: count=%d error=%v", count, err)
	}
}
