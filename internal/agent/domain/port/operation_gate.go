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
	Fingerprint   string // F1 content hash for attribution
}

// OperationGate is the central security gate for agent mutations.
// Phase 2 implementations enforce delegation policies, budget caps,
// and approval workflows.
type OperationGate interface {
	Check(ctx context.Context, req OperationRequest) (allowed bool, reason string)
}
