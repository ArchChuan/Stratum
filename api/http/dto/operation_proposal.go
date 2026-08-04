package dto

import (
	"encoding/json"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

// RejectOperationProposalRequest carries the mandatory rejection note.
type RejectOperationProposalRequest struct {
	Note string `json:"note" binding:"required"`
}

// OperationProposalResponse is the reviewer-facing view of an operation
// approval proposal. PayloadSummary is the de-sensitised diff surface shown
// on the approval screen; secrets were removed server-side at proposal time.
type OperationProposalResponse struct {
	ID                 string                  `json:"id"`
	AgentID            string                  `json:"agentId"`
	TargetAgentID      string                  `json:"targetAgentId,omitempty"`
	OpType             string                  `json:"opType"`
	Delegation         string                  `json:"delegation,omitempty"`
	MaxDailyCostUSD    float64                 `json:"maxDailyCostUsd,omitempty"`
	MaxDailyExecutions int                     `json:"maxDailyExecutions,omitempty"`
	PayloadSummary     json.RawMessage         `json:"payloadSummary"`
	Status             domain.OpProposalStatus `json:"status"`
	ProposerID         string                  `json:"proposerId"`
	ReviewedBy         string                  `json:"reviewedBy,omitempty"`
	ReviewNote         string                  `json:"reviewNote,omitempty"`
	CreatedAt          time.Time               `json:"createdAt"`
	ResolvedAt         *time.Time              `json:"resolvedAt,omitempty"`
	ExpiresAt          *time.Time              `json:"expiresAt,omitempty"`
}

func ToOperationProposalResponse(p domain.OperationProposal) OperationProposalResponse {
	return OperationProposalResponse{
		ID:                 p.ID,
		AgentID:            p.AgentID,
		TargetAgentID:      p.TargetAgentID,
		OpType:             p.OpType,
		Delegation:         p.Delegation,
		MaxDailyCostUSD:    p.MaxDailyCostUSD,
		MaxDailyExecutions: p.MaxDailyExecutions,
		PayloadSummary:     p.PayloadSummary,
		Status:             p.Status,
		ProposerID:         p.ProposerID,
		ReviewedBy:         p.ReviewedBy,
		ReviewNote:         p.ReviewNote,
		CreatedAt:          p.CreatedAt,
		ResolvedAt:         p.ResolvedAt,
		ExpiresAt:          p.ExpiresAt,
	}
}
