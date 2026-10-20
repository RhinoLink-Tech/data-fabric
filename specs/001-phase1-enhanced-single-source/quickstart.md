# Phase 1 Quickstart Guide

**Feature**: Phase 1 Enhanced Single-Source Capabilities
**Date**: 2025-12-24
**Purpose**: Get started with querying 30+ data source types through Data Fabric

---

## Overview

Data Fabric Phase 1 extends support beyond traditional relational databases to include:
- **Data Lake Table Formats**: Apache Iceberg, Delta Lake, Apache Hudi
- **Cloud Data Warehouses**: Snowflake, Databricks, Redshift, BigQuery
- **Object Storage**: AWS S3, MinIO, Alibaba OSS, Tencent COS, Azure Blob
- **OLAP Engines**: ClickHouse, Apache Doris, StarRocks, Apache Druid
- **Domestic Chinese Databases**: OceanBase, TiDB, TDSQL, GaussDB, DaMeng, KingbaseES, GBase, Oscar, OpenGauss
- **Distributed File Systems**: HDFS

---

## Prerequisites

- Data Fabric v1.1+ installed
- JWT authentication token (obtain from your Data Fabric administrator)
- Access to at least one supported data source
- `curl` or equivalent HTTP client for testing

---

## Quickstart: Your First Query (5 minutes)

### Step 1: Start the Gateway

```bash
# Using Docker
docker run -d \
  --name data-fabric \
  -p 8099:8099 \
  -v $(pwd)/configs:/app/configs \
  data-fabric:1.1.0

# Or run locally
go run cmd/server/main.go
```

### Step 2: Set Your Authentication Token

```bash
export DATA_FABRIC_TOKEN="your-jwt-token-here"
```

### Step 3: Create a Data Source

```bash
curl -X POST http://localhost:8099/api/v1/datasources \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DATA_FABRIC_TOKEN" \
  -d '{
    "name": "My Snowflake Warehouse",
    "type": "snowflake",
    "config": {
      "account": "xy12345.us-east-1",
      "warehouse": "compute_wh",
      "database": "analytics",
      "schema": "public",
      "username": "readonly_user",
      "password": "secure_password",
      "role": "reader"
    }
  }'
```

**Response**:
```json
{
  "success": true,
  "data": {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "name": "My Snowflake Warehouse",
    "type": "snowflake",
    "status": "active",
    "created_at": "2025-12-24T10:00:00Z"
  },
  "correlationId": "req_123456789"
}
```

### Step 4: Execute Your First Query

```bash
curl -X POST http://localhost:8099/api/v1/query \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DATA_FABRIC_TOKEN" \
  -d '{
    "dataSourceId": "550e8400-e29b-41d4-a716-446655440000",
    "sql": "SELECT * FROM users LIMIT 10",
    "limit": 10
  }'
```

**Response**:
```json
{
  "success": true,
  "data": {
    "columns": [
      {"name": "id", "type": "NUMBER", "nullable": false},
      {"name": "name", "type": "VARCHAR", "nullable": false},
      {"name": "email", "type": "VARCHAR", "nullable": true}
    ],
    "rows": [
      [1, "Alice", "alice@example.com"],
      [2, "Bob", "bob@example.com"]
    ],
    "metadata": {
      "rowCount": 2,
      "executionTimeMs": 145,
      "dataSourceId": "550e8400-e29b-41d4-a716-446655440000",
      "databaseType": "snowflake"
    }
  },
  "correlationId": "req_987654321"
}
```

---

## Configuration Examples by Data Source Type

### Data Lake Table Formats

#### Apache Iceberg

```json
{
  "name": "Production Iceberg Tables",
  "type": "iceberg",
  "config": {
    "catalogType": "rest",
    "catalogUri": "https://iceberg-catalog.example.com",
    "warehousePath": "s3://my-bucket/warehouse",
    "authentication": {
      "type": "iam",
      "awsRegion": "us-east-1"
    }
  }
}
```

**Time Travel Query** (Iceberg snapshot):
```sql
SELECT * FROM my_table FOR SYSTEM_VERSION AS OF 1234567890
```

#### Delta Lake

```json
{
  "name": "Databricks Delta Lake",
  "type": "delta_lake",
  "config": {
    "tablePath": "s3://my-bucket/delta-tables",
    "workspaceUrl": "https://mycompany.cloud.databricks.com",
    "warehouseId": "abc123456",
    "authentication": {
      "type": "pat",
      "token": "dapi1234567890abcdef"
    }
  }
}
```

**Time Travel Query** (Delta version):
```sql
SELECT * FROM my_table VERSION AS OF 42
```

#### Apache Hudi

```json
{
  "name": "Hudi on S3",
  "type": "hudi",
  "config": {
    "tablePath": "s3://my-bucket/hudi-tables",
    "tableType": "mor",
    "baseFileFormat": "parquet",
    "authentication": {
      "type": "accessKey",
      "accessKeyId": "AKIAIOSFODNN7EXAMPLE",
      "secretAccessKey": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
    }
  }
}
```

---

### Cloud Data Warehouses

#### Snowflake

```json
{
  "name": "Snowflake Production",
  "type": "snowflake",
  "config": {
    "account": "xy12345.us-east-1",
    "warehouse": "compute_wh",
    "database": "analytics_db",
    "schema": "public",
    "username": "readonly_user",
    "password": "secure_password",
    "role": "reader"
  }
}
```

#### Databricks

```json
{
  "name": "Databricks SQL Warehouse",
  "type": "databricks",
  "config": {
    "workspaceUrl": "https://mycompany.cloud.databricks.com",
    "warehouseId": "abc123456",
    "catalog": "unity_catalog",
    "database": "analytics",
    "schema": "public",
    "httpPath": "/sql/1.0/warehouses/abc123456",
    "authentication": {
      "type": "pat",
      "token": "dapi1234567890abcdef"
    }
  }
}
```

#### Amazon Redshift

```json
{
  "name": "Redshift Cluster",
  "type": "redshift",
  "config": {
    "host": "redshift-cluster.abc123.us-east-1.redshift.amazonaws.com",
    "port": 5439,
    "database": "analytics",
    "username": "readonly_user",
    "password": "secure_password",
    "ssl": true,
    "authentication": {
      "type": "iam",
      "iamRoleArn": "arn:aws:iam::123456789012:role/RedshiftReadOnlyRole",
      "awsRegion": "us-east-1"
    }
  }
}
```

#### Google BigQuery

```json
{
  "name": "BigQuery Production",
  "type": "bigquery",
  "config": {
    "project": "my-gcp-project",
    "dataset": "analytics_dataset",
    "authentication": {
      "type": "serviceAccount",
      "credentialsJson": "{\"type\":\"service_account\",\"project_id\":\"...\"}"
    },
    "additionalProps": {
      "location": "US",
      "maxBytesBilled": "1000000000"
    }
  }
}
```

---

### Object Storage (Query Data Files)

#### AWS S3 (Parquet Files)

```json
{
  "name": "S3 Parquet Analytics",
  "type": "s3_parquet",
  "config": {
    "bucket": "my-analytics-bucket",
    "region": "us-east-1",
    "prefix": "data/year=2024/",
    "fileFormat": "parquet",
    "authentication": {
      "type": "iam",
      "iamRoleArn": "arn:aws:iam::123456789012:role/S3ReadOnlyRole"
    }
  }
}
```

**Query Example**:
```sql
SELECT * FROM s3object WHERE column1 = 'value' LIMIT 100
```

#### MinIO (Self-Hosted S3-Compatible)

```json
{
  "name": "MinIO Local Storage",
  "type": "minio_parquet",
  "config": {
    "endpoint": "http://localhost:9000",
    "bucket": "analytics",
    "prefix": "data/",
    "fileFormat": "parquet",
    "authentication": {
      "type": "accessKey",
      "accessKeyId": "minioadmin",
      "secretAccessKey": "minioadmin"
    },
    "additionalProps": {
      "useSSL": false
    }
  }
}
```

#### Alibaba Cloud OSS

```json
{
  "name": "Alibaba OSS Data Lake",
  "type": "oss_parquet",
  "config": {
    "endpoint": "oss-cn-hangzhou.aliyuncs.com",
    "bucket": "my-data-lake",
    "prefix": "analytics/",
    "fileFormat": "parquet",
    "authentication": {
      "type": "accessKey",
      "accessKeyId": "LTAI5txxxxxxx",
      "secretAccessKey": "xxxxxxsecretxxxxxx"
    }
  }
}
```

#### Azure Blob Storage

```json
{
  "name": "Azure Blob Analytics",
  "type": "azure_parquet",
  "config": {
    "endpoint": "https://mystorageaccount.blob.core.windows.net",
    "bucket": "analytics-container",
    "prefix": "data/",
    "fileFormat": "parquet",
    "authentication": {
      "type": "sasToken",
      "sasToken": "sv=2020-08-04&ss=b&srt=sco...sp=rwdlac&se=2025-12-31T23:59:59Z&st=2025-01-01T00:00:00Z&spr=https&sig=xxxxxx"
    }
  }
}
```

---

### OLAP Engines

#### ClickHouse

```json
{
  "name": "ClickHouse Analytics",
  "type": "clickhouse",
  "config": {
    "host": "clickhouse-server.example.com",
    "port": 9000,
    "protocol": "native",
    "database": "analytics",
    "username": "readonly_user",
    "password": "secure_password",
    "ssl": false
  }
}
```

#### Apache Doris

```json
{
  "name": "Doris Cluster",
  "type": "doris",
  "config": {
    "host": "doris-fe.example.com",
    "port": 9030,
    "database": "analytics",
    "username": "readonly_user",
    "password": "secure_password"
  }
}
```

#### StarRocks

```json
{
  "name": "StarRocks Warehouse",
  "type": "starrocks",
  "config": {
    "host": "starroocks-fe.example.com",
    "port": 9030,
    "database": "analytics",
    "username": "readonly_user",
    "password": "secure_password"
  }
}
```

#### Apache Druid

```json
{
  "name": "Druid Cluster",
  "type": "druid",
  "config": {
    "brokerHost": "druid-broker.example.com",
    "brokerPort": 8082,
    "authentication": {
      "type": "basic",
      "username": "readonly_user",
      "password": "secure_password"
    }
  }
}
```

---

### Domestic Chinese Databases

#### OceanBase (MySQL Mode)

```json
{
  "name": "OceanBase Cluster",
  "type": "oceanbase_mysql",
  "config": {
    "host": "oceanbase.example.com",
    "port": 2881,
    "database": "production_db",
    "username": "readonly_user",
    "password": "secure_password",
    "additionalProps": {
      "tenant": "my_tenant"
    }
  }
}
```

#### TiDB

```json
{
  "name": "TiDB Distributed Database",
  "type": "tidb",
  "config": {
    "host": "tidb.example.com",
    "port": 4000,
    "database": "analytics_db",
    "username": "readonly_user",
    "password": "secure_password",
    "ssl": false,
    "additionalProps": {
      "charset": "utf8mb4"
    }
  }
}
```

#### DaMeng (DM)

```json
{
  "name": "DaMeng Database",
  "type": "dameng",
  "config": {
    "host": "dameng.example.com",
    "port": 5236,
    "database": "production_db",
    "username": "readonly_user",
    "password": "secure_password",
    "charset": "UTF8"
  }
}
```

#### KingbaseES

```json
{
  "name": "KingbaseES Database",
  "type": "kingbasees",
  "config": {
    "host": "kingbase.example.com",
    "port": 54321,
    "database": "analytics_db",
    "username": "readonly_user",
    "password": "secure_password",
    "ssl": false,
    "additionalProps": {
      "charset": "UTF8"
    }
  }
}
```

---

### Distributed File Systems

#### HDFS

```json
{
  "name": "HDFS Analytics",
  "type": "hdfs_parquet",
  "config": {
    "namenodeHost": "hdfs-namenode.example.com",
    "namenodePort": 8020,
    "path": "/data/analytics",
    "fileFormat": "parquet",
    "authentication": {
      "type": "kerberos",
      "kerberosPrincipal": "hdfs-user@EXAMPLE.COM",
      "kerberosKeytab": "BASE64_ENCODED_KEYTAB_CONTENT",
      "kerberosRealm": "EXAMPLE.COM",
      "kerberosKdc": "kdc.example.com"
    }
  }
}
```

#### Apache Ozone

Apache Ozone is not currently a supported connector. The example configuration has been removed because the client operations are placeholders and the Ozone driver types are not registered. See [the current Ozone support status](../../docs/ozone-support.md) before using this repository's connector list.

---

## Advanced Usage

### Parameterized Queries (Prevent SQL Injection)

```bash
curl -X POST http://localhost:8099/api/v1/query \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DATA_FABRIC_TOKEN" \
  -d '{
    "dataSourceId": "550e8400-e29b-41d4-a716-446655440000",
    "sql": "SELECT * FROM users WHERE status = ? AND created_at > ?",
    "parameters": ["active", "2025-01-01"],
    "limit": 100
  }'
```

### Pagination

```bash
curl -X POST http://localhost:8099/api/v1/query \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DATA_FABRIC_TOKEN" \
  -d '{
    "dataSourceId": "550e8400-e29b-41d4-a716-446655440000",
    "sql": "SELECT * FROM large_table",
    "limit": 100,
    "offset": 200
  }'
```

### Query Validation (Without Execution)

```bash
curl -X POST http://localhost:8099/api/v1/query/validate \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DATA_FABRIC_TOKEN" \
  -d '{
    "dataSourceId": "550e8400-e29b-41d4-a716-446655440000",
    "sql": "SELECT * FROM users WHERE id = ?"
  }'
```

**Response**:
```json
{
  "success": true,
  "valid": true,
  "errors": [],
  "correlationId": "req_validation_123"
}
```

### Test Data Source Connection

```bash
curl -X POST http://localhost:8099/api/v1/datasources/550e8400-e29b-41d4-a716-446655440000/test \
  -H "Authorization: Bearer $DATA_FABRIC_TOKEN"
```

**Response**:
```json
{
  "success": true,
  "data": {
    "connected": true,
    "message": "Successfully connected to data source",
    "executionTimeMs": 23,
    "testedAt": "2025-12-24T10:05:00Z"
  },
  "correlationId": "req_test_123"
}
```

### List Data Sources (Filtered)

```bash
# List all active Snowflake data sources
curl -X GET "http://localhost:8099/api/v1/datasources?type=snowflake&status=active&page=1&limit=20" \
  -H "Authorization: Bearer $DATA_FABRIC_TOKEN"
```

---

## Common Patterns

### Pattern 1: Time Travel Queries (Delta Lake, Iceberg)

**Delta Lake** (version-based):
```sql
SELECT * FROM orders VERSION AS OF 42 WHERE status = 'completed'
```

**Iceberg** (snapshot-based):
```sql
SELECT * FROM orders FOR SYSTEM_VERSION AS OF 1234567890 WHERE status = 'completed'
```

### Pattern 2: Schema Evolution Handling

Data lake tables evolve. Data Fabric automatically handles added columns:

```sql
-- Query after column added works without error
SELECT id, name, email, new_column FROM users WHERE id = 1
```

### Pattern 3: Partition Pruning (Performance Optimization)

Data Fabric automatically prunes partitions for Iceberg, Delta Lake, and Hudi:

```sql
-- Only reads partition=2025-12-24
SELECT * FROM events WHERE partition_date = '2025-12-24'
```

### Pattern 4: Chinese Character Queries (Domestic Databases)

```sql
-- Query with Chinese characters (DaMeng, TiDB, KingbaseES, etc.)
SELECT * FROM customers WHERE name = '张三' AND city = '北京'
```

Ensure `charset` is set to `UTF8` in data source configuration.

### Pattern 5: Cloud Warehouse Cost Control

**BigQuery**:
```json
{
  "type": "bigquery",
  "config": {
    "additionalProps": {
      "maxBytesBilled": "1000000000"
    }
  }
}
```

**Snowflake**:
```sql
-- Use query tags for cost tracking
SELECT * FROM large_table /* tag=cost_center_123 */
```

---

## Troubleshooting

### Error: Authentication Failed

**Problem**: Invalid credentials or token expired.

**Solution**:
```bash
# Refresh your JWT token
export DATA_FABRIC_TOKEN="new-jwt-token"

# For cloud services, check auth configuration
# Snowflake: Verify account, username, password
# BigQuery: Verify service account JSON
# AWS S3: Verify IAM role or access keys
```

### Error: Query Timeout

**Problem**: Query exceeded execution time limit.

**Solution**:
- Reduce data scanned: Add `WHERE` clause filters
- Use partition pruning: Filter on partition columns
- Increase timeout in data source config (if appropriate)
- Add `LIMIT` clause to reduce result set size

### Error: Connection Refused

**Problem**: Network or firewall issue.

**Solution**:
- Verify host and port are correct
- Check firewall rules allow gateway → data source
- For cloud services, verify IP allowlists
- Test connectivity: `telnet <host> <port>`

### Error: Schema Mismatch

**Problem**: Table schema changed (schema evolution).

**Solution**:
- Data Fabric auto-handles added columns
- For type changes, recreate data source with updated schema
- Check schema cache freshness

### Error: Chinese Character Corruption

**Problem**: Invalid character encoding for domestic databases.

**Solution**:
```json
{
  "type": "dameng",
  "config": {
    "charset": "UTF8"
  }
}
```

---

## Best Practices

### Security

1. **Use Read-Only Users**: Always connect with read-only database accounts
2. **Enable SSL/TLS**: Set `"ssl": true` for production connections
3. **Rotate Credentials**: Use IAM roles instead of long-lived access keys
4. **Encrypt at Rest**: Credentials are encrypted automatically in Data Fabric

### Performance

1. **Use Parameterized Queries**: Prevent injection and enable statement caching
2. **Leverage Partitioning**: Filter on partition columns for data lakes
3. **Set Appropriate Limits**: Use `limit` parameter to avoid large result sets
4. **Optimize Query Timeouts**: Configure per data source type (OLAP: 5s, data lakes: 30s)

### Monitoring

1. **Track Query Performance**: Check `executionTimeMs` in response metadata
2. **Monitor Data Scanned**: Review `dataProcessedBytes` for cost control
3. **Health Check Regularly**: Use `/datasources/{id}/test` endpoint
4. **Review Query History**: Analyze patterns in `/api/v1/query-history` (if implemented)

---

## Next Steps

1. **Explore API Documentation**: Access Swagger UI at `http://localhost:8099/swagger/index.html`
2. **Read Data Model**: See `data-model.md` for entity definitions
3. **Review Research Findings**: See `research.md` for technology decisions
4. **Check Implementation Plan**: See `plan.md` for architecture details

---

## Support

- **Documentation**: [Full Docs](https://github.com/RhinoLink-Tech/data-fabric/tree/master/docs)
- **Issues**: [GitHub Issues](https://github.com/RhinoLink-Tech/data-fabric/issues)
- **Discussions**: [GitHub Discussions](https://github.com/RhinoLink-Tech/data-fabric/discussions)

---

**Quickstart Completed!** 🎉

You're now ready to query 30+ data source types through Data Fabric Phase 1.
