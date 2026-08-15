package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
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
	err = repo.Register(tenantCtx(`bad"tenant`), &domain.AgentConfig{ID: "a1"}, nil, nil)
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
		WithArgs("a1", "Alpha", string(domain.ReActAgent), "", "", "gpt-4o", 5, 0, "", "", "{}").
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
	if err := repo.Register(tenantCtx("t1"), cfg, nil, nil); err != nil {
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
		WithArgs("a1", "Alpha", string(domain.ReActAgent), "", "", "gpt-4o", 5, 0, "", "", "{}").
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
	if err := repo.Register(tenantCtx("t1"), cfg, nil, nil); err != nil {
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
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "type", "description", "system_prompt", "llm_model", "max_iterations", "max_context_tokens", "memory_scope", "system_key", "created_by", "parameters"}).
			AddRow("a1", "Alpha", string(domain.ReActAgent), "", "", "gpt-4o", 5, 8000, "", "", "", "{}"))
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
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "type", "description", "system_prompt", "llm_model", "max_iterations", "max_context_tokens", "memory_scope", "system_key", "created_by", "parameters"}))
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
	pool.ExpectQuery("SELECT COALESCE\\(system_key").
		WithArgs("a1").WillReturnRows(pgxmock.NewRows([]string{"system_key"}).AddRow(""))
	pool.ExpectExec("DELETE FROM agents").
		WithArgs("a1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))
	pool.ExpectExec("DELETE FROM resource_editors").
		WithArgs("agent", "a1").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectCommit()

	repo := &PgAgentRepo{pool: pool}
	if err := repo.Remove(tenantCtx("t1"), "a1", nil); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAgentRepo_RemoveSystemAssistantRollsBackBeforeDelete(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT COALESCE\\(system_key").
		WithArgs("stratum-platform-assistant").
		WillReturnRows(pgxmock.NewRows([]string{"system_key"}).AddRow("stratum.platform_assistant"))
	pool.ExpectRollback()

	repo := &PgAgentRepo{pool: pool}
	err = repo.Remove(tenantCtx("t1"), "stratum-platform-assistant", nil)
	if !errors.Is(err, domain.ErrSystemAssistantManaged) {
		t.Fatalf("expected managed assistant error, got %v", err)
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
	pool.ExpectQuery("SELECT COALESCE\\(system_key").
		WithArgs("a1").WillReturnRows(pgxmock.NewRows([]string{"system_key"}).AddRow(""))
	pool.ExpectExec("UPDATE agents").
		WithArgs("Beta", "", "", "gpt-4o", 5, 0, "", "{}", "a1").
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
	if err := repo.Update(tenantCtx("t1"), cfg, nil, "", false); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestAgentRepo_SamplingParametersRoundTrip pins the promote write-back fix
// (断层 1+2): UPDATE must persist the 4 sampling parameters into
// agents.parameters JSONB (0 values omitted = unset), and a subsequent Get
// must read them back through the same column — production AgentConfig must
// no longer see sampling fields frozen at 0.
func TestAgentRepo_SamplingParametersRoundTrip(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT COALESCE\\(system_key").
		WithArgs("a1").WillReturnRows(pgxmock.NewRows([]string{"system_key"}).AddRow(""))
	// parameters 参数用 AnyArg:JSON map marshal 顺序不稳定,pack 内容
	// 由 TestPackSamplingParameters 单独断言(顺序无关)。
	pool.ExpectExec("UPDATE agents").
		WithArgs("Beta", "", "", "gpt-4o", 5, 0, "", pgxmock.AnyArg(), "a1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectExec("DELETE FROM agent_skill_links").
		WithArgs("a1").WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectExec("DELETE FROM agent_mcp_tool_links").
		WithArgs("a1").WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectExec("DELETE FROM agent_workspaces").
		WithArgs("a1").WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectCommit()

	repo := &PgAgentRepo{pool: pool}
	cfg := &domain.AgentConfig{
		ID: "a1", Name: "Beta", LLMModel: "gpt-4o", MaxIterations: 5,
		Temperature: 0.9, MaxTokens: 2048,
		CompactionRecentGroups: 3, CompactionSafetyRatio: 0.85,
	}
	if err := repo.Update(tenantCtx("t1"), cfg, nil, "", true); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}

	// 回读:DB 存 {temperature:0.9, compaction_recent_groups:3},
	// Get 必须解回采样字段,缺键保持 0=unset。
	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, name").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "type", "description", "system_prompt", "llm_model",
			"max_iterations", "max_context_tokens", "memory_scope", "system_key",
			"created_by", "parameters",
		}).AddRow("a1", "Beta", "react", "", "", "gpt-4o", 5, 0, "", "", "", `{"temperature":0.9,"compaction_recent_groups":3}`))
	pool.ExpectQuery("SELECT skill_id FROM agent_skill_links").
		WithArgs("a1").WillReturnRows(pgxmock.NewRows([]string{"skill_id"}))
	pool.ExpectQuery("SELECT server_id, tool_name FROM agent_mcp_tool_links").
		WithArgs("a1").WillReturnRows(pgxmock.NewRows([]string{"server_id", "tool_name"}))
	pool.ExpectQuery("SELECT aw.workspace_id").
		WithArgs("a1").WillReturnRows(pgxmock.NewRows([]string{"workspace_id", "name", "description"}))
	pool.ExpectCommit()

	got, ok, err := repo.Get(tenantCtx("t1"), "a1")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Temperature != 0.9 {
		t.Errorf("temperature = %v, want 0.9", got.Temperature)
	}
	if got.CompactionRecentGroups != 3 {
		t.Errorf("compaction_recent_groups = %d, want 3", got.CompactionRecentGroups)
	}
	if got.MaxTokens != 0 || got.CompactionSafetyRatio != 0 {
		t.Errorf("absent keys must stay unset(0): max_tokens=%d safety_ratio=%v", got.MaxTokens, got.CompactionSafetyRatio)
	}
}

// TestPackSamplingParameters pins the 0=unset omitempty contract: explicit 0
// never reaches the JSONB column, so a form that omits a sampling field
// cannot erase a previously persisted value (merge semantics).
func TestPackSamplingParameters(t *testing.T) {
	cfg := &domain.AgentConfig{
		Temperature: 0.7, MaxTokens: 0,
		CompactionRecentGroups: 2, CompactionSafetyRatio: 0,
	}
	raw, err := packSamplingParameters(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatal(err)
	}
	if len(params) != 2 || params["temperature"] != 0.7 || params["compaction_recent_groups"] != float64(2) {
		t.Fatalf("packed = %v, want only non-zero sampling keys", params)
	}
}

// TestPackAllSamplingParameters pins the overall-replace contract (promote
// path): zero sampling fields serialize as explicit JSON null so a reset
// erases a previously persisted value, unlike packSamplingParameters which
// omits zeros (merge semantics).
func TestPackAllSamplingParameters(t *testing.T) {
	cfg := &domain.AgentConfig{
		Temperature: 0.7, MaxTokens: 0,
		CompactionRecentGroups: 2, CompactionSafetyRatio: 0,
	}
	raw, err := packAllSamplingParameters(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatal(err)
	}
	if len(params) != 5 {
		t.Fatalf("packAll must carry all 5 keys, got %v", params)
	}
	if params["temperature"] != 0.7 || params["compaction_recent_groups"] != float64(2) {
		t.Errorf("non-zero fields must keep value: %v", params)
	}
	if v, ok := params["max_tokens"]; !ok || v != nil {
		t.Errorf("zero max_tokens must serialize as explicit null, got %v", params["max_tokens"])
	}
	if v, ok := params["compaction_safety_ratio"]; !ok || v != nil {
		t.Errorf("zero compaction_safety_ratio must serialize as explicit null, got %v", params["compaction_safety_ratio"])
	}
	if v, ok := params["reasoning_effort"]; !ok || v != nil {
		t.Errorf("empty reasoning_effort must serialize as explicit null, got %v", params["reasoning_effort"])
	}
}

// TestPackAllSamplingParameters_ReasoningEffortTier pins the string branch:
// a declared tier keeps its value under overall-replace semantics.
func TestPackAllSamplingParameters_ReasoningEffortTier(t *testing.T) {
	cfg := &domain.AgentConfig{ReasoningEffort: "high"}
	raw, err := packAllSamplingParameters(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatal(err)
	}
	if params["reasoning_effort"] != "high" {
		t.Errorf("reasoning_effort = %v, want high", params["reasoning_effort"])
	}
}

// TestAgentRepo_Update_MergeSQLShape pins the merge path SQL: form/API
// updates must concatenate JSONB (`parameters || $8::jsonb`) so an old-style
// client PUT that omits sampling fields cannot erase persisted values.
// replaceParams=false is the only shape asserted here; the promote shape
// (plain assignment) is covered by the SamplingParametersReplace test.
func TestAgentRepo_Update_MergeSQLShape(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT COALESCE\\(system_key").
		WithArgs("a1").WillReturnRows(pgxmock.NewRows([]string{"system_key"}).AddRow(""))
	// 完整 SQL 正则:merge 路径必须含 JSONB 拼接,禁止回落为整体覆盖。
	pool.ExpectExec(`UPDATE agents[\s\S]*parameters\s*=\s*parameters\s*\|\|\s*\$8::jsonb[\s\S]*WHERE id=\$9`).
		WithArgs("Beta", "", "", "gpt-4o", 5, 0, "", pgxmock.AnyArg(), "a1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectExec("DELETE FROM agent_skill_links").
		WithArgs("a1").WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectExec("DELETE FROM agent_mcp_tool_links").
		WithArgs("a1").WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectExec("DELETE FROM agent_workspaces").
		WithArgs("a1").WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectCommit()

	repo := &PgAgentRepo{pool: pool}
	cfg := &domain.AgentConfig{
		ID: "a1", Name: "Beta", LLMModel: "gpt-4o", MaxIterations: 5,
		Temperature: 0.3, CompactionRecentGroups: 3,
	}
	// replaceParams=false = 表单/API merge 路径。
	if err := repo.Update(tenantCtx("t1"), cfg, nil, "", false); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestAgentRepo_SamplingParametersReplace pins the promote end-to-end shape:
// replaceParams=true walks packAll (zeros become JSON null), and a Get that
// reads back a null field must resolve it to 0=unset — a promote reset really
// erases the previously persisted sampling value.
func TestAgentRepo_SamplingParametersReplace(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT COALESCE\\(system_key").
		WithArgs("a1").WillReturnRows(pgxmock.NewRows([]string{"system_key"}).AddRow(""))
	// packAll 内容(含 null)由 TestPackAllSamplingParameters 断言。
	pool.ExpectExec("UPDATE agents").
		WithArgs("Beta", "", "", "gpt-4o", 5, 0, "", pgxmock.AnyArg(), "a1").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))
	pool.ExpectExec("DELETE FROM agent_skill_links").
		WithArgs("a1").WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectExec("DELETE FROM agent_mcp_tool_links").
		WithArgs("a1").WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectExec("DELETE FROM agent_workspaces").
		WithArgs("a1").WillReturnResult(pgxmock.NewResult("DELETE", 0))
	pool.ExpectCommit()

	repo := &PgAgentRepo{pool: pool}
	cfg := &domain.AgentConfig{
		ID: "a1", Name: "Beta", LLMModel: "gpt-4o", MaxIterations: 5,
		// promote 重置:全部采样字段归零。
	}
	if err := repo.Update(tenantCtx("t1"), cfg, nil, "", true); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}

	// 回读:DB 存 {temperature:null, max_tokens:null, compaction_recent_groups:2,...}
	// null 必须解回 0=unset(null 与缺键同义)。
	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, name").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "type", "description", "system_prompt", "llm_model",
			"max_iterations", "max_context_tokens", "memory_scope", "system_key",
			"created_by", "parameters",
		}).AddRow("a1", "Beta", "react", "", "", "gpt-4o", 5, 0, "", "", "", `{"temperature":null,"max_tokens":null,"compaction_recent_groups":2,"compaction_safety_ratio":null}`))
	pool.ExpectQuery("SELECT skill_id FROM agent_skill_links").
		WithArgs("a1").WillReturnRows(pgxmock.NewRows([]string{"skill_id"}))
	pool.ExpectQuery("SELECT server_id, tool_name FROM agent_mcp_tool_links").
		WithArgs("a1").WillReturnRows(pgxmock.NewRows([]string{"server_id", "tool_name"}))
	pool.ExpectQuery("SELECT aw.workspace_id").
		WithArgs("a1").WillReturnRows(pgxmock.NewRows([]string{"workspace_id", "name", "description"}))
	pool.ExpectCommit()

	got, ok, err := repo.Get(tenantCtx("t1"), "a1")
	if err != nil || !ok {
		t.Fatalf("Get: ok=%v err=%v", ok, err)
	}
	if got.Temperature != 0 || got.MaxTokens != 0 || got.CompactionSafetyRatio != 0 {
		t.Errorf("null fields must resolve to unset(0): temp=%v max_tokens=%d safety=%v",
			got.Temperature, got.MaxTokens, got.CompactionSafetyRatio)
	}
	if got.CompactionRecentGroups != 2 {
		t.Errorf("compaction_recent_groups = %d, want 2", got.CompactionRecentGroups)
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
	pool.ExpectQuery("SELECT COALESCE\\(system_key").
		WithArgs("missing").WillReturnError(pgx.ErrNoRows)
	pool.ExpectRollback()

	repo := &PgAgentRepo{pool: pool}
	cfg := &domain.AgentConfig{ID: "missing", Name: "Beta", LLMModel: "gpt-4o", MaxIterations: 5}
	err = repo.Update(tenantCtx("t1"), cfg, nil, "", false)
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

func TestAgentRepo_UpdateSystemAssistantRollsBackBeforeRelations(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT COALESCE\\(system_key").
		WithArgs("stratum-platform-assistant").
		WillReturnRows(pgxmock.NewRows([]string{"system_key"}).AddRow("stratum.platform_assistant"))
	pool.ExpectRollback()

	repo := &PgAgentRepo{pool: pool}
	err = repo.Update(tenantCtx("t1"), &domain.AgentConfig{ID: "stratum-platform-assistant"}, nil, "", false)
	if !errors.Is(err, domain.ErrSystemAssistantManaged) {
		t.Fatalf("expected managed assistant error, got %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAgentRepo_GetSystemAssistant(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, name").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "type", "description", "system_prompt", "llm_model", "max_iterations", "max_context_tokens", "memory_scope", "system_key", "created_by", "parameters"}).
			AddRow("stratum-platform-assistant", "Stratum 系统助手", "react", "managed", "", "qwen-plus", 10, 8000, "user", "stratum.platform_assistant", "", "{}"))
	pool.ExpectQuery("SELECT skill_id FROM agent_skill_links").
		WithArgs("stratum-platform-assistant").WillReturnRows(pgxmock.NewRows([]string{"skill_id"}))
	pool.ExpectQuery("SELECT server_id, tool_name FROM agent_mcp_tool_links").
		WithArgs("stratum-platform-assistant").WillReturnRows(pgxmock.NewRows([]string{"server_id", "tool_name"}))
	pool.ExpectQuery("SELECT aw.workspace_id").
		WithArgs("stratum-platform-assistant").WillReturnRows(pgxmock.NewRows([]string{"workspace_id", "name", "description"}))
	pool.ExpectCommit()

	repo := &PgAgentRepo{pool: pool}
	cfg, ok, err := repo.GetSystemAssistant(tenantCtx("t1"))
	if err != nil || !ok {
		t.Fatalf("GetSystemAssistant: ok=%v err=%v", ok, err)
	}
	if cfg.SystemKey != "stratum.platform_assistant" || !cfg.IsSystem || cfg.ManagementMode != "platform" {
		t.Fatalf("unexpected managed identity: %+v", cfg)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAgentRepo_UpdateSystemAssistantModel(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("UPDATE agents SET llm_model=\\$1.*updated_at=NOW\\(\\).*RETURNING id").
		WithArgs("qwen-plus", "user", 10, 8000).WillReturnRows(pgxmock.NewRows([]string{
		"id", "name", "type", "description", "system_prompt", "llm_model",
		"max_iterations", "max_context_tokens", "memory_scope", "system_key", "created_by",
	}).AddRow(domain.SystemAssistantID, "平台助手", string(domain.ReActAgent), "", "", "qwen-plus", 5, 0,
		"", domain.SystemAssistantKey, ""))
	pool.ExpectQuery("SELECT skill_id FROM agent_skill_links").
		WithArgs(domain.SystemAssistantID).WillReturnRows(pgxmock.NewRows([]string{"skill_id"}))
	pool.ExpectQuery("SELECT server_id, tool_name FROM agent_mcp_tool_links").
		WithArgs(domain.SystemAssistantID).WillReturnRows(pgxmock.NewRows([]string{"server_id", "tool_name"}))
	pool.ExpectQuery("SELECT aw.workspace_id").
		WithArgs(domain.SystemAssistantID).WillReturnRows(pgxmock.NewRows([]string{"workspace_id", "name", "description"}))
	pool.ExpectCommit()

	repo := &PgAgentRepo{pool: pool}
	cfg, err := repo.UpdateSystemAssistantModel(tenantCtx("t1"), "qwen-plus", "user", 10, 8000, nil)
	if err != nil {
		t.Fatalf("UpdateSystemAssistantModel: %v", err)
	}
	if cfg.LLMModel != "qwen-plus" || !cfg.IsSystem || cfg.ManagementMode != "platform" {
		t.Fatalf("unexpected returned config: %+v", cfg)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAgentRepo_UpdateSystemAssistantModelNotFound(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("UPDATE agents SET llm_model=\\$1.*updated_at=NOW\\(\\).*RETURNING id").
		WithArgs("qwen-plus", "user", 10, 8000).WillReturnError(pgx.ErrNoRows)
	pool.ExpectRollback()

	repo := &PgAgentRepo{pool: pool}
	_, err = repo.UpdateSystemAssistantModel(tenantCtx("t1"), "qwen-plus", "user", 10, 8000, nil)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAgentRepo_UpdateSystemAssistantModelRelationFailureRollsBack(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("UPDATE agents SET llm_model=\\$1.*updated_at=NOW\\(\\).*RETURNING id").
		WithArgs("qwen-plus", "user", 10, 8000).WillReturnRows(pgxmock.NewRows([]string{
		"id", "name", "type", "description", "system_prompt", "llm_model",
		"max_iterations", "max_context_tokens", "memory_scope", "system_key", "created_by",
	}).AddRow(domain.SystemAssistantID, "平台助手", string(domain.ReActAgent), "", "", "qwen-plus", 5, 0,
		"", domain.SystemAssistantKey, ""))
	pool.ExpectQuery("SELECT skill_id FROM agent_skill_links").
		WithArgs(domain.SystemAssistantID).WillReturnError(errors.New("relations unavailable"))
	pool.ExpectRollback()

	repo := &PgAgentRepo{pool: pool}
	cfg, err := repo.UpdateSystemAssistantModel(tenantCtx("t1"), "qwen-plus", "user", 10, 8000, nil)
	if err == nil || cfg != nil || !strings.Contains(err.Error(), "relations unavailable") {
		t.Fatalf("expected rollback relation error, cfg=%+v err=%v", cfg, err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// systemAssistantUpdateRow builds the 12-column UPDATE ... RETURNING row shared
// by the UpdateSystemAssistantAll tests (parameters appended last).
func systemAssistantUpdateRow() *pgxmock.Rows {
	return pgxmock.NewRows([]string{
		"id", "name", "type", "description", "system_prompt", "llm_model",
		"max_iterations", "max_context_tokens", "memory_scope", "system_key", "created_by", "parameters",
	}).AddRow(domain.SystemAssistantID, "平台助手", string(domain.ReActAgent), "", "", "qwen-plus", 10, 8000,
		"user", domain.SystemAssistantKey, "", `{"temperature":0.5,"max_tokens":2048}`)
}

func TestAgentRepo_UpdateSystemAssistantAll_ParametersMergeAndReadback(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("UPDATE agents SET llm_model=\\$1.*updated_at=NOW\\(\\).*RETURNING id").
		WithArgs("qwen-plus", "user", 10, 8000, `{"max_tokens":2048}`).
		WillReturnRows(systemAssistantUpdateRow())
	pool.ExpectQuery("SELECT skill_id FROM agent_skill_links").
		WithArgs(domain.SystemAssistantID).WillReturnRows(pgxmock.NewRows([]string{"skill_id"}))
	pool.ExpectQuery("SELECT server_id, tool_name FROM agent_mcp_tool_links").
		WithArgs(domain.SystemAssistantID).WillReturnRows(pgxmock.NewRows([]string{"server_id", "tool_name"}))
	pool.ExpectQuery("SELECT aw.workspace_id").
		WithArgs(domain.SystemAssistantID).WillReturnRows(pgxmock.NewRows([]string{"workspace_id", "name", "description"}))
	pool.ExpectCommit()

	repo := &PgAgentRepo{pool: pool}
	cfg, err := repo.UpdateSystemAssistantAll(tenantCtx("t1"), "qwen-plus", "user", 10, 8000, 2048, nil)
	if err != nil {
		t.Fatalf("UpdateSystemAssistantAll: %v", err)
	}
	if cfg.MaxTokens != 2048 {
		t.Errorf("MaxTokens = %d, want 2048 (RETURNING parameters unpack)", cfg.MaxTokens)
	}
	if cfg.Temperature != 0.5 {
		t.Errorf("Temperature = %v, want 0.5 (pre-existing parameter preserved)", cfg.Temperature)
	}
	if !cfg.IsSystem || cfg.ManagementMode != "platform" {
		t.Fatalf("unexpected managed identity: %+v", cfg)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAgentRepo_UpdateSystemAssistantAll_ZeroMaxTokensSendsEmptyFragment(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	// 0=unset:JSONB 拼接空对象,存量 parameters 不被清除。
	pool.ExpectQuery("UPDATE agents SET llm_model=\\$1.*RETURNING id").
		WithArgs("qwen-plus", "user", 10, 8000, `{}`).
		WillReturnRows(systemAssistantUpdateRow())
	pool.ExpectQuery("SELECT skill_id FROM agent_skill_links").
		WithArgs(domain.SystemAssistantID).WillReturnRows(pgxmock.NewRows([]string{"skill_id"}))
	pool.ExpectQuery("SELECT server_id, tool_name FROM agent_mcp_tool_links").
		WithArgs(domain.SystemAssistantID).WillReturnRows(pgxmock.NewRows([]string{"server_id", "tool_name"}))
	pool.ExpectQuery("SELECT aw.workspace_id").
		WithArgs(domain.SystemAssistantID).WillReturnRows(pgxmock.NewRows([]string{"workspace_id", "name", "description"}))
	pool.ExpectCommit()

	repo := &PgAgentRepo{pool: pool}
	cfg, err := repo.UpdateSystemAssistantAll(tenantCtx("t1"), "qwen-plus", "user", 10, 8000, 0, nil)
	if err != nil {
		t.Fatalf("UpdateSystemAssistantAll: %v", err)
	}
	if cfg.MaxTokens != 2048 {
		t.Errorf("MaxTokens = %d, want 2048 (stored value read back unchanged)", cfg.MaxTokens)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestAgentRepo_UpdateSystemAssistantAll_AuditFailureRollsBackParameters(t *testing.T) {
	// CLAUDE.md:数据库写入必须验证事务回滚和失败传播。审计 INSERT 失败 →
	// parameters 更新不得提交,错误必须向上传播且不返回 config。
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("UPDATE agents SET llm_model=\\$1.*RETURNING id").
		WithArgs("qwen-plus", "user", 10, 8000, `{"max_tokens":2048}`).
		WillReturnRows(systemAssistantUpdateRow())
	pool.ExpectQuery("SELECT skill_id FROM agent_skill_links").
		WithArgs(domain.SystemAssistantID).WillReturnRows(pgxmock.NewRows([]string{"skill_id"}))
	pool.ExpectQuery("SELECT server_id, tool_name FROM agent_mcp_tool_links").
		WithArgs(domain.SystemAssistantID).WillReturnRows(pgxmock.NewRows([]string{"server_id", "tool_name"}))
	pool.ExpectQuery("SELECT aw.workspace_id").
		WithArgs(domain.SystemAssistantID).WillReturnRows(pgxmock.NewRows([]string{"workspace_id", "name", "description"}))
	pool.ExpectExec("INSERT INTO resource_change_audits").
		WithArgs(pgxmock.AnyArg(), "t1", "agent", domain.SystemAssistantID, "update", "user-1",
			pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg(), pgxmock.AnyArg()).
		WillReturnError(errors.New("audit write failed"))
	pool.ExpectRollback()

	repo := &PgAgentRepo{pool: pool}
	ev := &auditdomain.ResourceChangeAuditEvent{
		ResourceKind: auditdomain.ResourceKindAgent, ResourceID: domain.SystemAssistantID,
		Operation: auditdomain.ChangeOpUpdate, ActorID: "user-1",
	}
	cfg, err := repo.UpdateSystemAssistantAll(tenantCtx("t1"), "qwen-plus", "user", 10, 8000, 2048, ev)
	if err == nil || cfg != nil || !strings.Contains(err.Error(), "audit write failed") {
		t.Fatalf("expected audit rollback error, cfg=%+v err=%v", cfg, err)
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

func TestAgentRepo_Register_WithEditors(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec("INSERT INTO agents").
		WithArgs("a1", "Alpha", string(domain.ReActAgent), "", "", "gpt-4o", 5, 0, "", "creator-1", "{}").
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
	// Editor eligibility is validated inside the same transaction (fail closed).
	pool.ExpectQuery("SELECT EXISTS").
		WithArgs("t1", "user-1").
		WillReturnRows(pgxmock.NewRows([]string{"bool"}).AddRow(true))
	pool.ExpectExec("INSERT INTO resource_editors").
		WithArgs("agent", "a1", "user-1", "creator-1").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()

	repo := &PgAgentRepo{pool: pool}
	cfg := &domain.AgentConfig{
		ID: "a1", Name: "Alpha", Type: domain.ReActAgent,
		LLMModel: "gpt-4o", MaxIterations: 5, CreatedBy: "creator-1",
	}
	if err := repo.Register(tenantCtx("t1"), cfg, nil, []string{"user-1"}); err != nil {
		t.Fatalf("Register with editors: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestAgentRepo_Register_ForgedEditorRollsBack pins the fail-closed editor
// eligibility: a non-admin/owner id fails the whole transaction, so the agent
// row and the editor grant never coexist.
func TestAgentRepo_Register_ForgedEditorRollsBack(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec("INSERT INTO agents").
		WithArgs("a1", "Alpha", string(domain.ReActAgent), "", "", "gpt-4o", 5, 0, "", "creator-1", "{}").
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
	pool.ExpectQuery("SELECT EXISTS").
		WithArgs("t1", "member-1").
		WillReturnRows(pgxmock.NewRows([]string{"bool"}).AddRow(false))
	pool.ExpectRollback()

	repo := &PgAgentRepo{pool: pool}
	cfg := &domain.AgentConfig{
		ID: "a1", Name: "Alpha", Type: domain.ReActAgent,
		LLMModel: "gpt-4o", MaxIterations: 5, CreatedBy: "creator-1",
	}
	err = repo.Register(tenantCtx("t1"), cfg, nil, []string{"member-1"})
	if !errors.Is(err, domain.ErrEditorNotEligible) {
		t.Fatalf("Register error = %v, want ErrEditorNotEligible", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestAgentRepo_Update_EditorActorRevalidates pins the in-transaction
// re-validation: role admin/owner AND editor presence share the transaction
// with the business UPDATE, closing the check-then-write TOCTOU window.
func TestAgentRepo_Update_EditorActorRevalidates(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT COALESCE\\(system_key").
		WithArgs("a1").WillReturnRows(pgxmock.NewRows([]string{"system_key"}).AddRow(""))
	pool.ExpectQuery("SELECT EXISTS").
		WithArgs("t1", "editor-1").
		WillReturnRows(pgxmock.NewRows([]string{"bool"}).AddRow(true))
	pool.ExpectQuery("SELECT EXISTS").
		WithArgs("agent", "a1", "editor-1").
		WillReturnRows(pgxmock.NewRows([]string{"bool"}).AddRow(true))
	pool.ExpectExec("UPDATE agents").
		WithArgs("Beta", "", "", "gpt-4o", 5, 0, "", "{}", "a1").
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
	if err := repo.Update(tenantCtx("t1"), cfg, nil, "editor-1", false); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

// TestAgentRepo_Update_EditorActorDeniedRollsBack pins fail-closed semantics:
// an actor present in resource_editors but no longer eligible (role downgrade)
// or whose grant was removed is rejected inside the transaction.
func TestAgentRepo_Update_EditorActorDeniedRollsBack(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT COALESCE\\(system_key").
		WithArgs("a1").WillReturnRows(pgxmock.NewRows([]string{"system_key"}).AddRow(""))
	pool.ExpectQuery("SELECT EXISTS").
		WithArgs("t1", "editor-1").
		WillReturnRows(pgxmock.NewRows([]string{"bool"}).AddRow(true))
	// Grant was removed -> presence check fails closed.
	pool.ExpectQuery("SELECT EXISTS").
		WithArgs("agent", "a1", "editor-1").
		WillReturnRows(pgxmock.NewRows([]string{"bool"}).AddRow(false))
	pool.ExpectRollback()

	repo := &PgAgentRepo{pool: pool}
	cfg := &domain.AgentConfig{ID: "a1", Name: "Beta", LLMModel: "gpt-4o", MaxIterations: 5}
	err = repo.Update(tenantCtx("t1"), cfg, nil, "editor-1", false)
	if !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("Update error = %v, want ErrForbidden", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
