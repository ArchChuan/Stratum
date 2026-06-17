package middleware

import (
	"errors"
	"net/http"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"
	knowledgedomain "github.com/byteBuilderX/stratum/internal/knowledge/domain"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	memoryapp "github.com/byteBuilderX/stratum/internal/memory/application"
	skilldomain "github.com/byteBuilderX/stratum/internal/skill/domain"
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

	switch {
	// 404 — NotFound
	case errors.Is(err, pgx.ErrNoRows),
		errors.Is(err, knowledgedomain.ErrWorkspaceNotFound),
		errors.Is(err, iamdomain.ErrMemberNotFound),
		errors.Is(err, iamdomain.ErrTenantNotFound),
		errors.Is(err, agentapp.ErrNotFound),
		errors.Is(err, memoryapp.ErrNotFound):
		return http.StatusNotFound

	// 409 — Conflict
	case errors.Is(err, knowledgedomain.ErrWorkspaceConflict),
		errors.Is(err, knowledgedomain.ErrWorkspaceLinked),
		errors.Is(err, agentapp.ErrNameConflict),
		errors.Is(err, mcpdomain.ErrNameConflict):
		return http.StatusConflict

	// 422 — Unprocessable Entity
	case errors.Is(err, agentapp.ErrInvalidSkill):
		return http.StatusUnprocessableEntity

	// 429 — Too Many Requests
	case errors.Is(err, skilldomain.ErrConcurrencyLimit):
		return http.StatusTooManyRequests

	// 401 — Unauthorized
	case errors.Is(err, iamapp.ErrInviterMissing):
		return http.StatusUnauthorized

	// 403 — Forbidden
	case errors.Is(err, iamapp.ErrForbiddenAdminOrOwner),
		errors.Is(err, iamapp.ErrForbiddenOwner),
		errors.Is(err, iamapp.ErrForbiddenSelfModify),
		errors.Is(err, iamapp.ErrForbiddenOwnerRole),
		errors.Is(err, iamapp.ErrForbiddenRemoveOwner),
		errors.Is(err, iamapp.ErrForbiddenAdminRemove):
		return http.StatusForbidden

	// 400 — Validation / Bad Request
	case errors.Is(err, iamapp.ErrInvalidSettings),
		errors.Is(err, iamapp.ErrEmbedModelAlreadySet),
		errors.Is(err, knowledgedomain.ErrInvalidEmbeddingModel),
		errors.Is(err, knowledgedomain.ErrInvalidQueryMode),
		errors.Is(err, knowledgedomain.ErrEmbeddingModelImmutable),
		errors.Is(err, knowledgedomain.ErrChunkSizeImmutable),
		errors.Is(err, knowledgedomain.ErrChunkOverlapImmutable):
		return http.StatusBadRequest
	}

	return http.StatusInternalServerError
}
