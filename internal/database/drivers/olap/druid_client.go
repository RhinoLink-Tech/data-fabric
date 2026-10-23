package olap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DruidRESTClient handles Druid SQL API requests
type DruidRESTClient struct {
	baseURL    string
	httpClient *http.Client
	brokerURL  string
	username   string
	password   string
}

// NewDruidRESTClient creates a new Druid REST client
func NewDruidRESTClient(brokerURL string) *DruidRESTClient {
	normalizedURL := strings.TrimRight(strings.TrimSpace(brokerURL), "/")
	return &DruidRESTClient{
		baseURL:   normalizedURL,
		brokerURL: normalizedURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetBasicAuth configures optional HTTP basic authentication. Druid instances
// without authentication continue to send no Authorization header.
func (c *DruidRESTClient) SetBasicAuth(username, password string) {
	c.username = strings.TrimSpace(username)
	c.password = password
}

// ExecuteSQLQuery executes a SQL query via Druid SQL API
func (c *DruidRESTClient) ExecuteSQLQuery(ctx context.Context, sql string) (*DruidQueryResult, error) {
	return c.ExecuteSQLQueryWithParameters(ctx, sql, nil)
}

// ExecuteSQLQueryWithParameters executes a SQL query with Druid SQL dynamic
// parameters and asks for array rows with column and SQL type headers. Array
// output prevents map iteration from reordering columns in the gateway API.
func (c *DruidRESTClient) ExecuteSQLQueryWithParameters(ctx context.Context, sql string, parameters []DruidQueryParameter) (*DruidQueryResult, error) {
	endpoint := fmt.Sprintf("%s/druid/v2/sql", c.baseURL)

	request := DruidSQLRequest{
		Query:          sql,
		ResultFormat:   "array",
		Header:         true,
		SQLTypesHeader: true,
		Parameters:     parameters,
	}

	reqBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := c.newRequest(ctx, http.MethodPost, endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("query failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	result, err := parseDruidSQLRows(body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Druid SQL response: %w", err)
	}
	result.QueryID = resp.Header.Get("X-Druid-SQL-Query-Id")
	return result, nil
}

// DruidSQLRequest represents Druid SQL request
type DruidSQLRequest struct {
	Query          string                 `json:"query"`
	ResultFormat   string                 `json:"resultFormat,omitempty"`
	Header         bool                   `json:"header,omitempty"`
	SQLTypesHeader bool                   `json:"sqlTypesHeader,omitempty"`
	Parameters     []DruidQueryParameter  `json:"parameters,omitempty"`
	Context        map[string]interface{} `json:"context,omitempty"`
}

// DruidQueryParameter represents query parameter
type DruidQueryParameter struct {
	Type  string      `json:"type"`
	Value interface{} `json:"value"`
}

// DruidQueryResult represents query result
type DruidQueryResult struct {
	Rows        []map[string]interface{}
	Values      [][]interface{}
	Columns     []string
	ColumnTypes []string
	RowCount    int
	QueryID     string
	ElapsedMS   int64
}

func parseDruidSQLRows(body []byte) (*DruidQueryResult, error) {
	var resultRows [][]json.RawMessage
	if err := json.Unmarshal(body, &resultRows); err != nil {
		return nil, err
	}
	if len(resultRows) == 0 {
		return &DruidQueryResult{Rows: []map[string]interface{}{}, Values: [][]interface{}{}, Columns: []string{}, ColumnTypes: []string{}}, nil
	}
	if len(resultRows) < 2 {
		return nil, fmt.Errorf("Druid response is missing the SQL type header")
	}

	columns := make([]string, len(resultRows[0]))
	for index, rawColumn := range resultRows[0] {
		if err := json.Unmarshal(rawColumn, &columns[index]); err != nil || strings.TrimSpace(columns[index]) == "" {
			return nil, fmt.Errorf("invalid Druid column header at index %d", index)
		}
	}
	if len(resultRows[1]) != len(columns) {
		return nil, fmt.Errorf("Druid SQL type header has %d columns, want %d", len(resultRows[1]), len(columns))
	}
	types := make([]string, len(columns))
	for index, rawType := range resultRows[1] {
		types[index] = druidSQLTypeName(rawType)
	}

	rows := make([]map[string]interface{}, 0, len(resultRows)-2)
	values := make([][]interface{}, 0, len(resultRows)-2)
	for rowIndex, rawRow := range resultRows[2:] {
		if len(rawRow) != len(columns) {
			return nil, fmt.Errorf("Druid data row %d has %d columns, want %d", rowIndex, len(rawRow), len(columns))
		}
		row := make(map[string]interface{}, len(columns))
		rowValues := make([]interface{}, len(columns))
		for columnIndex, rawValue := range rawRow {
			value, err := druidValueFromJSON(rawValue)
			if err != nil {
				return nil, fmt.Errorf("Druid data row %d column %q: %w", rowIndex, columns[columnIndex], err)
			}
			row[columns[columnIndex]] = value
			rowValues[columnIndex] = value
		}
		rows = append(rows, row)
		values = append(values, rowValues)
	}
	return &DruidQueryResult{Rows: rows, Values: values, Columns: columns, ColumnTypes: types, RowCount: len(rows)}, nil
}

// GetDatasources retrieves list of datasources
func (c *DruidRESTClient) GetDatasources(ctx context.Context) ([]string, error) {
	endpoint := fmt.Sprintf("%s/druid/coordinator/v1/datasources", c.baseURL)

	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("datasource request failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var datasources []string
	if err := json.Unmarshal(body, &datasources); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return datasources, nil
}

// GetDatasourceMetadata retrieves metadata for a datasource
func (c *DruidRESTClient) GetDatasourceMetadata(ctx context.Context, datasource string) (*DruidDatasourceMetadata, error) {
	endpoint := fmt.Sprintf("%s/druid/coordinator/v1/datasources/%s", c.baseURL, url.PathEscape(datasource))

	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("datasource metadata request failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var metadata DruidDatasourceMetadata
	if err := json.Unmarshal(body, &metadata); nil != err {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &metadata, nil
}

// DruidDatasourceMetadata represents datasource metadata
type DruidDatasourceMetadata struct {
	Name       string                 `json:"name"`
	Segments   int                    `json:"segments"`
	Columns    []string               `json:"columns"`
	Properties map[string]interface{} `json:"properties"`
}

// GetClusterStatus retrieves cluster status
func (c *DruidRESTClient) GetClusterStatus(ctx context.Context) (*DruidClusterStatus, error) {
	endpoint := fmt.Sprintf("%s/status", c.baseURL)

	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status request failed with status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	var status DruidClusterStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &status, nil
}

// DruidClusterStatus represents cluster status
type DruidClusterStatus struct {
	Version     string `json:"version"`
	Coordinator bool   `json:"coordinator"`
	Overlord    bool   `json:"overlord"`
	Broker      bool   `json:"broker"`
}

// CancelQuery cancels a running query
func (c *DruidRESTClient) CancelQuery(ctx context.Context, queryID string) error {
	if strings.TrimSpace(queryID) == "" {
		return fmt.Errorf("Druid query ID is required")
	}
	endpoint := fmt.Sprintf("%s/druid/v2/sql/%s", c.baseURL, url.PathEscape(queryID))

	req, err := c.newRequest(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("failed to cancel query with status %d", resp.StatusCode)
	}

	return nil
}

func (c *DruidRESTClient) newRequest(ctx context.Context, method, endpoint string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
	return req, nil
}
