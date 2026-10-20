package database

import (
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func TestValidateDataSourceConfigurationUsesRegisteredDriverCategory(t *testing.T) {
	checker := &HealthChecker{registry: NewDriverRegistry()}

	if !checker.IsSupportedDataSourceType(model.DatabaseTypeSQLServer) {
		t.Fatal("SQL Server should be supported by the driver registry")
	}
	if checker.IsSupportedDataSourceType(model.DatabaseType("not_registered")) {
		t.Fatal("unknown database type must not be reported as supported")
	}

	sqlServerConfig := &model.DataSourceConfig{
		Host: "sql.example.test", Database: "analytics", Username: "gateway",
	}
	if err := checker.ValidateDataSourceConfiguration(sqlServerConfig, model.DatabaseTypeSQLServer); err != nil {
		t.Fatalf("registered SQL Server config should validate: %v", err)
	}
	if sqlServerConfig.Port != 1433 {
		t.Errorf("SQL Server default port = %d, want 1433", sqlServerConfig.Port)
	}

	mongoConfig := &model.DataSourceConfig{Host: "mongo.example.test", Database: "analytics"}
	if err := checker.ValidateDataSourceConfiguration(mongoConfig, model.DatabaseTypeMongoDB); err != nil {
		t.Fatalf("MongoDB config without authentication should validate: %v", err)
	}
	if mongoConfig.Port != 27017 {
		t.Errorf("MongoDB default port = %d, want 27017", mongoConfig.Port)
	}

	s3Config := &model.DataSourceConfig{Host: "us-east-1", Database: "analytics"}
	if err := checker.ValidateDataSourceConfiguration(s3Config, model.DatabaseTypeS3Parquet); err != nil {
		t.Fatalf("S3 config should validate through its registered driver: %v", err)
	}
	if s3Config.Port != 443 {
		t.Errorf("S3 default port = %d, want 443", s3Config.Port)
	}

	if checker.IsSupportedDataSourceType(model.DatabaseTypeOzoneParquet) {
		t.Fatal("Ozone must stay unregistered until its object operations are implemented")
	}
	if err := checker.ValidateDataSourceConfiguration(&model.DataSourceConfig{Host: "ozone.example.test"}, model.DatabaseTypeOzoneParquet); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Ozone validation error = %v, want unsupported-type error", err)
	}

	invalidPort := &model.DataSourceConfig{
		Host: "sql.example.test", Port: 70000, Database: "analytics", Username: "gateway",
	}
	if err := checker.ValidateDataSourceConfiguration(invalidPort, model.DatabaseTypeSQLServer); err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("invalid port error = %v, want a port validation error", err)
	}

	if err := checker.ValidateDataSourceConfiguration(sqlServerConfig, model.DatabaseType("not_registered")); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported type error = %v, want an unsupported type error", err)
	}
}
