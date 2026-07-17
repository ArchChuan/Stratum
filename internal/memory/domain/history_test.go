package domain

import (
	"testing"
	"time"
)

func TestHistoryAggregationKeyIsDeterministicAndScoped(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	a := HistoryAggregationKey("user-1", "agent-1", "user", HistoryTierRecent, start, end, "m1", "m9")
	b := HistoryAggregationKey("user-1", "agent-1", "user", HistoryTierRecent, start, end, "m1", "m9")
	if a == "" || a != b {
		t.Fatalf("aggregation key must be stable: %q != %q", a, b)
	}
	if a == HistoryAggregationKey("user-2", "agent-1", "user", HistoryTierRecent, start, end, "m1", "m9") {
		t.Fatal("aggregation key must include scope ownership")
	}
}

func TestHistoryCompressionKeyIsDeterministicAndIncludesExactSourceIDs(t *testing.T) {
	a := HistoryCompressionKey("user-1", "agent-1", "user", HistoryTierEarlier, []string{"s1", "s2"})
	b := HistoryCompressionKey("user-1", "agent-1", "user", HistoryTierEarlier, []string{"s1", "s2"})
	if a == "" || a != b {
		t.Fatalf("compression key must be stable: %q != %q", a, b)
	}
	if a == HistoryCompressionKey("user-1", "agent-1", "user", HistoryTierEarlier, []string{"s1", "s3"}) {
		t.Fatal("compression key must include every exact source ID")
	}
}

func TestHistoryTierPromotion(t *testing.T) {
	if got := NextHistoryTier(HistoryTierRecent); got != HistoryTierEarlier {
		t.Fatalf("recent promotion = %q", got)
	}
	if got := NextHistoryTier(HistoryTierEarlier); got != HistoryTierBackground {
		t.Fatalf("earlier promotion = %q", got)
	}
	if got := NextHistoryTier(HistoryTierBackground); got != "" {
		t.Fatalf("background must be terminal, got %q", got)
	}
}

func TestHistorySegmentValidation(t *testing.T) {
	now := time.Now().UTC()
	valid := HistorySegment{ConversationID: "c", UserID: "u", AgentID: "a", Scope: ScopeUser, Tier: HistoryTierRecent, PeriodStart: now.Add(-time.Hour), PeriodEnd: now, Summary: "summary", SourceStart: "m1", SourceEnd: "m2", AggregationKey: "key", Importance: .7, Confidence: .8, Status: HistoryStatusActive}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid segment rejected: %v", err)
	}
	valid.PeriodEnd = valid.PeriodStart.Add(-time.Second)
	if err := valid.Validate(); err == nil {
		t.Fatal("reversed period must be rejected")
	}
	valid.PeriodEnd = now
	valid.AggregationKey = ""
	if err := valid.Validate(); err == nil {
		t.Fatal("missing idempotency key must be rejected")
	}
	valid.AggregationKey = "key"
	valid.SourceIDs = []string{"m1", ""}
	if err := valid.Validate(); err == nil {
		t.Fatal("blank source ID must be rejected")
	}
}
