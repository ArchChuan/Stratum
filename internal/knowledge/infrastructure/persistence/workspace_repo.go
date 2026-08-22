package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

// WorkspaceRepo persists knowledge workspaces in per-tenant schemas.
type WorkspaceRepo struct {
	db poolIface
}

// NewWorkspaceRepo constructs a WorkspaceRepo backed by the given pool.
func NewWorkspaceRepo(db poolIface) *WorkspaceRepo {
	return &WorkspaceRepo{db: db}
}

// jsonbConfig matches the JSONB shape stored in rag_workspaces.config.
type jsonbConfig struct {
	EmbeddingModel   string  `json:"embedding_model"`
	ChunkSize        int     `json:"chunk_size"`
	ChunkOverlap     int     `json:"chunk_overlap"`
	QueryMode        string  `json:"query_mode"`
	TopK             int     `json:"top_k"`
	ChunkingStrategy string  `json:"chunking_strategy"`
	Reranking        string  `json:"reranking,omitempty"`
	ScoreThreshold   float32 `json:"score_threshold,omitempty"`
	RerankTopK       int     `json:"rerank_top_k,omitempty"`
	// 不带 omitempty：空模型也显式落键，迁移谓词依赖（见 TestJSONBEmptyModelsWriteEmptyKeys）。
	RerankModel string `json:"rerank_model"`
	JudgeModel  string `json:"judge_model"`
}

func toJSONB(c domain.WorkspaceConfig) string {
	b, _ := json.Marshal(jsonbConfig{
		EmbeddingModel:   c.EmbeddingModel,
		ChunkSize:        c.ChunkSize,
		ChunkOverlap:     c.ChunkOverlap,
		QueryMode:        c.QueryMode,
		TopK:             c.TopK,
		ChunkingStrategy: c.ChunkingStrategy,
		Reranking:        c.Reranking,
		ScoreThreshold:   c.ScoreThreshold,
		RerankTopK:       c.RerankTopK,
		RerankModel:      c.RerankModel,
		JudgeModel:       c.JudgeModel,
	})
	return string(b)
}

func fromJSONB(c jsonbConfig) domain.WorkspaceConfig {
	return domain.WorkspaceConfig{
		EmbeddingModel:   c.EmbeddingModel,
		ChunkSize:        c.ChunkSize,
		ChunkOverlap:     c.ChunkOverlap,
		QueryMode:        c.QueryMode,
		TopK:             c.TopK,
		ChunkingStrategy: c.ChunkingStrategy,
		Reranking:        c.Reranking,
		ScoreThreshold:   c.ScoreThreshold,
		RerankTopK:       c.RerankTopK,
		RerankModel:      c.RerankModel,
		JudgeModel:       c.JudgeModel,
	}
}

// resourceEditorKind identifies knowledge rows in the shared resource_editors table.
const resourceEditorKind = "knowledge"

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
		return fmt.Errorf("editor membership check: %w", err)
	}
	if !present {
		return domain.ErrForbidden
	}
	return nil
}

// insertChangeAudit inserts the audit row in the SAME transaction as the
// business write; an audit failure rolls the business change back (fail
// closed). A nil event skips auditing — reserved for internal reentrant
// paths. Incomplete events are a caller bug and fail the transaction.
// tenantID is explicit because knowledge repos receive it as a parameter
// rather than from the tenant context.
func insertChangeAudit(ctx context.Context, tx pgx.Tx, tenantID string, ev *auditdomain.ResourceChangeAuditEvent) error {
	ev = ev.Normalized()
	if ev == nil {
		return nil
	}
	if ev.ResourceID == "" || ev.Operation == "" || ev.ResourceKind == "" {
		return fmt.Errorf("change audit: incomplete event (kind=%s id=%q op=%q)",
			ev.ResourceKind, ev.ResourceID, ev.Operation)
	}
	_, err := tx.Exec(ctx, auditdomain.ChangeAuditInsertSQL,
		uuid.Must(uuid.NewV7()).String(), tenantID,
		ev.ResourceKind, ev.ResourceID, ev.Operation, ev.ActorID, ev.ActorType, ev.Source,
		ev.ProposalID, ev.Before, ev.After)
	if err != nil {
		return fmt.Errorf("insert change audit %s %s: %w", ev.ResourceKind, ev.ResourceID, err)
	}
	return nil
}

// Create inserts a workspace, returning ErrWorkspaceConflict on unique
// violation. editors, when non-empty, is validated and persisted in the same
// transaction.
func (r *WorkspaceRepo) Create(ctx context.Context, tenantID string, ws *domain.Workspace, editors []string, audit *auditdomain.ResourceChangeAuditEvent) error {
	var id string
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO rag_workspaces (name, description, config, created_by)
	                     VALUES ($1, $2, $3, $4) RETURNING id`,
			ws.Name, ws.Description, toJSONB(ws.Config), ws.CreatedBy,
		).Scan(&id); err != nil {
			return err
		}
		// The workspace ID is generated by the database, so the audit row must
		// reference it after the INSERT returns it.
		if audit != nil {
			audit.ResourceID = id
		}
		if err := insertEditors(ctx, tx, tenantID, resourceEditorKind, id, editors, ws.CreatedBy); err != nil {
			return err
		}
		return insertChangeAudit(ctx, tx, tenantID, audit)
	})
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			return domain.ErrWorkspaceConflict
		}
		return fmt.Errorf("workspace_repo: create: %w", err)
	}
	ws.ID = id
	return nil
}

// GetByName returns a workspace by name; ErrWorkspaceNotFound if absent.
func (r *WorkspaceRepo) GetByName(ctx context.Context, tenantID, name string) (*domain.Workspace, error) {
	var (
		ws domain.Workspace
		jc jsonbConfig
	)
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, name, COALESCE(description,''), config,
		                     COALESCE(system_key,''), COALESCE(management_mode,'tenant_managed'),
		                     COALESCE(created_by,''), created_at, updated_at
	                     FROM rag_workspaces WHERE name = $1`, name,
		).Scan(&ws.ID, &ws.Name, &ws.Description, &jc,
			&ws.SystemKey, &ws.ManagementMode,
			&ws.CreatedBy, &ws.CreatedAt, &ws.UpdatedAt)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWorkspaceNotFound
		}
		return nil, fmt.Errorf("workspace_repo: get by name: %w", err)
	}
	ws.Config = fromJSONB(jc)
	return &ws, nil
}

// GetByID returns a workspace by UUID; ErrWorkspaceNotFound if absent.
func (r *WorkspaceRepo) GetByID(ctx context.Context, tenantID, id string) (*domain.Workspace, error) {
	var (
		ws domain.Workspace
		jc jsonbConfig
	)
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT id, name, COALESCE(description,''), config,
		                     COALESCE(system_key,''), COALESCE(management_mode,'tenant_managed'),
		                     COALESCE(created_by,''), created_at, updated_at
	                     FROM rag_workspaces WHERE id = $1::uuid`, id,
		).Scan(&ws.ID, &ws.Name, &ws.Description, &jc,
			&ws.SystemKey, &ws.ManagementMode,
			&ws.CreatedBy, &ws.CreatedAt, &ws.UpdatedAt)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWorkspaceNotFound
		}
		return nil, fmt.Errorf("workspace_repo: get by id: %w", err)
	}
	ws.Config = fromJSONB(jc)
	return &ws, nil
}

// List returns workspaces ordered by created_at DESC.
func (r *WorkspaceRepo) List(ctx context.Context, tenantID string) ([]*domain.Workspace, error) {
	var out []*domain.Workspace
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, COALESCE(description,''), config,
		                     COALESCE(system_key,''), COALESCE(management_mode,'tenant_managed'),
		                     COALESCE(created_by,''), created_at, updated_at
	                     FROM rag_workspaces ORDER BY created_at DESC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ws domain.Workspace
			var jc jsonbConfig
			if err := rows.Scan(&ws.ID, &ws.Name, &ws.Description, &jc,
				&ws.SystemKey, &ws.ManagementMode,
				&ws.CreatedBy, &ws.CreatedAt, &ws.UpdatedAt); err != nil {
				return fmt.Errorf("workspace_repo: scan list row: %w", err)
			}
			ws.Config = fromJSONB(jc)
			out = append(out, &ws)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("workspace_repo: list: %w", err)
	}
	return out, nil
}

// UpdateWorkspaceAll applies rename, description and config atomically in one
// transaction, then writes the change audit in the same transaction. renameTo
// and description may be nil to leave the column untouched; the config is
// always written (callers pass the merged value). ErrWorkspaceNotFound on 0
// rows, ErrWorkspaceConflict on duplicate rename. editorActor, when
// non-empty, is re-validated inside the transaction (role + editor
// membership) before the UPDATE, closing the check-then-write TOCTOU window.
func (r *WorkspaceRepo) UpdateWorkspaceAll(
	ctx context.Context, tenantID, name string,
	renameTo, description *string,
	cfg domain.WorkspaceConfig,
	editorActor string,
	audit *auditdomain.ResourceChangeAuditEvent,
) error {
	var tag pgconn.CommandTag
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if editorActor != "" {
			var id string
			if err := tx.QueryRow(ctx, `SELECT id FROM rag_workspaces WHERE name=$1`, name).Scan(&id); err != nil {
				return err
			}
			if err := revalidateEditorAccess(ctx, tx, tenantID, resourceEditorKind, id, editorActor); err != nil {
				return err
			}
		}
		var err error
		tag, err = tx.Exec(ctx, `UPDATE rag_workspaces
	                     SET name = COALESCE($1, name),
	                         description = COALESCE($2, description),
	                         config = $3,
	                         updated_at = NOW()
			WHERE name = $4`, renameTo, description, toJSONB(cfg), name)
		if err != nil {
			return err
		}
		return insertChangeAudit(ctx, tx, tenantID, audit)
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return domain.ErrWorkspaceConflict
		}
		return fmt.Errorf("workspace_repo: update: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrWorkspaceNotFound
	}
	return nil
}

func (r *WorkspaceRepo) Delete(ctx context.Context, tenantID, name string, audit *auditdomain.ResourceChangeAuditEvent) error {
	var tag pgconn.CommandTag
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		// The workspace id (DB-generated UUID) is the resource_editors key;
		// resolve it before deleting the editors rows.
		var id string
		if err := tx.QueryRow(ctx, `SELECT id FROM rag_workspaces WHERE name=$1`, name).Scan(&id); err != nil {
			return err
		}
		var err error
		tag, err = tx.Exec(ctx, `DELETE FROM rag_workspaces WHERE name = $1`, name)
		if err != nil {
			return err
		}
		// Editors die with the resource in the same transaction.
		if _, err := tx.Exec(ctx,
			`DELETE FROM resource_editors WHERE resource_kind=$1 AND resource_id=$2`,
			resourceEditorKind, id,
		); err != nil {
			return fmt.Errorf("delete editors: %w", err)
		}
		return insertChangeAudit(ctx, tx, tenantID, audit)
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return domain.ErrWorkspaceLinked
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrWorkspaceNotFound
		}
		return fmt.Errorf("workspace_repo: delete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrWorkspaceNotFound
	}
	return nil
}

// GetConfigForUpload returns just the config of a workspace; ErrWorkspaceNotFound if absent.
func (r *WorkspaceRepo) GetConfigForUpload(ctx context.Context, tenantID, name string) (domain.WorkspaceConfig, error) {
	var jc jsonbConfig
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT config FROM rag_workspaces WHERE name = $1`, name).Scan(&jc)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.WorkspaceConfig{}, domain.ErrWorkspaceNotFound
		}
		return domain.WorkspaceConfig{}, fmt.Errorf("workspace_repo: get config: %w", err)
	}
	return fromJSONB(jc), nil
}

// GetConfigByID returns just the config of a workspace resolved by UUID; ErrWorkspaceNotFound if absent.
func (r *WorkspaceRepo) GetConfigByID(ctx context.Context, tenantID, id string) (domain.WorkspaceConfig, error) {
	var jc jsonbConfig
	err := execTenant(ctx, r.db, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT config FROM rag_workspaces WHERE id = $1::uuid`, id).Scan(&jc)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.WorkspaceConfig{}, domain.ErrWorkspaceNotFound
		}
		return domain.WorkspaceConfig{}, fmt.Errorf("workspace_repo: get config by id: %w", err)
	}
	return fromJSONB(jc), nil
}
