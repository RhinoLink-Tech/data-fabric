# Phase 1 Data Model: Enhanced Single-Source Capabilities

**Feature**: Phase 1 Enhanced Single-Source Capabilities
**Date**: 2025-12-24
**Purpose**: Define data entities, relationships, validation rules, and state transitions for Phase 1 implementation

## Overview

Phase 1 extends the existing `DataSource` model to support 30+ new data source types across 6 categories. The data model is additive - no existing entities are modified in breaking ways. All changes maintain backward compatibility with existing MySQL/PostgreSQL/Oracle data sources.

---

## Entity Relationship Diagram

```text
┌─────────────────────────────────────────────────────────────────────────────┐
│                            data_sources (TABLE)                              │
├─────────────────────────────────────────────────────────────────────────────┤
│ id (PK, CHAR(36))                                                           │
│ name (VARCHAR(255), UNIQUE)                                                  │
│ type (ENUM - 30+ values extended)                                           │
│ config (JSON - DataSourceConfig extensions)                                 │
│ status (ENUM: active, inactive, error)                                      │
│ created_at (TIMESTAMP)                                                       │
│ updated_at (TIMESTAMP)                                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│ Relationships:                                                              │
│   - One-to-Many → query_history (data_source_id)                            │
│   - One-to-Many → connection_pool (data_source_id)                          │
│   - One-to-One → data_source_schema (data_source_id, cached metadata)       │
└─────────────────────────────────────────────────────────────────────────────┘
                                    │
                                    │ 1:N
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                            query_history (TABLE)                             │
├─────────────────────────────────────────────────────────────────────────────┤
│ id (PK, CHAR(36))                                                           │
│ data_source_id (FK, CHAR(36))                                               │
│ user_id (CHAR(36), nullable)                                                │
│ sql (TEXT)                                                                  │
│ parameters (JSON, nullable)                                                  │
│ execution_time_ms (INT)                                                     │
│ row_count (INT)                                                             │
│ status (ENUM: success, error, timeout)                                      │
│ error_message (TEXT, nullable)                                              │
│ correlation_id (CHAR(36))                                                   │
│ created_at (TIMESTAMP)                                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│ Relationships:                                                              │
│   - Many-to-One → data_sources (id)                                         │
└─────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────┐
│                    data_source_schema (NEW TABLE - Optional)                │
├─────────────────────────────────────────────────────────────────────────────┤
│ data_source_id (PK, FK, CHAR(36))                                          │
│ schema_metadata (JSON - table/column/index definitions)                    │
│ schema_version (VARCHAR(50) - e.g., Iceberg snapshot ID)                   │
│ last_refreshed_at (TIMESTAMP)                                               │
│ created_at (TIMESTAMP)                                                       │
│ updated_at (TIMESTAMP)                                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│ Purpose: Cache schema metadata for query validation and optimization        │
└─────────────────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────────────────┐
│                   credential_vault (NEW TABLE - Optional)                    │
├─────────────────────────────────────────────────────────────────────────────┤
│ id (PK, CHAR(36))                                                           │
│ data_source_id (FK, CHAR(36), UNIQUE)                                       │
│ encrypted_credentials (BLOB/TEXT - AES-256-GCM encrypted)                  │
│ encryption_key_id (VARCHAR(100) - KMS key identifier)                       │
│ auth_type (ENUM: basic, oauth2, iam, kerberos, jwt)                        │
│ token_expires_at (TIMESTAMP, nullable)                                      │
│ last_rotated_at (TIMESTAMP, nullable)                                       │
│ created_at (TIMESTAMP)                                                       │
│ updated_at (TIMESTAMP)                                                       │
├─────────────────────────────────────────────────────────────────────────────┤
│ Purpose: Secure credential storage with auto-rotation support               │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Entity Definitions

### 1. DataSource (Extended)

**Existing Entity** - Extended to support 30+ new data source types.

**Purpose**: Represents a connection configuration for a single data storage system.

**Table Name**: `data_sources`

**Primary Key**: `id (CHAR(36))` - UUID

**Fields**:

| Field | Type | Constraints | Description | Phase 1 Changes |
|-------|------|-------------|-------------|-----------------|
| `id` | CHAR(36) | PK, NOT NULL | UUID identifier | No change |
| `name` | VARCHAR(255) | NOT NULL, UNIQUE | Human-readable name | No change |
| `type` | ENUM | NOT NULL | Data source type identifier | ⚠️ **EXTENDED** - 30+ new values |
| `config` | JSON | NOT NULL | Connection configuration | ⚠️ **EXTENDED** - New config schemas |
| `status` | ENUM | NOT NULL, DEFAULT 'active' | Connection health status | No change |
| `created_at` | TIMESTAMP | NOT NULL, DEFAULT NOW() | Creation timestamp | No change |
| `updated_at` | TIMESTAMP | NOT NULL, AUTO UPDATE | Last update timestamp | No change |

---

#### DatabaseType Enum Extension (CRITICAL CHANGE)

**Existing Values**:
```go
const (
    DatabaseTypeMySQL      DatabaseType = "mysql"
    DatabaseTypeMariaDB    DatabaseType = "mariadb"
    DatabaseTypePostgreSQL DatabaseType = "postgresql"
    DatabaseTypeOracle     DatabaseType = "oracle"
)
```

**Phase 1 Additions** (30+ new values):

```go
// Data Lake Table Formats
const (
    DatabaseTypeApacheIceberg  DatabaseType = "iceberg"
    DatabaseTypeDeltaLake      DatabaseType = "delta_lake"
    DatabaseTypeApacheHudi     DatabaseType = "hudi"
)

// Cloud Data Warehouses
const (
    DatabaseTypeSnowflake   DatabaseType = "snowflake"
    DatabaseTypeDatabricks  DatabaseType = "databricks"
    DatabaseTypeRedshift    DatabaseType = "redshift"
    DatabaseTypeBigQuery    DatabaseType = "bigquery"
)

// Object Storage (file formats)
const (
    DatabaseTypeS3Parquet        DatabaseType = "s3_parquet"
    DatabaseTypeS3ORC            DatabaseType = "s3_orc"
    DatabaseTypeS3Delta          DatabaseType = "s3_delta"
    DatabaseTypeS3Avro           DatabaseType = "s3_avro"
    DatabaseTypeS3CSV            DatabaseType = "s3_csv"
    DatabaseTypeS3JSON           DatabaseType = "s3_json"
    DatabaseTypeMinIOParquet     DatabaseType = "minio_parquet"
    DatabaseTypeMinIOCSV         DatabaseType = "minio_csv"
    DatabaseTypeMinIOORC         DatabaseType = "minio_orc"
    DatabaseTypeMinIOAvro        DatabaseType = "minio_avro"
    DatabaseTypeMinIOJSON        DatabaseType = "minio_json"
    DatabaseTypeAlibabaOSSParquet DatabaseType = "oss_parquet"
    DatabaseTypeTencentCOSParquet DatabaseType = "cos_parquet"
    DatabaseTypeAzureBlobAvro DatabaseType = "azure_blob_avro"
    DatabaseTypeAzureBlobORC DatabaseType = "azure_blob_orc"
    DatabaseTypeAzureBlobCSV DatabaseType = "azure_blob_csv"
    DatabaseTypeAzureBlobJSON DatabaseType = "azure_blob_json"
    DatabaseTypeAzureBlobParquet DatabaseType = "azure_blob_parquet"
    DatabaseTypeAzureParquet DatabaseType = "azure_parquet"
)

// Distributed File Systems
const (
    DatabaseTypeHDFSAvro    DatabaseType = "hdfs_avro"
    DatabaseTypeHDFSParquet DatabaseType = "hdfs_parquet"
    DatabaseTypeHDFSCSV     DatabaseType = "hdfs_csv"
    DatabaseTypeOzoneParquet DatabaseType = "ozone_parquet"
     DatabaseTypeOzoneORC DatabaseType = "ozone_orc"
)

// OLAP Engines
const (
    DatabaseTypeClickHouse  DatabaseType = "clickhouse"
    DatabaseTypeApacheDoris DatabaseType = "doris"
    DatabaseTypeStarRocks   DatabaseType = "starrocks"
    DatabaseTypeApacheDruid DatabaseType = "druid"
)

// Domestic Chinese Databases
const (
    DatabaseTypeOceanBaseMySQL   DatabaseType = "oceanbase_mysql"
    DatabaseTypeOceanBaseOracle  DatabaseType = "oceanbase_oracle"
    DatabaseTypeTiDB             DatabaseType = "tidb"
    DatabaseTypeTDSQL            DatabaseType = "tdsql"
    DatabaseTypeGaussDBMySQL     DatabaseType = "gaussdb_mysql"
    DatabaseTypeGaussDBPostgres  DatabaseType = "gaussdb_postgres"
    DatabaseTypeDaMeng           DatabaseType = "dameng"
    DatabaseTypeKingbaseES       DatabaseType = "kingbasees"
    DatabaseTypeGBase8s          DatabaseType = "gbase_8s"
    DatabaseTypeGBase8t          DatabaseType = "gbase_8t"
    DatabaseTypeOscar            DatabaseType = "oscar"
    DatabaseTypeOpenGauss        DatabaseType = "opengauss"
)
```

**Migration Strategy**:
- **ALTER TABLE**: Extend ENUM type (PostgreSQL: `ALTER TYPE ... ADD VALUE`, MySQL: `MODIFY ENUM`)
- **Backward Compatibility**: Existing values unchanged, new values additive
- **Database Migration**: Generate migration script in Phase 2

---

#### DataSourceConfig JSON Extensions

**Existing Schema** (Traditional databases):
```json
{
  "host": "string",
  "port": "int (1-65535)",
  "database": "string",
  "username": "string",
  "password": "string",
  "ssl": "bool",
  "timeout": "int (seconds)",
  "maxPoolSize": "int",
  "idleTimeout": "int (seconds)",
  "maxLifetime": "int (seconds)",
  "timezone": "string",
  "additionalProps": "map<string, any>"
}
```

**Phase 1 Extension Strategy**:
- Reuse existing fields where applicable (`host`, `port`, `username`, `password`, `ssl`)
- Add source-specific fields via `additionalProps` map
- Define config schemas per data source category (see below)

---

##### Data Lake Table Format Configs

**Apache Iceberg**:
```json
{
  "catalogType": "string (rest|hive|glue)",
  "catalogUri": "string (REST catalog URL or Hive metastore URI)",
  "warehousePath": "string (s3://bucket/warehouse or hdfs://...)",
  "namespace": "string (default namespace)",
  "authentication": {
    "type": "string (basic|oauth2|iam)",
    "credentials": "...",
    "awsRegion": "string (for S3-backed catalogs)"
  },
  "additionalProps": {
    "snapshotId": "string (optional - for time travel queries)",
    "schemaVersion": "string (optional - for schema evolution)"
  }
}
```

**Delta Lake**:
```json
{
  "tablePath": "string (s3://bucket/path or dbfs:/path)",
  "catalogType": "string (unity|hive|databricks)",
  "workspaceUrl": "string (Databricks workspace URL)",
  "warehouseId": "string (SQL warehouse ID)",
  "authentication": {
    "type": "string (pat|oauth2)",
    "token": "string (Personal Access Token or OAuth2 token)",
    "databricksInstanceId": "string (optional)"
  },
  "additionalProps": {
    "version": "string (optional - for time travel, e.g., AS OF version)",
    "timestamp": "string (optional - for time travel, e.g., AS OF timestamp)"
  }
}
```

**Apache Hudi**:
```json
{
  "tablePath": "string (s3://bucket/path or hdfs://...)",
  "baseFileFormat": "string (parquet|orc)",
  "tableType": "string (cow|mor - Copy-On-Write or Merge-On-Read)",
  "authentication": {
    "type": "string (basic|iam|kerberos)",
    "credentials": "..."
  },
  "additionalProps": {
    "beginInstantTime": "string (optional - for incremental queries)",
    "endInstantTime": "string (optional)"
  }
}
```

---

##### Cloud Data Warehouse Configs

**Snowflake**:
```json
{
  "account": "string (e.g., xy12345.us-east-1)",
  "warehouse": "string (warehouse name)",
  "database": "string",
  "schema": "string",
  "username": "string",
  "password": "string",
  "role": "string (optional - default role)",
  "authentication": {
    "type": "string (password|keypair|oauth)",
    "privateKey": "string (for keypair auth)",
    "oauthToken": "string (for OAuth)"
  },
  "additionalProps": {
    "region": "string",
    "params": {
      "application": "string",
      "authenticator": "string (snowflake|okta|...)"
    }
  }
}
```

**Databricks**:
```json
{
  "workspaceUrl": "string (e.g., https://mycompany.cloud.databricks.com)",
  "warehouseId": "string (SQL warehouse ID)",
  "clusterId": "string (optional - for interactive clusters)",
  "catalog": "string (optional - Unity Catalog)",
  "database": "string",
  "schema": "string",
  "httpPath": "string (HTTP path for warehouse/cluster)",
  "authentication": {
    "type": "string (pat|oauth2)",
    "token": "string (Personal Access Token)"
  },
  "additionalProps": {
    "port": "int (443)",
    "protocol": "string (https)"
  }
}
```

**Amazon Redshift**:
```json
{
  "host": "string (cluster endpoint)",
  "port": "int (default 5439)",
  "database": "string",
  "username": "string",
  "password": "string",
  "ssl": "bool (default true)",
  "authentication": {
    "type": "string (password|iam)",
    "iamRoleArn": "string (for IAM auth)",
    "awsRegion": "string"
  },
  "additionalProps": {
    "dbname": "string",
    "sslmode": "string (require|verify-full)"
  }
}
```

**Google BigQuery**:
```json
{
  "project": "string (GCP project ID)",
  "dataset": "string (BigQuery dataset)",
  "authentication": {
    "type": "string (oauth2|serviceAccount)",
    "credentialsJson": "string (service account JSON)",
    "oauthToken": "string (OAuth2 token)"
  },
  "additionalProps": {
    "location": "string (e.g., US, EU)",
    "defaultExpirationTime": "int (milliseconds)",
    "maxBytesBilled": "string (e.g., '1000000000' - cost control)"
  }
}
```

---

##### Object Storage Configs

**AWS S3 (for file querying)**:
```json
{
  "bucket": "string",
  "region": "string",
  "prefix": "string (optional - folder prefix)",
  "fileFormat": "string (parquet|orc|avro|csv|json)",
  "authentication": {
    "type": "string (accessKey|iam)",
    "accessKeyId": "string",
    "secretAccessKey": "string",
    "sessionToken": "string (optional - for temporary credentials)",
    "iamRoleArn": "string (for IAM auth)"
  },
  "additionalProps": {
    "endpoint": "string (optional - for S3-compatible services)",
    "delimiter": "string (for CSV - default ',')",
    "headerRow": "bool (for CSV - default true)",
    "schema": "object (explicit schema for CSV/JSON)"
  }
}
```

**MinIO**:
```json
{
  "endpoint": "string (e.g., http://localhost:9000)",
  "bucket": "string",
  "prefix": "string (optional)",
  "fileFormat": "string (parquet|orc|avro|csv|json)",
  "authentication": {
    "type": "string (accessKey)",
    "accessKeyId": "string",
    "secretAccessKey": "string"
  },
  "additionalProps": {
    "useSSL": "bool (default false)"
  }
}
```

**Alibaba OSS / Tencent COS / Azure Blob**:
```json
{
  "endpoint": "string (OSS: oss-cn-hangzhou.aliyuncs.com, COS: cos.ap-region.myqcloud.com)",
  "bucket": "string",
  "prefix": "string (optional)",
  "fileFormat": "string (parquet|orc|avro|csv|json)",
  "authentication": {
    "type": "string (accessKey|sasToken)",
    "accessKeyId": "string",
    "secretAccessKey": "string",
    "sasToken": "string (for Azure Blob SAS)"
  },
  "additionalProps": {
    "region": "string",
    "storageAccount": "string (for Azure Blob)"
  }
}
```

---

##### HDFS / Apache Ozone Configs

**HDFS**:
```json
{
  "namenodeHost": "string",
  "namenodePort": "int (default 8020)",
  "path": "string (HDFS path)",
  "fileFormat": "string (parquet|orc|avro|csv|json)",
  "authentication": {
    "type": "string (basic|kerberos)",
    "username": "string (for basic)",
    "kerberosPrincipal": "string (for Kerberos)",
    "kerberosKeytab": "string (base64-encoded keytab)",
    "kerberosRealm": "string",
    "kerberosKdc": "string"
  },
  "additionalProps": {
    "nameservice": "string (for HA - e.g., mycluster)",
    "haNamenodes": "array (e.g., ['nn1', 'nn2'])",
    "enableShortCircuit": "bool (default false)"
  }
}
```

**Apache Ozone**:
```json
{
  "endpoint": "string (e.g., http://ozone1:9880)",
  "bucket": "string (volume/bucket)",
  "prefix": "string (optional)",
  "fileFormat": "string (parquet|orc|avro|csv|json)",
  "authentication": {
    "type": "string (accessKey|kerberos)",
    "accessKeyId": "string",
    "secretAccessKey": "string"
  },
  "additionalProps": {
    "useS3A": "bool (use S3A-compatible protocol)",
    "omHost": "string (Ozone Manager host)",
    "omPort": "int (default 9880)"
  }
}
```

---

##### OLAP Engine Configs

**ClickHouse**:
```json
{
  "host": "string",
  "port": "int (native: 9000, HTTP: 8123)",
  "protocol": "string (native|http)",
  "database": "string",
  "username": "string (default default)",
  "password": "string",
  "ssl": "bool (default false)",
  "additionalProps": {
    "compression": "bool (default true)",
    "queryTimeout": "int (seconds)",
    "maxExecutionTime": "int (seconds)"
  }
}
```

**Apache Doris / StarRocks** (MySQL-compatible):
```json
{
  "host": "string (FE host)",
  "port": "int (default 9030)",
  "database": "string",
  "username": "string",
  "password": "string",
  "additionalProps": {
    "queryTimeout": "int (seconds)",
    "loadTimezone": "string"
  }
}
```

**Apache Druid**:
```json
{
  "brokerHost": "string",
  "brokerPort": "int (default 8082)",
  "coordinatorHost": "string (optional - for metadata queries)",
  "coordinatorPort": "int (default 8081)",
  "authentication": {
    "type": "string (basic|oauth2)",
    "username": "string",
    "password": "string"
  },
  "additionalProps": {
    "context": {
      "queryTimeout": "int (milliseconds, default 300000)",
      "priority": "int (default 0)"
    }
  }
}
```

---

##### Domestic Database Configs

**OceanBase (MySQL mode)**:
```json
{
  "host": "string",
  "port": "int (default 2881 for SQL port)",
  "database": "string",
  "username": "string",
  "password": "string",
  "ssl": "bool",
  "compatibilityMode": "string (mysql|oracle)",
  "additionalProps": {
    "tenant": "string (OceanBase tenant name)",
    "cluster": "string (optional cluster name)"
  }
}
```

**TiDB / TDSQL / GaussDB (MySQL-compatible)**:
```json
{
  "host": "string",
  "port": "int (TiDB: 4000, TDSQL: varies, GaussDB: varies)",
  "database": "string",
  "username": "string",
  "password": "string",
  "ssl": "bool",
  "additionalProps": {
    "charset": "string (utf8mb4 - for Chinese character support)",
    "tidbSessionVars": "object (TiDB-specific session variables)"
  }
}
```

**DaMeng**:
```json
{
  "host": "string",
  "port": "int (default 5236)",
  "database": "string",
  "username": "string",
  "password": "string",
  "charset": "string (UTF8|GBK|GB18030 - default UTF8)",
  "additionalProps": {
    "mode": "string (dm|oracle - compatibility mode)",
    "timezone": "string"
  }
}
```

**KingbaseES / Oscar / OpenGauss** (PostgreSQL-compatible):
```json
{
  "host": "string",
  "port": "int (default 54321 for KingbaseES)",
  "database": "string",
  "username": "string",
  "password": "string",
  "ssl": "bool",
  "additionalProps": {
    "charset": "string (UTF8 - for Chinese character support)",
    "searchPath": "string (schema search path)",
    "clientEncoding": "string (UTF8)"
  }
}
```

**GBase 8s** (Informix-compatible):
```json
{
  "host": "string",
  "port": "int (default 9088)",
  "database": "string",
  "server": "string (GBase server name)",
  "username": "string",
  "password": "string",
  "additionalProps": {
    "dblocale": "string (locale setting)",
    "clientCharset": "string (UTF8)"
  }
}
```

**GBase 8t** (TDDL/MySQL-compatible):
```json
{
  "host": "string",
  "port": "int (default 3306)",
  "database": "string",
  "username": "string",
  "password": "string",
  "ssl": "bool",
  "additionalProps": {
    "charset": "string (utf8mb4)"
  }
}
```

---

### 2. QueryHistory (Extended - Optional New Fields)

**Existing Entity** - May add new fields for enhanced metrics (FR-045).

**Purpose**: Records query execution for auditing, analytics, and debugging.

**Table Name**: `query_history`

**Potential Phase 1 Additions**:

| Field | Type | Description | Reason |
|-------|------|-------------|--------|
| `data_processed_bytes` | BIGINT (nullable) | Amount of data scanned/processed | FR-045 - Query execution metrics |
| `query_hash` | CHAR(32) (nullable) | MD5 hash of normalized SQL | Query pattern analysis |
| `snapshot_id` | VARCHAR(100) (nullable) | Table format snapshot ID (e.g., Iceberg) | Schema evolution tracking |
| `retry_count` | INT (default 0) | Number of retries before success/failure | FR-047 - Retry logic |

**Migration**: `ALTER TABLE ADD COLUMN` (nullable, backward compatible)

---

### 3. DataSourceSchema (NEW TABLE - Optional)

**Purpose**: Cache table/column/index metadata for query validation and optimization.

**Table Name**: `data_source_schema` (or `cached_schemas`)

**Schema**:

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| `data_source_id` | CHAR(36) | PK, FK → data_sources.id, NOT NULL | Associated data source |
| `schema_metadata` | JSON/JSONB | NOT NULL | Table/column/index definitions |
| `schema_version` | VARCHAR(50) | nullable | Version identifier (e.g., Iceberg snapshot ID, Delta version) |
| `last_refreshed_at` | TIMESTAMP | nullable | Last metadata refresh timestamp |
| `created_at` | TIMESTAMP | NOT NULL, DEFAULT NOW() | Creation timestamp |
| `updated_at` | TIMESTAMP | NOT NULL, AUTO UPDATE | Last update timestamp |

**`schema_metadata` JSON Structure**:
```json
{
  "tables": [
    {
      "name": "string",
      "schema": "string (optional - for databases with schema concept)",
      "columns": [
        {
          "name": "string",
          "type": "string (data type)",
          "nullable": "bool",
          "partitionKey": "bool (is this a partition column?)"
        }
      ],
      "partitions": [
        {
          "key": "string (partition column)",
          "values": ["array of partition values"]
        }
      ],
      "format": "string (iceberg|delta|hudi|parquet|orc etc.)",
      "location": "string (S3 path, HDFS path, etc.)",
      "properties": {
        "snapshotId": "string (Iceberg)",
        "version": "string (Delta)",
        "tableType": "string (Hudi)"
      }
    }
  ],
  "formatVersion": "string (e.g., 'iceberg-1.0', 'delta-2.0')",
  "extractedAt": "string (timestamp)"
}
```

**Relationships**:
- One-to-One with `data_sources` (each data source has at most one cached schema)
- Lazy loading: Cache populated on-demand when query is validated

**Validation Rules**:
- `schema_metadata` must be valid JSON
- `schema_version` format validated per data source type
- Foreign key constraint ensures referential integrity

---

### 4. CredentialVault (NEW TABLE - Optional)

**Purpose**: Secure credential storage with encryption and auto-rotation support.

**Table Name**: `credential_vault`

**Schema**:

| Field | Type | Constraints | Description |
|-------|------|-------------|-------------|
| `id` | CHAR(36) | PK, NOT NULL | UUID identifier |
| `data_source_id` | CHAR(36) | UNIQUE, FK → data_sources.id, NOT NULL | Associated data source |
| `encrypted_credentials` | TEXT/BLOB | NOT NULL | AES-256-GCM encrypted credentials JSON |
| `encryption_key_id` | VARCHAR(100) | nullable | KMS key identifier (for key rotation) |
| `auth_type` | ENUM | NOT NULL | Authentication method |
| `token_expires_at` | TIMESTAMP | nullable | Token expiration time (for OAuth2, IAM, Kerberos) |
| `last_rotated_at` | TIMESTAMP | nullable | Last credential/token rotation timestamp |
| `created_at` | TIMESTAMP | NOT NULL, DEFAULT NOW() | Creation timestamp |
| `updated_at` | TIMESTAMP | NOT NULL, AUTO UPDATE | Last update timestamp |

**`encrypted_credentials` Decrypted Structure** (internal, not stored):
```json
{
  "username": "string (nullable)",
  "password": "string (nullable)",
  "accessToken": "string (nullable)",
  "refreshToken": "string (nullable)",
  "privateKey": "string (nullable)",
  "keytab": "string (base64-encoded, nullable)",
  "credentialsJson": "object (nullable - for service account JSON)",
  "iamRoleArn": "string (nullable)",
  "additionalSecrets": "object (optional)"
}
```

**`auth_type` Enum Values**:
```go
const (
    AuthTypeBasic     AuthType = "basic"      // Username/password
    AuthTypeOAuth2    AuthType = "oauth2"     // OAuth2 access token
    AuthTypeIAM       AuthType = "iam"        // AWS IAM role
    AuthTypeKerberos  AuthType = "kerberos"   // Kerberos ticket/keytab
    AuthTypeJWT       AuthType = "jwt"        // JWT bearer token
    AuthTypeSAS       AuthType = "sas"        // Azure SAS token
    AuthTypePAT       AuthType = "pat"        // Personal Access Token
    AuthTypeKeyPair   AuthType = "keypair"    // Public/private key pair
)
```

**Relationships**:
- One-to-One with `data_sources`
- Background job rotates credentials when `token_expires_at` approaches

**Validation Rules**:
- `encrypted_credentials` must be decryptable with current encryption key
- `token_expires_at` must be in future for active authentication tokens
- `last_rotated_at` <= `token_expires_at`

---

## State Transitions

### DataSource Status Lifecycle

```text
                    ┌───────────────────────────────────┐
                    │                                   │
                    ▼                                   │
┌─────────┐  Test Connection  ┌─────────┐  Query Error  │
│  draft  │ ─────────────────► │ active  │ ─────────────►│  error  │
└─────────┘    (async)         └─────────┘   (retry)     └─────────┘
     │                                ▲                    │
     │ User creates                   │                    │
     ▼                                │                    ▼
 ┌─────────┐  Manual Reactivation  ┌─────────┐  Auto-Recovery
│inactive │ ◄─────────────────────│  error  │ ─────────────►
 └─────────┘                       └─────────┘   (health check)
     ▲                                    │
     │                                    │
     └──────────────── User deactivates ──┘
```

**States**:
- **`draft`**: Initial state when data source is created (not tested yet)
- **`active`**: Connection tested successfully, accepting queries
- **`inactive`**: User-disabled (administrative action)
- **`error`**: Connection failed or query error occurred, retry in progress

**Transitions**:
- `draft` → `active`: Successful connection test (POST `/api/v1/datasources/:id/test`)
- `draft` → `error`: Connection test failed
- `active` → `error`: Query execution fails (connection timeout, auth failure)
- `error` → `active`: Health check succeeds or manual reactivation
- `error` → `inactive`: User disables after error
- `inactive` → `active`: User re-enables (requires connection test)
- `active` → `inactive`: User disables (administrative)
- `inactive` → `draft`: Reset for re-configuration (optional)

**Auto-Recovery**:
- Background health check (every 30 seconds) attempts to reconnect `error` state sources
- Exponential backoff: 30s, 60s, 120s, 300s, 600s (max 10min interval)
- Max retries: 10 before giving up (requires manual intervention)

---

### Query Execution Lifecycle

```text
┌────────────┐  Parse & Validate  ┌────────────┐  Acquire Connection  ┌──────────────┐
│  received  │ ──────────────────► │ validating │ ────────────────────► │ connecting   │
└────────────┘                     └────────────┘                      └──────────────┘
                                                                                │
                                                                                ▼
┌────────────┐  Timeout/Cancel   ┌────────────┐  Execute Query     ┌──────────────┐
│  canceled  │ ◄───────────────── │executing  │ ◄────────────────── │ query_ready  │
└────────────┘                     └────────────┘                    └──────────────┘
                                        ▲                                    │
                                        │                                    ▼
                                        │                            ┌──────────────┐
                                        │  Error/Exception          │  streaming   │
                                        └────────────────────────── │   results    │
                                                                     └──────────────┘
                                                                                │
                                                                                ▼
                                                                     ┌──────────────┐
                                                                     │  completed   │
                                                                     └──────────────┘
```

**Query States** (internal, not persisted):
- **`received`**: Initial state, request parsed
- **`validating`**: SQL validation, schema check, permission check
- **`connecting`**: Acquiring connection from pool or establishing new connection
- **`query_ready`**: Connection acquired, query prepared
- **`executing`**: Query running on data source
- **`streaming`**: Results being streamed back to client
- **`completed`**: Query finished successfully
- **`canceled`**: Client canceled request or timeout

**State Triggers**:
- `received` → `validating`: Request validation passes
- `validating` → `connecting`: SQL validation passes, schema check passes
- `connecting` → `query_ready`: Connection acquired successfully
- `query_ready` → `executing`: Query submitted to data source
- `executing` → `streaming`: First row received
- `streaming` → `completed`: All rows streamed, connection released
- `any` → `canceled`: Client disconnect or timeout

---

## Validation Rules

### DataSource Validation

**Create/Update Validation**:

| Rule | Field | Condition | Error Message |
|------|-------|-----------|---------------|
| `DS-001` | `name` | Must be unique | Data source name already exists |
| `DS-002` | `name` | Length 1-255 characters | Name must be 1-255 characters |
| `DS-003` | `type` | Must be valid DatabaseType enum value | Unsupported data source type |
| `DS-004` | `config` | Valid JSON | Configuration must be valid JSON |
| `DS-005` | `config.host` | Required for most types (except object storage) | Host is required |
| `DS-006` | `config.port` | 1-65535 | Port must be between 1 and 65535 |
| `DS-007` | `config.username` | Required for databases with auth | Username is required |
| `DS-008` | `config.password` | Required for basic auth | Password is required |
| `DS-009` | `config.authentication` | Required for cloud services | Authentication configuration is required |
| `DS-010` | `config.authentication.type` | Valid auth type for data source | Unsupported authentication type |
| `DS-011` | `config.encryption_key_id` | Required for encrypted sources | Encryption key identifier is required |
| `DS-012` | `config.fileFormat` | Required for object storage sources | File format must be specified (parquet|orc|avro|csv|json) |
| `DS-013` | `config.bucket` | Required for S3/MinIO/OSS/COS/Blob | Bucket name is required |
| `DS-014` | `config.charset` | Must be UTF8|GBK|GB18030 for domestic databases | Unsupported charset (use UTF8, GBK, or GB18030) |
| `DS-015` | `config.compatibilityMode` | Required for OceanBase/GaussDB | Compatibility mode must be specified (mysql|oracle|postgres) |

---

### Query Validation

**Pre-Execution Validation** (existing `sql_validator.go` extended):

| Rule | Check | Error Message |
|------|-------|---------------|
| `QV-001` | SQL must start with `SELECT` or `WITH` | Only SELECT queries are allowed |
| `QV-002` | No forbidden keywords (INSERT, UPDATE, DELETE, DROP, ALTER, CREATE, TRUNCATE) | Query contains forbidden keywords |
| `QV-003` | SQL syntax validation (parseable) | Invalid SQL syntax |
| `QV-004` | Parameterized query validation | Parameter count mismatch |
| `QV-005` | Schema validation (if cached schema available) | Column does not exist in schema |
| `QV-006` | Table format-specific validation (e.g., Iceberg snapshot exists) | Invalid snapshot ID |
| `QV-007` | Data source type-specific SQL dialect checks | Unsupported SQL construct for this data source type |
| `QV-008` | Query complexity limit (e.g., max JOIN depth) | Query exceeds complexity limit |
| `QV-009` | Query length limit (e.g., 100KB max) | Query exceeds maximum length |
| `QV-010` | Chinese character encoding validation for domestic databases | Invalid character encoding |

---

### Credential Validation

**Credential Vault Validation**:

| Rule | Check | Error Message |
|------|-------|---------------|
| `CV-001` | `encrypted_credentials` must be decryptable | Failed to decrypt credentials |
| `CV-002` | `auth_type` must match data source type | Incompatible authentication type |
| `CV-003` | `token_expires_at` must be in future for active sources | Token has expired |
| `CV-004` | `encryption_key_id` must exist in KMS | Encryption key not found |
| `CV-005` | OAuth2 refresh token must be present if access token expiring | Refresh token required |
| `CV-006` | Kerberos keytab must be valid base64 | Invalid keytab encoding |
| `CV-007` | Service account JSON must be valid | Invalid service account credentials |

---

## Indexes

### DataSource Table Indexes (Existing + New)

| Index | Columns | Type | Purpose |
|-------|---------|------|---------|
| `PRIMARY` | `id` | BTREE | Primary key lookup |
| `UNIQUE` | `name` | BTREE | Uniqueness constraint |
| `idx_type` | `type` | BTREE | Filter by data source type (Phase 1 - new index) |
| `idx_status` | `status` | BTREE | Filter by status (existing) |
| `idx_type_status` | `type, status` | BTREE | Composite: List active sources by type (Phase 1) |
| `idx_created_at` | `created_at` | BTREE | Temporal queries (existing) |

**Rationale**: Phase 1 adds 30+ data source types, `idx_type` and `idx_type_status` enable efficient filtering by type.

---

### QueryHistory Table Indexes (Existing + New)

| Index | Columns | Type | Purpose |
|-------|---------|------|---------|
| `PRIMARY` | `id` | BTREE | Primary key lookup |
| `idx_data_source_id` | `data_source_id` | BTREE | Filter by data source (existing) |
| `idx_created_at` | `created_at` | BTREE | Temporal queries (existing) |
| `idx_status` | `status` | BTREE | Filter by success/error (existing - if added) |
| `idx_user_id` | `user_id` | BTREE | User query history (Phase 1 - if added) |
| `idx_query_hash` | `query_hash` | BTREE | Query pattern analysis (Phase 1 - if added) |
| `idx_correlation_id` | `correlation_id` | BTREE | Distributed tracing (Phase 1 - if added) |

---

### DataSourceSchema Table Indexes (New Table)

| Index | Columns | Type | Purpose |
|-------|---------|------|---------|
| `PRIMARY` | `data_source_id` | BTREE | Primary key lookup |
| `idx_schema_version` | `schema_version` | BTREE | Schema version lookup |
| `idx_last_refreshed_at` | `last_refreshed_at` | BTREE | Cache invalidation (old schemas) |

---

### CredentialVault Table Indexes (New Table)

| Index | Columns | Type | Purpose |
|-------|---------|------|---------|
| `PRIMARY` | `id` | BTREE | Primary key lookup |
| `UNIQUE` | `data_source_id` | BTREE | One-to-one with data sources |
| `idx_token_expires_at` | `token_expires_at` | BTREE | Token rotation scheduler (expiring soon) |
| `idx_auth_type` | `auth_type` | BTREE | Filter by authentication type |

---

## Migration Plan

### Database Migration Steps

**Phase 1 Migration** (executed before implementation):

1. **Extend `data_sources.type` ENUM**:
   ```sql
   -- MySQL (simplified - actual migration needs all values)
   ALTER TABLE data_sources
   MODIFY COLUMN type ENUM(
     'mysql', 'mariadb', 'postgresql', 'oracle',
     'iceberg', 'delta_lake', 'hudi',
     'snowflake', 'databricks', 'redshift', 'bigquery',
     's3_parquet', 'minio_parquet', 'oss_parquet', 'oss_orc', 'cos_parquet', 'cos_orc', 'cos_avro', 'azure_blob_avro', 'azure_blob_orc', 'azure_blob_csv', 'azure_blob_json', 'azure_blob_parquet', 'azure_parquet',
     'hdfs_avro', 'ozone_parquet', 'ozone_orc',
     'clickhouse', 'doris', 'starrocks', 'druid',
     'oceanbase_mysql', 'oceanbase_oracle', 'tidb', 'tdsql',
     'gaussdb_mysql', 'gaussdb_postgres', 'dameng', 'kingbasees',
     'gbase_8s', 'gbase_8t', 'oscar', 'opengauss'
   ) NOT NULL;
   ```

   ```sql
   -- PostgreSQL
   ALTER TYPE databasetype_enum ADD VALUE 'iceberg';
   ALTER TYPE databasetype_enum ADD VALUE 'delta_lake';
   -- ... repeat for all new values
   ```

2. **Add Optional Columns to `query_history`** (if implementing enhanced metrics):
   ```sql
   ALTER TABLE query_history
   ADD COLUMN data_processed_bytes BIGINT DEFAULT NULL,
   ADD COLUMN query_hash CHAR(32) DEFAULT NULL,
   ADD COLUMN snapshot_id VARCHAR(100) DEFAULT NULL,
   ADD COLUMN retry_count INT DEFAULT 0;
   ```

3. **Create `data_source_schema` Table** (if implementing schema caching):
   ```sql
   CREATE TABLE data_source_schema (
     data_source_id CHAR(36) PRIMARY KEY,
     schema_metadata JSON NOT NULL,
     schema_version VARCHAR(50),
     last_refreshed_at TIMESTAMP NULL,
     created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
     updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
     FOREIGN KEY (data_source_id) REFERENCES data_sources(id) ON DELETE CASCADE
   );
   ```

4. **Create `credential_vault` Table** (if implementing encrypted credentials):
   ```sql
   CREATE TABLE credential_vault (
     id CHAR(36) PRIMARY KEY,
     data_source_id CHAR(36) UNIQUE NOT NULL,
     encrypted_credentials TEXT NOT NULL,
     encryption_key_id VARCHAR(100),
     auth_type ENUM('basic', 'oauth2', 'iam', 'kerberos', 'jwt', 'sas', 'pat', 'keypair') NOT NULL,
     token_expires_at TIMESTAMP NULL,
     last_rotated_at TIMESTAMP NULL,
     created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
     updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
     FOREIGN KEY (data_source_id) REFERENCES data_sources(id) ON DELETE CASCADE
   );
   ```

5. **Create Indexes**:
   ```sql
   -- DataSource indexes
   CREATE INDEX idx_datasources_type ON data_sources(type);
   CREATE INDEX idx_datasources_type_status ON data_sources(type, status);

   -- CredentialVault indexes
   CREATE INDEX idx_credential_vault_token_expires ON credential_vault(token_expires_at);
   ```

**Rollback Plan**:
- Drop new tables: `DROP TABLE IF EXISTS data_source_schema, credential_vault;`
- Remove new columns: `ALTER TABLE query_history DROP COLUMN data_processed_bytes, ...;`
- Restore old ENUM: `ALTER TABLE data_sources MODIFY COLUMN type ENUM('mysql', 'mariadb', 'postgresql', 'oracle');`

---

## Data Model Completed: ✅ READY FOR API CONTRACTS

All entities, relationships, validation rules, and state transitions defined. Ready to proceed to API contract generation (Phase 1).
