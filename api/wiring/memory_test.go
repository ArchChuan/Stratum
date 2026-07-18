package wiring

import (
	"context"
	"testing"

	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	memworkers "github.com/byteBuilderX/stratum/internal/memory/infrastructure/workers"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/config"
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

	workerSet := appendTenantLLMWorkers(nil, "tenant-1", nil, nil, nil, resolver, zap.NewNop())
	if resolved != 0 {
		t.Fatalf("tenant LLM resolved during worker construction: %d calls", resolved)
	}
	if len(workerSet) != 3 {
		t.Fatalf("dynamic worker count = %d, want supersede, profile and history", len(workerSet))
	}
	if _, ok := workerSet[0].(*memworkers.SupersedeWorker); !ok {
		t.Fatalf("first dynamic worker = %T, want *SupersedeWorker", workerSet[0])
	}
	if _, ok := workerSet[1].(*memworkers.ProfileWorker); !ok {
		t.Fatalf("second dynamic worker = %T, want *ProfileWorker", workerSet[1])
	}
	if _, ok := workerSet[2].(*memworkers.HistoryWorker); !ok {
		t.Fatalf("third dynamic worker = %T, want *HistoryWorker", workerSet[2])
	}
}

type completionClientForWiringTest struct{}

func (completionClientForWiringTest) Complete(context.Context, *llmdomain.CompletionRequest) (*llmdomain.CompletionResponse, error) {
	return &llmdomain.CompletionResponse{}, nil
}
