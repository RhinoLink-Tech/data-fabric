package acceptance

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
)

func acceptanceProfileForRun(env *reference.Environment) map[string]any {
	profile := acceptanceProfile()
	topology := "single"
	restOwners, mcpOwners := 1, 1
	if env.RunInfo.MultiReplica {
		topology = "two-replica"
		restOwners, mcpOwners = 2, 2
	}
	profile["topology"] = topology
	profile["rest_owner_replicas"] = restOwners
	profile["mcp_owner_replicas"] = mcpOwners
	profile["query_session_ttl_seconds"] = env.RunInfo.SessionTTLSeconds
	profile["tenant_query_capacity"] = env.RunInfo.TenantConcurrency
	profile["candidate_sha"] = env.RunInfo.SourceRevision
	profile["working_tree_dirty"] = env.RunInfo.WorkingTreeDirty
	profile["gateway_image"] = env.RunInfo.GatewayImage
	profile["gateway_image_id"] = env.RunInfo.GatewayImageID
	profile["mysql_image"] = env.RunInfo.MySQLImage
	profile["postgresql_image"] = env.RunInfo.PostgreSQLImage
	return profile
}

func logicalQueryMetricSnapshot(ctx context.Context, w wire) (map[string]float64, error) {
	var metricBodies []string
	if w.env.RunInfo.MultiReplica {
		slots := []string{"rest-a", "rest-b"}
		if w.kind == mcpHTTP {
			slots = []string{"mcp-a", "mcp-b"}
		}
		for _, slot := range slots {
			body, err := ownerMetrics(ctx, w.env, slot)
			if err != nil {
				return nil, err
			}
			metricBodies = append(metricBodies, body)
		}
	} else {
		endpoint := w.env.APIURL
		token := w.env.Identities["operator"].RESTToken
		if w.kind == mcpHTTP {
			endpoint = strings.TrimSuffix(w.env.MCPURL, "/mcp")
			token = w.env.Identities["operator"].MCPToken
		}
		response, err := w.request(ctx, http.MethodGet, endpoint+"/metrics", token, "", nil)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s metrics returned HTTP %d", w.kind, response.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse))
		if err != nil {
			return nil, fmt.Errorf("%s metrics response could not be read", w.kind)
		}
		metricBodies = append(metricBodies, string(body))
	}

	values := make(map[string]float64)
	for _, outcome := range []string{"success", "timeout", "budget_duration", "budget_result", "canceled", "session_expired", "owner_unavailable"} {
		values["outcomes/"+outcome] = sumLogicalMetric(metricBodies, "data_fabric_logical_query_outcomes_total", outcome)
		values["rows/"+outcome] = sumLogicalMetric(metricBodies, "data_fabric_logical_query_rows_returned_total", outcome)
		values["batches/"+outcome] = sumLogicalMetric(metricBodies, "data_fabric_logical_query_batches_total", outcome)
	}
	return values, nil
}

func sumLogicalMetric(bodies []string, name, outcome string) float64 {
	var total float64
	needle := "outcome=\"" + outcome + "\""
	for _, body := range bodies {
		for _, line := range strings.Split(body, "\n") {
			if !strings.HasPrefix(line, name+"{") || !strings.Contains(line, needle) {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			value, err := strconv.ParseFloat(fields[1], 64)
			if err == nil {
				total += value
			}
		}
	}
	return total
}

func assertLogicalMetricDelta(before, after map[string]float64, outcome string, wantQueries, wantRows, wantBatches int) error {
	checks := []struct {
		key  string
		want float64
	}{
		{"outcomes/" + outcome, float64(wantQueries)},
		{"rows/" + outcome, float64(wantRows)},
		{"batches/" + outcome, float64(wantBatches)},
	}
	for _, check := range checks {
		if delta := after[check.key] - before[check.key]; delta != check.want {
			return fmt.Errorf("logical-query %s delta = %s, want %s", check.key, strconv.FormatFloat(delta, 'f', -1, 64), strconv.FormatFloat(check.want, 'f', -1, 64))
		}
	}
	return nil
}

func logicalQueryMetrics(ctx context.Context, w wire) error {
	return forEachSource(w, func(_ string, source reference.Source) error {
		before, err := logicalQueryMetricSnapshot(ctx, w)
		if err != nil {
			return err
		}
		first, err := readQuery(ctx, w, source, orderedIDs, nil, 1)
		if err != nil {
			return err
		}
		if err := expectPage(first, "", "1", 3, true); err != nil {
			return err
		}
		current := first
		for row := 2; row <= 3; row++ {
			next, err := readBatch(w.next(ctx, reader, current))
			if err != nil {
				return err
			}
			if err := expectPage(next, first.QueryID, strconv.Itoa(row), 3, row < 3); err != nil {
				return err
			}
			current = next
		}
		after, err := logicalQueryMetricSnapshot(ctx, w)
		if err != nil {
			return err
		}
		return assertLogicalMetricDelta(before, after, "success", 1, 3, 3)
	})
}

func assertOwnerLogicalOutcomeDelta(before, after string, outcome string, want int) error {
	prior := sumLogicalMetric([]string{before}, "data_fabric_logical_query_outcomes_total", outcome)
	current := sumLogicalMetric([]string{after}, "data_fabric_logical_query_outcomes_total", outcome)
	if current-prior != float64(want) {
		return fmt.Errorf("owner logical-query %s outcome delta = %s, want %d", outcome, strconv.FormatFloat(current-prior, 'f', -1, 64), want)
	}
	return nil
}
