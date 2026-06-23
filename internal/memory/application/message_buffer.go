package application

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// MessageBuffer accumulates messages in Redis and flushes when K=5 or T=2min.
type MessageBuffer struct {
	redis *redis.Client
	queue port.ExtractionQueue
}

// NewMessageBuffer constructs a MessageBuffer.
func NewMessageBuffer(redisClient *redis.Client, queue port.ExtractionQueue) *MessageBuffer {
	return &MessageBuffer{
		redis: redisClient,
		queue: queue,
	}
}

// BufferMessage accumulates a message in Redis; flushes if K>=5 or oldest >2min.
func (b *MessageBuffer) BufferMessage(ctx context.Context, req *BufferMessageRequest) error {
	if b.redis == nil {
		return fmt.Errorf("redis client not configured")
	}

	key := fmt.Sprintf("memory:buffer:%s:%s:%s:%s", req.TenantID, req.UserID, req.AgentID, req.ConversationID)

	// Marshal and push to list
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal buffer message: %w", err)
	}

	if err := b.redis.RPush(ctx, key, data).Err(); err != nil {
		return fmt.Errorf("redis rpush: %w", err)
	}

	// Check flush condition: K>=5
	count, err := b.redis.LLen(ctx, key).Result()
	if err != nil {
		return fmt.Errorf("redis llen: %w", err)
	}

	if count >= int64(constants.MemoryBufferFlushSize) {
		return b.flush(ctx, key, req.TenantID, req.UserID, req.AgentID, req.ConversationID)
	}

	// Check flush condition: oldest message >2min
	oldest, err := b.redis.LIndex(ctx, key, 0).Result()
	if err == redis.Nil {
		return nil // empty list, nothing to flush
	}
	if err != nil {
		return fmt.Errorf("redis lindex: %w", err)
	}

	var oldestMsg BufferMessageRequest
	if err := json.Unmarshal([]byte(oldest), &oldestMsg); err != nil {
		return fmt.Errorf("unmarshal oldest message: %w", err)
	}

	if time.Since(oldestMsg.CreatedAt) >= constants.MemoryBufferFlushInterval {
		return b.flush(ctx, key, req.TenantID, req.UserID, req.AgentID, req.ConversationID)
	}

	return nil
}

// flush reads all messages from Redis, enqueues extraction task, deletes list.
func (b *MessageBuffer) flush(ctx context.Context, key, tenantID, userID, agentID, conversationID string) error {
	messages, err := b.redis.LRange(ctx, key, 0, -1).Result()
	if err != nil {
		return fmt.Errorf("redis lrange: %w", err)
	}

	if len(messages) == 0 {
		return nil
	}

	var messageIDs []string
	var payloads []string

	for _, raw := range messages {
		var msg BufferMessageRequest
		if err := json.Unmarshal([]byte(raw), &msg); err != nil {
			continue // skip malformed
		}
		messageIDs = append(messageIDs, msg.MessageID)
		payloads = append(payloads, msg.Content)
	}

	// Enqueue extraction task
	task := &port.ExtractionTask{
		MessageID:  messageIDs[0], // representative ID
		UserID:     userID,
		AgentID:    agentID,
		Content:    fmt.Sprintf("%d messages", len(payloads)),
		Status:     "pending",
		RetryCount: 0,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if err := b.queue.Enqueue(ctx, task); err != nil {
		return fmt.Errorf("enqueue extraction: %w", err)
	}

	// Delete Redis list
	if err := b.redis.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("redis del: %w", err)
	}

	return nil
}
