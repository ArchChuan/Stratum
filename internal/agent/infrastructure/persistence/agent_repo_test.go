package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
)

func tenantCtx(tenantID string) context.Context {
	tc := &tenantdb.TenantContext{TenantID: tenantID, UserID: "u1", Role: tenantdb.RoleTenantAdmin}
	return tenantdb.WithTenant(context.Background(), tc)
}

func TestAgentRepoRejectsInvalidTenantBeforeBeginningTransaction(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	repo := &PgAgentRepo{pool: pool}
	err = repo.Register(tenantCtx(`bad"tenant`), &domain.AgentConfig{ID: "a1"})
	if err == nil || !strings.Contains(err.Error(), "postgres: invalid tenant_id") {
		t.Fatalf("expected shared tenant validation error, got %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentRepo_Register(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec("INSERT INTO agents").
		WithArgs("a1", "Alpha", string(domain.ReActAgent), "", "", "gpt-4o", "", 5, 0, "").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectExec("DELETE FROM agent_skill_links").
		WithArgs("a1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectExec("DELETE FROM agent_mcp_tool_links").
		WithArgs("a1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectExec("DELETE FROM agent_workspaces").
		WithArgs("a1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectCommit()

	repo := &PgAgentRepo{pool: pool}
	cfg := &domain.AgentConfig{ID: "a1", Name: "Alpha", Type: domain.ReActAgent, LLMModel: "gpt-4o", MaxIterations: 5}
	if err := repo.Register(tenantCtx("t1"), cfg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAgentRepo_Register_WithMCP(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec("INSERT INTO agents").
		WithArgs("a1", "Alpha", string(domain.ReActAgent), "", "", "gpt-4o", "", 5, 0, "").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectExec("DELETE FROM agent_skill_links").
		WithArgs("a1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectExec("DELETE FROM agent_mcp_tool_links").
		WithArgs("a1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectExec("INSERT INTO agent_mcp_tool_links").
		WithArgs("a1", "srv1", "search").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectExec("DELETE FROM agent_workspaces").
		WithArgs("a1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectCommit()

	repo := &PgAgentRepo{pool: pool}
	cfg := &domain.AgentConfig{
		ID: "a1", Name: "Alpha", Type: domain.ReActAgent,
		LLMModel: "gpt-4o", MaxIterations: 5,
		MCPToolIDs: []string{"mcp:srv1:search"},
	}
	if err := repo.Register(tenantCtx("t1"), cfg); err != nil {
		t.Fatalf("Register with MCP: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAgentRepo_Get(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, name").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "type", "description", "system_prompt", "llm_model", "embed_model", "max_iterations", "max_context_tokens", "memory_scope"}).
			AddRow("a1", "Alpha", string(domain.ReActAgent), "", "", "gpt-4o", "", 5, 8000, ""))
	pool.ExpectQuery("SELECT skill_id FROM agent_skill_links").
		WithArgs("a1").
		WillReturnRows(pgxmock.NewRows([]string{"skill_id"}))
	pool.ExpectQuery("SELECT server_id, tool_name FROM agent_mcp_tool_links").
		WithArgs("a1").
		WillReturnRows(pgxmock.NewRows([]string{"server_id", "tool_name"}))
	pool.ExpectQuery("SELECT aw.workspace_id").
		WithArgs("a1").
		WillReturnRows(pgxmock.NewRows([]string{"workspace_id", "name", "description"}))
	pool.ExpectCommit()

	repo := &PgAgentRepo{pool: pool}
	cfg, ok, err := repo.Get(tenantCtx("t1"), "a1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected to find agent")
	}
	if cfg.Name != "Alpha" {
		t.Errorf("unexpected name: %s", cfg.Name)
	}
	if got := cfg.AllowedSkills; len(got) != 0 {
		t.Errorf("expected empty allowed_skills, got %v", got)
	}
	if got := cfg.MCPToolIDs; len(got) != 0 {
		t.Errorf("expected empty mcp_server_ids, got %v", got)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAgentRepo_GetNotFound(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, name").
		WithArgs("missing").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "type", "description", "system_prompt", "llm_model", "embed_model", "max_iterations", "max_context_tokens", "memory_scope"}))
	pool.ExpectRollback()

	repo := &PgAgentRepo{pool: pool}
	_, ok, err := repo.Get(tenantCtx("t1"), "missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("expected not found")
	}
}

func TestAgentRepo_Remove(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec("DELETE FROM agents").
		WithArgs("a1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	pool.ExpectCommit()

	repo := &PgAgentRepo{pool: pool}
	if err := repo.Remove(tenantCtx("t1"), "a1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAgentRepo_Update_Success(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec("UPDATE agents").
		WithArgs("Beta", "", "", "gpt-4o", 5, 0, "", "a1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectExec("DELETE FROM agent_skill_links").
		WithArgs("a1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectExec("DELETE FROM agent_mcp_tool_links").
		WithArgs("a1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectExec("DELETE FROM agent_workspaces").
		WithArgs("a1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectCommit()

	repo := &PgAgentRepo{pool: pool}
	cfg := &domain.AgentConfig{ID: "a1", Name: "Beta", LLMModel: "gpt-4o", MaxIterations: 5}
	if err := repo.Update(tenantCtx("t1"), cfg); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAgentRepo_Update_NotFound(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec("UPDATE agents").
		WithArgs("Beta", "", "", "gpt-4o", 5, 0, "", "missing").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
	pool.ExpectRollback()

	repo := &PgAgentRepo{pool: pool}
	cfg := &domain.AgentConfig{ID: "missing", Name: "Beta", LLMModel: "gpt-4o", MaxIterations: 5}
	err = repo.Update(tenantCtx("t1"), cfg)
	if err == nil {
		t.Fatal("expected error for missing agent")
	}
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("expected domain.ErrNotFound, got: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAgentRepo_FindAgentBySkill_Found(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT agent_id FROM agent_skill_links WHERE skill_id").
		WithArgs("sk1").
		WillReturnRows(pgxmock.NewRows([]string{"agent_id"}).AddRow("a1"))
	pool.ExpectCommit()

	repo := &PgAgentRepo{pool: pool}
	agentID, found, err := repo.FindAgentBySkill(tenantCtx("t1"), "sk1")
	if err != nil {
		t.Fatalf("FindAgentBySkill: %v", err)
	}
	if !found || agentID != "a1" {
		t.Fatalf("want (a1,true), got (%q,%v)", agentID, found)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAgentRepo_FindAgentBySkill_NotFound(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT agent_id FROM agent_skill_links WHERE skill_id").
		WithArgs("sk-none").
		WillReturnError(pgx.ErrNoRows)
	pool.ExpectRollback()

	repo := &PgAgentRepo{pool: pool}
	agentID, found, err := repo.FindAgentBySkill(tenantCtx("t1"), "sk-none")
	if err != nil {
		t.Fatalf("FindAgentBySkill unexpected err: %v", err)
	}
	if found || agentID != "" {
		t.Fatalf("want (\"\",false), got (%q,%v)", agentID, found)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
