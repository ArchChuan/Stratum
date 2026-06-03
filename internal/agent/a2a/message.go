// Package a2a provides agent-to-agent communication and orchestration.
package a2a

import (
	"time"

	"github.com/google/uuid"
)

// MessageType defines the type of A2A message
type MessageType string

const (
	// Discovery messages
	MessageTypeDiscoveryRequest       MessageType = "discovery.request"
	MessageTypeDiscoveryResponse      MessageType = "discovery.response"
	MessageTypeCapabilityAnnouncement MessageType = "capability.announcement"
	MessageTypeAgentDeparture         MessageType = "agent.departure"

	// Negotiation messages
	MessageTypeTaskProposal MessageType = "task.proposal"
	MessageTypeTaskResponse MessageType = "task.response"
	MessageTypeTaskStart    MessageType = "task.start"
	MessageTypeTaskComplete MessageType = "task.complete"
	MessageTypeTaskCancel   MessageType = "task.cancel"

	// Collaboration messages
	MessageTypeCollaborationProposal  MessageType = "collaboration.proposal"
	MessageTypeCollaborationAccept    MessageType = "collaboration.accept"
	MessageTypeCollaborationReject    MessageType = "collaboration.reject"
	MessageTypeCollaborationStarted   MessageType = "collaboration.started"
	MessageTypeCollaborationCompleted MessageType = "collaboration.completed"
	MessageTypeJoinCollaboration      MessageType = "collaboration.join"
	MessageTypeLeaveCollaboration     MessageType = "collaboration.leave"

	// Data exchange messages
	MessageTypeDataRequest  MessageType = "data.request"
	MessageTypeDataResponse MessageType = "data.response"
	MessageTypeDataExchange MessageType = "data.exchange"

	// Control messages
	MessageTypeHeartbeat      MessageType = "control.heartbeat"
	MessageTypeProgressUpdate MessageType = "control.progress"
	MessageTypeError          MessageType = "control.error"
)

// Priority defines message priority
type Priority string

const (
	PriorityLow    Priority = "low"
	PriorityNormal Priority = "normal"
	PriorityHigh   Priority = "high"
	PriorityUrgent Priority = "urgent"
)

// AgentIdentity represents an agent's identity
type AgentIdentity struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Endpoint string            `json:"endpoint"`
	Labels   map[string]string `json:"labels,omitempty"`
	LastSeen time.Time         `json:"last_seen"`
}

// Capability represents an agent's capability
type Capability struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Version     string            `json:"version"`
	Parameters  map[string]string `json:"parameters,omitempty"`
}

// Message represents an A2A protocol message
type Message struct {
	ID        string                 `json:"id"`
	Type      MessageType            `json:"type"`
	From      AgentIdentity          `json:"from"`
	To        AgentIdentity          `json:"to"`
	Timestamp time.Time              `json:"timestamp"`
	Payload   map[string]interface{} `json:"payload"`
	Priority  Priority               `json:"priority,omitempty"`
	InReplyTo string                 `json:"in_reply_to,omitempty"`
	ReplyTo   string                 `json:"reply_to,omitempty"`
	Headers   map[string]string      `json:"headers,omitempty"`
	Retries   int                    `json:"retries,omitempty"`
	TraceID   string                 `json:"trace_id,omitempty"`
	SpanID    string                 `json:"span_id,omitempty"`
}

// NewMessage creates a new message
func NewMessage(msgType MessageType, from AgentIdentity) *Message {
	return &Message{
		ID:        generateMessageID(),
		Type:      msgType,
		From:      from,
		Timestamp: time.Now(),
		Payload:   make(map[string]interface{}),
		Headers:   make(map[string]string),
		Retries:   0,
	}
}

// WithRecipient sets the message recipient
func (m *Message) WithRecipient(to AgentIdentity) *Message {
	m.To = to
	return m
}

// WithPayload adds payload data
func (m *Message) WithPayload(key string, value interface{}) *Message {
	if m.Payload == nil {
		m.Payload = make(map[string]interface{})
	}
	m.Payload[key] = value
	return m
}

// WithPriority sets message priority
func (m *Message) WithPriority(priority Priority) *Message {
	m.Priority = priority
	return m
}

// WithReplyTo sets the reply-to message ID
func (m *Message) WithReplyTo(messageID string) *Message {
	m.InReplyTo = messageID
	return m
}

// WithTrace adds trace context
func (m *Message) WithTrace(traceID, spanID string) *Message {
	m.TraceID = traceID
	m.SpanID = spanID
	return m
}

// Clone creates a copy of the message
func (m *Message) Clone() *Message {
	headers := make(map[string]string, len(m.Headers))
	for k, v := range m.Headers {
		headers[k] = v
	}
	payload := make(map[string]interface{}, len(m.Payload))
	for k, v := range m.Payload {
		payload[k] = v
	}
	return &Message{
		ID:        m.ID,
		Type:      m.Type,
		From:      m.From,
		To:        m.To,
		Timestamp: m.Timestamp,
		Payload:   payload,
		Priority:  m.Priority,
		InReplyTo: m.InReplyTo,
		ReplyTo:   m.ReplyTo,
		Headers:   headers,
		Retries:   m.Retries,
		TraceID:   m.TraceID,
		SpanID:    m.SpanID,
	}
}

// generateMessageID generates a unique message ID
func generateMessageID() string {
	return uuid.New().String()
}

// currentTime returns the current time
func currentTime() time.Time {
	return time.Now()
}
