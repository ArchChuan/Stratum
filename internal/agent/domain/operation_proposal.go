package domain

import (
	"encoding/json"
	"time"
)

// OpProposalStatus is the lifecycle state of an operation proposal.
type OpProposalStatus string

const (
	OpProposed  OpProposalStatus = "proposed"
	OpReviewing OpProposalStatus = "reviewing"
	OpApproved  OpProposalStatus = "approved"
	OpRejected  OpProposalStatus = "rejected"
	// OpExecuted is the terminal state after an approved proposal is
	// consumed by a single replay (single-use approval).
	OpExecuted OpProposalStatus = "executed"
)

// OperationProposal is a pending agent mutation that requires approval.
// Fingerprint is the server-computed content hash of the intended change;
// PayloadSummary is a de-sensitized typed diff shown to reviewers so an
// approval is always bound to the reviewed content.
type OperationProposal struct {
	ID                 string           `json:"id"`
	TenantID           string           `json:"tenant_id"`
	AgentID            string           `json:"agent_id"`
	TargetAgentID      string           `json:"target_agent_id"`
	OpType             string           `json:"op_type"` // mirrors port.OperationType
	Delegation         string           `json:"delegation"`
	MaxDailyCostUSD    float64          `json:"max_daily_cost_usd"`
	MaxDailyExecutions int              `json:"max_daily_executions"`
	Fingerprint        string           `json:"fingerprint"`
	PayloadSummary     json.RawMessage  `json:"payload_summary,omitempty"`
	Status             OpProposalStatus `json:"status"`
	ProposerID         string           `json:"proposer_id"`
	ReviewedBy         string           `json:"reviewed_by"`
	ReviewNote         string           `json:"review_note"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
	ResolvedAt         *time.Time       `json:"resolved_at,omitempty"`
	ExpiresAt          *time.Time       `json:"expires_at,omitempty"`
}
