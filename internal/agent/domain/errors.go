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
	ErrSystemAssistantRevisionUnsupported = errors.New("system assistant revisions are unsupported")
)
