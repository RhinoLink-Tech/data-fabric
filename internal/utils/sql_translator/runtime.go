package sql_translator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/sqlanalysis"
)

var (
	ErrSQLTranslationDisabled    = errors.New("SQL translation is disabled")
	ErrSQLTranslationOutputLimit = errors.New("SQL translation output exceeded the configured limit")
)

const maxTranslatorStderrBytes = 16 << 10

type RuntimeOptions struct {
	Enabled          bool
	PythonPath       string
	ScriptPath       string
	PythonModulePath string
	Timeout          time.Duration
	MaxConcurrent    int
	MaxSQLBytes      int64
	MaxOutputBytes   int64
}

type contextSQLTranslator interface {
	TranslateContext(context.Context, string, string, string) (string, error)
	ValidateContext(context.Context, string, string) (bool, error)
}

type contextSQLAnalyzer interface {
	AnalyzeContext(context.Context, string, string) (sqlanalysis.Analysis, error)
}

func DefaultRuntimeOptions() RuntimeOptions {
	return RuntimeOptions{
		Enabled:          true,
		PythonPath:       "python3",
		ScriptPath:       "./scripts/sql_translate.py",
		PythonModulePath: "/opt/data-fabric/python",
		Timeout:          5 * time.Second,
		MaxConcurrent:    4,
		MaxSQLBytes:      1 << 20,
		MaxOutputBytes:   1 << 20,
	}
}

func normalizeRuntimeOptions(options RuntimeOptions) RuntimeOptions {
	defaults := DefaultRuntimeOptions()
	if strings.TrimSpace(options.PythonPath) == "" {
		options.PythonPath = defaults.PythonPath
	}
	if strings.TrimSpace(options.ScriptPath) == "" {
		options.ScriptPath = defaults.ScriptPath
	}
	if strings.TrimSpace(options.PythonModulePath) == "" {
		options.PythonModulePath = defaults.PythonModulePath
	}
	if options.Timeout <= 0 {
		options.Timeout = defaults.Timeout
	}
	if options.MaxConcurrent <= 0 {
		options.MaxConcurrent = defaults.MaxConcurrent
	}
	if options.MaxSQLBytes <= 0 {
		options.MaxSQLBytes = defaults.MaxSQLBytes
	}
	if options.MaxOutputBytes <= 0 {
		options.MaxOutputBytes = defaults.MaxOutputBytes
	}
	return options
}

func (e *ExternalSQLTranslator) TranslateContext(ctx context.Context, sql, sourceDialect, targetDialect string) (string, error) {
	if !e.useExternalLib {
		return NewMockSQLTranslator().Translate(sql, sourceDialect, targetDialect)
	}
	responseBytes, err := e.runSQLGlot(ctx, sql, map[string]string{
		"action":         "translate",
		"sql":            sql,
		"source_dialect": strings.ToLower(sourceDialect),
		"target_dialect": strings.ToLower(targetDialect),
	})
	if err != nil {
		return "", err
	}

	var response struct {
		TranslatedSQL string `json:"translated_sql"`
		Error         string `json:"error"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		return "", fmt.Errorf("SQL translation process returned invalid JSON")
	}
	if response.Error != "" {
		return "", &TranslationError{
			SourceDialect: sourceDialect,
			TargetDialect: targetDialect,
			Message:       response.Error,
		}
	}
	if strings.TrimSpace(response.TranslatedSQL) == "" {
		return "", fmt.Errorf("SQL translation process returned an empty result")
	}
	return response.TranslatedSQL, nil
}

// AnalyzeContext parses SQL in the target dialect and returns security-relevant
// AST facts. The same subprocess limits used for translation also apply here.
func (e *ExternalSQLTranslator) AnalyzeContext(ctx context.Context, sql, dialect string) (sqlanalysis.Analysis, error) {
	if !e.useExternalLib {
		return sqlanalysis.Analysis{}, fmt.Errorf("SQL AST analysis requires the SQLGlot runtime")
	}
	responseBytes, err := e.runSQLGlot(ctx, sql, map[string]string{
		"action":  "analyze",
		"sql":     sql,
		"dialect": strings.ToLower(strings.TrimSpace(dialect)),
	})
	if err != nil {
		return sqlanalysis.Analysis{}, err
	}

	var response struct {
		sqlanalysis.Analysis
		Error string `json:"error"`
	}
	if err := json.Unmarshal(responseBytes, &response); err != nil {
		return sqlanalysis.Analysis{}, fmt.Errorf("SQL analysis process returned invalid JSON")
	}
	if response.Error != "" {
		return sqlanalysis.Analysis{}, fmt.Errorf("SQL analysis failed")
	}
	return response.Analysis, nil
}

func (e *ExternalSQLTranslator) runSQLGlot(ctx context.Context, sql string, request any) ([]byte, error) {
	if int64(len(sql)) > e.runtimeOptions.MaxSQLBytes {
		return nil, fmt.Errorf("SQL input exceeds the configured limit of %d bytes", e.runtimeOptions.MaxSQLBytes)
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to encode SQL runtime request")
	}

	runCtx, cancel := context.WithTimeout(ctx, e.runtimeOptions.Timeout)
	defer cancel()

	select {
	case e.concurrent <- struct{}{}:
		defer func() { <-e.concurrent }()
	case <-runCtx.Done():
		return nil, runCtx.Err()
	}

	cmd := exec.CommandContext(runCtx, e.pythonPath, e.scriptPath)
	cmd.WaitDelay = 250 * time.Millisecond
	cmd.Env = e.pythonEnvironment()
	cmd.Stdin = bytes.NewReader(requestBytes)

	stdout := &boundedBuffer{limit: int(e.runtimeOptions.MaxOutputBytes), cancel: cancel}
	stderr := &boundedBuffer{limit: maxTranslatorStderrBytes, cancel: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Run()
	if stdout.exceeded || stderr.exceeded {
		return nil, ErrSQLTranslationOutputLimit
	}
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("SQL parser process timed out: %w", context.DeadlineExceeded)
		}
		if errors.Is(runCtx.Err(), context.Canceled) {
			return nil, runCtx.Err()
		}
		return nil, fmt.Errorf("SQL parser process failed")
	}
	return stdout.Bytes(), nil
}

func (e *ExternalSQLTranslator) ValidateContext(ctx context.Context, sql, dialect string) (bool, error) {
	if _, err := e.AnalyzeContext(ctx, sql, dialect); err != nil {
		return false, err
	}
	return true, nil
}

func (e *ExternalSQLTranslator) pythonEnvironment() []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"PYTHONPATH=" + e.runtimeOptions.PythonModulePath,
		"PYTHONUNBUFFERED=1",
		"PYTHONNOUSERSITE=1",
	}
}

// ValidateRuntime fails fast when the configured interpreter, script, or SQLGlot
// installation is missing. It runs before the metadata database is opened.
func (e *ExternalSQLTranslator) ValidateRuntime() error {
	if _, err := exec.LookPath(e.pythonPath); err != nil {
		return fmt.Errorf("SQL translation Python interpreter is unavailable")
	}
	if info, err := os.Stat(e.scriptPath); err != nil || info.IsDir() {
		return fmt.Errorf("SQL translation script is unavailable at %s", e.scriptPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, e.pythonPath, "-c", "import sqlglot")
	cmd.Env = e.pythonEnvironment()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SQL translation dependency sqlglot is unavailable")
	}
	return nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	cancel   context.CancelFunc
	exceeded bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - b.buffer.Len()
	if remaining < 0 {
		remaining = 0
	}
	if len(p) > remaining {
		if remaining > 0 {
			_, _ = b.buffer.Write(p[:remaining])
		}
		b.exceeded = true
		b.cancel()
		return remaining, ErrSQLTranslationOutputLimit
	}
	return b.buffer.Write(p)
}

func (b *boundedBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}

func NewExternalSQLTranslatorWithOptions(options RuntimeOptions) *ExternalSQLTranslator {
	options = normalizeRuntimeOptions(options)
	return &ExternalSQLTranslator{
		supportedDialects: []string{
			string(MySQL), string(PostgreSQL), string(Trino), string(Presto), string(OpenLooKeng), string(Spark),
			string(Oracle), string(SQLServer), string(Snowflake), string(SQLite),
			string(DuckDB), string(Redshift),
		},
		useExternalLib: true,
		runtimeOptions: options,
		pythonPath:     options.PythonPath,
		scriptPath:     options.ScriptPath,
		concurrent:     make(chan struct{}, options.MaxConcurrent),
	}
}

func SQLTranslationRuntimeOptionsFromConfig(enabled bool, pythonPath, scriptPath, pythonModulePath string, timeout time.Duration, maxConcurrent int, maxSQLBytes, maxOutputBytes int64) RuntimeOptions {
	return normalizeRuntimeOptions(RuntimeOptions{
		Enabled:          enabled,
		PythonPath:       pythonPath,
		ScriptPath:       scriptPath,
		PythonModulePath: pythonModulePath,
		Timeout:          timeout,
		MaxConcurrent:    maxConcurrent,
		MaxSQLBytes:      maxSQLBytes,
		MaxOutputBytes:   maxOutputBytes,
	})
}
