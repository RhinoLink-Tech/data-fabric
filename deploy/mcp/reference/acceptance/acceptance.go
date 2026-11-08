// Package acceptance runs the gateway delivery matrix over the public REST
// and MCP Streamable HTTP interfaces of the disposable reference stack.
package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
)

// SessionTTLSeconds and TenantCapacity are opt-in reference settings used
// to prove expiry and resource release within a bounded test run.
const (
	SessionTTLSeconds = 8
	TenantCapacity    = 2
)

type scenario struct {
	id              string
	check           func(context.Context, wire) error
	restUnsupported string
	smoke           bool
}

var matrix = []scenario{
	{id: "A01", check: discoverSources, smoke: true},
	{id: "A02", check: inspectSources},
	{id: "A03", check: listTables, restUnsupported: "no policy-filtered public REST table listing"},
	{id: "A04", check: describeTable, restUnsupported: "no policy-filtered public REST table description"},
	{id: "A05", check: bindParameters, smoke: true},
	{id: "A06", check: normalizedValues},
	{id: "A07", check: filterAggregateJoin},
	{id: "A08", check: interleavedBatches},
	{id: "A09", check: rejectReplay},
	{id: "A10", check: concurrentFetch},
	{id: "A11", check: expireSessions, smoke: true},
	{id: "A12", check: func(ctx context.Context, w wire) error { return w.rejectAuth(ctx, "") }, smoke: true},
	{id: "A13", check: invalidTokens},
	{id: "A14", check: wrongIssuer, restUnsupported: "REST uses HS256 authentication"},
	{id: "A15", check: wrongAudience, restUnsupported: "REST uses HS256 authentication"},
	{id: "A16", check: missingScope, restUnsupported: "REST uses HS256 authentication"},
	{id: "A17", check: crossTenant, smoke: true},
	{id: "A18", check: deniedTable},
	{id: "A19", check: deniedColumns, smoke: true},
	{id: "A20", check: unsafeStatements},
	{id: "A21", check: malformedSQL},
	{id: "A22", check: sourceOutage},
	{id: "A23", check: queryDeadline, smoke: true},
	{id: "A24", check: auditEvidence},
	{id: "A25", check: searchSchema, restUnsupported: "no public REST schema search", smoke: true},
	{id: "A26", check: logicalQueryMetrics, smoke: true},
	{id: "A27", check: managedPolicyAuditRecovery, smoke: true},
	{id: "A28", check: pageMutationSemantics, smoke: true},
}

type evidence struct {
	Case             string         `json:"case"`
	Transport        surface        `json:"transport"`
	Result           string         `json:"result"`
	DurationMS       int64          `json:"duration_ms"`
	CandidateSHA     string         `json:"candidate_sha"`
	WorkingTreeDirty bool           `json:"working_tree_dirty"`
	GatewayImage     string         `json:"gateway_image"`
	GatewayImageID   string         `json:"gateway_image_id"`
	Profile          map[string]any `json:"profile"`
	Observations     []string       `json:"observations,omitempty"`
	Reason           string         `json:"reason,omitempty"`
}

// Check writes a JSON line per selected case and transport. Failures are
// collected so an acceptance run can expose several regressions at once.
func Check(ctx context.Context, env *reference.Environment, out io.Writer, smoke bool) error {
	httpClient := newHTTP(env)
	defer httpClient.CloseIdleConnections()
	encoder := json.NewEncoder(out)
	var failures []error
	for _, test := range matrix {
		if smoke && !test.smoke {
			continue
		}
		for _, kind := range []surface{rest, mcpHTTP} {
			if err := ctx.Err(); err != nil {
				return errors.Join(errors.Join(failures...), err)
			}
			record := evidence{
				Case: test.id, Transport: kind, Result: "pass",
				CandidateSHA: env.RunInfo.SourceRevision, WorkingTreeDirty: env.RunInfo.WorkingTreeDirty,
				GatewayImage: env.RunInfo.GatewayImage, GatewayImageID: env.RunInfo.GatewayImageID,
				Profile: acceptanceProfileForRun(env),
			}
			if kind == rest && test.restUnsupported != "" {
				record.Result, record.Reason = "unsupported", test.restUnsupported
			} else {
				started := time.Now()
				var observations []string
				w := (wire{kind: kind, env: env, http: httpClient}).withMCPState()
				w.observe = func(value string) { observations = append(observations, value) }
				err := test.check(ctx, w)
				record.Observations = append([]string(nil), observations...)
				cleanupErr := w.releaseMCPSessions(ctx)
				err = errors.Join(err, cleanupErr)
				if err != nil {
					record.Result, record.Reason = "fail", err.Error()
					failures = append(failures, fmt.Errorf("%s %s: %w", test.id, kind, err))
				}
				record.DurationMS = time.Since(started).Milliseconds()
			}
			if err := encoder.Encode(record); err != nil {
				return fmt.Errorf("write acceptance evidence: %w", err)
			}
		}
	}
	return errors.Join(failures...)
}
