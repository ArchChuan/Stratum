// Package application provides agent orchestration. SQL/persistence lives
// in internal/agent/infrastructure/persistence behind port.AgentRepo.
package application

import (
	"context"
	"fmt"

	agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	versioningdomain "github.com/byteBuilderX/stratum/internal/versioning/domain"
	"go.uber.org/zap"
)

// Registry orchestrates Agent CRUD via a port-backed AgentRepo and
// hydrates returned Agents with capability/memory/recall hooks.
type Registry struct {
	repo           port.AgentRepo
	logger         *zap.Logger
	ledger         agentgraph.TokenRecorder
	memInjector    port.MemoryInjector
	recallFn       port.RecallMemoryFn
	platformPrompt port.PlatformPromptResolver
	taskStore      port.TaskRepo
}

// NewRegistry constructs a Registry around a domain-port AgentRepo.
func NewRegistry(repo port.AgentRepo, logger *zap.Logger) *Registry {
	return &Registry{repo: repo, logger: logger}
}

// SetMemoryInjector injects a MemoryInjector so agents created via Get/GetAll have it wired.
func (r *Registry) SetMemoryInjector(inj port.MemoryInjector) { r.memInjector = inj }

// SetRecallMemoryFn injects a recall_memory tool handler.
func (r *Registry) SetRecallMemoryFn(fn port.RecallMemoryFn) { r.recallFn = fn }

// SetPlatformPromptResolver injects the platform prompt resolver used to
// resolve agent.system_prompt（全局系统提示词）at execution time. 未配置即
// 执行 fail-closed；nil 仅在测试直构路径允许（回退 agent 字段）。
func (r *Registry) SetPlatformPromptResolver(resolver port.PlatformPromptResolver) {
	r.platformPrompt = resolver
}

// SetTaskStore injects the task persistence repo so agents hydrated via
// Get/GetAll can persist cross-session task snapshots (persistTaskSnapshot
// and the resume path both require it).
func (r *Registry) SetTaskStore(store port.TaskRepo) { r.taskStore = store }

// SetLedger injects the token/cost recorder so agents hydrated via Get/GetAll
// record LLM usage (production wires TokenLedger). nil leaves the Noop default.
func (r *Registry) SetLedger(l agentgraph.TokenRecorder) { r.ledger = l }

func (r *Registry) hydrate(cfg *domain.AgentConfig) (Agent, error) {
	a := NewBaseAgent(cfg, r.logger)
	if r.ledger != nil {
		a = a.WithLedger(r.ledger)
	}
	if r.memInjector != nil {
		a.MemoryInjector = r.memInjector
	}
	if r.recallFn != nil {
		a.RecallMemoryFn = r.recallFn
	}
	if r.taskStore != nil {
		a.TaskStore = r.taskStore
	}
	a.PlatformPromptResolver = r.platformPrompt
	return a, nil
}

// Register persists a new agent, auditing the create in the same transaction.
// editors are granted editor rights over the new resource (persisted in the
// same transaction, role-checked).
func (r *Registry) Register(ctx context.Context, a Agent, audit *auditdomain.ResourceChangeAuditEvent, editors []string) error {
	cfg := a.GetConfig()
	if err := r.repo.Register(ctx, cfg, audit, editors); err != nil {
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

// Remove deletes an agent, auditing the delete in the same transaction.
func (r *Registry) Remove(ctx context.Context, id string, audit *auditdomain.ResourceChangeAuditEvent) error {
	if err := r.repo.Remove(ctx, id, audit); err != nil {
		return err
	}
	if r.logger != nil {
		r.logger.Info("agent removed", zap.String("agent_id", id))
	}
	return nil
}

// Update replaces mutable fields on an existing agent, auditing the change in
// the same transaction. editorActor, when non-empty, re-validates editor
// eligibility inside the write transaction (see port.AgentRepo.Update).
// replaceParams selects sampling-parameter JSONB semantics: true = overall
// replace (promote), false = merge (old-client-safe). version, when non-nil,
// is written to resource_versions in the same transaction (通用产品版本基座).
func (r *Registry) Update(ctx context.Context, cfg *AgentConfig, audit *auditdomain.ResourceChangeAuditEvent, editorActor string, replaceParams bool, version *versioningdomain.Version) error {
	if err := r.repo.Update(ctx, cfg, audit, editorActor, replaceParams, version); err != nil {
		return err
	}
	if r.logger != nil {
		r.logger.Info("agent updated", zap.String("agent_id", cfg.ID))
	}
	return nil
}

// Rollback restores a deprecated product version, auditing the change in the
// same transaction. cfg must be rebuilt from the target version's snapshot
// payload (see AgentService.Rollback); the repo applies it to the agent row,
// promotes the target, and repoints active_version_id. editorActor re-validates
// editor eligibility inside the write transaction (same closure as Update).
func (r *Registry) Rollback(ctx context.Context, cfg *AgentConfig, audit *auditdomain.ResourceChangeAuditEvent, editorActor, targetVersionID string) error {
	if err := r.repo.Rollback(ctx, cfg, audit, editorActor, targetVersionID); err != nil {
		return err
	}
	if r.logger != nil {
		r.logger.Info("agent rolled back",
			zap.String("agent_id", cfg.ID), zap.String("version_id", targetVersionID))
	}
	return nil
}
