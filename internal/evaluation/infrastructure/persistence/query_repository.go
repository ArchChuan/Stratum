package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PgCenterQueryRepository struct{ pool *pgxpool.Pool }

func NewPgCenterQueryRepository(pool *pgxpool.Pool) *PgCenterQueryRepository {
	return &PgCenterQueryRepository{pool: pool}
}

func (r *PgCenterQueryRepository) tenant(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) error {
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	return tenantdb.ExecTenant(ctx, r.pool, fn)
}

func (r *PgCenterQueryRepository) Overview(ctx context.Context, tenantID string) (domain.CenterOverview, error) {
	var result domain.CenterOverview
	err := r.tenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT
			(SELECT COUNT(DISTINCT (resource_kind,resource_id)) FROM resource_revisions),
			(SELECT COUNT(*) FROM eval_suites), (SELECT COUNT(*) FROM eval_runs),
			(SELECT COUNT(*) FROM optimization_candidates), (SELECT COUNT(*) FROM evaluation_experiments)`).Scan(
			&result.Resources, &result.Suites, &result.Runs, &result.Candidates, &result.Experiments)
	})
	return result, wrapCenterQuery("overview", err)
}

func cursorValues(raw string) (*time.Time, *string, error) {
	if raw == "" {
		return nil, nil, nil
	}
	cursor, err := domain.DecodeCenterCursor(raw)
	if err != nil {
		return nil, nil, err
	}
	return &cursor.CreatedAt, &cursor.ID, nil
}

func pageCursor(createdAt time.Time, id string) string {
	return domain.EncodeCenterCursor(createdAt, id)
}

const timelineCursorSeparator = "\x00"

func timelineCursorValues(raw string) (*time.Time, *string, *string, error) {
	createdAt, qualifiedID, err := cursorValues(raw)
	if err != nil || qualifiedID == nil {
		return createdAt, qualifiedID, nil, err
	}
	parts := strings.Split(*qualifiedID, timelineCursorSeparator)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, nil, nil, domain.ErrInvalidCenterQuery
	}
	return createdAt, &parts[0], &parts[1], nil
}

func timelinePageCursor(event domain.TimelineEvent) string {
	return pageCursor(event.CreatedAt, event.ID+timelineCursorSeparator+event.Kind)
}

func (r *PgCenterQueryRepository) ListResources(ctx context.Context, tenantID string, filter port.CenterFilter) (domain.ResourcePage, error) {
	var page domain.ResourcePage
	ct, cid, err := cursorValues(filter.Cursor)
	if err != nil {
		return page, err
	}
	err = r.tenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `WITH latest AS (
			SELECT DISTINCT ON (rr.resource_kind,rr.resource_id) rr.id,rr.resource_kind,rr.resource_id,rr.status,
				rr.safe_summary,rr.created_at,d.stable_revision_id,
				(SELECT er.status FROM eval_runs er WHERE er.resource_kind=rr.resource_kind AND er.resource_id=rr.resource_id ORDER BY er.created_at DESC,er.id DESC LIMIT 1) latest_run_status
			FROM resource_revisions rr LEFT JOIN evaluation_deployments d USING(resource_kind,resource_id)
			WHERE ($1='' OR rr.resource_kind=$1) AND ($2='' OR rr.resource_id=$2)
			ORDER BY rr.resource_kind,rr.resource_id,rr.created_at DESC,rr.id DESC)
		SELECT id,resource_kind,resource_id,status,safe_summary,created_at,COALESCE(stable_revision_id,''),COALESCE(latest_run_status,'')
		FROM latest WHERE ($3='' OR status=$3) AND ($4::timestamptz IS NULL OR (created_at,id)<($4,$5))
		ORDER BY created_at DESC,id DESC LIMIT $6`,
			filter.ResourceKind, filter.ResourceID, filter.Status, ct, cid, filter.Limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item domain.ResourceSummary
			var kind string
			var safe []byte
			if err := rows.Scan(&item.ID, &kind, &item.ResourceID, &item.Status, &safe, &item.CreatedAt, &item.StableRevisionID, &item.LatestRunStatus); err != nil {
				return err
			}
			item.ResourceKind = domain.ResourceKind(kind)
			item.SafeSummary = parseSanitizedSafeSummary(safe)
			page.Items = append(page.Items, item)
		}
		return rows.Err()
	})
	trimResources(&page, filter.Limit)
	return page, wrapCenterQuery("list resources", err)
}

func trimResources(p *domain.ResourcePage, limit int) {
	if len(p.Items) > limit {
		last := p.Items[limit-1]
		p.NextCursor = pageCursor(last.CreatedAt, last.ID)
		p.Items = p.Items[:limit]
	}
}

func (r *PgCenterQueryRepository) ListSuites(ctx context.Context, tenantID string, filter port.CenterFilter) (domain.SuitePage, error) {
	var page domain.SuitePage
	ct, cid, e := cursorValues(filter.Cursor)
	if e != nil {
		return page, e
	}
	e = r.tenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT s.id,s.name,s.description,COALESCE(sr.status,''),s.created_at FROM eval_suites s LEFT JOIN eval_suite_revisions sr ON sr.id=COALESCE(s.active_revision_id,s.draft_revision_id) WHERE ($1='' OR sr.resource_kind=$1) AND ($2='' OR EXISTS (SELECT 1 FROM eval_runs r WHERE r.suite_revision_id=sr.id AND r.resource_id=$2)) AND ($3='' OR sr.status=$3) AND ($4::timestamptz IS NULL OR (s.created_at,s.id)<($4,$5)) ORDER BY s.created_at DESC,s.id DESC LIMIT $6`, filter.ResourceKind, filter.ResourceID, filter.Status, ct, cid, filter.Limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var x domain.SuiteSummary
			if e = rows.Scan(&x.ID, &x.Name, &x.Description, &x.Status, &x.CreatedAt); e != nil {
				return e
			}
			page.Items = append(page.Items, x)
		}
		return rows.Err()
	})
	if len(page.Items) > filter.Limit {
		last := page.Items[filter.Limit-1]
		page.NextCursor = pageCursor(last.CreatedAt, last.ID)
		page.Items = page.Items[:filter.Limit]
	}
	return page, wrapCenterQuery("list suites", e)
}

func (r *PgCenterQueryRepository) ListRuns(ctx context.Context, tenantID string, filter port.CenterFilter) (domain.RunPage, error) {
	var page domain.RunPage
	ct, cid, e := cursorValues(filter.Cursor)
	if e != nil {
		return page, e
	}
	e = r.tenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT id,resource_kind,resource_id,revision_id,status,passed,total_cases,passed_cases,created_at FROM eval_runs WHERE ($1='' OR resource_kind=$1) AND ($2='' OR resource_id=$2) AND ($3='' OR status=$3) AND ($4::timestamptz IS NULL OR (created_at,id)<($4,$5)) ORDER BY created_at DESC,id DESC LIMIT $6`, filter.ResourceKind, filter.ResourceID, filter.Status, ct, cid, filter.Limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var x domain.RunSummary
			var kind string
			if e = rows.Scan(&x.ID, &kind, &x.ResourceID, &x.RevisionID, &x.Status, &x.Passed, &x.TotalCases, &x.PassedCases, &x.CreatedAt); e != nil {
				return e
			}
			x.ResourceKind = domain.ResourceKind(kind)
			page.Items = append(page.Items, x)
		}
		return rows.Err()
	})
	if len(page.Items) > filter.Limit {
		last := page.Items[filter.Limit-1]
		page.NextCursor = pageCursor(last.CreatedAt, last.ID)
		page.Items = page.Items[:filter.Limit]
	}
	return page, wrapCenterQuery("list runs", e)
}

func (r *PgCenterQueryRepository) ListCandidates(ctx context.Context, tenantID string, filter port.CenterFilter) (domain.CandidatePage, error) {
	var page domain.CandidatePage
	ct, cid, e := cursorValues(filter.Cursor)
	if e != nil {
		return page, e
	}
	e = r.tenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT c.id,j.resource_kind,j.resource_id,c.revision_id,c.parent_revision_id,c.source,c.status,c.rank,c.state_version,COALESCE(parent.safe_summary,'{}'::jsonb),parent.id IS NOT NULL,COALESCE(candidate.safe_summary,'{}'::jsonb),c.created_at FROM optimization_candidates c JOIN optimization_jobs j ON j.id=c.optimization_job_id LEFT JOIN resource_revisions parent ON parent.resource_kind=j.resource_kind AND parent.resource_id=j.resource_id AND parent.id=c.parent_revision_id LEFT JOIN resource_revisions candidate ON candidate.resource_kind=j.resource_kind AND candidate.resource_id=j.resource_id AND candidate.id=c.revision_id WHERE ($1='' OR j.resource_kind=$1) AND ($2='' OR j.resource_id=$2) AND ($3='' OR c.status=$3 OR j.status=$3) AND ($4::timestamptz IS NULL OR (c.created_at,c.id)<($4,$5)) ORDER BY c.created_at DESC,c.id DESC LIMIT $6`, filter.ResourceKind, filter.ResourceID, filter.Status, ct, cid, filter.Limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var x domain.CandidateSummary
			var kind string
			var parent, candidate []byte
			var parentExists bool
			if e = rows.Scan(&x.ID, &kind, &x.ResourceID, &x.RevisionID, &x.ParentRevisionID, &x.Source, &x.Status,
				&x.Rank, &x.StateVersion, &parent, &parentExists, &candidate, &x.CreatedAt); e != nil {
				return e
			}
			x.ResourceKind = domain.ResourceKind(kind)
			x.SafeDiff = buildCandidateSafeDiff(parseSanitizedSafeSummary(parent),
				parseSanitizedSafeSummary(candidate), parentExists)
			page.Items = append(page.Items, x)
		}
		return rows.Err()
	})
	if len(page.Items) > filter.Limit {
		last := page.Items[filter.Limit-1]
		page.NextCursor = pageCursor(last.CreatedAt, last.ID)
		page.Items = page.Items[:filter.Limit]
	}
	return page, wrapCenterQuery("list candidates", e)
}

func (r *PgCenterQueryRepository) ListExperiments(ctx context.Context, tenantID string, filter port.CenterFilter) (domain.ExperimentPage, error) {
	var page domain.ExperimentPage
	ct, cid, e := cursorValues(filter.Cursor)
	if e != nil {
		return page, e
	}
	e = r.tenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		rows, e := tx.Query(ctx, `SELECT id,resource_kind,resource_id,stable_revision_id,canary_revision_id,status,stage_percent,recommendation,safety_stopped,state_version,created_at FROM evaluation_experiments WHERE ($1='' OR resource_kind=$1) AND ($2='' OR resource_id=$2) AND ($3='' OR status=$3) AND ($4::timestamptz IS NULL OR (created_at,id)<($4,$5)) ORDER BY created_at DESC,id DESC LIMIT $6`, filter.ResourceKind, filter.ResourceID, filter.Status, ct, cid, filter.Limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var x domain.ExperimentSummary
			var kind string
			if e = rows.Scan(&x.ID, &kind, &x.ResourceID, &x.StableRevisionID, &x.CanaryRevisionID, &x.Status,
				&x.StagePercent, &x.Recommendation, &x.SafetyStopped, &x.StateVersion, &x.CreatedAt); e != nil {
				return e
			}
			x.ResourceKind = domain.ResourceKind(kind)
			page.Items = append(page.Items, x)
		}
		return rows.Err()
	})
	if len(page.Items) > filter.Limit {
		last := page.Items[filter.Limit-1]
		page.NextCursor = pageCursor(last.CreatedAt, last.ID)
		page.Items = page.Items[:filter.Limit]
	}
	return page, wrapCenterQuery("list experiments", e)
}

func (r *PgCenterQueryRepository) Timeline(ctx context.Context, tenantID string, filter port.CenterFilter) (domain.TimelinePage, error) {
	var page domain.TimelinePage
	ct, cid, ckind, e := timelineCursorValues(filter.Cursor)
	if e != nil {
		return page, e
	}
	e = r.tenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		var exists bool
		if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM resource_revisions WHERE resource_kind=$1 AND resource_id=$2)`, filter.ResourceKind, filter.ResourceID).Scan(&exists); e != nil {
			return e
		}
		if !exists {
			return port.ErrCenterResourceNotFound
		}
		rows, e := tx.Query(ctx, `WITH events AS (
		SELECT id,'revision' kind,status,'' summary,safe_summary,resource_kind,resource_id,created_at FROM resource_revisions WHERE resource_kind=$1 AND resource_id=$2
		UNION ALL SELECT id,'run',status,CASE WHEN passed THEN 'passed' ELSE 'not passed' END,NULL::jsonb,resource_kind,resource_id,created_at FROM eval_runs WHERE resource_kind=$1 AND resource_id=$2
		UNION ALL SELECT c.id,'candidate',c.status,c.source,NULL::jsonb,j.resource_kind,j.resource_id,c.created_at FROM optimization_candidates c JOIN optimization_jobs j ON j.id=c.optimization_job_id WHERE j.resource_kind=$1 AND j.resource_id=$2
		UNION ALL SELECT id,'experiment',status,recommendation,NULL::jsonb,resource_kind,resource_id,created_at FROM evaluation_experiments WHERE resource_kind=$1 AND resource_id=$2
		UNION ALL SELECT d.id,'decision',d.new_status,d.action,NULL::jsonb,e.resource_kind,e.resource_id,d.created_at FROM experiment_decisions d JOIN evaluation_experiments e ON e.id=d.experiment_id WHERE e.resource_kind=$1 AND e.resource_id=$2)
		SELECT id,kind,status,summary,safe_summary,resource_kind,resource_id,created_at FROM events
		WHERE ($3='' OR status=$3) AND ($4::timestamptz IS NULL OR (created_at,id,kind)<($4,$5,$6))
		ORDER BY created_at DESC,id DESC,kind DESC LIMIT $7`,
			filter.ResourceKind, filter.ResourceID, filter.Status, ct, cid, ckind, filter.Limit+1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var x domain.TimelineEvent
			var kind string
			var safe []byte
			if e = rows.Scan(&x.ID, &x.Kind, &x.Status, &x.Summary, &safe, &kind, &x.ResourceID, &x.CreatedAt); e != nil {
				return e
			}
			if x.Kind == "revision" {
				sanitized, marshalErr := json.Marshal(parseSanitizedSafeSummary(safe))
				if marshalErr != nil {
					x.Summary = "{}"
				} else {
					x.Summary = string(sanitized)
				}
			}
			x.ResourceKind = domain.ResourceKind(kind)
			page.Items = append(page.Items, x)
		}
		return rows.Err()
	})
	if len(page.Items) > filter.Limit {
		last := page.Items[filter.Limit-1]
		page.NextCursor = timelinePageCursor(last)
		page.Items = page.Items[:filter.Limit]
	}
	return page, wrapCenterQuery("timeline", e)
}

func wrapCenterQuery(operation string, err error) error {
	if err == nil {
		return nil
	}
	if err == port.ErrCenterResourceNotFound {
		return err
	}
	return fmt.Errorf("evaluation center repository: %s: %w", operation, err)
}
