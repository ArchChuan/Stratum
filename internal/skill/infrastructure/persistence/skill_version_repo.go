package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// poolIface allows pgxmock injection in tests.
type poolIface interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

var _ poolIface = (*pgxpool.Pool)(nil)

type PgSkillRevisionRepo struct{ pool poolIface }

// execTenant runs fn in a transaction with search_path set to the tenant
// schema from ctx. Fails closed when the tenant context is missing or empty.
func (r *PgSkillRevisionRepo) execTenant(ctx context.Context, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tc, ok := tenantdb.FromContext(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("skill_revision_repo: missing tenant context")
	}
	return pgstore.ExecTenantWith(ctx, r.pool, tc.TenantID, fn)
}

func NewPgSkillRevisionRepo(pool *pgxpool.Pool) *PgSkillRevisionRepo {
	return &PgSkillRevisionRepo{pool: pool}
}

// resourceEditorKind identifies skill rows in the shared resource_editors table.
const resourceEditorKind = "skill"

// editorEligible checks, inside the write transaction, that userID is an
// active tenant member (whitelist semantics: any role may be granted editor).
// Fail closed on any lookup error. public.tenant_members is schema-qualified:
// the transaction search_path points at the tenant schema.
func editorEligible(ctx context.Context, tx pgx.Tx, tenantID, userID string) (bool, error) {
	var ok bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM public.tenant_members
			WHERE tenant_id=$1 AND user_id=$2)`,
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
// actor still qualifies as an editor of this resource: tenant membership AND
// presence in resource_editors. Both checks share the transaction with the
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

// revalidateEditorIfActor re-checks editor eligibility only when an editor
// actor is supplied; tenant context is resolved inside the transaction,
// closing the check-then-write TOCTOU window.
func revalidateEditorIfActor(ctx context.Context, tx pgx.Tx, kind, resourceID, actorID string) error {
	if actorID == "" {
		return nil
	}
	tc, ok := tenantdb.FromContext(ctx)
	if !ok || tc.TenantID == "" {
		return fmt.Errorf("skill_revision_repo: missing tenant context")
	}
	return revalidateEditorAccess(ctx, tx, tc.TenantID, kind, resourceID, actorID)
}

// insertChangeAudit inserts the audit row in the SAME transaction as the
// business write; an audit failure rolls the business change back (fail
// closed). A nil event skips auditing — reserved for internal reentrant
// paths. Incomplete events are a caller bug and fail the transaction.
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

func (r *PgSkillRevisionRepo) InsertSkill(
	ctx context.Context, skill port.SkillProductRow, revision domain.SkillRevision, audit *auditdomain.ResourceChangeAuditEvent, editors []string,
) error {
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// 创建即生效：skills 直接以 published 落库并指向第一版(revision_no=1)。
		if _, err := tx.Exec(ctx,
			`INSERT INTO skills (id, name, description, status, active_revision_id, created_by)
			 VALUES ($1, $2, $3, 'published', $4, $5)`,
			skill.ID, skill.Name, skill.Description, revision.ID, skill.CreatedBy,
		); err != nil {
			return err
		}
		if err := insertSkillRevision(ctx, tx, revision); err != nil {
			return err
		}
		tc, ok := tenantdb.FromContext(ctx)
		if !ok || tc.TenantID == "" {
			return fmt.Errorf("skill_revision_repo: missing tenant context")
		}
		if err := insertEditors(ctx, tx, tc.TenantID, resourceEditorKind, skill.ID, editors, skill.CreatedBy); err != nil {
			return err
		}
		return insertChangeAudit(ctx, tx, audit)
	})
}

func (r *PgSkillRevisionRepo) GetSkill(ctx context.Context, skillID string) (port.SkillProductRow, bool, error) {
	var row port.SkillProductRow
	found := false
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT id, name, description, status,
			        COALESCE(active_revision_id, ''), COALESCE(draft_revision_id, ''), COALESCE(created_by, '')
			 FROM skills WHERE id=$1`, skillID,
		).Scan(&row.ID, &row.Name, &row.Description, &row.Status, &row.ActiveRevisionID, &row.DraftRevisionID, &row.CreatedBy)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	return row, found, err
}

func (r *PgSkillRevisionRepo) ListSkills(ctx context.Context) ([]port.SkillProductRow, error) {
	var result []port.SkillProductRow
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, description, status,
			COALESCE(active_revision_id, ''), COALESCE(draft_revision_id, ''), COALESCE(created_by, '') FROM skills ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item port.SkillProductRow
			if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Status,
				&item.ActiveRevisionID, &item.DraftRevisionID, &item.CreatedBy); err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	return result, err
}

func (r *PgSkillRevisionRepo) DeleteSkill(ctx context.Context, skillID string, audit *auditdomain.ResourceChangeAuditEvent) error {
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM skills WHERE id=$1`, skillID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrSkillNotFound
		}
		// Editors die with the resource, same transaction (idempotent).
		if _, err := tx.Exec(ctx,
			`DELETE FROM resource_editors WHERE resource_kind=$1 AND resource_id=$2`,
			resourceEditorKind, skillID,
		); err != nil {
			return fmt.Errorf("delete skill editors: %w", err)
		}
		return insertChangeAudit(ctx, tx, audit)
	})
}

const revisionColumns = `id, skill_id, COALESCE(parent_revision_id, ''), COALESCE(revision_no, 0), status,
	source, content_hash, generation_metadata, name, description, instructions, publish_checks,
	created_by, created_at, published_at`

func (r *PgSkillRevisionRepo) GetActiveRevision(ctx context.Context, skillID string) (domain.SkillRevision, bool, error) {
	return r.getRevision(ctx, `SELECT `+prefixedRevisionColumns("r")+`
		FROM skill_revisions r JOIN skills s ON s.active_revision_id=r.id WHERE s.id=$1`, skillID)
}

func (r *PgSkillRevisionRepo) GetRevision(ctx context.Context, skillID, revisionID string) (domain.SkillRevision, bool, error) {
	return r.getRevision(ctx, `SELECT `+revisionColumns+` FROM skill_revisions WHERE skill_id=$1 AND id=$2`, skillID, revisionID)
}

func (r *PgSkillRevisionRepo) getRevision(ctx context.Context, query string, args ...any) (domain.SkillRevision, bool, error) {
	var revision domain.SkillRevision
	found := false
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		value, err := scanSkillRevision(tx.QueryRow(ctx, query, args...))
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		revision, found = value, true
		return nil
	})
	return revision, found, err
}

func (r *PgSkillRevisionRepo) InsertCandidate(ctx context.Context, candidate domain.SkillRevision, audit *auditdomain.ResourceChangeAuditEvent) error {
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := insertSkillRevision(ctx, tx, candidate); err != nil {
			return err
		}
		return insertChangeAudit(ctx, tx, audit)
	})
}

func (r *PgSkillRevisionRepo) SaveRevision(
	ctx context.Context,
	skillID, expectedContentHash string,
	skill port.SkillProductRow,
	revision domain.SkillRevision,
	audit *auditdomain.ResourceChangeAuditEvent,
	editorActor string,
) (domain.SkillRevision, error) {
	var saved domain.SkillRevision
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := revalidateEditorIfActor(ctx, tx, resourceEditorKind, skillID, editorActor); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM skills WHERE id=$1)`, skillID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return domain.ErrSkillNotFound
		}
		// 乐观并发：期望内容哈希必须匹配当前生效版本。空 baseline 表示直写
		// (或存量未发布 skill 的首版保存),跳过校验。
		if expectedContentHash != "" {
			var activeHash string
			if err := tx.QueryRow(ctx,
				`SELECT COALESCE(r.content_hash, '') FROM skills s
				 LEFT JOIN skill_revisions r ON r.id=s.active_revision_id WHERE s.id=$1`, skillID,
			).Scan(&activeHash); err != nil {
				return err
			}
			if activeHash != expectedContentHash {
				return domain.ErrSkillDraftStale
			}
		}
		// 旧生效版本降级,新版本立即生效(保存即生效,无发布步骤)。
		if _, err := tx.Exec(ctx,
			`UPDATE skill_revisions SET status='deprecated', updated_at=NOW() WHERE skill_id=$1 AND status='published'`, skillID,
		); err != nil {
			return err
		}
		if err := insertSkillRevision(ctx, tx, revision); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE skills SET name=$2, description=$3, status='published', active_revision_id=$4, updated_at=NOW() WHERE id=$1`,
			skillID, skill.Name, skill.Description, revision.ID,
		); err != nil {
			return err
		}
		saved = revision
		return insertChangeAudit(ctx, tx, audit)
	})
	return saved, err
}

func (r *PgSkillRevisionRepo) NextRevisionNo(ctx context.Context, skillID string) (int, error) {
	var next int
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(revision_no), 0) + 1 FROM skill_revisions WHERE skill_id=$1`, skillID,
		).Scan(&next)
	})
	return next, err
}

func (r *PgSkillRevisionRepo) RollbackRevision(
	ctx context.Context, skillID, targetRevisionID, actorID string, audit *auditdomain.ResourceChangeAuditEvent,
) error {
	return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if actorID != "" {
			tc, ok := tenantdb.FromContext(ctx)
			if !ok || tc.TenantID == "" {
				return fmt.Errorf("skill_revision_repo: missing tenant context")
			}
			if err := revalidateEditorAccess(ctx, tx, tc.TenantID, resourceEditorKind, skillID, actorID); err != nil {
				return err
			}
		}
		// 当前生效版本降级。
		if _, err := tx.Exec(ctx,
			`UPDATE skill_revisions SET status='deprecated', updated_at=NOW() WHERE skill_id=$1 AND status='published'`, skillID,
		); err != nil {
			return err
		}
		// 目标历史版本恢复为生效版本;必须是该 skill 的 deprecated 历史版本。
		tag, err := tx.Exec(ctx,
			`UPDATE skill_revisions SET status='published', published_at=NOW(), updated_at=NOW()
			 WHERE id=$1 AND skill_id=$2 AND status='deprecated'`,
			targetRevisionID, skillID,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrSkillNotFound
		}
		if _, err := tx.Exec(ctx,
			`UPDATE skills SET active_revision_id=$2, status='published', updated_at=NOW() WHERE id=$1`,
			skillID, targetRevisionID,
		); err != nil {
			return err
		}
		return insertChangeAudit(ctx, tx, audit)
	})
}

// listRevisionsSQL 返回版本历史(新→旧),is_current 由 skills.active_revision_id 推导。
const listRevisionsSQL = `SELECT r.id, r.skill_id, COALESCE(r.parent_revision_id, ''), COALESCE(r.revision_no, 0),
	r.status, r.source, r.content_hash, r.generation_metadata, r.name, r.description,
	r.instructions, r.publish_checks, r.created_by, r.created_at, r.published_at,
	COALESCE(s.active_revision_id = r.id, false) AS is_current
FROM skill_revisions r
JOIN skills s ON s.id = r.skill_id
WHERE r.skill_id=$1
ORDER BY r.revision_no DESC NULLS LAST, r.created_at DESC`

func (r *PgSkillRevisionRepo) ListRevisions(ctx context.Context, skillID string) ([]domain.SkillRevision, bool, error) {
	var result []domain.SkillRevision
	skillExists := false
	err := r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM skills WHERE id=$1)`, skillID).Scan(&exists); err != nil {
			return err
		}
		skillExists = exists
		if !exists {
			return nil
		}
		rows, err := tx.Query(ctx, listRevisionsSQL, skillID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			value, err := scanRevisionWithCurrent(rows)
			if err != nil {
				return err
			}
			result = append(result, value)
		}
		return rows.Err()
	})
	return result, skillExists, err
}

func insertSkillRevision(ctx context.Context, tx pgx.Tx, revision domain.SkillRevision) error {
	generationJSON, err := json.Marshal(revision.GenerationMetadata)
	if err != nil {
		return err
	}
	checksJSON, err := json.Marshal(revision.PublishChecks)
	if err != nil {
		return err
	}
	source := revision.Source
	if source == "" {
		source = "manual"
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO skill_revisions
		 (id, skill_id, parent_revision_id, revision_no, status, source, content_hash,
		  generation_metadata, name, description, instructions, publish_checks, created_by,
		  published_at)
		 VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, 0), $5, $6, $7, $8, $9, $10, $11, $12, $13,
		         CASE WHEN $5 = 'published' THEN NOW() ELSE NULL END)`,
		revision.ID, revision.SkillID, revision.ParentRevisionID, revision.RevisionNo, string(revision.Status), source,
		revision.ContentHash, string(generationJSON), revision.Name, revision.Description,
		revision.Instructions, string(checksJSON), revision.CreatedBy,
	)
	return err
}

type revisionScanner interface{ Scan(dest ...any) error }

func scanSkillRevision(row revisionScanner) (domain.SkillRevision, error) {
	return scanRevision(row, false)
}

// scanRevisionWithCurrent additionally scans the is_current flag produced by
// ListRevisions' skills.active_revision_id join.
func scanRevisionWithCurrent(row revisionScanner) (domain.SkillRevision, error) {
	return scanRevision(row, true)
}

func scanRevision(row revisionScanner, withCurrent bool) (domain.SkillRevision, error) {
	var revision domain.SkillRevision
	var status string
	var generationJSON, checksJSON []byte
	var publishedAt *time.Time
	dest := []any{
		&revision.ID, &revision.SkillID, &revision.ParentRevisionID, &revision.RevisionNo, &status,
		&revision.Source, &revision.ContentHash, &generationJSON,
		&revision.Name, &revision.Description, &revision.Instructions, &checksJSON,
		&revision.CreatedBy, &revision.CreatedAt, &publishedAt,
	}
	if withCurrent {
		dest = append(dest, &revision.IsCurrent)
	}
	if err := row.Scan(dest...); err != nil {
		return domain.SkillRevision{}, err
	}
	revision.Status = domain.VersionStatus(status)
	revision.PublishedAt = publishedAt
	_ = json.Unmarshal(generationJSON, &revision.GenerationMetadata)
	_ = json.Unmarshal(checksJSON, &revision.PublishChecks)
	return revision, nil
}

func prefixedRevisionColumns(alias string) string {
	return fmt.Sprintf(`%[1]s.id, %[1]s.skill_id, COALESCE(%[1]s.parent_revision_id, ''),
		COALESCE(%[1]s.revision_no, 0), %[1]s.status, %[1]s.source, %[1]s.content_hash,
		%[1]s.generation_metadata, %[1]s.name, %[1]s.description, %[1]s.instructions,
		%[1]s.publish_checks, %[1]s.created_by, %[1]s.created_at, %[1]s.published_at`, alias)
}
