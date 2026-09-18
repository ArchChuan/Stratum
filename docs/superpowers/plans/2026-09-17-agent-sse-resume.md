# Agent SSE 断线续传 PR 1（后端续传内核 + 停止按钮）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Agent SSE 流在断线重连后接着上次的位置继续输出（Redis Stream 可寻址输出日志），并去掉「客户端断连即杀 run」的行为，同时补上「停止生成」入口。

**Architecture:** run 的生命周期与 HTTP 请求解绑——runner 持有 PG 租约并向 Redis Stream 追加事件，HTTP 请求降级为纯订阅者。租约提供跨副本互斥（`run_generation` 的 CAS 是真正的 fencing token），流 key 带 generation 使两个 runner 交错在物理上不可能。冷启动（F5）走「全量回放当前 generation」，因此**不需要任何前端配合就已修复 F5**。

**Tech Stack:** Go 1.25.12、Gin v1.9.1、pgx v5.9.2、go-redis v9.7.3、miniredis v2.38.0、React 18.3 + Ant Design 5.20、TypeScript。

**Spec:** `docs/superpowers/specs/2026-09-17-agent-sse-resume-design.md`

## Global Constraints

- Go 行宽 ≤120；import 分组 stdlib → third-party → internal，组间空行。
- 错误逐层包装：`fmt.Errorf("operation: %w", err)`；禁止吞错。error 必须是最后一个返回值。
- 行为数字禁止内联：跨包放 `pkg/constants/<domain>.go`，包内共享放 `internal/<pkg>/defaults.go`。名称含 `Default`/`Max`/`Min` 或单位语义。
- 代码质量门禁：圈复杂度 ≤10、认知复杂度 ≤15、函数长度 ≤120 行、最大嵌套 ≤4。存量超限函数不得恶化。
- `pkg/` 不 import `internal/`；`domain/` 仅依赖 stdlib 与 `pkg/constants`；`application/` 不 import pgx、Redis、NATS 或 Gin；handler 不 import infrastructure 或存储驱动。
- 所有访问 tenant-scoped 表的 repository 方法必须经 `execTenantID(ctx, ...)`，port 方法必须显式含 `tenantID string`。
- 测试表驱动；mock 外部依赖，不 mock 领域逻辑。
- 前端：用户可见字符串中文；使用 `message`/`Modal.confirm`，禁止 `alert`/`confirm`；页面不得跨 `pages/` 导入。
- **本计划不改 `proto/`**（spec §11.4：`gen.ExecuteAgentRequest` 零引用，真正的绑定点是手写 DTO）。禁止修改 `config/config.go` 默认值。

---

## 文件结构

### 新增

| 文件 | 职责 |
|---|---|
| `pkg/storage/tenantnaming/redis.go` | 租户命名空间化 Redis key，fail-closed |
| `pkg/storage/redis/stream.go` | Redis Streams 通用薄封装（Append/Range/Tail/Expire/Delete/FirstID），只做 IO |
| `internal/agent/domain/port/agent_stream.go` | `AgentStreamStore` / `AgentControlBus` / `ExecutionLeaseRepo` 端口 + 事件名常量 |
| `internal/agent/infrastructure/stream/agent_stream_store.go` | 端口实现，内含租户命名空间构造 |
| `internal/agent/infrastructure/stream/control_bus.go` | Pub/Sub 控制通道实现，持独立 `*goredis.Client` |
| `internal/agent/infrastructure/persistence/checkpoint_lease.go` | 租约 CAS SQL（Stamp/Claim/Renew/Release/Status） |
| `internal/agent/application/agent_stream_plan.go` | 纯函数：游标→订阅计划、Stream ID 比较、终态判定 |
| `internal/agent/application/agent_stream_runner.go` | runner：租约心跳、viewer 注册、孤儿计时、控制订阅、写流 |
| `internal/agent/application/agent_stream_subscriber.go` | 订阅侧：回放、跟流、心跳、空闲看门狗、帧通道 |

> spec §14 把后三者合并为一个 `agent_stream_session.go`。此处拆成三个聚焦文件：纯函数（无 IO、最高单测价值）、runner、subscriber 三者职责与测试方式不同，合并会让单文件超 400 行。

### 修改

| 文件 | 改动 |
|---|---|
| `pkg/constants/agent.go` | 追加流/租约/viewer/孤儿超时常量 |
| `pkg/storage/postgres/tenant_schema.sql` | `lease_expires_at` 列（CREATE + 历史租户 `ADD COLUMN IF NOT EXISTS`） |
| `api/http/handler/sse_writer.go` | SSE `id:` 行 |
| `api/http/handler/agent_dto.go` | wire-only `generation` / `last_event_id` |
| `api/http/handler/agent_exec_handler.go` | 去 `cancel()`、订阅化、stop 端点 |
| `api/http/router.go` | stop 路由 |
| `internal/agent/application/agent_execution.go` | `ExecMeta` 加 `Generation`/`LastEventID`；`ExecuteStream` 签名重写 |
| `internal/agent/application/agent_service.go` | `AgentServiceDeps` 加流依赖与可注入时长 |
| `api/wiring/` | StreamStore / ControlBus / LeaseRepo 装配与逆序关闭 |
| `web/src/modules/agent/api/agent.api.ts` | `stopAgentExecution` |
| `web/src/modules/agent/hooks/ChatStreamContext.tsx` | `cancelStream` 接上 stop API |
| `web/src/modules/agent/components/ChatComposer.tsx` | 停止按钮 |
| `web/src/modules/agent/pages/AgentChatPage.tsx` | 停止按钮接线 |

---

## Task 1: 常量

**Files:**

- Modify: `pkg/constants/agent.go`
- Test: `pkg/constants/agent_test.go`

**Interfaces:**

- Consumes: 无
- Produces: `AgentStreamTTL`、`AgentStreamMaxLen`、`AgentStreamReplayBatch`、`AgentStreamReadBlock`、`AgentStreamIdleExit`、`AgentExecutionLeaseTTL`、`AgentExecutionLeaseRenewInterval`、`AgentViewerTimeout`、`AgentExecutionOrphanTimeout`

- [ ] **Step 1: 写失败测试**

追加到 `pkg/constants/agent_test.go` 末尾：

```go
func TestAgentLeaseRenewIntervalIsOneThirdOfTTL(t *testing.T) {
	// 续租间隔必须是租约的 1/3：单次续租失败仍有 2 次重试，同时把僵尸 runner
	// 的窗口压到 TTL/3（spec §6.7 D3）。
	if AgentExecutionLeaseRenewInterval*3 != AgentExecutionLeaseTTL {
		t.Fatalf("renew interval %v is not TTL/3 of lease %v",
			AgentExecutionLeaseRenewInterval, AgentExecutionLeaseTTL)
	}
}

func TestAgentViewerTimeoutIsShorterThanOrphanTimeout(t *testing.T) {
	// viewer 无心跳摘除必须显著早于孤儿取消，否则一个刚崩溃的订阅者会立刻
	// 触发孤儿计时（spec §7.1）。
	if AgentViewerTimeout >= AgentExecutionOrphanTimeout {
		t.Fatalf("viewer timeout %v must be shorter than orphan timeout %v",
			AgentViewerTimeout, AgentExecutionOrphanTimeout)
	}
}

func TestAgentStreamMaxLenCoversFullReplay(t *testing.T) {
	// MAXLEN 取 20000 而非 10000：F5 需要全量回放，裁剪造成的缺口会直接
	// 截断用户看到的答案（spec §6.5）。
	if AgentStreamMaxLen < 20000 {
		t.Fatalf("stream max len %d is too small for full replay", AgentStreamMaxLen)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-agent-sse-resume && go test ./pkg/constants/ -run 'TestAgentLease|TestAgentViewer|TestAgentStreamMaxLen' -v`
Expected: FAIL — `undefined: AgentExecutionLeaseRenewInterval` 等编译错误

- [ ] **Step 3: 追加常量**

在 `pkg/constants/agent.go` 末尾追加：

```go
// Agent 执行流续传（spec §6.7）。租约、viewer、孤儿超时三者共同决定 runner
// 的生命周期——它与任何 HTTP 连接都无关。
const (
	// AgentStreamTTL 是执行输出流在 Redis 中的存活时长。创建时设一次，
	// runner 续租心跳搭车 EXPIRE 刷新，订阅者 attach 时也续一次。
	AgentStreamTTL = 1 * time.Hour

	// AgentStreamMaxLen 是流的近似最大长度（XADD MAXLEN ~）。取 20000 而非
	// 10000：F5 场景需要全量回放，裁剪造成的缺口会直接截断用户看到的答案。
	AgentStreamMaxLen = 20000

	// AgentStreamReplayBatch 是单次 XRANGE 回放的最大条数，与 MAXLEN 对齐。
	AgentStreamReplayBatch = 20000

	// AgentStreamReadBlock 是 XREAD BLOCK 的单次阻塞时长。到期无新条目即返回
	// 空切片，让订阅循环有机会检查 ctx 与写心跳。
	AgentStreamReadBlock = 1 * time.Second

	// AgentStreamIdleExit 是订阅侧的看门狗上限：租约已失效且流持续无新条目超过
	// 该时长即结束订阅。只在 runner 被 SIGKILL（无终态帧）时兜底，避免前端
	// 无限 spinner。
	AgentStreamIdleExit = 2 * time.Minute

	// AgentExecutionLeaseTTL 是执行租约的有效期。续租 CAS 失败意味着另一个
	// runner 已抢占，僵尸 runner 必须立刻自取消。
	AgentExecutionLeaseTTL = 30 * time.Second

	// AgentExecutionLeaseRenewInterval 是续租间隔，取租约的 1/3。
	AgentExecutionLeaseRenewInterval = 10 * time.Second

	// AgentViewerTimeout 是订阅者无心跳后被摘除的时长。崩溃/断网自然过期，
	// runner 不需要查询 Redis。
	AgentViewerTimeout = 15 * time.Second

	// AgentExecutionOrphanTimeout 是无活跃订阅者后 run 被取消的时长。
	AgentExecutionOrphanTimeout = 2 * time.Minute
)
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./pkg/constants/ -run 'TestAgentLease|TestAgentViewer|TestAgentStreamMaxLen' -v`
Expected: PASS（3 个）

- [ ] **Step 5: 提交**

```bash
git add pkg/constants/agent.go pkg/constants/agent_test.go
git commit -m "feat(agent): 续传流与租约常量"
```

---

## Task 2: 租户命名空间化 Redis key

**Files:**

- Create: `pkg/storage/tenantnaming/redis.go`
- Test: `pkg/storage/tenantnaming/redis_test.go`

**Interfaces:**

- Consumes: `pgcontext.FromContext`（`pkg/storage/postgres`）
- Produces: `tenantnaming.TenantKey(ctx context.Context, prefix string, parts ...string) (string, error)`、`tenantnaming.RedisKeyPrefix = "agent"`

- [ ] **Step 1: 写失败测试**

创建 `pkg/storage/tenantnaming/redis_test.go`：

```go
package tenantnaming_test

import (
	"context"
	"testing"

	pgcontext "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/storage/tenantnaming"
)

func ctxWithTenant(tenantID string) context.Context {
	return pgcontext.WithTenant(context.Background(), &pgcontext.TenantContext{
		TenantID: tenantID, UserID: "u1", Role: pgcontext.RoleTenantAdmin,
	})
}

func TestTenantKey(t *testing.T) {
	got, err := tenantnaming.TenantKey(ctxWithTenant("acme"), "agent", "stream", "exec-1", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "agent:acme:stream:exec-1:1"; got != want {
		t.Errorf("TenantKey = %q, want %q", got, want)
	}
}

func TestTenantKey_MissingContext(t *testing.T) {
	if _, err := tenantnaming.TenantKey(context.Background(), "agent", "stream"); err == nil {
		t.Fatal("expected error for missing tenant context")
	}
}

func TestTenantKey_EmptyTenantID(t *testing.T) {
	if _, err := tenantnaming.TenantKey(ctxWithTenant(""), "agent", "stream"); err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}

func TestTenantKey_RejectsColonInPart(t *testing.T) {
	// execution_id 是客户端可控的。允许 ':' 会让调用方拼出穿越命名空间的 key
	// （如 execution_id="x:2" 伪造 generation 段，把别人的流读成自己的）。
	_, err := tenantnaming.TenantKey(ctxWithTenant("acme"), "agent", "stream", "x:2", "1")
	if err == nil {
		t.Fatal("expected error for key part containing ':'")
	}
}

func TestTenantKey_RejectsEmptyPart(t *testing.T) {
	if _, err := tenantnaming.TenantKey(ctxWithTenant("acme"), "agent", "stream", "", "1"); err == nil {
		t.Fatal("expected error for empty key part")
	}
}

func TestTenantKey_RejectsEmptyPrefix(t *testing.T) {
	if _, err := tenantnaming.TenantKey(ctxWithTenant("acme"), ""); err == nil {
		t.Fatal("expected error for empty prefix")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/storage/tenantnaming/ -run TestTenantKey -v`
Expected: FAIL — `undefined: tenantnaming.TenantKey`

- [ ] **Step 3: 实现**

创建 `pkg/storage/tenantnaming/redis.go`：

```go
package tenantnaming

import (
	"context"
	"fmt"
	"strings"

	pgcontext "github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

// RedisKeyPrefix 是所有 Agent 执行流 key 的公共前缀。
const RedisKeyPrefix = "agent"

// TenantKey 返回租户命名空间化的 Redis key，形如
// "{prefix}:{tenantID}:{part1}:{part2}..."。
//
// fail closed：租户上下文缺失或为空即报错，绝不退化成无命名空间的 key——
// execution_id 是客户端可控的（见 executionIDOrNew），只由它构成的 key 会让
// 提交别人 execution_id 的调用方读走别人的 token 流（spec D4）。
//
// part 含 ':' 同样报错：否则调用方可用 execution_id="x:2" 伪造出 generation
// 段，拼出另一个执行的 key。
func TenantKey(ctx context.Context, prefix string, parts ...string) (string, error) {
	tc, ok := pgcontext.FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("tenantnaming: missing tenant context")
	}
	if tc.TenantID == "" {
		return "", fmt.Errorf("tenantnaming: tenant_id is empty")
	}
	if prefix == "" {
		return "", fmt.Errorf("tenantnaming: prefix is empty")
	}
	if strings.ContainsRune(prefix, ':') {
		return "", fmt.Errorf("tenantnaming: prefix %q contains ':'", prefix)
	}
	segments := make([]string, 0, len(parts)+2)
	segments = append(segments, prefix, tc.TenantID)
	for _, p := range parts {
		if p == "" {
			return "", fmt.Errorf("tenantnaming: empty key part")
		}
		if strings.ContainsRune(p, ':') {
			return "", fmt.Errorf("tenantnaming: key part %q contains ':'", p)
		}
		segments = append(segments, p)
	}
	return strings.Join(segments, ":"), nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./pkg/storage/tenantnaming/ -v`
Expected: PASS（含既有 `TenantSubject`/Milvus/Neo4j 用例）

- [ ] **Step 5: 提交**

```bash
git add pkg/storage/tenantnaming/redis.go pkg/storage/tenantnaming/redis_test.go
git commit -m "feat(tenantnaming): 租户命名空间化 Redis key"
```

---

## Task 3: Redis Streams 薄封装

**Files:**

- Create: `pkg/storage/redis/stream.go`
- Test: `pkg/storage/redis/stream_test.go`

**Interfaces:**

- Consumes: `*goredis.Client`
- Produces: `redis.NewStreamStore(*goredis.Client) *StreamStore`；`StreamEntry{ID, Payload string}`；方法 `Append(ctx, key, maxLen int64, payload string) (string, error)`、`AppendEvent(ctx, key, maxLen int64, event, payload string) (string, error)`、`Range(ctx, key, afterID string, count int64) ([]StreamEntry, error)`、`FirstID(ctx, key) (string, error)`、`Tail(ctx, key, afterID string, count int64, block time.Duration) ([]StreamEntry, error)`、`Expire(ctx, key string, ttl time.Duration) error`、`Delete(ctx, key string) error`

- [ ] **Step 1: 写失败测试**

创建 `pkg/storage/redis/stream_test.go`：

```go
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
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./pkg/storage/redis/ -run TestStreamStore -v`
Expected: FAIL — `undefined: redis.NewStreamStore`

- [ ] **Step 3: 实现**

创建 `pkg/storage/redis/stream.go`：

```go
package redis

import (
	"context"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// StreamEntry 是流中的一条条目。ID 是 Redis 生成的 entry ID（形如
// 1726483200000-37），逐字作为 SSE 的 id: 行，客户端原样回传即可续传。
type StreamEntry struct {
	ID      string
	Event   string
	Payload string
}

// 条目使用两个固定字段名。字段名固定而非动态，是为了让 XADD/XREAD 的取值
// 形状与在线 wire 格式一一对应：Event 为空即整行省略 event:，零转换。
const (
	streamFieldEvent   = "e"
	streamFieldPayload = "d"
)

// StreamStore 是 Redis Streams 的薄封装。它只做 IO——key 由调用方构造
// （Agent 侧经 tenantnaming.TenantKey 生成），本类型不承担租户语义。
type StreamStore struct {
	client *goredis.Client
}

func NewStreamStore(client *goredis.Client) *StreamStore {
	return &StreamStore{client: client}
}

// AppendEvent 追加一条带事件名的条目并返回 Redis 生成的 entry ID。
func (s *StreamStore) AppendEvent(ctx context.Context, key string, maxLen int64, event, payload string) (string, error) {
	return s.append(ctx, key, maxLen, []any{streamFieldEvent, event, streamFieldPayload, payload})
}

// Append 追加一条不带事件名的条目（兼容今天无 event: 行的在线格式）。
func (s *StreamStore) Append(ctx context.Context, key string, maxLen int64, payload string) (string, error) {
	return s.append(ctx, key, maxLen, []any{streamFieldPayload, payload})
}

func (s *StreamStore) append(ctx context.Context, key string, maxLen int64, values []any) (string, error) {
	args := &goredis.XAddArgs{Stream: key, ID: "*", Values: values}
	if maxLen > 0 {
		// 近似（~）而非精确裁剪是刻意的：精确裁剪要逐条驱逐，代价高，而这个流
		// 的定位是尽力而为的显示缓冲，不是事实源（spec §3）。
		args.MaxLen = maxLen
		args.Approx = true
	}
	id, err := s.client.XAdd(ctx, args).Result()
	if err != nil {
		return "", fmt.Errorf("redis: xadd %q: %w", key, err)
	}
	return id, nil
}

// Range 返回 afterID 之后的条目。afterID 为空时从头全量回放。count <= 0
// 表示不限条数。游标是「已渲染到的最后一条」，因此范围**排他**——包含它会
// 让重连后的第一格重复渲染。
func (s *StreamStore) Range(ctx context.Context, key, afterID string, count int64) ([]StreamEntry, error) {
	start := "-"
	if afterID != "" {
		start = "(" + afterID
	}
	msgs, err := s.client.XRangeN(ctx, key, start, "+", count).Result()
	if err != nil {
		return nil, fmt.Errorf("redis: xrange %q: %w", key, err)
	}
	return toEntries(msgs), nil
}

// FirstID 返回流中最老条目的 ID；流不存在或为空时返回 ""。用于判定游标是否
// 因裁剪而早于现存最老条目（缺口检测）。
func (s *StreamStore) FirstID(ctx context.Context, key string) (string, error) {
	msgs, err := s.client.XRangeN(ctx, key, "-", "+", 1).Result()
	if err != nil {
		return "", fmt.Errorf("redis: xrange first %q: %w", key, err)
	}
	if len(msgs) == 0 {
		return "", nil
	}
	return msgs[0].ID, nil
}

// Tail 从 afterID 之后阻塞读取至多 block 时长。阻塞到期无新条目返回空切片
// 与 nil error——调用方据此检查 ctx 并写心跳，而不是当成失败。
func (s *StreamStore) Tail(
	ctx context.Context, key, afterID string, count int64, block time.Duration,
) ([]StreamEntry, error) {
	if afterID == "" {
		afterID = "0-0"
	}
	msgs, err := s.client.XRead(ctx, &goredis.XReadArgs{
		Streams: []string{key, afterID},
		Count:   count,
		Block:   block,
	}).Result()
	if errors.Is(err, goredis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("redis: xread %q: %w", key, err)
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	return toEntries(msgs[0].Messages), nil
}

func (s *StreamStore) Expire(ctx context.Context, key string, ttl time.Duration) error {
	if err := s.client.Expire(ctx, key, ttl).Err(); err != nil {
		return fmt.Errorf("redis: expire %q: %w", key, err)
	}
	return nil
}

func (s *StreamStore) Delete(ctx context.Context, key string) error {
	if err := s.client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis: del %q: %w", key, err)
	}
	return nil
}

func toEntries(msgs []goredis.XMessage) []StreamEntry {
	out := make([]StreamEntry, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, StreamEntry{
			ID:      m.ID,
			Event:   stringField(m.Values, streamFieldEvent),
			Payload: stringField(m.Values, streamFieldPayload),
		})
	}
	return out
}

// stringField 容忍缺字段与 nil 值：历史条目（无 e 字段）与运行期字段缺失都
// 必须降级为空串，而不是 panic 或记住一个 "nil" 字面量。
func stringField(values map[string]any, key string) string {
	v, ok := values[key]
	if !ok || v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./pkg/storage/redis/ -v`
Expected: PASS（8 个新用例 + 既有 Store 用例）

- [ ] **Step 5: 提交**

```bash
git add pkg/storage/redis/stream.go pkg/storage/redis/stream_test.go
git commit -m "feat(redis): Streams 薄封装（Append/Range/Tail/Expire/Delete）"
```

---

## Task 4: 领域端口

**Files:**

- Create: `internal/agent/domain/port/agent_stream.go`

**Interfaces:**

- Consumes: 无（仅 stdlib）
- Produces: `port.ErrLeaseConflict`、`port.StreamEvent`、`port.LeaseStatus`、`port.ControlMessage`、`port.ControlMessageViewer`、`port.ControlMessageCancel`、`port.AgentStreamStore`、`port.AgentControlBus`、`port.ExecutionLeaseRepo`

- [ ] **Step 1: 写端口**

创建 `internal/agent/domain/port/agent_stream.go`：

```go
package port

import (
	"context"
	"errors"
	"time"
)

// ErrLeaseConflict 表示租约 CAS 未命中：expect generation 已被别的 runner
// 推进（或已过期被抢）。调用方据此判定「没抢到」，转入 TAIL 读赢家的流。
var ErrLeaseConflict = errors.New("agent: execution lease conflict")

// 流事件名。事件名是流条目的固有属性（服务端据此识别终态与游标语义），
// 与「是否下发 event: 行」是两件事——下发的白名单在 handler 层（spec §6.8）。
const (
	StreamEventMeta             = "meta"
	StreamEventToken            = "token"
	StreamEventDelegate         = "delegate"
	StreamEventDone             = "done"
	StreamEventError            = "error"
	StreamEventStopped          = "stopped"
	StreamEventReset            = "reset"
	StreamEventApprovalRequired = "approval_required"
)

// IsTerminalStreamEvent 判定该事件是否终结一次 run。
func IsTerminalStreamEvent(name string) bool {
	switch name {
	case StreamEventDone, StreamEventError, StreamEventStopped, StreamEventApprovalRequired:
		return true
	default:
		return false
	}
}

// StreamEntry 是执行输出流中的一条事件。
type StreamEntry struct {
	// ID 是 Redis entry ID，逐字作为 SSE 的 id: 行，也是重连游标。
	ID string
	// Event 是事件名；空串表示该条目无事件名。
	Event string
	// Payload 是 data: 行的原文 JSON。
	Payload string
}

// AgentStreamStore 是执行输出流（尽力而为的显示缓冲，不是事实源）的存储抽象。
//
// 租户命名空间在实现内部构造：调用方只给 executionID / generation，永远拿不到
// 也构造不出裸 key。这是 spec D4 的落地——execution_id 客户端可控，key 必须
// 经过租户命名空间，且订阅前还有一次租户限定的 checkpoint 查询。
type AgentStreamStore interface {
	// Append 追加一条事件并返回其流内 ID。
	Append(ctx context.Context, executionID string, generation int, e StreamEntry) (string, error)
	// Replay 全量回放该 generation 的流（从最老条目开始）。
	Replay(ctx context.Context, executionID string, generation int) ([]StreamEntry, error)
	// ReplayAfter 回放 afterID 之后的条目（排他）。
	ReplayAfter(ctx context.Context, executionID string, generation int, afterID string) ([]StreamEntry, error)
	// FirstID 返回流最老条目的 ID；空流返回 ""。用于裁剪缺口判定。
	FirstID(ctx context.Context, executionID string, generation int) (string, error)
	// Tail 从 afterID 之后阻塞读取至多 block 时长；无新条目返回空切片。
	Tail(ctx context.Context, executionID string, generation int, afterID string, block int) ([]StreamEntry, error)
	// RefreshTTL 刷新流的存活时长。
	RefreshTTL(ctx context.Context, executionID string, generation int) error
	// Delete 删除该 generation 的流。
	Delete(ctx context.Context, executionID string, generation int) error
}

// ControlMessage 是控制通道上的一条消息。
type ControlMessage struct {
	Type     string `json:"type"`
	ViewerID string `json:"viewer_id,omitempty"`
}

// 控制消息类型。
const (
	ControlMessageViewer = "viewer"
	ControlMessageCancel = "cancel"
)

// AgentControlBus 是执行控制通道（Redis Pub/Sub）。
//
// 安全边界：Pub/Sub 通道不承载鉴权。PublishStop 的调用方必须在 HTTP 层先校验
// actor 对该 execution 的所有权，本接口不做也不该做这件事（spec §9）。
type AgentControlBus interface {
	PublishStop(ctx context.Context, executionID string) error
	PublishViewer(ctx context.Context, executionID, viewerID string) error
	// Subscribe 返回消息通道与取消函数。实现必须持独立的 *goredis.Client：
	// go-redis 的 Pub/Sub 独占一条连接，与 Stream 命令共用会互相饿死。
	// runner 必须在 run 启动之前完成订阅，否则会漏掉早到的 viewer 消息。
	Subscribe(ctx context.Context, executionID string) (<-chan ControlMessage, func(), error)
}

// LeaseStatus 是执行租约的一次性快照。
type LeaseStatus struct {
	// Generation 是 checkpoint 当前的 run_generation（fencing token）。
	Generation int
	// Active 表示租约仍在有效期内（lease_expires_at > NOW()）。
	Active bool
}

// ExecutionLeaseRepo 是执行租约的持久化抽象。刻意与 CheckpointRepo 分开：
// 扩展既有接口会一次性打破 7 处测试替身，而租约的语义（CAS + fencing token）
// 与 checkpoint 的状态迁移正交。
//
// 租约是**咨询性信号**（"现在还有没有别人在跑"），互斥本身来自 run_generation
// 的原子 CAS——ClaimLease 在 expect 上竞争，恰好一个获胜者（spec D2/D3）。
type ExecutionLeaseRepo interface {
	// StampLease 为新建执行盖上租约，返回当前 generation（NEW 路径 Upsert 已置
	// 1，本方法不推进 generation）。无条件执行是安全的：NEW 路径的 executionID
	// 由服务端生成，不可能与别人的执行撞上。
	StampLease(ctx context.Context, tenantID, executionID string, lease time.Duration) (int, error)
	// ClaimLease 在 run_generation == expect 时抢占：推进到 expect+1 并刷新租约，
	// 返回新 generation。CAS 未命中返回 ErrLeaseConflict。
	ClaimLease(ctx context.Context, tenantID, executionID string, expect int, lease time.Duration) (int, error)
	// RenewLease 续租。generation 不匹配（已被抢占）返回 ErrLeaseConflict，
	// 僵尸 runner 据此立刻自取消，不继续烧 token。
	RenewLease(ctx context.Context, tenantID, executionID string, generation int, lease time.Duration) error
	// ReleaseLease 主动释放租约（run 正常结束）。
	ReleaseLease(ctx context.Context, tenantID, executionID string, generation int) error
	// LeaseStatus 读取当前 generation 与租约是否在有效期内。
	LeaseStatus(ctx context.Context, tenantID, executionID string) (LeaseStatus, error)
}
```

- [ ] **Step 2: 编译确认**

Run: `go build ./internal/agent/domain/... && go vet ./internal/agent/domain/...`
Expected: 无输出（成功）。端口无行为，不写单测——行为由 Task 6/8/9 的实现测试覆盖。

- [ ] **Step 3: 提交**

```bash
git add internal/agent/domain/port/agent_stream.go
git commit -m "feat(agent): 执行流/控制通道/租约端口定义"
```

---

## Task 5: `lease_expires_at` 租户 DDL

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql:1017-1053`
- Test: `pkg/storage/postgres/tenant_schema_test.go`

**Interfaces:**

- Consumes: 无
- Produces: 列 `agent_execution_checkpoints.lease_expires_at TIMESTAMPTZ`（nullable，无默认）

- [ ] **Step 1: 写失败测试**

追加到 `pkg/storage/postgres/tenant_schema_test.go`：

```go
func TestTenantSchemaAddsCheckpointLeaseColumn(t *testing.T) {
	text := readTenantSchema(t)
	create := "lease_expires_at        TIMESTAMPTZ"
	alter := "ALTER TABLE agent_execution_checkpoints ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ;"
	// CREATE TABLE 内嵌新列只对新租户生效，存量租户必须靠 ADD COLUMN IF NOT EXISTS
	// 补齐。两者缺一都会让一半租户的租约查询报 column does not exist。
	if !strings.Contains(text, create) {
		t.Errorf("tenant schema missing lease_expires_at column in CREATE TABLE")
	}
	if !strings.Contains(text, alter) {
		t.Errorf("tenant schema missing idempotent ALTER for lease_expires_at")
	}
	// 新列必须排在依赖它的索引/约束之前：本列无索引依赖，但必须排在
	// CREATE UNIQUE INDEX 之前以保持历史 schema 顺序（先建表/补列，后建索引）。
	if strings.Index(text, alter) > strings.Index(text, "CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_execution_checkpoints_execution") {
		t.Errorf("lease_expires_at ALTER must precede the checkpoint indexes")
	}
	// 可空且无 DEFAULT 是刻意的：NULL 表示「无 runner 持有」，与
	// lease_expires_at <= NOW() 的「租约过期」是两种不同状态。
	if !strings.Contains(text, alter) || strings.Contains(text, "lease_expires_at TIMESTAMPTZ NOT NULL") {
		t.Errorf("lease_expires_at must be nullable without NOT NULL")
	}
}
```

> 若 `readTenantSchema(t)` 在本文件中不存在，用同文件既有用例读取 `tenant_schema.sql` 的同一个 helper（执行 Step 2 前先 `grep -n "func readTenantSchema\|embed" pkg/storage/postgres/tenant_schema_test.go` 确认名字；该文件已有多个 `strings.Contains(text, ...)` 形式的静态断言）。

- [ ] **Step 2: 运行测试确认失败**

Run: `grep -n "tenant_schema.sql\|func .*Schema.*t \*testing.T" pkg/storage/postgres/tenant_schema_test.go | head`
先确认 helper 名称，然后：
Run: `go test ./pkg/storage/postgres/ -run TestTenantSchemaAddsCheckpointLeaseColumn -v`
Expected: FAIL — `missing lease_expires_at column in CREATE TABLE`

- [ ] **Step 3: 改 DDL**

在 `pkg/storage/postgres/tenant_schema.sql` 中，`run_generation` 行的**紧后面**（同一 CREATE TABLE 内）追加：

```sql
    -- lease_expires_at: 执行租约（spec §6.7）。NULL 表示无 runner 持有；
    -- 非 NULL 且 <= NOW() 表示租约过期但 runner 可能仍是僵尸。租约本身只是
    -- 咨询性信号，真正的互斥来自 run_generation 的 CAS。
    lease_expires_at          TIMESTAMPTZ,
```

并在历史租户补齐块（`run_generation` 的 `ADD COLUMN IF NOT EXISTS` 之后、`CREATE UNIQUE INDEX` 之前）追加：

```sql
ALTER TABLE agent_execution_checkpoints ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMPTZ;
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./pkg/storage/postgres/ -run 'TestTenantSchema' -v`
Expected: PASS（新增用例 + 既有全部 schema 静态断言）
再跑：`go test ./pkg/storage/postgres/ -short`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add pkg/storage/postgres/tenant_schema.sql pkg/storage/postgres/tenant_schema_test.go
git commit -m "feat(postgres): agent_execution_checkpoints 增加 lease_expires_at"
```

---

## Task 6: 租约 CAS SQL

**Files:**

- Create: `internal/agent/infrastructure/persistence/checkpoint_lease.go`
- Test: `internal/agent/infrastructure/persistence/checkpoint_lease_integration_test.go`

**Interfaces:**

- Consumes: `port.ExecutionLeaseRepo`、`port.ErrLeaseConflict`、`port.LeaseStatus`（Task 4）；`execTenantID`（同包）
- Produces: `*PgCheckpointStore` 实现 `port.ExecutionLeaseRepo` 的五个方法

- [ ] **Step 1: 写失败测试**

创建 `internal/agent/infrastructure/persistence/checkpoint_lease_integration_test.go`：

```go
package persistence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// newLeaseTestStore 起一个隔离租户 schema 并写入一条 running checkpoint。
func newLeaseTestStore(t *testing.T, executionID string) (*PgCheckpointStore, string) {
	t.Helper()
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	tenantID := fmt.Sprintf("tmp_lease_%d", time.Now().UnixNano())
	schema := "tenant_" + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	store := NewPgCheckpointStore(pool)
	if err := store.Upsert(ctx, tenantID, domain.AgentExecutionCheckpoint{
		ExecutionID: executionID, TraceID: "t", AgentID: "a", UserID: "u",
		Status: "running", ExpiresAt: time.Now().Add(time.Hour), RunGeneration: 1,
	}); err != nil {
		t.Fatal(err)
	}
	return store, tenantID
}

func TestLeaseStampThenStatusActive(t *testing.T) {
	store, tenantID := newLeaseTestStore(t, "exec-stamp")
	ctx := context.Background()
	gen, err := store.StampLease(ctx, tenantID, "exec-stamp", time.Minute)
	if err != nil {
		t.Fatalf("StampLease: %v", err)
	}
	if gen != 1 {
		t.Fatalf("StampLease gen = %d, want 1 (stamp must not advance generation)", gen)
	}
	status, err := store.LeaseStatus(ctx, tenantID, "exec-stamp")
	if err != nil {
		t.Fatalf("LeaseStatus: %v", err)
	}
	if status.Generation != 1 || !status.Active {
		t.Fatalf("LeaseStatus = %+v, want {1 true}", status)
	}
}

func TestLeaseStampOnMissingCheckpointFails(t *testing.T) {
	store, tenantID := newLeaseTestStore(t, "exec-present")
	if _, err := store.StampLease(context.Background(), tenantID, "exec-absent", time.Minute); err == nil {
		t.Fatal("expected error stamping lease on missing checkpoint")
	}
}

func TestLeaseClaimIsExclusive(t *testing.T) {
	store, tenantID := newLeaseTestStore(t, "exec-claim")
	ctx := context.Background()
	if _, err := store.StampLease(ctx, tenantID, "exec-claim", time.Minute); err != nil {
		t.Fatal(err)
	}
	gen, err := store.ClaimLease(ctx, tenantID, "exec-claim", 1, time.Minute)
	if err != nil {
		t.Fatalf("ClaimLease: %v", err)
	}
	if gen != 2 {
		t.Fatalf("ClaimLease gen = %d, want 2", gen)
	}
	// 第二个 claimant 拿同样的 expect=1 必须失败——这就是唯一的互斥来源。
	if _, err := store.ClaimLease(ctx, tenantID, "exec-claim", 1, time.Minute); !errors.Is(err, port.ErrLeaseConflict) {
		t.Fatalf("second ClaimLease = %v, want ErrLeaseConflict", err)
	}
}

func TestLeaseRenewFencesStaleRunner(t *testing.T) {
	store, tenantID := newLeaseTestStore(t, "exec-renew")
	ctx := context.Background()
	if _, err := store.StampLease(ctx, tenantID, "exec-renew", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.RenewLease(ctx, tenantID, "exec-renew", 1, time.Minute); err != nil {
		t.Fatalf("RenewLease: %v", err)
	}
	// 抢占者把 generation 推到 2，僵尸 runner 的续租必须被 fence 掉。
	if _, err := store.ClaimLease(ctx, tenantID, "exec-renew", 1, time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.RenewLease(ctx, tenantID, "exec-renew", 1, time.Minute); !errors.Is(err, port.ErrLeaseConflict) {
		t.Fatalf("stale RenewLease = %v, want ErrLeaseConflict", err)
	}
}

func TestLeaseReleaseClearsActive(t *testing.T) {
	store, tenantID := newLeaseTestStore(t, "exec-release")
	ctx := context.Background()
	if _, err := store.StampLease(ctx, tenantID, "exec-release", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseLease(ctx, tenantID, "exec-release", 1); err != nil {
		t.Fatalf("ReleaseLease: %v", err)
	}
	status, err := store.LeaseStatus(ctx, tenantID, "exec-release")
	if err != nil {
		t.Fatal(err)
	}
	if status.Active {
		t.Fatalf("LeaseStatus.Active = true after release, want false")
	}
}

func TestLeaseStatusIsTenantScoped(t *testing.T) {
	store, tenantID := newLeaseTestStore(t, "exec-scoped")
	other := tenantID + "_other"
	ctx := context.Background()
	schema := "tenant_" + other
	t.Cleanup(func() {
		_, _ = store.pool.(*pgxpool.Pool).Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`)
	})
	if _, err := store.StampLease(ctx, tenantID, "exec-scoped", time.Minute); err != nil {
		t.Fatal(err)
	}
	// 另一个租户读同一个 execution_id 必须查不到行，而不是读到别人的租约。
	if _, err := store.LeaseStatus(ctx, other, "exec-scoped"); err == nil {
		t.Fatal("expected LeaseStatus to fail for a tenant without the checkpoint")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/infrastructure/persistence/ -run TestLease -v`
Expected: FAIL — `store.StampLease undefined`

- [ ] **Step 3: 实现**

创建 `internal/agent/infrastructure/persistence/checkpoint_lease.go`：

```go
package persistence

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/jackc/pgx/v5"
)

// 租约方法挂在 PgCheckpointStore 上而不是独立 repo：租约列与 checkpoint 同行，
// 拆表会让「抢占 generation」与「占用租约」失去同事务的原子性。端口侧仍是独立的
// ExecutionLeaseRepo，消费方不感知这次复用（spec D2）。
var _ port.ExecutionLeaseRepo = (*PgCheckpointStore)(nil)

// StampLease 为新建执行盖上租约并返回当前 generation。不推进 generation：
// NEW 路径的 Ensure/Upsert 已把 run_generation 初始化为 1。
func (s *PgCheckpointStore) StampLease(
	ctx context.Context, tenantID, executionID string, lease time.Duration,
) (int, error) {
	var generation int
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`UPDATE agent_execution_checkpoints
			    SET lease_expires_at = NOW() + $2::interval, updated_at = NOW()
			  WHERE execution_id = $1
			  RETURNING run_generation`,
			executionID, lease.String(),
		).Scan(&generation)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("checkpoint_store: stamp lease: no checkpoint %s", executionID)
	}
	if err != nil {
		return 0, fmt.Errorf("checkpoint_store: stamp lease: %w", err)
	}
	return generation, nil
}

// ClaimLease 在 run_generation == expect 时抢占。这是互斥的唯一来源：
// 并发的两个 claimant 传同一个 expect，恰好一个 RowsAffected == 1。
// 终态 checkpoint 不可抢占——恢复一个已完成/已失败的执行没有语义。
func (s *PgCheckpointStore) ClaimLease(
	ctx context.Context, tenantID, executionID string, expect int, lease time.Duration,
) (int, error) {
	var generation int
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`UPDATE agent_execution_checkpoints
			    SET lease_expires_at = NOW() + $3::interval,
			        run_generation = run_generation + 1,
			        updated_at = NOW()
			  WHERE execution_id = $1
			    AND run_generation = $2
			    AND status NOT IN ('completed', 'failed', 'expired')
			  RETURNING run_generation`,
			executionID, expect, lease.String(),
		).Scan(&generation)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, port.ErrLeaseConflict
	}
	if err != nil {
		return 0, fmt.Errorf("checkpoint_store: claim lease: %w", err)
	}
	return generation, nil
}

// RenewLease 续租。generation 不匹配即被抢——僵尸 runner 收到 ErrLeaseConflict
// 后必须立刻自取消，否则会继续烧 token（spec D3）。
func (s *PgCheckpointStore) RenewLease(
	ctx context.Context, tenantID, executionID string, generation int, lease time.Duration,
) error {
	var affected int64
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE agent_execution_checkpoints
			    SET lease_expires_at = NOW() + $3::interval, updated_at = NOW()
			  WHERE execution_id = $1 AND run_generation = $2`,
			executionID, generation, lease.String(),
		)
		if err != nil {
			return fmt.Errorf("checkpoint_store: renew lease: %w", err)
		}
		affected = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return err
	}
	if affected != 1 {
		return port.ErrLeaseConflict
	}
	return nil
}

// ReleaseLease 释放租约。generation 不匹配说明自己已被抢，无需清理。
// 用 generation 作为 guard 而非无条件清空：否则僵尸 runner 退出时会把
// 新 runner 刚占上的租约抹掉。
func (s *PgCheckpointStore) ReleaseLease(
	ctx context.Context, tenantID, executionID string, generation int,
) error {
	var affected int64
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE agent_execution_checkpoints
			    SET lease_expires_at = NULL, updated_at = NOW()
			  WHERE execution_id = $1 AND run_generation = $2`,
			executionID, generation,
		)
		if err != nil {
			return fmt.Errorf("checkpoint_store: release lease: %w", err)
		}
		affected = tag.RowsAffected()
		return nil
	})
	if err != nil {
		return err
	}
	if affected != 1 {
		return port.ErrLeaseConflict
	}
	return nil
}

// LeaseStatus 读取 generation 与租约是否有效。缺行返回错误（fail closed）：
// 调用方在此之前已用 GetLatest 做过租户限定的存在性判断，走到这里还没行
// 说明状态已变，不应静默降级成 generation=0。
func (s *PgCheckpointStore) LeaseStatus(
	ctx context.Context, tenantID, executionID string,
) (port.LeaseStatus, error) {
	var status port.LeaseStatus
	err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT run_generation, (lease_expires_at IS NOT NULL AND lease_expires_at > NOW())
			   FROM agent_execution_checkpoints
			  WHERE execution_id = $1`,
			executionID,
		).Scan(&status.Generation, &status.Active)
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return port.LeaseStatus{}, fmt.Errorf("checkpoint_store: lease status: no checkpoint %s", executionID)
	}
	if err != nil {
		return port.LeaseStatus{}, fmt.Errorf("checkpoint_store: lease status: %w", err)
	}
	return status, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent/infrastructure/persistence/ -run TestLease -v`
无 `STRATUM_TEST_POSTGRES_URL` 时 SKIP；有依赖服务时：

```bash
make infra-up
export STRATUM_TEST_POSTGRES_URL="postgres://stratum:stratum@localhost:5432/stratum?sslmode=disable"
go test ./internal/agent/infrastructure/persistence/ -run TestLease -v
```

Expected: PASS（6 个）
Run: `go build ./... && go vet ./internal/agent/...`
Expected: 成功

- [ ] **Step 5: 提交**

```bash
git add internal/agent/infrastructure/persistence/checkpoint_lease.go internal/agent/infrastructure/persistence/checkpoint_lease_integration_test.go
git commit -m "feat(agent): checkpoint 租约 CAS（Stamp/Claim/Renew/Release/Status）"
```

---

## Task 7: 订阅计划纯函数

**Files:**

- Create: `internal/agent/application/agent_stream_plan.go`
- Test: `internal/agent/application/agent_stream_plan_test.go`

**Interfaces:**

- Consumes: 无（纯函数，零 IO）
- Produces: `StreamPlan{Generation int; Reset bool; ResetReason string; AfterID string}`、`PlanStream(genClient, genNow int, lastEventID string) StreamPlan`、`CompareStreamID(a, b string) int`、`HasReplayGap(cursor, oldestID string) bool`、`ResetReasonGenerationChanged`、`ResetReasonStreamLost`

- [ ] **Step 1: 写失败测试**

创建 `internal/agent/application/agent_stream_plan_test.go`：

```go
package application

import "testing"

func TestCompareStreamID(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int
	}{
		// seq 不补零："...-9" 的字典序大于 "...-10"，字符串比较会判反。
		{"sequence not zero padded", "1726483200000-9", "1726483200000-10", -1},
		{"sequence zero padded", "1726483200000-09", "1726483200000-10", -1},
		{"ms dominates", "1726483200000-99", "1726483200001-0", -1},
		{"equal", "1726483200000-37", "1726483200000-37", 0},
		{"greater", "1726483200001-0", "1726483200000-99", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := CompareStreamID(tc.a, tc.b); got != tc.want {
				t.Errorf("CompareStreamID(%q,%q) = %d, want %d", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestPlanStream(t *testing.T) {
	cases := []struct {
		name        string
		genClient   int
		genNow      int
		lastEventID string
		wantReset   bool
		wantReason  string
		wantAfterID string
	}{
		{
			// F5：React 状态全丢，只发 generation。全量回放当前 gen，
			// 但不发 reset——气泡本来就是空的，发 reset 只会多一次清空。
			name: "fresh reload replays from start without reset",
			genClient: 1, genNow: 1, lastEventID: "",
			wantReset: false, wantAfterID: "",
		},
		{
			// 网络抖动：内存游标存活，增量回放。
			name: "cursor present replays incrementally",
			genClient: 1, genNow: 1, lastEventID: "1726483200000-37",
			wantReset: false, wantAfterID: "1726483200000-37",
		},
		{
			// pod 重启：generation 变了，必须清空重渲染。
			name: "generation changed resets",
			genClient: 1, genNow: 2, lastEventID: "1726483200000-37",
			wantReset: true, wantReason: "generation_changed", wantAfterID: "",
		},
		{
			// 老客户端不传 generation：一律 reset（fail closed）。对 F5 场景
			// 恰好正确——反正也是全量回放。
			name: "missing generation resets",
			genClient: 0, genNow: 1, lastEventID: "",
			wantReset: true, wantReason: "generation_changed", wantAfterID: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := PlanStream(tc.genClient, tc.genNow, tc.lastEventID)
			if got.Reset != tc.wantReset || got.ResetReason != tc.wantReason || got.AfterID != tc.wantAfterID {
				t.Errorf("PlanStream = %+v, want reset=%v reason=%q after=%q",
					got, tc.wantReset, tc.wantReason, tc.wantAfterID)
			}
			if got.Generation != tc.genNow {
				t.Errorf("Generation = %d, want %d", got.Generation, tc.genNow)
			}
		})
	}
}

func TestHasReplayGap(t *testing.T) {
	cases := []struct {
		name     string
		cursor   string
		oldestID string
		want     bool
	}{
		{"no cursor is full replay not a gap", "", "1726483200000-1", false},
		{"empty stream is not a gap", "1726483200000-1", "", false},
		{"cursor before oldest is a gap", "1726483200000-1", "1726483200000-50", true},
		{"cursor at oldest is not a gap", "1726483200000-50", "1726483200000-50", false},
		{"cursor after oldest is not a gap", "1726483200000-99", "1726483200000-50", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasReplayGap(tc.cursor, tc.oldestID); got != tc.want {
				t.Errorf("HasReplayGap(%q,%q) = %v, want %v", tc.cursor, tc.oldestID, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/application/ -run 'TestCompareStreamID|TestPlanStream|TestHasReplayGap' -v`
Expected: FAIL — `undefined: CompareStreamID`

- [ ] **Step 3: 实现**

创建 `internal/agent/application/agent_stream_plan.go`：

```go
// Package application — agent_stream_plan.go.
//
// 订阅计划的纯函数层：把「客户端游标 + 服务端现状」翻译成订阅动作。
// 零 IO、零状态，因此可以穷举边界——游标判定是整个续传协议最容易出错的地方。
package application

import (
	"strconv"
	"strings"
)

// reset 原因。reset 由订阅侧合成，不写入流——它是订阅者对「游标与当前
// generation 不匹配」的翻译，不是 run 的产出（spec §6.6）。
const (
	ResetReasonGenerationChanged = "generation_changed"
	ResetReasonStreamLost        = "stream_lost"
)

// StreamPlan 是一次订阅的行动计划。
type StreamPlan struct {
	// Generation 是本次订阅要读的流分代。
	Generation int
	// Reset 为 true 时订阅侧必须先下发 reset 帧，前端据此清空当前气泡。
	Reset bool
	// ResetReason 见 ResetReason* 常量，Reset 为 false 时为空。
	ResetReason string
	// AfterID 是增量回放的起点（排他）；空串表示从流的开头全量回放。
	AfterID string
}

// PlanStream 把客户端游标与服务端现状翻译成订阅计划（spec §6.4/§6.5）。
//
//   genClient != genNow → reset + 全量回放当前 generation
//   genClient == genNow → 有游标则增量回放，无游标则全量回放（F5 场景）
//
// 缺 generation（genClient == 0）一律 reset：连续性上 fail closed。老客户端
// 恰好落在 F5 场景，全量回放正是它需要的（spec §10）。
func PlanStream(genClient, genNow int, lastEventID string) StreamPlan {
	if genClient != genNow {
		return StreamPlan{Generation: genNow, Reset: true, ResetReason: ResetReasonGenerationChanged}
	}
	return StreamPlan{Generation: genNow, AfterID: lastEventID}
}

// HasReplayGap 判定游标是否早于流现存最老条目——即 MAXLEN 裁剪已经把游标
// 之后应有的内容剪掉了。有缺口时增量回放会给出半截答案，必须 reset 重来，
// 而不是把带洞的文本交给用户（spec §6.6）。
func HasReplayGap(cursor, oldestID string) bool {
	if cursor == "" || oldestID == "" {
		return false
	}
	return CompareStreamID(cursor, oldestID) < 0
}

// CompareStreamID 按 Redis Stream ID 的数值语义比较两个 entry ID
// （形如 "<ms>-<seq>"），返回 -1 / 0 / 1。
//
// 不能退化成字符串比较：seq 段不补零，"...-9" 的字典序大于 "...-10"，
// 会让缺口判定把「游标落后」误判成「游标领先」，从而漏掉一次本该发生的
// reset。缺失或畸形的段按 0 处理——退化方向是「判为无缺口」，与
// HasReplayGap 的空串语义一致。
func CompareStreamID(a, b string) int {
	aMs, aSeq := splitStreamID(a)
	bMs, bSeq := splitStreamID(b)
	if aMs != bMs {
		return cmpInt64(aMs, bMs)
	}
	return cmpInt64(aSeq, bSeq)
}

func splitStreamID(id string) (int64, int64) {
	ms, seq, found := strings.Cut(id, "-")
	if !found {
		return parseStreamIDPart(ms), 0
	}
	return parseStreamIDPart(ms), parseStreamIDPart(seq)
}

func parseStreamIDPart(s string) int64 {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent/application/ -run 'TestCompareStreamID|TestPlanStream|TestHasReplayGap' -v`
Expected: PASS（3 个顶层，13 个子用例）

- [ ] **Step 5: 提交**

```bash
git add internal/agent/application/agent_stream_plan.go internal/agent/application/agent_stream_plan_test.go
git commit -m "feat(agent): 订阅计划纯函数（游标判定/Stream ID 比较/缺口检测）"
```

---

## Task 8: 流存储端口实现

**Files:**

- Create: `internal/agent/infrastructure/stream/agent_stream_store.go`
- Test: `internal/agent/infrastructure/stream/agent_stream_store_test.go`

**Interfaces:**

- Consumes: `port.AgentStreamStore`（Task 4）、`redis.StreamStore`（Task 3）、`tenantnaming.TenantKey`（Task 2）、`constants.AgentStreamTTL`/`AgentStreamMaxLen`/`AgentStreamReadBlock`/`AgentStreamReplayBatch`（Task 1）
- Produces: `stream.NewAgentStreamStore(*redis.StreamStore) *AgentStreamStore`

- [ ] **Step 1: 写失败测试**

创建 `internal/agent/infrastructure/stream/agent_stream_store_test.go`：

```go
package stream_test

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	agentstream "github.com/byteBuilderX/stratum/internal/agent/infrastructure/stream"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	pgcontext "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/storage/redis"
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
	return agentstream.NewAgentStreamStore(redis.NewStreamStore(rdb))
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
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/infrastructure/stream/ -v`
Expected: FAIL — `no required module provides package .../infrastructure/stream`

- [ ] **Step 3: 实现**

创建 `internal/agent/infrastructure/stream/agent_stream_store.go`：

```go
// Package stream 实现 Agent 执行输出流的端口。
//
// 租户命名空间在这里收口：调用方只给 executionID / generation，key 由本包
// 经 tenantnaming.TenantKey 构造，因此调用方永远构造不出裸 key，也不可能
// 绕过租户边界（spec D4）。
package stream

import (
	"context"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/storage/redis"
	"github.com/byteBuilderX/stratum/pkg/storage/tenantnaming"
)

// 流 key 的段名。agent:stream:{tenant}:{executionID}:{gen}
const keySegmentStream = "stream"

// AgentStreamStore 是 port.AgentStreamStore 的 Redis 实现。
type AgentStreamStore struct {
	stream  *redis.StreamStore
	ttl     time.Duration
	maxLen  int64
	replay  int64
	readBlk time.Duration
}

var _ port.AgentStreamStore = (*AgentStreamStore)(nil)

// NewAgentStreamStore 用默认时长装配；测试可用 NewAgentStreamStoreWith。
func NewAgentStreamStore(s *redis.StreamStore) *AgentStreamStore {
	return NewAgentStreamStoreWith(s, constants.AgentStreamTTL, constants.AgentStreamMaxLen,
		constants.AgentStreamReplayBatch, constants.AgentStreamReadBlock)
}

// NewAgentStreamStoreWith 注入时长与容量，供跨实例测试把秒级等待压到毫秒级。
func NewAgentStreamStoreWith(
	s *redis.StreamStore, ttl time.Duration, maxLen, replay int64, readBlock time.Duration,
) *AgentStreamStore {
	return &AgentStreamStore{stream: s, ttl: ttl, maxLen: maxLen, replay: replay, readBlk: readBlock}
}

func (s *AgentStreamStore) Append(
	ctx context.Context, executionID string, generation int, e port.StreamEntry,
) (string, error) {
	key, err := s.key(ctx, executionID, generation)
	if err != nil {
		return "", err
	}
	id, err := s.stream.AppendEvent(ctx, key, s.maxLen, e.Event, e.Payload)
	if err != nil {
		return "", err
	}
	// TTL 在写路径搭车刷新，不额外起定时器。失败不阻断写入——流本身是尽力而为
	// 的显示缓冲，丢一次续期最多让流早一小时过期。
	if err := s.stream.Expire(ctx, key, s.ttl); err != nil {
		return id, nil //nolint:nilerr // 续期失败不影响本次写入，调用方已拿到 ID
	}
	return id, nil
}

func (s *AgentStreamStore) Replay(
	ctx context.Context, executionID string, generation int,
) ([]port.StreamEntry, error) {
	return s.ReplayAfter(ctx, executionID, generation, "")
}

func (s *AgentStreamStore) ReplayAfter(
	ctx context.Context, executionID string, generation int, afterID string,
) ([]port.StreamEntry, error) {
	key, err := s.key(ctx, executionID, generation)
	if err != nil {
		return nil, err
	}
	entries, err := s.stream.Range(ctx, key, afterID, s.replay)
	if err != nil {
		return nil, err
	}
	return toPortEntries(entries), nil
}

func (s *AgentStreamStore) FirstID(ctx context.Context, executionID string, generation int) (string, error) {
	key, err := s.key(ctx, executionID, generation)
	if err != nil {
		return "", err
	}
	return s.stream.FirstID(ctx, key)
}

func (s *AgentStreamStore) Tail(
	ctx context.Context, executionID string, generation int, afterID string, block int,
) ([]port.StreamEntry, error) {
	key, err := s.key(ctx, executionID, generation)
	if err != nil {
		return nil, err
	}
	d := s.readBlk
	if block > 0 {
		d = time.Duration(block) * time.Millisecond
	}
	entries, err := s.stream.Tail(ctx, key, afterID, s.replay, d)
	if err != nil {
		return nil, err
	}
	return toPortEntries(entries), nil
}

func (s *AgentStreamStore) RefreshTTL(ctx context.Context, executionID string, generation int) error {
	key, err := s.key(ctx, executionID, generation)
	if err != nil {
		return err
	}
	return s.stream.Expire(ctx, key, s.ttl)
}

func (s *AgentStreamStore) Delete(ctx context.Context, executionID string, generation int) error {
	key, err := s.key(ctx, executionID, generation)
	if err != nil {
		return err
	}
	return s.stream.Delete(ctx, key)
}

func (s *AgentStreamStore) key(ctx context.Context, executionID string, generation int) (string, error) {
	return tenantnaming.TenantKey(ctx, tenantnaming.RedisKeyPrefix,
		keySegmentStream, executionID, strconv.Itoa(generation))
}

func toPortEntries(entries []redis.StreamEntry) []port.StreamEntry {
	out := make([]port.StreamEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, port.StreamEntry{ID: e.ID, Event: e.Event, Payload: e.Payload})
	}
	return out
}
```

在 import 中加入 `"strconv"`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent/infrastructure/stream/ -v`
Expected: PASS（7 个）

- [ ] **Step 5: 提交**

```bash
git add internal/agent/infrastructure/stream/
git commit -m "feat(agent): 执行流存储端口实现（租户命名空间收口）"
```

---

## Task 9: 控制通道

**Files:**

- Create: `internal/agent/infrastructure/stream/control_bus.go`
- Test: `internal/agent/infrastructure/stream/control_bus_test.go`
- Modify: `pkg/storage/redis/client.go`（`Duplicate`）

**Interfaces:**

- Consumes: `port.AgentControlBus`、`tenantnaming.TenantKey`
- Produces: `stream.NewControlBus(*goredis.Client) *ControlBus`；`redis.Client.Duplicate() *Client`

- [ ] **Step 1: 写失败测试**

创建 `internal/agent/infrastructure/stream/control_bus_test.go`：

```go
package stream_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	agentstream "github.com/byteBuilderX/stratum/internal/agent/infrastructure/stream"
)

func newControlBus(t *testing.T) *agentstream.ControlBus {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return agentstream.NewControlBus(rdb)
}

func TestControlBusPublishesViewerAndStop(t *testing.T) {
	bus := newControlBus(t)
	ctx := tenantCtx("acme")
	// runner 必须在 run 启动前订阅好，这里同样先订阅再发布。
	msgs, unsubscribe, err := bus.Subscribe(ctx, "exec-1")
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer unsubscribe()

	// Pub/Sub 订阅建立是异步的：先确认订阅已生效再发布，否则首条消息会丢。
	if err := bus.WaitSubscribed(ctx, "exec-1"); err != nil {
		t.Fatalf("WaitSubscribed: %v", err)
	}
	if err := bus.PublishViewer(ctx, "exec-1", "viewer-a"); err != nil {
		t.Fatalf("PublishViewer: %v", err)
	}
	if err := bus.PublishStop(ctx, "exec-1"); err != nil {
		t.Fatalf("PublishStop: %v", err)
	}

	got := make([]port.ControlMessage, 0, 2)
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case m := <-msgs:
			got = append(got, m)
		case <-timeout:
			t.Fatalf("timed out; got %+v", got)
		}
	}
	if got[0].Type != port.ControlMessageViewer || got[0].ViewerID != "viewer-a" {
		t.Fatalf("msg[0] = %+v, want viewer viewer-a", got[0])
	}
	if got[1].Type != port.ControlMessageCancel {
		t.Fatalf("msg[1] = %+v, want cancel", got[1])
	}
}

func TestControlBusIsTenantScoped(t *testing.T) {
	// 两个租户的同名 execution_id 必须落在不同通道，否则一个租户的停止指令
	// 会杀掉另一个租户的运行中执行。
	bus := newControlBus(t)
	acme, unsub1, err := bus.Subscribe(tenantCtx("acme"), "shared-exec")
	if err != nil {
		t.Fatal(err)
	}
	defer unsub1()
	if err := bus.WaitSubscribed(tenantCtx("acme"), "shared-exec"); err != nil {
		t.Fatal(err)
	}
	if err := bus.PublishStop(tenantCtx("globex"), "shared-exec"); err != nil {
		t.Fatal(err)
	}
	select {
	case m := <-acme:
		t.Fatalf("cross-tenant stop leaked: %+v", m)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestControlBusSubscribeFailsClosedWithoutTenant(t *testing.T) {
	bus := newControlBus(t)
	if _, _, err := bus.Subscribe(context.Background(), "exec-1"); err == nil {
		t.Fatal("expected fail-closed error for missing tenant context")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/infrastructure/stream/ -run TestControlBus -v`
Expected: FAIL — `undefined: agentstream.NewControlBus`

- [ ] **Step 3: 实现**

创建 `internal/agent/infrastructure/stream/control_bus.go`：

```go
package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/tenantnaming"
	goredis "github.com/redis/go-redis/v9"
)

// 控制通道 key 的段名。agent:ctrl:{tenant}:{executionID}
const keySegmentControl = "ctrl"

// subscribeReadyTimeout 是 WaitSubscribed 等待 Pub/Sub 就绪的上限。
const subscribeReadyTimeout = 2 * time.Second

// ControlBus 是 port.AgentControlBus 的 Redis Pub/Sub 实现。
//
// 它持有独立的 *goredis.Client：go-redis 的 Subscribe 独占一条连接，与 Stream
// 命令共用同一实例会让订阅期间的普通命令排队饿死（spec §7.1）。
type ControlBus struct {
	client *goredis.Client

	mu    sync.Mutex
	ready map[string]chan struct{}
}

var _ port.AgentControlBus = (*ControlBus)(nil)

func NewControlBus(client *goredis.Client) *ControlBus {
	return &ControlBus{client: client, ready: make(map[string]chan struct{})}
}

func (b *ControlBus) PublishStop(ctx context.Context, executionID string) error {
	return b.publish(ctx, executionID, port.ControlMessage{Type: port.ControlMessageCancel})
}

func (b *ControlBus) PublishViewer(ctx context.Context, executionID, viewerID string) error {
	return b.publish(ctx, executionID, port.ControlMessage{Type: port.ControlMessageViewer, ViewerID: viewerID})
}

func (b *ControlBus) publish(ctx context.Context, executionID string, msg port.ControlMessage) error {
	channel, err := b.channel(ctx, executionID)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("control_bus: marshal: %w", err)
	}
	if err := b.client.Publish(ctx, channel, payload).Err(); err != nil {
		return fmt.Errorf("control_bus: publish %s: %w", execIDForLog(executionID), err)
	}
	return nil
}

// Subscribe 订阅控制通道。返回的消息通道在 unsubscribe 后被关闭——
// 调用方不得在 unsubscribe 之后继续读。
func (b *ControlBus) Subscribe(ctx context.Context, executionID string) (<-chan port.ControlMessage, func(), error) {
	channel, err := b.channel(ctx, executionID)
	if err != nil {
		return nil, nil, err
	}
	sub := b.client.Subscribe(ctx, channel)
	// Receive 确认订阅已在服务端生效，否则紧接着的 Publish 会被丢掉——
	// runner「必须在开跑前订阅好」的前提（spec §7.1）就落空了。
	if _, err := sub.Receive(ctx); err != nil {
		_ = sub.Close()
		return nil, nil, fmt.Errorf("control_bus: subscribe %s: %w", execIDForLog(executionID), err)
	}

	out := make(chan port.ControlMessage, 16)
	// 独立的消费 context：调用方传入的 ctx 往往是 HTTP 请求 context，
	// 它的取消不该终止 runner 的控制订阅（订阅生命周期由 unsubscribe 决定）。
	consumeCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			cancel()
			_ = sub.Close()
		})
	}

	go func() {
		defer close(out)
		ch := sub.Channel()
		for {
			select {
			case <-consumeCtx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				var parsed port.ControlMessage
				if err := json.Unmarshal([]byte(msg.Payload), &parsed); err != nil {
					// 畸形消息只丢弃，不断订阅——一条坏消息不该让 runner 失去
					// 停止能力。
					continue
				}
				out <- parsed
			}
		}
	}()

	b.markReady(channel)
	return out, unsubscribe, nil
}

// WaitSubscribed 阻塞至多 subscribeReadyTimeout，等待某通道的订阅建立完成。
// 供测试消除「订阅异步生效」的竞态；生产路径不需要——Subscribe 内部的
// Receive 已经同步确认过。
func (b *ControlBus) WaitSubscribed(ctx context.Context, executionID string) error {
	channel, err := b.channel(ctx, executionID)
	if err != nil {
		return err
	}
	ready := b.readyChannel(channel)
	select {
	case <-ready:
		return nil
	case <-time.After(subscribeReadyTimeout):
		return fmt.Errorf("control_bus: subscribe not ready for %s", execIDForLog(executionID))
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *ControlBus) markReady(channel string) {
	b.mu.Lock()
	ch, ok := b.ready[channel]
	if !ok {
		ch = make(chan struct{})
		b.ready[channel] = ch
	}
	b.mu.Unlock()
	close(ch)
}

func (b *ControlBus) readyChannel(channel string) chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch, ok := b.ready[channel]
	if !ok {
		ch = make(chan struct{})
		b.ready[channel] = ch
	}
	return ch
}

func (b *ControlBus) channel(ctx context.Context, executionID string) (string, error) {
	return tenantnaming.TenantKey(ctx, tenantnaming.RedisKeyPrefix, keySegmentControl, executionID)
}

// execIDForLog 只用于错误串：execution_id 本身不是凭据，但也无需完整落日志。
func execIDForLog(executionID string) string {
	if len(executionID) <= 8 {
		return executionID
	}
	return executionID[:8]
}
```

- [ ] **Step 4: 加 `Duplicate` 到 redis client**

在 `pkg/storage/redis/client.go` 的 `Wrap` 之后追加：

```go
// Duplicate 返回共享同一份连接参数、但拥有独立连接池的新 Client。
// go-redis 的 Pub/Sub 独占一条连接，控制通道必须与 Stream 命令分开实例，
// 否则订阅期间普通命令会在同一连接上排队饿死。
// 调用方负责关闭返回的实例。
func (c *Client) Duplicate() *Client {
	return &Client{client: goredis.NewClient(c.client.Options()), logger: c.logger}
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/agent/infrastructure/stream/ ./pkg/storage/redis/ -v`
Expected: PASS（3 个新用例 + 既有）
Run: `go test -race ./internal/agent/infrastructure/stream/`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/agent/infrastructure/stream/control_bus.go internal/agent/infrastructure/stream/control_bus_test.go pkg/storage/redis/client.go
git commit -m "feat(agent): 控制通道（Pub/Sub 独立连接）"
```

---

## Task 10: SSE `id:` 行

**Files:**

- Modify: `api/http/handler/sse_writer.go:10-14,87-99`
- Test: `api/http/handler/sse_writer_test.go`

**Interfaces:**

- Consumes: 无
- Produces: `sseEventWriter.EnqueueStreamFrame(id, name, data string) bool`

- [ ] **Step 1: 写失败测试**

追加到 `api/http/handler/sse_writer_test.go`：

```go
func TestSSEWriterEmitsIDLine(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newSSEEventWriter(rec)
	w.EnqueueStreamFrame("1726483200000-37", "", `{"token":"a"}`)
	w.Close()
	w.WriteUntilClosed(0)
	want := "id: 1726483200000-37\ndata: {\"token\":\"a\"}\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestSSEWriterEmitsIDAndEventLines(t *testing.T) {
	rec := httptest.NewRecorder()
	w := newSSEEventWriter(rec)
	w.EnqueueStreamFrame("1726483200000-37", "meta", `{"execution_id":"e1"}`)
	w.Close()
	w.WriteUntilClosed(0)
	want := "id: 1726483200000-37\nevent: meta\ndata: {\"execution_id\":\"e1\"}\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestSSEWriterOmitsIDLineWhenEmpty(t *testing.T) {
	// 既有帧（如 reset 合成帧）不带游标，不能凭空多出一行 id:。
	rec := httptest.NewRecorder()
	w := newSSEEventWriter(rec)
	w.EnqueueData(`{"done":true}`)
	w.Close()
	w.WriteUntilClosed(0)
	want := "data: {\"done\":true}\n\n"
	if got := rec.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./api/http/handler/ -run TestSSEWriter -v`
Expected: FAIL — `w.EnqueueStreamFrame undefined`

- [ ] **Step 3: 实现**

在 `api/http/handler/sse_writer.go` 中，把 `sseEvent` 结构体改为：

```go
type sseEvent struct {
	// id 是 Redis Stream entry ID，逐字作为 SSE 的 id: 行。不做任何编码——
	// 客户端原样回传即可用作断线续传游标，服务端可直接喂给 XRANGE。
	id      string
	comment string
	name    string
	data    string
}
```

在 `EnqueueComment` 之后追加：

```go
// EnqueueStreamFrame 入队一条带游标的 SSE 帧。name 为空则省略 event: 行。
func (w *sseEventWriter) EnqueueStreamFrame(id, name, data string) bool {
	return w.enqueue(sseEvent{id: id, name: name, data: data})
}
```

把 `write` 改为：

```go
func (w *sseEventWriter) write(ev sseEvent) {
	if ev.comment != "" {
		_, _ = fmt.Fprintf(w.w, ": %s\n\n", ev.comment)
	} else {
		if ev.id != "" {
			_, _ = fmt.Fprintf(w.w, "id: %s\n", ev.id)
		}
		if ev.name != "" {
			_, _ = fmt.Fprintf(w.w, "event: %s\n", ev.name)
		}
		_, _ = fmt.Fprintf(w.w, "data: %s\n\n", ev.data)
	}
	if w.flusher != nil {
		w.flusher.Flush()
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./api/http/handler/ -run TestSSEWriter -v`
Expected: PASS（3 个新用例 + 既有）
Run: `go test ./api/http/handler/ -short`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add api/http/handler/sse_writer.go api/http/handler/sse_writer_test.go
git commit -m "feat(agent): SSE 写出 id: 游标行"
```

---

## Task 11: runner

**Files:**

- Create: `internal/agent/application/agent_stream_runner.go`
- Test: `internal/agent/application/agent_stream_runner_test.go`

**Interfaces:**

- Consumes: `port.AgentStreamStore`、`port.AgentControlBus`、`port.ExecutionLeaseRepo`、`constants.*`
- Produces: `StreamRunnerConfig`、`DefaultStreamRunnerConfig() StreamRunnerConfig`、`(*AgentService).trackRunner`、`(*AgentService).StopAllRunners(ctx)`、`(*AgentService).shutdownRunners()`

- [ ] **Step 1: 写失败测试**

创建 `internal/agent/application/agent_stream_runner_test.go`：

```go
package application

import (
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

func TestViewerRegistryExpiresSilentViewers(t *testing.T) {
	now := time.Unix(0, 0)
	reg := newViewerRegistry(15*time.Second, func() time.Time { return now })
	reg.Seen("a")
	reg.Seen("b")
	if got := reg.Count(); got != 2 {
		t.Fatalf("Count = %d, want 2", got)
	}
	// b 不再心跳：崩溃/断网的订阅者靠自然过期摘除，runner 不查 Redis。
	now = now.Add(16 * time.Second)
	reg.Seen("a")
	if got := reg.Count(); got != 1 {
		t.Fatalf("Count after expiry = %d, want 1", got)
	}
}

func TestViewerRegistryForget(t *testing.T) {
	now := time.Unix(0, 0)
	reg := newViewerRegistry(time.Minute, func() time.Time { return now })
	reg.Seen("a")
	reg.Forget("a")
	if got := reg.Count(); got != 0 {
		t.Fatalf("Count after Forget = %d, want 0", got)
	}
}

func TestSnapshotStreamFrameEncodesPayload(t *testing.T) {
	cases := []struct {
		name    string
		event   string
		payload any
		want    string
	}{
		{
			name:  "token frame wraps token key",
			event: port.StreamEventToken,
			// 形状必须与今天 handler 写进 data: 的 JSON 逐字节一致，
			// 否则前端按字段嗅探会认不出 token（spec §6.2）。
			payload: map[string]string{"token": "x"},
			want:    `{"token":"x"}`,
		},
		{
			name:    "meta frame carries generation as number",
			event:   port.StreamEventMeta,
			payload: map[string]any{"execution_id": "e1", "generation": 2},
			want:    `{"execution_id":"e1","generation":2}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := snapshotStreamFrame(tc.event, tc.payload)
			if got.Event != tc.event {
				t.Errorf("Event = %q, want %q", got.Event, tc.event)
			}
			if got.Payload != tc.want {
				t.Errorf("Payload = %q, want %q", got.Payload, tc.want)
			}
		})
	}
}

func TestSnapshotStreamFrameFallsBackOnUnmarshalablePayload(t *testing.T) {
	// 不可序列化的载荷降级为空对象而不是 panic：一条坏帧不该带走整个 run。
	got := snapshotStreamFrame(port.StreamEventToken, make(chan int))
	if got.Payload != "{}" {
		t.Fatalf("Payload = %q, want \"{}\"", got.Payload)
	}
}
```

> 说明：断言必须落在生产函数 `snapshotStreamFrame` 上，而不是复述测试自身 fixture 的字段——后者不断言任何生产行为。第二个用例覆盖 marshal 失败路径，这是该函数唯一的错误分支。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/application/ -run 'TestViewerRegistry' -v`
Expected: FAIL — `undefined: newViewerRegistry`

- [ ] **Step 3: 实现**

创建 `internal/agent/application/agent_stream_runner.go`：

```go
// Package application — agent_stream_runner.go.
//
// runner 是唯一持有租约、在跑 run 的 goroutine。它的生命周期由租约、控制通道
// 与孤儿超时决定，与任何 HTTP 请求无关——这是「断线续传」成立的根因（spec §4.1）。
package application

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// StreamRunnerConfig 是 runner 的可注入时长与容量。做成可注入而非直接读包级
// 常量，是因为跨实例测试需要把 2 分钟量级的超时压到毫秒级（spec D5），
// 同时满足「行为数字禁止内联」的常量外置规则。
type StreamRunnerConfig struct {
	LeaseTTL      time.Duration
	LeaseRenew    time.Duration
	ViewerTimeout time.Duration
	OrphanTimeout time.Duration
	StreamTTL     time.Duration
	StreamMaxLen  int64
}

// DefaultStreamRunnerConfig 返回取自 pkg/constants 的默认配置。
func DefaultStreamRunnerConfig() StreamRunnerConfig {
	return StreamRunnerConfig{
		LeaseTTL:      constants.AgentExecutionLeaseTTL,
		LeaseRenew:    constants.AgentExecutionLeaseRenewInterval,
		ViewerTimeout: constants.AgentViewerTimeout,
		OrphanTimeout: constants.AgentExecutionOrphanTimeout,
		StreamTTL:     constants.AgentStreamTTL,
		StreamMaxLen:  constants.AgentStreamMaxLen,
	}
}

// viewerRegistry 跟踪订阅当前执行的 SSE 连接。
//
// viewerID 由订阅侧服务端为每条 SSE 连接生成，不接受客户端提供——客户端可控
// 的 ID 能被伪造，用同一 ID 反复续期即可永久压制孤儿计时（spec §7.1）。
type viewerRegistry struct {
	mu      sync.Mutex
	viewers map[string]time.Time
	timeout time.Duration
	now     func() time.Time
}

func newViewerRegistry(timeout time.Duration, now func() time.Time) *viewerRegistry {
	return &viewerRegistry{viewers: make(map[string]time.Time), timeout: timeout, now: now}
}

// Seen 记一次心跳。
func (r *viewerRegistry) Seen(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.viewers[id] = r.now()
}

// Forget 注销一个 viewer（SSE 连接断开）。
func (r *viewerRegistry) Forget(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.viewers, id)
}

// Count 返回未超时的 viewer 数，顺带摘除超时的。
func (r *viewerRegistry) Count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	for id, seen := range r.viewers {
		if now.Sub(seen) > r.timeout {
			delete(r.viewers, id)
		}
	}
	return len(r.viewers)
}

// localRunner 是本进程正在跑的流式执行，供进程关闭时统一取消。
type localRunner struct {
	executionID string
	generation  int
	cancel      context.CancelFunc
	done        chan struct{}
	closeOnce   sync.Once
}

// finish 幂等关闭 done。
func (r *localRunner) finish() {
	r.closeOnce.Do(func() { close(r.done) })
}

// runnerSet 是本进程的 runner 集合。它只用于关闭与孤儿计时，**不用于互斥**——
// 互斥来自 PG 上的 generation CAS，进程内状态回答不了「另一个 pod 在跑吗」
// （spec D2）。
type runnerSet struct {
	mu   sync.Mutex
	runs map[string]*localRunner
	wg   sync.WaitGroup
}

func newRunnerSet() *runnerSet {
	return &runnerSet{runs: make(map[string]*localRunner)}
}

// localRunnerSet 返回服务当前的 runner 集合；deps 未装配时为 nil。
func (s *AgentService) localRunnerSet() *runnerSet {
	if s.deps.StreamRunnerSet == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.deps.StreamRunnerSet == nil {
			s.deps.StreamRunnerSet = newRunnerSet()
		}
	}
	return s.deps.StreamRunnerSet
}

// track 登记一个 runner 并返回其句柄。
func (rs *runnerSet) track(executionID string, generation int, cancel context.CancelFunc) *localRunner {
	r := &localRunner{
		executionID: executionID,
		generation:  generation,
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	rs.mu.Lock()
	rs.runs[executionID] = r
	rs.mu.Unlock()
	rs.wg.Add(1)
	return r
}

// untrack 注销并等待该 runner 退出。
func (rs *runnerSet) untrack(r *localRunner) {
	r.finish()
	rs.mu.Lock()
	if cur, ok := rs.runs[r.executionID]; ok && cur == r {
		delete(rs.runs, r.executionID)
	}
	rs.mu.Unlock()
	rs.wg.Done()
}

// CancelAll 取消本进程所有在跑的 run 并等待它们退出（pod SIGTERM 路径）。
// run 中断时 upper 层保留 checkpoint，新 pod 可续（spec §7.4）。
func (rs *runnerSet) CancelAll() {
	rs.mu.Lock()
	for _, r := range rs.runs {
		r.cancel()
	}
	rs.mu.Unlock()
	rs.wg.Wait()
}

// ShutdownStreamRunners 取消本进程所有在跑的流式执行并等待退出。
// 未装配流依赖时为 no-op。
func (s *AgentService) ShutdownStreamRunners() {
	if s.deps.StreamRunnerSet == nil {
		return
	}
	s.deps.StreamRunnerSet.CancelAll()
}

// snapshotStreamFrame 把一条事件序列化成流条目载荷（spec §6.2：d 装的就是
// 今天原封不动塞进 data: 的那个 JSON，wire 格式零转换）。
func snapshotStreamFrame(event string, payload any) port.StreamEntry {
	encoded, err := json.Marshal(payload)
	if err != nil {
		// 调用方传的都是本包构造的 map/struct，marshal 不会失败；真失败也
		// 只能降级为空对象而不是 panic 掉整个 run。
		encoded = []byte("{}")
	}
	return port.StreamEntry{Event: event, Payload: string(encoded)}
}

// logStreamWriteFailure 记录写流失败。流是尽力而为的显示缓冲，写失败**不阻断
// run**（spec §10「Redis 不可用 → run 本身不受影响」），但必须显式记录。
func (s *AgentService) logStreamWriteFailure(executionID string, err error) {
	s.deps.Logger.Warn("agent stream: write failed",
		zap.String("execution_id", executionID), zap.Error(err))
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent/application/ -run 'TestViewerRegistry|TestStreamFrame' -v`
Expected: PASS（3 个）

- [ ] **Step 5: 提交**

```bash
git add internal/agent/application/agent_stream_runner.go internal/agent/application/agent_stream_runner_test.go
git commit -m "feat(agent): runner 基元（viewer 注册表、本地 runner 集合、流帧序列化）"
```

---

## Task 12: 订阅侧

**Files:**

- Create: `internal/agent/application/agent_stream_subscriber.go`
- Test: `internal/agent/application/agent_stream_subscriber_test.go`

**Interfaces:**

- Consumes: Task 7 纯函数、Task 4 端口、`stream.StreamEvent*` 常量
- Produces: `StreamSubscriptionConfig`、`DefaultStreamSubscriptionConfig()`、`SubscriptionDeps`、`NewExecutionSubscription(...)`、`(*ExecutionSubscription).Frames()`、`(*ExecutionSubscription).Close()`、`NewViewerID()`

- [ ] **Step 1: 写失败测试**

创建 `internal/agent/application/agent_stream_subscriber_test.go`：

```go
package application

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// fakeStreamStore 是订阅侧测试用的内存流。
type fakeStreamStore struct {
	mu      sync.Mutex
	entries []port.StreamEntry
	next    int
	block   time.Duration
}

func (f *fakeStreamStore) Append(
	_ context.Context, _ string, _ int, e port.StreamEntry,
) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	id := "1726483200000-" + itoa(f.next)
	entry := e
	entry.ID = id
	f.entries = append(f.entries, entry)
	return id, nil
}

func (f *fakeStreamStore) Replay(_ context.Context, _ string, _ int) ([]port.StreamEntry, error) {
	return f.ReplayAfter(context.Background(), "", 0, "")
}

func (f *fakeStreamStore) ReplayAfter(_ context.Context, _ string, _ int, afterID string) ([]port.StreamEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.afterLocked(afterID), nil
}

func (f *fakeStreamStore) FirstID(_ context.Context, _ string, _ int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.entries) == 0 {
		return "", nil
	}
	return f.entries[0].ID, nil
}

func (f *fakeStreamStore) Tail(
	ctx context.Context, _ string, _ int, afterID string, _ int,
) ([]port.StreamEntry, error) {
	f.mu.Lock()
	entries := f.afterLocked(afterID)
	f.mu.Unlock()
	if len(entries) > 0 {
		return entries, nil
	}
	// 无新条目：按配置阻塞后返回空，模拟 XREAD BLOCK 到期。
	select {
	case <-time.After(f.block):
	case <-ctx.Done():
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.afterLocked(afterID), nil
}

func (f *fakeStreamStore) RefreshTTL(context.Context, string, int) error { return nil }
func (f *fakeStreamStore) Delete(context.Context, string, int) error     { return nil }

func (f *fakeStreamStore) afterLocked(afterID string) []port.StreamEntry {
	if afterID == "" {
		return append([]port.StreamEntry(nil), f.entries...)
	}
	out := make([]port.StreamEntry, 0, len(f.entries))
	for _, e := range f.entries {
		if CompareStreamID(e.ID, afterID) > 0 {
			out = append(out, e)
		}
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

type fakeControlBus struct {
	mu    sync.Mutex
	msgs  []port.ControlMessage
	subs  []chan port.ControlMessage
}

func (f *fakeControlBus) PublishStop(context.Context, string) error { return nil }
func (f *fakeControlBus) PublishViewer(_ context.Context, _, viewerID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.msgs = append(f.msgs, port.ControlMessage{Type: port.ControlMessageViewer, ViewerID: viewerID})
	return nil
}

func (f *fakeControlBus) Subscribe(context.Context, string) (<-chan port.ControlMessage, func(), error) {
	ch := make(chan port.ControlMessage, 16)
	f.mu.Lock()
	f.subs = append(f.subs, ch)
	f.mu.Unlock()
	return ch, func() {}, nil
}

func (f *fakeControlBus) viewerCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.msgs)
}

func collectFrames(t *testing.T, sub *ExecutionSubscription, stop func([]StreamFrame) bool) []StreamFrame {
	t.Helper()
	var got []StreamFrame
	deadline := time.After(3 * time.Second)
	for {
		select {
		case f, ok := <-sub.Frames():
			if !ok {
				return got
			}
			got = append(got, f)
			if stop != nil && stop(got) {
				return got
			}
		case <-deadline:
			return got
		}
	}
}

func TestSubscriptionReplaysThenStopsAtTerminal(t *testing.T) {
	store := &fakeStreamStore{block: 5 * time.Millisecond}
	ctx := context.Background()
	_, _ = store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventMeta, Payload: `{"execution_id":"e1","generation":1}`})
	_, _ = store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventToken, Payload: `{"token":"a"}`})
	_, _ = store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventDone, Payload: `{"done":true}`})

	sub := NewExecutionSubscription(ExecutionSubscriptionDeps{
		Stream: store, Control: &fakeControlBus(), ExecutionID: "e1", Generation: 1,
		Plan: StreamPlan{Generation: 1},
		Cfg:  StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour, ReadBlock: 5},
	})
	defer sub.Close()

	got := collectFrames(t, sub, func(fs []StreamFrame) bool {
		return len(fs) > 0 && fs[len(fs)-1].Event == port.StreamEventDone
	})
	if len(got) != 3 {
		t.Fatalf("frames = %+v, want 3", got)
	}
	if got[0].ID == "" || got[2].ID == "" {
		t.Fatalf("frames must carry stream cursor ids: %+v", got)
	}
	if got[0].Event != port.StreamEventMeta {
		t.Fatalf("frame[0].Event = %q, want meta", got[0].Event)
	}
}

func TestSubscriptionReplaysAfterCursor(t *testing.T) {
	store := &fakeStreamStore{block: 5 * time.Millisecond}
	ctx := context.Background()
	first, _ := store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventToken, Payload: `{"token":"a"}`})
	_, _ = store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventToken, Payload: `{"token":"b"}`})
	_, _ = store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventDone, Payload: `{"done":true}`})

	sub := NewExecutionSubscription(ExecutionSubscriptionDeps{
		Stream: store, Control: &fakeControlBus(), ExecutionID: "e1", Generation: 1,
		Plan: StreamPlan{Generation: 1, AfterID: first},
		Cfg:  StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour, ReadBlock: 5},
	})
	defer sub.Close()

	got := collectFrames(t, sub, func(fs []StreamFrame) bool {
		return len(fs) > 0 && fs[len(fs)-1].Event == port.StreamEventDone
	})
	if len(got) != 2 || got[0].Payload != `{"token":"b"}` {
		t.Fatalf("frames = %+v, want incremental replay starting at b", got)
	}
}

func TestSubscriptionEmitsResetFrameFirst(t *testing.T) {
	store := &fakeStreamStore{block: 5 * time.Millisecond}
	_, _ = store.Append(context.Background(), "e1", 2, port.StreamEntry{Event: port.StreamEventDone, Payload: `{"done":true}`})

	sub := NewExecutionSubscription(ExecutionSubscriptionDeps{
		Stream: store, Control: &fakeControlBus(), ExecutionID: "e1", Generation: 2,
		Plan: StreamPlan{Generation: 2, Reset: true, ResetReason: ResetReasonGenerationChanged},
		Cfg:  StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour, ReadBlock: 5},
	})
	defer sub.Close()

	got := collectFrames(t, sub, func(fs []StreamFrame) bool {
		return len(fs) > 0 && fs[len(fs)-1].Event == port.StreamEventDone
	})
	if len(got) < 2 {
		t.Fatalf("frames = %+v, want reset then done", got)
	}
	if got[0].Event != port.StreamEventReset {
		t.Fatalf("frame[0].Event = %q, want reset", got[0].Event)
	}
	// reset 是订阅侧合成的，不来自流，因此没有游标——它不该污染客户端游标。
	if got[0].ID != "" {
		t.Fatalf("reset frame must not carry a cursor, got %q", got[0].ID)
	}
}

func TestSubscriptionNewViewerIDIsUnique(t *testing.T) {
	// viewerID 由服务端为每条 SSE 连接生成；重复 ID 会让两个连接互相续期，
	// 使孤儿计时永远不会触发。
	seen := make(map[string]bool)
	for i := 0; i < 64; i++ {
		id := NewViewerID()
		if id == "" {
			t.Fatal("NewViewerID returned empty")
		}
		if seen[id] {
			t.Fatalf("duplicate viewer id %q", id)
		}
		seen[id] = true
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/application/ -run TestSubscription -v`
Expected: FAIL — `undefined: NewExecutionSubscription`

- [ ] **Step 3: 实现**

创建 `internal/agent/application/agent_stream_subscriber.go`：

```go
// Package application — agent_stream_subscriber.go.
//
// 订阅侧：把「流」翻译成「SSE 帧」。它只读不写 run，因此任意 pod 的任意 HTTP
// 请求都能成为订阅者，且不持有者与持有者走完全相同的代码路径（spec D1 的收益）。
package application

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/google/uuid"
)

// StreamSubscriptionConfig 是可注入的订阅侧时长。默认取自 pkg/constants。
type StreamSubscriptionConfig struct {
	Heartbeat time.Duration
	IdleExit  time.Duration
	// ReadBlock 是单次 Tail 的阻塞毫秒数；0 表示用 store 默认。
	ReadBlock int
}

// DefaultStreamSubscriptionConfig 返回默认订阅配置。
func DefaultStreamSubscriptionConfig() StreamSubscriptionConfig {
	return StreamSubscriptionConfig{
		Heartbeat: constants.SSEHeartbeatInterval,
		IdleExit:  constants.AgentStreamIdleExit,
	}
}

// StreamFrame 是订阅侧要写给客户端的一帧。Comment 非空即 SSE 注释帧。
type StreamFrame struct {
	ID      string
	Event   string
	Data    string
	Comment string
}

// NewViewerID 生成一条 SSE 连接的唯一 viewer 标识。由服务端生成，不接受客户端
// 提供——客户端可控的 ID 可以被伪造，用同一 ID 反复续期就能永久压制孤儿计时
// （spec §7.1）。
func NewViewerID() string {
	return uuid.Must(uuid.NewV7()).String()
}

// ExecutionSubscriptionDeps 是订阅的依赖。
type ExecutionSubscriptionDeps struct {
	Stream      port.AgentStreamStore
	Control     port.AgentControlBus
	Lease       port.ExecutionLeaseRepo
	TenantID    string
	ExecutionID string
	Generation  int
	Plan        StreamPlan
	Cfg         StreamSubscriptionConfig
	ViewerID    string
}

// ExecutionSubscription 是一次订阅的句柄。handler 只消费 Frames()，
// 不接触 StreamStore / ControlBus——回放、跟流、心跳、看门狗全在应用层，
// transport 退化成纯写出。
type ExecutionSubscription struct {
	frames   chan StreamFrame
	cancel   context.CancelFunc
	done     chan struct{}
	closeOne sync.Once
}

// Frames 返回帧通道；订阅结束（终态帧、客户端断开、空闲看门狗触发）后关闭。
func (sub *ExecutionSubscription) Frames() <-chan StreamFrame { return sub.frames }

// Close 停止订阅并等待退出。幂等。
func (sub *ExecutionSubscription) Close() {
	sub.closeOne.Do(func() {
		sub.cancel()
		<-sub.done
	})
}

// NewExecutionSubscription 启动订阅。调用方必须在返回后用 defer Close()。
func NewExecutionSubscription(deps ExecutionSubscriptionDeps) *ExecutionSubscription {
	cfg := deps.Cfg
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = constants.SSEHeartbeatInterval
	}
	if cfg.IdleExit <= 0 {
		cfg.IdleExit = constants.AgentStreamIdleExit
	}
	viewerID := deps.ViewerID
	if viewerID == "" {
		viewerID = NewViewerID()
	}

	ctx, cancel := context.WithCancel(context.Background())
	sub := &ExecutionSubscription{
		frames: make(chan StreamFrame, 128),
		cancel: cancel,
		done:   make(chan struct{}),
	}
	go func() {
		defer close(sub.done)
		defer close(sub.frames)
		runSubscription(ctx, deps, cfg, viewerID, sub.frames)
	}()
	return sub
}

func runSubscription(
	ctx context.Context, deps ExecutionSubscriptionDeps, cfg StreamSubscriptionConfig,
	viewerID string, out chan<- StreamFrame,
) {
	// reset 由订阅侧合成，不来自流，因此没有游标（spec §6.6）。
	if deps.Plan.Reset {
		if !emit(out, StreamFrame{Event: port.StreamEventReset, Data: resetPayload(deps.Plan)}) {
			return
		}
	}

	cursor, terminal, ok := replay(ctx, deps, out)
	if !ok || terminal {
		return
	}

	// 心跳与 viewer 上报搭同一个 tick：零额外连接、零定时器（spec §7.1）。
	// 同时兼作空闲看门狗：runner 被 SIGKILL 时不会有终态帧，靠这里兜底退出，
	// 避免前端无限 spinner。
	tick := time.NewTicker(cfg.Heartbeat)
	defer tick.Stop()
	idleSince := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if !emit(out, StreamFrame{Comment: "heartbeat"}) {
				return
			}
			_ = deps.Control.PublishViewer(ctx, deps.ExecutionID, viewerID)
			if idleExceeded(ctx, deps, idleSince, cfg) {
				// 租约已失效且流持续无新条目：runner 不会再有产出。写一条
				// 错误终态帧让前端收敛，而不是让它无限等待。
				emit(out, StreamFrame{
					Event: port.StreamEventError,
					Data:  `{"error":"执行已中断，请重新发起"}`,
				})
				return
			}
		default:
		}

		entries, err := deps.Stream.Tail(ctx, deps.ExecutionID, deps.Generation, cursor, cfg.ReadBlock)
		if err != nil {
			// Redis 掉线：订阅侧降级，不阻塞 run。发一条错误终态帧后退出——
			// 持续重试只会在前端留下一个不再前进的流。
			emit(out, StreamFrame{
				Event: port.StreamEventError,
				Data:  `{"error":"流式连接已中断，请重试"}`,
			})
			return
		}
		if len(entries) == 0 {
			continue
		}
		for _, e := range entries {
			cursor = e.ID
			idleSince = time.Now()
			if !emit(out, StreamFrame{ID: e.ID, Event: e.Event, Data: e.Payload}) {
				return
			}
			if port.IsTerminalStreamEvent(e.Event) {
				return
			}
		}
	}
}

// replay 全量/增量回放并返回新游标。第二个返回值表示是否已遇到终态帧。
func replay(
	ctx context.Context, deps ExecutionSubscriptionDeps, out chan<- StreamFrame,
) (cursor string, terminal bool, ok bool) {
	entries, err := deps.Stream.ReplayAfter(ctx, deps.ExecutionID, deps.Generation, deps.Plan.AfterID)
	if err != nil {
		emit(out, StreamFrame{Event: port.StreamEventError, Data: `{"error":"流式连接已中断，请重试"}`})
		return "", false, false
	}
	// 裁剪缺口检测：游标早于流现存最老条目说明增量回放会给出半截答案，
	// 必须 reset 重来，而不是把带洞的文本交给用户（spec §6.6）。
	if len(entries) > 0 && HasReplayGap(deps.Plan.AfterID, entries[0].ID) {
		if !emit(out, StreamFrame{Event: port.StreamEventReset, Data: resetPayload(StreamPlan{
			Generation: deps.Generation, Reset: true, ResetReason: ResetReasonStreamLost,
		})}) {
			return "", false, false
		}
		entries, err = deps.Stream.Replay(ctx, deps.ExecutionID, deps.Generation)
		if err != nil {
			emit(out, StreamFrame{Event: port.StreamEventError, Data: `{"error":"流式连接已中断，请重试"}`})
			return "", false, false
		}
	}
	for _, e := range entries {
		cursor = e.ID
		if !emit(out, StreamFrame{ID: e.ID, Event: e.Event, Data: e.Payload}) {
			return cursor, false, false
		}
		if port.IsTerminalStreamEvent(e.Event) {
			return cursor, true, true
		}
	}
	return cursor, false, true
}

// idleExceeded 判定空闲看门狗是否应触发：租约已失效且流持续无新条目超过
// IdleExit。租约仍有效时无论多久都不退出——那是正常的长时间 LLM 调用。
func idleExceeded(
	ctx context.Context, deps ExecutionSubscriptionDeps, idleSince time.Time, cfg StreamSubscriptionConfig,
) bool {
	if time.Since(idleSince) < cfg.IdleExit {
		return false
	}
	if deps.Lease == nil {
		return false
	}
	status, err := deps.Lease.LeaseStatus(ctx, deps.TenantID, deps.ExecutionID)
	if err != nil {
		// 查询失败时 fail open（不退出）：宁可多等，也不要把一个仍在跑的
		// 执行误判为中断而关闭用户的流。
		return false
	}
	return !status.Active
}

func resetPayload(plan StreamPlan) string {
	payload, err := json.Marshal(map[string]any{
		"reset":      true,
		"generation": plan.Generation,
		"reason":     plan.ResetReason,
	})
	if err != nil {
		return `{"reset":true}`
	}
	return string(payload)
}

// emit 写出一帧。缓冲满时阻塞——这正是我们想要的背压边界：慢客户端只卡住
// 它自己的订阅 goroutine，不再通过内存 channel 一路顶到 LLM 读循环（spec §4.4）。
// ctx 取消时返回 false。
func emit(out chan<- StreamFrame, frame StreamFrame) bool {
	select {
	case out <- frame:
		return true
	default:
	}
	// 缓冲已满：转为可取消的阻塞写。
	select {
	case out <- frame:
		return true
	case <-time.After(streamEmitTimeout):
		return false
	}
}

// streamEmitTimeout 是单帧写出的上限。它必须存在：调用方在 ctx 取消后仍可能
// 阻塞在这里，而 Close() 会等待订阅退出（会死锁）。
const streamEmitTimeout = 30 * time.Second
```

> 注意：`emit` 不接收 ctx（避免在每个调用点透传），代价是 Close 最坏等 30s。若实现时发现测试超时，把 `emit` 改为接收 ctx 并用 `select { case out <- frame: case <-ctx.Done(): }`——两种写法都要保证 `Close()` 有界返回。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/agent/application/ -run TestSubscription -v`
Expected: PASS（5 个）
Run: `go test -race ./internal/agent/application/ -run TestSubscription`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/agent/application/agent_stream_subscriber.go internal/agent/application/agent_stream_subscriber_test.go
git commit -m "feat(agent): 订阅侧（回放/跟流/心跳/空闲看门狗）"
```

---

## Task 13: `ExecuteStream` 重写与依赖注入

**Files:**

- Modify: `internal/agent/application/agent_execution.go:125-137,336-392`
- Modify: `internal/agent/application/agent_service.go:107-119`
- Test: `internal/agent/application/agent_stream_execute_test.go`

**Interfaces:**

- Consumes: Task 4 端口、Task 11 runner、Task 12 订阅侧
- Produces:
  - `ExecMeta.Generation int`、`ExecMeta.LastEventID string`
  - `(*AgentService).ExecuteStream(ctx, agentID string, req ExecRequest, meta ExecMeta) (*StreamHandle, error)`
  - `(*AgentService).OpenStreamSubscription(ctx, agentID string, req ExecRequest, meta ExecMeta, cfg StreamSubscriptionConfig) (*ExecutionSubscription, error)`
  - `StreamHandle{ExecutionID string; Generation int}`
  - `AgentServiceDeps.StreamStore port.AgentStreamStore`、`.ControlBus port.AgentControlBus`、`.LeaseRepo port.ExecutionLeaseRepo`、`.StreamRunnerCfg StreamRunnerConfig`

- [ ] **Step 1: 写失败测试**

创建 `internal/agent/application/agent_stream_execute_test.go`：

```go
package application

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// fakeLeaseRepo 是租约端口的内存替身，用于验证三路分支的判定。
type fakeLeaseRepo struct {
	status    port.LeaseStatus
	claimGen  int
	claimErr  error
	stampGen  int
	stampErr  error
	claimCall int
}

func (f *fakeLeaseRepo) StampLease(context.Context, string, string, time.Duration) (int, error) {
	return f.stampGen, f.stampErr
}

func (f *fakeLeaseRepo) ClaimLease(context.Context, string, string, int, time.Duration) (int, error) {
	f.claimCall++
	if f.claimErr != nil {
		return 0, f.claimErr
	}
	return f.claimGen, nil
}

func (f *fakeLeaseRepo) RenewLease(context.Context, string, string, int, time.Duration) error {
	return nil
}

func (f *fakeLeaseRepo) ReleaseLease(context.Context, string, string, int) error { return nil }

func (f *fakeLeaseRepo) LeaseStatus(context.Context, string, string) (port.LeaseStatus, error) {
	return f.status, nil
}

func TestOpenStreamSubscriptionTailsWhenLeaseActive(t *testing.T) {
	store := &fakeStreamStore{block: 5 * time.Millisecond}
	lease := &fakeLeaseRepo{status: port.LeaseStatus{Generation: 3, Active: true}}
	_, _ = store.Append(context.Background(), "e1", 3, port.StreamEntry{
		Event: port.StreamEventDone, Payload: `{"done":true}`})

	svc := NewAgentService(AgentServiceDeps{})
	svc.deps.StreamStore = store
	svc.deps.ControlBus = &fakeControlBus{}
	svc.deps.LeaseRepo = lease

	sub, err := svc.OpenStreamSubscription(context.Background(), "agent-1", ExecRequest{},
		ExecMeta{TenantID: "t1", ExecutionID: "e1", Generation: 3},
		StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour, ReadBlock: 5})
	if err != nil {
		t.Fatalf("OpenStreamSubscription: %v", err)
	}
	defer sub.Close()

	// 租约有效时**不得**发起抢占：抢占会把正在跑的 runner fence 掉。
	if lease.claimCall != 0 {
		t.Fatalf("ClaimLease called %d times on an active lease, want 0", lease.claimCall)
	}
}

func TestOpenStreamSubscriptionRequiresCheckpointForResume(t *testing.T) {
	store := &fakeStreamStore{block: 5 * time.Millisecond}
	svc := NewAgentService(AgentServiceDeps{})
	svc.deps.StreamStore = store
	svc.deps.ControlBus = &fakeControlBus{}
	svc.deps.LeaseRepo = &fakeLeaseRepo{}
	svc.deps.CheckpointStore = &noopCheckpointStore{}

	// 带 execution_id 但租户限定查询查不到 → 404 语义，且不区分
	// 「不存在」与「不属于你」（spec D4）。
	if _, err := svc.OpenStreamSubscription(context.Background(), "agent-1", ExecRequest{},
		ExecMeta{TenantID: "t1", ExecutionID: "ghost"},
		StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour}); err == nil {
		t.Fatal("expected error for execution_id without a tenant-scoped checkpoint")
	}
}
```

> `noopCheckpointStore` **已在** `internal/agent/application/agent_service_extra_test.go:347` 定义，其 `GetLatest` 返回 `(nil, nil)`——正是本用例需要的「查不到」语义。**不要重复声明**，直接复用；同包重复声明会编译失败。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/application/ -run TestOpenStreamSubscription -v`
Expected: FAIL — `svc.deps.StreamStore undefined`

- [ ] **Step 3: 扩展 deps 与 ExecMeta**

在 `internal/agent/application/agent_service.go` 的 `AgentServiceDeps` 末尾（`ActorNameResolver` 之后）追加：

```go
	// StreamStore / ControlBus / LeaseRepo 是断线续传的三个依赖。
	// 流式路径未装配时 fail closed（见 ExecuteStream）——静默退化成「不可续传」
	// 会让线上表现与本设计不符，属必须暴露的失败。
	StreamStore port.AgentStreamStore
	ControlBus  port.AgentControlBus
	LeaseRepo   port.ExecutionLeaseRepo
	// StreamRunnerCfg 是可注入的 runner 时长；零值走 DefaultStreamRunnerConfig。
	StreamRunnerCfg StreamRunnerConfig
	// StreamRunnerSet 是进程内 runner 集合，供关闭时统一取消。内部惰性初始化。
	StreamRunnerSet *runnerSet
	// StreamRunFn 是 runner 实际执行的那次调用。生产为 nil，走 executeStreamRun；
	// 跨实例测试（Task 17）注入假 LLM 以断言「LLM 调用次数 == 1」——这是 spec
	// §11.2 那条北极星断言能在本地跑起来的前提。
	StreamRunFn func(context.Context, string, ExecRequest, ExecMeta, func(string)) (*domain.AgentResult, int, error)
```

在 `AgentService` 结构体中追加互斥锁：

```go
type AgentService struct {
	deps AgentServiceDeps
	mu   sync.Mutex // 保护 StreamRunnerSet 的惰性初始化
}
```

并在 import 中加入 `"sync"`。

在 `agent_execution.go` 的 `ExecMeta` 中，`ExecutionID` 之后追加：

```go
	// Generation 是客户端上一次订阅时看到的分代（缺省 0 = 客户端未提供），
	// 用于判定是否需要 reset（spec §6.4）。
	Generation int
	// LastEventID 是客户端内存持有的游标。仅存在于会话内，不跨页面重载——
	// F5 后前端不发它，服务端因此走全量回放（spec §6.5）。
	LastEventID string
```

- [ ] **Step 4: 重写 ExecuteStream**

用以下内容替换 `agent_execution.go` 中的 `ExecuteStream`（原 `:336-392`）：

```go
// StreamHandle 是一次流式执行的订阅坐标。它不返回 cancel——run 的取消由租约
// 续租失败、控制通道 stop 消息与孤儿超时三者驱动，订阅侧只读不写（spec §4.2）。
type StreamHandle struct {
	ExecutionID string
	Generation  int
}

// ExecuteStream 为一条 SSE 请求确定订阅坐标：NEW 建流开跑、租约有效则 TAIL、
// 租约失效则抢占（CAS 胜者跑，败者 TAIL）。
//
// 全部帧（meta/token/delegate/done/error/approval_required）由 runner 写入流，
// 本方法不再接收 tokenCb——执行侧不再直接写 socket（spec D1）。
func (s *AgentService) ExecuteStream(
	ctx context.Context, agentID string, req ExecRequest, meta ExecMeta,
) (*StreamHandle, error) {
	if s.deps.StreamStore == nil || s.deps.ControlBus == nil || s.deps.LeaseRepo == nil {
		return nil, fmt.Errorf("agent: execute stream: stream dependencies not configured")
	}
	cfg := s.streamRunnerConfig()
	executionID := executionIDOrNew(meta.ExecutionID)
	newExecution := meta.ExecutionID == ""

	if !newExecution {
		// D4：订阅/续跑前必须做一次租户限定的 checkpoint 查询。查不到 → 404
		// 语义，且不区分「不存在」与「不属于你」，关闭 existence oracle。
		cp, err := s.deps.CheckpointStore.GetLatest(ctx, meta.TenantID, executionID)
		if err != nil {
			return nil, fmt.Errorf("agent: execute stream: load checkpoint: %w", err)
		}
		if cp == nil {
			return nil, ErrNotFound
		}
	}

	generation, startRunner, err := s.settleGeneration(ctx, agentID, req, meta, executionID, newExecution, cfg)
	if err != nil {
		return nil, err
	}
	if !startRunner {
		// 别的 runner 持有租约（本 pod 或别的 pod 都一样）：本请求只订阅。
		return &StreamHandle{ExecutionID: executionID, Generation: generation}, nil
	}
	if err := s.launchStreamRunner(ctx, agentID, req, meta, executionID, generation, cfg); err != nil {
		return nil, err
	}
	return &StreamHandle{ExecutionID: executionID, Generation: generation}, nil
}

// settleGeneration 决定本次请求是「读现有 generation」还是「抢占后开新 generation」。
// 返回 startRunner=false 表示已有 runner 在跑，本请求只订阅。
func (s *AgentService) settleGeneration(
	ctx context.Context, agentID string, req ExecRequest, meta ExecMeta,
	executionID string, newExecution bool, cfg StreamRunnerConfig,
) (generation int, startRunner bool, err error) {
	if newExecution {
		gen, err := s.deps.LeaseRepo.StampLease(ctx, meta.TenantID, executionID, cfg.LeaseTTL)
		if err != nil {
			return 0, false, fmt.Errorf("agent: execute stream: stamp lease: %w", err)
		}
		return gen, true, nil
	}

	status, err := s.deps.LeaseRepo.LeaseStatus(ctx, meta.TenantID, executionID)
	if err != nil {
		return 0, false, fmt.Errorf("agent: execute stream: lease status: %w", err)
	}
	if status.Active {
		return status.Generation, false, nil
	}
	// 租约失效：CAS 抢占。两个实例同时走到这里，恰好一个拿到新 generation，
	// 另一个收到 ErrLeaseConflict 并转入 TAIL 读赢家的流（spec §7.2）。
	claimed, err := s.deps.LeaseRepo.ClaimLease(ctx, meta.TenantID, executionID, status.Generation, cfg.LeaseTTL)
	if err == nil {
		return claimed, true, nil
	}
	if !errors.Is(err, port.ErrLeaseConflict) {
		return 0, false, fmt.Errorf("agent: execute stream: claim lease: %w", err)
	}
	// 没抢到：重读赢家的 generation 并订阅它。
	latest, err := s.deps.LeaseRepo.LeaseStatus(ctx, meta.TenantID, executionID)
	if err != nil {
		return 0, false, fmt.Errorf("agent: execute stream: lease status after conflict: %w", err)
	}
	return latest.Generation, false, nil
}

func (s *AgentService) streamRunnerConfig() StreamRunnerConfig {
	cfg := s.deps.StreamRunnerCfg
	if cfg.LeaseTTL <= 0 {
		return DefaultStreamRunnerConfig()
	}
	return cfg
}
```

- [ ] **Step 5: 加 `launchStreamRunner` 与 `OpenStreamSubscription`**

创建 `internal/agent/application/agent_stream_execute.go`：

```go
// Package application — agent_stream_execute.go.
//
// runner 的启动与订阅的装配：把 Task 11 的基元与 Task 13 的 ExecuteStream 缝在一起。
package application

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// launchStreamRunner 启动一个持有租约的 runner。控制通道订阅在 run 启动之前
// 完成——否则会漏掉早到的 viewer 消息，让刚 attach 的执行被误判为孤儿（spec §7.1）。
func (s *AgentService) launchStreamRunner(
	ctx context.Context, agentID string, req ExecRequest, meta ExecMeta,
	executionID string, generation int, cfg StreamRunnerConfig,
) error {
	// runner 的 context 与请求解绑：HTTP 请求断开不得终止 run（spec §4.1）。
	// 这一行是整个改造的核心——今天 handler 在 clientCtx.Done() 时显式 cancel()。
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	msgCh, unsubscribe, err := s.deps.ControlBus.Subscribe(runCtx, executionID)
	if err != nil {
		cancel()
		return fmt.Errorf("agent: execute stream: subscribe control channel: %w", err)
	}
	viewers := newViewerRegistry(cfg.ViewerTimeout, time.Now)
	handle := s.localRunnerSet().track(executionID, generation, cancel)

	go func() {
		defer unsubscribe()
		defer cancel()
		defer handle.finish()
		defer s.localRunnerSet().untrack(handle)
		s.runStreamRunner(runCtx, agentID, req, meta, executionID, generation, cfg, viewers, msgCh)
	}()
	return nil
}

// runStreamRunner 是 runner 的主循环：写 meta 帧 → 跑 run → 写终态帧 → 释放租约。
// 租约续租心跳与控制消息在 run 期间并行消费。
func (s *AgentService) runStreamRunner(
	ctx context.Context, agentID string, req ExecRequest, meta ExecMeta,
	executionID string, generation int, cfg StreamRunnerConfig,
	viewers *viewerRegistry, msgCh <-chan port.ControlMessage,
) {
	// meta 帧必须是流的第一条：订阅侧据此得知 generation，前端据此获得恢复键。
	s.appendStreamEntry(ctx, executionID, generation, port.StreamEntry{
		Event: port.StreamEventMeta,
		Payload: mustJSON(map[string]any{
			"execution_id": executionID,
			"generation":   generation,
		}),
	})

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	// 续租、控制消息、孤儿计时共用一条 tick：三者语义相同（"我还活着" /
	// "你还在看着"），拆成三个 timer 只增加竞态面（spec §6.7）。
	stopHeartbeat := make(chan struct{})
	go s.streamRunnerHeartbeat(runCtx, cancelRun, executionID, generation, cfg, viewers, stopHeartbeat)

	go func() {
		for {
			select {
			case <-runCtx.Done():
				return
			case msg, ok := <-msgCh:
				if !ok {
					return
				}
				switch msg.Type {
				case port.ControlMessageViewer:
					viewers.Seen(msg.ViewerID)
				case port.ControlMessageCancel:
					cancelRun()
					return
				}
			}
		}
	}()

	result, _, runErr := s.executeStreamRun(runCtx, agentID, req, meta, executionID, generation)

	close(stopHeartbeat)
	// 无论成功、报错还是被取消，都写终态帧到流——订阅侧据此收敛，不会无限跟流。
	s.appendStreamTerminal(ctx, executionID, generation, result, runErr)

	if err := s.deps.LeaseRepo.ReleaseLease(ctx, meta.TenantID, executionID, generation); err != nil {
		s.deps.Logger.Warn("agent stream: release lease failed",
			zapString("execution_id", executionID), zapError(err))
	}
}

// streamRunnerHeartbeat 续租并维护 viewer 集合。续租 CAS 失败意味着已被抢占，
// 立刻自取消——僵尸 runner 不得继续烧 token（spec D3）。
func (s *AgentService) streamRunnerHeartbeat(
	ctx context.Context, cancel context.CancelFunc, executionID string, generation int,
	cfg StreamRunnerConfig, viewers *viewerRegistry, stop <-chan struct{},
) {
	interval := cfg.LeaseRenew
	if interval <= 0 {
		interval = cfg.LeaseTTL / 3
	}
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	orphanSince := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			if err := s.deps.LeaseRepo.RenewLease(ctx, s.tenantIDOf(ctx), executionID, generation, cfg.LeaseTTL); err != nil {
				s.deps.Logger.Warn("agent stream: lease renew failed, cancelling run",
					zapString("execution_id", executionID), zapError(err))
				cancel()
				return
			}
			// 搭车刷新流的 TTL，不新增定时器（spec §6.7）。
			if err := s.deps.StreamStore.RefreshTTL(ctx, executionID, generation); err != nil {
				s.deps.Logger.Debug("agent stream: refresh ttl failed",
					zapString("execution_id", executionID), zapError(err))
			}
			if viewers.Count() == 0 {
				if time.Since(orphanSince) >= cfg.OrphanTimeout {
					s.deps.Logger.Info("agent stream: no viewers, cancelling orphan run",
						zapString("execution_id", executionID))
					cancel()
					return
				}
				continue
			}
			orphanSince = time.Now()
		}
	}
}
```

> `zapString` / `zapError` 是 `zap.String` / `zap.Error` 的本地别名（避免在本文档中重复展开 import 行）。实现时直接用 `zap.String` / `zap.Error` 并在文件 import 块加入 `"go.uber.org/zap"`。
>
> `runStreamRunner` 需要 `tenantID` 才能续租。把 `meta` 的 `TenantID` 透传进来即可——`streamRunnerHeartbeat` 应接收 `tenantID string` 参数而不是从 ctx 取（ctx 来自 `context.WithoutCancel`，不带租户上下文）。实现时把 `s.tenantIDOf(ctx)` 改为传入的 `meta.TenantID`。

`executeStreamRun` 是把原 `ExecuteStream` 的准备链与 run 闭包搬过来的函数，签名：

```go
// executeStreamRun 复用 prepareAgentExecution 的全部准备语义，但把 token /
// delegate 回调改为写流而非写 socket，并补上终态帧的写入口。
func (s *AgentService) executeStreamRun(
	ctx context.Context, agentID string, req ExecRequest, meta ExecMeta,
	executionID string, generation int,
) (*AgentResult, int, error)
```

实现要点的第 0 条（在其余各条之前）：

```go
	// 测试注入点：跨实例测试用假 LLM 替换整次执行，否则无法断言
	// 「B 是续传而不是重新生成」。生产为 nil，走下面各条的真实路径。
	if s.deps.StreamRunFn != nil {
		return s.deps.StreamRunFn(ctx, agentID, req, meta, tokenCb)
	}
```

其余要点（与今天 `ExecuteStream` 的 `run` 闭包逐条对应）：

1. `a, req, meta, streamCtx, options, cfg, resuming, terminal, consumedApproval, err := s.prepareAgentExecution(ctx, agentID, req, meta, executionID)`，错误原样返回。
2. 构造写流的 token 回调与 delegate 回调：

```go
	streamEvent := func(e port.StreamEntry) {
		if _, err := s.deps.StreamStore.Append(ctx, executionID, generation, e); err != nil {
			s.logStreamWriteFailure(executionID, err)
		}
	}
	tokenCb := func(token string) {
		streamEvent(snapshotStreamFrame(port.StreamEventToken, map[string]string{"token": token}))
	}
	delegateCb := func(ev agentgraph.DelegateEvent) {
		streamEvent(snapshotStreamFrame(port.StreamEventDelegate, map[string]any{
			"delegate_status": string(ev.Status),
			"delegate_id":     ev.DelegateID,
			"goal":            ev.Goal,
			"summary":         ev.Summary,
			"tokens_used":     ev.TokensUsed,
			"result_status":   ev.ResultStatus,
		}))
	}
	meta.DelegateEventCb = delegateCb
```

1. 保留 `Metrics` 的 TTFT 包裹（`firstToken sync.Once` + `streamStarted`）。
2. `options = append(options, WithTokenCallback(wrappedTokenCb), WithDelegateEventCallback(delegateCb), WithExecutionID(executionID))`。
3. `execCtx, cancel := context.WithCancel(context.WithoutCancel(streamCtx))` + 规则拦截累积器（与今天一致），`defer cancel()`。
4. 执行 `a.Execute(execCtx, req.Query, options...)`，随后 `recordSystemAssistantExecution`、`logAgentExecution`、成功路径的 `bufferMemoryTurn` / `emitObservation`、`enqueueTrajectoryReflection`、`finishApprovalResume`——与今天的 `run` 闭包**逐行一致**，只是不返回给 handler。

`appendStreamTerminal` 的语义：

```go
// appendStreamTerminal 写终态帧。被取消（含用户停止）写 stopped 帧，其余按
// done / error 分流。stopped 帧刻意复用 done 的形状——今天的前端按 data 字段
// 嗅探分发，done 走覆盖语义（finalContent = output || accumulatedContent），
// output 为空时保留用户已看到的部分答案。
func (s *AgentService) appendStreamTerminal(
	ctx context.Context, executionID string, generation int, result *AgentResult, runErr error,
) {
	switch {
	case runErr == nil && result != nil:
		s.appendStreamEntry(ctx, executionID, generation, port.StreamEntry{
			Event: port.StreamEventDone, Payload: string(s.donePayloadBytes(result)),
		})
	case errors.Is(runErr, context.Canceled):
		s.appendStreamEntry(ctx, executionID, generation, port.StreamEntry{
			Event: port.StreamEventStopped,
			Payload: mustJSON(map[string]any{"done": true, "stopped": true}),
		})
	default:
		s.appendStreamEntry(ctx, executionID, generation, port.StreamEntry{
			Event: port.StreamEventError, Payload: string(s.errorPayloadBytes(runErr)),
		})
	}
}
```

`appendStreamEntry` 与 `mustJSON`：

```go
func (s *AgentService) appendStreamEntry(
	ctx context.Context, executionID string, generation int, e port.StreamEntry,
) {
	if _, err := s.deps.StreamStore.Append(ctx, executionID, generation, e); err != nil {
		s.logStreamWriteFailure(executionID, err)
	}
}

func mustJSON(v any) string {
	encoded, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}
```

`donePayloadBytes` / `errorPayloadBytes` 是 handler 层 `agentExecutionDonePayload` / `agentExecutionErrorPayload` 的应用层化：把 `api/http/handler/agent_exec_handler.go` 的同名逻辑搬到应用层（handler 保留薄包装以便 `ExecuteAgent` 非流式路径继续复用）。

`OpenStreamSubscription`：

```go
// OpenStreamSubscription 解析一条 SSE 请求的订阅计划。它先确保 run 已在跑
// （必要时抢占），再决定回放起点。
func (s *AgentService) OpenStreamSubscription(
	ctx context.Context, agentID string, req ExecRequest, meta ExecMeta, cfg StreamSubscriptionConfig,
) (*ExecutionSubscription, error) {
	handle, err := s.ExecuteStream(ctx, agentID, req, meta)
	if err != nil {
		return nil, err
	}
	plan := PlanStream(meta.Generation, handle.Generation, meta.LastEventID)
	return NewExecutionSubscription(ExecutionSubscriptionDeps{
		Stream:      s.deps.StreamStore,
		Control:     s.deps.ControlBus,
		Lease:       s.deps.LeaseRepo,
		TenantID:    meta.TenantID,
		ExecutionID: handle.ExecutionID,
		Generation:  handle.Generation,
		Plan:        plan,
		Cfg:         cfg,
	}), nil
}
```

- [ ] **Step 6: 修复受影响调用点**

Run: `grep -rn "ExecuteStream(" --include='*.go' . | grep -v '_test.go'`
Run: `go build ./... 2>&1 | head -40`
逐个修复编译错误。已知需要改的：

- `api/http/handler/agent_exec_handler.go` — Task 14 处理
- 所有 `_test.go` 中调用 `svc.ExecuteStream(...)` 的用例：改为 `svc.OpenStreamSubscription(...)` 或注入 fake 流依赖。

Run: `grep -rln "ExecuteStream(" --include='*_test.go' .`
对每个文件注入 `StreamStore`/`ControlBus`/`LeaseRepo` 的 fake（复用本任务 Step 1 的 `fakeStreamStore`/`fakeControlBus`/`fakeLeaseRepo`）。

- [ ] **Step 7: 运行测试确认通过**

Run: `go test ./internal/agent/... -short`
Expected: PASS

- [ ] **Step 8: 提交**

```bash
git add internal/agent/application/ internal/agent/domain/port/
git commit -m "feat(agent): ExecuteStream 拆出 runner/订阅，去请求绑定"
```

---

## Task 14: handler 重写 + stop 端点

**Files:**

- Modify: `api/http/handler/agent_exec_handler.go:112-224`
- Modify: `api/http/handler/agent_dto.go:96-103`
- Modify: `api/http/router.go:548-549`
- Test: `api/http/handler/agent_exec_stream_test.go`、`api/http/testdata/contracts/post_agents__id_executions__executionID_stop.golden.json`

**Interfaces:**

- Consumes: Task 13 的 `OpenStreamSubscription`、Task 10 的 `EnqueueStreamFrame`
- Produces: `(*AgentHandler).StopExecution(c *gin.Context)`；`ExecuteAgentRequest.Generation int` / `.LastEventID string`

- [ ] **Step 1: 扩展请求 DTO**

在 `api/http/handler/agent_dto.go` 的 `ExecuteAgentRequest` 中，`ExecutionID` 之后追加：

```go
	// Generation / LastEventID 是断线续传的游标（spec §6.4）。二者都是
	// wire-only 字段：本结构体是 agent execute 路由的实际绑定点，proto 中的
	// ExecuteAgentRequest 是零引用的死代码（spec §11.4）。
	// LastEventID 只存在于会话内，F5 后前端不发它 → 服务端全量回放。
	Generation  int    `json:"generation"`
	LastEventID string `json:"last_event_id"`
```

- [ ] **Step 2: 重写流式 handler**

用以下内容替换 `agent_exec_handler.go` 的 `ExecuteAgentStream`（原 `:112-224`）：

```go
// ExecuteAgentStream 是纯订阅者：它不再拥有 run，也不再有「客户端断连即
// cancel」的分支（spec §4.1）。执行的启动/续接交给应用层，本函数只负责把
// 订阅产出的帧写进 socket。
func (h *AgentHandler) ExecuteAgentStream(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	id := c.Param("id")
	var req ExecuteAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
		return
	}
	userID, _ := userIDFromCtx(c)

	// 带 execution_id 的请求是续接：先做租户限定的存在性判断，查不到即 404
	// （不区分「不存在」与「不属于你」），避免把 read 失败当成「无活跃执行」
	// 而静默起一个重复运行。
	sub, err := h.svc.OpenStreamSubscription(c.Request.Context(), id, agent.ExecRequest{
		Query:          req.Query,
		ConversationID: req.ConversationID,
		UserID:         userID,
		MaxSteps:       intOption(req.Options, "maxSteps"),
		Timeout:        timeoutOption(req.Options, "timeout"),
	}, agent.ExecMeta{
		TenantID:    tenantID,
		TraceID:     middleware.GetTraceID(c),
		ExecutionID: req.ExecutionID,
		Generation:  req.Generation,
		LastEventID: req.LastEventID,
		Stream:      true,
	})
	if err != nil {
		if errors.Is(err, agent.ErrNotFound) {
			_ = c.Error(err)
			return
		}
		if errors.Is(err, agent.ErrApprovalNotApproved) {
			c.JSON(http.StatusAccepted, gin.H{"status": "waiting_approval"})
			return
		}
		_ = c.Error(err)
		return
	}
	defer sub.Close()

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Transfer-Encoding", "chunked")

	writer := newSSEEventWriter(c.Writer)
	// 订阅 goroutine 持续产出；本 goroutine 只做写出。慢客户端的背压止步于
	// 订阅 goroutine 的帧通道，不再顶到 LLM 读循环（spec §4.4）。
	go func() {
		defer writer.Close()
		for frame := range sub.Frames() {
			if frame.Comment != "" {
				if !writer.EnqueueComment(frame.Comment) {
					return
				}
				continue
			}
			if !writer.EnqueueStreamFrame(frame.ID, sseEventName(frame.Event), frame.Data) {
				return
			}
		}
	}()

	// 客户端断开只结束本订阅：run 继续跑，viewer 由 runner 的自然超时摘除
	// （spec §7.4）。停掉 run 的唯一途径是 stop 端点或孤儿超时。
	writer.WriteUntilClosed(0)
}

// sseNamedEvents 是 PR 1 阶段下发给客户端的具名事件白名单。
// 其余事件名存在于流条目中供服务端内部判定（终态识别、游标语义），但不下发
// event: 行——今天的前端按 data 字段嗅探分发，全量具名下发排在 PR 3（spec §6.8）。
// approval_required 今天已具名，保持不变。
var sseNamedEvents = map[string]struct{}{
	agentport.StreamEventMeta:             {},
	agentport.StreamEventReset:            {},
	agentport.StreamEventApprovalRequired: {},
}

func sseEventName(event string) string {
	if _, ok := sseNamedEvents[event]; ok {
		return event
	}
	return ""
}
```

- [ ] **Step 3: 写 stop 端点**

在 `agent_exec_handler.go` 的 `ResumeExecution` 之后追加：

```go
// StopExecution 请求停止一次在跑的流式执行。它发布控制通道消息，由持有租约的
// runner 收到后取消——不直接杀任何本进程的 goroutine，因此对「run 在另一个
// pod 上」同样有效（spec §7.3）。
func (h *AgentHandler) StopExecution(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		respondMissingTenant(c)
		return
	}
	userID, _ := userIDFromCtx(c)
	executionID := c.Param("executionID")

	// 授权在 HTTP 层完成：Pub/Sub 通道不承载鉴权，发布前必须确认 actor 对该
	// execution 有所有权（spec §9）。存在性与归属合并为一次租户限定的查询，
	// 查不到即 404，不区分「不存在」与「不属于你」。
	if err := h.svc.StopExecution(c.Request.Context(), tenantID, executionID, userID); err != nil {
		_ = c.Error(err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "stopping"})
}
```

- [ ] **Step 4: 加应用层 StopExecution**

追加到 `internal/agent/application/agent_stream_execute.go`：

```go
// StopExecution 校验所有权后向控制通道发布取消消息。
//
// 授权必须在发布之前完成——Pub/Sub 通道本身不承载鉴权（spec §9）。
// 归属判定复用租户限定的 checkpoint 查询：查不到或 user_id 不匹配都返回
// ErrNotFound，不区分两者，关闭 existence oracle。
func (s *AgentService) StopExecution(
	ctx context.Context, tenantID, executionID, userID string,
) error {
	if s.deps.ControlBus == nil {
		return fmt.Errorf("agent: stop execution: control bus not configured")
	}
	cp, err := s.deps.CheckpointStore.GetLatest(ctx, tenantID, executionID)
	if err != nil {
		return fmt.Errorf("agent: stop execution: load checkpoint: %w", err)
	}
	if cp == nil {
		return ErrNotFound
	}
	if cp.UserID != "" && userID != "" && cp.UserID != userID {
		return ErrNotFound
	}
	if err := s.deps.ControlBus.PublishStop(ctx, executionID); err != nil {
		return fmt.Errorf("agent: stop execution: publish: %w", err)
	}
	return nil
}
```

- [ ] **Step 5: 加路由**

在 `api/http/router.go` 的 `:549`（resume 行）之后追加：

```go
		agents.POST("/:id/executions/:executionID/stop", requireActive, agentHandler.StopExecution)
```

- [ ] **Step 6: 写失败测试**

创建 `api/http/handler/agent_exec_stream_test.go`：

```go
package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
)

// stopControlBus 记录发布过的停止指令。
type stopControlBus struct {
	stops []string
}

func (b *stopControlBus) PublishStop(_ context.Context, executionID string) error {
	b.stops = append(b.stops, executionID)
	return nil
}

func (b *stopControlBus) PublishViewer(context.Context, string, string) error { return nil }

func (b *stopControlBus) Subscribe(context.Context, string) (<-chan port.ControlMessage, func(), error) {
	ch := make(chan port.ControlMessage)
	return ch, func() {}, nil
}

func TestStopExecutionPublishesStop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bus := &stopControlBus{}
	svc := agent.NewAgentService(agent.AgentServiceDeps{
		ControlBus:      bus,
		CheckpointStore: &stubCheckpointStore{userID: "u1"},
	})
	h := &AgentHandler{svc: svc, logger: zap.NewNop()}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/agents/a1/executions/e1/stop", nil)
	c.Params = gin.Params{{Key: "executionID", Value: "e1"}}
	c.Set("tenantID", "t1")
	c.Set("userID", "u1")

	h.StopExecution(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rec.Code, rec.Body.String())
	}
	if len(bus.stops) != 1 || bus.stops[0] != "e1" {
		t.Fatalf("published stops = %v, want [e1]", bus.stops)
	}
	if !strings.Contains(rec.Body.String(), `"stopping"`) {
		t.Fatalf("body = %q, want stopping", rec.Body.String())
	}
}

func TestStopExecutionHidesForeignExecution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	bus := &stopControlBus{}
	svc := agent.NewAgentService(agent.AgentServiceDeps{
		ControlBus:      bus,
		CheckpointStore: &stubCheckpointStore{userID: "someone-else"},
	})
	h := &AgentHandler{svc: svc, logger: zap.NewNop()}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/agents/a1/executions/e1/stop", nil)
	c.Params = gin.Params{{Key: "executionID", Value: "e1"}}
	c.Set("tenantID", "t1")
	c.Set("userID", "u1")

	h.StopExecution(c)

	// 别人的执行必须 404，且不能发出停止指令——否则任何人都能停掉任何执行。
	if len(bus.stops) != 0 {
		t.Fatalf("stop published for a foreign execution: %v", bus.stops)
	}
	if rec.Code == http.StatusOK {
		t.Fatalf("status = 200 for a foreign execution, want an error")
	}
}

// stubCheckpointStore 只实现 StopExecution 需要的 GetLatest。
type stubCheckpointStore struct {
	userID string
	err    error
}

func (s *stubCheckpointStore) Upsert(context.Context, string, domain.AgentExecutionCheckpoint) error {
	return nil
}

func (s *stubCheckpointStore) GetLatest(
	context.Context, string, string,
) (*domain.AgentExecutionCheckpoint, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &domain.AgentExecutionCheckpoint{ExecutionID: "e1", UserID: s.userID}, nil
}

func (s *stubCheckpointStore) MarkCompleted(context.Context, string, string) error { return nil }
func (s *stubCheckpointStore) UpdateStatus(context.Context, string, string, string) error {
	return nil
}
func (s *stubCheckpointStore) DeleteExpired(context.Context, string) (int64, error) { return 0, nil }
func (s *stubCheckpointStore) GetLatestActiveByConversation(
	context.Context, string, string,
) (*domain.AgentExecutionCheckpoint, error) {
	return nil, nil
}
func (s *stubCheckpointStore) UpdateStatusFrom(context.Context, string, string, string, string) error {
	return nil
}
func (s *stubCheckpointStore) AdvanceRunGeneration(context.Context, string, string, int) error {
	return nil
}
func (s *stubCheckpointStore) Terminate(context.Context, string, string, string) error { return nil }

var _ = errors.Is
var _ = time.Second
```

> `api/http/handler/agent_handler_extra_test.go:110` 已有 `fakeCheckpointStore`，但它的 `GetLatest` 恒返回 `(nil, nil)`（专为 Pause/Resume 失败路径而写），**无法**支撑本任务的归属断言——两条用例都会落到 404 分支，`TestStopExecutionPublishesStop` 会假红。因此新增 `stubCheckpointStore` 是必要的，且与既有名字不冲突。若实现时发现你想改名，避免用 `fakeCheckpointStore`（已占用，重名编译失败）。

- [ ] **Step 7: 运行测试确认失败→通过**

Run: `go test ./api/http/handler/ -run TestStopExecution -v`
Expected: 先 FAIL（`h.svc.StopExecution undefined`），实现后 PASS（2 个）

- [ ] **Step 8: 补 stop 端点的契约 golden**

`api/http/testdata/contracts/` 下新增 `post_agents__id_executions__executionID_stop.golden.json`：

```json
{
  "method": "POST",
  "path": "/agents/a1/executions/e1/stop",
  "want_status": 401,
  "want_body": {
    "error": "missing or invalid authorization header"
  }
}
```

> 先跑 `go test ./api/http/ -run TestContracts -v` 看既有 golden 的实际 `want_body` 文案，逐字对齐——golden 是逐字节比对，猜错文案会让测试红。spec §11.4 已记录：agent execute 的既有 golden 只有一条 401 用例，所以 stop 端点必须补这一条，否则它在契约层是裸的。

Run: `go test ./api/http/ -run TestContracts -v`
Expected: PASS

- [ ] **Step 9: 全量后端快速验证**

Run: `go vet ./... && go test -short ./...`
Expected: PASS

- [ ] **Step 10: 提交**

```bash
git add api/http/handler/ api/http/router.go api/http/testdata/contracts/
git commit -m "feat(agent): SSE 订阅化 + 停止生成端点"
```

---

## Task 15: wiring 装配与关闭顺序

**Files:**

- Modify: `api/wiring/`（container 与 agent 装配文件）
- Test: 由 `make test-verify-before-pr` 的 readiness/关闭路径 E2E 覆盖

**Interfaces:**

- Consumes: Task 8/9 的实现、Task 6 的租约 repo
- Produces: 生产环境装配完成的流依赖与逆序关闭

- [ ] **Step 1: 定位装配点**

Run: `grep -rn "NewAgentService\|AgentServiceDeps{" api/wiring/*.go | head`
Run: `grep -rn "redisClient\|redis.New(" api/wiring/*.go | head`

- [ ] **Step 2: 装配**

在 agent 装配处（`AgentServiceDeps{...}` 构造点）追加：

```go
	// 断线续传三件套：流（Redis Streams）、控制通道（Pub/Sub 独立连接）、
	// 租约（PG，与 checkpoint 同表）。
	StreamStore: agentstream.NewAgentStreamStore(redis.NewStreamStore(redisClient.Client())),
	ControlBus:  agentstream.NewControlBus(controlClient),
	LeaseRepo:   checkpointStore,
```

其中 `controlClient` 必须在同一装配函数内创建：

```go
	// Pub/Sub 独占一条连接：控制通道与 Stream 命令必须分开实例，否则订阅
	// 期间普通命令会在同一连接上排队饿死（spec §7.1）。
	controlClient := redisClient.Duplicate()
```

- [ ] **Step 3: 注册关闭**

按「逆序关闭」在 container 的 cleanup 链中注册：

```go
	cleanups = append(cleanups,
		func(ctx context.Context) error {
			// 先停 runner：取消所有在跑的 run 并等待退出，run 中断时上层保留
			// checkpoint，新 pod 可续（spec §7.4）。
			agentService.ShutdownStreamRunners()
			return nil
		},
		func(ctx context.Context) error {
			// 再关控制通道的独立连接。
			return controlClient.Close()
		},
	)
```

顺序要点：`ShutdownStreamRunners` **必须早于** controlClient/redisClient 关闭——runner 退出路径仍要写终态帧与发布/订阅。

- [ ] **Step 4: 编译与启动验证**

Run: `go build ./... && go vet ./...`
Expected: 成功
Run: `make infra-up && go run ./cmd/server` 后 `curl -sf localhost:8080/healthz`
Expected: 200/OK；日志无 Redis subscribe 报错
停止进程（Ctrl-C），确认日志中出现 runner 关闭路径且无 goroutine 泄漏告警。

- [ ] **Step 5: 提交**

```bash
git add api/wiring/
git commit -m "feat(wiring): 装配执行流依赖与逆序关闭"
```

---

## Task 16: 前端停止按钮（F6）

**Files:**

- Modify: `web/src/modules/agent/api/agent.api.ts`
- Modify: `web/src/modules/agent/hooks/ChatStreamContext.tsx:200-209`
- Modify: `web/src/modules/agent/components/ChatComposer.tsx`
- Modify: `web/src/modules/agent/pages/AgentChatPage.tsx:171-180`
- Test: `web/src/modules/agent/components/__tests__/ChatComposer.test.tsx`

**Interfaces:**

- Consumes: `services/client.ts` 的唯一 Axios 实例
- Produces: `stopAgentExecution(agentId: string, executionId: string): Promise<void>`

- [ ] **Step 1: 加 API**

在 `web/src/modules/agent/api/agent.api.ts` 中，与 `pauseExecution` 同区追加：

```ts
// stopAgentExecution 请求停止一次在跑的流式执行。服务端发布控制通道消息，
// 由持有租约的 runner 收到后取消——对 run 在别的 pod 上同样有效。
export async function stopAgentExecution(agentId: string, executionId: string): Promise<void> {
  await client.post(`/agents/${agentId}/executions/${executionId}/stop`);
}
```

- [ ] **Step 2: 写失败测试**

创建/追加 `web/src/modules/agent/components/__tests__/ChatComposer.test.tsx`：

```tsx
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import { ChatComposer } from '../ChatComposer';

const baseProps = {
  input: '',
  setInput: vi.fn(),
  sending: false,
  selectedConv: 'conv-1',
  onSend: vi.fn(),
};

describe('ChatComposer 停止按钮', () => {
  it('非流式时不渲染停止按钮', () => {
    render(<ChatComposer {...baseProps} streaming={false} onStop={vi.fn()} />);
    expect(screen.queryByLabelText('停止生成')).toBeNull();
  });

  it('流式时渲染停止按钮并回调 onStop', async () => {
    const onStop = vi.fn();
    render(<ChatComposer {...baseProps} streaming onStop={onStop} />);
    await userEvent.click(screen.getByLabelText('停止生成'));
    expect(onStop).toHaveBeenCalledTimes(1);
  });
});
```

- [ ] **Step 3: 运行测试确认失败**

Run: `cd web && npx vitest run src/modules/agent/components/__tests__/ChatComposer.test.tsx`
Expected: FAIL — 未找到 `停止生成`

- [ ] **Step 4: 改 ChatComposer**

`ChatComposer.tsx` 的 `Props` 增加：

```ts
  streaming?: boolean;
  onStop?: () => void;
```

解构处增加 `streaming = false,` 与 `onStop,`，并在发送按钮**之前**插入：

```tsx
      {streaming ? (
        <Button
          danger
          icon={<StopOutlined />}
          onClick={onStop}
          aria-label="停止生成"
        >
          {isMobile ? null : '停止'}
        </Button>
      ) : (
        <Button
          type="primary"
          icon={<SendOutlined />}
          onClick={onSend}
          loading={sending}
          disabled={!selectedConv || !input.trim()}
          aria-label="发送消息"
        >
          {isMobile ? null : '发送'}
        </Button>
      )}
```

把首行 import 改为 `import { SendOutlined, StopOutlined } from '@ant-design/icons';`，并删除被替换的原发送按钮。

- [ ] **Step 5: 接上 cancelStream**

`ChatStreamContext.tsx` 中 `cancelStream` 改为：

```tsx
  const cancelStream = useCallback(() => {
    const s = stateRef.current;
    if (!s.ctrl) return;
    // 保留恢复键：停止后前端不回退渲染，但服务端仍会写终态帧、保留 checkpoint。
    const executionId = s.executionId;
    const agentId = s.agentId;
    s.ctrl.abort();
    s.ctrl = null;
    s.delegateStatus = null;
    s.done = true;
    notify();
    // 通知服务端真正停掉 run：只 abort 本地 fetch 不会让 runner 退出
    // （run 的生命周期已与 HTTP 请求解绑）。失败只提示，不回滚本地状态——
    // 用户已经看到停止生效，孤儿超时会在 2 分钟内兜底取消。
    if (executionId && agentId) {
      stopAgentExecution(agentId, executionId).catch((err) => {
        message.error({ content: err.response?.data?.error || '停止失败', duration: 3 });
      });
    }
  }, [notify]);
```

在文件 import 中加入 `stopAgentExecution` 与 `message`（`import { message } from 'antd';`）。

- [ ] **Step 6: 接到 UI**

`AgentChatPage.tsx` 的 `<ChatComposer ... />` 增加：

```tsx
          streaming={streaming}
          onStop={cancelStream}
```

`streaming` 与 `cancelStream` 从 `useChatPage` 的返回值解构（与本文件已有用法一致；若当前未解构 `cancelStream`，在 `useChatPage()` 的解构处补上）。

- [ ] **Step 7: 运行前端验证**

Run: `cd web && npx vitest run src/modules/agent/components/__tests__/ChatComposer.test.tsx`
Expected: PASS（2 个）
Run: `make fe-lint && make fe-build`
Expected: 成功

- [ ] **Step 8: 提交**

```bash
git add web/src/modules/agent/
git commit -m "feat(agent): 停止生成入口接线"
```

---

## Task 17: 跨实例承重墙测试

**Files:**

- Create: `internal/agent/application/agent_stream_cross_instance_integration_test.go`

**Interfaces:**

- Consumes: Task 6 的 `PgCheckpointStore`、Task 8 的 `AgentStreamStore`、Task 9 的 `ControlBus`、Task 13 的 `AgentService`
- Produces: 对 spec §11.2 四条硬性断言的回归保护

- [ ] **Step 1: 写测试**

创建 `internal/agent/application/agent_stream_cross_instance_integration_test.go`（`package application_test`，避免与内置 fake 冲突）：

```go
package application_test

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	agentstream "github.com/byteBuilderX/stratum/internal/agent/infrastructure/stream"
	"github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
	pgcontext "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/storage/redis"
)

// TestCrossInstanceResumeDoesNotRegenerate 是本次改造的北极星断言：
// A 在跑、A 的 HTTP 断开、B 用同一 execution_id attach，B 必须收到 token，
// 且 LLM 调用次数恰好为 1——即 B 没有重新生成（spec §11.2）。
func TestCrossInstanceResumeDoesNotRegenerate(t *testing.T) {
	pgURL := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if pgURL == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		t.Fatal(err)
	}
	tenantID := fmt.Sprintf("tmp_xinst_%d", time.Now().UnixNano())
	schema := "tenant_" + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}

	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	leaseStore := persistence.NewPgCheckpointStore(pool)
	streamStore := agentstream.NewAgentStreamStore(redis.NewStreamStore(rdb))
	controlBus := agentstream.NewControlBus(rdb)

	var llmCalls int64
	cfg := agent.StreamRunnerConfig{
		LeaseTTL: 30 * time.Second, LeaseRenew: 50 * time.Millisecond,
		ViewerTimeout: 200 * time.Millisecond, OrphanTimeout: 5 * time.Second,
		StreamTTL: time.Hour, StreamMaxLen: 1000,
	}

	// instanceA / instanceB 共用同一套 PG + Redis，但各自持有独立的 AgentService——
	// 这正是「重连落到另一个 pod」的最小复现。
	newService := func() *agent.AgentService {
		return agent.NewAgentService(agent.AgentServiceDeps{
			CheckpointStore: leaseStore,
			StreamStore:     streamStore,
			ControlBus:      controlBus,
			LeaseRepo:       leaseStore,
			StreamRunnerCfg: cfg,
			Logger:          zap.NewNop(),
			// StreamRunFn 由测试注入：返回递增 token，并计数 LLM 调用。
			StreamRunFn: func(context.Context, string, agent.ExecRequest, agent.ExecMeta, func(string)) (*domain.AgentResult, int, error) {
				atomic.AddInt64(&llmCalls, 1)
				return &domain.AgentResult{Output: "hello"}, 1, nil
			},
		})
	}
	svcA := newService()
	svcB := newService()

	ctxA := pgcontext.WithTenant(ctx, &pgcontext.TenantContext{TenantID: tenantID, UserID: "u1"})

	// A 起一个 NEW 执行并 attach。
	subA, err := svcA.OpenStreamSubscription(ctxA, "agent-1", agent.ExecRequest{Query: "hi"},
		agent.ExecMeta{TenantID: tenantID}, agent.DefaultStreamSubscriptionConfig())
	if err != nil {
		t.Fatalf("A OpenStreamSubscription: %v", err)
	}
	executionID := firstMetaExecutionID(t, subA)
	subA.Close() // 模拟 A 的 HTTP 断开：只结束订阅，不杀 run

	// B 用同一 execution_id attach —— 不同实例、同一套依赖。
	subB, err := svcB.OpenStreamSubscription(ctxA, "agent-1", agent.ExecRequest{Query: "hi"},
		agent.ExecMeta{TenantID: tenantID, ExecutionID: executionID},
		agent.DefaultStreamSubscriptionConfig())
	if err != nil {
		t.Fatalf("B OpenStreamSubscription: %v", err)
	}
	defer subB.Close()

	if got := collectUntilTerminal(t, subB); !got {
		t.Fatal("B did not receive a terminal frame")
	}
	if n := atomic.LoadInt64(&llmCalls); n != 1 {
		t.Fatalf("LLM calls = %d, want 1 (B must resume, not regenerate)", n)
	}
	svcA.ShutdownStreamRunners()
	svcB.ShutdownStreamRunners()
}
```

- [ ] **Step 2: 运行**

```bash
make infra-up
export STRATUM_TEST_POSTGRES_URL="postgres://stratum:stratum@localhost:5432/stratum?sslmode=disable"
go test ./internal/agent/application/ -run TestCrossInstance -v -race
```

Expected: PASS

> 若 `AgentServiceDeps.StreamRunFn` 不存在，说明 Task 13 未把「run 函数来源」做成可注入。补一个 `StreamRunFn func(context.Context, string, ExecRequest, ExecMeta, func(string)) (*AgentResult, int, error)` 依赖字段，默认实现走 `executeStreamRun`，跨实例测试注入假 LLM。这是 §11.2 能够本地运行的前提。

- [ ] **Step 3: 补失败路径断言（risk-regression-guard 规则 7）**

在同一文件追加以下表驱动用例，每条对应 spec §11.3 的一行：

| 用例名 | 断言 |
|---|---|
| `TestCrossInstanceStopViaControlChannel` | B 发 `PublishStop` 后，A 的 run 在 1s 内退出并写入 `stopped` 终态帧 |
| `TestCrossInstanceLeaseConflictYieldsSingleRunner` | 两个实例同时以同一 `expect` 调 `ClaimLease`，恰好一个成功，另一个 `ErrLeaseConflict` |
| `TestCrossInstanceStaleRenewDoesNotResurrect` | 被抢占的 runner 续租得到 `ErrLeaseConflict`（心跳据此自取消） |
| `TestCrossInstanceStreamsDoNotInterleave` | gen=1 与 gen=2 的流内容互不出现（分代 key 的物理保证） |
| `TestCrossTenantExecutionIDIsNotFound` | 另一租户对同一 `execution_id` 的订阅返回 `ErrNotFound` |
| `TestOrphanRunCancelsAfterTimeout` | 无 viewer 且超过 `OrphanTimeout`（测试注入 200ms）后 run 被取消，checkpoint 仍存在 |

每个用例复用 Step 1 的 `newService` 与 schema 装配；表驱动结构为

```go
	cases := []struct {
		name string
		run  func(t *testing.T, svcA, svcB *agent.AgentService, executionID string)
	}{ /* ... */ }
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { /* 独立 tenant schema + 独立 service 对 */ })
	}
```

- [ ] **Step 4: 运行并提交**

```bash
go test ./internal/agent/application/ -run TestCrossInstance -v -race
go test ./internal/agent/application/ -run 'TestOrphan|TestCrossTenant' -v
git add internal/agent/application/agent_stream_cross_instance_integration_test.go
git commit -m "test(agent): 跨实例续传承重墙（LLM 调用次数 == 1）"
```

---

## 自审

### Spec 覆盖

| Spec 节 | 覆盖任务 |
|---|---|
| §4.4 执行侧不写 socket | Task 13（`tokenCb` 改为写流）、Task 14（handler 纯订阅） |
| §5 D1–D6 | D1→13/14；D2/D3→Task 6/11；D4→Task 2/8/13；D5→Task 11/12/13（可注入）；D6→Task 6（CAS 多订阅者） |
| §6.1–6.3 Key/Entry/帧映射 | Task 8（key）、Task 3（entry）、Task 10（`id:` 行） |
| §6.4/6.5/6.6 游标协议 | Task 7（纯函数）、Task 12（回放/reset） |
| §6.7 TTL 与容量 | Task 1（常量）、Task 11（搭车 `RefreshTTL`） |
| §6.8 事件命名 | Task 14（`sseNamedEvents` 白名单，全量具名留 PR 3） |
| §7.1 控制通道 | Task 9（独立连接）、Task 11（viewer 注册表）、Task 12（心跳上报） |
| §7.2 重连决策树 | Task 13（`settleGeneration` 三路分支） |
| §7.3 停止生成 | Task 14（端点 + 授权）、Task 13（`stopped` 终态帧） |
| §7.4 关闭顺序 | Task 15（wiring 逆序关闭） |
| §8.2 F6 停止按钮 | Task 16 |
| §9 安全 | Task 2（命名空间）、Task 13/14（404 不区分归属）、Task 14（发布前授权） |
| §10 降级 | Task 12（Redis 掉线/缺口/看门狗）、Task 11（续租失败自取消） |
| §11.1 分层测试 | Task 3/6/7/8/9/10/12/17 |
| §11.2 跨实例 | Task 17 |
| §11.3 失败路径 | Task 17 Step 3 |
| §11.4 契约 | Task 14 Step 1/8（不改 proto，补 stop golden） |
| §14 变更清单 | 文件结构表 |

**未覆盖且刻意的**：§13 明确不做的五项（前端持久化文本、流内快照、完整具名重写、`cancelled` 枚举、session affinity）在本计划中均无任务，符合 spec。

### 类型一致性

- `port.StreamEntry{ID, Event, Payload}` 在 Task 4 定义，Task 3 的 `redis.StreamEntry` 同名字段经 `toPortEntries` 转换（Task 8），Task 12 直接消费 port 类型。
- `StreamPlan{Generation, Reset, ResetReason, AfterID}` 在 Task 7 定义，Task 12 的 `replay`/`resetPayload` 与 Task 13 的 `OpenStreamSubscription` 一致使用。
- `StreamRunnerConfig` 六字段在 Task 11 定义，Task 13 的 `streamRunnerConfig()` 与 Task 17 的注入点字段名一致。
- `ExecutionSubscriptionDeps` 在 Task 12 定义，Task 13 构造时逐字段对齐。
- `port.ExecutionLeaseRepo` 五方法在 Task 4 定义，Task 6 的 `PgCheckpointStore` 实现与两处 fake（Task 13、Task 17）方法签名一致。

### 已核验的仓库事实（执行时以这些为准，勿再猜）

| 事实 | 位置 | 对本计划的影响 |
|---|---|---|
| `AgentServiceDeps` 已有 `Logger *zap.Logger`、`Metrics`、`CheckpointStore`、`Ledger`、`ApprovalService` | `internal/agent/application/agent_service.go` | Task 11/13/17 直接用 `s.deps.Logger`，**不要新增 Logger 字段** |
| `ErrNotFound = domain.ErrNotFound` | `application/chat_store.go:28` → `domain/errors.go:8` | Task 13/14 用 `agent.ErrNotFound` 不变；`ErrNotFound` 是别名，新增 `ErrNotFound` 会重复声明 |
| `ErrApprovalNotApproved` | `application/tool_approval_service.go:20` | Task 14 的既有分支保留即可 |
| `AgentResult` 定义在 **domain** 包 | `internal/agent/domain/agent.go:400` | `executeStreamRun` 与 `StreamRunFn` 的返回类型是 `*domain.AgentResult`，**不是** `*agent.AgentResult` |
| `AgentResult` 无 `Sources`→ 有；含 `TerminatedBy`/`Degraded`/`FactCheck`/`NoAnswer` | 同上 | `donePayloadBytes` 必须覆盖这些字段，与今天 handler 的 `agentExecutionDonePayload` 逐字段对齐 |
| handler 已 import `agent`(application)、`agentgraph`、`domain`、`agentport` | `api/http/handler/agent_exec_handler.go:11-22` | Task 14 可直接用 `agentport.StreamEvent*` |

### 已知的行内偏离（实现时必须按注释修正）

1. Task 12 的 `emit` 不接收 ctx，靠 `streamEmitTimeout` 兜底；若 `Close()` 在测试中超时，改为接收 ctx 的可取消写。
2. Task 13 的 `streamRunnerHeartbeat` 接收 `tenantID` 参数（不从 ctx 取）——ctx 来自 `context.WithoutCancel`，不带租户上下文。实现时把 `s.tenantIDOf(ctx)` 换成传入的 `meta.TenantID`。
3. Task 13 的 `zapString`/`zapError` 是 `zap.String`/`zap.Error` 的本地别名，实现时直接用 zap 原函数并补 import。
4. Task 17 依赖 `AgentServiceDeps.StreamRunFn` 可注入；若 Task 13 未加，需在 Task 17 前补上（这是跨实例测试能本地跑的前提）。
5. Task 5 的 `readTenantSchema(t)` 若在文件中不存在，改用同文件既有的 schema 读取 helper（Step 2 已给出确认命令）。

---

## 执行前必做

```bash
bash scripts/quality/risk-regression-guard.sh --explain
```

PR 前（R3，必须由 `stratum-e2e-tester` 执行，不得绕过 skill 手工拼装）：

```bash
make test-verify-before-pr
STATEFUL_E2E_PROFILE=test STATEFUL_E2E_DURATION_SEC=600 STATEFUL_E2E_PACKS=all make e2e-stateful
scripts/quality/run-eval-checks.sh
```
