package model

import (
	"database/sql/driver"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type DatabaseType string

const (
	// Existing types (Phase 0)
	DatabaseTypeMySQL      DatabaseType = "mysql"
	DatabaseTypeMariaDB    DatabaseType = "mariadb"
	DatabaseTypePostgreSQL DatabaseType = "postgresql"
	DatabaseTypeOracle     DatabaseType = "oracle"

	// Data Lake Table Formats (Phase 1)
	DatabaseTypeApacheIceberg DatabaseType = "iceberg"
	DatabaseTypeDeltaLake     DatabaseType = "delta_lake"
	DatabaseTypeApacheHudi    DatabaseType = "hudi"

	// Cloud Data Warehouses (Phase 1)
	DatabaseTypeSnowflake  DatabaseType = "snowflake"
	DatabaseTypeDatabricks DatabaseType = "databricks"
	DatabaseTypeRedshift   DatabaseType = "redshift"
	DatabaseTypeBigQuery   DatabaseType = "bigquery"

	// Object Storage (Phase 1)
	DatabaseTypeS3Parquet         DatabaseType = "s3_parquet"
	DatabaseTypeS3ORC             DatabaseType = "s3_orc"
	DatabaseTypeS3Avro            DatabaseType = "s3_avro"
	DatabaseTypeS3CSV             DatabaseType = "s3_csv"
	DatabaseTypeS3JSON            DatabaseType = "s3_json"
	DatabaseTypeMinIOParquet      DatabaseType = "minio_parquet"
	DatabaseTypeMinIOCSV          DatabaseType = "minio_csv"
	DatabaseTypeAlibabaOSSParquet DatabaseType = "oss_parquet"
	DatabaseTypeTencentCOSParquet DatabaseType = "cos_parquet"
	DatabaseTypeAzureBlobParquet  DatabaseType = "azure_blob_parquet"

	// Missing constants for compilation
	DatabaseTypeOSSDelta              DatabaseType = "oss_delta"
	DatabaseTypeMinIOIceberg          DatabaseType = "minio_iceberg"
	DatabaseTypeAzureDelta            DatabaseType = "azure_delta"
	DatabaseTypeOzoneText             DatabaseType = "ozone_text"
	DatabaseTypeOzoneAvro             DatabaseType = "ozone_avro"
	DatabaseTypeAzureParquet          DatabaseType = "azure_parquet"
	DatabaseTypeHDFSText              DatabaseType = "hdfs_text"
	DatabaseTypeHDFSParquetCompressed DatabaseType = "hdfs_parquet_compressed"
	DatabaseTypeMinIODelta            DatabaseType = "minio_delta"

	// Distributed File Systems (Phase 1)
	DatabaseTypeHDFSAvro     DatabaseType = "hdfs_avro"
	DatabaseTypeHDFSParquet  DatabaseType = "hdfs_parquet"
	DatabaseTypeHDFSCSV      DatabaseType = "hdfs_csv"
	DatabaseTypeOzoneParquet DatabaseType = "ozone_parquet"

	// OLAP Engines (Phase 1)
	DatabaseTypeClickHouse  DatabaseType = "clickhouse"
	DatabaseTypeApacheDoris DatabaseType = "doris"
	DatabaseTypeStarRocks   DatabaseType = "starrocks"
	DatabaseTypeApacheDruid DatabaseType = "druid"
	DatabaseTypeHologres    DatabaseType = "hologres"

	// Domestic Chinese Databases (Phase 1)
	DatabaseTypeOceanBaseMySQL  DatabaseType = "oceanbase_mysql"
	DatabaseTypeOceanBaseOracle DatabaseType = "oceanbase_oracle"
	DatabaseTypeTiDB            DatabaseType = "tidb"
	DatabaseTypeTDSQL           DatabaseType = "tdsql"
	DatabaseTypeGaussDBMySQL    DatabaseType = "gaussdb_mysql"
	DatabaseTypeGaussDBPostgres DatabaseType = "gaussdb_postgres"
	DatabaseTypeDaMeng          DatabaseType = "dameng"
	DatabaseTypeKingbaseES      DatabaseType = "kingbasees"
	DatabaseTypeGBase8s         DatabaseType = "gbase_8s"
	DatabaseTypeGBase8t         DatabaseType = "gbase_8t"
	DatabaseTypeOscar           DatabaseType = "oscar"
	DatabaseTypeOpenGauss       DatabaseType = "opengauss"

	// Big Data Warehouses
	DatabaseTypeApacheHive DatabaseType = "hive"

	// Traditional databases
	DatabaseTypeSQLServer DatabaseType = "sqlserver"

	// NoSQL databases
	DatabaseTypeMongoDB DatabaseType = "mongodb"

	// Big Data Processing Platforms
	DatabaseTypeMaxCompute DatabaseType = "maxcompute"
)

type DataSourceStatus string

const (
	DataSourceStatusActive   DataSourceStatus = "active"
	DataSourceStatusInactive DataSourceStatus = "inactive"
	DataSourceStatusError    DataSourceStatus = "error"
)

// DataSource represents a database connection configuration
type DataSource struct {
	ID        string           `gorm:"type:char(36);primaryKey" json:"id"`
	Name      string           `gorm:"size:255;not null;uniqueIndex" json:"name"`
	Type      DatabaseType     `gorm:"type:enum('mysql','mariadb','postgresql','oracle','iceberg','delta_lake','hudi','snowflake','databricks','redshift','bigquery','s3_parquet','s3_orc','s3_avro','s3_csv','s3_json','minio_parquet','minio_csv','oss_parquet','cos_parquet','azure_blob_parquet','hdfs_avro','hdfs_parquet','hdfs_csv','ozone_parquet','clickhouse','doris','starrocks','druid','oceanbase_mysql','oceanbase_oracle','tidb','tdsql','gaussdb_mysql','gaussdb_postgres','dameng','kingbasees','gbase_8s','gbase_8t','oscar','opengauss','oss_delta','minio_iceberg','azure_delta','ozone_text','ozone_avro','azure_parquet','hdfs_text','hdfs_parquet_compressed','minio_delta','hive','sqlserver','hologres','mongodb','maxcompute');not null" json:"type"`
	Config    DataSourceConfig `gorm:"type:json;not null" json:"config"`
	Status    DataSourceStatus `gorm:"type:enum('active','inactive','error');default:'active'" json:"status"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

// DataSourceConfig holds the connection configuration for a data source
type DataSourceConfig struct {
	Host            string                 `json:"host" validate:"required"`
	Port            int                    `json:"port" validate:"required,min=1,max=65535"`
	Database        string                 `json:"database" validate:"required"`
	Schema          string                 `json:"schema,omitempty"`
	Username        string                 `json:"username" validate:"required"`
	Password        string                 `json:"password"` // Never include this field in API response DTOs
	SSL             bool                   `json:"ssl"`
	Timeout         int                    `json:"timeout"`     // Connection timeout in seconds, default 30
	MaxPoolSize     int                    `json:"maxPoolSize"` // Maximum pool size, default 10
	IdleTimeout     int                    `json:"idleTimeout"` // Idle timeout in seconds, default 600
	MaxLifetime     int                    `json:"maxLifetime"` // Max connection lifetime in seconds, default 1800
	Timezone        string                 `json:"timezone"`    // Database timezone
	AdditionalProps map[string]interface{} `json:"additionalProps,omitempty"`
}

// Value implements driver.Valuer interface for GORM
func (dsc DataSourceConfig) Value() (driver.Value, error) {
	return json.Marshal(dsc)
}

// Scan implements sql.Scanner interface for GORM
func (dsc *DataSourceConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return json.Unmarshal([]byte(v.(string)), dsc)
	}

	return json.Unmarshal(bytes, dsc)
}

// TableName returns the table name for the DataSource model
func (DataSource) TableName() string {
	return "data_sources"
}

// BeforeCreate generates a new UUID if ID is empty
func (ds *DataSource) BeforeCreate(tx *gorm.DB) error {
	if ds.ID == "" {
		ds.ID = uuid.New().String()
	}
	return nil
}

// IsSensitiveDataSourceProperty reports whether an additional configuration key may contain a secret.
func IsSensitiveDataSourceProperty(key string) bool {
	normalized := strings.ToLower(strings.NewReplacer("-", "_", ".", "_").Replace(key))
	for _, marker := range []string{
		"password", "passwd", "token", "secret", "credential", "authorization",
		"privatekey", "private_key", "accesskey", "access_key", "apikey", "api_key",
		"keytab", "credentialsjson", "credentials_json", "ssl_key", "ssl_cert", "ssl_ca",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

// IsValidDatabaseType checks if a database type is valid
func IsValidDatabaseType(dbType string) bool {
	switch DatabaseType(dbType) {
	case DatabaseTypeMySQL, DatabaseTypeMariaDB, DatabaseTypePostgreSQL, DatabaseTypeOracle,
		DatabaseTypeApacheIceberg, DatabaseTypeDeltaLake, DatabaseTypeApacheHudi,
		DatabaseTypeSnowflake, DatabaseTypeDatabricks, DatabaseTypeRedshift, DatabaseTypeBigQuery,
		DatabaseTypeS3Parquet, DatabaseTypeS3ORC, DatabaseTypeS3Avro, DatabaseTypeS3CSV, DatabaseTypeS3JSON,
		DatabaseTypeMinIOParquet, DatabaseTypeMinIOCSV, DatabaseTypeAlibabaOSSParquet, DatabaseTypeTencentCOSParquet, DatabaseTypeAzureBlobParquet,
		DatabaseTypeHDFSAvro, DatabaseTypeHDFSParquet, DatabaseTypeHDFSCSV, DatabaseTypeOzoneParquet,
		DatabaseTypeClickHouse, DatabaseTypeApacheDoris, DatabaseTypeStarRocks, DatabaseTypeApacheDruid,
		DatabaseTypeOceanBaseMySQL, DatabaseTypeOceanBaseOracle, DatabaseTypeTiDB, DatabaseTypeTDSQL,
		DatabaseTypeGaussDBMySQL, DatabaseTypeGaussDBPostgres, DatabaseTypeDaMeng, DatabaseTypeKingbaseES,
		DatabaseTypeGBase8s, DatabaseTypeGBase8t, DatabaseTypeOscar, DatabaseTypeOpenGauss,
		DatabaseTypeOSSDelta, DatabaseTypeMinIOIceberg, DatabaseTypeAzureDelta, DatabaseTypeOzoneText,
		DatabaseTypeOzoneAvro, DatabaseTypeAzureParquet, DatabaseTypeHDFSText, DatabaseTypeHDFSParquetCompressed,
		DatabaseTypeMinIODelta, DatabaseTypeApacheHive, DatabaseTypeSQLServer, DatabaseTypeHologres, DatabaseTypeMongoDB, DatabaseTypeMaxCompute:
		return true
	default:
		return false
	}
}

// GetDatabaseCategory returns the category of a database type
func GetDatabaseCategory(dbType DatabaseType) string {
	switch dbType {
	case DatabaseTypeApacheIceberg, DatabaseTypeDeltaLake, DatabaseTypeApacheHudi:
		return "table_formats"
	case DatabaseTypeSnowflake, DatabaseTypeDatabricks, DatabaseTypeRedshift, DatabaseTypeBigQuery, DatabaseTypeApacheHive, DatabaseTypeMaxCompute:
		return "warehouses"
	case DatabaseTypeS3Parquet, DatabaseTypeS3ORC, DatabaseTypeS3Avro, DatabaseTypeS3CSV, DatabaseTypeS3JSON,
		DatabaseTypeMinIOParquet, DatabaseTypeMinIOCSV, DatabaseTypeAlibabaOSSParquet, DatabaseTypeTencentCOSParquet, DatabaseTypeAzureBlobParquet:
		return "object_storage"
	case DatabaseTypeHDFSAvro, DatabaseTypeHDFSParquet, DatabaseTypeHDFSCSV, DatabaseTypeOzoneParquet:
		return "filesystems"
	case DatabaseTypeClickHouse, DatabaseTypeApacheDoris, DatabaseTypeStarRocks, DatabaseTypeApacheDruid, DatabaseTypeHologres:
		return "olap"
	case DatabaseTypeMongoDB:
		return "nosql"
	case DatabaseTypeOceanBaseMySQL, DatabaseTypeOceanBaseOracle, DatabaseTypeTiDB, DatabaseTypeTDSQL,
		DatabaseTypeGaussDBMySQL, DatabaseTypeGaussDBPostgres, DatabaseTypeDaMeng, DatabaseTypeKingbaseES,
		DatabaseTypeGBase8s, DatabaseTypeGBase8t, DatabaseTypeOscar, DatabaseTypeOpenGauss:
		return "domestic"
	default:
		return "traditional"
	}
}

// RequiresSQLProtocol checks if a database type uses standard SQL protocol (MySQL/PostgreSQL compatible)
func RequiresSQLProtocol(dbType DatabaseType) bool {
	switch dbType {
	case DatabaseTypeOceanBaseMySQL, DatabaseTypeTiDB, DatabaseTypeTDSQL,
		DatabaseTypeGaussDBMySQL, DatabaseTypeGBase8t:
		return true // MySQL protocol
	case DatabaseTypeGaussDBPostgres, DatabaseTypeKingbaseES,
		DatabaseTypeOscar, DatabaseTypeOpenGauss:
		return true // PostgreSQL protocol
	default:
		return false
	}
}
