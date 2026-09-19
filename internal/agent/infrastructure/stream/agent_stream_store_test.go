package stream_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	agentstream "github.com/byteBuilderX/stratum/internal/agent/infrastructure/stream"
	pgcontext "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/storage/redis"
	"go.uber.org/zap"
)

func tenantCtx(tenantID string) context.Context {
	return pgcontext.WithTenant(context.Background(), &pgcontext.TenantContext{
		TenantID: tenantID, UserID: "u1", Role: pgcontext.RoleTenantAdmin,
	})
}

func newStore(t *testing.T) *agentstream.AgentStreamStore {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return agentstream.NewAgentStreamStore(redis.NewStreamStore(rdb), zap.NewNop())
}

// TestAgentStreamStoreNilLoggerIsNormalized 钉住构造点的 logger 归一化：续期失败
// 的 WARN 是「key 可能没有 TTL」分支的唯一留痕，漏传 logger 不得让这条路径 panic
// 或重新变成静默（与 application 侧 deps.Logger == nil → zap.NewNop() 同型）。
func TestAgentStreamStoreNilLoggerIsNormalized(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	store := agentstream.NewAgentStreamStoreWith(
		redis.NewStreamStore(rdb), time.Minute, 100, 10, time.Second, nil)

	if _, err := store.Append(tenantCtx("acme"), "exec-nil-logger", 1,
		port.StreamEntry{Event: port.StreamEventToken, Payload: `{"token":"a"}`}); err != nil {
		t.Fatalf("Append with a nil logger: %v", err)
	}
	// 写路径搭车续期：Append 之后 key 必须已带上 TTL（WARN 所守护的行为本身）。
	keys := mr.Keys()
	if len(keys) != 1 {
		t.Fatalf("keys = %v, want exactly the appended stream", keys)
	}
	if ttl := mr.TTL(keys[0]); ttl != time.Minute {
		t.Fatalf("stream TTL = %v, want 1m（写路径搭车续期）", ttl)
	}
}

func TestAgentStreamStoreAppendAndReplay(t *testing.T) {
	store := newStore(t)
	ctx := tenantCtx("acme")
	id, err := store.Append(ctx, "exec-1", 1, port.StreamEntry{Event: port.StreamEventToken, Payload: `{"token":"a"}`})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if id == "" {
		t.Fatal("Append returned empty id")
	}
	entries, err := store.Replay(ctx, "exec-1", 1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(entries) != 1 || entries[0].ID != id || entries[0].Event != port.StreamEventToken {
		t.Fatalf("Replay = %+v, want single token entry", entries)
	}
}

func TestAgentStreamStoreGenerationsAreIsolated(t *testing.T) {
	// 分代独立 key 是让两个 runner 交错「物理上不可能」的保证（spec §6.1）：
	// 僵尸 runner 的 token 落在 gen=1，新 runner 的落在 gen=2，互不可见。
	store := newStore(t)
	ctx := tenantCtx("acme")
	if _, err := store.Append(ctx, "exec-1", 1, port.StreamEntry{Event: port.StreamEventToken, Payload: `{"token":"old"}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, "exec-1", 2, port.StreamEntry{Event: port.StreamEventToken, Payload: `{"token":"new"}`}); err != nil {
		t.Fatal(err)
	}
	gen1, err := store.Replay(ctx, "exec-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	gen2, err := store.Replay(ctx, "exec-1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(gen1) != 1 || gen1[0].Payload != `{"token":"old"}` {
		t.Fatalf("gen1 = %+v, want old token only", gen1)
	}
	if len(gen2) != 1 || gen2[0].Payload != `{"token":"new"}` {
		t.Fatalf("gen2 = %+v, want new token only", gen2)
	}
}

func TestAgentStreamStoreTenantsAreIsolated(t *testing.T) {
	// execution_id 客户端可控，key 必须过租户命名空间（spec D4）。
	store := newStore(t)
	if _, err := store.Append(tenantCtx("acme"), "shared-exec", 1, port.StreamEntry{Payload: `{"token":"acme"}`}); err != nil {
		t.Fatal(err)
	}
	other, err := store.Replay(tenantCtx("globex"), "shared-exec", 1)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("cross-tenant replay leaked %d entries", len(other))
	}
}

func TestAgentStreamStoreMissingTenantFailsClosed(t *testing.T) {
	store := newStore(t)
	if _, err := store.Append(context.Background(), "exec-1", 1, port.StreamEntry{Payload: "x"}); err == nil {
		t.Fatal("expected fail-closed error for missing tenant context")
	}
}

func TestAgentStreamStoreRejectsColonInExecutionID(t *testing.T) {
	store := newStore(t)
	ctx := tenantCtx("acme")
	// 允许 ':' 就能伪造 generation 段，读走另一个执行的流。
	if _, err := store.Append(ctx, "exec-1:9", 1, port.StreamEntry{Payload: "x"}); err == nil {
		t.Fatal("expected error for execution_id containing ':'")
	}
}

func TestAgentStreamStoreReplayAfterAndFirstID(t *testing.T) {
	store := newStore(t)
	ctx := tenantCtx("acme")
	first, err := store.Append(ctx, "exec-1", 1, port.StreamEntry{Payload: `{"n":1}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(ctx, "exec-1", 1, port.StreamEntry{Payload: `{"n":2}`}); err != nil {
		t.Fatal(err)
	}
	after, err := store.ReplayAfter(ctx, "exec-1", 1, first)
	if err != nil {
		t.Fatalf("ReplayAfter: %v", err)
	}
	if len(after) != 1 || after[0].Payload != `{"n":2}` {
		t.Fatalf("ReplayAfter = %+v, want only n=2", after)
	}
	oldest, err := store.FirstID(ctx, "exec-1", 1)
	if err != nil {
		t.Fatalf("FirstID: %v", err)
	}
	if oldest != first {
		t.Fatalf("FirstID = %q, want %q", oldest, first)
	}
}

func TestAgentStreamStoreTailAndDelete(t *testing.T) {
	store := newStore(t)
	ctx := tenantCtx("acme")
	if _, err := store.Append(ctx, "exec-1", 1, port.StreamEntry{Payload: `{"n":1}`}); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Replay(ctx, "exec-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Tail(ctx, "exec-1", 1, entries[0].ID, 20)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Tail on idle stream = %+v, want empty", got)
	}
	if err := store.Delete(ctx, "exec-1", 1); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	after, err := store.Replay(ctx, "exec-1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 0 {
		t.Fatalf("Replay after Delete = %+v, want empty", after)
	}
}
