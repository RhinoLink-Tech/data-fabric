package service

import (
	"strings"
	"sync"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

const (
	defaultMaxConcurrentQueries          = 100
	defaultMaxConcurrentQueriesPerTenant = 25
)

// QueryAdmissionLimiter bounds source work shared by streaming and pagination
// services. A reservation covers stream setup and its open cursor, or one
// paginated query/batch operation.
type QueryAdmissionLimiter struct {
	mu            sync.Mutex
	maxConcurrent int
	maxPerTenant  int
	total         int
	byCallerKey   map[string]int
}

// NewQueryAdmissionLimiter returns a process-local limiter. Non-positive
// limits use the product defaults of 100 total slots and 25 per tenant.
func NewQueryAdmissionLimiter(maxConcurrent, maxPerTenant int) *QueryAdmissionLimiter {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrentQueries
	}
	if maxPerTenant <= 0 {
		maxPerTenant = defaultMaxConcurrentQueriesPerTenant
	}
	return &QueryAdmissionLimiter{
		maxConcurrent: maxConcurrent,
		maxPerTenant:  maxPerTenant,
		byCallerKey:   make(map[string]int),
	}
}

func queryAdmissionKey(caller *security.Caller) string {
	if caller != nil {
		if tenantID := strings.TrimSpace(caller.TenantID); tenantID != "" {
			return "tenant:" + tenantID
		}
		if userID := strings.TrimSpace(caller.UserID); userID != "" {
			return "user:" + userID
		}
	}
	return "anonymous"
}

// Acquire reserves one query slot for caller. The returned release function is
// safe to invoke more than once.
func (limiter *QueryAdmissionLimiter) Acquire(caller *security.Caller) (func(), bool) {
	key := queryAdmissionKey(caller)

	limiter.mu.Lock()
	if limiter.total >= limiter.maxConcurrent || limiter.byCallerKey[key] >= limiter.maxPerTenant {
		limiter.mu.Unlock()
		return nil, false
	}
	limiter.total++
	limiter.byCallerKey[key]++
	limiter.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			limiter.mu.Lock()
			defer limiter.mu.Unlock()
			limiter.total--
			limiter.byCallerKey[key]--
			if limiter.byCallerKey[key] == 0 {
				delete(limiter.byCallerKey, key)
			}
		})
	}, true
}
