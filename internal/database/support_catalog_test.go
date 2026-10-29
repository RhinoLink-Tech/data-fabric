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
			model.DatabaseTypePostgreSQL:  {productRange: "17.11", server: []string{"17.11"}},
			model.DatabaseTypeMySQL:       {productRange: "8.0.46", server: []string{"8.0.46"}},
			model.DatabaseTypeMariaDB:     {productRange: "11.8.9", server: []string{"11.8.9"}},
			model.DatabaseTypeTiDB:        {productRange: "8.5.8", server: []string{"8.0.11-TiDB-v8.5.8"}},
			model.DatabaseTypeOpenGauss:   {productRange: "6.0.0", server: []string{"openGauss 6.0.0 build aee4abd5"}},
			model.DatabaseTypeClickHouse:  {productRange: "26.8.20.9", server: []string{"26.8.20.9"}},
			model.DatabaseTypeSQLServer:   {productRange: "16.0.4305.5", server: []string{"16.0.4305.5"}},
			model.DatabaseTypeOracle:      {productRange: "23.26.3.0.0", server: []string{"23.26.3.0.0"}},
			model.DatabaseTypeApacheDoris: {productRange: "Apache Doris 4.1.3", server: []string{"Apache Doris 4.1.3"}},
			model.DatabaseTypeStarRocks:   {productRange: "StarRocks 4.1.6", server: []string{"4.1.6"}},
		}
		if dbType == model.DatabaseTypeS3CSV {
			if !profile.EndToEndVerified || profile.Status != ConnectorStatusExperimental || profile.SupportedVersionRange != "Garage v2.4.1 only" || !reflect.DeepEqual(profile.VerifiedServerVersions, []string{"Garage v2.4.1"}) {
				t.Errorf("S3 CSV contract evidence must remain provider-specific and experimental: %+v", profile)
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

	for _, dbType := range []model.DatabaseType{model.DatabaseTypeOzoneParquet, model.DatabaseTypeOzoneText, model.DatabaseTypeOzoneAvro} {
		profile, ok := profiles[string(dbType)]
		if !ok || profile.Supported || profile.Status != ConnectorStatusUnsupported {
			t.Errorf("unsupported type %q has incorrect profile: %+v", dbType, profile)
		}
	}
	for _, dbType := range []model.DatabaseType{
		model.DatabaseTypeHDFSAvro, model.DatabaseTypeHDFSCSV, model.DatabaseTypeHDFSParquet,
		model.DatabaseTypeHDFSText, model.DatabaseTypeHDFSParquetCompressed,
	} {
		profile := profiles[string(dbType)]
		if profile.Registered || profile.Supported || profile.Status != ConnectorStatusUnsupported {
			t.Errorf("HDFS type %q must be explicitly unsupported until a query path exists: %+v", dbType, profile)
		}
		if _, err := registry.GetDriver(dbType); err == nil {
			t.Errorf("HDFS type %q still resolves to a registered driver", dbType)
		}
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
	for _, dbType := range []model.DatabaseType{
		model.DatabaseTypeS3Parquet, model.DatabaseTypeS3ORC, model.DatabaseTypeS3Avro,
		model.DatabaseTypeS3JSON, model.DatabaseTypeAlibabaOSSParquet,
		model.DatabaseTypeTencentCOSParquet, model.DatabaseTypeAzureBlobParquet,
	} {
		profile := profiles[string(dbType)]
		if profile.Status != ConnectorStatusExperimental || !containsSupportLimitation(profile.Limitations, "No end-to-end query-service execution path") {
			t.Errorf("%s must disclose that gateway SQL execution is not verified: %+v", dbType, profile)
		}
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
	if !postgres.EndToEndVerified || postgres.SupportedVersionRange != "17.11 only" || !reflect.DeepEqual(postgres.VerifiedServerVersions, []string{"17.11"}) {
		t.Fatalf("type endpoint has missing or overly broad PostgreSQL verification evidence: %+v", postgres)
	}
	mysql, ok := info[string(model.DatabaseTypeMySQL)]
	if !ok || mysql.Status != ConnectorStatusEndToEndVerified || !mysql.EndToEndVerified || mysql.SupportedVersionRange != "8.0.46 only" || !reflect.DeepEqual(mysql.VerifiedServerVersions, []string{"8.0.46"}) {
		t.Fatalf("type endpoint has missing or overly broad MySQL verification evidence: %+v", mysql)
	}
	mariaDB, ok := info[string(model.DatabaseTypeMariaDB)]
	if !ok || mariaDB.Status != ConnectorStatusEndToEndVerified || !mariaDB.EndToEndVerified || mariaDB.SupportedVersionRange != "11.8.9 only" || !reflect.DeepEqual(mariaDB.VerifiedServerVersions, []string{"11.8.9"}) {
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
	if _, ok := info[string(model.DatabaseTypeOzoneParquet)]; ok {
		t.Fatal("type endpoint must not advertise unsupported Ozone")
	}
	if ozone, ok := compatibility[string(model.DatabaseTypeOzoneParquet)]; !ok || ozone.Status != ConnectorStatusUnsupported {
		t.Fatalf("compatibility endpoint must report Ozone as unsupported: %+v", ozone)
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
