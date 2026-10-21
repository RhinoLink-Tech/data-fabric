package table_formats

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// IcebergRESTClient implements a REST client for Apache Iceberg
type IcebergRESTClient struct {
	baseURL    string
	warehouse  string
	httpClient *http.Client
	auth       *IcebergAuth
}

// IcebergAuth holds authentication credentials for Iceberg REST API
type IcebergAuth struct {
	Credential string // OAuth token or basic auth credentials
	Type       string // "bearer", "basic", "oauth2"
}

// IcebergConfig holds configuration for Iceberg REST client
type IcebergConfig struct {
	BaseURL   string        // Iceberg REST API endpoint (e.g., http://localhost:8181)
	Warehouse string        // Warehouse name
	Timeout   time.Duration // Request timeout
	Auth      *IcebergAuth  // Authentication credentials
}

// NewIcebergRESTClient creates a new Iceberg REST client
func NewIcebergRESTClient(config *IcebergConfig) (*IcebergRESTClient, error) {
	if config.BaseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}

	timeout := config.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	return &IcebergRESTClient{
		baseURL:   config.BaseURL,
		warehouse: config.Warehouse,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		auth: config.Auth,
	}, nil
}

// ListNamespaces lists all namespaces in the catalog
func (c *IcebergRESTClient) ListNamespaces(ctx context.Context) ([]IcebergNamespace, error) {
	url := fmt.Sprintf("%s/v1/namespaces", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result struct {
		Namespaces []IcebergNamespace `json:"namespaces"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Namespaces, nil
}

// ListTables lists all tables in a namespace
func (c *IcebergRESTClient) ListTables(ctx context.Context, namespace string) ([]IcebergTableIdentifier, error) {
	url := fmt.Sprintf("%s/v1/namespaces/%s/tables", c.baseURL, namespace)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result struct {
		Identifiers []IcebergTableIdentifier `json:"identifiers"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return result.Identifiers, nil
}

// LoadTable loads table metadata
func (c *IcebergRESTClient) LoadTable(ctx context.Context, namespace, table string) (*IcebergTableMetadata, error) {
	url := fmt.Sprintf("%s/v1/namespaces/%s/tables/%s", c.baseURL, namespace, table)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeader(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var response map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	payload, ok := response["metadata"]
	if !ok {
		// Some compatible catalogs return bare metadata instead of the standard
		// Iceberg REST LoadTableResponse envelope.
		var err error
		payload, err = json.Marshal(response)
		if err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}
	}
	if len(payload) == 0 || string(payload) == "null" {
		return nil, fmt.Errorf("Iceberg REST LoadTableResponse did not include table metadata")
	}
	var metadata IcebergTableMetadata
	if err := json.Unmarshal(payload, &metadata); err != nil {
		return nil, fmt.Errorf("failed to decode table metadata: %w", err)
	}
	return &metadata, nil
}

// IcebergQueryResult represents query results from Iceberg
type IcebergQueryResult struct {
	Columns []string
	Rows    [][]interface{}
	Schema  *IcebergSchema
}

// QueryTable executes a query on the table (via REST API)
func (c *IcebergRESTClient) QueryTable(ctx context.Context, namespace, table, sql string) (*IcebergQueryResult, error) {
	// Iceberg REST API v1 doesn't support direct SQL queries
	// Queries are typically executed through compute engines like Spark, Trino
	// This is a placeholder for when the API supports queries or when using a compatible endpoint

	return nil, fmt.Errorf("direct SQL queries not supported through REST API - use a compute engine")
}

// GetTableSnapshot retrieves a specific snapshot for time travel
func (c *IcebergRESTClient) GetTableSnapshot(ctx context.Context, namespace, table, snapshotID string) (*IcebergSnapshot, error) {
	metadata, err := c.LoadTable(ctx, namespace, table)
	if err != nil {
		return nil, err
	}
	requestedID, err := strconv.ParseInt(snapshotID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid Iceberg snapshot ID %q: %w", snapshotID, err)
	}
	for i := range metadata.Snapshots {
		if metadata.Snapshots[i].SnapshotID == requestedID {
			return &metadata.Snapshots[i], nil
		}
	}
	return nil, fmt.Errorf("Iceberg snapshot %d was not found in table %s.%s", requestedID, namespace, table)
}

// CreateView creates a view in the catalog
func (c *IcebergRESTClient) CreateView(ctx context.Context, namespace string, view *IcebergView) error {
	url := fmt.Sprintf("%s/v1/namespaces/%s/views", c.baseURL, namespace)

	body, err := json.Marshal(view)
	if err != nil {
		return fmt.Errorf("failed to marshal view: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeader(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// CommitTable commits table metadata updates
func (c *IcebergRESTClient) CommitTable(ctx context.Context, namespace, table string, requirements []IcebergRequirement, updates []IcebergUpdate) error {
	url := fmt.Sprintf("%s/v1/namespaces/%s/tables/%s", c.baseURL, namespace, table)

	payload := map[string]interface{}{
		"requirements": requirements,
		"updates":      updates,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	c.setAuthHeader(req)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}

// setAuthHeader sets the authentication header based on auth type
func (c *IcebergRESTClient) setAuthHeader(req *http.Request) {
	if c.auth == nil {
		return
	}

	switch c.auth.Type {
	case "bearer", "oauth2":
		req.Header.Set("Authorization", "Bearer "+c.auth.Credential)
	case "basic":
		req.Header.Set("Authorization", "Basic "+c.auth.Credential)
	}
}

// =============================================================================
// Iceberg Data Structures
// =============================================================================

// IcebergNamespace represents a namespace (database) in Iceberg
type IcebergNamespace struct {
	Name       string            `json:"name"`
	Properties map[string]string `json:"properties,omitempty"`
}

// IcebergTableIdentifier uniquely identifies a table
type IcebergTableIdentifier struct {
	Namespace []string `json:"namespace"`
	Name      string   `json:"name"`
}

// IcebergTableMetadata represents table metadata
type IcebergTableMetadata struct {
	Format          string                  `json:"format"`
	FormatVersion   int                     `json:"format-version,omitempty"`
	Schema          IcebergSchema           `json:"schema"`
	Schemas         []IcebergSchema         `json:"-"`
	PartitionSpec   []IcebergPartitionField `json:"partition-spec"`
	PartitionSpecs  []IcebergPartitionSpec  `json:"-"`
	SpecID          int                     `json:"spec-id"`
	DefaultSpecID   int                     `json:"-"`
	Snapshots       []IcebergSnapshot       `json:"snapshots"`
	CurrentSchemaID int                     `json:"current-schema-id"`
	Properties      map[string]string       `json:"properties,omitempty"`
	SnapshotLog     []IcebergSnapshotLog    `json:"snapshot-log"`
	MetadataLog     []IcebergMetadataLog    `json:"metadata-log"`
}

// UnmarshalJSON accepts standard Iceberg table metadata and the legacy flat
// shape used by older catalog integrations.
func (m *IcebergTableMetadata) UnmarshalJSON(data []byte) error {
	type metadataAlias IcebergTableMetadata
	var decoded metadataAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var standard struct {
		FormatVersion  int                    `json:"format-version"`
		Schemas        []IcebergSchema        `json:"schemas"`
		DefaultSpecID  int                    `json:"default-spec-id"`
		PartitionSpecs []IcebergPartitionSpec `json:"partition-specs"`
	}
	if err := json.Unmarshal(data, &standard); err != nil {
		return err
	}
	*m = IcebergTableMetadata(decoded)
	m.FormatVersion = standard.FormatVersion
	m.Schemas = standard.Schemas
	m.PartitionSpecs = standard.PartitionSpecs
	m.DefaultSpecID = standard.DefaultSpecID

	if len(m.Schemas) != 0 {
		found := false
		for _, schema := range m.Schemas {
			if schema.SchemaID == m.CurrentSchemaID {
				m.Schema = schema
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("Iceberg metadata current schema ID %d was not found", m.CurrentSchemaID)
		}
	}
	if len(m.PartitionSpecs) != 0 {
		found := false
		for _, spec := range m.PartitionSpecs {
			if spec.SpecID == m.DefaultSpecID {
				m.SpecID = spec.SpecID
				m.PartitionSpec = spec.Fields
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("Iceberg metadata default partition spec ID %d was not found", m.DefaultSpecID)
		}
	}
	if m.Format == "" && m.FormatVersion > 0 {
		m.Format = fmt.Sprintf("iceberg-v%d", m.FormatVersion)
	}
	return nil
}

// IcebergSchema represents table schema
type IcebergSchema struct {
	SchemaID           int            `json:"schema-id"`
	Fields             []IcebergField `json:"fields"`
	IdentifierFieldIds []int          `json:"identifier-field-ids,omitempty"`
}

// IcebergField represents a column in the schema
type IcebergField struct {
	ID       int         `json:"id"`
	Name     string      `json:"name"`
	Type     IcebergType `json:"type"`
	Required bool        `json:"required"`
	Doc      string      `json:"doc,omitempty"`
}

// IcebergType represents a field type (can be nested)
type IcebergType interface{}

// IcebergPartitionField represents a partition field
type IcebergPartitionField struct {
	SourceID  int    `json:"source-id"`
	FieldID   int    `json:"field-id,omitempty"`
	Name      string `json:"name"`
	Transform string `json:"transform"`
}

// IcebergPartitionSpec is a versioned partition definition in table metadata.
type IcebergPartitionSpec struct {
	SpecID int                     `json:"spec-id"`
	Fields []IcebergPartitionField `json:"fields"`
}

// IcebergSnapshot represents a table snapshot
type IcebergSnapshot struct {
	SnapshotID       int64                  `json:"snapshot-id"`
	SchemaID         int                    `json:"schema-id"`
	SequenceNumber   int64                  `json:"sequence-number,omitempty"`
	ParentSnapshotID *int64                 `json:"parent-snapshot-id,omitempty"`
	TimestampMs      int64                  `json:"timestamp-ms"`
	Summary          IcebergSnapshotSummary `json:"summary"`
	ManifestList     string                 `json:"manifest-list,omitempty"`
	Manifests        []string               `json:"manifests,omitempty"`
}

// IcebergSnapshotSummary contains snapshot summary information
type IcebergSnapshotSummary struct {
	Operation string `json:"operation"`
}

// IcebergSnapshotLog represents a snapshot log entry
type IcebergSnapshotLog struct {
	SnapshotID  int64 `json:"snapshot-id"`
	TimestampMs int64 `json:"timestamp-ms"`
}

// IcebergMetadataLog represents a metadata log entry
type IcebergMetadataLog struct {
	MetadataFile string `json:"metadata-file"`
	TimestampMs  int64  `json:"timestamp-ms"`
}

// IcebergView represents a view definition
type IcebergView struct {
	Name       string            `json:"name"`
	Namespace  []string          `json:"namespace"`
	Schema     IcebergSchema     `json:"schema"`
	ViewSQL    string            `json:"sql"`
	Properties map[string]string `json:"properties,omitempty"`
}

// APIIcebergQueryResult represents query results from API response
type APIIcebergQueryResult struct {
	Columns []string        `json:"columns"`
	Rows    [][]interface{} `json:"rows"`
}

// IcebergRequirement represents a table requirement for commits
type IcebergRequirement struct {
	Type       string `json:"type"`
	SnapshotID int64  `json:"snapshot-id,omitempty"`
}

// IcebergUpdate represents a table update
type IcebergUpdate struct {
	Action string `json:"action"`
	// Additional fields based on action type
}

// ConvertIcebergToStandardType converts Iceberg type to standard database type
func ConvertIcebergToStandardType(icebergType interface{}) string {
	switch t := icebergType.(type) {
	case string:
		return convertPrimitiveType(t)
	case map[string]interface{}:
		// Handle nested types
		if typeStr, ok := t["type"].(string); ok {
			switch typeStr {
			case "list":
				return "ARRAY"
			case "map":
				return "MAP"
			case "struct":
				return "STRUCT"
			}
		}
		return "UNKNOWN"
	default:
		return "UNKNOWN"
	}
}

func convertPrimitiveType(t string) string {
	switch t {
	case "boolean":
		return "BOOLEAN"
	case "int":
		return "INTEGER"
	case "long":
		return "BIGINT"
	case "float":
		return "FLOAT"
	case "double":
		return "DOUBLE"
	case "decimal":
		return "DECIMAL"
	case "date":
		return "DATE"
	case "time":
		return "TIME"
	case "timestamp":
		return "TIMESTAMP"
	case "timestamptz":
		return "TIMESTAMPTZ"
	case "string":
		return "VARCHAR"
	case "uuid":
		return "UUID"
	case "fixed":
		return "BINARY"
	case "binary":
		return "VARBINARY"
	default:
		return "UNKNOWN"
	}
}

// ParseTableIdentifier parses a table identifier string (namespace.table)
func ParseTableIdentifier(identifier string) (namespace, table string, err error) {
	// Simple parser for "namespace.table" format
	// In production, use proper parsing logic
	parts := bytes.Split([]byte(identifier), []byte{'.'})
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid table identifier format: %s", identifier)
	}
	return string(parts[0]), string(parts[1]), nil
}
