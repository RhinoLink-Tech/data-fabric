package security

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	TablePolicyPropagationWindow = 3 * time.Second
	TablePolicyMaximumStaleness  = 3 * time.Second
	tablePolicyPollInterval      = 250 * time.Millisecond
	tablePolicyHeadID            = 1
)

var (
	ErrTablePolicyRevisionConflict = errors.New("table policy revision conflict")
	ErrTablePolicyInvalid          = errors.New("table policy is invalid")
	ErrTablePolicyAuditUnavailable = errors.New("table policy publication audit is unavailable")
)

type tablePolicyHead struct {
	ID       uint8 `gorm:"primaryKey"`
	Revision int64 `gorm:"not null"`
}

func (tablePolicyHead) TableName() string { return "table_policy_head" }

type tablePolicyVersion struct {
	Revision    int64     `gorm:"primaryKey"`
	ConfigJSON  string    `gorm:"type:longtext;not null"`
	PublishedBy string    `gorm:"size:255;not null"`
	SourceLabel string    `gorm:"size:64"`
	CreatedAt   time.Time `gorm:"not null"`
}

func (tablePolicyVersion) TableName() string { return "table_policy_versions" }

type tablePolicyPublication struct {
	ID               string    `gorm:"type:char(36);primaryKey"`
	ExpectedRevision int64     `gorm:"not null;index"`
	Revision         int64     `gorm:"not null;index"`
	PublishedBy      string    `gorm:"size:255;not null;index"`
	Outcome          string    `gorm:"size:24;not null;index"`
	ReasonCode       string    `gorm:"size:64"`
	CreatedAt        time.Time `gorm:"not null;index"`
}

func (tablePolicyPublication) TableName() string { return "table_policy_publications" }

type TablePolicyPublicationRecord struct {
	Revision         int64     `json:"revision"`
	ExpectedRevision int64     `json:"expectedRevision"`
	PublishedBy      string    `json:"publishedBy"`
	Outcome          string    `json:"outcome"`
	ReasonCode       string    `json:"reasonCode,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}

type TablePolicyStatus struct {
	Revision          string    `json:"revision"`
	PublishedBy       string    `json:"publishedBy,omitempty"`
	UpdatedAt         time.Time `json:"updatedAt"`
	LastSyncAt        time.Time `json:"lastSyncAt"`
	Fresh             bool      `json:"fresh"`
	PropagationWindow string    `json:"propagationWindow"`
	MaximumStaleness  string    `json:"maximumStaleness"`
	LastSyncError     string    `json:"lastSyncError,omitempty"`
	SourceLabel       string    `json:"sourceLabel,omitempty"`
}

type TablePolicyPublicationResult struct {
	PreviousRevision  string `json:"previousRevision"`
	Revision          string `json:"revision"`
	PublishedBy       string `json:"publishedBy"`
	Outcome           string `json:"outcome"`
	AuditRecorded     bool   `json:"auditRecorded"`
	PropagationWindow string `json:"propagationWindow"`
}

type TablePolicyManager struct {
	db           *gorm.DB
	authorizer   *TableAuthorizer
	pollInterval time.Duration
	maxStaleness time.Duration
	sourceLabel  string

	mu                  sync.Mutex
	lastSyncError       string
	syncFailureObserver func()
	cancel              context.CancelFunc
	done                chan struct{}
}

func NewTablePolicyManager(db *gorm.DB, authorizer *TableAuthorizer, initial TableAuthorizationConfig, sourceLabel string) (*TablePolicyManager, error) {
	if db == nil || authorizer == nil {
		return nil, fmt.Errorf("policy database and table authorizer are required")
	}
	if !initial.Enabled && !db.Migrator().HasTable(&tablePolicyHead{}) {
		return nil, nil
	}
	return newTablePolicyManager(db, authorizer, initial, sourceLabel, tablePolicyPollInterval, TablePolicyMaximumStaleness)
}

func newTablePolicyManager(db *gorm.DB, authorizer *TableAuthorizer, initial TableAuthorizationConfig, sourceLabel string, pollInterval, maxStaleness time.Duration) (*TablePolicyManager, error) {
	if db == nil || authorizer == nil {
		return nil, fmt.Errorf("policy database and table authorizer are required")
	}
	if pollInterval <= 0 || maxStaleness <= pollInterval {
		return nil, fmt.Errorf("policy poll interval must be positive and less than maximum staleness")
	}
	manager := &TablePolicyManager{
		db: db, authorizer: authorizer, pollInterval: pollInterval,
		maxStaleness: maxStaleness, sourceLabel: strings.TrimSpace(sourceLabel),
	}
	if err := db.AutoMigrate(&tablePolicyHead{}, &tablePolicyVersion{}, &tablePolicyPublication{}); err != nil {
		return nil, fmt.Errorf("migrate table policy metadata: %w", err)
	}
	snapshot, err := manager.initialize(context.Background(), initial)
	if err != nil {
		return nil, err
	}
	authorizer.install(snapshot, maxStaleness)
	return manager, nil
}

func (m *TablePolicyManager) initialize(ctx context.Context, initial TableAuthorizationConfig) (*TableAuthorizationSnapshot, error) {
	configJSON, err := json.Marshal(initial)
	if err != nil {
		return nil, fmt.Errorf("encode initial table policy: %w", err)
	}
	var currentRevision int64
	err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		head := tablePolicyHead{ID: tablePolicyHeadID}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&head).Error; err != nil {
			return fmt.Errorf("create table policy head: %w", err)
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&head, tablePolicyHeadID).Error; err != nil {
			return fmt.Errorf("read table policy head: %w", err)
		}
		if head.Revision == 0 {
			if !initial.Enabled {
				return fmt.Errorf("managed table policy has no active revision and startup configuration is disabled")
			}
			if err := initial.Validate(); err != nil {
				return fmt.Errorf("invalid initial table policy: %w", err)
			}
			now := time.Now().UTC()
			version := tablePolicyVersion{
				Revision: 1, ConfigJSON: string(configJSON), PublishedBy: "startup",
				SourceLabel: m.sourceLabel, CreatedAt: now,
			}
			if err := tx.Create(&version).Error; err != nil {
				return fmt.Errorf("seed table policy revision: %w", err)
			}
			head.Revision = version.Revision
			if err := tx.Save(&head).Error; err != nil {
				return fmt.Errorf("activate initial table policy: %w", err)
			}
			publication := tablePolicyPublication{
				ID: uuid.NewString(), Revision: version.Revision, PublishedBy: "startup",
				Outcome: "activated", ReasonCode: "initial_config", CreatedAt: now,
			}
			if err := tx.Create(&publication).Error; err != nil {
				return fmt.Errorf("record initial table policy activation: %w", err)
			}
		}
		currentRevision = head.Revision
		return nil
	})
	if err != nil {
		return nil, err
	}
	snapshot, err := m.loadRevision(ctx, currentRevision)
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (m *TablePolicyManager) loadHead(ctx context.Context) (int64, error) {
	var head tablePolicyHead
	if err := m.db.WithContext(ctx).First(&head, tablePolicyHeadID).Error; err != nil {
		return 0, err
	}
	if head.Revision <= 0 {
		return 0, fmt.Errorf("table policy head has no active revision")
	}
	return head.Revision, nil
}

func (m *TablePolicyManager) loadRevision(ctx context.Context, revision int64) (*TableAuthorizationSnapshot, error) {
	var version tablePolicyVersion
	if err := m.db.WithContext(ctx).First(&version, revision).Error; err != nil {
		return nil, fmt.Errorf("load table policy revision %d: %w", revision, err)
	}
	var policy TableAuthorizationConfig
	if err := json.Unmarshal([]byte(version.ConfigJSON), &policy); err != nil {
		return nil, fmt.Errorf("decode table policy revision %d: %w", revision, err)
	}
	if err := policy.Validate(); err != nil {
		return nil, fmt.Errorf("validate table policy revision %d: %w", revision, err)
	}
	if !policy.Enabled {
		return nil, fmt.Errorf("table policy revision %d disables authorization", revision)
	}
	return &TableAuthorizationSnapshot{
		Config: policy, Revision: fmt.Sprint(revision), PublishedBy: version.PublishedBy,
		SourceLabel: version.SourceLabel, UpdatedAt: version.CreatedAt,
	}, nil
}

func (m *TablePolicyManager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	m.cancel, m.done = cancel, done
	m.mu.Unlock()
	go func() {
		defer close(done)
		ticker := time.NewTicker(m.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-loopCtx.Done():
				return
			case <-ticker.C:
				if err := m.refresh(loopCtx); err != nil {
					m.setSyncError(err)
				}
			}
		}
	}()
}

func (m *TablePolicyManager) Stop() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done = nil, nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (m *TablePolicyManager) refresh(ctx context.Context) error {
	revision, err := m.loadHead(ctx)
	if err != nil {
		return err
	}
	current := m.authorizer.current.Load()
	if current == nil || current.Revision != fmt.Sprint(revision) {
		snapshot, err := m.loadRevision(ctx, revision)
		if err != nil {
			return err
		}
		m.authorizer.install(snapshot, m.maxStaleness)
	} else {
		m.authorizer.markSynchronized()
	}
	m.setSyncError(nil)
	return nil
}

func (m *TablePolicyManager) setSyncError(err error) {
	m.mu.Lock()
	previous := m.lastSyncError
	if err == nil {
		m.lastSyncError = ""
	} else {
		m.lastSyncError = err.Error()
	}
	current := m.lastSyncError
	observer := m.syncFailureObserver
	m.mu.Unlock()
	if err != nil && observer != nil {
		observer()
	}
	if current != "" && current != previous {
		slog.Error("table policy replica could not synchronize", "error", current)
	}
}

// SetSyncFailureObserver registers a bounded operational signal for failed
// refresh attempts. The observer must not retain or expose the underlying error.
func (m *TablePolicyManager) SetSyncFailureObserver(observer func()) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.syncFailureObserver = observer
	m.mu.Unlock()
}

func (m *TablePolicyManager) Status() TablePolicyStatus {
	current := m.authorizer.current.Load()
	status := TablePolicyStatus{
		PropagationWindow: TablePolicyPropagationWindow.String(),
		MaximumStaleness:  m.maxStaleness.String(),
	}
	if current != nil {
		status.Revision = current.Revision
		status.PublishedBy = current.PublishedBy
		status.UpdatedAt = current.UpdatedAt
		status.SourceLabel = current.SourceLabel
	}
	lastSync := m.authorizer.lastSyncNS.Load()
	if lastSync > 0 {
		status.LastSyncAt = time.Unix(0, lastSync).UTC()
	}
	status.Fresh = !m.authorizer.isStale(time.Now())
	m.mu.Lock()
	status.LastSyncError = m.lastSyncError
	m.mu.Unlock()
	return status
}

func (m *TablePolicyManager) CurrentSnapshot() TableAuthorizationSnapshot {
	current := m.authorizer.current.Load()
	if current == nil {
		return TableAuthorizationSnapshot{}
	}
	return *current
}

// GetRevision returns a validated historical version for review or rollback.
// Republishing it still creates a new monotonically increasing revision.
func (m *TablePolicyManager) GetRevision(ctx context.Context, revision int64) (TableAuthorizationSnapshot, error) {
	if revision <= 0 {
		return TableAuthorizationSnapshot{}, fmt.Errorf("revision must be positive")
	}
	snapshot, err := m.loadRevision(ctx, revision)
	if err != nil {
		return TableAuthorizationSnapshot{}, err
	}
	return *snapshot, nil
}

func (m *TablePolicyManager) Validate(policy TableAuthorizationConfig) error {
	if !policy.Enabled {
		return fmt.Errorf("%w: enabled must remain true while managed policy publication is active", ErrTablePolicyInvalid)
	}
	if err := policy.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrTablePolicyInvalid, err)
	}
	return nil
}

func (m *TablePolicyManager) Publish(ctx context.Context, expectedRevision int64, publisher string, policy TableAuthorizationConfig) (TablePolicyPublicationResult, error) {
	publisher = strings.TrimSpace(publisher)
	if publisher == "" {
		publisher = "unknown"
	}
	if len(publisher) > 255 {
		publisher = publisher[:255]
	}
	current := m.authorizer.current.Load()
	result := TablePolicyPublicationResult{
		PublishedBy: publisher, PropagationWindow: TablePolicyPropagationWindow.String(),
	}
	if current != nil {
		result.PreviousRevision = current.Revision
	}
	if err := m.Validate(policy); err != nil {
		if recordErr := m.recordAttempt(ctx, expectedRevision, 0, publisher, "rejected", "validation_failed"); recordErr != nil {
			return result, fmt.Errorf("%w: %v (validation: %v)", ErrTablePolicyAuditUnavailable, recordErr, err)
		}
		result.Outcome, result.AuditRecorded = "rejected", true
		return result, err
	}
	configJSON, err := json.Marshal(policy)
	if err != nil {
		if recordErr := m.recordAttempt(ctx, expectedRevision, 0, publisher, "rejected", "encoding_failed"); recordErr != nil {
			return result, fmt.Errorf("%w: %v", ErrTablePolicyAuditUnavailable, recordErr)
		}
		result.Outcome, result.AuditRecorded = "rejected", true
		return result, fmt.Errorf("%w: encode policy: %v", ErrTablePolicyInvalid, err)
	}
	var nextRevision int64
	var publishErr error
	err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var head tablePolicyHead
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&head, tablePolicyHeadID).Error; err != nil {
			return fmt.Errorf("lock table policy head: %w", err)
		}
		result.PreviousRevision = fmt.Sprint(head.Revision)
		if head.Revision != expectedRevision {
			publishErr = ErrTablePolicyRevisionConflict
			return tx.Create(&tablePolicyPublication{
				ID: uuid.NewString(), ExpectedRevision: expectedRevision, Revision: head.Revision,
				PublishedBy: publisher, Outcome: "conflict", ReasonCode: "revision_mismatch", CreatedAt: time.Now().UTC(),
			}).Error
		}
		if head.Revision == math.MaxInt64 {
			publishErr = fmt.Errorf("table policy revision exhausted")
			return tx.Create(&tablePolicyPublication{
				ID: uuid.NewString(), ExpectedRevision: expectedRevision, Revision: head.Revision,
				PublishedBy: publisher, Outcome: "rejected", ReasonCode: "revision_exhausted", CreatedAt: time.Now().UTC(),
			}).Error
		}
		nextRevision = head.Revision + 1
		now := time.Now().UTC()
		version := tablePolicyVersion{
			Revision: nextRevision, ConfigJSON: string(configJSON), PublishedBy: publisher,
			SourceLabel: m.sourceLabel, CreatedAt: now,
		}
		if err := tx.Create(&version).Error; err != nil {
			return fmt.Errorf("store table policy version: %w", err)
		}
		if err := tx.Model(&head).Update("revision", nextRevision).Error; err != nil {
			return fmt.Errorf("activate table policy version: %w", err)
		}
		return tx.Create(&tablePolicyPublication{
			ID: uuid.NewString(), ExpectedRevision: expectedRevision, Revision: nextRevision,
			PublishedBy: publisher, Outcome: "activated", ReasonCode: "published", CreatedAt: now,
		}).Error
	})
	if err != nil {
		return result, err
	}
	if publishErr != nil {
		if errors.Is(publishErr, ErrTablePolicyRevisionConflict) {
			result.Outcome = "conflict"
		} else {
			result.Outcome = "rejected"
		}
		result.AuditRecorded = true
		return result, publishErr
	}
	snapshot := &TableAuthorizationSnapshot{
		Config: policy, Revision: fmt.Sprint(nextRevision), PublishedBy: publisher,
		SourceLabel: m.sourceLabel, UpdatedAt: time.Now().UTC(),
	}
	m.authorizer.install(snapshot, m.maxStaleness)
	m.setSyncError(nil)
	result.Revision, result.Outcome, result.AuditRecorded = snapshot.Revision, "activated", true
	return result, nil
}

func (m *TablePolicyManager) recordAttempt(ctx context.Context, expectedRevision, revision int64, publisher, outcome, reason string) error {
	return m.db.WithContext(ctx).Create(&tablePolicyPublication{
		ID: uuid.NewString(), ExpectedRevision: expectedRevision, Revision: revision,
		PublishedBy: publisher, Outcome: outcome, ReasonCode: reason, CreatedAt: time.Now().UTC(),
	}).Error
}

func (m *TablePolicyManager) Publications(ctx context.Context, limit int) ([]TablePolicyPublicationRecord, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	var rows []tablePolicyPublication
	if err := m.db.WithContext(ctx).Order("created_at DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]TablePolicyPublicationRecord, 0, len(rows))
	for _, row := range rows {
		out = append(out, TablePolicyPublicationRecord{
			Revision: row.Revision, ExpectedRevision: row.ExpectedRevision, PublishedBy: row.PublishedBy,
			Outcome: row.Outcome, ReasonCode: row.ReasonCode, CreatedAt: row.CreatedAt,
		})
	}
	return out, nil
}
