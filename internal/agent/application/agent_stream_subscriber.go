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
	"go.uber.org/zap"
)

// 错误终态帧载荷。订阅侧有三条错误路径（回放失败、跟流失败、缺口重放失败）
// 共用同一文案（spec §6.7）：两种措辞留给前端，而不是每个调用点各写一种。
const (
	streamInterruptedNotice = `{"error":"流式连接已中断，请重试"}`
	streamIdleExitNotice    = `{"error":"执行已中断，请重新发起"}`
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
	Logger      *zap.Logger
}

// ExecutionSubscription 是一次订阅的句柄。handler 只消费 Frames()，
// 不接触 StreamStore / ControlBus——回放、跟流、心跳、看门狗全在应用层，
// transport 退化成纯写出。
type ExecutionSubscription struct {
	frames chan StreamFrame
	// ctx 只被订阅 goroutine 读取，且在 goroutine 启动前写入，无需再加锁。
	ctx      context.Context
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
	// 归一化 deps 本身而非另存局部变量：下游 runSubscription 收到的是 deps，
	// 只有写回它才能让每个失败点都拿到可用的 Logger（测试也不必逐点注入）。
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}

	sub := &ExecutionSubscription{
		frames: make(chan StreamFrame, constants.AgentStreamFrameBufferSize),
		done:   make(chan struct{}),
	}
	sub.ctx, sub.cancel = subscriptionContext()
	go func() {
		defer close(sub.done)
		defer close(sub.frames)
		runSubscription(sub.ctx, deps, cfg, viewerID, sub.frames)
	}()
	return sub
}

// subscriptionContext 构造一次订阅的生命周期 context，并把取消函数显式交还
// 调用方（与 revisionExecutionContext 同型）。订阅的取消权归 ExecutionSubscription
// 的 Close()，不是构造点——写成返回 cancel 而非就地持有，正是为了让这个所有权
// 转移在签名上可见。
func subscriptionContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

// runSubscription 是订阅的装配层：合成 reset 帧 → 回放 → 交给跟流主循环。
func runSubscription(
	ctx context.Context, deps ExecutionSubscriptionDeps, cfg StreamSubscriptionConfig,
	viewerID string, out chan<- StreamFrame,
) {
	// reset 由订阅侧合成，不来自流，因此没有游标（spec §6.6）。
	if deps.Plan.Reset {
		if !emit(ctx, out, StreamFrame{Event: port.StreamEventReset, Data: resetPayload(deps.Plan)}) {
			return
		}
	}

	cursor, terminal, ok := replay(ctx, deps, out)
	if !ok || terminal {
		return
	}
	tailLoop(ctx, deps, cfg, viewerID, out, cursor)
}

// tailLoop 是跟流主循环：心跳 tick 与 Tail 交替。循环节奏由 Tail 的阻塞时长
// 决定（select 带 default，从不阻塞），因此心跳的实际精度是
// max(Heartbeat, Tail 阻塞时长)。生产上 ReadBlock=0 回落 1s、心跳 5s，
// 有 5 倍采样余量；只有 ReadBlock 大于 Heartbeat 时才会漏 tick。
func tailLoop(
	ctx context.Context, deps ExecutionSubscriptionDeps, cfg StreamSubscriptionConfig,
	viewerID string, out chan<- StreamFrame, cursor string,
) {
	tick := time.NewTicker(cfg.Heartbeat)
	defer tick.Stop()
	idleSince := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if !onTick(ctx, deps, cfg, viewerID, out, idleSince) {
				return
			}
		default:
		}

		var cont bool
		cursor, idleSince, cont = tailOnce(ctx, deps, cfg, out, cursor, idleSince)
		if !cont {
			return
		}
	}
}

// tailOnce 走一轮 Tail 并把新条目转成帧。返回新游标、新的空闲基线，以及是否
// 继续跟流（false 表示已写出终态帧或错误帧，订阅应结束）。
//
// 空闲基线只在这一轮确实取到非空批次时前移：空批次前移会让空闲看门狗在
// 「永远没有新条目」的执行上永不触发，前端就会无限 spinner。
func tailOnce(
	ctx context.Context, deps ExecutionSubscriptionDeps, cfg StreamSubscriptionConfig,
	out chan<- StreamFrame, cursor string, idleSince time.Time,
) (string, time.Time, bool) {
	entries, err := deps.Stream.Tail(ctx, deps.ExecutionID, deps.Generation, cursor, cfg.ReadBlock)
	if err != nil {
		// Redis 掉线：订阅侧降级，不阻塞 run。发一条错误终态帧后退出——
		// 持续重试只会在前端留下一个不再前进的流。
		deps.Logger.Warn("agent stream: tail failed",
			zap.String("execution_id", deps.ExecutionID), zap.Error(err))
		emitStreamError(ctx, out)
		return cursor, idleSince, false
	}
	if len(entries) == 0 {
		return cursor, idleSince, true
	}
	idleSince = time.Now()

	next, terminal, ok := emitEntries(ctx, out, cursor, entries)
	if !ok || terminal {
		return next, idleSince, false
	}
	return next, idleSince, true
}

// onTick 处理一次心跳 tick：写 heartbeat 注释帧 → 上报 viewer → 判空闲看门狗。
// 返回 false 表示订阅应当结束（写帧失败，或看门狗判定 runner 已不会再产出）。
func onTick(
	ctx context.Context, deps ExecutionSubscriptionDeps, cfg StreamSubscriptionConfig,
	viewerID string, out chan<- StreamFrame, idleSince time.Time,
) bool {
	// 心跳与 viewer 上报搭同一个 tick：零额外连接、零定时器（spec §7.1）。
	if !emit(ctx, out, StreamFrame{Comment: "heartbeat"}) {
		return false
	}
	// 上报失败只影响孤儿计时，不影响本订阅的可读性，显式忽略。
	_ = deps.Control.PublishViewer(ctx, deps.ExecutionID, viewerID)
	if !idleExceeded(ctx, deps, idleSince, cfg) {
		return true
	}
	// 租约已失效且流持续无新条目：runner 不会再有产出。写一条错误终态帧让
	// 前端收敛，而不是让它无限等待。
	emit(ctx, out, StreamFrame{Event: port.StreamEventError, Data: streamIdleExitNotice})
	return false
}

// replay 全量/增量回放并返回新游标。第二个返回值表示是否已遇到终态帧，
// 第三个返回值表示回放是否正常完成（false 表示已写出错误帧、订阅应结束）。
func replay(
	ctx context.Context, deps ExecutionSubscriptionDeps, out chan<- StreamFrame,
) (cursor string, terminal bool, ok bool) {
	entries, err := deps.Stream.ReplayAfter(ctx, deps.ExecutionID, deps.Generation, deps.Plan.AfterID)
	if err != nil {
		emitStreamError(ctx, out)
		return "", false, false
	}
	entries, ok = handleReplayGap(ctx, deps, out, entries)
	if !ok {
		return "", false, false
	}
	// 游标种子必须是客户端的续传游标：批次为空时（客户端已追平流尾、重连无新
	// 条目）游标保持此值，Tail 才会从正确位置续读；写死 "" 会被 Tail 规范化成
	// "0-0" 并从流头重读，把整条流重复下发（F5 重连的必经路径）。
	return emitEntries(ctx, out, deps.Plan.AfterID, entries)
}

// handleReplayGap 检测裁剪缺口并在命中时先 reset 再全量重放。第二个返回值
// 表示是否可以继续（false 表示已写出错误帧、订阅应结束）。
func handleReplayGap(
	ctx context.Context, deps ExecutionSubscriptionDeps, out chan<- StreamFrame,
	entries []port.StreamEntry,
) ([]port.StreamEntry, bool) {
	// 裁剪缺口检测：游标早于流现存最老条目说明增量回放会给出半截答案，
	// 必须 reset 重来，而不是把带洞的文本交给用户（spec §6.6）。
	if len(entries) == 0 || !replayGapDetected(ctx, deps) {
		return entries, true
	}
	if !emit(ctx, out, StreamFrame{Event: port.StreamEventReset, Data: resetPayload(StreamPlan{
		Generation: deps.Generation, Reset: true, ResetReason: ResetReasonStreamLost,
	})}) {
		return nil, false
	}
	full, err := deps.Stream.Replay(ctx, deps.ExecutionID, deps.Generation)
	if err != nil {
		emitStreamError(ctx, out)
		return nil, false
	}
	return full, true
}

// replayGapDetected 判定增量回放是否落进了已被 MAXLEN 剪掉的那一段。
//
// 最老条目必须取自 FirstID（流现存最老条目），不能取回放结果的第一条：
// ReplayAfter 的条目在定义上永远大于游标，拿它比较会让每一次增量续传都被
// 误判成缺口，对每个 F5 重连都多发一次 reset 并退回全量重放。
func replayGapDetected(ctx context.Context, deps ExecutionSubscriptionDeps) bool {
	if deps.Plan.AfterID == "" {
		// 全量回放没有游标，结构性不可能有缺口，也省掉一次存储往返。
		return false
	}
	oldestID, err := deps.Stream.FirstID(ctx, deps.ExecutionID, deps.Generation)
	if err != nil {
		// 查询失败时 fail closed：宁可多发一次 reset 走全量重放，也不要把
		// 可能带洞的半截答案交给用户。降级方向与 idleExceeded 相反且各自正确，
		// 但「静默激活」不可接受，必须留痕。
		deps.Logger.Warn("agent stream: oldest stream id query failed",
			zap.String("execution_id", deps.ExecutionID), zap.Error(err))
		return true
	}
	return HasReplayGap(deps.Plan.AfterID, oldestID)
}

// emitEntries 把一批条目依次转成帧写出，返回新游标、是否遇到终态帧，以及是否
// 全部写出。终态条目本身先被写出、再据此结束——客户端必须收到那个终态帧。
func emitEntries(
	ctx context.Context, out chan<- StreamFrame, cursor string, entries []port.StreamEntry,
) (string, bool, bool) {
	for _, e := range entries {
		cursor = e.ID
		if !emit(ctx, out, StreamFrame{ID: e.ID, Event: e.Event, Data: e.Payload}) {
			return cursor, false, false
		}
		if port.IsTerminalStreamEvent(e.Event) {
			return cursor, true, true
		}
	}
	return cursor, false, true
}

// emitStreamError 写一条「流中断」错误终态帧。写出是否成功不影响调用方的
// 结束决定——三条错误路径都已无话可说，失败也只是客户端早一步断开。
func emitStreamError(ctx context.Context, out chan<- StreamFrame) {
	emit(ctx, out, StreamFrame{Event: port.StreamEventError, Data: streamInterruptedNotice})
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
		deps.Logger.Warn("agent stream: lease status query failed",
			zap.String("execution_id", deps.ExecutionID), zap.Error(err))
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
// ctx 取消时返回 false：Close() 要等待订阅 goroutine 退出，没有这个出口就会
// 一直卡在这里（红线第 6 条要求停止 worker 时可控地等待退出）。
func emit(ctx context.Context, out chan<- StreamFrame, frame StreamFrame) bool {
	select {
	case out <- frame:
		return true
	default:
	}
	select {
	case out <- frame:
		return true
	case <-ctx.Done():
		return false
	}
}
