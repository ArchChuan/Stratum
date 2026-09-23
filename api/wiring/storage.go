package wiring

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/constants"
	pkgnats "github.com/byteBuilderX/stratum/pkg/messaging/nats"
	"github.com/byteBuilderX/stratum/pkg/storage/milvus"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	pkgredis "github.com/byteBuilderX/stratum/pkg/storage/redis"
)

// Storage groups the infrastructure-level clients shared across
// application services.
//
// Degradation policy at startup: PostgreSQL is fatal (system of record).
// Milvus, NATS and Redis are not — their clients are constructed anyway and
// the failure is confined to the requests that need them (see connectRedis
// for the Redis fail-closed contract).
type Storage struct {
	PG     *postgres.Pool
	Redis  *pkgredis.Client
	Milvus *milvus.VectorStore
	NATS   *nats.Conn
	JS     nats.JetStreamContext
}

// connectRedis 构造 Redis 客户端并探测连通性。只有配置错误（非法 URL）是致命错误；
// 连通性失败降级启动：go-redis 在首个命令才拨号，失败后连接池按需重拨，所以 Redis
// 抖动/重启不会让进程进入 CrashLoop，恢复后无需重启即可继续工作。
//
// 降级不等于放行：Storage.Redis 保持非 nil，限流中间件在 Redis 报错时 fail closed
// （503 rate limit unavailable），禁止静默退化成进程内配额；token 黑名单查询同理
// 返回错误而不是「未吊销」。探针本身有超时预算，避免用 go-redis 默认的
// DialTimeout×重试把启动卡住。
func connectRedis(ctx context.Context, url string, logger *zap.Logger) (*pkgredis.Client, error) {
	rdb, err := pkgredis.New(url, logger)
	if err != nil {
		return nil, err
	}

	pingCtx, cancel := context.WithTimeout(ctx, constants.RedisStartupPingTimeout)
	defer cancel()
	if err := rdb.Ping(pingCtx); err != nil {
		logger.Warn("redis unavailable at startup, starting degraded",
			zap.String("addr", rdb.Addr()), zap.Error(err))
		return rdb, nil
	}
	logger.Info("redis connected", zap.String("addr", rdb.Addr()))
	return rdb, nil
}

func (c *Container) buildStorage(ctx context.Context) error {
	pg, err := postgres.New(ctx, c.Config.PostgresURL, c.Logger)
	if err != nil {
		return fmt.Errorf("postgres connect: %w", err)
	}
	c.shutdown = append(c.shutdown, func(_ context.Context) error { pg.Close(); return nil })

	rdb, err := connectRedis(ctx, c.Config.RedisURL, c.Logger)
	if err != nil {
		return fmt.Errorf("redis config: %w", err)
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
