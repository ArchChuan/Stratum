package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type AgentBindingKind string

const (
	AgentBindingSkill     AgentBindingKind = "skill"
	AgentBindingMCP       AgentBindingKind = "mcp"
	AgentBindingKnowledge AgentBindingKind = "knowledge"
)

type ModelParameters struct {
	MaxContextTokens int `json:"max_context_tokens,omitempty"`
}

type AgentBinding struct {
	Kind    AgentBindingKind `json:"kind"`
	ID      string           `json:"id"`
	Enabled bool             `json:"enabled"`
}

type AgentRevision struct {
	AgentID                        string          `json:"agent_id"`
	Type                           AgentType       `json:"type"`
	SystemPrompt                   string          `json:"system_prompt"`
	Model                          string          `json:"model"`
	EmbedModel                     string          `json:"embed_model,omitempty"`
	ModelParameters                ModelParameters `json:"model_parameters,omitempty"`
	MaxIterations                  int             `json:"max_iterations"`
	Bindings                       []AgentBinding  `json:"bindings"`
	MemoryScope                    string          `json:"memory_scope,omitempty"`
	CheckpointEnabled              bool            `json:"checkpoint_enabled,omitempty"`
	StuckThreshold                 int             `json:"stuck_threshold,omitempty"`
	GlobalSystemSuffix             string          `json:"global_system_suffix,omitempty"`
	KnowledgeWorkspaceNames        []string        `json:"knowledge_workspace_names,omitempty"`
	KnowledgeWorkspaceDescriptions []string        `json:"knowledge_workspace_descriptions,omitempty"`
	MemoryInjectorRequired         bool            `json:"memory_injector_required,omitempty"`
	RecallMemoryRequired           bool            `json:"recall_memory_required,omitempty"`
}

type AgentCandidatePatch struct {
	SystemPrompt    string
	Model           string
	ModelParameters *ModelParameters
	MaxIterations   int
	Bindings        []AgentBinding
	Permissions     []string
}

func (r AgentRevision) Validate() error {
	if strings.TrimSpace(r.AgentID) == "" {
		return errors.New("agent revision: agent id required")
	}
	if strings.TrimSpace(r.SystemPrompt) == "" {
		return errors.New("agent revision: system prompt required")
	}
	if strings.TrimSpace(r.Model) == "" {
		return errors.New("agent revision: model required")
	}
	switch r.Type {
	case ReActAgent, CoTAgent, PlanningAgent, ToolCallingAgent, RAGAgent, SwarmAgent:
	case "":
		return errors.New("agent revision: agent type required")
	default:
		return fmt.Errorf("agent revision: unsupported agent type %q", r.Type)
	}
	if r.MaxIterations < 1 || r.MaxIterations > 20 {
		return errors.New("agent revision: max iterations must be between 1 and 20")
	}
	seen := make(map[string]struct{}, len(r.Bindings))
	for _, binding := range r.Bindings {
		if err := binding.validate(); err != nil {
			return err
		}
		key := bindingKey(binding.Kind, binding.ID)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("agent revision: duplicate binding %s", key)
		}
		seen[key] = struct{}{}
	}
	return validateModelParameters(r.ModelParameters)
}

func (r AgentRevision) ContentHash() (string, error) {
	canonical := r.canonical()
	if err := canonical.Validate(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("agent revision: marshal canonical snapshot: %w", err)
	}
	hash := sha256.Sum256(payload)
	return hex.EncodeToString(hash[:]), nil
}

func (r AgentRevision) ApplyCandidate(patch AgentCandidatePatch) (AgentRevision, error) {
	if len(patch.Permissions) != 0 {
		return AgentRevision{}, errors.New("agent revision: candidate cannot change permissions")
	}
	candidate := r.canonical()
	if patch.SystemPrompt != "" {
		candidate.SystemPrompt = patch.SystemPrompt
	}
	if patch.Model != "" {
		candidate.Model = patch.Model
	}
	if patch.ModelParameters != nil {
		candidate.ModelParameters = *patch.ModelParameters
	}
	if patch.MaxIterations != 0 {
		candidate.MaxIterations = patch.MaxIterations
	}
	byKey := make(map[string]int, len(candidate.Bindings))
	for i, binding := range candidate.Bindings {
		byKey[bindingKey(binding.Kind, binding.ID)] = i
	}
	for _, binding := range patch.Bindings {
		key := bindingKey(binding.Kind, binding.ID)
		index, ok := byKey[key]
		if !ok {
			return AgentRevision{}, fmt.Errorf("agent revision: candidate binding is not authorized: %s", key)
		}
		candidate.Bindings[index].Enabled = binding.Enabled
	}
	if err := candidate.Validate(); err != nil {
		return AgentRevision{}, err
	}
	baselineHash, err := r.ContentHash()
	if err != nil {
		return AgentRevision{}, err
	}
	candidateHash, err := candidate.ContentHash()
	if err != nil {
		return AgentRevision{}, err
	}
	if baselineHash == candidateHash {
		return AgentRevision{}, errors.New("agent revision: candidate must change the baseline")
	}
	return candidate.canonical(), nil
}

func (r AgentRevision) SafeSummary() map[string]any {
	enabled := map[string]int{"skill": 0, "mcp": 0, "knowledge": 0}
	for _, binding := range r.Bindings {
		if binding.Enabled {
			enabled[string(binding.Kind)]++
		}
	}
	return map[string]any{
		"model": r.Model, "max_iterations": r.MaxIterations, "enabled_bindings": enabled,
		"prompt_present": strings.TrimSpace(r.SystemPrompt) != "",
	}
}

func (r AgentRevision) canonical() AgentRevision {
	r.Bindings = append([]AgentBinding(nil), r.Bindings...)
	sort.Slice(r.Bindings, func(i, j int) bool {
		left := bindingKey(r.Bindings[i].Kind, r.Bindings[i].ID)
		right := bindingKey(r.Bindings[j].Kind, r.Bindings[j].ID)
		return left < right
	})
	return r
}

func (b AgentBinding) validate() error {
	switch b.Kind {
	case AgentBindingSkill, AgentBindingMCP, AgentBindingKnowledge:
	default:
		return fmt.Errorf("agent revision: unsupported binding kind %q", b.Kind)
	}
	if strings.TrimSpace(b.ID) == "" {
		return errors.New("agent revision: binding id required")
	}
	return nil
}

func bindingKey(kind AgentBindingKind, id string) string {
	return string(kind) + ":" + strings.TrimSpace(id)
}

func validateModelParameters(params ModelParameters) error {
	if params.MaxContextTokens < 0 {
		return errors.New("agent revision: token limits cannot be negative")
	}
	return nil
}
