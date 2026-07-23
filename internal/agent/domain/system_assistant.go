package domain

import "time"

const (
	SystemAssistantKey                   = "stratum.platform_assistant"
	CurrentSystemAssistantProfileVersion = "2026-07-23.v1"
)

// SystemAssistantProfile is an immutable, code-reviewed runtime definition.
// Old versions remain addressable so historical traces and rollback targets
// continue to resolve after a new version becomes active.
type SystemAssistantProfile struct {
	Key              string
	Version          string
	Name             string
	Description      string
	SystemPrompt     string
	MaxIterations    int
	MaxContextTokens int
}

// Citation identifies one bounded excerpt from the platform-managed official
// documentation catalog.
type Citation struct {
	DocumentID     string `json:"documentId"`
	Title          string `json:"title"`
	ProductVersion string `json:"productVersion"`
	Section        string `json:"section"`
	URL            string `json:"url"`
	Excerpt        string `json:"excerpt"`
}

type DiagnosticScope string

const (
	DiagnosticScopeSelf   DiagnosticScope = "self"
	DiagnosticScopeTenant DiagnosticScope = "tenant"
)

type DiagnosticArea string

const (
	DiagnosticAreaAgent     DiagnosticArea = "agent"
	DiagnosticAreaSkill     DiagnosticArea = "skill"
	DiagnosticAreaMCP       DiagnosticArea = "mcp"
	DiagnosticAreaKnowledge DiagnosticArea = "knowledge"
	DiagnosticAreaModel     DiagnosticArea = "model"
)

const (
	DiagnosticGapUnavailable = "evidence_unavailable"
	DiagnosticGapTimeout     = "evidence_timeout"
	DiagnosticGapCancelled   = "evidence_cancelled"
)

type DiagnosticRequest struct {
	TenantID string           `json:"-"`
	UserID   string           `json:"-"`
	Scope    DiagnosticScope  `json:"scope"`
	Areas    []DiagnosticArea `json:"areas"`
}

type DiagnosticFact struct {
	Area          DiagnosticArea `json:"area"`
	ObjectID      string         `json:"objectId,omitempty"`
	Statement     string         `json:"statement"`
	Source        string         `json:"source"`
	ObservedAt    time.Time      `json:"observedAt"`
	SubjectUserID string         `json:"-"`
}

type EvidenceGap struct {
	Area DiagnosticArea `json:"area"`
	Code string         `json:"code"`
}

type DiagnosticEvidence struct {
	Scope       DiagnosticScope  `json:"scope"`
	Facts       []DiagnosticFact `json:"facts"`
	Gaps        []EvidenceGap    `json:"gaps"`
	CollectedAt time.Time        `json:"collectedAt"`
}

type TenantModelDiagnosticStatus struct {
	Configured bool
}

func (a DiagnosticArea) Valid() bool {
	switch a {
	case DiagnosticAreaAgent, DiagnosticAreaSkill, DiagnosticAreaMCP, DiagnosticAreaKnowledge, DiagnosticAreaModel:
		return true
	default:
		return false
	}
}
