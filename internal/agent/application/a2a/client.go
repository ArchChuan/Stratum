// Package a2a provides agent-to-agent communication and orchestration.
package a2a

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// A2AClient provides a client interface for agents to participate in the A2A protocol
type A2AClient struct {
	protocol       *A2AProtocol
	agentID        string
	agentName      string
	capabilities   []Capability
	logger         *zap.Logger
	mu             sync.RWMutex
	listeners      map[MessageType][]MessageHandler
	replyChans     map[string]chan *Message
	collaborations map[string]*CollaborationSession
}

// MessageHandler defines a handler function for incoming messages
type MessageHandler func(ctx context.Context, msg *Message) (*Message, error)

// CollaborationSession represents an active collaboration
type CollaborationSession struct {
	ID              string
	CollaborationID string
	Role            string
	Participants    []AgentIdentity
	State           string
	CreatedAt       time.Time
	SharedData      map[string]interface{}
	mu              sync.RWMutex
}

// NewA2AClient creates a new A2A client for an agent
func NewA2AClient(protocol *A2AProtocol, agentID, agentName string, logger *zap.Logger) *A2AClient {
	client := &A2AClient{
		protocol:       protocol,
		agentID:        agentID,
		agentName:      agentName,
		capabilities:   []Capability{},
		logger:         logger.With(zap.String("agent_id", agentID)),
		listeners:      make(map[MessageType][]MessageHandler),
		replyChans:     make(map[string]chan *Message),
		collaborations: make(map[string]*CollaborationSession),
	}

	// Register default message handlers
	client.registerDefaultHandlers()

	return client
}

// AnnounceCapabilities announces the agent's capabilities to the network
func (c *A2AClient) AnnounceCapabilities(ctx context.Context, capabilities []Capability) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.capabilities = capabilities

	msg := &Message{
		Type:      MessageTypeCapabilityAnnouncement,
		From:      AgentIdentity{ID: c.agentID, Name: c.agentName},
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"capabilities": capabilities,
		},
	}

	c.logger.Info("announcing capabilities",
		zap.Int("count", len(capabilities)),
		zap.Strings("names", getCapabilityNames(capabilities)))

	return c.protocol.handler.SendMessage(ctx, msg, "")
}

// DiscoverAgents discovers agents with specific capabilities
func (c *A2AClient) DiscoverAgents(ctx context.Context, requiredCapabilities []string) ([]AgentIdentity, error) {
	c.logger.Info("discovering agents",
		zap.Strings("required_capabilities", requiredCapabilities))

	// Use discovery service to find matching agents
	peers := c.protocol.discovery.GetPeersWithCapabilities(requiredCapabilities)

	identities := make([]AgentIdentity, 0, len(peers))
	for _, peer := range peers {
		identities = append(identities, peer.Identity)
	}

	c.logger.Info("discovered agents",
		zap.Int("count", len(identities)))

	return identities, nil
}

// ProposeTask proposes a task to another agent
func (c *A2AClient) ProposeTask(ctx context.Context, to AgentIdentity, taskDescription string, requirements []string) (string, error) {
	msg := &Message{
		Type:      MessageTypeTaskProposal,
		From:      AgentIdentity{ID: c.agentID, Name: c.agentName},
		To:        to,
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"task_id":            generateMessageID(),
			"description":        taskDescription,
			"requirements":       requirements,
			"proposed_by":        c.agentID,
			"estimated_duration": 300,
			"priority":           PriorityNormal,
		},
	}

	c.logger.Info("proposing task",
		zap.String("to", to.ID),
		zap.String("task", taskDescription))

	if err := c.protocol.handler.SendMessage(ctx, msg, ""); err != nil {
		return "", fmt.Errorf("failed to propose task: %w", err)
	}

	replyChan := make(chan *Message, 1)
	c.mu.Lock()
	c.replyChans[msg.ID] = replyChan
	c.mu.Unlock()

	select {
	case reply := <-replyChan:
		c.mu.Lock()
		delete(c.replyChans, msg.ID)
		c.mu.Unlock()

		if reply.Type == MessageTypeTaskResponse {
			if _, ok := reply.Payload["response"].(string); ok {
				return msg.ID, nil
			}
		}
		return "", fmt.Errorf("task rejected by agent %s", to.ID)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.replyChans, msg.ID)
		c.mu.Unlock()
		return "", ctx.Err()
	case <-time.After(c.protocol.config.TaskTimeout):
		c.mu.Lock()
		delete(c.replyChans, msg.ID)
		c.mu.Unlock()
		return "", fmt.Errorf("task proposal timeout")
	}
}

// AcceptTask accepts a proposed task
func (c *A2AClient) AcceptTask(ctx context.Context, proposalID string) error {
	msg := &Message{
		Type:      MessageTypeTaskResponse,
		From:      AgentIdentity{ID: c.agentID, Name: c.agentName},
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"proposal_id": proposalID,
			"response":    "accepted",
		},
	}

	c.logger.Info("accepting task",
		zap.String("proposal_id", proposalID))

	return c.protocol.handler.SendMessage(ctx, msg, "")
}

// RejectTask rejects a proposed task
func (c *A2AClient) RejectTask(ctx context.Context, proposalID string, reason string) error {
	msg := &Message{
		Type:      MessageTypeTaskResponse,
		From:      AgentIdentity{ID: c.agentID, Name: c.agentName},
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"proposal_id": proposalID,
			"response":    "rejected",
			"reason":      reason,
		},
	}

	c.logger.Info("rejecting task",
		zap.String("proposal_id", proposalID),
		zap.String("reason", reason))

	return c.protocol.handler.SendMessage(ctx, msg, "")
}

// RequestCollaboration initiates a collaboration
func (c *A2AClient) RequestCollaboration(ctx context.Context, participants []AgentIdentity, taskDescription string, strategy CollaborationStrategy) (string, error) {
	collabID := generateMessageID()

	msg := &Message{
		Type:      MessageTypeCollaborationProposal,
		From:      AgentIdentity{ID: c.agentID, Name: c.agentName},
		To:        AgentIdentity{ID: "broadcast"},
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"collaboration_id": collabID,
			"task_description": taskDescription,
			"strategy":         strategy,
			"initiator":        c.agentID,
			"participants":     participants,
		},
	}

	c.logger.Info("requesting collaboration",
		zap.String("collab_id", collabID),
		zap.Int("participants", len(participants)),
		zap.String("strategy", string(strategy)))

	if err := c.protocol.handler.SendMessage(ctx, msg, ""); err != nil {
		return "", fmt.Errorf("failed to request collaboration: %w", err)
	}

	return collabID, nil
}

// JoinCollaboration joins an existing collaboration
func (c *A2AClient) JoinCollaboration(ctx context.Context, collaborationID string) error {
	msg := &Message{
		Type:      MessageTypeJoinCollaboration,
		From:      AgentIdentity{ID: c.agentID, Name: c.agentName},
		To:        AgentIdentity{ID: "broadcast"},
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"collaboration_id": collaborationID,
			"agent_id":         c.agentID,
		},
	}

	c.logger.Info("joining collaboration",
		zap.String("collab_id", collaborationID))

	return c.protocol.handler.SendMessage(ctx, msg, "")
}

// LeaveCollaboration leaves a collaboration
func (c *A2AClient) LeaveCollaboration(ctx context.Context, collaborationID string) error {
	msg := &Message{
		Type:      MessageTypeLeaveCollaboration,
		From:      AgentIdentity{ID: c.agentID, Name: c.agentName},
		To:        AgentIdentity{ID: "broadcast"},
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"collaboration_id": collaborationID,
			"agent_id":         c.agentID,
		},
	}

	c.logger.Info("leaving collaboration",
		zap.String("collab_id", collaborationID))

	c.mu.Lock()
	delete(c.collaborations, collaborationID)
	c.mu.Unlock()

	return c.protocol.handler.SendMessage(ctx, msg, "")
}

// ReportProgress reports progress on a task
func (c *A2AClient) ReportProgress(ctx context.Context, taskID string, progress int, details string) error {
	msg := &Message{
		Type:      MessageTypeProgressUpdate,
		From:      AgentIdentity{ID: c.agentID, Name: c.agentName},
		To:        AgentIdentity{ID: "broadcast"},
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"task_id":  taskID,
			"progress": progress,
			"details":  details,
			"agent_id": c.agentID,
		},
	}

	c.logger.Info("reporting progress",
		zap.String("task_id", taskID),
		zap.Int("progress", progress))

	return c.protocol.handler.SendMessage(ctx, msg, "")
}

// SendData sends data to another agent
func (c *A2AClient) SendData(ctx context.Context, to AgentIdentity, dataType string, data interface{}) (string, error) {
	msg := &Message{
		Type:      MessageTypeDataExchange,
		From:      AgentIdentity{ID: c.agentID, Name: c.agentName},
		To:        to,
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"data_type": dataType,
			"data":      data,
		},
	}

	c.logger.Info("sending data",
		zap.String("to", to.ID),
		zap.String("data_type", dataType))

	if err := c.protocol.handler.SendMessage(ctx, msg, ""); err != nil {
		return "", fmt.Errorf("failed to send data: %w", err)
	}

	return msg.ID, nil
}

// RequestData requests data from another agent
func (c *A2AClient) RequestData(ctx context.Context, from AgentIdentity, dataType string, parameters map[string]interface{}) (interface{}, error) {
	msg := &Message{
		Type:      MessageTypeDataRequest,
		From:      AgentIdentity{ID: c.agentID, Name: c.agentName},
		To:        from,
		Timestamp: time.Now(),
		Payload: map[string]interface{}{
			"data_type":  dataType,
			"parameters": parameters,
		},
	}

	c.logger.Info("requesting data",
		zap.String("from", from.ID),
		zap.String("data_type", dataType))

	replyChan := make(chan *Message, 1)
	c.mu.Lock()
	c.replyChans[msg.ID] = replyChan
	c.mu.Unlock()

	if err := c.protocol.handler.SendMessage(ctx, msg, ""); err != nil {
		return "", fmt.Errorf("failed to request data: %w", err)
	}

	select {
	case reply := <-replyChan:
		c.mu.Lock()
		delete(c.replyChans, msg.ID)
		c.mu.Unlock()

		if reply.Type == MessageTypeDataResponse {
			if data, ok := reply.Payload["data"]; ok {
				return data, nil
			}
		}
		return nil, fmt.Errorf("data request failed")
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.replyChans, msg.ID)
		c.mu.Unlock()
		return nil, ctx.Err()
	case <-time.After(c.protocol.config.TaskTimeout):
		c.mu.Lock()
		delete(c.replyChans, msg.ID)
		c.mu.Unlock()
		return nil, fmt.Errorf("data request timeout")
	}
}

// Broadcast broadcasts a message to all agents
func (c *A2AClient) Broadcast(ctx context.Context, messageType MessageType, payload map[string]interface{}) error {
	msg := &Message{
		Type:      messageType,
		From:      AgentIdentity{ID: c.agentID, Name: c.agentName},
		To:        AgentIdentity{ID: "broadcast"},
		Timestamp: time.Now(),
		Payload:   payload,
	}

	c.logger.Info("broadcasting message",
		zap.String("type", string(messageType)))

	return c.protocol.handler.SendMessage(ctx, msg, "")
}

// RegisterHandler registers a handler for a specific message type
func (c *A2AClient) RegisterHandler(messageType MessageType, handler MessageHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.listeners[messageType] = append(c.listeners[messageType], handler)
	c.logger.Debug("registered handler",
		zap.String("message_type", string(messageType)))
}

// GetCapabilities returns the agent's capabilities
func (c *A2AClient) GetCapabilities() []Capability {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.capabilities
}

// GetCollaboration returns an active collaboration session
func (c *A2AClient) GetCollaboration(collabID string) *CollaborationSession {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.collaborations[collabID]
}

// GetActiveCollaborations returns all active collaborations
func (c *A2AClient) GetActiveCollaborations() []*CollaborationSession {
	c.mu.RLock()
	defer c.mu.RUnlock()

	sessions := make([]*CollaborationSession, 0, len(c.collaborations))
	for _, session := range c.collaborations {
		sessions = append(sessions, session)
	}
	return sessions
}

// GetAgentID returns the client's agent ID
func (c *A2AClient) GetAgentID() string {
	return c.agentID
}

// registerDefaultHandlers registers default message handlers
func (c *A2AClient) registerDefaultHandlers() {
	// Handle task proposals
	c.RegisterHandler(MessageTypeTaskProposal, func(ctx context.Context, msg *Message) (*Message, error) {
		c.logger.Info("received task proposal",
			zap.String("from", msg.From.ID),
			zap.String("description", msg.Payload["description"].(string)))

		// Reply channel will be used for the response
		return nil, nil
	})

	// Handle data requests
	c.RegisterHandler(MessageTypeDataRequest, func(ctx context.Context, msg *Message) (*Message, error) {
		c.logger.Info("received data request",
			zap.String("from", msg.From.ID),
			zap.String("data_type", msg.Payload["data_type"].(string)))

		// Default response: data not available
		reply := &Message{
			Type:      MessageTypeDataResponse,
			From:      AgentIdentity{ID: c.agentID, Name: c.agentName},
			To:        msg.From,
			Timestamp: time.Now(),
			Payload: map[string]interface{}{
				"data":  nil,
				"error": "data not available",
			},
		}
		return reply, nil
	})

	// Handle collaboration proposals
	c.RegisterHandler(MessageTypeCollaborationProposal, func(ctx context.Context, msg *Message) (*Message, error) {
		collabID := msg.Payload["collaboration_id"].(string)

		c.logger.Info("received collaboration proposal",
			zap.String("from", msg.From.ID),
			zap.String("collab_id", collabID))

		// Create a collaboration session
		session := &CollaborationSession{
			ID:              generateMessageID(),
			CollaborationID: collabID,
			Role:            "participant",
			Participants:    msg.Payload["participants"].([]AgentIdentity),
			State:           "invited",
			CreatedAt:       time.Now(),
			SharedData:      make(map[string]interface{}),
		}

		c.mu.Lock()
		c.collaborations[collabID] = session
		c.mu.Unlock()

		return nil, nil
	})

	// Handle collaboration started
	c.RegisterHandler(MessageTypeCollaborationStarted, func(ctx context.Context, msg *Message) (*Message, error) {
		collabID := msg.Payload["collaboration_id"].(string)
		c.mu.Lock()
		if session, ok := c.collaborations[collabID]; ok {
			session.mu.Lock()
			session.State = "active"
			session.mu.Unlock()
		}
		c.mu.Unlock()

		c.logger.Info("collaboration started",
			zap.String("collab_id", collabID))
		return nil, nil
	})

	// Handle collaboration completed
	c.RegisterHandler(MessageTypeCollaborationCompleted, func(ctx context.Context, msg *Message) (*Message, error) {
		collabID := msg.Payload["collaboration_id"].(string)
		c.mu.Lock()
		if session, ok := c.collaborations[collabID]; ok {
			session.mu.Lock()
			session.State = "completed"
			session.SharedData = msg.Payload["results"].(map[string]interface{})
			session.mu.Unlock()
		}
		c.mu.Unlock()

		c.logger.Info("collaboration completed",
			zap.String("collab_id", collabID))
		return nil, nil
	})

	// Handle progress updates
	c.RegisterHandler(MessageTypeProgressUpdate, func(ctx context.Context, msg *Message) (*Message, error) {
		taskID := msg.Payload["task_id"].(string)
		progress := msg.Payload["progress"].(int)

		c.logger.Info("progress update",
			zap.String("task_id", taskID),
			zap.Int("progress", progress),
			zap.String("agent", msg.From.ID))
		return nil, nil
	})

	// Handle join collaboration
	c.RegisterHandler(MessageTypeJoinCollaboration, func(ctx context.Context, msg *Message) (*Message, error) {
		collabID := msg.Payload["collaboration_id"].(string)
		agentID := msg.Payload["agent_id"].(string)

		c.logger.Info("agent joined collaboration",
			zap.String("collab_id", collabID),
			zap.String("agent_id", agentID))

		c.mu.RLock()
		if session, ok := c.collaborations[collabID]; ok {
			session.mu.Lock()
			session.Participants = append(session.Participants, msg.From)
			session.mu.Unlock()
		}
		c.mu.RUnlock()

		return nil, nil
	})

	// Handle leave collaboration
	c.RegisterHandler(MessageTypeLeaveCollaboration, func(ctx context.Context, msg *Message) (*Message, error) {
		collabID := msg.Payload["collaboration_id"].(string)
		agentID := msg.Payload["agent_id"].(string)

		c.logger.Info("agent left collaboration",
			zap.String("collab_id", collabID),
			zap.String("agent_id", agentID))

		c.mu.RLock()
		if session, ok := c.collaborations[collabID]; ok {
			session.mu.Lock()
			for i, p := range session.Participants {
				if p.ID == agentID {
					session.Participants = append(session.Participants[:i], session.Participants[i+1:]...)
					break
				}
			}
			session.mu.Unlock()
		}
		c.mu.RUnlock()

		return nil, nil
	})

	// Handle capability announcements
	c.RegisterHandler(MessageTypeCapabilityAnnouncement, func(ctx context.Context, msg *Message) (*Message, error) {
		c.logger.Info("capability announcement received",
			zap.String("from", msg.From.ID))
		return nil, nil
	})

	// Handle discovery requests
	c.RegisterHandler(MessageTypeDiscoveryRequest, func(ctx context.Context, msg *Message) (*Message, error) {
		c.logger.Info("discovery request received",
			zap.String("from", msg.From.ID))

		reply := &Message{
			Type:      MessageTypeDiscoveryResponse,
			From:      AgentIdentity{ID: c.agentID, Name: c.agentName},
			To:        msg.From,
			Timestamp: time.Now(),
			Payload: map[string]interface{}{
				"capabilities": c.GetCapabilities(),
			},
		}
		return reply, nil
	})

	// Handle data responses
	c.RegisterHandler(MessageTypeDataResponse, func(ctx context.Context, msg *Message) (*Message, error) {
		c.logger.Info("data response received",
			zap.String("from", msg.From.ID))

		// Route to waiting channel if exists
		c.mu.RLock()
		if replyChan, ok := c.replyChans[msg.InReplyTo]; ok {
			select {
			case replyChan <- msg:
			default:
			}
		}
		c.mu.RUnlock()

		return nil, nil
	})
}

// AddSharedData adds data to a collaboration's shared context
func (c *A2AClient) AddSharedData(collabID string, key string, value interface{}) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if session, ok := c.collaborations[collabID]; ok {
		session.mu.Lock()
		session.SharedData[key] = value
		session.mu.Unlock()
	}
}

// GetSharedData retrieves data from a collaboration's shared context
func (c *A2AClient) GetSharedData(collabID string, key string) (interface{}, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if session, ok := c.collaborations[collabID]; ok {
		session.mu.RLock()
		defer session.mu.RUnlock()
		value, exists := session.SharedData[key]
		return value, exists
	}
	return nil, false
}

// Stop stops the client and cleans up resources
func (c *A2AClient) Stop(ctx context.Context) error {
	c.logger.Info("stopping A2A client")

	// Announce departure
	msg := &Message{
		Type:      MessageTypeAgentDeparture,
		From:      AgentIdentity{ID: c.agentID, Name: c.agentName},
		To:        AgentIdentity{ID: "broadcast"},
		Timestamp: time.Now(),
	}

	_ = c.protocol.handler.SendMessage(ctx, msg, "")

	// Clean up reply channels
	c.mu.Lock()
	for _, ch := range c.replyChans {
		close(ch)
	}
	c.replyChans = make(map[string]chan *Message)
	c.collaborations = make(map[string]*CollaborationSession)
	c.mu.Unlock()

	return nil
}

// Helper functions

func getCapabilityNames(caps []Capability) []string {
	names := make([]string, len(caps))
	for i, cap := range caps {
		names[i] = cap.Name
	}
	return names
}
