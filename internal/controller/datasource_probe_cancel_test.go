package controller

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/gin-gonic/gin"
)

// A real PostgreSQL driver starts a handshake against a deliberately stalled
// local peer. No external service or business data is involved.
func TestEditedSourceProbePostgresHandshakeCancellation(t *testing.T) {
	testEditedProbeHandshakeCancellation(t, model.DatabaseTypePostgreSQL)
}

func TestEditedSourceProbeMySQLHandshakeCancellation(t *testing.T) {
	testEditedProbeHandshakeCancellation(t, model.DatabaseTypeMySQL)
}

func testEditedProbeHandshakeCancellation(t *testing.T, sourceType model.DatabaseType) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			if sourceType == model.DatabaseTypePostgreSQL {
				// PostgreSQL sends its startup packet first; wait for it so that
				// cancellation happens during the actual protocol handshake.
				buf := make([]byte, 1)
				if _, err := conn.Read(buf); err != nil {
					conn.Close()
					return
				}
			}
			accepted <- conn
		}
	}()
	source := &model.DataSource{ID: authzTestUUID, Type: sourceType, OwnerID: "owner", Config: model.DataSourceConfig{Host: "127.0.0.1", Port: port, Database: "postgres", Username: "reader", Password: "synthetic-only"}}
	dc := NewDataSourceController(&dataSourceControllerServiceStub{dataSource: source})
	dc.SetConnectionTester(database.NewHealthChecker(database.NewConnectionPool(nil, nil, nil, false)))
	body := fmt.Sprintf(`{"host":"127.0.0.1","port":%s,"database":"postgres","username":"reader"}`, strconv.Itoa(port))
	ctx, _ := authzMutationContext(t, &security.Claims{UserID: "owner"}, body)
	ctx.Params = gin.Params{{Key: "id", Value: authzTestUUID}}
	requestCtx, cancel := context.WithCancel(ctx.Request.Context())
	defer cancel()
	ctx.Request = ctx.Request.WithContext(requestCtx)
	done := make(chan struct{})
	go func() { dc.TestEditedDataSourceConnection(ctx); close(done) }()
	var peer net.Conn
	select {
	case peer = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("driver did not connect")
	}
	defer peer.Close()
	cancel()
	select {
	case <-done:
		if err := peer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, peer); err != nil {
			t.Fatalf("canceled probe left its connection open: %v", err)
		}
	case <-time.After(2 * time.Second):
		peer.Close()
		<-done
		t.Fatalf("canceled request did not stop the %s handshake", sourceType)
	}
}
