package redis_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
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

// recordingHook 记录经过 ProcessHook 的命令名与参数。用它是因为 miniredis 对
// `XRANGE ... COUNT 0` 的语义与真实 Redis 相反（前者当成不限条数，后者返回 null
// array），返回值无法区分「发 COUNT」与「不发 COUNT」两种实现，只有断言真正上线
// 的命令形状才能钉住实现选择。
type recordingHook struct {
	mu   sync.Mutex
	name string
	args []string
}

func (h *recordingHook) DialHook(next goredis.DialHook) goredis.DialHook { return next }

func (h *recordingHook) ProcessHook(next goredis.ProcessHook) goredis.ProcessHook {
	return func(ctx context.Context, cmd goredis.Cmder) error {
		args := make([]string, 0, len(cmd.Args()))
		for _, a := range cmd.Args() {
			args = append(args, fmt.Sprint(a))
		}
		h.mu.Lock()
		h.name, h.args = strings.ToLower(cmd.Name()), args
		h.mu.Unlock()
		return next(ctx, cmd)
	}
}

func (h *recordingHook) ProcessPipelineHook(next goredis.ProcessPipelineHook) goredis.ProcessPipelineHook {
	return next
}

func (h *recordingHook) last() (string, []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.name, slices.Clone(h.args)
}

// newRecordingStore 返回挂上 recordingHook 的 store，用于断言命令的 wire 形状。
func newRecordingStore(t *testing.T) (*redis.StreamStore, *recordingHook) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	rec := &recordingHook{}
	rdb.AddHook(rec)
	return redis.NewStreamStore(rdb), rec
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
	_, _ = store.Append(ctx, "k", 0, `{"n":1}`)
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

func TestStreamStore_RangeWireCommand(t *testing.T) {
	cases := []struct {
		name     string
		count    int64
		wantArgs []string
	}{
		{
			// count <= 0 走 XRange：XRangeN 无条件拼 COUNT，真实 Redis 收到
			// `COUNT 0` 返回 null array，客户端把它映射成 redis.Nil。
			name:     "count 0 means unlimited and sends no COUNT",
			count:    0,
			wantArgs: []string{"xrange", "k", "-", "+"},
		},
		{
			name:     "count above zero keeps the COUNT cap",
			count:    2,
			wantArgs: []string{"xrange", "k", "-", "+", "count", "2"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, rec := newRecordingStore(t)
			ctx := context.Background()
			if _, err := store.AppendEvent(ctx, "k", 0, "token", `{"n":"1"}`); err != nil {
				t.Fatalf("AppendEvent: %v", err)
			}
			if _, err := store.Range(ctx, "k", "", tc.count); err != nil {
				t.Fatalf("Range: %v", err)
			}
			name, args := rec.last()
			if name != "xrange" {
				t.Fatalf("wire command = %q, want xrange", name)
			}
			if !slices.Equal(args, tc.wantArgs) {
				t.Fatalf("wire args = %v, want %v", args, tc.wantArgs)
			}
		})
	}
}

// tailWithin 在独立 goroutine 里执行 Tail，并以 2 秒上限兜底：非正 block 若退回
// `BLOCK 0`（Redis 语义是永不超时），阻塞的读不会自行返回，用例必须在上限处失败，
// 而不是把整个 suite 拖成超时。不能用有界 ctx 兜底——go-redis 默认
// ContextTimeoutEnabled=false，会把 ctx 换掉，期限根本到不了这个读上。
func tailWithin(t *testing.T, store *redis.StreamStore, block time.Duration) ([]redis.StreamEntry, error) {
	t.Helper()
	type tailResult struct {
		entries []redis.StreamEntry
		err     error
	}
	done := make(chan tailResult, 1)
	go func() {
		entries, err := store.Tail(context.Background(), "k", "0-0", 0, block)
		done <- tailResult{entries: entries, err: err}
	}()

	select {
	case got := <-done:
		return got.entries, got.err
	case <-time.After(2 * time.Second):
		t.Fatal("Tail did not return within 2s: BLOCK 0 means never time out in Redis")
		return nil, nil
	}
}

func TestStreamStore_TailWithZeroBlockReturnsImmediately(t *testing.T) {
	store, _ := newStreamStore(t)
	entries, err := tailWithin(t, store, 0)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("Tail on empty stream = %+v, want no entries", entries)
	}
}

func TestStreamStore_TailEmptyResultIsEmptySlice(t *testing.T) {
	cases := []struct {
		name  string
		block time.Duration
	}{
		{name: "no entries on a non-blocking read", block: 0},
		{name: "no entries before the block deadline", block: 20 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, _ := newStreamStore(t)
			got, err := tailWithin(t, store, tc.block)
			if err != nil {
				t.Fatalf("Tail: %v", err)
			}
			// 契约是空切片：nil 序列化出去是 null，调用方拿到的是 []。
			if got == nil {
				t.Fatal("Tail returned nil, want an empty slice")
			}
			if len(got) != 0 {
				t.Fatalf("Tail = %+v, want empty", got)
			}
		})
	}
}
