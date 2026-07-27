package dto

import (
	"encoding/json"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

type UpdateResourceChangeProposalRequest struct {
	Payload json.RawMessage `json:"payload" binding:"required"`
}

type ResourceChangeProposalResponse struct {
	ID                  string                   `json:"id"`
	ConversationID      string                   `json:"conversationId,omitempty"`
	ProposerID          string                   `json:"proposerId"`
	ConfirmerID         string                   `json:"confirmerId,omitempty"`
	ResourceKind        domain.ResourceKind      `json:"resourceKind"`
	ResourceID          string                   `json:"resourceId,omitempty"`
	Operation           domain.ProposalOperation `json:"operation"`
	BaselineFingerprint string                   `json:"baselineFingerprint,omitempty"`
	Payload             json.RawMessage          `json:"payload"`
	Summary             string                   `json:"summary"`
	Status              domain.ProposalStatus    `json:"status"`
	ErrorCode           string                   `json:"errorCode,omitempty"`
	ApplyResult         domain.ApplyResult       `json:"applyResult,omitempty"`
	Events              []domain.ProposalEvent   `json:"events"`
	ExpiresAt           time.Time                `json:"expiresAt"`
	CreatedAt           time.Time                `json:"createdAt"`
	UpdatedAt           time.Time                `json:"updatedAt"`
}

func NewResourceChangeProposalResponse(
	proposal domain.ResourceChangeProposal,
	events []domain.ProposalEvent,
) ResourceChangeProposalResponse {
	if events == nil {
		events = []domain.ProposalEvent{}
	}
	return ResourceChangeProposalResponse{
		ID: proposal.ID, ConversationID: proposal.ConversationID, ProposerID: proposal.ProposerID,
		ConfirmerID: proposal.ConfirmerID, ResourceKind: proposal.ResourceKind, ResourceID: proposal.ResourceID,
		Operation: proposal.Operation, BaselineFingerprint: proposal.BaselineFingerprint,
		Payload: proposal.Payload, Summary: proposal.Summary, Status: proposal.Status,
		ErrorCode: proposal.ErrorCode, ApplyResult: proposal.ApplyResult, Events: events,
		ExpiresAt: proposal.ExpiresAt, CreatedAt: proposal.CreatedAt, UpdatedAt: proposal.UpdatedAt,
	}
}
