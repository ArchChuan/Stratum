package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisMessageBufferStore_LIndexTranslatesMissingValue(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { assert.NoError(t, client.Close()) })

	store := NewRedisMessageBufferStore(client)
	value, ok, err := store.LIndex(context.Background(), "missing", 0)

	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, value)
}

func TestRedisMessageBufferStore_LIndexReturnsExistingValue(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	ctx := context.Background()
	store := NewRedisMessageBufferStore(client)

	require.NoError(t, store.RPush(ctx, "messages", []byte("hello")))

	value, ok, err := store.LIndex(ctx, "messages", 0)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "hello", value)
}

func TestRedisMessageBufferStore_RemoveValues_RemovesOnlyGivenValues(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	ctx := context.Background()
	store := NewRedisMessageBufferStore(client)

	require.NoError(t, store.RPush(ctx, "messages", []byte("v1")))
	require.NoError(t, store.RPush(ctx, "messages", []byte("v2")))
	require.NoError(t, store.RPush(ctx, "messages", []byte("v3")))
	_, err := store.HIncrBy(ctx, "messages:meta", "byte_size", 30)
	require.NoError(t, err)

	require.NoError(t, store.RemoveValues(ctx, "messages", "messages:meta", []string{"v1", "v2"}, []int64{10, 10}))

	left, err := client.LRange(ctx, "messages", 0, -1).Result()
	require.NoError(t, err)
	assert.Equal(t, []string{"v3"}, left, "只删除给定的值，快照之外的值必须保留")
	meta, err := store.HGetAll(ctx, "messages:meta")
	require.NoError(t, err)
	assert.Equal(t, "10", meta["byte_size"], "byte_size 只按实际删除的值回扣")
}

func TestRedisMessageBufferStore_RemoveValues_DeletesKeysWhenEmpty(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	ctx := context.Background()
	store := NewRedisMessageBufferStore(client)

	require.NoError(t, store.RPush(ctx, "messages", []byte("v1")))
	_, err := store.HIncrBy(ctx, "messages:meta", "byte_size", 10)
	require.NoError(t, err)

	require.NoError(t, store.RemoveValues(ctx, "messages", "messages:meta", []string{"v1"}, []int64{10}))

	n, err := client.Exists(ctx, "messages", "messages:meta").Result()
	require.NoError(t, err)
	assert.Zero(t, n, "列表清空后 list 与 meta 必须一起删除，状态完全复位")
}

func TestRedisMessageBufferStore_FlushLock_SingleFlight(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	ctx := context.Background()
	store := NewRedisMessageBufferStore(client)

	ok, err := store.TryAcquireFlushLock(ctx, "lock:key", "token-a", time.Minute)
	require.NoError(t, err)
	assert.True(t, ok)

	ok, err = store.TryAcquireFlushLock(ctx, "lock:key", "token-b", time.Minute)
	require.NoError(t, err)
	assert.False(t, ok, "同一 key 只允许一个 flusher")

	require.NoError(t, store.ReleaseFlushLock(ctx, "lock:key", "token-b"))
	ok, err = store.TryAcquireFlushLock(ctx, "lock:key", "token-c", time.Minute)
	require.NoError(t, err)
	assert.False(t, ok, "非持有者释放不得生效（TTL 前锁仍属于 token-a）")

	require.NoError(t, store.ReleaseFlushLock(ctx, "lock:key", "token-a"))
	ok, err = store.TryAcquireFlushLock(ctx, "lock:key", "token-c", time.Minute)
	require.NoError(t, err)
	assert.True(t, ok, "持有者释放后锁可重新获取")
}

func TestRedisMessageBufferStore_NilClient(t *testing.T) {
	assert.Nil(t, NewRedisMessageBufferStore(nil))
}

func TestRedisMessageBufferStore_Expire(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	ctx := context.Background()
	store := NewRedisMessageBufferStore(client)

	require.NoError(t, store.RPush(ctx, "messages", []byte("hello")))
	require.NoError(t, store.Expire(ctx, "messages", time.Minute))
	ttl, err := client.TTL(ctx, "messages").Result()
	require.NoError(t, err)
	assert.Greater(t, ttl, time.Duration(0))
}

func TestRedisMessageBufferStore_HSetNX(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	ctx := context.Background()
	store := NewRedisMessageBufferStore(client)

	require.NoError(t, store.HSetNX(ctx, "hash", "field", "first"))
	// Second attempt on the same field is a no-op, not an error.
	require.NoError(t, store.HSetNX(ctx, "hash", "field", "second"))
	got, err := client.HGet(ctx, "hash", "field").Result()
	require.NoError(t, err)
	assert.Equal(t, "first", got)
}

func TestRedisMessageBufferStore_HIncrBy(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	ctx := context.Background()
	store := NewRedisMessageBufferStore(client)

	v1, err := store.HIncrBy(ctx, "counter", "n", 3)
	require.NoError(t, err)
	assert.Equal(t, int64(3), v1)
	v2, err := store.HIncrBy(ctx, "counter", "n", 2)
	require.NoError(t, err)
	assert.Equal(t, int64(5), v2)
}

func TestRedisMessageBufferStore_HSetAndHGetAll(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	ctx := context.Background()
	store := NewRedisMessageBufferStore(client)

	require.NoError(t, store.HSet(ctx, "profile", "name", "alice", "role", "user"))
	got, err := store.HGetAll(ctx, "profile")
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"name": "alice", "role": "user"}, got)
}

func TestRedisMessageBufferStore_LLenAndLRange(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	ctx := context.Background()
	store := NewRedisMessageBufferStore(client)

	require.NoError(t, store.RPush(ctx, "queue", []byte("a")))
	require.NoError(t, store.RPush(ctx, "queue", []byte("b")))
	require.NoError(t, store.RPush(ctx, "queue", []byte("c")))
	n, err := store.LLen(ctx, "queue")
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)

	values, err := store.LRange(ctx, "queue", 0, -1)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b", "c"}, values)

	partial, err := store.LRange(ctx, "queue", 1, 2)
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "c"}, partial)
}

func TestRedisMessageBufferStore_Del(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	ctx := context.Background()
	store := NewRedisMessageBufferStore(client)

	require.NoError(t, store.RPush(ctx, "a", []byte("1")))
	require.NoError(t, store.RPush(ctx, "b", []byte("2")))
	require.NoError(t, store.Del(ctx, "a", "b"))
	n, err := client.Exists(ctx, "a", "b").Result()
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

func TestRedisMessageBufferStore_Scan(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { assert.NoError(t, client.Close()) })
	ctx := context.Background()
	store := NewRedisMessageBufferStore(client)

	for i := 0; i < 5; i++ {
		require.NoError(t, client.Set(ctx, "buf:key"+string(rune('a'+i)), "v", 0).Err())
	}
	keys, cursor, err := store.Scan(ctx, 0, "buf:*", 100)
	require.NoError(t, err)
	assert.Equal(t, uint64(0), cursor)
	assert.Len(t, keys, 5)
}
