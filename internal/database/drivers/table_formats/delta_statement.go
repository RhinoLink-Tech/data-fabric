package table_formats

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type deltaStatementRequest struct {
	Statement     string `json:"statement"`
	WarehouseID   string `json:"warehouse_id"`
	WaitTimeout   string `json:"wait_timeout"`
	OnWaitTimeout string `json:"on_wait_timeout"`
	Format        string `json:"format"`
	Disposition   string `json:"disposition"`
}

type deltaStatementStatusWire struct {
	State string `json:"state"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type deltaStatementManifestWire struct {
	Schema    DeltaResultSchema `json:"schema"`
	Truncated bool              `json:"truncated"`
}

type deltaStatementResultWire struct {
	DataArray             [][]interface{} `json:"data_array"`
	NextChunkIndex        *int64          `json:"next_chunk_index"`
	NextChunkInternalLink string          `json:"next_chunk_internal_link"`
}

// UnmarshalJSON maps Databricks' separate manifest and result objects onto the
// legacy result type while retaining the opaque chunk continuation fields.
func (result *DeltaQueryResult) UnmarshalJSON(data []byte) error {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if len(envelope["statement_id"]) > 0 {
		if err := json.Unmarshal(envelope["statement_id"], &result.StatementID); err != nil {
			return err
		}
	}
	var status deltaStatementStatusWire
	if len(envelope["status"]) > 0 {
		if err := json.Unmarshal(envelope["status"], &status); err != nil {
			return err
		}
	}
	result.Status.State = status.State
	result.ErrorMessage = ""
	if status.Error != nil {
		result.ErrorMessage = status.Error.Message
	}
	if len(envelope["manifest"]) > 0 {
		var manifest deltaStatementManifestWire
		if err := json.Unmarshal(envelope["manifest"], &manifest); err != nil {
			return err
		}
		result.Manifest.Schema = manifest.Schema
		result.Manifest.Truncated = manifest.Truncated
	}
	if len(envelope["result"]) > 0 {
		var chunk deltaStatementResultWire
		if err := json.Unmarshal(envelope["result"], &chunk); err != nil {
			return err
		}
		result.Manifest.Data = chunk.DataArray
		result.NextChunkIndex = chunk.NextChunkIndex
		result.NextChunkInternalLink = chunk.NextChunkInternalLink
	} else if len(envelope["data_array"]) > 0 {
		var chunk deltaStatementResultWire
		if err := json.Unmarshal(data, &chunk); err != nil {
			return err
		}
		result.Manifest.Data = chunk.DataArray
		result.NextChunkIndex = chunk.NextChunkIndex
		result.NextChunkInternalLink = chunk.NextChunkInternalLink
	}
	return nil
}

// executeStatement uses the public Databricks SQL Statement Execution API,
// follows asynchronous execution, and reads every inline result chunk.
func (c *DeltaRESTClient) executeStatement(ctx context.Context, warehouseID, statement string) (*DeltaQueryResult, error) {
	if warehouseID == "" {
		warehouseID = c.warehouseID
	}
	if warehouseID == "" {
		return nil, fmt.Errorf("Databricks SQL warehouse ID is required")
	}
	if strings.TrimSpace(statement) == "" {
		return nil, fmt.Errorf("SQL statement is required")
	}
	timeout := c.timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	body, err := json.Marshal(deltaStatementRequest{
		Statement: statement, WarehouseID: warehouseID, WaitTimeout: "0s",
		OnWaitTimeout: "CONTINUE", Format: "JSON_ARRAY", Disposition: "INLINE",
	})
	if err != nil {
		return nil, fmt.Errorf("encode Databricks statement request: %w", err)
	}
	result := &DeltaQueryResult{}
	if err := c.requestDeltaJSON(runCtx, http.MethodPost, c.baseURL+"/api/2.0/sql/statements", body, result); err != nil {
		return nil, err
	}
	if result.StatementID == "" && (result.Status.State == "PENDING" || result.Status.State == "RUNNING") {
		return nil, fmt.Errorf("Databricks returned an asynchronous statement without an ID")
	}

	for result.Status.State == "PENDING" || result.Status.State == "RUNNING" {
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-runCtx.Done():
			timer.Stop()
			return nil, runCtx.Err()
		case <-timer.C:
		}
		endpoint := c.baseURL + "/api/2.0/sql/statements/" + url.PathEscape(result.StatementID)
		polled := &DeltaQueryResult{}
		if err := c.requestDeltaJSON(runCtx, http.MethodGet, endpoint, nil, polled); err != nil {
			return nil, err
		}
		result = polled
	}
	if result.Status.State == "FAILED" || result.Status.State == "CANCELED" || result.Status.State == "CLOSED" {
		message := result.ErrorMessage
		if message == "" {
			message = "statement did not succeed"
		}
		return nil, fmt.Errorf("Databricks SQL statement %s: %s", result.Status.State, message)
	}
	if result.Status.State != "SUCCEEDED" {
		return nil, fmt.Errorf("unexpected Databricks SQL statement state %q", result.Status.State)
	}
	if result.Manifest.Truncated {
		return nil, fmt.Errorf("Databricks SQL statement result was truncated")
	}

	rows := append([][]interface{}(nil), result.Manifest.Data...)
	nextIndex := result.NextChunkIndex
	nextLink := result.NextChunkInternalLink
	seenChunks := make(map[string]struct{})
	for nextIndex != nil || nextLink != "" {
		if len(seenChunks) >= 10000 {
			return nil, fmt.Errorf("Databricks statement exceeded the 10000 chunk safety limit")
		}
		key := nextLink
		if nextIndex != nil {
			key = strconv.FormatInt(*nextIndex, 10)
		}
		if _, exists := seenChunks[key]; exists {
			return nil, fmt.Errorf("Databricks returned a repeated result chunk continuation")
		}
		seenChunks[key] = struct{}{}

		endpoint, err := c.deltaChunkURL(result.StatementID, warehouseID, nextIndex, nextLink)
		if err != nil {
			return nil, err
		}
		chunk := &DeltaQueryResult{}
		if err := c.requestDeltaJSON(runCtx, http.MethodGet, endpoint, nil, chunk); err != nil {
			return nil, err
		}
		rows = append(rows, chunk.Manifest.Data...)
		nextIndex = chunk.NextChunkIndex
		nextLink = chunk.NextChunkInternalLink
	}
	result.Manifest.Data = rows
	return result, nil
}

func (c *DeltaRESTClient) requestDeltaJSON(ctx context.Context, method, endpoint string, body []byte, target interface{}) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return fmt.Errorf("create Databricks request: %w", err)
	}
	c.setAuthHeader(req)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("Databricks request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("Databricks request returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if target == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return fmt.Errorf("decode Databricks response: %w", err)
	}
	return nil
}

func (c *DeltaRESTClient) deltaChunkURL(statementID, warehouseID string, index *int64, internalLink string) (string, error) {
	if internalLink != "" {
		base, err := url.Parse(c.baseURL)
		if err != nil {
			return "", fmt.Errorf("invalid Databricks workspace URL: %w", err)
		}
		ref, err := url.Parse(internalLink)
		if err != nil {
			return "", fmt.Errorf("invalid Databricks chunk link: %w", err)
		}
		resolved := base.ResolveReference(ref)
		if resolved.Scheme != base.Scheme || resolved.Host != base.Host {
			return "", fmt.Errorf("Databricks result chunk link points outside the configured workspace")
		}
		return resolved.String(), nil
	}
	if index == nil {
		return "", fmt.Errorf("Databricks result chunk has no next index")
	}
	params := url.Values{}
	params.Set("warehouse_id", warehouseID)
	return c.baseURL + "/api/2.0/sql/statements/" + url.PathEscape(statementID) +
		"/result/chunks/" + strconv.FormatInt(*index, 10) + "?" + params.Encode(), nil
}

func deltaHistoryIdentifier(tablePath string) (string, error) {
	trimmed := strings.TrimSpace(tablePath)
	if trimmed == "" {
		return "", fmt.Errorf("Delta table identifier or path is required")
	}
	quote := string(rune(96))
	if strings.Contains(trimmed, "://") || strings.HasPrefix(trimmed, "/") {
		return "delta." + quote + strings.ReplaceAll(trimmed, quote, quote+quote) + quote, nil
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) > 3 {
		return "", fmt.Errorf("Delta table identifiers may have at most three parts")
	}
	for index, part := range parts {
		if strings.TrimSpace(part) == "" {
			return "", fmt.Errorf("Delta table identifier contains an empty part")
		}
		parts[index] = quote + strings.ReplaceAll(part, quote, quote+quote) + quote
	}
	return strings.Join(parts, "."), nil
}

func (c *DeltaRESTClient) readTableHistory(ctx context.Context, tablePath string) ([]DeltaCommitInfo, error) {
	identifier, err := deltaHistoryIdentifier(tablePath)
	if err != nil {
		return nil, err
	}
	result, err := c.executeStatement(ctx, c.warehouseID, "DESCRIBE HISTORY "+identifier+" LIMIT 1000")
	if err != nil {
		return nil, err
	}

	commits := make([]DeltaCommitInfo, 0, len(result.Manifest.Data))
	for rowIndex, row := range result.Manifest.Data {
		get := func(name string) interface{} {
			for index, column := range result.Manifest.Schema.Columns {
				if index < len(row) && strings.EqualFold(column.Name, name) {
					return row[index]
				}
			}
			return nil
		}
		version, ok := deltaHistoryInt64(get("version"))
		if !ok {
			return nil, fmt.Errorf("Delta history row %d has no valid version", rowIndex)
		}
		timestamp, ok := deltaHistoryTimestamp(get("timestamp"))
		if !ok {
			return nil, fmt.Errorf("Delta history row %d has no valid timestamp", rowIndex)
		}
		commit := DeltaCommitInfo{
			Version: version, Timestamp: timestamp, UserId: fmt.Sprint(get("userId")),
			UserName: fmt.Sprint(get("userName")), Operation: fmt.Sprint(get("operation")),
		}
		if parameters := get("operationParameters"); parameters != nil {
			switch value := parameters.(type) {
			case map[string]interface{}:
				commit.OperationParameters = value
			case string:
				if err := json.Unmarshal([]byte(value), &commit.OperationParameters); err != nil {
					return nil, fmt.Errorf("Delta history row %d operation parameters: %w", rowIndex, err)
				}
			}
		}
		commits = append(commits, commit)
	}
	return commits, nil
}

func deltaHistoryInt64(value interface{}) (int64, bool) {
	switch value := value.(type) {
	case float64:
		return int64(value), true
	case string:
		parsed, err := strconv.ParseInt(value, 10, 64)
		return parsed, err == nil
	case json.Number:
		parsed, err := value.Int64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func deltaHistoryTimestamp(value interface{}) (time.Time, bool) {
	switch value := value.(type) {
	case string:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999"} {
			if parsed, err := time.Parse(layout, value); err == nil {
				return parsed, true
			}
		}
		if millis, err := strconv.ParseInt(value, 10, 64); err == nil {
			return time.UnixMilli(millis), true
		}
	case float64:
		return time.UnixMilli(int64(value)), true
	}
	return time.Time{}, false
}
