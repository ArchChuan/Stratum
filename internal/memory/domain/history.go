package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	HistoryTierRecent     = "recent_months"
	HistoryTierEarlier    = "earlier_context"
	HistoryTierBackground = "long_term_background"
	HistoryStatusActive   = "active"
	HistoryStatusArchived = "archived"
)

var ErrInvalidHistorySegment = errors.New("invalid history segment")

// HistorySegment is a bounded long-term summary for one tenant-local user/agent scope.
type HistorySegment struct {
	ID, TenantID, UserID, AgentID, ConversationID string
	Tier, Summary, SourceStart, SourceEnd         string
	Scope                                         Scope
	AggregationKey, Status                        string
	PeriodStart, PeriodEnd, CreatedAt, UpdatedAt  time.Time
	Importance, Confidence                        float64
	SourceIDs                                     []string
}

type HistorySource struct {
	ID, Content string
	CreatedAt   time.Time
}

type HistoryBatch struct {
	ConversationID, UserID, AgentID string
	Scope                           Scope
	Entries                         []HistorySource
}

// HistoryOverflowGroup contains one tenant-local identity/tier group selected for compression.
type HistoryOverflowGroup struct {
	ConversationID, UserID, AgentID, Tier string
	Scope                                 Scope
	Sources                               []HistorySegment
}

func (h HistorySegment) Validate() error {
	if h.ConversationID == "" || h.UserID == "" || h.Summary == "" || h.SourceStart == "" || h.SourceEnd == "" || h.AggregationKey == "" ||
		(h.Scope != ScopeUser && h.Scope != ScopeAgent) ||
		(h.Tier != HistoryTierRecent && h.Tier != HistoryTierEarlier && h.Tier != HistoryTierBackground) ||
		(h.Status != HistoryStatusActive && h.Status != HistoryStatusArchived) ||
		h.PeriodStart.IsZero() || h.PeriodEnd.Before(h.PeriodStart) ||
		math.IsNaN(h.Importance) || h.Importance < 0 || h.Importance > 1 ||
		math.IsNaN(h.Confidence) || h.Confidence < 0 || h.Confidence > 1 {
		return ErrInvalidHistorySegment
	}
	for _, id := range h.SourceIDs {
		if id == "" {
			return ErrInvalidHistorySegment
		}
	}
	return nil
}

func NextHistoryTier(tier string) string {
	switch tier {
	case HistoryTierRecent:
		return HistoryTierEarlier
	case HistoryTierEarlier:
		return HistoryTierBackground
	default:
		return ""
	}
}

// HistoryAggregationKey makes retries idempotent without retaining source payloads.
func HistoryAggregationKey(userID, agentID, scope, tier string, start, end time.Time, sourceStart, sourceEnd string) string {
	raw := fmt.Sprintf("%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s", userID, agentID, scope, tier,
		start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano), sourceStart, sourceEnd)
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// HistoryCompressionKey identifies the exact source segment set replaced by a compression.
func HistoryCompressionKey(userID, agentID, scope, tier string, sourceIDs []string) string {
	h := sha256.New()
	for _, value := range []string{userID, agentID, scope, tier} {
		_, _ = h.Write([]byte(value))
		_, _ = h.Write([]byte{0})
	}
	for _, id := range sourceIDs {
		_, _ = h.Write([]byte(id))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
