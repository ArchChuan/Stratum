//go:build integration

package application_test

// Task 17：跨实例承重墙。本文件是整支里唯一直接守护 spec §11.2「LLM 调用次数 == 1」
// 的用例，也是唯一同时带真实 AgentStreamStore（真 Redis Streams 语义）与真实
// PgCheckpointStore 的落点。
//
// 为什么必须带 build tag 而不是「无 tag + t.Skip」：PR 门禁的
// scripts/quality/run-planned-checks.sh 会显式 `env -u STRATUM_TEST_POSTGRES_URL`，
// 无 tag 的文件会照常编译进默认测试二进制、走 t.Skip 静默变绿，§11.2 的断言一次都
// 不执行。带 tag 后它不进默认二进制，只有显式 `-tags=integration` 才会被编译与运行。

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	agent "github.com/byteBuilderX/stratum/internal/agent/application"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/internal/agent/infrastructure/persistence"
	agentstream "github.com/byteBuilderX/stratum/internal/agent/infrastructure/stream"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/storage/redis"
)

// crossInstanceOwner 是 A、B 共用的执行所有者。ExecuteStream 的 D4 归属闸门是
//
//	cp == nil || req.UserID == "" || cp.UserID != req.UserID
//
// 三条析取项都返回同一个 ErrNotFound。A 建 checkpoint 行时写入的就是 req.UserID，
// B 续传时按它比对，因此**两侧都必须非空且严格相等**：只给 B 补非空救不了
// （cp.UserID("") != req.UserID("u1")），只给 A 补也一样（req.UserID == ""）。
const crossInstanceOwner = "u1"

// crossInstanceAgentID 是两侧共用的 agent 标识，只用于 checkpoint 行与日志。
const crossInstanceAgentID = "agent-1"

// 失败上限，不是 sleep：用例靠 channel 编排推进，这些值只在「被测行为压根没发生」
// 时触发失败。取值刻意宽于理论时延（订阅侧一轮 Tail + -race 调度抖动），
// 避免把「通常够快」写成「确定性成立」。
const (
	crossInstanceStartTimeout    = 5 * time.Second
	crossInstanceCancelTimeout   = 5 * time.Second
	crossInstanceTerminalTimeout = 10 * time.Second
)

// streamRunFn 是 `AgentServiceDeps.StreamRunFn` 字段类型的别名。生产包**没有**
// `agent.StreamRunFn` 这个具名类型（字段就是裸函数字面量），所以这里用 `=` 别名
// （不新增类型），只为免去两处重复写长签名。
type streamRunFn = func(
	context.Context, string, agent.ExecRequest, agent.ExecMeta, func(string),
) (*domain.AgentResult, int, error)

// crossInstanceRig 是一个隔离租户下的真实依赖装配：真 PG（独立 schema）+ 真 Redis
// 协议实现（miniredis 提供 Streams / Pub/Sub 语义，不引入外部服务）+ 独立
// AgentService 工厂。两个 service 共用同一套存储与总线，各持自己的 runner 集合和
// 控制订阅——这正是「重连落到另一个 pod」的最小复现。
type crossInstanceRig struct {
	t           *testing.T
	pool        *pgxpool.Pool
	tenantID    string
	ctx         context.Context // 带租户的请求 context（流 key / 控制通道 key 都依赖它）
	leaseStore  *persistence.PgCheckpointStore
	streamStore *agentstream.AgentStreamStore
	controlBus  *agentstream.ControlBus
	cfg         agent.StreamRunnerConfig

	llmCalls   int64
	started    chan struct{} // A 的 run 走到阻塞点
	release    chan struct{} // 测试放行 A 的 run 跑完
	cancelled  chan struct{} // A 的 run 观察到 ctx 取消
	startOnce  sync.Once
	cancelOnce sync.Once
}

// newCrossInstanceRig 装配一个隔离租户。cfg 逐用例注入，孤儿超时因此可以在
// 200ms 与 30s 之间切换而不改生产默认值。
func newCrossInstanceRig(t *testing.T, cfg agent.StreamRunnerConfig) *crossInstanceRig {
	t.Helper()
	pgURL := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if pgURL == "" {
		// build tag 已把本文件挡在默认测试二进制之外；这一层兜底的是「带了 tag 但没给
		// DSN」，避免误连开发库（与既有 persistence 集成测试同构：先 tag 后 skip）。
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		t.Fatalf("provision public schema: %v", err)
	}

	tenantID := "tmp_xinst_" + uuid.NewString()[:8]
	schema := "tenant_" + tenantID
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`)
	})
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatalf("provision tenant schema: %v", err)
	}

	rdb := goredis.NewClient(&goredis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	owner := &postgres.TenantContext{TenantID: tenantID, UserID: crossInstanceOwner}
	return &crossInstanceRig{
		t:           t,
		pool:        pool,
		tenantID:    tenantID,
		ctx:         postgres.WithTenant(ctx, owner),
		leaseStore:  persistence.NewPgCheckpointStore(pool),
		streamStore: agentstream.NewAgentStreamStore(redis.NewStreamStore(rdb)),
		controlBus:  agentstream.NewControlBus(rdb),
		cfg:         cfg,
		started:     make(chan struct{}),
		release:     make(chan struct{}),
		cancelled:   make(chan struct{}),
	}
}

// newService 造一个独立实例（自己的 runner 集合），与另一个实例共用存储与总线。
func (r *crossInstanceRig) newService() *agent.AgentService {
	return agent.NewAgentService(agent.AgentServiceDeps{
		CheckpointStore: r.leaseStore,
		StreamStore:     r.streamStore,
		ControlBus:      r.controlBus,
		LeaseRepo:       r.leaseStore,
		StreamRunnerCfg: r.cfg,
		Logger:          zap.NewNop(),
		StreamRunFn:     r.runFn(),
	})
}

// runFn 是注入的假 LLM：先吐一条 token，再阻塞到测试放行。
//
// 阻塞是这条用例的立身之本。若 runner 立刻返回，B attach 时 A 的 run 早已结束、
// 租约早已释放，llmCalls == 1 会退化成「B 回放了一条历史流」——把续传逻辑整个删掉、
// 只留回放，测试照样绿（裁定 4）。阻塞之后，B attach 时 A 的 run 一定还活着、
// 租约仍在 A 手上，断言才回到它要守护的东西。
func (r *crossInstanceRig) runFn() streamRunFn {
	return func(
		ctx context.Context, _ string, _ agent.ExecRequest, _ agent.ExecMeta, tokenCb func(string),
	) (*domain.AgentResult, int, error) {
		atomic.AddInt64(&r.llmCalls, 1)
		tokenCb("hello ")
		r.startOnce.Do(func() { close(r.started) })
		select {
		case <-r.release:
		case <-ctx.Done():
			r.cancelOnce.Do(func() { close(r.cancelled) })
			return nil, 0, ctx.Err()
		}
		tokenCb("world")
		return &domain.AgentResult{Output: "hello world"}, 1, nil
	}
}

// startRunOnA 用 ExecuteStream（而不是 OpenStreamSubscription）起一条 NEW 执行，
// 并等它走到阻塞点后返回 execution_id。
//
// 刻意不订阅：订阅会在心跳 tick 上上报 viewer，而孤儿超时用例要的正是「无 viewer」。
func (r *crossInstanceRig) startRunOnA(svcA *agent.AgentService) string {
	r.t.Helper()
	handle, err := svcA.ExecuteStream(r.ctx, crossInstanceAgentID,
		agent.ExecRequest{Query: "hi", UserID: crossInstanceOwner},
		agent.ExecMeta{TenantID: r.tenantID})
	if err != nil {
		r.t.Fatalf("A ExecuteStream: %v", err)
	}
	select {
	case <-r.started:
	case <-time.After(crossInstanceStartTimeout):
		r.t.Fatal("A's run never reached the blocking point")
	}
	return handle.ExecutionID
}

// provisionForeignTenant 再建一个隔离租户 schema 并返回其 tenant_id。
//
// 必须真的建表：否则跨租户查询报的是「表不存在」，用例就退化成在验证 schema 缺失，
// 而不是在验证租户边界（与既有 persistence 集成测试的同名 helper 同理由）。
func (r *crossInstanceRig) provisionForeignTenant() string {
	r.t.Helper()
	other := r.tenantID + "_other"
	schema := "tenant_" + other
	r.t.Cleanup(func() {
		_, _ = r.pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`)
	})
	if err := postgres.ProvisionTenantSchema(context.Background(), r.pool, other); err != nil {
		r.t.Fatalf("provision foreign tenant schema: %v", err)
	}
	return other
}

// crossInstanceRunnerConfig 返回跨实例用例的 runner 配置。
//
// orphanTimeout <= 0 时取 30s：除孤儿超时专项用例外，其余用例都必须让孤儿计时永不
// 触发，否则「A 的 run 为什么退出」会被孤儿超时污染——用例就无法区分自己真的观察到
// 了 stop / 续租失败，还是只是等到了孤儿超时（裁定 4.3）。
func crossInstanceRunnerConfig(orphanTimeout time.Duration) agent.StreamRunnerConfig {
	if orphanTimeout <= 0 {
		orphanTimeout = 30 * time.Second
	}
	return agent.StreamRunnerConfig{
		LeaseTTL:      30 * time.Second,
		LeaseRenew:    50 * time.Millisecond,
		ViewerTimeout: 200 * time.Millisecond,
		OrphanTimeout: orphanTimeout,
		StreamTTL:     time.Hour,
		StreamMaxLen:  1000,
	}
}

// crossInstanceSubConfig 是失败路径子用例的订阅配置：只把 Tail 的单轮读周期从默认
// 1s 压到 50ms。默认读周期会主导「run 结束后多久收到终态帧」，压小它让判定回到被测
// 行为本身；心跳（5s）与空闲看门狗（2min）保持默认，测试时长内都不会触发。
func crossInstanceSubConfig() agent.StreamSubscriptionConfig {
	cfg := agent.DefaultStreamSubscriptionConfig()
	cfg.ReadBlock = 50
	return cfg
}

// TestCrossInstanceResumeDoesNotRegenerate 是本次改造的北极星断言：A 在跑、A 的
// HTTP 断开、B 用同一 execution_id attach，B 必须收到 A 断开前已产出的 token，
// 且 LLM 调用次数恰好为 1——即 B 接上了在跑的 run，没有重新生成（spec §11.2）。
//
// llmCalls == 1 的保证机制：它不来自任何计时，而来自 started / release 的确定性
// 编排——A 的 runner 在吐完第一条 token 后阻塞在 run 中间，测试等到 started 才继续，
// 于是 B attach 时租约必然仍被 A 持有（LeaseTTL 30s、续租 50ms、OrphanTimeout 30s
// 保证测试窗口内既不过期也不被孤儿取消）。B 的 ExecuteStream 因此走
// 「LeaseStatus.Active → 只订阅、不启动 runner」这一支，runFn 不会被调用第二次。
func TestCrossInstanceResumeDoesNotRegenerate(t *testing.T) {
	rig := newCrossInstanceRig(t, crossInstanceRunnerConfig(0))
	svcA, svcB := rig.newService(), rig.newService()
	defer svcA.ShutdownStreamRunners()
	defer svcB.ShutdownStreamRunners()

	// A 起一个 NEW 执行并 attach。
	subA, err := svcA.OpenStreamSubscription(rig.ctx, crossInstanceAgentID,
		agent.ExecRequest{Query: "hi", UserID: crossInstanceOwner},
		agent.ExecMeta{TenantID: rig.tenantID}, agent.DefaultStreamSubscriptionConfig())
	if err != nil {
		t.Fatalf("A OpenStreamSubscription: %v", err)
	}
	select {
	case <-rig.started:
	case <-time.After(crossInstanceStartTimeout):
		t.Fatal("A's run never started")
	}
	executionID := firstMetaExecutionID(t, subA)
	subA.Close() // 模拟 A 的 HTTP 断开：只结束订阅，不杀 run

	// B 用同一 execution_id attach —— 不同实例、同一套依赖。
	subB, err := svcB.OpenStreamSubscription(rig.ctx, crossInstanceAgentID,
		agent.ExecRequest{Query: "hi", UserID: crossInstanceOwner},
		agent.ExecMeta{TenantID: rig.tenantID, ExecutionID: executionID},
		agent.DefaultStreamSubscriptionConfig())
	if err != nil {
		t.Fatalf("B OpenStreamSubscription: %v", err)
	}
	defer subB.Close()

	// B 必须收到 A 已经产出的那条 token —— 这是「接上在跑的 run」而非「重新生成」的
	// 直接证据。同时登记 B 实际走的 plan：见 collectUntilToken 的说明。
	firstEvent, sawToken := collectUntilToken(t, subB, "hello ")
	t.Logf("B 实际走的 plan：首帧事件 = %q（%s）；收到 A 断开前的 token = %v",
		firstEvent, describePlan(firstEvent), sawToken)
	if !sawToken {
		t.Fatal("B did not receive the token A produced before the disconnect")
	}
	if n := atomic.LoadInt64(&rig.llmCalls); n != 1 {
		t.Fatalf("LLM calls = %d while A's run is still blocked, want 1 (B must attach, not regenerate)", n)
	}

	close(rig.release)
	if !collectUntilTerminal(t, subB) {
		t.Fatal("B did not receive a terminal frame")
	}
	if n := atomic.LoadInt64(&rig.llmCalls); n != 1 {
		t.Fatalf("LLM calls = %d, want 1 (B must resume, not regenerate)", n)
	}
}

// describePlan 把首帧事件名翻译成「B 实际走的是哪条 plan」的可读结论，只用于日志与
// 报告取证，不参与断言。
func describePlan(firstEvent string) string {
	if firstEvent == port.StreamEventReset {
		return "reset + 全量回放，非 TAIL"
	}
	return "直接跟流/回放，非 reset"
}

// TestCrossInstanceFailurePaths 收口 spec §11.3 的 6 条失败路径（裁定 5：单一顶层
// 函数，`-run TestCrossInstance` 一条命令即可覆盖北极星 + 全部失败路径）。
//
// 每个子用例独立 tenant schema + 独立 service 对，彼此不共享可变状态。
func TestCrossInstanceFailurePaths(t *testing.T) {
	cases := []struct {
		name string
		// orphanTimeout 只对孤儿超时用例有意义；0 表示标准 30s（测试窗口内永不触发）。
		orphanTimeout time.Duration
		run           func(t *testing.T, rig *crossInstanceRig, svcA, svcB *agent.AgentService, executionID string)
	}{
		{"stop via control channel stops the owning run", 0, caseCrossInstanceStopViaControlChannel},
		{"concurrent claim yields a single runner", 0, caseCrossInstanceLeaseConflictYieldsSingleRunner},
		{"stale renew does not resurrect a fenced runner", 0, caseCrossInstanceStaleRenewDoesNotResurrect},
		{"stream generations do not interleave", 0, caseCrossInstanceStreamsDoNotInterleave},
		{"cross tenant execution id is not found", 0, caseCrossTenantExecutionIDIsNotFound},
		{"orphan run cancels after the timeout", 200 * time.Millisecond, caseOrphanRunCancelsAfterTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newCrossInstanceRig(t, crossInstanceRunnerConfig(tc.orphanTimeout))
			svcA, svcB := rig.newService(), rig.newService()
			defer svcA.ShutdownStreamRunners()
			defer svcB.ShutdownStreamRunners()
			// 每例先在 A 上起一条阻塞中的执行；需要 execution_id 的用例据此推进，
			// 不需要的（分代隔离）忽略它即可。
			executionID := rig.startRunOnA(svcA)
			tc.run(t, rig, svcA, svcB, executionID)
		})
	}
}

// caseCrossInstanceStopViaControlChannel：B 在另一个实例上发 stop，A 的 run 必须
// 退出并写入 stopped 终态帧（spec §11.3、§7.3）。
func caseCrossInstanceStopViaControlChannel(
	t *testing.T, rig *crossInstanceRig, _, svcB *agent.AgentService, executionID string,
) {
	subB, err := svcB.OpenStreamSubscription(rig.ctx, crossInstanceAgentID,
		agent.ExecRequest{Query: "hi", UserID: crossInstanceOwner},
		agent.ExecMeta{TenantID: rig.tenantID, ExecutionID: executionID},
		crossInstanceSubConfig())
	if err != nil {
		t.Fatalf("B OpenStreamSubscription: %v", err)
	}
	defer subB.Close()

	// 先确认 B 是 attach 而不是另起了一个 runner——否则下面的 stopped 可能来自 B 自己
	// 启动的那次执行，用例就失去了跨实例语义。
	if n := atomic.LoadInt64(&rig.llmCalls); n != 1 {
		t.Fatalf("LLM calls = %d before the stop, want 1 (B must attach, not regenerate)", n)
	}
	if err := svcB.StopExecution(rig.ctx, rig.tenantID, executionID, crossInstanceOwner); err != nil {
		t.Fatalf("StopExecution: %v", err)
	}
	// 上限 5s 而非 brief 的 1s：终态帧要等订阅侧一轮 Tail（本用例已压到 50ms）加上
	// -race 下的调度抖动，1s 是「通常够快」而不是「确定性成立」。
	if event := collectTerminalEvent(subB, crossInstanceCancelTimeout); event != port.StreamEventStopped {
		t.Fatalf("terminal frame = %q, want %q", event, port.StreamEventStopped)
	}
	waitCancelled(t, rig, crossInstanceCancelTimeout, "stop must cancel the run that owns the lease")
	if n := atomic.LoadInt64(&rig.llmCalls); n != 1 {
		t.Fatalf("LLM calls = %d after the stop, want 1 (stop must not respawn the run)", n)
	}
}

// caseCrossInstanceLeaseConflictYieldsSingleRunner：两个实例以同一 expect 并发
// ClaimLease，恰好一个成功、另一个 ErrLeaseConflict——这是互斥的唯一来源（spec §11.3、D2）。
func caseCrossInstanceLeaseConflictYieldsSingleRunner(
	t *testing.T, rig *crossInstanceRig, _, _ *agent.AgentService, executionID string,
) {
	// NEW 路径的 run_generation 恒为 1：writeInitialCheckpoint 写 RunGeneration: 1，
	// 而 StampLease 刻意不推进分代（它只盖租约、返回当前分代）。
	const expect = 1

	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := rig.leaseStore.ClaimLease(rig.ctx, rig.tenantID, executionID, expect, time.Minute)
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	won, conflicted := 0, 0
	for err := range results {
		switch {
		case err == nil:
			won++
		case errors.Is(err, port.ErrLeaseConflict):
			conflicted++
		default:
			t.Fatalf("ClaimLease returned an unexpected error: %v", err)
		}
	}
	// 谁赢是随机的，赢几个不是：这是确定性的计数断言，不是竞态赌注。
	if won != 1 || conflicted != 1 {
		t.Fatalf("claims: %d won, %d conflicted; want exactly one winner", won, conflicted)
	}
	status, err := rig.leaseStore.LeaseStatus(rig.ctx, rig.tenantID, executionID)
	if err != nil {
		t.Fatalf("LeaseStatus after the claim: %v", err)
	}
	if status.Generation != expect+1 {
		t.Fatalf("generation after the winning claim = %d, want %d", status.Generation, expect+1)
	}
}

// caseCrossInstanceStaleRenewDoesNotResurrect：被抢占的 runner 续租必须得到
// ErrLeaseConflict，心跳据此自取消（spec §11.3、D3）。
func caseCrossInstanceStaleRenewDoesNotResurrect(
	t *testing.T, rig *crossInstanceRig, _, _ *agent.AgentService, executionID string,
) {
	// 抢占：generation 1 → 2，A 的 runner 即刻成为僵尸。
	if _, err := rig.leaseStore.ClaimLease(rig.ctx, rig.tenantID, executionID, 1, time.Minute); err != nil {
		t.Fatalf("preempting ClaimLease: %v", err)
	}
	if err := rig.leaseStore.RenewLease(rig.ctx, rig.tenantID, executionID, 1, time.Minute); !errors.Is(err, port.ErrLeaseConflict) {
		t.Fatalf("stale RenewLease = %v, want ErrLeaseConflict", err)
	}
	// 自取消必须由心跳驱动（LeaseRenew = 50ms），而不是等到 30s 的孤儿超时——
	// 等待上限 5s 把这条区分钉死。
	waitCancelled(t, rig, crossInstanceCancelTimeout, "a fenced runner must cancel itself on the next renew tick")
	if n := atomic.LoadInt64(&rig.llmCalls); n != 1 {
		t.Fatalf("LLM calls = %d, want 1 (a fenced runner must not restart)", n)
	}
}

// caseCrossInstanceStreamsDoNotInterleave：gen=1 与 gen=2 的流内容互不出现——
// 分代 key 的物理保证（spec §11.3、§6.1）。
func caseCrossInstanceStreamsDoNotInterleave(
	t *testing.T, rig *crossInstanceRig, _, _ *agent.AgentService, _ string,
) {
	// 用专属 execution_id 而不是驱动的那条：驱动那条流里混有 runner 自己的
	// meta / token 帧，而本用例断言的是分代 key 的隔离，不该把 run 的产出算进来。
	const (
		subject     = "exec-cross-instance-generations"
		gen1Payload = `{"token":"gen-one-only"}`
		gen2Payload = `{"token":"gen-two-only"}`
	)

	for generation, payload := range map[int]string{1: gen1Payload, 2: gen2Payload} {
		if _, err := rig.streamStore.Append(rig.ctx, subject, generation,
			port.StreamEntry{Event: port.StreamEventToken, Payload: payload}); err != nil {
			t.Fatalf("append gen %d: %v", generation, err)
		}
	}

	got1 := replayPayloads(t, rig, subject, 1)
	if len(got1) != 1 || got1[0] != gen1Payload {
		t.Fatalf("gen 1 replay = %v, want exactly [%s]", got1, gen1Payload)
	}
	got2 := replayPayloads(t, rig, subject, 2)
	if len(got2) != 1 || got2[0] != gen2Payload {
		t.Fatalf("gen 2 replay = %v, want exactly [%s]", got2, gen2Payload)
	}
}

// caseCrossTenantExecutionIDIsNotFound：另一租户对同一 execution_id 的订阅返回
// ErrNotFound（spec §11.3、§9）。
//
// 判别力（裁定 7.4）：D4 闸门 `cp == nil || req.UserID == "" || cp.UserID != req.UserID`
// 的三条析取项都给出同一个 ErrNotFound。为了证明这里的 404 来自**租户边界**而不是
// 「空身份」，本用例先把另外两条钉死不成立：
//
//	① 归属租户查得到该行，且 cp.UserID 严格等于调用身份（cp != nil 且身份非空）；
//	② 同一身份在归属租户续传成功（非 404）。
//
// 两条都成立后，唯一还能命中的失败前置条件就只剩「另一租户下 cp == nil」。
func caseCrossTenantExecutionIDIsNotFound(
	t *testing.T, rig *crossInstanceRig, _, svcB *agent.AgentService, executionID string,
) {
	cp, err := rig.leaseStore.GetLatest(rig.ctx, rig.tenantID, executionID)
	if err != nil {
		t.Fatalf("GetLatest in the owning tenant: %v", err)
	}
	if cp == nil {
		t.Fatal("owning tenant cannot see the checkpoint: setup is broken")
	}
	if cp.UserID != crossInstanceOwner {
		t.Fatalf("checkpoint owner = %q, want %q", cp.UserID, crossInstanceOwner)
	}

	// 对照组：同一非空身份在归属租户续传必须成功——用它排掉「因空身份而 404」。
	owningSub, err := svcB.OpenStreamSubscription(rig.ctx, crossInstanceAgentID,
		agent.ExecRequest{Query: "hi", UserID: crossInstanceOwner},
		agent.ExecMeta{TenantID: rig.tenantID, ExecutionID: executionID},
		crossInstanceSubConfig())
	if err != nil {
		t.Fatalf("owning-tenant resume with the same non-empty user must succeed, got %v", err)
	}
	owningSub.Close()

	// 实验组：另一租户（schema 真建过）以同一非空身份续传同一 execution_id，必须 404。
	other := rig.provisionForeignTenant()
	foreignCtx := postgres.WithTenant(context.Background(),
		&postgres.TenantContext{TenantID: other, UserID: crossInstanceOwner})
	_, err = svcB.OpenStreamSubscription(foreignCtx, crossInstanceAgentID,
		agent.ExecRequest{Query: "hi", UserID: crossInstanceOwner},
		agent.ExecMeta{TenantID: other, ExecutionID: executionID},
		crossInstanceSubConfig())
	if !errors.Is(err, agent.ErrNotFound) {
		t.Fatalf("cross-tenant resume = %v, want ErrNotFound", err)
	}
}

// caseOrphanRunCancelsAfterTimeout：无 viewer 且超过 OrphanTimeout（本用例注入
// 200ms）后 run 被取消，checkpoint 仍存在（spec §11.3、§7.1）。
//
// 「没有 viewer」由驱动保证：run 经 ExecuteStream 起，全程没有订阅，因此不会有心跳
// 上报 viewer；runner 的孤儿计时从心跳循环启动起就一直累积。
func caseOrphanRunCancelsAfterTimeout(
	t *testing.T, rig *crossInstanceRig, _, _ *agent.AgentService, executionID string,
) {
	waitCancelled(t, rig, crossInstanceCancelTimeout,
		"a run with no viewer must be cancelled once OrphanTimeout elapses")
	cp, err := rig.leaseStore.GetLatest(rig.ctx, rig.tenantID, executionID)
	if err != nil {
		t.Fatalf("GetLatest after the orphan cancel: %v", err)
	}
	if cp == nil {
		t.Fatal("checkpoint row disappeared after the orphan cancel; the row is the resume key")
	}
	if n := atomic.LoadInt64(&rig.llmCalls); n != 1 {
		t.Fatalf("LLM calls = %d, want 1", n)
	}
}

// firstMetaExecutionID 读出订阅的第一条 meta 帧并返回其中的 execution_id。
// meta 帧由 runner 建立时写入，必然最先到；取到即返回，不关订阅——
// 调用方负责 Close，用例需要继续观察这条流。
func firstMetaExecutionID(t *testing.T, sub *agent.ExecutionSubscription) string {
	t.Helper()
	deadline := time.After(crossInstanceStartTimeout)
	seen := make([]string, 0, 8)
	for {
		select {
		case frame, ok := <-sub.Frames():
			if !ok {
				t.Fatalf("subscription closed before the meta frame; frames seen: %v", seen)
			}
			seen = append(seen, describeFrame(frame))
			if frame.Event != port.StreamEventMeta {
				continue
			}
			var payload struct {
				ExecutionID string `json:"execution_id"`
			}
			if err := json.Unmarshal([]byte(frame.Data), &payload); err != nil {
				t.Fatalf("unmarshal meta frame: %v", err)
			}
			if payload.ExecutionID == "" {
				t.Fatal("meta frame carries an empty execution_id")
			}
			return payload.ExecutionID
		case <-deadline:
			t.Fatalf("timed out waiting for the meta frame; frames seen: %v", seen)
		}
	}
}

// describeFrame 把一帧压成排障用的短标签（排障信息进 Fatal 消息，不靠日志）。
func describeFrame(frame agent.StreamFrame) string {
	switch {
	case frame.Comment != "":
		return "#" + frame.Comment
	case frame.Event == "":
		return "<unnamed>"
	default:
		return frame.Event + ":" + frame.Data
	}
}

// collectUntilTerminal 排空订阅直到终态帧，返回是否收到终态帧。
func collectUntilTerminal(t *testing.T, sub *agent.ExecutionSubscription) bool {
	t.Helper()
	deadline := time.After(crossInstanceTerminalTimeout)
	for {
		select {
		case frame, ok := <-sub.Frames():
			if !ok {
				return false
			}
			if port.IsTerminalStreamEvent(frame.Event) {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// collectTerminalEvent 排空订阅直到终态帧并返回其事件名；关流或超时返回 ""。
// 与 collectUntilTerminal 的唯一差别是「要不要那个事件名」——失败路径用例靠事件名
// 区分 stopped 与 done / error。
func collectTerminalEvent(sub *agent.ExecutionSubscription, within time.Duration) string {
	deadline := time.After(within)
	for {
		select {
		case frame, ok := <-sub.Frames():
			if !ok {
				return ""
			}
			if port.IsTerminalStreamEvent(frame.Event) {
				return frame.Event
			}
		case <-deadline:
			return ""
		}
	}
}

// collectUntilToken 读到 token == want 的帧返回 (首帧事件名, true)；终态 / 关流 /
// 超时返回 (首帧事件名, false)。
//
// 载荷形状：token 帧的 data 是 {"token":"..."}（executeStreamRun 的 tokenCb 经
// snapshotStreamFrame 序列化），因此比对的是解析后的 token 字段，不是整段 data。
//
// 首帧事件名是「B 实际走的 plan」的观测证据：reset ⇒ PlanStream 判定
// genClient != genNow，走 reset + 全量回放；meta / token ⇒ 直接回放。
// 这里刻意只记录、不硬断言，是为了不把 PlanStream 的当前分支写死进承重墙用例；
// TAIL 的专项覆盖在 TestResumeAtTailDoesNotReplayStream。
func collectUntilToken(
	t *testing.T, sub *agent.ExecutionSubscription, want string,
) (firstEvent string, sawToken bool) {
	t.Helper()
	seenFrame := false
	deadline := time.After(crossInstanceTerminalTimeout)
	for {
		select {
		case frame, ok := <-sub.Frames():
			if !ok {
				return firstEvent, false
			}
			if !seenFrame {
				firstEvent, seenFrame = frame.Event, true
			}
			if frame.Event == port.StreamEventToken && decodeToken(t, frame.Data) == want {
				return firstEvent, true
			}
			if port.IsTerminalStreamEvent(frame.Event) {
				return firstEvent, false
			}
		case <-deadline:
			return firstEvent, false
		}
	}
}

// decodeToken 解出 token 帧载荷里的 token 字段。
func decodeToken(t *testing.T, payload string) string {
	t.Helper()
	var decoded struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		t.Fatalf("decode token frame %q: %v", payload, err)
	}
	return decoded.Token
}

// replayPayloads 回放某分代的全部载荷，用于断言分代隔离。
func replayPayloads(t *testing.T, rig *crossInstanceRig, executionID string, generation int) []string {
	t.Helper()
	entries, err := rig.streamStore.Replay(rig.ctx, executionID, generation)
	if err != nil {
		t.Fatalf("replay gen %d: %v", generation, err)
	}
	payloads := make([]string, 0, len(entries))
	for _, e := range entries {
		payloads = append(payloads, e.Payload)
	}
	return payloads
}

// waitCancelled 等待 runner 的 run 观察到 ctx 取消。它是「run 真的被停掉」最直接的
// 观测：runFn 只在 ctx.Done() 分支关闭该 channel，比任何计时断言都稳。
func waitCancelled(t *testing.T, rig *crossInstanceRig, within time.Duration, reason string) {
	t.Helper()
	select {
	case <-rig.cancelled:
	case <-time.After(within):
		t.Fatalf("the run was not cancelled within %v: %s", within, reason)
	}
}

// TestResumeAtTailDoesNotReplayStream 覆盖「客户端已追平流尾后重连」这条真实链路
// （F5 刷新最常见的那一刻）：此刻流中没有新条目，若续传游标被规范化成空串／"0-0"，
// 订阅会从流头重读并把整条流重复下发。
//
// 这条断言必须落在真实 AgentStreamStore 上：游标规范化发生在
// pkg/storage/redis/stream.go 的 Tail（afterID == "" → "0-0"），Task 12 的单测用
// fakeStreamStore，假 store 自己实现 Tail、不经过这层规范化，守护不到真实链路。
//
// 判据是真实 entry ID 集合，不是帧数、也不是 sleep：首帧之后收到的每一条流帧的 id
// 都必须严格大于追平时的游标。
func TestResumeAtTailDoesNotReplayStream(t *testing.T) {
	rig := newCrossInstanceRig(t, crossInstanceRunnerConfig(0))
	svc := rig.newService()
	defer svc.ShutdownStreamRunners()

	const subject = "exec-tail-resume"
	entries := []port.StreamEntry{
		{Event: port.StreamEventMeta, Payload: `{"execution_id":"exec-tail-resume","generation":1}`},
		{Event: port.StreamEventToken, Payload: `{"token":"alpha"}`},
		{Event: port.StreamEventToken, Payload: `{"token":"beta"}`},
	}
	cursor := ""
	for _, e := range entries {
		id, err := rig.streamStore.Append(rig.ctx, subject, 1, e)
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		cursor = id // 客户端游标 = 已渲染到的最后一条
	}
	if cursor == "" {
		t.Fatal("stream is empty: the setup did not produce a cursor")
	}

	// 以追平后的游标续传：此刻没有新条目，游标必须原样传下去。
	plan := agent.PlanStream(1, 1, cursor)
	if plan.Reset || plan.AfterID != cursor {
		t.Fatalf("plan = %+v, want no reset and AfterID %q", plan, cursor)
	}
	replayed, err := rig.streamStore.ReplayAfter(rig.ctx, subject, 1, plan.AfterID)
	if err != nil {
		t.Fatalf("ReplayAfter: %v", err)
	}
	if len(replayed) != 0 {
		t.Fatalf("replay after the tail cursor returned %d entries, want 0 (no re-delivery)", len(replayed))
	}

	// 追平状态下若把游标丢掉，规范化会让 Tail 从流头重读——这条断言就是本用例存在的
	// 理由：同一条真实链路上，"0-0" 会整条重放，而真实游标不会。
	fromHead, err := rig.streamStore.ReplayAfter(rig.ctx, subject, 1, "")
	if err != nil {
		t.Fatalf("ReplayAfter from head: %v", err)
	}
	if len(fromHead) != len(entries) {
		t.Fatalf("replay from head returned %d entries, want %d", len(fromHead), len(entries))
	}

	tailed, err := rig.streamStore.Tail(rig.ctx, subject, 1, plan.AfterID, 20)
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	for _, e := range tailed {
		if !streamIDAfter(e.ID, cursor) {
			t.Fatalf("tail re-delivered entry %s at or before the cursor %s", e.ID, cursor)
		}
	}
}

// streamIDAfter 判定 Redis entry ID a 是否严格晚于 b（格式 "<ms>-<seq>"）。
//
// 不能用字符串比较：Redis 的 seq 是十进制不做零填充，"100-9" 字典序大于 "100-10"，
// 而实际顺序相反。按 (ms, seq) 数值比较才是 ID 的真实全序。
func streamIDAfter(a, b string) bool {
	aMS, aSeq, okA := parseStreamID(a)
	bMS, bSeq, okB := parseStreamID(b)
	if !okA || !okB {
		return false
	}
	if aMS != bMS {
		return aMS > bMS
	}
	return aSeq > bSeq
}

func parseStreamID(id string) (ms, seq int64, ok bool) {
	i := strings.IndexByte(id, '-')
	if i < 0 {
		return 0, 0, false
	}
	ms, err := strconv.ParseInt(id[:i], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	seq, err = strconv.ParseInt(id[i+1:], 10, 64)
	if err != nil {
		return 0, 0, false
	}
	return ms, seq, true
}
