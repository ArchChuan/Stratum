package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

// errNoCheckpointRows 是 PgCheckpointStore.StampLease 缺行错误的本地同义词。
// 刻意不 import pgx：端口契约只承诺「缺行即失败」，用例不该绑死驱动实现。
var errNoCheckpointRows = errors.New("checkpoint_store: stamp lease: no checkpoint")

// newExecutionRig 是 NEW 路径的近真实租约语义装置：checkpoint 行与租约共享一份
// 状态，StampLease 只有在同一 executionID 先被 Upsert 过才成功——与 PG 上
// 「UPDATE ... RETURNING 缺行即 ErrNoRows」的契约一致。恒成功的 fakeLeaseRepo
// 结构上发现不了「先盖章后建行」，C-1 正是这样漏网的。
type executionRig struct {
	mu        sync.Mutex
	calls     []string
	records   map[string]bool
	upsertErr error
	stampErr  error
}

func newExecutionRig() *executionRig {
	return &executionRig{records: map[string]bool{}}
}

func (r *executionRig) record(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, name)
}

func (r *executionRig) callSequence() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *executionRig) stampCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(slices.DeleteFunc(append([]string(nil), r.calls...), func(c string) bool {
		return c != "stamp"
	}))
}

func (r *executionRig) lease() *contractLeaseRepo {
	return &contractLeaseRepo{rig: r}
}

func (r *executionRig) checkpoints() *contractCheckpointStore {
	return &contractCheckpointStore{rig: r}
}

// contractLeaseRepo 只实现 NEW 路径真正会走到的三个方法，其余保持中立返回值。
type contractLeaseRepo struct{ rig *executionRig }

func (f *contractLeaseRepo) StampLease(_ context.Context, _, executionID string, _ time.Duration) (int, error) {
	f.rig.record("stamp")
	f.rig.mu.Lock()
	defer f.rig.mu.Unlock()
	if f.rig.stampErr != nil {
		return 0, f.rig.stampErr
	}
	if !f.rig.records[executionID] {
		return 0, errNoCheckpointRows
	}
	return 1, nil
}

func (f *contractLeaseRepo) ClaimLease(context.Context, string, string, int, time.Duration) (int, error) {
	return 0, port.ErrLeaseConflict
}

func (f *contractLeaseRepo) RenewLease(context.Context, string, string, int, time.Duration) error {
	return nil
}

func (f *contractLeaseRepo) ReleaseLease(context.Context, string, string, int) error {
	f.rig.record("release")
	return nil
}

func (f *contractLeaseRepo) LeaseStatus(context.Context, string, string) (port.LeaseStatus, error) {
	return port.LeaseStatus{}, nil
}

// contractCheckpointStore 记录 Upsert 并把「行已存在」写回共享装置；其余方法
// 继承「查不到」替身（NEW 路径不会发起租户限定查询）。
type contractCheckpointStore struct {
	streamNoopCheckpointStore
	rig *executionRig
}

func (s *contractCheckpointStore) Upsert(
	_ context.Context, _ string, cp domain.AgentExecutionCheckpoint,
) error {
	s.rig.record("upsert")
	s.rig.mu.Lock()
	defer s.rig.mu.Unlock()
	if s.rig.upsertErr != nil {
		return s.rig.upsertErr
	}
	s.rig.records[cp.ExecutionID] = true
	return nil
}

// ctxAwareStreamStore 记录「用已取消的 ctx 写流」这一事件。终态帧必须在 run
// 被取消之后仍然写得进去，因此它用的 ctx 不能是随 run 取消的那条（F2 第 4 点）。
// 恒忽略 ctx 的 fakeStreamStore 结构上无法发现这个回归。
type ctxAwareStreamStore struct {
	fakeStreamStore
	mu                  sync.Mutex
	canceledCtxAppended []string
}

func (s *ctxAwareStreamStore) Append(
	ctx context.Context, executionID string, generation int, e port.StreamEntry,
) (string, error) {
	if ctx.Err() != nil {
		s.mu.Lock()
		s.canceledCtxAppended = append(s.canceledCtxAppended, e.Event)
		s.mu.Unlock()
	}
	return s.fakeStreamStore.Append(ctx, executionID, generation, e)
}

func (s *ctxAwareStreamStore) appendedWithCanceledCtx() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.canceledCtxAppended...)
}

// streamControlBus 让用例从「用户点停止」这条真实入口投递取消，而不是直接调
// cancelRun——被绕过的入口正是 C-2 的成因。
type streamControlBus struct {
	mu   sync.Mutex
	subs []chan port.ControlMessage
}

var _ port.AgentControlBus = (*streamControlBus)(nil)

func (b *streamControlBus) PublishStop(context.Context, string) error           { return nil }
func (b *streamControlBus) PublishViewer(context.Context, string, string) error { return nil }
func (b *streamControlBus) Subscribe(context.Context, string) (<-chan port.ControlMessage, func(), error) {
	ch := make(chan port.ControlMessage, 4)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch, func() {}, nil
}

func (b *streamControlBus) subscriberCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.subs)
}

// publisher 返回当前订阅通道；runner 在 ExecuteStream 返回前完成订阅。
func (b *streamControlBus) publisher() chan port.ControlMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.subs) == 0 {
		return nil
	}
	return b.subs[len(b.subs)-1]
}

// runnerLeaseRepo 观测 runner 的租约生命周期：释放时刻、释放时是否仍有续租在飞、
// 以及是否有续租在释放之后才提交（租约复活，I-1）。
type runnerLeaseRepo struct {
	status   port.LeaseStatus
	claimGen int
	// renewGate 非 nil 时 RenewLease 阻塞到它关闭：把「心跳正在飞」做成可复现
	// 状态，而不是靠 sleep 撞竞态。
	renewGate    <-chan struct{}
	renewEntered chan struct{}
	releasedCh   chan struct{}

	mu                    sync.Mutex
	released              bool
	releaseCalls          int
	renewCalls            int
	inFlight              int
	inFlightAtRelease     int
	revivedAfterRelease   bool
	releasedWithCanceledC bool
}

var _ port.ExecutionLeaseRepo = (*runnerLeaseRepo)(nil)

func newRunnerLeaseRepo(status port.LeaseStatus, claimGen int) *runnerLeaseRepo {
	return &runnerLeaseRepo{
		status:       status,
		claimGen:     claimGen,
		renewEntered: make(chan struct{}, 1),
		releasedCh:   make(chan struct{}, 1),
	}
}

func (f *runnerLeaseRepo) StampLease(context.Context, string, string, time.Duration) (int, error) {
	return 1, nil
}

func (f *runnerLeaseRepo) ClaimLease(context.Context, string, string, int, time.Duration) (int, error) {
	return f.claimGen, nil
}

func (f *runnerLeaseRepo) LeaseStatus(context.Context, string, string) (port.LeaseStatus, error) {
	return f.status, nil
}

func (f *runnerLeaseRepo) RenewLease(context.Context, string, string, int, time.Duration) error {
	f.mu.Lock()
	f.renewCalls++
	f.inFlight++
	f.mu.Unlock()
	f.renewEntered <- struct{}{}
	if f.renewGate != nil {
		<-f.renewGate
	}
	f.mu.Lock()
	f.inFlight--
	if f.released {
		f.revivedAfterRelease = true
	}
	f.mu.Unlock()
	return nil
}

func (f *runnerLeaseRepo) ReleaseLease(ctx context.Context, _, _ string, _ int) error {
	f.mu.Lock()
	f.released = true
	f.releaseCalls++
	f.inFlightAtRelease = f.inFlight
	f.releasedWithCanceledC = ctx.Err() != nil
	f.mu.Unlock()
	f.releasedCh <- struct{}{}
	return nil
}

func (f *runnerLeaseRepo) wasReleasedWithCanceledCtx() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.releasedWithCanceledC
}

func (f *runnerLeaseRepo) releaseCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.releaseCalls
}

func (f *runnerLeaseRepo) wasRevivedAfterRelease() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.revivedAfterRelease
}

func (f *runnerLeaseRepo) renewsInFlightAtRelease() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inFlightAtRelease
}

// blockingPromptResolver 在 a.Execute 的最内层阻塞到 ctx 取消：它是「取消是否
// 抵达执行」唯一可靠的探针——探针必须在 execCtx 被创建之后、且不依赖任何 LLM
// 依赖就能停下。
type blockingPromptResolver struct{ entered chan context.Context }

func (r *blockingPromptResolver) ResolvePlatform(ctx context.Context, _ string) (any, bool, error) {
	r.entered <- ctx
	<-ctx.Done()
	return nil, false, ctx.Err()
}

func waitForContext(t *testing.T, entered chan context.Context, store *fakeStreamStore, what string) context.Context {
	t.Helper()
	select {
	case ctx := <-entered:
		return ctx
	case <-time.After(5 * time.Second):
		t.Fatalf("%s：run 未进入执行；流内容=%+v", what, store.snapshot())
		return nil
	}
}

// waitForStreamEvent 轮询等待某类帧出现，返回该帧。
func waitForStreamEvent(t *testing.T, store *fakeStreamStore, event string) port.StreamEntry {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range store.snapshot() {
			if e.Event == event {
				return e
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("流中没有出现 %q 帧；已写出=%+v", event, store.snapshot())
	return port.StreamEntry{}
}

// F1：NEW 路径必须先写 init checkpoint 再盖章。真实 PG 上 StampLease 是纯 UPDATE，
// 缺行即失败，而 checkpoint 行的唯一创建者在 runner 内部——先盖章会让新会话
// 100% 失败。本用例用按真实契约行为的替身把这个顺序钉死。
func TestExecuteStreamNewExecutionCreatesCheckpointBeforeStampingLease(t *testing.T) {
	rig := newExecutionRig()
	store := &fakeStreamStore{}
	bus := &streamControlBus{}
	svc := NewAgentService(AgentServiceDeps{
		StreamStore:     store,
		ControlBus:      bus,
		LeaseRepo:       rig.lease(),
		CheckpointStore: rig.checkpoints(),
		StreamRunFn: func(context.Context, string, ExecRequest, ExecMeta, func(string)) (*domain.AgentResult, int, error) {
			return &domain.AgentResult{Output: "ok"}, 1, nil
		},
	})

	handle, err := svc.ExecuteStream(context.Background(), "agent-1", ExecRequest{Query: "hi"},
		ExecMeta{TenantID: "t1"})
	if err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	if handle.Generation != 1 {
		t.Fatalf("generation = %d, want 1", handle.Generation)
	}
	if got := rig.callSequence(); !slices.Equal(got, []string{"upsert", "stamp"}) {
		t.Fatalf("调用序 = %v, want [upsert stamp]（先建行后盖章）", got)
	}
	if bus.subscriberCount() != 1 {
		t.Fatalf("控制通道订阅 %d 次, want 1（runner 必须已启动）", bus.subscriberCount())
	}
}

// F1：init checkpoint 写失败必须整条请求 fail closed——绝不 launch，也不得
// 继续盖章。否则缺行的租约会在真实 PG 上抛错，错误只会更晚、更难定位。
func TestExecuteStreamFailsClosedWhenInitialCheckpointWriteFails(t *testing.T) {
	rig := newExecutionRig()
	rig.upsertErr = errors.New("checkpoint store down")
	store := &fakeStreamStore{}
	bus := &streamControlBus{}
	svc := NewAgentService(AgentServiceDeps{
		StreamStore:     store,
		ControlBus:      bus,
		LeaseRepo:       rig.lease(),
		CheckpointStore: rig.checkpoints(),
		StreamRunFn: func(context.Context, string, ExecRequest, ExecMeta, func(string)) (*domain.AgentResult, int, error) {
			t.Error("init checkpoint 写入失败后不得启动 runner")
			return &domain.AgentResult{}, 0, nil
		},
	})

	_, err := svc.ExecuteStream(context.Background(), "agent-1", ExecRequest{Query: "hi"},
		ExecMeta{TenantID: "t1"})
	if err == nil {
		t.Fatal("init checkpoint 写入失败必须返回错误")
	}
	if !strings.Contains(err.Error(), "init checkpoint") {
		t.Fatalf("错误必须指向 init checkpoint 写入：%v", err)
	}
	if got := rig.stampCalls(); got != 0 {
		t.Fatalf("StampLease 调用 %d 次, want 0（建行失败不得盖章）", got)
	}
	if got := bus.subscriberCount(); got != 0 {
		t.Fatalf("控制通道订阅 %d 次, want 0（不得启动 runner）", got)
	}
}

// F2(a)：控制通道收到的取消必须抵达 a.Execute 的 ctx。这正是 C-2：旧实现用
// context.WithoutCancel 剪断取消链，心跳 CAS 失败 / 孤儿超时 / 控制通道 stop /
// runnerSet.CancelAll 四个取消源对正在执行的 run 全部是 no-op。
//
// 同时覆盖 F2 第 4 点（controller 补充发现）：终态帧与 ReleaseLease 必须仍然
// 落盘——取消之后才写终态帧，写它用的 ctx 因此不能是 runCtx。
func TestStreamRunnerCancelFromControlChannelReachesExecution(t *testing.T) {
	resolver := &blockingPromptResolver{entered: make(chan context.Context, 1)}
	registry := NewRegistry(systemAssistantProfileRepo{cfgs: []*domain.AgentConfig{{
		ID: "a1", Name: "Runner Agent", Type: domain.ReActAgent,
		SystemPrompt: "sys", LLMModel: "qwen-plus", MaxIterations: 2,
	}}}, zap.NewNop())
	registry.SetPlatformPromptResolver(resolver)

	store := &ctxAwareStreamStore{}
	bus := &streamControlBus{}
	lease := newRunnerLeaseRepo(port.LeaseStatus{Generation: 1}, 2)
	svc := NewAgentService(AgentServiceDeps{
		Registry:             registry,
		TenantModelValidator: &stubTenantModelValidator{},
		CheckpointStore:      streamResumableCheckpointStore{},
		StreamStore:          store,
		ControlBus:           bus,
		LeaseRepo:            lease,
		StreamRunnerCfg:      longRunningStreamCfg(),
		Logger:               zap.NewNop(),
		TenantResolver:       tenantResolverFake{},
		MCPTools:             fullChainMCPTools{},
		ApprovalService:      nil,
		ChatStore:            resumeChatRepo{conv: &domain.ChatConversation{ID: "conv-1"}},
		TenantRoleResolver:   stubTenantRole{role: "member"},
		ToolAuthorizer:       NewToolAuthorizer(stubToolUserScopeResolver{scope: port.ToolUserScope{UserActive: true, AllowsTool: true}}),
	})

	if _, err := svc.ExecuteStream(context.Background(), "a1",
		ExecRequest{Query: "hi", UserID: "u1", ConversationID: "conv-1"},
		ExecMeta{TenantID: "t1", ExecutionID: "e1", Generation: 1}); err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	execCtx := waitForContext(t, resolver.entered, &store.fakeStreamStore, "a.Execute 探针")

	publisher := bus.publisher()
	if publisher == nil {
		t.Fatal("runner 尚未订阅控制通道")
	}
	publisher <- port.ControlMessage{Type: port.ControlMessageCancel}

	select {
	case <-execCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatalf("控制通道的取消没有抵达 a.Execute；已写出=%+v", store.snapshot())
	}

	stopped := waitForStreamEvent(t, &store.fakeStreamStore, port.StreamEventStopped)
	if stopped.Payload != `{"done":true,"stopped":true}` {
		t.Fatalf("stopped 载荷 = %s, want {\"done\":true,\"stopped\":true}", stopped.Payload)
	}
	<-lease.releasedCh

	// F2 第 4 点：终态帧与 ReleaseLease 必须用**未随 run 取消**的 ctx。写它们时
	// runCtx 已 Done，若改用 runCtx，帧写不进流、租约也不释放，重连的 viewer
	// 只会在末尾悬挂到孤儿超时。
	if got := store.appendedWithCanceledCtx(); len(got) != 0 {
		t.Fatalf("用已取消的 ctx 写出的帧：%v（终态帧应与 run 的取消解耦）", got)
	}
	if lease.wasReleasedWithCanceledCtx() {
		t.Fatal("ReleaseLease 用了已取消的 ctx：租约释放会失败")
	}
}

// F2(b)：入参 ctx 取消（HTTP 断线）不得终止 run——脱钩必须保留。它与 (a) 构成
// 双向守卫，缺一不可：只守 (a) 会诱导实现把脱钩整个删掉。
func TestStreamRunnerSurvivesRequestContextCancellation(t *testing.T) {
	runCtxCh := make(chan context.Context, 1)
	allowFinish := make(chan struct{})
	store := &fakeStreamStore{}
	svc := NewAgentService(AgentServiceDeps{
		StreamStore:     store,
		ControlBus:      &streamControlBus{},
		LeaseRepo:       newRunnerLeaseRepo(port.LeaseStatus{Generation: 1}, 2),
		CheckpointStore: streamResumableCheckpointStore{},
		StreamRunnerCfg: longRunningStreamCfg(),
		StreamRunFn: func(ctx context.Context, _ string, _ ExecRequest, _ ExecMeta, _ func(string)) (*domain.AgentResult, int, error) {
			runCtxCh <- ctx
			<-allowFinish
			return &domain.AgentResult{Output: "ok"}, 1, nil
		},
	})

	reqCtx, cancelReq := context.WithCancel(context.Background())
	if _, err := svc.ExecuteStream(reqCtx, "a1", ExecRequest{Query: "hi"},
		ExecMeta{TenantID: "t1", ExecutionID: "e1", Generation: 1}); err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	var runCtx context.Context
	select {
	case runCtx = <-runCtxCh:
	case <-time.After(5 * time.Second):
		t.Fatalf("run 未启动；已写出=%+v", store.snapshot())
	}

	cancelReq()
	select {
	case <-runCtx.Done():
		t.Fatal("HTTP 断线（入参 ctx 取消）不得终止 run")
	case <-time.After(50 * time.Millisecond):
	}

	close(allowFinish)
	done := waitForStreamEvent(t, store, port.StreamEventDone)
	if !strings.Contains(done.Payload, `"output":"ok"`) {
		t.Fatalf("done 载荷 = %s, want output=ok", done.Payload)
	}
}

// F3：心跳/控制消费 goroutine 必须先 join 再释放租约。否则心跳可能带着一次
// 在飞的 RenewLease 越过 ReleaseLease 提交，把已释放的租约复活（I-1）。
//
// 用例把「在飞」做成确定状态：RenewLease 阻塞在 gate 上，run 已结束而 join
// 未完成时释放租约即判定失败。
func TestStreamRunnerJoinsHeartbeatBeforeReleasingLease(t *testing.T) {
	gate := make(chan struct{})
	lease := newRunnerLeaseRepo(port.LeaseStatus{Generation: 1}, 2)
	lease.renewGate = gate
	runStarted := make(chan struct{})
	allowFinish := make(chan struct{})
	store := &fakeStreamStore{}
	cfg := longRunningStreamCfg()
	cfg.LeaseRenew = time.Millisecond
	svc := NewAgentService(AgentServiceDeps{
		StreamStore:     store,
		ControlBus:      &streamControlBus{},
		LeaseRepo:       lease,
		CheckpointStore: streamResumableCheckpointStore{},
		StreamRunnerCfg: cfg,
		StreamRunFn: func(context.Context, string, ExecRequest, ExecMeta, func(string)) (*domain.AgentResult, int, error) {
			close(runStarted)
			<-allowFinish
			return &domain.AgentResult{Output: "ok"}, 1, nil
		},
	})

	if _, err := svc.ExecuteStream(context.Background(), "a1", ExecRequest{Query: "hi"},
		ExecMeta{TenantID: "t1", ExecutionID: "e1", Generation: 1}); err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	<-runStarted
	select {
	case <-lease.renewEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("心跳未发起续租，用例前提不成立")
	}
	close(allowFinish)

	// 修复前：run 结束即释放租约（心跳仍在飞）。修复后：join 先阻塞，只能走超时
	// 兜底放行——放行后心跳已经退出，不会再有续租越过释放点。
	released := false
	select {
	case <-lease.releasedCh:
		released = true
	case <-time.After(200 * time.Millisecond):
	}
	close(gate)
	if !released {
		select {
		case <-lease.releasedCh:
		case <-time.After(5 * time.Second):
			t.Fatal("run 结束后租约未释放")
		}
	}
	if lease.releaseCount() != 1 {
		t.Fatalf("ReleaseLease 调用 %d 次, want 1", lease.releaseCount())
	}
	if got := lease.renewsInFlightAtRelease(); got != 0 {
		t.Fatalf("ReleaseLease 时仍有 %d 个续租在飞：goroutine 未 join", got)
	}
	if lease.wasRevivedAfterRelease() {
		t.Fatal("心跳续租在租约释放之后才提交：租约被复活")
	}
	waitForStreamEvent(t, store, port.StreamEventDone)
}

// M-1（地雷）：部分填充的配置必须逐字段回落。整体判定会让 OrphanTimeout=0 /
// ViewerTimeout=0 直接生效，首个 tick 静默杀掉 run。
func TestStreamRunnerConfigFillsZeroFieldsPerField(t *testing.T) {
	def := DefaultStreamRunnerConfig()
	cases := []struct {
		name string
		in   StreamRunnerConfig
		want StreamRunnerConfig
	}{
		{name: "zero value falls back entirely", in: StreamRunnerConfig{}, want: def},
		{
			name: "partial fill keeps set fields and fills the rest",
			in:   StreamRunnerConfig{LeaseTTL: time.Minute, StreamMaxLen: 7},
			want: StreamRunnerConfig{
				LeaseTTL: time.Minute, LeaseRenew: def.LeaseRenew, ViewerTimeout: def.ViewerTimeout,
				OrphanTimeout: def.OrphanTimeout, StreamTTL: def.StreamTTL, StreamMaxLen: 7,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := NewAgentService(AgentServiceDeps{StreamRunnerCfg: tc.in})
			if got := svc.streamRunnerConfig(); got != tc.want {
				t.Fatalf("streamRunnerConfig() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// longRunningStreamCfg 是显式全字段配置：避免部分填充触发「零值即立即过期」
// 的归一化路径，让这些用例只考察取消/join 语义本身。
func longRunningStreamCfg() StreamRunnerConfig {
	return StreamRunnerConfig{
		LeaseTTL:      30 * time.Second,
		LeaseRenew:    time.Hour,
		ViewerTimeout: time.Minute,
		OrphanTimeout: time.Hour,
		StreamTTL:     time.Hour,
		StreamMaxLen:  20000,
	}
}
