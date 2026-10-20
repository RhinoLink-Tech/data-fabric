package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/database"
	"github.com/RhinoLink-Tech/data-fabric/internal/model"
	"github.com/RhinoLink-Tech/data-fabric/internal/repository"
	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"github.com/RhinoLink-Tech/data-fabric/internal/utils/sql_translator"
	"github.com/google/uuid"
)

type StreamingService interface {
	//ExecuteQuery(ctx context.Context, req *model.QueryRequest) (*model.QueryResponse, error)
	//ValidateQuery(ctx context.Context, req *model.QueryRequest) error
	//GetQueryStats(ctx context.Context) (*model.QueryStats, error)
	FetchWithStreaming(ctx context.Context, caller *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error)
	FetchNextBatchWithStreaming(ctx context.Context, caller *security.Caller, queryID, slug, token string, batchSize int) (*model.FetchQueryResponse, error)
	// Start cleanup using the owning server's context, and close active streams
	// after that context ends.
	StartCleanupRoutine(ctx context.Context, interval time.Duration)
	Shutdown() error
}

func (ss *streamingService) translateSQLIfNeeded(sql, sourceDialect, targetDialect string) (string, error) {
	if sourceDialect == "" || strings.EqualFold(sourceDialect, targetDialect) {
		return sql, nil
	}
	if ss.sqlTranslator == nil {
		return "", fmt.Errorf("SQL translator is not configured")
	}
	return ss.sqlTranslator.TranslateQuery(sql, sourceDialect, targetDialect)
}

func (ss *streamingService) mapQueryEngineTypeToStandard(dbTypeName string) model.StandardizedType {
	upperTypeName := strings.ToUpper(dbTypeName)
	switch {
	case strings.Contains(upperTypeName, "BIGINT"):
		return model.TypeBigInt
	case strings.Contains(upperTypeName, "TINYINT"), strings.Contains(upperTypeName, "SMALLINT"), strings.Contains(upperTypeName, "INTEGER"):
		return model.TypeInteger
	case strings.Contains(upperTypeName, "DOUBLE"):
		return model.TypeDouble
	case strings.Contains(upperTypeName, "REAL"):
		return model.TypeFloat
	case strings.Contains(upperTypeName, "DECIMAL"), strings.Contains(upperTypeName, "NUMERIC"):
		return model.TypeDecimal
	case strings.Contains(upperTypeName, "ARRAY"):
		return model.TypeArray
	case strings.Contains(upperTypeName, "ROW"):
		return model.TypeStruct
	case strings.Contains(upperTypeName, "MAP"), strings.Contains(upperTypeName, "JSON"),
		strings.Contains(upperTypeName, "BINGTILE"), strings.Contains(upperTypeName, "KDBTREE"):
		return model.TypeJSON
	case strings.Contains(upperTypeName, "VARBINARY"):
		return model.TypeBinary
	case strings.Contains(upperTypeName, "DATE"):
		return model.TypeDate
	case strings.Contains(upperTypeName, "TIMESTAMP"):
		return model.TypeTimestamp
	case strings.Contains(upperTypeName, "TIME"):
		return model.TypeTime
	case strings.Contains(upperTypeName, "BOOLEAN"):
		return model.TypeBoolean
	case strings.Contains(upperTypeName, "UUID"):
		return model.TypeUUID
	case strings.Contains(upperTypeName, "VARCHAR"), strings.Contains(upperTypeName, "CHAR"),
		strings.Contains(upperTypeName, "IPADDRESS"), strings.Contains(upperTypeName, "GEOMETRY"),
		strings.Contains(upperTypeName, "SPHERICALGEOGRAPHY"), strings.Contains(upperTypeName, "INTERVAL"):
		return model.TypeString
	default:
		return model.TypeUnknown
	}
}

// StreamingSession represents a streaming query session for batch fetching
type StreamingSession struct {
	QueryID string
	Slug    string
	Token   string
	// CreatorID binds a session to the identity that created it (#9).
	CreatorID        string
	DataSourceID     string
	SQL              string
	Type             int
	BatchSize        int
	Cursor           interface{} // Cursor for pagination
	TotalRows        int64
	FetchedRows      int64
	Columns          []model.ColumnInfo
	Entries          []*[]interface{}
	CreatedAt        time.Time
	ExpiresAt        time.Time
	continuationLock sessionContinuationLock
	StreamID         string // Associated stream ID
}

// StreamingSessionManager manages streaming query sessions
type StreamingSessionManager struct {
	sessions map[string]*StreamingSession
	mutex    sync.RWMutex
	ttl      time.Duration
}

// NewStreamingSessionManager creates a new streaming session manager
func NewStreamingSessionManager() *StreamingSessionManager {
	sm := &StreamingSessionManager{
		sessions: make(map[string]*StreamingSession),
		ttl:      30 * time.Minute, // Session TTL: 30 minutes
	}

	return sm
}

// Store stores a streaming session
func (sm *StreamingSessionManager) Store(session *StreamingSession) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	sm.sessions[session.QueryID] = session
}

// Get retrieves a streaming session
func (sm *StreamingSessionManager) Get(queryID string) (*StreamingSession, error) {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	session, exists := sm.sessions[queryID]
	if !exists {
		return nil, fmt.Errorf("session not found")
	}

	// Check if session is expired
	if time.Now().After(session.ExpiresAt) {
		return nil, fmt.Errorf("session expired")
	}

	return session, nil
}

// getAndLock obtains a session and serializes its continuation-token state.
// Rechecking the map after acquiring the session lock handles concurrent
// session expiry or deletion between the initial lookup and lock acquisition.
func (sm *StreamingSessionManager) getAndLock(ctx context.Context, queryID string) (*StreamingSession, error) {
	session, err := sm.Get(queryID)
	if err != nil {
		return nil, ErrQuerySessionUnavailable
	}
	if err := session.continuationLock.Lock(ctx); err != nil {
		return nil, err
	}

	current, err := sm.Get(queryID)
	if err != nil || current != session {
		session.continuationLock.Unlock()
		return nil, ErrQuerySessionUnavailable
	}
	return session, nil
}

func (session *StreamingSession) validateToken(slug, token string) error {
	return validateContinuationToken(session.Slug, session.Token, slug, token)
}

// Delete removes a streaming session
func (sm *StreamingSessionManager) Delete(queryID string) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	delete(sm.sessions, queryID)
}

func (sm *StreamingSessionManager) DeleteByStreamID(streamID string) {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	for queryID, session := range sm.sessions {
		if session.StreamID == streamID {
			delete(sm.sessions, queryID)
		}
	}
}

func (sm *StreamingSessionManager) expiredSessions(now time.Time) []*StreamingSession {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()

	expired := make([]*StreamingSession, 0)
	for _, session := range sm.sessions {
		if now.After(session.ExpiresAt) {
			expired = append(expired, session)
		}
	}
	return expired
}

func (sm *StreamingSessionManager) isCurrent(session *StreamingSession) bool {
	sm.mutex.RLock()
	defer sm.mutex.RUnlock()
	return sm.sessions[session.QueryID] == session
}

func (sm *StreamingSessionManager) deleteIfCurrent(session *StreamingSession) bool {
	sm.mutex.Lock()
	defer sm.mutex.Unlock()

	if sm.sessions[session.QueryID] != session {
		return false
	}
	delete(sm.sessions, session.QueryID)
	return true
}

type streamingService struct {
	datasourceRepo          repository.DataSourceRepository
	connPool                *database.ConnectionPool
	activeStreams           map[string]*ActiveStream
	streamsMutex            sync.RWMutex
	maxStreams              int
	streamTimeout           time.Duration
	streamingSessionManager *StreamingSessionManager
	sqlValidator            *security.SQLValidator
	sqlTranslator           *sql_translator.SQLTranslationManager
}

// ActiveStream represents an active streaming query
type ActiveStream struct {
	ID             string
	DataSourceID   string
	DatabaseType   model.DatabaseType
	Rows           *sql.Rows
	Columns        []model.ColumnInfo
	StartedAt      time.Time
	LastActivityAt time.Time
	RowsSent       int64
	BytesSent      int64
	CancelFunc     context.CancelFunc
	mutex          sync.Mutex
	activityMutex  sync.Mutex
}

func (stream *ActiveStream) touch(at time.Time) {
	stream.activityMutex.Lock()
	stream.LastActivityAt = at
	stream.activityMutex.Unlock()
}

func (stream *ActiveStream) lastActivity() time.Time {
	stream.activityMutex.Lock()
	defer stream.activityMutex.Unlock()
	return stream.LastActivityAt
}

// StreamChunk represents a chunk of streamed data
type StreamChunk struct {
	StreamID string             `json:"streamId"`
	ChunkID  int64              `json:"chunkId"`
	Columns  []model.ColumnInfo `json:"columns,omitempty"` // Only in first chunk
	Rows     [][]interface{}    `json:"rows"`
	Metadata StreamMetadata     `json:"metadata"`
}

// StreamMetadata contains metadata about the stream
type StreamMetadata struct {
	RowCount      int64     `json:"rowCount"`
	ChunkSize     int       `json:"chunkSize"`
	ExecutionTime time.Time `json:"executionTime"`
}

// SupportsStreaming resolves a source's registered driver capability without
// opening a connection. It applies the same access and active-source gates as
// query execution before returning the capability.
func (ss *streamingService) SupportsStreaming(ctx context.Context, caller *security.Caller, dataSourceID string) (bool, error) {
	dataSource, err := ss.datasourceRepo.GetByID(ctx, dataSourceID)
	if err != nil {
		return false, fmt.Errorf("failed to get data source: %w", err)
	}
	if !caller.CanRead(dataSource) {
		return false, security.ErrDataSourceAccessDenied
	}
	if err := requireActiveDataSource(dataSource); err != nil {
		return false, err
	}
	driver, err := database.GetDriverRegistry().GetDriver(dataSource.Type)
	if err != nil {
		return false, fmt.Errorf("failed to resolve data source driver: %w", err)
	}
	return driver.GetCapabilities().SupportsStreaming, nil
}

// NewStreamingService creates a new streaming service.
func NewStreamingService(datasourceRepo repository.DataSourceRepository, connPool *database.ConnectionPool) StreamingService {
	ss := &streamingService{
		datasourceRepo:          datasourceRepo,
		connPool:                connPool,
		activeStreams:           make(map[string]*ActiveStream),
		maxStreams:              100, // Max concurrent streams
		streamTimeout:           5 * time.Minute,
		streamingSessionManager: NewStreamingSessionManager(),
		sqlValidator:            security.NewSQLValidator(10000),
		sqlTranslator:           sql_translator.NewSQLTranslationManager(true),
	}
	return ss
}

// ExecuteStreamQuery executes a query and returns a stream ID
func (ss *streamingService) ExecuteStreamQuery(ctx context.Context, caller *security.Caller, req *model.QueryRequest) (string, int64, error) {
	requestCtx, requestCancel := withQueryTimeout(ctx, req.Timeout)
	defer requestCancel()

	// Check stream limit
	if ss.getActiveStreamCount() >= ss.maxStreams {
		return "", 0, ErrMaxConcurrentStreams
	}

	dataSource, err := ss.datasourceRepo.GetByID(requestCtx, req.DataSourceID)
	if err != nil {
		return "", 0, fmt.Errorf("failed to get data source: %w", err)
	}
	if !caller.CanRead(dataSource) {
		return "", 0, security.ErrDataSourceAccessDenied
	}

	// Inactive or errored sources must not open streams (#118)
	if err := requireActiveDataSource(dataSource); err != nil {
		return "", 0, err
	}

	querySQL, err := ss.translateSQLIfNeeded(req.SQL, req.SourceDialect, string(dataSource.Type))
	if err != nil {
		return "", 0, fmt.Errorf("failed to translate SQL: %w", err)
	}
	if ss.sqlValidator != nil {
		if err := ss.sqlValidator.ValidateStatementForDataSource(querySQL, dataSource.Type); err != nil {
			return "", 0, fmt.Errorf("SQL validation failed: %w", err)
		}
	}

	streamLifetime := ss.streamTimeout
	if streamLifetime <= 0 {
		streamLifetime = 5 * time.Minute
	}
	// Keep the result cursor alive after the first response while still
	// honoring cancellation and deadlines during query initialization.
	streamCtx, cancel := context.WithTimeout(context.WithoutCancel(requestCtx), streamLifetime)
	watchStop := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-requestCtx.Done():
			cancel()
		case <-watchStop:
		}
	}()
	watcherStopped := false
	stopRequestWatcher := func() {
		if !watcherStopped {
			close(watchStop)
			<-watchDone
			watcherStopped = true
		}
	}
	streamTransferred := false
	defer func() {
		stopRequestWatcher()
		if !streamTransferred {
			cancel()
		}
	}()

	db, err := ss.connPool.GetConnection(streamCtx, dataSource)
	if err != nil {
		if requestCtx.Err() != nil {
			return "", 0, requestCtx.Err()
		}
		return "", 0, fmt.Errorf("failed to get connection: %w", err)
	}

	rows, err := db.QueryContext(streamCtx, querySQL, req.Parameters...)
	if err != nil {
		if requestCtx.Err() != nil {
			return "", 0, requestCtx.Err()
		}
		return "", 0, fmt.Errorf("query execution failed: %w", err)
	}

	columns, err := rows.ColumnTypes()
	if err != nil {
		_ = rows.Close()
		if requestCtx.Err() != nil {
			return "", 0, requestCtx.Err()
		}
		return "", 0, fmt.Errorf("failed to get columns: %w", err)
	}
	columnInfo := ss.buildColumnInfo(columns, dataSource.Type)

	streamID := ss.generateStreamID()
	stream := &ActiveStream{
		ID:             streamID,
		DataSourceID:   req.DataSourceID,
		DatabaseType:   dataSource.Type,
		Rows:           rows,
		Columns:        columnInfo,
		StartedAt:      time.Now(),
		LastActivityAt: time.Now(),
		CancelFunc:     cancel,
	}
	ss.streamsMutex.Lock()
	ss.activeStreams[streamID] = stream
	ss.streamsMutex.Unlock()

	totalCount, err := ss.getRowCount(requestCtx, db, querySQL)
	if err != nil {
		_ = ss.CloseStream(streamID)
		if requestCtx.Err() != nil {
			return "", 0, requestCtx.Err()
		}
		return "", 0, fmt.Errorf("failed to get row count: %w", err)
	}
	if err := requestCtx.Err(); err != nil {
		_ = ss.CloseStream(streamID)
		return "", 0, err
	}
	stopRequestWatcher()
	if err := requestCtx.Err(); err != nil {
		_ = ss.CloseStream(streamID)
		return "", 0, err
	}

	streamTransferred = true
	return streamID, totalCount, nil
}

// FetchStreamChunk fetches the next chunk of data from a stream
func (ss *streamingService) FetchStreamChunk(ctx context.Context, streamID string, batchSize int) (*StreamChunk, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		_ = ss.CloseStream(streamID)
		return nil, err
	}

	ss.streamsMutex.RLock()
	stream, exists := ss.activeStreams[streamID]
	ss.streamsMutex.RUnlock()
	if !exists {
		return nil, fmt.Errorf("stream not found: %s", streamID)
	}

	stream.mutex.Lock()
	defer stream.mutex.Unlock()

	timeout := ss.streamTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	if time.Since(stream.lastActivity()) > timeout {
		_ = ss.closeStreamLocked(streamID, stream)
		return nil, fmt.Errorf("stream timed out")
	}
	stream.touch(time.Now())

	watchStop := make(chan struct{})
	watchDone := make(chan struct{})
	if stream.CancelFunc != nil {
		go func() {
			defer close(watchDone)
			select {
			case <-ctx.Done():
				stream.CancelFunc()
			case <-watchStop:
			}
		}()
	} else {
		close(watchDone)
	}
	defer func() {
		close(watchStop)
		<-watchDone
	}()

	if err := ctx.Err(); err != nil {
		_ = ss.closeStreamLocked(streamID, stream)
		return nil, err
	}
	var rows [][]interface{}
	for len(rows) < batchSize {
		if err := ctx.Err(); err != nil {
			_ = ss.closeStreamLocked(streamID, stream)
			return nil, err
		}
		if !stream.Rows.Next() {
			if err := ctx.Err(); err != nil {
				_ = ss.closeStreamLocked(streamID, stream)
				return nil, err
			}
			break
		}

		row := make([]interface{}, len(stream.Columns))
		rowPointers := make([]interface{}, len(row))
		for i := range row {
			rowPointers[i] = &row[i]
		}
		if err := stream.Rows.Scan(rowPointers...); err != nil {
			_ = ss.closeStreamLocked(streamID, stream)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("error scanning row: %w", err)
		}
		rows = append(rows, row)
	}

	if err := stream.Rows.Err(); err != nil {
		_ = ss.closeStreamLocked(streamID, stream)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("row iteration error: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = ss.closeStreamLocked(streamID, stream)
		return nil, err
	}

	stream.RowsSent += int64(len(rows))
	stream.touch(time.Now())
	return &StreamChunk{
		StreamID: streamID,
		ChunkID:  stream.RowsSent,
		Columns:  stream.Columns,
		Rows:     rows,
		Metadata: StreamMetadata{
			RowCount:      stream.RowsSent,
			ChunkSize:     batchSize,
			ExecutionTime: stream.StartedAt,
		},
	}, nil
}

// CloseStream closes an active stream. It cancels the query before waiting for
// an in-flight chunk read, then serializes row closure with that read.
func (ss *streamingService) CloseStream(streamID string) error {
	ss.streamsMutex.RLock()
	stream := ss.activeStreams[streamID]
	ss.streamsMutex.RUnlock()
	if stream == nil {
		// Closing is idempotent so concurrent expiry, timeout, and client-close
		// paths can safely converge on the same stream.
		return nil
	}

	if stream.CancelFunc != nil {
		stream.CancelFunc()
	}
	stream.mutex.Lock()
	defer stream.mutex.Unlock()
	return ss.closeStreamLocked(streamID, stream)
}

func (ss *streamingService) closeSessionStream(session *StreamingSession) error {
	if session == nil {
		return nil
	}
	if err := ss.CloseStream(session.StreamID); err != nil {
		log.Printf("stream session cleanup failed for session %s (stream %s): %v", session.QueryID, session.StreamID, err)
		return err
	}
	if ss.streamingSessionManager != nil {
		ss.streamingSessionManager.Delete(session.QueryID)
	}
	return nil
}

// closeStreamLocked requires stream.mutex and keeps the session addressable
// until the underlying SQL rows have been closed successfully.
func (ss *streamingService) closeStreamLocked(streamID string, stream *ActiveStream) error {
	ss.streamsMutex.RLock()
	current := ss.activeStreams[streamID]
	ss.streamsMutex.RUnlock()
	if current != stream {
		return nil
	}

	if stream.CancelFunc != nil {
		stream.CancelFunc()
	}
	if stream.Rows != nil {
		if err := stream.Rows.Close(); err != nil {
			return fmt.Errorf("close stream %s rows: %w", streamID, err)
		}
	}

	ss.streamsMutex.Lock()
	if ss.activeStreams[streamID] == stream {
		delete(ss.activeStreams, streamID)
	}
	ss.streamsMutex.Unlock()

	if ss.streamingSessionManager != nil {
		ss.streamingSessionManager.DeleteByStreamID(streamID)
	}
	return nil
}

// GetStreamStatus returns the status of an active stream.
func (ss *streamingService) GetStreamStatus(streamID string) (*ActiveStream, error) {
	ss.streamsMutex.RLock()
	stream := ss.activeStreams[streamID]
	ss.streamsMutex.RUnlock()
	if stream == nil {
		return nil, fmt.Errorf("stream not found: %s", streamID)
	}

	stream.mutex.Lock()
	defer stream.mutex.Unlock()
	ss.streamsMutex.RLock()
	current := ss.activeStreams[streamID]
	ss.streamsMutex.RUnlock()
	if current != stream {
		return nil, fmt.Errorf("stream not found: %s", streamID)
	}

	// Return a copy to avoid exposing mutable stream state.
	return &ActiveStream{
		ID:             stream.ID,
		DataSourceID:   stream.DataSourceID,
		DatabaseType:   stream.DatabaseType,
		Columns:        stream.Columns,
		StartedAt:      stream.StartedAt,
		LastActivityAt: stream.LastActivityAt,
		RowsSent:       stream.RowsSent,
		BytesSent:      stream.BytesSent,
	}, nil
}

// GetAllStreams returns all active streams
func (ss *streamingService) GetAllStreams() []*ActiveStream {
	ss.streamsMutex.RLock()
	defer ss.streamsMutex.RUnlock()

	streams := make([]*ActiveStream, 0, len(ss.activeStreams))
	for _, stream := range ss.activeStreams {
		streams = append(streams, stream)
	}

	return streams
}

// CleanupInactiveStreams closes streams that have exceeded their idle timeout.
func (ss *streamingService) CleanupInactiveStreams() int {
	timeout := ss.streamTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	cutoff := time.Now().Add(-timeout)

	ss.streamsMutex.RLock()
	streams := make(map[string]*ActiveStream, len(ss.activeStreams))
	for id, stream := range ss.activeStreams {
		streams[id] = stream
	}
	ss.streamsMutex.RUnlock()

	cleaned := 0
	for id, stream := range streams {
		stream.activityMutex.Lock()
		expired := stream.LastActivityAt.Before(cutoff)
		if expired && stream.CancelFunc != nil {
			stream.CancelFunc()
		}
		stream.activityMutex.Unlock()
		if !expired {
			continue
		}

		stream.mutex.Lock()
		if !stream.lastActivity().Before(cutoff) {
			stream.mutex.Unlock()
			continue
		}
		err := ss.closeStreamLocked(id, stream)
		stream.mutex.Unlock()
		if err != nil {
			log.Printf("stream cleanup failed for inactive stream %s: %v", id, err)
			continue
		}
		cleaned++
	}
	return cleaned
}

// CleanupExpiredSessions closes each expired stream before removing its session.
// The per-session lock prevents cleanup from racing with continuation requests.
func (ss *streamingService) CleanupExpiredSessions(ctx context.Context) int {
	if ss.streamingSessionManager == nil {
		return 0
	}
	if ctx == nil {
		ctx = context.Background()
	}

	candidates := ss.streamingSessionManager.expiredSessions(time.Now())
	cleaned := 0
	for _, session := range candidates {
		if session.StreamID != "" {
			// Cancel first so a fetch blocked in Rows.Next can release the
			// continuation and stream locks before cleanup waits for them.
			ss.cancelStream(session.StreamID)
		}

		lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := session.continuationLock.Lock(lockCtx)
		cancel()
		if err != nil {
			log.Printf("stream session cleanup failed for expired session %s: waiting for continuation: %v", session.QueryID, err)
			continue
		}

		if !ss.streamingSessionManager.isCurrent(session) || !time.Now().After(session.ExpiresAt) {
			session.continuationLock.Unlock()
			continue
		}
		if session.StreamID != "" {
			if err := ss.CloseStream(session.StreamID); err != nil {
				log.Printf("stream session cleanup failed for expired session %s (stream %s): %v", session.QueryID, session.StreamID, err)
				session.continuationLock.Unlock()
				continue
			}
		}
		if ss.streamingSessionManager.deleteIfCurrent(session) || session.StreamID != "" {
			cleaned++
		}
		session.continuationLock.Unlock()
	}
	return cleaned
}

func (ss *streamingService) cancelStream(streamID string) {
	ss.streamsMutex.RLock()
	stream := ss.activeStreams[streamID]
	ss.streamsMutex.RUnlock()
	if stream != nil && stream.CancelFunc != nil {
		stream.CancelFunc()
	}
}

// StartCleanupRoutine starts maintenance until its owning server context ends.
func (ss *streamingService) StartCleanupRoutine(ctx context.Context, interval time.Duration) {
	if ctx == nil {
		ctx = context.Background()
	}
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			inactive := ss.CleanupInactiveStreams()
			expired := ss.CleanupExpiredSessions(ctx)
			if inactive > 0 || expired > 0 {
				log.Printf("stream maintenance closed %d inactive streams and expired %d sessions", inactive, expired)
			}
		}
	}
}

// Shutdown closes all active streams after maintenance has stopped.
func (ss *streamingService) Shutdown() error {
	ss.streamsMutex.RLock()
	ids := make([]string, 0, len(ss.activeStreams))
	for id := range ss.activeStreams {
		ids = append(ids, id)
	}
	ss.streamsMutex.RUnlock()

	var closeErrors []error
	for _, id := range ids {
		if err := ss.CloseStream(id); err != nil {
			log.Printf("stream shutdown cleanup failed for stream %s: %v", id, err)
			closeErrors = append(closeErrors, err)
		}
	}
	return errors.Join(closeErrors...)
}

// Helper methods

func (ss *streamingService) generateStreamID() string {
	return fmt.Sprintf("stream_%d", time.Now().UnixNano())
}

func (ss *streamingService) getActiveStreamCount() int {
	ss.streamsMutex.RLock()
	defer ss.streamsMutex.RUnlock()
	return len(ss.activeStreams)
}

func (ss *streamingService) buildColumnInfo(columns []*sql.ColumnType, dbType model.DatabaseType) []model.ColumnInfo {
	columnInfo := make([]model.ColumnInfo, len(columns))

	for i, col := range columns {
		nullable, _ := col.Nullable()
		columnInfo[i] = model.ColumnInfo{
			Name:     col.Name(),
			Type:     ss.mapDatabaseTypeToStandard(col.DatabaseTypeName(), dbType, nullable),
			Nullable: nullable,
		}
	}

	return columnInfo
}

func (ss *streamingService) writeCSVHeader(writer io.Writer, columns []model.ColumnInfo) error {
	header := ""
	for i, col := range columns {
		if i > 0 {
			header += ","
		}
		header += col.Name
	}
	header += "\n"
	_, err := io.WriteString(writer, header)
	return err
}

func (ss *streamingService) writeCSVRows(writer io.Writer, rows [][]interface{}) error {
	for _, row := range rows {
		line := ""
		for i, val := range row {
			if i > 0 {
				line += ","
			}
			if val == nil {
				line += "NULL"
			} else {
				line += fmt.Sprintf("%v", val)
			}
		}
		line += "\n"
		if _, err := io.WriteString(writer, line); err != nil {
			return err
		}
	}
	return nil
}

// fetchWithStreaming executes a fetch query using the streaming approach
func (ss *streamingService) FetchWithStreaming(ctx context.Context, caller *security.Caller, req *model.FetchQueryRequest) (*model.FetchQueryResponse, error) {
	ctx, cancel := withQueryTimeout(ctx, req.Timeout)
	defer cancel()

	// Create a new streaming session
	session := &StreamingSession{
		QueryID:      ss.generateSessionID(),
		Slug:         ss.generateSlug(),
		Token:        ss.generateToken(),
		CreatorID:    caller.Key(),
		DataSourceID: req.DataSourceID,
		SQL:          req.SQL,
		Type:         req.Type,
		BatchSize:    req.BatchSize,
		CreatedAt:    time.Now(),
		ExpiresAt:    time.Now().Add(30 * time.Minute),
	}

	// Execute streaming query to get the stream ID
	streamReq := &model.QueryRequest{
		DataSourceID:  req.DataSourceID,
		SQL:           req.SQL,
		SourceDialect: req.SourceDialect,
		Limit:         req.BatchSize,
		Offset:        0,
		Timeout:       req.Timeout,
	}
	streamID, totalCount, err := ss.ExecuteStreamQuery(ctx, caller, streamReq)
	if err != nil {
		return nil, fmt.Errorf("failed to start streaming query: %w", err)
	}

	// Fetch the first batch of data
	chunk, err := ss.FetchStreamChunk(ctx, streamID, req.BatchSize)
	if err != nil {
		// Try to close the stream even if there was an error
		ss.CloseStream(streamID)
		return nil, fmt.Errorf("failed to fetch stream chunk: %w", err)
	}

	// Convert chunk data to the expected format
	entries := make([]*[]interface{}, 0, len(chunk.Rows))
	for _, row := range chunk.Rows {
		entry := make([]interface{}, len(row))
		for j, val := range row {
			if val == nil {
				entry[j] = "NULL"
			} else if b, ok := val.([]byte); ok {
				entry[j] = string(b)
			} else {
				entry[j] = val
			}
		}
		entries = append(entries, &entry)
	}

	// Convert columns to the expected format
	columns := make([]model.ColumnInfo, len(chunk.Columns))
	for i, col := range chunk.Columns {
		columns[i] = model.ColumnInfo{
			Name:     col.Name,
			Type:     col.Type,
			Nullable: col.Nullable,
		}
	}

	// Create a response similar to pagination service
	response := &model.FetchQueryResponse{
		QueryID:    session.QueryID,
		Slug:       session.Slug,
		Token:      session.Token,
		NextURI:    "", // We'll set this if there are more results
		Columns:    columns,
		Entries:    entries,
		TotalCount: int(totalCount),
	}

	if req.Type == 2 && len(entries) < int(totalCount) {
		// Add NextURI if there are more results
		response.NextURI = ss.buildNextURI(session.QueryID, session.Slug, session.Token, req.BatchSize)

		// Update session data
		session.StreamID = streamID
		session.Columns = columns
		session.Entries = entries
		session.TotalRows = totalCount
		session.FetchedRows = int64(len(entries))

		// Store the session
		ss.streamingSessionManager.Store(session)

	} else {
		ss.CloseStream(streamID)
	}

	return response, nil
}

// fetchNextBatchWithStreaming fetches the next batch using the streaming approach
func (ss *streamingService) FetchNextBatchWithStreaming(ctx context.Context, caller *security.Caller, queryID, slug, token string, batchSize int) (*model.FetchQueryResponse, error) {
	ctx, cancel := withQueryTimeout(ctx, defaultQueryTimeoutSeconds)
	defer cancel()

	// Serialize continuation requests for a session. Reusing the same token
	// concurrently lets the first request advance the cursor; later requests
	// wait here and are rejected when they present the now-stale token.
	session, err := ss.streamingSessionManager.getAndLock(ctx, queryID)
	if err != nil {
		return nil, err
	}
	defer session.continuationLock.Unlock()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := session.validateToken(slug, token); err != nil {
		return nil, err
	}

	// Sessions are bound to their creator (#9). The response matches an
	// unknown session so session existence is not disclosed.
	if session.CreatorID != caller.Key() {
		return nil, ErrQuerySessionUnavailable
	}

	// Re-check the data source on continuation: the source may have been
	// disabled or deleted since the session was created (#118). A source that
	// can no longer serve queries also invalidates its engine cursor.
	datasource, err := ss.datasourceRepo.GetByID(ctx, session.DataSourceID)
	if err != nil {
		return nil, fmt.Errorf("failed to get data source: %w", err)
	}
	if !caller.CanRead(datasource) {
		_ = ss.closeSessionStream(session)
		return nil, security.ErrDataSourceAccessDenied
	}
	if err := requireActiveDataSource(datasource); err != nil {
		_ = ss.closeSessionStream(session)
		return nil, err
	}

	// Check if stream exists and is valid
	_, err = ss.GetStreamStatus(session.StreamID)
	if err != nil {
		_ = ss.closeSessionStream(session)
		return nil, ErrQuerySessionUnavailable
	}

	// Fetch the next chunk of data. A disconnected page request cancels the
	// engine query and releases its cursor.
	chunk, err := ss.FetchStreamChunk(ctx, session.StreamID, batchSize)
	if err != nil {
		_ = ss.closeSessionStream(session)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("failed to fetch stream chunk: %w", err)
	}
	if ctx.Err() != nil {
		_ = ss.closeSessionStream(session)
		return nil, ctx.Err()
	}

	// Convert chunk data to the expected format
	entries := make([]*[]interface{}, 0, len(chunk.Rows))
	for _, row := range chunk.Rows {
		entry := make([]interface{}, len(row))
		for j, val := range row {
			if val == nil {
				entry[j] = "NULL"
			} else if b, ok := val.([]byte); ok {
				entry[j] = string(b)
			} else {
				entry[j] = val
			}
		}
		entries = append(entries, &entry)
	}

	// Convert columns to the expected format (use the first chunk's columns if available)
	var columns []model.ColumnInfo
	if len(chunk.Columns) > 0 {
		// Use columns from the current chunk
		columns = make([]model.ColumnInfo, len(chunk.Columns))
		for i, col := range chunk.Columns {
			columns[i] = model.ColumnInfo{
				Name:     col.Name,
				Type:     col.Type,
				Nullable: col.Nullable,
			}
		}
	} else {
		// Use columns from the session
		columns = session.Columns
	}

	// Update session data
	session.Entries = entries
	session.FetchedRows += int64(len(entries))
	session.Token = ss.generateToken()

	// Create a response similar to pagination service
	response := &model.FetchQueryResponse{
		QueryID:    session.QueryID,
		Slug:       session.Slug,
		Token:      session.Token,
		Columns:    columns,
		Entries:    entries,
		TotalCount: int(session.TotalRows),
	}

	// Add NextURI if there are more results
	if session.FetchedRows < session.TotalRows {
		response.NextURI = ss.buildNextURI(session.QueryID, session.Slug, session.Token, batchSize)
	} else {
		// Close the stream if no more results
		_ = ss.closeSessionStream(session)
	}

	return response, nil
}

// buildNextURI builds the next URI for fetching more data
func (ss *streamingService) buildNextURI(queryID, slug, token string, batchSize int) string {
	return fmt.Sprintf("/api/v1/fetch/%s/%s/%s?batch_size=%d", queryID, slug, token, batchSize)
}

// Helper functions

func (ss *streamingService) generateSessionID() string {
	return uuid.New().String()
}

func (ss *streamingService) generateSlug() string {
	return uuid.New().String()[:8]
}

func (ss *streamingService) generateToken() string {
	return uuid.New().String()
}

// getRowCount gets the total row count for the query
func (ss *streamingService) getRowCount(ctx context.Context, db *sql.DB, sql string) (int64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	return countRowsInQuery(ctx, db, sql)
}

// mapDatabaseTypeToStandard maps database-specific type to standardized type
func (ss *streamingService) mapDatabaseTypeToStandard(dbTypeName string, dbType model.DatabaseType, nullable bool) model.StandardizedType {
	// Use the DataTypeMapper from utils package or implement basic mapping
	switch dbType {
	case model.DatabaseTypeMySQL, model.DatabaseTypeMariaDB:
		return ss.mapMySQLTypeToStandard(dbTypeName, nullable)
	case model.DatabaseTypePostgreSQL:
		return ss.mapPostgreSQLTypeToStandard(dbTypeName, nullable)
	case model.DatabaseTypeOracle:
		return ss.mapOracleTypeToStandard(dbTypeName, nullable)
	case model.DatabaseTypeSnowflake:
		return ss.mapSnowflakeTypeToStandard(dbTypeName, nullable)
	case model.DatabaseTypeBigQuery:
		return ss.mapBigQueryTypeToStandard(dbTypeName, nullable)
	case model.DatabaseTypeDatabricks:
		return ss.mapDatabricksTypeToStandard(dbTypeName, nullable)
	case model.DatabaseTypeRedshift:
		return ss.mapRedshiftTypeToStandard(dbTypeName, nullable)
	case model.DatabaseTypeTrino, model.DatabaseTypePresto, model.DatabaseTypeOpenLooKeng:
		return ss.mapQueryEngineTypeToStandard(dbTypeName)
	default:
		// Default mapping for unknown database types
		return ss.mapGenericDatabaseTypeToStandard(dbTypeName, nullable)
	}
}

// mapGenericDatabaseTypeToStandard provides generic type mapping
func (ss *streamingService) mapGenericDatabaseTypeToStandard(dbTypeName string, nullable bool) model.StandardizedType {
	upperTypeName := strings.ToUpper(dbTypeName)

	switch {
	case strings.Contains(upperTypeName, "INT"):
		return model.StandardizedTypeInteger
	case strings.Contains(upperTypeName, "BIGINT"):
		return model.StandardizedTypeBigInt
	case strings.Contains(upperTypeName, "FLOAT"), strings.Contains(upperTypeName, "REAL"):
		return model.StandardizedTypeFloat
	case strings.Contains(upperTypeName, "DOUBLE"):
		return model.StandardizedTypeDouble
	case strings.Contains(upperTypeName, "DECIMAL"), strings.Contains(upperTypeName, "NUMERIC"):
		return model.StandardizedTypeDecimal
	case strings.Contains(upperTypeName, "VARCHAR"), strings.Contains(upperTypeName, "CHAR"),
		strings.Contains(upperTypeName, "TEXT"), strings.Contains(upperTypeName, "CLOB"):
		return model.StandardizedTypeString
	case strings.Contains(upperTypeName, "DATE"):
		return model.StandardizedTypeDate
	case strings.Contains(upperTypeName, "TIME"):
		return model.StandardizedTypeTime
	case strings.Contains(upperTypeName, "TIMESTAMP"):
		return model.StandardizedTypeTimestamp
	case strings.Contains(upperTypeName, "BOOLEAN"), strings.Contains(upperTypeName, "BOOL"):
		return model.StandardizedTypeBoolean
	case strings.Contains(upperTypeName, "BINARY"), strings.Contains(upperTypeName, "VARBINARY"),
		strings.Contains(upperTypeName, "BLOB"), strings.Contains(upperTypeName, "IMAGE"):
		return model.StandardizedTypeBinary
	default:
		return model.StandardizedTypeString
	}
}

// mapMySQLTypeToStandard maps MySQL types to standard types
func (ss *streamingService) mapMySQLTypeToStandard(dbTypeName string, nullable bool) model.StandardizedType {
	return ss.mapGenericDatabaseTypeToStandard(dbTypeName, nullable)
}

// mapPostgreSQLTypeToStandard maps PostgreSQL types to standard types
func (ss *streamingService) mapPostgreSQLTypeToStandard(dbTypeName string, nullable bool) model.StandardizedType {
	return ss.mapGenericDatabaseTypeToStandard(dbTypeName, nullable)
}

// mapOracleTypeToStandard maps Oracle types to standard types
func (ss *streamingService) mapOracleTypeToStandard(dbTypeName string, nullable bool) model.StandardizedType {
	return ss.mapGenericDatabaseTypeToStandard(dbTypeName, nullable)
}

// mapSnowflakeTypeToStandard maps Snowflake types to standard types
func (ss *streamingService) mapSnowflakeTypeToStandard(dbTypeName string, nullable bool) model.StandardizedType {
	return ss.mapGenericDatabaseTypeToStandard(dbTypeName, nullable)
}

// mapBigQueryTypeToStandard maps BigQuery types to standard types
func (ss *streamingService) mapBigQueryTypeToStandard(dbTypeName string, nullable bool) model.StandardizedType {
	return ss.mapGenericDatabaseTypeToStandard(dbTypeName, nullable)
}

// mapDatabricksTypeToStandard maps Databricks types to standard types
func (ss *streamingService) mapDatabricksTypeToStandard(dbTypeName string, nullable bool) model.StandardizedType {
	return ss.mapGenericDatabaseTypeToStandard(dbTypeName, nullable)
}

// mapRedshiftTypeToStandard maps Redshift types to standard types
func (ss *streamingService) mapRedshiftTypeToStandard(dbTypeName string, nullable bool) model.StandardizedType {
	return ss.mapGenericDatabaseTypeToStandard(dbTypeName, nullable)
}
