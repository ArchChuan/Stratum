package application

import (
	"context"
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
	messages, err := b.store.LRange(ctx, key, 0, -1)
	if err != nil {
		return fmt.Errorf("redis lrange: %w", err)
	}
	if len(messages) == 0 {
		return nil
	}

	var firstMessageID string
	var msgs []msgEntry
	for _, raw := range messages {
		var msg BufferMessageRequest
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			continue
		}
		if firstMessageID == "" {
			firstMessageID = msg.MessageID
		}
		msgs = append(msgs, msgEntry{Role: msg.Role, Content: msg.Content})
	}
	if len(msgs) == 0 {
		return nil
	}

	// Quality gate: skip extraction if non-tool content is below minimum threshold.
	// Prevents low-value flushes where messages are all tool outputs or short acknowledgements.
	discarded, err := b.discardLowValueBatch(ctx, key, msgs)
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
	if err := b.store.Del(ctx, key, metaKey); err != nil {
		return fmt.Errorf("redis del: %w", err)
	}
	return nil
}

// discardLowValueBatch deletes the batch (skipping extraction) when its
// non-tool content is below the minimum threshold. Returns true when the
// batch was discarded. On delete failure the batch is kept in place: a
// silently failed delete would re-enter this flush on every message
// (LLen >= K) forever.
func (b *MessageBuffer) discardLowValueBatch(ctx context.Context, key string, msgs []msgEntry) (bool, error) {
	contentRunes := 0
	for _, m := range msgs {
		if m.Role != "tool" {
			contentRunes += len([]rune(m.Content))
		}
	}
	if contentRunes < constants.MemoryBufferMinContentRunes {
		metaKey := "memory:buffer:meta:" + key[len("memory:buffer:"):]
		if err := b.store.Del(ctx, key, metaKey); err != nil {
			return false, fmt.Errorf("redis del: %w", err)
		}
		return true, nil
	}
	return false, nil
}
