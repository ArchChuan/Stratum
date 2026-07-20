// Package persistence — Postgres adapter for agent configuration. Implements
// port.AgentRepo using per-tenant schema search_path.
//
// All cross-table relations (agent_skill_links, agent_mcp_tool_links,
// agent_workspaces ⨯ rag_workspaces) are loaded inside a single
// transaction so the returned AgentConfig is internally consistent.

package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// poolIface allows pgxmock injection in tests.
type poolIface interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

var _ poolIface = (*pgxpool.Pool)(nil)

// PgAgentRepo persists Agent configs in PostgreSQL under per-tenant schemas.
type PgAgentRepo struct {
	pool poolIface
}

// NewPgAgentRepo constructs a Postgres-backed AgentRepo.
func NewPgAgentRepo(pool *pgxpool.Pool) *PgAgentRepo {
	return &PgAgentRepo{pool: pool}
}

// execTenant runs fn in a transaction with search_path set to the tenant schema from ctx.
func (r *PgAgentRepo) execTenant(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tc, ok := tenantdb.FromContext(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("agent_repo: missing tenant context")
	}
	return pgstore.ExecTenantWith(ctx, r.pool, tc.TenantID, fn)
}

func (r *PgAgentRepo) replaceSkills(ctx context.Context, tx pgx.Tx, agentID string, skillIDs []string) error {
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
				return fmt.Errorf("%w: %s", domain.ErrInvalidSkill, sid)
			}
			return fmt.Errorf("replace agent_skill_links insert agent %s skill %s: %w", agentID, sid, err)
		}
	}
	return nil
}

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

func (r *PgAgentRepo) replaceMCPTools(ctx context.Context, tx pgx.Tx, agentID string, toolIDs []string) error {
	if _, err := tx.Exec(ctx, `DELETE FROM agent_mcp_tool_links WHERE agent_id = $1`, agentID); err != nil {
		return fmt.Errorf("replace agent_mcp_tool_links delete agent %s: %w", agentID, err)
	}
	for _, toolID := range toolIDs {
		serverID, toolName, ok := parseMCPToolID(toolID)
		if !ok {
			return fmt.Errorf("invalid MCP tool ID %q", toolID)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO agent_mcp_tool_links (agent_id, server_id, tool_name) VALUES ($1, $2, $3)`,
			agentID, serverID, toolName,
		); err != nil {
			return fmt.Errorf("replace agent_mcp_tool_links insert agent %s tool %s: %w", agentID, toolID, err)
		}
	}
	return nil
}

func loadMCPToolIDs(ctx context.Context, tx pgx.Tx, agentID string) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT server_id, tool_name FROM agent_mcp_tool_links WHERE agent_id = $1`, agentID)
	if err != nil {
		return nil, fmt.Errorf("load mcp_configs agent %s: %w", agentID, err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var serverID, toolName string
		if err := rows.Scan(&serverID, &toolName); err != nil {
			return nil, err
		}
		ids = append(ids, "mcp:"+serverID+":"+toolName)
	}
	return ids, rows.Err()
}

func parseMCPToolID(id string) (string, string, bool) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 || parts[0] != "mcp" || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func (r *PgAgentRepo) replaceKnowledgeWorkspaces(ctx context.Context, tx pgx.Tx, agentID string, workspaceIDs []string) error {
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

func loadKnowledgeWorkspaces(ctx context.Context, tx pgx.Tx, agentID string) ([]string, []string, []string, error) {
	rows, err := tx.Query(ctx,
		`SELECT aw.workspace_id::text, rw.name, COALESCE(rw.description, '')
		   FROM agent_workspaces aw
		   JOIN rag_workspaces rw ON rw.id = aw.workspace_id
		  WHERE aw.agent_id = $1`, agentID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load knowledge_workspaces agent %s: %w", agentID, err)
	}
	defer rows.Close()
	var ids, names, descs []string
	for rows.Next() {
		var id, name, desc string
		if err := rows.Scan(&id, &name, &desc); err != nil {
			return nil, nil, nil, err
		}
		ids = append(ids, id)
		names = append(names, name)
		descs = append(descs, desc)
	}
	return ids, names, descs, rows.Err()
}

// Register inserts a new agent row + relations.
func (r *PgAgentRepo) Register(ctx context.Context, cfg *domain.AgentConfig) error {
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO agents (id, name, type, description, system_prompt, llm_model, embed_model, max_iterations, max_context_tokens, memory_scope)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			cfg.ID, cfg.Name, string(cfg.Type), cfg.Description,
			cfg.SystemPrompt, cfg.LLMModel, cfg.EmbedModel, cfg.MaxIterations, cfg.MaxContextTokens, cfg.MemoryScope,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				return fmt.Errorf("%w: agent name %q", domain.ErrNameConflict, cfg.Name)
			}
			return fmt.Errorf("register agent %s: %w", cfg.ID, err)
		}
		if err := r.replaceSkills(ctx, tx, cfg.ID, cfg.AllowedSkills); err != nil {
			return err
		}
		if err := r.replaceMCPTools(ctx, tx, cfg.ID, cfg.MCPToolIDs); err != nil {
			return err
		}
		if err := r.replaceKnowledgeWorkspaces(ctx, tx, cfg.ID, cfg.KnowledgeWorkspaceIDs); err != nil {
			return err
		}
		return nil
	})
}

// Get returns a populated AgentConfig (with relations) or (nil, false, nil) on miss.
func (r *PgAgentRepo) Get(ctx context.Context, id string) (*domain.AgentConfig, bool, error) {
	var cfg domain.AgentConfig
	var agentType string
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT id, name, type, description, system_prompt, llm_model, embed_model, max_iterations, max_context_tokens, memory_scope
			 FROM agents WHERE id = $1`, id).
			Scan(&cfg.ID, &cfg.Name, &agentType, &cfg.Description,
				&cfg.SystemPrompt, &cfg.LLMModel, &cfg.EmbedModel, &cfg.MaxIterations, &cfg.MaxContextTokens, &cfg.MemoryScope); err != nil {
			return err
		}
		skillIDs, err := loadSkillIDs(ctx, tx, id)
		if err != nil {
			return err
		}
		cfg.AllowedSkills = skillIDs
		ids, err := loadMCPToolIDs(ctx, tx, id)
		if err != nil {
			return err
		}
		cfg.MCPToolIDs = ids
		wsIDs, wsNames, wsDescs, err := loadKnowledgeWorkspaces(ctx, tx, id)
		if err != nil {
			return err
		}
		cfg.KnowledgeWorkspaceIDs = wsIDs
		cfg.KnowledgeWorkspaceNames = wsNames
		cfg.KnowledgeWorkspaceDescriptions = wsDescs
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if cfg.AllowedSkills == nil {
		cfg.AllowedSkills = []string{}
	}
	if cfg.MCPToolIDs == nil {
		cfg.MCPToolIDs = []string{}
	}
	if cfg.KnowledgeWorkspaceIDs == nil {
		cfg.KnowledgeWorkspaceIDs = []string{}
	}
	cfg.Type = domain.AgentType(agentType)
	return &cfg, true, nil
}

// GetAll returns all agents in the tenant schema.
//
// Uses 4 batched queries (agents + 3 association tables via WHERE agent_id = ANY($1))
// instead of fanning out per-agent loaders, so cost is O(1) round-trips, not O(N).
func (r *PgAgentRepo) GetAll(ctx context.Context) ([]*domain.AgentConfig, error) {
	var out []*domain.AgentConfig
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		cfgs, ids, err := scanAgents(ctx, tx)
		if err != nil {
			return err
		}
		if len(cfgs) == 0 {
			return nil
		}
		skills, err := loadSkillsByAgents(ctx, tx, ids)
		if err != nil {
			return err
		}
		mcps, err := loadMCPsByAgents(ctx, tx, ids)
		if err != nil {
			return err
		}
		wsIDs, wsNames, wsDescs, err := loadWorkspacesByAgents(ctx, tx, ids)
		if err != nil {
			return err
		}
		for _, cfg := range cfgs {
			cfg.AllowedSkills = nonNil(skills[cfg.ID])
			cfg.MCPToolIDs = nonNil(mcps[cfg.ID])
			cfg.KnowledgeWorkspaceIDs = nonNil(wsIDs[cfg.ID])
			cfg.KnowledgeWorkspaceNames = nonNil(wsNames[cfg.ID])
			cfg.KnowledgeWorkspaceDescriptions = nonNil(wsDescs[cfg.ID])
		}
		out = cfgs
		return nil
	})
	return out, err
}

func scanAgents(ctx context.Context, tx pgx.Tx) ([]*domain.AgentConfig, []string, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, name, type, description, system_prompt, llm_model, embed_model, max_iterations, max_context_tokens, memory_scope
		 FROM agents ORDER BY created_at`)
	if err != nil {
		return nil, nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()
	var cfgs []*domain.AgentConfig
	var ids []string
	for rows.Next() {
		var cfg domain.AgentConfig
		var agentType string
		if err := rows.Scan(&cfg.ID, &cfg.Name, &agentType, &cfg.Description,
			&cfg.SystemPrompt, &cfg.LLMModel, &cfg.EmbedModel, &cfg.MaxIterations, &cfg.MaxContextTokens, &cfg.MemoryScope); err != nil {
			return nil, nil, fmt.Errorf("scan agent row: %w", err)
		}
		cfg.Type = domain.AgentType(agentType)
		cfgs = append(cfgs, &cfg)
		ids = append(ids, cfg.ID)
	}
	return cfgs, ids, rows.Err()
}

func loadSkillsByAgents(ctx context.Context, tx pgx.Tx, agentIDs []string) (map[string][]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT agent_id, skill_id FROM agent_skill_links WHERE agent_id = ANY($1)`, agentIDs)
	if err != nil {
		return nil, fmt.Errorf("load skill_links: %w", err)
	}
	defer rows.Close()
	out := make(map[string][]string, len(agentIDs))
	for rows.Next() {
		var aid, sid string
		if err := rows.Scan(&aid, &sid); err != nil {
			return nil, err
		}
		out[aid] = append(out[aid], sid)
	}
	return out, rows.Err()
}

func loadMCPsByAgents(ctx context.Context, tx pgx.Tx, agentIDs []string) (map[string][]string, error) {
	rows, err := tx.Query(ctx,
		`SELECT agent_id, server_id, tool_name FROM agent_mcp_tool_links WHERE agent_id = ANY($1)`, agentIDs)
	if err != nil {
		return nil, fmt.Errorf("load mcp_links: %w", err)
	}
	defer rows.Close()
	out := make(map[string][]string, len(agentIDs))
	for rows.Next() {
		var aid, serverID, toolName string
		if err := rows.Scan(&aid, &serverID, &toolName); err != nil {
			return nil, err
		}
		out[aid] = append(out[aid], "mcp:"+serverID+":"+toolName)
	}
	return out, rows.Err()
}

func loadWorkspacesByAgents(ctx context.Context, tx pgx.Tx, agentIDs []string) (
	map[string][]string, map[string][]string, map[string][]string, error,
) {
	rows, err := tx.Query(ctx,
		`SELECT aw.agent_id, aw.workspace_id::text, rw.name, COALESCE(rw.description, '')
		   FROM agent_workspaces aw
		   JOIN rag_workspaces rw ON rw.id = aw.workspace_id
		  WHERE aw.agent_id = ANY($1)`, agentIDs)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load knowledge_workspaces: %w", err)
	}
	defer rows.Close()
	ids := make(map[string][]string, len(agentIDs))
	names := make(map[string][]string, len(agentIDs))
	descs := make(map[string][]string, len(agentIDs))
	for rows.Next() {
		var aid, wid, name, desc string
		if err := rows.Scan(&aid, &wid, &name, &desc); err != nil {
			return nil, nil, nil, err
		}
		ids[aid] = append(ids[aid], wid)
		names[aid] = append(names[aid], name)
		descs[aid] = append(descs[aid], desc)
	}
	return ids, names, descs, rows.Err()
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Remove deletes an agent from the tenant schema.
func (r *PgAgentRepo) Remove(ctx context.Context, id string) error {
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM agents WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("remove agent %s: %w", id, domain.ErrNotFound)
		}
		return nil
	})
}

// Update replaces an agent's mutable fields in the tenant schema.
func (r *PgAgentRepo) Update(ctx context.Context, cfg *domain.AgentConfig) error {
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE agents
			 SET name=$1, description=$2, system_prompt=$3,
			     llm_model=$4, max_iterations=$5, max_context_tokens=$6,
			     memory_scope=$7, updated_at=NOW()
			 WHERE id=$8`,
			cfg.Name, cfg.Description, cfg.SystemPrompt,
			cfg.LLMModel, cfg.MaxIterations, cfg.MaxContextTokens, cfg.MemoryScope, cfg.ID,
		)
		if err != nil {
			return fmt.Errorf("update agent %s: %w", cfg.ID, err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("update agent %s: %w", cfg.ID, domain.ErrNotFound)
		}
		if err := r.replaceSkills(ctx, tx, cfg.ID, cfg.AllowedSkills); err != nil {
			return err
		}
		if err := r.replaceMCPTools(ctx, tx, cfg.ID, cfg.MCPToolIDs); err != nil {
			return err
		}
		if err := r.replaceKnowledgeWorkspaces(ctx, tx, cfg.ID, cfg.KnowledgeWorkspaceIDs); err != nil {
			return err
		}
		return nil
	})
}

// FindAgentBySkill returns the id of an agent bound to skillID via
// agent_skill_links, or found=false when no agent references the skill.
// Ordering by agent_id makes the pick deterministic when several agents share
// the skill. This owns the agent_skill_links read that the evaluation
// composition root previously issued as raw SQL from api/wiring.
func (r *PgAgentRepo) FindAgentBySkill(ctx context.Context, skillID string) (string, bool, error) {
	var agentID string
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT agent_id FROM agent_skill_links WHERE skill_id = $1 ORDER BY agent_id LIMIT 1`,
			skillID,
		).Scan(&agentID)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return agentID, true, nil
}
