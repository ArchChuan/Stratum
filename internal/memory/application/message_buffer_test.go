package application

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestMessageBuffer_BufferMessage_NoRedis(t *testing.T) {
	queue := new(MockExtractionQueue)
	buffer := NewMessageBuffer(nil, queue)

	req := &BufferMessageRequest{
		TenantID:       "tenant1",
		UserID:         "user1",
		AgentID:        "agent1",
		ConversationID: "conv1",
		MessageID:      "msg1",
		Role:           "user",
		Content:        "test",
		CreatedAt:      time.Now(),
	}

	err := buffer.BufferMessage(context.Background(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "redis client not configured")
}

// Integration tests requiring Redis are skipped in -short mode
func TestBufferMessage_FlushAtK5(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	// TODO: implement with real Redis in integration tests
}

func TestBufferMessage_FlushAt2Min(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	// TODO: implement with real Redis in integration tests
}
