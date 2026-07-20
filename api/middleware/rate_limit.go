package middleware

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

const rateLimiterKeyPrefix = "rate_limit:"

var redisTokenBucket = redis.NewScript(`
local current = redis.call('TIME')
local now = tonumber(current[1]) + tonumber(current[2]) / 1000000
local values = redis.call('HMGET', KEYS[1], 'tokens', 'last_refill')
local tokens = tonumber(values[1])
local last_refill = tonumber(values[2])
local rate = tonumber(ARGV[1])
local burst = tonumber(ARGV[2])
if tokens == nil then tokens = burst end
if last_refill == nil then last_refill = now end
tokens = math.min(burst, tokens + math.max(0, now - last_refill) * rate)
local allowed = 0
local retry_ms = 0
if tokens >= 1 then
  allowed = 1
  tokens = tokens - 1
else
  retry_ms = math.ceil((1 - tokens) / rate * 1000)
end
redis.call('HSET', KEYS[1], 'tokens', tokens, 'last_refill', now)
redis.call('PEXPIRE', KEYS[1], math.ceil((burst / rate) * 2000))
return {allowed, retry_ms}
`)

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
	rdb       *redis.Client
}

// NewRedisRateLimiterStore creates a replica-shared limiter. Redis failures are
// returned to middleware and never degrade to a process-local quota.
func NewRedisRateLimiterStore(rdb *redis.Client, r rate.Limit, b int) *RateLimiterStore {
	s := NewRateLimiterStore(r, b)
	s.rdb = rdb
	return s
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

func (s *RateLimiterStore) allow(ctx context.Context, key string) (bool, time.Duration, error) {
	if s.rdb == nil {
		allowed := s.get(key).Allow()
		if allowed {
			return true, 0, nil
		}
		return false, localRetryAfter(s.r), nil
	}
	result, err := redisTokenBucket.Run(ctx, s.rdb, []string{rateLimiterKeyPrefix + key}, float64(s.r), s.b).Result()
	if err != nil {
		return false, 0, fmt.Errorf("distributed rate limit: %w", err)
	}
	values, ok := result.([]interface{})
	if !ok || len(values) != 2 {
		return false, 0, fmt.Errorf("distributed rate limit: unexpected result %T", result)
	}
	allowed, err := redisInt64(values[0])
	if err != nil {
		return false, 0, err
	}
	retryMS, err := redisInt64(values[1])
	if err != nil {
		return false, 0, err
	}
	return allowed == 1, time.Duration(retryMS) * time.Millisecond, nil
}

func redisInt64(value interface{}) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		parsed, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("distributed rate limit: parse result: %w", err)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("distributed rate limit: unexpected value %T", value)
	}
}

func localRetryAfter(r rate.Limit) time.Duration {
	return time.Duration(math.Ceil(float64(time.Second) / float64(r)))
}

// RateLimit returns a middleware that limits requests per client IP.
func RateLimit(store *RateLimiterStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		applyRateLimit(c, store, c.ClientIP())
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
		if key == "" {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many requests"})
			return
		}
		applyRateLimit(c, store, key)
	}
}

func applyRateLimit(c *gin.Context, store *RateLimiterStore, identity string) {
	route := c.FullPath()
	if route == "" {
		route = c.Request.URL.Path
	}
	allowed, retryAfter, err := store.allow(c.Request.Context(), route+":"+identity)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "rate limit unavailable"})
		return
	}
	if !allowed {
		seconds := int(math.Ceil(retryAfter.Seconds()))
		if seconds < 1 {
			seconds = 1
		}
		c.Header("Retry-After", strconv.Itoa(seconds))
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many requests"})
		return
	}
	c.Next()
}
