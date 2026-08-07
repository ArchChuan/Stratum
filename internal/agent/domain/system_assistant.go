package domain

import (
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

const (
	SystemAssistantKey                   = "stratum.platform_assistant"
	SystemAssistantID                    = "stratum-platform-assistant"
	CurrentSystemAssistantProfileVersion = "2026-08-08.v3"
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
	Area   DiagnosticArea `json:"area,omitempty"`
	Source string         `json:"source,omitempty"`
	Code   string         `json:"code"`
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
	Tool        string                              `json:"tool"`
	Citations   []Citation                          `json:"citations,omitempty"`
	Evidence    *DiagnosticEvidence                 `json:"evidence,omitempty"`
	LatencyMs   int64                               `json:"latencyMs"`
	Outcome     string                              `json:"outcome"`
	ErrorCode   string                              `json:"errorCode,omitempty"`
	Proposal    *ResourceChangeProposalArtifact     `json:"proposal,omitempty"`
	DirectApply *SystemAssistantDirectApplyArtifact `json:"directApply,omitempty"`
}

type ResourceChangeProposalArtifact struct {
	ID           string            `json:"id"`
	ResourceKind ResourceKind      `json:"resourceKind"`
	Operation    ProposalOperation `json:"operation"`
	Status       ProposalStatus    `json:"status"`
	Summary      string            `json:"summary"`
	ExpiresAt    time.Time         `json:"expiresAt"`
}

// SystemAssistantDirectApplyArtifact is typed evidence captured from the
// in-process direct-write tool (stratum_apply_resource_change). Unlike the
// proposal artifact there is no proposal lifecycle: the change either landed
// in the same transaction as the audit row or the tool reports a typed error.
type SystemAssistantDirectApplyArtifact struct {
	Tool         string            `json:"tool"`
	ResourceKind ResourceKind      `json:"resourceKind"`
	Operation    ProposalOperation `json:"operation"`
	ResourceID   string            `json:"resourceId,omitempty"`
	Outcome      string            `json:"outcome"`
	ErrorCode    string            `json:"errorCode,omitempty"`
}

type DiagnosticReport struct {
	Facts              []DiagnosticFact `json:"facts"`
	Inferences         []string         `json:"inferences"`
	EvidenceGaps       []EvidenceGap    `json:"evidenceGaps"`
	RecommendedActions []string         `json:"recommendedActions"`
	Citations          []Citation       `json:"citations"`
	Steps              []DiagnosticStep `json:"steps"`
}

type DiagnosticStep struct {
	Tool      string `json:"tool"`
	Outcome   string `json:"outcome"`
	ErrorCode string `json:"errorCode,omitempty"`
	LatencyMs int64  `json:"latencyMs"`
}

type ExecutionArtifact struct {
	Type                   string                              `json:"type"`
	ProfileVersion         string                              `json:"profileVersion,omitempty"`
	Citations              []Citation                          `json:"citations,omitempty"`
	DiagnosticReport       *DiagnosticReport                   `json:"diagnosticReport,omitempty"`
	ResourceChangeProposal *ResourceChangeProposalArtifact     `json:"resourceChangeProposal,omitempty"`
	DirectApply            *SystemAssistantDirectApplyArtifact `json:"directApply,omitempty"`
}

var diagnosticRecommendedActions = map[string]string{
	DiagnosticGapUnavailable: "Check the Opik backend, OTEL Collector export path, and dependency health.",
	DiagnosticGapTimeout:     "Check Opik backend and OTEL Collector latency, then retry the diagnostic request.",
	DiagnosticGapCancelled:   "Retry the diagnostic request and verify the Opik backend and OTEL Collector are reachable.",
}

func BuildDiagnosticReport(toolArtifacts []SystemAssistantToolArtifact) *DiagnosticReport {
	r := &DiagnosticReport{Facts: []DiagnosticFact{}, Inferences: []string{}, EvidenceGaps: []EvidenceGap{}, RecommendedActions: []string{}, Citations: []Citation{}, Steps: []DiagnosticStep{}}
	for _, a := range toolArtifacts {
		if len(r.Steps) < constants.SystemAssistantDiagnosticAreaResultsMaxCount {
			r.Steps = append(r.Steps, DiagnosticStep{Tool: boundEvidenceString(a.Tool), Outcome: boundEvidenceString(a.Outcome), ErrorCode: boundEvidenceString(a.ErrorCode), LatencyMs: a.LatencyMs})
		}
		if a.Tool == "stratum_diagnose_tenant" {
			r.Citations = appendUniqueCitations(r.Citations, BoundCitations(a.Citations)...)
		}
		if a.Evidence != nil {
			bounded := BoundDiagnosticEvidence(*a.Evidence)
			r.Facts = appendUniqueFacts(r.Facts, bounded.Facts...)
			r.EvidenceGaps = appendUniqueGaps(r.EvidenceGaps, bounded.Gaps...)
		}
		if a.ErrorCode != "" && a.Evidence == nil {
			r.EvidenceGaps = append(r.EvidenceGaps, EvidenceGap{Source: a.Tool, Code: a.ErrorCode})
		}
	}
	r.RecommendedActions = recommendedActionsForGaps(r.EvidenceGaps)
	return r
}

func recommendedActionsForGaps(gaps []EvidenceGap) []string {
	actions := make([]string, 0, len(gaps))
	for _, gap := range gaps {
		action, ok := diagnosticRecommendedActions[gap.Code]
		if !ok {
			continue
		}
		action = boundEvidenceString(action)
		duplicate := false
		for _, existing := range actions {
			if existing == action {
				duplicate = true
				break
			}
		}
		if !duplicate && len(actions) < constants.SystemAssistantDiagnosticGapsMaxCount {
			actions = append(actions, action)
		}
	}
	return actions
}

func appendUniqueCitations(dst []Citation, values ...Citation) []Citation {
	for _, v := range values {
		found := false
		for _, old := range dst {
			if old.DocumentID == v.DocumentID && old.Section == v.Section && old.URL == v.URL {
				found = true
				break
			}
		}
		if !found && len(dst) < constants.SystemAssistantCitationMaxCount {
			dst = append(dst, v)
		}
	}
	return dst
}
func appendUniqueFacts(dst []DiagnosticFact, values ...DiagnosticFact) []DiagnosticFact {
	for _, v := range values {
		found := false
		for _, old := range dst {
			if old.Area == v.Area && old.ObjectID == v.ObjectID && old.Statement == v.Statement && old.Source == v.Source {
				found = true
				break
			}
		}
		if !found && len(dst) < constants.SystemAssistantDiagnosticFactsMaxCount {
			dst = append(dst, v)
		}
	}
	return dst
}
func appendUniqueGaps(dst []EvidenceGap, values ...EvidenceGap) []EvidenceGap {
	for _, v := range values {
		v.Source = boundEvidenceString(v.Source)
		found := false
		for _, old := range dst {
			if old.Area == v.Area && old.Source == v.Source && old.Code == v.Code {
				found = true
				break
			}
		}
		if !found && len(dst) < constants.SystemAssistantDiagnosticGapsMaxCount {
			dst = append(dst, v)
		}
	}
	return dst
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
