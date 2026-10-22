package database

import (
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestMaxComputeDriverRegistration(t *testing.T) {
	registry := NewDriverRegistry()

	// Test if MaxCompute driver is registered
	if !registry.IsSupported(model.DatabaseTypeMaxCompute) {
		t.Errorf("MaxCompute driver should be supported")
		return
	}

	driver, err := registry.GetDriver(model.DatabaseTypeMaxCompute)
	if err != nil {
		t.Errorf("Failed to get MaxCompute driver: %v", err)
		return
	}

	if driver.GetDatabaseTypeName() != string(model.DatabaseTypeMaxCompute) {
		t.Errorf("Expected database type %s, got %s", model.DatabaseTypeMaxCompute, driver.GetDatabaseTypeName())
	}

	category, err := registry.GetDriverCategory(model.DatabaseTypeMaxCompute)
	if err != nil {
		t.Errorf("Failed to get MaxCompute driver category: %v", err)
	} else if category != drivers.CategoryWarehouse {
		t.Errorf("Expected category %q, got %q", drivers.CategoryWarehouse, category)
	}

	t.Logf("MaxCompute driver successfully registered with type: %s and category: %s",
		driver.GetDatabaseTypeName(), category)
}

func TestOpenGaussRegistryDriverBuildsDSNWithoutOptionalConfig(t *testing.T) {
	driver, err := NewDriverRegistry().GetDriver(model.DatabaseTypeOpenGauss)
	if err != nil {
		t.Fatalf("GetDriver(openGauss): %v", err)
	}
	config := &model.DataSourceConfig{
		Host:     "127.0.0.1",
		Port:     5432,
		Database: "postgres",
		Username: "gaussdb",
		Password: "test-password",
	}
	dsn := driver.BuildDSN(config)
	if !strings.Contains(dsn, "sslmode=disable") {
		t.Fatalf("default openGauss DSN = %q, want sslmode=disable", dsn)
	}

	config.SSL = true
	dsn = driver.BuildDSN(config)
	if !strings.Contains(dsn, "sslmode=require") {
		t.Fatalf("TLS-enabled openGauss DSN = %q, want sslmode=require", dsn)
	}
}
