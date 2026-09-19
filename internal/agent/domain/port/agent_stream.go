package port

import (
	"context"
	"errors"
	"time"
)

// ErrLeaseConflict 表示租约 CAS 未命中：expect generation 已被别的 runner
// 推进（或已过期被抢）。调用方据此判定「没抢到」，转入 TAIL 读赢家的流。
var ErrLeaseConflict = errors.New("agent: execution lease conflict")

// ErrCheckpointNotFound 表示租户命名空间下不存在该 execution_id 的 checkpoint 行，
// 租约操作因此无从施加（StampLease 是纯 UPDATE、LeaseStatus 是单行 SELECT）。
//
// 与 ErrLeaseConflict 分开的为什么不是「更细」而是「必需」：两者在调用方眼里
// 的归档完全不同——Conflict 是并发竞争的常态（重读并订阅赢家），NotFound 是
// 「这一行不存在」的事实，必须与「连接断开 / schema 未建 / SQL 语法错」这类
// 基础设施故障可区分。只要求「有 error」的测试会让上述故障冒充 NotFound 通过。
var ErrCheckpointNotFound = errors.New("agent: execution checkpoint not found")

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
	//
	// ID 由存储在写入时分配（Redis 生成的 entry ID）。调用方传入的 e.ID 被忽略：
	// 需要这条新条目的 ID 只能取返回值，写进 e.ID 没有作用。
	Append(ctx context.Context, executionID string, generation int, e StreamEntry) (string, error)
	// Replay 全量回放该 generation 的流（从最老条目开始）。
	Replay(ctx context.Context, executionID string, generation int) ([]StreamEntry, error)
	// ReplayAfter 回放 afterID 之后的条目（排他）。
	ReplayAfter(ctx context.Context, executionID string, generation int, afterID string) ([]StreamEntry, error)
	// FirstID 返回流最老条目的 ID；空流返回 ""。用于裁剪缺口判定。
	FirstID(ctx context.Context, executionID string, generation int) (string, error)
	// Tail 从 afterID 之后阻塞读取至多 block 毫秒；无新条目返回空切片与 nil error。
	//
	// block 的单位是毫秒，与 Redis `XREAD ... BLOCK` 的参数语义逐字对齐：实现把它
	// 原样透传给 XREAD，不做单位换算。读错单位不会报错，只会静默劣化——把秒当毫秒
	// （传 15 想要 15s）退化成 15ms 的热轮询；把毫秒当秒则挂起千倍时长。
	//
	// 调用方通常传 0，表示「按实现的默认读周期阻塞」——实现回落到
	// pkg/constants.AgentStreamReadBlock（1s）。因此在本端口上 block <= 0 是一次
	// 正常的阻塞读，不是非阻塞读。
	//
	// 这与 pkg/storage/redis.StreamStore.Tail 的内部归一化不同：后者确实把 block <= 0
	// 变成省略 BLOCK 的非阻塞读。那是本端口之下的实现细节，端口调用方观察不到，也不
	// 得依赖——同一个 <= 0 在两层的含义恰好相反。
	//
	// 正值是自定义毫秒数：需要显式控制读周期时，用 pkg/constants 中的 time.Duration
	// 常量（如 AgentStreamReadBlock）取 .Milliseconds() 传入。
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
