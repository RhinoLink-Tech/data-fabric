package config

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"gorm.io/gorm/logger"
)

// captureStreams runs fn with os.Stdout and os.Stderr pointing at pipes and
// returns what each stream received.
//
// The two streams are process-wide, so a test that uses this must not run in
// parallel. The writes under test are a few lines, well inside the pipe
// buffer, and both pipes are drained after fn returns.
func captureStreams(t *testing.T, fn func()) (stdout, stderr string) {
	t.Helper()
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = origOut, origErr }()

	fn()

	outW.Close()
	errW.Close()
	outBytes, _ := io.ReadAll(outR)
	errBytes, _ := io.ReadAll(errR)
	outR.Close()
	errR.Close()

	return string(outBytes), string(errBytes)
}

// TestLoadKeepsStdoutClean locks the first half of #535: an MCP stdio client
// reads stdout as a stream of protocol frames, so the missing-config notice
// must reach stderr instead. Before the fix, Load printed it with
// fmt.Println and a stdio session met prose where a frame should be.
func TestLoadKeepsStdoutClean(t *testing.T) {
	t.Chdir(t.TempDir()) // no config file here, so Load takes the fallback path
	t.Setenv("MCP_ENABLED", "true")
	t.Setenv("MCP_TRANSPORT", "stdio")

	var loadErr error
	stdout, stderr := captureStreams(t, func() {
		_, loadErr = Load()
	})
	if loadErr != nil {
		t.Fatalf("Load() from an empty directory: %v", loadErr)
	}

	if got := strings.TrimSpace(stdout); got != "" {
		t.Errorf("stdout = %q, want nothing: stdout is the MCP stdio protocol stream", got)
	}
	if !strings.Contains(stderr, "Config file not found") {
		t.Errorf("stderr = %q, want the missing-config notice", stderr)
	}
}

// TestMetadataLoggerWritesToStderr locks the second half of #535: the GORM
// logger InitDatabase installs is the package default unless it is rebuilt,
// and logger.Default captures os.Stdout as its writer. Its Warn and Error
// lines therefore landed inside the protocol stream whenever the metadata
// database reported one.
func TestMetadataLoggerWritesToStderr(t *testing.T) {
	var l logger.Interface
	stdout, stderr := captureStreams(t, func() {
		// Built inside the capture: the logger holds the os.Stderr it saw at
		// construction, exactly as InitDatabase does at startup.
		l = metadataLogger(logger.Warn)
		l.Warn(context.Background(), "metadata diagnostic %s", "synthetic")
	})

	if got := strings.TrimSpace(stdout); got != "" {
		t.Errorf("stdout = %q, want nothing: stdout is the MCP stdio protocol stream", got)
	}
	if !strings.Contains(stderr, "metadata diagnostic synthetic") {
		t.Errorf("stderr = %q, want the diagnostic", stderr)
	}
}

// TestLogLevelMapping keeps the configured level meaning what it did through
// logger.Default's LogMode, since the logger is now built rather than reused.
func TestLogLevelMapping(t *testing.T) {
	cases := []struct {
		configured string
		want       logger.LogLevel
	}{
		{"debug", logger.Info},
		{"info", logger.Warn},
		{"warn", logger.Error},
		{"error", logger.Silent},
		{"", logger.Warn},
		{"trace", logger.Warn},
	}
	for _, c := range cases {
		if got := logLevel(c.configured); got != c.want {
			t.Errorf("logLevel(%q) = %v, want %v", c.configured, got, c.want)
		}
	}
}
