package persistence

import (
	"strings"
	"testing"
)

func TestHistoryQueriesEnforceThresholdIdempotencyAndTenantScope(t *testing.T) {
	if got := strings.Count(historyBatchQuery, "s.conversation_id=e.conversation_id"); got != 2 {
		t.Fatalf("both History watermarks must be conversation-scoped, got %d", got)
	}
	for _, want := range []string{"HAVING COUNT(*) >= $1", "ORDER BY created_at ASC", "LIMIT $2"} {
		if !strings.Contains(historyBatchQuery, want) {
			t.Fatalf("batch query missing %q", want)
		}
	}
	for _, want := range []string{"ON CONFLICT (aggregation_key)", "DO NOTHING"} {
		if !strings.Contains(historyUpsertQuery, want) {
			t.Fatalf("upsert query missing %q", want)
		}
	}
}

func TestHistoryLifecycleQueriesPromoteAndProtectFacts(t *testing.T) {
	for _, want := range []string{"tier = $1", "period_end < $2", "status = 'active'"} {
		if !strings.Contains(historyPromotionQuery, want) {
			t.Fatalf("promotion query missing %q", want)
		}
	}
	for _, want := range []string{"category <> 'preference'", "source <> 'explicit_user'", "importance < $2", "confidence < $3", "last_accessed_at < $1"} {
		if !strings.Contains(archiveColdFactsQuery, want) {
			t.Fatalf("archive query missing protection %q", want)
		}
	}
}

func TestHistoryOverflowQueryReturnsOneBoundedTenantLocalGroupWithoutTruncation(t *testing.T) {
	for _, want := range []string{
		"PARTITION BY user_id,agent_id,scope,tier",
		"rn > max_segments",
		"LIMIT $3",
		"id::text",
		"summary",
		"period_start",
		"period_end",
		"source_start",
		"source_end",
	} {
		if !strings.Contains(historyOverflowQuery, want) {
			t.Fatalf("overflow query missing %q", want)
		}
	}
	for _, forbidden := range []string{"LEFT(", "string_agg(summary"} {
		if strings.Contains(historyOverflowQuery, forbidden) {
			t.Fatalf("overflow query must not contain %q", forbidden)
		}
	}
}

func TestHistoryReplacementQueryArchivesExactSourcesOnlyAfterReplacementExists(t *testing.T) {
	for _, want := range []string{
		"ON CONFLICT (aggregation_key)",
		"WHERE aggregation_key IS NOT NULL",
		"status='archived'",
		"id::text = ANY($16::text[])",
		"SELECT aggregation_key FROM inserted",
		"SELECT aggregation_key FROM memory_summaries WHERE aggregation_key=$12 AND status='active'",
		"EXISTS (SELECT 1 FROM present)",
	} {
		if !strings.Contains(historyReplacementQuery, want) {
			t.Fatalf("replacement query missing %q", want)
		}
	}
}

func TestHistoryRelevanceQueryIsScopedBoundedAndRanked(t *testing.T) {
	for _, want := range []string{"user_id = $1", "scope = 'user'", "agent_id = $2", "similarity(summary, $3) DESC", "importance DESC", "confidence DESC", "LIMIT $4"} {
		if !strings.Contains(historyRelevanceQuery, want) {
			t.Fatalf("relevance query missing %q", want)
		}
	}
}
