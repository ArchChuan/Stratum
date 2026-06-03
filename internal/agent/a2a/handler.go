// Package a2a provides agent-to-agent communication and orchestration.
package a2a

import (
	"context"
	"sync"

	"go.uber.org/zap"
)

// ProtocolHandler handles A2A message routing
type ProtocolHandler struct {
	inbox    chan *Message
	outbox   chan *Message
	handlers map[MessageType][]func(context.Context, *Message) (*Message, error)
	mu       sync.RWMutex
	logger   *zap.Logger
	running  bool
	wg       sync.WaitGroup
}

// NewProtocolHandler creates a new protocol handler
func NewProtocolHandler(logger *zap.Logger) *ProtocolHandler {
	h := &ProtocolHandler{
		inbox:    make(chan *Message, 1000),
		outbox:   make(chan *Message, 1000),
		handlers: make(map[MessageType][]func(context.Context, *Message) (*Message, error)),
		logger:   logger.Named("handler"),
		running:  false,
	}

	h.registerDefaultHandlers()
	return h
}

// Start starts the handler
func (h *ProtocolHandler) Start(ctx context.Context) error {
	h.mu.Lock()
	if h.running {
		h.mu.Unlock()
		return nil
	}
	h.running = true
	h.mu.Unlock()

	h.wg.Add(2)
	go h.processInbox(ctx)
	go h.processOutbox(ctx)

	h.logger.Info("protocol handler started")
	return nil
}

// Stop stops the handler
func (h *ProtocolHandler) Stop() {
	h.mu.Lock()
	h.running = false
	h.mu.Unlock()

	close(h.inbox)
	close(h.outbox)
	h.wg.Wait()

	h.logger.Info("protocol handler stopped")
}

// RegisterHandler registers a message handler
func (h *ProtocolHandler) RegisterHandler(msgType MessageType, handler func(context.Context, *Message) (*Message, error)) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.handlers[msgType] = append(h.handlers[msgType], handler)
	h.logger.Debug("handler registered",
		zap.String("message_type", string(msgType)))
}

// Receive receives a message
func (h *ProtocolHandler) Receive(msg *Message) {
	h.inbox <- msg
}

// SendMessage sends a message
func (h *ProtocolHandler) SendMessage(ctx context.Context, msg *Message, replyTo string) error {
	if replyTo != "" {
		msg.InReplyTo = replyTo
	}
	h.outbox <- msg
	return nil
}

// processInbox processes incoming messages
func (h *ProtocolHandler) processInbox(ctx context.Context) {
	defer h.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-h.inbox:
			if !ok {
				return
			}
			h.handleMessage(ctx, msg)
		}
	}
}

// processOutbox processes outgoing messages
func (h *ProtocolHandler) processOutbox(ctx context.Context) {
	defer h.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-h.outbox:
			if !ok {
				return
			}
			// In a real implementation, this would send via the transport layer
			// For now, we just log it
			h.logger.Debug("message sent",
				zap.String("type", string(msg.Type)),
				zap.String("to", msg.To.ID),
				zap.String("from", msg.From.ID))
		}
	}
}

// handleMessage routes a message to its handlers
func (h *ProtocolHandler) handleMessage(ctx context.Context, msg *Message) {
	h.mu.RLock()
	handlers, exists := h.handlers[msg.Type]
	h.mu.RUnlock()

	if !exists {
		h.logger.Warn("no handler for message type",
			zap.String("message_type", string(msg.Type)))
		return
	}

	for _, handler := range handlers {
		reply, err := handler(ctx, msg)
		if err != nil {
			h.logger.Error("handler error",
				zap.String("message_type", string(msg.Type)),
				zap.Error(err))
			continue
		}

		if reply != nil {
			h.outbox <- reply
		}
	}
}

// registerDefaultHandlers registers default message handlers
func (h *ProtocolHandler) registerDefaultHandlers() {
	// Discovery handlers
	h.RegisterHandler(MessageTypeDiscoveryRequest, func(ctx context.Context, msg *Message) (*Message, error) {
		h.logger.Info("discovery request received",
			zap.String("from", msg.From.ID))

		reply := NewMessage(MessageTypeDiscoveryResponse, msg.To).
			WithRecipient(msg.From).
			WithPayload("capabilities", []Capability{}).
			WithReplyTo(msg.ID)

		return reply, nil
	})

	// Negotiation handlers
	h.RegisterHandler(MessageTypeTaskProposal, func(ctx context.Context, msg *Message) (*Message, error) {
		h.logger.Info("task proposal received",
			zap.String("from", msg.From.ID),
			zap.String("description", msg.Payload["description"].(string)))

		// Default: reject all tasks
		reply := NewMessage(MessageTypeTaskResponse, msg.To).
			WithRecipient(msg.From).
			WithPayload("response", "rejected").
			WithPayload("reason", "no handler configured").
			WithReplyTo(msg.ID)

		return reply, nil
	})

	// Collaboration handlers
	h.RegisterHandler(MessageTypeCollaborationProposal, func(ctx context.Context, msg *Message) (*Message, error) {
		collabID := msg.Payload["collaboration_id"].(string)
		h.logger.Info("collaboration proposal received",
			zap.String("from", msg.From.ID),
			zap.String("collaboration_id", collabID))

		// Default: accept collaborations
		reply := NewMessage(MessageTypeCollaborationAccept, msg.To).
			WithRecipient(msg.From).
			WithPayload("collaboration_id", collabID).
			WithPayload("agent_id", msg.To.ID).
			WithReplyTo(msg.ID)

		return reply, nil
	})

	// Heartbeat handlers
	h.RegisterHandler(MessageTypeHeartbeat, func(ctx context.Context, msg *Message) (*Message, error) {
		// Just log heartbeat, no reply needed
		h.logger.Debug("heartbeat received",
			zap.String("from", msg.From.ID))
		return nil, nil
	})

	// Progress handlers
	h.RegisterHandler(MessageTypeProgressUpdate, func(ctx context.Context, msg *Message) (*Message, error) {
		h.logger.Info("progress update",
			zap.String("task_id", msg.Payload["task_id"].(string)),
			zap.Int("progress", msg.Payload["progress"].(int)))
		return nil, nil
	})
}
