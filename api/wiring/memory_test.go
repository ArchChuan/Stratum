package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/config"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	memworkers "github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
	parametersapp "github.com/byteBuilderX/stratum/internal/parameters/application"
	parametersdomain "github.com/byteBuilderX/stratum/internal/parameters/domain"
	parametersport "github.com/byteBuilderX/stratum/internal/parameters/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

func TestBuildMemoryCreatesInjectorWithoutMilvus(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://localhost/stratum?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	c := &Container{
		Config:  &config.Config{},
		Logger:  zap.NewNop(),
		Storage: &Storage{PG: postgres.Wrap(pool)},
	}
	if err := c.buildMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.Memory == nil || c.Memory.Injector == nil {
		t.Fatal("PostgreSQL-backed injector must be wired when Milvus is unavailable")
	}
	if c.Memory.RecallFn != nil {
		t.Fatal("vector recall must remain disabled without Milvus")
	}
}

func TestBuildTenantLLMWorkersUsesDynamicProcessorsWithoutEagerResolve(t *testing.T) {
	resolved := 0
	resolver := func(context.Context, string) (memworkers.TenantLLMClient, error) {
		resolved++
		return completionClientForWiringTest{}, nil
	}

	// baseline nil：证明 worker 构造不因基线缺失而解析（懒注入，纯内存兜底）。
	workerSet := appendTenantLLMWorkers(nil, "tenant-1", nil, nil, resolver, nil, "", zap.NewNop())
	if resolved != 0 {
		t.Fatalf("tenant LLM resolved during worker construction: %d calls", resolved)
	}
	if len(workerSet) != 2 {
		t.Fatalf("dynamic worker count = %d, want supersede and history", len(workerSet))
	}
	if _, ok := workerSet[0].(*memworkers.SupersedeWorker); !ok {
		t.Fatalf("first dynamic worker = %T, want *SupersedeWorker", workerSet[0])
	}
	if _, ok := workerSet[1].(*memworkers.HistoryWorker); !ok {
		t.Fatalf("second dynamic worker = %T, want *HistoryWorker", workerSet[1])
	}
}

type completionClientForWiringTest struct{}

func (completionClientForWiringTest) Complete(context.Context, *memport.CompletionRequest) (*memport.CompletionResponse, error) {
	return &memport.CompletionResponse{}, nil
}

type nilCompletionClientForWiringTest struct{}

func (nilCompletionClientForWiringTest) Complete(context.Context, *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
	return nil, nil
}

func TestMemoryLLMAdapterRejectsNilProviderResponse(t *testing.T) {
	_, err := (memoryLLMAdapter{client: nilCompletionClientForWiringTest{}}).Complete(
		context.Background(), &memport.CompletionRequest{},
	)
	if err == nil {
		t.Fatal("expected nil provider response to fail closed")
	}
}

type tenantCapturingClientForWiringTest struct{ gotTenant string }

func (c *tenantCapturingClientForWiringTest) Complete(ctx context.Context, _ *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
	c.gotTenant = reqctx.TenantIDFromContext(ctx)
	return &llmdomain.CompletionResponse{Usage: llmdomain.TokenUsage{CompletionTokens: 1}}, nil
}

func TestMemoryLLMAdapterInjectsTenantIntoContext(t *testing.T) {
	client := &tenantCapturingClientForWiringTest{}
	adapter := memoryLLMAdapter{client: client, tenantID: "tenant-123"}
	if _, err := adapter.Complete(context.Background(), &memport.CompletionRequest{}); err != nil {
		t.Fatal(err)
	}
	if client.gotTenant != "tenant-123" {
		t.Fatalf("gateway saw tenant %q, want %q", client.gotTenant, "tenant-123")
	}
}

func TestMemoryLLMAdapterEmptyTenantKeepsContextTenant(t *testing.T) {
	client := &tenantCapturingClientForWiringTest{}
	adapter := memoryLLMAdapter{client: client}
	ctx := reqctx.WithTenantID(context.Background(), "ctx-tenant")
	if _, err := adapter.Complete(ctx, &memport.CompletionRequest{}); err != nil {
		t.Fatal(err)
	}
	if client.gotTenant != "ctx-tenant" {
		t.Fatalf("gateway saw tenant %q, want existing ctx tenant %q", client.gotTenant, "ctx-tenant")
	}
}

// requestCapturingGatewayCompleter 捕获传给 llmgateway 的完整请求（逐字段重建
// 由 memoryLLMAdapter 完成，这里验证 response_format 透传）。
type requestCapturingGatewayCompleter struct{ captured *llmdomain.CompletionRequest }

func (c *requestCapturingGatewayCompleter) Complete(_ context.Context, req *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
	cloned := *req
	c.captured = &cloned
	return &llmdomain.CompletionResponse{Usage: llmdomain.TokenUsage{CompletionTokens: 1}}, nil
}

func TestMemoryLLMAdapterForwardsResponseFormat(t *testing.T) {
	t.Run("json_object passthrough", func(t *testing.T) {
		client := &requestCapturingGatewayCompleter{}
		adapter := memoryLLMAdapter{client: client}
		if _, err := adapter.Complete(context.Background(), &memport.CompletionRequest{
			ResponseFormat: &memport.ResponseFormat{Type: "json_object"},
		}); err != nil {
			t.Fatal(err)
		}
		if client.captured == nil || client.captured.ResponseFormat == nil {
			t.Fatalf("gateway request must carry response_format, got %#v", client.captured)
		}
		if client.captured.ResponseFormat.Type != "json_object" {
			t.Fatalf("response_format type = %q, want json_object", client.captured.ResponseFormat.Type)
		}
	})

	t.Run("nil stays nil", func(t *testing.T) {
		client := &requestCapturingGatewayCompleter{}
		adapter := memoryLLMAdapter{client: client}
		if _, err := adapter.Complete(context.Background(), &memport.CompletionRequest{}); err != nil {
			t.Fatal(err)
		}
		if client.captured == nil || client.captured.ResponseFormat != nil {
			t.Fatalf("nil response_format must stay nil, got %#v", client.captured)
		}
	})
}

// resolverAgentRepoStub implements only Get (used through the lazily-captured
// func() agentport.AgentRepo seam); every other method is an unreachable no-op.
type resolverAgentRepoStub struct {
	cfg *agentdomain.AgentConfig
	ok  bool
	err error
}

func (s *resolverAgentRepoStub) Get(context.Context, string) (*agentdomain.AgentConfig, bool, error) {
	return s.cfg, s.ok, s.err
}

func (s *resolverAgentRepoStub) Register(context.Context, *agentdomain.AgentConfig, *auditdomain.ResourceChangeAuditEvent, []string) error {
	return nil
}
func (s *resolverAgentRepoStub) GetSystemAssistant(context.Context) (*agentdomain.AgentConfig, bool, error) {
	return nil, false, nil
}
func (s *resolverAgentRepoStub) GetAll(context.Context) ([]*agentdomain.AgentConfig, error) {
	return nil, nil
}
func (s *resolverAgentRepoStub) Remove(context.Context, string, *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (s *resolverAgentRepoStub) Update(context.Context, *agentdomain.AgentConfig, *auditdomain.ResourceChangeAuditEvent, string, bool) error {
	return nil
}
func (s *resolverAgentRepoStub) UpdateSystemAssistantModel(context.Context, string, string, bool, int, int, *auditdomain.ResourceChangeAuditEvent) (*agentdomain.AgentConfig, error) {
	return nil, nil
}
func (s *resolverAgentRepoStub) UpdateSystemAssistantAll(context.Context, string, string, bool, int, int, int, *auditdomain.ResourceChangeAuditEvent) (*agentdomain.AgentConfig, error) {
	return nil, nil
}

// wiringPlatformStore is an in-memory PlatformStore for adapter tests (the
// parameters application package keeps its own copy; wiring must not import it).
type wiringPlatformStore struct {
	values map[string]json.RawMessage
}

func (m *wiringPlatformStore) GetValue(_ context.Context, key string) (json.RawMessage, bool, error) {
	raw, ok := m.values[key]
	return raw, ok, nil
}

func (m *wiringPlatformStore) SetValue(_ context.Context, key string, value json.RawMessage, _ string) error {
	m.values[key] = value
	return nil
}

func (m *wiringPlatformStore) GetAll(_ context.Context) ([]parametersport.PlatformValue, error) {
	var out []parametersport.PlatformValue
	for k, v := range m.values {
		out = append(out, parametersport.PlatformValue{Key: k, Value: v})
	}
	return out, nil
}

// TestAgentResourceParamResolverFallback 守护 agentResourceParamResolver 的编排:
// 命中 agent 记录用其声明值;无声明回落 registry 默认(迁移删平台行后不再有
// platform 中间层);repo 缺失/不存在/报错一律 fail-closed 到 absent。
func TestAgentResourceParamResolverFallback(t *testing.T) {
	svc := parametersapp.NewService(parametersdomain.NewParametersRegistry(), &wiringPlatformStore{values: map[string]json.RawMessage{}})
	resolver := func(repo agentport.AgentRepo) agentResourceParamResolver {
		return agentResourceParamResolver{agentRepo: func() agentport.AgentRepo { return repo }, svc: svc}
	}

	t.Run("declared memory value wins", func(t *testing.T) {
		r := resolver(&resolverAgentRepoStub{ok: true, cfg: &agentdomain.AgentConfig{
			MemoryParameters: map[string]any{"memory.max_facts_per_extraction": 35},
		}})
		v, ok, err := r.Resolve(context.Background(), "t1", "a1", "memory.max_facts_per_extraction")
		if err != nil {
			t.Fatalf("resolve error: %v", err)
		}
		if !ok {
			t.Fatal("declared value must resolve as present")
		}
		vv, isInt := v.(int64)
		if !isInt || vv != 35 {
			t.Fatalf("got v=%#v (%T), want int64(35)", v, v)
		}
	})

	t.Run("absent declaration falls to definition default", func(t *testing.T) {
		r := resolver(&resolverAgentRepoStub{ok: true, cfg: &agentdomain.AgentConfig{}})
		v, ok, err := r.Resolve(context.Background(), "t1", "a1", "memory.max_facts_per_extraction")
		if err != nil || !ok || v != int64(20) {
			t.Fatalf("got (%v, %v, %v), want (20, true, nil)", v, ok, err)
		}
	})

	t.Run("agent not found reports absent", func(t *testing.T) {
		r := resolver(&resolverAgentRepoStub{ok: false})
		if _, ok, err := r.Resolve(context.Background(), "t1", "a1", "memory.max_facts_per_extraction"); ok || err != nil {
			t.Fatalf("got (%v, %v), want (false, nil)", ok, err)
		}
	})

	t.Run("repo error propagates", func(t *testing.T) {
		r := resolver(&resolverAgentRepoStub{err: errors.New("db down")})
		if _, _, err := r.Resolve(context.Background(), "t1", "a1", "memory.max_facts_per_extraction"); err == nil {
			t.Fatal("repo error must propagate, not degrade silently")
		}
	})

	t.Run("nil service or repo stays absent (build-order degrade)", func(t *testing.T) {
		r := agentResourceParamResolver{agentRepo: func() agentport.AgentRepo { return nil }, svc: svc}
		if _, ok, err := r.Resolve(context.Background(), "t1", "a1", "memory.max_facts_per_extraction"); ok || err != nil {
			t.Fatalf("nil repo must be absent: (%v, %v)", ok, err)
		}
		if _, ok, err := (agentResourceParamResolver{agentRepo: func() agentport.AgentRepo { return &resolverAgentRepoStub{ok: true, cfg: &agentdomain.AgentConfig{}} }, svc: nil}).Resolve(context.Background(), "t1", "a1", "memory.max_facts_per_extraction"); ok || err != nil {
			t.Fatalf("nil svc must be absent: (%v, %v)", ok, err)
		}
	})
}
