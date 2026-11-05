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

func TestS3DeltaDatabaseTypeIsValid(t *testing.T) {
	if !IsValidDatabaseType(string(DatabaseTypeS3Delta)) {
		t.Fatal("s3_delta should be a valid data source type")
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
		{DatabaseTypeHDFSORC, "filesystems", false},
		{DatabaseTypeClickHouse, "olap", false},
		{DatabaseTypeMongoDB, "nosql", false},
		// These later additions must retain their storage-family category.
		{DatabaseTypeOSSDelta, "object_storage", false},
		{DatabaseTypeS3Delta, "object_storage", false},
		{DatabaseTypeMinIOIceberg, "object_storage", false},
		{DatabaseTypeTencentCOSIceberg, "object_storage", false},
		{DatabaseTypeAzureDelta, "object_storage", false},
		{DatabaseTypeAzureParquet, "object_storage", false},
		{DatabaseTypeOzoneParquet, "object_storage", false},
		{DatabaseTypeOzoneORC, "object_storage", false},
		{DatabaseTypeMinIODelta, "object_storage", false},
		{DatabaseTypeMinIOORC, "object_storage", false},
		{DatabaseTypeMinIOAvro, "object_storage", false},
		{DatabaseTypeMinIOJSON, "object_storage", false},
		{DatabaseTypeOzoneText, "object_storage", false},
		{DatabaseTypeOzoneAvro, "object_storage", false},
		{DatabaseTypeOzoneJSON, "object_storage", false},
		{DatabaseTypeOzoneCSV, "object_storage", false},
		{DatabaseTypeOzoneDelta, "object_storage", false},
		{DatabaseTypeOSSCSV, "object_storage", false},
		{DatabaseTypeAlibabaOSSJSON, "object_storage", false},
		{DatabaseTypeAlibabaOSSAvro, "object_storage", false},
		{DatabaseTypeAlibabaOSSORC, "object_storage", false},
		{DatabaseTypeTencentCOSCSV, "object_storage", false},
		{DatabaseTypeTencentCOSJSON, "object_storage", false},
		{DatabaseTypeTencentCOSORC, "object_storage", false},
		{DatabaseTypeTencentCOSAvro, "object_storage", false},
		{DatabaseTypeTencentCOSDelta, "object_storage", false},
		{DatabaseTypeAzureBlobAvro, "object_storage", false},
		{DatabaseTypeAzureBlobORC, "object_storage", false},
		{DatabaseTypeAzureBlobCSV, "object_storage", false},
		{DatabaseTypeAzureBlobJSON, "object_storage", false},
		{DatabaseTypeHDFSJSON, "filesystems", false},
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
