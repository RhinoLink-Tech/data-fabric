package metadata

import (
	"context"
	"sync"
	"time"
)

// SchemaCache caches database schema metadata to avoid repeated expensive queries
type SchemaCache struct {
	cache      map[string]*CachedSchema
	mutex      sync.RWMutex
	ttl        time.Duration
	cleanupInt time.Duration
	stopChan   chan struct{}
}

// CachedSchema represents cached schema metadata for a data source
type CachedSchema struct {
	DataSourceID string
	Schema       *DataSourceSchema
	CachedAt     time.Time
	ExpiresAt    time.Time
	Version      string // Schema version for cache invalidation
}

// DataSourceSchema represents the complete schema of a data source
type DataSourceSchema struct {
	Tables map[string]*TableSchema `json:"tables"`
}

// TableSchema represents the schema of a single table
// TableIdentity is the minimum metadata needed to authorize a table name
// before returning it from a discovery API.
type TableIdentity struct {
	Catalog string `json:"catalog,omitempty"`
	Name    string `json:"name"`
	Schema  string `json:"schema,omitempty"`
	Type    string `json:"type,omitempty"`
}

type TableSchema struct {
	Name        string                 `json:"name"`
	Schema      string                 `json:"schema,omitempty"` // Database/schema name
	Type        string                 `json:"type,omitempty"`   // TABLE, VIEW, etc.
	Columns     []ColumnSchema         `json:"columns"`
	PrimaryKey  []string               `json:"primaryKey,omitempty"`
	Indexes     []IndexSchema          `json:"indexes,omitempty"`
	ForeignKeys []ForeignKeySchema     `json:"foreignKeys,omitempty"`
	Properties  map[string]interface{} `json:"properties,omitempty"` // Table-specific properties (e.g., Iceberg partitioning)
}

// ColumnSchema represents a column definition
type ColumnSchema struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default,omitempty"`
	Comment  string `json:"comment,omitempty"`
	// Extended type information
	Length    int64 `json:"length,omitempty"`
	Precision int   `json:"precision,omitempty"`
	Scale     int   `json:"scale,omitempty"`
}

// IndexSchema represents an index definition
type IndexSchema struct {
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
	Unique  bool     `json:"unique"`
	Type    string   `json:"type,omitempty"` // BTREE, HASH, etc.
}

// ForeignKeySchema represents a foreign key constraint
type ForeignKeySchema struct {
	Name       string   `json:"name"`
	Columns    []string `json:"columns"`
	RefTable   string   `json:"refTable"`
	RefColumns []string `json:"refColumns"`
	OnDelete   string   `json:"onDelete,omitempty"`
	OnUpdate   string   `json:"onUpdate,omitempty"`
}

// NewSchemaCache creates a new schema cache
func NewSchemaCache(ttl time.Duration) *SchemaCache {
	if ttl <= 0 {
		ttl = 5 * time.Minute // Default TTL
	}

	return &SchemaCache{
		cache:      make(map[string]*CachedSchema),
		ttl:        ttl,
		cleanupInt: 10 * time.Minute, // Cleanup every 10 minutes
		stopChan:   make(chan struct{}),
	}
}

// Start begins the background cleanup process
func (sc *SchemaCache) Start(ctx context.Context) {
	ticker := time.NewTicker(sc.cleanupInt)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-sc.stopChan:
			return
		case <-ticker.C:
			sc.cleanupExpired()
		}
	}
}

// Stop stops the background cleanup process
func (sc *SchemaCache) Stop() {
	close(sc.stopChan)
}

// Get retrieves cached schema for a data source
func (sc *SchemaCache) Get(dataSourceID string) (*DataSourceSchema, bool) {
	sc.mutex.RLock()
	defer sc.mutex.RUnlock()

	cached, exists := sc.cache[dataSourceID]
	if !exists {
		return nil, false
	}

	// Check if expired
	if time.Now().After(cached.ExpiresAt) {
		return nil, false
	}

	return cached.Schema, true
}

// Set stores schema in cache
func (sc *SchemaCache) Set(dataSourceID string, schema *DataSourceSchema, version string) {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()

	sc.cache[dataSourceID] = &CachedSchema{
		DataSourceID: dataSourceID,
		Schema:       schema,
		CachedAt:     time.Now(),
		ExpiresAt:    time.Now().Add(sc.ttl),
		Version:      version,
	}
}

// Invalidate removes cached schema for a data source
func (sc *SchemaCache) Invalidate(dataSourceID string) {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()

	delete(sc.cache, dataSourceID)
}

// InvalidateByPattern invalidates cache entries matching a pattern
// Used when multiple tables might be affected (e.g., DROP DATABASE)
func (sc *SchemaCache) InvalidateByPattern(pattern string) {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()

	for id := range sc.cache {
		// Simple pattern matching - can be enhanced with regex
		if contains(id, pattern) {
			delete(sc.cache, id)
		}
	}
}

// Refresh updates cache if expired or missing
func (sc *SchemaCache) Refresh(ctx context.Context, dataSourceID string, extractor SchemaExtractor) (*DataSourceSchema, error) {
	// Check cache first
	if schema, ok := sc.Get(dataSourceID); ok {
		return schema, nil
	}

	// Extract fresh schema
	schema, version, err := extractor.ExtractSchema(ctx, dataSourceID)
	if err != nil {
		return nil, err
	}

	// Store in cache
	sc.Set(dataSourceID, schema, version)

	return schema, nil
}

// GetTable retrieves a specific table schema from cache
func (sc *SchemaCache) GetTable(dataSourceID, tableName string) (*TableSchema, bool) {
	schema, ok := sc.Get(dataSourceID)
	if !ok {
		return nil, false
	}

	table, exists := schema.Tables[tableName]
	return table, exists
}

// cleanupExpired removes all expired entries from cache
func (sc *SchemaCache) cleanupExpired() {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()

	now := time.Now()
	for id, cached := range sc.cache {
		if now.After(cached.ExpiresAt) {
			delete(sc.cache, id)
		}
	}
}

// GetStats returns cache statistics
func (sc *SchemaCache) GetStats() CacheStats {
	sc.mutex.RLock()
	defer sc.mutex.RUnlock()

	totalEntries := len(sc.cache)
	expiredEntries := 0
	now := time.Now()

	for _, cached := range sc.cache {
		if now.After(cached.ExpiresAt) {
			expiredEntries++
		}
	}

	return CacheStats{
		TotalEntries:   totalEntries,
		ActiveEntries:  totalEntries - expiredEntries,
		ExpiredEntries: expiredEntries,
		TTL:            sc.ttl,
	}
}

// CacheStats represents cache statistics
type CacheStats struct {
	TotalEntries   int           `json:"totalEntries"`
	ActiveEntries  int           `json:"activeEntries"`
	ExpiredEntries int           `json:"expiredEntries"`
	TTL            time.Duration `json:"ttl"`
}

// SetTTL updates the cache TTL
func (sc *SchemaCache) SetTTL(ttl time.Duration) {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()

	sc.ttl = ttl
}

// Clear clears all cache entries
func (sc *SchemaCache) Clear() {
	sc.mutex.Lock()
	defer sc.mutex.Unlock()

	sc.cache = make(map[string]*CachedSchema)
}

// Helper function for simple pattern matching
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr)))
}

// SchemaExtractor interface for extracting schema metadata
type SchemaExtractor interface {
	ExtractSchema(ctx context.Context, dataSourceID string) (*DataSourceSchema, string, error)
	ExtractTableSchema(ctx context.Context, dataSourceID, tableName string) (*TableSchema, error)
	ValidateSchema(ctx context.Context, dataSourceID string) error
}
