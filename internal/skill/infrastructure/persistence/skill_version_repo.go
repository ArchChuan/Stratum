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

type PgSkillVersionRepo struct {
	pool *pgxpool.Pool
}

func NewPgSkillVersionRepo(pool *pgxpool.Pool) *PgSkillVersionRepo {
	return &PgSkillVersionRepo{pool: pool}
}

func (r *PgSkillVersionRepo) InsertSkillWithDraft(
	ctx context.Context,
	skill port.SkillProductRow,
	draft domain.SkillVersion,
	firstCase port.SkillTestCaseRow,
) error {
	return tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO skills (id, name, description, type, config, draft_version_id)
			 VALUES ($1, $2, $3, 'capability', '{}', $4)`,
			skill.ID, skill.Name, skill.Description, draft.ID,
		); err != nil {
			return err
		}
		if err := insertSkillVersion(ctx, tx, draft); err != nil {
			return err
		}
		return insertSkillTestCase(ctx, tx, firstCase)
	})
}

func (r *PgSkillVersionRepo) GetSkill(ctx context.Context, skillID string) (port.SkillProductRow, bool, error) {
	var row port.SkillProductRow
	found := false
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT id, name, description, COALESCE(active_version_id, ''), COALESCE(draft_version_id, '')
			 FROM skills WHERE id=$1`,
			skillID,
		).Scan(&row.ID, &row.Name, &row.Description, &row.ActiveVersionID, &row.DraftVersionID)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		found = true
		row.Status = skillStatus(row.ActiveVersionID, row.DraftVersionID)
		return nil
	})
	return row, found, err
}

func (r *PgSkillVersionRepo) GetDraftVersion(ctx context.Context, skillID string) (domain.SkillVersion, bool, error) {
	var version domain.SkillVersion
	found := false
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT id, skill_id, COALESCE(parent_version_id, ''), COALESCE(version_no, 0), status,
			        source, content_hash, generation_metadata, capability, tool_contract, implementation,
			        test_baseline, publish_checks
			 FROM skill_versions WHERE skill_id=$1 AND status='draft'`,
			skillID,
		)
		v, err := scanSkillVersion(row)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		version = v
		found = true
		return nil
	})
	return version, found, err
}

func (r *PgSkillVersionRepo) GetActiveVersion(ctx context.Context, skillID string) (domain.SkillVersion, bool, error) {
	var version domain.SkillVersion
	found := false
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT v.id, v.skill_id, COALESCE(v.parent_version_id, ''), COALESCE(v.version_no, 0), v.status,
			        v.source, v.content_hash, v.generation_metadata, v.capability, v.tool_contract,
			        v.implementation, v.test_baseline, v.publish_checks
			 FROM skill_versions v
			 JOIN skills s ON s.active_version_id = v.id
			 WHERE s.id=$1`,
			skillID,
		)
		v, err := scanSkillVersion(row)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		version = v
		found = true
		return nil
	})
	return version, found, err
}

func (r *PgSkillVersionRepo) GetVersion(
	ctx context.Context,
	skillID, versionID string,
) (domain.SkillVersion, bool, error) {
	var version domain.SkillVersion
	found := false
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT id, skill_id, COALESCE(parent_version_id, ''), COALESCE(version_no, 0), status,
			        source, content_hash, generation_metadata, capability, tool_contract, implementation,
			        test_baseline, publish_checks
			 FROM skill_versions WHERE id=$1 AND skill_id=$2`, versionID, skillID)
		v, err := scanSkillVersion(row)
		if err == pgx.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		version = v
		found = true
		return nil
	})
	return version, found, err
}

func (r *PgSkillVersionRepo) InsertCandidate(ctx context.Context, candidate domain.SkillVersion) error {
	return tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		return insertSkillVersion(ctx, tx, candidate)
	})
}

func (r *PgSkillVersionRepo) UpdateDraftCapability(
	ctx context.Context, skillID string, capability domain.Capability, contentHash string,
) (domain.SkillVersion, error) {
	var version domain.SkillVersion
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		capabilityJSON, err := json.Marshal(capability)
		if err != nil {
			return fmt.Errorf("skill_version_repo: marshal capability: %w", err)
		}
		row := tx.QueryRow(ctx,
			`UPDATE skill_versions
			 SET capability=$2, content_hash=$3, updated_at=NOW()
			 WHERE skill_id=$1 AND status='draft'
			 RETURNING id, skill_id, COALESCE(parent_version_id, ''), COALESCE(version_no, 0), status,
			           source, content_hash, generation_metadata, capability, tool_contract, implementation,
			           test_baseline, publish_checks`,
			skillID, string(capabilityJSON), contentHash,
		)
		v, err := scanSkillVersion(row)
		if err != nil {
			return err
		}
		version = v
		_, err = tx.Exec(ctx, `UPDATE skills SET description=$2, updated_at=NOW() WHERE id=$1`, skillID, capability.Goal)
		return err
	})
	return version, err
}

func (r *PgSkillVersionRepo) UpdateDraftContract(
	ctx context.Context, skillID string, contract domain.ToolContract, contentHash string,
) (domain.SkillVersion, error) {
	payload, err := json.Marshal(contract)
	if err != nil {
		return domain.SkillVersion{}, fmt.Errorf("skill_version_repo: marshal contract: %w", err)
	}
	return r.updateDraftJSON(ctx, skillID, "tool_contract", string(payload), contentHash)
}

func (r *PgSkillVersionRepo) UpdateDraftImplementation(
	ctx context.Context, skillID string, implementation domain.Implementation, contentHash string,
) (domain.SkillVersion, error) {
	payload, err := json.Marshal(implementation)
	if err != nil {
		return domain.SkillVersion{}, fmt.Errorf("skill_version_repo: marshal implementation: %w", err)
	}
	return r.updateDraftJSON(ctx, skillID, "implementation", string(payload), contentHash)
}

func (r *PgSkillVersionRepo) updateDraftJSON(
	ctx context.Context, skillID, column, payload, contentHash string,
) (domain.SkillVersion, error) {
	var version domain.SkillVersion
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			fmt.Sprintf(`UPDATE skill_versions
			 SET %s=$2, content_hash=$3, updated_at=NOW()
			 WHERE skill_id=$1 AND status='draft'
			 RETURNING id, skill_id, COALESCE(parent_version_id, ''), COALESCE(version_no, 0), status,
			           source, content_hash, generation_metadata, capability, tool_contract, implementation,
			           test_baseline, publish_checks`, column),
			skillID, payload, contentHash,
		)
		v, err := scanSkillVersion(row)
		if err != nil {
			return err
		}
		version = v
		return nil
	})
	return version, err
}

func (r *PgSkillVersionRepo) CountEnabledTestCases(ctx context.Context, skillID string) (int, error) {
	var count int
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT COUNT(*) FROM skill_test_cases WHERE skill_id=$1 AND enabled=true`, skillID).Scan(&count)
	})
	return count, err
}

func (r *PgSkillVersionRepo) NextVersionNo(ctx context.Context, skillID string) (int, error) {
	var next int
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(version_no), 0) + 1 FROM skill_versions WHERE skill_id=$1`,
			skillID,
		).Scan(&next)
	})
	return next, err
}

func (r *PgSkillVersionRepo) PublishDraft(
	ctx context.Context,
	skillID string,
	draftVersionID string,
	nextVersionNo int,
	baseline map[string]any,
) (domain.SkillVersion, error) {
	var version domain.SkillVersion
	err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		baselineJSON, err := json.Marshal(baseline)
		if err != nil {
			return fmt.Errorf("skill_version_repo: marshal baseline: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE skill_versions
			 SET status='deprecated'
			 WHERE skill_id=$1 AND status='published'`,
			skillID,
		); err != nil {
			return err
		}
		row := tx.QueryRow(ctx,
			`UPDATE skill_versions
			 SET status='published', version_no=$3, test_baseline=$4, published_at=NOW(), updated_at=NOW()
			 WHERE id=$1 AND skill_id=$2 AND status='draft'
			 RETURNING id, skill_id, COALESCE(parent_version_id, ''), COALESCE(version_no, 0), status,
			           source, content_hash, generation_metadata, capability, tool_contract, implementation,
			           test_baseline, publish_checks`,
			draftVersionID, skillID, nextVersionNo, string(baselineJSON),
		)
		v, err := scanSkillVersion(row)
		if err != nil {
			return err
		}
		version = v
		_, err = tx.Exec(ctx,
			`UPDATE skills SET active_version_id=$2, draft_version_id=NULL, updated_at=NOW() WHERE id=$1`,
			skillID, draftVersionID,
		)
		return err
	})
	return version, err
}

func insertSkillVersion(ctx context.Context, tx pgx.Tx, version domain.SkillVersion) error {
	capabilityJSON, err := json.Marshal(version.Capability)
	if err != nil {
		return err
	}
	contractJSON, err := json.Marshal(version.ToolContract)
	if err != nil {
		return err
	}
	implementationJSON, err := json.Marshal(version.Implementation)
	if err != nil {
		return err
	}
	baselineJSON, err := json.Marshal(version.TestBaseline)
	if err != nil {
		return err
	}
	checksJSON, err := json.Marshal(version.PublishChecks)
	if err != nil {
		return err
	}
	generationJSON, err := json.Marshal(version.GenerationMetadata)
	if err != nil {
		return err
	}
	source := version.Source
	if source == "" {
		source = "manual"
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO skill_versions
		 (id, skill_id, parent_version_id, version_no, status, source, content_hash, generation_metadata,
		  capability, tool_contract, implementation, test_baseline, publish_checks)
		 VALUES ($1, $2, NULLIF($3, ''), NULLIF($4, 0), $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
		version.ID, version.SkillID, version.ParentVersionID, version.VersionNo, string(version.Status), source,
		version.ContentHash, string(generationJSON), string(capabilityJSON), string(contractJSON),
		string(implementationJSON), string(baselineJSON), string(checksJSON),
	)
	return err
}

func insertSkillTestCase(ctx context.Context, tx pgx.Tx, testCase port.SkillTestCaseRow) error {
	inputJSON, err := json.Marshal(testCase.Input)
	if err != nil {
		return err
	}
	expectedJSON, err := json.Marshal(testCase.ExpectedOutput)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO skill_test_cases (id, skill_id, name, input, expected_output, assertion_mode, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		testCase.ID, testCase.SkillID, testCase.Name, string(inputJSON), string(expectedJSON),
		testCase.AssertionMode, testCase.Enabled,
	)
	return err
}

type versionScanner interface {
	Scan(dest ...any) error
}

func scanSkillVersion(row versionScanner) (domain.SkillVersion, error) {
	var version domain.SkillVersion
	var status string
	var generationJSON, capabilityJSON, contractJSON, implementationJSON, baselineJSON, checksJSON []byte
	if err := row.Scan(
		&version.ID,
		&version.SkillID,
		&version.ParentVersionID,
		&version.VersionNo,
		&status,
		&version.Source,
		&version.ContentHash,
		&generationJSON,
		&capabilityJSON,
		&contractJSON,
		&implementationJSON,
		&baselineJSON,
		&checksJSON,
	); err != nil {
		return domain.SkillVersion{}, err
	}
	version.Status = domain.VersionStatus(status)
	_ = json.Unmarshal(generationJSON, &version.GenerationMetadata)
	_ = json.Unmarshal(capabilityJSON, &version.Capability)
	_ = json.Unmarshal(contractJSON, &version.ToolContract)
	_ = json.Unmarshal(implementationJSON, &version.Implementation)
	_ = json.Unmarshal(baselineJSON, &version.TestBaseline)
	_ = json.Unmarshal(checksJSON, &version.PublishChecks)
	return version, nil
}

func skillStatus(activeVersionID, draftVersionID string) string {
	switch {
	case activeVersionID != "" && draftVersionID != "":
		return "published_with_draft"
	case activeVersionID != "":
		return "published"
	default:
		return "draft"
	}
}
