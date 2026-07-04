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
	Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error)
	HGetAll(ctx context.Context, key string) (map[string]string, error)
}
