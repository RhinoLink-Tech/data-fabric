package mcp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

type deletionStub struct {
	*managementStub
	deleted bool
}

func (s *deletionStub) DeleteSourceScoped(_ context.Context, c *security.Caller, id, name string) error {
	if !s.authorized(c) || name != "orders" {
		return repository.ErrDataSourceNotFound
	}
	s.deleted = true
	return nil
}

type probeStub struct {
	started chan struct{}
	release chan struct{}
	err     error
}

func (p *probeStub) CheckStoredSourceConnectivity(ctx context.Context, source *model.DataSource) error {
	if p.started != nil {
		p.started <- struct{}{}
	}
	if p.release != nil {
		select {
		case <-p.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return p.err
}
func TestDeleteRequiresManagementAndExactName(t *testing.T) {
	stub := &deletionStub{managementStub: &managementStub{source: &model.DataSource{OwnerID: "alice", TenantID: "a"}}}
	srv := NewMCPServer(nil, nil, nil)
	srv.SetDataSourceManagementService(stub)
	for _, tc := range []struct {
		caller *security.Caller
		name   string
	}{
		{&security.Caller{UserID: "alice", TenantID: "a"}, "orders"},
		{&security.Caller{UserID: "bob", TenantID: "a", Scopes: []string{sourceManagementScope}}, "orders"},
		{&security.Caller{UserID: "alice", TenantID: "b", Scopes: []string{sourceManagementScope}}, "orders"},
		{&security.Caller{UserID: "alice", TenantID: "a", Scopes: []string{sourceManagementScope}}, "ORDERS"},
	} {
		ctx := context.WithValue(context.Background(), callerContextKey{}, tc.caller)
		result, err := srv.handleDeleteDataSource(ctx, callToolRequest("delete_data_source", map[string]any{"datasource_id": "id", "confirmation_name": tc.name}))
		if err != nil || !result.IsError || stub.deleted {
			t.Fatal("unexpected delete success")
		}
	}
	caller := &security.Caller{UserID: "alice", TenantID: "a", Scopes: []string{sourceManagementScope}}
	result, err := srv.handleDeleteDataSource(context.WithValue(context.Background(), callerContextKey{}, caller), callToolRequest("delete_data_source", map[string]any{"datasource_id": "id", "confirmation_name": "orders"}))
	if err != nil || result.IsError || !stub.deleted {
		t.Fatal("authorized delete failed")
	}
}
func TestConnectionProbeSanitizesAndRequiresOwnership(t *testing.T) {
	stub := &managementStub{source: &model.DataSource{ID: "id", OwnerID: "alice", TenantID: "a", Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Password: "secret-marker"}}}
	srv := NewMCPServer(nil, nil, nil)
	srv.SetDataSourceManagementService(stub)
	srv.SetSourceConnectivityTester(&probeStub{err: errors.New("backend-secret-marker")})
	for _, caller := range []*security.Caller{{UserID: "alice", TenantID: "a"}, {UserID: "alice", TenantID: "b", Scopes: []string{sourceManagementScope}}, {UserID: "bob", TenantID: "a", Scopes: []string{sourceManagementScope}}} {
		result, err := srv.handleTestDataSourceConnection(context.WithValue(context.Background(), callerContextKey{}, caller), callToolRequest("test_data_source_connection", map[string]any{"datasource_id": "id"}))
		if err != nil || !result.IsError {
			t.Fatal("expected rejection")
		}
	}
	caller := &security.Caller{UserID: "alice", TenantID: "a", Scopes: []string{sourceManagementScope}}
	ctx := context.WithValue(context.Background(), callerContextKey{}, caller)
	for _, args := range []map[string]any{{"datasource_id": "id"}, {"datasource_id": "id", "password": "secret-marker"}} {
		result, err := srv.handleTestDataSourceConnection(ctx, callToolRequest("test_data_source_connection", args))
		if err != nil || !result.IsError || strings.Contains(resultText(t, result), "secret-marker") {
			t.Fatal("failure or unexpected field leaked")
		}
	}
}
func TestConnectionProbeLimitsConcurrentChecks(t *testing.T) {
	stub := &managementStub{source: &model.DataSource{ID: "id", OwnerID: "alice", TenantID: "a", Type: model.DatabaseTypeMySQL}}
	tester := &probeStub{started: make(chan struct{}, 4), release: make(chan struct{})}
	srv := NewMCPServer(nil, nil, nil)
	srv.SetDataSourceManagementService(stub)
	srv.SetSourceConnectivityTester(tester)
	caller := &security.Caller{UserID: "alice", TenantID: "a", Scopes: []string{sourceManagementScope}}
	ctx := context.WithValue(context.Background(), callerContextKey{}, caller)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv.handleTestDataSourceConnection(ctx, callToolRequest("test_data_source_connection", map[string]any{"datasource_id": "id"}))
		}()
	}
	for i := 0; i < 4; i++ {
		<-tester.started
	}
	result, err := srv.handleTestDataSourceConnection(ctx, callToolRequest("test_data_source_connection", map[string]any{"datasource_id": "id"}))
	close(tester.release)
	wg.Wait()
	if err != nil || !result.IsError || !strings.Contains(resultText(t, result), "busy") {
		t.Fatal("fifth concurrent test was not rejected")
	}
}
func TestConnectionProbeSuccessAndRejectsCustomCA(t *testing.T) {
	stub := &managementStub{source: &model.DataSource{ID: "id", OwnerID: "alice", TenantID: "a", Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Password: "secret-marker"}}}
	srv := NewMCPServer(nil, nil, nil)
	srv.SetDataSourceManagementService(stub)
	srv.SetSourceConnectivityTester(&probeStub{})
	caller := &security.Caller{UserID: "alice", TenantID: "a", Scopes: []string{sourceManagementScope}}
	ctx := context.WithValue(context.Background(), callerContextKey{}, caller)
	req := callToolRequest("test_data_source_connection", map[string]any{"datasource_id": "id"})
	result, err := srv.handleTestDataSourceConnection(ctx, req)
	if err != nil || result.IsError || strings.Contains(resultText(t, result), "secret-marker") {
		t.Fatal("successful probe failed or leaked")
	}
	stub.source.Config.SSLRootCert = "/tmp/example-ca"
	result, err = srv.handleTestDataSourceConnection(ctx, req)
	if err != nil || !result.IsError || !strings.Contains(resultText(t, result), "certificate paths") {
		t.Fatal("custom CA file should be rejected before probing")
	}
}

func TestConnectionProbeDelegatesTLS(t *testing.T) {
	source := &model.DataSource{ID: "id", OwnerID: "alice", TenantID: "a", Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{SSL: true, TLSServerName: "db.example.test", TLSCA: "custom"}}
	srv := NewMCPServer(nil, nil, nil)
	srv.SetDataSourceManagementService(&managementStub{source: source})
	caller := &security.Caller{UserID: "alice", TenantID: "a", Scopes: []string{sourceManagementScope}}
	ctx := context.WithValue(context.Background(), callerContextKey{}, caller)
	req := callToolRequest("test_data_source_connection", map[string]any{"datasource_id": "id"})
	tester := &probeStub{}
	srv.SetSourceConnectivityTester(tester)
	result, err := srv.handleTestDataSourceConnection(ctx, req)
	if err != nil || result.IsError {
		t.Fatal("TLS probe was not delegated")
	}
	tester.err = errors.New("certificate rejected")
	result, err = srv.handleTestDataSourceConnection(ctx, req)
	if err != nil || !result.IsError {
		t.Fatal("TLS verifier failure was not propagated")
	}
}
