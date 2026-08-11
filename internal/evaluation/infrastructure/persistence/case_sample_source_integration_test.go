//go:build integration

package persistence

import (
	"fmt"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

// TestPgCaseSampleSourceJoinsFeedbackWithConversation verifies the sampling
// join evaluation_feedback(trace_id) → chat_messages(user query, assistant
// answer) with negative feedback first, and that feedback without a
// trace-linked conversation never surfaces.
func TestPgCaseSampleSourceJoinsFeedbackWithConversation(t *testing.T) {
	pool, ctx, tenantID := feedbackRepositoryTestPool(t, "sample_source")
	repo := NewPgCaseSampleSource(pool)

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("fixture insert failed: %v", err)
		}
	}
	agentID := "agent-1"
	exec(fmt.Sprintf(`INSERT INTO "tenant_%s".agents (id, name) VALUES ($1, $1)`, tenantID), agentID)
	exec(fmt.Sprintf(`INSERT INTO "tenant_%s".chat_conversations (id, agent_id, user_id, name) VALUES ('00000000-0000-0000-0000-000000000001', $1, 'user-1', '会话')`, tenantID), agentID)
	// trace-A: negative feedback, trace-B: positive. Both have a user
	// question and one assistant answer after it (the schema's final role
	// CHECK is ('user','assistant'), re-applied by a later ALTER).
	for _, m := range []struct{ trace, role, content string }{
		{trace: "trace-A", role: "user", content: "快递没更新"},
		{trace: "trace-A", role: "assistant", content: "为您查询物流进度"},
		{trace: "trace-B", role: "user", content: "如何退款"},
		{trace: "trace-B", role: "assistant", content: "退款流程说明如下"},
	} {
		exec(fmt.Sprintf(
			`INSERT INTO "tenant_%s".chat_messages (conversation_id, role, content, trace_id) VALUES ('00000000-0000-0000-0000-000000000001', $1, $2, $3)`,
			tenantID), m.role, m.content, m.trace)
	}

	fbRepo := NewPgFeedbackRepository(pool)
	for _, fb := range []domain.FeedbackRequest{
		{TraceID: "trace-A", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
			RevisionID: "revision-1", Score: 0.2, Outcome: map[string]any{"label": "bad"}, IdempotencyKey: "fb-a"},
		{TraceID: "trace-B", ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
			RevisionID: "revision-1", Score: 0.9, Outcome: map[string]any{"label": "good"}, IdempotencyKey: "fb-b"},
	} {
		if _, err := fbRepo.Record(ctx, tenantID, fb); err != nil {
			t.Fatalf("record feedback: %v", err)
		}
	}

	got, err := repo.ListSamples(ctx, tenantID, domain.ResourceKindSkill, domain.SamplePolicyNegativeFirst, 10)
	if err != nil {
		t.Fatalf("ListSamples: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 samples, got %d: %+v", len(got), got)
	}
	if got[0].TraceID != "trace-A" || got[0].Query != "快递没更新" || got[0].Response != "为您查询物流进度" {
		t.Fatalf("negative feedback must come first with query/response paired: %+v", got[0])
	}
	if got[1].TraceID != "trace-B" || got[1].FeedbackRef == "" || got[1].Score == nil || *got[1].Score != 0.9 {
		t.Fatalf("second sample must carry positive feedback signal: %+v", got[1])
	}

	// Kind filter: agent kind has no feedback rows.
	none, err := repo.ListSamples(ctx, tenantID, domain.ResourceKindAgent, domain.SamplePolicyNegativeFirst, 10)
	if err != nil {
		t.Fatalf("ListSamples(agent): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no agent samples, got %d", len(none))
	}
}

// TestPgCaseSampleSourceBalancedAlternates verifies the balanced policy
// alternates negative and non-negative samples from the SQL ordering.
func TestPgCaseSampleSourceBalancedAlternates(t *testing.T) {
	pool, ctx, tenantID := feedbackRepositoryTestPool(t, "sample_balanced")
	repo := NewPgCaseSampleSource(pool)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("fixture insert failed: %v", err)
		}
	}
	exec(fmt.Sprintf(`INSERT INTO "tenant_%s".agents (id, name) VALUES ('agent-2', 'agent-2')`, tenantID))
	exec(fmt.Sprintf(`INSERT INTO "tenant_%s".chat_conversations (id, agent_id, user_id, name) VALUES ('00000000-0000-0000-0000-000000000002', 'agent-2', 'user-2', '会话')`, tenantID))
	for i := 0; i < 4; i++ {
		trace := fmt.Sprintf("bal-%d", i)
		exec(fmt.Sprintf(
			`INSERT INTO "tenant_%s".chat_messages (conversation_id, role, content, trace_id) VALUES ('00000000-0000-0000-0000-000000000002', 'user', $1, $2)`,
			tenantID), "查询"+trace, trace)
		exec(fmt.Sprintf(
			`INSERT INTO "tenant_%s".chat_messages (conversation_id, role, content, trace_id) VALUES ('00000000-0000-0000-0000-000000000002', 'assistant', $1, $2)`,
			tenantID), "回答"+trace, trace)
	}
	fbRepo := NewPgFeedbackRepository(pool)
	for i := 0; i < 4; i++ {
		score := 0.9
		if i%2 == 0 {
			score = 0.1
		}
		if _, err := fbRepo.Record(ctx, tenantID, domain.FeedbackRequest{
			TraceID: fmt.Sprintf("bal-%d", i), ResourceKind: domain.ResourceKindSkill, ResourceID: "skill-1",
			RevisionID: "revision-1", Score: float64(score), IdempotencyKey: fmt.Sprintf("bal-fb-%d", i),
		}); err != nil {
			t.Fatalf("record feedback: %v", err)
		}
	}
	got, err := repo.ListSamples(ctx, tenantID, domain.ResourceKindSkill, domain.SamplePolicyBalanced, 10)
	if err != nil {
		t.Fatalf("ListSamples: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 samples, got %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		prevNeg := got[i-1].Score != nil && *got[i-1].Score < 0.5
		curNeg := got[i].Score != nil && *got[i].Score < 0.5
		if prevNeg == curNeg {
			t.Fatalf("balanced output not alternating at %d: %+v %+v", i, got[i-1], got[i])
		}
	}
}
