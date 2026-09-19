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

// 控制通道 key 中「控制」段的段名，最终 key 为 agent:{tenant}:ctrl:{executionID}。
const keySegmentControl = "ctrl"

// subscribeReadyTimeout 是 WaitSubscribed 等待 Pub/Sub 就绪的上限。
const subscribeReadyTimeout = 2 * time.Second

// controlQueueSize 是每条订阅的消息缓冲深度。控制通道流量极低（stop / viewer
// 进入离开），16 足以吸收突发；缓冲区满时的行为见 Subscribe。
const controlQueueSize = 16

// readyGate 是「某通道的订阅已建立」的一次性信号。
//
// 必须幂等：同一 execution_id 的第二次 Subscribe 会再次 mark 同一把门，而对已关闭
// 的 channel 再 close 会 panic（close of closed channel）——F5 重连、双 tab、
// 跨实例 attach 都会走到这条路。
type readyGate struct {
	once sync.Once
	ch   chan struct{}
}

func newReadyGate() *readyGate { return &readyGate{ch: make(chan struct{})} }

func (g *readyGate) mark() { g.once.Do(func() { close(g.ch) }) }

func (g *readyGate) wait() <-chan struct{} { return g.ch }

// ControlBus 是 port.AgentControlBus 的 Redis Pub/Sub 实现。
//
// 它持有独立的 *goredis.Client：go-redis 的 Subscribe 独占一条连接，与 Stream
// 命令共用同一实例会让订阅期间的普通命令排队饿死（spec §7.1）。
type ControlBus struct {
	client *goredis.Client

	mu    sync.Mutex
	ready map[string]*readyGate
}

var _ port.AgentControlBus = (*ControlBus)(nil)

func NewControlBus(client *goredis.Client) *ControlBus {
	return &ControlBus{client: client, ready: make(map[string]*readyGate)}
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

// Subscribe 订阅控制通道。返回的取消函数**同步**：它返回后消费 goroutine 已退出、
// 返回的消息通道已关闭，调用方不需要（也不应）再读它。重复调用取消函数是安全的。
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

	out := make(chan port.ControlMessage, controlQueueSize)
	// 门在订阅确认之后取：Receive 失败时不该在 map 里留下无人认领的条目。
	gate := b.gate(channel)
	// 独立的消费 context：调用方传入的 ctx 往往是 HTTP 请求 context，
	// 它的取消不该终止 runner 的控制订阅（订阅生命周期由 unsubscribe 决定）。
	consumeCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(out)
		forwardControlMessages(consumeCtx, sub.Channel(), out)
	}()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			cancel()
			_ = sub.Close()
			b.forgetGate(channel, gate)
		})
		// 等消费 goroutine 真正退出：返回后 out 已关闭、不会再有写入，
		// 调用方（runner 的 defer unsubscribe）可以安全地不再读它。
		<-done
	}

	gate.mark()
	return out, unsubscribe, nil
}

// forwardControlMessages 把订阅通道上的消息转投到 out，直到 ctx 取消或订阅结束。
func forwardControlMessages(ctx context.Context, in <-chan *goredis.Message, out chan port.ControlMessage) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-in:
			if !ok {
				return
			}
			if err := deliverControlMessage(ctx, out, msg.Payload); err != nil {
				return
			}
		}
	}
}

// deliverControlMessage 解析并投递一条控制消息。返回 nil 表示消费继续：畸形消息
// 只丢弃，不断订阅——一条坏消息不该让 runner 失去停止能力。返回 ctx.Err() 表示
// 消费者已停止读取，订阅应当结束。
func deliverControlMessage(ctx context.Context, out chan port.ControlMessage, payload string) error {
	var parsed port.ControlMessage
	if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
		return nil
	}
	// 发送必须可取消：缓冲满且消费者停读时，裸发送会永久阻塞，goroutine 再也
	// 看不到 ctx.Done()，close(out) 不执行，unsubscribe 也救不回来（红线第 6 条）。
	select {
	case out <- parsed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// WaitSubscribed 阻塞至多 subscribeReadyTimeout，等待某通道的订阅建立完成。
// 供测试消除「订阅异步生效」的竞态；生产路径不需要——Subscribe 内部的
// Receive 已经同步确认过。
func (b *ControlBus) WaitSubscribed(ctx context.Context, executionID string) error {
	channel, err := b.channel(ctx, executionID)
	if err != nil {
		return err
	}
	gate := b.gate(channel)
	select {
	case <-gate.wait():
		return nil
	case <-time.After(subscribeReadyTimeout):
		return fmt.Errorf("control_bus: subscribe not ready for %s", execIDForLog(executionID))
	case <-ctx.Done():
		return ctx.Err()
	}
}

// forgetGate 摘除某个订阅留下的门。gate 是 ready 的唯一写入点，若没有这个
// 删除点，map 就会按「pod 生命周期内启动过的流式执行数」单调增长——生产
// Subscribe 每启动一个 runner 取一次门，而条目永久驻留（无界增长）。
//
// 选择删除而非换成有界结构：门的唯一用途是弥合「订阅异步生效」与
// WaitSubscribed 之间的竞态，订阅关闭后这个用途已经结束，删除是语义上精确的
// 生命周期终点。有界 map 反而要引入淘汰策略，而淘汰一个仍被等待者持有的门
// 会让 WaitSubscribed 永久等一个不再被 mark 的新门——比内存增长更糟。
//
// 只删自己那把门（cur == g）：同一 channel 上并存的第二个订阅（双 tab、跨实例
// attach）仍可能被 WaitSubscribed 等待，先退出者不得把后者的门摘掉。
func (b *ControlBus) forgetGate(channel string, g *readyGate) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if cur, ok := b.ready[channel]; ok && cur == g {
		delete(b.ready, channel)
	}
}

// gate 返回 channel 对应的信号门，不存在则创建。Subscribe 与 WaitSubscribed
// 都经此取门，保证「先等后订」与「先订后等」都能拿到同一个门。
func (b *ControlBus) gate(channel string) *readyGate {
	b.mu.Lock()
	defer b.mu.Unlock()
	g, ok := b.ready[channel]
	if !ok {
		g = newReadyGate()
		b.ready[channel] = g
	}
	return g
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
