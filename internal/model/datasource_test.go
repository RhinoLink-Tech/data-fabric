package model

import "testing"

func TestQueryEngineDatabaseTypes(t *testing.T) {
	for _, dbType := range []DatabaseType{
		DatabaseTypeTrino,
		DatabaseTypePresto,
		DatabaseTypeOpenLooKeng,
	} {
		t.Run(string(dbType), func(t *testing.T) {
			if !IsValidDatabaseType(string(dbType)) {
				t.Fatalf("%s should be a valid data source type", dbType)
			}
			if got := GetDatabaseCategory(dbType); got != "query_engine" {
				t.Fatalf("unexpected %s category: %q", dbType, got)
			}
		})
	}
}

func TestDatabaseTypeMetadata(t *testing.T) {
	tests := []struct {
		dbType              DatabaseType
		category            string
		requiresSQLProtocol bool
	}{
		{DatabaseTypeMySQL, "traditional", false},
		{DatabaseTypeMariaDB, "traditional", false},
		{DatabaseTypePostgreSQL, "traditional", false},
		{DatabaseTypeOracle, "traditional", false},
		{DatabaseTypeApacheIceberg, "table_formats", false},
		{DatabaseTypeBigQuery, "warehouses", false},
		{DatabaseTypeS3CSV, "object_storage", false},
		{DatabaseTypeHDFSParquet, "filesystems", false},
		{DatabaseTypeClickHouse, "olap", false},
		{DatabaseTypeMongoDB, "nosql", false},
		// These later additions must retain their storage-family category.
		{DatabaseTypeOSSDelta, "object_storage", false},
		{DatabaseTypeMinIOIceberg, "object_storage", false},
		{DatabaseTypeAzureDelta, "object_storage", false},
		{DatabaseTypeAzureParquet, "object_storage", false},
		{DatabaseTypeMinIODelta, "object_storage", false},
		{DatabaseTypeOzoneText, "filesystems", false},
		{DatabaseTypeOzoneAvro, "filesystems", false},
		{DatabaseTypeHDFSText, "filesystems", false},
		{DatabaseTypeHDFSParquetCompressed, "filesystems", false},
		// RequiresSQLProtocol describes the legacy domestic compatibility set.
		{DatabaseTypeOceanBaseMySQL, "domestic", true},
		{DatabaseTypeOceanBaseOracle, "domestic", false},
		{DatabaseTypeTiDB, "domestic", true},
		{DatabaseTypeTDSQL, "domestic", true},
		{DatabaseTypeGaussDBMySQL, "domestic", true},
		{DatabaseTypeGaussDBPostgres, "domestic", true},
		{DatabaseTypeDaMeng, "domestic", false},
		{DatabaseTypeKingbaseES, "domestic", true},
		{DatabaseTypeGBase8s, "domestic", false},
		{DatabaseTypeGBase8t, "domestic", true},
		{DatabaseTypeOscar, "domestic", true},
		{DatabaseTypeOpenGauss, "domestic", true},
	}

	for _, tt := range tests {
		t.Run(string(tt.dbType), func(t *testing.T) {
			if !IsValidDatabaseType(string(tt.dbType)) {
				t.Errorf("IsValidDatabaseType(%q) = false, want true", tt.dbType)
			}
			if got := GetDatabaseCategory(tt.dbType); got != tt.category {
				t.Errorf("GetDatabaseCategory(%q) = %q, want %q", tt.dbType, got, tt.category)
			}
			if got := RequiresSQLProtocol(tt.dbType); got != tt.requiresSQLProtocol {
				t.Errorf("RequiresSQLProtocol(%q) = %v, want %v", tt.dbType, got, tt.requiresSQLProtocol)
			}
		})
	}
}

func TestInvalidDatabaseTypeMetadata(t *testing.T) {
	for _, dbType := range []string{
		"", "unknown", "MySQL", "MYSQL", "PostgreSQL", " mysql", "mysql ", "\tpostgresql\n",
	} {
		t.Run(dbType, func(t *testing.T) {
			if IsValidDatabaseType(dbType) {
				t.Errorf("IsValidDatabaseType(%q) = true, want false", dbType)
			}
			if got := GetDatabaseCategory(DatabaseType(dbType)); got != "traditional" {
				t.Errorf("GetDatabaseCategory(%q) = %q, want traditional fallback", dbType, got)
			}
			if RequiresSQLProtocol(DatabaseType(dbType)) {
				t.Errorf("RequiresSQLProtocol(%q) = true, want false", dbType)
			}
		})
	}
}
