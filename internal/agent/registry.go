// Package agent provides the core agent system.
package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

// poolIface allows pgxmock injection in tests.
type poolIface interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

var _ poolIface = (*pgxpool.Pool)(nil) // compile-time check

// Registry persists Agent configs in PostgreSQL under per-tenant schemas.
type Registry struct {
	pool   poolIface
	logger *zap.Logger
	capGW  capgateway.CapabilityGateway
}

// NewRegistry creates a Registry. pool must not be nil.
func NewRegistry(pool *pgxpool.Pool, logger *zap.Logger) *Registry {
	return &Registry{pool: pool, logger: logger}
}

// SetCapGateway injects a CapabilityGateway so agents created via Get/GetAll have it wired.
func (r *Registry) SetCapGateway(gw capgateway.CapabilityGateway) {
	r.capGW = gw
}

// execTenant runs fn in a transaction with search_path set to the tenant schema from ctx.
func (r *Registry) execTenant(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tc, ok := tenantdb.FromContext(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("registry: missing tenant context")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("registry: begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()
	schema := "tenant_" + tc.TenantID
	if _, err := tx.Exec(ctx, fmt.Sprintf(`SET LOCAL search_path = "%s", public`, schema)); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("registry: set search_path: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

// Register writes the agent config into the tenant schema agents table.
func (r *Registry) Register(ctx context.Context, a Agent) error {
	cfg := a.GetConfig()
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		skills := cfg.AllowedSkills
		if skills == nil {
			skills = []string{}
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO agents (id, name, type, description, persona, system_prompt, llm_model, max_iterations, allowed_skills)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			cfg.ID, cfg.Name, string(cfg.Type), cfg.Description,
			cfg.Persona, cfg.SystemPrompt, cfg.LLMModel, cfg.MaxIterations,
			skills,
		)
		if err != nil {
			return fmt.Errorf("register agent %s: %w", cfg.ID, err)
		}
		r.logger.Info("agent registered", zap.String("agent_id", cfg.ID))
		return nil
	})
}

// Get retrieves an agent by ID from the tenant schema.
func (r *Registry) Get(ctx context.Context, id string) (Agent, bool) {
	var cfg AgentConfig
	var agentType string
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id, name, type, description, persona, system_prompt, llm_model, max_iterations, allowed_skills
			 FROM agents WHERE id = $1`, id).
			Scan(&cfg.ID, &cfg.Name, &agentType, &cfg.Description,
				&cfg.Persona, &cfg.SystemPrompt, &cfg.LLMModel, &cfg.MaxIterations, &cfg.AllowedSkills)
	})
	if err != nil {
		return nil, false
	}
	if cfg.AllowedSkills == nil {
		cfg.AllowedSkills = []string{}
	}
	cfg.Type = AgentType(agentType)
	a := NewBaseAgent(&cfg, r.logger)
	if r.capGW != nil {
		a.SetCapGateway(r.capGW)
	}
	return a, true
}

// GetAll returns all agents in the tenant schema.
func (r *Registry) GetAll(ctx context.Context) []Agent {
	var agents []Agent
	_ = r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, name, type, description, persona, system_prompt, llm_model, max_iterations, allowed_skills
			 FROM agents ORDER BY created_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var cfg AgentConfig
			var agentType string
			if err := rows.Scan(&cfg.ID, &cfg.Name, &agentType, &cfg.Description,
				&cfg.Persona, &cfg.SystemPrompt, &cfg.LLMModel, &cfg.MaxIterations, &cfg.AllowedSkills); err != nil {
				continue
			}
			if cfg.AllowedSkills == nil {
				cfg.AllowedSkills = []string{}
			}
			cfg.Type = AgentType(agentType)
			a := NewBaseAgent(&cfg, r.logger)
			if r.capGW != nil {
				a.SetCapGateway(r.capGW)
			}
			agents = append(agents, a)
		}
		return rows.Err()
	})
	return agents
}

// Remove deletes an agent from the tenant schema.
func (r *Registry) Remove(ctx context.Context, id string) error {
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM agents WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("agent with ID %s not found", id)
		}
		r.logger.Info("agent removed", zap.String("agent_id", id))
		return nil
	})
}

// ErrNotFound is returned by Update when no agent with the given ID exists.
var ErrNotFound = errors.New("agent not found")

// Update replaces an agent's mutable fields in the tenant schema.
func (r *Registry) Update(ctx context.Context, cfg *AgentConfig) error {
	skills := cfg.AllowedSkills
	if skills == nil {
		skills = []string{}
	}
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE agents
			 SET name=$1, description=$2, persona=$3, system_prompt=$4,
			     llm_model=$5, max_iterations=$6, allowed_skills=$7,
			     updated_at=NOW()
			 WHERE id=$8`,
			cfg.Name, cfg.Description, cfg.Persona, cfg.SystemPrompt,
			cfg.LLMModel, cfg.MaxIterations, skills, cfg.ID,
		)
		if err != nil {
			return fmt.Errorf("update agent %s: %w", cfg.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("update agent %s: %w", cfg.ID, ErrNotFound)
		}
		r.logger.Info("agent updated", zap.String("agent_id", cfg.ID))
		return nil
	})
}
