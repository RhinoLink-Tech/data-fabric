package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
)

const (
	trinoCrossCatalogSQL = "SELECT n.nationkey, n.name AS nation_name, r.name AS region_name " +
		"FROM tpch.tiny.nation AS n " +
		"JOIN tpch_copy.tiny.region AS r ON n.regionkey = r.regionkey " +
		"WHERE n.nationkey < 3 " +
		"ORDER BY n.nationkey"
	trinoCoordinatorFailureSQL = "SELECT nationkey FROM tpch.tiny.nation WHERE nationkey = 0"
	trinoCancellationSQL       = "SELECT count(orderkey) FROM tpch.sf100.lineitem"
	trinoEngineFailureSQL      = "SELECT trino_reference_missing_function(name) FROM tpch.tiny.nation LIMIT 1"
)

type trinoScenario struct {
	name  string
	check func(context.Context, wire, reference.Source) error
}

type trinoEvidence struct {
	Case       string  `json:"case"`
	Transport  surface `json:"transport"`
	Result     string  `json:"result"`
	DurationMS int64   `json:"duration_ms"`
	Reason     string  `json:"reason,omitempty"`
}

// CheckTrino exercises the registered Trino adapter through the public REST and
// MCP surfaces. It runs only from the dedicated opt-in reference command.
func CheckTrino(ctx context.Context, env *reference.Environment, out io.Writer) error {
	source, ok := env.Sources["trino"]
	if !ok || source.ID == "" || env.TrinoCoordinatorURL == "" {
		return fmt.Errorf("the Trino integration environment is incomplete")
	}
	scenarios := []trinoScenario{
		{name: "T01-cross-catalog-read", check: checkTrinoCrossCatalog},
		{name: "T02-coordinator-outage", check: checkTrinoCoordinatorFailure},
		{name: "T03-query-timeout-cancels", check: checkTrinoCancellation},
		{name: "T04-engine-error-sanitized", check: checkTrinoEngineFailure},
	}
	encoder := json.NewEncoder(out)
	var failures []error
	for _, kind := range []surface{rest, mcpHTTP} {
		httpClient := newHTTP(env)
		w := (wire{kind: kind, env: env, http: httpClient}).withMCPState()
		for _, scenario := range scenarios {
			if err := ctx.Err(); err != nil {
				failures = append(failures, err)
				break
			}
			record := trinoEvidence{Case: scenario.name, Transport: kind, Result: "pass"}
			started := time.Now()
			if err := scenario.check(ctx, w, source); err != nil {
				record.Result, record.Reason = "fail", err.Error()
				failures = append(failures, fmt.Errorf("%s %s: %w", scenario.name, kind, err))
			}
			record.DurationMS = time.Since(started).Milliseconds()
			if err := encoder.Encode(record); err != nil {
				failures = append(failures, fmt.Errorf("write Trino acceptance evidence: %w", err))
				break
			}
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		failures = append(failures, w.releaseMCPSessions(cleanupCtx))
		cancel()
		httpClient.CloseIdleConnections()
	}
	return errors.Join(failures...)
}

func checkTrinoCrossCatalog(ctx context.Context, w wire, source reference.Source) error {
	result, err := w.query(ctx, reader, "", source, trinoCrossCatalogSQL, nil, 10, 20)
	if err != nil {
		return err
	}
	var actual batch
	if err := result.decode(&actual); err != nil {
		return err
	}
	columns := []string{"nationkey", "nation_name", "region_name"}
	if len(actual.Columns) != len(columns) {
		return fmt.Errorf("cross-catalog query returned %d columns, want %d", len(actual.Columns), len(columns))
	}
	for index, name := range columns {
		if !strings.EqualFold(actual.Columns[index].Name, name) {
			return fmt.Errorf("cross-catalog column %d is %q, want %q", index, actual.Columns[index].Name, name)
		}
	}
	expected := [][]string{
		{"0", "ALGERIA", "AFRICA"},
		{"1", "ARGENTINA", "AMERICA"},
		{"2", "BRAZIL", "AMERICA"},
	}
	if len(actual.Entries) != len(expected) {
		return fmt.Errorf("cross-catalog query returned %d rows, want %d", len(actual.Entries), len(expected))
	}
	for rowIndex, want := range expected {
		if len(actual.Entries[rowIndex]) != len(want) {
			return fmt.Errorf("cross-catalog row %d has %d values, want %d", rowIndex, len(actual.Entries[rowIndex]), len(want))
		}
		for columnIndex, value := range want {
			if fmt.Sprint(actual.Entries[rowIndex][columnIndex]) != value {
				return fmt.Errorf("cross-catalog row %d column %d did not match expected data", rowIndex, columnIndex)
			}
		}
	}
	return nil
}

func checkTrinoEngineFailure(ctx context.Context, w wire, source reference.Source) error {
	result, err := w.query(ctx, reader, "", source, trinoEngineFailureSQL, nil, 10, 10)
	if err != nil {
		return err
	}
	return result.expectError(w.kind, http.StatusBadGateway, "QUERY_ENGINE_ERROR", sourceFailure)
}

func checkTrinoCoordinatorFailure(ctx context.Context, w wire, source reference.Source) (err error) {
	if err := w.env.SetSourceAvailable(ctx, "trino", false); err != nil {
		return fmt.Errorf("stop pinned Trino coordinator: %w", err)
	}
	result, queryErr := w.query(ctx, reader, "", source, trinoCoordinatorFailureSQL, nil, 10, 5)
	if queryErr != nil {
		err = queryErr
	} else if result.code == "QUERY_TIMEOUT" || result.message == queryTimeout {
		err = result.expectError(w.kind, http.StatusGatewayTimeout, "QUERY_TIMEOUT", queryTimeout)
	} else {
		err = result.expectError(w.kind, http.StatusBadGateway, "QUERY_ENGINE_ERROR", sourceFailure)
	}
	restore, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	if restoreErr := w.env.SetSourceAvailable(restore, "trino", true); restoreErr != nil {
		return errors.Join(err, fmt.Errorf("restart pinned Trino coordinator: %w", restoreErr))
	}
	return errors.Join(err, checkTrinoCrossCatalog(restore, w, source))
}

func checkTrinoCancellation(ctx context.Context, w wire, source reference.Source) error {
	// Keep query identities distinct because Trino retains terminal history.
	marker := strings.ReplaceAll(string(w.kind), "-", "_")
	querySQL := strings.Replace(trinoCancellationSQL, "count(orderkey)", "count(orderkey) AS df_cancel_"+marker, 1)
	type queryOutcome struct {
		result reply
		err    error
	}
	queryCtx, cancelQuery := context.WithTimeout(ctx, 15*time.Second)
	defer cancelQuery()
	outcome := make(chan queryOutcome, 1)
	go func() {
		result, err := w.query(queryCtx, reader, "", source, querySQL, nil, 1, 1)
		outcome <- queryOutcome{result: result, err: err}
	}()

	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var completed *queryOutcome
	observedActive := false
	observedTerminal := false
	for {
		if completed != nil && observedActive && observedTerminal {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if completed == nil {
				return fmt.Errorf("query did not return its configured one-second timeout")
			}
			if !observedActive {
				return fmt.Errorf("Trino coordinator never exposed the timed query as active")
			}
			return fmt.Errorf("Trino coordinator did not reach a terminal state after gateway cancellation")
		case result := <-outcome:
			if completed != nil {
				continue
			}
			if result.err != nil {
				return result.err
			}
			if err := result.result.expectError(w.kind, http.StatusGatewayTimeout, "QUERY_TIMEOUT", queryTimeout); err != nil {
				return err
			}
			completed = &result
		case <-ticker.C:
			seen, active, terminal, err := trinoQueryState(ctx, w.env, querySQL)
			if err != nil {
				return err
			}
			observedActive = observedActive || (seen && active)
			observedTerminal = observedTerminal || (seen && terminal)
		}
	}
}

func trinoQueryState(ctx context.Context, env *reference.Environment, sql string) (seen, active, terminal bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, env.TrinoCoordinatorURL+"/v1/query", nil)
	if err != nil {
		return false, false, false, fmt.Errorf("construct Trino query-state request")
	}
	req.Header.Set("X-Trino-User", "reference-reader")
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, false, false, fmt.Errorf("read Trino coordinator query state: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, false, false, fmt.Errorf("Trino coordinator query-state endpoint returned HTTP %d", resp.StatusCode)
	}
	var queries []struct {
		Query string `json:"query"`
		State string `json:"state"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&queries); err != nil {
		return false, false, false, fmt.Errorf("decode Trino coordinator query state")
	}
	want := strings.Join(strings.Fields(sql), " ")
	for _, query := range queries {
		actual := strings.Join(strings.Fields(query.Query), " ")
		if !strings.Contains(strings.ToLower(actual), strings.ToLower(want)) {
			continue
		}
		seen = true
		switch strings.ToUpper(query.State) {
		case "QUEUED", "WAITING_FOR_RESOURCES", "PLANNING", "STARTING", "RUNNING", "FINISHING":
			active = true
		case "FINISHED", "FAILED", "CANCELED", "CANCELLED":
			terminal = true
		default:
			return false, false, false, fmt.Errorf("Trino coordinator reported an unknown query state")
		}
	}
	return seen, active, terminal, nil
}
