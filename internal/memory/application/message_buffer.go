package application

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/timeutil"
)

// MessageBuffer accumulates messages in Redis and flushes when K=5, size>=8KB, or T=2min.
type MessageBuffer struct {
	redis *redis.Client
	queue port.ExtractionQueue
}

func NewMessageBuffer(redisClient *redis.Client, queue port.ExtractionQueue) *MessageBuffer {
	return &MessageBuffer{redis: redisClient, queue: queue}
}

// BufferMessage accumulates a message in Redis; flushes if K>=5, size>=8KB, or oldest >2min.
func (b *MessageBuffer) BufferMessage(ctx context.Context, req *BufferMessageRequest) error {
	if b.redis == nil {
		return fmt.Errorf("redis client not configured")
	}

	key := fmt.Sprintf("memory:buffer:%s:%s:%s:%s", req.TenantID, req.UserID, req.AgentID, req.ConversationID)
	metaKey := "memory:buffer:meta:" + key[len("memory:buffer:"):]

	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal buffer message: %w", err)
	}

	if err := b.redis.RPush(ctx, key, data).Err(); err != nil {
		return fmt.Errorf("redis rpush: %w", err)
	}
	b.redis.Expire(ctx, key, constants.MemoryBufferKeyTTL)

	// Update meta: first_at (only if not set), last_at, scope, byte_size
	now := timeutil.Now().Format(time.RFC3339)
	b.redis.HSetNX(ctx, metaKey, "first_at", now)
	newSize, _ := b.redis.HIncrBy(ctx, metaKey, "byte_size", int64(len(data))).Result()
	b.redis.HSet(ctx, metaKey, "last_at", now, "scope", req.Scope)
	b.redis.Expire(ctx, metaKey, constants.MemoryBufferKeyTTL)

	// Flush condition: K>=5
	count, err := b.redis.LLen(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("redis llen: %w", err)
	}
	if count >= int64(constants.MemoryBufferFlushSize) {
		return b.flush(ctx, key, req.TenantID, req.UserID, req.AgentID, req.ConversationID, req.Scope)
	}

	// Flush condition: size >= 8KB
	if newSize >= int64(constants.MemoryBufferSizeLimit) {
		return b.flush(ctx, key, req.TenantID, req.UserID, req.AgentID, req.ConversationID, req.Scope)
	}

	// Flush condition: oldest message > 2min
	oldest, err := b.redis.LIndex(ctx, key, 0).Result()
	if err == redis.Nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("redis lindex: %w", err)
	}
	var oldestMsg BufferMessageRequest
	if err := json.Unmarshal([]byte(oldest), &oldestMsg); err != nil {
		return fmt.Errorf("unmarshal oldest message: %w", err)
	}
	if time.Since(oldestMsg.CreatedAt) >= constants.MemoryBufferFlushInterval {
		return b.flush(ctx, key, req.TenantID, req.UserID, req.AgentID, req.ConversationID, req.Scope)
	}
	return nil
}

// flush reads all messages from Redis, enqueues extraction task, deletes list and meta.
func (b *MessageBuffer) flush(ctx context.Context, key, tenantID, userID, agentID, conversationID, scope string) error {
	messages, err := b.redis.LRange(ctx, key, 0, -1).Result()
	if err != nil {
		return fmt.Errorf("redis lrange: %w", err)
	}
	if len(messages) == 0 {
		return nil
	}

	type msgEntry struct {
		Role    string `json:"role"`
		Content string `json:"content"`
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
	if err := b.redis.Del(ctx, key, metaKey).Err(); err != nil {
		return fmt.Errorf("redis del: %w", err)
	}
	return nil
}
