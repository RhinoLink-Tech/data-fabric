# Development Guide

## Overview

This guide provides information for developers looking to contribute to Data Fabric or extend its functionality. It covers the project structure, coding standards, and extension mechanisms.

## Project Structure

Understanding the project structure is crucial for effective development:

```
data-fabric/
├── cmd/
│   └── server/
│       └── main.go              # Application entry point
├── internal/
│   ├── controller/              # HTTP request handlers
│   │   ├── query_controller.go
│   │   ├── datasource_controller.go
│   │   └── health_controller.go
│   ├── service/                 # Business logic layer
│   │   ├── query_service.go
│   │   ├── datasource_service.go
│   │   └── driver_service.go
│   ├── repository/              # Data access layer
│   │   ├── datasource_repository.go
│   │   └── query_history_repository.go
│   ├── model/                   # Data models
│   │   ├── datasource.go
│   │   ├── query.go
│   │   └── result.go
│   ├── middleware/              # Middleware
│   │   ├── auth.go
│   │   ├── rate_limit.go
│   │   └── logger.go
│   └── database/
│       ├── connection_pool.go    # Connection pooling
│       ├── driver_registry.go    # Driver registration system
│       ├── health_checker.go     # Health check utilities
│       └── drivers/             # Database drivers
│           ├── common/           # Shared driver utilities
│           │   └── types.go      # Driver interface definition
│           ├── table_format/    # Iceberg, Delta, Hudi
│           ├── warehouses/      # Snowflake, Databricks, etc.
│           ├── olap/            # ClickHouse, Doris, etc.
│           ├── object_storage/  # S3, MinIO, etc.
│           ├── file_system/     # HDFS; Ozone placeholders are not registered
│           └── domestic/        # OceanBase, TiDB, etc.
├── configs/                     # Configuration files
├── docs/                        # Documentation
├── pkg/                         # Reusable packages
├── migrations/                  # Database migration scripts
├── specs/                       # Specification documents
└── test/                        # Test files
```

## Driver Framework Design

### Core Components

#### Driver Interface (drivers.Driver)

All drivers must implement the unified interface:

```go
type Driver interface {
    Open(dsn string) (*sql.DB, error)           // Open database connection
    ValidateDSN(dsn string) error               // Validate connection string
    GetDefaultPort() int                        // Get default port
    BuildDSN(config *model.DataSourceConfig) string  // Build connection string
    GetDatabaseTypeName() string                // Get database type name
    TestConnection(db *sql.DB) error            // Test connection
    GetDriverName() string                      // Get driver name
    GetCategory() DriverCategory                // Get driver category
    GetCapabilities() DriverCapabilities        // Get driver capabilities
    ConfigureAuth(authConfig interface{}) error // Configure authentication
}
```

#### Driver Registry

- **Singleton Pattern**: Provides globally unique driver registry instance via `GetDriverRegistry()` function
- **Factory Pattern**: Uses `map[model.DatabaseType]func() drivers.Driver` to store driver creation functions
- **Thread Safe**: Uses `sync.RWMutex` to ensure concurrent safety

#### Driver Categories

- `traditional`: Traditional relational databases (MySQL, PostgreSQL, Oracle, etc.)
- `warehouses`: Cloud data warehouses (Snowflake, Redshift, BigQuery, etc.)
- `olap`: OLAP engines (ClickHouse, Doris, StarRocks, etc.)
- `object_storage`: Object storage (S3, MinIO, etc.)
- `file_system`: Distributed file systems (HDFS; Ozone client placeholders are not registered as supported drivers)
- `table_formats`: Data lake table formats (Iceberg, Delta, Hudi)
- `domestic`: Domestic databases (OceanBase, TiDB, Dameng, etc.)

### Driver Registration Mechanism

Driver registration is implemented through the `DriverRegistry.registerDrivers()` method using the following strategy:

1. **Static Registration**: Register all supported drivers at application startup
2. **Factory Functions**: Each driver type is associated with a creation function, supporting on-demand instantiation
3. **Categorized Management**: Organize drivers by functional categories for easier management and expansion

## Adding New Drivers

### Driver Implementation Standards

#### Required Methods Implementation

All drivers must implement the following methods:

1. **Connection Management**:
   - `Open()`: Establish database connection
   - `ValidateDSN()`: Validate connection parameters
   - `GetDefaultPort()`: Return default port

2. **Configuration Processing**:
   - `BuildDSN()`: Build connection string from configuration
   - `ConfigureAuth()`: Configure authentication parameters

3. **Metadata**:
   - `GetDatabaseTypeName()`: Return database type
   - `GetDriverName()`: Return driver name
   - `GetCategory()`: Return driver category

4. **Capability Query**:
   - `GetCapabilities()`: Return driver capabilities

### Type Mapping Standards

To ensure cross-database compatibility, all drivers should implement type mapping:

```go
type StandardizedType int

const (
    StandardizedTypeBoolean StandardizedType = iota
    StandardizedTypeInteger
    StandardizedTypeLong
    StandardizedTypeFloat
    StandardizedTypeDouble
    StandardizedTypeDecimal
    StandardizedTypeString
    StandardizedTypeText
    StandardizedTypeDate
    StandardizedTypeTime
    StandardizedTypeTimestamp
    StandardizedTypeDateTime
    StandardizedTypeTimestampWithZone
    StandardizedTypeBinary
    StandardizedTypeVariant
    StandardizedTypeArray
    StandardizedTypeStruct
    StandardizedTypeObject
    StandardizedTypeGeography
)
```

### Security Features

1. **SQL Injection Protection**: All queries use parameterized queries
2. **Read-Only Queries**: Only SELECT statements allowed by default
3. **Connection Pool Management**: Independent connection pool per data source
4. **Credential Encryption**: Connection credentials encrypted storage

## Getting Started

### Prerequisites

- Go 1.26.1 or higher
- Git
- Make (optional, for convenience targets)

### Setup

1. Clone the repository:
   ```bash
   git clone https://github.com/RhinoLink-Tech/data-fabric.git
   cd data-fabric
   ```

2. Install dependencies:
   ```bash
   go mod download
   ```

3. Set up configuration:
   ```bash
   # If you already have a local configs/config.yaml, back it up first —
   # it is no longer tracked by git.
   cp configs/config.yaml.example configs/config.yaml
   # Edit configs/config.yaml with your settings; environment variable names
   # use uppercase config paths (for example SERVER_PORT). Inject secrets via
   # DATABASE_USERNAME / DATABASE_PASSWORD / SECURITY_JWT_SECRET instead.
   ```

Before running the application, inject a persistent data-source credential keyring. Generate its key with openssl rand -base64 32, store it securely, and keep it unchanged across restarts:
    export DATA_FABRIC_CREDENTIAL_ACTIVE_KEY_ID=dev-v1
    export DATA_FABRIC_CREDENTIAL_KEYS='{"dev-v1":"<base64-encoded-32-byte-key>"}'

4. Run the application:
   ```bash
   go run ./cmd/server
   ```

   Or use the Make targets, which are the same entry points CI exercises:
   ```bash
   make run    # go run ./cmd/server
   make build  # package build into bin/data-fabric
   make test   # go test ./...
   ```

   `make build` compiles the whole `./cmd/server` package (never a single
   `main.go` file) and is what the CI workflow runs in addition to
   `go build ./...`, so a broken build entry fails the same check locally.

## Coding Standards

### Go Code Standards

- Follow the official Go formatting guidelines (gofmt)
- Use meaningful variable and function names
- Write clear, concise comments
- Document exported functions and types
- Follow the project's error handling patterns

### Naming Conventions

- Use camelCase for variable and function names
- Use PascalCase for exported types and functions
- Use descriptive names that reflect purpose
- Follow Go idioms and patterns

### Error Handling

```go
// Good pattern
if err := someOperation(); err != nil {
    return fmt.Errorf("failed to perform operation: %w", err)
}
```

## Adding a New Database Driver

### Step-by-Step Process

1. **Choose the appropriate category directory**:
   - `table_format/` for data lake formats (Iceberg, Delta Lake, Hudi)
   - `warehouses/` for cloud data warehouses (Snowflake, Databricks, etc.)
   - `olap/` for OLAP engines (ClickHouse, Doris, etc.)
   - `object_storage/` for object storage systems (S3, MinIO, etc.)
   - `file_system/` for distributed file systems (HDFS; Ozone is not currently a registered driver)
   - `domestic/` for Chinese domestic databases (OceanBase, TiDB, etc.)

2. **Implement the Driver Interface**:

```go
package warehouses

import (
    "database/sql"
    "github.com/RhinoLink-Tech/data-fabric/internal/database/drivers"
    "github.com/RhinoLink-Tech/data-fabric/internal/model"
)

type ExampleDriver struct{}

func (d *ExampleDriver) Open(dsn string) (*sql.DB, error) {
    // Implementation for opening database connection
    return sql.Open("driver_name", dsn)
}

func (d *ExampleDriver) ValidateDSN(dsn string) error {
    // Implementation for validating connection string
    return nil
}

func (d *ExampleDriver) GetDefaultPort() int {
    // Return the default port for this database
    return 1234
}

func (d *ExampleDriver) BuildDSN(config *model.DataSourceConfig) string {
    // Build connection string from configuration
    return ""
}

func (d *ExampleDriver) GetDatabaseTypeName() string {
    // Return the database type identifier
    return "example_db"
}

func (d *ExampleDriver) TestConnection(db *sql.DB) error {
    // Implementation for testing connection
    return db.Ping()
}

func (d *ExampleDriver) GetDriverName() string {
    // Return the driver name
    return "ExampleDriver"
}

func (d *ExampleDriver) GetCategory() drivers.DriverCategory {
    // Return the driver category
    return drivers.CategoryWarehouse
}

func (d *ExampleDriver) GetCapabilities() DriverCapabilities {
    // Return the capabilities of this driver
    return DriverCapabilities{
        SupportsStreaming: true,
        SupportsTimeTravel: true,
        // ... other capabilities
    }
}

func (d *ExampleDriver) ConfigureAuth(authConfig interface{}) error {
    // Implementation for configuring authentication
    return nil
}
```

3. **Register the driver** in the driver registry:
   - Add the import to the registry file
   - Register the driver in the `registerDrivers` function

4. **Add the database type constant** to the [model/datasource.go](file:///D:/opensource/data-fabric/internal/model/datasource.go) file

5. **Update documentation** to include the new driver

### Driver Interface Requirements

All drivers must implement the complete `drivers.Driver` interface:

```go
type Driver interface {
    Open(dsn string) (*sql.DB, error)
    ValidateDSN(dsn string) error
    GetDefaultPort() int
    BuildDSN(config *model.DataSourceConfig) string
    GetDatabaseTypeName() string
    TestConnection(db *sql.DB) error
    GetDriverName() string
    GetCategory() DriverCategory
    GetCapabilities() DriverCapabilities
    ConfigureAuth(authConfig interface{}) error
}
```

## Testing

### Unit Tests

Run all unit tests:
```bash
go test ./...
```

Run tests for a specific package:
```bash
go test ./internal/service/
```

### Integration Tests

Integration tests may require specific database setups. Check the `test/` directory for specific integration test requirements.

### Adding Tests

When adding new functionality:
1. Write unit tests for new functions
2. Write integration tests for new features
3. Ensure existing tests continue to pass

## Security Considerations

### SQL Injection Prevention

All database interactions must use parameterized queries:
```go
// Good
rows, err := db.Query("SELECT * FROM table WHERE id = ?", id)

// Bad - vulnerable to SQL injection
rows, err := db.Query(fmt.Sprintf("SELECT * FROM table WHERE id = %s", id))
```

### Authentication and Authorization

- Use JWT tokens for authentication
- Validate permissions before executing queries
- Encrypt sensitive data at rest
- Use HTTPS/TLS for all communications

## Performance Optimization

### Connection Pooling

- Use the built-in connection pooling mechanism
- Configure appropriate pool sizes
- Handle connection failures gracefully

### Query Optimization

- Use prepared statements where possible
- Implement efficient query execution paths
- Cache frequently accessed data
- Optimize for large result sets with streaming

## Debugging

### Logging

The application uses structured logging:
- Use appropriate log levels (debug, info, warn, error)
- Include relevant context in log messages
- Use correlation IDs for request tracing

### Profiling

The server does not currently expose CPU or memory profiling flags. To run it locally:
```bash
go run ./cmd/server
```

## Contributing

### Pull Request Process

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/AmazingFeature`)
3. Make your changes
4. Add tests for new functionality
5. Ensure all tests pass (`go test ./...`)
6. Update documentation as needed
7. Commit your changes (`git commit -m 'Add some AmazingFeature'`)
8. Push to the branch (`git push origin feature/AmazingFeature`)
9. Open a Pull Request

### Code Review Checklist

Before submitting your PR, ensure:
- [ ] All tests pass
- [ ] Code follows project standards
- [ ] Security considerations are addressed
- [ ] Performance implications are considered
- [ ] Documentation is updated
- [ ] Comments explain complex logic
- [ ] Error handling is comprehensive
- [ ] New features are well-tested