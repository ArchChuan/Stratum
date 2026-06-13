// Package agent provides the core agent system.
package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/capgateway"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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

// replaceSkills replaces agent_skill_links rows for agentID within tx.
func (r *Registry) replaceSkills(ctx context.Context, tx pgx.Tx, agentID string, skillIDs []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM agent_skill_links WHERE agent_id = $1`, agentID); err != nil {
		return fmt.Errorf("replace agent_skill_links delete agent %s: %w", agentID, err)
	}
	for _, sid := range skillIDs {
		if sid == "" {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO agent_skill_links (agent_id, skill_id) VALUES ($1, $2)`,
			agentID, sid,
		); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" {
				return fmt.Errorf("%w: %s", ErrInvalidSkill, sid)
			}
			return fmt.Errorf("replace agent_skill_links insert agent %s skill %s: %w", agentID, sid, err)
		}
	}
	return nil
}

// loadSkillIDs queries agent_skill_links for the given agentID within an existing transaction.
func loadSkillIDs(ctx context.Context, tx pgx.Tx, agentID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT skill_id FROM agent_skill_links WHERE agent_id = $1`, agentID)
	if err != nil {
		return nil, fmt.Errorf("load skill_links agent %s: %w", agentID, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, err
		}
		ids = append(ids, sid)
	}
	return ids, rows.Err()
}

// replaceMCPServers replaces agent_mcp_links rows for agentID.
// serverIDs may be nil or empty to clear all associations.
func (r *Registry) replaceMCPServers(ctx context.Context, tx pgx.Tx, agentID string, serverIDs []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM agent_mcp_links WHERE agent_id = $1`, agentID); err != nil {
		return fmt.Errorf("replace agent_mcp_links delete agent %s: %w", agentID, err)
	}
	for _, sid := range serverIDs {
		if sid == "" {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO agent_mcp_links (agent_id, server_id) VALUES ($1, $2)`,
			agentID, sid,
		); err != nil {
			return fmt.Errorf("replace agent_mcp_links insert agent %s server %s: %w", agentID, sid, err)
		}
	}
	return nil
}

// loadMCPServerIDs queries agent_mcp_links for the given agentID within an existing transaction.
func loadMCPServerIDs(ctx context.Context, tx pgx.Tx, agentID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT server_id FROM agent_mcp_links WHERE agent_id = $1`, agentID)
	if err != nil {
		return nil, fmt.Errorf("load mcp_configs agent %s: %w", agentID, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, err
		}
		ids = append(ids, sid)
	}
	return ids, rows.Err()
}

// replaceKnowledgeWorkspaces replaces agent_workspaces rows for agentID.
func (r *Registry) replaceKnowledgeWorkspaces(ctx context.Context, tx pgx.Tx, agentID string, workspaceIDs []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM agent_workspaces WHERE agent_id = $1`, agentID); err != nil {
		return fmt.Errorf("replace agent_workspaces delete agent %s: %w", agentID, err)
	}
	for _, wid := range workspaceIDs {
		if wid == "" {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO agent_workspaces (agent_id, workspace_id) VALUES ($1, $2::uuid)`,
			agentID, wid,
		); err != nil {
			return fmt.Errorf("replace agent_workspaces insert agent %s workspace %s: %w", agentID, wid, err)
		}
	}
	return nil
}

// loadKnowledgeWorkspaces queries agent_workspaces + rag_workspaces for the given agentID.
// Returns (ids, names, error).
func loadKnowledgeWorkspaces(ctx context.Context, tx pgx.Tx, agentID string) ([]string, []string, error) {
	rows, err := tx.Query(ctx,
		`SELECT aw.workspace_id::text, rw.name
		   FROM agent_workspaces aw
		   JOIN rag_workspaces rw ON rw.id = aw.workspace_id
		  WHERE aw.agent_id = $1`, agentID)
	if err != nil {
		return nil, nil, fmt.Errorf("load knowledge_workspaces agent %s: %w", agentID, err)
	}
	defer rows.Close()
	var ids, names []string
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, nil, err
		}
		ids = append(ids, id)
		names = append(names, name)
	}
	return ids, names, rows.Err()
}

// Register writes the agent config into the tenant schema agents table.
func (r *Registry) Register(ctx context.Context, a Agent) error {
	cfg := a.GetConfig()
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO agents (id, name, type, description, persona, system_prompt, llm_model, max_iterations)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
			cfg.ID, cfg.Name, string(cfg.Type), cfg.Description,
			cfg.Persona, cfg.SystemPrompt, cfg.LLMModel, cfg.MaxIterations,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return fmt.Errorf("%w: agent name %q", ErrNameConflict, cfg.Name)
			}
			return fmt.Errorf("register agent %s: %w", cfg.ID, err)
		}
		if err := r.replaceSkills(ctx, tx, cfg.ID, cfg.AllowedSkills); err != nil {
			return err
		}
		if err := r.replaceMCPServers(ctx, tx, cfg.ID, cfg.MCPServerIDs); err != nil {
			return err
		}
		if err := r.replaceKnowledgeWorkspaces(ctx, tx, cfg.ID, cfg.KnowledgeWorkspaceIDs); err != nil {
			return err
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
		if err := tx.QueryRow(ctx,
			`SELECT id, name, type, description, persona, system_prompt, llm_model, max_iterations
			 FROM agents WHERE id = $1`, id).
			Scan(&cfg.ID, &cfg.Name, &agentType, &cfg.Description,
				&cfg.Persona, &cfg.SystemPrompt, &cfg.LLMModel, &cfg.MaxIterations); err != nil {
			return err
		}
		skillIDs, err := loadSkillIDs(ctx, tx, id)
		if err != nil {
			return err
		}
		cfg.AllowedSkills = skillIDs
		ids, err := loadMCPServerIDs(ctx, tx, id)
		if err != nil {
			return err
		}
		cfg.MCPServerIDs = ids
		wsIDs, wsNames, err := loadKnowledgeWorkspaces(ctx, tx, id)
		if err != nil {
			return err
		}
		cfg.KnowledgeWorkspaceIDs = wsIDs
		cfg.KnowledgeWorkspaceNames = wsNames
		return nil
	})
	if err != nil {
		return nil, false
	}
	if cfg.AllowedSkills == nil {
		cfg.AllowedSkills = []string{}
	}
	if cfg.MCPServerIDs == nil {
		cfg.MCPServerIDs = []string{}
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
			`SELECT id, name, type, description, persona, system_prompt, llm_model, max_iterations
			 FROM agents ORDER BY created_at`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var cfg AgentConfig
			var agentType string
			if err := rows.Scan(&cfg.ID, &cfg.Name, &agentType, &cfg.Description,
				&cfg.Persona, &cfg.SystemPrompt, &cfg.LLMModel, &cfg.MaxIterations); err != nil {
				continue
			}
			cfg.Type = AgentType(agentType)
			skillIDs, err := loadSkillIDs(ctx, tx, cfg.ID)
			if err == nil {
				cfg.AllowedSkills = skillIDs
			}
			if cfg.AllowedSkills == nil {
				cfg.AllowedSkills = []string{}
			}
			ids, err := loadMCPServerIDs(ctx, tx, cfg.ID)
			if err == nil {
				cfg.MCPServerIDs = ids
			}
			if cfg.MCPServerIDs == nil {
				cfg.MCPServerIDs = []string{}
			}
			wsIDs, wsNames, err := loadKnowledgeWorkspaces(ctx, tx, cfg.ID)
			if err == nil {
				cfg.KnowledgeWorkspaceIDs = wsIDs
				cfg.KnowledgeWorkspaceNames = wsNames
			}
			if cfg.KnowledgeWorkspaceIDs == nil {
				cfg.KnowledgeWorkspaceIDs = []string{}
			}
			if cfg.KnowledgeWorkspaceNames == nil {
				cfg.KnowledgeWorkspaceNames = []string{}
			}
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

// ErrNameConflict is returned by Register when an agent with the same name already exists in the tenant.
var ErrNameConflict = errors.New("agent name already exists")

// ErrInvalidSkill is returned when a skill ID does not exist in the tenant's skills table.
var ErrInvalidSkill = errors.New("skill not found")

// Update replaces an agent's mutable fields in the tenant schema.
func (r *Registry) Update(ctx context.Context, cfg *AgentConfig) error {
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE agents
			 SET name=$1, description=$2, persona=$3, system_prompt=$4,
			     llm_model=$5, max_iterations=$6,
			     updated_at=NOW()
			 WHERE id=$7`,
			cfg.Name, cfg.Description, cfg.Persona, cfg.SystemPrompt,
			cfg.LLMModel, cfg.MaxIterations, cfg.ID,
		)
		if err != nil {
			return fmt.Errorf("update agent %s: %w", cfg.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("update agent %s: %w", cfg.ID, ErrNotFound)
		}
		if err := r.replaceSkills(ctx, tx, cfg.ID, cfg.AllowedSkills); err != nil {
			return err
		}
		if err := r.replaceMCPServers(ctx, tx, cfg.ID, cfg.MCPServerIDs); err != nil {
			return err
		}
		if err := r.replaceKnowledgeWorkspaces(ctx, tx, cfg.ID, cfg.KnowledgeWorkspaceIDs); err != nil {
			return err
		}
		r.logger.Info("agent updated", zap.String("agent_id", cfg.ID))
		return nil
	})
}
