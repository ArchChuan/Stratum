package domain

import "errors"

// Sentinel errors shared across the Agent domain. Application aliases these
// where callers must preserve errors.Is checks across layers.
var (
	ErrNotFound                           = errors.New("agent not found")
	ErrNameConflict                       = errors.New("agent name already exists")
	ErrInvalidSkill                       = errors.New("skill not found")
	ErrSystemAssistantManaged             = errors.New("system assistant is platform managed")
	ErrInvalidOfficialEvidenceQuery       = errors.New("official evidence query is empty")
	ErrOfficialEvidenceNotFound           = errors.New("official evidence not found")
	ErrDiagnosticForbidden                = errors.New("diagnostic forbidden")
	ErrDiagnosticEvidenceUnavailable      = errors.New("diagnostic evidence unavailable")
	ErrAssistantModelUnavailable          = errors.New("system assistant model unavailable")
	ErrInvalidSystemAssistantModel        = errors.New("invalid system assistant model")
	ErrSystemAssistantRevisionUnsupported = errors.New("system assistant revisions are unsupported")
	ErrProposalInvalid                    = errors.New("proposal invalid")
	ErrProposalNotFound                   = errors.New("proposal not found")
	ErrProposalStale                      = errors.New("proposal stale")
	ErrProposalExpired                    = errors.New("proposal expired")
	ErrProposalForbidden                  = errors.New("proposal forbidden")
	ErrProposalAlreadyClaimed             = errors.New("proposal already claimed")
	ErrProposalApplyFailed                = errors.New("proposal apply failed")
	ErrProposalUnknownOutcome             = errors.New("proposal outcome unknown")
)
