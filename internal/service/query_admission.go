package service

import (
	"strings"
	"sync"
	"time"

	"github.com/RhinoLink-Tech/data-fabric/internal/security"
	"golang.org/x/time/rate"
)

const (
	defaultMaxConcurrentQueries            = 100
	defaultMaxConcurrentQueriesPerTenant   = 25
	defaultMaxQueryRequestRateLimitBuckets = 10000
)

// QueryAdmissionLimiter bounds source work shared by streaming and pagination
// services and may apply a shared per-tenant query request-rate budget.
type QueryAdmissionLimiter struct {
	mu                         sync.Mutex
	maxConcurrent              int
	maxPerTenant               int
	total                      int
	byCallerKey                map[string]int
	requestsPerMinutePerTenant int
	requestBurstPerTenant      int
	maxRequestBuckets          int
	requestBuckets             map[string]*queryRequestRateBucket
	lastRequestBucketCleanupAt time.Time
}

type queryRequestRateBucket struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewQueryAdmissionLimiter returns a process-local limiter. Non-positive
// limits use the product defaults of 100 total slots and 25 per tenant.
func NewQueryAdmissionLimiter(maxConcurrent, maxPerTenant int) *QueryAdmissionLimiter {
	return NewQueryAdmissionLimiterWithRateLimit(maxConcurrent, maxPerTenant, 0, 0)
}

// NewQueryAdmissionLimiterWithRateLimit returns a process-local limiter for
// concurrent query work and, when configured, per-tenant query request rates.
// A non-positive rate or burst disables the request-rate budget.
func NewQueryAdmissionLimiterWithRateLimit(maxConcurrent, maxPerTenant, requestsPerMinutePerTenant, requestBurstPerTenant int) *QueryAdmissionLimiter {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrentQueries
	}
	if maxPerTenant <= 0 {
		maxPerTenant = defaultMaxConcurrentQueriesPerTenant
	}
	return &QueryAdmissionLimiter{
		maxConcurrent:              maxConcurrent,
		maxPerTenant:               maxPerTenant,
		byCallerKey:                make(map[string]int),
		requestsPerMinutePerTenant: requestsPerMinutePerTenant,
		requestBurstPerTenant:      requestBurstPerTenant,
		maxRequestBuckets:          defaultMaxQueryRequestRateLimitBuckets,
		requestBuckets:             make(map[string]*queryRequestRateBucket),
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

// AllowQueryRequest charges one query execution or continuation request to its
// validated tenant bucket. Tenant-less callers use their validated user ID;
// anonymous callers share one process-local bucket. New buckets are rejected
// when the process-local bucket table reaches its fixed cardinality bound.
func (limiter *QueryAdmissionLimiter) AllowQueryRequest(caller *security.Caller) bool {
	if limiter == nil || limiter.requestsPerMinutePerTenant <= 0 || limiter.requestBurstPerTenant <= 0 {
		return true
	}

	key := queryAdmissionKey(caller)
	now := time.Now()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()

	if limiter.requestBuckets == nil {
		limiter.requestBuckets = make(map[string]*queryRequestRateBucket)
	}
	if now.Sub(limiter.lastRequestBucketCleanupAt) >= time.Minute {
		for bucketKey, bucket := range limiter.requestBuckets {
			if now.Sub(bucket.lastSeen) > 10*time.Minute {
				delete(limiter.requestBuckets, bucketKey)
			}
		}
		limiter.lastRequestBucketCleanupAt = now
	}

	bucket := limiter.requestBuckets[key]
	maxRequestBuckets := limiter.maxRequestBuckets
	if maxRequestBuckets <= 0 {
		maxRequestBuckets = defaultMaxQueryRequestRateLimitBuckets
	}
	if bucket == nil && len(limiter.requestBuckets) >= maxRequestBuckets {
		return false
	}
	if bucket == nil {
		bucket = &queryRequestRateBucket{
			limiter: rate.NewLimiter(
				rate.Limit(float64(limiter.requestsPerMinutePerTenant)/60),
				limiter.requestBurstPerTenant,
			),
		}
		limiter.requestBuckets[key] = bucket
	}
	bucket.lastSeen = now
	return bucket.limiter.AllowN(now, 1)
}
