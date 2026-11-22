//go:build !datafabric_core

package database

import (
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/domestic"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/file_system"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/nosql"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/object_storage"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/olap"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/table_formats"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/traditional"
	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers/warehouses"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// registerDrivers registers all available drivers using optimized generic drivers
func (dr *DriverRegistry) registerDrivers() {
	dr.mutex.Lock()
	defer dr.mutex.Unlock()

	// Relational databases
	dr.register(model.DatabaseTypeMySQL, func() drivers.Driver {
		return &traditional.MySQLDriver{}
	})
	dr.register(model.DatabaseTypeMariaDB, func() drivers.Driver {
		return &traditional.MySQLDriver{}
	})
	dr.register(model.DatabaseTypePostgreSQL, func() drivers.Driver {
		return &traditional.PostgreSQLDriver{}
	})
	dr.register(model.DatabaseTypeOracle, func() drivers.Driver {
		return &traditional.OracleDriver{}
	})
	dr.register(model.DatabaseTypeSQLServer, func() drivers.Driver {
		return traditional.NewSQLServerDriver()
	})
	dr.register(model.DatabaseTypeHDFSCSV, func() drivers.Driver {
		return &file_system.HDFSCSVDriver{}
	})
	dr.register(model.DatabaseTypeHDFSParquet, func() drivers.Driver {
		return &file_system.HDFSParquetDriver{}
	})
	dr.register(model.DatabaseTypeHDFSParquetCompressed, func() drivers.Driver {
		return &file_system.HDFSParquetCompressionDriver{}
	})
	dr.register(model.DatabaseTypeHDFSAvro, func() drivers.Driver {
		return &file_system.HDFSAvroDriver{}
	})
	dr.register(model.DatabaseTypeHDFSORC, func() drivers.Driver {
		return &file_system.HDFSORCDriver{}
	})
	dr.register(model.DatabaseTypeOzoneParquet, func() drivers.Driver {
		return &file_system.OzoneParquetDriver{}
	})
	dr.register(model.DatabaseTypeOzoneORC, func() drivers.Driver {
		return &file_system.OzoneORCDriver{}
	})
	dr.register(model.DatabaseTypeOzoneText, func() drivers.Driver {
		return &file_system.OzoneTextDriver{}
	})
	dr.register(model.DatabaseTypeOzoneAvro, func() drivers.Driver {
		return &file_system.OzoneAvroDriver{}
	})
	dr.register(model.DatabaseTypeOzoneJSON, func() drivers.Driver {
		return &file_system.OzoneJSONDriver{}
	})
	dr.register(model.DatabaseTypeOzoneCSV, func() drivers.Driver {
		return &file_system.OzoneCSVDriver{}
	})
	dr.register(model.DatabaseTypeOzoneDelta, func() drivers.Driver {
		return &object_storage.OzoneDeltaDriver{}
	})
	dr.register(model.DatabaseTypeHDFSJSON, func() drivers.Driver {
		return &file_system.HDFSJSONDriver{}
	})
	dr.register(model.DatabaseTypeHDFSText, func() drivers.Driver {
		return &file_system.HDFSTextDriver{}
	})

	// Chinese domestic databases
	dr.register(model.DatabaseTypeDaMeng, func() drivers.Driver {
		return &domestic.DaMengDBAdapter{
			Inner: &domestic.DaMengDriver{},
		}
	})
	dr.register(model.DatabaseTypeOceanBaseMySQL, func() drivers.Driver {
		return &domestic.OceanBaseDriver{}
	})
	dr.register(model.DatabaseTypeOceanBaseOracle, func() drivers.Driver {
		return &domestic.OceanBaseOracleDriver{}
	})
	dr.register(model.DatabaseTypeTiDB, func() drivers.Driver {
		return &domestic.TiDBDriver{}
	})
	dr.register(model.DatabaseTypeTDSQL, func() drivers.Driver {
		return &domestic.TDSQLDriver{}
	})
	dr.register(model.DatabaseTypeGaussDBMySQL, func() drivers.Driver {
		return &domestic.GaussDBMySQLDriver{}
	})
	dr.register(model.DatabaseTypeGaussDBPostgres, func() drivers.Driver {
		return &domestic.GaussDBDriver{}
	})
	dr.register(model.DatabaseTypeKingbaseES, func() drivers.Driver {
		return &domestic.KingbaseESDriver{}
	})
	dr.register(model.DatabaseTypeGBase8s, func() drivers.Driver {
		return &domestic.GBaseDriver{}
	})
	dr.register(model.DatabaseTypeGBase8t, func() drivers.Driver {
		return &domestic.GBaseDriver{}
	})
	dr.register(model.DatabaseTypeOscar, func() drivers.Driver {
		return &domestic.OscarDriver{}
	})
	dr.register(model.DatabaseTypeOpenGauss, func() drivers.Driver {
		return &domestic.OpenGaussDriver{}
	})

	//// Cloud Data Warehouses
	dr.register(model.DatabaseTypeSnowflake, func() drivers.Driver {
		return &warehouses.SnowflakeDriver{} // 使用 olap 包中的 SnowflakeDriver 而不是 warehouses 包中的
	})
	dr.register(model.DatabaseTypeDatabricks, func() drivers.Driver {
		return &warehouses.DatabricksDriver{}
	})
	// Cloud Data Warehouses
	dr.register(model.DatabaseTypeRedshift, func() drivers.Driver {
		return &warehouses.RedshiftDriver{}
	})
	dr.register(model.DatabaseTypeBigQuery, func() drivers.Driver {
		return &warehouses.BigQueryDriver{}
	})
	// Big Data Warehouses
	dr.register(model.DatabaseTypeApacheHive, func() drivers.Driver {
		return warehouses.NewHiveDriver()
	})
	dr.register(model.DatabaseTypeTrino, func() drivers.Driver {
		return warehouses.NewTrinoDriver()
	})
	dr.register(model.DatabaseTypePresto, func() drivers.Driver {
		return warehouses.NewPrestoDriver()
	})
	dr.register(model.DatabaseTypeOpenLooKeng, func() drivers.Driver {
		return warehouses.NewOpenLooKengDriver()
	})
	dr.register(model.DatabaseTypeMaxCompute, func() drivers.Driver {
		return warehouses.NewMaxComputeDriver()
	})

	// OLAP Engines
	dr.register(model.DatabaseTypeClickHouse, func() drivers.Driver {
		return &olap.ClickHouseDriver{}
	})
	dr.register(model.DatabaseTypeApacheDoris, func() drivers.Driver {
		return &olap.DorisDriver{}
	})
	dr.register(model.DatabaseTypeStarRocks, func() drivers.Driver {
		return &olap.StarRocksDriver{}
	})
	dr.register(model.DatabaseTypeApacheDruid, func() drivers.Driver {
		return &olap.DruidDriver{}
	})
	dr.register(model.DatabaseTypeHologres, func() drivers.Driver {
		return olap.NewHologresDriver()
	})

	// NoSQL databases
	dr.register(model.DatabaseTypeMongoDB, func() drivers.Driver {
		return nosql.NewMongoDriver()
	})

	// Data Lake Table Formats
	dr.register(model.DatabaseTypeApacheIceberg, func() drivers.Driver {
		return &table_formats.IcebergDriver{}
	})
	dr.register(model.DatabaseTypeDeltaLake, func() drivers.Driver {
		return &table_formats.DeltaDriver{}
	})
	dr.register(model.DatabaseTypeApacheHudi, func() drivers.Driver {
		return &table_formats.HudiDriver{}
	})

	// Object Storage
	dr.register(model.DatabaseTypeS3Parquet, func() drivers.Driver {
		return &object_storage.S3ParquetDriver{}
	})
	dr.register(model.DatabaseTypeS3ORC, func() drivers.Driver {
		return &object_storage.S3ORCDriver{}
	})
	dr.register(model.DatabaseTypeS3Delta, func() drivers.Driver {
		return &object_storage.S3DeltaDriver{}
	})
	dr.register(model.DatabaseTypeS3Avro, func() drivers.Driver {
		return &object_storage.S3AvroDriver{}
	})
	dr.register(model.DatabaseTypeS3CSV, func() drivers.Driver {
		return &object_storage.S3CSVDriver{}
	})
	dr.register(model.DatabaseTypeGCSCSV, func() drivers.Driver {
		return &object_storage.GCSCSVDriver{}
	})
	dr.register(model.DatabaseTypeS3JSON, func() drivers.Driver {
		return &object_storage.S3JSONDriver{}
	})
	dr.register(model.DatabaseTypeMinIOParquet, func() drivers.Driver {
		return &object_storage.MinIOParquetDriver{}
	})
	dr.register(model.DatabaseTypeMinIOCSV, func() drivers.Driver {
		// MinIO exposes an S3-compatible API. Reusing the S3 CSV SQL adapter
		// gives MinIO sources the same config-aware database/sql path used by
		// the unified query service instead of a disconnected custom API.
		return &object_storage.S3CSVDriver{}
	})
	dr.register(model.DatabaseTypeMinIOORC, func() drivers.Driver {
		return &object_storage.MinIOORCDriver{}
	})
	dr.register(model.DatabaseTypeMinIOAvro, func() drivers.Driver {
		return &object_storage.MinIOAvroDriver{}
	})
	dr.register(model.DatabaseTypeMinIOJSON, func() drivers.Driver {
		return &object_storage.MinIOJSONDriver{}
	})
	dr.register(model.DatabaseTypeAlibabaOSSParquet, func() drivers.Driver {
		return &object_storage.OSSParquetDriver{}
	})
	dr.register(model.DatabaseTypeOSSCSV, func() drivers.Driver {
		return &object_storage.OSSCSVDriver{}
	})
	dr.register(model.DatabaseTypeAlibabaOSSJSON, func() drivers.Driver {
		return &object_storage.OSSJSONDriver{}
	})
	dr.register(model.DatabaseTypeAlibabaOSSAvro, func() drivers.Driver {
		return &object_storage.OSSAvroDriver{}
	})
	dr.register(model.DatabaseTypeAlibabaOSSORC, func() drivers.Driver {
		return &object_storage.OSSORCDriver{}
	})
	dr.register(model.DatabaseTypeTencentCOSParquet, func() drivers.Driver {
		return &object_storage.COSParquetDriver{}
	})
	dr.register(model.DatabaseTypeTencentCOSCSV, func() drivers.Driver {
		return &object_storage.COSCSVDriver{}
	})
	dr.register(model.DatabaseTypeTencentCOSJSON, func() drivers.Driver {
		return &object_storage.COSJSONDriver{}
	})
	dr.register(model.DatabaseTypeTencentCOSORC, func() drivers.Driver {
		return &object_storage.COSORCDriver{}
	})
	dr.register(model.DatabaseTypeTencentCOSAvro, func() drivers.Driver {
		return &object_storage.COSAvroDriver{}
	})
	dr.register(model.DatabaseTypeTencentCOSDelta, func() drivers.Driver {
		return &object_storage.COSDeltaDriver{}
	})
	dr.register(model.DatabaseTypeAzureBlobAvro, func() drivers.Driver { return &object_storage.AzureBlobAvroDriver{} })
	dr.register(model.DatabaseTypeAzureBlobORC, func() drivers.Driver { return &object_storage.AzureBlobORCDriver{} })
	dr.register(model.DatabaseTypeAzureBlobCSV, func() drivers.Driver {
		return &object_storage.AzureBlobCSVDriver{}
	})
	dr.register(model.DatabaseTypeAzureBlobJSON, func() drivers.Driver {
		return &object_storage.AzureBlobJSONDriver{}
	})
	dr.register(model.DatabaseTypeAzureBlobParquet, func() drivers.Driver {
		return &object_storage.AzureBlobParquetDriver{}
	})
	dr.register(model.DatabaseTypeOSSDelta, func() drivers.Driver {
		return &object_storage.OSSDeltaDriver{}
	})
	dr.register(model.DatabaseTypeMinIOIceberg, func() drivers.Driver {
		return &object_storage.MinIOIcebergDriver{}
	})
	dr.register(model.DatabaseTypeTencentCOSIceberg, func() drivers.Driver {
		return &object_storage.COSIcebergDriver{}
	})
	dr.register(model.DatabaseTypeAzureDelta, func() drivers.Driver {
		return &object_storage.AzureDeltaDriver{}
	})
	dr.register(model.DatabaseTypeAzureParquet, func() drivers.Driver {
		return &object_storage.AzureBlobParquetDriver{}
	})
	dr.register(model.DatabaseTypeMinIODelta, func() drivers.Driver {
		return &object_storage.MinIODeltaDriver{}
	})

}
