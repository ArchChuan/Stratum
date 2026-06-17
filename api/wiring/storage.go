package wiring

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	pkgnats "github.com/byteBuilderX/stratum/pkg/messaging/nats"
	"github.com/byteBuilderX/stratum/pkg/storage/milvus"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	pkgredis "github.com/byteBuilderX/stratum/pkg/storage/redis"
)

// Storage groups the infrastructure-level clients shared across
// application services. NATS and JetStream are optional: if NATS
// connection fails we degrade gracefully (matching cmd/server/main.go).
type Storage struct {
	PG     *postgres.Pool
	Redis  *pkgredis.Client
	Milvus *milvus.VectorStore
	NATS   *nats.Conn
	JS     nats.JetStreamContext
}

func (c *Container) buildStorage(ctx context.Context) error {
	pg, err := postgres.New(ctx, c.Config.PostgresURL, c.Logger)
	if err != nil {
		return fmt.Errorf("postgres connect: %w", err)
	}
	c.shutdown = append(c.shutdown, func(_ context.Context) error { pg.Close(); return nil })

	rdb, err := pkgredis.New(ctx, c.Config.RedisURL, c.Logger)
	if err != nil {
		return fmt.Errorf("redis connect: %w", err)
	}
	c.shutdown = append(c.shutdown, func(_ context.Context) error { return rdb.Close() })

	mil := milvus.NewVectorStore(c.Config.MilvusHost, c.Config.MilvusPort, c.Logger)
	if err := mil.Connect(ctx); err != nil {
		// Non-fatal — match cmd/server/main.go behavior.
		c.Logger.Warn("failed to connect to Milvus", zap.Error(err))
	}
	c.shutdown = append(c.shutdown, func(_ context.Context) error { return mil.Close() })

	// NATS is optional in this codebase; hermes/pipeline degrade gracefully.
	nc, err := pkgnats.Connect(c.Config.NatsURL)
	if err != nil {
		c.Logger.Warn("NATS connect failed", zap.Error(err))
		c.Storage = &Storage{PG: pg, Redis: rdb, Milvus: mil}
		return nil
	}
	c.shutdown = append(c.shutdown, func(_ context.Context) error { nc.Close(); return nil })

	js, err := nc.JetStream()
	if err != nil {
		c.Logger.Warn("JetStream context init failed", zap.Error(err))
		c.Storage = &Storage{PG: pg, Redis: rdb, Milvus: mil, NATS: nc}
		return nil
	}

	c.Storage = &Storage{PG: pg, Redis: rdb, Milvus: mil, NATS: nc, JS: js}
	return nil
}
