package contracttest

import (
	"context"
	"time"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
)

type contractQueryRepo struct{}

func (contractQueryRepo) Overview(context.Context, string) (domain.CenterOverview, error) {
	return domain.CenterOverview{}, nil
}
func (contractQueryRepo) ListResources(context.Context, string, port.CenterFilter) (domain.ResourcePage, error) {
	return domain.ResourcePage{Items: []domain.ResourceSummary{}}, nil
}
func (contractQueryRepo) ListSuites(context.Context, string, port.CenterFilter) (domain.SuitePage, error) {
	return domain.SuitePage{Items: []domain.SuiteSummary{}}, nil
}
func (contractQueryRepo) ListRuns(context.Context, string, port.CenterFilter) (domain.RunPage, error) {
	return domain.RunPage{Items: []domain.RunSummary{}}, nil
}
func (contractQueryRepo) ListCandidates(context.Context, string, port.CenterFilter) (domain.CandidatePage, error) {
	return domain.CandidatePage{Items: []domain.CandidateSummary{}}, nil
}
func (contractQueryRepo) ListExperiments(context.Context, string, port.CenterFilter) (domain.ExperimentPage, error) {
	return domain.ExperimentPage{Items: []domain.ExperimentSummary{}}, nil
}
func (contractQueryRepo) Timeline(context.Context, string, port.CenterFilter) (domain.TimelinePage, error) {
	return domain.TimelinePage{Items: []domain.TimelineEvent{}}, nil
}

type contractExperimentRepo struct{}

func (contractExperimentRepo) ValidatePrerequisites(context.Context, string, domain.ResourceRef,
	domain.ResourceRef, string) error {
	return nil
}
func (contractExperimentRepo) Create(context.Context, string, domain.Experiment, domain.Deployment, *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (contractExperimentRepo) Get(context.Context, string, string) (domain.Experiment, bool, error) {
	return domain.Experiment{}, false, nil
}
func (contractExperimentRepo) SaveDecision(context.Context, string, domain.Experiment, domain.Decision, domain.StageMetrics, string, string) (domain.Experiment, domain.Decision, error) {
	return domain.Experiment{}, domain.DecisionHold, nil
}
func (contractExperimentRepo) ApplyCommand(context.Context, string, string, domain.ExperimentCommandAction, domain.ExperimentCommand) (domain.Experiment, error) {
	return domain.Experiment{}, domain.ErrExperimentStateConflict
}
func (contractExperimentRepo) ResolveDeployment(context.Context, string, string, string) (domain.Deployment, bool, error) {
	return domain.Deployment{}, false, nil
}
func (contractExperimentRepo) HasRunningExperiment(context.Context, string, string, string) (bool, error) {
	return false, nil
}
func (contractExperimentRepo) ListPendingExperiments(context.Context, string, string, string) ([]domain.Experiment, error) {
	return nil, nil
}
func (contractExperimentRepo) ListRunningExperiments(context.Context, string) ([]domain.Experiment, error) {
	return nil, nil
}

type contractCandidateRepo struct{}

func (contractCandidateRepo) Reject(context.Context, string, string, domain.CandidateCommand) (domain.CandidateSummary, error) {
	return domain.CandidateSummary{}, domain.ErrCandidateCommandConflict
}

// contractObservationRepo 为运行态观测查询 API 提供确定性单条/分页响应
// （P1a；golden 文件与此 stub 的返回一一对应）。
type contractObservationRepo struct{}

func (contractObservationRepo) Save(_ context.Context, _ string, _ *domain.EvalObservation) error {
	return nil
}

func (contractObservationRepo) Get(_ context.Context, _, _ string) (*domain.EvalObservation, error) {
	return &domain.EvalObservation{
		ID: "obs-1", TraceID: "trace-1",
		Resource:  domain.ObservationResourceRef{Kind: domain.ResourceKindAgent, ResourceID: "agent-1"},
		Verdict:   domain.VerdictPass,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}, nil
}

func (contractObservationRepo) QueryByResource(_ context.Context, _, _, _ string,
	_, _ *time.Time, _, _ int,
) ([]domain.EvalObservation, error) {
	return []domain.EvalObservation{{
		ID: "obs-1", TraceID: "trace-1",
		Resource:  domain.ObservationResourceRef{Kind: domain.ResourceKindAgent, ResourceID: "agent-1"},
		Verdict:   domain.VerdictPass,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}}, nil
}

func (contractObservationRepo) FindLatestByTrace(_ context.Context, _, _ string) (*domain.EvalObservation, error) {
	return nil, nil
}

func (contractObservationRepo) UpdateBehaviorSignals(_ context.Context, _, _ string, _ domain.BehaviorSignals) error {
	return nil
}

// reviewItem 是评审池 golden 的确定性条目：pending 状态使 Decide 走真实
// pending → reviewed 转换（reviewed_at 为 live 时间戳，由 WantBodyRE 容错）。
func reviewItem() *domain.ReviewItem {
	return &domain.ReviewItem{
		ID:            "review-1",
		SourceType:    domain.ReviewSourceObservation,
		SourceID:      "obs-1",
		RunID:         "run-1",
		TraceID:       "trace-1",
		ResourceKind:  domain.ResourceKindAgent,
		ResourceID:    "agent-1",
		TriggerReason: domain.TriggerLowConfidence,
		RiskLevel:     domain.ReviewRiskMedium,
		Snapshot:      map[string]any{"signals": map[string]any{"judge": []any{}}},
		Status:        domain.ReviewStatusPending,
		CreatedAt:     time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// contractReviewRepo 为评审池查询/决策 API 提供确定性响应（P1c；golden 文件与
// 此 stub 的返回一一对应）。
type contractReviewRepo struct{}

func (contractReviewRepo) UpsertItem(_ context.Context, _ string, _ *domain.ReviewItem) (bool, error) {
	return true, nil
}

func (contractReviewRepo) GetItem(_ context.Context, _, _ string) (*domain.ReviewItem, error) {
	return reviewItem(), nil
}

func (contractReviewRepo) ListItems(_ context.Context, _ string, _ port.ReviewFilter,
) ([]domain.ReviewItem, int64, error) {
	return []domain.ReviewItem{*reviewItem()}, 1, nil
}

func (contractReviewRepo) MarkReviewed(_ context.Context, _, _ string, _ domain.HumanVerdict, _, _ string) error {
	return nil
}

func (contractReviewRepo) CreateCalibrationSample(_ context.Context, _ string, _ *domain.CalibrationSample) error {
	return nil
}

func (contractReviewRepo) CreateAttributionEntry(_ context.Context, _ string, _ *domain.AttributionEntry) error {
	return nil
}

func (contractReviewRepo) CountPending(_ context.Context, _ string) (int64, error) {
	return 0, nil
}
