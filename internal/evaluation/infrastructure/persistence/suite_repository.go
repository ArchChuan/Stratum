package persistence

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PgSuiteRepository struct {
	pool *pgxpool.Pool
}

func NewPgSuiteRepository(pool *pgxpool.Pool) *PgSuiteRepository {
	return &PgSuiteRepository{pool: pool}
}

func (r *PgSuiteRepository) CreateSuite(
	ctx context.Context,
	tenantID string,
	suite domain.EvalSuite,
	revision domain.EvalSuiteRevision,
) error {
	return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO eval_suites (id, name, description, draft_revision_id) VALUES ($1,$2,$3,$4)`,
			suite.ID, suite.Name, suite.Description, revision.ID,
		); err != nil {
			return fmt.Errorf("evaluation suite repository: insert suite: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO eval_suite_revisions (id, suite_id, parent_id, version_no, status, resource_kind)
			 VALUES ($1,$2,NULLIF($3,''),NULLIF($4,0),$5,$6)`,
			revision.ID, revision.SuiteID, revision.ParentID, revision.VersionNo,
			string(revision.Status), string(revision.ResourceKind),
		); err != nil {
			return fmt.Errorf("evaluation suite repository: insert revision: %w", err)
		}
		for _, testCase := range revision.Cases {
			if err := insertEvalCase(ctx, tx, revision.ID, testCase); err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *PgSuiteRepository) GetDraftRevision(
	ctx context.Context,
	tenantID, suiteID string,
) (domain.EvalSuiteRevision, bool, error) {
	var revision domain.EvalSuiteRevision
	found := false
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		revision, found, err = loadSuiteRevision(ctx, tx,
			`SELECT id, suite_id, COALESCE(parent_id, ''), COALESCE(version_no, 0), status, resource_kind
			 FROM eval_suite_revisions WHERE suite_id=$1 AND status='draft'`, suiteID)
		return err
	})
	return revision, found, err
}

func (r *PgSuiteRepository) GetRevision(
	ctx context.Context,
	tenantID, revisionID string,
) (domain.EvalSuiteRevision, bool, error) {
	var revision domain.EvalSuiteRevision
	found := false
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		revision, found, err = loadSuiteRevision(ctx, tx,
			`SELECT id, suite_id, COALESCE(parent_id, ''), COALESCE(version_no, 0), status, resource_kind
			 FROM eval_suite_revisions WHERE id=$1`, revisionID)
		return err
	})
	return revision, found, err
}

func (r *PgSuiteRepository) NextVersionNo(ctx context.Context, tenantID, suiteID string) (int, error) {
	next := 0
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(version_no), 0) + 1 FROM eval_suite_revisions WHERE suite_id=$1`, suiteID,
		).Scan(&next)
	})
	return next, err
}

func (r *PgSuiteRepository) PublishRevision(
	ctx context.Context,
	tenantID, suiteID, revisionID string,
	versionNo int,
) (domain.EvalSuiteRevision, error) {
	var revision domain.EvalSuiteRevision
	err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE eval_suite_revisions
			 SET status='published', version_no=$3, published_at=NOW()
			 WHERE id=$1 AND suite_id=$2 AND status='draft'`, revisionID, suiteID, versionNo)
		if err != nil {
			return fmt.Errorf("evaluation suite repository: publish revision: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("evaluation suite repository: draft revision not found")
		}
		if _, err := tx.Exec(ctx,
			`UPDATE eval_suites SET active_revision_id=$2, draft_revision_id=NULL, updated_at=NOW() WHERE id=$1`,
			suiteID, revisionID,
		); err != nil {
			return fmt.Errorf("evaluation suite repository: activate revision: %w", err)
		}
		var found bool
		revision, found, err = loadSuiteRevision(ctx, tx,
			`SELECT id, suite_id, COALESCE(parent_id, ''), COALESCE(version_no, 0), status, resource_kind
			 FROM eval_suite_revisions WHERE id=$1`, revisionID)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("evaluation suite repository: published revision not found")
		}
		return nil
	})
	return revision, err
}

func (r *PgSuiteRepository) execTenant(
	ctx context.Context,
	tenantID string,
	fn func(context.Context, pgx.Tx) error,
) error {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	return tenantdb.ExecTenant(ctx, r.pool, fn)
}

func insertEvalCase(ctx context.Context, tx pgx.Tx, revisionID string, testCase domain.EvalCase) error {
	inputJSON, err := json.Marshal(testCase.Input)
	if err != nil {
		return fmt.Errorf("evaluation suite repository: marshal input: %w", err)
	}
	expectedJSON, err := json.Marshal(testCase.ExpectedOutput)
	if err != nil {
		return fmt.Errorf("evaluation suite repository: marshal expected output: %w", err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO eval_cases
		 (id, suite_revision_id, name, input, expected_output, assertion_mode, enabled)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		testCase.ID, revisionID, testCase.Name, string(inputJSON), string(expectedJSON),
		string(testCase.AssertionMode), testCase.Enabled,
	)
	if err != nil {
		return fmt.Errorf("evaluation suite repository: insert case: %w", err)
	}
	return nil
}

func loadSuiteRevision(
	ctx context.Context,
	tx pgx.Tx,
	query string,
	arg string,
) (domain.EvalSuiteRevision, bool, error) {
	var revision domain.EvalSuiteRevision
	var status, kind string
	err := tx.QueryRow(ctx, query, arg).Scan(
		&revision.ID, &revision.SuiteID, &revision.ParentID, &revision.VersionNo, &status, &kind,
	)
	if err == pgx.ErrNoRows {
		return domain.EvalSuiteRevision{}, false, nil
	}
	if err != nil {
		return domain.EvalSuiteRevision{}, false, err
	}
	revision.Status = domain.SuiteRevisionStatus(status)
	revision.ResourceKind = domain.ResourceKind(kind)
	rows, err := tx.Query(ctx,
		`SELECT id, name, input, expected_output, assertion_mode, enabled
		 FROM eval_cases WHERE suite_revision_id=$1 ORDER BY created_at, id`, revision.ID)
	if err != nil {
		return domain.EvalSuiteRevision{}, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var testCase domain.EvalCase
		var inputJSON, expectedJSON []byte
		var mode string
		if err := rows.Scan(&testCase.ID, &testCase.Name, &inputJSON, &expectedJSON, &mode, &testCase.Enabled); err != nil {
			return domain.EvalSuiteRevision{}, false, err
		}
		testCase.AssertionMode = domain.AssertionMode(mode)
		_ = json.Unmarshal(inputJSON, &testCase.Input)
		_ = json.Unmarshal(expectedJSON, &testCase.ExpectedOutput)
		revision.Cases = append(revision.Cases, testCase)
	}
	return revision, true, rows.Err()
}
