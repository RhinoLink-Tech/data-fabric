package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
)

type ownerStatus struct {
	OwnerSlot       string     `json:"ownerSlot"`
	BootID          string     `json:"bootId"`
	PolicyRevision  string     `json:"policyRevision"`
	State           string     `json:"state"`
	DrainDeadline   *time.Time `json:"drainDeadline"`
	PendingSessions int        `json:"pendingSessions"`
	InFlight        int        `json:"inFlight"`
	CleanupComplete bool       `json:"cleanupComplete"`
}

func acceptanceProfile() map[string]any {
	goVersion, mcpClientVersion := "unknown", "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		goVersion = info.GoVersion
		for _, dependency := range info.Deps {
			if dependency.Path != "github.com/mark3labs/mcp-go" {
				continue
			}
			mcpClientVersion = dependency.Version
			if dependency.Replace != nil && dependency.Replace.Version != "" {
				mcpClientVersion = dependency.Replace.Version
			}
			break
		}
	}
	return map[string]any{
		"go_version": goVersion,
		"mcp_client_module": map[string]string{
			"path": "github.com/mark3labs/mcp-go", "version": mcpClientVersion,
		},
		"rest_owner_replicas":            2,
		"mcp_owner_replicas":             2,
		"query_session_ttl_seconds":      SessionTTLSeconds,
		"tenant_query_capacity":          TenantCapacity,
		"routed_mcp_session_ttl_seconds": reference.ReferenceRoutedMCPSessionTTLSeconds,
		"max_routed_mcp_sessions":        reference.ReferenceMaxRoutedMCPSessions,
		"owner_drain_timeout_seconds":    reference.ReferenceOwnerDrainTimeoutSeconds,
	}
}

func checkOwnerTopology(ctx context.Context, env *reference.Environment, out io.Writer) error {
	owners := make(map[string]ownerStatus, 4)
	for _, slot := range []string{"rest-a", "rest-b", "mcp-a", "mcp-b"} {
		state, err := readOwnerStatus(ctx, env, slot)
		if err != nil {
			return err
		}
		if state.State != "ready" {
			return fmt.Errorf("%s owner did not start ready and idle", slot)
		}
		owners[slot] = state
		if _, err := ownerMetrics(ctx, env, slot); err != nil {
			return err
		}
		status, _, _, err := ownerRequest(ctx, env, slot, "app-a", http.MethodGet, "/internal/replica")
		if err != nil || status != http.StatusForbidden {
			return fmt.Errorf("%s owner status accepted a non-admin principal", slot)
		}
	}
	return json.NewEncoder(out).Encode(map[string]any{
		"case": "topology", "result": "pass", "profile": acceptanceProfile(), "owners": owners,
	})
}

func ownerToken(env *reference.Environment, slot, identity string) (string, error) {
	id, ok := env.Identities[identity]
	if !ok {
		return "", fmt.Errorf("unknown owner admin identity")
	}
	if strings.HasPrefix(slot, "mcp-") {
		if id.MCPToken == "" {
			return "", fmt.Errorf("owner identity has no MCP token")
		}
		return id.MCPToken, nil
	}
	if id.RESTToken == "" {
		return "", fmt.Errorf("owner identity has no REST token")
	}
	return id.RESTToken, nil
}

func ownerRequest(ctx context.Context, env *reference.Environment, slot, identity, method, path string) (int, http.Header, []byte, error) {
	base, ok := env.OwnerURLs[slot]
	if !ok {
		return 0, nil, nil, fmt.Errorf("unknown loopback owner slot")
	}
	token, err := ownerToken(env, slot, identity)
	if err != nil {
		return 0, nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, nil)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("construct owner admin request")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json, text/plain")
	client := newHTTP(env)
	defer client.CloseIdleConnections()
	res, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("send owner admin request: %T", err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponse))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("read owner admin response")
	}
	return res.StatusCode, res.Header.Clone(), body, nil
}

func readOwnerStatus(ctx context.Context, env *reference.Environment, slot string) (ownerStatus, error) {
	status, _, body, err := ownerRequest(ctx, env, slot, "operator", http.MethodGet, "/internal/replica")
	if err != nil {
		return ownerStatus{}, err
	}
	if status != http.StatusOK {
		return ownerStatus{}, fmt.Errorf("%s owner status returned HTTP %d", slot, status)
	}
	var state ownerStatus
	if err := json.Unmarshal(body, &state); err != nil {
		return ownerStatus{}, fmt.Errorf("%s owner status is invalid", slot)
	}
	if state.OwnerSlot != slot || state.BootID == "" || state.PolicyRevision == "" || state.State == "" {
		return ownerStatus{}, fmt.Errorf("%s owner status omitted identity or lifecycle fields", slot)
	}
	return state, nil
}

func ownerMetrics(ctx context.Context, env *reference.Environment, slot string) (string, error) {
	status, _, body, err := ownerRequest(ctx, env, slot, "operator", http.MethodGet, "/metrics")
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("%s metrics returned HTTP %d", slot, status)
	}
	return string(body), nil
}

type ownerEvidenceSnapshot struct {
	states  map[string]ownerStatus
	queries map[string]float64
	http    map[string]map[string]float64
}

func captureOwnerEvidence(ctx context.Context, env *reference.Environment) (ownerEvidenceSnapshot, error) {
	snapshot := ownerEvidenceSnapshot{
		states:  make(map[string]ownerStatus, 4),
		queries: make(map[string]float64, 4),
		http:    make(map[string]map[string]float64, 4),
	}
	for _, slot := range []string{"rest-a", "rest-b", "mcp-a", "mcp-b"} {
		state, err := readOwnerStatus(ctx, env, slot)
		if err != nil {
			return ownerEvidenceSnapshot{}, err
		}
		metrics, err := ownerMetrics(ctx, env, slot)
		if err != nil {
			return ownerEvidenceSnapshot{}, err
		}
		snapshot.states[slot] = state
		snapshot.queries[slot] = metricValue(metrics, "data_fabric_query_total", "")
		snapshot.http[slot] = httpRequestValues(metrics)
	}
	return snapshot, nil
}

func httpRequestValues(metrics string) map[string]float64 {
	values := make(map[string]float64)
	const name = "data_fabric_http_requests_total"
	for _, line := range strings.Split(metrics, "\n") {
		if !strings.HasPrefix(line, name+"{") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		var value float64
		if _, err := fmt.Sscan(fields[1], &value); err == nil {
			values[fields[0]] = value
		}
	}
	return values
}

func (before ownerEvidenceSnapshot) results(after ownerEvidenceSnapshot) (map[string]ownerStatus, map[string]float64, map[string]int, map[string]map[string]float64, map[string]bool) {
	queries := make(map[string]float64, 4)
	sessions := make(map[string]int, 4)
	http := make(map[string]map[string]float64, 4)
	resets := make(map[string]bool, 4)
	for slot, state := range after.states {
		prior := before.states[slot]
		if reset := state.BootID != prior.BootID; reset {
			resets[slot] = true
			queries[slot] = 0
			sessions[slot] = 0
			http[slot] = map[string]float64{}
			continue
		}
		resets[slot] = false
		queries[slot] = after.queries[slot] - before.queries[slot]
		sessions[slot] = state.PendingSessions - prior.PendingSessions
		counts := make(map[string]float64)
		for label, value := range after.http[slot] {
			if delta := value - before.http[slot][label]; delta != 0 {
				counts[label] = delta
			}
		}
		http[slot] = counts
	}
	return after.states, queries, sessions, http, resets
}

func metricValue(body, metric, endpoint string) float64 {
	var total float64
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, metric+"{") {
			continue
		}
		if endpoint != "" && !strings.Contains(line, `endpoint="`+endpoint+`"`) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 2 {
			var value float64
			if _, err := fmt.Sscan(fields[1], &value); err == nil {
				total += value
			}
		}
	}
	return total
}
