package e2e

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	pgstorage "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

func TestActiveSnapshotRepositoryE2E(t *testing.T) {
	env := SetupMemoryTestEnv(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)

	snapshot := &domain.ActiveSnapshot{
		TenantID: env.TenantID, UserID: env.UserID, AgentID: env.AgentID,
		WorkContext: []string{"first task"}, PersonalContext: []string{"prefers concise answers"},
		TopOfMind: []string{"release"}, Source: domain.SnapshotSource{Type: "message", Reference: "msg-1"},
		ExpiresAt: now.Add(time.Hour), UpdatedAt: now, Status: domain.SnapshotStatusActive,
	}
	require.NoError(t, env.SnapshotRepo.Upsert(ctx, snapshot))

	got, err := env.SnapshotRepo.Get(ctx, env.TenantID, env.UserID, env.AgentID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, int64(1), got.Version)
	require.Equal(t, snapshot.WorkContext, got.WorkContext)
	require.Equal(t, snapshot.PersonalContext, got.PersonalContext)
	require.Equal(t, snapshot.TopOfMind, got.TopOfMind)
	require.Equal(t, snapshot.Source, got.Source)
	require.Equal(t, snapshot.Status, got.Status)

	snapshot.WorkContext = []string{"replacement task"}
	snapshot.Source.Reference = "msg-2"
	snapshot.UpdatedAt = now.Add(time.Minute)
	snapshot.ExpiresAt = now.Add(2 * time.Hour)
	require.NoError(t, env.SnapshotRepo.Upsert(ctx, snapshot))

	got, err = env.SnapshotRepo.Get(ctx, env.TenantID, env.UserID, env.AgentID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, int64(2), got.Version)
	require.Equal(t, []string{"replacement task"}, got.WorkContext)
	require.Equal(t, "msg-2", got.Source.Reference)

	stale := *snapshot
	stale.WorkContext = []string{"stale task"}
	stale.Source.Reference = "msg-stale"
	stale.UpdatedAt = now.Add(-time.Minute)
	stale.ExpiresAt = stale.UpdatedAt.Add(time.Hour)
	require.NoError(t, env.SnapshotRepo.Upsert(ctx, &stale))
	got, err = env.SnapshotRepo.Get(ctx, env.TenantID, env.UserID, env.AgentID)
	require.NoError(t, err)
	require.Equal(t, int64(2), got.Version)
	require.Equal(t, []string{"replacement task"}, got.WorkContext)
	require.Equal(t, "msg-2", got.Source.Reference)

	otherScope, err := env.SnapshotRepo.Get(ctx, env.TenantID, env.UserID, "other-agent")
	require.NoError(t, err)
	require.Nil(t, otherScope)
	secondTenantSnapshot := *snapshot
	secondTenantSnapshot.TenantID = env.SecondTenantID
	secondTenantSnapshot.WorkContext = []string{"second tenant task"}
	require.NoError(t, env.SnapshotRepo.Upsert(ctx, &secondTenantSnapshot))
	otherTenant, err := env.SnapshotRepo.Get(ctx, env.SecondTenantID, env.UserID, env.AgentID)
	require.NoError(t, err)
	require.NotNil(t, otherTenant)
	require.Equal(t, []string{"second tenant task"}, otherTenant.WorkContext)
	got, err = env.SnapshotRepo.Get(ctx, env.TenantID, env.UserID, env.AgentID)
	require.NoError(t, err)
	require.Equal(t, []string{"replacement task"}, got.WorkContext)

	expired := *snapshot
	expired.AgentID = "expired-agent"
	expired.UpdatedAt = now.Add(-2 * time.Hour)
	expired.ExpiresAt = now.Add(-time.Hour)
	require.NoError(t, env.SnapshotRepo.Upsert(ctx, &expired))
	gotExpired, err := env.SnapshotRepo.Get(ctx, env.TenantID, env.UserID, expired.AgentID)
	require.NoError(t, err)
	require.Nil(t, gotExpired)
}

func TestHistoryRepositoryE2E(t *testing.T) {
	env := SetupMemoryTestEnv(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	segment := domain.HistorySegment{
		TenantID: env.TenantID, UserID: env.UserID, AgentID: env.AgentID, ConversationID: uuid.NewString(),
		Tier: domain.HistoryTierRecent, Scope: domain.ScopeAgent, Summary: "quarterly launch planning",
		SourceStart: "msg-1", SourceEnd: "msg-4", PeriodStart: now.Add(-100 * 24 * time.Hour),
		PeriodEnd: now.Add(-99 * 24 * time.Hour), Importance: 0.8, Confidence: 0.9, Status: domain.HistoryStatusActive,
	}
	segment.SourceIDs = []string{uuid.NewString(), uuid.NewString()}
	segment.AggregationKey = domain.HistoryAggregationKey(segment.UserID, segment.AgentID, string(segment.Scope), segment.Tier, segment.PeriodStart, segment.PeriodEnd, segment.SourceStart, segment.SourceEnd)
	require.NoError(t, seedHistoryConversation(ctx, env, env.TenantID, segment.ConversationID))

	require.NoError(t, env.HistoryRepo.Upsert(ctx, env.TenantID, &segment))
	require.NoError(t, env.HistoryRepo.Upsert(ctx, env.TenantID, &segment))

	var count int
	var tier, status string
	var periodStart, periodEnd time.Time
	require.NoError(t, withTenantTx(ctx, env, env.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COUNT(*), MIN(tier), MIN(status), MIN(period_start), MIN(period_end)
			FROM memory_summaries WHERE aggregation_key = $1`, segment.AggregationKey).
			Scan(&count, &tier, &status, &periodStart, &periodEnd)
	}))
	require.Equal(t, 1, count)
	require.Equal(t, domain.HistoryTierRecent, tier)
	require.Equal(t, domain.HistoryStatusActive, status)
	require.WithinDuration(t, segment.PeriodStart, periodStart, time.Microsecond)
	require.WithinDuration(t, segment.PeriodEnd, periodEnd, time.Microsecond)

	require.NoError(t, env.HistoryRepo.Maintain(ctx, env.TenantID))
	active, err := env.HistoryRepo.SearchRelevant(ctx, env.TenantID, env.UserID, env.AgentID, "quarterly launch planning", 10)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, domain.HistoryTierEarlier, active[0].Tier)
	require.Equal(t, segment.Summary, active[0].Summary)

	otherScope, err := env.HistoryRepo.SearchRelevant(ctx, env.TenantID, env.UserID, "other-agent", "quarterly launch planning", 10)
	require.NoError(t, err)
	require.Empty(t, otherScope)
	secondTenantSegment := segment
	secondTenantSegment.TenantID = env.SecondTenantID
	secondTenantSegment.Summary = "second tenant private planning"
	require.NoError(t, seedHistoryConversation(ctx, env, env.SecondTenantID, secondTenantSegment.ConversationID))
	require.NoError(t, env.HistoryRepo.Upsert(ctx, env.SecondTenantID, &secondTenantSegment))
	otherTenant, err := env.HistoryRepo.SearchRelevant(ctx, env.SecondTenantID, env.UserID, env.AgentID, "quarterly launch planning", 10)
	require.NoError(t, err)
	require.Len(t, otherTenant, 1)
	require.Equal(t, secondTenantSegment.Summary, otherTenant[0].Summary)
	active, err = env.HistoryRepo.SearchRelevant(ctx, env.TenantID, env.UserID, env.AgentID, "quarterly launch planning", 10)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, segment.Summary, active[0].Summary)
}

func TestHistoryRepositoryNextBatchTracksExactSourceIDsAtWatermark(t *testing.T) {
	env := SetupMemoryTestEnv(t)
	ctx := context.Background()
	conversationID := uuid.NewString()
	watermark := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)

	require.NoError(t, seedHistoryConversation(ctx, env, env.TenantID, conversationID))

	initialIDs := deterministicUUIDs("history-watermark-initial", 60)
	require.NoError(t, seedEnrichedHistoryEntries(ctx, env, conversationID, initialIDs, watermark))

	first, err := env.HistoryRepo.NextBatch(ctx, env.TenantID, 10, 50)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Len(t, first.Entries, 50)
	requireHistorySourceIDs(t, initialIDs[:50], first.Entries)
	require.NoError(t, env.HistoryRepo.Upsert(ctx, env.TenantID, historySegmentForBatch(first, "first tied batch")))

	second, err := env.HistoryRepo.NextBatch(ctx, env.TenantID, 10, 50)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.Len(t, second.Entries, 10)
	requireHistorySourceIDs(t, initialIDs[50:], second.Entries)
	require.NoError(t, env.HistoryRepo.Upsert(ctx, env.TenantID, historySegmentForBatch(second, "second tied batch")))

	lateIDs := deterministicUUIDs("history-watermark-late", 10)
	require.NoError(t, seedEnrichedHistoryEntries(ctx, env, conversationID, lateIDs, watermark.Add(-time.Hour)))

	late, err := env.HistoryRepo.NextBatch(ctx, env.TenantID, 10, 50)
	require.NoError(t, err)
	require.NotNil(t, late)
	require.Len(t, late.Entries, 10)
	requireHistorySourceIDs(t, lateIDs, late.Entries)
}

func deterministicUUIDs(namespace string, count int) []string {
	ids := make([]string, count)
	for i := range ids {
		ids[i] = uuid.NewSHA1(uuid.NameSpaceOID, []byte(fmt.Sprintf("%s-%03d", namespace, i))).String()
	}
	sort.Strings(ids)
	return ids
}

func seedEnrichedHistoryEntries(ctx context.Context, env *MemoryTestEnv, conversationID string, ids []string, createdAt time.Time) error {
	return withTenantTx(ctx, env, env.TenantID, func(ctx context.Context, tx pgx.Tx) error {
		for i, id := range ids {
			if _, err := tx.Exec(ctx, `INSERT INTO memory_entries
				(id, user_id, agent_id, scope, role, content, type, enriched_at, conversation_id, created_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
				id, env.UserID, env.AgentID, string(domain.ScopeAgent), "user", fmt.Sprintf("history entry %d", i),
				"long_term", createdAt, conversationID, createdAt); err != nil {
				return err
			}
		}
		return nil
	})
}

func historySegmentForBatch(batch *domain.HistoryBatch, summary string) *domain.HistorySegment {
	first := batch.Entries[0]
	last := batch.Entries[len(batch.Entries)-1]
	sourceIDs := make([]string, len(batch.Entries))
	for i, entry := range batch.Entries {
		sourceIDs[i] = entry.ID
	}
	segment := &domain.HistorySegment{
		ConversationID: batch.ConversationID,
		UserID:         batch.UserID,
		AgentID:        batch.AgentID,
		Scope:          batch.Scope,
		Tier:           domain.HistoryTierRecent,
		Summary:        summary,
		SourceStart:    first.ID,
		SourceEnd:      last.ID,
		SourceIDs:      sourceIDs,
		PeriodStart:    first.CreatedAt,
		PeriodEnd:      last.CreatedAt,
		Importance:     0.5,
		Confidence:     0.5,
		Status:         domain.HistoryStatusActive,
	}
	segment.AggregationKey = domain.HistoryAggregationKey(segment.UserID, segment.AgentID, string(segment.Scope), segment.Tier, segment.PeriodStart, segment.PeriodEnd, segment.SourceStart, segment.SourceEnd)
	return segment
}

func requireHistorySourceIDs(t *testing.T, expected []string, entries []domain.HistorySource) {
	t.Helper()
	actual := make([]string, len(entries))
	for i, entry := range entries {
		actual[i] = entry.ID
	}
	sort.Strings(actual)
	want := append([]string(nil), expected...)
	sort.Strings(want)
	require.Equal(t, want, actual)
}

func withTenantTx(ctx context.Context, env *MemoryTestEnv, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	if tenantID == "" {
		return fmt.Errorf("tenant ID is required")
	}
	return pgstorage.Wrap(env.PGPool).ExecTenant(ctx, tenantID, fn)
}

func seedHistoryConversation(ctx context.Context, env *MemoryTestEnv, tenantID, conversationID string) error {
	return withTenantTx(ctx, env, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO agents (id, name) VALUES ($1, $2) ON CONFLICT (id) DO NOTHING`, env.AgentID, "E2E memory agent"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO chat_conversations (id, agent_id, user_id, name) VALUES ($1, $2, $3, $4) ON CONFLICT (id) DO NOTHING`, conversationID, env.AgentID, env.UserID, "E2E memory conversation")
		return err
	})
}
