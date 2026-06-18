// Package application — agent_service.go.
//
// AgentService is the orchestration façade handlers consume for agent
// CRUD + execution. It aggregates Registry / TenantSettings / repos so
// HTTP handlers degrade to pure transport. SQL/HTTP/IO never appear in
// this file — every persistence call goes through a domain port.

package application

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// AgentServiceDeps groups the consumer-side dependencies of AgentService.
// Everything is an interface or value type — no concrete infrastructure
// imports allowed.
type AgentServiceDeps struct {
	Registry       *Registry
	TenantSettings port.TenantSettings
	SkillLookup    port.SkillLookup
	RAGSearch      port.RAGSearchProvider
	TenantResolver port.TenantCapabilityResolver
	MCPTools       port.MCPToolProvider
	ExecStore      ExecutionStore
	ChatStore      ChatStore
	Metrics        observability.MetricsProvider
	Logger         *zap.Logger
}

// AgentService aggregates agent CRUD + Execute/ExecuteStream and shields
// HTTP handlers from cross-context wiring. Construct via NewAgentService.
type AgentService struct {
	deps AgentServiceDeps
}

// NewAgentService wires an AgentService. Logger defaults to NopLogger
// when nil so callers can omit it in unit tests.
func NewAgentService(deps AgentServiceDeps) *AgentService {
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &AgentService{deps: deps}
}

// CreateAgentInput is the create-agent payload application receives from
// transport.
type CreateAgentInput struct {
	TenantID              string
	Name                  string
	Type                  string
	Description           string
	Persona               string
	SystemPrompt          string
	LLMModel              string
	EmbedModel            string
	MaxIterations         int
	MaxContextTokens      int
	AllowedSkills         []string
	MCPServerIDs          []string
	KnowledgeWorkspaceIDs []string
}

// UpdateAgentInput mirrors CreateAgentInput minus immutable EmbedModel.
type UpdateAgentInput struct {
	Name                  string
	Type                  string
	Description           string
	Persona               string
	SystemPrompt          string
	LLMModel              string
	MaxIterations         int
	MaxContextTokens      int
	AllowedSkills         []string
	MCPServerIDs          []string
	KnowledgeWorkspaceIDs []string
}

// AgentDTO is the wire shape returned by AgentService for transport
// rendering. Strings only — handler reuses field-for-field.
type AgentDTO struct {
	ID                    string
	Name                  string
	Type                  string
	Description           string
	Persona               string
	SystemPrompt          string
	LLMModel              string
	EmbedModel            string
	MaxIterations         int
	MaxContextTokens      int
	AllowedSkills         []string
	MCPServerIDs          []string
	KnowledgeWorkspaceIDs []string
	CreatedAt             string
}

// Create persists a new agent for the tenant. Inherits embed_model from
// tenant defaults when caller omits it.
func (s *AgentService) Create(ctx context.Context, in CreateAgentInput) (AgentDTO, error) {
	embedModel := in.EmbedModel
	if embedModel == "" && s.deps.TenantSettings != nil {
		inherited, err := s.deps.TenantSettings.GetEmbedModel(ctx, in.TenantID)
		if err != nil {
			return AgentDTO{}, fmt.Errorf("agent service: get embed_model: %w", err)
		}
		embedModel = inherited
	}

	id := uuid.New().String()
	cfg := &domain.AgentConfig{
		ID:                    id,
		Name:                  in.Name,
		Type:                  parseAgentTypeWire(in.Type),
		Description:           in.Description,
		Persona:               in.Persona,
		SystemPrompt:          in.SystemPrompt,
		LLMModel:              in.LLMModel,
		EmbedModel:            embedModel,
		MaxIterations:         in.MaxIterations,
		MaxContextTokens:      in.MaxContextTokens,
		AllowedSkills:         in.AllowedSkills,
		MCPServerIDs:          in.MCPServerIDs,
		KnowledgeWorkspaceIDs: in.KnowledgeWorkspaceIDs,
		Capabilities:          []domain.AgentCapability{},
	}

	a := NewBaseAgent(cfg, s.deps.Logger)
	if s.deps.Metrics != nil {
		a = a.WithMetrics(s.deps.Metrics)
	}
	if err := s.deps.Registry.Register(ctx, a); err != nil {
		return AgentDTO{}, err
	}
	s.deps.Logger.Info("agent created", zap.String("id", id), zap.String("name", in.Name))
	return cfgToDTO(cfg), nil
}

// Get returns the agent's DTO or ErrNotFound.
func (s *AgentService) Get(ctx context.Context, id string) (AgentDTO, error) {
	a, ok := s.deps.Registry.Get(ctx, id)
	if !ok {
		return AgentDTO{}, ErrNotFound
	}
	return cfgToDTO(a.GetConfig()), nil
}

// List returns all agents in the tenant schema.
func (s *AgentService) List(ctx context.Context) ([]AgentDTO, error) {
	agents := s.deps.Registry.GetAll(ctx)
	out := make([]AgentDTO, 0, len(agents))
	for _, a := range agents {
		out = append(out, cfgToDTO(a.GetConfig()))
	}
	return out, nil
}

// Update replaces mutable fields on an existing agent. EmbedModel is
// immutable post-create — callers cannot change it through Update.
func (s *AgentService) Update(ctx context.Context, id string, in UpdateAgentInput) (AgentDTO, error) {
	existing, ok := s.deps.Registry.Get(ctx, id)
	if !ok {
		return AgentDTO{}, ErrNotFound
	}
	skills := in.AllowedSkills
	if skills == nil {
		skills = []string{}
	}
	cfg := &domain.AgentConfig{
		ID:                    id,
		Name:                  in.Name,
		Type:                  parseAgentTypeWire(in.Type),
		Description:           in.Description,
		Persona:               in.Persona,
		SystemPrompt:          in.SystemPrompt,
		LLMModel:              in.LLMModel,
		EmbedModel:            existing.GetConfig().EmbedModel,
		MaxIterations:         in.MaxIterations,
		MaxContextTokens:      in.MaxContextTokens,
		AllowedSkills:         skills,
		MCPServerIDs:          in.MCPServerIDs,
		KnowledgeWorkspaceIDs: in.KnowledgeWorkspaceIDs,
	}
	if err := s.deps.Registry.Update(ctx, cfg); err != nil {
		return AgentDTO{}, err
	}
	s.deps.Logger.Info("agent updated", zap.String("id", id), zap.String("name", in.Name))
	return cfgToDTO(cfg), nil
}

// Delete removes an agent.
func (s *AgentService) Delete(ctx context.Context, id string) error {
	if err := s.deps.Registry.Remove(ctx, id); err != nil {
		return err
	}
	s.deps.Logger.Info("agent deleted", zap.String("id", id))
	return nil
}

// parseAgentTypeWire maps the wire-format agent type to the domain enum,
// defaulting to ReActAgent.
func parseAgentTypeWire(t string) domain.AgentType {
	switch t {
	case "react":
		return domain.ReActAgent
	case "cot":
		return domain.CoTAgent
	case "planning":
		return domain.PlanningAgent
	case "tool_calling":
		return domain.ToolCallingAgent
	case "rag":
		return domain.RAGAgent
	case "swarm":
		return domain.SwarmAgent
	default:
		return domain.ReActAgent
	}
}

func cfgToDTO(cfg *domain.AgentConfig) AgentDTO {
	return AgentDTO{
		ID:                    cfg.ID,
		Name:                  cfg.Name,
		Type:                  string(cfg.Type),
		Description:           cfg.Description,
		Persona:               cfg.Persona,
		SystemPrompt:          cfg.SystemPrompt,
		LLMModel:              cfg.LLMModel,
		EmbedModel:            cfg.EmbedModel,
		MaxIterations:         cfg.MaxIterations,
		MaxContextTokens:      cfg.MaxContextTokens,
		AllowedSkills:         cfg.AllowedSkills,
		MCPServerIDs:          cfg.MCPServerIDs,
		KnowledgeWorkspaceIDs: cfg.KnowledgeWorkspaceIDs,
		CreatedAt:             time.Now().Format(time.RFC3339),
	}
}
