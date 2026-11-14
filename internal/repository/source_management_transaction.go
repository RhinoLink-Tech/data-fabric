package repository

import (
	"context"
	"gorm.io/gorm"
)

type sourceManagementTransaction struct {
	db          *gorm.DB
	afterCommit []func()
}
type sourceManagementTransactionKey struct{}

// WithSourceManagementTransaction binds the authorized metadata transaction to
// the management repository path. It is created only by trusted server code.
func WithSourceManagementTransaction(ctx context.Context, tx *gorm.DB) (context.Context, func()) {
	state := &sourceManagementTransaction{db: tx}
	return context.WithValue(ctx, sourceManagementTransactionKey{}, state), func() {
		for _, f := range state.afterCommit {
			f()
		}
	}
}

// SourceManagementDB selects the transaction bound to this request when one
// exists. It must only be used for the guarded source management operations.
func SourceManagementDB(ctx context.Context, fallback *gorm.DB) *gorm.DB {
	if state, ok := ctx.Value(sourceManagementTransactionKey{}).(*sourceManagementTransaction); ok && state.db != nil {
		return state.db.WithContext(ctx)
	}
	return fallback.WithContext(ctx)
}

// AfterSourceManagementCommit queues a side effect only if a guarded
// transaction exists; otherwise callers must perform it immediately.
func AfterSourceManagementCommit(ctx context.Context, f func()) bool {
	if state, ok := ctx.Value(sourceManagementTransactionKey{}).(*sourceManagementTransaction); ok && state.db != nil {
		state.afterCommit = append(state.afterCommit, f)
		return true
	}
	return false
}

// SourceManagementTransaction returns the active trusted management transaction.
func SourceManagementTransaction(ctx context.Context) *gorm.DB {
	if state, ok := ctx.Value(sourceManagementTransactionKey{}).(*sourceManagementTransaction); ok {
		return state.db
	}
	return nil
}
