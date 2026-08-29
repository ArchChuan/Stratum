package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/pkg/resourceaccess"
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

// insertEditors validates and persists the editor set inside the write
// transaction. A non-eligible id fails the whole transaction (fail closed),
// so a forged editor can never be created alongside the resource. Thin
// wrapper over pkg/resourceaccess; domain.ErrEditorNotEligible propagates.
func insertEditors(ctx context.Context, tx pgx.Tx, tenantID, kind, resourceID string, editorIDs []string, createdBy string) error {
	return resourceaccess.InsertEditors(ctx, tx, tenantID, kind, resourceID, editorIDs, createdBy, domain.ErrEditorNotEligible)
}

// revalidateEditorAccess re-checks, inside the write transaction, that the
// actor still qualifies as an editor of this resource: whitelisted tenant
// membership AND presence in resource_editors. Both checks share the
// transaction with the business UPDATE, closing the check-then-write TOCTOU
// window. Thin wrapper over pkg/resourceaccess; domain.ErrForbidden
// propagates.
func revalidateEditorAccess(ctx context.Context, tx pgx.Tx, tenantID, kind, resourceID, actorID string) error {
	return resourceaccess.RevalidateEditorAccess(ctx, tx, tenantID, kind, resourceID, actorID, domain.ErrForbidden)
}

// insertChangeAudit persists one audit row inside the business transaction.
// ev == nil skips the write (internal reentrant paths only). Thin wrapper
// over the shared implementation in pkg/resourceaccess. tenantID is explicit
// because knowledge repos receive it as a parameter rather than from the
// tenant context.
func insertChangeAudit(ctx context.Context, tx pgx.Tx, tenantID string, ev *auditdomain.ResourceChangeAuditEvent) error {
	ev = ev.Normalized()
	if ev == nil {
		return nil
	}
	return resourceaccess.InsertChangeAudit(ctx, tx, tenantID, auditdomain.ChangeAuditInsertSQL, resourceaccess.ChangeAudit{
		ResourceKind: ev.ResourceKind,
		ResourceID:   ev.ResourceID,
		Operation:    ev.Operation,
		ActorID:      ev.ActorID,
		ActorType:    ev.ActorType,
		Source:       ev.Source,
		ProposalID:   ev.ProposalID,
		Before:       ev.Before,
		After:        ev.After,
	})
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
		                     COALESCE(created_by,''), created_at, updated_at
	                     FROM rag_workspaces WHERE name = $1`, name,
		).Scan(&ws.ID, &ws.Name, &ws.Description, &jc,
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
		                     COALESCE(created_by,''), created_at, updated_at
	                     FROM rag_workspaces WHERE id = $1::uuid`, id,
		).Scan(&ws.ID, &ws.Name, &ws.Description, &jc,
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
// always written (callers pass the merged value via snap.Config).
// ErrWorkspaceNotFound on 0 rows, ErrWorkspaceConflict on duplicate rename.
// editorActor, when non-empty, is re-validated inside the transaction (role +
// editor membership) before the UPDATE, closing the check-then-write TOCTOU
// window. actorID is the version's CreatedBy (actual operator) and is only
// consumed by the version write (Task 4); the column write here is unchanged.
func (r *WorkspaceRepo) UpdateWorkspaceAll(
	ctx context.Context, tenantID, name string,
	renameTo, description *string,
	snap domain.KnowledgeWorkspaceSnapshot,
	editorActor, actorID string,
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
			WHERE name = $4`, renameTo, description, toJSONB(snap.Config), name)
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
