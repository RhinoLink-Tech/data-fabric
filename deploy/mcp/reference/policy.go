package reference

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

func (env *Environment) tableAuthorizationPolicy() security.TableAuthorizationConfig {
	sources := make(map[string]security.DataSourceTablePolicy, len(env.Sources)+len(env.TenantBSources))
	register := func(source Source, tenant string) {
		scopes := []security.TableScope{
			{Schema: source.Schema, Table: "orders", Columns: []string{"id", "amount", "status", "created_at"}},
			{Schema: source.Schema, Table: "order_items", Columns: []string{"id", "order_id", "item", "quantity", "discount"}},
		}
		if source.Type == "trino" {
			scopes = []security.TableScope{
				{Catalog: "tpch", Schema: "tiny", Table: "nation", Columns: []string{"nationkey", "name", "regionkey"}},
				{Catalog: "tpch_copy", Schema: "tiny", Table: "region", Columns: []string{"regionkey", "name"}},
				{Catalog: "tpch", Schema: "sf100", Table: "lineitem", Columns: []string{"orderkey"}},
			}
		}
		sources[source.ID] = security.DataSourceTablePolicy{
			Rules: []security.TableAccessRule{{TenantID: tenant, Read: scopes, Metadata: scopes}},
		}
	}
	for _, source := range env.Sources {
		register(source, "tenant-a")
	}
	for _, source := range env.TenantBSources {
		register(source, "tenant-b")
	}
	return security.TableAuthorizationConfig{Enabled: true, DataSources: sources}
}

func (env *Environment) publishManagedPolicy(ctx context.Context) error {
	policy := env.tableAuthorizationPolicy()
	if len(policy.DataSources) == 0 {
		return fmt.Errorf("cannot publish an empty reference table policy")
	}

	var current struct {
		Status security.TablePolicyStatus
	}
	if err := env.policyAdminRequest(ctx, "rest-a", http.MethodGet, "/internal/table-policy", nil, &current); err != nil {
		return fmt.Errorf("read active table policy: %w", err)
	}
	expected, err := strconv.ParseInt(current.Status.Revision, 10, 64)
	if err != nil || expected <= 0 {
		return fmt.Errorf("active table policy revision is invalid")
	}

	var validation struct {
		Valid            bool
		ExpectedRevision string
	}
	if err := env.policyAdminRequest(ctx, "rest-a", http.MethodPost, "/internal/table-policy/validate",
		map[string]any{"policy": policy}, &validation); err != nil {
		return fmt.Errorf("preflight reference table policy: %w", err)
	}
	if !validation.Valid || validation.ExpectedRevision != current.Status.Revision {
		return fmt.Errorf("reference table policy preflight returned a different head")
	}

	var result security.TablePolicyPublicationResult
	if err := env.policyAdminRequest(ctx, "rest-a", http.MethodPost, "/internal/table-policy/publish",
		map[string]any{"expectedRevision": expected, "policy": policy}, &result); err != nil {
		return fmt.Errorf("publish reference table policy: %w", err)
	}
	if result.Revision != strconv.FormatInt(expected+1, 10) ||
		result.PreviousRevision != current.Status.Revision ||
		result.Outcome != "activated" || !result.AuditRecorded {
		return fmt.Errorf("reference table policy publication was not audited and activated")
	}
	if env.replica != nil {
		if err := env.waitForPolicyRevision(ctx, result.Revision); err != nil {
			return err
		}
	}
	return nil
}

func (env *Environment) waitForPolicyRevision(ctx context.Context, revision string) error {
	deadline := time.Now().Add(security.TablePolicyPropagationWindow)
	for {
		allFresh := true
		for _, slot := range []string{"rest-a", "rest-b", "mcp-a", "mcp-b"} {
			var status struct {
				TablePolicyRevision string
				TablePolicyFresh    bool
			}
			if err := env.policyAdminRequest(ctx, slot, http.MethodGet, "/internal/replica", nil, &status); err != nil ||
				status.TablePolicyRevision != revision || !status.TablePolicyFresh {
				allFresh = false
				break
			}
		}
		if allFresh {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("reference policy revision %s did not reach every owner within %s", revision, security.TablePolicyPropagationWindow)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func (env *Environment) policyAdminRequest(ctx context.Context, slot, method, path string, payload, response any) error {
	base := env.APIURL
	if owner, exists := env.OwnerURLs[slot]; exists {
		base = owner
	}
	if base == "" {
		return fmt.Errorf("reference admin endpoint is unavailable")
	}

	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("encode admin request")
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(base, "/")+path, body)
	if err != nil {
		return fmt.Errorf("construct admin request")
	}
	identity := env.Identities["operator"]
	token := identity.RESTToken
	if strings.HasPrefix(slot, "mcp-") {
		token = identity.MCPToken
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	if strings.HasPrefix(base, "https://") && env.TLSConfig != nil {
		client.Transport = &http.Transport{TLSClientConfig: env.TLSConfig.Clone()}
	}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("send admin request: %T", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("admin endpoint returned HTTP %d", resp.StatusCode)
	}
	if response == nil {
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	if err := decoder.Decode(response); err != nil {
		return fmt.Errorf("decode admin response")
	}
	return nil
}
