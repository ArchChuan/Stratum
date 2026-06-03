// Package a2a provides agent-to-agent communication and orchestration.
package a2a

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ProtocolConfig defines configuration for the A2A protocol
type ProtocolConfig struct {
	HeartbeatInterval   time.Duration
	TaskTimeout         time.Duration
	MaxRetries          int
	RetryBackoff        time.Duration
	PeerCleanupInterval time.Duration
	EnableTracing       bool
}

// DefaultProtocolConfig returns default protocol configuration
func DefaultProtocolConfig() *ProtocolConfig {
	return &ProtocolConfig{
		HeartbeatInterval:   30 * time.Second,
		TaskTimeout:         5 * time.Minute,
		MaxRetries:          3,
		RetryBackoff:        1 * time.Second,
		PeerCleanupInterval: 5 * time.Minute,
		EnableTracing:       true,
	}
}

// ProtocolMetrics tracks protocol metrics
type ProtocolMetrics struct {
	mu                sync.RWMutex
	MessagesSent      int64
	MessagesReceived  int64
	MessagesFailed    int64
	NegotiationsTotal int64
	Collaborations    int64
	BytesTransferred  int64
}

// A2AProtocol is the main A2A protocol implementation
type A2AProtocol struct {
	config       *ProtocolConfig
	discovery    *DiscoveryService
	negotiation  *NegotiationService
	orchestrator *Orchestrator
	handler      *ProtocolHandler
	metrics      *ProtocolMetrics
	clients      map[string]*A2AClient
	logger       *zap.Logger
	ctx          context.Context
	cancel       context.CancelFunc
	running      bool
	mu           sync.RWMutex
}

// NewA2AProtocol creates a new A2A protocol instance
func NewA2AProtocol(config *ProtocolConfig, logger *zap.Logger) *A2AProtocol {
	if config == nil {
		config = DefaultProtocolConfig()
	}

	return &A2AProtocol{
		config:       config,
		discovery:    NewDiscoveryService(logger),
		negotiation:  NewNegotiationService(logger),
		orchestrator: NewOrchestrator(logger),
		handler:      NewProtocolHandler(logger),
		metrics:      &ProtocolMetrics{},
		clients:      make(map[string]*A2AClient),
		logger:       logger.Named("a2a-protocol"),
		running:      false,
	}
}

// Start starts the A2A protocol
func (p *A2AProtocol) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return nil
	}

	p.ctx, p.cancel = context.WithCancel(ctx)

	// Start handler
	if err := p.handler.Start(p.ctx); err != nil {
		return err
	}

	// Start background tasks
	go p.runHeartbeatLoop(p.ctx)
	go p.runCleanupLoop(p.ctx)

	p.running = true
	p.logger.Info("A2A protocol started")

	return nil
}

// Stop stops the A2A protocol
func (p *A2AProtocol) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.running {
		return nil
	}

	p.cancel()
	p.handler.Stop()

	p.mu.Lock()
	p.running = false
	p.mu.Unlock()

	p.logger.Info("A2A protocol stopped")
	return nil
}

// CreateClient creates a new A2A client for an agent
func (p *A2AProtocol) CreateClient(agentID, agentName string) *A2AClient {
	client := NewA2AClient(p, agentID, agentName, p.logger)

	p.mu.Lock()
	p.clients[agentID] = client
	p.mu.Unlock()

	return client
}

// GetClient retrieves a client by agent ID
func (p *A2AProtocol) GetClient(agentID string) (*A2AClient, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	client, exists := p.clients[agentID]
	return client, exists
}

// runHeartbeatLoop runs the heartbeat loop
func (p *A2AProtocol) runHeartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(p.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.sendHeartbeats()
		}
	}
}

// sendHeartbeats sends heartbeats to all connected agents
func (p *A2AProtocol) sendHeartbeats() {
	peers := p.discovery.GetAllPeers()

	msg := NewMessage(MessageTypeHeartbeat, AgentIdentity{
		ID:   "system",
		Name: "a2a-protocol",
	})

	for _, peer := range peers {
		msg.WithRecipient(peer.Identity)
		p.handler.SendMessage(context.Background(), msg, "")
	}
}

// runCleanupLoop runs the cleanup loop
func (p *A2AProtocol) runCleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(p.config.PeerCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.discovery.Cleanup(p.config.PeerCleanupInterval * 3)
			p.negotiation.Cleanup(time.Hour)
			p.orchestrator.Cleanup(time.Hour)
		}
	}
}

// GetMetrics returns protocol metrics
func (p *A2AProtocol) GetMetrics() *ProtocolMetrics {
	p.metrics.mu.RLock()
	defer p.metrics.mu.RUnlock()
	return p.metrics
}

// IncrementMessageSent increments sent message counter
func (p *A2AProtocol) IncrementMessageSent() {
	p.metrics.mu.Lock()
	p.metrics.MessagesSent++
	p.metrics.mu.Unlock()
}

// IncrementMessageReceived increments received message counter
func (p *A2AProtocol) IncrementMessageReceived() {
	p.metrics.mu.Lock()
	p.metrics.MessagesReceived++
	p.metrics.mu.Unlock()
}

// IncrementMessageFailed increments failed message counter
func (p *A2AProtocol) IncrementMessageFailed() {
	p.metrics.mu.Lock()
	p.metrics.MessagesFailed++
	p.metrics.mu.Unlock()
}
