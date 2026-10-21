package table_formats

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DeltaRESTClient implements a REST client for Delta Lake
type DeltaRESTClient struct {
	baseURL     string
	warehouseID string
	timeout     time.Duration
	httpClient  *http.Client
	auth        *DeltaAuth
}

// DeltaAuth holds authentication credentials for Delta Lake API
type DeltaAuth struct {
	// Databricks uses personal access tokens
	Token     string
	TokenType string // "bearer", "databricks-token"
}

// DeltaConfig holds configuration for Delta Lake REST client
type DeltaConfig struct {
	BaseURL     string        // Databricks workspace URL for Unity Catalog and SQL Statement Execution
	WarehouseID string        // SQL warehouse used for table history and statements
	Timeout     time.Duration // Request timeout
	Auth        *DeltaAuth    // Authentication credentials
}

// NewDeltaRESTClient creates a new Delta Lake REST client
func NewDeltaRESTClient(config *DeltaConfig) (*DeltaRESTClient, error) {
	if config == nil {
		return nil, fmt.Errorf("Delta configuration is required")
	}
	parsed, err := url.Parse(strings.TrimRight(config.BaseURL, "/"))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return nil, fmt.Errorf("base URL must be an absolute HTTP or HTTPS workspace URL")
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &DeltaRESTClient{
		baseURL: strings.TrimRight(config.BaseURL, "/"), warehouseID: config.WarehouseID,
		timeout: timeout, httpClient: &http.Client{Timeout: timeout}, auth: config.Auth,
	}, nil
}

// ListTables lists Unity Catalog tables and follows the service continuation token.
func (c *DeltaRESTClient) ListTables(ctx context.Context, catalog, schema string) ([]DeltaTableIdentifier, error) {
	tables := make([]DeltaTableIdentifier, 0)
	pageToken := ""
	seenTokens := make(map[string]struct{})
	for pageCount := 0; pageCount < 10000; pageCount++ {
		params := url.Values{}
		if catalog != "" {
			params.Set("catalog_name", catalog)
		}
		if schema != "" {
			params.Set("schema_name", schema)
		}
		params.Set("max_results", "100")
		if pageToken != "" {
			params.Set("page_token", pageToken)
		}
		endpoint := c.baseURL + "/api/2.1/unity-catalog/tables?" + params.Encode()

		var response map[string]json.RawMessage
		if err := c.requestDeltaJSON(ctx, http.MethodGet, endpoint, nil, &response); err != nil {
			return nil, err
		}
		var page []DeltaTableIdentifier
		if err := json.Unmarshal(response["tables"], &page); err != nil {
			return nil, fmt.Errorf("decode Unity Catalog table list: %w", err)
		}
		tables = append(tables, page...)
		var nextToken string
		if len(response["next_page_token"]) > 0 {
			if err := json.Unmarshal(response["next_page_token"], &nextToken); err != nil {
				return nil, fmt.Errorf("decode Unity Catalog page token: %w", err)
			}
		}
		if nextToken == "" {
			return tables, nil
		}
		if _, exists := seenTokens[nextToken]; exists {
			return nil, fmt.Errorf("Unity Catalog repeated a page token")
		}
		seenTokens[nextToken] = struct{}{}
		pageToken = nextToken
	}
	return nil, fmt.Errorf("Unity Catalog table listing exceeded the page safety limit")
}

// GetTableDetails retrieves table details from Unity Catalog.
func (c *DeltaRESTClient) GetTableDetails(ctx context.Context, catalog, schema, table string) (*DeltaTableDetails, error) {
	parts := []string{catalog, schema, table}
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return nil, fmt.Errorf("catalog, schema, and table are required")
		}
	}
	fullName := strings.Join(parts, ".")
	endpoint := c.baseURL + "/api/2.1/unity-catalog/tables/" + url.PathEscape(fullName)
	var details DeltaTableDetails
	if err := c.requestDeltaJSON(ctx, http.MethodGet, endpoint, nil, &details); err != nil {
		return nil, err
	}
	return &details, nil
}

// QueryTable executes a statement through the Databricks SQL Statement Execution API.
func (c *DeltaRESTClient) QueryTable(ctx context.Context, warehouseID, statement string) (*DeltaQueryResult, error) {
	return c.executeStatement(ctx, warehouseID, statement)
}

// GetTableHistory reads commit history through Databricks SQL Statement Execution.
func (c *DeltaRESTClient) GetTableHistory(ctx context.Context, tablePath string) ([]DeltaCommitInfo, error) {
	return c.readTableHistory(ctx, tablePath)
}

// ReadChangeDataFeed reads a fixed inclusive Delta version range through the
// Databricks SQL table_changes function. Change Data Feed must be enabled for
// the requested table and range.
func (c *DeltaRESTClient) ReadChangeDataFeed(ctx context.Context, warehouseID, catalog, schema, table string, startVersion int64, endVersion *int64) (*DeltaQueryResult, error) {
	statement, err := BuildDeltaChangeDataFeedStatement(catalog, schema, table, startVersion, endVersion)
	if err != nil {
		return nil, err
	}
	return c.executeStatement(ctx, warehouseID, statement)
}

// BuildDeltaChangeDataFeedStatement validates identifiers and versions before
// composing the Databricks table_changes table-valued function call.
func BuildDeltaChangeDataFeedStatement(catalog, schema, table string, startVersion int64, endVersion *int64) (string, error) {
	parts := []string{catalog, schema, table}
	quoted := make([]string, len(parts))
	for index, part := range parts {
		if strings.TrimSpace(part) == "" || strings.ContainsRune(part, 0) {
			return "", fmt.Errorf("catalog, schema, and table identifiers are required")
		}
		quoted[index] = "`" + strings.ReplaceAll(part, "`", "``") + "`"
	}
	if startVersion < 0 {
		return "", fmt.Errorf("Delta start version must be non-negative")
	}
	versionRange := fmt.Sprintf("%d", startVersion)
	if endVersion != nil {
		if *endVersion < startVersion {
			return "", fmt.Errorf("Delta end version must be greater than or equal to start version")
		}
		versionRange += fmt.Sprintf(", %d", *endVersion)
	}
	tableIdentifier := strings.Join(quoted, ".")
	tableLiteral := "'" + strings.ReplaceAll(tableIdentifier, "'", "''") + "'"
	return fmt.Sprintf("SELECT * FROM table_changes(%s, %s)", tableLiteral, versionRange), nil
}

// setAuthHeader sets the authentication header
func (c *DeltaRESTClient) setAuthHeader(req *http.Request) {
	if c.auth == nil {
		return
	}

	switch c.auth.TokenType {
	case "", "bearer", "databricks-token":
		req.Header.Set("Authorization", "Bearer "+c.auth.Token)
	default:
		req.Header.Set("Authorization", c.auth.Token)
	}
}

// =============================================================================
// Delta Lake Data Structures
// =============================================================================

// DeltaTableIdentifier identifies a Delta table
type DeltaTableIdentifier struct {
	CatalogName string `json:"catalog_name"`
	SchemaName  string `json:"schema_name"`
	Name        string `json:"name"`
	TableType   string `json:"table_type,omitempty"`
}

// DeltaTableDetails represents table details
type DeltaTableDetails struct {
	DeltaTableIdentifier
	Comment         string            `json:"comment,omitempty"`
	CreatedAt       int64             `json:"created_at"`
	CreatedBy       string            `json:"created_by"`
	Properties      map[string]string `json:"properties,omitempty"`
	Columns         []DeltaColumn     `json:"columns"`
	StorageLocation string            `json:"storage_location"`
	Format          DeltaFormat       `json:"format"`
}

// DeltaColumn represents a column definition
type DeltaColumn struct {
	Name              string `json:"name"`
	TypeText          string `json:"type_text"`
	TypeQualifiedName string `json:"type_name,omitempty"`
	Nullable          bool   `json:"nullable"`
	Comment           string `json:"comment,omitempty"`
}

// DeltaFormat represents table format
type DeltaFormat struct {
	Name string `json:"name"` // "delta"
}

// DeltaQueryResult represents query results
type DeltaQueryResult struct {
	ErrorMessage          string
	NextChunkIndex        *int64
	NextChunkInternalLink string
	StatementID           string               `json:"statement_id"`
	Status                DeltaStatementStatus `json:"status"`
	Manifest              DeltaResultManifest  `json:"result"`
}

// DeltaStatementStatus represents statement status
type DeltaStatementStatus struct {
	State string `json:"state"` // "pending", "running", "succeeded", "failed"
}

// DeltaResultManifest represents result manifest
type DeltaResultManifest struct {
	Schema    DeltaResultSchema `json:"schema"`
	Data      [][]interface{}   `json:"data"`
	Truncated bool              `json:"truncated,omitempty"`
}

// DeltaResultSchema represents result schema
type DeltaResultSchema struct {
	Columns []DeltaResultColumn `json:"columns"`
}

// DeltaResultColumn represents a result column
type DeltaResultColumn struct {
	Name     string                 `json:"name"`
	Type     string                 `json:"type"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// DeltaCommitInfo represents a commit in Delta log
type DeltaCommitInfo struct {
	Version             int64                  `json:"version"`
	Timestamp           time.Time              `json:"timestamp"`
	UserId              string                 `json:"user_id"`
	UserName            string                 `json:"user_name"`
	Operation           string                 `json:"operation"`
	OperationParameters map[string]interface{} `json:"operation_parameters,omitempty"`
	Note                string                 `json:"note,omitempty"`
}

// ConvertDeltaTypeToStandardType converts Delta Lake types to standard types
func ConvertDeltaTypeToStandardType(deltaType string) string {
	switch deltaType {
	case "boolean":
		return "BOOLEAN"
	case "tinyint", "smallint", "int":
		return "INTEGER"
	case "bigint":
		return "BIGINT"
	case "float", "double":
		return "DOUBLE"
	case "decimal":
		return "DECIMAL"
	case "date":
		return "DATE"
	case "timestamp":
		return "TIMESTAMP"
	case "string":
		return "VARCHAR"
	case "binary":
		return "VARBINARY"
	case "array":
		return "ARRAY"
	case "map":
		return "MAP"
	case "struct":
		return "STRUCT"
	default:
		return deltaType
	}
}

// ParseDeltaTablePath parses a Delta table path
func ParseDeltaTablePath(path string) (catalog, schema, table string, err error) {
	// Delta paths can be:
	// - catalog.schema.table
	// - s3://bucket/path/to/table
	// - /local/path/to/table

	// Simple implementation for three-part name
	parts := bytes.Split([]byte(path), []byte{'.'})
	if len(parts) == 3 {
		return string(parts[0]), string(parts[1]), string(parts[2]), nil
	}

	return "", "", "", fmt.Errorf("invalid Delta table path: %s", path)
}
