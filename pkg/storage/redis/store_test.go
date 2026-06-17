package redis_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/byteBuilderX/stratum/pkg/storage/redis"
)

func TestStore_GetSetDel(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	store := redis.NewStore(rdb)
	ctx := context.Background()

	if err := store.Set(ctx, "k", "v", time.Minute); err != nil {
		t.Fatalf("Set: %v", err)
	}
	v, err := store.Get(ctx, "k")
	if err != nil || v != "v" {
		t.Fatalf("Get got (%q,%v), want (\"v\",nil)", v, err)
	}
	if err := store.Del(ctx, "k"); err != nil {
		t.Fatalf("Del: %v", err)
	}
	if _, err := store.Get(ctx, "k"); !errors.Is(err, redis.ErrKeyNotFound) {
		t.Fatalf("Get after Del: want ErrKeyNotFound, got %v", err)
	}
}

func TestStore_GetMissing(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	store := redis.NewStore(rdb)
	if _, err := store.Get(context.Background(), "missing"); !errors.Is(err, redis.ErrKeyNotFound) {
		t.Fatalf("want ErrKeyNotFound, got %v", err)
	}
}
