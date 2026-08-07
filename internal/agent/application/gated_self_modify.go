package application

import (
	"context"
	"errors"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"go.uber.org/zap"
)

// operationGateActor is the system actor recorded for approved replays. The
// mutation is executed by the operation gate on behalf of the human approval,
// so ownership is enforced at proposal time (member proposals are always
// gated) and audited as a system execution with the member's proposal row
// carrying proposer/reviewer provenance.
const operationGateActor = "operation-gate"

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
		TenantID: tenantID,
		AgentID:  agentID,
		OpType:   port.OpSelfModify,
		// self-modify 是内容变更通道：委托策略恒 no_delegate（服务端策略，
		// 客户端不可自报），缺省空串会违反 delegation CHECK 约束
		Delegation:  port.DelegationNone,
		Fingerprint: fingerprint,
		ProposerID:  actorID,
	}, req)
	if err != nil {
		return GatedSelfModifyResult{}, fmt.Errorf("gate self modify: %w", err)
	}
	if !decision.Allowed {
		return GatedSelfModifyResult{Decision: decision}, nil
	}
	// An approved replay is executed by the gate as a system actor: the member
	// proposing it is not the resource owner, and ownership was already
	// adjudicated by the human approver. The audit row still records the
	// system actor with the proposal's proposer/reviewer carrying provenance.
	ctx = reqctx.WithSystemActor(ctx, operationGateActor)
	// SelfModifyRequest 是 member 受控内容变更子集：Temperature/MaxTokens/
	// Compaction* 等派生配置不在自改面内，显式构造留零值（Update 对
	// MaxContextTokens<=0 有 derive 兜底，此处成员已显式传值不受影响）
	dto, err := s.Update(ctx, agentID, UpdateAgentInput{
		Name:                  req.Name,
		Type:                  req.Type,
		Description:           req.Description,
		SystemPrompt:          req.SystemPrompt,
		LLMModel:              req.LLMModel,
		MaxIterations:         req.MaxIterations,
		MaxContextTokens:      req.MaxContextTokens,
		AllowedSkills:         req.AllowedSkills,
		MCPToolIDs:            req.MCPToolIDs,
		KnowledgeWorkspaceIDs: req.KnowledgeWorkspaceIDs,
		MemoryScope:           req.MemoryScope,
		CheckpointEnabled:     req.CheckpointEnabled,
		ActorID:               actorID,
	})
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
