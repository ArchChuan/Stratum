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
	// closing 是关闭闸门：一旦置位就不再接受新登记（见 track）。它与 runs、wg
	// 同受 mu 保护，三者必须一起看——只加锁不改闸门挡不住「取消快照之后才进门」
	// 的 runner。
	closing bool
}

func newRunnerSet() *runnerSet {
	return &runnerSet{runs: make(map[string]*localRunner)}
}

// localRunnerSet 返回服务当前的 runner 集合，未装配时惰性创建。
//
// 惰性初始化必须读写都在锁内：runner 由多个 SSE 请求各自启动，共享的是
// s.deps 这一个字段，锁外读会与锁内写构成竞争。
func (s *AgentService) localRunnerSet() *runnerSet {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deps.StreamRunnerSet == nil {
		s.deps.StreamRunnerSet = newRunnerSet()
	}
	return s.deps.StreamRunnerSet
}

// track 登记一个 runner 并返回其句柄。返回 nil 表示集合已进入关闭流程。
//
// nil 是**关闭信号而非错误**：集合已经取消过一轮，此刻登记的 runner 不在那次
// 取消的遍历快照里，既不会被取消、也不会被等待，只会让关闭路径的 wg.Wait()
// 一直挂着。调用方必须据此放弃启动 runner，并且不得对 nil 调用 untrack。
func (rs *runnerSet) track(executionID string, generation int, cancel context.CancelFunc) *localRunner {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if rs.closing {
		return nil
	}
	r := &localRunner{
		executionID: executionID,
		generation:  generation,
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	rs.runs[executionID] = r
	// Add 必须在锁内：Add 与 Wait 的交叉是 Go 明文禁止的误用（计数归零时的
	// Add 会 panic），且锁内 Add 保证「map 里可见」与「wg 计数」两个状态始终
	// 一致——CancelAll 拿着同一把锁，看到的要么是完整的登记，要么什么都没有。
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
//
// closing 闸门与取消遍历在同一临界区内置位，这是 Wait 能收敛的原因：先拿到锁的
// track 在锁内完成 Add，其登记与本次取消构成 happens-before；后到的 track 看到
// closing 直接返回 nil。缺了闸门，取消遍历之后登记的 runner 会溜进 Wait 的等待
// 集合里等死（pod 关闭 hang）。
func (rs *runnerSet) CancelAll() {
	rs.mu.Lock()
	rs.closing = true
	for _, r := range rs.runs {
		r.cancel()
	}
	rs.mu.Unlock()
	rs.wg.Wait()
}

// ShutdownStreamRunners 取消本进程所有在跑的流式执行并等待退出。
// 未装配流依赖时为 no-op。
func (s *AgentService) ShutdownStreamRunners() {
	// 先在锁内取到指针、释放锁后再 CancelAll：CancelAll 会等待 runner 退出，
	// 而 runner 的退出路径可能再次调用 localRunnerSet()——持锁等待会自锁。
	s.mu.Lock()
	rs := s.deps.StreamRunnerSet
	s.mu.Unlock()
	if rs == nil {
		return
	}
	rs.CancelAll()
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
