//go:build integration

package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPgCenterQueryRepositoryEvidenceTimelinePaginationAndIsolation(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; center query integration test requires real PostgreSQL")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tenants := []string{"center_query_one", "center_query_two"}
	for _, tenant := range tenants {
		if err := postgres.ProvisionTenantSchema(ctx, pool, tenant); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, tenant := range tenants {
			_, _ = pool.Exec(context.Background(), fmt.Sprintf(`DROP SCHEMA IF EXISTS "tenant_%s" CASCADE`, tenant))
		}
	})

	seedCenterQuery(t, ctx, pool, "tenant_center_query_one", "one")
	seedCenterQuery(t, ctx, pool, "tenant_center_query_two", "two")
	repo := NewPgCenterQueryRepository(pool)
	overview, err := repo.Overview(ctx, tenants[0])
	if err != nil {
		t.Fatal(err)
	}
	if overview.Resources != 2 || overview.Suites != 1 || overview.Runs != 1 || overview.Candidates != 1 || overview.Experiments != 1 {
		t.Fatalf("overview=%+v", overview)
	}

	first, err := repo.ListResources(ctx, tenants[0], port.CenterFilter{ResourceKind: "skill", Status: "published", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].SafeSummary["label"] != "one-new" || first.NextCursor == "" {
		t.Fatalf("first page=%+v", first)
	}
	second, err := repo.ListResources(ctx, tenants[0], port.CenterFilter{ResourceKind: "skill", Status: "published", Limit: 1, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].ID == first.Items[0].ID {
		t.Fatalf("second page=%+v", second)
	}
	other, err := repo.ListResources(ctx, tenants[1], port.CenterFilter{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(other.Items) == 0 || other.Items[0].SafeSummary["label"] != "two-new" {
		t.Fatalf("tenant isolation=%+v", other)
	}

	timeline, err := repo.Timeline(ctx, tenants[0], port.CenterFilter{ResourceKind: "skill", ResourceID: "shared", Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, event := range timeline.Items {
		kinds[event.Kind] = true
	}
	for _, kind := range []string{"revision", "run", "candidate", "experiment", "decision"} {
		if !kinds[kind] {
			t.Errorf("timeline missing %s: %+v", kind, timeline.Items)
		}
	}
	b, _ := json.Marshal(struct {
		Resources any `json:"resources"`
		Timeline  any `json:"timeline"`
	}{first.Items, timeline.Items})
	serialized := strings.ToLower(string(b))
	for _, forbidden := range []string{"object://secret", "payload_ref", "payload_hash", "content_hash", "actual_output", "feedback", "decision_snapshot", "metrics"} {
		if strings.Contains(serialized, forbidden) {
			t.Errorf("safe response contains %q: %s", forbidden, serialized)
		}
	}
}

func seedCenterQuery(t *testing.T, ctx context.Context, pool *pgxpool.Pool, schema, label string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	statements := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO ` + schema + `.eval_suites(id,name,description,created_at) VALUES('suite','suite-` + label + `','safe',$1)`, []any{now}},
		{`INSERT INTO ` + schema + `.eval_suite_revisions(id,suite_id,version_no,status,resource_kind,created_at) VALUES('suite-rev','suite',1,'published','skill',$1)`, []any{now}},
		{`UPDATE ` + schema + `.eval_suites SET active_revision_id='suite-rev' WHERE id='suite'`, nil},
		{`INSERT INTO ` + schema + `.resource_revisions(id,resource_kind,resource_id,source,status,content_hash,payload_hash,payload_ref,safe_summary,created_at) VALUES('rev-old','skill','shared','manual','published','secret-content','secret-hash','object://secret','{"label":"` + label + `-old"}',$1),('rev-new','skill','shared','manual','published','secret-content','secret-hash','object://secret','{"label":"` + label + `-new"}',$2)`, []any{now.Add(-time.Minute), now}},
		{`INSERT INTO ` + schema + `.resource_revisions(id,resource_kind,resource_id,source,status,content_hash,payload_hash,payload_ref,safe_summary,created_at) VALUES('rev-other','skill','other','manual','published','secret-content','secret-hash','object://secret','{"label":"` + label + `-other"}',$1)`, []any{now.Add(-2 * time.Minute)}},
		{`INSERT INTO ` + schema + `.eval_runs(id,resource_kind,resource_id,revision_id,suite_revision_id,status,passed,total_cases,passed_cases,metrics,created_at) VALUES('run','skill','shared','rev-new','suite-rev','succeeded',true,1,1,'{"secret":"metric"}',$1)`, []any{now.Add(time.Second)}},
		{`INSERT INTO ` + schema + `.optimization_jobs(id,resource_kind,resource_id,baseline_revision_id,suite_revision_id,status,created_at) VALUES('job','skill','shared','rev-old','suite-rev','succeeded',$1)`, []any{now.Add(2 * time.Second)}},
		{`INSERT INTO ` + schema + `.optimization_candidates(id,optimization_job_id,revision_id,parent_revision_id,source,rationale,created_at) VALUES('candidate','job','rev-new','rev-old','rewrite','safe rationale',$1)`, []any{now.Add(3 * time.Second)}},
		{`INSERT INTO ` + schema + `.evaluation_experiments(id,resource_kind,resource_id,stable_revision_id,canary_revision_id,suite_revision_id,status,decision_snapshot,created_at) VALUES('experiment','skill','shared','rev-old','rev-new','suite-rev','completed','{"secret":"body"}',$1)`, []any{now.Add(4 * time.Second)}},
		{`INSERT INTO ` + schema + `.experiment_decisions(id,experiment_id,action,actor_type,prior_status,new_status,metrics,reason,idempotency_key,created_at) VALUES('decision','experiment','promote','system','active','completed','{"secret":"body"}','safe reason','key',$1)`, []any{now.Add(5 * time.Second)}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}
