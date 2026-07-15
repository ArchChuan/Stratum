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

type ToolContract struct {
	ToolName        string         `json:"toolName"`
	Description     string         `json:"description"`
	InputSchema     map[string]any `json:"inputSchema"`
	OutputSchema    map[string]any `json:"outputSchema"`
	CallingGuidance string         `json:"callingGuidance,omitempty"`
	Confirmed       bool           `json:"confirmed"`
}

type Implementation struct {
	Mode        string         `json:"mode"`
	Source      map[string]any `json:"source"`
	Runtime     map[string]any `json:"runtime,omitempty"`
	Permissions map[string]any `json:"permissions,omitempty"`
	SecretRefs  []string       `json:"secretRefs,omitempty"`
}

type SkillVersion struct {
	ID                 string
	SkillID            string
	ParentVersionID    string
	VersionNo          int
	Status             VersionStatus
	Source             string
	ContentHash        string
	GenerationMetadata map[string]any
	Capability         Capability
	ToolContract       ToolContract
	Implementation     Implementation
	TestBaseline       map[string]any
	PublishChecks      map[string]any
}

func (v SkillVersion) ComputeContentHash() (string, error) {
	payload := struct {
		Capability     Capability     `json:"capability"`
		ToolContract   ToolContract   `json:"tool_contract"`
		Implementation Implementation `json:"implementation"`
	}{
		Capability:     v.Capability,
		ToolContract:   v.ToolContract,
		Implementation: v.Implementation,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal skill version content: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

var toolNamePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{0,63}$`)

func (c ToolContract) Validate() error {
	if !toolNamePattern.MatchString(c.ToolName) {
		return fmt.Errorf("invalid tool name: %s", c.ToolName)
	}
	if strings.TrimSpace(c.Description) == "" {
		return fmt.Errorf("tool description required")
	}
	if !isObjectSchema(c.InputSchema) {
		return fmt.Errorf("input schema must be object schema")
	}
	if !isObjectSchema(c.OutputSchema) {
		return fmt.Errorf("output schema must be object schema")
	}
	if !c.Confirmed {
		return fmt.Errorf("tool contract not confirmed")
	}
	return nil
}

func (v SkillVersion) ValidatePublishable(enabledTestCount int) error {
	if strings.TrimSpace(v.Capability.Goal) == "" {
		return fmt.Errorf("capability goal required")
	}
	if strings.TrimSpace(v.Capability.WhenToUse) == "" {
		return fmt.Errorf("capability whenToUse required")
	}
	if len(v.Capability.Examples) == 0 && enabledTestCount == 0 {
		return fmt.Errorf("at least one example or enabled test case required")
	}
	if err := v.ToolContract.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(v.Implementation.Mode) == "" {
		return fmt.Errorf("implementation mode required")
	}
	if v.Implementation.Source == nil {
		return fmt.Errorf("implementation source required")
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
