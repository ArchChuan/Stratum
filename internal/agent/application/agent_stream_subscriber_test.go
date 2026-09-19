package application

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// 两条断言把「测试替身与冻结端口签名一致」变成编译期错误，防止端口漂移后
// 测试替身静默失配。
var (
	_ port.AgentStreamStore = (*fakeStreamStore)(nil)
	_ port.AgentControlBus  = (*fakeControlBus)(nil)
)

// fakeStreamStore 是订阅侧测试用的内存流。
type fakeStreamStore struct {
	mu      sync.Mutex
	entries []port.StreamEntry
	next    int
	block   time.Duration
	// tailCursors 记录每次 Tail 收到的游标，让「续传起点」能被确定性断言，
	// 而不必靠「等一段时间看有没有帧」的时间窗赌博。nil 时投递被静默丢弃。
	tailCursors chan string
	// replayCtxs 记录每次 ReplayAfter 收到的 ctx——这条路径是订阅的第一步存储
	// 调用，也是「调用方 ctx 的 Values 是否抵达存储层」的观测点。nil 时投递被
	// 静默丢弃（与 tailCursors 同型）。
	replayCtxs chan context.Context
}

func (f *fakeStreamStore) Append(
	_ context.Context, _ string, _ int, e port.StreamEntry,
) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	id := "1726483200000-" + strconv.Itoa(f.next)
	entry := e
	entry.ID = id
	f.entries = append(f.entries, entry)
	return id, nil
}

func (f *fakeStreamStore) Replay(_ context.Context, _ string, _ int) ([]port.StreamEntry, error) {
	return f.ReplayAfter(context.Background(), "", 0, "")
}

func (f *fakeStreamStore) ReplayAfter(ctx context.Context, _ string, _ int, afterID string) ([]port.StreamEntry, error) {
	// 非阻塞投递：容量满不阻塞被测代码；nil channel 走 default，安全。
	select {
	case f.replayCtxs <- ctx:
	default:
	}
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
	// 非阻塞投递：容量满不阻塞被测代码；nil channel 走 default，安全。
	select {
	case f.tailCursors <- afterID:
	default:
	}
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

// firstIDErrorStore 覆写 FirstID，让缺口检测走「查询失败 → fail closed」路径。
// 其余方法全部继承 fakeStreamStore。
type firstIDErrorStore struct{ *fakeStreamStore }

func (firstIDErrorStore) FirstID(context.Context, string, int) (string, error) {
	return "", errors.New("redis: connection refused")
}

// tailErrorStore 覆写 Tail，让跟流首轮即失败，用于验证失败被留痕且错误终态帧
// 照旧写出（加日志不得顶替既有控制流）。
type tailErrorStore struct{ *fakeStreamStore }

func (tailErrorStore) Tail(context.Context, string, int, string, int) ([]port.StreamEntry, error) {
	return nil, errors.New("redis: connection refused")
}

type fakeControlBus struct {
	mu   sync.Mutex
	msgs []port.ControlMessage
	subs []chan port.ControlMessage
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

// nextTailCursor 读取一次 Tail 收到的游标；超时视为失败（订阅没有跟流）。
func nextTailCursor(t *testing.T, cursors <-chan string) string {
	t.Helper()
	select {
	case c := <-cursors:
		return c
	case <-time.After(2 * time.Second):
		t.Fatal("Tail 在 2s 内未被调用")
		return ""
	}
}

// drainFrames 非阻塞地取走帧通道中此刻已缓冲的帧。
func drainFrames(sub *ExecutionSubscription) []StreamFrame {
	var got []StreamFrame
	for {
		select {
		case f, ok := <-sub.Frames():
			if !ok {
				return got
			}
			got = append(got, f)
		default:
			return got
		}
	}
}

func TestSubscriptionReplaysThenStopsAtTerminal(t *testing.T) {
	store := &fakeStreamStore{block: 5 * time.Millisecond}
	ctx := context.Background()
	_, _ = store.Append(ctx, "e1", 1, port.StreamEntry{
		Event: port.StreamEventMeta, Payload: `{"execution_id":"e1","generation":1}`,
	})
	_, _ = store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventToken, Payload: `{"token":"a"}`})
	_, _ = store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventDone, Payload: `{"done":true}`})

	sub := NewExecutionSubscription(ctx, ExecutionSubscriptionDeps{
		Stream: store, Control: &fakeControlBus{}, ExecutionID: "e1", Generation: 1,
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

	sub := NewExecutionSubscription(ctx, ExecutionSubscriptionDeps{
		Stream: store, Control: &fakeControlBus{}, ExecutionID: "e1", Generation: 1,
		Plan: StreamPlan{Generation: 1, AfterID: first},
		Cfg:  StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour, ReadBlock: 5},
	})
	defer sub.Close()

	got := collectFrames(t, sub, func(fs []StreamFrame) bool {
		return len(fs) > 0 && fs[len(fs)-1].Event == port.StreamEventDone
	})
	if len(got) != 2 || got[0].Data != `{"token":"b"}` {
		t.Fatalf("frames = %+v, want incremental replay starting at b", got)
	}
}

func TestSubscriptionEmitsResetFrameFirst(t *testing.T) {
	store := &fakeStreamStore{block: 5 * time.Millisecond}
	_, _ = store.Append(context.Background(), "e1", 2, port.StreamEntry{
		Event: port.StreamEventDone, Payload: `{"done":true}`,
	})

	sub := NewExecutionSubscription(context.Background(), ExecutionSubscriptionDeps{
		Stream: store, Control: &fakeControlBus{}, ExecutionID: "e1", Generation: 2,
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

// C1：客户端已追平流尾（增量回放批次为空）后重连，跟流必须从客户端游标续读。
// 断言的是机制本身——Tail 收到的游标——而不是「等一段时间看有没有帧」。
func TestSubscriptionResumesAfterCaughtUpWithoutReplay(t *testing.T) {
	store := &fakeStreamStore{block: 5 * time.Millisecond, tailCursors: make(chan string, 4)}
	ctx := context.Background()
	_, _ = store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventToken, Payload: `{"token":"a"}`})
	last, _ := store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventToken, Payload: `{"token":"b"}`})

	sub := NewExecutionSubscription(ctx, ExecutionSubscriptionDeps{
		Stream: store, Control: &fakeControlBus{}, ExecutionID: "e1", Generation: 1,
		Plan: StreamPlan{Generation: 1, AfterID: last},
		Cfg:  StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour, ReadBlock: 5},
	})
	defer sub.Close()

	if got := nextTailCursor(t, store.tailCursors); got != last {
		t.Fatalf("首个 Tail 游标 = %q, want %q（追平流尾后重连不得从流头重读）", got, last)
	}
	// 加固：等到第二轮 Tail，第一轮该写的帧必然已全部落入缓冲。修复前游标种子
	// 是 ""，Tail("") 会立刻回放整条流（token a/b），此处必然失败；修复后无新
	// 条目、一帧都没有。方向安全，不是概率断言。
	_ = nextTailCursor(t, store.tailCursors)
	if got := drainFrames(sub); len(got) != 0 {
		t.Fatalf("frames = %+v, want none（追平后重连不得重放）", got)
	}
}

// seedThreeEntryStream 造一条 a(token)/b(token)/done 的流，返回 store 与最老
// 条目的 ID。
func seedThreeEntryStream(t *testing.T) (*fakeStreamStore, string) {
	t.Helper()
	store := &fakeStreamStore{block: 5 * time.Millisecond}
	ctx := context.Background()
	oldest, _ := store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventToken, Payload: `{"token":"a"}`})
	_, _ = store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventToken, Payload: `{"token":"b"}`})
	_, _ = store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventDone, Payload: `{"done":true}`})
	return store, oldest
}

// assertResetThenFullReplay 断言「先合成 reset（无游标、reason=stream_lost），
// 再全量重放直到终态帧」——缺口命中与 fail-closed 两条出口共用同一形状。
func assertResetThenFullReplay(t *testing.T, got []StreamFrame, oldest string) {
	t.Helper()
	if len(got) != 4 {
		t.Fatalf("frames = %+v, want reset + 3 条全量", got)
	}
	if got[0].Event != port.StreamEventReset {
		t.Fatalf("frame[0].Event = %q, want reset", got[0].Event)
	}
	if got[0].ID != "" {
		t.Fatalf("reset frame must not carry a cursor, got %q", got[0].ID)
	}
	if !strings.Contains(got[0].Data, ResetReasonStreamLost) {
		t.Fatalf("frame[0].Data = %q, want reason %q", got[0].Data, ResetReasonStreamLost)
	}
	if got[1].ID != oldest || got[1].Data != `{"token":"a"}` {
		t.Fatalf("全量重放必须从流现存最老条目开始，frame[1] = %+v", got[1])
	}
	if got[3].Event != port.StreamEventDone {
		t.Fatalf("frame[3].Event = %q, want done", got[3].Event)
	}
}

// I1.1：游标早于流现存最老条目（MAXLEN 裁剪过）→ 先 reset 再全量重放，
// 不把带洞的半截答案交给用户。
func TestSubscriptionReplayGapResetsThenReplaysFull(t *testing.T) {
	store, oldest := seedThreeEntryStream(t)
	if oldest != "1726483200000-1" {
		t.Fatalf("oldest = %q, want 1726483200000-1", oldest)
	}

	sub := NewExecutionSubscription(context.Background(), ExecutionSubscriptionDeps{
		Stream: store, Control: &fakeControlBus{}, ExecutionID: "e1", Generation: 1,
		// 游标落在最老条目之前，但 ReplayAfter 仍返回非空（满足缺口检测前置条件）。
		Plan: StreamPlan{Generation: 1, AfterID: "1726483200000-0"},
		Cfg:  StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour, ReadBlock: 5},
	})
	defer sub.Close()

	got := collectFrames(t, sub, func(fs []StreamFrame) bool {
		return len(fs) > 0 && fs[len(fs)-1].Event == port.StreamEventDone
	})
	assertResetThenFullReplay(t, got, oldest)
}

// I1.2：FirstID 查询失败仍按缺口处理（fail closed）——宁可多发一次 reset 走
// 全量重放，也不要把可能带洞的半截答案交给用户。降级激活必须留痕。
func TestSubscriptionReplayGapFailClosedOnFirstIDError(t *testing.T) {
	base, oldest := seedThreeEntryStream(t)
	core, logs := observer.New(zapcore.DebugLevel)

	sub := NewExecutionSubscription(context.Background(), ExecutionSubscriptionDeps{
		Stream: &firstIDErrorStore{base}, Control: &fakeControlBus{}, ExecutionID: "e1", Generation: 1,
		Plan:   StreamPlan{Generation: 1, AfterID: "1726483200000-0"},
		Cfg:    StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour, ReadBlock: 5},
		Logger: zap.New(core),
	})
	defer sub.Close()

	got := collectFrames(t, sub, func(fs []StreamFrame) bool {
		return len(fs) > 0 && fs[len(fs)-1].Event == port.StreamEventDone
	})
	assertResetThenFullReplay(t, got, oldest)

	entries := logs.FilterMessage("agent stream: oldest stream id query failed").All()
	if len(entries) < 1 {
		t.Fatal("FirstID 失败被静默激活：缺口降级没有留下 WARN")
	}
	if got := entries[0].ContextMap()["execution_id"]; got != "e1" {
		t.Fatalf("execution_id = %v, want e1", got)
	}
}

// I2.2：Tail 失败必须留痕，且错误终态帧照旧写出——加日志不得顶替既有控制流。
func TestSubscriptionTailFailureIsLoggedAndEmitsErrorFrame(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	store := &tailErrorStore{&fakeStreamStore{block: 5 * time.Millisecond}}

	sub := NewExecutionSubscription(context.Background(), ExecutionSubscriptionDeps{
		Stream: store, Control: &fakeControlBus{}, ExecutionID: "e1", Generation: 1,
		Plan:   StreamPlan{Generation: 1},
		Cfg:    StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour, ReadBlock: 5},
		Logger: zap.New(core),
	})
	defer sub.Close()

	got := collectFrames(t, sub, func(fs []StreamFrame) bool {
		return len(fs) > 0 && fs[len(fs)-1].Event == port.StreamEventError
	})
	if len(got) != 1 || got[0].Event != port.StreamEventError {
		t.Fatalf("frames = %+v, want 单条错误终态帧", got)
	}

	entries := logs.FilterMessage("agent stream: tail failed").All()
	if len(entries) != 1 {
		t.Fatalf("warn 条目 = %d, want 1", len(entries))
	}
	if entries[0].Level != zapcore.WarnLevel {
		t.Fatalf("level = %v, want warn", entries[0].Level)
	}
	if got := entries[0].ContextMap()["execution_id"]; got != "e1" {
		t.Fatalf("execution_id = %v, want e1", got)
	}
}

// subscriptionSentinelKey 是私有 key 类型：挂在 parent ctx 上的 sentinel 值
// 不可能由 context.Background() 派生出来，因此能坐实「Value 是否真的从调用方
// 抵达了存储调用」，而不必在单测里 import 存储驱动去断言租户本身。
type subscriptionSentinelKey struct{}

// 订阅 ctx 必须继承调用方 ctx 的 Values——租户上下文是其中承重的一项：
// AgentStreamStore 的每个方法都经 tenantnaming.TenantKey 取租户，取不到就
// fail closed。旧实现用 context.Background() 造生命周期 ctx，丢掉了调用方的
// 租户，于是生产上每一次 SSE 订阅都在回放第一步（ReplayAfter）就写出错误帧
// 并断开。「必须真的是租户」由 integration 测试用真实 store 承担，本用例锁住
// 「调用方 Values 被传递」这个更基础的不变量。
func TestSubscriptionStoreSeesCallerContextValues(t *testing.T) {
	store := &fakeStreamStore{
		block:      5 * time.Millisecond,
		replayCtxs: make(chan context.Context, 4),
	}
	_, _ = store.Append(context.Background(), "e1", 1,
		port.StreamEntry{Event: port.StreamEventDone, Payload: `{"done":true}`})

	const sentinel = "caller-tenant-sentinel"
	parent := context.WithValue(context.Background(), subscriptionSentinelKey{}, sentinel)

	sub := NewExecutionSubscription(parent, ExecutionSubscriptionDeps{
		Stream: store, Control: &fakeControlBus{}, ExecutionID: "e1", Generation: 1,
		Plan: StreamPlan{Generation: 1},
		Cfg:  StreamSubscriptionConfig{Heartbeat: time.Hour, IdleExit: time.Hour, ReadBlock: 5},
	})
	defer sub.Close()

	var replayCtx context.Context
	select {
	case replayCtx = <-store.replayCtxs:
	case <-time.After(2 * time.Second):
		t.Fatal("ReplayAfter 在 2s 内未被调用")
	}
	if got := replayCtx.Value(subscriptionSentinelKey{}); got != sentinel {
		t.Fatalf("ReplayAfter 收到的 ctx 丢失调用方 Value：got %v, want %q"+
			"（订阅 ctx 必须继承调用方 ctx 的 Values，否则存储层取不到租户而 fail closed）",
			got, sentinel)
	}
}
