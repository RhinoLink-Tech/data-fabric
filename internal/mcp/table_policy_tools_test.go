package mcp

import (
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMCPPolicyToolsRequireAdminAndPublishVersion(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:mcp-table-policy-"+uuid.NewString()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	initial := security.TableAuthorizationConfig{Enabled: true}
	authorizer := security.NewTableAuthorizer(initial)
	manager, err := security.NewTablePolicyManager(db, authorizer, initial, "")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewMCPServerWithTableAuthorizer(nil, nil, nil, authorizer)
	srv.SetTablePolicyManager(manager)

	policyJSON := "{\"enabled\":true,\"data_sources\":{}}"
	nonAdminCtx, nonAdminRequest := tablePolicyRequest(&security.Caller{UserID: "reader"}, "publish_table_policy", map[string]any{
		"expected_revision": float64(1), "policy_json": policyJSON,
	})
	denied, err := srv.handlePublishTablePolicy(nonAdminCtx, nonAdminRequest)
	if err != nil || !denied.IsError {
		t.Fatalf("non-admin policy publication result=%+v, error=%v", denied, err)
	}
	if manager.Status().Revision != "1" {
		t.Fatalf("non-admin request changed policy revision: %+v", manager.Status())
	}

	adminCtx, adminRequest := tablePolicyRequest(&security.Caller{UserID: "policy-admin", Roles: []string{security.AdminRole}}, "publish_table_policy", map[string]any{
		"expected_revision": float64(1), "policy_json": policyJSON,
	})
	published, err := srv.handlePublishTablePolicy(adminCtx, adminRequest)
	if err != nil || published.IsError {
		t.Fatalf("admin policy publication result=%+v, error=%v", published, err)
	}
	if manager.Status().Revision != "2" {
		t.Fatalf("admin request did not publish revision 2: %+v", manager.Status())
	}
	historyCtx, historyRequest := tablePolicyRequest(&security.Caller{UserID: "policy-admin", Roles: []string{security.AdminRole}}, "list_table_policy_publications", map[string]any{"limit": float64(10)})
	history, err := srv.handleListTablePolicyPublications(historyCtx, historyRequest)
	if err != nil || history.IsError || !strings.Contains(history.Content[0].(mcp.TextContent).Text, "\"publishedBy\":\"policy-admin\"") {
		t.Fatalf("MCP history result=%+v error=%v", history, err)
	}
	if _, ok := mcpPolicyRevision(float64(2)); !ok {
		t.Fatal("integer revision was rejected")
	}
	if _, ok := mcpPolicyRevision(float64(2.5)); ok {
		t.Fatal("fractional revision was accepted")
	}
}
