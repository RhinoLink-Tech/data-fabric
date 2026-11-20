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
	trinoFederatedSQL = "SELECT o.id AS order_id, i.id AS item_id, i.item " +
		"FROM mysql.sample.orders AS o JOIN postgresql.public.order_items AS i ON o.id = i.order_id " +
		"ORDER BY o.id, i.id"
	trinoCoordinatorFailureSQL = "SELECT nationkey FROM tpch.tiny.nation WHERE nationkey = 0"
	trinoCancellationSQL       = "SELECT count(orderkey) FROM tpch.sf100.lineitem"
	trinoEngineFailureSQL      = "SELECT trino_reference_missing_function(name) FROM tpch.tiny.nation LIMIT 1"
)

type trinoScenario struct {
	name  string
	check func(context.Context, wire, reference.Source) error
}

type trinoEvidence struct {
	Case             string  `json:"case"`
	Transport        surface `json:"transport"`
	Result           string  `json:"result"`
	DurationMS       int64   `json:"duration_ms"`
	Reason           string  `json:"reason,omitempty"`
	CandidateSHA     string  `json:"candidate_sha"`
	WorkingTreeDirty bool    `json:"working_tree_dirty"`
	GatewayImage     string  `json:"gateway_image"`
	GatewayImageID   string  `json:"gateway_image_id"`
	MySQLImage       string  `json:"mysql_image"`
	PostgreSQLImage  string  `json:"postgresql_image"`
	TrinoImage       string  `json:"trino_image"`
}

// CheckTrino exercises the registered Trino adapter through the public REST and
// MCP surfaces. It runs only from the dedicated opt-in reference command.
func CheckTrino(ctx context.Context, env *reference.Environment, out io.Writer) error {
	return runTrinoScenarios(ctx, env, out, []trinoScenario{
		{name: "T01-cross-catalog-read", check: checkTrinoCrossCatalog},
		{name: "T05-mysql-postgresql-join", check: checkTrinoFederatedSources},
		{name: "T06-federated-column-denied", check: checkTrinoFederatedDenied},
		{name: "T07-federated-no-count", check: checkTrinoFederatedNoCount},
		{name: "T02-coordinator-outage", check: checkTrinoCoordinatorFailure},
		{name: "T03-query-timeout-cancels", check: checkTrinoCancellation},
		{name: "T04-engine-error-sanitized", check: checkTrinoEngineFailure},
	})
}

// CheckFederated runs the real MySQL-to-PostgreSQL scenario on both surfaces.
func CheckFederated(ctx context.Context, env *reference.Environment, out io.Writer) error {
	return runTrinoScenarios(ctx, env, out, []trinoScenario{
		{name: "T05-mysql-postgresql-join", check: checkTrinoFederatedSources},
		{name: "T06-federated-column-denied", check: checkTrinoFederatedDenied},
		{name: "T07-federated-no-count", check: checkTrinoFederatedNoCount},
	})
}

func runTrinoScenarios(ctx context.Context, env *reference.Environment, out io.Writer, scenarios []trinoScenario) error {
	source, ok := env.Sources["trino"]
	if !ok || source.ID == "" || env.TrinoCoordinatorURL == "" {
		return fmt.Errorf("the Trino integration environment is incomplete")
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
			record := trinoEvidence{Case: scenario.name, Transport: kind, Result: "pass",
				CandidateSHA: env.RunInfo.SourceRevision, WorkingTreeDirty: env.RunInfo.WorkingTreeDirty,
				GatewayImage: env.RunInfo.GatewayImage, GatewayImageID: env.RunInfo.GatewayImageID,
				MySQLImage: env.RunInfo.MySQLImage, PostgreSQLImage: env.RunInfo.PostgreSQLImage, TrinoImage: env.RunInfo.TrinoImage}
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

// checkTrinoFederatedSources follows bounded one-row pages so both transports
// exercise continuation of one query joining two physically distinct databases.
func checkTrinoFederatedSources(ctx context.Context, w wire, source reference.Source) error {
	page, err := readBatch(w.query(ctx, reader, "", source, trinoFederatedSQL, nil, 1, 30))
	if err != nil {
		return err
	}
	expected := [][]string{{"1", "1", "widget"}, {"1", "2", "cable"}, {"2", "3", "widget"}}
	queryID := page.QueryID
	for index, want := range expected {
		if page.QueryID == "" || page.QueryID != queryID || len(page.Entries) != 1 || len(page.Entries[0]) != len(want) {
			return fmt.Errorf("unexpected federated page %d", index+1)
		}
		for col, value := range want {
			if fmt.Sprint(page.Entries[0][col]) != value {
				return fmt.Errorf("unexpected federated row %d column %d", index+1, col+1)
			}
		}
		if index < len(expected)-1 {
			if page.NextURI == "" {
				return fmt.Errorf("missing federated continuation")
			}
			page, err = readBatch(w.next(ctx, reader, page))
			if err != nil {
				return err
			}
		}
	}
	if page.NextURI != "" {
		return fmt.Errorf("unexpected extra federated page")
	}
	return nil
}

// The raw total field is captured separately: decoding null into an int would
// otherwise incorrectly turn an unknown count into zero.
type trinoNoCountBatch struct {
	batch
	CountMode string          `json:"countMode"`
	HasMore   *bool           `json:"hasMore"`
	Total     json.RawMessage `json:"totalCount"`
}

func readTrinoNoCount(result reply, err error) (trinoNoCountBatch, error) {
	var page trinoNoCountBatch
	if err != nil {
		return page, err
	}
	if err := result.decode(&page); err != nil {
		return page, err
	}
	if page.CountMode != "none" || page.HasMore == nil || string(page.Total) != "null" || *page.HasMore != (page.NextURI != "") {
		return page, fmt.Errorf("invalid no-count completion or total")
	}
	return page, nil
}

// Test true MySQL-to-PostgreSQL joins across both REST/MCP transports and
// pagination/streaming. Cover empty, exact-full, multi-page and SQL LIMIT.
func checkTrinoFederatedNoCount(ctx context.Context, w wire, source reference.Source) error {
	expected := [][]string{{"1", "1", "widget"}, {"1", "2", "cable"}, {"2", "3", "widget"}}
	for _, mode := range []string{"pagination", "streaming"} {
		for _, tc := range []struct {
			name, sql  string
			size       int
			expected   [][]string
			parameters []any
		}{
			{"multi_page", trinoFederatedSQL, 1, expected, nil},
			{"exact_full", trinoFederatedSQL, 3, expected, nil},
			{"sql_limit", trinoFederatedSQL + " LIMIT 2", 1, expected[:2], nil},
			{"sql_limit_offset", trinoFederatedSQL + " OFFSET 1 LIMIT 2", 1, expected[1:], nil},
			{"empty", "SELECT o.id AS order_id, i.id AS item_id, i.item FROM mysql.sample.orders AS o JOIN postgresql.public.order_items AS i ON o.id = i.order_id WHERE o.id < 0 ORDER BY o.id, i.id", 1, nil, nil},
			{"bound_parameter", "SELECT o.id AS order_id, i.id AS item_id, i.item FROM mysql.sample.orders AS o JOIN postgresql.public.order_items AS i ON o.id = i.order_id WHERE o.id >= ? ORDER BY o.id, i.id", 1, expected[2:], []any{2}},
		} {
			page, err := readTrinoNoCount(w.queryNoCountWithMode(ctx, reader, source, tc.sql, tc.parameters, tc.size, mode))
			if err != nil {
				return fmt.Errorf("%s %s initial: %w", mode, tc.name, err)
			}
			if page.ExecutionMode != mode {
				return fmt.Errorf("%s %s used %s", mode, tc.name, page.ExecutionMode)
			}
			seen, pages := 0, 0
			for {
				pages++
				for _, row := range page.Entries {
					if seen >= len(tc.expected) || len(row) != len(tc.expected[seen]) {
						return fmt.Errorf("%s %s unexpected row %d", mode, tc.name, seen)
					}
					for col, value := range tc.expected[seen] {
						if fmt.Sprint(row[col]) != value {
							return fmt.Errorf("%s %s row %d column %d mismatch", mode, tc.name, seen, col)
						}
					}
					seen++
				}
				if !*page.HasMore {
					break
				}
				if pages > len(tc.expected)+1 {
					return fmt.Errorf("%s %s continuation did not end", mode, tc.name)
				}
				page, err = readTrinoNoCount(w.next(ctx, reader, page.batch))
				if err != nil {
					return fmt.Errorf("%s %s continuation: %w", mode, tc.name, err)
				}
			}
			if seen != len(tc.expected) {
				return fmt.Errorf("%s %s returned %d rows, want %d", mode, tc.name, seen, len(tc.expected))
			}
		}
		denied := "SELECT o.customer FROM mysql.sample.orders AS o JOIN postgresql.public.order_items AS i ON o.id = i.order_id"
		result, err := w.queryNoCountWithMode(ctx, reader, source, denied, nil, 1, mode)
		if err != nil {
			return err
		}
		if err := result.expectError(w.kind, http.StatusForbidden, "TABLE_ACCESS_DENIED", tableDenied); err != nil {
			return fmt.Errorf("%s restricted column: %w", mode, err)
		}
	}
	return nil
}

func checkTrinoFederatedDenied(ctx context.Context, w wire, source reference.Source) error {
	return deniedQuery(ctx, w, reader, source,
		"SELECT o.customer FROM mysql.sample.orders AS o JOIN postgresql.public.order_items AS i ON o.id = i.order_id")
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
