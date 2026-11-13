package controller

import (
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"testing"
)

func TestSourceResponseHidesURLCredentials(t *testing.T) {
	source := &model.DataSource{Config: model.DataSourceConfig{Host: "https://user:pass@example.test/data?token=secret#fragment"}}
	got := toDataSourceResponse(source).Config.Host
	if got != "https://redacted@example.test/data?redacted#redacted" {
		t.Fatalf("host not redacted: %q", got)
	}
}
