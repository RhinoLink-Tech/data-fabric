package mcp

import (
	"context"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"strings"
	"testing"
)

func TestMCPSourceToolsHideURLCredentials(t *testing.T) {
	source := sourceWithSecrets()
	source.Config.Host = "https://urluser:urlpassword@example.test/data?token=urltoken#urlfragment"
	srv := NewMCPServer(nil, listingRepoStub{sources: []*model.DataSource{source}}, &stubTableDescriber{})
	list, err := srv.handleListDataSources(context.Background(), callToolRequest("list_data_sources", nil))
	if err != nil {
		t.Fatal(err)
	}
	info, err := srv.handleGetDataSourceInfo(context.Background(), callToolRequest("get_data_source_info", map[string]any{"datasource_id": "ds-1"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []string{resultText(t, list), resultText(t, info)} {
		for _, secret := range []string{"urluser", "urlpassword", "urltoken", "urlfragment"} {
			if strings.Contains(result, secret) {
				t.Errorf("tool leaked %s", secret)
			}
		}
	}
}
