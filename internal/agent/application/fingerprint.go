package application

import (
	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

// CaptureFingerprint builds an ExecutionFingerprint from the execution context.
// It is called at the end of every agent execution so the immutable snapshot
// can be written to OTEL span attributes and attached to the token ledger record.
func CaptureFingerprint(
	resolvedModel string,
	routedVia []string,
	promptVersion string,
	skillRevisions map[string]string,
	tunables map[string]any,
	abBucket int,
) *domain.ExecutionFingerprint {
	return &domain.ExecutionFingerprint{
		ModelResolved:   resolvedModel,
		ModelRoutedVia:  routedVia,
		PromptVersion:   promptVersion,
		SkillRevisions:  skillRevisions,
		TunableSnapshot: tunables,
		ABBucket:        abBucket,
	}
}
