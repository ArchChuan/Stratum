package port

import (
	"context"
	"errors"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

var ErrRevisionCommitUnknown = errors.New("revision metadata commit outcome unknown")

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

type CreateRevisionInput struct {
	ResourceKind                                            domain.ResourceKind
	ResourceID, ParentRevisionID, CreatedBy, IdempotencyKey string
	Source                                                  domain.RevisionSource
	Payload                                                 any
	SafeSummary                                             map[string]any
}

type RevisionRepository interface {
	Create(context.Context, string, domain.ResourceRevision, string) (domain.ResourceRevision, bool, error)
	Get(context.Context, string, domain.ResourceRef) (domain.ResourceRevision, bool, error)
}

type RevisionObjectStore interface {
	Put(context.Context, RevisionPayload) (RevisionPayloadRef, error)
	Get(context.Context, RevisionPayloadRef) ([]byte, error)
	Delete(context.Context, RevisionPayloadRef) error
}

type RevisionPayload struct {
	TenantID, Namespace, ID string
	Value                   any
}

type RevisionPayloadRef struct {
	URI, SHA256 string
	SizeBytes   int64
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
	StageFeedback(
		ctx context.Context,
		tenantID string,
		experiment domain.Experiment,
	) (feedback []domain.EvaluationFeedback, observedMinutes int, err error)
}

type ObservedResourceAssignment struct {
	RevisionID   string
	ExperimentID string
	Variant      string
}

type ObservedTrace struct {
	TraceID           string
	CostUSD           float64
	LatencyMs         int64
	Success           bool
	SecurityViolation bool
	Assignments       map[string]ObservedResourceAssignment
}

type TraceEvidenceReader interface {
	Resolve(context.Context, string, string) (ObservedTrace, error)
	ResolveBatch(context.Context, string, []string) (map[string]ObservedTrace, error)
}
