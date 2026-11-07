package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/deploy/mcp/reference"
)

const (
	benchmarkWarmups = 5
	benchmarkActor   = reader
)

type benchmarkRecord struct {
	Type             string   `json:"type"`
	FormatVersion    int      `json:"format_version,omitempty"`
	StartedAtUTC     string   `json:"started_at_utc,omitempty"`
	SourceRevision   string   `json:"source_revision,omitempty"`
	WorkingTreeDirty bool     `json:"working_tree_dirty"`
	Platform         string   `json:"platform,omitempty"`
	GoVersion        string   `json:"go_version,omitempty"`
	CPUCount         int      `json:"cpu_count,omitempty"`
	MemoryTotalBytes uint64   `json:"memory_total_bytes,omitempty"`
	DockerVersion    string   `json:"docker_server_version,omitempty"`
	ComposeVersion   string   `json:"compose_version,omitempty"`
	GatewayImage     string   `json:"gateway_image,omitempty"`
	GatewayImageID   string   `json:"gateway_image_id,omitempty"`
	MySQLImage       string   `json:"mysql_image,omitempty"`
	PostgreSQLImage  string   `json:"postgresql_image,omitempty"`
	Profile          string   `json:"profile,omitempty"`
	MetadataStore    string   `json:"metadata_store,omitempty"`
	Sources          []string `json:"sources,omitempty"`
	FixtureRows      int      `json:"fixture_rows,omitempty"`
	SampleTarget     int      `json:"sample_target,omitempty"`
	WarmupCount      int      `json:"warmup_count,omitempty"`
	Parallel         bool     `json:"parallel"`
	Notes            string   `json:"notes,omitempty"`
	Transport        string   `json:"transport,omitempty"`
	Source           string   `json:"source,omitempty"`
	Scenario         string   `json:"scenario,omitempty"`
	Iteration        int      `json:"iteration,omitempty"`
	DurationNS       int64    `json:"duration_ns,omitempty"`
	RequestCount     int      `json:"gateway_request_count,omitempty"`
	RowsReturned     int      `json:"rows_returned,omitempty"`
	Result           string   `json:"result,omitempty"`
	Error            string   `json:"error,omitempty"`
	SampleCount      int      `json:"successful_sample_count,omitempty"`
	FailedCount      int      `json:"failed_sample_count,omitempty"`
	P50MS            float64  `json:"p50_ms,omitempty"`
	P95MS            float64  `json:"p95_ms,omitempty"`
	P99MS            float64  `json:"p99_ms,omitempty"`
}

// Benchmark measures warmed, serial query journeys through the public REST and
// MCP HTTP interfaces on the disposable reference profile. It writes JSONL so
// every timed attempt and the percentile summary can be reviewed independently.
func Benchmark(ctx context.Context, env *reference.Environment, out io.Writer, samples int) error {
	if samples < 20 || samples > 10000 {
		return fmt.Errorf("benchmark samples must be between 20 and 10000")
	}
	if env == nil || out == nil {
		return fmt.Errorf("benchmark requires a reference environment and output writer")
	}
	encoder := json.NewEncoder(out)
	memoryBytes := hostMemoryBytes()
	metadata := benchmarkRecord{
		Type: "run", FormatVersion: 1, StartedAtUTC: time.Now().UTC().Format(time.RFC3339Nano),
		SourceRevision: env.RunInfo.SourceRevision, WorkingTreeDirty: env.RunInfo.WorkingTreeDirty,
		Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(), CPUCount: runtime.NumCPU(),
		MemoryTotalBytes: memoryBytes, DockerVersion: env.RunInfo.DockerServerVersion,
		ComposeVersion: env.RunInfo.ComposeVersion, GatewayImage: env.RunInfo.GatewayImage,
		GatewayImageID: env.RunInfo.GatewayImageID, MySQLImage: env.RunInfo.MySQLImage,
		PostgreSQLImage: env.RunInfo.PostgreSQLImage, Profile: "single-node synthetic reference",
		MetadataStore: "MySQL", Sources: []string{"MySQL", "PostgreSQL"}, FixtureRows: 3,
		SampleTarget: samples, WarmupCount: benchmarkWarmups, Parallel: false,
		Notes: "Serial warmed measurements. MCP initialize is excluded after warmup. Gateway request count includes the first-page and continuation calls; internal source SQL/COUNT statement counts are not instrumented. Full-result pagination can re-execute source SQL. The full acceptance JSONL is captured separately by the command.",
	}
	if err := encoder.Encode(metadata); err != nil {
		return fmt.Errorf("write benchmark metadata: %w", err)
	}

	var failures []error
	for _, kind := range []surface{rest, mcpHTTP} {
		httpClient := newHTTP(env)
		w := (wire{kind: kind, env: env, http: httpClient}).withMCPState()
		for _, sourceKey := range []string{"mysql", "postgresql"} {
			source := env.Sources[sourceKey]
			for _, scenario := range []string{"first_page", "complete_result"} {
				measurements := make([]int64, 0, samples)
				failed := 0
				warmupFailed := false
				for i := 1; i <= benchmarkWarmups; i++ {
					started := time.Now()
					requests, rowsReturned, err := runBenchmarkTask(ctx, w, source, scenario)
					record := benchmarkRecord{
						Type: "warmup", Transport: string(kind), Source: sourceKey, Scenario: scenario,
						Iteration: i, DurationNS: time.Since(started).Nanoseconds(), RequestCount: requests,
						RowsReturned: rowsReturned, Result: "pass",
					}
					if err != nil {
						record.Result, record.Error = "fail", err.Error()
						failures = append(failures, fmt.Errorf("warmup %s/%s/%s/%d: %w", kind, sourceKey, scenario, i, err))
						failed++
						warmupFailed = true
					}
					if err := encoder.Encode(record); err != nil {
						return fmt.Errorf("write benchmark warmup: %w", err)
					}
				}
				if warmupFailed {
					summary := benchmarkRecord{
						Type: "summary", Transport: string(kind), Source: sourceKey, Scenario: scenario,
						FailedCount: failed, Error: "timed samples skipped because a warm-up task failed",
					}
					if err := encoder.Encode(summary); err != nil {
						return fmt.Errorf("write benchmark summary: %w", err)
					}
					continue
				}
				for i := 1; i <= samples; i++ {
					started := time.Now()
					requests, rowsReturned, err := runBenchmarkTask(ctx, w, source, scenario)
					duration := time.Since(started)
					record := benchmarkRecord{
						Type: "sample", Transport: string(kind), Source: sourceKey, Scenario: scenario,
						Iteration: i, DurationNS: duration.Nanoseconds(), RequestCount: requests,
						RowsReturned: rowsReturned, Result: "pass",
					}
					if err != nil {
						record.Result, record.Error = "fail", err.Error()
						failed++
						failures = append(failures, fmt.Errorf("sample %s/%s/%s/%d: %w", kind, sourceKey, scenario, i, err))
					} else {
						measurements = append(measurements, record.DurationNS)
					}
					if err := encoder.Encode(record); err != nil {
						return fmt.Errorf("write benchmark sample: %w", err)
					}
					if ctx.Err() != nil {
						break
					}
				}
				summary := benchmarkRecord{
					Type: "summary", Transport: string(kind), Source: sourceKey, Scenario: scenario,
					SampleCount: len(measurements), FailedCount: failed,
				}
				if len(measurements) > 0 {
					summary.P50MS = percentileMS(measurements, 0.50)
					summary.P95MS = percentileMS(measurements, 0.95)
					summary.P99MS = percentileMS(measurements, 0.99)
				}
				if err := encoder.Encode(summary); err != nil {
					return fmt.Errorf("write benchmark summary: %w", err)
				}
				if ctx.Err() != nil {
					failures = append(failures, ctx.Err())
					break
				}
			}
			if err := w.releaseMCPSessions(ctx); err != nil {
				failures = append(failures, fmt.Errorf("release %s benchmark sessions: %w", kind, err))
			}
			httpClient.CloseIdleConnections()
		}
	}
	return errors.Join(failures...)
}

func runBenchmarkTask(ctx context.Context, w wire, source reference.Source, scenario string) (int, int, error) {
	if scenario == "first_page" {
		result, err := w.query(ctx, benchmarkActor, "", source,
			"SELECT id FROM "+source.Schema+".orders WHERE id = 1", nil, 10, 5)
		if err != nil {
			return 1, 0, err
		}
		page, err := readBatch(result, nil)
		if err != nil {
			return 1, 0, err
		}
		if err := rows(page, id("1")); err != nil {
			return 1, len(page.Entries), err
		}
		return 1, len(page.Entries), nil
	}
	if scenario != "complete_result" {
		return 0, 0, fmt.Errorf("unsupported benchmark scenario")
	}

	const expectedRows = 3
	current, err := readBatch(w.query(ctx, benchmarkActor, "", source, orderedIDsFor(source), nil, 1, 5))
	if err != nil {
		return 1, 0, err
	}
	requests, rowsReturned := 1, 0
	queryID := current.QueryID
	for expected := 1; expected <= expectedRows; expected++ {
		if current.QueryID != queryID || current.TotalCount != expectedRows || len(current.Entries) != 1 {
			return requests, rowsReturned + len(current.Entries), fmt.Errorf("complete result returned an unexpected page contract")
		}
		if err := rows(current, id(strconv.Itoa(expected))); err != nil {
			return requests, rowsReturned + len(current.Entries), err
		}
		rowsReturned += len(current.Entries)
		if expected == expectedRows {
			if current.NextURI != "" {
				return requests, rowsReturned, fmt.Errorf("complete result exposed an extra page")
			}
			break
		}
		if current.NextURI == "" {
			return requests, rowsReturned, fmt.Errorf("complete result ended before all fixture rows were returned")
		}
		result, err := w.next(ctx, benchmarkActor, current)
		if err != nil {
			return requests, rowsReturned, err
		}
		current, err = readBatch(result, nil)
		requests++
		if err != nil {
			return requests, rowsReturned, err
		}
	}
	return requests, rowsReturned, nil
}

func orderedIDsFor(source reference.Source) string {
	return "SELECT id FROM " + source.Schema + ".orders ORDER BY id"
}

func percentileMS(samples []int64, percentile float64) float64 {
	ordered := append([]int64(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := int(math.Ceil(float64(len(ordered)) * percentile))
	if index < 1 {
		index = 1
	}
	if index > len(ordered) {
		index = len(ordered)
	}
	return float64(ordered[index-1]) / float64(time.Millisecond)
}

func hostMemoryBytes() uint64 {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kib, err := strconv.ParseUint(fields[1], 10, 64)
			if err == nil {
				return kib * 1024
			}
		}
	}
	return 0
}
