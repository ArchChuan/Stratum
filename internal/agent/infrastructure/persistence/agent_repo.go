// Package persistence — Postgres adapter for agent configuration. Implements
// port.AgentRepo using per-tenant schema search_path.
//
// All cross-table relations (agent_skill_links, agent_mcp_tool_links,
// agent_workspaces ⨯ rag_workspaces) are loaded inside a single
// transaction so the returned AgentConfig is internally consistent.

package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/google/uuid"
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

// resourceEditorKind identifies agent rows in the shared resource_editors table.
const resourceEditorKind = "agent"

// editorEligible checks, inside the write transaction, that userID currently
// holds role admin or owner in the tenant. Fail closed on any lookup error.
// public.tenant_members is schema-qualified: the transaction search_path
// points at the tenant schema.
func editorEligible(ctx context.Context, tx pgx.Tx, tenantID, userID string) (bool, error) {
	var ok bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM public.tenant_members
			WHERE tenant_id=$1 AND user_id=$2 AND role IN ('admin','owner'))`,
		tenantID, userID,
	).Scan(&ok); err != nil {
		return false, fmt.Errorf("editor role check: %w", err)
	}
	return ok, nil
}

// agents.parameters JSONB carries the sampling parameters as flat scalar
// keys (temperature/max_tokens/compaction_recent_groups/
// compaction_safety_ratio/reasoning_effort + compaction_prompt/_temperature/
// _model). An explicit 0 / "" is indistinguishable from an absent key under
// omitempty, so 0/"" == unset == gateway/provider default. Keys match the
// registry evaluation keys so promote can write them back without mapping.
func packSamplingParameters(cfg *domain.AgentConfig) (string, error) {
	params := map[string]any{}
	putIfNonZero(params, "temperature", cfg.Temperature, float32(0))
	putIfNonZero(params, "max_tokens", cfg.MaxTokens, 0)
	putIfNonZero(params, "compaction_recent_groups", cfg.CompactionRecentGroups, 0)
	putIfNonZero(params, "compaction_safety_ratio", cfg.CompactionSafetyRatio, float32(0))
	putIfNonZero(params, "reasoning_effort", cfg.ReasoningEffort, "")
	putIfNonZero(params, "compaction_prompt", cfg.CompactionPrompt, "")
	putIfNonZero(params, "compaction_temperature", cfg.CompactionTemperature, float32(0))
	putIfNonZero(params, "compaction_model", cfg.CompactionModel, "")
	// memory.* resource-scope keys are pre-filtered by the application layer
	// (zeros dropped), so they copy verbatim onto the same JSONB.
	for k, v := range cfg.MemoryParameters {
		params[k] = v
	}
	b, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("pack sampling parameters: %w", err)
	}
	return string(b), nil
}

// putIfNonZero writes key→v into params unless v equals the zero sentinel
// (0 / ""), mirroring the skip-zero semantics of packSamplingParameters.
func putIfNonZero[T comparable](params map[string]any, key string, v, zero T) {
	if v != zero {
		params[key] = v
	}
}

// packMaxTokensFragment renders the parameters merge fragment for the system
// assistant update. 0 = unset (skip), mirroring packSamplingParameters' skip-zero
// semantics so an old client PUT cannot erase stored sampling parameters.
func packMaxTokensFragment(maxTokens int) (string, error) {
	if maxTokens <= 0 {
		return "{}", nil
	}
	b, err := json.Marshal(map[string]any{"max_tokens": maxTokens})
	if err != nil {
		return "", fmt.Errorf("pack max tokens fragment: %w", err)
	}
	return string(b), nil
}

// packAllSamplingParameters builds the full JSONB map including explicit
// nulls for zero sampling fields. A JSONB null == explicit clear: under
// overall-replace semantics (promote) a zero field must erase a previously
// persisted value, which packSkipZero alone cannot express. unpack treats
// null and absent identically (0 = unset).
func packAllSamplingParameters(cfg *domain.AgentConfig) (string, error) {
	params := map[string]any{
		"temperature":              cfg.Temperature,
		"max_tokens":               cfg.MaxTokens,
		"compaction_recent_groups": cfg.CompactionRecentGroups,
		"compaction_safety_ratio":  cfg.CompactionSafetyRatio,
		"reasoning_effort":         cfg.ReasoningEffort,
		"compaction_prompt":        cfg.CompactionPrompt,
		"compaction_temperature":   cfg.CompactionTemperature,
		"compaction_model":         cfg.CompactionModel,
	}
	// 0 / "" → nil → JSON null;非零/非空保持原值。
	for k, v := range params {
		if isZeroSamplingValue(v) {
			params[k] = nil
		}
	}
	// memory.* resource params are not part of the evaluation candidate space;
	// the application layer already merged existing values into the incoming
	// patch, so they persist verbatim across the overall-replace write.
	for k, v := range cfg.MemoryParameters {
		params[k] = v
	}
	b, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("pack sampling parameters: %w", err)
	}
	return string(b), nil
}

// isZeroSamplingValue reports whether a sampling value is the unset sentinel
// for its type (0 for numbers, "" for strings). packAllSamplingParameters maps
// such values to JSON null (explicit clear under overall-replace semantics).
func isZeroSamplingValue(v any) bool {
	switch val := v.(type) {
	case float32:
		return val == 0
	case int:
		return val == 0
	case string:
		return val == ""
	default:
		return false
	}
}

// unpackSamplingParameters fills the sampling fields (temperature/max_tokens/
// compaction×3 + reasoning_effort + compaction_prompt/_temperature/_model)
// from JSONB; absent keys leave the zero value (unset) untouched.
func unpackSamplingParameters(raw string, cfg *domain.AgentConfig) error {
	if raw == "" {
		return nil
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		return fmt.Errorf("unpack sampling parameters: %w", err)
	}
	unpackNumber(params, "temperature", &cfg.Temperature)
	unpackNumber(params, "max_tokens", &cfg.MaxTokens)
	unpackNumber(params, "compaction_recent_groups", &cfg.CompactionRecentGroups)
	unpackNumber(params, "compaction_safety_ratio", &cfg.CompactionSafetyRatio)
	unpackString(params, "reasoning_effort", &cfg.ReasoningEffort)
	unpackString(params, "compaction_prompt", &cfg.CompactionPrompt)
	unpackNumber(params, "compaction_temperature", &cfg.CompactionTemperature)
	unpackString(params, "compaction_model", &cfg.CompactionModel)
	extractMemoryParameters(params, cfg)
	return nil
}

// unpackNumber decodes a JSON numeric key into *dst. A JSON null (explicit
// clear) or absent key both land as zero = unset, matching pack's skip-zero.
func unpackNumber[T int | float32](params map[string]any, key string, dst *T) {
	if v, ok := numericValue(params[key]); ok {
		*dst = T(v)
	}
}

// unpackString decodes a JSON string key into *dst. JSON null (explicit
// clear) and absent both land as "" = unset.
func unpackString(params map[string]any, key string, dst *string) {
	if v, ok := params[key].(string); ok {
		*dst = v
	}
}

// extractMemoryParameters copies memory.* dotted keys into cfg.MemoryParameters
// verbatim; JSON null (explicit clear on a promote write) and absent both stay
// absent so the pipeline falls back to the definition default and the edit
// form sees an unset value.
func extractMemoryParameters(params map[string]any, cfg *domain.AgentConfig) {
	for k, v := range params {
		if strings.HasPrefix(k, "memory.") && v != nil {
			if cfg.MemoryParameters == nil {
				cfg.MemoryParameters = map[string]any{}
			}
			cfg.MemoryParameters[k] = v
		}
	}
}

func numericValue(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// insertEditors validates and persists the editor set inside the write
// transaction. A non-eligible id fails the whole transaction (fail closed),
// so a forged editor can never be created alongside the resource.
func insertEditors(ctx context.Context, tx pgx.Tx, tenantID, kind, resourceID string, editorIDs []string, createdBy string) error {
	for _, id := range editorIDs {
		eligible, err := editorEligible(ctx, tx, tenantID, id)
		if err != nil {
			return err
		}
		if !eligible {
			return fmt.Errorf("%w: user %s", domain.ErrEditorNotEligible, id)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO resource_editors (resource_kind, resource_id, editor_id, created_by)
			 VALUES ($1,$2,$3,$4)`,
			kind, resourceID, id, createdBy,
		); err != nil {
			return fmt.Errorf("insert editor %s: %w", id, err)
		}
	}
	return nil
}

// revalidateEditorAccess re-checks, inside the write transaction, that the
// actor still qualifies as an editor of this resource: role admin/owner AND
// present in resource_editors. Both checks share the transaction with the
// business UPDATE, closing the check-then-write TOCTOU window.
func revalidateEditorAccess(ctx context.Context, tx pgx.Tx, tenantID, kind, resourceID, actorID string) error {
	eligible, err := editorEligible(ctx, tx, tenantID, actorID)
	if err != nil {
		return err
	}
	if !eligible {
		return domain.ErrForbidden
	}
	var present bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM resource_editors
		 WHERE resource_kind=$1 AND resource_id=$2 AND editor_id=$3)`,
		kind, resourceID, actorID,
	).Scan(&present); err != nil {
		return fmt.Errorf("editor presence check: %w", err)
	}
	if !present {
		return domain.ErrForbidden
	}
	return nil
}

// execTenant runs fn in a transaction with search_path set to the tenant schema from ctx.
func (r *PgAgentRepo) execTenant(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tc, ok := tenantdb.FromContext(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("agent_repo: missing tenant context")
	}
	return pgstore.ExecTenantWith(ctx, r.pool, tc.TenantID, fn)
}

// insertChangeAudit persists one audit row inside the business transaction.
// ev == nil skips the write (internal reentrant paths only); a non-nil event
// must be complete — an empty resource id or operation is a caller bug and
// fails the transaction closed.
func insertChangeAudit(ctx context.Context, tx pgx.Tx, ev *auditdomain.ResourceChangeAuditEvent) error {
	ev = ev.Normalized()
	if ev == nil {
		return nil
	}
	if ev.ResourceID == "" || ev.Operation == "" || ev.ResourceKind == "" {
		return fmt.Errorf("change audit: incomplete event (kind=%s id=%q op=%q)",
			ev.ResourceKind, ev.ResourceID, ev.Operation)
	}
	tc, ok := tenantdb.FromContext(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("change audit: missing tenant context")
	}
	_, err := tx.Exec(ctx, auditdomain.ChangeAuditInsertSQL,
		uuid.Must(uuid.NewV7()).String(), tc.TenantID,
		ev.ResourceKind, ev.ResourceID, ev.Operation, ev.ActorID, ev.ActorType, ev.Source,
		ev.ProposalID, ev.Before, ev.After)
	if err != nil {
		return fmt.Errorf("insert change audit %s %s: %w", ev.ResourceKind, ev.ResourceID, err)
	}
	return nil
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

// Register inserts a new agent row + relations, auditing the create in the
// same transaction.
func (r *PgAgentRepo) Register(ctx context.Context, cfg *domain.AgentConfig, audit *auditdomain.ResourceChangeAuditEvent, editors []string) error {
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		params, err := packSamplingParameters(cfg)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx,
			`INSERT INTO agents (id, name, type, description, system_prompt, llm_model, max_iterations, max_context_tokens, memory_scope, system_key, created_by, parameters)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULL,$10,$11)`,
			cfg.ID, cfg.Name, string(cfg.Type), cfg.Description,
			cfg.SystemPrompt, cfg.LLMModel, cfg.MaxIterations, cfg.MaxContextTokens, cfg.MemoryScope, cfg.CreatedBy, params,
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
		if err := insertEditorsIfAny(ctx, tx, resourceEditorKind, cfg.ID, editors, cfg.CreatedBy); err != nil {
			return err
		}
		if err := insertChangeAudit(ctx, tx, audit); err != nil {
			return err
		}
		return nil
	})
}

// insertEditorsIfAny persists the editor set only when non-empty, resolving
// tenant context from the transaction. Editors are validated in-transaction
// (fail closed), so a forged editor can never be created with the resource.
func insertEditorsIfAny(ctx context.Context, tx pgx.Tx, kind, resourceID string, editorIDs []string, createdBy string) error {
	if len(editorIDs) == 0 {
		return nil
	}
	tc, ok := tenantdb.FromContext(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("agent_repo: missing tenant context")
	}
	return insertEditors(ctx, tx, tc.TenantID, kind, resourceID, editorIDs, createdBy)
}

// revalidateEditorIfActor re-checks editor eligibility only when an editor
// actor is supplied; tenant context is resolved inside the transaction,
// closing the check-then-write TOCTOU window.
func revalidateEditorIfActor(ctx context.Context, tx pgx.Tx, kind, resourceID, actorID string) error {
	if actorID == "" {
		return nil
	}
	tc, ok := tenantdb.FromContext(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("agent_repo: missing tenant context")
	}
	return revalidateEditorAccess(ctx, tx, tc.TenantID, kind, resourceID, actorID)
}

// Get returns a populated AgentConfig (with relations) or (nil, false, nil) on miss.
func (r *PgAgentRepo) Get(ctx context.Context, id string) (*domain.AgentConfig, bool, error) {
	var cfg domain.AgentConfig
	var agentType string
	var rawParams string
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT id, name, type, description, system_prompt, llm_model, max_iterations, max_context_tokens, memory_scope,
			        COALESCE(system_key, ''), COALESCE(created_by, ''), parameters
			 FROM agents WHERE id = $1`, id).
			Scan(&cfg.ID, &cfg.Name, &agentType, &cfg.Description,
				&cfg.SystemPrompt, &cfg.LLMModel, &cfg.MaxIterations, &cfg.MaxContextTokens, &cfg.MemoryScope,
				&cfg.SystemKey, &cfg.CreatedBy, &rawParams); err != nil {
			return err
		}
		if err := unpackSamplingParameters(rawParams, &cfg); err != nil {
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
	cfg.Type = domain.AgentType(agentType)
	setManagedIdentity(&cfg)
	cfg.AllowedSkills = nonNil(cfg.AllowedSkills)
	cfg.MCPToolIDs = nonNil(cfg.MCPToolIDs)
	cfg.KnowledgeWorkspaceIDs = nonNil(cfg.KnowledgeWorkspaceIDs)
	return &cfg, true, nil
}

func (r *PgAgentRepo) GetSystemAssistant(ctx context.Context) (*domain.AgentConfig, bool, error) {
	// 平台助手不进参数闭环(采样参数不优化、不走 promote 写回),但快照读取
	// 与普通 agent 同形状,一并补读 parameters 列保持 schema 一致。
	var cfg domain.AgentConfig
	var agentType string
	var rawParams string
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`SELECT id, name, type, description, system_prompt, llm_model, max_iterations, max_context_tokens,
			        memory_scope, system_key, COALESCE(created_by, ''), parameters
			 FROM agents WHERE system_key = 'stratum.platform_assistant'`).
			Scan(&cfg.ID, &cfg.Name, &agentType, &cfg.Description, &cfg.SystemPrompt, &cfg.LLMModel,
				&cfg.MaxIterations, &cfg.MaxContextTokens, &cfg.MemoryScope, &cfg.SystemKey, &cfg.CreatedBy, &rawParams); err != nil {
			return err
		}
		if err := unpackSamplingParameters(rawParams, &cfg); err != nil {
			return err
		}
		if err := loadAgentRelations(ctx, tx, &cfg); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	cfg.Type = domain.AgentType(agentType)
	setManagedIdentity(&cfg)
	cfg.AllowedSkills = nonNil(cfg.AllowedSkills)
	cfg.MCPToolIDs = nonNil(cfg.MCPToolIDs)
	cfg.KnowledgeWorkspaceIDs = nonNil(cfg.KnowledgeWorkspaceIDs)
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
		`SELECT id, name, type, description, system_prompt, llm_model, max_iterations, max_context_tokens, memory_scope,
		        COALESCE(system_key, ''), COALESCE(created_by, ''), parameters
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
		var rawParams string
		if err := rows.Scan(&cfg.ID, &cfg.Name, &agentType, &cfg.Description,
			&cfg.SystemPrompt, &cfg.LLMModel, &cfg.MaxIterations, &cfg.MaxContextTokens, &cfg.MemoryScope,
			&cfg.SystemKey, &cfg.CreatedBy, &rawParams); err != nil {
			return nil, nil, fmt.Errorf("scan agent row: %w", err)
		}
		if err := unpackSamplingParameters(rawParams, &cfg); err != nil {
			return nil, nil, fmt.Errorf("scan agent row: %w", err)
		}
		cfg.Type = domain.AgentType(agentType)
		setManagedIdentity(&cfg)
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

// Remove deletes an agent from the tenant schema, auditing the delete in the
// same transaction.
func (r *PgAgentRepo) Remove(ctx context.Context, id string, audit *auditdomain.ResourceChangeAuditEvent) error {
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := rejectManagedAssistant(ctx, tx, id); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `DELETE FROM agents WHERE id = $1`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("remove agent %s: %w", id, domain.ErrNotFound)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM resource_editors WHERE resource_kind=$1 AND resource_id=$2`,
			resourceEditorKind, id,
		); err != nil {
			return fmt.Errorf("remove agent %s editors: %w", id, err)
		}
		if err := insertChangeAudit(ctx, tx, audit); err != nil {
			return err
		}
		return nil
	})
}

// Update replaces an agent's mutable fields in the tenant schema, auditing
// the change in the same transaction. created_by is deliberately not in the
// SET list — ownership never changes after creation. replaceParams selects
// the parameters JSONB semantics: true = overall replace (promote, zero
// fields become explicit nulls), false = merge (zero fields omitted).
func (r *PgAgentRepo) Update(ctx context.Context, cfg *domain.AgentConfig, audit *auditdomain.ResourceChangeAuditEvent, editorActor string, replaceParams bool) error {
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := rejectManagedAssistant(ctx, tx, cfg.ID); err != nil {
			return err
		}
		if err := revalidateEditorIfActor(ctx, tx, resourceEditorKind, cfg.ID, editorActor); err != nil {
			return fmt.Errorf("update agent %s: %w", cfg.ID, err)
		}
		parametersSet, params, err := samplingParameterSet(cfg, replaceParams)
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx,
			`UPDATE agents
			 SET name=$1, description=$2, system_prompt=$3,
			     llm_model=$4, max_iterations=$5, max_context_tokens=$6,
			     memory_scope=$7, `+parametersSet+`, updated_at=NOW()
			 WHERE id=$9`,
			cfg.Name, cfg.Description, cfg.SystemPrompt,
			cfg.LLMModel, cfg.MaxIterations, cfg.MaxContextTokens, cfg.MemoryScope, params, cfg.ID,
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
		if err := insertChangeAudit(ctx, tx, audit); err != nil {
			return err
		}
		return nil
	})
}

// samplingParameterSet renders the parameters UPDATE fragment and packed JSON
// for the merge (form/API) or replace (promote) semantics. merge 路径用 JSONB
// 拼接:仅覆盖本次出现的 key,旧客户端 PUT 不清除已存参数;replace 路径整体
// 覆盖,零值以 JSON null 显式清除。0=unset 语义见 pack 函数注释。
func samplingParameterSet(cfg *domain.AgentConfig, replaceParams bool) (string, string, error) {
	var params string
	var err error
	if replaceParams {
		params, err = packAllSamplingParameters(cfg)
	} else {
		params, err = packSamplingParameters(cfg)
	}
	if err != nil {
		return "", "", err
	}
	if replaceParams {
		return "parameters=$8", params, nil
	}
	return "parameters=parameters || $8::jsonb", params, nil
}

// UpdateSystemAssistantModel updates the platform assistant's model fields in
// a single transaction, auditing the change. Ownership checks do not apply —
// the platform assistant is exempt — but every change is still recorded.
func (r *PgAgentRepo) UpdateSystemAssistantModel(ctx context.Context, model string, memoryScope string, maxIterations int, maxContextTokens int, audit *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	var cfg domain.AgentConfig
	var agentType string
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `UPDATE agents SET llm_model=$1, memory_scope=$2,
			max_iterations=$3, max_context_tokens=$4, updated_at=NOW()
			WHERE system_key='stratum.platform_assistant'
			RETURNING id, name, type, description, system_prompt, llm_model,
			          max_iterations, max_context_tokens, memory_scope, system_key, created_by`, model, memoryScope, maxIterations, maxContextTokens).
			Scan(&cfg.ID, &cfg.Name, &agentType, &cfg.Description, &cfg.SystemPrompt, &cfg.LLMModel,
				&cfg.MaxIterations, &cfg.MaxContextTokens, &cfg.MemoryScope, &cfg.SystemKey, &cfg.CreatedBy); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("update system assistant model: %w", domain.ErrNotFound)
			}
			return fmt.Errorf("update system assistant model: %w", err)
		}
		if err := loadAgentRelations(ctx, tx, &cfg); err != nil {
			return fmt.Errorf("update system assistant model relations: %w", err)
		}
		if err := insertChangeAudit(ctx, tx, audit); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	cfg.Type = domain.AgentType(agentType)
	setManagedIdentity(&cfg)
	cfg.AllowedSkills = nonNil(cfg.AllowedSkills)
	cfg.MCPToolIDs = nonNil(cfg.MCPToolIDs)
	cfg.KnowledgeWorkspaceIDs = nonNil(cfg.KnowledgeWorkspaceIDs)
	return &cfg, nil
}

// UpdateSystemAssistantAll applies the platform assistant's model fields and
// (unchanged) bindings in ONE transaction so the change audit lands with the
// business write atomically. Formerly UpdateSystemAssistantModel +
// UpdateSystemAssistantBindings in two separate transactions.
func (r *PgAgentRepo) UpdateSystemAssistantAll(ctx context.Context, model, memoryScope string, maxIterations, maxContextTokens, maxTokens int, audit *auditdomain.ResourceChangeAuditEvent) (*domain.AgentConfig, error) {
	var cfg domain.AgentConfig
	var agentType string
	var rawParams string
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		fragment, err := packMaxTokensFragment(maxTokens)
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `UPDATE agents SET llm_model=$1, memory_scope=$2,
			max_iterations=$3, max_context_tokens=$4,
			parameters = COALESCE(parameters, '{}'::jsonb) || $5::jsonb,
			updated_at=NOW()
			WHERE system_key='stratum.platform_assistant'
			RETURNING id, name, type, description, system_prompt, llm_model,
			          max_iterations, max_context_tokens, memory_scope, system_key, created_by, parameters`, model, memoryScope, maxIterations, maxContextTokens, fragment).
			Scan(&cfg.ID, &cfg.Name, &agentType, &cfg.Description, &cfg.SystemPrompt, &cfg.LLMModel,
				&cfg.MaxIterations, &cfg.MaxContextTokens, &cfg.MemoryScope, &cfg.SystemKey, &cfg.CreatedBy, &rawParams); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("update system assistant: %w", domain.ErrNotFound)
			}
			return fmt.Errorf("update system assistant: %w", err)
		}
		if err := unpackSamplingParameters(rawParams, &cfg); err != nil {
			return fmt.Errorf("update system assistant: %w", err)
		}
		if err := loadAgentRelations(ctx, tx, &cfg); err != nil {
			return fmt.Errorf("update system assistant relations: %w", err)
		}
		if err := insertChangeAudit(ctx, tx, audit); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	cfg.Type = domain.AgentType(agentType)
	setManagedIdentity(&cfg)
	cfg.AllowedSkills = nonNil(cfg.AllowedSkills)
	cfg.MCPToolIDs = nonNil(cfg.MCPToolIDs)
	cfg.KnowledgeWorkspaceIDs = nonNil(cfg.KnowledgeWorkspaceIDs)
	return &cfg, nil
}

func rejectManagedAssistant(ctx context.Context, tx pgx.Tx, id string) error {
	var systemKey string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(system_key, '') FROM agents WHERE id = $1`, id).Scan(&systemKey); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("agent %s: %w", id, domain.ErrNotFound)
		}
		return fmt.Errorf("load agent %s system key: %w", id, err)
	}
	if systemKey != "" {
		return domain.ErrSystemAssistantManaged
	}
	return nil
}

func setManagedIdentity(cfg *domain.AgentConfig) {
	if cfg.SystemKey != "" {
		cfg.IsSystem = true
		cfg.ManagementMode = "platform"
	}
}

func loadAgentRelations(ctx context.Context, tx pgx.Tx, cfg *domain.AgentConfig) error {
	var err error
	if cfg.AllowedSkills, err = loadSkillIDs(ctx, tx, cfg.ID); err != nil {
		return err
	}
	if cfg.MCPToolIDs, err = loadMCPToolIDs(ctx, tx, cfg.ID); err != nil {
		return err
	}
	cfg.KnowledgeWorkspaceIDs, cfg.KnowledgeWorkspaceNames, cfg.KnowledgeWorkspaceDescriptions, err =
		loadKnowledgeWorkspaces(ctx, tx, cfg.ID)
	return err
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
