package pipeline

import (
	"encoding/json"
	"time"
)

type MemoryRawEvent struct {
	MessageID      string    `json:"message_id"`
	ConversationID string    `json:"conversation_id"`
	TenantID       string    `json:"tenant_id"`
	UserID         string    `json:"user_id"`
	AgentID        string    `json:"agent_id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
	TraceID        string    `json:"trace_id,omitempty"`
}

func (e *MemoryRawEvent) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

func UnmarshalRawEvent(data []byte) (*MemoryRawEvent, error) {
	var ev MemoryRawEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, err
	}
	ev.CreatedAt = ev.CreatedAt.UTC()
	return &ev, nil
}

type MemoryEnrichedEvent struct {
	MemoryRawEvent
	VectorID string `json:"vector_id"`
}

func (e *MemoryEnrichedEvent) Marshal() ([]byte, error) {
	return json.Marshal(e)
}

func UnmarshalEnrichedEvent(data []byte) (*MemoryEnrichedEvent, error) {
	var ev MemoryEnrichedEvent
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, err
	}
	ev.CreatedAt = ev.CreatedAt.UTC()
	return &ev, nil
}
