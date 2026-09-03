package wiring

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/config"
	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
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

	workerSet := appendTenantLLMWorkers(nil, "tenant-1", nil, nil, nil, resolver, nil, zap.NewNop())
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

func (completionClientForWiringTest) Complete(context.Context, *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
	return &llmdomain.CompletionResponse{}, nil
}

type nilCompletionClientForWiringTest struct{}

func (nilCompletionClientForWiringTest) Complete(context.Context, *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
	return nil, nil
}

func TestMemoryLLMAdapterRejectsNilProviderResponse(t *testing.T) {
	_, err := (memoryLLMAdapter{client: nilCompletionClientForWiringTest{}}).Complete(
		context.Background(), &llmdomain.CompletionRequest{},
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
	if _, err := adapter.Complete(context.Background(), &llmdomain.CompletionRequest{}); err != nil {
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
	if _, err := adapter.Complete(ctx, &llmdomain.CompletionRequest{}); err != nil {
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
		if _, err := adapter.Complete(context.Background(), &llmdomain.CompletionRequest{
			ResponseFormat: &llmdomain.ResponseFormat{Type: "json_object"},
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
		if _, err := adapter.Complete(context.Background(), &llmdomain.CompletionRequest{}); err != nil {
			t.Fatal(err)
		}
		if client.captured == nil || client.captured.ResponseFormat != nil {
			t.Fatalf("nil response_format must stay nil, got %#v", client.captured)
		}
	})
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

// GetSnapshot 按 GroupForKey 过滤出该组快照（模拟 production label 所指版本
// 的不可变快照）；GetValue 保留读取路径的单一 key 查询语义。
func (m *wiringPlatformStore) GetSnapshot(_ context.Context, groupKey string) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage)
	for key, raw := range m.values {
		if parametersdomain.GroupForKey(key) == groupKey {
			out[key] = raw
		}
	}
	return out, nil
}

func (m *wiringPlatformStore) CreateDraft(_ context.Context, _ string, _ map[string]json.RawMessage, _, _ string) (parametersport.PlatformVersion, error) {
	return parametersport.PlatformVersion{}, nil
}

func (m *wiringPlatformStore) Publish(_ context.Context, _ string, _ int64, _ string) error {
	return nil
}

func (m *wiringPlatformStore) Rollback(_ context.Context, _ string, _ int64, _ string) error {
	return nil
}

func (m *wiringPlatformStore) ListVersions(_ context.Context, _ string) ([]parametersport.PlatformVersion, error) {
	return []parametersport.PlatformVersion{}, nil
}

// GetVersion/UpdateEvalState 补齐接口（分层门禁 P1）：wiringPlatformStore 只模拟
// 单值 settings（无版本列），版本寻址恒 ErrVersionNotFound。
func (m *wiringPlatformStore) GetVersion(context.Context, string, int64) (parametersport.PlatformVersion, error) {
	return parametersport.PlatformVersion{}, parametersdomain.ErrVersionNotFound
}

func (m *wiringPlatformStore) UpdateEvalState(context.Context, string, int64, string, string) error {
	return parametersdomain.ErrVersionNotFound
}

// TestPlatformParamResolverFallback 守护 platformParamResolver 的编排：
// 平台声明值优先；无声明回落 registry 定义默认；svc 缺失 fail-closed 到 absent。
func TestPlatformParamResolverFallback(t *testing.T) {
	svc := parametersapp.NewService(parametersdomain.NewParametersRegistry(), &wiringPlatformStore{values: map[string]json.RawMessage{}})
	r := platformParamResolver{svc: svc}

	t.Run("platform declared value wins", func(t *testing.T) {
		store := &wiringPlatformStore{values: map[string]json.RawMessage{
			"memory.max_facts_per_extraction": json.RawMessage(`8`),
		}}
		r2 := platformParamResolver{svc: parametersapp.NewService(parametersdomain.NewParametersRegistry(), store)}
		v, ok, err := r2.ResolvePlatform(context.Background(), "memory.max_facts_per_extraction")
		if err != nil || !ok || v == nil {
			t.Fatalf("got (%v, %v, %v), want platform value present", v, ok, err)
		}
	})

	t.Run("absent platform value falls to definition default", func(t *testing.T) {
		v, ok, err := r.ResolvePlatform(context.Background(), "memory.max_facts_per_extraction")
		if err != nil || !ok || v == nil {
			t.Fatalf("got (%v, %v, %v), want definition default present", v, ok, err)
		}
	})

	t.Run("nil service stays absent", func(t *testing.T) {
		var nilResolver platformParamResolver
		if _, ok, err := nilResolver.ResolvePlatform(context.Background(), "memory.max_facts_per_extraction"); ok || err != nil {
			t.Fatalf("nil svc must be absent: (%v, %v)", ok, err)
		}
	})
}
