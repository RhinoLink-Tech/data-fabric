package database

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

// When a candidate pool is installed over a pool built from a different
// fingerprint, the displaced pool must be closed rather than leaked.
func TestCreateConnectionClosesDisplacedPool(t *testing.T) {
	registry := NewDriverRegistry()
	registry.RegisterDriver(stubPoolTestType, stubPoolDriver{})
	pool := NewConnectionPool(registry, nil, nil, false)

	displaced := sql.OpenDB(pingableConnector{})
	pool.pools["ds-1"] = displaced
	pool.fingerprints["ds-1"] = "old-fingerprint"

	ds := stubPoolDataSource("ds-1")
	db, err := pool.createConnection(context.Background(), ds, "new-fingerprint", 0, time.Time{})
	if err != nil {
		t.Fatalf("createConnection: %v", err)
	}
	defer db.Close()

	if err := displaced.PingContext(context.Background()); err == nil {
		t.Fatalf("displaced pool was not closed: ping still succeeds")
	}
}
