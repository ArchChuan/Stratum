package wiring

import (
	"context"
	"testing"

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
