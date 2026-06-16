package pipeline

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryRawEvent_RoundTrip(t *testing.T) {
	ev := &MemoryRawEvent{
		MessageID:      "msg-123",
		ConversationID: "conv-456",
		TenantID:       "tenant-1",
		UserID:         "user-1",
		AgentID:        "agent-1",
		Role:           "user",
		Content:        "Hello world",
		CreatedAt:      time.Now().UTC().Truncate(time.Millisecond),
	}
	data, err := ev.Marshal()
	require.NoError(t, err)

	got, err := UnmarshalRawEvent(data)
	require.NoError(t, err)
	assert.Equal(t, ev, got)
}

func TestMemoryEnrichedEvent_RoundTrip(t *testing.T) {
	ev := &MemoryEnrichedEvent{
		MemoryRawEvent: MemoryRawEvent{
			MessageID:      "msg-789",
			ConversationID: "conv-abc",
			TenantID:       "tenant-2",
			UserID:         "user-2",
			AgentID:        "agent-2",
			Role:           "agent",
			Content:        "Response text",
			CreatedAt:      time.Now().UTC().Truncate(time.Millisecond),
		},
		VectorID: "vec-001",
	}
	data, err := ev.Marshal()
	require.NoError(t, err)

	got, err := UnmarshalEnrichedEvent(data)
	require.NoError(t, err)
	assert.Equal(t, ev, got)
}
