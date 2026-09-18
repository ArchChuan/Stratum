package redis_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/byteBuilderX/stratum/pkg/storage/redis"
)

func newStreamStore(t *testing.T) (*redis.StreamStore, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return redis.NewStreamStore(rdb), mr
}

func TestStreamStore_AppendReturnsReadableID(t *testing.T) {
	store, _ := newStreamStore(t)
	ctx := context.Background()

	id, err := store.AppendEvent(ctx, "k", 0, "token", `{"token":"a"}`)
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if id == "" {
		t.Fatal("AppendEvent returned empty id")
	}
	entries, err := store.Range(ctx, "k", "", 0)
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != id || entries[0].Payload != `{"token":"a"}` || entries[0].Event != "token" {
		t.Fatalf("Range = %+v, want single entry id=%s", entries, id)
	}
}

func TestStreamStore_RangeAfterCursorIsExclusive(t *testing.T) {
	store, _ := newStreamStore(t)
	ctx := context.Background()
	first, _ := store.Append(ctx, "k", 0, `{"n":1}`)
	if _, err := store.Append(ctx, "k", 0, `{"n":2}`); err != nil {
		t.Fatalf("Append: %v", err)
	}
	entries, err := store.Range(ctx, "k", first, 0)
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	// 游标是「已经渲染到的最后一条」，因此必须排他——包含它会重复渲染一格。
	if len(entries) != 1 || entries[0].Payload != `{"n":2}` {
		t.Fatalf("Range after cursor = %+v, want only n=2", entries)
	}
}

func TestStreamStore_RangeOnMissingKeyIsEmpty(t *testing.T) {
	store, _ := newStreamStore(t)
	entries, err := store.Range(context.Background(), "nope", "", 0)
	if err != nil {
		t.Fatalf("Range on missing key: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("want empty, got %+v", entries)
	}
}

func TestStreamStore_FirstID(t *testing.T) {
	store, _ := newStreamStore(t)
	ctx := context.Background()
	if id, err := store.FirstID(ctx, "k"); err != nil || id != "" {
		t.Fatalf("FirstID on missing key = (%q,%v), want (\"\",nil)", id, err)
	}
	first, _ := store.Append(ctx, "k", 0, `{"n":1}`)
	_, _ = store.Append(ctx, "k", 0, `{"n":2}`)
	if id, err := store.FirstID(ctx, "k"); err != nil || id != first {
		t.Fatalf("FirstID = (%q,%v), want (%q,nil)", id, err, first)
	}
}

func TestStreamStore_AppendTrimsToMaxLen(t *testing.T) {
	store, _ := newStreamStore(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := store.Append(ctx, "k", 3, `{"n":1}`); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	entries, err := store.Range(ctx, "k", "", 0)
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	// MAXLEN ~ 是近似裁剪，miniredis 按精确语义执行；断言「不超过上限」而非
	// 「恰好等于」，避免把近似语义写死进测试。
	if len(entries) > 3 {
		t.Fatalf("len = %d, want <= 3", len(entries))
	}
}

func TestStreamStore_TailReturnsNewEntries(t *testing.T) {
	store, _ := newStreamStore(t)
	ctx := context.Background()
	if _, err := store.Append(ctx, "k", 0, `{"n":1}`); err != nil {
		t.Fatalf("Append: %v", err)
	}
	entries, err := store.Range(ctx, "k", "", 0)
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = store.Append(ctx, "k", 0, `{"n":2}`)
	}()
	got, err := store.Tail(ctx, "k", entries[0].ID, 0, time.Second)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(got) != 1 || got[0].Payload != `{"n":2}` {
		t.Fatalf("Tail = %+v, want only n=2", got)
	}
}

func TestStreamStore_TailTimesOutEmpty(t *testing.T) {
	store, _ := newStreamStore(t)
	got, err := store.Tail(context.Background(), "k", "0-0", 0, 20*time.Millisecond)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty on timeout, got %+v", got)
	}
}

func TestStreamStore_ExpireAndDelete(t *testing.T) {
	store, mr := newStreamStore(t)
	ctx := context.Background()
	key, _ := store.Append(ctx, "k", 0, `{"n":1}`)
	_ = key
	if err := store.Expire(ctx, "k", time.Minute); err != nil {
		t.Fatalf("Expire: %v", err)
	}
	if ttl := mr.TTL("k"); ttl <= 0 || ttl > time.Minute {
		t.Fatalf("TTL = %v, want (0,1m]", ttl)
	}
	if err := store.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if mr.Exists("k") {
		t.Fatal("key still exists after Delete")
	}
}
