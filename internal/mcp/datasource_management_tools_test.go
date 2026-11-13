package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
)

type managementStub struct {
	service.DataSourceService
	calls     int
	gotCaller *security.Caller
	err       error
	source    *model.DataSource
}

func (s *managementStub) GetDataSource(_ context.Context, _ *security.Caller, id string) (*model.DataSource, error) {
	if s.source != nil {
		return s.source, nil
	}
	return &model.DataSource{ID: id, OwnerID: "alice"}, nil
}
func (s *managementStub) UpdateDataSource(_ context.Context, c *security.Caller, id string, req *service.UpdateDataSourceRequest) (*model.DataSource, error) {
	s.calls++
	s.gotCaller = c
	if s.err != nil {
		return nil, s.err
	}
	return &model.DataSource{ID: id, Name: *req.Name, Status: model.DataSourceStatusActive, Config: model.DataSourceConfig{Password: "secret-marker"}}, nil
}
func (s *managementStub) ActivateDataSource(_ context.Context, c *security.Caller, _ string) error {
	s.calls++
	s.gotCaller = c
	return s.err
}
func (s *managementStub) DeactivateDataSource(_ context.Context, c *security.Caller, _ string) error {
	s.calls++
	s.gotCaller = c
	return s.err
}
func TestManagementToolsRequireExplicitScopeAndRejectExtraArguments(t *testing.T) {
	stub := &managementStub{}
	srv := NewMCPServer(nil, nil, nil)
	srv.SetDataSourceManagementService(stub)
	args := map[string]any{"datasource_id": "id", "name": "new name"}
	for _, c := range []*security.Caller{nil, {UserID: "user", Scopes: []string{"data-fabric.query"}}, {UserID: "", Scopes: []string{sourceManagementScope}}} {
		ctx := context.Background()
		if c != nil {
			ctx = context.WithValue(ctx, callerContextKey{}, c)
		}
		result, err := srv.handleRenameDataSource(ctx, callToolRequest("rename_data_source", args))
		if err != nil || !result.IsError || stub.calls != 0 {
			t.Fatalf("unauthorized call: %+v %v calls=%d", result, err, stub.calls)
		}
	}
	caller := &security.Caller{UserID: "alice", Scopes: []string{sourceManagementScope}}
	ctx := context.WithValue(context.Background(), callerContextKey{}, caller)
	result, err := srv.handleRenameDataSource(ctx, callToolRequest("rename_data_source", map[string]any{"datasource_id": "id", "name": "new", "password": "secret-marker"}))
	if err != nil || !result.IsError || stub.calls != 0 {
		t.Fatal("extra credential argument must fail before the service")
	}
	result, err = srv.handleRenameDataSource(ctx, callToolRequest("rename_data_source", args))
	if err != nil || result.IsError || stub.calls != 1 || stub.gotCaller != caller {
		t.Fatalf("valid call failed: %+v %v", result, err)
	}
	if strings.Contains(resultText(t, result), "secret-marker") {
		t.Fatal("response leaked credentials")
	}
	stub.err = errors.New("sensitive backend detail secret-marker")
	result, err = srv.handleSetDataSourceStatus(ctx, callToolRequest("set_data_source_status", map[string]any{"datasource_id": "id", "status": "inactive"}))
	if err != nil || !result.IsError || strings.Contains(resultText(t, result), "secret-marker") {
		t.Fatal("backend error leaked or was not rejected")
	}
}

func TestManagementRegistrationHasRequiredFields(t *testing.T) {
	srv := NewMCPServer(nil, nil, nil)
	srv.SetDataSourceManagementService(&managementStub{})
	for name, want := range map[string][]string{"rename_data_source": {"datasource_id", "name"}, "set_data_source_status": {"datasource_id", "status"}} {
		tool, ok := srv.server.ListTools()[name]
		if !ok {
			t.Fatalf("missing %s", name)
		}
		if !equalIgnoringOrder(tool.Tool.InputSchema.Required, want) {
			t.Fatalf("%s required: %v", name, tool.Tool.InputSchema.Required)
		}
	}
}

func TestManagementRequiresMatchingOwnerAndTenant(t *testing.T) {
	stub := &managementStub{source: &model.DataSource{ID: "id", OwnerID: "alice", TenantID: "tenant-a"}}
	srv := NewMCPServer(nil, nil, nil)
	srv.SetDataSourceManagementService(stub)
	for _, caller := range []*security.Caller{
		{UserID: "alice", TenantID: "tenant-b", Scopes: []string{sourceManagementScope}},
		{UserID: "bob", TenantID: "tenant-a", Scopes: []string{sourceManagementScope}},
	} {
		ctx := context.WithValue(context.Background(), callerContextKey{}, caller)
		for _, status := range []string{"active", "inactive"} {
			result, err := srv.handleSetDataSourceStatus(ctx, callToolRequest("set_data_source_status", map[string]any{"datasource_id": "id", "status": status}))
			if err != nil || !result.IsError || stub.calls != 0 {
				t.Fatalf("unauthorized mutation: %+v, %v", result, err)
			}
		}
	}
	caller := &security.Caller{UserID: "alice", TenantID: "tenant-a", Scopes: []string{sourceManagementScope}}
	ctx := context.WithValue(context.Background(), callerContextKey{}, caller)
	result, err := srv.handleSetDataSourceStatus(ctx, callToolRequest("set_data_source_status", map[string]any{"datasource_id": "id", "status": "inactive"}))
	if err != nil || result.IsError || stub.calls != 1 {
		t.Fatalf("owner mutation failed: %+v, %v", result, err)
	}
}
