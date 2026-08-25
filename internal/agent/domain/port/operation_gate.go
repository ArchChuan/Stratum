// Package port defines the agent security gate interface. Implementations
// (Phase 2) gate all agent mutation operations through approval workflows.
package port

import "context"

// OperationType classifies the kind of agent operation being gated.
type OperationType string

const (
	OpRevisionApply      OperationType = "revision_apply"
	OpCrossAgentDelegate OperationType = "cross_agent_delegate"
	OpScheduleCreate     OperationType = "schedule_create"
	OpSelfModify         OperationType = "self_modify"
	// OpGrantEditor 标记「成员白名单自助申请」提案（agent/skill 编辑权、
	// knowledge_doc 查看权）。批准即授予：与 self_modify 不同，不走
	// approved→proposer-replay 两段式，由 OperationProposalService.Approve
	// 特判分发到 grantEditor 闭包直接落库。
	OpGrantEditor OperationType = "grant_editor"
)

// DelegationPolicy controls cross-agent data sharing.
type DelegationPolicy string

const (
	DelegationNone     DelegationPolicy = "no_delegate"
	DelegationReadOnly DelegationPolicy = "read_only"
	DelegationFull     DelegationPolicy = "full"
)

// OperationBudget captures cost and execution limits for a gated operation.
type OperationBudget struct {
	MaxDailyCostUSD    float64
	MaxDailyExecutions int
}

// OperationRequest is the input to OperationGate.Check.
type OperationRequest struct {
	TenantID      string
	AgentID       string
	TargetAgentID string
	OpType        OperationType
	Delegation    DelegationPolicy
	Budget        OperationBudget
	Fingerprint   string // server-computed content hash (sha256(agentID|opType|canonicalJSON(payload)))
	ProposerID    string // actor requesting the operation; binds approved replays to the proposer
}

// OperationGate is the central security gate for agent mutations. It
// enforces delegation policies, budget caps, and human approval workflows.
// Fingerprints are always computed server-side; clients never supply them.
type OperationGate interface {
	// Check is the thin port entry for callers that pre-computed the
	// fingerprint and have no reviewable payload (replay-only flows).
	Check(ctx context.Context, req OperationRequest) (allowed bool, reason string)
	// CheckWithProposal runs the full decision table, persisting a proposal
	// (with de-sensitised payload summary) when human approval is required.
	CheckWithProposal(ctx context.Context, req OperationRequest, payload any) (GateDecision, error)
	// ComputeFingerprint derives sha256(agentID | opType | canonicalJSON(payload)).
	ComputeFingerprint(agentID string, opType OperationType, payload any) (string, error)
	// RecordUsage adds the daily usage counters after a gated operation ran.
	// Failure is surfaced, not swallowed; the caller decides how to handle it.
	RecordUsage(ctx context.Context, tenantID, agentID string, opType OperationType, costUSD float64) error
}

// GateDecision is the outcome of a gate check. Reason is one of the
// GateReason* contract strings; ProposalID is set when a proposal was created
// and the operation must wait for review.
type GateDecision struct {
	Allowed    bool
	Reason     string
	ProposalID string
}
