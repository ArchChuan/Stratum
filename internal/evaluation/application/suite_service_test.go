package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

func TestSuiteServiceCreatesDraftAndPublishesImmutableRevision(t *testing.T) {
	repo := &fakeSuiteRepo{}
	svc := NewSuiteService(repo)

	suite, revision, err := svc.Create(context.Background(), "tenant-1", CreateSuiteInput{
		Name: "投诉分类基线", ResourceKind: domain.ResourceKindSkill,
		Cases: []domain.EvalCase{{Name: "物流", Input: "快递没更新", ExpectedOutput: "物流", AssertionMode: domain.AssertionContains, Enabled: true}},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if revision.Status != domain.SuiteRevisionDraft || suite.DraftRevisionID != revision.ID || revision.Cases[0].ID == "" {
		t.Fatalf("unexpected draft: suite=%+v revision=%+v", suite, revision)
	}

	published, err := svc.Publish(context.Background(), "tenant-1", suite.ID)
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	if published.Status != domain.SuiteRevisionPublished || published.VersionNo != 1 {
		t.Fatalf("unexpected published revision: %+v", published)
	}
}

// TestSuiteServiceCreateCarriesJudgeSpecIntoRevision verifies the judge
// authoring path: a judge case's JudgeSpec set at create time survives the
// service unchanged into the persisted revision (the repository's
// insertEvalCase then writes it into evaluator_config via ToConfig).
func TestSuiteServiceCreateCarriesJudgeSpecIntoRevision(t *testing.T) {
	repo := &fakeSuiteRepo{}
	svc := NewSuiteService(repo)

	suite, revision, err := svc.Create(context.Background(), "tenant-1", CreateSuiteInput{
		Name: "judge 基线", ResourceKind: domain.ResourceKindAgent,
		Cases: []domain.EvalCase{{
			Name: "j1", Input: "帮我总结", ExpectedOutput: "要点",
			AssertionMode: domain.AssertionJudge, Enabled: true,
			JudgeSpec: &domain.JudgeSpec{Model: "judge-v1", Rubric: "总结要点覆盖度"},
		}},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if revision.Cases[0].AssertionMode != domain.AssertionJudge {
		t.Fatalf("assertion mode=%q, want judge", revision.Cases[0].AssertionMode)
	}
	if got := revision.Cases[0].JudgeSpec; got == nil || got.Model != "judge-v1" || got.Rubric != "总结要点覆盖度" {
		t.Fatalf("judge spec lost through Create: %+v", got)
	}
	// ToConfig must be non-nil so insertEvalCase persists the spec.
	if cfg := revision.Cases[0].ToConfig(); cfg == nil || cfg.JudgeSpec == nil || cfg.JudgeSpec.Model != "judge-v1" {
		t.Fatalf("ToConfig does not carry judge spec: %+v", cfg)
	}
	if suite.ID == "" || revision.Cases[0].ID == "" {
		t.Fatalf("expected generated IDs, suite=%+v revision=%+v", suite, revision)
	}
}

type fakeSuiteRepo struct {
	suite    domain.EvalSuite
	revision domain.EvalSuiteRevision
}

func (f *fakeSuiteRepo) CreateSuite(_ context.Context, _ string, suite domain.EvalSuite, revision domain.EvalSuiteRevision) error {
	f.suite, f.revision = suite, revision
	return nil
}

func (f *fakeSuiteRepo) GetDraftRevision(_ context.Context, _ string, suiteID string) (domain.EvalSuiteRevision, bool, error) {
	return f.revision, f.revision.SuiteID == suiteID && f.revision.Status == domain.SuiteRevisionDraft, nil
}

func (f *fakeSuiteRepo) PublishRevision(_ context.Context, _ string, suiteID, revisionID string, versionNo int) (domain.EvalSuiteRevision, error) {
	f.revision.Status = domain.SuiteRevisionPublished
	f.revision.VersionNo = versionNo
	f.suite.ActiveRevisionID = revisionID
	f.suite.DraftRevisionID = ""
	return f.revision, nil
}

func (f *fakeSuiteRepo) NextVersionNo(_ context.Context, _ string, _ string) (int, error) {
	return 1, nil
}

func (f *fakeSuiteRepo) GetRevision(_ context.Context, _ string, revisionID string) (domain.EvalSuiteRevision, bool, error) {
	return f.revision, f.revision.ID == revisionID, nil
}

func (f *fakeSuiteRepo) GetActiveRevision(_ context.Context, _ string, suiteID string) (domain.EvalSuiteRevision, bool, error) {
	return f.revision,
		f.suite.ID == suiteID && f.suite.ActiveRevisionID == f.revision.ID && f.revision.Status == domain.SuiteRevisionPublished,
		nil
}

func (f *fakeSuiteRepo) GetSuite(_ context.Context, _ string, suiteID string) (domain.EvalSuite, bool, error) {
	if f.suite.ID != suiteID {
		return domain.EvalSuite{}, false, nil
	}
	return f.suite, true, nil
}

func (f *fakeSuiteRepo) ListSuiteRevisions(_ context.Context, _ string, _ string) ([]domain.SuiteRevisionMeta, error) {
	meta := domain.SuiteRevisionMeta{
		ID: f.revision.ID, VersionNo: f.revision.VersionNo, Status: f.revision.Status,
		ResourceKind: f.revision.ResourceKind, CreatedBy: f.revision.CreatedBy,
	}
	for _, c := range f.revision.Cases {
		if c.Enabled {
			meta.EnabledCaseCount++
		}
	}
	if meta.ID == "" {
		return nil, nil
	}
	return []domain.SuiteRevisionMeta{meta}, nil
}

// The four draft-management methods exist so the fake satisfies
// port.SuiteRepository; UpdateDraftCase mutates the fake's revision to
// exercise UpdateDraftCase on the service.
func (f *fakeSuiteRepo) CreateDraftRevision(_ context.Context, _ string, _ string) (domain.EvalSuiteRevision, error) {
	return domain.EvalSuiteRevision{}, nil
}

func (f *fakeSuiteRepo) AddDraftCases(_ context.Context, _ string, _ string, _ []domain.EvalCase) error {
	return nil
}

func (f *fakeSuiteRepo) UpdateDraftCase(_ context.Context, _ string, _ string, testCase domain.EvalCase) error {
	for i := range f.revision.Cases {
		if f.revision.Cases[i].ID == testCase.ID {
			f.revision.Cases[i] = testCase
			return nil
		}
	}
	// Mirrors the real repository: an UPDATE that matches no row is an error.
	return errors.New("draft case not found")
}

func (f *fakeSuiteRepo) DeleteDraftCase(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

func TestSuiteServiceGetDraftAndUpdateDraftCase(t *testing.T) {
	repo := &fakeSuiteRepo{}
	svc := NewSuiteService(repo)
	suite, _, err := svc.Create(context.Background(), "tenant-1", CreateSuiteInput{
		Name: "投诉分类", ResourceKind: domain.ResourceKindSkill,
		Cases: []domain.EvalCase{
			{Name: "物流", Input: "快递没更新", ExpectedOutput: "物流", AssertionMode: domain.AssertionContains, Enabled: true},
			{Name: "退款", Input: "要退款", ExpectedOutput: "退款", AssertionMode: domain.AssertionContains, Enabled: true},
		},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	ctx := context.Background()

	draft, err := svc.GetDraft(ctx, "tenant-1", suite.ID)
	if err != nil {
		t.Fatalf("GetDraft returned error: %v", err)
	}
	if len(draft.Cases) != 2 || draft.Status != domain.SuiteRevisionDraft {
		t.Fatalf("unexpected draft: %+v", draft)
	}

	// Edit: full field replacement.
	edited, err := svc.UpdateDraftCase(ctx, "tenant-1", suite.ID, draft.Cases[0].ID, domain.EvalCase{
		ID: draft.Cases[0].ID, Name: "物流改", Input: "物流进度查询", ExpectedOutput: "物流查询",
		AssertionMode: domain.AssertionExact, Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpdateDraftCase edit returned error: %v", err)
	}
	if edited.Name != "物流改" || edited.AssertionMode != domain.AssertionExact || edited.Input != "物流进度查询" {
		t.Fatalf("edited case not persisted: %+v", edited)
	}

	// Reject: enabled=false keeps the case in the draft for later approval.
	rejected, err := svc.UpdateDraftCase(ctx, "tenant-1", suite.ID, draft.Cases[0].ID, domain.EvalCase{
		ID: draft.Cases[0].ID, Name: "物流改", Input: "物流进度查询", ExpectedOutput: "物流查询",
		AssertionMode: domain.AssertionExact, Enabled: false,
	})
	if err != nil {
		t.Fatalf("UpdateDraftCase reject returned error: %v", err)
	}
	if rejected.Enabled {
		t.Fatalf("expected rejected case to be disabled: %+v", rejected)
	}

	// Unknown case: error propagates from the repository path.
	if _, err := svc.UpdateDraftCase(ctx, "tenant-1", suite.ID, "missing", domain.EvalCase{
		ID: "missing", Name: "x", Input: "x", ExpectedOutput: "x", AssertionMode: domain.AssertionExact, Enabled: true,
	}); err == nil {
		t.Fatal("expected error for unknown draft case")
	}
}

func TestSuiteServiceGetDraftNotFound(t *testing.T) {
	svc := NewSuiteService(&fakeSuiteRepo{})
	if _, err := svc.GetDraft(context.Background(), "tenant-1", "missing"); !errors.Is(err, ErrSuiteNotFound) {
		t.Fatalf("expected ErrSuiteNotFound, got %v", err)
	}
}

func TestSuiteServiceGetActiveRevision(t *testing.T) {
	repo := &fakeSuiteRepo{}
	svc := NewSuiteService(repo)

	t.Run("published suite returns active revision", func(t *testing.T) {
		suite, revision, err := svc.Create(context.Background(), "tenant-1", CreateSuiteInput{
			Name: "技能基准集", ResourceKind: domain.ResourceKindSkill,
			Cases: []domain.EvalCase{{Name: "抽取", Input: "x", AssertionMode: domain.AssertionJudge, Enabled: true}},
		})
		if err != nil {
			t.Fatalf("Create returned error: %v", err)
		}
		if _, err := svc.Publish(context.Background(), "tenant-1", suite.ID); err != nil {
			t.Fatalf("Publish returned error: %v", err)
		}
		active, err := svc.GetActiveRevision(context.Background(), "tenant-1", suite.ID)
		if err != nil {
			t.Fatalf("GetActiveRevision returned error: %v", err)
		}
		if active.ID != revision.ID || active.Status != domain.SuiteRevisionPublished {
			t.Fatalf("expected published active revision %s, got %+v", revision.ID, active)
		}
	})

	t.Run("unpublished suite returns ErrSuiteNotFound", func(t *testing.T) {
		svc := NewSuiteService(&fakeSuiteRepo{})
		if _, err := svc.GetActiveRevision(context.Background(), "tenant-1", "missing"); !errors.Is(err, ErrSuiteNotFound) {
			t.Fatalf("expected ErrSuiteNotFound, got %v", err)
		}
	})
}

// sessionScriptFixture 构造一个合法的双轮会话剧本 case（阶段 B §5.4 authoring
// round-trip 用）：首轮纯 user 消息、末轮带工具过程断言。
func sessionScriptFixture() domain.EvalCase {
	return domain.EvalCase{
		Name: "会话投诉", ExpectedOutput: "已给用户可执行处理", AssertionMode: domain.AssertionContains,
		Enabled: true,
		Session: &domain.EvalSessionScript{
			Goal: "用户投诉快递未收到：定位物流状态并给出签收异常处理",
			Turns: []domain.SessionTurn{
				{User: "快递一直没到，帮我看看", Probe: "识别物流查询意图"},
				{User: "物流显示已签收但我没收到", Probe: "进入签收异常处理",
					ToolSpec: &domain.ToolSpec{MustCall: []string{"track_package"}, MaxCalls: 2}},
			},
		},
	}
}

func TestSuiteServiceCreateCarriesSessionScriptIntoRevision(t *testing.T) {
	repo := &fakeSuiteRepo{}
	svc := NewSuiteService(repo)

	suite, revision, err := svc.Create(context.Background(), "tenant-1", CreateSuiteInput{
		Name: "会话基线", ResourceKind: domain.ResourceKindAgent,
		Cases: []domain.EvalCase{sessionScriptFixture()},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if suite.ID == "" || revision.Cases[0].ID == "" {
		t.Fatalf("expected generated IDs, suite=%+v revision=%+v", suite, revision)
	}
	got := revision.Cases[0].Session
	if got == nil {
		t.Fatal("session script lost through Create")
	}
	if got.Goal != "用户投诉快递未收到：定位物流状态并给出签收异常处理" || len(got.Turns) != 2 {
		t.Fatalf("session script not preserved verbatim: %+v", got)
	}
	if got.Turns[1].ToolSpec == nil || len(got.Turns[1].ToolSpec.MustCall) != 1 ||
		got.Turns[1].ToolSpec.MustCall[0] != "track_package" || got.Turns[1].ToolSpec.MaxCalls != 2 {
		t.Fatalf("per-turn tool spec not preserved: %+v", got.Turns[1])
	}
	// 会话 case 的 input 无执行语义，Create 应放行（服务层不要求单轮输入）。
	if !revision.Cases[0].IsSession() {
		t.Fatal("expected case to report IsSession()=true")
	}
}

func TestSuiteServiceRejectsInvalidSessionScript(t *testing.T) {
	repo := &fakeSuiteRepo{}
	svc := NewSuiteService(repo)

	tests := []struct {
		name string
		wrap func() error
	}{
		{name: "zero turns",
			wrap: func() error {
				_, _, err := svc.Create(context.Background(), "tenant-1", CreateSuiteInput{
					Name: "x", ResourceKind: domain.ResourceKindAgent,
					Cases: []domain.EvalCase{{Name: "c", ExpectedOutput: "e", AssertionMode: domain.AssertionContains,
						Enabled: true, Session: &domain.EvalSessionScript{Goal: "g", Turns: nil}}},
				})
				return err
			}},
		{name: "empty turn user",
			wrap: func() error {
				_, _, err := svc.Create(context.Background(), "tenant-1", CreateSuiteInput{
					Name: "x", ResourceKind: domain.ResourceKindAgent,
					Cases: []domain.EvalCase{{Name: "c", ExpectedOutput: "e", AssertionMode: domain.AssertionContains,
						Enabled: true, Session: &domain.EvalSessionScript{Goal: "g",
							Turns: []domain.SessionTurn{{User: "  "}}}}},
				})
				return err
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.wrap(); !errors.Is(err, ErrSuiteCaseInvalidScript) {
				t.Fatalf("expected ErrSuiteCaseInvalidScript, got %v", err)
			}
		})
	}
}

func TestSuiteServiceRejectsSingleTurnCaseWithoutInput(t *testing.T) {
	svc := NewSuiteService(&fakeSuiteRepo{})

	// Create：单轮 case（session nil）缺 input → 400 哨兵。
	if _, _, err := svc.Create(context.Background(), "tenant-1", CreateSuiteInput{
		Name: "x", ResourceKind: domain.ResourceKindSkill,
		Cases: []domain.EvalCase{{Name: "c", ExpectedOutput: "e", AssertionMode: domain.AssertionExact, Enabled: true}},
	}); !errors.Is(err, ErrSuiteCaseInputRequired) {
		t.Fatalf("expected ErrSuiteCaseInputRequired on Create, got %v", err)
	}

	// UpdateDraftCase 同规则：编辑不能把单轮 case 的 input 清空。
	repo := &fakeSuiteRepo{}
	createSvc := NewSuiteService(repo)
	_, revision, err := createSvc.Create(context.Background(), "tenant-1", CreateSuiteInput{
		Name: "x", ResourceKind: domain.ResourceKindSkill,
		Cases: []domain.EvalCase{{Name: "c", Input: "q", ExpectedOutput: "e", AssertionMode: domain.AssertionExact, Enabled: true}},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	svc = NewSuiteService(repo)
	if _, err := svc.UpdateDraftCase(context.Background(), "tenant-1", revision.SuiteID, revision.Cases[0].ID, domain.EvalCase{
		ID: revision.Cases[0].ID, Name: "c", ExpectedOutput: "e",
		AssertionMode: domain.AssertionExact, Enabled: true,
	}); !errors.Is(err, ErrSuiteCaseInputRequired) {
		t.Fatalf("expected ErrSuiteCaseInputRequired on UpdateDraftCase, got %v", err)
	}
}

func TestSuiteServiceUpdateDraftCaseSessionRoundTrip(t *testing.T) {
	repo := &fakeSuiteRepo{}
	svc := NewSuiteService(repo)
	_, revision, err := svc.Create(context.Background(), "tenant-1", CreateSuiteInput{
		Name: "会话基线", ResourceKind: domain.ResourceKindAgent,
		Cases: []domain.EvalCase{sessionScriptFixture()},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	caseID := revision.Cases[0].ID

	// 编辑会话剧本：goal 与轮次内容可随 draft case full replacement 更新。
	edited, err := svc.UpdateDraftCase(context.Background(), "tenant-1", revision.SuiteID, caseID, domain.EvalCase{
		ID: caseID, Name: "会话投诉改", ExpectedOutput: "已给用户可执行处理",
		AssertionMode: domain.AssertionContains, Enabled: true,
		Session: &domain.EvalSessionScript{
			Goal: "快递签收异常：先核实再给处理",
			Turns: []domain.SessionTurn{
				{User: "签收异常怎么处理", Probe: "进入异常处理"},
			},
		},
	})
	if err != nil {
		t.Fatalf("UpdateDraftCase returned error: %v", err)
	}
	if edited.Session == nil || edited.Session.Goal != "快递签收异常：先核实再给处理" || len(edited.Session.Turns) != 1 {
		t.Fatalf("session edit not persisted: %+v", edited.Session)
	}

	// 清空 session（会话 case 转回单轮）：nil Session 被接受，必须补 input。
	reverted, err := svc.UpdateDraftCase(context.Background(), "tenant-1", revision.SuiteID, caseID, domain.EvalCase{
		ID: caseID, Name: "会话投诉改", Input: "签收异常怎么处理", ExpectedOutput: "已给用户可执行处理",
		AssertionMode: domain.AssertionContains, Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpdateDraftCase reverted to single-turn returned error: %v", err)
	}
	if reverted.IsSession() {
		t.Fatalf("expected session cleared, got %+v", reverted.Session)
	}
}
