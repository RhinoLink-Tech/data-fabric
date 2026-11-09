package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
)

type loadRecord struct {
	Type                     string                      `json:"type"`
	StartedAtUTC             string                      `json:"started_at_utc,omitempty"`
	CandidateSHA             string                      `json:"candidate_sha,omitempty"`
	WorkingTreeDirty         bool                        `json:"working_tree_dirty,omitempty"`
	GatewayImage             string                      `json:"gateway_image,omitempty"`
	GatewayImageID           string                      `json:"gateway_image_id,omitempty"`
	GatewayImageSizeBytes    uint64                      `json:"gateway_image_size_bytes,omitempty"`
	MySQLImage               string                      `json:"mysql_image,omitempty"`
	MySQLImageSizeBytes      uint64                      `json:"mysql_image_size_bytes,omitempty"`
	PostgreSQLImage          string                      `json:"postgresql_image,omitempty"`
	PostgreSQLImageSizeBytes uint64                      `json:"postgresql_image_size_bytes,omitempty"`
	Platform                 string                      `json:"platform,omitempty"`
	GoVersion                string                      `json:"go_version,omitempty"`
	CPUCount                 int                         `json:"cpu_count,omitempty"`
	MemoryTotalBytes         uint64                      `json:"memory_total_bytes,omitempty"`
	DockerVersion            string                      `json:"docker_server_version,omitempty"`
	ComposeVersion           string                      `json:"compose_version,omitempty"`
	SessionTTLSeconds        int                         `json:"session_ttl_seconds,omitempty"`
	TenantConcurrency        int                         `json:"tenant_concurrency,omitempty"`
	FixtureRows              int                         `json:"fixture_rows,omitempty"`
	Workload                 string                      `json:"workload,omitempty"`
	Phase                    string                      `json:"phase,omitempty"`
	Transport                string                      `json:"transport,omitempty"`
	Source                   string                      `json:"source,omitempty"`
	Scenario                 string                      `json:"scenario,omitempty"`
	Tenant                   string                      `json:"tenant,omitempty"`
	Service                  string                      `json:"service,omitempty"`
	Wave                     int                         `json:"wave,omitempty"`
	Worker                   int                         `json:"worker,omitempty"`
	Concurrency              int                         `json:"concurrency,omitempty"`
	DurationNS               int64                       `json:"duration_ns,omitempty"`
	ElapsedMS                float64                     `json:"elapsed_ms,omitempty"`
	ServiceRestartMS         float64                     `json:"service_restart_ms,omitempty"`
	FirstAuthorizedResultMS  float64                     `json:"first_authorized_result_after_restart_ms,omitempty"`
	FirstRequestMS           float64                     `json:"first_successful_request_ms,omitempty"`
	FailedAttempts           int                         `json:"failed_attempts,omitempty"`
	GatewayRequests          int                         `json:"gateway_request_count,omitempty"`
	RowsReturned             int                         `json:"rows_returned,omitempty"`
	OutputBytes              int64                       `json:"output_bytes,omitempty"`
	ResultBudgetBytes        int64                       `json:"result_budget_bytes,omitempty"`
	GatewayMemoryLimitBytes  int64                       `json:"gateway_memory_limit_bytes,omitempty"`
	Connections              *connectionPoolEvidence     `json:"connections,omitempty"`
	Result                   string                      `json:"result,omitempty"`
	Error                    string                      `json:"error,omitempty"`
	AttemptedTasks           int                         `json:"attempted_tasks,omitempty"`
	SuccessfulTasks          int                         `json:"successful_tasks,omitempty"`
	AdmissionRejections      int                         `json:"admission_rejections,omitempty"`
	RateLimitedTasks         int                         `json:"rate_limited_tasks,omitempty"`
	FailedTasks              int                         `json:"failed_tasks,omitempty"`
	RequestsPerSecond        float64                     `json:"tasks_per_second,omitempty"`
	P50MS                    float64                     `json:"p50_ms,omitempty"`
	P95MS                    float64                     `json:"p95_ms,omitempty"`
	P99MS                    float64                     `json:"p99_ms,omitempty"`
	RecoveryWaitSeconds      int                         `json:"recovery_wait_seconds,omitempty"`
	Resource                 *reference.ResourceSnapshot `json:"resource,omitempty"`
	StatementID              string                      `json:"statement_id,omitempty"`
	Statement                string                      `json:"statement,omitempty"`
	CountStatement           bool                        `json:"count_statement,omitempty"`
	StatementCalls           uint64                      `json:"statement_calls,omitempty"`
	RowsExamined             uint64                      `json:"rows_examined,omitempty"`
	RowsReturnedByStatement  uint64                      `json:"statement_rows_returned,omitempty"`
	RowsExaminedAvailable    bool                        `json:"rows_examined_available,omitempty"`
	StatementExecutionTimeMS float64                     `json:"statement_execution_time_ms,omitempty"`
}

type loadWorker struct {
	actor    string
	tenant   string
	kind     surface
	source   reference.Source
	sourceID string
	scenario string
	http     *http.Client
	wire     wire
}

func newLoadWorker(env *reference.Environment, kind surface, actor, tenant, sourceID, scenario string, source reference.Source) *loadWorker {
	client := newHTTP(env)
	return &loadWorker{
		actor: actor, tenant: tenant, kind: kind, source: source,
		sourceID: sourceID, scenario: scenario, http: client,
		wire: (wire{kind: kind, env: env, http: client}).withMCPState(),
	}
}

func (worker *loadWorker) close() {
	worker.wire.closeMCPSessions()
	worker.http.CloseIdleConnections()
}

func writeLoadRecord(encoder *json.Encoder, record loadRecord) error {
	if err := encoder.Encode(record); err != nil {
		return fmt.Errorf("write load evidence: %w", err)
	}
	return nil
}

// CapacityAndSoak measures restart-to-first-result, per-source statement cost,
// bounded one/four-way concurrency, cross-tenant behavior, mixed task lengths,
// a fixed-duration soak, and resource return on the disposable reference stack.
func CapacityAndSoak(ctx context.Context, env *reference.Environment, out io.Writer, duration, recoveryWait time.Duration) error {
	if env == nil || out == nil {
		return fmt.Errorf("capacity run requires a reference environment and output writer")
	}
	if duration < 15*time.Second || duration > 10*time.Minute {
		return fmt.Errorf("load duration must be between 15 seconds and 10 minutes")
	}
	if env.RunInfo.MultiReplica || env.RunInfo.TrinoIntegration {
		return fmt.Errorf("capacity and soak currently require the single-node REST/MCP reference profile")
	}
	if recoveryWait < 0 || recoveryWait > 10*time.Minute {
		return fmt.Errorf("recovery wait must be between 0 and 10 minutes")
	}
	encoder := json.NewEncoder(out)
	if err := writeLoadRecord(encoder, loadRecord{
		Type: "run", StartedAtUTC: time.Now().UTC().Format(time.RFC3339Nano),
		CandidateSHA: env.RunInfo.SourceRevision, WorkingTreeDirty: env.RunInfo.WorkingTreeDirty,
		GatewayImage: env.RunInfo.GatewayImage, GatewayImageID: env.RunInfo.GatewayImageID,
		GatewayImageSizeBytes: env.RunInfo.GatewayImageSizeBytes,
		MySQLImage:            env.RunInfo.MySQLImage, MySQLImageSizeBytes: env.RunInfo.MySQLImageSizeBytes,
		PostgreSQLImage: env.RunInfo.PostgreSQLImage, PostgreSQLImageSizeBytes: env.RunInfo.PostgreSQLImageSizeBytes,
		Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(),
		CPUCount: runtime.NumCPU(), MemoryTotalBytes: hostMemoryBytes(),
		DockerVersion: env.RunInfo.DockerServerVersion, ComposeVersion: env.RunInfo.ComposeVersion,
		SessionTTLSeconds: env.RunInfo.SessionTTLSeconds, TenantConcurrency: env.RunInfo.TenantConcurrency,
		FixtureRows: 3,
		Workload:    "synthetic MySQL metadata + MySQL/PostgreSQL source; separate REST and MCP processes",
		Phase:       "1/4-way and mixed tenant contention waves followed by a fixed-duration mixed-task soak",
		Scenario:    fmt.Sprintf("soak_target=%s recovery_wait=%s; 3-row fixture; no production SLO or cloud-price claim", duration, recoveryWait),
	}); err != nil {
		return err
	}

	for _, sourceID := range []string{"mysql", "postgresql"} {
		if err := env.EnsureTenantBSource(ctx, sourceID); err != nil {
			return fmt.Errorf("prepare tenant-b synthetic %s source: %w", sourceID, err)
		}
	}

	var failures []error
	for _, target := range []struct {
		kind    surface
		service string
	}{
		{rest, "data-fabric-api"},
		{mcpHTTP, "data-fabric-mcp"},
	} {
		if err := checkRestart(ctx, env, encoder, target.kind, target.service); err != nil {
			failures = append(failures, err)
		}
	}

	for _, kind := range []surface{rest, mcpHTTP} {
		for _, sourceID := range []string{"mysql", "postgresql"} {
			if err := measureSourceCost(ctx, env, encoder, kind, sourceID, env.Sources[sourceID]); err != nil {
				failures = append(failures, err)
			}
		}
	}

	if err := emitResourceSnapshot(ctx, env, encoder, "before-capacity"); err != nil {
		failures = append(failures, err)
	}
	for _, kind := range []surface{rest, mcpHTTP} {
		for _, sourceID := range []string{"mysql", "postgresql"} {
			source := env.Sources[sourceID]
			failures = append(failures, runCapacityGroup(ctx, env, encoder, kind, sourceID, source)...)
		}
	}
	if err := emitResourceSnapshot(ctx, env, encoder, "after-capacity"); err != nil {
		failures = append(failures, err)
	}
	if err := emitResourceSnapshot(ctx, env, encoder, "before-soak"); err != nil {
		failures = append(failures, err)
	}

	failures = append(failures, runSoak(ctx, env, encoder, duration, recoveryWait)...)
	return errors.Join(failures...)
}

func checkRestart(ctx context.Context, env *reference.Environment, encoder *json.Encoder, kind surface, service string) error {
	started := time.Now()
	restartDuration, restartErr := env.RestartService(ctx, service)
	record := loadRecord{
		Type: "restart", Workload: "service restart and first authorized query",
		Transport: string(kind), Service: service, ServiceRestartMS: float64(restartDuration.Microseconds()) / 1000,
	}
	if restartErr != nil {
		record.Result, record.Error = "fail", restartErr.Error()
		if err := writeLoadRecord(encoder, record); err != nil {
			return err
		}
		return fmt.Errorf("restart %s: %w", service, restartErr)
	}

	worker := newLoadWorker(env, kind, benchmarkActor, "tenant-a", "mysql", "first_page", env.Sources["mysql"])
	defer worker.close()
	deadlineCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var requestDuration time.Duration
	var resultErr error
	for {
		requestStarted := time.Now()
		_, _, resultErr = runTaskAs(deadlineCtx, worker.wire, worker.actor, worker.source, "first_page")
		if resultErr == nil {
			requestDuration = time.Since(requestStarted)
			break
		}
		record.FailedAttempts++
		if deadlineCtx.Err() != nil {
			break
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-deadlineCtx.Done():
			timer.Stop()
		case <-timer.C:
		}
		if deadlineCtx.Err() != nil {
			break
		}
	}
	record.FirstAuthorizedResultMS = float64(time.Since(started).Microseconds()) / 1000
	record.FirstRequestMS = float64(requestDuration.Microseconds()) / 1000
	if resultErr != nil {
		record.Result, record.Error = "fail", resultErr.Error()
		if err := writeLoadRecord(encoder, record); err != nil {
			return err
		}
		return fmt.Errorf("first authorized result after restarting %s: %w", service, resultErr)
	}
	record.Result = "pass"
	return writeLoadRecord(encoder, record)
}

func measureSourceCost(ctx context.Context, env *reference.Environment, encoder *json.Encoder, kind surface, sourceID string, source reference.Source) error {
	var failures []error
	for _, scenario := range []string{"first_page", "complete_result"} {
		worker := newLoadWorker(env, kind, benchmarkActor, "tenant-a", sourceID, scenario, source)
		_, _, warmErr := runTaskAs(ctx, worker.wire, worker.actor, source, "first_page")
		if warmErr != nil {
			worker.close()
			failures = append(failures, fmt.Errorf("warm %s/%s source-cost task: %w", kind, sourceID, warmErr))
			continue
		}
		if err := env.ResetSourceStatementStatistics(ctx, sourceID); err != nil {
			worker.close()
			failures = append(failures, err)
			continue
		}
		started := time.Now()
		requests, rowsReturned, taskErr := runTaskAs(ctx, worker.wire, worker.actor, source, scenario)
		duration := time.Since(started)
		worker.close()
		stats, statsErr := env.SourceStatementStatistics(ctx, sourceID)
		record := loadRecord{
			Type: "source_cost_task", Workload: "one public query task with database digest counters reset",
			Phase:     "per-source statements observed on the disposable source; COUNT is classified from normalized SQL",
			Transport: string(kind), Source: sourceID, Scenario: scenario,
			DurationNS: duration.Nanoseconds(), GatewayRequests: requests, RowsReturned: rowsReturned,
			Result: "pass",
		}
		if taskErr != nil {
			record.Result, record.Error = "fail", taskErr.Error()
			failures = append(failures, fmt.Errorf("%s/%s/%s source-cost task: %w", kind, sourceID, scenario, taskErr))
		}
		if err := writeLoadRecord(encoder, record); err != nil {
			return err
		}
		if statsErr != nil {
			failures = append(failures, statsErr)
			continue
		}
		if len(stats) == 0 {
			if err := writeLoadRecord(encoder, loadRecord{
				Type: "source_statement", Workload: "per-task source SQL instrumentation",
				Transport: string(kind), Source: sourceID, Scenario: scenario,
				Result: "no matching orders/order_items digest observed",
			}); err != nil {
				return err
			}
		}
		for _, stat := range stats {
			if err := writeLoadRecord(encoder, loadRecord{
				Type: "source_statement", Workload: "per-task source SQL instrumentation",
				Transport: string(kind), Source: sourceID, Scenario: scenario,
				StatementID: stat.StatementID, Statement: stat.Query, CountStatement: stat.CountStatement,
				StatementCalls: stat.Calls, RowsReturnedByStatement: stat.RowsReturned,
				RowsExamined: stat.RowsExamined, RowsExaminedAvailable: stat.RowsExaminedAvailable,
				StatementExecutionTimeMS: stat.TotalExecutionTimeMS, Result: "pass",
			}); err != nil {
				return err
			}
		}
	}
	return errors.Join(failures...)
}

func emitStatementStatistics(ctx context.Context, env *reference.Environment, encoder *json.Encoder, sourceID string, kind surface, workload, phase string) error {
	stats, err := env.SourceStatementStatistics(ctx, sourceID)
	if err != nil {
		return err
	}
	if len(stats) == 0 {
		return writeLoadRecord(encoder, loadRecord{
			Type: "source_statement", Workload: workload, Phase: phase,
			Transport: string(kind), Source: sourceID, Result: "no matching orders/order_items digest observed",
		})
	}
	for _, stat := range stats {
		if err := writeLoadRecord(encoder, loadRecord{
			Type: "source_statement", Workload: workload, Phase: phase,
			Transport: string(kind), Source: sourceID, StatementID: stat.StatementID,
			Statement: stat.Query, CountStatement: stat.CountStatement, StatementCalls: stat.Calls,
			RowsReturnedByStatement: stat.RowsReturned, RowsExamined: stat.RowsExamined,
			RowsExaminedAvailable:    stat.RowsExaminedAvailable,
			StatementExecutionTimeMS: stat.TotalExecutionTimeMS, Result: "pass",
		}); err != nil {
			return err
		}
	}
	return nil
}

func warmLoadWorkers(ctx context.Context, workers []*loadWorker, sourceID string) []error {
	var failures []error
	for _, worker := range workers {
		if _, _, err := runTaskAs(ctx, worker.wire, worker.actor, worker.source, "first_page"); err != nil {
			failures = append(failures, fmt.Errorf("warm %s/%s worker: %w", worker.kind, sourceID, err))
		}
	}
	return failures
}

func closeLoadWorkers(workers []*loadWorker) {
	for _, worker := range workers {
		worker.close()
	}
}

func runCapacityGroup(ctx context.Context, env *reference.Environment, encoder *json.Encoder, kind surface, sourceID string, source reference.Source) []error {
	var failures []error
	sameTenant := make([]*loadWorker, 4)
	for i := range sameTenant {
		sameTenant[i] = newLoadWorker(env, kind, benchmarkActor, "tenant-a", sourceID, "first_page", source)
	}
	failures = append(failures, warmLoadWorkers(ctx, sameTenant, sourceID)...)
	if err := env.ResetSourceStatementStatistics(ctx, sourceID); err != nil {
		failures = append(failures, err)
	}
	for _, concurrency := range []int{1, 4} {
		if err := runLoadGroup(ctx, encoder, fmt.Sprintf("same-tenant-%d-way-small", concurrency), sameTenant[:concurrency], 5); err != nil {
			failures = append(failures, err)
		}
	}
	if err := emitStatementStatistics(ctx, env, encoder, sourceID, kind,
		"same-tenant one/four-way source SQL", "counters reset after worker warm-up; exact COUNT digests are explicit"); err != nil {
		failures = append(failures, err)
	}
	closeLoadWorkers(sameTenant)

	crossTenant := []*loadWorker{
		newLoadWorker(env, kind, "app-a", "tenant-a", sourceID, "first_page", env.Sources[sourceID]),
		newLoadWorker(env, kind, "app-b", "tenant-a", sourceID, "first_page", env.Sources[sourceID]),
		newLoadWorker(env, kind, "app-a-tenant-b", "tenant-b", sourceID, "first_page", env.TenantBSources[sourceID]),
		newLoadWorker(env, kind, "app-a-tenant-b", "tenant-b", sourceID, "first_page", env.TenantBSources[sourceID]),
	}
	failures = append(failures, warmLoadWorkers(ctx, crossTenant, sourceID)...)
	if err := env.ResetSourceStatementStatistics(ctx, sourceID); err != nil {
		failures = append(failures, err)
	}
	if err := runLoadGroup(ctx, encoder, "two-tenant-four-way-small", crossTenant, 3); err != nil {
		failures = append(failures, err)
	}
	if err := emitStatementStatistics(ctx, env, encoder, sourceID, kind,
		"two-tenant four-way source SQL", "counters reset after worker warm-up; tenant-a and tenant-b counters share one synthetic source database"); err != nil {
		failures = append(failures, err)
	}
	closeLoadWorkers(crossTenant)

	mixed := []*loadWorker{
		newLoadWorker(env, kind, "app-a", "tenant-a", sourceID, "complete_result", env.Sources[sourceID]),
		newLoadWorker(env, kind, "app-b", "tenant-a", sourceID, "first_page", env.Sources[sourceID]),
		newLoadWorker(env, kind, "app-a-tenant-b", "tenant-b", sourceID, "complete_result", env.TenantBSources[sourceID]),
		newLoadWorker(env, kind, "app-a-tenant-b", "tenant-b", sourceID, "first_page", env.TenantBSources[sourceID]),
	}
	failures = append(failures, warmLoadWorkers(ctx, mixed, sourceID)...)
	if err := env.ResetSourceStatementStatistics(ctx, sourceID); err != nil {
		failures = append(failures, err)
	}
	if err := runLoadGroup(ctx, encoder, "two-tenant-mixed-fast-and-full-result", mixed, 3); err != nil {
		failures = append(failures, err)
	}
	if err := emitStatementStatistics(ctx, env, encoder, sourceID, kind,
		"two-tenant mixed fast/full-result source SQL", "full-result is a longer multi-page task, not a deliberately slow or large source query"); err != nil {
		failures = append(failures, err)
	}
	closeLoadWorkers(mixed)
	return failures
}

func runLoadGroup(ctx context.Context, encoder *json.Encoder, workload string, workers []*loadWorker, repeats int) error {
	var failures []error
	var successful []int64
	started := time.Now()
	attempted, rejected, rateLimited, failed, requests := 0, 0, 0, 0, 0
	for wave := 1; wave <= repeats; wave++ {
		results, elapsed := runWave(ctx, workers)
		for index, result := range results {
			attempted++
			requests += result.requests
			record := loadRecord{
				Type: "task", Workload: workload, Wave: wave, Worker: index + 1,
				Transport: string(result.worker.kind), Source: result.worker.sourceID,
				Scenario: result.worker.scenario, Tenant: result.worker.tenant,
				Concurrency: len(workers), DurationNS: result.duration.Nanoseconds(),
				GatewayRequests: result.requests, RowsReturned: result.rows, Result: result.status,
			}
			if result.err != nil {
				record.Error = safeLoadError(result.err)
			}
			switch result.status {
			case "pass":
				successful = append(successful, result.duration.Nanoseconds())
			case "admission_rejected":
				rejected++
			case "rate_limited":
				rateLimited++
			default:
				failed++
				failures = append(failures, fmt.Errorf("%s worker %d: %w", workload, index+1, result.err))
			}
			if err := writeLoadRecord(encoder, record); err != nil {
				return err
			}
		}
		if elapsed <= 0 {
			return fmt.Errorf("%s produced a non-positive wave duration", workload)
		}
	}
	elapsed := time.Since(started)
	result := "pass"
	switch {
	case failed > 0:
		result = "fail"
	case rejected > 0 && rateLimited > 0:
		result = "pass_with_admission_rejections_and_rate_limits"
	case rejected > 0:
		result = "pass_with_admission_rejections"
	case rateLimited > 0:
		result = "pass_with_rate_limits"
	}
	summary := loadRecord{
		Type: "summary", Workload: workload, Transport: string(workers[0].kind),
		Source: workers[0].sourceID, Concurrency: len(workers),
		ElapsedMS:      float64(elapsed.Microseconds()) / 1000,
		AttemptedTasks: attempted, SuccessfulTasks: len(successful), AdmissionRejections: rejected,
		RateLimitedTasks: rateLimited, FailedTasks: failed, GatewayRequests: requests,
		RequestsPerSecond: float64(attempted) / elapsed.Seconds(),
		Result:            result,
	}
	if len(successful) > 0 {
		summary.P50MS = percentileMS(successful, 0.50)
		summary.P95MS = percentileMS(successful, 0.95)
		summary.P99MS = percentileMS(successful, 0.99)
	}
	if err := writeLoadRecord(encoder, summary); err != nil {
		return err
	}
	return errors.Join(failures...)
}

type waveResult struct {
	worker   *loadWorker
	duration time.Duration
	requests int
	rows     int
	status   string
	err      error
}

func runWave(ctx context.Context, workers []*loadWorker) ([]waveResult, time.Duration) {
	results := make([]waveResult, len(workers))
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	ready.Add(len(workers))
	done.Add(len(workers))
	waveStarted := time.Now()
	for index, worker := range workers {
		go func(index int, worker *loadWorker) {
			defer done.Done()
			ready.Done()
			<-start
			taskStarted := time.Now()
			requests, rowsReturned, err := runTaskAs(ctx, worker.wire, worker.actor, worker.source, worker.scenario)
			results[index] = waveResult{
				worker: worker, duration: time.Since(taskStarted), requests: requests, rows: rowsReturned,
				status: classifyLoadError(err), err: err,
			}
		}(index, worker)
	}
	ready.Wait()
	close(start)
	done.Wait()
	return results, time.Since(waveStarted)
}

func classifyLoadError(err error) string {
	if err == nil {
		return "pass"
	}
	message := err.Error()
	if strings.Contains(message, "QUERY_ADMISSION_LIMIT") || strings.Contains(message, capacityError) {
		return "admission_rejected"
	}
	if strings.Contains(message, "QUERY_RATE_LIMIT_EXCEEDED") || strings.Contains(message, "RATE_LIMIT_EXCEEDED") ||
		strings.Contains(message, "Rate limit exceeded. Please try again later.") ||
		strings.Contains(message, "MCP request rate limited") {
		return "rate_limited"
	}
	return "fail"
}

func safeLoadError(err error) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	if len(message) > 240 {
		return message[:240]
	}
	return message
}

func emitResourceSnapshot(ctx context.Context, env *reference.Environment, encoder *json.Encoder, phase string) error {
	snapshot, err := env.CaptureResourceSnapshot(ctx)
	if err != nil {
		return err
	}
	if env.RunInfo.GatewayMemoryLimitBytes > 0 {
		if err := assertGatewayMemoryCaps(snapshot, env.RunInfo.GatewayMemoryLimitBytes); err != nil {
			return err
		}
	}
	return writeLoadRecord(encoder, loadRecord{
		Type: "resource", Phase: phase, Workload: "whole-stack instantaneous Docker component sample",
		Resource: &snapshot, Result: "pass",
	})
}

func runSoak(ctx context.Context, env *reference.Environment, encoder *json.Encoder, duration, recoveryWait time.Duration) []error {
	var failures []error
	workers := make([]*loadWorker, 0, 8)
	for _, kind := range []surface{rest, mcpHTTP} {
		for _, tenant := range []string{"tenant-a", "tenant-b"} {
			actors := []string{"app-a", "app-b"}
			if tenant == "tenant-b" {
				actors = []string{"app-a-tenant-b", "app-a-tenant-b"}
			}
			workers = append(workers,
				newLoadWorker(env, kind, actors[0], tenant, "mysql", "first_page", sourceForTenant(env, tenant, "mysql")),
				newLoadWorker(env, kind, actors[1], tenant, "postgresql", "complete_result", sourceForTenant(env, tenant, "postgresql")),
			)
		}
	}
	for _, worker := range workers {
		if _, _, err := runTaskAs(ctx, worker.wire, worker.actor, worker.source, "first_page"); err != nil {
			failures = append(failures, fmt.Errorf("warm %s soak worker: %w", worker.kind, err))
		}
	}
	for _, sourceID := range []string{"mysql", "postgresql"} {
		if err := env.ResetSourceStatementStatistics(ctx, sourceID); err != nil {
			failures = append(failures, err)
		}
	}

	type soakResult struct {
		worker   *loadWorker
		duration time.Duration
		requests int
		rows     int
		status   string
		err      error
	}
	results := make(chan soakResult, 8192)
	started := time.Now()
	deadline := started.Add(duration)
	var done sync.WaitGroup
	done.Add(len(workers))
	for _, worker := range workers {
		go func(worker *loadWorker) {
			defer done.Done()
			nextStart := time.Now()
			for time.Now().Before(deadline) && ctx.Err() == nil {
				if delay := time.Until(nextStart); delay > 0 {
					timer := time.NewTimer(delay)
					select {
					case <-ctx.Done():
						timer.Stop()
						return
					case <-timer.C:
					}
				}
				if !time.Now().Before(deadline) {
					return
				}
				taskCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
				taskStarted := time.Now()
				requests, rowsReturned, err := runTaskAs(taskCtx, worker.wire, worker.actor, worker.source, worker.scenario)
				taskDuration := time.Since(taskStarted)
				nextStart = taskStarted.Add(time.Second)
				cancel()
				results <- soakResult{
					worker: worker, duration: taskDuration, requests: requests, rows: rowsReturned,
					status: classifyLoadError(err), err: err,
				}
				if err != nil {
					time.Sleep(100 * time.Millisecond)
				}
			}
		}(worker)
	}
	go func() {
		done.Wait()
		close(results)
	}()

	midpoint := time.NewTimer(duration / 2)
	defer midpoint.Stop()
	var successDurations []int64
	attempted, successful, rejected, rateLimited, failed, requests := 0, 0, 0, 0, 0, 0
	snapshotTaken := false
	ctxDone := ctx.Done()
	for results != nil {
		select {
		case result, ok := <-results:
			if !ok {
				results = nil
				continue
			}
			attempted++
			requests += result.requests
			record := loadRecord{
				Type: "task", Workload: "fixed-duration mixed-task soak",
				Phase: fmt.Sprintf("target=%s", duration), Transport: string(result.worker.kind),
				Source: result.worker.sourceID, Scenario: result.worker.scenario, Tenant: result.worker.tenant,
				DurationNS: result.duration.Nanoseconds(), GatewayRequests: result.requests,
				RowsReturned: result.rows, Result: result.status,
			}
			if result.err != nil {
				record.Error = safeLoadError(result.err)
			}
			switch result.status {
			case "pass":
				successful++
				successDurations = append(successDurations, result.duration.Nanoseconds())
			case "admission_rejected":
				rejected++
			case "rate_limited":
				rateLimited++
			default:
				failed++
				failures = append(failures, fmt.Errorf("soak %s/%s/%s: %w",
					result.worker.kind, result.worker.tenant, result.worker.sourceID, result.err))
			}
			if err := writeLoadRecord(encoder, record); err != nil {
				failures = append(failures, err)
			}
		case <-midpoint.C:
			if !snapshotTaken {
				if err := emitResourceSnapshot(ctx, env, encoder, "during-soak-midpoint"); err != nil {
					failures = append(failures, err)
				}
				snapshotTaken = true
			}
		case <-ctxDone:
			failures = append(failures, ctx.Err())
			ctxDone = nil
		}
	}
	closeLoadWorkers(workers)
	elapsed := time.Since(started)
	summary := loadRecord{
		Type: "soak_summary", Workload: "fixed-duration mixed-task soak",
		ElapsedMS: float64(elapsed.Microseconds()) / 1000, AttemptedTasks: attempted,
		SuccessfulTasks: successful, AdmissionRejections: rejected,
		RateLimitedTasks: rateLimited, FailedTasks: failed,
		GatewayRequests: requests, RequestsPerSecond: float64(attempted) / elapsed.Seconds(),
		RecoveryWaitSeconds: int(recoveryWait.Seconds()), Result: "pass",
	}
	if len(successDurations) > 0 {
		summary.P50MS = percentileMS(successDurations, 0.50)
		summary.P95MS = percentileMS(successDurations, 0.95)
		summary.P99MS = percentileMS(successDurations, 0.99)
	}
	if failed > 0 {
		summary.Result = "fail"
	}
	if err := writeLoadRecord(encoder, summary); err != nil {
		failures = append(failures, err)
	}
	if err := emitResourceSnapshot(ctx, env, encoder, "after-soak"); err != nil {
		failures = append(failures, err)
	}
	for _, sourceID := range []string{"mysql", "postgresql"} {
		stats, err := env.SourceStatementStatistics(ctx, sourceID)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, stat := range stats {
			if err := writeLoadRecord(encoder, loadRecord{
				Type: "source_statement", Workload: "soak source SQL aggregate",
				Source: sourceID, StatementID: stat.StatementID, Statement: stat.Query,
				CountStatement: stat.CountStatement, StatementCalls: stat.Calls,
				RowsReturnedByStatement: stat.RowsReturned, RowsExamined: stat.RowsExamined,
				RowsExaminedAvailable:    stat.RowsExaminedAvailable,
				StatementExecutionTimeMS: stat.TotalExecutionTimeMS, Result: "pass",
			}); err != nil {
				failures = append(failures, err)
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
	for _, sourceID := range []string{"mysql", "postgresql"} {
		if err := emitStatementStatistics(ctx, env, encoder, sourceID, "", "soak source SQL aggregate after recovery", "after-recovery-wait"); err != nil {
			failures = append(failures, err)
		}
	}
	if err := emitResourceSnapshot(ctx, env, encoder, "after-recovery-wait"); err != nil {
		failures = append(failures, err)
	}
	return failures
}

func sourceForTenant(env *reference.Environment, tenant, sourceID string) reference.Source {
	if tenant == "tenant-b" {
		return env.TenantBSources[sourceID]
	}
	return env.Sources[sourceID]
}
