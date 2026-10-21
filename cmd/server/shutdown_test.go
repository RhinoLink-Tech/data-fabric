package main

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
)

// closingMetadataConn counts Close calls so shutdown tests can observe that
// the metadata database was actually released.
type closingMetadataConn struct{ closes *atomic.Int32 }

func (closingMetadataConn) Prepare(string) (sqldriver.Stmt, error) {
	return nil, errors.New("prepared statements are not supported")
}
func (c closingMetadataConn) Close() error {
	c.closes.Add(1)
	return nil
}
func (closingMetadataConn) Begin() (sqldriver.Tx, error) {
	return nil, errors.New("transactions unused")
}

type closingMetadataConnector struct{ closes *atomic.Int32 }

func (c closingMetadataConnector) Connect(context.Context) (sqldriver.Conn, error) {
	return closingMetadataConn{closes: c.closes}, nil
}
func (closingMetadataConnector) Driver() sqldriver.Driver { return closingMetadataDriver{} }

type closingMetadataDriver struct{}

func (closingMetadataDriver) Open(string) (sqldriver.Conn, error) {
	return nil, errors.New("must be used through the connector")
}

// fakeMetadataDB hands out the *sql.DB under a gorm-shaped handle.
type fakeMetadataDB struct{ db *sql.DB }

func (f fakeMetadataDB) DB() (*sql.DB, error) { return f.db, nil }

// closeGatewayResources must release the metadata database (open connections
// closed exactly once) and tolerate nil handles without panicking (#32).
func TestCloseGatewayResourcesReleasesMetadataDB(t *testing.T) {
	var closes atomic.Int32
	db := sql.OpenDB(closingMetadataConnector{closes: &closes})
	defer db.Close()
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("prepare fake metadata connection: %v", err)
	}

	closeGatewayResources(fakeMetadataDB{db: db}, nil)

	if got := closes.Load(); got != 1 {
		t.Errorf("close count = %d, want 1 (the opened physical connection)", got)
	}
	if err := db.PingContext(context.Background()); err == nil || !strings.Contains(err.Error(), "database is closed") {
		t.Errorf("metadata DB still usable after shutdown close: %v", err)
	}
}

// Nil arguments must not panic: startup paths that failed before a resource
// existed still unwind through the same defer.
func TestCloseGatewayResourcesToleratesNilHandles(t *testing.T) {
	closeGatewayResources(nil, nil)
	closeGatewayResources(nil, database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false))
}
