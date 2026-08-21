package port

import (
	"context"
	"time"
)

// MessageBufferStore is the consumer-side port for transient conversation
// message buffering. Implementations may use Redis, but application code only
// depends on this key/value contract.
type MessageBufferStore interface {
	RPush(ctx context.Context, key string, value []byte) error
	Expire(ctx context.Context, key string, ttl time.Duration) error
	HSetNX(ctx context.Context, key, field string, value any) error
	HIncrBy(ctx context.Context, key, field string, incr int64) (int64, error)
	HSet(ctx context.Context, key string, values ...any) error
	LLen(ctx context.Context, key string) (int64, error)
	LIndex(ctx context.Context, key string, index int64) (string, bool, error)
	LRange(ctx context.Context, key string, start, stop int64) ([]string, error)
	Del(ctx context.Context, keys ...string) error
	// RemoveValues atomically removes exactly the given values from the list and
	// decrements byte_size on the meta hash by the corresponding size for each
	// value actually removed. When the list becomes empty it deletes both keys.
	// Implementations must be atomic (Redis Lua) so a flush can never delete a
	// message that arrived after the flush read its snapshot.
	RemoveValues(ctx context.Context, key, metaKey string, values []string, sizes []int64) error
	// TryAcquireFlushLock acquires a per-key single-flight lock so concurrent
	// flushers (multi-instance deployments, BufferScanner vs BufferMessage)
	// never drain the same buffer with divergent snapshots. Returns false when
	// another flusher holds the lock.
	TryAcquireFlushLock(ctx context.Context, key, token string, ttl time.Duration) (bool, error)
	// ReleaseFlushLock releases the per-key flush lock only if still owned by
	// token (avoids releasing a successor's lock after TTL expiry).
	ReleaseFlushLock(ctx context.Context, key, token string) error
	Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error)
	HGetAll(ctx context.Context, key string) (map[string]string, error)
}
