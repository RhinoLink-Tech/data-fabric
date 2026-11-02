package service

import (
	"strings"
	"sync"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
)

const (
	defaultMaxConcurrentStreams          = 100
	defaultMaxConcurrentStreamsPerTenant = 25
)

// streamAdmissionLimiter accounts for streams from the moment they are
// admitted until their SQL rows are closed. This includes query startup, so
// simultaneous requests cannot race past the process or caller limits.
type streamAdmissionLimiter struct {
	mu          sync.Mutex
	total       int
	byCallerKey map[string]int
}

func newStreamAdmissionLimiter() *streamAdmissionLimiter {
	return &streamAdmissionLimiter{byCallerKey: make(map[string]int)}
}

func streamAdmissionKey(caller *security.Caller) string {
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

func (limiter *streamAdmissionLimiter) acquire(caller *security.Caller, maxTotal, maxPerCaller int) (func(), bool) {
	if maxTotal <= 0 {
		maxTotal = defaultMaxConcurrentStreams
	}
	if maxPerCaller <= 0 {
		maxPerCaller = defaultMaxConcurrentStreamsPerTenant
	}
	key := streamAdmissionKey(caller)

	limiter.mu.Lock()
	if limiter.total >= maxTotal || limiter.byCallerKey[key] >= maxPerCaller {
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
