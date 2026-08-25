package persistence

import (
	"context"
	"encoding/json"
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

func TestMemoryScopeOrNormalizesEmptyToAgentDefault(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty zero value falls back to column default", in: "", want: "agent"},
		{name: "user scope preserved", in: "user", want: "user"},
		{name: "agent scope preserved", in: "agent", want: "agent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Register/Update 直写 memory_scope 列；空值会被
			// agents_memory_scope_check 拒绝，故必须归一化为默认 'agent'，
			// 否则系统路径（AgentConfig 不设 MemoryScope）UPDATE 抛 SQLSTATE 23514。
			if got := memoryScopeOr(tc.in); got != tc.want {
				t.Fatalf("memoryScopeOr(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
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
		WithArgs("a1", "Alpha", string(domain.ReActAgent), "", "", "gpt-4o", 5, 0, "agent", "", "{}", false, 0, 0).
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
		WithArgs("a1", "Alpha", string(domain.ReActAgent), "", "", "gpt-4o", 5, 0, "agent", "", "{}", false, 0, 0).
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
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "type", "description", "system_prompt", "llm_model", "max_iterations", "max_context_tokens", "memory_scope", "system_key", "created_by", "parameters", "delegate_enabled", "delegate_max_depth", "delegate_default_max_steps"}).
			AddRow("a1", "Alpha", string(domain.ReActAgent), "", "", "gpt-4o", 5, 8000, "", "", "", "{}", false, 0, 0))
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
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "type", "description", "system_prompt", "llm_model", "max_iterations", "max_context_tokens", "memory_scope", "system_key", "created_by", "parameters", "delegate_enabled", "delegate_max_depth", "delegate_default_max_steps"}))
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

func TestAgentRepo_Update_Success(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec("UPDATE agents").
		WithArgs("Beta", "", "", "gpt-4o", 5, 0, "agent", "{}", false, 0, 0, "a1").
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
	// parameters 参数用 AnyArg:JSON map marshal 顺序不稳定,pack 内容
	// 由 TestPackSamplingParameters 单独断言(顺序无关)。
	pool.ExpectExec("UPDATE agents").
		WithArgs("Beta", "", "", "gpt-4o", 5, 0, "agent", pgxmock.AnyArg(), false, 0, 0, "a1").
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
		MemoryParameters: map[string]any{"memory.fact_injection_top_n": 8},
	}
	if err := repo.Update(tenantCtx("t1"), cfg, nil, "", true); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}

	// 回读:DB 存 {temperature:0.9, memory.fact_injection_top_n:8},
	// Get 必须解回采样字段与 memory.* dotted 键,缺键保持 0=unset。
	// 压缩配置已迁平台参数,不再落 agent JSONB。
	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, name").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "type", "description", "system_prompt", "llm_model",
			"max_iterations", "max_context_tokens", "memory_scope", "system_key",
			"created_by", "parameters", "delegate_enabled", "delegate_max_depth",
			"delegate_default_max_steps",
		}).AddRow("a1", "Beta", "react", "", "", "gpt-4o", 5, 0, "", "", "", `{"temperature":0.9,"memory.fact_injection_top_n":8}`, false, 0, 0))
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
	if got.MaxTokens != 0 {
		t.Errorf("absent keys must stay unset(0): max_tokens=%d", got.MaxTokens)
	}
	if got.MemoryParameters["memory.fact_injection_top_n"] != float64(8) {
		t.Errorf("memory.fact_injection_top_n = %v, want 8", got.MemoryParameters["memory.fact_injection_top_n"])
	}
}

// TestPackSamplingParameters pins the 0=unset omitempty contract: explicit 0
// never reaches the JSONB column, so a form that omits a sampling field
// cannot erase a previously persisted value (merge semantics).
func TestPackSamplingParameters(t *testing.T) {
	cfg := &domain.AgentConfig{
		Temperature: 0.7, MaxTokens: 0,
		MemoryParameters: map[string]any{"memory.fact_injection_top_n": 8},
	}
	raw, err := packSamplingParameters(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatal(err)
	}
	if len(params) != 2 || params["temperature"] != 0.7 {
		t.Fatalf("packed = %v, want only non-zero sampling keys", params)
	}
	if params["memory.fact_injection_top_n"] != float64(8) {
		t.Errorf("memory.* dotted key must copy verbatim, got %v", params["memory.fact_injection_top_n"])
	}
}

// TestPackAllSamplingParameters pins the overall-replace contract (promote
// path): zero sampling fields serialize as explicit JSON null so a reset
// erases a previously persisted value, unlike packSamplingParameters which
// omits zeros (merge semantics).
func TestPackAllSamplingParameters(t *testing.T) {
	cfg := &domain.AgentConfig{
		Temperature: 0.7, MaxTokens: 0,
		MemoryParameters: map[string]any{"memory.max_facts_per_extraction": 20},
	}
	raw, err := packAllSamplingParameters(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatal(err)
	}
	if len(params) != 4 {
		t.Fatalf("packAll must carry all 3 sampling keys + memory.*, got %v", params)
	}
	if params["temperature"] != 0.7 {
		t.Errorf("non-zero fields must keep value: %v", params)
	}
	if v, ok := params["max_tokens"]; !ok || v != nil {
		t.Errorf("zero max_tokens must serialize as explicit null, got %v", params["max_tokens"])
	}
	if v, ok := params["reasoning_effort"]; !ok || v != nil {
		t.Errorf("empty reasoning_effort must serialize as explicit null, got %v", params["reasoning_effort"])
	}
	if params["memory.max_facts_per_extraction"] != float64(20) {
		t.Errorf("memory.* must survive overall-replace write, got %v", params["memory.max_facts_per_extraction"])
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
// updates must concatenate JSONB and strip explicit null deletion markers, so
// an old-style client PUT that omits sampling fields cannot erase persisted
// values while an edited memory override can be cleared.
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
	// 完整 SQL 正则:merge 路径必须含 JSONB 拼接和 null 清理,禁止回落为整体覆盖。
	pool.ExpectExec(`UPDATE agents[\s\S]*parameters\s*=\s*jsonb_strip_nulls\(parameters\s*\|\|\s*\$8::jsonb\)[\s\S]*WHERE id=\$12`).
		WithArgs("Beta", "", "", "gpt-4o", 5, 0, "agent", pgxmock.AnyArg(), false, 0, 0, "a1").
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
		Temperature: 0.3,
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
	// packAll 内容(含 null)由 TestPackAllSamplingParameters 断言。
	pool.ExpectExec("UPDATE agents").
		WithArgs("Beta", "", "", "gpt-4o", 5, 0, "agent", pgxmock.AnyArg(), false, 0, 0, "a1").
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

	// 回读:DB 存 {temperature:null, max_tokens:null,...}
	// null 必须解回 0=unset(null 与缺键同义)。
	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, name").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "type", "description", "system_prompt", "llm_model",
			"max_iterations", "max_context_tokens", "memory_scope", "system_key",
			"created_by", "parameters", "delegate_enabled", "delegate_max_depth",
			"delegate_default_max_steps",
		}).AddRow("a1", "Beta", "react", "", "", "gpt-4o", 5, 0, "", "", "", `{"temperature":null,"max_tokens":null}`, false, 0, 0))
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
	if got.Temperature != 0 || got.MaxTokens != 0 {
		t.Errorf("null fields must resolve to unset(0): temp=%v max_tokens=%d",
			got.Temperature, got.MaxTokens)
	}
}

// TestAgentRepo_DelegateFieldsRoundTrip pins the stratum_delegate per-agent
// parameters through the storage round-trip: UPDATE persists
// delegate_enabled/max_depth/default_max_steps columns, and Get reads them back.
func TestAgentRepo_DelegateFieldsRoundTrip(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec("UPDATE agents").
		WithArgs("Beta", "", "", "gpt-4o", 5, 0, "agent", "{}", true, 2, 7, "a1").
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
		DelegateEnabled: true, DelegateMaxDepth: 2, DelegateDefaultMaxSteps: 7,
	}
	if err := repo.Update(tenantCtx("t1"), cfg, nil, "", false); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// 回读:DB 三列必须解回 AgentConfig 委托参数。
	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, name").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{
			"id", "name", "type", "description", "system_prompt", "llm_model",
			"max_iterations", "max_context_tokens", "memory_scope", "system_key",
			"created_by", "parameters", "delegate_enabled", "delegate_max_depth",
			"delegate_default_max_steps",
		}).AddRow("a1", "Beta", "react", "", "", "gpt-4o", 5, 0, "", "", "", "{}", true, 2, 7))
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
	if !got.DelegateEnabled || got.DelegateMaxDepth != 2 || got.DelegateDefaultMaxSteps != 7 {
		t.Fatalf("delegate fields not round-tripped: enabled=%v depth=%d steps=%d",
			got.DelegateEnabled, got.DelegateMaxDepth, got.DelegateDefaultMaxSteps)
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
		WithArgs("Beta", "", "", "gpt-4o", 5, 0, "agent", "{}", false, 0, 0, "missing").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))
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
		WithArgs("a1", "Alpha", string(domain.ReActAgent), "", "", "gpt-4o", 5, 0, "agent", "creator-1", "{}", false, 0, 0).
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
		WithArgs("a1", "Alpha", string(domain.ReActAgent), "", "", "gpt-4o", 5, 0, "agent", "creator-1", "{}", false, 0, 0).
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
	pool.ExpectQuery("SELECT EXISTS").
		WithArgs("t1", "editor-1").
		WillReturnRows(pgxmock.NewRows([]string{"bool"}).AddRow(true))
	pool.ExpectQuery("SELECT EXISTS").
		WithArgs("agent", "a1", "editor-1").
		WillReturnRows(pgxmock.NewRows([]string{"bool"}).AddRow(true))
	pool.ExpectExec("UPDATE agents").
		WithArgs("Beta", "", "", "gpt-4o", 5, 0, "agent", "{}", false, 0, 0, "a1").
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
