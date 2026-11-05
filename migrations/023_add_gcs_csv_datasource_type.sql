-- Add the Google Cloud Storage CSV data source type.
ALTER TABLE data_sources MODIFY COLUMN type ENUM(
    'mysql', 'mariadb', 'postgresql', 'oracle', 'trino', 'presto', 'openlookeng',
    'iceberg', 'delta_lake', 'hudi', 'snowflake', 'databricks', 'redshift', 'bigquery',
    's3_parquet', 's3_orc', 's3_delta', 's3_avro', 's3_csv', 'gcs_csv', 's3_json',
    'minio_parquet', 'minio_csv', 'minio_orc', 'minio_avro', 'minio_json',
    'oss_parquet', 'oss_csv', 'oss_json', 'oss_avro', 'oss_orc', 'cos_parquet', 'cos_csv', 'cos_json', 'cos_orc', 'cos_avro', 'cos_delta', 'cos_iceberg', 'azure_blob_avro', 'azure_blob_orc', 'azure_blob_csv', 'azure_blob_json', 'azure_blob_parquet',
    'hdfs_avro', 'hdfs_orc', 'hdfs_parquet', 'hdfs_csv', 'hdfs_json', 'ozone_parquet', 'ozone_orc',
    'clickhouse', 'doris', 'starrocks', 'druid',
    'oceanbase_mysql', 'oceanbase_oracle', 'tidb', 'tdsql', 'gaussdb_mysql',
    'gaussdb_postgres', 'dameng', 'kingbasees', 'gbase_8s', 'gbase_8t', 'oscar',
    'opengauss', 'oss_delta', 'minio_iceberg', 'azure_delta',
    'ozone_text', 'ozone_avro', 'ozone_json', 'ozone_csv', 'azure_parquet',
    'hdfs_text', 'hdfs_parquet_compressed', 'minio_delta', 'hive',
    'sqlserver', 'hologres', 'mongodb', 'maxcompute', 'ozone_delta'
) NOT NULL;
