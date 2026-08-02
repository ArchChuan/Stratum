package domain

import "time"

// OpProposalStatus is the lifecycle state of an operation proposal.
type OpProposalStatus string

const (
	OpProposed  OpProposalStatus = "proposed"
	OpReviewing OpProposalStatus = "reviewing"
	OpApproved  OpProposalStatus = "approved"
	OpRejected  OpProposalStatus = "rejected"
)

// OperationProposal is a pending agent mutation that requires approval.
// Each proposal ties to an F1 fingerprint for deterministic attribution.
type OperationProposal struct {
	ID          string           `json:"id"`
	TenantID    string           `json:"tenant_id"`
	AgentID     string           `json:"agent_id"`
	OpType      string           `json:"op_type"` // mirrors port.OperationType
	Fingerprint string           `json:"fingerprint"`
	Status      OpProposalStatus `json:"status"`
	ReviewedBy  string           `json:"reviewed_by"`
	ReviewNote  string           `json:"review_note"`
	CreatedAt   time.Time        `json:"created_at"`
	ResolvedAt  *time.Time       `json:"resolved_at,omitempty"`
}
