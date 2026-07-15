package port

import (
	"context"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

type ExecutionResult struct {
	Output     any
	TraceID    string
	Tokens     int
	CostUSD    float64
	DurationMs int
}

type ResourceAdapter interface {
	ExecuteRevision(ctx context.Context, tenantID string, ref domain.ResourceRef, testCase domain.EvalCase) (ExecutionResult, error)
}

type RunRepository interface {
	SaveRun(ctx context.Context, tenantID string, run domain.EvalRun) error
	GetRun(ctx context.Context, tenantID, runID string) (domain.EvalRun, bool, error)
}

type SuiteRepository interface {
	CreateSuite(ctx context.Context, tenantID string, suite domain.EvalSuite, revision domain.EvalSuiteRevision) error
	GetDraftRevision(ctx context.Context, tenantID, suiteID string) (domain.EvalSuiteRevision, bool, error)
	GetRevision(ctx context.Context, tenantID, revisionID string) (domain.EvalSuiteRevision, bool, error)
	NextVersionNo(ctx context.Context, tenantID, suiteID string) (int, error)
	PublishRevision(ctx context.Context, tenantID, suiteID, revisionID string, versionNo int) (domain.EvalSuiteRevision, error)
}

type JobRepository interface {
	Enqueue(ctx context.Context, tenantID string, job domain.EvaluationJob) (domain.EvaluationJob, error)
	Get(ctx context.Context, tenantID, jobID string) (domain.EvaluationJob, bool, error)
	Claim(ctx context.Context, tenantID, workerID string, lease time.Duration) (*domain.EvaluationJob, error)
	Complete(ctx context.Context, tenantID, jobID, resultID string) error
	Fail(ctx context.Context, tenantID, jobID, errorMessage string) error
}

type CandidateCreator interface {
	LoadOptimizableSnapshot(ctx context.Context, tenantID string, baseline domain.ResourceRef) (map[string]any, error)
	CreateCandidate(ctx context.Context, tenantID string, baseline domain.ResourceRef, patch domain.CandidatePatch) (domain.ResourceRef, error)
}

type OptimizationRepository interface {
	SaveJobWithCandidates(
		ctx context.Context,
		tenantID string,
		job domain.OptimizationJob,
		candidates []domain.OptimizationCandidate,
	) error
}

type ExperimentRepository interface {
	Create(ctx context.Context, tenantID string, experiment domain.Experiment, deployment domain.Deployment) error
	Get(ctx context.Context, tenantID, experimentID string) (domain.Experiment, bool, error)
	SaveDecision(
		ctx context.Context,
		tenantID string,
		experiment domain.Experiment,
		decision domain.Decision,
		metrics domain.StageMetrics,
	) error
	ResolveDeployment(ctx context.Context, tenantID, resourceKind, resourceID string) (domain.Deployment, bool, error)
}

type FeedbackRepository interface {
	Record(ctx context.Context, tenantID string, input domain.FeedbackRequest) (domain.EvaluationFeedback, error)
	ActiveExperiment(ctx context.Context, tenantID, resourceKind, resourceID string) (domain.Experiment, bool, error)
	Observations(
		ctx context.Context,
		tenantID string,
		experiment domain.Experiment,
	) (stable []domain.OnlineObservation, canary []domain.OnlineObservation, observedMinutes int, err error)
}
