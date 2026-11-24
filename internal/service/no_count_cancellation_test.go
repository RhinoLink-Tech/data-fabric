package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

// A cancelled no-count continuation must not leave a cursor or a reusable
// token behind, even when its lookahead is waiting on the coordinator.
func TestNoCountStreamingContinuationCancellation(t *testing.T) {
	fetchStarted := make(chan struct{}, 1)
	deleteReceived := make(chan struct{}, 1)
	releaseFetch := make(chan struct{})
	var countCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			sql := string(body)
			switch {
			case strings.Contains(strings.ToUpper(sql), "COUNT(*)"):
				countCalls.Add(1)
				streamCoordinatorResponse(w, "count", "", "[[2]]")
			case strings.TrimSpace(sql) == "SELECT 1":
				streamCoordinatorResponse(w, "ping", "", "[[1]]")
			default:
				streamCoordinatorResponse(w, "no-count-stream", serverURL(r)+"/v1/next/no-count-stream", "[[1],[2]]")
			}
		case http.MethodGet:
			select {
			case fetchStarted <- struct{}{}:
			default:
			}
			select {
			case <-r.Context().Done():
			case <-releaseFetch:
			}
		case http.MethodDelete:
			if r.URL.Path == "/v1/next/no-count-stream" {
				select {
				case deleteReceived <- struct{}{}:
				default:
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":"no-count-stream","stats":{"state":"CANCELED"}}`)
		default:
			http.Error(w, "unexpected request", http.StatusMethodNotAllowed)
		}
	}))
	var pool *database.ConnectionPool
	defer func() {
		close(releaseFetch)
		if pool != nil {
			_ = pool.CloseAll()
		}
		server.Close()
	}()

	svc, p := newStreamingTestService(t, server)
	pool = p
	defer svc.Shutdown()
	svc.admissionLimiter = NewQueryAdmissionLimiter(100, 1)
	source := svc.datasourceRepo.(federationSourceRepository).source
	first, err := svc.FetchWithStreaming(context.Background(), nil, &model.FetchQueryRequest{
		DataSourceID: source.ID, SQL: "SELECT * FROM sales.orders",
		BatchSize: 1, CountMode: model.QueryCountNone,
	})
	if err != nil {
		t.Fatalf("first no-count page: %v", err)
	}
	if first.NextURI == "" || first.HasMore == nil || !*first.HasMore || len(first.Entries) != 1 {
		t.Fatalf("first page lacks the expected continuation: %+v", first)
	}
	if got := countCalls.Load(); got != 0 {
		t.Fatalf("no-count first page issued %d count statements", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := svc.FetchNextBatchWithStreaming(ctx, nil, first.QueryID, first.Slug, first.Token, 1)
		result <- err
	}()
	select {
	case <-fetchStarted:
	case <-time.After(3 * time.Second):
		cancel()
		t.Fatal("no-count lookahead did not reach the coordinator")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("continuation error = %v, want context cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("continuation did not stop after cancellation")
	}
	select {
	case <-deleteReceived:
	case <-time.After(3 * time.Second):
		t.Fatal("coordinator did not receive cancellation")
	}
	if got := len(svc.GetAllStreams()); got != 0 {
		t.Fatalf("cancelled continuation retained %d streams", got)
	}
	if _, err := svc.streamingSessionManager.Get(first.QueryID); err == nil {
		t.Fatal("cancelled continuation retained its session")
	}
	if _, err := svc.FetchNextBatchWithStreaming(context.Background(), nil, first.QueryID, first.Slug, first.Token, 1); err == nil {
		t.Fatal("cancelled continuation token was reusable")
	}
	if got := countCalls.Load(); got != 0 {
		t.Fatalf("no-count continuation issued %d count statements", got)
	}
	release, admitted := svc.acquireStreamAdmission(nil)
	if !admitted {
		t.Fatal("cancelled continuation retained stream admission")
	}
	release()
}
