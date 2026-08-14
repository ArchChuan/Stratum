package wiring

import (
	"context"
	"testing"

	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	memworkers "github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/config"
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
