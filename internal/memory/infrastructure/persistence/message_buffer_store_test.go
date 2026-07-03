package persistence

import (
	"context"
	"testing"

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
