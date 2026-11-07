package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func budgetFixturePage(value interface{}) ([]model.ColumnInfo, []*[]interface{}) {
	columns := []model.ColumnInfo{{Name: "id", Type: model.TypeInteger}}
	row := []interface{}{value}
	return columns, []*[]interface{}{&row}
}

func TestGatewayQueryServicesCapBudgetDurationToSessionTTL(t *testing.T) {
	pagination, streaming := NewGatewayQueryServicesWithBudgets(
		nil, nil, false, nil, nil, nil, 8*time.Second,
		QueryBudgetLimits{MaxDuration: 5 * time.Minute}, nil,
	)
	t.Cleanup(func() {
		pagination.Shutdown()
		_ = streaming.Shutdown()
	})
	paginationService := pagination.(*queryService)
	streamingService := streaming.(*streamingService)
	if got := paginationService.budgetLimits.MaxDuration; got != 8*time.Second {
		t.Fatalf("pagination query budget = %s, want session TTL 8s", got)
	}
	if got := streamingService.budgetLimits.MaxDuration; got != 8*time.Second {
		t.Fatalf("streaming query budget = %s, want session TTL 8s", got)
	}
}

func TestQueryBudgetAccountsCumulativeRowsAndEncodedBytesAtExactBoundary(t *testing.T) {
	columns, first := budgetFixturePage(int64(1))
	_, second := budgetFixturePage(int64(2))

	probe := newQueryBudgetState(time.Now(), DefaultQueryBudgetLimits())
	if err := probe.accountPage(columns, first); err != nil {
		t.Fatalf("account first page: %v", err)
	}
	if err := probe.accountPage(columns, second); err != nil {
		t.Fatalf("account second page: %v", err)
	}

	budget := newQueryBudgetState(time.Now(), QueryBudgetLimits{
		MaxRows:     2,
		MaxBytes:    probe.bytes,
		MaxDuration: time.Minute,
	})
	if err := budget.accountPage(columns, first); err != nil {
		t.Fatalf("first page at exact cumulative boundary: %v", err)
	}
	if err := budget.accountPage(columns, second); err != nil {
		t.Fatalf("second page at exact cumulative boundary: %v", err)
	}
	if budget.rows != 2 || budget.bytes != probe.bytes {
		t.Fatalf("accounted rows/bytes = %d/%d, want 2/%d", budget.rows, budget.bytes, probe.bytes)
	}
}

func TestQueryBudgetRejectsOverLimitPageWithoutAccountingAnyOfIt(t *testing.T) {
	columns, page := budgetFixturePage("value")
	probe := newQueryBudgetState(time.Now(), DefaultQueryBudgetLimits())
	if err := probe.accountPage(columns, page); err != nil {
		t.Fatalf("measure fixture page: %v", err)
	}
	firstRow := []interface{}{"first"}
	secondRow := []interface{}{"second"}
	twoRows := []*[]interface{}{&firstRow, &secondRow}

	tests := []struct {
		name    string
		limits  QueryBudgetLimits
		entries []*[]interface{}
	}{
		{name: "row limit", limits: QueryBudgetLimits{MaxRows: 1, MaxBytes: probe.bytes + 100, MaxDuration: time.Minute}, entries: twoRows},
		{name: "byte limit", limits: QueryBudgetLimits{MaxRows: 10, MaxBytes: probe.bytes - 1, MaxDuration: time.Minute}, entries: page},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			budget := newQueryBudgetState(time.Now(), tc.limits)
			if err := budget.accountPage(columns, tc.entries); !errors.Is(err, ErrQueryResultLimitExceeded) {
				t.Fatalf("accountPage() error = %v, want result limit error", err)
			}
			if budget.rows != 0 || budget.bytes != 0 {
				t.Fatalf("over-limit page changed counters to %d/%d", budget.rows, budget.bytes)
			}
		})
	}
}

func TestQueryBudgetDeadlineIsAbsoluteAndContextBound(t *testing.T) {
	started := time.Now().Add(-2 * time.Second)
	budget := newQueryBudgetState(started, QueryBudgetLimits{MaxDuration: time.Second})
	if !budget.expired(time.Now()) {
		t.Fatal("budget should be expired from the original start time")
	}
	ctx, cancel := budget.withDeadline(context.Background())
	defer cancel()
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("deadline context error = %v, want deadline exceeded", ctx.Err())
	}
}
