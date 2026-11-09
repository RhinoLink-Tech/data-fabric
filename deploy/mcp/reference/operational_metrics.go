package reference

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ContainerResource is one instantaneous Docker resource sample for the
// disposable reference stack. It is evidence for this profile, not a limit.
type ContainerResource struct {
	Name             string  `json:"name"`
	CPUPercent       float64 `json:"cpu_percent"`
	MemoryBytes      uint64  `json:"memory_bytes"`
	MemoryLimitBytes uint64  `json:"memory_limit_bytes"`
}

// ResourceSnapshot records component-level memory and CPU at one point in a
// reference run.
type ResourceSnapshot struct {
	CapturedAtUTC    string              `json:"captured_at_utc"`
	Containers       []ContainerResource `json:"containers"`
	TotalMemoryBytes uint64              `json:"total_memory_bytes"`
}

// StatementStatistic is a normalized database statement digest and its
// cumulative counters from the synthetic source database.
type StatementStatistic struct {
	Source                string  `json:"source"`
	StatementID           string  `json:"statement_id"`
	Query                 string  `json:"query"`
	CountStatement        bool    `json:"count_statement"`
	Calls                 uint64  `json:"calls"`
	RowsReturned          uint64  `json:"rows_returned"`
	RowsExamined          uint64  `json:"rows_examined,omitempty"`
	RowsExaminedAvailable bool    `json:"rows_examined_available"`
	TotalExecutionTimeMS  float64 `json:"total_execution_time_ms"`
}

var countStatementPattern = regexp.MustCompile(`(?i)\bcount\s*\(`)

// CaptureResourceSnapshot returns per-container stats from the active
// disposable reference stack.
func (env *Environment) CaptureResourceSnapshot(ctx context.Context) (ResourceSnapshot, error) {
	if env == nil || env.stack == nil {
		return ResourceSnapshot{}, fmt.Errorf("reference resource snapshot is unavailable")
	}
	return env.stack.resourceSnapshot(ctx)
}

// RestartService stops and starts one public gateway process in the isolated
// reference stack. Callers should measure the first successful request too;
// a running container alone does not prove the service is ready for work.
func (env *Environment) RestartService(ctx context.Context, service string) (time.Duration, error) {
	if env == nil || env.stack == nil {
		return 0, fmt.Errorf("reference service restart is unavailable")
	}
	if service != "data-fabric-api" && service != "data-fabric-mcp" {
		return 0, fmt.Errorf("unsupported reference service restart")
	}
	started := time.Now()
	if _, err := env.stack.compose(ctx, "stop", service); err != nil {
		return time.Since(started), fmt.Errorf("stop reference service: %w", err)
	}
	if err := env.stack.up(ctx, service); err != nil {
		return time.Since(started), fmt.Errorf("start reference service: %w", err)
	}
	var err error
	switch service {
	case "data-fabric-api":
		env.APIURL, err = env.stack.url(ctx, service, "8080", "http")
	case "data-fabric-mcp":
		var endpoint string
		endpoint, err = env.stack.url(ctx, service, "8081", "https")
		if err == nil {
			env.MCPURL = endpoint + "/mcp"
		}
	}
	if err != nil {
		return time.Since(started), fmt.Errorf("refresh reference service endpoint: %w", err)
	}
	return time.Since(started), nil
}

// ResetSourceStatementStatistics clears only statement counters in the
// disposable MySQL/PostgreSQL source container. It never connects to metadata.
func (env *Environment) ResetSourceStatementStatistics(ctx context.Context, source string) error {
	if env == nil || env.stack == nil {
		return fmt.Errorf("reference source statistics are unavailable")
	}
	var service, command string
	switch source {
	case "mysql":
		service = "sample-source"
		command = `MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot --database=sample --batch --skip-column-names --execute="TRUNCATE TABLE performance_schema.events_statements_summary_by_digest"`
	case "postgresql":
		service = "sample-postgres"
		command = `PGPASSWORD="$POSTGRES_PASSWORD" psql -U postgres -d sample -At -c "SELECT pg_stat_statements_reset()"`
	default:
		return fmt.Errorf("unsupported reference source statistics target")
	}
	if _, err := env.stack.compose(ctx, "exec", "-T", service, "sh", "-lc", command); err != nil {
		return fmt.Errorf("reset %s statement statistics: %w", source, err)
	}
	return nil
}

// SourceStatementStatistics returns per-digest counters limited to synthetic
// orders/order_items work. MySQL uses Performance Schema; PostgreSQL uses the
// reference-only pg_stat_statements extension.
func (env *Environment) SourceStatementStatistics(ctx context.Context, source string) ([]StatementStatistic, error) {
	if env == nil || env.stack == nil {
		return nil, fmt.Errorf("reference source statistics are unavailable")
	}
	var service, command string
	switch source {
	case "mysql":
		service = "sample-source"
		command = `MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot --database=sample --batch --raw --skip-column-names --execute="SELECT COALESCE(DIGEST, ''), REPLACE(REPLACE(REPLACE(COALESCE(DIGEST_TEXT, ''), CHAR(9), ' '), CHAR(10), ' '), CHAR(13), ' '), COALESCE(COUNT_STAR, 0), COALESCE(SUM_ROWS_SENT, 0), COALESCE(SUM_ROWS_EXAMINED, 0), COALESCE(SUM_TIMER_WAIT, 0) FROM performance_schema.events_statements_summary_by_digest WHERE SCHEMA_NAME = 'sample' AND (DIGEST_TEXT LIKE '%orders%' OR DIGEST_TEXT LIKE '%order_items%') ORDER BY DIGEST_TEXT"`
	case "postgresql":
		service = "sample-postgres"
		command = `PGPASSWORD="$POSTGRES_PASSWORD" psql -U postgres -d sample -At -F '|' -c "SELECT queryid::text, calls::text, rows::text, total_exec_time::text, regexp_replace(query, '[[:space:]]+', ' ', 'g') FROM pg_stat_statements WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database()) AND (query ILIKE '%orders%' OR query ILIKE '%order_items%') AND query NOT ILIKE '%pg_stat_statements%' ORDER BY queryid"`
	default:
		return nil, fmt.Errorf("unsupported reference source statistics target")
	}
	output, err := env.stack.compose(ctx, "exec", "-T", service, "sh", "-lc", command)
	if err != nil {
		return nil, fmt.Errorf("read %s statement statistics: %w", source, err)
	}
	var statistics []StatementStatistic
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		delimiter := "\t"
		if source == "postgresql" {
			delimiter = "|"
		}
		fields := strings.Split(line, delimiter)
		var statistic StatementStatistic
		statistic.Source = source
		if source == "mysql" {
			if len(fields) != 6 {
				return nil, fmt.Errorf("unexpected MySQL statement statistics row")
			}
			statistic.StatementID, statistic.Query = fields[0], fields[1]
			statistic.Calls, err = parseUint(fields[2])
			if err == nil {
				statistic.RowsReturned, err = parseUint(fields[3])
			}
			if err == nil {
				statistic.RowsExamined, err = parseUint(fields[4])
			}
			if err == nil {
				var picoseconds uint64
				picoseconds, err = parseUint(fields[5])
				statistic.TotalExecutionTimeMS = float64(picoseconds) / 1e9
			}
			statistic.RowsExaminedAvailable = true
		} else {
			if len(fields) != 5 {
				return nil, fmt.Errorf("unexpected PostgreSQL statement statistics row")
			}
			statistic.StatementID, statistic.Query = fields[0], fields[4]
			statistic.Calls, err = parseUint(fields[1])
			if err == nil {
				statistic.RowsReturned, err = parseUint(fields[2])
			}
			if err == nil {
				statistic.TotalExecutionTimeMS, err = strconv.ParseFloat(fields[3], 64)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("parse %s statement statistics: %w", source, err)
		}
		statistic.CountStatement = countStatementPattern.MatchString(statistic.Query)
		statistics = append(statistics, statistic)
	}
	return statistics, nil
}

func parseUint(value string) (uint64, error) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, err
	}
	return parsed, nil
}

// SourceConnectionSnapshot reports connections owned by the synthetic
// sample_reader account. Active connections must return to idle (or close) once
// a reference query has finished.
type SourceConnectionSnapshot struct {
	Source string
	Total  int
	Active int
	Idle   int
}

// CaptureSourceConnectionSnapshot reads the connection state from an isolated
// reference source database. It never connects to a user configured source.
func (env *Environment) CaptureSourceConnectionSnapshot(ctx context.Context, source string) (SourceConnectionSnapshot, error) {
	if env == nil || env.stack == nil {
		return SourceConnectionSnapshot{}, fmt.Errorf("reference source connection snapshot is unavailable")
	}
	var service, command string
	quote := string(rune(34))
	var stdin io.Reader
	switch source {
	case "mysql":
		service = "sample-source"
		password := env.stack.env["SAMPLE_READER_PASSWORD"]
		if password == "" {
			return SourceConnectionSnapshot{}, fmt.Errorf("reference MySQL reader credential is unavailable")
		}
		stdin = strings.NewReader(password + string(rune(10)))
		command = "IFS= read -r MYSQL_PWD && export MYSQL_PWD && mysql -usample_reader" +
			" --database=sample --batch --skip-column-names --execute=" + quote +
			"SELECT COUNT(*), COALESCE(SUM(COMMAND <> 'Sleep'), 0) FROM information_schema.PROCESSLIST WHERE USER = 'sample_reader' AND DB = 'sample' AND ID <> CONNECTION_ID()" + quote
	case "postgresql":
		service = "sample-postgres"
		command = "psql -U postgres -d sample -At -F '|' -c " + quote +
			"SELECT COUNT(*), COUNT(*) FILTER (WHERE state IS DISTINCT FROM 'idle') FROM pg_stat_activity WHERE usename = 'sample_reader' AND datname = current_database() AND pid <> pg_backend_pid()" + quote
	default:
		return SourceConnectionSnapshot{}, fmt.Errorf("unsupported reference source connection target")
	}
	output, err := env.stack.composeWithStdin(ctx, stdin, "exec", "-T", service, "sh", "-lc", command)
	if err != nil {
		return SourceConnectionSnapshot{}, fmt.Errorf("read %s source connection state: %w", source, err)
	}
	fields := strings.Fields(strings.TrimSpace(output))
	if source == "postgresql" {
		fields = strings.Split(strings.TrimSpace(output), "|")
	}
	if len(fields) != 2 {
		return SourceConnectionSnapshot{}, fmt.Errorf("unexpected %s source connection snapshot", source)
	}
	first, err := strconv.Atoi(fields[0])
	if err != nil {
		return SourceConnectionSnapshot{}, fmt.Errorf("parse %s source connection count: %w", source, err)
	}
	second, err := strconv.Atoi(fields[1])
	if err != nil {
		return SourceConnectionSnapshot{}, fmt.Errorf("parse %s source connection count: %w", source, err)
	}
	if first < second {
		return SourceConnectionSnapshot{}, fmt.Errorf("%s active source connections exceeded total", source)
	}
	return SourceConnectionSnapshot{Source: source, Total: first, Active: second, Idle: first - second}, nil
}
