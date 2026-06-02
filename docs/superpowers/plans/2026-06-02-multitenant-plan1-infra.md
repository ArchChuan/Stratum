# Multi-Tenant Plan 1: 基础设施 — PostgreSQL + Redis 接入

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在项目中接入 PostgreSQL 和 Redis，建立 public schema 的所有基础表，并提供连接池封装供后续计划使用。

**Architecture:** 新增 `pkg/postgres` 和 `pkg/redis` 两个独立包，封装连接池；新增 `internal/migration` 负责 public schema 的 DDL 执行；docker-compose 添加 postgres 和 redis 服务。

**Tech Stack:** Go 1.24, `github.com/jackc/pgx/v5`（PostgreSQL），`github.com/redis/go-redis/v9`（Redis），`golang-migrate/migrate/v4`（schema migration）

---

## File Map

| 文件 | 类型 | 职责 |
|------|------|------|
| `docker-compose.yml` | Modify | 添加 postgres、redis 服务 |
| `pkg/postgres/postgres.go` | Create | pgx 连接池封装，`New`/`Close`/`DB()` |
| `pkg/postgres/postgres_test.go` | Create | 连接测试（integration tag） |
| `pkg/redis/redis.go` | Create | go-redis 客户端封装，`New`/`Close`/`Client()` |
| `pkg/redis/redis_test.go` | Create | ping 测试（integration tag） |
| `internal/migration/migration.go` | Create | 执行 SQL 文件的 migration runner |
| `internal/migration/public_schema.sql` | Create | public schema 所有建表 DDL |
| `internal/config/config.go` | Modify | 添加 PostgresURL、RedisURL 字段 |
| `cmd/server/main.go` | Modify | 启动时初始化 postgres、redis，执行 migration |

---

### Task 1: docker-compose 添加 postgres 和 redis

**Files:**
- Modify: `docker-compose.yml`

- [ ] **Step 1: 在 docker-compose.yml 的 services 块末尾添加 postgres 和 redis**

```yaml
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: clawhermes
      POSTGRES_PASSWORD: clawhermes
      POSTGRES_DB: clawhermes
    ports:
      - "5432:5432"
    volumes:
      - postgres_data:/var/lib/postgresql/data

  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    command: redis-server --maxmemory 256mb --maxmemory-policy allkeys-lru
```

在文件末尾的 `volumes:` 块添加：

```yaml
  postgres_data:
```

- [ ] **Step 2: 验证 compose 语法**

```bash
docker compose config --quiet
```

Expected: 无错误输出

- [ ] **Step 3: 启动新服务**

```bash
docker compose up -d postgres redis
```

Expected: 两个容器 Started 状态

- [ ] **Step 4: 验证连通性**

```bash
docker compose exec postgres psql -U clawhermes -c "SELECT version();"
docker compose exec redis redis-cli ping
```

Expected: postgres 打印版本信息，redis 返回 PONG

- [ ] **Step 5: Commit**

```bash
git add docker-compose.yml
git commit -m "chore: add postgres and redis to docker-compose"
```

---

### Task 2: 添加 Go 依赖

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: 安装依赖**

```bash
go get github.com/jackc/pgx/v5@v5.7.2
go get github.com/jackc/pgx/v5/pgxpool@v5.7.2
go get github.com/redis/go-redis/v9@v9.7.3
go get github.com/golang-migrate/migrate/v4@v4.18.1
go get github.com/golang-migrate/migrate/v4/database/pgx/v5@v4.18.1
go get github.com/golang-migrate/migrate/v4/source/file@v4.18.1
```

- [ ] **Step 2: 整理 go.mod**

```bash
go mod tidy
```

Expected: 无错误

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add pgx, go-redis, golang-migrate dependencies"
```

---

### Task 3: 封装 PostgreSQL 连接池

**Files:**
- Create: `pkg/postgres/postgres.go`
- Create: `pkg/postgres/postgres_test.go`

- [ ] **Step 1: 写失败测试**

新建 `pkg/postgres/postgres_test.go`：

```go
//go:build integration

package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/postgres"
	"go.uber.org/zap"
)

func TestNew_Connect(t *testing.T) {
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		url = "postgres://clawhermes:clawhermes@localhost:5432/clawhermes"
	}

	logger := zap.NewNop()
	pool, err := postgres.New(context.Background(), url, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer pool.Close()

	if err := pool.DB().Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test -v -tags=integration ./pkg/postgres/...
```

Expected: `FAIL` — package postgres not found

- [ ] **Step 3: 实现 postgres.go**

新建 `pkg/postgres/postgres.go`：

```go
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type Pool struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

func New(ctx context.Context, url string, logger *zap.Logger) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse config: %w", err)
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	logger.Info("postgres connected", zap.String("url", maskPassword(url)))
	return &Pool{pool: pool, logger: logger}, nil
}

func (p *Pool) DB() *pgxpool.Pool { return p.pool }

func (p *Pool) Close() {
	p.pool.Close()
	p.logger.Info("postgres connection closed")
}

func maskPassword(url string) string {
	// 仅用于日志，隐藏密码部分
	return "postgres://***@" + extractHost(url)
}

func extractHost(url string) string {
	// 简单提取 @host 部分用于日志
	for i := len(url) - 1; i >= 0; i-- {
		if url[i] == '@' {
			return url[i+1:]
		}
	}
	return url
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -v -tags=integration ./pkg/postgres/...
```

Expected: `PASS TestNew_Connect`

- [ ] **Step 5: Commit**

```bash
git add pkg/postgres/
git commit -m "feat(postgres): add connection pool package"
```

---

### Task 4: 封装 Redis 客户端

**Files:**
- Create: `pkg/redis/redis.go`
- Create: `pkg/redis/redis_test.go`

- [ ] **Step 1: 写失败测试**

新建 `pkg/redis/redis_test.go`：

```go
//go:build integration

package redis_test

import (
	"context"
	"os"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/redis"
	"go.uber.org/zap"
)

func TestNew_Ping(t *testing.T) {
	url := os.Getenv("REDIS_URL")
	if url == "" {
		url = "redis://localhost:6379"
	}

	logger := zap.NewNop()
	client, err := redis.New(context.Background(), url, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer client.Close()

	if err := client.Client().Ping(context.Background()).Err(); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test -v -tags=integration ./pkg/redis/...
```

Expected: `FAIL` — package redis not found

- [ ] **Step 3: 实现 redis.go**

新建 `pkg/redis/redis.go`：

```go
package redis

import (
	"context"
	"fmt"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type Client struct {
	client *goredis.Client
	logger *zap.Logger
}

func New(ctx context.Context, url string, logger *zap.Logger) (*Client, error) {
	opts, err := goredis.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("redis: parse url: %w", err)
	}

	client := goredis.NewClient(opts)
	if err := client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, fmt.Errorf("redis: ping: %w", err)
	}

	logger.Info("redis connected", zap.String("addr", opts.Addr))
	return &Client{client: client, logger: logger}, nil
}

func (c *Client) Client() *goredis.Client { return c.client }

func (c *Client) Close() error {
	c.logger.Info("redis connection closed")
	return c.client.Close()
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test -v -tags=integration ./pkg/redis/...
```

Expected: `PASS TestNew_Ping`

- [ ] **Step 5: Commit**

```bash
git add pkg/redis/
git commit -m "feat(redis): add client package"
```

---

### Task 5: Public Schema Migration

**Files:**
- Create: `internal/migration/migration.go`
- Create: `internal/migration/sql/001_public_schema.up.sql`
- Create: `internal/migration/sql/001_public_schema.down.sql`

- [ ] **Step 1: 创建 DDL 文件 `internal/migration/sql/001_public_schema.up.sql`**

```sql
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- 用户表
CREATE TABLE IF NOT EXISTS users (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  github_id       TEXT UNIQUE NOT NULL,
  github_login    TEXT NOT NULL,
  email           TEXT,
  display_name    TEXT,
  avatar_url      TEXT,
  global_role     TEXT NOT NULL DEFAULT 'user',
  last_login_at   TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 租户表
CREATE TABLE IF NOT EXISTS tenants (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  name            TEXT NOT NULL,
  slug            TEXT UNIQUE NOT NULL,
  github_org_id   TEXT,
  github_org_name TEXT,
  avatar_url      TEXT,
  plan            TEXT NOT NULL DEFAULT 'free',
  status          TEXT NOT NULL DEFAULT 'active',
  settings        JSONB NOT NULL DEFAULT '{}',
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  deleted_at      TIMESTAMPTZ
);

-- 租户成员
CREATE TABLE IF NOT EXISTS tenant_members (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role            TEXT NOT NULL DEFAULT 'member',
  invited_by      UUID REFERENCES users(id),
  joined_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, user_id)
);

-- 邀请
CREATE TABLE IF NOT EXISTS invitations (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  email           TEXT NOT NULL,
  role            TEXT NOT NULL DEFAULT 'member',
  token_hash      TEXT NOT NULL,
  invited_by      UUID NOT NULL REFERENCES users(id),
  expires_at      TIMESTAMPTZ NOT NULL,
  accepted_at     TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Refresh Token
CREATE TABLE IF NOT EXISTS refresh_tokens (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  tenant_id       UUID REFERENCES tenants(id) ON DELETE CASCADE,
  token_hash      TEXT NOT NULL,
  device_info     JSONB,
  issued_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at      TIMESTAMPTZ NOT NULL,
  last_used_at    TIMESTAMPTZ,
  revoked_at      TIMESTAMPTZ
);

-- 租户 API Key
CREATE TABLE IF NOT EXISTS tenant_api_keys (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  display_name    TEXT NOT NULL,
  key_hash        TEXT NOT NULL,
  key_prefix      TEXT NOT NULL,
  scopes          TEXT[] NOT NULL DEFAULT '{}',
  expires_at      TIMESTAMPTZ,
  last_used_at    TIMESTAMPTZ,
  created_by      UUID REFERENCES users(id),
  revoked_at      TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 审计日志
CREATE TABLE IF NOT EXISTS audit_logs (
  id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
  tenant_id       UUID REFERENCES tenants(id) ON DELETE SET NULL,
  user_id         UUID,
  action          TEXT NOT NULL,
  resource_type   TEXT,
  resource_id     TEXT,
  old_value       JSONB,
  new_value       JSONB,
  ip_address      TEXT,
  user_agent      TEXT,
  trace_id        TEXT,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 模型供应商
CREATE TABLE IF NOT EXISTS model_providers (
  id                    TEXT PRIMARY KEY,
  name                  TEXT NOT NULL,
  base_url              TEXT,
  auth_type             TEXT NOT NULL DEFAULT 'bearer',
  supports_completion   BOOL NOT NULL DEFAULT true,
  supports_embedding    BOOL NOT NULL DEFAULT false,
  supports_streaming    BOOL NOT NULL DEFAULT false,
  supports_vision       BOOL NOT NULL DEFAULT false,
  status                TEXT NOT NULL DEFAULT 'active',
  created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 模型目录
CREATE TABLE IF NOT EXISTS models (
  id                        TEXT PRIMARY KEY,
  provider_id               TEXT NOT NULL REFERENCES model_providers(id),
  display_name              TEXT NOT NULL,
  type                      TEXT NOT NULL,
  context_window            INT,
  max_output_tokens         INT,
  input_cost_per_1k         NUMERIC(10,6),
  output_cost_per_1k        NUMERIC(10,6),
  embedding_dimensions      INT,
  supports_streaming        BOOL NOT NULL DEFAULT true,
  supports_function_calling BOOL NOT NULL DEFAULT false,
  supports_vision           BOOL NOT NULL DEFAULT false,
  supports_json_mode        BOOL NOT NULL DEFAULT false,
  is_deprecated             BOOL NOT NULL DEFAULT false,
  deprecated_at             TIMESTAMPTZ,
  available_from            TIMESTAMPTZ NOT NULL DEFAULT now(),
  metadata                  JSONB NOT NULL DEFAULT '{}'
);

-- 内置供应商种子数据
INSERT INTO model_providers (id, name, base_url, auth_type, supports_completion, supports_embedding, supports_streaming)
VALUES
  ('openai',    'OpenAI',    'https://api.openai.com/v1',    'bearer',    true, true,  true),
  ('anthropic', 'Anthropic', 'https://api.anthropic.com/v1', 'x-api-key', true, false, true),
  ('ollama',    'Ollama',    'http://localhost:11434',        'none',      true, false, true)
ON CONFLICT (id) DO NOTHING;

INSERT INTO models (id, provider_id, display_name, type, context_window, max_output_tokens, input_cost_per_1k, output_cost_per_1k, supports_function_calling)
VALUES
  ('gpt-4o',              'openai',    'GPT-4o',              'completion', 128000, 4096,  0.005000, 0.015000, true),
  ('gpt-4o-mini',         'openai',    'GPT-4o Mini',         'completion', 128000, 16384, 0.000150, 0.000600, true),
  ('text-embedding-3-small', 'openai', 'Embedding 3 Small',   'embedding',  8191,   NULL,  0.000020, 0.000000, false),
  ('claude-opus-4-6',     'anthropic', 'Claude Opus 4.6',     'completion', 200000, 4096,  0.015000, 0.075000, true),
  ('claude-sonnet-4-6',   'anthropic', 'Claude Sonnet 4.6',   'completion', 200000, 8096,  0.003000, 0.015000, true),
  ('claude-haiku-4-5',    'anthropic', 'Claude Haiku 4.5',    'completion', 200000, 4096,  0.000800, 0.004000, true)
ON CONFLICT (id) DO NOTHING;

-- 索引
CREATE INDEX IF NOT EXISTS idx_tenant_members_user    ON tenant_members(user_id);
CREATE INDEX IF NOT EXISTS idx_tenant_members_tenant  ON tenant_members(tenant_id);
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user    ON refresh_tokens(user_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_tenant      ON audit_logs(tenant_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created     ON audit_logs(created_at DESC);
```

- [ ] **Step 2: 创建 down migration**

新建 `internal/migration/sql/001_public_schema.down.sql`：

```sql
DROP TABLE IF EXISTS models;
DROP TABLE IF EXISTS model_providers;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS tenant_api_keys;
DROP TABLE IF EXISTS refresh_tokens;
DROP TABLE IF EXISTS invitations;
DROP TABLE IF EXISTS tenant_members;
DROP TABLE IF EXISTS tenants;
DROP TABLE IF EXISTS users;
DROP EXTENSION IF EXISTS "uuid-ossp";
```

- [ ] **Step 3: 实现 migration runner**

新建 `internal/migration/migration.go`：

```go
package migration

import (
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"go.uber.org/zap"
)

func RunPublicSchema(postgresURL string, logger *zap.Logger) error {
	m, err := migrate.New("file://internal/migration/sql", postgresURL)
	if err != nil {
		return fmt.Errorf("migration: init: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migration: up: %w", err)
	}

	logger.Info("public schema migration complete")
	return nil
}
```

- [ ] **Step 4: 写 migration 集成测试**

新建 `internal/migration/migration_test.go`：

```go
//go:build integration

package migration_test

import (
	"os"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/migration"
	"go.uber.org/zap"
)

func TestRunPublicSchema(t *testing.T) {
	url := os.Getenv("POSTGRES_URL")
	if url == "" {
		url = "pgx5://clawhermes:clawhermes@localhost:5432/clawhermes"
	}

	logger := zap.NewNop()
	if err := migration.RunPublicSchema(url, logger); err != nil {
		t.Fatalf("RunPublicSchema() error = %v", err)
	}
}
```

- [ ] **Step 5: 运行测试确认通过**

```bash
go test -v -tags=integration ./internal/migration/...
```

Expected: `PASS TestRunPublicSchema`

- [ ] **Step 6: Commit**

```bash
git add internal/migration/ pkg/postgres/ pkg/redis/
git commit -m "feat(migration): add public schema DDL and migration runner"
```

---

### Task 6: 更新 Config，接入 main.go

**Files:**
- Modify: `internal/config/config.go`
- Modify: `cmd/server/main.go`

- [ ] **Step 1: 在 config.go 的 Config 结构体添加字段**

在 `internal/config/config.go` 的 `Config` struct 中添加：

```go
PostgresURL string
RedisURL    string
```

在 `Load()` 函数中添加：

```go
PostgresURL: getEnv("POSTGRES_URL", "postgres://clawhermes:clawhermes@localhost:5432/clawhermes"),
RedisURL:    getEnv("REDIS_URL", "redis://localhost:6379"),
```

- [ ] **Step 2: 运行现有单元测试确认未破坏**

```bash
go test -v ./internal/config/...
```

Expected: 全部 PASS

- [ ] **Step 3: 在 main.go 初始化 postgres、redis，执行 migration**

在 `cmd/server/main.go` 中，在现有服务初始化之后添加：

```go
import (
    "github.com/byteBuilderX/ClawHermes-AI-Go/internal/migration"
    "github.com/byteBuilderX/ClawHermes-AI-Go/pkg/postgres"
    pkgredis "github.com/byteBuilderX/ClawHermes-AI-Go/pkg/redis"
)

// 在 cfg 加载之后添加：
pgPool, err := postgres.New(ctx, cfg.PostgresURL, logger)
if err != nil {
    logger.Fatal("failed to connect postgres", zap.Error(err))
}
defer pgPool.Close()

redisClient, err := pkgredis.New(ctx, cfg.RedisURL, logger)
if err != nil {
    logger.Fatal("failed to connect redis", zap.Error(err))
}
defer redisClient.Close()

if err := migration.RunPublicSchema(cfg.PostgresURL, logger); err != nil {
    logger.Fatal("migration failed", zap.Error(err))
}
```

- [ ] **Step 4: 编译验证**

```bash
go build ./cmd/server/...
```

Expected: 编译成功，无错误

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go cmd/server/main.go
git commit -m "feat: wire postgres and redis into server startup"
```

---

### Task 7: 全量测试验证

- [ ] **Step 1: 运行单元测试（无需 Docker）**

```bash
go test -v -race ./... -short
```

Expected: 全部 PASS，无新增失败

- [ ] **Step 2: 运行集成测试（需 Docker 服务运行）**

```bash
go test -v -tags=integration ./pkg/postgres/... ./pkg/redis/... ./internal/migration/...
```

Expected: 全部 PASS

- [ ] **Step 3: 验证 public schema 表已创建**

```bash
docker compose exec postgres psql -U clawhermes -c "\dt public.*"
```

Expected: 列出 users, tenants, tenant_members, invitations, refresh_tokens, tenant_api_keys, audit_logs, model_providers, models

- [ ] **Step 4: Final commit**

```bash
git add .
git commit -m "chore: plan1 complete — postgres/redis infra and public schema"
```
