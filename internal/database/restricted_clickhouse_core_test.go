//go:build datafabric_core

package database

import (
	"errors"
	"testing"
)

func TestCoreRejectsClickHouseGate(t *testing.T) {
	_, err := NewFixedIPEgressPolicyWithClickHouse([]string{"127.0.0.1/32"}, []int{9440}, "", false, false, false, true)
	if !errors.Is(err, ErrEgressDenied) {
		t.Fatal("Core build accepted ClickHouse gate")
	}
}
