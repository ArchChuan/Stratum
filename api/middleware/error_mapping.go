package middleware

import (
	"context"
	"errors"
	"net/http"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"
	knowledgedomain "github.com/byteBuilderX/stratum/internal/knowledge/domain"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	memoryapp "github.com/byteBuilderX/stratum/internal/memory/application"
	memorydomain "github.com/byteBuilderX/stratum/internal/memory/domain"
	skilldomain "github.com/byteBuilderX/stratum/internal/skill/domain"
	workflowdomain "github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/jackc/pgx/v5"
)

// HTTPError carries an explicit HTTP status alongside an error so handlers
// can short-circuit ErrorHandler's sentinel matching for one-off cases
// (validation failures, missing tenant context, etc.).
type HTTPError struct {
	Status int
	Err    error
}

func (e *HTTPError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *HTTPError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// NewHTTPError wraps err with an explicit HTTP status.
func NewHTTPError(status int, err error) *HTTPError {
	return &HTTPError{Status: status, Err: err}
}

// errorStatusTable maps sentinel domain errors to HTTP status codes.
// New entries only need to be added here; MapErrorToStatus walks the table.
var errorStatusTable = map[error]int{
	context.DeadlineExceeded:                      http.StatusGatewayTimeout,
	llmgatewaydomain.ErrUpstreamRequestFailed:     http.StatusBadGateway,
	agentdomain.ErrEvidenceUnavailable:            http.StatusServiceUnavailable,
	agentdomain.ErrAssistantModelUnavailable:      http.StatusServiceUnavailable,
	agentdomain.ErrEvidenceInvalid:                http.StatusBadGateway,
	pgx.ErrNoRows:                                 http.StatusNotFound,
	agentdomain.ErrEvidenceNotFound:               http.StatusNotFound,
	knowledgedomain.ErrWorkspaceNotFound:          http.StatusNotFound,
	knowledgedomain.ErrDocumentNotFound:           http.StatusNotFound,
	iamdomain.ErrMemberNotFound:                   http.StatusNotFound,
	iamdomain.ErrTenantNotFound:                   http.StatusNotFound,
	agentapp.ErrNotFound:                          http.StatusNotFound,
	agentdomain.ErrApprovalNotFound:               http.StatusNotFound,
	agentdomain.ErrProposalNotFound:               http.StatusNotFound,
	agentdomain.ErrNotFound:                       http.StatusNotFound,
	memoryapp.ErrNotFound:                         http.StatusNotFound,
	memorydomain.ErrEntryNotFound:                 http.StatusNotFound,
	memorydomain.ErrSessionNotFound:               http.StatusNotFound,
	memorydomain.ErrFactNotFound:                  http.StatusNotFound,
	memorydomain.ErrEntityNotFound:                http.StatusNotFound,
	skilldomain.ErrSkillNotFound:                  http.StatusNotFound,
	mcpdomain.ErrServerNotFound:                   http.StatusNotFound,
	evalapp.ErrSuiteNotFound:                      http.StatusNotFound,
	evalapp.ErrJobNotFound:                        http.StatusNotFound,
	evalapp.ErrRunNotFound:                        http.StatusNotFound,
	evalapp.ErrExperimentNotFound:                 http.StatusNotFound,
	evaldomain.ErrCenterResourceNotFound:          http.StatusNotFound,
	evaldomain.ErrCandidateNotFound:               http.StatusNotFound,
	workflowdomain.ErrNotFound:                    http.StatusNotFound,
	agentapp.ErrApprovalExpired:                   http.StatusGone,
	knowledgedomain.ErrWorkspaceConflict:          http.StatusConflict,
	agentdomain.ErrProposalStale:                  http.StatusConflict,
	agentdomain.ErrProposalExpired:                http.StatusConflict,
	agentdomain.ErrProposalAlreadyClaimed:         http.StatusConflict,
	agentdomain.ErrProposalUnknownOutcome:         http.StatusConflict,
	agentdomain.ErrSystemAssistantManaged:         http.StatusConflict,
	mcpdomain.ErrPlatformManagedServer:            http.StatusConflict,
	knowledgedomain.ErrPlatformManagedWorkspace:   http.StatusConflict,
	knowledgedomain.ErrWorkspaceLinked:            http.StatusConflict,
	knowledgedomain.ErrDuplicateDocument:          http.StatusConflict,
	knowledgedomain.ErrDocumentProcessing:         http.StatusConflict,
	agentapp.ErrNameConflict:                      http.StatusConflict,
	mcpdomain.ErrNameConflict:                     http.StatusConflict,
	skilldomain.ErrSkillNameConflict:              http.StatusConflict,
	skilldomain.ErrSkillDraftNotFound:             http.StatusConflict,
	skilldomain.ErrSkillLinked:                    http.StatusConflict,
	memorydomain.ErrFactQuotaExceeded:             http.StatusConflict,
	memorydomain.ErrFactAlreadyDeleted:            http.StatusConflict,
	evaldomain.ErrOptimizationIdempotencyConflict: http.StatusConflict,
	evaldomain.ErrFeedbackIdempotencyConflict:     http.StatusConflict,
	agentapp.ErrApprovalNotApproved:               http.StatusConflict,
	agentapp.ErrApprovalOutcomeUnknown:            http.StatusConflict,
	agentdomain.ErrApprovalAlreadyDecided:         http.StatusConflict,
	agentdomain.ErrApprovalAlreadyExecuted:        http.StatusConflict,
	workflowdomain.ErrRevisionConflict:            http.StatusConflict,
	workflowdomain.ErrIdempotencyConflict:         http.StatusConflict,
	workflowdomain.ErrInvalidTransition:           http.StatusConflict,
	workflowdomain.ErrGenerationConflict:          http.StatusConflict,
	workflowdomain.ErrFenceConflict:               http.StatusConflict,
	workflowdomain.ErrDecisionConflict:            http.StatusConflict,
	workflowdomain.ErrApprovalRequired:            http.StatusConflict,
	evaldomain.ErrExperimentStateConflict:         http.StatusConflict,
	evaldomain.ErrExperimentCommandConflict:       http.StatusConflict,
	evaldomain.ErrExperimentDeploymentConflict:    http.StatusConflict,
	evaldomain.ErrExperimentStableNotPublished:    http.StatusConflict,
	evaldomain.ErrExperimentInvalidCandidate:      http.StatusConflict,
	evaldomain.ErrExperimentSuiteNotPublished:     http.StatusConflict,
	evaldomain.ErrExperimentOfflineRunRequired:    http.StatusConflict,
	evaldomain.ErrExperimentCommandNotAllowed:     http.StatusConflict,
	evaldomain.ErrCandidateStateConflict:          http.StatusConflict,
	evaldomain.ErrCandidateCommandConflict:        http.StatusConflict,
	evaldomain.ErrCandidateCommandNotAllowed:      http.StatusConflict,
	evaldomain.ErrFeedbackTraceForbidden:          http.StatusForbidden,
	agentapp.ErrInvalidSkill:                      http.StatusUnprocessableEntity,
	agentdomain.ErrProposalApplyFailed:            http.StatusUnprocessableEntity,
	skilldomain.ErrConcurrencyLimit:               http.StatusTooManyRequests,
	knowledgedomain.ErrIngestQueueFull:            http.StatusTooManyRequests,
	iamapp.ErrForbiddenAdminOrOwner:               http.StatusForbidden,
	agentdomain.ErrProposalForbidden:              http.StatusForbidden,
	iamapp.ErrForbiddenOwner:                      http.StatusForbidden,
	iamapp.ErrForbiddenSelfModify:                 http.StatusForbidden,
	iamapp.ErrForbiddenOwnerRole:                  http.StatusForbidden,
	iamapp.ErrForbiddenRemoveOwner:                http.StatusForbidden,
	iamapp.ErrForbiddenAdminRemove:                http.StatusForbidden,
	memorydomain.ErrAgentMemoryDisabled:           http.StatusForbidden,
	memorydomain.ErrScopeMismatch:                 http.StatusForbidden,
	agentapp.ErrPlatformMCPBindingForbidden:       http.StatusForbidden,
	workflowdomain.ErrForbidden:                   http.StatusForbidden,
	iamapp.ErrInvalidSettings:                     http.StatusBadRequest,
	agentdomain.ErrProposalInvalid:                http.StatusBadRequest,
	agentdomain.ErrInvalidSystemAssistantModel:    http.StatusBadRequest,
	iamdomain.ErrDefaultTenantDelete:              http.StatusBadRequest,
	iamdomain.ErrUsernameTaken:                    http.StatusConflict,
	knowledgedomain.ErrInvalidEmbeddingModel:      http.StatusBadRequest,
	knowledgedomain.ErrInvalidQueryMode:           http.StatusBadRequest,
	knowledgedomain.ErrEmbeddingModelImmutable:    http.StatusBadRequest,
	knowledgedomain.ErrChunkSizeImmutable:         http.StatusBadRequest,
	knowledgedomain.ErrChunkOverlapImmutable:      http.StatusBadRequest,
	knowledgedomain.ErrChunkLimitExceeded:         http.StatusBadRequest,
	skilldomain.ErrSkillTypeImmutable:             http.StatusBadRequest,
	skilldomain.ErrNotCodeSkill:                   http.StatusBadRequest,
	skilldomain.ErrSkillUnsupportedType:           http.StatusBadRequest,
	skilldomain.ErrSkillCodeAnalysis:              http.StatusBadRequest,
	skilldomain.ErrSkillNotPublishable:            http.StatusBadRequest,
	evalapp.ErrSuiteNameRequired:                  http.StatusBadRequest,
	evalapp.ErrSuiteCasesRequired:                 http.StatusBadRequest,
	evaldomain.ErrInvalidCenterQuery:              http.StatusBadRequest,
	evaldomain.ErrInvalidCandidateCommand:         http.StatusBadRequest,
	memorydomain.ErrInvalidStatus:                 http.StatusBadRequest,
	memorydomain.ErrUserIDMismatch:                http.StatusBadRequest,
	memorydomain.ErrEmptyContent:                  http.StatusBadRequest,
	workflowdomain.ErrInvalidSpec:                 http.StatusBadRequest,
	workflowdomain.ErrInvalidInputSchema:          http.StatusBadRequest,
}

// MapErrorToStatus walks the wrap chain and returns the HTTP status that
// should be sent for err. Handlers that emit `c.Error(err)` must rely on
// this table — no scattered `errors.Is` switch blocks elsewhere.
//
// Mapping policy:
//   - NotFound family    → 404
//   - Conflict / dup     → 409
//   - Forbidden family   → 403
//   - Unauthorized       → 401
//   - Validation / 4xx   → 400
//   - Concurrency limit  → 429
//   - Unprocessable      → 422
//   - default            → 500
func MapErrorToStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}

	var he *HTTPError
	if errors.As(err, &he) && he.Status != 0 {
		return he.Status
	}

	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		return http.StatusRequestEntityTooLarge
	}

	for sentinel, status := range errorStatusTable {
		if errors.Is(err, sentinel) {
			return status
		}
	}

	return http.StatusInternalServerError
}
