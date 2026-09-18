package application

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
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

func TestSubscriptionReplaysThenStopsAtTerminal(t *testing.T) {
	store := &fakeStreamStore{block: 5 * time.Millisecond}
	ctx := context.Background()
	_, _ = store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventMeta, Payload: `{"execution_id":"e1","generation":1}`})
	_, _ = store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventToken, Payload: `{"token":"a"}`})
	_, _ = store.Append(ctx, "e1", 1, port.StreamEntry{Event: port.StreamEventDone, Payload: `{"done":true}`})

	sub := NewExecutionSubscription(ExecutionSubscriptionDeps{
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

	sub := NewExecutionSubscription(ExecutionSubscriptionDeps{
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
	_, _ = store.Append(context.Background(), "e1", 2, port.StreamEntry{Event: port.StreamEventDone, Payload: `{"done":true}`})

	sub := NewExecutionSubscription(ExecutionSubscriptionDeps{
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
