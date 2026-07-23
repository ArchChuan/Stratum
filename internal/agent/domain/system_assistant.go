package domain

import "time"

const (
	SystemAssistantKey                   = "stratum.platform_assistant"
	SystemAssistantID                    = "stratum-platform-assistant"
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
	Scope       DiagnosticScope        `json:"scope"`
	Facts       []DiagnosticFact       `json:"facts"`
	Gaps        []EvidenceGap          `json:"gaps"`
	AreaResults []DiagnosticAreaResult `json:"areaResults"`
	CollectedAt time.Time              `json:"collectedAt"`
}

type DiagnosticAreaResult struct {
	Area       DiagnosticArea `json:"area"`
	Outcome    string         `json:"outcome"`
	DurationMs int64          `json:"durationMs"`
}

type DiagnosticAuthorization struct {
	Request   DiagnosticRequest
	RoleClass string
}

// SystemAssistantToolArtifact is typed evidence captured directly from a
// governed internal tool. It is never reconstructed from model prose.
type SystemAssistantToolArtifact struct {
	Tool      string              `json:"tool"`
	Citations []Citation          `json:"citations,omitempty"`
	Evidence  *DiagnosticEvidence `json:"evidence,omitempty"`
	LatencyMs int64               `json:"latencyMs"`
	Outcome   string              `json:"outcome"`
	ErrorCode string              `json:"errorCode,omitempty"`
}

type DiagnosticReport struct {
	Facts              []DiagnosticFact       `json:"facts"`
	Inferences         []string               `json:"inferences"`
	EvidenceGaps       []EvidenceGap          `json:"evidenceGaps"`
	RecommendedActions []string               `json:"recommendedActions"`
	Citations          []Citation             `json:"citations"`
	Steps              []DiagnosticAreaResult `json:"steps"`
}

type ExecutionArtifact struct {
	Type             string            `json:"type"`
	ProfileVersion   string            `json:"profileVersion,omitempty"`
	Citations        []Citation        `json:"citations,omitempty"`
	DiagnosticReport *DiagnosticReport `json:"diagnosticReport,omitempty"`
}

func BuildDiagnosticReport(toolArtifacts []SystemAssistantToolArtifact) *DiagnosticReport {
	r := &DiagnosticReport{Facts: []DiagnosticFact{}, Inferences: []string{}, EvidenceGaps: []EvidenceGap{}, RecommendedActions: []string{}, Citations: []Citation{}, Steps: []DiagnosticAreaResult{}}
	for _, a := range toolArtifacts {
		r.Citations = append(r.Citations, a.Citations...)
		if a.Evidence != nil {
			r.Facts = append(r.Facts, a.Evidence.Facts...)
			r.EvidenceGaps = append(r.EvidenceGaps, a.Evidence.Gaps...)
			r.Steps = append(r.Steps, a.Evidence.AreaResults...)
		}
	}
	return r
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
