# Architecture Guide

## Overview

Data Fabric follows a clean, modular architecture designed for extensibility and maintainability. The system is built around a core gateway that routes queries to appropriate database drivers based on data source configuration.

## High-Level Architecture

~~~text
Application
    |
    v
Data Fabric API --- source ID ---> Connection and Query Services
                                      |                 |
                                      v                 v
                                Source Drivers    Query Engine Adapter
                                      |                 |
                                      v                 v
                              Individual Sources   Trino / Presto /
                                                   OpenLooKeng
                                                        |
                                                        v
                                             Engine Catalogs and Connectors
                                                        |
                                                        v
                                                Data Systems
~~~

The gateway supports direct access to an individual source and an optional federated path through an external query engine. In the federated path, Data Fabric manages the coordinator connection and query request; the engine owns catalogs, connectors, planning, and cross-catalog execution.

## Internal Architecture

### Core Components

```
data-fabric/
├── cmd/server/                    # Application entry point
├── internal/
│   ├── controller/               # HTTP request handlers
│   │   ├── query_controller.go
│   │   ├── datasource_controller.go
│   │   ├── health_controller.go
│   │   └── ...
│   ├── service/                  # Business logic layer
│   │   ├── query_service.go
│   │   ├── datasource_service.go
│   │   ├── driver_service.go
│   │   └── ...
│   ├── repository/               # Data access layer
│   │   ├── datasource_repository.go
│   │   └── ...
│   ├── model/                    # Data models and DTOs
│   │   ├── datasource.go
│   │   ├── query.go
│   │   └── ...
│   ├── middleware/               # HTTP middleware (JWT, rate limiting, etc.)
│   │   ├── auth_middleware.go
│   │   ├── rate_limit.go
│   │   └── ...
│   └── database/
│       ├── connection_pool.go    # Connection pooling
│       ├── driver_registry.go    # Driver registration system
│       ├── health_checker.go     # Health check utilities
│       └── drivers/              # Database drivers (90+ implementations)
│           ├── common/           # Shared driver utilities
│           │   └── types.go
│           ├── table_format/     # Iceberg, Delta Lake, Hudi
│           ├── warehouses/       # Snowflake, Databricks, Redshift, BigQuery
│           ├── olap/             # ClickHouse, Doris, StarRocks, Druid
│           ├── object_storage/   # S3, MinIO, OSS, COS, Azure Blob
│           ├── file_system/      # HDFS; Ozone placeholders are not registered
│           └── domestic/         # OceanBase, TiDB, TDSQL, etc.
├── configs/                      # Configuration files
├── pkg/response/                 # Response formatting utilities
├── migrations/                   # Database migration scripts
└── specs/                        # Specification documents
```

### Object-storage Parquet reads

The S3 and MinIO Parquet readers use seekable object adapters with byte-range
requests. The shared decoder maps flat primitive columns and supported Parquet
logical annotations into the gateway schema, preserves optional values as null,
and reports the decoded schema. Nested and repeated columns return an explicit
unsupported-schema error.

The MinIO driver's direct Query method supports column selection, equality
filters, offset, and limit. It currently decodes the file before applying those
operations, so it does not perform Parquet predicate or column pruning. This
driver-level method is separate from the standard HTTP query route.

### Key Design Patterns

#### 1. Driver Registry Pattern
The system uses a centralized registry to manage different database drivers:

```go
type DriverRegistry struct {
    drivers map[model.DatabaseType]func() drivers.Driver
    mutex   sync.RWMutex
}

func (dr *DriverRegistry) register(dbType model.DatabaseType, factoryFunc func() drivers.Driver) {
    dr.mutex.Lock()
    defer dr.mutex.Unlock()
    dr.drivers[dbType] = factoryFunc
}

func (dr *DriverRegistry) GetDriver(dbType model.DatabaseType) (drivers.Driver, error) {
    dr.mutex.RLock()
    defer dr.mutex.RUnlock()
    
    factoryFunc, exists := dr.drivers[dbType]
    if !exists {
        return nil, fmt.Errorf("unsupported database type: %s", dbType)
    }
    
    return factoryFunc(), nil
}
```

#### 2. Connection Pooling
Each data source maintains its own connection pool to optimize resource usage:

- Separate pools per data source type
- Configurable max connections
- Health checks and automatic recovery
- Reuse of existing connections

#### 3. Query Routing

The same query API routes through the selected data source driver:

1. Resolve the requested data source and verify that it is active.
2. Validate the query as a single read-only statement.
3. If sourceDialect is provided, translate SQL within the supported dialect contract.
4. Select the driver from the data source type and open or reuse its pooled connection.
5. For Trino, Presto, or OpenLooKeng data sources, the shared protocol adapter submits the statement to the configured coordinator. The coordinator resolves catalog-qualified table names using its own catalogs and connectors.
6. Normalize result columns and values, then return results using the query or streaming API.
7. Propagate request deadlines and cancellation to the active query and release result resources.

For direct database sources, the selected source driver executes the query itself. For federated SQL, Data Fabric is the application-facing gateway; it does not embed a second planner or duplicate the engine catalog configuration.

## Security Architecture

### Authentication Flow
```
Client Request → JWT Validation → Data Source ACL → Query Validation → Execution
```

### Security Controls
- JWT-based authentication
- SQL injection prevention
- Connection credential encryption
- Rate limiting
- Query validation (SELECT only)
- Data source access controls

## Performance Architecture

### Caching Strategy
- Query result caching (optional)
- Connection pooling
- Schema caching
- Prepared statement caching

### Streaming Support
- Server-side cursors for large result sets
- Chunked response delivery
- Configurable batch sizes
- Automatic fallback to LIMIT/OFFSET

## Extensibility Points

### Adding New Drivers
The system is designed to easily accommodate new database types:

1. Implement the `drivers.Driver` interface
2. Register the driver in the registry
3. Add configuration support
4. Update documentation

### Middleware Extensions
New middleware can be added to enhance functionality:
- Authentication methods
- Logging enhancements
- Performance monitoring
- Custom validation rules

## Deployment Architecture

### Single Instance
```
┌─────────────────┐
│   Load Balancer │
└─────────┬───────┘
          │
    ┌─────▼──────┐
    │  Gateway   │
    │  Instance  │
    └─────┬──────┘
          │
    ┌─────▼─────────────┐
    │   Data Sources    │
    └───────────────────┘
```

### Multi-Instance
```
┌─────────────────┐
│   Load Balancer │
└─────────┬───────┘
          │
    ┌─────▼─────────────┐
    │  Gateway Cluster  │
    ├───────────────────┤
    │ GW-1 │ GW-2 │ GW-3 │
    └──────┴──────┴─────┘
          │
    ┌─────▼─────────────┐
    │   Data Sources    │
    └───────────────────┘
```

## Monitoring and Observability

### Metrics Collection
- Query execution times
- Connection pool metrics
- Error rates
- Throughput measurements
- Resource utilization

### Logging
- Structured JSON logs
- Correlation IDs for request tracing
- Detailed audit trails
- Performance insights