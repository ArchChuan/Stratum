package persistence

import (
	"context"
	"time"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/redis/go-redis/v9"
)

// RedisMessageBufferStore adapts go-redis to the memory message-buffer port.
type RedisMessageBufferStore struct {
	client *redis.Client
}

func NewRedisMessageBufferStore(client *redis.Client) memport.MessageBufferStore {
	if client == nil {
		return nil
	}
	return &RedisMessageBufferStore{client: client}
}

func (s *RedisMessageBufferStore) RPush(ctx context.Context, key string, value []byte) error {
	return s.client.RPush(ctx, key, value).Err()
}

func (s *RedisMessageBufferStore) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return s.client.Expire(ctx, key, ttl).Err()
}

func (s *RedisMessageBufferStore) HSetNX(ctx context.Context, key, field string, value any) error {
	return s.client.HSetNX(ctx, key, field, value).Err()
}

func (s *RedisMessageBufferStore) HIncrBy(ctx context.Context, key, field string, incr int64) (int64, error) {
	return s.client.HIncrBy(ctx, key, field, incr).Result()
}

func (s *RedisMessageBufferStore) HSet(ctx context.Context, key string, values ...any) error {
	return s.client.HSet(ctx, key, values...).Err()
}

func (s *RedisMessageBufferStore) LLen(ctx context.Context, key string) (int64, error) {
	return s.client.LLen(ctx, key).Result()
}

func (s *RedisMessageBufferStore) LIndex(ctx context.Context, key string, index int64) (string, bool, error) {
	value, err := s.client.LIndex(ctx, key, index).Result()
	if err == redis.Nil {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (s *RedisMessageBufferStore) LRange(ctx context.Context, key string, start, stop int64) ([]string, error) {
	return s.client.LRange(ctx, key, start, stop).Result()
}

func (s *RedisMessageBufferStore) Del(ctx context.Context, keys ...string) error {
	return s.client.Del(ctx, keys...).Err()
}

func (s *RedisMessageBufferStore) Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error) {
	return s.client.Scan(ctx, cursor, match, count).Result()
}

func (s *RedisMessageBufferStore) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return s.client.HGetAll(ctx, key).Result()
}
