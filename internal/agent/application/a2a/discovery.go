// Package a2a provides agent-to-agent communication and orchestration.
package a2a

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// PeerInfo represents information about a peer agent
type PeerInfo struct {
	Identity          AgentIdentity
	Capabilities      []Capability
	LastSeen          time.Time
	HeartbeatInterval time.Duration
	mu                sync.RWMutex
}

// DiscoveryEvent represents a discovery event
type DiscoveryEvent struct {
	Type      string // "joined", "left", "updated"
	Peer      *PeerInfo
	Timestamp time.Time
}

// DiscoveryService handles agent discovery and capability matching
type DiscoveryService struct {
	peers        map[string]*PeerInfo
	capabilities map[string]map[string]*PeerInfo // capability name -> peers
	eventChans   []chan DiscoveryEvent
	mu           sync.RWMutex
	logger       *zap.Logger
}

// NewDiscoveryService creates a new discovery service
func NewDiscoveryService(logger *zap.Logger) *DiscoveryService {
	return &DiscoveryService{
		peers:        make(map[string]*PeerInfo),
		capabilities: make(map[string]map[string]*PeerInfo),
		eventChans:   make([]chan DiscoveryEvent, 0),
		logger:       logger.Named("discovery"),
	}
}

// RegisterPeer registers a peer agent
func (d *DiscoveryService) RegisterPeer(ctx context.Context, identity AgentIdentity, capabilities []Capability) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	peer := &PeerInfo{
		Identity:          identity,
		Capabilities:      capabilities,
		LastSeen:          time.Now(),
		HeartbeatInterval: 30 * time.Second,
	}

	d.peers[identity.ID] = peer

	// Update capability index
	for _, cap := range capabilities {
		if d.capabilities[cap.Name] == nil {
			d.capabilities[cap.Name] = make(map[string]*PeerInfo)
		}
		d.capabilities[cap.Name][identity.ID] = peer
	}

	event := DiscoveryEvent{
		Type:      "joined",
		Peer:      peer,
		Timestamp: time.Now(),
	}
	d.notifyEvent(event)

	d.logger.Info("peer registered",
		zap.String("peer_id", identity.ID),
		zap.String("peer_name", identity.Name),
		zap.Int("capabilities", len(capabilities)))

	return nil
}

// UnregisterPeer removes a peer agent
func (d *DiscoveryService) UnregisterPeer(ctx context.Context, agentID string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	peer, exists := d.peers[agentID]
	if !exists {
		return nil
	}

	// Remove from capability index
	for _, cap := range peer.Capabilities {
		if caps, ok := d.capabilities[cap.Name]; ok {
			delete(caps, agentID)
			if len(caps) == 0 {
				delete(d.capabilities, cap.Name)
			}
		}
	}

	delete(d.peers, agentID)

	event := DiscoveryEvent{
		Type:      "left",
		Peer:      peer,
		Timestamp: time.Now(),
	}
	d.notifyEvent(event)

	d.logger.Info("peer unregistered",
		zap.String("peer_id", agentID))

	return nil
}

// GetPeer retrieves peer information
func (d *DiscoveryService) GetPeer(agentID string) *PeerInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.peers[agentID]
}

// GetAllPeers returns all registered peers
func (d *DiscoveryService) GetAllPeers() []*PeerInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	peers := make([]*PeerInfo, 0, len(d.peers))
	for _, peer := range d.peers {
		peers = append(peers, peer)
	}
	return peers
}

// GetPeersWithCapabilities returns peers that have all specified capabilities
func (d *DiscoveryService) GetPeersWithCapabilities(requiredCaps []string) []*PeerInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if len(requiredCaps) == 0 {
		return d.GetAllPeers()
	}

	// Build candidate set from first capability
	candidates := make(map[string]*PeerInfo)
	if caps, ok := d.capabilities[requiredCaps[0]]; ok {
		for id, peer := range caps {
			candidates[id] = peer
		}
	}

	// Filter by remaining capabilities
	for _, capName := range requiredCaps[1:] {
		caps, ok := d.capabilities[capName]
		if !ok {
			// No peers have this capability
			return []*PeerInfo{}
		}

		// Intersect with candidates
		for id := range candidates {
			if _, ok := caps[id]; !ok {
				delete(candidates, id)
			}
		}
	}

	result := make([]*PeerInfo, 0, len(candidates))
	for _, peer := range candidates {
		result = append(result, peer)
	}
	return result
}

// UpdateHeartbeat updates the last seen time for a peer
func (d *DiscoveryService) UpdateHeartbeat(agentID string) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if peer, ok := d.peers[agentID]; ok {
		peer.mu.Lock()
		peer.LastSeen = time.Now()
		peer.mu.Unlock()
	}
}

// Subscribe subscribes to discovery events
func (d *DiscoveryService) Subscribe(ctx context.Context) chan DiscoveryEvent {
	d.mu.Lock()
	defer d.mu.Unlock()

	eventChan := make(chan DiscoveryEvent, 100)
	d.eventChans = append(d.eventChans, eventChan)

	go func() {
		<-ctx.Done()
		d.unsubscribe(eventChan)
	}()

	return eventChan
}

// notifyEvent notifies all subscribers of a discovery event
func (d *DiscoveryService) notifyEvent(event DiscoveryEvent) {
	for _, eventChan := range d.eventChans {
		select {
		case eventChan <- event:
		default:
			d.logger.Warn("event channel full, dropping event",
				zap.String("event_type", event.Type))
		}
	}
}

// unsubscribe removes an event channel
func (d *DiscoveryService) unsubscribe(eventChan chan DiscoveryEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()

	for i, ch := range d.eventChans {
		if ch == eventChan {
			d.eventChans = append(d.eventChans[:i], d.eventChans[i+1:]...)
			close(eventChan)
			break
		}
	}
}

// FindBestPeer finds the best peer for a task based on capabilities and load
func (d *DiscoveryService) FindBestPeer(ctx context.Context, requiredCapabilities []string, preferLowLoad bool) (*PeerInfo, error) {
	candidates := d.GetPeersWithCapabilities(requiredCapabilities)

	if len(candidates) == 0 {
		return nil, ErrNoPeersFound
	}

	// For now, return the first candidate
	// In a production system, you'd consider factors like:
	// - Current load
	// - Response time
	// - Availability
	// - Priority
	return candidates[0], nil
}

// Cleanup removes inactive peers
func (d *DiscoveryService) Cleanup(timeout time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := time.Now()
	for id, peer := range d.peers {
		peer.mu.RLock()
		inactive := now.Sub(peer.LastSeen) > timeout
		peer.mu.RUnlock()

		if inactive {
			d.logger.Info("removing inactive peer",
				zap.String("peer_id", id))

			// Remove from capability index
			for _, cap := range peer.Capabilities {
				if caps, ok := d.capabilities[cap.Name]; ok {
					delete(caps, id)
					if len(caps) == 0 {
						delete(d.capabilities, cap.Name)
					}
				}
			}

			delete(d.peers, id)

			event := DiscoveryEvent{
				Type:      "left",
				Peer:      peer,
				Timestamp: time.Now(),
			}
			d.notifyEvent(event)
		}
	}
}

// StartCleanupLoop starts the cleanup loop
func (d *DiscoveryService) StartCleanupLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.Cleanup(interval * 3)
		}
	}
}

var ErrNoPeersFound = &A2AError{
	Type:    ErrorTypeDiscovery,
	Message: "no peers found with required capabilities",
}
