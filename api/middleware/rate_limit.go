package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type ipLimiter struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// RateLimiterStore holds per-IP token-bucket limiters.
type RateLimiterStore struct {
	mu        sync.Mutex
	limiters  map[string]*ipLimiter
	r         rate.Limit
	b         int
	doneCh    chan struct{}
	lastPrune time.Time
}

// NewRateLimiterStore creates a store with the given rate and burst.
// Stale entries are pruned every 10 minutes.
func NewRateLimiterStore(r rate.Limit, b int) *RateLimiterStore {
	s := &RateLimiterStore{
		limiters: make(map[string]*ipLimiter),
		r:        r,
		b:        b,
		doneCh:   make(chan struct{}),
	}
	close(s.doneCh)
	return s
}

// Stop is retained for lifecycle symmetry. RateLimiterStore has no background
// goroutine; the returned channel is already closed.
func (s *RateLimiterStore) Stop() <-chan struct{} {
	return s.doneCh
}

func (s *RateLimiterStore) get(ip string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	entry, ok := s.limiters[ip]
	if !ok {
		entry = &ipLimiter{limiter: rate.NewLimiter(s.r, s.b)}
		s.limiters[ip] = entry
	}
	entry.lastSeen = now
	if s.lastPrune.IsZero() {
		s.lastPrune = now
	}
	if now.Sub(s.lastPrune) >= 10*time.Minute {
		s.pruneLocked(now)
		s.lastPrune = now
	}
	return entry.limiter
}

func (s *RateLimiterStore) pruneLocked(now time.Time) {
	for ip, entry := range s.limiters {
		if now.Sub(entry.lastSeen) > 30*time.Minute {
			delete(s.limiters, ip)
		}
	}
}

// RateLimit returns a middleware that limits requests per client IP.
func RateLimit(store *RateLimiterStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !store.get(c.ClientIP()).Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many requests"})
			return
		}
		c.Next()
	}
}

const (
	LLMExecRate  = rate.Limit(1.0 / 3.0) // 20 req/min per user
	LLMExecBurst = 3

	// AuthRate/AuthBurst: per-IP bucket for auth endpoints.
	// 5 req/s sustained, burst 50 — handles shared-IP demo traffic without
	// blocking normal login/refresh flows.
	AuthRate  = rate.Limit(5)
	AuthBurst = 50
)

// RateLimitByKey limits requests using a caller-supplied key function.
// Empty key rejects unauthenticated callers.
func RateLimitByKey(store *RateLimiterStore, keyFn func(*gin.Context) string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := keyFn(c)
		if key == "" || !store.get(key).Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many requests"})
			return
		}
		c.Next()
	}
}
