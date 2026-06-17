// Package application provides agent orchestration. SQL/persistence lives
// in internal/agent/infrastructure/persistence behind port.AgentRepo.
package application

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

// Registry orchestrates Agent CRUD via a port-backed AgentRepo and
// hydrates returned Agents with capability/memory/recall hooks.
type Registry struct {
	repo        port.AgentRepo
	logger      *zap.Logger
	capGW       port.CapabilityGateway
	memInjector port.MemoryInjector
	recallFn    port.RecallMemoryFn
}

// NewRegistry constructs a Registry around a domain-port AgentRepo.
func NewRegistry(repo port.AgentRepo, logger *zap.Logger) *Registry {
	return &Registry{repo: repo, logger: logger}
}

// SetCapGateway injects a CapabilityGateway so agents created via Get/GetAll have it wired.
func (r *Registry) SetCapGateway(gw port.CapabilityGateway) { r.capGW = gw }

// SetMemoryInjector injects a MemoryInjector so agents created via Get/GetAll have it wired.
func (r *Registry) SetMemoryInjector(inj port.MemoryInjector) { r.memInjector = inj }

// SetRecallMemoryFn injects a recall_memory tool handler.
func (r *Registry) SetRecallMemoryFn(fn port.RecallMemoryFn) { r.recallFn = fn }

func (r *Registry) hydrate(cfg *domain.AgentConfig) Agent {
	a := NewBaseAgent(cfg, r.logger)
	if r.capGW != nil {
		a.SetCapGateway(r.capGW)
	}
	if r.memInjector != nil {
		a.MemoryInjector = r.memInjector
	}
	if r.recallFn != nil {
		a.RecallMemoryFn = r.recallFn
	}
	return a
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

// Get retrieves a hydrated Agent by ID. Returns (nil, false) on miss.
func (r *Registry) Get(ctx context.Context, id string) (Agent, bool) {
	cfg, found, err := r.repo.Get(ctx, id)
	if err != nil {
		if r.logger != nil {
			r.logger.Error("registry: get agent failed",
				zap.String("agent_id", id), zap.Error(err))
		}
		return nil, false
	}
	if !found {
		return nil, false
	}
	return r.hydrate(cfg), true
}

// GetAll returns all hydrated agents in the tenant schema.
func (r *Registry) GetAll(ctx context.Context) []Agent {
	cfgs, err := r.repo.GetAll(ctx)
	if err != nil {
		if r.logger != nil {
			r.logger.Error("registry: list agents failed", zap.Error(err))
		}
		return nil
	}
	out := make([]Agent, 0, len(cfgs))
	for _, c := range cfgs {
		out = append(out, r.hydrate(c))
	}
	return out
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
