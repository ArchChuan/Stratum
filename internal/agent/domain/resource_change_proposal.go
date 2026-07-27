package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type ProposalStatus string

const (
	StatusDraft          ProposalStatus = "draft"
	StatusReadyForReview ProposalStatus = "ready_for_review"
	StatusConfirmed      ProposalStatus = "confirmed"
	StatusApplying       ProposalStatus = "applying"
	StatusApplied        ProposalStatus = "applied"
	StatusInvalid        ProposalStatus = "invalid"
	StatusStale          ProposalStatus = "stale"
	StatusExpired        ProposalStatus = "expired"
	StatusFailed         ProposalStatus = "failed"
	StatusUnknownOutcome ProposalStatus = "unknown_outcome"
	StatusCancelled      ProposalStatus = "cancelled"
)

type ResourceKind string

const (
	ResourceAgent              ResourceKind = "agent"
	ResourceSkillDraft         ResourceKind = "skill_draft"
	ResourceMCPConfig          ResourceKind = "mcp_config"
	ResourceKnowledgeWorkspace ResourceKind = "knowledge_workspace"
)

type ProposalOperation string

const (
	OperationCreate ProposalOperation = "create"
	OperationUpdate ProposalOperation = "update"
)

type ResourceChangeProposal struct {
	ID                  string            `json:"id"`
	TenantID            string            `json:"-"`
	ConversationID      string            `json:"conversationId,omitempty"`
	ProposerID          string            `json:"proposerId"`
	ConfirmerID         string            `json:"confirmerId,omitempty"`
	ResourceKind        ResourceKind      `json:"resourceKind"`
	ResourceID          string            `json:"resourceId,omitempty"`
	Operation           ProposalOperation `json:"operation"`
	BaselineFingerprint string            `json:"baselineFingerprint,omitempty"`
	Payload             json.RawMessage   `json:"payload"`
	Summary             string            `json:"summary"`
	Status              ProposalStatus    `json:"status"`
	ErrorCode           string            `json:"errorCode,omitempty"`
	ApplyResult         ApplyResult       `json:"applyResult,omitempty"`
	ExpiresAt           time.Time         `json:"expiresAt"`
	CreatedAt           time.Time         `json:"createdAt"`
	UpdatedAt           time.Time         `json:"updatedAt"`
	ConfirmedAt         *time.Time        `json:"confirmedAt,omitempty"`
	AppliedAt           *time.Time        `json:"appliedAt,omitempty"`
}

type ProposalEvent struct {
	ID         string         `json:"id"`
	ProposalID string         `json:"proposalId"`
	ActorID    string         `json:"actorId"`
	FromStatus ProposalStatus `json:"fromStatus,omitempty"`
	ToStatus   ProposalStatus `json:"toStatus"`
	Code       string         `json:"code,omitempty"`
	Summary    string         `json:"summary,omitempty"`
	CreatedAt  time.Time      `json:"createdAt"`
}

type ApplyResult struct {
	ResourceID  string          `json:"resourceId,omitempty"`
	Fingerprint string          `json:"fingerprint,omitempty"`
	Readback    json.RawMessage `json:"readback,omitempty"`
}

type ProposalEnvelope struct {
	Proposal ResourceChangeProposal
	Payload  any
}

type AgentChange struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	Model            string   `json:"model"`
	MaxIterations    int      `json:"maxIterations"`
	MaxContextTokens int      `json:"maxContextTokens"`
	SkillIDs         []string `json:"skillIds,omitempty"`
	MCPToolIDs       []string `json:"mcpToolIds,omitempty"`
	WorkspaceIDs     []string `json:"workspaceIds,omitempty"`
}

type SkillDraftChange struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
}

type MCPConfigChange struct {
	Name         string          `json:"name"`
	Version      string          `json:"version"`
	Transport    string          `json:"transport"`
	Command      string          `json:"command,omitempty"`
	Args         []string        `json:"args,omitempty"`
	URL          string          `json:"url,omitempty"`
	Capabilities []string        `json:"capabilities,omitempty"`
	TimeoutSec   int             `json:"timeoutSec"`
	Retry        *MCPRetryChange `json:"retry,omitempty"`
}

type MCPRetryChange struct {
	Enabled        bool    `json:"enabled"`
	MaxRetries     int     `json:"maxRetries"`
	InitialDelayMs int64   `json:"initialDelayMs"`
	MaxDelayMs     int64   `json:"maxDelayMs"`
	BackoffFactor  float64 `json:"backoffFactor"`
}

type KnowledgeWorkspaceChange struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	EmbeddingModel string `json:"embeddingModel"`
}

func CanTransition(from, to ProposalStatus) bool {
	allowed := map[ProposalStatus]map[ProposalStatus]bool{
		StatusDraft:          {StatusReadyForReview: true, StatusInvalid: true, StatusExpired: true, StatusCancelled: true},
		StatusReadyForReview: {StatusConfirmed: true, StatusStale: true, StatusExpired: true, StatusCancelled: true},
		StatusConfirmed:      {StatusApplying: true, StatusStale: true, StatusExpired: true},
		StatusApplying:       {StatusApplied: true, StatusFailed: true, StatusStale: true, StatusUnknownOutcome: true},
	}
	return allowed[from][to]
}

func (p ResourceChangeProposal) Validate(now time.Time) error {
	if p.ID == "" || p.TenantID == "" || p.ProposerID == "" {
		return fmt.Errorf("%w: missing proposal identity", ErrProposalInvalid)
	}
	if !p.ExpiresAt.After(now) {
		return ErrProposalExpired
	}
	if !p.ResourceKind.Valid() || !p.Operation.Valid() {
		return fmt.Errorf("%w: unsupported resource or operation", ErrProposalInvalid)
	}
	if p.Operation == OperationCreate && p.ResourceID != "" {
		return fmt.Errorf("%w: create cannot target a resource", ErrProposalInvalid)
	}
	if p.Operation == OperationUpdate && (p.ResourceID == "" || p.BaselineFingerprint == "") {
		return fmt.Errorf("%w: update requires resource and baseline", ErrProposalInvalid)
	}
	if _, err := DecodeProposalPayload(p.ResourceKind, p.Operation, p.Payload); err != nil {
		return err
	}
	return nil
}

func (k ResourceKind) Valid() bool {
	switch k {
	case ResourceAgent, ResourceSkillDraft, ResourceMCPConfig, ResourceKnowledgeWorkspace:
		return true
	default:
		return false
	}
}

func (o ProposalOperation) Valid() bool {
	return o == OperationCreate || o == OperationUpdate
}

func DecodeProposalPayload(kind ResourceKind, operation ProposalOperation, raw json.RawMessage) (any, error) {
	if !kind.Valid() || !operation.Valid() {
		return nil, fmt.Errorf("%w: unsupported resource or operation", ErrProposalInvalid)
	}
	var target any
	switch kind {
	case ResourceAgent:
		target = &AgentChange{}
	case ResourceSkillDraft:
		target = &SkillDraftChange{}
	case ResourceMCPConfig:
		target = &MCPConfigChange{}
	case ResourceKnowledgeWorkspace:
		target = &KnowledgeWorkspaceChange{}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, fmt.Errorf("%w: decode payload: %v", ErrProposalInvalid, err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProposalInvalid, err)
	}
	return target, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("payload contains multiple JSON values")
		}
		return fmt.Errorf("decode trailing payload: %w", err)
	}
	return nil
}
