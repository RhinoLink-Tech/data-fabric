package domestic

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

// The shipped build must not advertise a working native driver by accident.
func TestDaMengNativeDriverUnavailable(t *testing.T) {
	for _, name := range sql.Drivers() {
		if name == "dameng" {
			t.Fatal("native DaMeng driver is now registered; add a pinned live contract and reassess its support status")
		}
	}
	db, err := (&DaMengDriver{}).Open("synthetic-secret")
	if db != nil || !errors.Is(err, ErrDaMengDriverUnavailable) {
		t.Fatalf("db=%v err=%v", db, err)
	}
	if strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatal("connection error exposed the DSN")
	}
}
