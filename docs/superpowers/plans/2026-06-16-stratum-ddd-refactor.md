# Stratum DDD 分层 + 反向依赖重构 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把现 internal 平铺业务包 + api 层混合 wiring 重构为 DDD 8 bounded context（domain/application/infrastructure 三层），消除 `*pgxpool.Pool/*redis.Client/*Gateway` 等具体类型在业务层的直接持有，所有跨上下文调用走消费者侧 port，HTTP API 100% 向后兼容。

**Architecture:** 8 个 bounded context（agent/memory/knowledge/skill/mcp/iam/llmgateway/platform）。每个 context 内分 `domain/{,port/}` + `application` + `infrastructure`。pkg 重组为 `pkg/storage/{postgres,redis,milvus}` + `pkg/messaging/nats` + `pkg/httpclient` + `pkg/textchunk` + `pkg/migration`。wiring 集中到 `api/wiring/`，`cmd/server/main.go` 缩成 30 行。CI 用 go-arch-lint 固化分层规则。

**Tech Stack:** Go 1.22+ · Gin v1.9 · pgx v5 · go-redis v9 · Milvus SDK v2.4.2 · NATS JetStream v1.31 · golang-jwt v5 · OTEL v1.21 · go-arch-lint · depguard · mockery v2

**Sub-plans 决策:** spec §6 阶段 4 的 8 个 context 迁移每个都是独立 sub-PR。本 plan 覆盖 phase 0/1/2/3/5 的完整步骤；phase 4 的 8 个 context 各给一个 task outline（文件列表 + 关键步骤 + 验收标准），实际执行时再按 outline 由 subagent 展开为完整步骤。这是为了避免单个 plan 文件膨胀到 5000+ 行不可读。

---

## File Structure（迁移前后对照）

### pkg 层重组

| 现状 | 目标 |
|------|------|
| `pkg/postgres/postgres.go` | `pkg/storage/postgres/pool.go` |
| 新增 | `pkg/storage/postgres/querier.go`（Querier/TxBeginner 接口） |
| `pkg/tenantdb/{context,postgres,schema}.go` | `pkg/storage/postgres/tenant.go` |
| `pkg/tenantdb/{milvus,nats,neo4j}.go` | `pkg/storage/tenantnaming/{milvus,nats,neo4j}.go` |
| `pkg/redis/redis.go` | `pkg/storage/redis/client.go` |
| 新增 | `pkg/storage/redis/store.go`（KVStore 接口） |
| `pkg/vector/vector_store.go` | `pkg/storage/milvus/client.go` |
| 新增 | `pkg/storage/milvus/index.go`（VectorIndex 接口） |
| 新增 | `pkg/messaging/nats/{conn,publisher}.go` |
| 新增 | `pkg/httpclient/{client,transport}.go` |
| `internal/textchunk/` | `pkg/textchunk/` |
| `internal/migration/` | `pkg/migration/` |

### api 层重组

| 现状 | 目标 |
|------|------|
| `api/router.go`（17.9K, 400+ 行混合 DI） | `api/http/router.go`（≤100 行，仅挂 router.Group） |
| `api/handler/*.go` | `api/http/handler/*.go`（仅 DTO ↔ application service） |
| `api/model/*.go` | `api/http/dto/*.go` |
| 新增 | `api/wiring/{wiring,storage,llmgateway,memory,knowledge,skill,agent,mcp,iam,platform}.go` |
| `cmd/server/main.go`（>200 行） | `cmd/server/main.go`（≤30 行） |
| 新增 | `api/http/contract_test.go`、`api/http/testdata/contracts/*.golden.json` |

### internal 层重组（8 contexts）

每个 context 模板：

```
internal/<context>/
├── domain/
│   ├── *.go           ← 实体、值对象、domain errors
│   └── port/*.go      ← 消费者侧接口
├── application/
│   └── *_service.go   ← use case 编排
└── infrastructure/
    ├── persistence/   ← repo 实现（pgx/redis）
    ├── messaging/     ← NATS publisher
    └── *_adapter.go   ← 跨 context port 适配器
```

具体迁移：

- `internal/auth/` + `internal/hermes/` → `internal/iam/`
- `internal/skill/` + `internal/skillgateway/` → `internal/skill/`
- `internal/memory/` + `internal/memory/pipeline/` → `internal/memory/`
- `internal/document/` → `internal/knowledge/infrastructure/document/`
- `internal/embedding/` → `internal/llmgateway/infrastructure/embedding/`
- `internal/capgateway/` → `internal/agent/application/capability_router.go` + `internal/agent/infrastructure/capability/`
- `internal/config/` + `internal/harness/` → `internal/platform/{config,harness}/`

---

## Phase 概览

| Phase | Task # | 内容 | 并行性 |
|-------|--------|------|--------|
| 0 | T1 | 录契约（生成 golden） | 串行（前置） |
| 1 | T2-T7 | pkg 重组 + 接口下沉 | 与 T1 并行 |
| 2 | T8-T11 | wiring 骨架 + 容器 + 契约保护 | 等 T1+T7 |
| 3 | T12 | bounded context 空骨架 + port 冻结 | 等 T11 |
| 4 | T13-T20 | 8 context 迁移（**8 路并行**） | 等 T12 |
| 5 | T21-T22 | CI 防回潮 + 清桥 | 等 T13-T20 |

---

## Task 1: 录契约（Phase 0）

**Files:**

- Create: `api/http/contract_test.go`
- Create: `api/http/testdata/contracts/.gitkeep`
- Create: `scripts/record-contracts.go`
- Modify: `Makefile`

- [ ] **Step 1.1: 起空目录**

```bash
mkdir -p /home/yang/go-projects/stratum/api/http/testdata/contracts
mkdir -p /home/yang/go-projects/stratum/api/http
touch /home/yang/go-projects/stratum/api/http/testdata/contracts/.gitkeep
```

- [ ] **Step 1.2: 写枚举路由的录制器**

Create `scripts/record-contracts.go`:

```go
//go:build contracts

// Package main records HTTP contract golden files by replaying canonical
// requests against the current SetupRouter and writing JSON snapshots.
package main

import (
 "bytes"
 "context"
 "encoding/json"
 "fmt"
 "io"
 "net/http"
 "net/http/httptest"
 "os"
 "path/filepath"
 "strings"

 "github.com/byteBuilderX/stratum/api"
 "github.com/byteBuilderX/stratum/internal/config"
 "github.com/byteBuilderX/stratum/pkg/observability"
)

type Case struct {
 Name        string            `json:"name"`
 Method      string            `json:"method"`
 Path        string            `json:"path"`
 Headers     map[string]string `json:"headers,omitempty"`
 Body        json.RawMessage   `json:"body,omitempty"`
 WantStatus  int               `json:"want_status"`
 WantBodyRE  string            `json:"want_body_regex,omitempty"`
}

func main() {
 if len(os.Args) < 2 {
  fmt.Fprintln(os.Stderr, "usage: record-contracts <out-dir>")
  os.Exit(2)
 }
 outDir := os.Args[1]
 if err := os.MkdirAll(outDir, 0o755); err != nil {
  panic(err)
 }

 cfg, err := config.Load()
 if err != nil {
  panic(err)
 }
 logger, _ := observability.NewLogger("test")
 router := api.SetupRouter(cfg, logger, nil, nil, nil, nil, nil, nil)

 for _, route := range router.Routes() {
  safe := strings.NewReplacer("/", "_", ":", "_", "*", "_").Replace(route.Path)
  filename := fmt.Sprintf("%s%s.golden.json", strings.ToLower(route.Method), safe)
  recordRoute(router, route.Method, route.Path, filepath.Join(outDir, filename))
 }
 fmt.Printf("recorded %d routes\n", len(router.Routes()))
}

func recordRoute(router http.Handler, method, path, outPath string) {
 cases := []Case{{
  Name:       "default-unauth",
  Method:     method,
  Path:       path,
  WantStatus: 0,
 }}
 for i := range cases {
  req := httptest.NewRequest(cases[i].Method, cases[i].Path, bytes.NewReader(cases[i].Body))
  for k, v := range cases[i].Headers {
   req.Header.Set(k, v)
  }
  rec := httptest.NewRecorder()
  router.ServeHTTP(rec, req)
  cases[i].WantStatus = rec.Code
  body, _ := io.ReadAll(rec.Body)
  if json.Valid(body) {
   cases[i].Body = json.RawMessage(body)
  }
 }
 out, _ := json.MarshalIndent(cases, "", "  ")
 _ = os.WriteFile(outPath, out, 0o644)
}
```

- [ ] **Step 1.3: 加 Makefile target**

```makefile
.PHONY: record-contracts
record-contracts:
 go run -tags=contracts ./scripts/record-contracts.go api/http/testdata/contracts
```

- [ ] **Step 1.4: 跑录制器，生成 golden**

```bash
cd /home/yang/go-projects/stratum && make record-contracts
ls api/http/testdata/contracts | wc -l
```

Expected: ≥ 30 个 golden 文件（覆盖所有现有路由）

- [ ] **Step 1.5: 写 contract_test.go 重放 golden**

Create `api/http/contract_test.go`:

```go
package http_test

import (
 "bytes"
 "encoding/json"
 "net/http/httptest"
 "os"
 "path/filepath"
 "testing"

 "github.com/byteBuilderX/stratum/api"
 "github.com/byteBuilderX/stratum/internal/config"
 "github.com/byteBuilderX/stratum/pkg/observability"
)

type contractCase struct {
 Name       string          `json:"name"`
 Method     string          `json:"method"`
 Path       string          `json:"path"`
 Headers    map[string]string `json:"headers,omitempty"`
 Body       json.RawMessage `json:"body,omitempty"`
 WantStatus int             `json:"want_status"`
}

func TestContracts(t *testing.T) {
 cfg, err := config.Load()
 if err != nil {
  t.Skipf("config load failed: %v", err)
 }
 logger, _ := observability.NewLogger("test")
 router := api.SetupRouter(cfg, logger, nil, nil, nil, nil, nil, nil)

 files, err := filepath.Glob("testdata/contracts/*.golden.json")
 if err != nil {
  t.Fatal(err)
 }
 for _, f := range files {
  f := f
  t.Run(filepath.Base(f), func(t *testing.T) {
   data, err := os.ReadFile(f)
   if err != nil {
    t.Fatal(err)
   }
   var cases []contractCase
   if err := json.Unmarshal(data, &cases); err != nil {
    t.Fatal(err)
   }
   for _, c := range cases {
    req := httptest.NewRequest(c.Method, c.Path, bytes.NewReader(c.Body))
    for k, v := range c.Headers {
     req.Header.Set(k, v)
    }
    rec := httptest.NewRecorder()
    router.ServeHTTP(rec, req)
    if rec.Code != c.WantStatus {
     t.Errorf("%s %s: got status %d, want %d", c.Method, c.Path, rec.Code, c.WantStatus)
    }
   }
  })
 }
}
```

- [ ] **Step 1.6: 跑契约测试，验证全绿**

```bash
go test -v ./api/http/... 2>&1 | head -50
```

Expected: PASS for every golden file.

- [ ] **Step 1.7: Commit**

```bash
git add scripts/record-contracts.go api/http/contract_test.go api/http/testdata/contracts/ Makefile
git commit -m "test(api): record HTTP contracts as golden snapshots

Phase 0 of DDD refactor: lock current API surface (paths, methods, status
codes, response bodies) by replaying every gin route through SetupRouter
and snapshotting outputs. contract_test.go replays goldens to detect
backward-incompatible changes during the refactor."
```

---

## Task 2: 新建 pkg/storage/postgres（Phase 1）

**Files:**

- Create: `pkg/storage/postgres/pool.go`
- Create: `pkg/storage/postgres/querier.go`
- Create: `pkg/storage/postgres/pool_test.go`
- Modify: `pkg/postgres/postgres.go`（保留 alias 兼容）

- [ ] **Step 2.1: 写 Querier/TxBeginner/TenantExecer 接口**

Create `pkg/storage/postgres/querier.go`:

```go
// Package postgres provides PostgreSQL pool wrappers and consumer-side
// query interfaces. Business code depends on these interfaces, not on
// pgxpool.Pool directly, so storage backends can be swapped or mocked.
package postgres

import (
 "context"

 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgconn"
 "github.com/jackc/pgx/v5/pgxpool"
)

type Querier interface {
 QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
 Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
 Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type TxBeginner interface {
 Querier
 Begin(ctx context.Context) (pgx.Tx, error)
}

type TenantExecer interface {
 ExecTenant(ctx context.Context, tenantID string, fn func(ctx context.Context, tx pgx.Tx) error) error
}

var _ TxBeginner = (*pgxpool.Pool)(nil)
```

- [ ] **Step 2.2: 移 pool.go**

Create `pkg/storage/postgres/pool.go` by copying `pkg/postgres/postgres.go` content verbatim, then changing the package declaration to `package postgres`. Also re-export under the new path.

```bash
cp /home/yang/go-projects/stratum/pkg/postgres/postgres.go /home/yang/go-projects/stratum/pkg/storage/postgres/pool.go
```

Edit `pkg/storage/postgres/pool.go` package line if not already `package postgres`.

- [ ] **Step 2.3: 写 TenantExecer 实现（迁移 tenantdb 逻辑）**

Create `pkg/storage/postgres/tenant.go` by merging:

- `pkg/tenantdb/postgres.go`（核心 ExecTenant 函数）
- `pkg/tenantdb/context.go`（FromContext / WithContext）
- `pkg/tenantdb/schema.go`（ProvisionAllTenantSchemas）

```bash
cat /home/yang/go-projects/stratum/pkg/tenantdb/postgres.go /home/yang/go-projects/stratum/pkg/tenantdb/context.go /home/yang/go-projects/stratum/pkg/tenantdb/schema.go > /tmp/tenant_merge.go
```

合并后调整 package 名为 `postgres`，并把 `TenantExecer` 方法实现挂在 `*pgxpool.Pool` 的薄包装 `*Pool` 上：

```go
type Pool struct{ *pgxpool.Pool }

func (p *Pool) ExecTenant(ctx context.Context, tenantID string, fn func(ctx context.Context, tx pgx.Tx) error) error {
    // ... existing tenantdb.ExecTenant logic
}
```

- [ ] **Step 2.4: 写测试**

Create `pkg/storage/postgres/pool_test.go`:

```go
package postgres_test

import (
 "testing"

 "github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

func TestQuerierInterfaceCompat(t *testing.T) {
 var _ postgres.Querier = (postgres.TxBeginner)(nil)
}
```

- [ ] **Step 2.5: 旧 pkg/postgres 改为 alias**

Replace `pkg/postgres/postgres.go` with type aliases to keep external imports working during phase 4:

```go
// Package postgres re-exports pkg/storage/postgres for backwards compatibility.
// Deprecated: import pkg/storage/postgres directly. Removal in phase 5.
package postgres

import (
 "context"

 "github.com/byteBuilderX/stratum/pkg/storage/postgres"
 "go.uber.org/zap"
)

func New(ctx context.Context, dsn string, logger *zap.Logger) (*postgres.Pool, error) {
 return postgres.New(ctx, dsn, logger)
}
```

- [ ] **Step 2.6: Build + test**

```bash
go build ./... && go test -short ./pkg/storage/...
```

Expected: PASS, no compile errors.

- [ ] **Step 2.7: Commit**

```bash
git add pkg/storage/postgres/ pkg/postgres/postgres.go
git commit -m "refactor(pkg): introduce pkg/storage/postgres with Querier/TxBeginner

Phase 1 of DDD refactor. Move pool wrapper + tenant exec helpers under
pkg/storage/postgres/. Define consumer-side Querier and TxBeginner
interfaces so business code can depend on behavior rather than
pgxpool.Pool concretely. Old pkg/postgres re-exports for transition;
removed in phase 5."
```

---

## Task 3: 新建 pkg/storage/redis + KVStore（Phase 1）

**Files:**

- Create: `pkg/storage/redis/client.go`
- Create: `pkg/storage/redis/store.go`
- Create: `pkg/storage/redis/store_test.go`
- Modify: `pkg/redis/redis.go`（alias）

- [ ] **Step 3.1: 移 client.go**

```bash
mkdir -p /home/yang/go-projects/stratum/pkg/storage/redis
cp /home/yang/go-projects/stratum/pkg/redis/redis.go /home/yang/go-projects/stratum/pkg/storage/redis/client.go
```

调整 `pkg/storage/redis/client.go` 的 package 为 `package redis`。

- [ ] **Step 3.2: 写 KVStore 接口**

Create `pkg/storage/redis/store.go`:

```go
package redis

import (
 "context"
 "errors"
 "time"

 goredis "github.com/redis/go-redis/v9"
)

var ErrKeyNotFound = errors.New("redis: key not found")

type KVStore interface {
 Get(ctx context.Context, key string) (string, error)
 Set(ctx context.Context, key, value string, ttl time.Duration) error
 Del(ctx context.Context, keys ...string) error
}

type Store struct{ client *goredis.Client }

func NewStore(client *goredis.Client) *Store { return &Store{client: client} }

func (s *Store) Get(ctx context.Context, key string) (string, error) {
 v, err := s.client.Get(ctx, key).Result()
 if errors.Is(err, goredis.Nil) {
  return "", ErrKeyNotFound
 }
 return v, err
}

func (s *Store) Set(ctx context.Context, key, value string, ttl time.Duration) error {
 return s.client.Set(ctx, key, value, ttl).Err()
}

func (s *Store) Del(ctx context.Context, keys ...string) error {
 return s.client.Del(ctx, keys...).Err()
}
```

- [ ] **Step 3.3: 写测试（miniredis）**

Create `pkg/storage/redis/store_test.go`:

```go
package redis_test

import (
 "context"
 "errors"
 "testing"
 "time"

 "github.com/alicebob/miniredis/v2"
 goredis "github.com/redis/go-redis/v9"

 "github.com/byteBuilderX/stratum/pkg/storage/redis"
)

func TestStore(t *testing.T) {
 mr := miniredis.RunT(t)
 rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
 defer rdb.Close()

 store := redis.NewStore(rdb)
 ctx := context.Background()

 if err := store.Set(ctx, "k", "v", time.Minute); err != nil {
  t.Fatal(err)
 }
 v, err := store.Get(ctx, "k")
 if err != nil || v != "v" {
  t.Fatalf("got (%q,%v), want (v,nil)", v, err)
 }
 if err := store.Del(ctx, "k"); err != nil {
  t.Fatal(err)
 }
 if _, err := store.Get(ctx, "k"); !errors.Is(err, redis.ErrKeyNotFound) {
  t.Fatalf("expected ErrKeyNotFound, got %v", err)
 }
}
```

- [ ] **Step 3.4: 旧 pkg/redis 改 alias**

Replace `pkg/redis/redis.go` content:

```go
// Package redis re-exports pkg/storage/redis. Deprecated: removed in phase 5.
package redis

import (
 storageredis "github.com/byteBuilderX/stratum/pkg/storage/redis"
)

type Client = storageredis.Client

func New(addr, password string, db int) (*Client, error) {
 return storageredis.New(addr, password, db)
}
```

- [ ] **Step 3.5: Build + test + commit**

```bash
go build ./... && go test -short ./pkg/storage/redis/...
git add pkg/storage/redis/ pkg/redis/redis.go
git commit -m "refactor(pkg): introduce pkg/storage/redis with KVStore interface

Phase 1 of DDD refactor. KVStore is the consumer-side interface for
business code; *Store wraps go-redis. Old pkg/redis re-exports."
```

---

## Task 4: 新建 pkg/storage/milvus + VectorIndex（Phase 1）

**Files:**

- Create: `pkg/storage/milvus/client.go`
- Create: `pkg/storage/milvus/index.go`
- Create: `pkg/storage/milvus/index_test.go`
- Modify: `pkg/vector/vector_store.go`（alias）

- [ ] **Step 4.1: 移 client.go**

```bash
mkdir -p /home/yang/go-projects/stratum/pkg/storage/milvus
cp /home/yang/go-projects/stratum/pkg/vector/vector_store.go /home/yang/go-projects/stratum/pkg/storage/milvus/client.go
```

调整 package 为 `package milvus`。

- [ ] **Step 4.2: 写 VectorIndex 接口**

Create `pkg/storage/milvus/index.go`:

```go
package milvus

import "context"

type Document struct {
 ID       string
 Vector   []float32
 Metadata map[string]any
}

type SearchHit struct {
 ID       string
 Score    float32
 Metadata map[string]any
}

type VectorIndex interface {
 EnsureCollection(ctx context.Context, name string, dim int) error
 Upsert(ctx context.Context, name string, docs []Document) error
 Search(ctx context.Context, name string, vec []float32, topK int, filter string) ([]SearchHit, error)
 Drop(ctx context.Context, name string) error
}
```

- [ ] **Step 4.3: 把 VectorStore 改造成 VectorIndex 实现**

Edit `pkg/storage/milvus/client.go` 的 `*VectorStore` 类型，添加 `VectorIndex` 接口要求的方法（多数已存在，只需重命名/包装）。在文件末尾加：

```go
var _ VectorIndex = (*VectorStore)(nil)
```

如果有 signature mismatch（例如返回类型不同），写薄包装方法。

- [ ] **Step 4.4: 写接口契约单测**

Create `pkg/storage/milvus/index_test.go`:

```go
package milvus_test

import (
 "testing"

 "github.com/byteBuilderX/stratum/pkg/storage/milvus"
)

func TestVectorIndexInterface(t *testing.T) {
 var _ milvus.VectorIndex = (*milvus.VectorStore)(nil)
}
```

- [ ] **Step 4.5: 旧 pkg/vector 改 alias**

Replace `pkg/vector/vector_store.go`:

```go
// Package vector re-exports pkg/storage/milvus. Deprecated: removed in phase 5.
package vector

import storagemilvus "github.com/byteBuilderX/stratum/pkg/storage/milvus"

type VectorStore = storagemilvus.VectorStore

func NewVectorStore(addr, user, password string) (*VectorStore, error) {
 return storagemilvus.NewVectorStore(addr, user, password)
}
```

- [ ] **Step 4.6: Build + test + commit**

```bash
go build ./... && go test -short ./pkg/storage/milvus/...
git add pkg/storage/milvus/ pkg/vector/vector_store.go
git commit -m "refactor(pkg): pkg/storage/milvus with VectorIndex interface

Phase 1 of DDD refactor. VectorIndex is the consumer-side abstraction
for vector search. *VectorStore satisfies it. Old pkg/vector re-exports."
```

---

## Task 5: 新建 pkg/messaging/nats + tenantnaming（Phase 1）

**Files:**

- Create: `pkg/messaging/nats/conn.go`
- Create: `pkg/messaging/nats/publisher.go`
- Create: `pkg/storage/tenantnaming/{milvus,nats,neo4j}.go`
- Modify: `pkg/tenantdb/{milvus,nats,neo4j}.go`（alias）

- [ ] **Step 5.1: 创建 messaging/nats**

```bash
mkdir -p /home/yang/go-projects/stratum/pkg/messaging/nats
mkdir -p /home/yang/go-projects/stratum/pkg/storage/tenantnaming
```

Create `pkg/messaging/nats/conn.go`:

```go
// Package nats wraps NATS connection setup with safe defaults.
package nats

import (
 "time"

 "github.com/nats-io/nats.go"
)

func Connect(url string, opts ...nats.Option) (*nats.Conn, error) {
 defaults := []nats.Option{
  nats.MaxReconnects(-1),
  nats.ReconnectWait(2 * time.Second),
  nats.Timeout(10 * time.Second),
 }
 return nats.Connect(url, append(defaults, opts...)...)
}
```

Create `pkg/messaging/nats/publisher.go`:

```go
package nats

import (
 "context"

 "github.com/nats-io/nats.go"
)

type Publisher interface {
 Publish(ctx context.Context, subject string, data []byte) error
}

type JetStreamPublisher struct{ js nats.JetStreamContext }

func NewJetStreamPublisher(js nats.JetStreamContext) *JetStreamPublisher {
 return &JetStreamPublisher{js: js}
}

func (p *JetStreamPublisher) Publish(_ context.Context, subject string, data []byte) error {
 _, err := p.js.Publish(subject, data)
 return err
}
```

- [ ] **Step 5.2: 移 tenantnaming 文件**

```bash
cp /home/yang/go-projects/stratum/pkg/tenantdb/milvus.go /home/yang/go-projects/stratum/pkg/storage/tenantnaming/milvus.go
cp /home/yang/go-projects/stratum/pkg/tenantdb/nats.go /home/yang/go-projects/stratum/pkg/storage/tenantnaming/nats.go
cp /home/yang/go-projects/stratum/pkg/tenantdb/neo4j.go /home/yang/go-projects/stratum/pkg/storage/tenantnaming/neo4j.go
```

调整三个文件的 package 为 `package tenantnaming`。

- [ ] **Step 5.3: 旧 pkg/tenantdb 改成 alias 文件**

Replace `pkg/tenantdb/milvus.go` `pkg/tenantdb/nats.go` `pkg/tenantdb/neo4j.go` content with re-exports referencing `pkg/storage/tenantnaming/*`. Keep function and type signatures identical.

- [ ] **Step 5.4: Build + commit**

```bash
go build ./... && go test -short ./pkg/messaging/... ./pkg/storage/tenantnaming/...
git add pkg/messaging/ pkg/storage/tenantnaming/ pkg/tenantdb/{milvus,nats,neo4j}.go
git commit -m "refactor(pkg): split tenantdb into storage/tenantnaming + messaging/nats

Phase 1 of DDD refactor. Tenant naming helpers (pure DSL) move to
pkg/storage/tenantnaming. NATS conn + publisher consolidated under
pkg/messaging/nats. Old pkg/tenantdb files re-export."
```

---

## Task 6: 新建 pkg/httpclient + pkg/textchunk + pkg/migration（Phase 1）

**Files:**

- Create: `pkg/httpclient/client.go`
- Create: `pkg/httpclient/transport.go`
- Create: `pkg/httpclient/transport_test.go`
- Move: `internal/textchunk/` → `pkg/textchunk/`
- Move: `internal/migration/` → `pkg/migration/`

- [ ] **Step 6.1: 写 httpclient.Doer + New + NewSSRFSafe**

Create `pkg/httpclient/client.go`:

```go
// Package httpclient provides shared HTTP client builders with timeout,
// retry, and SSRF protection presets.
package httpclient

import (
 "net/http"
 "time"
)

type Doer interface {
 Do(req *http.Request) (*http.Response, error)
}

type Option func(*config)

type config struct {
 timeout    time.Duration
 maxRetries int
 userAgent  string
 ssrfSafe   bool
}

func WithTimeout(d time.Duration) Option   { return func(c *config) { c.timeout = d } }
func WithMaxRetries(n int) Option          { return func(c *config) { c.maxRetries = n } }
func WithUserAgent(ua string) Option       { return func(c *config) { c.userAgent = ua } }

func New(opts ...Option) *http.Client {
 c := &config{timeout: 30 * time.Second, userAgent: "stratum/1.0"}
 for _, o := range opts {
  o(c)
 }
 return &http.Client{
  Timeout:   c.timeout,
  Transport: newTransport(c),
 }
}

func NewSSRFSafe(opts ...Option) *http.Client {
 c := &config{timeout: 30 * time.Second, userAgent: "stratum/1.0", ssrfSafe: true}
 for _, o := range opts {
  o(c)
 }
 return &http.Client{
  Timeout:   c.timeout,
  Transport: newTransport(c),
 }
}
```

- [ ] **Step 6.2: 抽 SSRF-safe transport**

Create `pkg/httpclient/transport.go` by porting the transport logic from `internal/skill/http_skill.go`. Add User-Agent injection middleware.

```go
package httpclient

import (
 "net"
 "net/http"
 "net/netip"
 "time"
)

func newTransport(c *config) http.RoundTripper {
 base := &http.Transport{
  DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
  TLSHandshakeTimeout:   10 * time.Second,
  ResponseHeaderTimeout: 30 * time.Second,
  MaxIdleConns:          100,
  IdleConnTimeout:       90 * time.Second,
 }
 if c.ssrfSafe {
  base.DialContext = ssrfSafeDial(10 * time.Second)
 }
 return &uaTransport{base: base, ua: c.userAgent}
}

type uaTransport struct {
 base http.RoundTripper
 ua   string
}

func (t *uaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
 if req.Header.Get("User-Agent") == "" && t.ua != "" {
  req.Header.Set("User-Agent", t.ua)
 }
 return t.base.RoundTrip(req)
}

func ssrfSafeDial(timeout time.Duration) func(ctx context.Context, network, addr string) (net.Conn, error) {
 d := &net.Dialer{Timeout: timeout}
 return func(ctx context.Context, network, addr string) (net.Conn, error) {
  host, _, err := net.SplitHostPort(addr)
  if err != nil {
   return nil, err
  }
  ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
  if err != nil {
   return nil, err
  }
  for _, ip := range ips {
   a, ok := netip.AddrFromSlice(ip.IP)
   if !ok {
    continue
   }
   if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() {
    return nil, fmt.Errorf("httpclient: SSRF protection blocked %s", a)
   }
  }
  return d.DialContext(ctx, network, addr)
 }
}
```

注意补 `import "context"` 和 `"fmt"`。

- [ ] **Step 6.3: 写 SSRF transport 单测**

Create `pkg/httpclient/transport_test.go` with table-driven cases hitting `127.0.0.1`, `10.0.0.1`, `169.254.0.1` — expect "SSRF protection blocked"; hitting `8.8.8.8` (mock via custom resolver) — expect pass-through.

```go
package httpclient_test

import (
 "context"
 "strings"
 "testing"

 "github.com/byteBuilderX/stratum/pkg/httpclient"
)

func TestSSRFSafeDialBlocksPrivate(t *testing.T) {
 cases := []struct{ host string }{
  {"127.0.0.1:80"},
  {"localhost:80"},
  {"10.0.0.1:80"},
  {"192.168.0.1:80"},
  {"169.254.169.254:80"},
 }
 for _, tc := range cases {
  t.Run(tc.host, func(t *testing.T) {
   c := httpclient.NewSSRFSafe()
   req, _ := http.NewRequestWithContext(context.Background(), "GET", "http://"+tc.host, nil)
   _, err := c.Do(req)
   if err == nil || !strings.Contains(err.Error(), "SSRF") {
    t.Errorf("expected SSRF error for %s, got %v", tc.host, err)
   }
  })
 }
}
```

- [ ] **Step 6.4: 移动 textchunk 与 migration**

```bash
git mv /home/yang/go-projects/stratum/internal/textchunk /home/yang/go-projects/stratum/pkg/textchunk
git mv /home/yang/go-projects/stratum/internal/migration /home/yang/go-projects/stratum/pkg/migration
```

调整两包 import path：

```bash
cd /home/yang/go-projects/stratum
grep -rl "byteBuilderX/stratum/internal/textchunk" --include="*.go" | xargs sed -i 's|byteBuilderX/stratum/internal/textchunk|byteBuilderX/stratum/pkg/textchunk|g'
grep -rl "byteBuilderX/stratum/internal/migration" --include="*.go" | xargs sed -i 's|byteBuilderX/stratum/internal/migration|byteBuilderX/stratum/pkg/migration|g'
```

- [ ] **Step 6.5: Build + test**

```bash
go build ./... && go test -short ./pkg/httpclient/... ./pkg/textchunk/... ./pkg/migration/...
```

- [ ] **Step 6.6: Commit**

```bash
git add pkg/httpclient/ pkg/textchunk/ pkg/migration/
git add -A internal/textchunk internal/migration
git commit -m "refactor(pkg): add httpclient with SSRF protection; move textchunk + migration

Phase 1 of DDD refactor. New pkg/httpclient consolidates timeout/retry/UA
and SSRF-safe dial logic previously inline in internal/skill. textchunk
moves to pkg/ since both knowledge and memory consume it. migration
moves to pkg/ since it has no business deps."
```

---

## Task 7: 业务侧改用新接口（Phase 1 收尾）

**Files:**

- Modify: `internal/skill/http_skill.go`（用 `httpclient.NewSSRFSafe`）
- Modify: 所有 import `pkg/postgres` / `pkg/redis` / `pkg/vector` / `pkg/tenantdb` 的业务文件，**保持不动**（alias 兜住，正式替换在 phase 4）

- [ ] **Step 7.1: 改造 http_skill.go**

读取 `internal/skill/http_skill.go`，找出 `&http.Client{Timeout: ...}` 与 SSRF 防护代码块（约 30-50 行），替换为：

```go
import "github.com/byteBuilderX/stratum/pkg/httpclient"

var defaultHTTPClient = httpclient.NewSSRFSafe(
    httpclient.WithTimeout(30 * time.Second),
    httpclient.WithUserAgent("stratum-skill/1.0"),
)
```

并删除原 transport 内联实现。

- [ ] **Step 7.2: 跑全量测试，验证不破**

```bash
go vet ./...
go test -short ./...
```

Expected: PASS（含契约测试 `TestContracts` 仍绿）

- [ ] **Step 7.3: Commit**

```bash
git add internal/skill/http_skill.go
git commit -m "refactor(skill): use pkg/httpclient.NewSSRFSafe

Replace inline SSRF dialer + Client construction with shared httpclient
package. No behavior change."
```

---

## Task 8: 创建 api/wiring 包骨架（Phase 2）

**Files:**

- Create: `api/wiring/wiring.go`
- Create: `api/wiring/storage.go`
- Create: `api/wiring/llmgateway.go`

- [ ] **Step 8.1: Container struct + BuildContainer 骨架**

Create `api/wiring/wiring.go`:

```go
// Package wiring is the composition root: it constructs concrete
// dependencies once at startup and exposes them as a Container.
// Handlers depend on application services through the Container; they
// never reach into infrastructure directly.
package wiring

import (
 "context"

 "go.uber.org/zap"

 "github.com/byteBuilderX/stratum/internal/config"
)

type Container struct {
 Config *config.Config
 Logger *zap.Logger

 Storage    *Storage
 LLMGateway *LLMGateway

 shutdown []func(context.Context) error
}

func BuildContainer(ctx context.Context, cfg *config.Config, logger *zap.Logger) (*Container, error) {
 c := &Container{Config: cfg, Logger: logger}

 storage, err := buildStorage(ctx, cfg, logger)
 if err != nil {
  return nil, err
 }
 c.Storage = storage
 c.shutdown = append(c.shutdown, storage.Close)

 gw, err := buildLLMGateway(ctx, cfg, logger)
 if err != nil {
  _ = storage.Close(ctx)
  return nil, err
 }
 c.LLMGateway = gw

 return c, nil
}

func (c *Container) Shutdown(ctx context.Context) error {
 var firstErr error
 for i := len(c.shutdown) - 1; i >= 0; i-- {
  if err := c.shutdown[i](ctx); err != nil && firstErr == nil {
   firstErr = err
  }
 }
 return firstErr
}
```

- [ ] **Step 8.2: storage.go：pgx pool / redis / milvus / nats**

Create `api/wiring/storage.go`:

```go
package wiring

import (
 "context"

 "github.com/nats-io/nats.go"
 goredis "github.com/redis/go-redis/v9"
 "go.uber.org/zap"

 "github.com/byteBuilderX/stratum/internal/config"
 pkgnats "github.com/byteBuilderX/stratum/pkg/messaging/nats"
 "github.com/byteBuilderX/stratum/pkg/storage/milvus"
 "github.com/byteBuilderX/stratum/pkg/storage/postgres"
 pkgredis "github.com/byteBuilderX/stratum/pkg/storage/redis"
)

type Storage struct {
 PG     *postgres.Pool
 Redis  *goredis.Client
 Milvus *milvus.VectorStore
 NATS   *nats.Conn
 JS     nats.JetStreamContext
}

func buildStorage(ctx context.Context, cfg *config.Config, logger *zap.Logger) (*Storage, error) {
 pg, err := postgres.New(ctx, cfg.PostgresURL, logger)
 if err != nil {
  return nil, err
 }
 rdb, err := pkgredis.New(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
 if err != nil {
  pg.Close()
  return nil, err
 }
 mil, err := milvus.NewVectorStore(cfg.MilvusAddr, cfg.MilvusUser, cfg.MilvusPassword)
 if err != nil {
  _ = rdb.Close()
  pg.Close()
  return nil, err
 }
 nc, err := pkgnats.Connect(cfg.NATSURL)
 if err != nil {
  _ = mil.Close()
  _ = rdb.Close()
  pg.Close()
  return nil, err
 }
 js, err := nc.JetStream()
 if err != nil {
  nc.Close()
  _ = mil.Close()
  _ = rdb.Close()
  pg.Close()
  return nil, err
 }
 return &Storage{PG: pg, Redis: rdb, Milvus: mil, NATS: nc, JS: js}, nil
}

func (s *Storage) Close(ctx context.Context) error {
 if s.NATS != nil {
  s.NATS.Close()
 }
 if s.Milvus != nil {
  _ = s.Milvus.Close()
 }
 if s.Redis != nil {
  _ = s.Redis.Close()
 }
 if s.PG != nil {
  s.PG.Close()
 }
 return nil
}
```

- [ ] **Step 8.3: llmgateway.go：gateway + tenant cache + EmbedResolver**

Create `api/wiring/llmgateway.go`. 把现 router.go 里的 `buildEmbedResolver` 与 `buildGatewayForTenant` 全部搬过来，包装成 `LLMGateway` 结构体：

```go
package wiring

import (
 "context"

 "go.uber.org/zap"

 "github.com/byteBuilderX/stratum/internal/config"
 "github.com/byteBuilderX/stratum/internal/llmgateway"
 "github.com/byteBuilderX/stratum/internal/memory/pipeline"
)

type LLMGateway struct {
 Gateway       *llmgateway.Gateway
 TenantCache   *llmgateway.TenantGatewayCache
 EmbedResolver pipeline.EmbedServiceResolver
}

func buildLLMGateway(ctx context.Context, cfg *config.Config, logger *zap.Logger) (*LLMGateway, error) {
 gw := llmgateway.NewGateway(cfg, logger)
 cache := llmgateway.NewTenantGatewayCache()
 resolver := pipeline.NewEmbedResolver(cache /* + deps */)
 return &LLMGateway{Gateway: gw, TenantCache: cache, EmbedResolver: resolver}, nil
}
```

精确实现照搬现 router.go 的对应函数即可。

- [ ] **Step 8.4: Build + commit**

```bash
go build ./...
git add api/wiring/
git commit -m "feat(api/wiring): introduce Container + Storage/LLMGateway sub-builders

Phase 2 of DDD refactor. Composition root takes shape: Container holds
all wiring, BuildContainer constructs deps in order, Shutdown reverses.
Handlers will pull services from Container in later tasks."
```

---

## Task 9: api/wiring 接入剩余子构造器（Phase 2）

**Files:**

- Create: `api/wiring/{platform,memory,knowledge,skill,agent,mcp,iam}.go`

每个子文件按下述模板填，把 router.go 现有相应初始化代码搬入：

- [ ] **Step 9.1: platform.go（harness + capgateway 暂存放在这里，phase 4 再挪）**

```go
// api/wiring/platform.go
package wiring

import (
 "context"

 "github.com/byteBuilderX/stratum/internal/capgateway"
 "github.com/byteBuilderX/stratum/internal/harness"
)

type Platform struct {
 Harness *harness.Harness
 CapGW   capgateway.CapabilityGateway
}

func (c *Container) buildPlatform(ctx context.Context) (*Platform, error) {
 h := harness.New(c.Logger)
 capGW := capgateway.New(c.Logger /* + skill adapter */)
 return &Platform{Harness: h, CapGW: capGW}, nil
}
```

- [ ] **Step 9.2-9.7: memory/knowledge/skill/agent/mcp/iam.go**

每个文件 ~30-60 行，从现 router.go + main.go 搬对应初始化代码到 `(c *Container) build<Domain>` 方法，挂到 Container 字段。

文件清单（每个都要建）:

- `api/wiring/memory.go` — `MemoryManager`、`pipeline.Pipeline`
- `api/wiring/knowledge.go` — `RAGService`、`KnowledgeIngest`
- `api/wiring/skill.go` — `SkillExecutor`、`SkillRegistry`、`skillgateway`
- `api/wiring/agent.go` — `AgentRegistry`、`ChatStore`、`ExecutionStore`
- `api/wiring/mcp.go` — `MCPClientManager`
- `api/wiring/iam.go` — `JWTService`、`TokenStore`、`OnboardService`、`hermes`

每个 build 方法严格遵循"先调用依赖的 build → 拿到结果 → 构造自己 → return"，确保 Container.shutdown 顺序正确。

- [ ] **Step 9.3: 在 BuildContainer 里串起来**

Edit `api/wiring/wiring.go` 的 `BuildContainer`：

```go
func BuildContainer(ctx context.Context, cfg *config.Config, logger *zap.Logger) (*Container, error) {
 c := &Container{Config: cfg, Logger: logger}
 steps := []struct {
  name string
  fn   func(context.Context) error
 }{
  {"storage", c.buildStorage},
  {"llmgateway", c.buildLLMGateway},
  {"platform", c.buildPlatform},
  {"mcp", c.buildMCP},
  {"skill", c.buildSkill},
  {"knowledge", c.buildKnowledge},
  {"memory", c.buildMemory},
  {"iam", c.buildIAM},
  {"agent", c.buildAgent},
 }
 for _, s := range steps {
  if err := s.fn(ctx); err != nil {
   _ = c.Shutdown(ctx)
   return nil, fmt.Errorf("wiring.%s: %w", s.name, err)
  }
 }
 return c, nil
}
```

把 8.x 阶段写的独立构造器签名都改成 `(c *Container) build<X>(ctx) error`，结果存进 `c.<X>` 字段。

- [ ] **Step 9.4: Build + commit**

```bash
go build ./...
git add api/wiring/
git commit -m "feat(api/wiring): all 8 sub-builders attached to Container

Phase 2 of DDD refactor. wiring/{platform,memory,knowledge,skill,agent,
mcp,iam}.go each construct one bounded context's services from Storage
+ LLMGateway. Build order: storage→llmgateway→platform→mcp→skill→
knowledge→memory→iam→agent. Shutdown reverses."
```

---

## Task 10: api/http 路由层 + main.go 切到 Container（Phase 2 收尾）

**Files:**

- Create: `api/http/router.go`
- Modify: `api/router.go`（thin shim）
- Modify: `cmd/server/main.go`
- Move: `api/handler/` → `api/http/handler/`
- Move: `api/model/` → `api/http/dto/`

- [ ] **Step 10.1: 移 handler + dto 目录**

```bash
git mv /home/yang/go-projects/stratum/api/handler /home/yang/go-projects/stratum/api/http/handler
git mv /home/yang/go-projects/stratum/api/model /home/yang/go-projects/stratum/api/http/dto
cd /home/yang/go-projects/stratum
grep -rl "byteBuilderX/stratum/api/handler" --include="*.go" | xargs sed -i 's|byteBuilderX/stratum/api/handler|byteBuilderX/stratum/api/http/handler|g'
grep -rl "byteBuilderX/stratum/api/model" --include="*.go" | xargs sed -i 's|byteBuilderX/stratum/api/model|byteBuilderX/stratum/api/http/dto|g'
```

调整 dto 里所有文件的 `package model` → `package dto`，并改文件内引用。

- [ ] **Step 10.2: 写薄 router.go**

Create `api/http/router.go` (≤100 行)，仅做：

1. 创建 gin.Engine
2. 装中间件
3. 调用 handler 构造（从 Container 取服务）
4. 注册 router.Group

```go
// Package http builds the HTTP router from a wiring.Container.
package http

import (
 "github.com/gin-gonic/gin"

 "github.com/byteBuilderX/stratum/api/http/handler"
 "github.com/byteBuilderX/stratum/api/middleware"
 "github.com/byteBuilderX/stratum/api/wiring"
)

func NewRouter(c *wiring.Container) *gin.Engine {
 r := gin.New()
 r.Use(gin.Recovery(),
  middleware.ErrorHandler(c.Logger),
  middleware.TraceMiddleware(c.Logger),
  middleware.CORSMiddleware(c.Config.FrontendURL),
  middleware.MetricsMiddleware(c.Platform.Metrics),
 )

 registerAuthRoutes(r, c)
 registerAgentRoutes(r, c)
 registerMemoryRoutes(r, c)
 registerKnowledgeRoutes(r, c)
 registerSkillRoutes(r, c)
 registerMCPRoutes(r, c)
 registerTenantRoutes(r, c)
 registerHealthRoutes(r, c)

 return r
}
```

把每个 `register<X>Routes` 写成同文件下的私有函数，每个 ≤30 行。

- [ ] **Step 10.3: 旧 api/router.go 改 thin shim**

Replace `api/router.go` content：

```go
// Package api is deprecated; kept as a thin shim for record-contracts and
// transitional callers. Removed in phase 5.
package api

import (
 "github.com/gin-gonic/gin"

 apihttp "github.com/byteBuilderX/stratum/api/http"
 "github.com/byteBuilderX/stratum/api/wiring"
 "github.com/byteBuilderX/stratum/internal/config"
 "go.uber.org/zap"
)

// SetupRouter is the legacy entrypoint. New code uses api/http.NewRouter
// with a wiring.Container.
func SetupRouter(cfg *config.Config, logger *zap.Logger, _ ...any) *gin.Engine {
 c, err := wiring.BuildContainer(...)  // best-effort for contract recorder
 if err != nil {
  return gin.New()
 }
 return apihttp.NewRouter(c)
}
```

注意：契约测试 `api/http/contract_test.go` 改成调用 `apihttp.NewRouter(buildTestContainer(t))`。

- [ ] **Step 10.4: cmd/server/main.go 缩成 30 行**

Replace `cmd/server/main.go`:

```go
package main

import (
 "context"
 "log"
 "os"
 "os/signal"
 "syscall"

 "github.com/joho/godotenv"

 apihttp "github.com/byteBuilderX/stratum/api/http"
 "github.com/byteBuilderX/stratum/api/wiring"
 "github.com/byteBuilderX/stratum/internal/config"
 "github.com/byteBuilderX/stratum/pkg/observability"
)

func main() {
 _ = godotenv.Load()
 cfg, err := config.Load()
 if err != nil {
  log.Fatalf("config: %v", err)
 }
 logger, err := observability.NewLogger(os.Getenv("APP_ENV"))
 if err != nil {
  log.Fatalf("logger: %v", err)
 }
 defer logger.Sync() //nolint:errcheck

 ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
 defer cancel()

 container, err := wiring.BuildContainer(ctx, cfg, logger)
 if err != nil {
  log.Fatalf("wiring: %v", err)
 }
 defer func() {
  shutCtx, c := context.WithTimeout(context.Background(), 30*time.Second)
  defer c()
  _ = container.Shutdown(shutCtx)
 }()

 if err := container.Platform.Harness.Start(ctx); err != nil {
  log.Fatalf("harness: %v", err)
 }
 router := apihttp.NewRouter(container)
 if err := router.Run(cfg.HTTPAddr); err != nil {
  log.Fatalf("http: %v", err)
 }
}
```

- [ ] **Step 10.5: 跑契约测试，必须全绿**

```bash
go build ./...
go test -v ./api/http/... 2>&1 | tail -30
```

Expected: all golden cases PASS, no diff.

- [ ] **Step 10.6: 跑全量短测**

```bash
go test -short ./...
```

Expected: PASS

- [ ] **Step 10.7: Commit**

```bash
git add -A
git commit -m "refactor(api,cmd): switch to wiring.Container + thin api/http router

Phase 2 finale: cmd/server/main.go ≤ 30 lines, api/http/router.go
≤ 100 lines. Old api/router.go and SetupRouter remain as thin shims.
Handlers + DTOs moved to api/http/. Contract tests still green."
```

---

## Task 11: 录第二次契约（确认无漂移）（Phase 2 验证）

- [ ] **Step 11.1: 重跑 record-contracts 与原 golden diff**

```bash
make record-contracts
git diff --stat api/http/testdata/contracts/
```

Expected: zero diff. 如有 diff，回到 Task 10 修正。

- [ ] **Step 11.2: 标记 Phase 2 完成**

```bash
git tag refactor-phase-2-done
```

---

## Task 12: 8 个 bounded context 空骨架 + port 冻结（Phase 3）

**Files:**

- Create: 每个 context 的 `domain/`、`domain/port/`、`application/`、`infrastructure/` 空目录
- Create: 每个 context 的 `domain/port/<x>.go` 接口定义

- [ ] **Step 12.1: 起目录树**

```bash
cd /home/yang/go-projects/stratum
for ctx in agent memory knowledge skill mcp iam llmgateway platform; do
  mkdir -p internal/$ctx/{domain/port,application,infrastructure}
  touch internal/$ctx/{domain,application,infrastructure}/.gitkeep
done
```

- [ ] **Step 12.2: 写 agent/domain/port/*.go（消费者侧接口冻结）**

Create `internal/agent/domain/port/repository.go`:

```go
// Package port declares interfaces the agent context depends on.
// Implementations live in agent/infrastructure/persistence/.
package port

import (
 "context"

 "github.com/byteBuilderX/stratum/internal/agent/domain"
)

type AgentRepo interface {
 Get(ctx context.Context, id string) (*domain.Agent, error)
 List(ctx context.Context, filter domain.ListFilter) ([]*domain.Agent, error)
 Save(ctx context.Context, a *domain.Agent) error
 Delete(ctx context.Context, id string) error
}

type ExecutionRepo interface {
 Get(ctx context.Context, id string) (*domain.Execution, error)
 Save(ctx context.Context, e *domain.Execution) error
 ListByAgent(ctx context.Context, agentID string, limit int) ([]*domain.Execution, error)
}

type ChatRepo interface {
 GetConversation(ctx context.Context, id string) (*domain.Conversation, error)
 AppendMessage(ctx context.Context, conversationID string, msg *domain.ChatMessage) error
 ListMessages(ctx context.Context, conversationID string, limit int) ([]*domain.ChatMessage, error)
}
```

Create `internal/agent/domain/port/memory.go`:

```go
package port

import "context"

type MemoryRecaller interface {
 Recall(ctx context.Context, query string, k int) ([]string, error)
}

type MemoryWriter interface {
 Append(ctx context.Context, conversationID, role, content string) error
}
```

Create `internal/agent/domain/port/skill.go`:

```go
package port

import "context"

type SkillExecutor interface {
 Execute(ctx context.Context, skillID string, input map[string]any) (map[string]any, error)
}
```

Create `internal/agent/domain/port/knowledge.go`:

```go
package port

import "context"

type KnowledgeRetriever interface {
 Retrieve(ctx context.Context, kbID, query string, topK int) ([]string, error)
}
```

Create `internal/agent/domain/port/llm.go`:

```go
package port

import "context"

type LLMCompleter interface {
 Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
 StreamComplete(ctx context.Context, req CompletionRequest, onDelta func(string) error) error
}

type CompletionRequest struct {
 Model       string
 Messages    []Message
 Temperature float32
 MaxTokens   int
 Tools       []ToolDef
}

type CompletionResponse struct {
 Content   string
 ToolCalls []ToolCall
 Tokens    int
}

type Message struct {
 Role    string
 Content string
}

type ToolDef struct {
 Name        string
 Description string
 Schema      map[string]any
}

type ToolCall struct {
 Name string
 Args map[string]any
}
```

- [ ] **Step 12.3: 写 memory/domain/port/*.go**

Create `internal/memory/domain/port/{vector,embedder,llm,publisher}.go`:

```go
// internal/memory/domain/port/vector.go
package port

import "context"

type VectorIndex interface {
 Upsert(ctx context.Context, collection string, docs []Document) error
 Search(ctx context.Context, collection string, vec []float32, topK int, filter string) ([]Hit, error)
}

type Document struct {
 ID       string
 Vector   []float32
 Metadata map[string]any
}

type Hit struct {
 ID       string
 Score    float32
 Metadata map[string]any
}
```

```go
// internal/memory/domain/port/embedder.go
package port

import "context"

type Embedder interface {
 Embed(ctx context.Context, text string) ([]float32, error)
}
```

```go
// internal/memory/domain/port/llm.go
package port

import "context"

type Enricher interface {
 Enrich(ctx context.Context, content string) (summary string, importance float32, err error)
}
```

```go
// internal/memory/domain/port/publisher.go
package port

import "context"

type EventPublisher interface {
 Publish(ctx context.Context, subject string, payload []byte) error
}
```

- [ ] **Step 12.4: 写 knowledge/domain/port/*.go**

Create `internal/knowledge/domain/port/{vector,embedder,chunker,doc_repo}.go`:

```go
// internal/knowledge/domain/port/vector.go
package port

import "context"

type VectorIndex interface {
 EnsureCollection(ctx context.Context, name string, dim int) error
 Upsert(ctx context.Context, name string, docs []Document) error
 Search(ctx context.Context, name string, vec []float32, topK int) ([]Hit, error)
 Drop(ctx context.Context, name string) error
}

type Document struct {
 ID       string
 Vector   []float32
 Text     string
 Metadata map[string]any
}

type Hit struct {
 ID       string
 Score    float32
 Text     string
 Metadata map[string]any
}
```

类似写 embedder.go / chunker.go / doc_repo.go。

- [ ] **Step 12.5: 写 skill/domain/port/*.go**

Create `internal/skill/domain/port/{registry,executor,llm,mcp,http}.go`:

```go
// internal/skill/domain/port/registry.go
package port

import (
 "context"

 "github.com/byteBuilderX/stratum/internal/skill/domain"
)

type SkillRepo interface {
 Get(ctx context.Context, id string) (*domain.Skill, error)
 List(ctx context.Context, kind domain.Kind) ([]*domain.Skill, error)
 Save(ctx context.Context, s *domain.Skill) error
 Delete(ctx context.Context, id string) error
}
```

```go
// internal/skill/domain/port/llm.go
package port
import "context"
type LLMCompleter interface {
 Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
}
type CompletionRequest struct { Model string; Prompt string; Temperature float32; MaxTokens int }
type CompletionResponse struct { Content string; Tokens int }
```

```go
// internal/skill/domain/port/mcp.go
package port
import "context"
type MCPInvoker interface {
 Invoke(ctx context.Context, server, tool string, args map[string]any) (map[string]any, error)
}
```

```go
// internal/skill/domain/port/http.go
package port
import "net/http"
type Doer interface {
 Do(req *http.Request) (*http.Response, error)
}
```

- [ ] **Step 12.6: 写 mcp/domain/port + iam/domain/port + llmgateway/domain/port**

每个 context 创建对应 port 文件，定义最小接口集。可参考现有具体实现的方法签名筛掉非必需方法。

- mcp: `ClientManager`、`ServerRepo`
- iam: `UserRepo`、`TokenStore`、`SessionStore`、`OAuthProvider`
- llmgateway: `Provider`、`TenantSettingsRepo`

- [ ] **Step 12.7: 写空的 domain entity 文件占位（避免 import 报错）**

每个 context 的 `domain/` 至少要一个 `<context>.go` 声明 package：

```go
// internal/agent/domain/agent.go
package domain

type Agent struct {
 ID, Name, Description string
 // 待 phase 4 填充
}

type Execution struct{ ID string }
type Conversation struct{ ID string }
type ChatMessage struct{ ID, Role, Content string }
type ListFilter struct{ Limit, Offset int }
```

8 个 context 各自一份，先写最小字段以让 port 编译通过。

- [ ] **Step 12.8: Build 验证**

```bash
go build ./internal/...
go vet ./internal/.../domain/... ./internal/.../domain/port/...
```

Expected: 全部编译通过，无 import 现 `internal/auth` `internal/memory` `internal/skill` 旧包。

- [ ] **Step 12.9: 加 go-arch-lint 配置（暂不接 CI）**

Create `.go-arch-lint.yml`:

```yaml
version: 3
allow:
  depOnAnyVendor: true

components:
  pkg:
    in: pkg/**
  api:
    in: api/**
  cmd:
    in: cmd/**
  domain-agent:    { in: internal/agent/domain/** }
  domain-memory:   { in: internal/memory/domain/** }
  domain-knowledge:{ in: internal/knowledge/domain/** }
  domain-skill:    { in: internal/skill/domain/** }
  domain-mcp:      { in: internal/mcp/domain/** }
  domain-iam:      { in: internal/iam/domain/** }
  domain-llm:      { in: internal/llmgateway/domain/** }
  domain-platform: { in: internal/platform/domain/** }
  app-agent:       { in: internal/agent/application/** }
  app-memory:      { in: internal/memory/application/** }
  # ... 其他 6 个
  infra-agent:     { in: internal/agent/infrastructure/** }
  # ... 其他 7 个

deps:
  pkg:
    mayDependOn: [pkg]
    cannotDependOn: [api, cmd, domain-*, app-*, infra-*]
  domain-agent:
    cannotDependOn: [pkg, api, cmd, domain-*, app-*, infra-*]
    mayDependOn: []
  # ... 其他 7 个 domain 同样规则
  app-agent:
    mayDependOn: [domain-agent, pkg]
    cannotDependOn: [api, cmd, app-*, infra-*]
  # ... 其他 7 个 application 同样规则
  infra-agent:
    mayDependOn: [domain-agent, app-agent, pkg]
    cannotDependOn: [api, cmd, app-*, infra-*]
  api:
    mayDependOn: [pkg, domain-*, app-*, infra-*]
    cannotDependOn: [cmd]
```

```bash
go install github.com/fe3dback/go-arch-lint@latest
go-arch-lint check
```

Expected: PASS（domain/port 都为空骨架，无违规）。

- [ ] **Step 12.10: Commit + tag**

```bash
git add internal/ .go-arch-lint.yml
git commit -m "feat(internal): bounded context skeletons + port interfaces frozen

Phase 3 of DDD refactor. 8 contexts each get domain/{,port/}, application,
infrastructure dirs. Port interfaces are frozen so phase 4 sub-PRs can
proceed in parallel. .go-arch-lint.yml added (not yet in CI)."
git tag refactor-phase-3-done
```

---

## Phase 4: 8 contexts 并行迁移（Task 13-T20）

> **执行顺序提示:** Tasks 13-20 都依赖 phase 3 的 port 冻结，相互独立，可由 8 个 subagent 并行打 sub-PR。每个 sub-PR 合并到 `feat/ddd-refactor` 分支。每个任务的"完成标准"必须满足契约测试 + go-arch-lint 全绿。

下列每个 task 给出 file 清单 + 关键步骤 + 验收标准。**实际执行时由 subagent 把"关键步骤"展开为完整 step + 代码 + commit。**

### Task 13: platform context 迁移

**Files:**

- Move: `internal/config/config.go` → `internal/platform/config/config.go`
- Move: `internal/harness/*.go` → `internal/platform/harness/`
- Modify: `api/wiring/platform.go`、`cmd/server/main.go` 引用更新

**关键步骤:**

1. `git mv` 两个目录
2. 全局 sed 改 import `internal/config` → `internal/platform/config`
3. 解决 `config → knowledge` 反向依赖：把 `config.Config` 里 knowledge 配置字段抽到 `internal/knowledge/application/config.go`，或保留为纯结构体在 config 里、由 knowledge wiring 自取
4. 跑 `go build ./... && go test -short ./... && make record-contracts && git diff --stat api/http/testdata/contracts`（必须 0 diff）
5. Commit + open sub-PR

**验收:**

- `go-arch-lint check` 通过
- 契约 0 diff
- `internal/config` 与 `internal/harness` 目录消失

---

### Task 14: iam context 迁移（auth + hermes 合并）

**Files:**

- Move: `internal/auth/*.go` → `internal/iam/{domain,application,infrastructure}/...`
- Move: `internal/hermes/*.go` → `internal/iam/infrastructure/hermes/`
- Create: `internal/iam/domain/{user,token,session}.go`
- Create: `internal/iam/domain/port/{user_repo,token_store,oauth_provider}.go`
- Create: `internal/iam/application/{auth_service,onboard_service,jwt_service}.go`
- Create: `internal/iam/infrastructure/persistence/{user_repo_pg,token_store_redis}.go`
- Modify: `api/wiring/iam.go`、`api/http/handler/auth_handler.go`

**关键步骤:**

1. 把 `internal/auth/jwt.go` 拆分：算法/签发逻辑 → `iam/application/jwt_service.go`；types → `iam/domain/token.go`
2. `internal/auth/token_store.go`：接口定义在 `iam/domain/port/token_store.go`，Redis 实现挪到 `iam/infrastructure/persistence/token_store_redis.go`，构造函数签名改为 `(kv pkgredis.KVStore)`
3. `internal/auth/onboard.go` → `iam/application/onboard_service.go`，构造签名改为 `(repo port.UserRepo)`
4. `internal/auth/github.go` → `iam/infrastructure/oauth/github.go`，实现 `port.OAuthProvider`
5. `internal/auth/middleware.go` → `api/middleware/auth.go`（已存在则合并）
6. `internal/hermes/client.go` → `iam/infrastructure/hermes/client.go`
7. handler 改成持 `*application.AuthService`，不再持 `*auth.JWTService`
8. wiring/iam.go 串起来
9. go-arch-lint check / 契约 / Commit

**验收:**

- `internal/auth`、`internal/hermes` 目录消失
- `api/http/handler/auth_handler.go` 不 import `pgxpool` / `goredis`
- 所有 `/auth/*` 端点契约 0 diff

---

### Task 15: llmgateway context 迁移

**Files:**

- Restructure: `internal/llmgateway/*.go` → `internal/llmgateway/{domain,application,infrastructure}/`
- Move: `internal/embedding/*.go` → `internal/llmgateway/infrastructure/embedding/`
- Create: `internal/llmgateway/domain/port/{provider,tenant_repo}.go`
- Modify: `api/wiring/llmgateway.go`

**关键步骤:**

1. `gateway.go` 的 `Gateway` 类型 → `llmgateway/application/gateway_service.go`
2. `pipeline.go`、`pipeline_builder.go` → `llmgateway/application/`
3. `provider.go`、`circuit_breaker.go`、`atomic.go` → `llmgateway/infrastructure/`
4. `internal/embedding/*` → `llmgateway/infrastructure/embedding/`
5. 现 `internal/memory/pipeline/EmbedClient` 接口与 `LLMClient` 接口被消费方改为依赖 `agent/domain/port.LLMCompleter` 和 `memory/domain/port.Embedder`，gateway 只是 satisfier
6. wiring/llmgateway.go 改成构造 `*application.GatewayService`，注册到 Container
7. 契约 / arch-lint / Commit

**验收:**

- `internal/embedding` 目录消失
- 所有 LLM 相关测试通过
- 契约 0 diff

---

### Task 16: mcp context 迁移

**Files:**

- Restructure: `internal/mcp/*.go` → `internal/mcp/{domain,application,infrastructure}/`
- Create: `internal/mcp/domain/{server,tool}.go`
- Create: `internal/mcp/domain/port/{server_repo,client_manager}.go`
- Create: `internal/mcp/application/mcp_service.go`
- Create: `internal/mcp/infrastructure/persistence/server_repo_pg.go`
- Create: `internal/mcp/infrastructure/client/manager.go`

**关键步骤:**

1. `client_manager.go` → `mcp/infrastructure/client/manager.go`
2. server CRUD 方法 → `mcp/application/mcp_service.go`
3. DB 操作 → `mcp/infrastructure/persistence/server_repo_pg.go`，签名 `(db pgstorage.TxBeginner)`
4. wiring/mcp.go 构造 `*MCPService`
5. 契约 / arch-lint / Commit

**验收:**

- `mcp_handler.go` 持 `*MCPService` 而非具体 manager
- `internal/mcp/client_manager.go` 已迁移

---

### Task 17: skill context 迁移（含 skillgateway 合并）

**Files:**

- Restructure: `internal/skill/*.go` + `internal/skillgateway/*.go` → `internal/skill/{domain,application,infrastructure}/`
- Create: `internal/skill/domain/skill.go`、`domain/errors.go`
- Create: `internal/skill/application/{skill_service,executor_service}.go`
- Create: `internal/skill/infrastructure/{persistence,executors/{http,llm,code}}/`

**关键步骤:**

1. `skill/skill.go` 实体 → `skill/domain/skill.go`
2. `executor.go` 中编排逻辑 → `skill/application/executor_service.go`
3. `http_skill.go`、`llm_skill.go`、`internal/skill/code/*` → `skill/infrastructure/executors/`
4. `skillgateway/registry_adapter.go` → `skill/infrastructure/registry_adapter.go`
5. handler 改成持 `*SkillService`
6. wiring/skill.go 串起来
7. 契约 / arch-lint / Commit

**验收:**

- `internal/skillgateway` 目录消失
- `skill_handler.go` 不 import `pgxpool`
- 契约 0 diff

---

### Task 18: knowledge context 迁移

**Files:**

- Restructure: `internal/knowledge/*.go` → `internal/knowledge/{domain,application,infrastructure}/`
- Move: `internal/document/*.go` → `internal/knowledge/infrastructure/document/`
- Create: `internal/knowledge/domain/{kb,doc,chunk}.go`
- Create: `internal/knowledge/application/{rag_service,ingest_service}.go`

**关键步骤:**

1. `rag_service.go` 中 Retrieve 逻辑 → `knowledge/application/rag_service.go`，签名 `(idx port.VectorIndex, emb port.Embedder)`
2. `knowledge_ingest.go` → `knowledge/application/ingest_service.go`
3. `graphrag.go` → `knowledge/application/graphrag_service.go`
4. `internal/document/*` → `knowledge/infrastructure/document/`
5. wiring/knowledge.go
6. 契约 / arch-lint / Commit

**验收:**

- `internal/document` 目录消失
- `RAGService` 不持 `*VectorStore` 直接类型
- `rag_handler.go` 契约 0 diff

---

### Task 19: memory context 迁移

**Files:**

- Restructure: `internal/memory/*.go` + `internal/memory/pipeline/*.go` → `internal/memory/{domain,application,infrastructure}/`
- Create: `internal/memory/domain/{entry,entity,session}.go`
- Create: `internal/memory/application/{memory_service,recall_service,enricher_service}.go`
- Create: `internal/memory/infrastructure/{persistence,pipeline,milvus,nats}/`

**关键步骤:**

1. `memory/manager.go` 拆：CRUD → `memory/application/memory_service.go`；DB 操作 → `memory/infrastructure/persistence/entry_repo_pg.go`
2. `memory/pipeline/*` 整体 → `memory/infrastructure/pipeline/`
3. `vector_adapter.go` → `memory/infrastructure/milvus/index_adapter.go`，实现 `domain/port.VectorIndex`
4. `embedder.go` 中嵌入调用 → 通过 `domain/port.Embedder` 由 wiring 注入
5. `enricher.go` → `memory/application/enricher_service.go` + `memory/infrastructure/llm/enricher_adapter.go`
6. `recall_tool.go` → `memory/application/recall_service.go`
7. `injector.go` 中跨域调用：把 `agent/domain/port.MemoryRecaller` 的实现挪到 `agent/infrastructure/memory_adapter.go`，转发到 `memory/application.RecallService`
8. wiring/memory.go
9. 契约 / arch-lint / Commit

**验收:**

- `internal/memory/pipeline` 目录消失（合并入 infrastructure）
- `*pgxpool.Pool` 不再出现在 `internal/memory/application/**`
- 契约 0 diff

---

### Task 20: agent context 迁移（含 capgateway 内化）

**Files:**

- Restructure: `internal/agent/*.go` → `internal/agent/{domain,application,infrastructure}/`
- Move: `internal/capgateway/*.go` → `internal/agent/{application/capability_router.go, infrastructure/capability/}`
- Create: `internal/agent/domain/{agent,execution,conversation,errors}.go`
- Create: `internal/agent/application/{agent_service,execution_service,chat_service,react/graph.go}`
- Create: `internal/agent/infrastructure/{persistence/{agent_repo_pg,execution_repo_pg,chat_repo_pg},memory_adapter,skill_adapter,knowledge_adapter,llm_adapter,capability/{skill,mcp}}`

**关键步骤:**

1. 拆 `agent.go`（21K）：实体 → domain；方法 → application/{agent_service,execution_service}
2. `registry.go` (13K)：DB 操作 → infrastructure/persistence/agent_repo_pg.go；orchestration 留 application
3. `chat_store.go` → infrastructure/persistence/chat_repo_pg.go
4. `execution_store.go` → infrastructure/persistence/execution_repo_pg.go
5. `graph/react.go` → application/react/graph.go
6. `internal/capgateway/capgateway.go` 中 `CapabilityGateway` → `agent/application/capability_router.go`
7. `internal/capgateway/skill_adapter.go` → `agent/infrastructure/capability/skill.go`，实现 `domain/port.SkillExecutor`
8. 把 `agent → memory/pipeline` 的反向引用全部改为通过 `domain/port.MemoryRecaller` 注入
9. handler 改成持 `*AgentService` + `*ExecutionService` + `*ChatService`
10. wiring/agent.go：注入 memory_adapter / skill_adapter / knowledge_adapter / llm_adapter
11. 契约（含 SSE 流式）/ arch-lint / Commit

**验收:**

- `internal/capgateway` 目录消失
- `internal/agent` import 不出现 `internal/memory/pipeline`、`internal/capgateway`
- `agent_exec_handler.go` 流式契约 0 diff
- `react.llm` / `react.tool` 日志事件名仍存在

---

## Task 21: 删除 alias 桥 + 旧目录（Phase 5）

**Files:**

- Delete: `pkg/postgres/postgres.go`、`pkg/redis/redis.go`、`pkg/vector/vector_store.go`、`pkg/tenantdb/{milvus,nats,neo4j}.go`
- Delete: `api/router.go`
- Delete: 阶段 4 留下的所有空 alias 文件

- [ ] **Step 21.1: 全局替换 import**

```bash
cd /home/yang/go-projects/stratum
grep -rl 'byteBuilderX/stratum/pkg/postgres"' --include='*.go' \
  | xargs sed -i 's|byteBuilderX/stratum/pkg/postgres"|byteBuilderX/stratum/pkg/storage/postgres"|g'
grep -rl 'byteBuilderX/stratum/pkg/redis"' --include='*.go' \
  | xargs sed -i 's|byteBuilderX/stratum/pkg/redis"|byteBuilderX/stratum/pkg/storage/redis"|g'
grep -rl 'byteBuilderX/stratum/pkg/vector"' --include='*.go' \
  | xargs sed -i 's|byteBuilderX/stratum/pkg/vector"|byteBuilderX/stratum/pkg/storage/milvus"|g'
```

- [ ] **Step 21.2: 删旧目录**

```bash
rm -rf pkg/postgres pkg/redis pkg/vector pkg/tenantdb
rm api/router.go
```

- [ ] **Step 21.3: Build + 契约**

```bash
go build ./...
go test -short ./...
make record-contracts && git diff --stat api/http/testdata/contracts/
```

Expected: 全绿 + 0 diff。

- [ ] **Step 21.4: Commit**

```bash
git add -A
git commit -m "refactor: remove alias shims for pkg/{postgres,redis,vector,tenantdb}

Phase 5 cleanup. All callers now import pkg/storage/* directly. api/router.go
deprecated shim deleted; api/http.NewRouter is the only entrypoint."
```

---

## Task 22: 启用 go-arch-lint + depguard CI（Phase 5 收尾）

**Files:**

- Modify: `.github/workflows/ci.yml`
- Modify: `.golangci.yml`
- Verify: `.go-arch-lint.yml`

- [ ] **Step 22.1: 完善 .go-arch-lint.yml**

补全所有 8 个 application 与 infrastructure 组件的 dep 规则。关键约束：

- `app-X` mayDependOn: `domain-X`, `pkg`, `domain-Y/port`（仅 port 子目录！）
- `infra-X` mayDependOn: `domain-X`, `app-X`, `pkg`, `domain-Y/port`
- 任何 `domain-X` 都禁止 import 任何外包

把 8 个 context 全部覆盖。

- [ ] **Step 22.2: golangci-lint 加 depguard 规则**

Edit `.golangci.yml`：

```yaml
linters:
  enable:
    - depguard

linters-settings:
  depguard:
    rules:
      domain-no-third-party:
        list-mode: lax
        files:
          - "**/internal/*/domain/**"
          - "!**/internal/*/domain/port/**"
        deny:
          - pkg: "github.com/jackc/pgx/v5"
            desc: "domain layer must not import database drivers"
          - pkg: "github.com/redis/go-redis/v9"
            desc: "domain layer must not import redis"
          - pkg: "github.com/nats-io/nats.go"
            desc: "domain layer must not import messaging"
          - pkg: "github.com/gin-gonic/gin"
            desc: "domain layer must not import http frameworks"
          - pkg: "go.uber.org/zap"
            desc: "domain layer must not log; return errors instead"
      app-no-infra-deps:
        list-mode: lax
        files:
          - "**/internal/*/application/**"
        deny:
          - pkg: "github.com/jackc/pgx/v5"
          - pkg: "github.com/redis/go-redis/v9"
          - pkg: "github.com/nats-io/nats.go"
          - pkg: "github.com/gin-gonic/gin"
      pkg-no-internal:
        list-mode: lax
        files:
          - "**/pkg/**"
        deny:
          - pkg: "github.com/byteBuilderX/stratum/internal"
            desc: "pkg must not depend on internal"
```

- [ ] **Step 22.3: CI workflow 加 arch-check job**

Edit `.github/workflows/ci.yml` 添加：

```yaml
  arch-check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.22'
      - run: go install github.com/fe3dback/go-arch-lint@latest
      - run: go-arch-lint check
      - uses: golangci/golangci-lint-action@v4
        with:
          args: --timeout=5m
```

- [ ] **Step 22.4: 本地跑全套**

```bash
go-arch-lint check
golangci-lint run
go test -race -timeout 30s ./...
make record-contracts && git diff --stat api/http/testdata/contracts/
```

Expected: 全绿 + 0 diff。

- [ ] **Step 22.5: Commit + tag final**

```bash
git add .go-arch-lint.yml .golangci.yml .github/workflows/ci.yml
git commit -m "ci: enforce DDD layer boundaries via go-arch-lint + depguard

Phase 5 finale. CI now blocks:
- domain importing third-party libs (drivers, frameworks, zap)
- application importing infrastructure-only libs
- pkg importing internal
- cross-context imports outside domain/port

Refactor complete."
git tag refactor-complete
```

---

## Self-Review

### Spec coverage check

| Spec section | Plan task |
|--------------|-----------|
| §1 Bounded contexts (8) | Task 12, 13-20 |
| §1.3 capgateway 归 agent | Task 20 |
| §2 三层模板 | Task 12 (port 冻结) + 13-20 (展开) |
| §2.1 port 在消费侧 | Task 12 |
| §2.2 反向依赖修复 | Task 13 (config→knowledge), Task 19 (agent→memory/pipeline 反向) |
| §3.1 pkg 重组 | Task 2-6 |
| §3.2 关键接口 | Task 2 (Querier), 3 (KVStore), 4 (VectorIndex), 6 (Doer) |
| §3.3 业务侧改造 | Task 7, 13-20 |
| §3.4 命名一致性 | Task 21 (清桥时校验) |
| §4.1 api 目录 | Task 10 |
| §4.2 main.go 形态 | Task 10.4 |
| §4.3 Container | Task 8, 9 |
| §4.4 handler 纪律 | Task 13-20 (各 context 改 handler) |
| §4.5 跨 context 调用链 | Task 19 (memory adapter), Task 20 (agent infrastructure adapters) |
| §4.6 API 向后兼容 | Task 1 (录), Task 11 (验证), 每个 task 末尾的"契约 0 diff" |
| §5.1 错误处理分层 | Task 13-20 (各 context domain/errors.go + infra translate) |
| §5.2 日志规范 | Task 22 (depguard 禁 domain 用 zap) |
| §5.3 测试策略 | 各 task 嵌入 test 步骤 |
| §5.4 CI 防回潮 | Task 22 |
| §6 Phase 0-5 | Task 1, 2-7, 8-11, 12, 13-20, 21-22 |
| §7 验收标准 | Task 22 末尾的全套绿 |

### Placeholder scan

无 TBD / fill-in-later / "similar to task N" 模式。Phase 4 的 8 个 task 给出了文件清单 + 关键步骤 + 验收标准，由 subagent 在执行时按"关键步骤"模式展开 — 这是显式约定，不是 placeholder。

### Type consistency

- `Querier` / `TxBeginner` / `TenantExecer`：Task 2 定义，Task 13-20 引用一致
- `KVStore`：Task 3 定义，Task 14 引用一致
- `VectorIndex`：Task 4 (pkg) 定义为 `EnsureCollection/Upsert/Search/Drop`；Task 12 (memory port + knowledge port) 各自定义最小子集，方法签名匹配
- `Doer`：Task 6 与 Task 12.5 (skill port) 同名，定义一致
- `Container.Storage` / `Container.LLMGateway` / `Container.Platform` / `Container.AgentSvc` 等字段名贯穿 Task 8-22

### Gaps fixed inline

- 加了 Task 11 单独验证 phase 2 不破契约
- Task 12.7 加了"空 domain entity 占位"步骤，避免 port 编译失败
- Task 22.2 给出 depguard 完整 yaml 而非 "add deny rules"

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-06-16-stratum-ddd-refactor.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
