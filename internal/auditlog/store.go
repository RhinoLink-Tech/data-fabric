package auditlog

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/sqlanalysis"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"
	DecisionError = "error"
)

// Event contains identifiers and outcomes only. SQL text, bind values,
// credentials, and result rows are never persisted.
type Event struct {
	ID               string    `json:"id" gorm:"type:char(36);primaryKey"`
	CorrelationID    string    `json:"correlationId,omitempty" gorm:"size:128;index"`
	ActorID          string    `json:"actorId,omitempty" gorm:"size:255;index"`
	TenantID         string    `json:"tenantId,omitempty" gorm:"size:255;index"`
	Transport        string    `json:"transport" gorm:"size:32;not null;index"`
	Operation        string    `json:"operation" gorm:"size:255;not null;index"`
	DataSourceID     string    `json:"dataSourceId,omitempty" gorm:"size:255;index"`
	Tables           []string  `json:"tables,omitempty" gorm:"serializer:json;type:json"`
	QueryFingerprint string    `json:"queryFingerprint,omitempty" gorm:"size:64"`
	Decision         string    `json:"decision" gorm:"size:16;not null;index"`
	ReasonCode       string    `json:"reasonCode,omitempty" gorm:"size:128"`
	Outcome          string    `json:"outcome" gorm:"size:16;not null;index"`
	StatusCode       int       `json:"statusCode,omitempty"`
	DurationMS       int64     `json:"durationMs"`
	RowsReturned     int64     `json:"rowsReturned"`
	OccurredAt       time.Time `json:"occurredAt" gorm:"not null;index"`
}

func (Event) TableName() string { return "audit_events" }

type Filter struct {
	ActorID       string
	TenantID      string
	Transport     string
	Operation     string
	DataSourceID  string
	Decision      string
	CorrelationID string
	From          *time.Time
	To            *time.Time
	Limit         int
	Offset        int
}

type Page struct {
	Events []Event `json:"events"`
	Total  int64   `json:"total"`
	Limit  int     `json:"limit"`
	Offset int     `json:"offset"`
}

type Recorder interface {
	Record(context.Context, Event) error
}

type Store struct {
	db             *gorm.DB
	fingerprintKey []byte
	retentionDays  int
}

type captureContextKey struct{}
type tableCapture struct {
	mu         sync.Mutex
	names      []string
	decision   string
	reasonCode string
}

func WithCapture(ctx context.Context) context.Context {
	if ctx.Value(captureContextKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, captureContextKey{}, &tableCapture{})
}

// AddTableReferences reuses table identifiers already extracted for policy
// evaluation, avoiding a second parse on the query path.
func AddTableReferences(ctx context.Context, references []sqlanalysis.TableReference) {
	capture, _ := ctx.Value(captureContextKey{}).(*tableCapture)
	if capture == nil {
		return
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	seen := make(map[string]struct{}, len(capture.names))
	for _, name := range capture.names {
		seen[name] = struct{}{}
	}
	for _, ref := range references {
		parts := make([]string, 0, 3)
		if ref.Catalog != "" {
			parts = append(parts, ref.Catalog)
		}
		if ref.Schema != "" {
			parts = append(parts, ref.Schema)
		}
		if ref.Table != "" {
			parts = append(parts, ref.Table)
		}
		name := strings.Join(parts, ".")
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		capture.names = append(capture.names, name)
	}
}

func CapturedTables(ctx context.Context) []string {
	capture, _ := ctx.Value(captureContextKey{}).(*tableCapture)
	if capture == nil {
		return nil
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return append([]string(nil), capture.names...)
}

func MarkDecision(ctx context.Context, decision, reasonCode string) {
	capture, _ := ctx.Value(captureContextKey{}).(*tableCapture)
	if capture == nil {
		return
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.decision, capture.reasonCode = decision, reasonCode
}

func CapturedDecision(ctx context.Context) (string, string) {
	capture, _ := ctx.Value(captureContextKey{}).(*tableCapture)
	if capture == nil {
		return "", ""
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.decision, capture.reasonCode
}

func Migrate(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("audit database is required")
	}
	return db.AutoMigrate(&Event{})
}

func NewStore(db *gorm.DB, fingerprintSecret string, retentionDays int) (*Store, error) {
	if db == nil {
		return nil, fmt.Errorf("audit database is required")
	}
	if retentionDays <= 0 {
		retentionDays = 90
	}
	key := []byte(strings.TrimSpace(fingerprintSecret))
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate audit fingerprint key: %w", err)
		}
	}
	return &Store{db: db, fingerprintKey: key, retentionDays: retentionDays}, nil
}

// Fingerprint creates a keyed digest so common statements cannot be recovered
// from the audit table using a simple dictionary of candidate SQL strings.
func (s *Store) Fingerprint(query string) string {
	normalized := strings.Join(strings.Fields(query), " ")
	if normalized == "" {
		return ""
	}
	mac := hmac.New(sha256.New, s.fingerprintKey)
	_, _ = mac.Write([]byte(normalized))
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Store) Record(ctx context.Context, event Event) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("audit store is unavailable")
	}
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = time.Now().UTC()
	}
	event.CorrelationID = truncateRunes(event.CorrelationID, 128)
	event.ActorID = truncateRunes(event.ActorID, 255)
	event.TenantID = truncateRunes(event.TenantID, 255)
	event.Transport = truncateRunes(event.Transport, 32)
	event.Operation = truncateRunes(event.Operation, 255)
	event.DataSourceID = truncateRunes(event.DataSourceID, 255)
	event.ReasonCode = truncateRunes(event.ReasonCode, 128)
	if len(event.Tables) > 100 {
		event.Tables = event.Tables[:100]
	}
	for i := range event.Tables {
		event.Tables[i] = truncateRunes(event.Tables[i], 255)
	}
	if event.Tables == nil {
		event.Tables = []string{}
	}
	return s.db.WithContext(ctx).Create(&event).Error
}

func truncateRunes(value string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}

func (s *Store) List(ctx context.Context, filter Filter) (*Page, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	query := s.db.WithContext(ctx).Model(&Event{})
	if filter.ActorID != "" {
		query = query.Where("actor_id = ?", filter.ActorID)
	}
	if filter.TenantID != "" {
		query = query.Where("tenant_id = ?", filter.TenantID)
	}
	if filter.Transport != "" {
		query = query.Where("transport = ?", filter.Transport)
	}
	if filter.Operation != "" {
		query = query.Where("operation = ?", filter.Operation)
	}
	if filter.DataSourceID != "" {
		query = query.Where("data_source_id = ?", filter.DataSourceID)
	}
	if filter.Decision != "" {
		query = query.Where("decision = ?", filter.Decision)
	}
	if filter.CorrelationID != "" {
		query = query.Where("correlation_id = ?", filter.CorrelationID)
	}
	if filter.From != nil {
		query = query.Where("occurred_at >= ?", *filter.From)
	}
	if filter.To != nil {
		query = query.Where("occurred_at < ?", *filter.To)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, err
	}
	events := make([]Event, 0, limit)
	if err := query.Order("occurred_at DESC, id DESC").Limit(limit).Offset(offset).Find(&events).Error; err != nil {
		return nil, err
	}
	return &Page{Events: events, Total: total, Limit: limit, Offset: offset}, nil
}

func (s *Store) PruneExpired(ctx context.Context) (int64, error) {
	cutoff := time.Now().UTC().AddDate(0, 0, -s.retentionDays)
	var total int64
	for {
		ids := make([]string, 0, 1000)
		if err := s.db.WithContext(ctx).Model(&Event{}).Where("occurred_at < ?", cutoff).
			Order("occurred_at ASC, id ASC").Limit(1000).Pluck("id", &ids).Error; err != nil {
			return total, err
		}
		if len(ids) == 0 {
			return total, nil
		}
		result := s.db.WithContext(ctx).Where("id IN ?", ids).Delete(&Event{})
		if result.Error != nil {
			return total, result.Error
		}
		if result.RowsAffected == 0 {
			return total, nil
		}
		total += result.RowsAffected
	}
}

func (s *Store) RetentionDays() int { return s.retentionDays }
