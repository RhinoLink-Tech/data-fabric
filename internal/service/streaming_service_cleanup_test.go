package service

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/model"
)

type cleanupRowsState struct {
	manager               *StreamingSessionManager
	queryID               string
	closeErr              error
	closed                bool
	sessionPresentOnClose bool
	mutex                 sync.Mutex
}

type cleanupRowsConnector struct {
	state *cleanupRowsState
}

func (c cleanupRowsConnector) Connect(context.Context) (driver.Conn, error) {
	return cleanupRowsConn{state: c.state}, nil
}

func (c cleanupRowsConnector) Driver() driver.Driver {
	return cleanupRowsDriver{state: c.state}
}

type cleanupRowsDriver struct {
	state *cleanupRowsState
}

func (d cleanupRowsDriver) Open(string) (driver.Conn, error) {
	return cleanupRowsConn{state: d.state}, nil
}

type cleanupRowsConn struct {
	state *cleanupRowsState
}

func (c cleanupRowsConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("Prepare is not supported")
}

func (cleanupRowsConn) Close() error { return nil }

func (c cleanupRowsConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported")
}

func (c cleanupRowsConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &cleanupDriverRows{state: c.state}, nil
}

type cleanupDriverRows struct {
	state *cleanupRowsState
	sent  bool
}

func (cleanupDriverRows) Columns() []string { return []string{"value"} }

func (r *cleanupDriverRows) Close() error {
	r.state.mutex.Lock()
	defer r.state.mutex.Unlock()
	r.state.closed = true
	r.state.manager.mutex.RLock()
	_, r.state.sessionPresentOnClose = r.state.manager.sessions[r.state.queryID]
	r.state.manager.mutex.RUnlock()
	return r.state.closeErr
}

func (r *cleanupDriverRows) Next(dest []driver.Value) error {
	if r.sent {
		return io.EOF
	}
	r.sent = true
	dest[0] = int64(1)
	return nil
}

func newCleanupTestRows(t *testing.T, state *cleanupRowsState) (*sql.Rows, *sql.DB) {
	t.Helper()
	db := sql.OpenDB(cleanupRowsConnector{state: state})
	rows, err := db.QueryContext(context.Background(), "SELECT 1")
	if err != nil {
		_ = db.Close()
		t.Fatalf("open test rows: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return rows, db
}

func newExpiredStreamingFixture(t *testing.T, closeErr error) (*streamingService, *StreamingSessionManager, *StreamingSession, *cleanupRowsState, chan struct{}) {
	t.Helper()
	manager := &StreamingSessionManager{sessions: make(map[string]*StreamingSession)}
	state := &cleanupRowsState{
		manager:  manager,
		queryID:  "expired-session",
		closeErr: closeErr,
	}
	rows, _ := newCleanupTestRows(t, state)
	cancelled := make(chan struct{})
	var cancelOnce sync.Once
	stream := &ActiveStream{
		ID:             "expired-stream",
		Rows:           rows,
		LastActivityAt: time.Now(),
		CancelFunc: func() {
			cancelOnce.Do(func() { close(cancelled) })
		},
	}
	session := &StreamingSession{
		QueryID:   state.queryID,
		StreamID:  stream.ID,
		ExpiresAt: time.Now().Add(-time.Second),
	}
	manager.Store(session)
	return &streamingService{
		activeStreams:           map[string]*ActiveStream{stream.ID: stream},
		streamTimeout:           time.Minute,
		streamingSessionManager: manager,
	}, manager, session, state, cancelled
}

func TestCleanupExpiredStreamingSessionClosesRowsBeforeRemovingSession(t *testing.T) {
	svc, manager, session, state, _ := newExpiredStreamingFixture(t, nil)

	if got := svc.CleanupExpiredSessions(context.Background()); got != 1 {
		t.Fatalf("CleanupExpiredSessions() = %d, want 1", got)
	}

	state.mutex.Lock()
	closed, sessionPresentOnClose := state.closed, state.sessionPresentOnClose
	state.mutex.Unlock()
	if !closed {
		t.Fatal("expired stream rows were not closed")
	}
	if !sessionPresentOnClose {
		t.Fatal("session was removed before its underlying rows were closed")
	}
	if manager.isCurrent(session) {
		t.Fatal("expired session remains addressable")
	}
	if got := len(svc.GetAllStreams()); got != 0 {
		t.Fatalf("active streams = %d, want 0", got)
	}
}

func TestExpiredSessionCleanupWaitsForConcurrentContinuation(t *testing.T) {
	svc, manager, session, state, cancelled := newExpiredStreamingFixture(t, nil)
	stream := svc.activeStreams[session.StreamID]
	if err := session.continuationLock.Lock(context.Background()); err != nil {
		t.Fatalf("lock session: %v", err)
	}
	stream.mutex.Lock()

	done := make(chan int, 1)
	go func() {
		done <- svc.CleanupExpiredSessions(context.Background())
	}()

	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not cancel the expired stream before waiting for the fetch lock")
	}
	if !manager.isCurrent(session) {
		t.Fatal("cleanup removed the session before closing its underlying stream")
	}

	stream.mutex.Unlock()
	session.continuationLock.Unlock()

	select {
	case cleaned := <-done:
		if cleaned != 1 {
			t.Fatalf("CleanupExpiredSessions() = %d, want 1", cleaned)
		}
	case <-time.After(time.Second):
		t.Fatal("expired session cleanup did not finish after continuation released its locks")
	}
	state.mutex.Lock()
	closed := state.closed
	state.mutex.Unlock()
	if !closed {
		t.Fatal("concurrent cleanup did not close the SQL rows")
	}
}

func TestExpiredSessionCleanupKeepsSessionAndLogsWhenRowsCloseFails(t *testing.T) {
	closeErr := errors.New("driver close failure")
	svc, manager, session, _, _ := newExpiredStreamingFixture(t, closeErr)

	var output bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(originalWriter)

	if got := svc.CleanupExpiredSessions(context.Background()); got != 0 {
		t.Fatalf("CleanupExpiredSessions() = %d, want 0 when row close fails", got)
	}
	if !manager.isCurrent(session) {
		t.Fatal("session was removed even though its rows failed to close")
	}
	if len(svc.GetAllStreams()) != 1 {
		t.Fatal("stream was removed even though its rows failed to close")
	}
	if !strings.Contains(output.String(), "driver close failure") {
		t.Fatalf("cleanup failure was not logged: %q", output.String())
	}
}

func TestCloseStreamCancelsBeforeWaitingForInFlightChunk(t *testing.T) {
	cancelled := make(chan struct{})
	var cancelOnce sync.Once
	stream := &ActiveStream{
		ID: "in-flight",
		CancelFunc: func() {
			cancelOnce.Do(func() { close(cancelled) })
		},
	}
	svc := &streamingService{activeStreams: map[string]*ActiveStream{stream.ID: stream}}
	stream.mutex.Lock()

	done := make(chan error, 1)
	go func() { done <- svc.CloseStream(stream.ID) }()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("CloseStream did not cancel the query before waiting for the chunk lock")
	}

	select {
	case err := <-done:
		t.Fatalf("CloseStream returned while a chunk held the stream lock: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	stream.mutex.Unlock()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CloseStream() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseStream did not finish after the chunk released the lock")
	}
	if got := len(svc.GetAllStreams()); got != 0 {
		t.Fatalf("active streams = %d, want 0", got)
	}
}

func TestCleanupRoutineStopsWhenItsServerContextEnds(t *testing.T) {
	svc := &streamingService{
		activeStreams:           make(map[string]*ActiveStream),
		streamingSessionManager: &StreamingSessionManager{sessions: make(map[string]*StreamingSession)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.StartCleanupRoutine(ctx, time.Hour)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup routine did not stop when its server context ended")
	}
}

func TestFetchStreamChunkClosesRowsAndSessionAfterScanFailure(t *testing.T) {
	const streamID = "scan-failure-stream"
	manager := &StreamingSessionManager{sessions: make(map[string]*StreamingSession)}
	state := &cleanupRowsState{
		manager: manager,
		queryID: "scan-failure-session",
	}
	rows, _ := newCleanupTestRows(t, state)
	session := &StreamingSession{QueryID: state.queryID, StreamID: streamID}
	manager.Store(session)

	stream := &ActiveStream{
		ID:             streamID,
		Rows:           rows,
		Columns:        []model.ColumnInfo{{Name: "value"}, {Name: "unexpected"}},
		StartedAt:      time.Now(),
		LastActivityAt: time.Now(),
	}
	svc := &streamingService{
		activeStreams:           map[string]*ActiveStream{streamID: stream},
		streamTimeout:           time.Minute,
		streamingSessionManager: manager,
	}

	_, err := svc.FetchStreamChunk(context.Background(), streamID, 1)
	if err == nil || !strings.Contains(err.Error(), "error scanning row") {
		t.Fatalf("FetchStreamChunk() error = %v, want a row scan error", err)
	}

	state.mutex.Lock()
	closed := state.closed
	state.mutex.Unlock()
	if !closed {
		t.Fatal("stream rows were not closed after a scan failure")
	}
	if got := len(svc.GetAllStreams()); got != 0 {
		t.Fatalf("active streams = %d after scan failure, want 0", got)
	}
	if manager.isCurrent(session) {
		t.Fatal("streaming session remains after a scan failure")
	}
}

// A cancelled stream's Rows.Close reports the cancellation itself. Cleanup
// must treat that as the expected outcome — deregister the stream, keep the
// log quiet — instead of logging an error and leaking the entry (#262).
func TestExpiredSessionCleanupTreatsCanceledRowsCloseAsExpected(t *testing.T) {
	svc, manager, session, _, _ := newExpiredStreamingFixture(t, context.Canceled)

	var output bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&output)
	defer log.SetOutput(originalWriter)

	if got := svc.CleanupExpiredSessions(context.Background()); got != 1 {
		t.Fatalf("CleanupExpiredSessions() = %d, want 1 for a cancelled stream", got)
	}
	if manager.isCurrent(session) {
		t.Fatal("session was kept even though cancellation is an expected close result")
	}
	if len(svc.GetAllStreams()) != 0 {
		t.Fatal("cancelled stream was not deregistered")
	}
	if strings.Contains(output.String(), "cleanup failed") {
		t.Fatalf("expected cancellation was logged as a failure: %q", output.String())
	}
}
