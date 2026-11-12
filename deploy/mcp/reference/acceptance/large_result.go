package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
)

const (
	largeResultBatchSize = 1000
	largeResultRowBytes  = 4096
	largeResultRows      = 19683
)

type gatewayMemoryPeak struct {
	ContainerName string
	CapturedAtUTC string
	MemoryBytes   uint64
}

type connectionPoolEvidence struct {
	Source string `json:"source"`
	Total  int    `json:"total_connections"`
	Active int    `json:"active_connections"`
	Idle   int    `json:"idle_connections"`
}

// LargeResultOutput exercises the configured cumulative result-byte budget
// through public REST and MCP while each gateway process has an explicit
// cgroup memory ceiling. It is opt-in because each case transfers over 64 MiB.
func LargeResultOutput(ctx context.Context, env *reference.Environment, out io.Writer, recoveryWait time.Duration) error {
	if env == nil || out == nil {
		return fmt.Errorf("large-result run requires a reference environment and output writer")
	}
	if env.RunInfo.MultiReplica || env.RunInfo.TrinoIntegration {
		return fmt.Errorf("large-result run requires the single-node REST/MCP reference profile")
	}
	if env.RunInfo.GatewayMemoryLimitBytes == 0 {
		return fmt.Errorf("large-result run requires an explicit gateway memory limit")
	}
	if env.RunInfo.QueryResultBudgetBytes <= 0 {
		return fmt.Errorf("large-result run requires a configured query result byte budget")
	}
	if recoveryWait < 0 || recoveryWait > 10*time.Minute {
		return fmt.Errorf("recovery wait must be between 0 and 10 minutes")
	}

	encoder := json.NewEncoder(out)
	if err := writeLoadRecord(encoder, loadRecord{
		Type: "large_result_run", StartedAtUTC: time.Now().UTC().Format(time.RFC3339Nano),
		CandidateSHA: env.RunInfo.SourceRevision, WorkingTreeDirty: env.RunInfo.WorkingTreeDirty,
		GatewayImage: env.RunInfo.GatewayImage, GatewayImageID: env.RunInfo.GatewayImageID,
		GatewayImageSizeBytes: env.RunInfo.GatewayImageSizeBytes,
		MySQLImage:            env.RunInfo.MySQLImage, MySQLImageSizeBytes: env.RunInfo.MySQLImageSizeBytes,
		PostgreSQLImage: env.RunInfo.PostgreSQLImage, PostgreSQLImageSizeBytes: env.RunInfo.PostgreSQLImageSizeBytes,
		Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(),
		DockerVersion: env.RunInfo.DockerServerVersion, ComposeVersion: env.RunInfo.ComposeVersion,
		GatewayMemoryLimitBytes: env.RunInfo.GatewayMemoryLimitBytes,
		ResultBudgetBytes:       env.RunInfo.QueryResultBudgetBytes,
		FixtureRows:             3,
		Workload:                "synthetic nine-way cross join with 4096-byte strings; REST and MCP against MySQL and PostgreSQL",
		Phase:                   "byte-budget rejection, cgroup memory sampling, source connection return, and post-recovery sampling",
		Scenario: fmt.Sprintf("logical_output_at_least=%d bytes; no production dataset or SLO claim; recovery_wait=%s",
			largeResultRows*largeResultRowBytes, recoveryWait),
	}); err != nil {
		return err
	}

	var failures []error
	for _, target := range []struct {
		kind    surface
		service string
	}{
		{rest, "data-fabric-api"},
		{mcpHTTP, "data-fabric-mcp"},
	} {
		for _, sourceID := range []string{"mysql", "postgresql"} {
			if err := runLargeResultCase(ctx, env, encoder, target.kind, target.service, sourceID, env.Sources[sourceID]); err != nil {
				failures = append(failures, fmt.Errorf("%s/%s: %w", target.kind, sourceID, err))
			}
		}
	}

	if recoveryWait > 0 {
		timer := time.NewTimer(recoveryWait)
		select {
		case <-ctx.Done():
			timer.Stop()
			failures = append(failures, ctx.Err())
		case <-timer.C:
		}
	}
	snapshot, err := env.CaptureResourceSnapshot(ctx)
	if err != nil {
		failures = append(failures, err)
	} else {
		if err := assertGatewayMemoryCaps(snapshot, env.RunInfo.GatewayMemoryLimitBytes); err != nil {
			failures = append(failures, err)
		}
		if err := writeLoadRecord(encoder, loadRecord{
			Type: "large_result_resource", Workload: "gateway memory after recovery wait",
			Phase: "after-recovery", RecoveryWaitSeconds: int(recoveryWait.Seconds()),
			GatewayMemoryLimitBytes: env.RunInfo.GatewayMemoryLimitBytes, Resource: &snapshot,
			Result: "observed",
		}); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func runLargeResultCase(ctx context.Context, env *reference.Environment, encoder *json.Encoder, kind surface, service, sourceID string, source reference.Source) error {
	if source.ID == "" {
		return fmt.Errorf("synthetic source is unavailable")
	}
	snapshot, err := env.CaptureResourceSnapshot(ctx)
	if err != nil {
		return err
	}
	if err := assertGatewayMemoryCaps(snapshot, env.RunInfo.GatewayMemoryLimitBytes); err != nil {
		return err
	}
	if err := writeLargeResourceRecord(encoder, snapshot, kind, sourceID, "before-query", env.RunInfo.GatewayMemoryLimitBytes); err != nil {
		return err
	}

	client := newHTTP(env)
	w := (wire{kind: kind, env: env, http: client}).withMCPState()
	defer func() {
		_ = w.releaseMCPSessions(context.Background())
		client.CloseIdleConnections()
	}()

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	peak := map[string]gatewayMemoryPeak{}
	for _, container := range snapshot.Containers {
		name := gatewayServiceName(container.Name)
		if name != "" {
			peak[name] = gatewayMemoryPeak{
				ContainerName: container.Name, CapturedAtUTC: snapshot.CapturedAtUTC,
				MemoryBytes: container.MemoryBytes,
			}
		}
	}
	stopSampling, sampleDone := startLargeResourceSampler(runCtx, env, encoder, kind, sourceID, env.RunInfo.GatewayMemoryLimitBytes, peak)

	started := time.Now()
	current, err := readBatch(w.query(runCtx, reader, "", source, largeResultSQL(source.Schema), nil, largeResultBatchSize, 60))
	if err != nil {
		close(stopSampling)
		if sampleErr := <-sampleDone; sampleErr != nil {
			err = errors.Join(err, sampleErr)
		}
		return writeLargeResultFailure(encoder, kind, service, sourceID, started, env, "initial query failed", err)
	}
	if current.TotalCount != largeResultRows || len(current.Columns) != 1 || len(current.Entries) == 0 {
		close(stopSampling)
		_ = <-sampleDone
		return writeLargeResultFailure(encoder, kind, service, sourceID, started, env, "synthetic cardinality mismatch", fmt.Errorf("unexpected deterministic result shape"))
	}

	requests, rowsReturned := 1, 0
	var outputBytes int64
	var resultErr error
	for {
		pageBytes, err := encodedBudgetPageBytes(current)
		if err != nil {
			resultErr = err
			break
		}
		outputBytes += pageBytes
		rowsReturned += len(current.Entries)
		if current.NextURI == "" {
			resultErr = fmt.Errorf("large result completed without reaching the byte budget")
			break
		}
		reply, err := w.nextWithBatchSize(runCtx, reader, current, largeResultBatchSize)
		requests++
		if err != nil {
			resultErr = err
			break
		}
		if reply.failed {
			if kind == rest {
				resultErr = reply.expectError(rest, 413, "QUERY_RESULT_LIMIT_EXCEEDED", resultBudgetMessage)
			} else if reply.message != resultBudgetMessage {
				resultErr = fmt.Errorf("MCP did not report the configured result byte budget")
			}
			break
		}
		current, err = readBatch(reply, nil)
		if err != nil {
			resultErr = err
			break
		}
		if current.TotalCount != largeResultRows || len(current.Columns) != 1 {
			resultErr = fmt.Errorf("continuation changed the synthetic result contract")
			break
		}
	}
	close(stopSampling)
	sampleErr := <-sampleDone
	if sampleErr != nil {
		resultErr = errors.Join(resultErr, sampleErr)
	}

	if resultErr == nil && (rowsReturned == 0 || rowsReturned >= largeResultRows ||
		outputBytes == 0 || outputBytes > env.RunInfo.QueryResultBudgetBytes) {
		resultErr = fmt.Errorf("observed output exceeded or did not exercise the configured result budget")
	}

	connectionCtx, connectionCancel := context.WithTimeout(ctx, 15*time.Second)
	defer connectionCancel()
	connections, connectionErr := waitForSourceConnections(connectionCtx, env, sourceID)
	evidence := (*connectionPoolEvidence)(nil)
	if connectionErr == nil {
		evidence = &connectionPoolEvidence{
			Source: connections.Source, Total: connections.Total,
			Active: connections.Active, Idle: connections.Idle,
		}
		if connections.Active != 0 || connections.Total != connections.Idle {
			connectionErr = fmt.Errorf("source query connections did not return to idle or close")
		}
	}
	if connectionErr != nil {
		resultErr = errors.Join(resultErr, connectionErr)
	}

	snapshot, resourceErr := env.CaptureResourceSnapshot(ctx)
	if resourceErr == nil {
		resourceErr = assertGatewayMemoryCaps(snapshot, env.RunInfo.GatewayMemoryLimitBytes)
	}
	if resourceErr != nil {
		resultErr = errors.Join(resultErr, resourceErr)
	} else if err := writeLargeResourceRecord(encoder, snapshot, kind, sourceID, "after-budget-stop", env.RunInfo.GatewayMemoryLimitBytes); err != nil {
		resultErr = errors.Join(resultErr, err)
	}

	record := loadRecord{
		Type: "large_result", Workload: "large synthetic result rejected at cumulative output-byte budget",
		Transport: string(kind), Source: sourceID, Scenario: "nine-way cross join; batch_size=1000",
		DurationNS: time.Since(started).Nanoseconds(), GatewayRequests: requests,
		RowsReturned: rowsReturned, OutputBytes: outputBytes,
		ResultBudgetBytes:       env.RunInfo.QueryResultBudgetBytes,
		GatewayMemoryLimitBytes: env.RunInfo.GatewayMemoryLimitBytes,
		Connections:             evidence,
		Result:                  "pass",
	}
	if resultErr != nil {
		record.Result, record.Error = "fail", "large-result resource or budget assertion failed"
	}
	if err := writeLoadRecord(encoder, record); err != nil {
		resultErr = errors.Join(resultErr, err)
	}
	if resultErr != nil {
		return resultErr
	}
	for serviceName, memory := range peak {
		if err := writeLoadRecord(encoder, loadRecord{
			Type: "large_result_peak_memory", Workload: "gateway process memory during large-result query",
			Transport: string(kind), Source: sourceID, Service: serviceName, Phase: "peak",
			GatewayMemoryLimitBytes: env.RunInfo.GatewayMemoryLimitBytes,
			Resource: &reference.ResourceSnapshot{
				CapturedAtUTC: memory.CapturedAtUTC,
				Containers: []reference.ContainerResource{{
					Name: memory.ContainerName, MemoryBytes: memory.MemoryBytes,
					MemoryLimitBytes: uint64(env.RunInfo.GatewayMemoryLimitBytes),
				}},
			},
			Result: "pass",
		}); err != nil {
			return err
		}
	}
	return nil
}

func startLargeResourceSampler(ctx context.Context, env *reference.Environment, encoder *json.Encoder, kind surface, sourceID string, limit int64, peak map[string]gatewayMemoryPeak) (chan struct{}, chan error) {
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				done <- ctx.Err()
				return
			case <-stop:
				done <- nil
				return
			case <-ticker.C:
				snapshot, err := env.CaptureResourceSnapshot(ctx)
				if err == nil {
					err = assertGatewayMemoryCaps(snapshot, limit)
				}
				if err == nil {
					for _, container := range snapshot.Containers {
						service := gatewayServiceName(container.Name)
						if service != "" && container.MemoryBytes > peak[service].MemoryBytes {
							peak[service] = gatewayMemoryPeak{
								ContainerName: container.Name, CapturedAtUTC: snapshot.CapturedAtUTC,
								MemoryBytes: container.MemoryBytes,
							}
						}
					}
					err = writeLargeResourceRecord(encoder, snapshot, kind, sourceID, "during-query", limit)
				}
				if err != nil {
					done <- err
					return
				}
			}
		}
	}()
	return stop, done
}

func writeLargeResourceRecord(encoder *json.Encoder, snapshot reference.ResourceSnapshot, kind surface, sourceID, phase string, limit int64) error {
	return writeLoadRecord(encoder, loadRecord{
		Type: "large_result_resource", Workload: "container resource sample during output-budget verification",
		Transport: string(kind), Source: sourceID, Phase: phase,
		GatewayMemoryLimitBytes: limit, Resource: &snapshot,
		Result: "observed",
	})
}

func assertGatewayMemoryCaps(snapshot reference.ResourceSnapshot, limit int64) error {
	if limit <= 0 {
		return fmt.Errorf("gateway memory limit is not configured")
	}
	seen := map[string]bool{}
	for _, container := range snapshot.Containers {
		service := gatewayServiceName(container.Name)
		if service == "" {
			continue
		}
		seen[service] = true
		if container.MemoryLimitBytes != uint64(limit) {
			return fmt.Errorf("%s memory limit observed %d bytes, expected %d", service, container.MemoryLimitBytes, limit)
		}
		if container.MemoryBytes > uint64(limit) {
			return fmt.Errorf("%s memory usage exceeded its configured cgroup limit", service)
		}
	}
	if !seen["data-fabric-api"] || !seen["data-fabric-mcp"] {
		return fmt.Errorf("resource snapshot did not include both gateway processes")
	}
	return nil
}

func gatewayServiceName(containerName string) string {
	for _, name := range []string{"data-fabric-api", "data-fabric-mcp"} {
		if strings.Contains(containerName, name) {
			return name
		}
	}
	return ""
}

func largeResultSQL(schema string) string {
	var query strings.Builder
	query.WriteString("SELECT REPEAT('x', 4096) AS payload FROM ")
	for index := 0; index < 9; index++ {
		if index > 0 {
			query.WriteString(" CROSS JOIN ")
		}
		query.WriteString(schema)
		query.WriteString(".orders AS t")
		query.WriteString(strconv.Itoa(index))
	}
	return query.String()
}

func encodedBudgetPageBytes(page batch) (int64, error) {
	payload, err := json.Marshal(struct {
		Columns []column `json:"columns"`
		Entries [][]any  `json:"entries"`
	}{
		Columns: page.Columns,
		Entries: page.Entries,
	})
	if err != nil {
		return 0, fmt.Errorf("encode synthetic query page size")
	}
	return int64(len(payload)), nil
}

func waitForSourceConnections(ctx context.Context, env *reference.Environment, sourceID string) (reference.SourceConnectionSnapshot, error) {
	var latest reference.SourceConnectionSnapshot
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		snapshot, err := env.CaptureSourceConnectionSnapshot(ctx, sourceID)
		if err != nil {
			return latest, err
		}
		latest = snapshot
		if snapshot.Active == 0 {
			return latest, nil
		}
		select {
		case <-ctx.Done():
			return latest, ctx.Err()
		case <-ticker.C:
		}
	}
}

func writeLargeResultFailure(encoder *json.Encoder, kind surface, service, sourceID string, started time.Time, env *reference.Environment, reason string, cause error) error {
	if err := writeLoadRecord(encoder, loadRecord{
		Type: "large_result", Workload: "large synthetic result rejected at cumulative output-byte budget",
		Transport: string(kind), Service: service, Source: sourceID,
		Scenario: reason, DurationNS: time.Since(started).Nanoseconds(),
		ResultBudgetBytes:       env.RunInfo.QueryResultBudgetBytes,
		GatewayMemoryLimitBytes: env.RunInfo.GatewayMemoryLimitBytes,
		Result:                  "fail", Error: "large-result request did not complete its expected budget scenario",
	}); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}
