package traditional

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// An isolated proxy forwards startup to the real contract server and holds its
// authentication response until the request context has been canceled.
func verifyPostgresHealthCancellation(t *testing.T, driver *PostgreSQLDriver, cfg *model.DataSourceConfig) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	ready := make(chan net.Conn, 1)
	errs := make(chan error, 1)
	go func() {
		client, err := listener.Accept()
		if err != nil {
			errs <- err
			return
		}
		upstream, err := net.DialTimeout("tcp", net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)), 3*time.Second)
		if err != nil {
			client.Close()
			errs <- err
			return
		}
		defer upstream.Close()
		_ = upstream.SetDeadline(time.Now().Add(8 * time.Second))
		_ = client.SetDeadline(time.Now().Add(8 * time.Second))
		header := make([]byte, 4)
		if _, err = io.ReadFull(client, header); err != nil {
			client.Close()
			errs <- err
			return
		}
		length := binary.BigEndian.Uint32(header)
		if length < 8 || length > 65536 {
			client.Close()
			errs <- fmt.Errorf("invalid PostgreSQL startup packet")
			return
		}
		packet := make([]byte, length)
		copy(packet, header)
		if _, err = io.ReadFull(client, packet[4:]); err != nil {
			client.Close()
			errs <- err
			return
		}
		if _, err = upstream.Write(packet); err != nil {
			client.Close()
			errs <- err
			return
		}
		var response [1]byte
		if _, err = io.ReadFull(upstream, response[:]); err != nil {
			client.Close()
			errs <- err
			return
		}
		if response[0] != 'R' {
			client.Close()
			errs <- fmt.Errorf("unexpected PostgreSQL startup response")
			return
		}
		ready <- client
	}()
	local := *cfg
	local.Host = "127.0.0.1"
	local.Port = listener.Addr().(*net.TCPAddr).Port
	local.SSL = false
	local.SSLMode = "disable"
	local.SSLRootCert = ""
	db, err := driver.OpenForHealthCheck(driver.BuildDSN(&local))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- db.PingContext(ctx) }()
	var peer net.Conn
	select {
	case peer = <-ready:
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(9 * time.Second):
		t.Fatal("PostgreSQL handshake did not reach proxy")
	}
	defer peer.Close()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled health check succeeded")
		}
	case <-time.After(2 * time.Second):
		peer.Close()
		<-done
		t.Fatal("canceled health check did not return")
	}
	if _, err := io.Copy(io.Discard, peer); err != nil {
		t.Fatalf("health connection remained open: %v", err)
	}
}
