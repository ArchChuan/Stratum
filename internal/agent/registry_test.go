package agent

import (
	"context"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
	"github.com/pashagolub/pgxmock/v2"
	"go.uber.org/zap"
)

type mockAgent struct {
	config *AgentConfig
}

func (m *mockAgent) GetConfig() *AgentConfig { return m.config }
func (m *mockAgent) Execute(ctx context.Context, input string, options ...ExecutionOption) (*AgentResult, error) {
	return &AgentResult{AgentID: m.config.ID, Input: input, Output: "mock"}, nil
}
func (m *mockAgent) Reset()               {}
func (m *mockAgent) GetMemory() []Message { return nil }

func tenantCtx(tenantID string) context.Context {
	tc := &tenantdb.TenantContext{TenantID: tenantID, UserID: "u1", Role: tenantdb.RoleTenantAdmin}
	return tenantdb.WithTenant(context.Background(), tc)
}

func TestRegistry_Register(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectExec("INSERT INTO agents").
		WithArgs("a1", "Alpha", string(ReActAgent), "", "", "", "gpt-4o", 5).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	pool.ExpectCommit()

	reg := &Registry{pool: pool, logger: zap.NewNop()}
	a := &mockAgent{config: &AgentConfig{ID: "a1", Name: "Alpha", Type: ReActAgent, LLMModel: "gpt-4o", MaxIterations: 5}}
	if err := reg.Register(tenantCtx("t1"), a); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRegistry_Get(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, name").
		WithArgs(pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "type", "description", "persona", "system_prompt", "llm_model", "max_iterations"}).
			AddRow("a1", "Alpha", string(ReActAgent), "", "", "", "gpt-4o", 5))
	pool.ExpectCommit()

	reg := &Registry{pool: pool, logger: zap.NewNop()}
	a, ok := reg.Get(tenantCtx("t1"), "a1")
	if !ok {
		t.Fatal("expected to find agent")
	}
	if a.GetConfig().Name != "Alpha" {
		t.Errorf("unexpected name: %s", a.GetConfig().Name)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestRegistry_GetNotFound(t *testing.T) {
	pool, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	pool.ExpectBegin()
	pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
	pool.ExpectQuery("SELECT id, name").
		WithArgs("missing").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "type", "description", "persona", "system_prompt", "llm_model", "max_iterations"}))
	pool.ExpectRollback()

	reg := &Registry{pool: pool, logger: zap.NewNop()}
	_, ok := reg.Get(tenantCtx("t1"), "missing")
	if ok {
		t.Fatal("expected not found")
	}
}

func TestRegistry_Remove(t *testing.T) {
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

	reg := &Registry{pool: pool, logger: zap.NewNop()}
	if err := reg.Remove(tenantCtx("t1"), "a1"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := pool.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}
