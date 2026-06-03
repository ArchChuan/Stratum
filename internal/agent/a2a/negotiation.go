// Package a2a provides agent-to-agent communication and orchestration.
package a2a

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// NegotiationState represents the state of a negotiation
type NegotiationState string

const (
	NegotiationStatePending     NegotiationState = "pending"
	NegotiationStateNegotiating NegotiationState = "negotiating"
	NegotiationStateAccepted    NegotiationState = "accepted"
	NegotiationStateRejected    NegotiationState = "rejected"
	NegotiationStateCancelled   NegotiationState = "cancelled"
)

// TaskOffer represents a task offer
type TaskOffer struct {
	ID           string
	From         AgentIdentity
	To           AgentIdentity
	Description  string
	Requirements []string
	Priority     Priority
	Timeout      time.Duration
	EstDuration  time.Duration
	ProposedAt   time.Time
	ExpiresAt    time.Time
	mu           sync.RWMutex
}

// TaskResponse represents a response to a task offer
type TaskResponse struct {
	OfferID     string
	From        AgentIdentity
	Accepted    bool
	Reason      string
	ETA         time.Duration
	RespondedAt time.Time
}

// Negotiation represents an active negotiation
type Negotiation struct {
	ID        string
	Offer     *TaskOffer
	Response  *TaskResponse
	State     NegotiationState
	CreatedAt time.Time
	UpdatedAt time.Time
	mu        sync.RWMutex
}

// NegotiationService handles task negotiation between agents
type NegotiationService struct {
	negotiations map[string]*Negotiation
	mu           sync.RWMutex
	logger       *zap.Logger
	stats        NegotiationStats
}

// NegotiationStats tracks negotiation statistics
type NegotiationStats struct {
	TotalProposed   int64
	TotalAccepted   int64
	TotalRejected   int64
	AverageDuration time.Duration
	mu              sync.RWMutex
}

// NewNegotiationService creates a new negotiation service
func NewNegotiationService(logger *zap.Logger) *NegotiationService {
	return &NegotiationService{
		negotiations: make(map[string]*Negotiation),
		logger:       logger.Named("negotiation"),
	}
}

// ProposeTask proposes a task to another agent
func (n *NegotiationService) ProposeTask(ctx context.Context, offer *TaskOffer) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	neg := &Negotiation{
		ID:        generateMessageID(),
		Offer:     offer,
		State:     NegotiationStatePending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	n.negotiations[neg.ID] = neg

	n.stats.mu.Lock()
	n.stats.TotalProposed++
	n.stats.mu.Unlock()

	n.logger.Info("task proposed",
		zap.String("negotiation_id", neg.ID),
		zap.String("to", offer.To.ID),
		zap.String("description", offer.Description))

	// Set up expiration timer
	time.AfterFunc(offer.Timeout, func() {
		n.mu.Lock()
		defer n.mu.Unlock()

		if current, exists := n.negotiations[neg.ID]; exists && current.State == NegotiationStatePending {
			current.mu.Lock()
			current.State = NegotiationStateCancelled
			current.mu.Unlock()
			delete(n.negotiations, neg.ID)
		}
	})

	return neg.ID, nil
}

// RespondToOffer responds to a task offer
func (n *NegotiationService) RespondToOffer(ctx context.Context, negotiationID string, response *TaskResponse) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	neg, exists := n.negotiations[negotiationID]
	if !exists {
		return ErrNegotiationNotFound
	}

	neg.mu.Lock()
	defer neg.mu.Unlock()

	if neg.State != NegotiationStatePending {
		return ErrInvalidNegotiationState
	}

	neg.Response = response
	neg.UpdatedAt = time.Now()

	if response.Accepted {
		neg.State = NegotiationStateAccepted

		n.stats.mu.Lock()
		n.stats.TotalAccepted++
		duration := time.Since(neg.CreatedAt)
		// Update average (exponential moving average)
		if n.stats.AverageDuration == 0 {
			n.stats.AverageDuration = duration
		} else {
			n.stats.AverageDuration = (n.stats.AverageDuration*3 + duration) / 4
		}
		n.stats.mu.Unlock()

		n.logger.Info("task offer accepted",
			zap.String("negotiation_id", negotiationID),
			zap.String("by", response.From.ID),
			zap.Duration("duration", duration))
	} else {
		neg.State = NegotiationStateRejected

		n.stats.mu.Lock()
		n.stats.TotalRejected++
		n.stats.mu.Unlock()

		n.logger.Info("task offer rejected",
			zap.String("negotiation_id", negotiationID),
			zap.String("by", response.From.ID),
			zap.String("reason", response.Reason))
	}

	return nil
}

// GetNegotiation retrieves a negotiation
func (n *NegotiationService) GetNegotiation(negotiationID string) (*Negotiation, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	neg, exists := n.negotiations[negotiationID]
	if !exists {
		return nil, ErrNegotiationNotFound
	}

	return neg, nil
}

// GetPendingNegotiations returns all pending negotiations
func (n *NegotiationService) GetPendingNegotiations() []*Negotiation {
	n.mu.RLock()
	defer n.mu.RUnlock()

	var pending []*Negotiation
	for _, neg := range n.negotiations {
		neg.mu.RLock()
		if neg.State == NegotiationStatePending {
			pending = append(pending, neg)
		}
		neg.mu.RUnlock()
	}

	return pending
}

// CancelNegotiation cancels a negotiation
func (n *NegotiationService) CancelNegotiation(ctx context.Context, negotiationID string) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	neg, exists := n.negotiations[negotiationID]
	if !exists {
		return ErrNegotiationNotFound
	}

	neg.mu.Lock()
	neg.State = NegotiationStateCancelled
	neg.UpdatedAt = time.Now()
	neg.mu.Unlock()

	n.logger.Info("negotiation cancelled",
		zap.String("negotiation_id", negotiationID))

	return nil
}

// Cleanup removes old negotiations
func (n *NegotiationService) Cleanup(maxAge time.Duration) {
	n.mu.Lock()
	defer n.mu.Unlock()

	now := time.Now()
	for id, neg := range n.negotiations {
		neg.mu.RLock()
		old := now.Sub(neg.CreatedAt) > maxAge
		final := neg.State == NegotiationStateAccepted ||
			neg.State == NegotiationStateRejected ||
			neg.State == NegotiationStateCancelled
		neg.mu.RUnlock()

		if old && final {
			delete(n.negotiations, id)
		}
	}
}

// GetStats returns negotiation statistics
func (n *NegotiationService) GetStats() *NegotiationStats {
	n.stats.mu.RLock()
	defer n.stats.mu.RUnlock()
	return &n.stats
}

var (
	ErrNegotiationNotFound     = &A2AError{Type: ErrorTypeNegotiation, Message: "negotiation not found"}
	ErrInvalidNegotiationState = &A2AError{Type: ErrorTypeNegotiation, Message: "invalid negotiation state"}
)
