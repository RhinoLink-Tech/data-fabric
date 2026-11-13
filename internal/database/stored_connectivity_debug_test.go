package database

import (
	"context"
	"encoding/binary"
	"fmt"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

const probeDebugTestSecret = "synthetic-probe-password-marker"

func TestPostgresProbeDebugChild(t *testing.T) {
	mode := os.Getenv("DF_PROBE_DEBUG_CHILD")
	if mode == "" {
		return
	}
	port, err := strconv.Atoi(os.Getenv("DF_PROBE_DEBUG_PORT"))
	if err != nil {
		t.Fatal(err)
	}
	if mode == "unreadable" {
		postgresProbeDebugAtInit = unsafePostgresDebugFromEnvironment("linux", func(string) (io.ReadCloser, error) { return nil, os.ErrPermission })
	}
	if mode == "unavailable" {
		postgresProbeDebugAtInit = true
	}
	// pq captured the process-start value already. Simulate later unsetenv.
	if err = os.Unsetenv("PQGO_DEBUG"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = NewHealthChecker(nil).CheckStoredSourceConnectivity(ctx, &model.DataSource{Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Host: "127.0.0.1", Port: port, Database: "sample", Username: "reader", Password: probeDebugTestSecret}})
	if mode != "normal" && err == nil {
		t.Fatal("debug mode did not fail closed")
	}
	if mode == "normal" && err != nil {
		t.Fatalf("normal probe failed: %v", err)
	}
}
func TestPostgresProbeDebugCannotLeakPassword(t *testing.T) {
	modes := []string{"debug", "unreadable"}
	if !postgresProbeDebugAtInit {
		modes = append(modes, "normal")
	} else {
		modes = append(modes, "unavailable")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer l.Close()
			listener := l.(*net.TCPListener)
			listener.SetDeadline(time.Now().Add(4 * time.Second))
			seen := make(chan error, 1)
			go func() {
				c, e := listener.Accept()
				if e != nil {
					seen <- e
					return
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				var h [4]byte
				if _, e = io.ReadFull(c, h[:]); e != nil {
					seen <- e
					return
				}
				n := binary.BigEndian.Uint32(h[:])
				if n < 4 || n > 65536 {
					seen <- fmt.Errorf("invalid startup")
					return
				}
				if _, e = io.CopyN(io.Discard, c, int64(n-4)); e != nil {
					seen <- e
					return
				}
				if _, e = c.Write([]byte{'R', 0, 0, 0, 8, 0, 0, 0, 3}); e != nil {
					seen <- e
					return
				}
				var head [5]byte
				if _, e = io.ReadFull(c, head[:]); e != nil {
					seen <- e
					return
				}
				n = binary.BigEndian.Uint32(head[1:])
				if head[0] != 'p' || n < 4 || n > 1024 {
					seen <- fmt.Errorf("invalid password packet")
					return
				}
				payload := make([]byte, n-4)
				if _, e = io.ReadFull(c, payload); e != nil {
					seen <- e
					return
				}
				if string(payload) != probeDebugTestSecret+"\x00" {
					seen <- fmt.Errorf("wrong synthetic password")
					return
				}
				if _, e = c.Write([]byte{'R', 0, 0, 0, 8, 0, 0, 0, 0, 'K', 0, 0, 0, 12, 0, 0, 0, 1, 0, 0, 0, 2, 'Z', 0, 0, 0, 5, 'I'}); e != nil {
					seen <- e
					return
				}
				if _, e = io.ReadFull(c, head[:]); e != nil {
					seen <- e
					return
				}
				n = binary.BigEndian.Uint32(head[1:])
				if n < 4 || n > 1024 {
					seen <- fmt.Errorf("invalid query")
					return
				}
				if _, e = io.CopyN(io.Discard, c, int64(n-4)); e != nil {
					seen <- e
					return
				}
				_, e = c.Write([]byte{'I', 0, 0, 0, 4, 'Z', 0, 0, 0, 5, 'I'})
				seen <- e
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPostgresProbeDebugChild$", "-test.v")
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "PQGO_DEBUG=") && !strings.HasPrefix(entry, "DF_PROBE_DEBUG_") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			debugValue := "0"
			if mode == "debug" {
				debugValue = "1"
			}
			cmd.Env = append(cmd.Env, "PQGO_DEBUG="+debugValue, "DF_PROBE_DEBUG_CHILD="+mode, fmt.Sprintf("DF_PROBE_DEBUG_PORT=%d", l.Addr().(*net.TCPAddr).Port))
			output, e := cmd.CombinedOutput()
			if strings.Contains(string(output), probeDebugTestSecret) {
				t.Fatal("synthetic credential leaked to process output")
			}
			if e != nil {
				t.Fatalf("child failed: %v; output %s", e, output)
			}
			if mode != "normal" {
				listener.Close()
				listener.SetDeadline(time.Now())
				if e = <-seen; e == nil {
					t.Fatal("debug probe unexpectedly reached server")
				}
			} else if e = <-seen; e != nil {
				t.Fatalf("normal probe did not complete: %v", e)
			}
		})
	}
}

func TestPostgresProbeRejectsDebugEnabledAfterStartup(t *testing.T) {
	t.Setenv("PQGO_DEBUG", "1")
	source := &model.DataSource{Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Host: "127.0.0.1", Port: 5432, Database: "sample", Username: "reader", Password: probeDebugTestSecret}}
	err := NewHealthChecker(nil).CheckStoredSourceConnectivity(context.Background(), source)
	if err == nil || !strings.Contains(err.Error(), "debug") {
		t.Fatal("current debug setting must fail closed")
	}
}

func TestPostgresProbeStartupEnvironmentFailClosed(t *testing.T) {
	errorOpen := func(string) (io.ReadCloser, error) { return nil, os.ErrPermission }
	if !unsafePostgresDebugFromEnvironment("windows", errorOpen) {
		t.Fatal("non-Linux must fail closed")
	}
	if !unsafePostgresDebugFromEnvironment("linux", errorOpen) {
		t.Fatal("unreadable proc must fail closed")
	}
	for _, tc := range []struct {
		env  string
		want bool
	}{
		{"USER=test\x00PQGO_DEBUG=1\x00", true},
		{"USER=test\x00PQGO_DEBUG=0\x00", false},
		{strings.Repeat("x", (1<<20)+1), true},
	} {
		opener := func(string) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(tc.env)), nil }
		if got := unsafePostgresDebugFromEnvironment("linux", opener); got != tc.want {
			t.Fatalf("startup env: got %v want %v", got, tc.want)
		}
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("DF_TEST_PROC_UNREADABLE") == "1" {
		postgresProbeDebugAtInit = unsafePostgresDebugFromEnvironment("linux", func(string) (io.ReadCloser, error) { return nil, os.ErrPermission })
	}
	os.Exit(m.Run())
}
