-- Add the bounded Azure Blob JSON object adapter to the data source type enum.
ALTER TABLE data_sources MODIFY COLUMN type ENUM(
    'mysql', 'mariadb', 'postgresql', 'oracle', 'trino', 'presto', 'openlookeng',
    'iceberg', 'delta_lake', 'hudi', 'snowflake', 'databricks', 'redshift', 'bigquery',
    's3_parquet', 's3_orc', 's3_delta', 's3_avro', 's3_csv', 's3_json',
    'minio_parquet', 'minio_csv', 'minio_orc', 'minio_avro', 'minio_json',
    'oss_parquet', 'oss_csv', 'cos_parquet', 'cos_csv', 'azure_blob_csv', 'azure_blob_json', 'azure_blob_parquet',
    'hdfs_avro', 'hdfs_parquet', 'hdfs_csv', 'hdfs_json', 'ozone_parquet',
    'clickhouse', 'doris', 'starrocks', 'druid',
    'oceanbase_mysql', 'oceanbase_oracle', 'tidb', 'tdsql', 'gaussdb_mysql',
    'gaussdb_postgres', 'dameng', 'kingbasees', 'gbase_8s', 'gbase_8t', 'oscar',
    'opengauss', 'oss_delta', 'minio_iceberg', 'azure_delta',
    'ozone_text', 'ozone_avro', 'ozone_json', 'ozone_csv', 'azure_parquet',
    'hdfs_text', 'hdfs_parquet_compressed', 'minio_delta', 'hive',
    'sqlserver', 'hologres', 'mongodb', 'maxcompute'
) NOT NULL;
