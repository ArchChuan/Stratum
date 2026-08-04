package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

// ErrGateUnavailable fails closed when the operation gate was never injected
// (misconfiguration); wiring registers the self-modify route only when the
// gate exists, so this is a defensive bound rather than a user-visible path.
var ErrGateUnavailable = errors.New("operation gate unavailable")

// SelfModifyRequest is the member-facing self-modification input. It carries
// no Budget/Delegation/Fingerprint: those are server policy — self-modify is
// always proposed for human approval, never budget- or delegation-gated by
// the client. The fingerprint is computed server-side over this payload.
type SelfModifyRequest struct {
	Name                  string
	Type                  string
	Description           string
	SystemPrompt          string
	LLMModel              string
	MaxIterations         int
	MaxContextTokens      int
	AllowedSkills         []string
	MCPToolIDs            []string
	KnowledgeWorkspaceIDs []string
	MemoryScope           string
	CheckpointEnabled     bool
}

// GatedSelfModifyResult is the outcome of GatedSelfModify. DTO is populated
// only when the mutation landed (approved replay). UsageErr reports a
// post-commit accounting failure: the mutation succeeded but usage recording
// did not — callers must surface it, never swallow it.
type GatedSelfModifyResult struct {
	Decision port.GateDecision
	DTO      AgentDTO
	UsageErr error
}

// GatedSelfModify is the member-controlled mutation channel: the request is
// hashed server-side, gated, and either lands immediately (single-use approved
// replay by the same proposer) or returns a pending approval decision without
// mutating anything. Approval unlocks a replay — it never executes remotely.
// The tool-level ToolExecutionGuard/ToolAuthorizer still run inside Update's
// execution path; the operation gate is a second, orthogonal layer.
func (s *AgentService) GatedSelfModify(
	ctx context.Context,
	tenantID, actorID, agentID string,
	req SelfModifyRequest,
) (GatedSelfModifyResult, error) {
	if s.deps.OperationGate == nil {
		return GatedSelfModifyResult{}, fmt.Errorf("gated self modify: %w", ErrGateUnavailable)
	}
	fingerprint, err := s.deps.OperationGate.ComputeFingerprint(agentID, port.OpSelfModify, req)
	if err != nil {
		return GatedSelfModifyResult{}, fmt.Errorf("compute self modify fingerprint: %w", err)
	}
	decision, err := s.deps.OperationGate.CheckWithProposal(ctx, port.OperationRequest{
		TenantID:    tenantID,
		AgentID:     agentID,
		OpType:      port.OpSelfModify,
		Fingerprint: fingerprint,
		ProposerID:  actorID,
	}, req)
	if err != nil {
		return GatedSelfModifyResult{}, fmt.Errorf("gate self modify: %w", err)
	}
	if !decision.Allowed {
		return GatedSelfModifyResult{Decision: decision}, nil
	}
	dto, err := s.Update(ctx, agentID, UpdateAgentInput(req))
	if err != nil {
		return GatedSelfModifyResult{Decision: decision}, err
	}
	// Usage accounting is best-effort and must not fail the landed mutation,
	// but a failure is real: WARN it and hand it back to the caller so the
	// response exposes it instead of silently dropping the run's record.
	if err := s.deps.OperationGate.RecordUsage(ctx, tenantID, agentID, port.OpSelfModify, 0); err != nil {
		s.deps.Logger.Warn("self modify usage accounting failed",
			zap.String("tenant_id", tenantID),
			zap.String("agent_id", agentID),
			zap.Error(err))
		return GatedSelfModifyResult{Decision: decision, DTO: dto, UsageErr: err}, nil
	}
	return GatedSelfModifyResult{Decision: decision, DTO: dto}, nil
}
