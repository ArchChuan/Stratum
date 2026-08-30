package application

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type fakeReviewRepo struct {
	inserted []domain.ReviewItem
	marked   map[string]domain.HumanVerdict
	err      error
}

func (f *fakeReviewRepo) UpsertItem(_ context.Context, _ string, item *domain.ReviewItem) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	f.inserted = append(f.inserted, *item)
	return true, nil
}
func (f *fakeReviewRepo) GetItem(_ context.Context, _, id string) (*domain.ReviewItem, error) {
	for i := range f.inserted {
		if f.inserted[i].ID == id {
			return &f.inserted[i], nil
		}
	}
	return nil, nil
}
func (f *fakeReviewRepo) ListItems(
	_ context.Context, _ string, _ port.ReviewFilter,
) ([]domain.ReviewItem, int64, error) {
	return f.inserted, int64(len(f.inserted)), nil
}
func (f *fakeReviewRepo) MarkReviewed(_ context.Context, _, id string, v domain.HumanVerdict, _, _ string) error {
	if f.err != nil {
		return f.err
	}
	if f.marked == nil {
		f.marked = map[string]domain.HumanVerdict{}
	}
	f.marked[id] = v
	return nil
}
func (f *fakeReviewRepo) CreateCalibrationSample(_ context.Context, _ string, _ *domain.CalibrationSample) error {
	if f.err != nil {
		return f.err
	}
	return nil
}
func (f *fakeReviewRepo) CreateAttributionEntry(_ context.Context, _ string, _ *domain.AttributionEntry) error {
	if f.err != nil {
		return f.err
	}
	return nil
}
func (f *fakeReviewRepo) CountPending(_ context.Context, _ string) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return int64(len(f.inserted)), nil
}

// raceReviewRepo 模拟并发评审竞态：Decide 首次 GetItem 读到 pending，MarkReviewed
// 返回 PgReviewRepository 的普通错误签名（非 sentinel，条目已非 pending），再次
// GetItem 返回已被另一评审者置为 reviewed 的最新条目。用于验证 Decide 对真实 Pg
// 错误签名的幂等降级。
type raceReviewRepo struct {
	*fakeReviewRepo
	getCalls int
}

func (r *raceReviewRepo) GetItem(ctx context.Context, tenantID, id string) (*domain.ReviewItem, error) {
	r.getCalls++
	item, err := r.fakeReviewRepo.GetItem(ctx, tenantID, id)
	if err != nil || item == nil {
		return item, err
	}
	if r.getCalls > 1 {
		reviewed := *item
		reviewed.Status = domain.ReviewStatusReviewed
		reviewed.HumanVerdict = domain.HumanVerdictPass
		return &reviewed, nil
	}
	return item, nil
}

func (r *raceReviewRepo) MarkReviewed(_ context.Context, _, id string, _ domain.HumanVerdict, _, _ string) error {
	// 模拟 PgReviewRepository.MarkReviewed：条目已非 pending 时返回普通错误。
	return fmt.Errorf("eval review item %s not pending (or missing)", id)
}

func newTestReviewService(repo port.ReviewRepository) *ReviewService {
	return newTestReviewServiceWithMetrics(repo, observability.NoopMetrics{})
}

func newTestReviewServiceWithMetrics(repo port.ReviewRepository, metrics observability.MetricsProvider) *ReviewService {
	return NewReviewService(ReviewServiceDeps{
		Repo:    repo,
		Metrics: metrics,
		Logger:  zap.NewNop(),
		Cfg:     domain.ReviewConfig{LowConfidenceThreshold: 0.6, JudgePassThreshold: 0.5},
	})
}

// stubReviewMetrics 记录 SetEvalReviewBacklog 调用序列（嵌入 NoopMetrics 满足
// MetricsProvider 全接口，只覆盖积压指标）。
type stubReviewMetrics struct {
	observability.NoopMetrics
	backlog []int64
}

func (m *stubReviewMetrics) SetEvalReviewBacklog(count int64) {
	m.backlog = append(m.backlog, count)
}

// countPendingErrRepo 让 CountPending 独立失败，用于验证积压刷新失败 fail-open。
type countPendingErrRepo struct {
	*fakeReviewRepo
}

func (r *countPendingErrRepo) CountPending(_ context.Context, _ string) (int64, error) {
	return 0, errors.New("count pending down")
}

func observationForTest() *domain.EvalObservation {
	return &domain.EvalObservation{
		ID: "obs-1", TraceID: "t-1",
		Resource: domain.ObservationResourceRef{Kind: domain.ResourceKindSkill, ResourceID: "s1"},
		Verdict:  domain.VerdictPass,
		Signals: domain.ObservationSignals{Judge: []domain.JudgeSignal{
			{Dimension: "faithfulness", Score: 1.0, Confidence: 0.9},
		}},
	}
}

func TestTryEscalateObservationFiresOnLowConfidence(t *testing.T) {
	repo := &fakeReviewRepo{}
	svc := newTestReviewService(repo)
	obs := observationForTest()
	obs.Signals.Judge[0].Confidence = 0.3
	if err := svc.TryEscalateObservation(context.Background(), "t1", obs); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if len(repo.inserted) != 1 {
		t.Fatalf("inserted = %d, want 1", len(repo.inserted))
	}
	got := repo.inserted[0]
	if got.TriggerReason != domain.TriggerLowConfidence || got.SourceType != domain.ReviewSourceObservation {
		t.Fatalf("unexpected item: %+v", got)
	}
	if got.ResourceKind != domain.ResourceKindSkill || got.ResourceID != "s1" {
		t.Fatalf("resource mismatch: %+v", got)
	}
}

func TestTryEscalateObservationNoTriggerNoInsert(t *testing.T) {
	repo := &fakeReviewRepo{}
	svc := newTestReviewService(repo)
	if err := svc.TryEscalateObservation(context.Background(), "t1", observationForTest()); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if len(repo.inserted) != 0 {
		t.Fatalf("inserted = %d, want 0", len(repo.inserted))
	}
}

func TestTryEscalateCaseResultFiresOnNeedsReview(t *testing.T) {
	repo := &fakeReviewRepo{}
	svc := newTestReviewService(repo)
	result := domain.EvalCaseResult{ID: "cr-1", CaseID: "c1", TraceID: "t-1", Passed: true}
	c := domain.EvalCase{ID: "c1", NeedsReview: true}
	assertion := domain.AssertionResult{Passed: true, Confidence: 0.9}
	if err := svc.TryEscalateCaseResult(context.Background(), "t1", "run-1", result, c, assertion); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if len(repo.inserted) != 1 || repo.inserted[0].TriggerReason != domain.TriggerNeedsReview {
		t.Fatalf("unexpected: %+v", repo.inserted)
	}
	if repo.inserted[0].RunID != "run-1" || repo.inserted[0].SourceID != "cr-1" {
		t.Fatalf("run/source mismatch: %+v", repo.inserted[0])
	}
}

func TestTryEscalatePropagatesRepoError(t *testing.T) {
	repo := &fakeReviewRepo{err: errors.New("db down")}
	svc := newTestReviewService(repo)
	obs := observationForTest()
	obs.Signals.Judge[0].Confidence = 0.3
	// fail-open：错误原样返回，主流程侧忽略（TryEscalate 不 panic 不吞错）。
	if err := svc.TryEscalateObservation(context.Background(), "t1", obs); err == nil {
		t.Fatal("want error propagated")
	}
}

func TestDecideStateMachine(t *testing.T) {
	repo := &fakeReviewRepo{}
	svc := newTestReviewService(repo)
	obs := observationForTest()
	obs.Signals.Judge[0].Confidence = 0.3
	if err := svc.TryEscalateObservation(context.Background(), "t1", obs); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	id := repo.inserted[0].ID
	item, err := svc.Decide(context.Background(), "t1", id, "reviewer@x", domain.HumanVerdictFail, "实际输出与上下文冲突")
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if item.Status != domain.ReviewStatusReviewed || item.HumanVerdict != domain.HumanVerdictFail {
		t.Fatalf("unexpected item: %+v", item)
	}
	if repo.marked[id] != domain.HumanVerdictFail {
		t.Fatalf("mark reviewed not recorded: %+v", repo.marked)
	}
}

func TestDecideRejectsInvalidVerdict(t *testing.T) {
	svc := newTestReviewService(&fakeReviewRepo{})
	_, err := svc.Decide(context.Background(), "t1", "ri-x", "reviewer@x", domain.HumanVerdict("bogus"), "")
	if err == nil {
		t.Fatal("want error for invalid verdict")
	}
}

// TestDecidePromoteSkippedWhenSuitesNil 覆盖 promote 分支：judge_misjudgment
// 时经 suites.AddDraftCases 沉淀（Suites/Evidence 为 nil 时跳过 promote，仅落
// calibration sample）。
func TestDecidePromoteSkippedWhenSuitesNil(t *testing.T) {
	repo := &fakeReviewRepo{}
	svc := newTestReviewService(repo)
	item := domain.ReviewItem{
		ID: uuid.NewString(), SourceType: domain.ReviewSourceObservation,
		SourceID: "obs-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "s1",
		TriggerReason: domain.TriggerLowConfidence, Status: domain.ReviewStatusPending,
		Snapshot: map[string]any{"signals": observationForTest().Signals},
	}
	repo.inserted = append(repo.inserted, item)
	_, err := svc.Decide(context.Background(), "t1", item.ID, "reviewer@x",
		domain.HumanVerdictJudgeMisjudgment, "judge 判错")
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if repo.marked[item.ID] != domain.HumanVerdictJudgeMisjudgment {
		t.Fatalf("not marked: %+v", repo.marked)
	}
}

// TestDecideConcurrentRaceReturnsLatest 验证对 PgReviewRepository.MarkReviewed
// 普通错误签名（非 sentinel）的降级：条目已被另一评审者置为 reviewed 时返回最新
// 条目（幂等/并发竞态语义），而非报错。
func TestDecideConcurrentRaceReturnsLatest(t *testing.T) {
	base := &fakeReviewRepo{}
	repo := &raceReviewRepo{fakeReviewRepo: base}
	svc := newTestReviewService(repo)
	item := domain.ReviewItem{
		ID: uuid.NewString(), SourceType: domain.ReviewSourceObservation,
		SourceID: "obs-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "s1",
		TriggerReason: domain.TriggerLowConfidence, Status: domain.ReviewStatusPending,
		Snapshot: map[string]any{"signals": observationForTest().Signals},
	}
	base.inserted = append(base.inserted, item)
	got, err := svc.Decide(context.Background(), "t1", item.ID, "reviewer@x", domain.HumanVerdictFail, "并发竞态")
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if got.Status != domain.ReviewStatusReviewed {
		t.Fatalf("status = %s, want reviewed", got.Status)
	}
	if got.HumanVerdict != domain.HumanVerdictPass {
		t.Fatalf("verdict = %s, want pass（另一评审者结论）", got.HumanVerdict)
	}
}

// TestDecideMarkReviewedErrorPropagated 验证 MarkReviewed 返回错误且条目仍为
// pending（非并发竞态）时，错误原样返回。
func TestDecideMarkReviewedErrorPropagated(t *testing.T) {
	repo := &fakeReviewRepo{err: errors.New("db down")}
	svc := newTestReviewService(repo)
	item := domain.ReviewItem{
		ID: uuid.NewString(), SourceType: domain.ReviewSourceObservation,
		SourceID: "obs-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "s1",
		TriggerReason: domain.TriggerLowConfidence, Status: domain.ReviewStatusPending,
	}
	repo.inserted = append(repo.inserted, item)
	if _, err := svc.Decide(context.Background(), "t1", item.ID, "reviewer@x", domain.HumanVerdictFail, "x"); err == nil {
		t.Fatal("want error propagated")
	}
}

// TestDecideIdempotentAlreadyReviewed 验证对已 reviewed 条目重复决策直接返回
// 现状，不重复写副作用（repo.marked 保持 nil 表示 MarkReviewed 未被再次调用）。
func TestDecideIdempotentAlreadyReviewed(t *testing.T) {
	repo := &fakeReviewRepo{}
	svc := newTestReviewService(repo)
	item := domain.ReviewItem{
		ID: uuid.NewString(), SourceType: domain.ReviewSourceObservation,
		SourceID: "obs-1", ResourceKind: domain.ResourceKindSkill, ResourceID: "s1",
		TriggerReason: domain.TriggerLowConfidence, Status: domain.ReviewStatusReviewed,
		HumanVerdict: domain.HumanVerdictPass,
	}
	repo.inserted = append(repo.inserted, item)
	got, err := svc.Decide(context.Background(), "t1", item.ID, "another@x", domain.HumanVerdictFail, "重复")
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if got.Status != domain.ReviewStatusReviewed || got.HumanVerdict != domain.HumanVerdictPass {
		t.Fatalf("unexpected item: %+v", got)
	}
	if repo.marked != nil {
		t.Fatalf("must not re-mark: %+v", repo.marked)
	}
}

func TestGetNotFound(t *testing.T) {
	svc := newTestReviewService(&fakeReviewRepo{})
	if _, err := svc.Get(context.Background(), "t1", "nope"); !errors.Is(err, ErrReviewItemNotFound) {
		t.Fatalf("err = %v, want ErrReviewItemNotFound", err)
	}
}

func TestListDelegates(t *testing.T) {
	repo := &fakeReviewRepo{}
	repo.inserted = []domain.ReviewItem{{ID: "i1"}}
	svc := newTestReviewService(repo)
	items, total, err := svc.List(context.Background(), "t1", port.ReviewFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("unexpected: items=%d total=%d", len(items), total)
	}
}

func TestRefreshBacklog(t *testing.T) {
	svc := newTestReviewService(&fakeReviewRepo{})
	if err := svc.RefreshBacklog(context.Background(), "t1"); err != nil {
		t.Fatalf("refresh: %v", err)
	}
}

func TestRefreshBacklogPropagatesError(t *testing.T) {
	svc := newTestReviewService(&fakeReviewRepo{err: errors.New("db down")})
	if err := svc.RefreshBacklog(context.Background(), "t1"); err == nil {
		t.Fatal("want error propagated")
	}
}

func TestEscalateObservationRefreshesBacklog(t *testing.T) {
	repo := &fakeReviewRepo{}
	metrics := &stubReviewMetrics{}
	svc := newTestReviewServiceWithMetrics(repo, metrics)
	obs := observationForTest()
	obs.Signals.Judge[0].Confidence = 0.3
	if err := svc.TryEscalateObservation(context.Background(), "t1", obs); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if len(metrics.backlog) != 1 || metrics.backlog[0] != 1 {
		t.Fatalf("backlog = %v, want [1]", metrics.backlog)
	}
}

func TestEscalateCaseResultRefreshesBacklog(t *testing.T) {
	repo := &fakeReviewRepo{}
	metrics := &stubReviewMetrics{}
	svc := newTestReviewServiceWithMetrics(repo, metrics)
	result := domain.EvalCaseResult{ID: "cr-1", CaseID: "c1", TraceID: "t-1", Passed: true}
	c := domain.EvalCase{ID: "c1", NeedsReview: true}
	assertion := domain.AssertionResult{Passed: true, Confidence: 0.9}
	if err := svc.TryEscalateCaseResult(context.Background(), "t1", "run-1", result, c, assertion); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if len(metrics.backlog) != 1 || metrics.backlog[0] != 1 {
		t.Fatalf("backlog = %v, want [1]", metrics.backlog)
	}
}

func TestDecideRefreshesBacklog(t *testing.T) {
	repo := &fakeReviewRepo{inserted: []domain.ReviewItem{{ID: "i1", Status: domain.ReviewStatusPending}}}
	metrics := &stubReviewMetrics{}
	svc := newTestReviewServiceWithMetrics(repo, metrics)
	if _, err := svc.Decide(context.Background(), "t1", "i1", "admin", domain.HumanVerdictFail, "bad"); err != nil {
		t.Fatalf("decide: %v", err)
	}
	if len(metrics.backlog) != 1 {
		t.Fatalf("backlog calls = %v, want 1 refresh after decision", metrics.backlog)
	}
}

func TestEscalateBacklogRefreshFailureIsFailOpen(t *testing.T) {
	repo := &countPendingErrRepo{fakeReviewRepo: &fakeReviewRepo{}}
	svc := newTestReviewService(repo)
	obs := observationForTest()
	obs.Signals.Judge[0].Confidence = 0.3
	// 积压刷新失败不得阻断升级主流程（fail-open）。
	if err := svc.TryEscalateObservation(context.Background(), "t1", obs); err != nil {
		t.Fatalf("escalate should succeed despite backlog refresh failure: %v", err)
	}
}

func TestEscalateNoTriggerSkipsBacklogRefresh(t *testing.T) {
	repo := &fakeReviewRepo{}
	metrics := &stubReviewMetrics{}
	svc := newTestReviewServiceWithMetrics(repo, metrics)
	if err := svc.TryEscalateObservation(context.Background(), "t1", observationForTest()); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if len(metrics.backlog) != 0 {
		t.Fatalf("backlog calls = %v, want none", metrics.backlog)
	}
}

// TestDecideNilMetricsDoesNotPanic 覆盖构造时未注入 Metrics（如 contract test /
// record-contracts wiring）的场景：Decide 触发的积压刷新不得因 nil Metrics 空指针
// panic，否则契约测试 500。回归 P1c 积压指标修复引入的缺陷。
func TestDecideNilMetricsDoesNotPanic(t *testing.T) {
	repo := &fakeReviewRepo{
		inserted: []domain.ReviewItem{{ID: "i1", Status: domain.ReviewStatusPending}},
	}
	svc := NewReviewService(ReviewServiceDeps{
		Repo:   repo,
		Logger: zap.NewNop(),
		// 刻意不注入 Metrics，复现 contract_test.go / record-contracts.go 的装配。
	})
	got, err := svc.Decide(context.Background(), "t1", "i1", "admin", domain.HumanVerdictFail, "bad")
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if got.Status != domain.ReviewStatusReviewed || got.HumanVerdict != domain.HumanVerdictFail {
		t.Fatalf("unexpected item: %+v", got)
	}
}
