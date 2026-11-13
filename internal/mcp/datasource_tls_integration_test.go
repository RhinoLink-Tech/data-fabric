package mcp

import (
	"context"
	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"net"
	"os"
	"strconv"
	"testing"
)

func TestMCPRestrictedTLSRealDatabases(t *testing.T) {
	ca := os.Getenv("DF_TLS_CA")
	if ca == "" {
		t.Skip("isolated TLS integration not configured")
	}
	for _, tc := range []struct {
		env, user string
		typ       model.DatabaseType
	}{
		{"DF_TLS_POSTGRES", "postgres", model.DatabaseTypePostgreSQL},
		{"DF_TLS_MYSQL", "root", model.DatabaseTypeMySQL},
	} {
		t.Run(string(tc.typ), func(t *testing.T) {
			host, ps, err := net.SplitHostPort(os.Getenv(tc.env))
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(ps)
			if err != nil {
				t.Fatal(err)
			}
			policy, err := database.NewFixedIPEgressPolicyWithCA([]string{"127.0.0.1/32"}, []int{port}, ca)
			if err != nil {
				t.Fatal(err)
			}
			pool, err := database.NewRestrictedConnectionPool(nil, nil, nil, false, policy)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.CloseAll()
			source := &model.DataSource{ID: "id", OwnerID: "alice", TenantID: "a", Type: tc.typ, Config: model.DataSourceConfig{Host: host, Port: port, Database: "synthetic", Username: tc.user, Password: "SyntheT1c@:/?&=测试", SSL: true, TLSServerName: "db.example.test", TLSCA: "custom"}}
			srv := NewMCPServer(nil, nil, nil)
			srv.SetDataSourceManagementService(&managementStub{source: source})
			srv.SetSourceConnectivityTester(database.NewHealthChecker(pool))
			caller := &security.Caller{UserID: "alice", TenantID: "a", Scopes: []string{sourceManagementScope}}
			ctx := context.WithValue(context.Background(), callerContextKey{}, caller)
			req := callToolRequest("test_data_source_connection", map[string]any{"datasource_id": "id"})
			result, err := srv.handleTestDataSourceConnection(ctx, req)
			if err != nil || result.IsError {
				t.Fatalf("valid TLS probe failed: %v", result)
			}
			source.Config.TLSServerName = "wrong.example.test"
			result, err = srv.handleTestDataSourceConnection(ctx, req)
			if err != nil || !result.IsError {
				t.Fatal("wrong hostname accepted by MCP")
			}
		})
	}
}
