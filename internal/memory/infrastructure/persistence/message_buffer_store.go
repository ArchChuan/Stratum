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

// drainValuesScript 原子地按值删除列表项并回扣 byte_size：
//   - LREM 每次只删除 flush 快照里读到的具体值——flush 读快照之后到达的新
//     消息不在 ARGV 中，绝不会被误删；
//   - 只有实际删除（removed > 0）才回扣 byte_size，多 flusher 因单飞锁串行，
//     回扣精确不重复；
//   - 列表清空时同时删除 list 与 meta，状态完全复位。
const drainValuesScript = `
for i = 1, #ARGV, 2 do
  local removed = redis.call('LREM', KEYS[1], 0, ARGV[i])
  if removed > 0 then
    redis.call('HINCRBY', KEYS[2], 'byte_size', -tonumber(ARGV[i + 1]))
  end
end
if tonumber(redis.call('LLEN', KEYS[1])) == 0 then
  redis.call('DEL', KEYS[1], KEYS[2])
end
return 0
`

func (s *RedisMessageBufferStore) RemoveValues(ctx context.Context, key, metaKey string, values []string, sizes []int64) error {
	if len(values) == 0 {
		return nil
	}
	args := make([]interface{}, 0, len(values)*2)
	for i, v := range values {
		args = append(args, v, sizes[i])
	}
	return s.client.Eval(ctx, drainValuesScript, []string{key, metaKey}, args...).Err()
}

func (s *RedisMessageBufferStore) TryAcquireFlushLock(ctx context.Context, key, token string, ttl time.Duration) (bool, error) {
	return s.client.SetNX(ctx, key, token, ttl).Result()
}

// releaseFlushLockScript 仅当锁仍属于当前 token 时删除——TTL 过期后其他
// flusher 已接管时不会误删别人的锁。
const releaseFlushLockScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`

func (s *RedisMessageBufferStore) ReleaseFlushLock(ctx context.Context, key, token string) error {
	return s.client.Eval(ctx, releaseFlushLockScript, []string{key}, token).Err()
}

func (s *RedisMessageBufferStore) Scan(ctx context.Context, cursor uint64, match string, count int64) ([]string, uint64, error) {
	return s.client.Scan(ctx, cursor, match, count).Result()
}

func (s *RedisMessageBufferStore) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return s.client.HGetAll(ctx, key).Result()
}
