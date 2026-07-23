package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

// SystemAssistantProfileSource exposes one selected immutable runtime profile.
// Implementations must return defensive value copies.
type SystemAssistantProfileSource interface {
	Profile() domain.SystemAssistantProfile
	Version() string
}

type DiagnosticEvidenceProvider interface {
	Authorize(context.Context, domain.DiagnosticRequest) (domain.DiagnosticAuthorization, error)
	CollectAuthorized(context.Context, domain.DiagnosticRequest) (domain.DiagnosticEvidence, error)
}

type TenantRoleResolver interface {
	ResolveTenantRole(context.Context, string, string) (string, error)
}

type TenantModelDiagnosticProvider interface {
	DiagnosticModelStatus(context.Context, string) (domain.TenantModelDiagnosticStatus, error)
}
