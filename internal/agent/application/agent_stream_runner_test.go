package application

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
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

// localRunnerSet 由多个并发 SSE 请求各自调用，惰性初始化必须是单实例且无竞争
// （-race 下运行本用例才有效力）。
func TestLocalRunnerSetLazyInitIsRaceFree(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{})
	const goroutines = 16
	got := make([]*runnerSet, goroutines)
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = svc.localRunnerSet()
		}()
	}
	wg.Wait()
	for i, rs := range got {
		if rs == nil {
			t.Fatalf("localRunnerSet()[%d] = nil", i)
		}
		if rs != got[0] {
			t.Fatalf("localRunnerSet()[%d] 与 [0] 不是同一实例：惰性初始化被重复执行", i)
		}
	}
}

// 未装配流依赖时 ShutdownStreamRunners 必须是 no-op，而不是空指针 panic。
func TestShutdownStreamRunnersNoopWhenUnwired(t *testing.T) {
	NewAgentService(AgentServiceDeps{}).ShutdownStreamRunners()
}

// 装配路径（SIGTERM 真正会走的那条）必须取消已登记的 runner 并等它注销：
// 上面的 no-op 用例覆盖不到 localRunnerSet() 拿到的那个集合。
func TestShutdownStreamRunnersCancelsTrackedRunners(t *testing.T) {
	svc := NewAgentService(AgentServiceDeps{})
	rs := svc.localRunnerSet()

	ctx, cancel := context.WithCancel(context.Background())
	run := rs.track("exec-1", 1, cancel)
	if run == nil {
		t.Fatal("未进入关闭流程的集合不应拒绝 track")
	}
	// runner 的退出路径由持有 ctx 的 goroutine 代表：ctx 被取消即退出。
	exited := make(chan struct{})
	go func() {
		<-ctx.Done()
		rs.untrack(run)
		close(exited)
	}()

	svc.ShutdownStreamRunners()

	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("ShutdownStreamRunners 后 ctx.Err() = %v, want context.Canceled", ctx.Err())
	}
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("ShutdownStreamRunners 返回后 runner 仍未退出")
	}
	rs.mu.Lock()
	remaining := len(rs.runs)
	rs.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("ShutdownStreamRunners 后 runs 剩余 %d 条，want 0", remaining)
	}
}

// 关闭闸门回归锁：CancelAll 之后再 track 必须被拒绝。放行的 runner 不在取消
// 遍历的快照里，既不会被取消也不会被等待，会把关闭路径的 wg.Wait() 拖到它
// 自然结束——pod 关闭时这是 hang 不是泄漏。
//
// 用例是确定性的：CancelAll 返回意味着 wg 已归零，之后的 track 与之无时序竞争。
func TestRunnerSetTrackRejectedAfterCancelAll(t *testing.T) {
	rs := newRunnerSet()
	rs.CancelAll()

	cancelConsumed := false
	run := rs.track("late", 1, func() { cancelConsumed = true })
	if run != nil {
		t.Fatalf("closing 后 track 返回 %+v，want nil", run)
	}
	if cancelConsumed {
		t.Fatal("被拒绝的 track 不应消费调用方的 cancel")
	}
	rs.mu.Lock()
	remaining := len(rs.runs)
	rs.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("被拒绝的 track 仍写入了 runs：剩余 %d 条，want 0", remaining)
	}
}

func TestRunnerSetCancelAllUnblocksRunners(t *testing.T) {
	rs := newRunnerSet()
	ctx, cancel := context.WithCancel(context.Background())
	run := rs.track("exec-1", 1, cancel)
	// runner 的退出路径由持有 ctx 的 goroutine 代表：ctx 被取消即退出。
	exited := make(chan struct{})
	go func() {
		<-ctx.Done()
		rs.untrack(run)
		close(exited)
	}()
	rs.CancelAll()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("CancelAll 返回后 runner 仍未退出")
	}
	rs.mu.Lock()
	remaining := len(rs.runs)
	rs.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("untrack 后 runs 剩余 %d 条，want 0", remaining)
	}
}

// 流是尽力而为的显示缓冲：写失败只留痕，不返回错误、不中断 run。
func TestLogStreamWriteFailureRecordsWarning(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	svc := NewAgentService(AgentServiceDeps{Logger: zap.New(core)})
	svc.logStreamWriteFailure("exec-1", errors.New("redis down"))

	entries := logs.FilterMessage("agent stream: write failed").All()
	if len(entries) != 1 {
		t.Fatalf("warn 条目 = %d, want 1", len(entries))
	}
	if entries[0].Level != zapcore.WarnLevel {
		t.Fatalf("level = %v, want warn", entries[0].Level)
	}
	if got := entries[0].ContextMap()["execution_id"]; got != "exec-1" {
		t.Fatalf("execution_id = %v, want exec-1", got)
	}
}
