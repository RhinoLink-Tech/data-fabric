package unifiedservice

import (
	"context"
	"errors"
	"testing"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/service"
)

type stubPaginationService struct {
	service.QueryService
	resp *model.FetchQueryResponse
	err  error
}

func (s stubPaginationService) FetchQuery(_ context.Context, _ *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	return s.resp, s.err
}

func (s stubPaginationService) FetchNextBatch(_ context.Context, _, _, _ string, _ int) (*model.FetchQueryResponse, error) {
	return s.resp, s.err
}

type stubStreamingService struct {
	service.StreamingService
}

func newTestUnifiedService(resp *model.FetchQueryResponse, err error) UnifiedQueryService {
	return NewUnifiedQueryService(
		stubPaginationService{resp: resp, err: err},
		stubStreamingService{},
		nil,
	)
}

// Every executed fetch query lands in the history with the identity carried
// in the context, for both success and failure (#103).
func TestFetchQueryRecordsHistory(t *testing.T) {
	unified := newTestUnifiedService(&model.FetchQueryResponse{QueryID: "q-1"}, nil)

	if _, err := unified.FetchQuery(service.WithQueryUser(context.Background(), "alice"), &model.FetchQueryRequest{
		DataSourceID: "ds-1",
		SQL:          "SELECT 1",
	}); err != nil {
		t.Fatalf("FetchQuery failed: %v", err)
	}

	page, err := unified.GetQueryHistory(context.Background(), service.QueryHistoryFilter{User: "alice"})
	if err != nil {
		t.Fatalf("GetQueryHistory failed: %v", err)
	}
	if page.Total != 1 || len(page.Records) != 1 {
		t.Fatalf("history page = %d/%d, want 1/1", page.Total, len(page.Records))
	}
	record := page.Records[0]
	if record.QueryID != "q-1" || record.DataSourceID != "ds-1" || record.SQL != "SELECT 1" || record.Status != "success" {
		t.Errorf("record = %+v, want q-1/ds-1/SELECT 1/success", record)
	}
	if record.User != "alice" {
		t.Errorf("record.User = %q, want alice", record.User)
	}

	// The record is scoped: another user cannot see it.
	if page := historyList(t, unified, "bob"); page.Total != 0 {
		t.Errorf("bob sees %d of alice's records, want 0", page.Total)
	}
}

func historyList(t *testing.T, unified UnifiedQueryService, user string) *service.QueryHistoryPage {
	t.Helper()
	page, err := unified.GetQueryHistory(context.Background(), service.QueryHistoryFilter{User: user})
	if err != nil {
		t.Fatalf("GetQueryHistory failed: %v", err)
	}
	return page
}

// Failed queries are recorded as errors, and callers without identity land in
// the anonymous bucket rather than leaking into other users' views (#103).
func TestFetchQueryRecordsErrorsAndAnonymousCallers(t *testing.T) {
	unified := newTestUnifiedService(nil, errors.New("engine unavailable"))

	if _, err := unified.FetchQuery(context.Background(), &model.FetchQueryRequest{
		DataSourceID: "ds-1",
		SQL:          "SELECT 1",
	}); err == nil {
		t.Fatal("FetchQuery unexpectedly succeeded")
	}

	anonymous := historyList(t, unified, "anonymous")
	if anonymous.Total != 1 || anonymous.Records[0].Status != "error" {
		t.Fatalf("anonymous page = %+v, want one error record", anonymous)
	}

	// The error record stays invisible to identified users.
	if page := historyList(t, unified, "alice"); page.Total != 0 {
		t.Errorf("alice sees %d anonymous records, want 0", page.Total)
	}
}
