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
// End-to-end evidence is version-specific and can coexist with an experimental
// status when a live contract validates only a deliberately restricted adapter.
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
				"No versioned end-to-end contract test is recorded for this connector; see the driver contract test report.",
			},
		}
		applySupportStatus(&profile, dbType)
		applyVersionVerificationEvidence(&profile, dbType)
		profile.Version = profile.SupportedVersionRange
		profile.Features = append([]string(nil), profile.Operations...)
		catalog = append(catalog, profile)
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
		profile.AuthenticationDisclosure = "Password authentication and TLS certificate/hostname verification are covered by the PostgreSQL 17.11 contract test."
		profile.Limitations = []string{
			"The PostgreSQL contract test covers password authentication, read-only session enforcement, SQL execution, pagination, type scanning, cancellation, and query errors against PostgreSQL 17.11.",
			"The TLS contract verifies sslmode=verify-full with a trusted CA and rejects an untrusted CA or hostname mismatch; other TLS modes and PostgreSQL versions are not covered.",
		}
	case model.DatabaseTypeMySQL:
		profile.Status = ConnectorStatusEndToEndVerified
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "8.0.46 only"
		profile.VerifiedServerVersions = []string{"8.0.46"}
		profile.VersionPolicy = "Only MySQL 8.0.46 has a pinned driver contract result; no other server version or MySQL-compatible product is implied to be compatible."
		profile.AuthenticationMethods = []string{"username/password"}
		profile.AuthenticationDisclosure = "Password authentication and TLS certificate/hostname verification are covered by the MySQL 8.0.46 contract tests."
		profile.Limitations = []string{
			"The MySQL contract test covers password authentication, connection and health checks, schema and value scanning, pagination, database-side read-only sessions, cancellation, and query errors against MySQL 8.0.46.",
			"The TLS contract verifies a trusted private CA and hostname, rejects an untrusted CA or hostname mismatch, and confirms ssl=false remains plaintext. Other MySQL versions are not covered.",
		}
	case model.DatabaseTypeMariaDB:
		profile.Status = ConnectorStatusEndToEndVerified
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "11.8.9 only"
		profile.VerifiedServerVersions = []string{"11.8.9"}
		profile.VersionPolicy = "Only MariaDB 11.8.9 has a pinned contract result; other MariaDB versions and MySQL are not implied to be compatible."
		profile.AuthenticationMethods = []string{"username/password"}
		profile.AuthenticationDisclosure = "Password authentication is covered by the MariaDB 11.8.9 contract test; TLS is not covered by this baseline."
		profile.Limitations = []string{
			"The MariaDB contract test exercises the shared MySQL-protocol driver against MariaDB 11.8.9, including database-side read-only sessions.",
			"TLS behavior and other MariaDB versions are not covered by this baseline.",
		}
	case model.DatabaseTypeTiDB:
		profile.Status = ConnectorStatusEndToEndVerified
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "8.5.8 only"
		profile.VerifiedServerVersions = []string{"8.0.11-TiDB-v8.5.8"}
		profile.VersionPolicy = "Only TiDB v8.5.8, reporting 8.0.11-TiDB-v8.5.8, has a pinned contract result; no other TiDB version or MySQL-compatible product is implied."
		profile.AuthenticationMethods = []string{"username/password"}
		profile.AuthenticationDisclosure = "Password authentication and TLS certificate/hostname verification are covered by the TiDB v8.5.8 contract tests."
		profile.Limitations = []string{
			"The TiDB contract test covers the MySQL-wire driver against one TiDB v8.5.8 server instance; it does not validate a multi-node TiKV cluster.",
			"TiDB v8.5.8 does not implement SET SESSION TRANSACTION READ ONLY; the generic database-side read-only initializer is disabled for TiDB. Use a TiDB account with restricted privileges.",
			"The TLS contract verifies the server certificate and hostname with a trusted CA, rejects an untrusted CA or hostname mismatch, and confirms ssl=false remains plaintext. Other TiDB versions are not covered.",
		}
	case model.DatabaseTypeOpenGauss:
		profile.Status = ConnectorStatusEndToEndVerified
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "6.0.0 only"
		profile.VerifiedServerVersions = []string{"openGauss 6.0.0 build aee4abd5"}
		profile.VersionPolicy = "Only openGauss 6.0.0 build aee4abd5 has a pinned contract result; no other openGauss or PostgreSQL version is implied to be compatible."
		profile.AuthenticationMethods = []string{"username/password"}
		profile.AuthenticationDisclosure = "Password authentication is covered by the openGauss 6.0.0 contract test; TLS is not covered by this baseline."
		profile.Limitations = []string{
			"The openGauss contract test covers the PostgreSQL-wire driver against one openGauss 6.0.0 server instance.",
			"Database-side read-only session enforcement, SQL execution, schema metadata, pagination, cancellation, and query errors are verified. TLS, other openGauss versions, and other PostgreSQL-compatible products are not covered.",
		}
	case model.DatabaseTypeClickHouse:
		profile.Status = ConnectorStatusEndToEndVerified
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "26.8.20.9 only"
		profile.VerifiedServerVersions = []string{"26.8.20.9"}
		profile.VersionPolicy = "Only ClickHouse 26.8.20.9 has a pinned native-protocol contract result; other ClickHouse versions are not implied to be compatible."
		profile.AuthenticationMethods = []string{"username/password"}
		profile.AuthenticationDisclosure = "Password authentication is covered by the ClickHouse 26.8.20.9 contract test; TLS is not covered by this baseline."
		profile.Limitations = []string{
			"The ClickHouse contract test covers the native-protocol SQL driver, connection and health checks, schema and value scanning, pagination, cancellation, and query errors against ClickHouse 26.8.20.9.",
			"Gateway read-only session initialization is not supported for ClickHouse; use a server account with restricted privileges. TLS behavior and other ClickHouse versions are not covered by this baseline.",
		}
	case model.DatabaseTypeMongoDB:
		profile.Status = ConnectorStatusExperimental
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "8.0.34 only"
		profile.VerifiedServerVersions = []string{"8.0.34"}
		profile.VersionPolicy = "Only MongoDB 8.0.34 has a pinned contract result for the restricted SQL adapter; other MongoDB versions are not implied to be compatible."
		profile.AuthenticationMethods = []string{"username/password"}
		profile.AuthenticationDisclosure = "Database-scoped username/password authentication is covered by the MongoDB 8.0.34 contract test; TLS is not covered by this baseline."
		profile.Limitations = []string{
			"The MongoDB 8.0.34 contract covers authentication, health checks, the restricted single-collection SELECT subset with projection/filter/order/pagination, COUNT(*), NULL scanning, and rejection of writes and unsupported SQL. The connector remains experimental because this is a restricted SQL mapping over a document database.",
			"Joins, OR predicates, grouping, schema discovery, and transactions are not supported by this adapter.",
			"SELECT * infers result columns and types from the first document; an empty result has no inferred columns and heterogeneous document shapes may not fit one SQL row schema.",
			"TLS behavior, replica-set and sharded deployments, and other MongoDB versions are not covered by this baseline.",
		}
	case model.DatabaseTypeSQLServer:
		profile.Status = ConnectorStatusEndToEndVerified
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "16.0.4305.5 only"
		profile.VerifiedServerVersions = []string{"16.0.4305.5"}
		profile.VersionPolicy = "Only SQL Server 16.0.4305.5 has a pinned contract result; other SQL Server versions are not implied to be compatible."
		profile.AuthenticationMethods = []string{"username/password"}
		profile.AuthenticationDisclosure = "SQL authentication is covered by the SQL Server 16.0.4305.5 contract test; the test trusts the container's self-signed TLS certificate, so production certificate validation is not covered."
		profile.Limitations = []string{
			"The SQL Server contract test covers SQL authentication, connection and health checks, database selection, value scanning, pagination, cancellation, and query errors against SQL Server 16.0.4305.5.",
			"Database-side read-only session enforcement is not supported; use a least-privilege SQL Server account. Production TLS certificate validation and other SQL Server versions are not covered by this baseline.",
		}
	case model.DatabaseTypeOracle:
		profile.Status = ConnectorStatusEndToEndVerified
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "23.26.3.0.0 only"
		profile.VerifiedServerVersions = []string{"23.26.3.0.0"}
		profile.VersionPolicy = "Only Oracle AI Database 26ai Free 23.26.3.0.0 has a pinned contract result; other Oracle versions and editions are not implied to be compatible."
		profile.AuthenticationMethods = []string{"username/password"}
		profile.AuthenticationDisclosure = "The contract uses a dedicated application schema account for SQL operations and SYSTEM only to read the exact server version; TLS is not covered by this baseline."
		profile.Limitations = []string{
			"The Oracle contract test covers the go-ora SQL driver, connection and health checks, bind parameters, value and NULL scanning, DML, COUNT(*), Oracle OFFSET/FETCH pagination, and query errors against Oracle AI Database 26ai Free 23.26.3.0.0.",
			"Gateway read-only session initialization is not supported for Oracle; use an Oracle account with restricted privileges. TLS behavior, other Oracle versions, and licensed editions are not covered by this baseline.",
		}
	case model.DatabaseTypeS3CSV:
		profile.Status = ConnectorStatusExperimental
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "Garage v2.4.1 only"
		profile.VerifiedServerVersions = []string{"Garage v2.4.1"}
		profile.VersionPolicy = "The S3 CSV adapter has a pinned live contract against Garage v2.4.1 only; AWS S3 and other S3-compatible providers are not implied to be compatible."
		profile.AuthenticationMethods = []string{"S3 access key/secret key (SigV4)"}
		profile.AuthenticationDisclosure = "Signed S3 requests using an access key and secret key are covered against Garage v2.4.1; IAM roles, session tokens, and provider-specific authentication are not covered by this baseline."
		profile.Limitations = []string{
			"The Garage v2.4.1 contract covers signed object upload, listing, metadata lookup, object retrieval, health checks, a restricted read-only SQL query, and rejection of writes.",
			"This contract does not verify AWS S3, other S3-compatible providers, or other Garage versions.",
			"The gateway query path supports one configured CSV object and a limited SELECT subset; it downloads the full object into gateway memory.",
		}
	case model.DatabaseTypeS3Parquet:
		profile.Status = ConnectorStatusExperimental
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "Garage v2.4.1 only"
		profile.VerifiedServerVersions = []string{"Garage v2.4.1"}
		profile.VersionPolicy = "The S3 Parquet adapter has a pinned live contract against Garage v2.4.1 only; AWS S3 and other S3-compatible providers are not implied to be compatible."
		profile.AuthenticationMethods = []string{"S3 access key/secret key (SigV4)"}
		profile.AuthenticationDisclosure = "Signed S3 requests using an access key and secret key are covered against Garage v2.4.1; IAM roles, session tokens, and provider-specific authentication are not covered by this baseline."
		profile.Limitations = []string{
			"The Garage v2.4.1 contract covers signed Parquet upload, metadata and health checks, schema discovery, unified SQL execution, pagination, COUNT(*), and write rejection.",
			"The adapter exposes one configured flat-schema Parquet object as one SQL table. It reads object ranges, but currently decodes all rows before applying SQL filters and pagination.",
			"This contract does not verify AWS S3, other S3-compatible providers, S3 Select execution, nested or repeated fields, partition scans, or multiple objects.",
		}
	case model.DatabaseTypeS3JSON:
		profile.Status = ConnectorStatusExperimental
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "Garage v2.4.1 only"
		profile.VerifiedServerVersions = []string{"Garage v2.4.1"}
		profile.VersionPolicy = "The S3 JSON adapter has a pinned live contract against Garage v2.4.1 only; AWS S3 and other S3-compatible providers are not implied to be compatible."
		profile.AuthenticationMethods = []string{"S3 access key/secret key (SigV4)"}
		profile.AuthenticationDisclosure = "Signed S3 requests using an access key and secret key are covered against Garage v2.4.1; IAM roles, session tokens, and provider-specific authentication are not covered by this baseline."
		profile.Limitations = []string{
			"The Garage v2.4.1 contract covers signed upload, object and JSON Lines formats, schema discovery, read-only SQL, pagination, COUNT(*), and rejection of writes.",
			"The adapter exposes one configured JSON object as one SQL table. It downloads the full object into gateway memory before parsing and applying filters or pagination.",
			"Only top-level scalar fields with stable types are supported. Nested object fields require flatten_nested=true; array fields, flattened-name collisions, heterogeneous scalar fields, multiple objects, and partition scans are rejected or outside this adapter boundary.",
			"S3 Select execution is disabled; compatibility with AWS S3, other S3-compatible providers, and other Garage versions is not established by this contract.",
		}
	case model.DatabaseTypeS3Avro:
		profile.Status = ConnectorStatusExperimental
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "Garage v2.4.1 only"
		profile.VerifiedServerVersions = []string{"Garage v2.4.1"}
		profile.VersionPolicy = "The S3 Avro adapter has a pinned live contract against Garage v2.4.1 only; AWS S3 and other S3-compatible providers are not implied to be compatible."
		profile.AuthenticationMethods = []string{"S3 access key/secret key (SigV4)"}
		profile.AuthenticationDisclosure = "Signed S3 requests using an access key and secret key are covered against Garage v2.4.1; IAM roles, session tokens, and provider-specific authentication are not covered by this baseline."
		profile.Limitations = []string{
			"The Garage v2.4.1 contract covers signed OCF upload, flat-record schema discovery, read-only SQL, LIMIT 0 schema discovery, COUNT(*), and rejection of writes.",
			"The adapter exposes one configured Avro OCF object as one SQL table. On a cache miss, it downloads and decodes the full object into gateway memory before applying filters or pagination; results for the same ETag may be reused for up to five seconds across count and page requests.",
			"Only flat records with primitive fields or nullable unions of null and one primitive type are supported. Nested records, arrays, maps, named unions, schema registries, multiple objects, and partition scans are not supported.",
			"Only null, deflate, and Snappy OCF codecs are supported by the current decoder; other codecs and provider versions are not verified.",
		}
	case model.DatabaseTypeS3ORC:
		profile.Status = ConnectorStatusExperimental
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "Garage v2.4.1 only"
		profile.VerifiedServerVersions = []string{"Garage v2.4.1"}
		profile.VersionPolicy = "The S3 ORC adapter has a pinned live contract against Garage v2.4.1 only; AWS S3 and other S3-compatible providers are not implied to be compatible."
		profile.AuthenticationMethods = []string{"S3 access key/secret key (SigV4)"}
		profile.AuthenticationDisclosure = "Signed S3 requests using an access key and secret key are covered against Garage v2.4.1; IAM roles, session tokens, and provider-specific authentication are not covered by this baseline."
		profile.Limitations = []string{
			"The Garage v2.4.1 contract covers signed ORC upload, health checks, flat-schema metadata, read-only SQL, LIMIT 0 schema discovery, COUNT(*), and rejection of writes.",
			"The adapter exposes one configured flat-schema ORC object as one SQL table. Queries read object ranges but decode the full object into gateway memory before filtering and pagination.",
			"Only primitive top-level columns and uncompressed, ZLIB, and Snappy stripes are supported. Nested/repeated types, other codecs, partition scans, multiple objects, and stripe-statistics pushdown are not supported.",
			"The ORC decoder currently has a restricted compatibility baseline; compatibility with additional ORC writers and S3 providers has not been established.",
		}
	case model.DatabaseTypeMinIOCSV:
		profile.Status = ConnectorStatusExperimental
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "RELEASE.2025-04-22T22-12-26Z only"
		profile.VerifiedServerVersions = []string{"RELEASE.2025-04-22T22-12-26Z"}
		profile.VersionPolicy = "Only the pinned MinIO RELEASE.2025-04-22T22-12-26Z source build has a contract result; other MinIO releases and S3-compatible providers are not implied to be compatible."
		profile.AuthenticationMethods = []string{"S3 access key/secret key (SigV4)"}
		profile.AuthenticationDisclosure = "MinIO root access key and secret key authentication are covered by the pinned source-build contract; TLS, STS, and other authentication modes are not covered."
		profile.Limitations = []string{
			"The MinIO contract covers bucket creation, object upload, metadata lookup, health checks, a restricted read-only SQL query, pagination, COUNT(*), and rejection of writes through the shared S3 CSV adapter.",
			"MinIO CSV uses the same config-aware, read-only S3 CSV SQL adapter as S3-compatible sources. It supports one configured CSV object and a limited SELECT subset, and downloads the full object into gateway memory.",
			"Set additionalProps.endpoint_url to the MinIO endpoint and force_path_style to true. TLS, STS, distributed MinIO deployments, and other releases are not covered by this baseline.",
		}
	case model.DatabaseTypeMinIOParquet:
		profile.Status = ConnectorStatusExperimental
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "RELEASE.2025-04-22T22-12-26Z only"
		profile.VerifiedServerVersions = []string{"RELEASE.2025-04-22T22-12-26Z"}
		profile.VersionPolicy = "Only the pinned MinIO RELEASE.2025-04-22T22-12-26Z source build has a contract result; other MinIO releases and S3-compatible providers are not implied to be compatible."
		profile.AuthenticationMethods = []string{"S3 access key/secret key (SigV4)"}
		profile.AuthenticationDisclosure = "MinIO root access key and secret key authentication are covered by the pinned source-build contract; TLS, STS, and other authentication modes are not covered."
		profile.Limitations = []string{
			"The MinIO contract covers bucket creation, Parquet upload, metadata and health checks, schema discovery, a restricted read-only SQL query, filtering, ordering, pagination, COUNT(*), and write rejection.",
			"The adapter exposes one configured flat-schema Parquet object as one SQL table; it uses ranged object reads but currently decodes the object's rows before applying the supported SQL filters and page.",
			"Nested or repeated fields, partition scans, multiple objects, schema evolution, TLS, STS, distributed MinIO deployments, and other releases are not covered by this baseline.",
		}
	case model.DatabaseTypeApacheDoris:
		profile.Status = ConnectorStatusEndToEndVerified
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "Apache Doris 4.1.3 only"
		profile.VerifiedServerVersions = []string{"Apache Doris 4.1.3"}
		profile.VersionPolicy = "Only the pinned Apache Doris 4.1.3 all-in-one image has a contract result; other Doris releases are not implied to be compatible."
		profile.AuthenticationMethods = []string{"username/password"}
		profile.AuthenticationDisclosure = "A password-authenticated database user is covered against Apache Doris 4.1.3; TLS and other authentication modes are not covered by this baseline."
		profile.Limitations = []string{
			"The Doris contract covers the MySQL-protocol driver, authenticated connection and health checks, FE version reporting, database selection, SQL values and counts, schema discovery, pagination, and query errors against the pinned 4.1.3 all-in-one image.",
			"TLS, multi-node deployments, and other Doris releases are not covered by this baseline.",
		}
	case model.DatabaseTypeStarRocks:
		profile.Status = ConnectorStatusEndToEndVerified
		profile.EndToEndVerified = true
		profile.SupportedVersionRange = "StarRocks 4.1.6 only"
		profile.VerifiedServerVersions = []string{"4.1.6"}
		profile.VersionPolicy = "Only the pinned StarRocks 4.1.6 all-in-one image has a contract result; other StarRocks releases are not implied to be compatible."
		profile.AuthenticationMethods = []string{"username/password"}
		profile.AuthenticationDisclosure = "A password-authenticated user with table-scoped SELECT permission is covered against StarRocks 4.1.6; TLS and other authentication modes are not covered by this baseline."
		profile.Limitations = []string{
			"The StarRocks 4.1.6 contract covers the MySQL-protocol driver, password authentication, version and database reporting, value scanning, counts, pagination, schema metadata, query errors, and the application's table metadata extractor.",
			"TLS, multi-node deployments, write operations through the application account, and other StarRocks releases are not covered by this baseline.",
		}
	default:
		return
	}
}

func applySupportStatus(profile *ConnectorSupport, dbType model.DatabaseType) {
	// Known placeholders and incomplete execution paths must not read as stable.
	switch dbType {
	case model.DatabaseTypeHDFSText:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"HDFS simple user identity"}
		profile.AuthenticationDisclosure = "The local file-client fixture covers configured user forwarding only; Kerberos and HDFS authorization behavior are not covered."
		profile.Limitations = append(profile.Limitations,
			"The adapter reads one configured UTF-8 text file, capped at 64 MiB and 100,000 lines; directory scans, multiple files, compression, and streaming are not supported.",
			"Each physical line is exposed as line_number and line; structured multiline log parsing is not provided.",
			"The deterministic local fixture does not establish compatibility with a live HDFS release.")
	case model.DatabaseTypeHDFSCSV:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"HDFS simple user identity"}
		profile.AuthenticationDisclosure = "The local file-client fixture covers configured user forwarding only; Kerberos and HDFS authorization behavior are not covered."
		profile.Limitations = append(profile.Limitations,
			"The adapter reads one configured CSV file, capped at 64 MiB and 100,000 rows; directory scans, multiple files, partitions, and streaming are not supported.",
			"CSV fields are parsed into inferred scalar values in gateway memory before filtering and pagination; flat row-oriented data only is supported.",
			"The deterministic local fixture does not establish compatibility with a live HDFS release.")
	case model.DatabaseTypeHDFSParquet, model.DatabaseTypeHDFSParquetCompressed:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"HDFS simple user identity"}
		profile.AuthenticationDisclosure = "The local file-client fixture covers configured user forwarding only; Kerberos and HDFS authorization behavior are not covered."
		profile.Limitations = append(profile.Limitations,
			"The adapter reads one configured Parquet file, capped at 64 MiB and 100,000 rows; directory scans, multiple files, partitions, and streaming are not supported.",
			"Parquet page compression is decoded by the shared Parquet reader; externally compressed files and codecs rejected by that reader are not supported.",
			"The configured file is read into gateway memory before row decoding; this flat-file adapter does not provide column pruning, predicate pushdown, or row-group pruning.",
			"The deterministic local fixture does not establish compatibility with a live HDFS release.")
	case model.DatabaseTypeOzoneParquet, model.DatabaseTypeOzoneText, model.DatabaseTypeOzoneAvro:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"S3 access key/secret key (SigV4) through Ozone S3 Gateway"}
		profile.AuthenticationDisclosure = "Access-key signing is implemented through the S3-compatible gateway client; compatibility with a live Apache Ozone release has not yet been contract-tested."
		if dbType == model.DatabaseTypeOzoneParquet {
			profile.Limitations = append(profile.Limitations,
				"The adapter reads one configured Parquet object from one bucket through the explicitly configured S3 Gateway endpoint; it does not use native Ozone RPC or expose volume administration.",
				"Objects are capped at 64 MiB and 100,000 rows, downloaded into gateway memory, and exposed through the restricted read-only Parquet SQL subset.",
				"Path-style S3 requests are used. Native Ozone Kerberos, volume/bucket administration, multiple-object scans, and live Ozone compatibility are not covered.")
		} else if dbType == model.DatabaseTypeOzoneText {
			profile.Limitations = append(profile.Limitations,
				"The adapter reads one configured UTF-8 text object from one bucket through the explicitly configured S3 Gateway endpoint; it does not use native Ozone RPC or expose volume administration.",
				"Objects are capped at 64 MiB and 100,000 lines, downloaded into gateway memory, and exposed through the restricted read-only text SQL subset.",
				"Path-style S3 requests are used. Native Ozone Kerberos, volume/bucket administration, multiple-object scans, multiline parsing, and live Ozone compatibility are not covered.")
		} else {
			profile.Limitations = append(profile.Limitations,
				"The adapter reads one configured Avro OCF object from one bucket through the explicitly configured S3 Gateway endpoint; it does not use native Ozone RPC or expose volume administration.",
				"Objects are capped at 64 MiB and 100,000 records, downloaded into gateway memory, and exposed through the restricted read-only Avro SQL subset.",
				"Only flat records with supported primitive or nullable primitive fields are queryable. Path-style S3 requests are used; native Ozone Kerberos, volume/bucket administration, multiple-object scans, nested schemas, and live Ozone compatibility are not covered.")
		}
	case model.DatabaseTypeHDFSAvro:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"HDFS simple user identity"}
		profile.AuthenticationDisclosure = "The local file-client fixture covers configured user forwarding only; Kerberos and HDFS authorization behavior are not covered."
		profile.Limitations = append(profile.Limitations,
			"The adapter reads one configured Avro OCF file, capped at 64 MiB and 100,000 records; directory scans, multiple files, partitions, and streaming are not supported.",
			"Only flat records with supported primitive or nullable primitive fields are queryable; nested, named, and non-primitive fields are rejected.",
			"The deterministic local fixture does not establish compatibility with a live HDFS release.")
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
	case model.DatabaseTypeApacheDruid:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"optional HTTP basic authentication"}
		profile.AuthenticationDisclosure = "The protocol fixture covers optional HTTP basic authentication. No live Druid authentication mode or TLS configuration has a versioned contract result."
		profile.Limitations = append(profile.Limitations,
			"The unified query path sends Druid SQL over HTTP and accepts only top-level SELECT or EXPLAIN statements; transactions, DML, native JSON queries, and streaming are not available through this adapter.",
			"The adapter has deterministic HTTP protocol and query-service tests, but no live Druid cluster or versioned server contract is recorded; retain experimental status until a version-pinned live contract is recorded.",
			"TLS behavior, asynchronous statements, propagation of gateway cancellation to Druid's cancel endpoint, and coordinator metadata behavior are not covered by this adapter baseline.")
	case model.DatabaseTypeS3CSV:
		profile.Status = ConnectorStatusExperimental
		profile.Limitations = append(profile.Limitations,
			"The gateway query path supports one configured CSV object and a limited SELECT subset; it downloads the full object into gateway memory.")
	case model.DatabaseTypeAlibabaOSSParquet:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"Alibaba Cloud access key and optional STS session token"}
		profile.AuthenticationDisclosure = "The OSS SDK protocol fixture covers access-key authentication; STS credentials are accepted but are not covered by a live contract."
		profile.Limitations = append(profile.Limitations,
			"The adapter reads one configured object with a flat primitive Parquet schema and a restricted read-only SELECT subset; nested and repeated fields are rejected.",
			"The complete object is downloaded into gateway memory before Parquet decoding and SQL filtering, so this baseline is intended for bounded objects.",
			"Deterministic protocol-fixture coverage does not establish compatibility with a particular Alibaba OSS service release; no live OSS version contract is recorded.")
	case model.DatabaseTypeTencentCOSParquet:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"Tencent Cloud SecretId and SecretKey"}
		profile.AuthenticationDisclosure = "The local COS SDK protocol fixture covers signed SecretId/SecretKey requests; no live COS release or temporary credential contract is recorded."
		profile.Limitations = append(profile.Limitations,
			"The adapter reads one configured object with a flat primitive Parquet schema and a restricted read-only SELECT subset; nested and repeated fields are rejected.",
			"The complete object is downloaded into gateway memory before Parquet decoding and SQL filtering, so this baseline is intended for bounded objects.",
			"The deterministic SDK protocol fixture does not establish compatibility with a Tencent COS service release.")
	case model.DatabaseTypeAzureBlobParquet, model.DatabaseTypeAzureParquet:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"Azure Shared Key account key or SAS token"}
		profile.AuthenticationDisclosure = "Local SDK fixtures cover Shared Key request signing and SAS query propagation; no live Azure service version contract is recorded."
		profile.Limitations = append(profile.Limitations,
			"The adapter reads one configured blob with a flat primitive Parquet schema and a restricted read-only SELECT subset; nested and repeated fields are rejected.",
			"The complete blob is downloaded into gateway memory before Parquet decoding and SQL filtering, so this baseline is intended for bounded objects.",
			"The local Azure SDK protocol fixture does not establish compatibility with a live Azure Storage version or deployment.")
	case model.DatabaseTypeOSSDelta:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"Alibaba Cloud access key and optional STS session token"}
		profile.AuthenticationDisclosure = "Local SDK protocol fixtures cover access-key request signing; no live OSS version contract is recorded."
		profile.Limitations = append(profile.Limitations,
			"The adapter reads one unpartitioned Delta table with Parquet files, JSON transaction logs, and reader protocol version 1; checkpoints, deletion vectors, partitioning, empty tables, and newer reader features are rejected.",
			"Queries use a restricted read-only SELECT subset and may download up to 64 MiB of active data files into gateway memory; the table must have at most 256 active files and 100,000 decoded rows.",
			"The local protocol fixture does not establish compatibility with a live Alibaba OSS service release.")
	case model.DatabaseTypeS3Delta:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"S3 access key/secret key (SigV4), AWS credential chain, or assumed role"}
		profile.AuthenticationDisclosure = "The local signed S3 protocol fixture covers static access-key authentication; AWS credential-chain, assumed-role, session-token, and provider-specific authorization modes are not covered by this fixture."
		profile.Limitations = append(profile.Limitations,
			"The adapter reads one unpartitioned Delta table with Parquet files, JSON transaction logs, and reader protocol version 1; checkpoints, deletion vectors, partitioning, empty tables, and newer reader features are rejected.",
			"Queries use a restricted read-only SELECT subset and may download up to 64 MiB of active data files into gateway memory; the table must have at most 256 active files and 100,000 decoded rows.",
			"The local S3 protocol fixture does not establish compatibility with AWS S3 or any S3-compatible provider release.")
	case model.DatabaseTypeMinIODelta:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"S3-compatible access key and secret key, with optional session token"}
		profile.AuthenticationDisclosure = "Local MinIO SDK protocol fixtures cover signed requests; no live MinIO release contract is recorded."
		profile.Limitations = append(profile.Limitations,
			"The adapter reads one unpartitioned Delta table with Parquet files, JSON transaction logs, and reader protocol version 1; checkpoints, deletion vectors, partitioning, empty tables, and newer reader features are rejected.",
			"Queries use a restricted read-only SELECT subset and may download up to 64 MiB of active data files into gateway memory; the table must have at most 256 active files and 100,000 decoded rows.",
			"The local protocol fixture does not establish compatibility with a live MinIO release.")
	case model.DatabaseTypeAzureDelta:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"Azure Storage Shared Key or SAS token"}
		profile.AuthenticationDisclosure = "Local Azure SDK protocol fixtures cover Shared Key request signing and SAS query propagation; no live Azure Storage version contract is recorded."
		profile.Limitations = append(profile.Limitations,
			"The adapter reads one unpartitioned Delta table with Parquet files, JSON transaction logs, and reader protocol version 1; checkpoints, deletion vectors, partitioning, empty tables, and newer reader features are rejected.",
			"Queries use a restricted read-only SELECT subset and may download up to 64 MiB of active data files into gateway memory; the table must have at most 256 active files and 100,000 decoded rows.",
			"The local protocol fixture does not establish compatibility with a live Azure Storage release.")
	case model.DatabaseTypeMinIOIceberg:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"Iceberg REST catalog credentials and MinIO S3-compatible access key/secret key"}
		profile.AuthenticationDisclosure = "Local REST catalog and configuration fixtures cover query routing and MinIO FileIO property mapping; no live MinIO or REST Catalog release contract is recorded."
		profile.Limitations = append(profile.Limitations,
			"The adapter exposes one configured table through an Iceberg REST catalog and MinIO S3 FileIO; catalog_uri, warehouse, namespace, table_name, endpoint, and MinIO credentials are required.",
			"Queries use the shared Iceberg read-only SQL subset and row limit. Writes, a built-in REST catalog, catalog-wide table enumeration, and direct metadata-file discovery are not supported.")
	case model.DatabaseTypeMinIOORC:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"MinIO S3-compatible access key and secret key (SigV4)"}
		profile.AuthenticationDisclosure = "A local S3-compatible HTTP fixture covers signed object metadata and byte-range reads; no live MinIO release contract is recorded."
		profile.Limitations = append(profile.Limitations,
			"The adapter exposes one configured ORC object as one SQL table and reads object ranges into gateway memory before SQL filtering and pagination.",
			"Only primitive top-level columns and the codecs supported by the shared ORC decoder are available; nested/repeated columns, multi-object scans, partition discovery, and write operations are not supported.")
	case model.DatabaseTypeMinIOAvro:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"MinIO S3-compatible access key and secret key (SigV4)"}
		profile.AuthenticationDisclosure = "A local S3-compatible HTTP fixture covers signed object metadata and full-object reads; no live MinIO release contract is recorded."
		profile.Limitations = append(profile.Limitations,
			"The adapter exposes one configured Avro OCF object as one SQL table and downloads the full object into gateway memory before decoding and applying filters or pagination.",
			"Only flat records with primitive fields or nullable unions of null and one primitive type are supported. Nested records, arrays, maps, named unions, schema registries, multiple objects, partition scans, and writes are not supported.",
			"Only null, deflate, and Snappy OCF codecs are supported by the current decoder.")
	case model.DatabaseTypeMinIOJSON:
		profile.Status = ConnectorStatusExperimental
		profile.AuthenticationMethods = []string{"MinIO S3-compatible access key and secret key (SigV4)"}
		profile.AuthenticationDisclosure = "A local S3-compatible HTTP fixture covers signed object metadata and full-object reads for JSON objects, arrays, and JSON Lines; no live MinIO release contract is recorded."
		profile.Limitations = append(profile.Limitations,
			"The adapter exposes one configured JSON object as one SQL table and downloads the full object into gateway memory before parsing and applying filters or pagination.",
			"Only top-level scalar fields with stable types are supported. Nested object fields require flatten_nested=true; array-valued fields, flattened-name collisions, heterogeneous scalar fields, multiple objects, and partition scans are rejected or outside the adapter boundary.")
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
	case model.DatabaseTypeClickHouse:
		return "clickhouse_native_protocol"
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
	out.WriteString("\n`endToEndVerified` is true only after a versioned contract test passes. No compatibility range is declared for a source until the project records the evidence; connection attempts are allowed and the detected version is reported when available. Authentication methods are reported only when catalogued. For executed evidence, see [the driver contract test report](driver-contract-tests.md); new connector verification work should be scoped to a concrete user need and a pinned server version. For Delta Lake and Apache Hudi range semantics and adapter boundaries, see [the table-format read contracts](table-format-read-contracts.md).\n\nRegenerate this page after catalog changes with `go run ./cmd/generate-support-catalog`.\n")
	return out.String()
}

func capabilitySummary(capabilities drivers.DriverCapabilities) string {
	return fmt.Sprintf("SQL=%t; schema=%t; transactions=%t; streaming=%t; time_travel=%t",
		capabilities.SupportsSQL, capabilities.SupportsSchemaDiscovery,
		capabilities.SupportsTransaction, capabilities.SupportsStreaming,
		capabilities.SupportsTimeTravel)
}
