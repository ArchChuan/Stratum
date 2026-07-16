package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func TestProvisionTenantSchema_ReplacesLegacySkillsOnceAndRetainsAgentTraces(t *testing.T) {
	url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
		t.Fatal(err)
	}

	tenantID := fmt.Sprintf("tmp_skill_reset_%d", time.Now().UnixNano())
	schema := `tenant_` + tenantID
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
	if _, err := pool.Exec(ctx, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatal(err)
	}
	legacy := `SET search_path = "` + schema + `", public;
		CREATE TABLE skills (id TEXT PRIMARY KEY, name TEXT);
		CREATE TABLE skill_versions (id TEXT PRIMARY KEY, skill_id TEXT, implementation JSONB);
		CREATE TABLE agent_skill_links (agent_id TEXT, skill_id TEXT);
		CREATE TABLE skill_test_cases (id TEXT);
		CREATE TABLE skill_eval_runs (id TEXT);
		CREATE TABLE agent_tool_traces (
		 id UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(), trace_id TEXT NOT NULL DEFAULT '',
		 conversation_id UUID, step_index INT NOT NULL DEFAULT 0, provider_type TEXT NOT NULL DEFAULT '',
		 raw_result_text TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
		INSERT INTO skills VALUES ('legacy-skill', 'legacy');
		INSERT INTO skill_versions VALUES ('legacy-version', 'legacy-skill', '{"mode":"code"}');
		INSERT INTO agent_tool_traces (provider_type, raw_result_text) VALUES ('skill', 'historical');`
	if _, err := pool.Exec(ctx, legacy); err != nil {
		t.Fatal(err)
	}

	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	var legacyVersions, revisions, traces int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name='skill_versions'`, schema).Scan(&legacyVersions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema=$1 AND table_name='skill_revisions'`, schema).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "`+schema+`".agent_tool_traces WHERE provider_type='skill'`).Scan(&traces); err != nil {
		t.Fatal(err)
	}
	if legacyVersions != 0 || revisions != 1 || traces != 1 {
		t.Fatalf("legacy=%d revisions=%d traces=%d", legacyVersions, revisions, traces)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO "`+schema+`".skills (id,name) VALUES ('new-skill','new'); INSERT INTO "`+schema+`".skill_revisions (id,skill_id,instructions) VALUES ('new-revision','new-skill','instructions')`); err != nil {
		t.Fatal(err)
	}
	if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
		t.Fatal(err)
	}
	var newRows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "`+schema+`".skill_revisions WHERE id='new-revision'`).Scan(&newRows); err != nil {
		t.Fatal(err)
	}
	if newRows != 1 {
		t.Fatalf("second provision deleted new Skill revision")
	}
}
