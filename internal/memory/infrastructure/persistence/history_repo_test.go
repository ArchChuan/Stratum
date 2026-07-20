package persistence

import (
	"strings"
	"testing"
)

func TestHistoryQueriesEnforceThresholdIdempotencyAndTenantScope(t *testing.T) {
	for _, want := range []string{
		"HAVING COUNT(*) >= $1",
		"NOT EXISTS",
		"e.id = ANY(s.source_ids)",
		"s.conversation_id=e.conversation_id",
		"s.user_id=e.user_id",
		"s.agent_id=COALESCE(e.agent_id, '')",
		"s.scope=e.scope",
		"s.status='active'",
		"ORDER BY e.created_at ASC, e.id ASC",
		"LIMIT $2",
	} {
		if !strings.Contains(historyBatchQuery, want) {
			t.Fatalf("batch query missing %q", want)
		}
	}
	if strings.Contains(historyBatchQuery, "MAX(s.period_end)") || strings.Contains(historyBatchQuery, "e.created_at > COALESCE") {
		t.Fatal("batch query must not use a timestamp watermark")
	}
	for _, want := range []string{"source_ids", "ON CONFLICT (aggregation_key)", "DO NOTHING"} {
		if !strings.Contains(historyUpsertQuery, want) {
			t.Fatalf("upsert query missing %q", want)
		}
	}
}

func TestHistoryBatchQueryExcludesLegacyPeriodsAndExactModernSources(t *testing.T) {
	for _, want := range []string{
		"s.source_ids IS NULL",
		"e.created_at >= s.period_start",
		"e.created_at <= s.period_end",
		"s.source_ids IS NOT NULL",
		"e.id = ANY(s.source_ids)",
	} {
		if got := strings.Count(historyBatchQuery, want); got != 2 {
			t.Fatalf("batch query must contain %q in eligibility and selection branches, got %d", want, got)
		}
	}
}

func TestHistoryInsertQueriesCastSourceIDsToUUIDArray(t *testing.T) {
	for name, query := range map[string]string{"upsert": historyUpsertQuery, "replacement": historyReplacementQuery} {
		if !strings.Contains(query, "$16::text[]::uuid[]") {
			t.Fatalf("%s query must explicitly cast source IDs to uuid[]", name)
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
		"$1::bigint",
		"$2::bigint",
		"PARTITION BY conversation_id,user_id,agent_id,scope,tier",
		"GROUP BY conversation_id,user_id,agent_id,scope,tier",
		"SELECT conversation_id,user_id,agent_id,scope,tier FROM groups",
		"USING (conversation_id,user_id,agent_id,scope,tier)",
		"rn > max_segments",
		"LIMIT $3",
		"id::text",
		"summary",
		"period_start",
		"period_end",
		"source_start",
		"source_end",
		"source_ids",
	} {
		if !strings.Contains(historyOverflowQuery, want) {
			t.Fatalf("overflow query missing %q", want)
		}
	}
	if strings.Contains(historyOverflowQuery, "PARTITION BY user_id,agent_id,scope,tier") {
		t.Fatal("overflow ranking must not combine conversations")
	}
	for _, forbidden := range []string{"LEFT(", "string_agg(summary"} {
		if strings.Contains(historyOverflowQuery, forbidden) {
			t.Fatalf("overflow query must not contain %q", forbidden)
		}
	}
}

func TestHistoryOverflowQueryExcludesRowsWithoutExactSourceIDs(t *testing.T) {
	if !strings.Contains(historyOverflowQuery, "source_ids IS NOT NULL") {
		t.Fatal("overflow ranking must exclude legacy rows without exact source IDs")
	}
}

func TestHistoryReplacementQueryArchivesExactSourcesOnlyAfterReplacementExists(t *testing.T) {
	for _, want := range []string{
		"ON CONFLICT (aggregation_key)",
		"WHERE aggregation_key IS NOT NULL",
		"status='archived'",
		"id::text = ANY($17::text[])",
		"SELECT aggregation_key FROM inserted",
		"SELECT aggregation_key FROM memory_summaries WHERE aggregation_key=$12 AND status='active'",
		"EXISTS (SELECT 1 FROM present)",
		"source_ids",
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
