package controller

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
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

// Forward a PostgreSQL startup packet to a real isolated server, then hold its
// authentication response so cancellation occurs during an in-flight handshake.
func TestEditedSourceProbePostgresLiveCancellation(t *testing.T) {
	port := os.Getenv("DATA_FABRIC_EDIT_PROBE_PG_PORT")
	if port == "" {
		t.Skip("no isolated PostgreSQL fixture configured")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ready := make(chan net.Conn, 1)
	proxyErrors := make(chan error, 1)
	go func() {
		client, err := listener.Accept()
		if err != nil {
			proxyErrors <- err
			return
		}
		upstream, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), 3*time.Second)
		if err != nil {
			client.Close()
			proxyErrors <- err
			return
		}
		defer upstream.Close()
		_ = upstream.SetDeadline(time.Now().Add(5 * time.Second))
		_ = client.SetDeadline(time.Now().Add(5 * time.Second))
		header := make([]byte, 4)
		if _, err = io.ReadFull(client, header); err != nil {
			client.Close()
			proxyErrors <- err
			return
		}
		length := binary.BigEndian.Uint32(header)
		if length < 8 || length > 65536 {
			client.Close()
			proxyErrors <- fmt.Errorf("invalid startup length")
			return
		}
		packet := make([]byte, length)
		copy(packet, header)
		if _, err = io.ReadFull(client, packet[4:]); err != nil {
			client.Close()
			proxyErrors <- err
			return
		}
		if _, err = upstream.Write(packet); err != nil {
			client.Close()
			proxyErrors <- err
			return
		}
		var response [1]byte
		if _, err = io.ReadFull(upstream, response[:]); err != nil {
			client.Close()
			proxyErrors <- err
			return
		}
		if response[0] != 'R' {
			client.Close()
			proxyErrors <- fmt.Errorf("unexpected PostgreSQL response")
			return
		}
		ready <- client
	}()
	proxyPort := listener.Addr().(*net.TCPAddr).Port
	source := &model.DataSource{ID: authzTestUUID, Type: model.DatabaseTypePostgreSQL, OwnerID: "owner", Config: model.DataSourceConfig{Host: "127.0.0.1", Port: proxyPort, Database: "data_fabric_contract", Username: "data_fabric_contract", Password: "synthetic-only"}}
	dc := NewDataSourceController(&dataSourceControllerServiceStub{dataSource: source})
	dc.SetConnectionTester(database.NewHealthChecker(database.NewConnectionPool(nil, nil, nil, false)))
	body := fmt.Sprintf(`{"host":"127.0.0.1","port":%d,"database":"data_fabric_contract","username":"data_fabric_contract"}`, proxyPort)
	ctx, _ := authzMutationContext(t, &security.Claims{UserID: "owner"}, body)
	ctx.Params = gin.Params{{Key: "id", Value: authzTestUUID}}
	requestCtx, cancel := context.WithCancel(ctx.Request.Context())
	defer cancel()
	ctx.Request = ctx.Request.WithContext(requestCtx)
	done := make(chan struct{})
	go func() { dc.TestEditedDataSourceConnection(ctx); close(done) }()
	var peer net.Conn
	select {
	case peer = <-ready:
	case err := <-proxyErrors:
		t.Fatal(err)
	case <-time.After(6 * time.Second):
		t.Fatal("real PostgreSQL handshake did not reach proxy")
	}
	defer peer.Close()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		peer.Close()
		<-done
		t.Fatal("canceled probe did not stop")
	}
	if _, err := io.Copy(io.Discard, peer); err != nil {
		t.Fatalf("probe did not close its connection: %v", err)
	}
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
