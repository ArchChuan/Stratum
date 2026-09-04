//go:build integration

package persistence

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestPgSuiteRepositoryDraftLifecycle covers the Phase 3c draft methods:
// CreateDraftRevision inherits kind from the published revision, generated
// cases round-trip their provenance and judge spec through the wrapped
// evaluator_config, editing keeps provenance, and deletion removes the row.
func TestPgSuiteRepositoryDraftLifecycle(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	tenantID := "eval_draft_lifecycle"
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, tenantID)); err != nil {
			t.Logf("cleanup tenant %s: %v", tenantID, err)
		}
		pool.Close()
	})

	repo := NewPgSuiteRepository(pool)
	suite := domain.EvalSuite{ID: "suite-draft", Name: "生成基线", DraftRevisionID: "rev-0"}
	base := domain.EvalSuiteRevision{
		ID: "rev-0", SuiteID: suite.ID, Status: domain.SuiteRevisionDraft,
		ResourceKind: domain.ResourceKindSkill,
		Cases: []domain.EvalCase{
			{ID: "hand-case", Name: "手工", Input: "问", ExpectedOutput: "答", AssertionMode: domain.AssertionExact, Enabled: true},
		},
	}
	if err := repo.CreateSuite(ctx, tenantID, suite, base); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PublishRevision(ctx, tenantID, suite.ID, base.ID, 1); err != nil {
		t.Fatal(err)
	}

	// A fresh draft after publishing inherits resource_kind and parent.
	draft, err := repo.CreateDraftRevision(ctx, tenantID, suite.ID)
	if err != nil {
		t.Fatalf("CreateDraftRevision: %v", err)
	}
	if draft.ResourceKind != domain.ResourceKindSkill || draft.ParentID != "rev-0" || draft.Status != domain.SuiteRevisionDraft {
		t.Fatalf("draft not inherited from active revision: %+v", draft)
	}

	// Generated case with provenance + judge spec.
	generated := domain.EvalCase{
		ID: "gen-case-1", Name: "物流查询", Input: "快递没更新", ExpectedOutput: "物流查询",
		AssertionMode: domain.AssertionContains, Enabled: true,
		SourceTraceID: "trace-9", FeedbackRef: "fb-9", GenerateReason: "负反馈样本",
		JudgeSpec: &domain.JudgeSpec{Model: "qwen-max", Rubric: "判断是否解决问题"},
	}
	if err := repo.AddDraftCases(ctx, tenantID, draft.ID, []domain.EvalCase{generated}); err != nil {
		t.Fatalf("AddDraftCases: %v", err)
	}
	loaded, ok, err := repo.GetDraftRevision(ctx, tenantID, suite.ID)
	if err != nil || !ok {
		t.Fatalf("GetDraftRevision: ok=%v err=%v", ok, err)
	}
	if len(loaded.Cases) != 1 {
		t.Fatalf("expected 1 generated case, got %d: %+v", len(loaded.Cases), loaded.Cases)
	}
	got := loaded.Cases[0]
	if got.SourceTraceID != "trace-9" || got.FeedbackRef != "fb-9" || got.GenerateReason != "负反馈样本" {
		t.Fatalf("provenance lost on round-trip: %+v", got)
	}
	if got.JudgeSpec == nil || got.JudgeSpec.Model != "qwen-max" || got.JudgeSpec.Rubric != "判断是否解决问题" {
		t.Fatalf("judge spec lost on round-trip: %+v", got)
	}

	// Editing the case keeps provenance and judge spec.
	edited := got
	edited.Name = "物流查询改"
	edited.Input = "物流进度没更新"
	edited.ExpectedOutput = "物流查询结果"
	edited.AssertionMode = domain.AssertionExact
	if err := repo.UpdateDraftCase(ctx, tenantID, draft.ID, edited); err != nil {
		t.Fatalf("UpdateDraftCase: %v", err)
	}
	loaded, _, err = repo.GetDraftRevision(ctx, tenantID, suite.ID)
	if err != nil {
		t.Fatal(err)
	}
	got = loaded.Cases[0]
	if got.Name != "物流查询改" || got.AssertionMode != domain.AssertionExact || got.Input != "物流进度没更新" {
		t.Fatalf("edit not applied: %+v", got)
	}
	if got.SourceTraceID != "trace-9" || got.JudgeSpec == nil {
		t.Fatalf("edit wiped provenance or judge spec: %+v", got)
	}

	// Deletion removes the case from the draft.
	if err := repo.DeleteDraftCase(ctx, tenantID, draft.ID, got.ID); err != nil {
		t.Fatalf("DeleteDraftCase: %v", err)
	}
	loaded, _, err = repo.GetDraftRevision(ctx, tenantID, suite.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Cases) != 0 {
		t.Fatalf("expected empty draft after delete, got %d", len(loaded.Cases))
	}

	// Update of a case in another revision must fail (no cross-revision writes).
	if err := repo.UpdateDraftCase(ctx, tenantID, "rev-0", got); err == nil {
		t.Fatal("expected error updating a case outside the draft revision")
	}
}

// TestPgSuiteRepositoryReadsBareJudgeSpecCompat verifies the pre-3c layout
// (evaluator_config holding a bare JudgeSpec object) still loads.
func TestPgSuiteRepositoryReadsBareJudgeSpecCompat(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	tenantID := "eval_bare_spec"
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, tenantID)); err != nil {
			t.Logf("cleanup tenant %s: %v", tenantID, err)
		}
		pool.Close()
	})

	repo := NewPgSuiteRepository(pool)
	suite := domain.EvalSuite{ID: "suite-bare", Name: "兼容", DraftRevisionID: "rev-bare"}
	revision := domain.EvalSuiteRevision{
		ID: "rev-bare", SuiteID: suite.ID, Status: domain.SuiteRevisionDraft,
		ResourceKind: domain.ResourceKindSkill,
		Cases: []domain.EvalCase{
			{ID: "case-bare", Name: "法官", Input: "问题", AssertionMode: domain.AssertionJudge, Enabled: true},
		},
	}
	if err := repo.CreateSuite(ctx, tenantID, suite, revision); err != nil {
		t.Fatal(err)
	}
	// Simulate the 3b layout: evaluator_config stores the bare JudgeSpec.
	if _, err := pool.Exec(ctx, fmt.Sprintf(
		`UPDATE "tenant_%s".eval_cases SET evaluator_config='{"model":"qwen-max","rubric":"旧 rubric"}'::jsonb WHERE id='case-bare'`,
		tenantID)); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := repo.GetDraftRevision(ctx, tenantID, suite.ID)
	if err != nil || !ok {
		t.Fatalf("GetDraftRevision: ok=%v err=%v", ok, err)
	}
	got := loaded.Cases[0]
	if got.JudgeSpec == nil || got.JudgeSpec.Model != "qwen-max" || got.JudgeSpec.Rubric != "旧 rubric" {
		t.Fatalf("bare judge spec not read back: %+v", got)
	}
	if got.SourceTraceID != "" {
		t.Fatalf("bare layout must not fabricate provenance: %+v", got)
	}
}

// TestPgSuiteSessionRoundTrip 覆盖阶段 B 会话剧本持久化 round-trip：会话 case 的
// Session（Goal / Turns / per-turn ToolSpec）写后读回逐字段一致；旧单轮 case
// Session 保持 nil；UpdateDraftCase 编辑会话 full replacement、nil 写回回退单轮。
func TestPgSuiteSessionRoundTrip(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	tenantID := "eval_session_round_trip"
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, tenantID)); err != nil {
			t.Logf("cleanup tenant %s: %v", tenantID, err)
		}
		pool.Close()
	})

	repo := NewPgSuiteRepository(pool)
	suite := domain.EvalSuite{ID: "suite-sess", Name: "会话剧本", DraftRevisionID: "rev-sess"}
	sessionScript := &domain.EvalSessionScript{
		Goal: "处理退换货",
		Turns: []domain.SessionTurn{
			{User: "我的订单 3 天没发货", ToolSpec: &domain.ToolSpec{MustNotCall: []string{"delete"}}},
			{User: "好的帮我退款", Probe: "应调用退款工具", ToolSpec: &domain.ToolSpec{MustCall: []string{"refund"}}},
		},
	}
	revision := domain.EvalSuiteRevision{
		ID: "rev-sess", SuiteID: suite.ID, Status: domain.SuiteRevisionDraft,
		ResourceKind: domain.ResourceKindSkill,
		Cases: []domain.EvalCase{
			{ID: "case-sess", Name: "多轮", Input: nil, ExpectedOutput: "已退款",
				AssertionMode: domain.AssertionContains, Enabled: true, Session: sessionScript},
			{ID: "case-legacy", Name: "单轮", Input: "问", ExpectedOutput: "答",
				AssertionMode: domain.AssertionContains, Enabled: true},
		},
	}
	if err := repo.CreateSuite(ctx, tenantID, suite, revision); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := repo.GetDraftRevision(ctx, tenantID, suite.ID)
	if err != nil || !ok {
		t.Fatalf("GetDraftRevision: ok=%v err=%v", ok, err)
	}
	if len(loaded.Cases) != 2 {
		t.Fatalf("expected 2 cases, got %d: %+v", len(loaded.Cases), loaded.Cases)
	}
	var sessCase, legacyCase *domain.EvalCase
	for i := range loaded.Cases {
		switch loaded.Cases[i].ID {
		case "case-sess":
			sessCase = &loaded.Cases[i]
		case "case-legacy":
			legacyCase = &loaded.Cases[i]
		}
	}
	if sessCase == nil || legacyCase == nil {
		t.Fatalf("cases missing on round-trip: %+v", loaded.Cases)
	}
	if !sessCase.IsSession() {
		t.Fatalf("session case lost its script on round-trip")
	}
	if sessCase.Session.Goal != "处理退换货" || len(sessCase.Session.Turns) != 2 {
		t.Fatalf("session shape mismatch: %+v", sessCase.Session)
	}
	turn := sessCase.Session.Turns[1]
	if turn.User != "好的帮我退款" || turn.Probe != "应调用退款工具" ||
		turn.ToolSpec == nil || len(turn.ToolSpec.MustCall) != 1 || turn.ToolSpec.MustCall[0] != "refund" {
		t.Fatalf("per-turn spec mismatch: %+v", turn)
	}
	if legacyCase.IsSession() {
		t.Fatalf("legacy case must round-trip with nil session")
	}

	// Editing the session case replaces its script (full replacement).
	edited := *sessCase
	edited.Session = &domain.EvalSessionScript{Goal: "改后目标", Turns: []domain.SessionTurn{{User: "单轮开场"}}}
	if err := repo.UpdateDraftCase(ctx, tenantID, revision.ID, edited); err != nil {
		t.Fatalf("UpdateDraftCase session: %v", err)
	}
	loaded, _, err = repo.GetDraftRevision(ctx, tenantID, suite.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := findCase(t, loaded.Cases, "case-sess")
	if got.Session == nil || got.Session.Goal != "改后目标" || len(got.Session.Turns) != 1 {
		t.Fatalf("session edit not applied: %+v", got.Session)
	}

	// nil session write-back reverts the case to single-turn semantics.
	edited = got
	edited.Session = nil
	if err := repo.UpdateDraftCase(ctx, tenantID, revision.ID, edited); err != nil {
		t.Fatalf("UpdateDraftCase clear session: %v", err)
	}
	loaded, _, err = repo.GetDraftRevision(ctx, tenantID, suite.ID)
	if err != nil {
		t.Fatal(err)
	}
	got = findCase(t, loaded.Cases, "case-sess")
	if got.IsSession() {
		t.Fatalf("nil session write-back must clear the script: %+v", got.Session)
	}
}

func findCase(t *testing.T, cases []domain.EvalCase, id string) domain.EvalCase {
	t.Helper()
	for _, c := range cases {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("case %s not found", id)
	return domain.EvalCase{}
}
