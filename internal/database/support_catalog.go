package database

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

type ConnectorStatus string

const (
	ConnectorStatusImplementationPresent ConnectorStatus = "implementation_present"
	ConnectorStatusEndToEndVerified      ConnectorStatus = "end_to_end_verified"
	ConnectorStatusExperimental          ConnectorStatus = "experimental"
	ConnectorStatusPlanned               ConnectorStatus = "planned"
	ConnectorStatusUnsupported           ConnectorStatus = "unsupported"
)

// ConnectorSupport is shared by the database APIs and generated support docs.
// Empty version fields mean no compatibility evidence has been recorded.
type ConnectorSupport struct {
	Type        string `json:"type"`
	DriverName  string `json:"driverName,omitempty"`
	DefaultPort int    `json:"defaultPort,omitempty"`
	Category    string `json:"category"`
	Registered  bool   `json:"registered"`
	Supported   bool   `json:"supported"`
	// Legacy compatibility fields retained for existing API clients.
	Version                      string                     `json:"version"`
	Features                     []string                   `json:"features"`
	Recommended                  bool                       `json:"recommended"`
	Status                       ConnectorStatus            `json:"status"`
	EndToEndVerified             bool                       `json:"endToEndVerified"`
	SupportedVersionRange        string                     `json:"supportedVersionRange"`
	VerifiedServerVersions       []string                   `json:"verifiedServerVersions"`
	VersionPolicy                string                     `json:"versionPolicy"`
	VersionDependentCapabilities []string                   `json:"versionDependentCapabilities"`
	CompatibilityMode            string                     `json:"compatibilityMode"`
	ServerVersionDetection       string                     `json:"serverVersionDetection"`
	Operations                   []string                   `json:"operations"`
	AuthenticationMethods        []string                   `json:"authenticationMethods"`
	AuthenticationDisclosure     string                     `json:"authenticationDisclosure"`
	Capabilities                 drivers.DriverCapabilities `json:"capabilities"`
	Limitations                  []string                   `json:"limitations"`
}

// SupportCatalog returns the active driver set plus explicit unsupported
// types, without inferring end-to-end verification from registry presence.
func SupportCatalog(registry *DriverRegistry) []ConnectorSupport {
	if registry == nil {
		return nil
	}

	catalog := make([]ConnectorSupport, 0, len(registry.ListDrivers())+8)
	for _, dbType := range registry.ListDrivers() {
		driver, err := registry.GetDriver(dbType)
		if err != nil || driver == nil {
			continue
		}
		capabilities := driver.GetCapabilities()
		profile := ConnectorSupport{
			Type:                         string(dbType),
			DriverName:                   driver.GetDriverName(),
			DefaultPort:                  driver.GetDefaultPort(),
			Category:                     string(driver.GetCategory()),
			Registered:                   true,
			Supported:                    true,
			Status:                       ConnectorStatusImplementationPresent,
			EndToEndVerified:             false,
			SupportedVersionRange:        "",
			VerifiedServerVersions:       []string{},
			CompatibilityMode:            compatibilityMode(dbType, driver.GetCategory()),
			VersionPolicy:                "No compatibility range is declared; connection attempts are allowed and do not imply compatibility.",
			VersionDependentCapabilities: []string{},
			ServerVersionDetection:       serverVersionDetection(dbType, driver.GetCategory()),
			Operations:                   supportedOperations(capabilities),
			AuthenticationMethods:        []string{},
			AuthenticationDisclosure:     "Authentication methods are connector-specific and have not yet been catalogued.",
			Capabilities:                 capabilities,
			Limitations: []string{
				"No supported server-version range or exact verified server version is recorded.",
				"A versioned end-to-end contract test is not recorded; see issue #35.",
			},
		}
		applySupportStatus(&profile, dbType)
		applyVersionVerificationEvidence(&profile, dbType)
		profile.Version = profile.SupportedVersionRange
		profile.Features = append([]string(nil), profile.Operations...)
		catalog = append(catalog, profile)
	}

	// HDFS file-format adapters are not registered until they have a
	// functioning unified query path. Keep their legacy type names visible but
	// explicitly unsupported so registry presence is not implied.
	for _, dbType := range []model.DatabaseType{
		model.DatabaseTypeHDFSAvro,
		model.DatabaseTypeHDFSCSV,
		model.DatabaseTypeHDFSParquet,
		model.DatabaseTypeHDFSText,
		model.DatabaseTypeHDFSParquetCompressed,
	} {
		catalog = append(catalog, ConnectorSupport{
			Type:                         string(dbType),
			Category:                     string(drivers.CategoryFileSystem),
			Registered:                   false,
			Supported:                    false,
			Version:                      "",
			Features:                     []string{},
			Recommended:                  false,
			Status:                       ConnectorStatusUnsupported,
			VerifiedServerVersions:       []string{},
			VersionPolicy:                "Unsupported; registration and connection attempts are rejected.",
			VersionDependentCapabilities: []string{},
			CompatibilityMode:            "filesystem_protocol",
			ServerVersionDetection:       "not_applicable",
			Operations:                   []string{},
			AuthenticationMethods:        []string{},
			AuthenticationDisclosure:     "Not applicable while unsupported.",
			Limitations:                  []string{"No verified file read path is connected to the unified query service; legacy format parser methods are incomplete."},
		})
	}

	// Ozone is intentionally not registered while its object operations remain
	// incomplete. Keep it visible as explicitly unsupported.
	for _, dbType := range []model.DatabaseType{
		model.DatabaseTypeOzoneParquet,
		model.DatabaseTypeOzoneText,
		model.DatabaseTypeOzoneAvro,
	} {
		catalog = append(catalog, ConnectorSupport{
			Type:                         string(dbType),
			Category:                     string(drivers.CategoryObjectStorage),
			Registered:                   false,
			Supported:                    false,
			Version:                      "",
			Features:                     []string{},
			Recommended:                  false,
			Status:                       ConnectorStatusUnsupported,
			VerifiedServerVersions:       []string{},
			VersionPolicy:                "Unsupported; registration and connection attempts are rejected.",
			VersionDependentCapabilities: []string{},
			CompatibilityMode:            "object_storage_api",
			ServerVersionDetection:       "not_applicable",
			Operations:                   []string{},
			AuthenticationMethods:        []string{},
			AuthenticationDisclosure:     "Not applicable while unsupported.",
			Limitations:                  []string{"Object operations are incomplete; source configuration is rejected."},
		})
	}

	sort.Slice(catalog, func(i, j int) bool { return catalog[i].Type < catalog[j].Type })
	return catalog
}

func applyVersionVerificationEvidence(profile *ConnectorSupport, dbType model.DatabaseType) {
	switch dbType {
	case model.DatabaseTypePostgreSQL:
		profile.Status = ConnectorStatusEndToEndVerified
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "17.11 only"
		profile.VerifiedServerVersions = []string{"17.11"}
		profile.VersionPolicy = "Only PostgreSQL 17.11 has a pinned driver contract result; no other server version is implied to be compatible."
		profile.AuthenticationMethods = []string{"username/password"}
		profile.AuthenticationDisclosure = "Password authentication is covered by the PostgreSQL 17.11 contract test; TLS is not covered by this baseline."
		profile.Limitations = []string{
			"The PostgreSQL contract test covers password authentication, read-only session enforcement, SQL execution, pagination, type scanning, cancellation, and query errors against PostgreSQL 17.11.",
			"TLS behavior and other PostgreSQL versions are not covered by this baseline.",
		}
	case model.DatabaseTypeMySQL:
		profile.Status = ConnectorStatusEndToEndVerified
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "8.0.46 only"
		profile.VerifiedServerVersions = []string{"8.0.46"}
		profile.VersionPolicy = "Only MySQL 8.0.46 has a pinned driver contract result; no other server version or MySQL-compatible product is implied to be compatible."
		profile.AuthenticationMethods = []string{"username/password"}
		profile.AuthenticationDisclosure = "Password authentication is covered by the MySQL 8.0.46 contract test; TLS is not covered by this baseline."
		profile.Limitations = []string{
			"The MySQL contract test covers password authentication, connection and health checks, schema and value scanning, pagination, database-side read-only sessions, cancellation, and query errors against MySQL 8.0.46.",
			"TLS behavior, MariaDB, and other MySQL versions are not covered by this baseline.",
		}
	default:
		return
	}
}

func applySupportStatus(profile *ConnectorSupport, dbType model.DatabaseType) {
	// Known placeholders and incomplete execution paths must not read as stable.
	switch dbType {
	case model.DatabaseTypeApacheIceberg:
		profile.Status = ConnectorStatusExperimental
		profile.Limitations = append(profile.Limitations,
			"SQL execution is read-only and limited to one table, projections, AND comparisons or NULL checks, LIMIT/OFFSET, and COUNT(*); joins, OR, grouping, ordering, and DML are rejected.",
			"No live, versioned Iceberg REST catalog and data-file contract test is recorded; retain experimental status until that evidence exists.")
	case model.DatabaseTypeDeltaLake:
		profile.Status = ConnectorStatusExperimental
		profile.Limitations = append(profile.Limitations,
			"Metadata discovery uses Unity Catalog; SQL statements, table history, and Change Data Feed reads require a configured Databricks SQL Warehouse.",
			"Change Data Feed must be enabled and its requested versions must remain within the table's retained history; start and end versions are inclusive.",
			"Statement results use inline JSON_ARRAY responses; transactions and streaming are not supported by this connector.")
	case model.DatabaseTypeApacheHudi:
		profile.Status = ConnectorStatusExperimental
		profile.Limitations = append(profile.Limitations,
			"Apache Hudi has no generic REST query endpoint. Metadata discovery requires a configured storage reader; row-level incremental reads require an explicitly configured Hudi-capable query engine.",
			"The S3 metadata reader supports classic active timeline files and table format versions below 8; archived history and Hudi 1.x LSM timelines are not discovered.",
			"Incremental reads use a fixed end instant and engine-owned opaque page tokens; SQL execution, writes, time travel, and streaming are not provided by this adapter.")
	case model.DatabaseTypeMongoDB:
		profile.Status = ConnectorStatusExperimental
		profile.Limitations = append(profile.Limitations,
			"MongoDB is not a SQL server; the gateway maps a restricted single-collection SELECT subset to native find/count commands.",
			"Joins, OR predicates, grouping, schema discovery, and transactions are not supported by this adapter.",
			"SELECT * infers result columns and types from the first document; an empty result has no inferred columns and heterogeneous document shapes may not fit one SQL row schema.",
			"No live MongoDB server-version contract test is recorded.")
	case model.DatabaseTypeS3CSV:
		profile.Status = ConnectorStatusExperimental
		profile.Limitations = append(profile.Limitations,
			"The gateway query path supports one configured CSV object and a limited SELECT subset; it downloads the full object into gateway memory.",
			"The end-to-end test uses an S3-compatible HTTP fixture; no AWS S3 or provider-version contract test is recorded.")
	case model.DatabaseTypeS3Parquet, model.DatabaseTypeS3ORC, model.DatabaseTypeS3Avro,
		model.DatabaseTypeS3JSON, model.DatabaseTypeMinIOCSV, model.DatabaseTypeMinIOParquet,
		model.DatabaseTypeAlibabaOSSParquet, model.DatabaseTypeTencentCOSParquet,
		model.DatabaseTypeAzureBlobParquet, model.DatabaseTypeAzureParquet,
		model.DatabaseTypeOSSDelta, model.DatabaseTypeMinIOIceberg,
		model.DatabaseTypeAzureDelta, model.DatabaseTypeMinIODelta:
		profile.Status = ConnectorStatusExperimental
		profile.Limitations = append(profile.Limitations,
			"No end-to-end query-service execution path is recorded for this connector; registry presence alone does not mean SQL queries are available.")
	}
}

func supportedOperations(capabilities drivers.DriverCapabilities) []string {
	operations := make([]string, 0, 7)
	if capabilities.SupportsSQL {
		operations = append(operations, "sql_query")
	}
	if capabilities.SupportsSchemaDiscovery {
		operations = append(operations, "schema_discovery")
	}
	if capabilities.SupportsTransaction {
		operations = append(operations, "transactions")
	}
	if capabilities.SupportsStreaming {
		operations = append(operations, "streaming")
	}
	if capabilities.SupportsTimeTravel {
		operations = append(operations, "time_travel")
	}
	if capabilities.SupportsSnapshotDiscovery {
		operations = append(operations, "snapshot_discovery")
	}
	if capabilities.SupportsIncrementalRead {
		operations = append(operations, "incremental_read")
	}
	return operations
}

func compatibilityMode(dbType model.DatabaseType, category drivers.DriverCategory) string {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB, model.DatabaseTypeOceanBaseMySQL,
		model.DatabaseTypeTiDB, model.DatabaseTypeTDSQL, model.DatabaseTypeGaussDBMySQL,
		model.DatabaseTypeGBase8t:
		return "mysql_compatible"
	case model.DatabaseTypePostgreSQL, model.DatabaseTypeGaussDBPostgres,
		model.DatabaseTypeKingbaseES, model.DatabaseTypeOscar, model.DatabaseTypeOpenGauss,
		model.DatabaseTypeHologres:
		return "postgresql_compatible"
	case model.DatabaseTypeTrino, model.DatabaseTypePresto, model.DatabaseTypeOpenLooKeng:
		return "trino_http_protocol_family"
	case model.DatabaseTypeOceanBaseOracle:
		return "oracle_compatible"
	}
	switch category {
	case drivers.CategoryObjectStorage:
		return "object_storage_api"
	case drivers.CategoryFileSystem:
		return "filesystem_protocol"
	case drivers.CategoryTableFormat:
		return "table_format_catalog_api"
	case drivers.CategoryNoSQL:
		return "connector_specific_nosql"
	default:
		return "connector_specific"
	}
}

func serverVersionDetection(dbType model.DatabaseType, category drivers.DriverCategory) string {
	if _, ok := serverVersionProbe(dbType); ok {
		return "best_effort_sql_probe"
	}
	switch category {
	case drivers.CategoryObjectStorage, drivers.CategoryFileSystem, drivers.CategoryTableFormat:
		return "not_applicable"
	default:
		return "unavailable"
	}
}

type versionProbe struct {
	query string
}

func serverVersionProbe(dbType model.DatabaseType) (versionProbe, bool) {
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB, model.DatabaseTypeOceanBaseMySQL,
		model.DatabaseTypeTiDB, model.DatabaseTypeTDSQL, model.DatabaseTypeGaussDBMySQL,
		model.DatabaseTypeGBase8t:
		return versionProbe{query: "SELECT VERSION()"}, true
	case model.DatabaseTypePostgreSQL, model.DatabaseTypeGaussDBPostgres,
		model.DatabaseTypeKingbaseES, model.DatabaseTypeOscar, model.DatabaseTypeOpenGauss,
		model.DatabaseTypeHologres:
		return versionProbe{query: "SELECT version()"}, true
	case model.DatabaseTypeOracle, model.DatabaseTypeOceanBaseOracle:
		return versionProbe{query: "SELECT banner FROM v$version WHERE ROWNUM = 1"}, true
	case model.DatabaseTypeSQLServer:
		return versionProbe{query: "SELECT @@VERSION"}, true
	case model.DatabaseTypeSnowflake:
		return versionProbe{query: "SELECT CURRENT_VERSION()"}, true
	case model.DatabaseTypeTrino, model.DatabaseTypePresto, model.DatabaseTypeOpenLooKeng,
		model.DatabaseTypeClickHouse, model.DatabaseTypeApacheDoris, model.DatabaseTypeStarRocks,
		model.DatabaseTypeRedshift, model.DatabaseTypeDatabricks:
		return versionProbe{query: "SELECT version()"}, true
	default:
		return versionProbe{}, false
	}
}

// DetectedServerInfo is returned by explicit connection tests. Probe failure
// does not fail the connection test and never invents a server identity.
type DetectedServerInfo struct {
	Product string `json:"product"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

func detectServerInfo(ctx context.Context, db *sql.DB, dbType model.DatabaseType, category drivers.DriverCategory) *DetectedServerInfo {
	probe, ok := serverVersionProbe(dbType)
	if !ok {
		status := "not_available"
		if serverVersionDetection(dbType, category) == "not_applicable" {
			status = "not_applicable"
		}
		return &DetectedServerInfo{Product: "unknown", Version: "unknown", Status: status}
	}
	if db == nil {
		return &DetectedServerInfo{Product: "unknown", Version: "unknown", Status: "unavailable"}
	}

	info := &DetectedServerInfo{Product: "unknown", Version: "unknown", Status: "unavailable"}
	var version string
	if err := db.QueryRowContext(ctx, probe.query).Scan(&version); err != nil {
		return info
	}
	version = strings.TrimSpace(version)
	if version == "" {
		return info
	}
	info.Version = version
	info.Product = identifyServerProduct(version)
	info.Status = "detected"
	return info
}

func identifyServerProduct(version string) string {
	lower := strings.ToLower(version)
	for _, product := range []struct{ marker, name string }{
		{"oceanbase", "OceanBase"}, {"tidb", "TiDB"}, {"mariadb", "MariaDB"},
		{"kingbase", "KingbaseES"}, {"opengauss", "openGauss"}, {"gaussdb", "GaussDB"},
		{"tdsql", "TDSQL"}, {"gbase", "GBase"}, {"oscar", "Oscar"},
		{"openlookeng", "OpenLooKeng"}, {"trino", "Trino"}, {"presto", "Presto"},
		{"clickhouse", "ClickHouse"}, {"starrocks", "StarRocks"}, {"doris", "Doris"},
		{"redshift", "Amazon Redshift"}, {"snowflake", "Snowflake"},
		{"postgresql", "PostgreSQL"}, {"oracle database", "Oracle"},
		{"microsoft sql server", "Microsoft SQL Server"},
	} {
		if strings.Contains(lower, product.marker) {
			return product.name
		}
	}
	return "unknown"
}

// DriverInfo preserves the original type-list fields and adds the shared profile.
type DriverInfo struct {
	ConnectorSupport
}

// CompatibilityByType returns the catalog in the existing map response shape.
func CompatibilityByType(catalog []ConnectorSupport) map[string]ConnectorSupport {
	result := make(map[string]ConnectorSupport, len(catalog))
	for _, profile := range catalog {
		result[profile.Type] = profile
	}
	return result
}

// RenderSupportCatalogMarkdown renders the connector matrix from the same data
// exposed by the database APIs.
func RenderSupportCatalogMarkdown(catalog []ConnectorSupport) string {
	var out strings.Builder
	out.WriteString("# Data Source Support Catalog\n\n")
	out.WriteString("This page is generated from the runtime connector catalog. A registered driver means implementation code is present; it does not imply end-to-end verification. Empty version ranges and version lists are intentional when no pinned evidence exists. Authentication remains connector-specific where it has not been catalogued. The legacy API field `supported` means the type is registered; use `status` and `endToEndVerified` to assess evidence.\n\n")
	out.WriteString("| Type | Category | Registered | Status | End-to-end verified | Version range | Exact versions verified | Version policy | Compatibility mode | Version detection | Operations | Authentication methods | Authentication note | Capabilities | Version-dependent capabilities | Limitations |\n")
	out.WriteString("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	for _, profile := range catalog {
		versions := strings.Join(profile.VerifiedServerVersions, ", ")
		if versions == "" {
			versions = "—"
		}
		versionRange := profile.SupportedVersionRange
		if versionRange == "" {
			versionRange = "—"
		}
		operations := strings.Join(profile.Operations, ", ")
		if operations == "" {
			operations = "—"
		}
		limitations := strings.Join(profile.Limitations, "<br>")
		authentication := strings.Join(profile.AuthenticationMethods, ", ")
		if authentication == "" {
			authentication = "Not catalogued"
		}
		versionCapabilities := strings.Join(profile.VersionDependentCapabilities, ", ")
		if versionCapabilities == "" {
			versionCapabilities = "Not catalogued"
		}
		out.WriteString(fmt.Sprintf("| %s | %s | %t | %s | %t | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			profile.Type, profile.Category, profile.Registered, profile.Status, profile.EndToEndVerified,
			versionRange, versions, profile.VersionPolicy, profile.CompatibilityMode,
			profile.ServerVersionDetection, operations, authentication, profile.AuthenticationDisclosure,
			capabilitySummary(profile.Capabilities), versionCapabilities, limitations))
	}
	out.WriteString("\n`endToEndVerified` is true only after a versioned contract test passes. No compatibility range is declared for a source until the project records the evidence; connection attempts are allowed and the detected version is reported when available. Authentication methods are reported only when catalogued. For executed evidence, see [the driver contract test report](driver-contract-tests.md); for the plan, see [issue #35](https://github.com/RhinoLink-Tech/data-fabric/issues/35). For Delta Lake and Apache Hudi range semantics and adapter boundaries, see [the table-format read contracts](table-format-read-contracts.md).\n\nRegenerate this page after catalog changes with `go run ./cmd/generate-support-catalog`.\n")
	return out.String()
}

func capabilitySummary(capabilities drivers.DriverCapabilities) string {
	return fmt.Sprintf("SQL=%t; schema=%t; transactions=%t; streaming=%t; time_travel=%t",
		capabilities.SupportsSQL, capabilities.SupportsSchemaDiscovery,
		capabilities.SupportsTransaction, capabilities.SupportsStreaming,
		capabilities.SupportsTimeTravel)
}
