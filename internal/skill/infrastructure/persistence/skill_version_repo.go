package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PgSkillRevisionRepo struct{ pool *pgxpool.Pool }

func NewPgSkillRevisionRepo(pool *pgxpool.Pool) *PgSkillRevisionRepo {
	return &PgSkillRevisionRepo{pool: pool}
}

func (r *PgSkillRevisionRepo) InsertSkillWithDraft(
	ctx context.Context, skill port.SkillProductRow, draft domain.SkillRevision,
) error {
	return tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO skills (id, name, description, status, draft_revision_id)
			 VALUES ($1, $2, $3, 'draft', $4)`,
			skill.ID, skill.Name, skill.Description, draft.ID,
		); err != nil {
			return err
		}
		return insertSkillRevision(ctx, tx, draft)
	})
}

func (r *PgSkillRevisionRepo) GetSkill(ctx context.Context, skillID string) (port.SkillProductRow, bool, error) {
	var row port.SkillProductRow
	found := false
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT id, name, description, status,
			        COALESCE(active_revision_id, ''), COALESCE(draft_revision_id, '')
			 FROM skills WHERE id=$1`, skillID,
		).Scan(&row.ID, &row.Name, &row.Description, &row.Status, &row.ActiveRevisionID, &row.DraftRevisionID)
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
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT id, name, description, status,
			COALESCE(active_revision_id, ''), COALESCE(draft_revision_id, '') FROM skills ORDER BY name`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item port.SkillProductRow
			if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Status,
				&item.ActiveRevisionID, &item.DraftRevisionID); err != nil {
				return err
			}
			result = append(result, item)
		}
		return rows.Err()
	})
	return result, err
}

func (r *PgSkillRevisionRepo) DeleteSkill(ctx context.Context, skillID string) error {
	return tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `DELETE FROM skills WHERE id=$1`, skillID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrSkillNotFound
		}
		return nil
	})
}

const revisionColumns = `id, skill_id, COALESCE(parent_revision_id, ''), COALESCE(revision_no, 0), status,
	source, content_hash, generation_metadata, capability, activation_contract, instructions, requirements, publish_checks`

func (r *PgSkillRevisionRepo) GetDraftRevision(ctx context.Context, skillID string) (domain.SkillRevision, bool, error) {
	return r.getRevision(ctx, `SELECT `+revisionColumns+` FROM skill_revisions WHERE skill_id=$1 AND status='draft'`, skillID)
}

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
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
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

func (r *PgSkillRevisionRepo) InsertCandidate(ctx context.Context, candidate domain.SkillRevision) error {
	return tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		return insertSkillRevision(ctx, tx, candidate)
	})
}

func (r *PgSkillRevisionRepo) UpdateDraftCapability(
	ctx context.Context, skillID string, capability domain.Capability, contentHash string,
) (domain.SkillRevision, error) {
	payload, err := json.Marshal(capability)
	if err != nil {
		return domain.SkillRevision{}, fmt.Errorf("skill_revision_repo: marshal capability: %w", err)
	}
	revision, err := r.updateDraft(ctx, skillID, "capability=$2", []any{string(payload)}, contentHash)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	_ = tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE skills SET description=$2, updated_at=NOW() WHERE id=$1`, skillID, capability.Goal)
		return err
	})
	return revision, nil
}

func (r *PgSkillRevisionRepo) UpdateDraftActivation(
	ctx context.Context, skillID string, contract domain.ActivationContract, contentHash string,
) (domain.SkillRevision, error) {
	payload, err := json.Marshal(contract)
	if err != nil {
		return domain.SkillRevision{}, fmt.Errorf("skill_revision_repo: marshal activation contract: %w", err)
	}
	return r.updateDraft(ctx, skillID, "activation_contract=$2", []any{string(payload)}, contentHash)
}

func (r *PgSkillRevisionRepo) UpdateDraftInstructions(
	ctx context.Context, skillID, instructions string, requirements domain.Requirements, contentHash string,
) (domain.SkillRevision, error) {
	payload, err := json.Marshal(requirements)
	if err != nil {
		return domain.SkillRevision{}, fmt.Errorf("skill_revision_repo: marshal requirements: %w", err)
	}
	return r.updateDraft(ctx, skillID, "instructions=$2, requirements=$3", []any{instructions, string(payload)}, contentHash)
}

func (r *PgSkillRevisionRepo) updateDraft(
	ctx context.Context, skillID, assignments string, values []any, contentHash string,
) (domain.SkillRevision, error) {
	var revision domain.SkillRevision
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		args := append([]any{skillID}, values...)
		args = append(args, contentHash)
		hashArg := len(args)
		query := fmt.Sprintf(`UPDATE skill_revisions SET %s, content_hash=$%d, updated_at=NOW()
			WHERE skill_id=$1 AND status='draft' RETURNING %s`, assignments, hashArg, revisionColumns)
		value, err := scanSkillRevision(tx.QueryRow(ctx, query, args...))
		if err != nil {
			return err
		}
		revision = value
		return nil
	})
	return revision, err
}

func (r *PgSkillRevisionRepo) NextRevisionNo(ctx context.Context, skillID string) (int, error) {
	var next int
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(revision_no), 0) + 1 FROM skill_revisions WHERE skill_id=$1`, skillID,
		).Scan(&next)
	})
	return next, err
}

func (r *PgSkillRevisionRepo) PublishDraft(
	ctx context.Context, skillID, draftRevisionID string, nextRevisionNo int, checks map[string]any,
) (domain.SkillRevision, error) {
	var revision domain.SkillRevision
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		checksJSON, err := json.Marshal(checks)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`UPDATE skill_revisions SET status='deprecated' WHERE skill_id=$1 AND status='published'`, skillID,
		); err != nil {
			return err
		}
		value, err := scanSkillRevision(tx.QueryRow(ctx,
			`UPDATE skill_revisions SET status='published', revision_no=$3, publish_checks=$4,
			 published_at=NOW(), updated_at=NOW() WHERE id=$1 AND skill_id=$2 AND status='draft'
			 RETURNING `+revisionColumns,
			draftRevisionID, skillID, nextRevisionNo, string(checksJSON),
		))
		if err != nil {
			return err
		}
		revision = value
		_, err = tx.Exec(ctx,
			`UPDATE skills SET status='published', active_revision_id=$2, draft_revision_id=NULL, updated_at=NOW() WHERE id=$1`,
			skillID, draftRevisionID,
		)
		return err
	})
	return revision, err
}

func insertSkillRevision(ctx context.Context, tx pgx.Tx, revision domain.SkillRevision) error {
	generationJSON, err := json.Marshal(revision.GenerationMetadata)
	if err != nil {
		return err
	}
	capabilityJSON, err := json.Marshal(revision.Capability)
	if err != nil {
		return err
	}
	activationJSON, err := json.Marshal(revision.ActivationContract)
	if err != nil {
		return err
	}
	requirementsJSON, err := json.Marshal(revision.Requirements)
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
		  generation_metadata, capability, activation_contract, instructions, requirements, publish_checks)
		 VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, 0), $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		revision.ID, revision.SkillID, revision.ParentRevisionID, revision.RevisionNo, string(revision.Status), source,
		revision.ContentHash, string(generationJSON), string(capabilityJSON), string(activationJSON),
		revision.Instructions, string(requirementsJSON), string(checksJSON),
	)
	return err
}

type revisionScanner interface{ Scan(dest ...any) error }

func scanSkillRevision(row revisionScanner) (domain.SkillRevision, error) {
	var revision domain.SkillRevision
	var status string
	var generationJSON, capabilityJSON, activationJSON, requirementsJSON, checksJSON []byte
	if err := row.Scan(
		&revision.ID, &revision.SkillID, &revision.ParentRevisionID, &revision.RevisionNo, &status,
		&revision.Source, &revision.ContentHash, &generationJSON, &capabilityJSON, &activationJSON,
		&revision.Instructions, &requirementsJSON, &checksJSON,
	); err != nil {
		return domain.SkillRevision{}, err
	}
	revision.Status = domain.VersionStatus(status)
	_ = json.Unmarshal(generationJSON, &revision.GenerationMetadata)
	_ = json.Unmarshal(capabilityJSON, &revision.Capability)
	_ = json.Unmarshal(activationJSON, &revision.ActivationContract)
	_ = json.Unmarshal(requirementsJSON, &revision.Requirements)
	_ = json.Unmarshal(checksJSON, &revision.PublishChecks)
	return revision, nil
}

func prefixedRevisionColumns(alias string) string {
	return fmt.Sprintf(`%[1]s.id, %[1]s.skill_id, COALESCE(%[1]s.parent_revision_id, ''),
		COALESCE(%[1]s.revision_no, 0), %[1]s.status, %[1]s.source, %[1]s.content_hash,
		%[1]s.generation_metadata, %[1]s.capability, %[1]s.activation_contract,
		%[1]s.instructions, %[1]s.requirements, %[1]s.publish_checks`, alias)
}
