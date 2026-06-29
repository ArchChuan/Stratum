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
	mu       sync.Mutex
	limiters map[string]*ipLimiter
	r        rate.Limit
	b        int
}

// NewRateLimiterStore creates a store with the given rate and burst.
// Stale entries are pruned every 10 minutes.
func NewRateLimiterStore(r rate.Limit, b int) *RateLimiterStore {
	s := &RateLimiterStore{
		limiters: make(map[string]*ipLimiter),
		r:        r,
		b:        b,
	}
	go s.cleanup()
	return s
}

func (s *RateLimiterStore) get(ip string) *rate.Limiter {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.limiters[ip]
	if !ok {
		entry = &ipLimiter{limiter: rate.NewLimiter(s.r, s.b)}
		s.limiters[ip] = entry
	}
	entry.lastSeen = time.Now()
	return entry.limiter
}

func (s *RateLimiterStore) cleanup() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		for ip, entry := range s.limiters {
			if time.Since(entry.lastSeen) > 30*time.Minute {
				delete(s.limiters, ip)
			}
		}
		s.mu.Unlock()
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
