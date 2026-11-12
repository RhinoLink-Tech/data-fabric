package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
	"github.com/RhinoLink-Tech/data-fabric/internal/replicarouting"
)

type requestResult struct {
	response reply
	err      error
}

func waitOwnerInFlight(ctx context.Context, env *reference.Environment, slot string, minimum int, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := readOwnerStatus(ctx, env, slot)
		if err == nil && status.InFlight >= minimum {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if err != nil {
				return err
			}
			return fmt.Errorf("%s did not report the expected in-flight request", slot)
		case <-ticker.C:
		}
	}
}

func waitOwnerRetired(ctx context.Context, env *reference.Environment, slot string, timeout time.Duration) (ownerStatus, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := readOwnerStatus(ctx, env, slot)
		if err == nil && status.State == "retired" && status.CleanupComplete && status.PendingSessions == 0 && status.InFlight == 0 {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return ownerStatus{}, ctx.Err()
		case <-deadline.C:
			if err != nil {
				return ownerStatus{}, err
			}
			return ownerStatus{}, fmt.Errorf("%s did not finish retirement and cleanup", slot)
		case <-ticker.C:
		}
	}
}

func checkRESTDrainCutoff(ctx context.Context, env *reference.Environment, client *http.Client) error {
	if err := env.SetRoutedNewOwner(ctx, replicarouting.REST, "rest-a"); err != nil {
		return err
	}
	w := wire{kind: rest, env: env, http: client}
	type attempt struct {
		response reply
		err      error
	}
	var draining ownerStatus
	err := env.WithPostgresOrdersLock(ctx, func(locked context.Context) error {
		queryCtx, cancel := context.WithCancel(locked)
		defer cancel()
		done := make(chan attempt, 1)
		go func() {
			response, queryErr := w.query(queryCtx, reader, "", env.Sources["postgresql"],
				"SELECT id FROM orders ORDER BY id LIMIT 3", nil, 1, 10)
			done <- attempt{response: response, err: queryErr}
		}()
		if err := waitOwnerInFlight(locked, env, "rest-a", 1, 2*time.Second); err != nil {
			return err
		}
		_, _, body, err := ownerRequest(locked, env, "rest-a", "operator", http.MethodPost, "/internal/replica/drain")
		if err != nil {
			return err
		}
		if err := json.Unmarshal(body, &draining); err != nil || draining.State != "draining" || draining.DrainDeadline == nil {
			return fmt.Errorf("REST owner did not enter deadline drain")
		}
		if wait := time.Until(*draining.DrainDeadline) + 100*time.Millisecond; wait > 0 {
			if err := sleepContext(locked, wait); err != nil {
				return err
			}
		}
		if _, err := waitOwnerRetired(locked, env, "rest-a", 3*time.Second); err != nil {
			return err
		}
		select {
		case result := <-done:
			if result.err == nil && !result.response.failed {
				return fmt.Errorf("in-flight SQL request returned data after the drain deadline")
			}
			if result.err == nil && !noExposedQueryResult(result.response) {
				return fmt.Errorf("cancelled SQL request exposed query results")
			}
			return nil
		case <-time.After(2 * time.Second):
			return fmt.Errorf("drain deadline did not cancel the in-flight SQL request")
		}
	})
	if err != nil {
		return err
	}
	if err := env.SetRoutedNewOwner(ctx, replicarouting.REST, "rest-b"); err != nil {
		return err
	}
	fresh, err := readQuery(ctx, w, env.Sources["mysql"], orderedIDs, nil, 1)
	if err != nil {
		return fmt.Errorf("healthy owner did not take new work after cutoff: %w", err)
	}
	if err := checkRESTPageOwner(env, fresh, "rest-b"); err != nil {
		return err
	}
	if err := finishThirdRow(ctx, w, fresh, fresh.QueryID); err != nil {
		return err
	}
	if err := env.RestartReplica(ctx, "rest-a"); err != nil {
		return err
	}
	_, err = waitOwnerBoot(ctx, env, "rest-a", draining.BootID, 90*time.Second)
	return err
}

func checkQueryCancellationAndBudget(ctx context.Context, env *reference.Environment, client *http.Client, kind surface) error {
	w := (wire{kind: kind, env: env, http: client}).withMCPState()
	defer w.closeMCPSessions()
	pool, slot := replicarouting.REST, "rest-b"
	if kind == mcpHTTP {
		pool, slot = replicarouting.MCP, "mcp-b"
	}
	if err := env.SetRoutedNewOwner(ctx, pool, slot); err != nil {
		return err
	}
	beforeMetrics, err := ownerMetrics(ctx, env, slot)
	if err != nil {
		return err
	}
	first, err := readQuery(ctx, w, env.Sources["postgresql"],
		"SELECT id FROM orders ORDER BY id LIMIT 3", nil, 1)
	if err != nil {
		return err
	}
	if kind == rest {
		err = checkRESTPageOwner(env, first, slot)
	} else {
		err = checkMCPPageOwner(env, first, slot)
	}
	if err != nil {
		return err
	}
	err = env.WithPostgresOrdersLock(ctx, func(locked context.Context) error {
		requestCtx, cancel := context.WithCancel(locked)
		done := make(chan requestResult, 1)
		go func() {
			response, callErr := w.next(requestCtx, reader, first)
			done <- requestResult{response, callErr}
		}()
		if err := waitOwnerInFlight(locked, env, slot, 1, 2*time.Second); err != nil {
			cancel()
			return err
		}
		cancel()
		select {
		case attempt := <-done:
			if attempt.err == nil && !attempt.response.failed {
				return fmt.Errorf("client cancellation still returned a continuation batch")
			}
			if attempt.err == nil && !noExposedQueryResult(attempt.response) {
				return fmt.Errorf("cancelled continuation exposed query results")
			}
			return nil
		case <-time.After(2 * time.Second):
			return fmt.Errorf("cancelling the client request did not stop the owner request")
		}
	})
	if err != nil {
		return err
	}
	replay, err := w.next(ctx, reader, first)
	if err != nil {
		return err
	}
	if kind == rest {
		if err := replay.expectError(rest, http.StatusNotFound, "QUERY_SESSION_UNAVAILABLE", sessionError); err != nil {
			return fmt.Errorf("cancelled REST continuation retained its cursor: %w", err)
		}
	} else if err := replay.expectError(mcpHTTP, 0, "", sessionError); err != nil {
		return fmt.Errorf("cancelled MCP continuation retained its cursor: %w", err)
	}
	afterMetrics, err := ownerMetrics(ctx, env, slot)
	if err != nil {
		return err
	}
	if err := assertOwnerLogicalOutcomeDelta(beforeMetrics, afterMetrics, "canceled", 1); err != nil {
		return fmt.Errorf("%s cancellation was not recorded once as a logical-query outcome: %w", kind, err)
	}
	if err := w.releaseMCPSessions(ctx); err != nil {
		return fmt.Errorf("release MCP transport session after cancellation: %w", err)
	}
	if status, err := readOwnerStatus(ctx, env, slot); err != nil {
		return err
	} else if status.InFlight != 0 {
		return fmt.Errorf("%s owner retained in-flight work after cancellation", slot)
	}
	streams, err := openAtCapacity(ctx, w, env.Sources["mysql"])
	if err != nil {
		return err
	}
	for _, stream := range streams {
		if err := finishThirdRow(ctx, w, stream, stream.QueryID); err != nil {
			return err
		}
	}
	return w.releaseMCPSessions(ctx)
}

func checkMCPOwnerDrain(ctx context.Context, env *reference.Environment, client *http.Client) error {
	if err := env.SetRoutedNewOwner(ctx, replicarouting.MCP, "mcp-b"); err != nil {
		return err
	}
	mcpClient, capture, err := openMCPForIdentity(ctx, env, reader)
	if err != nil {
		return err
	}
	defer mcpClient.Close()
	session := capture.sessionID()
	first, err := runMCPQuery(ctx, mcpClient, env.Sources["mysql"])
	if err != nil {
		return err
	}
	if err := checkMCPPageOwner(env, first, "mcp-b"); err != nil {
		return err
	}

	getCtx, cancelGET := context.WithCancel(ctx)
	defer cancelGET()
	request, err := http.NewRequestWithContext(getCtx, http.MethodGet, env.MCPURL, nil)
	if err != nil {
		return fmt.Errorf("construct MCP event stream request")
	}
	request.Header.Set("Authorization", "Bearer "+env.Identities[reader].MCPToken)
	request.Header.Set("Mcp-Session-Id", session)
	request.Header.Set("Accept", "text/event-stream")
	stream, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("open MCP event stream: %T", err)
	}
	if stream.StatusCode != http.StatusOK {
		stream.Body.Close()
		return fmt.Errorf("MCP event stream returned HTTP %d", stream.StatusCode)
	}
	streamDone := make(chan error, 1)
	go func() { _, copyErr := io.Copy(io.Discard, stream.Body); streamDone <- copyErr }()
	_, _, body, err := ownerRequest(ctx, env, "mcp-b", "operator", http.MethodPost, "/internal/replica/drain")
	if err != nil {
		cancelGET()
		stream.Body.Close()
		return err
	}
	var draining ownerStatus
	if err := json.Unmarshal(body, &draining); err != nil || draining.State != "draining" || draining.DrainDeadline == nil {
		cancelGET()
		stream.Body.Close()
		return fmt.Errorf("MCP owner did not enter deadline drain")
	}
	status, _, initBody, err := rawMCPInitialize(ctx, env, client, mcpInitializeBody(71))
	if err != nil {
		cancelGET()
		stream.Body.Close()
		return err
	}
	if status != http.StatusServiceUnavailable || !strings.Contains(string(initBody), "owner_draining") {
		cancelGET()
		stream.Body.Close()
		return fmt.Errorf("draining MCP owner admitted a new transport session")
	}
	second, err := runMCPNext(ctx, mcpClient, first)
	if err != nil {
		cancelGET()
		stream.Body.Close()
		return fmt.Errorf("draining MCP owner stopped an existing continuation: %w", err)
	}
	if err := expectPage(second, first.QueryID, "2", 3, true); err != nil {
		cancelGET()
		stream.Body.Close()
		return err
	}
	last, err := runMCPNext(ctx, mcpClient, second)
	if err != nil {
		cancelGET()
		stream.Body.Close()
		return err
	}
	if err := expectPage(last, first.QueryID, "3", 3, false); err != nil {
		cancelGET()
		stream.Body.Close()
		return err
	}
	if _, err := waitOwnerRetired(ctx, env, "mcp-b", 6*time.Second); err != nil {
		cancelGET()
		stream.Body.Close()
		return err
	}
	select {
	case <-streamDone:
	case <-time.After(2 * time.Second):
		cancelGET()
		stream.Body.Close()
		return fmt.Errorf("MCP event stream remained open after the owner cutoff")
	}
	cancelGET()
	_ = stream.Body.Close()
	staleBody := mcpToolCallBody(72, "fetch_next_batch", nextArguments(first))
	status, _, stale, err := sendHTTP(ctx, client, http.MethodPost, env.MCPURL, env.Identities[reader].MCPToken, session, "", staleBody)
	if err != nil {
		return err
	}
	if err := checkMCPUnavailable(status, stale, first.QueryID, first.Slug, first.Token); err != nil {
		return err
	}
	if err := env.RestartReplica(ctx, "mcp-b"); err != nil {
		return err
	}
	_, err = waitOwnerBoot(ctx, env, "mcp-b", draining.BootID, 90*time.Second)
	return err
}
