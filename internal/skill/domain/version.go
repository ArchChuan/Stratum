package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type VersionStatus string

const (
	VersionStatusDraft      VersionStatus = "draft"
	VersionStatusCandidate  VersionStatus = "candidate"
	VersionStatusPublished  VersionStatus = "published"
	VersionStatusDeprecated VersionStatus = "deprecated"
)

type Capability struct {
	Goal       string              `json:"goal"`
	WhenToUse  string              `json:"whenToUse"`
	InputSpec  string              `json:"inputSpec,omitempty"`
	OutputSpec string              `json:"outputSpec,omitempty"`
	Examples   []CapabilityExample `json:"examples,omitempty"`
}

type CapabilityExample struct {
	Input          any `json:"input"`
	ExpectedOutput any `json:"expectedOutput"`
}

type ActivationContract struct {
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema"`
	Confirmed    bool           `json:"confirmed"`
}

type Requirements struct {
	MCPToolIDs            []string `json:"mcpToolIds,omitempty"`
	KnowledgeWorkspaceIDs []string `json:"knowledgeWorkspaceIds,omitempty"`
	MemoryScopes          []string `json:"memoryScopes,omitempty"`
}

type SkillRevision struct {
	ID                 string
	SkillID            string
	ParentRevisionID   string
	RevisionNo         int
	Status             VersionStatus
	Source             string
	ContentHash        string
	GenerationMetadata map[string]any
	Capability         Capability
	ActivationContract ActivationContract
	Instructions       string
	Requirements       Requirements
	PublishChecks      map[string]any
}

func (v SkillRevision) ComputeContentHash() (string, error) {
	payload := struct {
		Capability         Capability         `json:"capability"`
		ActivationContract ActivationContract `json:"activation_contract"`
		Instructions       string             `json:"instructions"`
		Requirements       Requirements       `json:"requirements"`
	}{
		Capability:         v.Capability,
		ActivationContract: v.ActivationContract,
		Instructions:       v.Instructions,
		Requirements:       v.Requirements,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal skill version content: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

var activationNamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,63}$`)
var mcpToolIDPattern = regexp.MustCompile(`^mcp:[^:[:space:]]+:[^:[:space:]]+$`)

func (c ActivationContract) Validate() error {
	if !activationNamePattern.MatchString(c.Name) {
		return fmt.Errorf("invalid activation name: %s: %w", c.Name, ErrSkillNotPublishable)
	}
	if strings.TrimSpace(c.Description) == "" {
		return fmt.Errorf("activation description required: %w", ErrSkillNotPublishable)
	}
	if !isObjectSchema(c.InputSchema) {
		return fmt.Errorf("input schema must be object schema: %w", ErrSkillNotPublishable)
	}
	if !isObjectSchema(c.OutputSchema) {
		return fmt.Errorf("output schema must be object schema: %w", ErrSkillNotPublishable)
	}
	if !c.Confirmed {
		return fmt.Errorf("activation contract not confirmed: %w", ErrSkillNotPublishable)
	}
	return nil
}

func (r Requirements) Validate() error {
	seen := make(map[string]struct{}, len(r.MCPToolIDs))
	for _, toolID := range r.MCPToolIDs {
		if !mcpToolIDPattern.MatchString(toolID) {
			return fmt.Errorf("invalid MCP tool ID %q: %w", toolID, ErrSkillNotPublishable)
		}
		if _, ok := seen[toolID]; ok {
			return fmt.Errorf("duplicate MCP tool ID %q: %w", toolID, ErrSkillNotPublishable)
		}
		seen[toolID] = struct{}{}
	}
	for _, workspaceID := range r.KnowledgeWorkspaceIDs {
		if strings.TrimSpace(workspaceID) == "" {
			return fmt.Errorf("empty knowledge workspace ID: %w", ErrSkillNotPublishable)
		}
	}
	for _, scope := range r.MemoryScopes {
		switch scope {
		case "conversation", "user", "agent":
		default:
			return fmt.Errorf("invalid memory scope %q: %w", scope, ErrSkillNotPublishable)
		}
	}
	return nil
}

func (v SkillRevision) ValidatePublishable(enabledTestCount int) error {
	if strings.TrimSpace(v.Capability.Goal) == "" {
		return fmt.Errorf("capability goal required: %w", ErrSkillNotPublishable)
	}
	if strings.TrimSpace(v.Capability.WhenToUse) == "" {
		return fmt.Errorf("capability whenToUse required: %w", ErrSkillNotPublishable)
	}
	if len(v.Capability.Examples) == 0 && enabledTestCount == 0 {
		return fmt.Errorf("at least one example or enabled test case required: %w", ErrSkillNotPublishable)
	}
	if err := v.ActivationContract.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(v.Instructions) == "" {
		return fmt.Errorf("instructions required: %w", ErrSkillNotPublishable)
	}
	if err := v.Requirements.Validate(); err != nil {
		return err
	}
	return nil
}

func isObjectSchema(schema map[string]any) bool {
	if schema == nil {
		return false
	}
	typ, _ := schema["type"].(string)
	return typ == "object"
}
