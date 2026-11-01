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

	s3JSONConfig := &model.DataSourceConfig{
		Host: "records.json", Database: "analytics",
		Username: "access", Password: "secret",
	}
	if err := checker.ValidateDataSourceConfiguration(s3JSONConfig, model.DatabaseTypeS3JSON); err != nil {
		t.Fatalf("S3 JSON config should validate through its registered driver: %v", err)
	}
	if err := checker.ValidateDataSourceConfiguration(&model.DataSourceConfig{
		Database: "analytics",
	}, model.DatabaseTypeS3JSON); err == nil || !strings.Contains(err.Error(), "object_key") {
		t.Fatalf("S3 JSON config without an object key error = %v, want object_key validation error", err)
	}
	if err := checker.ValidateDataSourceConfiguration(&model.DataSourceConfig{
		Host: "records.json", Database: "analytics", Username: "access",
	}, model.DatabaseTypeS3JSON); err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("S3 JSON partial credentials error = %v, want paired credential error", err)
	}

	s3AvroConfig := &model.DataSourceConfig{
		Host: "records.avro", Database: "analytics",
		Username: "access", Password: "secret",
	}
	if err := checker.ValidateDataSourceConfiguration(s3AvroConfig, model.DatabaseTypeS3Avro); err != nil {
		t.Fatalf("S3 Avro config should validate through its registered driver: %v", err)
	}
	if err := checker.ValidateDataSourceConfiguration(&model.DataSourceConfig{
		Database: "analytics",
	}, model.DatabaseTypeS3Avro); err == nil || !strings.Contains(err.Error(), "object_key") {
		t.Fatalf("S3 Avro config without an object key error = %v, want object_key validation error", err)
	}
	if err := checker.ValidateDataSourceConfiguration(&model.DataSourceConfig{
		Host: "records.avro", Database: "analytics", Username: "access",
	}, model.DatabaseTypeS3Avro); err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("S3 Avro partial credentials error = %v, want paired credential error", err)
	}

	s3DeltaConfig := &model.DataSourceConfig{
		Host: "delta/events", Database: "analytics", Username: "access", Password: "secret",
		AdditionalProps: map[string]interface{}{"table_path": "delta/events", "endpoint_url": "https://s3.example.test"},
	}
	if err := checker.ValidateDataSourceConfiguration(s3DeltaConfig, model.DatabaseTypeS3Delta); err != nil {
		t.Fatalf("S3 Delta config should validate through its registered driver: %v", err)
	}
	if err := checker.ValidateDataSourceConfiguration(&model.DataSourceConfig{Database: "analytics"}, model.DatabaseTypeS3Delta); err == nil || !strings.Contains(err.Error(), "table_path") {
		t.Fatalf("S3 Delta config without a table path error = %v, want table_path validation error", err)
	}
	if err := checker.ValidateDataSourceConfiguration(&model.DataSourceConfig{
		Host: "delta/events", Database: "analytics", Username: "access",
	}, model.DatabaseTypeS3Delta); err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("S3 Delta partial credentials error = %v, want paired credential error", err)
	}
	if err := checker.ValidateDataSourceConfiguration(&model.DataSourceConfig{
		Host: "../escape", Database: "analytics",
	}, model.DatabaseTypeS3Delta); err == nil {
		t.Fatal("S3 Delta config accepted a table path that escapes the bucket")
	}

	s3ORCConfig := &model.DataSourceConfig{
		Host: "records.orc", Database: "analytics",
		Username: "access", Password: "secret",
	}
	if err := checker.ValidateDataSourceConfiguration(s3ORCConfig, model.DatabaseTypeS3ORC); err != nil {
		t.Fatalf("S3 ORC config should validate through its registered driver: %v", err)
	}
	if err := checker.ValidateDataSourceConfiguration(&model.DataSourceConfig{
		Database: "analytics",
	}, model.DatabaseTypeS3ORC); err == nil || !strings.Contains(err.Error(), "object_key") {
		t.Fatalf("S3 ORC config without an object key error = %v, want object_key validation error", err)
	}
	if err := checker.ValidateDataSourceConfiguration(&model.DataSourceConfig{
		Host: "records.orc", Database: "analytics", Username: "access",
	}, model.DatabaseTypeS3ORC); err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("S3 ORC partial credentials error = %v, want paired credential error", err)
	}

	if !checker.IsSupportedDataSourceType(model.DatabaseTypeOzoneParquet) {
		t.Fatal("Ozone Parquet should be registered through its S3 Gateway adapter")
	}
	ozoneConfig := &model.DataSourceConfig{
		Host: "warehouse/events.parquet", Database: "analytics",
		Username: "access", Password: "secret",
		AdditionalProps: map[string]interface{}{"endpoint_url": "http://ozone.example.test"},
	}
	if err := checker.ValidateDataSourceConfiguration(ozoneConfig, model.DatabaseTypeOzoneParquet); err != nil {
		t.Fatalf("Ozone Parquet config should validate through its S3 Gateway adapter: %v", err)
	}
	if ozoneConfig.Port != 9878 {
		t.Fatalf("Ozone S3 Gateway default port = %d, want 9878", ozoneConfig.Port)
	}
	if err := checker.ValidateDataSourceConfiguration(&model.DataSourceConfig{
		Host: "warehouse/events.parquet", Database: "analytics", Username: "access", Password: "secret",
	}, model.DatabaseTypeOzoneParquet); err == nil || !strings.Contains(err.Error(), "endpoint_url") {
		t.Fatalf("Ozone config without endpoint error = %v, want endpoint_url validation error", err)
	}
	ozoneTextConfig := &model.DataSourceConfig{
		Host: "warehouse/events.log", Database: "analytics", Username: "access", Password: "secret",
		AdditionalProps: map[string]interface{}{"endpoint_url": "http://ozone.example.test"},
	}
	if !checker.IsSupportedDataSourceType(model.DatabaseTypeOzoneText) {
		t.Fatal("Ozone text should be registered through its S3 Gateway adapter")
	}
	if err := checker.ValidateDataSourceConfiguration(ozoneTextConfig, model.DatabaseTypeOzoneText); err != nil {
		t.Fatalf("Ozone text config should validate through its S3 Gateway adapter: %v", err)
	}
	if ozoneTextConfig.Port != 9878 {
		t.Fatalf("Ozone text S3 Gateway default port = %d, want 9878", ozoneTextConfig.Port)
	}
	ozoneAvroConfig := &model.DataSourceConfig{
		Host: "warehouse/events.avro", Database: "analytics", Username: "access", Password: "secret",
		AdditionalProps: map[string]interface{}{"endpoint_url": "http://ozone.example.test"},
	}
	if !checker.IsSupportedDataSourceType(model.DatabaseTypeOzoneAvro) {
		t.Fatal("Ozone Avro should be registered through its S3 Gateway adapter")
	}
	if err := checker.ValidateDataSourceConfiguration(ozoneAvroConfig, model.DatabaseTypeOzoneAvro); err != nil {
		t.Fatalf("Ozone Avro config should validate through its S3 Gateway adapter: %v", err)
	}
	if ozoneAvroConfig.Port != 9878 {
		t.Fatalf("Ozone Avro S3 Gateway default port = %d, want 9878", ozoneAvroConfig.Port)
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
