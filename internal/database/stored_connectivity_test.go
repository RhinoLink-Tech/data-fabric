package database

import (
	"context"
	"encoding/binary"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"io"
	"net"
	"testing"
	"time"
)

func TestStoredSourceConnectivityBoundsStalledHandshake(t *testing.T) {
	for _, kind := range []model.DatabaseType{model.DatabaseTypePostgreSQL, model.DatabaseTypeMySQL} {
		t.Run(string(kind), func(t *testing.T) {
			if kind == model.DatabaseTypePostgreSQL && postgresProbeDebugAtInit {
				t.Skip("PostgreSQL probe is fail-closed on this platform")
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			stopped := make(chan struct{})
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, e := listener.Accept()
				if e != nil {
					return
				}
				defer conn.Close()
				<-stopped
			}()
			addr := listener.Addr().(*net.TCPAddr)
			source := &model.DataSource{Type: kind, Config: model.DataSourceConfig{Host: "127.0.0.1", Port: addr.Port, Database: "sample", Username: "reader", Password: "synthetic"}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			start := time.Now()
			err = NewHealthChecker(nil).CheckStoredSourceConnectivity(ctx, source)
			close(stopped)
			listener.Close()
			<-done
			if err == nil {
				t.Fatal("stalled handshake reported healthy")
			}
			if elapsed := time.Since(start); elapsed > 4500*time.Millisecond {
				t.Fatalf("handshake exceeded bound: %s", elapsed)
			}
		})
	}
}

// A PostgreSQL server can complete authentication and then stop replying to
// Ping. The absolute socket deadline must survive pq's post-startup reset.
func TestStoredPostgresProbeBoundsUnresponsivePing(t *testing.T) {
	if postgresProbeDebugAtInit {
		t.Skip("PostgreSQL probe is fail-closed on this platform")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	stopped := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		defer conn.Close()
		var length [4]byte
		if _, e = io.ReadFull(conn, length[:]); e != nil {
			return
		}
		n := binary.BigEndian.Uint32(length[:])
		if n < 4 || n > 65536 {
			return
		}
		if _, e = io.CopyN(io.Discard, conn, int64(n-4)); e != nil {
			return
		}
		// AuthenticationOk, BackendKeyData, ReadyForQuery.
		_, e = conn.Write([]byte{'R', 0, 0, 0, 8, 0, 0, 0, 0, 'K', 0, 0, 0, 12, 0, 0, 0, 1, 0, 0, 0, 2, 'Z', 0, 0, 0, 5, 'I'})
		if e != nil {
			return
		}
		<-stopped
	}()
	addr := listener.Addr().(*net.TCPAddr)
	source := &model.DataSource{Type: model.DatabaseTypePostgreSQL, Config: model.DataSourceConfig{Host: "127.0.0.1", Port: addr.Port, Database: "sample", Username: "reader", Password: "synthetic"}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	err = NewHealthChecker(nil).CheckStoredSourceConnectivity(ctx, source)
	close(stopped)
	listener.Close()
	<-done
	if err == nil {
		t.Fatal("unresponsive query reported healthy")
	}
	if elapsed := time.Since(start); elapsed > 4500*time.Millisecond {
		t.Fatalf("query exceeded bound: %s", elapsed)
	}
}
