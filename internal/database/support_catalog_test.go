package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

type versionProbeConnector struct {
	query   string
	version string
	err     error
}

func (c versionProbeConnector) Connect(context.Context) (driver.Conn, error) {
	return versionProbeConn{connector: c}, nil
}

func (versionProbeConnector) Driver() driver.Driver { return versionProbeDriver{} }

type versionProbeDriver struct{}

func (versionProbeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("unexpected Open")
}

type versionProbeConn struct{ connector versionProbeConnector }

func (versionProbeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not implemented")
}
func (versionProbeConn) Close() error              { return nil }
func (versionProbeConn) Begin() (driver.Tx, error) { return nil, errors.New("not implemented") }
func (c versionProbeConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.connector.err != nil {
		return nil, c.connector.err
	}
	if query != c.connector.query {
		return nil, errors.New("unexpected probe query")
	}
	return &versionProbeRows{version: c.connector.version}, nil
}

type versionProbeRows struct {
	version string
	done    bool
}

func (*versionProbeRows) Columns() []string { return []string{"version"} }
func (*versionProbeRows) Close() error      { return nil }
func (r *versionProbeRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	dest[0] = r.version
	return nil
}

func TestSupportCatalogSeparatesRegistrationFromVerification(t *testing.T) {
	registry := NewDriverRegistry()
	catalog := SupportCatalog(registry)
	profiles := make(map[string]ConnectorSupport, len(catalog))
	for _, profile := range catalog {
		profiles[profile.Type] = profile
	}

	for _, dbType := range registry.ListDrivers() {
		profile, ok := profiles[string(dbType)]
		if !ok {
			t.Fatalf("registered type %q has no catalog entry", dbType)
		}
		if !profile.Supported || profile.Status == "" {
			t.Errorf("registered type %q has invalid support state: %+v", dbType, profile)
		}
		verifiedVersions := map[model.DatabaseType]struct {
			productRange string
			server       []string
		}{
			model.DatabaseTypePostgreSQL:  {productRange: "15.19, 16.15, 17.11, and 18.6", server: []string{"15.19", "16.15", "17.11", "18.6"}},
			model.DatabaseTypeMySQL:       {productRange: "8.0.46 and 8.4.13", server: []string{"8.0.46", "8.4.13"}},
			model.DatabaseTypeMariaDB:     {productRange: "10.11.19, 11.4.13, 11.8.9, and 12.3.3", server: []string{"10.11.19", "11.4.13", "11.8.9", "12.3.3"}},
			model.DatabaseTypeTiDB:        {productRange: "8.5.8", server: []string{"8.0.11-TiDB-v8.5.8"}},
			model.DatabaseTypeOpenGauss:   {productRange: "6.0.0", server: []string{"openGauss 6.0.0 build aee4abd5"}},
			model.DatabaseTypeClickHouse:  {productRange: "26.8.20.9", server: []string{"26.8.20.9"}},
			model.DatabaseTypeSQLServer:   {productRange: "16.0.4305.5", server: []string{"16.0.4305.5"}},
			model.DatabaseTypeOracle:      {productRange: "23.26.3.0.0", server: []string{"23.26.3.0.0"}},
			model.DatabaseTypeApacheDoris: {productRange: "Apache Doris 4.1.3", server: []string{"Apache Doris 4.1.3"}},
			model.DatabaseTypeStarRocks:   {productRange: "StarRocks 4.1.6", server: []string{"4.1.6"}},
			model.DatabaseTypeHDFSCSV:     {productRange: "Apache Hadoop 3.5.0", server: []string{"3.5.0"}},
			model.DatabaseTypeHDFSParquet: {productRange: "Apache Hadoop 3.5.0", server: []string{"3.5.0"}},
		}
		if dbType == model.DatabaseTypeS3CSV {
			if !profile.EndToEndVerified || profile.Status != ConnectorStatusExperimental || profile.SupportedVersionRange != "Garage v2.4.1 only" || !reflect.DeepEqual(profile.VerifiedServerVersions, []string{"Garage v2.4.1"}) {
				t.Errorf("S3 CSV contract evidence must remain provider-specific and experimental: %+v", profile)
			}
			continue
		}
		if dbType == model.DatabaseTypeS3Parquet {
			if !profile.EndToEndVerified || profile.Status != ConnectorStatusExperimental || profile.SupportedVersionRange != "Garage v2.4.1 only" || !reflect.DeepEqual(profile.VerifiedServerVersions, []string{"Garage v2.4.1"}) {
				t.Errorf("S3 Parquet contract evidence must remain provider-specific and experimental: %+v", profile)
			}
			continue
		}
		if dbType == model.DatabaseTypeS3JSON {
			if !profile.EndToEndVerified || profile.Status != ConnectorStatusExperimental || profile.SupportedVersionRange != "Garage v2.4.1 only" || !reflect.DeepEqual(profile.VerifiedServerVersions, []string{"Garage v2.4.1"}) || !profile.Capabilities.SupportsSQL {
				t.Errorf("S3 JSON contract evidence must remain provider-specific and experimental: %+v", profile)
			}
			continue
		}
		if dbType == model.DatabaseTypeS3Avro {
			if !profile.EndToEndVerified || profile.Status != ConnectorStatusExperimental || profile.SupportedVersionRange != "Garage v2.4.1 only" || !reflect.DeepEqual(profile.VerifiedServerVersions, []string{"Garage v2.4.1"}) || !profile.Capabilities.SupportsSQL {
				t.Errorf("S3 Avro contract evidence must remain provider-specific and experimental: %+v", profile)
			}
			continue
		}
		if dbType == model.DatabaseTypeS3ORC {
			if !profile.EndToEndVerified || profile.Status != ConnectorStatusExperimental || profile.SupportedVersionRange != "Garage v2.4.1 only" || !reflect.DeepEqual(profile.VerifiedServerVersions, []string{"Garage v2.4.1"}) || !profile.Capabilities.SupportsSQL {
				t.Errorf("S3 ORC contract evidence must remain provider-specific and experimental: %+v", profile)
			}
			continue
		}
		if dbType == model.DatabaseTypeMinIOCSV {
			if !profile.EndToEndVerified || profile.Status != ConnectorStatusExperimental || profile.SupportedVersionRange != "RELEASE.2025-04-22T22-12-26Z only" || !reflect.DeepEqual(profile.VerifiedServerVersions, []string{"RELEASE.2025-04-22T22-12-26Z"}) {
				t.Errorf("MinIO CSV contract evidence must remain provider-specific and experimental: %+v", profile)
			}
			continue
		}
		if dbType == model.DatabaseTypeMinIOParquet {
			if !profile.EndToEndVerified || profile.Status != ConnectorStatusExperimental || profile.SupportedVersionRange != "RELEASE.2025-04-22T22-12-26Z only" || !reflect.DeepEqual(profile.VerifiedServerVersions, []string{"RELEASE.2025-04-22T22-12-26Z"}) {
				t.Errorf("MinIO Parquet contract evidence must remain provider-specific and experimental: %+v", profile)
			}
			continue
		}
		if dbType == model.DatabaseTypeMongoDB {
			if !profile.EndToEndVerified || profile.Status != ConnectorStatusExperimental || profile.SupportedVersionRange != "8.0.34 only" || !reflect.DeepEqual(profile.VerifiedServerVersions, []string{"8.0.34"}) {
				t.Errorf("MongoDB subset evidence must remain version-specific and experimental: %+v", profile)
			}
			continue
		}
		if expected, ok := verifiedVersions[dbType]; ok {
			if !profile.EndToEndVerified || profile.Status != ConnectorStatusEndToEndVerified || profile.SupportedVersionRange != expected.productRange+" only" || !reflect.DeepEqual(profile.VerifiedServerVersions, expected.server) {
				t.Errorf("%s version contract evidence is missing or too broad: %+v", dbType, profile)
			}
			continue
		}
		if profile.EndToEndVerified || len(profile.VerifiedServerVersions) != 0 || profile.SupportedVersionRange != "" {
			t.Errorf("type %q claims unrecorded version verification: %+v", dbType, profile)
		}
	}

	ozoneParquet := profiles[string(model.DatabaseTypeOzoneParquet)]
	if !ozoneParquet.Registered || !ozoneParquet.Supported || ozoneParquet.Status != ConnectorStatusExperimental || !ozoneParquet.Capabilities.SupportsSQL {
		t.Errorf("Ozone Parquet must expose its experimental S3 Gateway SQL path: %+v", ozoneParquet)
	}
	if !containsSupportLimitation(ozoneParquet.Limitations, "S3 Gateway endpoint") || !containsSupportLimitation(ozoneParquet.Limitations, "64 MiB") {
		t.Errorf("Ozone Parquet catalog must disclose endpoint and resource limits: %+v", ozoneParquet.Limitations)
	}
	ozoneText := profiles[string(model.DatabaseTypeOzoneText)]
	if !ozoneText.Registered || !ozoneText.Supported || ozoneText.Status != ConnectorStatusExperimental || !ozoneText.Capabilities.SupportsSQL {
		t.Errorf("Ozone text must expose its experimental S3 Gateway SQL path: %+v", ozoneText)
	}
	if !containsSupportLimitation(ozoneText.Limitations, "one configured UTF-8 text object") || !containsSupportLimitation(ozoneText.Limitations, "100,000 lines") {
		t.Errorf("Ozone text catalog must disclose its object and line limits: %+v", ozoneText.Limitations)
	}
	ozoneAvro := profiles[string(model.DatabaseTypeOzoneAvro)]
	if !ozoneAvro.Registered || !ozoneAvro.Supported || ozoneAvro.Status != ConnectorStatusExperimental || !ozoneAvro.Capabilities.SupportsSQL {
		t.Errorf("Ozone Avro must expose its experimental S3 Gateway SQL path: %+v", ozoneAvro)
	}
	if !containsSupportLimitation(ozoneAvro.Limitations, "one configured Avro OCF object") || !containsSupportLimitation(ozoneAvro.Limitations, "100,000 records") {
		t.Errorf("Ozone Avro catalog must disclose its object and record limits: %+v", ozoneAvro.Limitations)
	}

	hdfsText := profiles[string(model.DatabaseTypeHDFSText)]
	if !hdfsText.Registered || !hdfsText.Supported || hdfsText.Status != ConnectorStatusExperimental || !hdfsText.Capabilities.SupportsSQL {
		t.Errorf("HDFS text must expose its experimental read-only SQL path: %+v", hdfsText)
	}
	if !containsSupportLimitation(hdfsText.Limitations, "one configured UTF-8 text file") || !containsSupportLimitation(hdfsText.Limitations, "100,000 lines") {
		t.Errorf("HDFS text catalog must disclose its file and line boundaries: %+v", hdfsText.Limitations)
	}
	hdfsCSV := profiles[string(model.DatabaseTypeHDFSCSV)]
	if !hdfsCSV.Registered || !hdfsCSV.Supported || hdfsCSV.Status != ConnectorStatusEndToEndVerified || !hdfsCSV.EndToEndVerified || !hdfsCSV.Capabilities.SupportsSQL {
		t.Errorf("HDFS CSV must expose only its verified read-only SQL path: %+v", hdfsCSV)
	}
	if hdfsCSV.SupportedVersionRange != "Apache Hadoop 3.5.0 only" || !reflect.DeepEqual(hdfsCSV.VerifiedServerVersions, []string{"3.5.0"}) {
		t.Errorf("HDFS CSV contract must be limited to Hadoop 3.5.0: %+v", hdfsCSV)
	}
	if !containsSupportLimitation(hdfsCSV.Limitations, "one configured CSV file") || !containsSupportLimitation(hdfsCSV.Limitations, "64 MiB") || !containsSupportLimitation(hdfsCSV.Limitations, "does not cover Kerberos") {
		t.Errorf("HDFS CSV catalog must disclose its file, memory, and security boundaries: %+v", hdfsCSV.Limitations)
	}
	hdfsParquetCompressed := profiles[string(model.DatabaseTypeHDFSParquetCompressed)]
	if !hdfsParquetCompressed.Registered || !hdfsParquetCompressed.Supported || hdfsParquetCompressed.Status != ConnectorStatusExperimental || !hdfsParquetCompressed.Capabilities.SupportsSQL {
		t.Errorf("HDFS compressed Parquet must expose its experimental read-only SQL path: %+v", hdfsParquetCompressed)
	}
	if !containsSupportLimitation(hdfsParquetCompressed.Limitations, "Parquet page compression") || !containsSupportLimitation(hdfsParquetCompressed.Limitations, "64 MiB") {
		t.Errorf("HDFS compressed Parquet catalog must disclose codec and file limits: %+v", hdfsParquetCompressed.Limitations)
	}
	hdfsParquet := profiles[string(model.DatabaseTypeHDFSParquet)]
	if !hdfsParquet.Registered || !hdfsParquet.Supported || hdfsParquet.Status != ConnectorStatusEndToEndVerified || !hdfsParquet.EndToEndVerified || !hdfsParquet.Capabilities.SupportsSQL {
		t.Errorf("HDFS Parquet must expose only its verified read-only SQL path: %+v", hdfsParquet)
	}
	if hdfsParquet.SupportedVersionRange != "Apache Hadoop 3.5.0 only" || !reflect.DeepEqual(hdfsParquet.VerifiedServerVersions, []string{"3.5.0"}) {
		t.Errorf("HDFS Parquet contract must be limited to Hadoop 3.5.0: %+v", hdfsParquet)
	}
	if !containsSupportLimitation(hdfsParquet.Limitations, "one configured Parquet file") || !containsSupportLimitation(hdfsParquet.Limitations, "64 MiB") || !containsSupportLimitation(hdfsParquet.Limitations, "does not cover Kerberos") {
		t.Errorf("HDFS Parquet catalog must disclose its file, memory, and security boundaries: %+v", hdfsParquet.Limitations)
	}
	hdfsAvro := profiles[string(model.DatabaseTypeHDFSAvro)]
	if !hdfsAvro.Registered || !hdfsAvro.Supported || hdfsAvro.Status != ConnectorStatusExperimental || !hdfsAvro.Capabilities.SupportsSQL {
		t.Errorf("HDFS Avro must expose its experimental read-only SQL path: %+v", hdfsAvro)
	}
	if !containsSupportLimitation(hdfsAvro.Limitations, "one configured Avro OCF file") || !containsSupportLimitation(hdfsAvro.Limitations, "64 MiB") {
		t.Errorf("HDFS Avro catalog must disclose its file and schema boundaries: %+v", hdfsAvro.Limitations)
	}
	mongo := profiles[string(model.DatabaseTypeMongoDB)]
	if mongo.Status != ConnectorStatusExperimental || !mongo.Capabilities.SupportsSQL || !mongo.EndToEndVerified {
		t.Errorf("MongoDB must expose versioned contract evidence while remaining experimental: %+v", mongo)
	}
	if !containsSupportLimitation(mongo.Limitations, "restricted single-collection SELECT subset") {
		t.Errorf("MongoDB catalog must describe its SQL mapping boundary: %+v", mongo.Limitations)
	}
	if !containsSupportLimitation(mongo.Limitations, "MongoDB 8.0.34 contract") {
		t.Errorf("MongoDB catalog must record the pinned live contract scope: %+v", mongo.Limitations)
	}
	druid := profiles[string(model.DatabaseTypeApacheDruid)]
	if druid.Status != ConnectorStatusExperimental || druid.EndToEndVerified || !druid.Capabilities.SupportsSQL || druid.Capabilities.SupportsTimeTravel {
		t.Errorf("Druid must disclose its read-only HTTP adapter as experimental without a live version contract: %+v", druid)
	}
	if !containsSupportLimitation(druid.Limitations, "accepts only top-level SELECT or EXPLAIN") || !containsSupportLimitation(druid.Limitations, "no live Druid cluster") {
		t.Errorf("Druid catalog must state the unified query boundary and lack of a live contract: %+v", druid.Limitations)
	}
	sqlServer := profiles[string(model.DatabaseTypeSQLServer)]
	if !containsSupportLimitation(sqlServer.Limitations, "Database-side read-only session enforcement is not supported") {
		t.Errorf("SQL Server catalog must disclose that database-side read-only enforcement is unavailable: %+v", sqlServer.Limitations)
	}

	s3CSV := profiles[string(model.DatabaseTypeS3CSV)]
	if s3CSV.Status != ConnectorStatusExperimental || !s3CSV.EndToEndVerified || s3CSV.SupportedVersionRange != "Garage v2.4.1 only" || !reflect.DeepEqual(s3CSV.VerifiedServerVersions, []string{"Garage v2.4.1"}) {
		t.Errorf("S3 CSV must remain experimental while recording its pinned Garage contract: %+v", s3CSV)
	}
	if !containsSupportLimitation(s3CSV.Limitations, "downloads the full object into gateway memory") {
		t.Errorf("S3 CSV catalog must disclose full-object buffering: %+v", s3CSV.Limitations)
	}
	if !containsSupportLimitation(s3CSV.Limitations, "does not verify AWS S3") {
		t.Errorf("S3 CSV catalog must bound Garage evidence to the tested provider: %+v", s3CSV.Limitations)
	}
	minioCSV := profiles[string(model.DatabaseTypeMinIOCSV)]
	if minioCSV.Status != ConnectorStatusExperimental || !minioCSV.EndToEndVerified || minioCSV.SupportedVersionRange != "RELEASE.2025-04-22T22-12-26Z only" || !reflect.DeepEqual(minioCSV.VerifiedServerVersions, []string{"RELEASE.2025-04-22T22-12-26Z"}) || !minioCSV.Capabilities.SupportsSQL {
		t.Errorf("MinIO CSV must expose its SQL adapter with its pinned live contract scope: %+v", minioCSV)
	}
	if !containsSupportLimitation(minioCSV.Limitations, "same config-aware, read-only S3 CSV SQL adapter") {
		t.Errorf("MinIO CSV catalog must describe the shared gateway query path: %+v", minioCSV.Limitations)
	}
	minioParquet := profiles[string(model.DatabaseTypeMinIOParquet)]
	if minioParquet.Status != ConnectorStatusExperimental || !minioParquet.EndToEndVerified || !minioParquet.Capabilities.SupportsSQL {
		t.Errorf("MinIO Parquet must expose its SQL path with pinned live contract evidence: %+v", minioParquet)
	}
	if !containsSupportLimitation(minioParquet.Limitations, "one configured flat-schema Parquet object") || !containsSupportLimitation(minioParquet.Limitations, "Nested or repeated fields") {
		t.Errorf("MinIO Parquet catalog must describe the adapter boundary: %+v", minioParquet.Limitations)
	}
	s3Parquet := profiles[string(model.DatabaseTypeS3Parquet)]
	if s3Parquet.Status != ConnectorStatusExperimental || !s3Parquet.EndToEndVerified || !s3Parquet.Capabilities.SupportsSQL {
		t.Errorf("S3 Parquet must expose its SQL path with pinned Garage contract evidence: %+v", s3Parquet)
	}
	if !containsSupportLimitation(s3Parquet.Limitations, "one configured flat-schema Parquet object") || !containsSupportLimitation(s3Parquet.Limitations, "does not verify AWS S3") {
		t.Errorf("S3 Parquet catalog must disclose its adapter and provider boundary: %+v", s3Parquet.Limitations)
	}
	s3JSON := profiles[string(model.DatabaseTypeS3JSON)]
	if s3JSON.Status != ConnectorStatusExperimental || !s3JSON.EndToEndVerified || !s3JSON.Capabilities.SupportsSQL {
		t.Errorf("S3 JSON must expose its SQL path with pinned Garage contract evidence: %+v", s3JSON)
	}
	if !containsSupportLimitation(s3JSON.Limitations, "one configured JSON object") || !containsSupportLimitation(s3JSON.Limitations, "Nested object fields") {
		t.Errorf("S3 JSON catalog must disclose its format and object boundary: %+v", s3JSON.Limitations)
	}
	s3Avro := profiles[string(model.DatabaseTypeS3Avro)]
	if s3Avro.Status != ConnectorStatusExperimental || !s3Avro.EndToEndVerified || !s3Avro.Capabilities.SupportsSQL {
		t.Errorf("S3 Avro must expose its SQL path with pinned Garage contract evidence: %+v", s3Avro)
	}
	if !containsSupportLimitation(s3Avro.Limitations, "one configured Avro OCF object") || !containsSupportLimitation(s3Avro.Limitations, "Nested records") {
		t.Errorf("S3 Avro catalog must disclose its format and object boundary: %+v", s3Avro.Limitations)
	}
	s3ORC := profiles[string(model.DatabaseTypeS3ORC)]
	if s3ORC.Status != ConnectorStatusExperimental || !s3ORC.EndToEndVerified || !s3ORC.Capabilities.SupportsSQL {
		t.Errorf("S3 ORC must expose its SQL path with pinned Garage contract evidence: %+v", s3ORC)
	}
	if !containsSupportLimitation(s3ORC.Limitations, "one configured flat-schema ORC object") || !containsSupportLimitation(s3ORC.Limitations, "Nested/repeated types") {
		t.Errorf("S3 ORC catalog must disclose its format and object boundary: %+v", s3ORC.Limitations)
	}
	for _, dbType := range []model.DatabaseType{
		model.DatabaseTypeAlibabaOSSParquet, model.DatabaseTypeTencentCOSParquet,
		model.DatabaseTypeAzureBlobParquet, model.DatabaseTypeAzureParquet,
	} {
		profile := profiles[string(dbType)]
		if profile.Status != ConnectorStatusExperimental || !profile.Capabilities.SupportsSQL {
			t.Errorf("%s must expose its experimental read-only SQL adapter: %+v", dbType, profile)
		}
		if !containsSupportLimitation(profile.Limitations, "one configured") || !containsSupportLimitation(profile.Limitations, "downloaded into gateway memory") {
			t.Errorf("%s must disclose its object scope and buffering boundary: %+v", dbType, profile.Limitations)
		}
	}
	ossDelta := profiles[string(model.DatabaseTypeOSSDelta)]
	if ossDelta.Status != ConnectorStatusExperimental || !ossDelta.Capabilities.SupportsSQL || ossDelta.EndToEndVerified {
		t.Errorf("OSS Delta must expose its experimental SQL adapter without claiming a live version contract: %+v", ossDelta)
	}
	if !containsSupportLimitation(ossDelta.Limitations, "checkpoints") || !containsSupportLimitation(ossDelta.Limitations, "64 MiB") || !containsSupportLimitation(ossDelta.Limitations, "100,000 decoded rows") {
		t.Errorf("OSS Delta catalog must disclose its snapshot and memory boundaries: %+v", ossDelta.Limitations)
	}
	s3Delta := profiles[string(model.DatabaseTypeS3Delta)]
	if s3Delta.Status != ConnectorStatusExperimental || !s3Delta.Capabilities.SupportsSQL || s3Delta.EndToEndVerified {
		t.Errorf("S3 Delta must expose an experimental SQL adapter without claiming a live version contract: %+v", s3Delta)
	}
	if !containsSupportLimitation(s3Delta.Limitations, "checkpoints") || !containsSupportLimitation(s3Delta.Limitations, "100,000 decoded rows") || !containsSupportLimitation(s3Delta.Limitations, "does not establish compatibility with AWS S3") {
		t.Errorf("S3 Delta catalog must disclose its snapshot, memory, and provider boundaries: %+v", s3Delta.Limitations)
	}
	minioDelta := profiles[string(model.DatabaseTypeMinIODelta)]
	if minioDelta.Status != ConnectorStatusExperimental || !minioDelta.Capabilities.SupportsSQL || minioDelta.EndToEndVerified {
		t.Errorf("MinIO Delta must expose its experimental SQL adapter without claiming a live version contract: %+v", minioDelta)
	}
	if !containsSupportLimitation(minioDelta.Limitations, "checkpoints") || !containsSupportLimitation(minioDelta.Limitations, "100,000 decoded rows") {
		t.Errorf("MinIO Delta catalog must disclose its snapshot and memory boundaries: %+v", minioDelta.Limitations)
	}
	minioIceberg := profiles[string(model.DatabaseTypeMinIOIceberg)]
	if minioIceberg.Status != ConnectorStatusExperimental || !minioIceberg.Capabilities.SupportsSQL || minioIceberg.EndToEndVerified {
		t.Errorf("MinIO Iceberg must expose its experimental SQL adapter without claiming a live version contract: %+v", minioIceberg)
	}
	if !containsSupportLimitation(minioIceberg.Limitations, "one configured table") || !containsSupportLimitation(minioIceberg.Limitations, "Writes") {
		t.Errorf("MinIO Iceberg catalog must disclose its table and read-only boundaries: %+v", minioIceberg.Limitations)
	}
	minioORC := profiles[string(model.DatabaseTypeMinIOORC)]
	if minioORC.Status != ConnectorStatusExperimental || !minioORC.Capabilities.SupportsSQL || minioORC.EndToEndVerified {
		t.Errorf("MinIO ORC must expose its experimental SQL adapter without claiming a live version contract: %+v", minioORC)
	}
	if !containsSupportLimitation(minioORC.Limitations, "one configured ORC object") || !containsSupportLimitation(minioORC.Limitations, "nested/repeated") {
		t.Errorf("MinIO ORC catalog must disclose its object and schema boundaries: %+v", minioORC.Limitations)
	}
	minioAvro := profiles[string(model.DatabaseTypeMinIOAvro)]
	if minioAvro.Status != ConnectorStatusExperimental || !minioAvro.Capabilities.SupportsSQL || minioAvro.EndToEndVerified {
		t.Errorf("MinIO Avro must expose its experimental SQL adapter without claiming a live version contract: %+v", minioAvro)
	}
	if !containsSupportLimitation(minioAvro.Limitations, "one configured Avro OCF object") || !containsSupportLimitation(minioAvro.Limitations, "Nested records") {
		t.Errorf("MinIO Avro catalog must disclose its object and schema boundaries: %+v", minioAvro.Limitations)
	}
	minioJSON := profiles[string(model.DatabaseTypeMinIOJSON)]
	if minioJSON.Status != ConnectorStatusExperimental || !minioJSON.Capabilities.SupportsSQL || minioJSON.EndToEndVerified {
		t.Errorf("MinIO JSON must expose its experimental SQL adapter without claiming a live version contract: %+v", minioJSON)
	}
	if !containsSupportLimitation(minioJSON.Limitations, "one configured JSON object") || !containsSupportLimitation(minioJSON.Limitations, "flatten_nested=true") {
		t.Errorf("MinIO JSON catalog must disclose its object and schema boundaries: %+v", minioJSON.Limitations)
	}
	azureDelta := profiles[string(model.DatabaseTypeAzureDelta)]
	if azureDelta.Status != ConnectorStatusExperimental || !azureDelta.Capabilities.SupportsSQL || azureDelta.EndToEndVerified {
		t.Errorf("Azure Delta must expose its experimental SQL adapter without claiming a live version contract: %+v", azureDelta)
	}
	if !containsSupportLimitation(azureDelta.Limitations, "checkpoints") || !containsSupportLimitation(azureDelta.Limitations, "100,000 decoded rows") {
		t.Errorf("Azure Delta catalog must disclose its snapshot and memory boundaries: %+v", azureDelta.Limitations)
	}
}

func containsSupportLimitation(limitations []string, expected string) bool {
	for _, limitation := range limitations {
		if strings.Contains(limitation, expected) {
			return true
		}
	}
	return false
}

func TestDriverInfoAndCompatibilityUseSupportCatalog(t *testing.T) {
	checker := &HealthChecker{registry: NewDriverRegistry()}
	info := checker.GetDriverInfo()
	catalog := checker.GetSupportCatalog()
	compatibility := CompatibilityByType(catalog)

	postgres, ok := info[string(model.DatabaseTypePostgreSQL)]
	if !ok || postgres.Status != ConnectorStatusEndToEndVerified {
		t.Fatalf("type endpoint did not expose the verified PostgreSQL catalog profile: %+v", postgres)
	}
	if !postgres.EndToEndVerified || postgres.SupportedVersionRange != "15.19, 16.15, 17.11, and 18.6 only" || !reflect.DeepEqual(postgres.VerifiedServerVersions, []string{"15.19", "16.15", "17.11", "18.6"}) {
		t.Fatalf("type endpoint has missing or overly broad PostgreSQL verification evidence: %+v", postgres)
	}
	mysql, ok := info[string(model.DatabaseTypeMySQL)]
	if !ok || mysql.Status != ConnectorStatusEndToEndVerified || !mysql.EndToEndVerified || mysql.SupportedVersionRange != "8.0.46 and 8.4.13 only" || !reflect.DeepEqual(mysql.VerifiedServerVersions, []string{"8.0.46", "8.4.13"}) {
		t.Fatalf("type endpoint has missing or overly broad MySQL verification evidence: %+v", mysql)
	}
	mariaDB, ok := info[string(model.DatabaseTypeMariaDB)]
	if !ok || mariaDB.Status != ConnectorStatusEndToEndVerified || !mariaDB.EndToEndVerified || mariaDB.SupportedVersionRange != "10.11.19, 11.4.13, 11.8.9, and 12.3.3 only" || !reflect.DeepEqual(mariaDB.VerifiedServerVersions, []string{"10.11.19", "11.4.13", "11.8.9", "12.3.3"}) {
		t.Fatalf("type endpoint has missing or overly broad MariaDB verification evidence: %+v", mariaDB)
	}
	tiDB, ok := info[string(model.DatabaseTypeTiDB)]
	if !ok || tiDB.Status != ConnectorStatusEndToEndVerified || !tiDB.EndToEndVerified || tiDB.SupportedVersionRange != "8.5.8 only" || !reflect.DeepEqual(tiDB.VerifiedServerVersions, []string{"8.0.11-TiDB-v8.5.8"}) || tiDB.CompatibilityMode != "mysql_compatible" {
		t.Fatalf("type endpoint has missing or overly broad TiDB verification evidence: %+v", tiDB)
	}
	openGauss, ok := info[string(model.DatabaseTypeOpenGauss)]
	if !ok || openGauss.Status != ConnectorStatusEndToEndVerified || !openGauss.EndToEndVerified || openGauss.SupportedVersionRange != "6.0.0 only" || !reflect.DeepEqual(openGauss.VerifiedServerVersions, []string{"openGauss 6.0.0 build aee4abd5"}) || openGauss.CompatibilityMode != "postgresql_compatible" {
		t.Fatalf("type endpoint has missing or overly broad openGauss verification evidence: %+v", openGauss)
	}
	clickHouse, ok := info[string(model.DatabaseTypeClickHouse)]
	if !ok || clickHouse.Status != ConnectorStatusEndToEndVerified || !clickHouse.EndToEndVerified || clickHouse.SupportedVersionRange != "26.8.20.9 only" || !reflect.DeepEqual(clickHouse.VerifiedServerVersions, []string{"26.8.20.9"}) || clickHouse.CompatibilityMode != "clickhouse_native_protocol" {
		t.Fatalf("type endpoint has missing or overly broad ClickHouse verification evidence: %+v", clickHouse)
	}
	oracle, ok := info[string(model.DatabaseTypeOracle)]
	if !ok || oracle.Status != ConnectorStatusEndToEndVerified || !oracle.EndToEndVerified || oracle.SupportedVersionRange != "23.26.3.0.0 only" || !reflect.DeepEqual(oracle.VerifiedServerVersions, []string{"23.26.3.0.0"}) {
		t.Fatalf("type endpoint has missing or overly broad Oracle verification evidence: %+v", oracle)
	}
	if ozone, ok := info[string(model.DatabaseTypeOzoneParquet)]; !ok || ozone.Status != ConnectorStatusExperimental || !ozone.Capabilities.SupportsSQL {
		t.Fatalf("type endpoint must advertise the experimental Ozone Parquet adapter: %+v", ozone)
	}
	if ozone, ok := compatibility[string(model.DatabaseTypeOzoneParquet)]; !ok || ozone.Status != ConnectorStatusExperimental {
		t.Fatalf("compatibility endpoint must report Ozone Parquet as experimental: %+v", ozone)
	}
	if len(compatibility) != len(catalog) {
		t.Fatalf("compatibility profiles = %d, catalog = %d", len(compatibility), len(catalog))
	}
}

func TestServerVersionProbeAndProductRecognition(t *testing.T) {
	for _, dbType := range []model.DatabaseType{
		model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB, model.DatabaseTypeTiDB,
		model.DatabaseTypePostgreSQL, model.DatabaseTypeOpenGauss, model.DatabaseTypeOracle,
		model.DatabaseTypeSQLServer, model.DatabaseTypeTrino,
	} {
		if _, ok := serverVersionProbe(dbType); !ok {
			t.Errorf("%s has no version probe", dbType)
		}
	}
	if _, ok := serverVersionProbe(model.DatabaseTypeMongoDB); ok {
		t.Fatal("MongoDB should not claim a SQL version probe")
	}
	tests := []struct{ version, product string }{
		{"8.0.37-TiDB-v8.1.0", "TiDB"},
		{"(openGauss 6.0.0 build aee4abd5) compiled on x86_64", "openGauss"},
		{"PostgreSQL 16.2 on x86_64", "PostgreSQL"},
		{"KingbaseES V008R006", "KingbaseES"},
		{"19.0.0.0.0", "unknown"},
	}
	for _, tt := range tests {
		if got := identifyServerProduct(tt.version); got != tt.product {
			t.Errorf("identifyServerProduct(%q) = %q, want %q", tt.version, got, tt.product)
		}
	}
}

func TestDetectServerInfoReportsVerifiedSignatureOrUnknown(t *testing.T) {
	db := sql.OpenDB(versionProbeConnector{query: "SELECT VERSION()", version: "8.0.37-TiDB-v8.1.0"})
	defer db.Close()
	info := detectServerInfo(context.Background(), db, model.DatabaseTypeTiDB, drivers.CategoryRelational)
	if info.Status != "detected" || info.Product != "TiDB" || info.Version != "8.0.37-TiDB-v8.1.0" {
		t.Fatalf("detected server info = %+v", info)
	}

	openGaussVersion := "(openGauss 6.0.0 build aee4abd5) compiled on x86_64"
	openGaussDB := sql.OpenDB(versionProbeConnector{query: "SELECT version()", version: openGaussVersion})
	defer openGaussDB.Close()
	openGaussInfo := detectServerInfo(context.Background(), openGaussDB, model.DatabaseTypeOpenGauss, drivers.CategoryRelational)
	if openGaussInfo.Status != "detected" || openGaussInfo.Product != "openGauss" || openGaussInfo.Version != openGaussVersion {
		t.Fatalf("detected openGauss server info = %+v", openGaussInfo)
	}

	plainVersionDB := sql.OpenDB(versionProbeConnector{query: "SELECT VERSION()", version: "8.0.37"})
	defer plainVersionDB.Close()
	plain := detectServerInfo(context.Background(), plainVersionDB, model.DatabaseTypeMySQL, drivers.CategoryRelational)
	if plain.Status != "detected" || plain.Product != "unknown" || plain.Version != "8.0.37" {
		t.Fatalf("unidentified compatible server info = %+v", plain)
	}

	noProbe := detectServerInfo(context.Background(), nil, model.DatabaseTypeMongoDB, drivers.CategoryNoSQL)
	if noProbe.Status != "not_available" || noProbe.Product != "unknown" || noProbe.Version != "unknown" {
		t.Fatalf("server without a SQL probe = %+v", noProbe)
	}
}

func TestSupportCatalogDocumentationIsCurrent(t *testing.T) {
	want := RenderSupportCatalogMarkdown(SupportCatalog(NewDriverRegistry()))
	got, err := os.ReadFile("../../docs/data-source-support.md")
	if err != nil {
		t.Fatalf("read generated support catalog: %v", err)
	}
	if !reflect.DeepEqual(string(got), want) {
		t.Fatal("docs/data-source-support.md is stale; run go run ./cmd/generate-support-catalog")
	}
}
