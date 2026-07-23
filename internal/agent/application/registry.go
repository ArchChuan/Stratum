// Package application provides agent orchestration. SQL/persistence lives
// in internal/agent/infrastructure/persistence behind port.AgentRepo.
package application

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

// Registry orchestrates Agent CRUD via a port-backed AgentRepo and
// hydrates returned Agents with capability/memory/recall hooks.
type Registry struct {
	repo               port.AgentRepo
	logger             *zap.Logger
	memInjector        port.MemoryInjector
	recallFn           port.RecallMemoryFn
	globalSystemSuffix string
	systemProfile      *domain.SystemAssistantProfile
}

// NewRegistry constructs a Registry around a domain-port AgentRepo.
func NewRegistry(
	repo port.AgentRepo, systemProfile *domain.SystemAssistantProfile, logger *zap.Logger,
) *Registry {
	return &Registry{repo: repo, systemProfile: systemProfile, logger: logger}
}

// SetMemoryInjector injects a MemoryInjector so agents created via Get/GetAll have it wired.
func (r *Registry) SetMemoryInjector(inj port.MemoryInjector) { r.memInjector = inj }

// SetRecallMemoryFn injects a recall_memory tool handler.
func (r *Registry) SetRecallMemoryFn(fn port.RecallMemoryFn) { r.recallFn = fn }

// SetGlobalSystemSuffix injects a platform-level system prompt appended to every agent's prompt.
func (r *Registry) SetGlobalSystemSuffix(s string) { r.globalSystemSuffix = s }

func (r *Registry) hydrate(cfg *domain.AgentConfig) (Agent, error) {
	composed, err := ComposeSystemAssistantProfile(cfg, r.systemProfile)
	if err != nil {
		return nil, fmt.Errorf("registry hydrate agent: %w", err)
	}
	a := NewBaseAgent(composed, r.logger)
	if r.memInjector != nil {
		a.MemoryInjector = r.memInjector
	}
	if r.recallFn != nil {
		a.RecallMemoryFn = r.recallFn
	}
	if r.globalSystemSuffix != "" {
		a.GlobalSystemSuffix = r.globalSystemSuffix
	}
	return a, nil
}

// Register persists a new agent.
func (r *Registry) Register(ctx context.Context, a Agent) error {
	cfg := a.GetConfig()
	if err := r.repo.Register(ctx, cfg); err != nil {
		return err
	}
	if r.logger != nil {
		r.logger.Info("agent registered", zap.String("agent_id", cfg.ID))
	}
	return nil
}

// Get retrieves a hydrated Agent by ID while preserving repository and
// composition failures. A miss is the only case returning found=false.
func (r *Registry) Get(ctx context.Context, id string) (Agent, bool, error) {
	cfg, found, err := r.repo.Get(ctx, id)
	if err != nil {
		return nil, false, fmt.Errorf("registry get agent %s: %w", id, err)
	}
	if !found {
		return nil, false, nil
	}
	agent, err := r.hydrate(cfg)
	if err != nil {
		return nil, false, fmt.Errorf("registry get agent %s: %w", id, err)
	}
	return agent, true, nil
}

// GetAll returns all hydrated agents in the tenant schema.
func (r *Registry) GetAll(ctx context.Context) ([]Agent, error) {
	cfgs, err := r.repo.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("registry list agents: %w", err)
	}
	out := make([]Agent, 0, len(cfgs))
	for _, c := range cfgs {
		agent, err := r.hydrate(c)
		if err != nil {
			return nil, fmt.Errorf("registry list agents: %w", err)
		}
		out = append(out, agent)
	}
	return out, nil
}

// Remove deletes an agent.
func (r *Registry) Remove(ctx context.Context, id string) error {
	if err := r.repo.Remove(ctx, id); err != nil {
		return err
	}
	if r.logger != nil {
		r.logger.Info("agent removed", zap.String("agent_id", id))
	}
	return nil
}

// Update replaces mutable fields on an existing agent.
func (r *Registry) Update(ctx context.Context, cfg *AgentConfig) error {
	if err := r.repo.Update(ctx, cfg); err != nil {
		return err
	}
	if r.logger != nil {
		r.logger.Info("agent updated", zap.String("agent_id", cfg.ID))
	}
	return nil
}
