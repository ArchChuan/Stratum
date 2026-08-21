package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/timeutil"
)

// MessageBuffer accumulates messages in Redis and flushes when K=5, size>=8KB, or T=2min.
type MessageBuffer struct {
	store port.MessageBufferStore
	queue port.ExtractionQueue
}

// msgEntry is the trimmed per-message shape enqueued into the extraction task.
type msgEntry struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func NewMessageBuffer(store port.MessageBufferStore, queue port.ExtractionQueue) *MessageBuffer {
	return &MessageBuffer{store: store, queue: queue}
}

// BufferMessage accumulates a message in Redis; flushes if K>=5, size>=8KB, or oldest >2min.
func (b *MessageBuffer) BufferMessage(ctx context.Context, req *BufferMessageRequest) error {
	if b.store == nil {
		return fmt.Errorf("redis client not configured")
	}

	key := fmt.Sprintf("memory:buffer:%s:%s:%s:%s", req.TenantID, req.UserID, req.AgentID, req.ConversationID)
	metaKey := "memory:buffer:meta:" + key[len("memory:buffer:"):]

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal buffer message: %w", err)
	}

	if err := b.store.RPush(ctx, key, data); err != nil {
		return fmt.Errorf("redis rpush: %w", err)
	}
	if err := b.store.Expire(ctx, key, constants.MemoryBufferKeyTTL); err != nil {
		return fmt.Errorf("redis expire: %w", err)
	}

	// Update meta: first_at (only if not set), last_at, scope, byte_size.
	// Every meta write is fail-closed: the flush thresholds (K, size, age)
	// and the key TTL are driven by this state, so a silently dropped write
	// would skew the counters or leak the key in Redis forever.
	newSize, err := b.updateMeta(ctx, metaKey, req, len(data))
	if err != nil {
		return err
	}

	// Flush conditions: K>=5, size>=8KB, or oldest message >2min.
	shouldFlush, err := b.shouldFlush(ctx, key, req, newSize)
	if err != nil {
		return err
	}
	if shouldFlush {
		return b.flush(ctx, key, req.TenantID, req.UserID, req.AgentID, req.ConversationID, req.Scope)
	}
	return nil
}

// updateMeta records first_at (only if not set), last_at, scope, byte_size on
// the meta key and refreshes its TTL. Every write is fail-closed: the flush
// thresholds (K, size, age) and the key TTL are driven by this state, so a
// silently dropped write would skew the counters or leak the key in Redis
// forever. Returns the accumulated byte_size.
func (b *MessageBuffer) updateMeta(ctx context.Context, metaKey string, req *BufferMessageRequest, dataLen int) (int64, error) {
	now := timeutil.Now().Format(time.RFC3339)
	if err := b.store.HSetNX(ctx, metaKey, "first_at", now); err != nil {
		return 0, fmt.Errorf("redis hsetnx: %w", err)
	}
	newSize, err := b.store.HIncrBy(ctx, metaKey, "byte_size", int64(dataLen))
	if err != nil {
		return 0, fmt.Errorf("redis hincrby: %w", err)
	}
	if err := b.store.HSet(ctx, metaKey, "last_at", now, "scope", req.Scope); err != nil {
		return 0, fmt.Errorf("redis hset: %w", err)
	}
	if err := b.store.Expire(ctx, metaKey, constants.MemoryBufferKeyTTL); err != nil {
		return 0, fmt.Errorf("redis expire: %w", err)
	}
	return newSize, nil
}

// shouldFlush reports whether the buffer crossed any flush threshold:
// K>=5, size>=8KB, or oldest message >2min. Read errors fail closed.
func (b *MessageBuffer) shouldFlush(ctx context.Context, key string, req *BufferMessageRequest, newSize int64) (bool, error) {
	count, err := b.store.LLen(ctx, key)
	if err != nil {
		return false, fmt.Errorf("redis llen: %w", err)
	}
	if count >= int64(constants.MemoryBufferFlushSize) {
		return true, nil
	}
	if newSize >= int64(constants.MemoryBufferSizeLimit) {
		return true, nil
	}
	oldest, ok, err := b.store.LIndex(ctx, key, 0)
	if err != nil {
		return false, fmt.Errorf("redis lindex: %w", err)
	}
	if !ok {
		return false, nil
	}
	var oldestMsg BufferMessageRequest
	if err := json.Unmarshal([]byte(oldest), &oldestMsg); err != nil {
		return false, fmt.Errorf("unmarshal oldest message: %w", err)
	}
	return time.Since(oldestMsg.CreatedAt) >= constants.MemoryBufferFlushInterval, nil
}

// flush reads all messages from Redis, enqueues extraction task, deletes list and meta.
func (b *MessageBuffer) flush(ctx context.Context, key, tenantID, userID, agentID, conversationID, scope string) error {
	return b.withFlushLock(ctx, key, func() error {
		return b.drainBuffer(ctx, key, tenantID, userID, agentID, conversationID, scope)
	})
}

// withFlushLock 以单飞锁包裹排水操作：其他 flusher 持锁时跳过本轮（返回
// nil），持锁成功则执行 fn 并在结束后释放（TTL 兜底崩溃场景）。
func (b *MessageBuffer) withFlushLock(ctx context.Context, key string, fn func() error) error {
	locked, release, err := b.acquireFlushLock(ctx, key)
	if err != nil {
		return err
	}
	if !locked {
		return nil // 其他 flusher 正在排水，跳过本轮
	}
	defer release()
	return fn()
}

// drainBuffer 读取快照、入队提取任务、并按值精确清理 Redis。快照之后到达
// 的新消息不在 ARGV 中，保留等待下一轮；Enqueue 成功后清理失败时消息仍在
// 列表，下轮重读 + 幂等去重后补删（at-least-once）。
func (b *MessageBuffer) drainBuffer(ctx context.Context, key, tenantID, userID, agentID, conversationID, scope string) error {
	messages, err := b.store.LRange(ctx, key, 0, -1)
	if err != nil {
		return fmt.Errorf("redis lrange: %w", err)
	}
	if len(messages) == 0 {
		return nil
	}

	firstMessageID, msgs, raws, sizes := parseBufferMessages(messages)
	if len(msgs) == 0 {
		return nil
	}

	// Quality gate: skip extraction if non-tool content is below minimum threshold.
	// Prevents low-value flushes where messages are all tool outputs or short acknowledgements.
	discarded, err := b.discardLowValueBatch(ctx, key, msgs, raws, sizes)
	if err != nil {
		return err
	}
	if discarded {
		return nil
	}

	content, err := json.Marshal(msgs)
	if err != nil {
		return fmt.Errorf("marshal messages: %w", err)
	}

	task := &port.ExtractionTask{
		TenantID:       tenantID,
		MessageID:      firstMessageID,
		UserID:         userID,
		AgentID:        agentID,
		ConversationID: conversationID,
		Scope:          scope,
		Content:        string(content),
	}
	if err := b.queue.Enqueue(ctx, tenantID, task); err != nil {
		return fmt.Errorf("enqueue extraction: %w", err)
	}

	metaKey := "memory:buffer:meta:" + key[len("memory:buffer:"):]
	// 按值精确删除本次快照读到的消息，而不是 Del 整个 key：快照之后到达的
	// 新消息必须保留，等待下一轮 flush。CRASH 于 Enqueue 与 RemoveValues
	// 之间时消息仍在列表，下轮重读 + Enqueue 幂等去重后补删——保持 at-least-once。
	if err := b.store.RemoveValues(ctx, key, metaKey, raws, sizes); err != nil {
		return fmt.Errorf("redis remove values: %w", err)
	}
	return nil
}

// parseBufferMessages 解析快照原始字节为 msgEntry，同时保留原始字节与大小
// （供值精确删除与 byte_size 回扣）。不可解析的条目跳过，但不从 raws 中移除
// ——清理必须与 Enqueue 覆盖的内容一致。
func parseBufferMessages(messages []string) (firstMessageID string, msgs []msgEntry, raws []string, sizes []int64) {
	raws = make([]string, 0, len(messages))
	sizes = make([]int64, 0, len(messages))
	for _, raw := range messages {
		var msg BufferMessageRequest
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			continue
		}
		raws = append(raws, raw)
		sizes = append(sizes, int64(len(raw)))
		if firstMessageID == "" {
			firstMessageID = msg.MessageID
		}
		msgs = append(msgs, msgEntry{Role: msg.Role, Content: msg.Content})
	}
	return firstMessageID, msgs, raws, sizes
}

// acquireFlushLock 获取该 buffer key 的单飞锁。同一 key 同时只允许一个
// flusher 排水：两个实例（或 BufferScanner 与 BufferMessage 并发）若各自读
// 快照，Enqueue 按 firstMessageID 幂等去重会让后到者 no-op，但其清理仍可能
// 删掉自己快照里独有的新消息——单飞锁消除该发散。其他 flusher 持锁时返回
// (false, nil) 表示本轮跳过；返回的 release 仅在成功获取时非 nil。
func (b *MessageBuffer) acquireFlushLock(ctx context.Context, key string) (bool, func(), error) {
	lockKey := key + ":flush"
	lockToken, err := newFlushLockToken()
	if err != nil {
		return false, nil, fmt.Errorf("flush lock token: %w", err)
	}
	locked, err := b.store.TryAcquireFlushLock(ctx, lockKey, lockToken, constants.MemoryBufferFlushLockTTL)
	if err != nil {
		return false, nil, fmt.Errorf("redis acquire flush lock: %w", err)
	}
	if !locked {
		return false, nil, nil
	}
	return true, func() {
		_ = b.store.ReleaseFlushLock(ctx, lockKey, lockToken) // best-effort；TTL 兜底
	}, nil
}

// discardLowValueBatch deletes the batch (skipping extraction) when its
// non-tool content is below the minimum threshold. Returns true when the
// batch was discarded. On delete failure the batch is kept in place: a
// silently failed delete would re-enter this flush on every message
// (LLen >= K) forever.
func (b *MessageBuffer) discardLowValueBatch(ctx context.Context, key string, msgs []msgEntry, raws []string, sizes []int64) (bool, error) {
	contentRunes := 0
	for _, m := range msgs {
		if m.Role != "tool" {
			contentRunes += len([]rune(m.Content))
		}
	}
	if contentRunes < constants.MemoryBufferMinContentRunes {
		metaKey := "memory:buffer:meta:" + key[len("memory:buffer:"):]
		if err := b.store.RemoveValues(ctx, key, metaKey, raws, sizes); err != nil {
			return false, fmt.Errorf("redis remove values: %w", err)
		}
		return true, nil
	}
	return false, nil
}

// newFlushLockToken 生成一次性锁 token（crypto/rand），避免跨实例 token
// 碰撞导致误释放别人的锁。
func newFlushLockToken() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
