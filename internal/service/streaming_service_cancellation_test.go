package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

func newStreamingTestService(t *testing.T, server *httptest.Server) (*streamingService, *database.ConnectionPool) {
	t.Helper()
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(endpoint.Port())
	if err != nil {
		t.Fatal(err)
	}
	source := &model.DataSource{
		ID: "d377b04b-78b7-4f8f-9fd6-6cf29e5581f1", Name: "federation",
		Type: model.DatabaseTypeTrino, Status: model.DataSourceStatusActive,
		Config: model.DataSourceConfig{Host: endpoint.Hostname(), Port: port, Database: "lakehouse", Schema: "sales", Username: "data-fabric"},
	}
	database.GetDriverRegistry()
	pool := database.NewConnectionPool(database.NewDriverRegistry(), nil, nil, false)
	svc := NewStreamingService(federationSourceRepository{source: source}, pool, newTestSQLTranslationManager()).(*streamingService)
	return svc, pool
}

func streamCoordinatorResponse(w http.ResponseWriter, id, nextURI string, rows string) {
	w.Header().Set("Content-Type", "application/json")
	if nextURI == "" {
		_, _ = fmt.Fprintf(w, "{\"id\":%q,\"columns\":[{\"name\":\"result\",\"type\":\"bigint\",\"typeSignature\":{\"rawType\":\"bigint\"}}],\"data\":%s,\"stats\":{\"state\":\"FINISHED\"}}", id, rows)
		return
	}
	_, _ = fmt.Fprintf(w, "{\"id\":%q,\"nextUri\":%q,\"columns\":[{\"name\":\"result\",\"type\":\"bigint\",\"typeSignature\":{\"rawType\":\"bigint\"}}],\"data\":%s,\"stats\":{\"state\":\"RUNNING\"}}", id, nextURI, rows)
}

func TestExecuteStreamQueryHonorsRequestDeadline(t *testing.T) {
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "unexpected request", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "slow_table") {
			started <- struct{}{}
			<-r.Context().Done()
			return
		}
		streamCoordinatorResponse(w, "ping", "", "[[1]]")
	}))
	defer server.Close()

	svc, pool := newStreamingTestService(t, server)
	defer func() { _ = pool.CloseAll() }()
	_, _, err := svc.ExecuteStreamQuery(context.Background(), nil, &model.QueryRequest{
		DataSourceID: svc.datasourceRepo.(federationSourceRepository).source.ID,
		SQL:          "SELECT * FROM slow_table",
		Timeout:      1,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ExecuteStreamQuery() error = %v, want context deadline exceeded", err)
	}
	select {
	case <-started:
	default:
		t.Fatal("query statement was not sent to the coordinator")
	}
	if got := len(svc.GetAllStreams()); got != 0 {
		t.Fatalf("timed out query left %d active streams", got)
	}
}

func TestFetchStreamChunkCancellationDeletesCoordinatorQuery(t *testing.T) {
	fetchStarted := make(chan struct{}, 1)
	deleteReceived := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			query := string(body)
			switch {
			case strings.Contains(strings.ToUpper(query), "COUNT(*)"):
				streamCoordinatorResponse(w, "count", "", "[[2]]")
			case strings.TrimSpace(query) == "SELECT 1":
				streamCoordinatorResponse(w, "ping", "", "[[1]]")
			default:
				streamCoordinatorResponse(w, "stream-query", serverURL(r)+"/v1/next/stream-query", "[[1]]")
			}
		case http.MethodGet:
			fetchStarted <- struct{}{}
			<-r.Context().Done()
		case http.MethodDelete:
			deleteReceived <- struct{}{}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "stream-query", "stats": map[string]string{"state": "CANCELED"}})
		default:
			http.Error(w, "unexpected request", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	svc, pool := newStreamingTestService(t, server)
	defer func() { _ = pool.CloseAll() }()
	streamID, total, err := svc.ExecuteStreamQuery(context.Background(), nil, &model.QueryRequest{
		DataSourceID: svc.datasourceRepo.(federationSourceRepository).source.ID,
		SQL:          "SELECT * FROM sales.orders",
	})
	if err != nil {
		t.Fatalf("ExecuteStreamQuery() error = %v", err)
	}
	if total != 2 {
		t.Fatalf("row count = %d, want 2", total)
	}
	svc.streamingSessionManager.Store(&StreamingSession{QueryID: "cancel-session", StreamID: streamID})

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := svc.FetchStreamChunk(ctx, streamID, 2)
		result <- err
	}()
	select {
	case <-fetchStarted:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("coordinator result fetch did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("FetchStreamChunk() error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FetchStreamChunk did not stop after cancellation")
	}
	select {
	case <-deleteReceived:
	case <-time.After(2 * time.Second):
		t.Fatal("coordinator did not receive query cancellation DELETE")
	}
	if got := len(svc.GetAllStreams()); got != 0 {
		t.Fatalf("cancelled query left %d active streams", got)
	}
	if _, err := svc.streamingSessionManager.Get("cancel-session"); err == nil {
		t.Fatal("cancelled query left its streaming session")
	}
}

func TestExecuteStreamQueryCleansUpWhenCountFails(t *testing.T) {
	deleteReceived := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			query := string(body)
			switch {
			case strings.Contains(strings.ToUpper(query), "COUNT(*)"):
				http.Error(w, "count failed", http.StatusBadRequest)
			case strings.TrimSpace(query) == "SELECT 1":
				streamCoordinatorResponse(w, "ping", "", "[[1]]")
			default:
				streamCoordinatorResponse(w, "stream-query", serverURL(r)+"/v1/next/stream-query", "[[1]]")
			}
		case http.MethodDelete:
			deleteReceived <- struct{}{}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "stream-query", "stats": map[string]string{"state": "CANCELED"}})
		default:
			http.Error(w, "unexpected request", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	svc, pool := newStreamingTestService(t, server)
	defer func() { _ = pool.CloseAll() }()
	_, _, err := svc.ExecuteStreamQuery(context.Background(), nil, &model.QueryRequest{
		DataSourceID: svc.datasourceRepo.(federationSourceRepository).source.ID,
		SQL:          "SELECT * FROM sales.orders",
	})
	if err == nil {
		t.Fatal("ExecuteStreamQuery() succeeded despite count failure")
	}
	select {
	case <-deleteReceived:
	case <-time.After(2 * time.Second):
		t.Fatal("query failure did not cancel the active coordinator query")
	}
	if got := len(svc.GetAllStreams()); got != 0 {
		t.Fatalf("failed query left %d active streams", got)
	}
}

func serverURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func TestCleanupInactiveStreamsCancelsAndRemovesSessions(t *testing.T) {
	cancelled := false
	stream := &ActiveStream{
		ID:             "idle-stream",
		LastActivityAt: time.Now().Add(-2 * time.Minute),
		CancelFunc:     func() { cancelled = true },
	}
	session := &StreamingSession{QueryID: "idle-session", StreamID: stream.ID}
	svc := &streamingService{
		activeStreams: map[string]*ActiveStream{stream.ID: stream},
		streamTimeout: time.Minute,
		streamingSessionManager: &StreamingSessionManager{
			sessions: map[string]*StreamingSession{session.QueryID: session},
		},
	}

	if cleaned := svc.CleanupInactiveStreams(); cleaned != 1 {
		t.Fatalf("CleanupInactiveStreams() = %d, want 1", cleaned)
	}
	if !cancelled {
		t.Fatal("idle stream context was not cancelled")
	}
	if len(svc.GetAllStreams()) != 0 {
		t.Fatal("idle stream remains active after cleanup")
	}
	if _, err := svc.streamingSessionManager.Get(session.QueryID); err == nil {
		t.Fatal("idle streaming session remains after cleanup")
	}
}
