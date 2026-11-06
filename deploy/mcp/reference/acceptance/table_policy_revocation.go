package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
	"github.com/RhinoLink-Tech/data-fabric/internal/replicarouting"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

type tablePolicyPublicationResult struct {
	PreviousRevision  string `json:"previousRevision"`
	Revision          string `json:"revision"`
	PublishedBy       string `json:"publishedBy"`
	Outcome           string `json:"outcome"`
	AuditRecorded     bool   `json:"auditRecorded"`
	PropagationWindow string `json:"propagationWindow"`
}

func checkTablePolicyRevocation(ctx context.Context, env *reference.Environment, client *http.Client) error {
	source, ok := env.Sources["mysql"]
	if !ok || source.ID == "" {
		return fmt.Errorf("reference MySQL source is unavailable")
	}
	restWire := wire{kind: rest, env: env, http: client}
	mcpWire := (wire{kind: mcpHTTP, env: env, http: client}).withMCPState()
	defer mcpWire.closeMCPSessions()

	if err := env.SetRoutedNewOwner(ctx, replicarouting.REST, "rest-a"); err != nil {
		return err
	}
	if err := env.SetRoutedNewOwner(ctx, replicarouting.MCP, "mcp-a"); err != nil {
		return err
	}
	restSession, err := readQuery(ctx, restWire, source, orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := checkRESTPageOwner(env, restSession, "rest-a"); err != nil {
		return err
	}
	mcpSession, err := readQuery(ctx, mcpWire, source, orderedIDs, nil, 1)
	if err != nil {
		return err
	}
	if err := checkMCPPageOwner(env, mcpSession, "mcp-a"); err != nil {
		return err
	}

	policy, expectedRevision, err := readManagedTablePolicy(ctx, env)
	if err != nil {
		return err
	}
	if !policy.Enabled {
		return fmt.Errorf("reference table authorization was not enabled")
	}
	revokedPolicy, err := cloneManagedTablePolicy(policy)
	if err != nil {
		return err
	}
	if _, exists := revokedPolicy.DataSources[source.ID]; !exists {
		return fmt.Errorf("reference source has no active table policy")
	}
	delete(revokedPolicy.DataSources, source.ID)

	// Switch new work to the other owners while preserving the two sessions on A.
	if err := env.SetRoutedNewOwner(ctx, replicarouting.REST, "rest-b"); err != nil {
		return err
	}
	if err := env.SetRoutedNewOwner(ctx, replicarouting.MCP, "mcp-b"); err != nil {
		return err
	}
	revokedRevision := expectedRevision + 1
	published, err := publishPolicyREST(ctx, env, client, expectedRevision, revokedPolicy)
	if err != nil {
		return err
	}
	if published.Revision != strconv.FormatInt(revokedRevision, 10) || published.PreviousRevision != strconv.FormatInt(expectedRevision, 10) ||
		published.PublishedBy != "operator" || published.Outcome != "activated" || !published.AuditRecorded {
		return fmt.Errorf("REST policy publication did not return an audited revision")
	}
	if err := waitForTablePolicyRevision(ctx, env, strconv.FormatInt(revokedRevision, 10)); err != nil {
		return fmt.Errorf("REST-published revocation did not reach every owner: %w", err)
	}

	for _, attempt := range []struct {
		name string
		wire wire
		page batch
	}{
		{"REST continuation", restWire, restSession},
		{"MCP continuation", mcpWire, mcpSession},
	} {
		result, err := attempt.wire.next(ctx, reader, attempt.page)
		if err != nil {
			return err
		}
		if err := result.expectError(attempt.wire.kind, http.StatusForbidden, "TABLE_ACCESS_DENIED", tableDenied); err != nil {
			return fmt.Errorf("%s continued after grant removal: %w", attempt.name, err)
		}
	}
	for _, current := range []wire{restWire, mcpWire} {
		result, err := current.query(ctx, reader, "", source, orderedIDs, nil, 10, 5)
		if err != nil {
			return err
		}
		if err := result.expectError(current.kind, http.StatusForbidden, "TABLE_ACCESS_DENIED", tableDenied); err != nil {
			return fmt.Errorf("%s new query did not observe revoked grant: %w", current.kind, err)
		}
	}

	// Exercise MCP publication as a rollback to the saved valid policy.
	// It creates the next revision; it never rewinds the active head.
	initialJSON, err := json.Marshal(policy)
	if err != nil {
		return fmt.Errorf("encode rollback policy")
	}
	validate, err := mcpWire.tool(ctx, "operator", "", "validate_table_policy", map[string]any{
		"policy_json": string(initialJSON),
	})
	if err != nil {
		return err
	}
	var validation struct {
		Valid            bool   `json:"valid"`
		ExpectedRevision string `json:"expectedRevision"`
	}
	if err := validate.decode(&validation); err != nil || !validation.Valid || validation.ExpectedRevision != strconv.FormatInt(revokedRevision, 10) {
		return fmt.Errorf("MCP policy preflight did not report the revoked revision")
	}
	rollbackRevision := revokedRevision + 1
	published, err = publishPolicyMCP(ctx, mcpWire, revokedRevision, policy)
	if err != nil {
		return err
	}
	if published.Revision != strconv.FormatInt(rollbackRevision, 10) || published.PreviousRevision != strconv.FormatInt(revokedRevision, 10) ||
		published.PublishedBy != "operator" || published.Outcome != "activated" || !published.AuditRecorded {
		return fmt.Errorf("MCP rollback publication did not create an audited revision")
	}
	if err := waitForTablePolicyRevision(ctx, env, strconv.FormatInt(rollbackRevision, 10)); err != nil {
		return fmt.Errorf("MCP rollback did not reach every owner: %w", err)
	}

	for _, current := range []wire{restWire, mcpWire} {
		page, err := readQuery(ctx, current, source, orderedIDs, nil, 10)
		if err != nil {
			return fmt.Errorf("%s query failed after rollback: %w", current.kind, err)
		}
		if err := rows(page, id("1"), id("2"), id("3")); err != nil {
			return fmt.Errorf("%s did not use restored policy: %w", current.kind, err)
		}
	}

	historyStatus, _, historyBody, err := ownerRequest(ctx, env, "rest-a", "operator", http.MethodGet, "/internal/table-policy/publications?limit=10")
	if err != nil || historyStatus != http.StatusOK {
		return fmt.Errorf("policy publication history is unavailable")
	}
	var history struct {
		Publications []security.TablePolicyPublicationRecord `json:"publications"`
	}
	if err := json.Unmarshal(historyBody, &history); err != nil || len(history.Publications) < 3 {
		return fmt.Errorf("policy publication history omitted outcomes")
	}
	if history.Publications[0].Revision != rollbackRevision || history.Publications[0].PublishedBy != "operator" ||
		history.Publications[0].Outcome != "activated" ||
		history.Publications[1].Revision != revokedRevision || history.Publications[1].PublishedBy != "operator" {
		return fmt.Errorf("policy publication history does not match the active rollback sequence")
	}
	return nil
}

func readManagedTablePolicy(ctx context.Context, env *reference.Environment) (security.TableAuthorizationConfig, int64, error) {
	status, _, body, err := ownerRequest(ctx, env, "rest-a", "operator", http.MethodGet, "/internal/table-policy")
	if err != nil || status != http.StatusOK {
		return security.TableAuthorizationConfig{}, 0, fmt.Errorf("managed table policy could not be read")
	}
	var response struct {
		Status security.TablePolicyStatus        `json:"status"`
		Policy security.TableAuthorizationConfig `json:"policy"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return security.TableAuthorizationConfig{}, 0, fmt.Errorf("managed table policy response was invalid")
	}
	revision, err := strconv.ParseInt(response.Status.Revision, 10, 64)
	if err != nil || revision <= 0 {
		return security.TableAuthorizationConfig{}, 0, fmt.Errorf("managed table policy revision was invalid")
	}
	return response.Policy, revision, nil
}

func cloneManagedTablePolicy(policy security.TableAuthorizationConfig) (security.TableAuthorizationConfig, error) {
	data, err := json.Marshal(policy)
	if err != nil {
		return security.TableAuthorizationConfig{}, fmt.Errorf("copy table policy")
	}
	var copy security.TableAuthorizationConfig
	if err := json.Unmarshal(data, &copy); err != nil {
		return security.TableAuthorizationConfig{}, fmt.Errorf("copy table policy")
	}
	return copy, nil
}

func publishPolicyREST(ctx context.Context, env *reference.Environment, client *http.Client, expected int64, policy security.TableAuthorizationConfig) (tablePolicyPublicationResult, error) {
	body, err := json.Marshal(map[string]any{"expectedRevision": expected, "policy": policy})
	if err != nil {
		return tablePolicyPublicationResult{}, fmt.Errorf("encode REST policy publication")
	}
	status, _, response, err := sendHTTP(ctx, client, http.MethodPost,
		env.OwnerURLs["rest-a"]+"/internal/table-policy/publish",
		env.Identities["operator"].RESTToken, "", "", body)
	if err != nil || status != http.StatusOK {
		return tablePolicyPublicationResult{}, fmt.Errorf("REST policy publication failed with HTTP %d", status)
	}
	var result tablePolicyPublicationResult
	if err := json.Unmarshal(response, &result); err != nil {
		return tablePolicyPublicationResult{}, fmt.Errorf("REST policy publication response was invalid")
	}
	return result, nil
}

func publishPolicyMCP(ctx context.Context, w wire, expected int64, policy security.TableAuthorizationConfig) (tablePolicyPublicationResult, error) {
	body, err := json.Marshal(policy)
	if err != nil {
		return tablePolicyPublicationResult{}, fmt.Errorf("encode MCP policy publication")
	}
	result, err := w.tool(ctx, "operator", "", "publish_table_policy", map[string]any{
		"expected_revision": float64(expected), "policy_json": string(body),
	})
	if err != nil {
		return tablePolicyPublicationResult{}, err
	}
	var publication tablePolicyPublicationResult
	if err := result.decode(&publication); err != nil {
		return tablePolicyPublicationResult{}, fmt.Errorf("MCP policy publication failed")
	}
	return publication, nil
}

func waitForTablePolicyRevision(ctx context.Context, env *reference.Environment, revision string) error {
	started := time.Now()
	deadline := started.Add(security.TablePolicyPropagationWindow)
	for {
		allFresh := true
		for _, slot := range []string{"rest-a", "rest-b", "mcp-a", "mcp-b"} {
			status, _, body, err := ownerRequest(ctx, env, slot, "operator", http.MethodGet, "/internal/replica")
			if err != nil || status != http.StatusOK {
				allFresh = false
				break
			}
			var state ownerStatus
			if err := json.Unmarshal(body, &state); err != nil ||
				state.TablePolicyRevision != revision || !state.TablePolicyFresh {
				allFresh = false
				break
			}
		}
		if allFresh {
			if time.Since(started) > security.TablePolicyPropagationWindow {
				return fmt.Errorf("owners exceeded the %s propagation window", security.TablePolicyPropagationWindow)
			}
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("not every owner reported fresh revision %s within %s", revision, security.TablePolicyPropagationWindow)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}
