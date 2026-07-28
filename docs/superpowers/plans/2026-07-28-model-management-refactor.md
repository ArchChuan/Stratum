# 模型管理重构 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 重构 llmgateway，支持多厂商模型接入（Anthropic/Ollama/DeepSeek 等），协议抽象层，模型成为一等公民带完整元数据，租户级完全独立管理，前端独立 Tab。

**Architecture:** Provider + Model 领域实体 → Protocol 接口抽象（ChatProtocol/EmbedProtocol）→ ModelRegistry 替代硬编码 clients map → 租户 DB 表替代 settings JSONB → HTTP API + 前端管理界面。现有 Gateway 重构为薄门面委托 ModelRegistry。

**Tech Stack:** Go 1.25, pgx v5, Gin, React 18 + Ant Design 5, TypeScript

**Design spec:** `docs/superpowers/specs/2026-07-28-model-management-refactor-design.md`

## Global Constraints

- Go 行宽 ≤120，圈复杂度 ≤10，import 按 stdlib/third-party/internal 分组
- 错误逐层用 `fmt.Errorf("operation: %w", err)` 包装，日志只用 Zap
- timeout/TTL/pagination 等行为数字放 `pkg/constants/` 或包内 `const` 块
- DDD 依赖方向 handler → application → domain/port，infrastructure 实现 port
- 所有 tenant-scoped repository 通过 `execTenant(ctx, tenantID, fn)`
- 新表用 `IF NOT EXISTS`，新列用 `ADD COLUMN IF NOT EXISTS`
- 前端：普通调用走 `client.ts` 唯一 Axios 实例，行为常量放 `web/src/constants/`
- 错误通知 `message.error({ content: ..., duration: 0 })`，成功 `message.success({ content: ..., duration: 2 })`
- 用户可见字符串中文，Token 不得进 localStorage
- Commit 格式 `[type](scope): description`

---

### Task 1: Domain entities — Provider & Model + port interfaces

**Files:**

- Create: `internal/llmgateway/domain/provider.go`
- Create: `internal/llmgateway/domain/model.go`
- Create: `internal/llmgateway/domain/port/provider_repo.go`
- Create: `internal/llmgateway/domain/port/model_repo.go`
- Modify: `internal/llmgateway/domain/port/model_catalog.go`

**Interfaces:**

- Produces:
  - `type ProviderKind string` — `ProviderOpenAICompat`, `ProviderAnthropic`, `ProviderOllama`
  - `type Provider struct { ID, TenantID, Name string; Kind ProviderKind; BaseURL, APIKey, DefaultModel string; Enabled bool; CreatedAt, UpdatedAt time.Time }`
  - `type ModelCapability string` — `CapChat`, `CapEmbedding`, `CapVision`, `CapToolUse`, `CapReasoning`
  - `type Model struct { ID, TenantID, ProviderID, Name, DisplayName string; Capabilities []ModelCapability; ContextWindow, MaxTokens int; InputPrice, OutputPrice float64; Recommended, Enabled, ProviderManaged bool; CreatedAt, UpdatedAt time.Time }`
  - `type ProviderRepository interface { Create(ctx, tenantID, *Provider) error; Get(ctx, tenantID, id) (*Provider, error); List(ctx, tenantID) ([]Provider, error); Update(ctx, tenantID, *Provider) error; Delete(ctx, tenantID, id) error }`
  - `type ModelRepository interface { Create(ctx, tenantID, *Model) error; Get(ctx, tenantID, id) (*Model, error); List(ctx, tenantID, filter ModelFilter) ([]Model, error); Update(ctx, tenantID, *Model) error; UpsertDiscovered(ctx, tenantID, providerID, []Model) ([]Model, error); Delete(ctx, tenantID, id) error; Toggle(ctx, tenantID, id, enabled bool) error }`
  - `type ModelFilter struct { Capability ModelCapability; ProviderID string; Enabled *bool }`
  - Extend `ModelCatalog`: add `ListChatModelsByTenant(ctx, tenantID) ([]string, error)` → return model names from DB; deprecate parameterless version

- [ ] **Step 1: Write domain types test**

```go
// internal/llmgateway/domain/provider_test.go
package domain_test

import (
    "testing"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

func TestProviderKindValues(t *testing.T) {
    if domain.ProviderOpenAICompat != "openai_compat" {
        t.Errorf("expected openai_compat, got %s", domain.ProviderOpenAICompat)
    }
    if domain.ProviderAnthropic != "anthropic" {
        t.Errorf("expected anthropic, got %s", domain.ProviderAnthropic)
    }
    if domain.ProviderOllama != "ollama" {
        t.Errorf("expected ollama, got %s", domain.ProviderOllama)
    }
}

func TestModelCapabilityValues(t *testing.T) {
    caps := map[domain.ModelCapability]bool{
        domain.CapChat: true, domain.CapEmbedding: true,
        domain.CapVision: true, domain.CapToolUse: true,
        domain.CapReasoning: true,
    }
    if len(caps) != 5 {
        t.Errorf("expected 5 capabilities, got %d", len(caps))
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd internal/llmgateway/domain && go test -run TestProviderKindValues -v`
Expected: FAIL — package domain_test, undefined types

- [ ] **Step 3: Create `internal/llmgateway/domain/provider.go`**

```go
package domain

import "time"

type ProviderKind string

const (
    ProviderOpenAICompat ProviderKind = "openai_compat"
    ProviderAnthropic    ProviderKind = "anthropic"
    ProviderOllama       ProviderKind = "ollama"
)

type Provider struct {
    ID           string
    TenantID     string
    Name         string
    Kind         ProviderKind
    BaseURL      string
    APIKey       string
    DefaultModel string
    Enabled      bool
    CreatedAt    time.Time
    UpdatedAt    time.Time
}
```

- [ ] **Step 4: Create `internal/llmgateway/domain/model.go`**

```go
package domain

import "time"

type ModelCapability string

const (
    CapChat      ModelCapability = "chat"
    CapEmbedding ModelCapability = "embedding"
    CapVision    ModelCapability = "vision"
    CapToolUse   ModelCapability = "tool_use"
    CapReasoning ModelCapability = "reasoning"
)

type Model struct {
    ID              string
    TenantID        string
    ProviderID      string
    Name            string
    DisplayName     string
    Capabilities    []ModelCapability
    ContextWindow   int
    MaxTokens       int
    InputPrice      float64
    OutputPrice     float64
    Recommended     bool
    Enabled         bool
    ProviderManaged bool
    CreatedAt       time.Time
    UpdatedAt       time.Time
}
```

- [ ] **Step 5: Create `internal/llmgateway/domain/port/provider_repo.go`**

```go
package port

import (
    "context"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

type ProviderRepository interface {
    Create(ctx context.Context, tenantID string, p *domain.Provider) error
    Get(ctx context.Context, tenantID, id string) (*domain.Provider, error)
    List(ctx context.Context, tenantID string) ([]domain.Provider, error)
    Update(ctx context.Context, tenantID string, p *domain.Provider) error
    Delete(ctx context.Context, tenantID, id string) error
}
```

- [ ] **Step 6: Create `internal/llmgateway/domain/port/model_repo.go`**

```go
package port

import (
    "context"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

type ModelFilter struct {
    Capability domain.ModelCapability
    ProviderID string
    Enabled    *bool
}

type ModelRepository interface {
    Create(ctx context.Context, tenantID string, m *domain.Model) error
    Get(ctx context.Context, tenantID, id string) (*domain.Model, error)
    List(ctx context.Context, tenantID string, filter ModelFilter) ([]domain.Model, error)
    Update(ctx context.Context, tenantID string, m *domain.Model) error
    UpsertDiscovered(ctx context.Context, tenantID, providerID string, models []domain.Model) ([]domain.Model, error)
    Delete(ctx context.Context, tenantID, id string) error
    Toggle(ctx context.Context, tenantID, id string, enabled bool) error
}
```

- [ ] **Step 7: Extend `internal/llmgateway/domain/port/model_catalog.go`**

Read existing, append:

```go
import "context"

// append to existing ModelCatalog interface:
// ListChatModelsByTenant(ctx context.Context, tenantID string) ([]string, error)
// ListEmbeddingModelsByTenant(ctx context.Context, tenantID string) ([]string, error)
```

- [ ] **Step 8: Run tests**

Run: `cd internal/llmgateway/domain && go test -v ./...`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/llmgateway/domain/provider.go internal/llmgateway/domain/model.go \
        internal/llmgateway/domain/port/provider_repo.go internal/llmgateway/domain/port/model_repo.go \
        internal/llmgateway/domain/port/model_catalog.go internal/llmgateway/domain/provider_test.go
git commit -m "[feat](llmgateway): add Provider and Model domain entities with port interfaces"
```

---

### Task 2: Protocol interface extraction

**Files:**

- Create: `internal/llmgateway/infrastructure/protocol.go`
- Modify: `internal/llmgateway/infrastructure/openai_compat.go`
- Modify: `internal/llmgateway/infrastructure/gateway.go:91-108` (move LLMClient/EmbeddingClient → new protocol.go, deprecate old)

**Interfaces:**

- Consumes: `ProviderConfig` (existing), `CompletionRequest`, `CompletionResponse`, `EmbeddingRequest`, `EmbeddingResponse` (existing in gateway.go)
- Produces:
  - `type ChatProtocol interface { Complete(ctx, cfg ProviderConfig, req) (*CompletionResponse, error); CompleteStream(ctx, cfg ProviderConfig, req, onToken) (*CompletionResponse, error); Health(ctx, cfg ProviderConfig) error; ListModels(ctx, cfg ProviderConfig) ([]string, error) }`
  - `type EmbedProtocol interface { CreateEmbeddings(ctx, cfg ProviderConfig, req) (*EmbeddingResponse, error); BatchSize() int }`

- [ ] **Step 1: Create `internal/llmgateway/infrastructure/protocol.go`**

```go
package infrastructure

import (
    "context"
)

// ChatProtocol defines the interface for chat-completion providers.
type ChatProtocol interface {
    Complete(ctx context.Context, cfg ProviderConfig, req *CompletionRequest) (*CompletionResponse, error)
    CompleteStream(ctx context.Context, cfg ProviderConfig, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error)
    Health(ctx context.Context, cfg ProviderConfig) error
    ListModels(ctx context.Context, cfg ProviderConfig) ([]string, error)
}

// EmbedProtocol defines the interface for embedding providers.
type EmbedProtocol interface {
    CreateEmbeddings(ctx context.Context, cfg ProviderConfig, req *EmbeddingRequest) (*EmbeddingResponse, error)
    BatchSize() int
}
```

- [ ] **Step 2: Refactor `OpenAICompatClient` to implement `ChatProtocol` + `EmbedProtocol`**

Add methods with `cfg ProviderConfig` parameter that delegate to existing instance-method logic. The existing methods keep working (backward compat). Add:

```go
// Chat protocol — stateless wrappers using ProviderConfig passed at call time.
func (c *OpenAICompatClient) ChatComplete(ctx context.Context, cfg ProviderConfig, req *CompletionRequest) (*CompletionResponse, error) {
    return c.Complete(ctx, req)
}

func (c *OpenAICompatClient) ChatCompleteStream(ctx context.Context, cfg ProviderConfig, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error) {
    return c.CompleteStream(ctx, req, onToken)
}

func (c *OpenAICompatClient) ChatHealth(ctx context.Context, cfg ProviderConfig) error {
    return c.Health(ctx)
}

func (c *OpenAICompatClient) ChatListModels(ctx context.Context, cfg ProviderConfig) ([]string, error) {
    return c.Models(), nil
}
```

NOTE: Full refactor to make `OpenAICompatClient` stateless per-call comes later. For now, add thin adapters verifying the new interface contracts compile.

- [ ] **Step 3: Verify compilation**

Run: `cd internal/llmgateway/infrastructure && go build ./...`
Expected: ok

- [ ] **Step 4: Verify existing tests pass**

Run: `cd internal/llmgateway/infrastructure && go test -short ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/llmgateway/infrastructure/protocol.go internal/llmgateway/infrastructure/openai_compat.go
git commit -m "[feat](llmgateway): extract ChatProtocol and EmbedProtocol interfaces"
```

---

### Task 3: Tenant DDL — providers & models tables

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Create: `pkg/storage/postgres/tenant_schema_test.go` (verify new tables exist)

**Interfaces:**

- Produces: `providers` table, `models` table with indexes

- [ ] **Step 1: Add DDL to `pkg/storage/postgres/tenant_schema.sql`**

Append to existing file:

```sql
-- Provider registry (tenant-scoped LLM providers)
CREATE TABLE IF NOT EXISTS providers (
    id          TEXT PRIMARY KEY DEFAULT gen_ulid(),
    tenant_id   TEXT NOT NULL,
    name        TEXT NOT NULL,
    kind        TEXT NOT NULL,
    base_url    TEXT NOT NULL DEFAULT '',
    api_key     TEXT NOT NULL DEFAULT '',
    default_model TEXT NOT NULL DEFAULT '',
    enabled     BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, name)
);

-- Model catalogue (tenant-scoped model registry)
CREATE TABLE IF NOT EXISTS models (
    id              TEXT PRIMARY KEY DEFAULT gen_ulid(),
    tenant_id       TEXT NOT NULL,
    provider_id     TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    display_name    TEXT NOT NULL DEFAULT '',
    capabilities    TEXT[] NOT NULL DEFAULT '{}',
    context_window  INT NOT NULL DEFAULT 0,
    max_tokens      INT NOT NULL DEFAULT 0,
    input_price     DOUBLE PRECISION NOT NULL DEFAULT 0,
    output_price    DOUBLE PRECISION NOT NULL DEFAULT 0,
    recommended     BOOLEAN NOT NULL DEFAULT false,
    enabled         BOOLEAN NOT NULL DEFAULT true,
    provider_managed BOOLEAN NOT NULL DEFAULT false,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(tenant_id, provider_id, name)
);

CREATE INDEX IF NOT EXISTS idx_models_tenant ON models(tenant_id);
CREATE INDEX IF NOT EXISTS idx_models_provider ON models(provider_id);
CREATE INDEX IF NOT EXISTS idx_models_enabled ON models(tenant_id, enabled);
```

- [ ] **Step 2: Verify provision applies cleanly**

Run: `go test -run TestProvisionTenantSchema -v ./pkg/storage/postgres/...`
Expected: PASS (or skip if no DB)

- [ ] **Step 3: Commit**

```bash
git add pkg/storage/postgres/tenant_schema.sql
git commit -m "[feat](llmgateway): add providers and models tenant DDL"
```

---

### Task 4: Provider & Model PostgreSQL repositories

**Files:**

- Create: `internal/llmgateway/infrastructure/provider_repo.go`
- Create: `internal/llmgateway/infrastructure/model_repo.go`
- Create: `internal/llmgateway/infrastructure/provider_repo_test.go`
- Create: `internal/llmgateway/infrastructure/model_repo_test.go`

**Interfaces:**

- Consumes: `port.ProviderRepository`, `port.ModelRepository` from Task 1
- Produces: `PgProviderRepo`, `PgModelRepo` implementing those ports via `execTenant`

- [ ] **Step 1: Create `internal/llmgateway/infrastructure/provider_repo.go`**

```go
package infrastructure

import (
    "context"
    "fmt"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
    "github.com/byteBuilderX/stratum/pkg/storage/postgres"
    "github.com/byteBuilderX/stratum/pkg/tenantdb"
)

type PgProviderRepo struct{ pool *pgxpool.Pool }

func NewPgProviderRepo(pool *pgxpool.Pool) *PgProviderRepo {
    return &PgProviderRepo{pool: pool}
}

func (r *PgProviderRepo) execTenant(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) error {
    ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
    return tenantdb.ExecTenant(ctx, r.pool, fn)
}

func (r *PgProviderRepo) Create(ctx context.Context, tenantID string, p *domain.Provider) error {
    return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
        _, err := tx.Exec(ctx,
            `INSERT INTO providers (id, tenant_id, name, kind, base_url, api_key, default_model, enabled)
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
            p.ID, tenantID, p.Name, string(p.Kind), p.BaseURL, p.APIKey, p.DefaultModel, p.Enabled,
        )
        if err != nil {
            return fmt.Errorf("create provider: %w", err)
        }
        return nil
    })
}

func (r *PgProviderRepo) Get(ctx context.Context, tenantID, id string) (*domain.Provider, error) {
    var p domain.Provider
    var kind string
    err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
        return tx.QueryRow(ctx,
            `SELECT id, tenant_id, name, kind, base_url, api_key, default_model, enabled, created_at, updated_at
             FROM providers WHERE id=$1`, id,
        ).Scan(&p.ID, &p.TenantID, &p.Name, &kind, &p.BaseURL, &p.APIKey, &p.DefaultModel, &p.Enabled,
            &p.CreatedAt, &p.UpdatedAt)
    })
    if err != nil {
        return nil, fmt.Errorf("get provider: %w", err)
    }
    p.Kind = domain.ProviderKind(kind)
    return &p, nil
}

func (r *PgProviderRepo) List(ctx context.Context, tenantID string) ([]domain.Provider, error) {
    var out []domain.Provider
    err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
        rows, err := tx.Query(ctx,
            `SELECT id, tenant_id, name, kind, base_url, api_key, default_model, enabled, created_at, updated_at
             FROM providers ORDER BY created_at`)
        if err != nil {
            return err
        }
        defer rows.Close()
        for rows.Next() {
            var p domain.Provider
            var kind string
            if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &kind, &p.BaseURL, &p.APIKey,
                &p.DefaultModel, &p.Enabled, &p.CreatedAt, &p.UpdatedAt); err != nil {
                return err
            }
            p.Kind = domain.ProviderKind(kind)
            out = append(out, p)
        }
        return rows.Err()
    })
    if err != nil {
        return nil, fmt.Errorf("list providers: %w", err)
    }
    if out == nil {
        out = []domain.Provider{}
    }
    return out, nil
}

func (r *PgProviderRepo) Update(ctx context.Context, tenantID string, p *domain.Provider) error {
    return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
        tag, err := tx.Exec(ctx,
            `UPDATE providers SET name=$1, kind=$2, base_url=$3, api_key=$4, default_model=$5, enabled=$6, updated_at=now()
             WHERE id=$7 AND tenant_id=$8`,
            p.Name, string(p.Kind), p.BaseURL, p.APIKey, p.DefaultModel, p.Enabled, p.ID, tenantID,
        )
        if err != nil {
            return fmt.Errorf("update provider: %w", err)
        }
        if tag.RowsAffected() == 0 {
            return fmt.Errorf("provider not found: %s", p.ID)
        }
        return nil
    })
}

func (r *PgProviderRepo) Delete(ctx context.Context, tenantID, id string) error {
    return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
        _, err := tx.Exec(ctx, `DELETE FROM providers WHERE id=$1 AND tenant_id=$2`, id, tenantID)
        return err
    })
}
```

- [ ] **Step 2: Create `internal/llmgateway/infrastructure/model_repo.go`**

```go
package infrastructure

import (
    "context"
    "fmt"
    "strings"
    "github.com/jackc/pgx/v5"
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
    "github.com/byteBuilderX/stratum/pkg/storage/postgres"
    "github.com/byteBuilderX/stratum/pkg/tenantdb"
)

type PgModelRepo struct{ pool *pgxpool.Pool }

func NewPgModelRepo(pool *pgxpool.Pool) *PgModelRepo {
    return &PgModelRepo{pool: pool}
}

func (r *PgModelRepo) execTenant(ctx context.Context, tenantID string, fn func(context.Context, pgx.Tx) error) error {
    ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
    return tenantdb.ExecTenant(ctx, r.pool, fn)
}

func (r *PgModelRepo) Create(ctx context.Context, tenantID string, m *domain.Model) error {
    return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
        caps := modelCapsToStrings(m.Capabilities)
        _, err := tx.Exec(ctx,
            `INSERT INTO models (id, tenant_id, provider_id, name, display_name, capabilities,
             context_window, max_tokens, input_price, output_price, recommended, enabled, provider_managed)
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
            m.ID, tenantID, m.ProviderID, m.Name, m.DisplayName, caps,
            m.ContextWindow, m.MaxTokens, m.InputPrice, m.OutputPrice,
            m.Recommended, m.Enabled, m.ProviderManaged,
        )
        if err != nil {
            return fmt.Errorf("create model: %w", err)
        }
        return nil
    })
}

func (r *PgModelRepo) Get(ctx context.Context, tenantID, id string) (*domain.Model, error) {
    var m domain.Model
    var caps []string
    err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
        return tx.QueryRow(ctx,
            `SELECT id, tenant_id, provider_id, name, display_name, capabilities,
             context_window, max_tokens, input_price, output_price, recommended, enabled, provider_managed,
             created_at, updated_at FROM models WHERE id=$1`, id,
        ).Scan(&m.ID, &m.TenantID, &m.ProviderID, &m.Name, &m.DisplayName, &caps,
            &m.ContextWindow, &m.MaxTokens, &m.InputPrice, &m.OutputPrice,
            &m.Recommended, &m.Enabled, &m.ProviderManaged, &m.CreatedAt, &m.UpdatedAt)
    })
    if err != nil {
        return nil, fmt.Errorf("get model: %w", err)
    }
    m.Capabilities = stringsToModelCaps(caps)
    return &m, nil
}

func (r *PgModelRepo) List(ctx context.Context, tenantID string, filter port.ModelFilter) ([]domain.Model, error) {
    var out []domain.Model
    err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
        query := `SELECT id, tenant_id, provider_id, name, display_name, capabilities,
                  context_window, max_tokens, input_price, output_price, recommended, enabled,
                  provider_managed, created_at, updated_at FROM models WHERE tenant_id=$1`
        args := []any{tenantID}
        argIdx := 2
        if filter.ProviderID != "" {
            query += fmt.Sprintf(" AND provider_id=$%d", argIdx)
            args = append(args, filter.ProviderID)
            argIdx++
        }
        if filter.Enabled != nil {
            query += fmt.Sprintf(" AND enabled=$%d", argIdx)
            args = append(args, *filter.Enabled)
            argIdx++
        }
        if filter.Capability != "" {
            query += fmt.Sprintf(" AND $%d = ANY(capabilities)", argIdx)
            args = append(args, string(filter.Capability))
            argIdx++
        }
        query += " ORDER BY name"
        rows, err := tx.Query(ctx, query, args...)
        if err != nil {
            return err
        }
        defer rows.Close()
        for rows.Next() {
            var m domain.Model
            var caps []string
            if err := rows.Scan(&m.ID, &m.TenantID, &m.ProviderID, &m.Name, &m.DisplayName, &caps,
                &m.ContextWindow, &m.MaxTokens, &m.InputPrice, &m.OutputPrice,
                &m.Recommended, &m.Enabled, &m.ProviderManaged, &m.CreatedAt, &m.UpdatedAt); err != nil {
                return err
            }
            m.Capabilities = stringsToModelCaps(caps)
            out = append(out, m)
        }
        return rows.Err()
    })
    if err != nil {
        return nil, fmt.Errorf("list models: %w", err)
    }
    if out == nil {
        out = []domain.Model{}
    }
    return out, nil
}

func (r *PgModelRepo) Update(ctx context.Context, tenantID string, m *domain.Model) error {
    return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
        caps := modelCapsToStrings(m.Capabilities)
        tag, err := tx.Exec(ctx,
            `UPDATE models SET display_name=$1, capabilities=$2, context_window=$3, max_tokens=$4,
             input_price=$5, output_price=$6, recommended=$7, enabled=$8, updated_at=now()
             WHERE id=$9 AND tenant_id=$10`,
            m.DisplayName, caps, m.ContextWindow, m.MaxTokens,
            m.InputPrice, m.OutputPrice, m.Recommended, m.Enabled, m.ID, tenantID,
        )
        if err != nil {
            return fmt.Errorf("update model: %w", err)
        }
        if tag.RowsAffected() == 0 {
            return fmt.Errorf("model not found: %s", m.ID)
        }
        return nil
    })
}

func (r *PgModelRepo) UpsertDiscovered(ctx context.Context, tenantID, providerID string, models []domain.Model) ([]domain.Model, error) {
    var result []domain.Model
    err := r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
        // Mark all provider-managed models for this provider as disabled,
        // then re-enable those still present.
        if _, err := tx.Exec(ctx,
            `UPDATE models SET enabled=false, updated_at=now()
             WHERE tenant_id=$1 AND provider_id=$2 AND provider_managed=true`,
            tenantID, providerID); err != nil {
            return fmt.Errorf("upsert models: disable phase: %w", err)
        }
        for _, m := range models {
            caps := modelCapsToStrings(m.Capabilities)
            var existing domain.Model
            var existingCaps []string
            err := tx.QueryRow(ctx,
                `SELECT id, display_name, capabilities, context_window, max_tokens,
                 input_price, output_price, recommended
                 FROM models WHERE tenant_id=$1 AND provider_id=$2 AND name=$3`,
                tenantID, providerID, m.Name,
            ).Scan(&existing.ID, &existing.DisplayName, &existingCaps,
                &existing.ContextWindow, &existing.MaxTokens,
                &existing.InputPrice, &existing.OutputPrice, &existing.Recommended)
            if err != nil {
                // New model — insert with defaults
                _, err = tx.Exec(ctx,
                    `INSERT INTO models (id, tenant_id, provider_id, name, display_name, capabilities,
                     context_window, max_tokens, input_price, output_price, recommended, enabled, provider_managed)
                     VALUES (gen_ulid(), $1,$2,$3,$4,$5,$6,$7,$8,$9,$10,true,true)`,
                    tenantID, providerID, m.Name, m.DisplayName, caps,
                    m.ContextWindow, m.MaxTokens, m.InputPrice, m.OutputPrice, m.Recommended,
                )
                if err != nil {
                    return fmt.Errorf("upsert models: insert %s: %w", m.Name, err)
                }
            } else {
                // Existing — re-enable, preserve user edits
                _, err = tx.Exec(ctx,
                    `UPDATE models SET enabled=true, updated_at=now()
                     WHERE id=$1`, existing.ID)
                if err != nil {
                    return fmt.Errorf("upsert models: update %s: %w", m.Name, err)
                }
            }
        }
        // Read back
        rows, err := tx.Query(ctx,
            `SELECT id, tenant_id, provider_id, name, display_name, capabilities,
             context_window, max_tokens, input_price, output_price, recommended, enabled,
             provider_managed, created_at, updated_at
             FROM models WHERE tenant_id=$1 AND provider_id=$2 ORDER BY name`,
            tenantID, providerID)
        if err != nil {
            return err
        }
        defer rows.Close()
        for rows.Next() {
            var model domain.Model
            var caps []string
            if err := rows.Scan(&model.ID, &model.TenantID, &model.ProviderID, &model.Name,
                &model.DisplayName, &caps, &model.ContextWindow, &model.MaxTokens,
                &model.InputPrice, &model.OutputPrice, &model.Recommended, &model.Enabled,
                &model.ProviderManaged, &model.CreatedAt, &model.UpdatedAt); err != nil {
                return err
            }
            model.Capabilities = stringsToModelCaps(caps)
            result = append(result, model)
        }
        return rows.Err()
    })
    return result, err
}

func (r *PgModelRepo) Delete(ctx context.Context, tenantID, id string) error {
    return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
        tag, err := tx.Exec(ctx,
            `DELETE FROM models WHERE id=$1 AND tenant_id=$2 AND provider_managed=false`, id, tenantID)
        if err != nil {
            return fmt.Errorf("delete model: %w", err)
        }
        if tag.RowsAffected() == 0 {
            return fmt.Errorf("model not found or is provider-managed: %s", id)
        }
        return nil
    })
}

func (r *PgModelRepo) Toggle(ctx context.Context, tenantID, id string, enabled bool) error {
    return r.execTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
        tag, err := tx.Exec(ctx,
            `UPDATE models SET enabled=$1, updated_at=now() WHERE id=$2 AND tenant_id=$3`,
            enabled, id, tenantID)
        if err != nil {
            return fmt.Errorf("toggle model: %w", err)
        }
        if tag.RowsAffected() == 0 {
            return fmt.Errorf("model not found: %s", id)
        }
        return nil
    })
}

func modelCapsToStrings(caps []domain.ModelCapability) []string {
    out := make([]string, len(caps))
    for i, c := range caps {
        out[i] = string(c)
    }
    if out == nil {
        out = []string{}
    }
    return out
}

func stringsToModelCaps(ss []string) []domain.ModelCapability {
    out := make([]domain.ModelCapability, len(ss))
    for i, s := range ss {
        out[i] = domain.ModelCapability(s)
    }
    return out
}
```

- [ ] **Step 3: Write repository integration tests**

```go
// internal/llmgateway/infrastructure/provider_repo_test.go
package infrastructure_test

import (
    "context"
    "testing"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
    "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
    "github.com/byteBuilderX/stratum/pkg/storage/postgres/postgrestest"
)

func TestPgProviderRepo_CRUD(t *testing.T) {
    pool := postgrestest.NewPool(t)
    tenantID := postgrestest.CreateTestTenant(t, pool)
    repo := infrastructure.NewPgProviderRepo(pool)
    ctx := context.Background()

    p := &domain.Provider{
        ID: "test-prov-1", Name: "test-qwen", Kind: domain.ProviderOpenAICompat,
        BaseURL: "https://test.example.com/v1", APIKey: "sk-test",
    }
    // Create
    if err := repo.Create(ctx, tenantID, p); err != nil {
        t.Fatalf("create: %v", err)
    }
    // Get
    got, err := repo.Get(ctx, tenantID, p.ID)
    if err != nil {
        t.Fatalf("get: %v", err)
    }
    if got.Name != p.Name {
        t.Errorf("name: got %q, want %q", got.Name, p.Name)
    }
    // List
    list, err := repo.List(ctx, tenantID)
    if err != nil {
        t.Fatalf("list: %v", err)
    }
    if len(list) != 1 {
        t.Fatalf("list len: got %d, want 1", len(list))
    }
    // Update
    p.Name = "test-qwen-2"
    if err := repo.Update(ctx, tenantID, p); err != nil {
        t.Fatalf("update: %v", err)
    }
    // Delete
    if err := repo.Delete(ctx, tenantID, p.ID); err != nil {
        t.Fatalf("delete: %v", err)
    }
    _, err = repo.Get(ctx, tenantID, p.ID)
    if err == nil {
        t.Fatal("expected error after delete")
    }
}
```

- [ ] **Step 4: Run tests**

Run: `go test -short -run TestPgProviderRepo_CRUD ./internal/llmgateway/infrastructure/...`
Expected: PASS (may need DB; skip if no test DB configured)

- [ ] **Step 5: Commit**

```bash
git add internal/llmgateway/infrastructure/provider_repo.go \
        internal/llmgateway/infrastructure/model_repo.go \
        internal/llmgateway/infrastructure/provider_repo_test.go \
        internal/llmgateway/infrastructure/model_repo_test.go
git commit -m "[feat](llmgateway): add Provider and Model PostgreSQL repositories"
```

---

### Task 5: ModelRegistry — cache + model resolution

**Files:**

- Create: `internal/llmgateway/infrastructure/model_registry.go`
- Create: `internal/llmgateway/infrastructure/model_registry_test.go`

**Interfaces:**

- Consumes: `port.ModelRepository`, `port.ProviderRepository`, `constants.GatewayCacheTTL`
- Produces:
  - `type ModelRegistry struct { ... }`
  - `func NewModelRegistry(modelRepo, providerRepo, cacheTTL) *ModelRegistry`
  - `func (r *ModelRegistry) Resolve(ctx, tenantID, modelName) (ProviderConfig, ChatProtocol, error)`
  - `func (r *ModelRegistry) ResolveEmbedding(ctx, tenantID, modelName) (ProviderConfig, EmbedProtocol, error)`
  - `func (r *ModelRegistry) ListChatModels(ctx, tenantID) ([]string, error)`
  - `func (r *ModelRegistry) ListEmbeddingModels(ctx, tenantID) ([]string, error)`
  - `func (r *ModelRegistry) WarmTenant(ctx, tenantID) error`
  - `func (r *ModelRegistry) Invalidate(tenantID)`

- [ ] **Step 1: Create `internal/llmgateway/infrastructure/model_registry.go`**

```go
package infrastructure

import (
    "context"
    "fmt"
    "sort"
    "sync"
    "time"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

type resolvedEntry struct {
    config   ProviderConfig
    provider domain.Provider
    expires  time.Time
}

type ModelRegistry struct {
    modelRepo    port.ModelRepository
    providerRepo port.ProviderRepository
    chatProtos   map[domain.ProviderKind]ChatProtocol
    embedProtos  map[domain.ProviderKind]EmbedProtocol
    cacheTTL     time.Duration
    mu           sync.RWMutex
    cache        map[string]map[string]*resolvedEntry // tenantID → modelName → entry
}

func NewModelRegistry(
    modelRepo port.ModelRepository,
    providerRepo port.ProviderRepository,
    chatProtos map[domain.ProviderKind]ChatProtocol,
    embedProtos map[domain.ProviderKind]EmbedProtocol,
    cacheTTL time.Duration,
) *ModelRegistry {
    return &ModelRegistry{
        modelRepo:    modelRepo,
        providerRepo: providerRepo,
        chatProtos:   chatProtos,
        embedProtos:  embedProtos,
        cacheTTL:     cacheTTL,
        cache:        make(map[string]map[string]*resolvedEntry),
    }
}

func (r *ModelRegistry) Resolve(ctx context.Context, tenantID, modelName string) (ProviderConfig, ChatProtocol, error) {
    if e := r.cacheGet(tenantID, modelName); e != nil {
        proto, ok := r.chatProtos[e.provider.Kind]
        if !ok {
            return ProviderConfig{}, nil, fmt.Errorf("model registry: no chat protocol for provider kind %q", e.provider.Kind)
        }
        return e.config, proto, nil
    }
    return r.resolveFromDB(ctx, tenantID, modelName)
}

func (r *ModelRegistry) ResolveEmbedding(ctx context.Context, tenantID, modelName string) (ProviderConfig, EmbedProtocol, error) {
    if e := r.cacheGet(tenantID, modelName); e != nil {
        proto, ok := r.embedProtos[e.provider.Kind]
        if !ok {
            return ProviderConfig{}, nil, fmt.Errorf("model registry: no embed protocol for provider kind %q", e.provider.Kind)
        }
        return e.config, proto, nil
    }
    return r.resolveEmbedFromDB(ctx, tenantID, modelName)
}

func (r *ModelRegistry) resolveFromDB(ctx context.Context, tenantID, modelName string) (ProviderConfig, ChatProtocol, error) {
    enabled := true
    models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{Enabled: &enabled})
    if err != nil {
        return ProviderConfig{}, nil, fmt.Errorf("model registry: list models: %w", err)
    }
    for _, m := range models {
        if m.Name == modelName {
            provider, err := r.providerRepo.Get(ctx, tenantID, m.ProviderID)
            if err != nil {
                return ProviderConfig{}, nil, fmt.Errorf("model registry: get provider: %w", err)
            }
            cfg := ProviderConfig{
                Name:        provider.Name,
                BaseURL:     provider.BaseURL,
                APIKey:      provider.APIKey,
                HealthModel: provider.DefaultModel,
                Models:      []string{m.Name},
            }
            r.cacheSet(tenantID, modelName, cfg, *provider)
            proto, ok := r.chatProtos[provider.Kind]
            if !ok {
                return ProviderConfig{}, nil, fmt.Errorf("model registry: no chat protocol for %q", provider.Kind)
            }
            return cfg, proto, nil
        }
    }
    return ProviderConfig{}, nil, fmt.Errorf("model registry: model %q not found for tenant %s", modelName, tenantID)
}

func (r *ModelRegistry) resolveEmbedFromDB(ctx context.Context, tenantID, modelName string) (ProviderConfig, EmbedProtocol, error) {
    // Similar to resolveFromDB but returns EmbedProtocol
    enabled := true
    models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{Enabled: &enabled})
    if err != nil {
        return ProviderConfig{}, nil, fmt.Errorf("model registry: list models: %w", err)
    }
    for _, m := range models {
        if m.Name == modelName {
            provider, err := r.providerRepo.Get(ctx, tenantID, m.ProviderID)
            if err != nil {
                return ProviderConfig{}, nil, fmt.Errorf("model registry: get provider: %w", err)
            }
            cfg := ProviderConfig{
                Name:        provider.Name,
                BaseURL:     provider.BaseURL,
                APIKey:      provider.APIKey,
                HealthModel: provider.DefaultModel,
                Models:      []string{m.Name},
            }
            r.cacheSet(tenantID, modelName, cfg, *provider)
            proto, ok := r.embedProtos[provider.Kind]
            if !ok {
                return ProviderConfig{}, nil, fmt.Errorf("model registry: no embed protocol for %q", provider.Kind)
            }
            return cfg, proto, nil
        }
    }
    return ProviderConfig{}, nil, fmt.Errorf("model registry: embedding model %q not found", modelName)
}

func (r *ModelRegistry) ListChatModels(ctx context.Context, tenantID string) ([]string, error) {
    enabled := true
    models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{
        Enabled:    &enabled,
        Capability: domain.CapChat,
    })
    if err != nil {
        return nil, err
    }
    names := make([]string, 0, len(models))
    for _, m := range models {
        names = append(names, m.Name)
    }
    sort.Strings(names)
    return names, nil
}

func (r *ModelRegistry) ListEmbeddingModels(ctx context.Context, tenantID string) ([]string, error) {
    enabled := true
    models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{
        Enabled:    &enabled,
        Capability: domain.CapEmbedding,
    })
    if err != nil {
        return nil, err
    }
    names := make([]string, 0, len(models))
    for _, m := range models {
        names = append(names, m.Name)
    }
    sort.Strings(names)
    return names, nil
}

func (r *ModelRegistry) WarmTenant(ctx context.Context, tenantID string) error {
    enabled := true
    _, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{Enabled: &enabled})
    return err
}

func (r *ModelRegistry) Invalidate(tenantID string) {
    r.mu.Lock()
    delete(r.cache, tenantID)
    r.mu.Unlock()
}

func (r *ModelRegistry) cacheGet(tenantID, modelName string) *resolvedEntry {
    r.mu.RLock()
    defer r.mu.RUnlock()
    tenantCache, ok := r.cache[tenantID]
    if !ok {
        return nil
    }
    e, ok := tenantCache[modelName]
    if !ok || time.Now().After(e.expires) {
        return nil
    }
    return e
}

func (r *ModelRegistry) cacheSet(tenantID, modelName string, cfg ProviderConfig, provider domain.Provider) {
    r.mu.Lock()
    defer r.mu.Unlock()
    if r.cache[tenantID] == nil {
        r.cache[tenantID] = make(map[string]*resolvedEntry)
    }
    r.cache[tenantID][modelName] = &resolvedEntry{
        config:   cfg,
        provider: provider,
        expires:  time.Now().Add(r.cacheTTL),
    }
}
```

- [ ] **Step 2: Write unit test with mock repos**

```go
// internal/llmgateway/infrastructure/model_registry_test.go
package infrastructure_test

import (
    "context"
    "testing"
    "time"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
    "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

type mockModelRepo struct {
    models []domain.Model
}

func (m *mockModelRepo) List(ctx context.Context, tenantID string, filter port.ModelFilter) ([]domain.Model, error) {
    return m.models, nil
}
// ... (implement remaining mock methods)

func TestModelRegistry_Resolve(t *testing.T) {
    // Setup mock repos, create registry, test resolution
}
```

- [ ] **Step 3: Run tests**

Run: `go test -short -run TestModelRegistry ./internal/llmgateway/infrastructure/...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/llmgateway/infrastructure/model_registry.go \
        internal/llmgateway/infrastructure/model_registry_test.go
git commit -m "[feat](llmgateway): add ModelRegistry with LRU cache and model resolution"
```

---

### Task 6: Gateway refactor — delegate to ModelRegistry

**Files:**

- Modify: `internal/llmgateway/infrastructure/gateway.go`

**Interfaces:**

- Consumes: `ModelRegistry` from Task 5
- Produces: Gateway.Complete/CompleteStream/CreateEmbeddings → delegate to registry + protocol

- [ ] **Step 1: Refactor Gateway struct**

Replace `clients map[ModelProvider]LLMClient` and `embeddingClients map[ModelProvider]EmbeddingClient` with `registry *ModelRegistry` + `chatProtos map[domain.ProviderKind]ChatProtocol` + `embedProtos map[domain.ProviderKind]EmbedProtocol`.

```go
type Gateway struct {
    registry     *ModelRegistry
    chatProtos   map[domain.ProviderKind]ChatProtocol
    embedProtos  map[domain.ProviderKind]EmbedProtocol
    metrics      observability.MetricsProvider
    logger       *zap.Logger
}

func NewGateway(registry *ModelRegistry, chatProtos map[domain.ProviderKind]ChatProtocol, embedProtos map[domain.ProviderKind]EmbedProtocol) *Gateway {
    return &Gateway{
        registry:    registry,
        chatProtos:  chatProtos,
        embedProtos: embedProtos,
        metrics:     observability.NoopMetrics{},
        logger:      zap.NewNop(),
    }
}
```

- [ ] **Step 2: Rewrite Complete()**

```go
func (g *Gateway) Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error) {
    tenantID := reqctx.TenantIDFromContext(ctx)
    cfg, proto, err := g.registry.Resolve(ctx, tenantID, req.Model)
    if err != nil {
        g.metrics.IncLLMRequest(req.Model, "unknown", llmStatusError)
        return nil, fmt.Errorf("llmgateway: resolve model %q: %w", req.Model, err)
    }
    // Logging + metrics (same as before, provider name from cfg.Name)
    traceID := reqctx.TraceIDFromContext(ctx)
    g.logger.Info("llm.request",
        zap.String("trace_id", traceID),
        zap.String("tenant_id", tenantID),
        zap.String("model", req.Model),
        zap.String("provider", cfg.Name),
        zap.Int("tool_count", len(req.Tools)),
    )
    start := time.Now()
    resp, err := proto.Complete(ctx, cfg, req)
    elapsed := time.Since(start).Seconds()
    // ... same metrics/logging as before, using cfg.Name for provider label
    return resp, err
}
```

- [ ] **Step 3: Rewrite CompleteStream()**

Same pattern — resolve via registry, delegate to `proto.CompleteStream(ctx, cfg, req, onToken)`.

- [ ] **Step 4: Rewrite CreateEmbeddings()**

```go
func (g *Gateway) CreateEmbeddings(ctx context.Context, req *EmbeddingRequest) (*EmbeddingResponse, error) {
    tenantID := reqctx.TenantIDFromContext(ctx)
    cfg, proto, err := g.registry.ResolveEmbedding(ctx, tenantID, req.Model)
    if err != nil {
        return nil, fmt.Errorf("llmgateway: resolve embedding model %q: %w", req.Model, err)
    }
    return proto.CreateEmbeddings(ctx, cfg, req)
}
```

- [ ] **Step 5: Rewrite ListChatModels/ListEmbeddingModels**

Delegate to `g.registry.ListChatModels(ctx, tenantID)` / `g.registry.ListEmbeddingModels(ctx, tenantID)`.

- [ ] **Step 6: Remove dead code**

Delete: `ModelProvider`, `ProviderQwen`/`ProviderZhipu` constants, `LLMClient`/`StreamingLLMClient`/`EmbeddingClient` interfaces, `parseProvider()`, `RegisterClient`, `RegisterEmbeddingClient`, `SetDefault`, `DefaultEmbeddingModel`, `HasEmbeddingClient`, `BatchSize` (move to protocol).

- [ ] **Step 7: Update all callers**

Search `grep -r "RegisterClient\|ModelProvider\|ProviderQwen\|ProviderZhipu\|parseProvider\|SetDefault" --include="*.go"` and update all references.

- [ ] **Step 8: Run tests**

Run: `go test -short ./internal/llmgateway/...`
Expected: PASS (fix compilation errors found in Step 7)

- [ ] **Step 9: Commit**

```bash
git add internal/llmgateway/infrastructure/gateway.go
git commit -m "[refactor](llmgateway): delegate Gateway to ModelRegistry, remove hardcoded provider routing"
```

---

### Task 7: Wiring layer update — Container, LLMGateway, tenantResolver

**Files:**

- Modify: `api/wiring/llmgateway.go`
- Modify: `api/wiring/tenant_resolver.go`
- Modify: `api/wiring/wiring.go`

**Interfaces:**

- Consumes: `ModelRegistry`, `PgProviderRepo`, `PgModelRepo`, protocol singletons
- Produces: Updated `Container.LLMGateway`, simplified `tenantCapabilityResolver`

- [ ] **Step 1: Update `api/wiring/llmgateway.go`**

```go
func (c *Container) buildLLMGateway(_ context.Context) error {
    metrics := observability.NewPrometheusMetrics(c.Logger)

    // Protocol singletons
    openAICompatProto := &llmgateway.OpenAICompatProtocol{}
    chatProtos := map[domain.ProviderKind]llmgateway.ChatProtocol{
        domain.ProviderOpenAICompat: openAICompatProto,
    }
    embedProtos := map[domain.ProviderKind]llmgateway.EmbedProtocol{
        domain.ProviderOpenAICompat: openAICompatProto,
    }

    modelRepo := llmgateway.NewPgModelRepo(c.DB())
    providerRepo := llmgateway.NewPgProviderRepo(c.DB())
    registry := llmgateway.NewModelRegistry(
        modelRepo, providerRepo,
        chatProtos, embedProtos,
        constants.GatewayCacheTTL,
    )
    gw := llmgateway.NewGateway(registry, chatProtos, embedProtos).
        WithLogger(c.Logger).WithMetrics(metrics)

    c.LLMGateway = &LLMGateway{
        Gateway:      gw,
        Metrics:      metrics,
        ModelService: llmapp.NewModelService(registry),
    }
    return nil
}
```

- [ ] **Step 2: Simplify `api/wiring/tenant_resolver.go`**

Remove `loadGateway()` (~100 lines). Replace `resolveGatewayResult()` with registry delegation:

```go
func (r *tenantCapabilityResolver) resolveGatewayResult(ctx context.Context, tenantID string, strict bool) (*llmgateway.Gateway, map[string]string, bool, error) {
    if r.registry == nil {
        return r.fallback, nil, r.fallback != nil, nil
    }
    if err := r.registry.WarmTenant(ctx, tenantID); err != nil {
        if strict {
            return nil, nil, false, fmt.Errorf("tenant llm: warm: %w", err)
        }
        return r.fallback, nil, r.fallback != nil, nil
    }
    return r.gateway, nil, true, nil
}
```

Update `newTenantCapabilityResolver` signature to accept `*ModelRegistry` instead of cache/fallback.

- [ ] **Step 3: Update `api/wiring/wiring.go` build calls**

Wire the `ModelRegistry` into `tenantCapabilityResolver` during container build.

- [ ] **Step 4: Run vet + build**

Run: `go vet ./api/wiring/... && go build ./api/wiring/...`
Expected: ok

- [ ] **Step 5: Commit**

```bash
git add api/wiring/llmgateway.go api/wiring/tenant_resolver.go api/wiring/wiring.go
git commit -m "[refactor](wiring): wire ModelRegistry into Container and tenant resolver"
```

---

### Task 8: Application services — ProviderService & ModelService

**Files:**

- Create: `internal/llmgateway/application/provider_service.go`
- Create: `internal/llmgateway/application/model_mgmt_service.go`
- Modify: `internal/llmgateway/application/model_service.go`
- Create: `internal/llmgateway/application/provider_service_test.go`

**Interfaces:**

- Consumes: `port.ProviderRepository`, `port.ModelRepository`, protocol maps
- Produces:
  - `type ProviderService struct { ... }`
  - `func (s *ProviderService) Create(ctx, tenantID, input) (*Provider, error)`
  - `func (s *ProviderService) List(ctx, tenantID) ([]Provider, error)`
  - `func (s *ProviderService) Update(ctx, tenantID, input) (*Provider, error)`
  - `func (s *ProviderService) Delete(ctx, tenantID, id) error`
  - `func (s *ProviderService) DiscoverModels(ctx, tenantID, providerID) ([]Model, error)`
  - `func (s *ProviderService) HealthCheck(ctx, tenantID, providerID) error`
  - `type ModelMgmtService struct { ... }`
  - `func (s *ModelMgmtService) List(ctx, tenantID, filter) ([]Model, error)`
  - `func (s *ModelMgmtService) Get(ctx, tenantID, id) (*Model, error)`
  - `func (s *ModelMgmtService) Update(ctx, tenantID, input) (*Model, error)`
  - `func (s *ModelMgmtService) Toggle(ctx, tenantID, id, enabled) error`
  - `func (s *ModelMgmtService) Delete(ctx, tenantID, id) error`

- [ ] **Step 1: Create `internal/llmgateway/application/provider_service.go`**

```go
package application

import (
    "context"
    "fmt"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
    "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
)

type ProviderService struct {
    repo        port.ProviderRepository
    modelRepo   port.ModelRepository
    chatProtos  map[domain.ProviderKind]infrastructure.ChatProtocol
}

func NewProviderService(repo port.ProviderRepository, modelRepo port.ModelRepository, chatProtos map[domain.ProviderKind]infrastructure.ChatProtocol) *ProviderService {
    return &ProviderService{repo: repo, modelRepo: modelRepo, chatProtos: chatProtos}
}

func (s *ProviderService) Create(ctx context.Context, tenantID string, input CreateProviderInput) (*domain.Provider, error) {
    p := &domain.Provider{
        ID:       genULID(),
        TenantID: tenantID,
        Name:     input.Name,
        Kind:     input.Kind,
        BaseURL:  input.BaseURL,
        APIKey:   input.APIKey,
        Enabled:  true,
    }
    if err := s.repo.Create(ctx, tenantID, p); err != nil {
        return nil, fmt.Errorf("provider service: create: %w", err)
    }
    // Auto-discover models
    if _, err := s.DiscoverModels(ctx, tenantID, p.ID); err != nil {
        // Log but don't fail — discovery is best-effort
    }
    return p, nil
}

func (s *ProviderService) DiscoverModels(ctx context.Context, tenantID, providerID string) ([]domain.Model, error) {
    provider, err := s.repo.Get(ctx, tenantID, providerID)
    if err != nil {
        return nil, fmt.Errorf("discover models: %w", err)
    }
    proto, ok := s.chatProtos[provider.Kind]
    if !ok {
        return nil, fmt.Errorf("discover models: no protocol for kind %q", provider.Kind)
    }
    cfg := infrastructure.ProviderConfig{
        Name:    provider.Name,
        BaseURL: provider.BaseURL,
        APIKey:  provider.APIKey,
    }
    names, err := proto.ListModels(ctx, cfg)
    if err != nil {
        return nil, fmt.Errorf("discover models: list from provider: %w", err)
    }
    models := make([]domain.Model, 0, len(names))
    for _, name := range names {
        models = append(models, domain.Model{
            TenantID:        tenantID,
            ProviderID:      providerID,
            Name:            name,
            DisplayName:     name,
            Capabilities:    []domain.ModelCapability{domain.CapChat},
            ProviderManaged: true,
            Enabled:         true,
        })
    }
    return s.modelRepo.UpsertDiscovered(ctx, tenantID, providerID, models)
}

func (s *ProviderService) HealthCheck(ctx context.Context, tenantID, providerID string) error {
    provider, err := s.repo.Get(ctx, tenantID, providerID)
    if err != nil {
        return err
    }
    proto, ok := s.chatProtos[provider.Kind]
    if !ok {
        return fmt.Errorf("no protocol for kind %q", provider.Kind)
    }
    cfg := infrastructure.ProviderConfig{
        Name:        provider.Name,
        BaseURL:     provider.BaseURL,
        APIKey:      provider.APIKey,
        HealthModel: provider.DefaultModel,
    }
    return proto.Health(ctx, cfg)
}

// List, Update, Delete methods similarly delegate to repo

type CreateProviderInput struct {
    Name    string              `json:"name"`
    Kind    domain.ProviderKind `json:"kind"`
    BaseURL string              `json:"baseUrl"`
    APIKey  string              `json:"apiKey"`
}

func genULID() string {
    // use ulid.Make() or similar
    return ""
}
```

- [ ] **Step 2: Create `internal/llmgateway/application/model_mgmt_service.go`**

```go
package application

import (
    "context"
    "fmt"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

type ModelMgmtService struct {
    repo port.ModelRepository
}

func NewModelMgmtService(repo port.ModelRepository) *ModelMgmtService {
    return &ModelMgmtService{repo: repo}
}

func (s *ModelMgmtService) List(ctx context.Context, tenantID string, filter port.ModelFilter) ([]domain.Model, error) {
    return s.repo.List(ctx, tenantID, filter)
}

func (s *ModelMgmtService) Get(ctx context.Context, tenantID, id string) (*domain.Model, error) {
    return s.repo.Get(ctx, tenantID, id)
}

func (s *ModelMgmtService) Update(ctx context.Context, tenantID string, input UpdateModelInput) (*domain.Model, error) {
    m, err := s.repo.Get(ctx, tenantID, input.ID)
    if err != nil {
        return nil, fmt.Errorf("model mgmt: %w", err)
    }
    m.DisplayName = input.DisplayName
    m.Capabilities = input.Capabilities
    m.ContextWindow = input.ContextWindow
    m.MaxTokens = input.MaxTokens
    m.InputPrice = input.InputPrice
    m.OutputPrice = input.OutputPrice
    m.Recommended = input.Recommended
    if err := s.repo.Update(ctx, tenantID, m); err != nil {
        return nil, fmt.Errorf("model mgmt: update: %w", err)
    }
    return m, nil
}

func (s *ModelMgmtService) Toggle(ctx context.Context, tenantID, id string, enabled bool) error {
    return s.repo.Toggle(ctx, tenantID, id, enabled)
}

func (s *ModelMgmtService) Delete(ctx context.Context, tenantID, id string) error {
    return s.repo.Delete(ctx, tenantID, id)
}

type UpdateModelInput struct {
    ID            string                  `json:"id"`
    DisplayName   string                  `json:"displayName"`
    Capabilities  []domain.ModelCapability `json:"capabilities"`
    ContextWindow int                     `json:"contextWindow"`
    MaxTokens     int                     `json:"maxTokens"`
    InputPrice    float64                 `json:"inputPrice"`
    OutputPrice   float64                 `json:"outputPrice"`
    Recommended   bool                    `json:"recommended"`
}
```

- [ ] **Step 3: Update `internal/llmgateway/application/model_service.go`**

Extend `ModelService` to accept `*ModelRegistry` and add tenant-aware methods:

```go
func (s *ModelService) Catalogue(ctx context.Context, tenantID string) (chat, embedding []string) {
    chat, _ = s.registry.ListChatModels(ctx, tenantID)
    embedding, _ = s.registry.ListEmbeddingModels(ctx, tenantID)
    // Fallback to non-nil
    if chat == nil { chat = []string{} }
    if embedding == nil { embedding = []string{} }
    return
}
```

- [ ] **Step 4: Run build**

Run: `go build ./internal/llmgateway/application/...`
Expected: ok

- [ ] **Step 5: Commit**

```bash
git add internal/llmgateway/application/
git commit -m "[feat](llmgateway): add ProviderService and ModelMgmtService application layer"
```

---

### Task 9: HTTP handlers & routes

**Files:**

- Create: `api/http/handler/provider_handler.go`
- Create: `api/http/handler/model_mgmt_handler.go`
- Modify: `api/http/handler/model_handler.go`
- Modify: `api/http/router.go`

- [ ] **Step 1: Create `api/http/handler/provider_handler.go`**

```go
package handler

import (
    "net/http"
    "github.com/gin-gonic/gin"
    llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

type ProviderHandler struct {
    svc *llmapp.ProviderService
}

func NewProviderHandler(svc *llmapp.ProviderService) *ProviderHandler {
    return &ProviderHandler{svc: svc}
}

func (h *ProviderHandler) List(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok { respondMissingTenant(c); return }
    providers, err := h.svc.List(c.Request.Context(), tenantID)
    if err != nil { _ = c.Error(err); return }
    c.JSON(http.StatusOK, gin.H{"providers": providers})
}

func (h *ProviderHandler) Create(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok { respondMissingTenant(c); return }
    var input llmapp.CreateProviderInput
    if err := c.ShouldBindJSON(&input); err != nil {
        _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
        return
    }
    provider, err := h.svc.Create(c.Request.Context(), tenantID, input)
    if err != nil { _ = c.Error(err); return }
    c.JSON(http.StatusCreated, provider)
}

func (h *ProviderHandler) Update(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok { respondMissingTenant(c); return }
    // bind + delegate to svc.Update
}

func (h *ProviderHandler) Delete(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok { respondMissingTenant(c); return }
    if err := h.svc.Delete(c.Request.Context(), tenantID, c.Param("id")); err != nil {
        _ = c.Error(err); return
    }
    c.JSON(http.StatusOK, gin.H{"message": "已删除"})
}

func (h *ProviderHandler) Discover(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok { respondMissingTenant(c); return }
    models, err := h.svc.DiscoverModels(c.Request.Context(), tenantID, c.Param("id"))
    if err != nil { _ = c.Error(err); return }
    c.JSON(http.StatusOK, gin.H{"models": models, "count": len(models)})
}

func (h *ProviderHandler) HealthCheck(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok { respondMissingTenant(c); return }
    if err := h.svc.HealthCheck(c.Request.Context(), tenantID, c.Param("id")); err != nil {
        _ = c.Error(err); return
    }
    c.JSON(http.StatusOK, gin.H{"status": "healthy"})
}
```

- [ ] **Step 2: Create `api/http/handler/model_mgmt_handler.go`**

```go
package handler

import (
    "net/http"
    "github.com/gin-gonic/gin"
    llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
    "github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

type ModelMgmtHandler struct {
    svc *llmapp.ModelMgmtService
}

func NewModelMgmtHandler(svc *llmapp.ModelMgmtService) *ModelMgmtHandler {
    return &ModelMgmtHandler{svc: svc}
}

func (h *ModelMgmtHandler) List(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok { respondMissingTenant(c); return }
    var filter port.ModelFilter
    if cap := c.Query("capability"); cap != "" {
        filter.Capability = domain.ModelCapability(cap)
    }
    if pid := c.Query("providerId"); pid != "" {
        filter.ProviderID = pid
    }
    models, err := h.svc.List(c.Request.Context(), tenantID, filter)
    if err != nil { _ = c.Error(err); return }
    c.JSON(http.StatusOK, gin.H{"models": models})
}

func (h *ModelMgmtHandler) Get(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok { respondMissingTenant(c); return }
    m, err := h.svc.Get(c.Request.Context(), tenantID, c.Param("id"))
    if err != nil { _ = c.Error(err); return }
    c.JSON(http.StatusOK, m)
}

func (h *ModelMgmtHandler) Update(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok { respondMissingTenant(c); return }
    var input llmapp.UpdateModelInput
    if err := c.ShouldBindJSON(&input); err != nil {
        _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
        return
    }
    input.ID = c.Param("id")
    m, err := h.svc.Update(c.Request.Context(), tenantID, input)
    if err != nil { _ = c.Error(err); return }
    c.JSON(http.StatusOK, m)
}

func (h *ModelMgmtHandler) Toggle(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok { respondMissingTenant(c); return }
    var req struct{ Enabled bool `json:"enabled"` }
    if err := c.ShouldBindJSON(&req); err != nil {
        _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
        return
    }
    if err := h.svc.Toggle(c.Request.Context(), tenantID, c.Param("id"), req.Enabled); err != nil {
        _ = c.Error(err); return
    }
    c.JSON(http.StatusOK, gin.H{"message": "已更新"})
}

func (h *ModelMgmtHandler) Delete(c *gin.Context) {
    tenantID, ok := tenantIDFromCtx(c)
    if !ok { respondMissingTenant(c); return }
    if err := h.svc.Delete(c.Request.Context(), tenantID, c.Param("id")); err != nil {
        _ = c.Error(err); return
    }
    c.JSON(http.StatusOK, gin.H{"message": "已删除"})
}
```

- [ ] **Step 3: Register routes in `api/http/router.go`**

Add `registerProviders` and `registerModels` functions, call from `NewRouter`:

```go
func registerProviders(r *gin.Engine, c *wiring.Container, requireActive gin.HandlerFunc) {
    if c.LLMGateway == nil || c.LLMGateway.ProviderService == nil {
        return
    }
    h := handler.NewProviderHandler(c.LLMGateway.ProviderService)
    admin := middleware.RequireTenantRole("admin")
    g := r.Group("/providers", protectedTenantMiddleware(c, admin)...)
    g.GET("", h.List)
    g.POST("", requireActive, h.Create)
    g.PUT("/:id", requireActive, h.Update)
    g.DELETE("/:id", requireActive, h.Delete)
    g.POST("/:id/discover", requireActive, h.Discover)
    g.POST("/:id/health", requireActive, h.HealthCheck)
}

func registerModels(r *gin.Engine, c *wiring.Container) {
    if c.LLMGateway == nil || c.LLMGateway.ModelMgmtService == nil {
        return
    }
    h := handler.NewModelMgmtHandler(c.LLMGateway.ModelMgmtService)
    admin := middleware.RequireTenantRole("admin")
    g := r.Group("/models", protectedTenantMiddleware(c, admin)...)
    g.GET("", h.List)
    g.GET("/:id", h.Get)
    g.PUT("/:id", h.Update)
    g.PATCH("/:id/toggle", h.Toggle)
    g.DELETE("/:id", h.Delete)
}
```

NOTE: `GET /models` (unauthenticated, health group) stays — update `ModelHandler.ListModels` to accept tenantID from context. The admin-only `GET /models` (with tenant middleware) is for the full model management list with metadata. Need to differentiate paths — use `/models` (public, names only) vs `/models/manage` (admin, full metadata). Or rename: model catalog route stays `/models`, management routes under `/admin/models`.

Actually, per the API design in the spec, the public `GET /models` stays for chat model names. Move management routes to a separate group. Let's use:

- Public: `GET /models` → keep in `registerHealth` (no auth needed, returns string[])
- Admin: `GET /admin/models` for full metadata list (or scope under `/models/manage`)

- [ ] **Step 4: Commit**

```bash
git add api/http/handler/provider_handler.go api/http/handler/model_mgmt_handler.go \
        api/http/handler/model_handler.go api/http/router.go
git commit -m "[feat](api): add provider and model management HTTP handlers and routes"
```

---

### Task 10: Frontend — LLM module

**Files:**

- Create: `web/src/modules/llm/model/llm.ts`
- Create: `web/src/modules/llm/api/llm.api.ts`
- Create: `web/src/modules/llm/hooks/useProviders.ts`
- Create: `web/src/modules/llm/hooks/useModels.ts`
- Create: `web/src/modules/llm/pages/ProviderListPage.tsx`
- Create: `web/src/modules/llm/pages/ModelListPage.tsx`
- Create: `web/src/modules/llm/components/ProviderForm.tsx`
- Create: `web/src/modules/llm/components/ModelEditDrawer.tsx`
- Create: `web/src/modules/llm/components/ModelCapabilityTags.tsx`
- Create: `web/src/modules/llm/components/DiscoverResultModal.tsx`
- Modify: `web/src/App.tsx` (add route)
- Modify: `web/src/constants/index.ts` (add LLM constants)

- [ ] **Step 1: Create Zod schemas and types**

```typescript
// web/src/modules/llm/model/llm.ts
import { z } from 'zod';

export const providerKindSchema = z.enum(['openai_compat', 'anthropic', 'ollama']);
export type ProviderKind = z.infer<typeof providerKindSchema>;

export const providerSchema = z.object({
  id: z.string(),
  name: z.string(),
  kind: providerKindSchema,
  baseUrl: z.string(),
  defaultModel: z.string(),
  enabled: z.boolean(),
  createdAt: z.string(),
  updatedAt: z.string(),
});
export type Provider = z.infer<typeof providerSchema>;

export const modelCapabilitySchema = z.enum(['chat', 'embedding', 'vision', 'tool_use', 'reasoning']);
export type ModelCapability = z.infer<typeof modelCapabilitySchema>;

export const modelSchema = z.object({
  id: z.string(),
  tenantId: z.string(),
  providerId: z.string(),
  name: z.string(),
  displayName: z.string(),
  capabilities: z.array(modelCapabilitySchema),
  contextWindow: z.number(),
  maxTokens: z.number(),
  inputPrice: z.number(),
  outputPrice: z.number(),
  recommended: z.boolean(),
  enabled: z.boolean(),
  providerManaged: z.boolean(),
  createdAt: z.string(),
  updatedAt: z.string(),
});
export type Model = z.infer<typeof modelSchema>;

export type CreateProviderInput = {
  name: string;
  kind: ProviderKind;
  baseUrl: string;
  apiKey: string;
};

export type UpdateModelInput = {
  displayName: string;
  capabilities: ModelCapability[];
  contextWindow: number;
  maxTokens: number;
  inputPrice: number;
  outputPrice: number;
  recommended: boolean;
};
```

- [ ] **Step 2: Create API client**

```typescript
// web/src/modules/llm/api/llm.api.ts
import { z } from 'zod';
import { providerSchema, modelSchema, type CreateProviderInput, type UpdateModelInput, type Provider, type Model } from '../model/llm';
import api from '@/services/client';

export const llmApi = {
  // Providers
  listProviders: async (): Promise<Provider[]> => {
    const res = await api.get('/providers');
    return z.array(providerSchema).parse(res.data?.providers ?? []);
  },
  createProvider: (data: CreateProviderInput) => api.post('/providers', data),
  updateProvider: (id: string, data: Partial<CreateProviderInput>) => api.put(`/providers/${id}`, data),
  deleteProvider: (id: string) => api.delete(`/providers/${id}`),
  discoverModels: (id: string) => api.post(`/providers/${id}/discover`),
  healthCheck: (id: string) => api.post(`/providers/${id}/health`),

  // Models
  listModels: async (): Promise<Model[]> => {
    const res = await api.get('/models/manage');
    return z.array(modelSchema).parse(res.data?.models ?? []);
  },
  getModel: async (id: string): Promise<Model> => {
    const res = await api.get(`/models/${id}`);
    return modelSchema.parse(res.data);
  },
  updateModel: (id: string, data: UpdateModelInput) => api.put(`/models/${id}`, data),
  toggleModel: (id: string, enabled: boolean) => api.patch(`/models/${id}/toggle`, { enabled }),
  deleteModel: (id: string) => api.delete(`/models/${id}`),
};
```

- [ ] **Step 3: Create hooks**

```typescript
// web/src/modules/llm/hooks/useProviders.ts
import { useState, useEffect, useCallback } from 'react';
import { message } from 'antd';
import { llmApi } from '../api/llm.api';
import type { Provider } from '../model/llm';

export function useProviders() {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(false);

  const fetch = useCallback(async () => {
    setLoading(true);
    try {
      const data = await llmApi.listProviders();
      setProviders(data);
    } catch (err: any) {
      message.error({ content: err.response?.data?.error || '加载厂商列表失败', duration: 0 });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { fetch(); }, [fetch]);

  return { providers, loading, refresh: fetch };
}
```

```typescript
// web/src/modules/llm/hooks/useModels.ts
import { useState, useEffect, useCallback } from 'react';
import { message } from 'antd';
import { llmApi } from '../api/llm.api';
import type { Model } from '../model/llm';

export function useModels() {
  const [models, setModels] = useState<Model[]>([]);
  const [loading, setLoading] = useState(false);

  const fetch = useCallback(async () => {
    setLoading(true);
    try {
      const data = await llmApi.listModels();
      setModels(data);
    } catch (err: any) {
      message.error({ content: err.response?.data?.error || '加载模型列表失败', duration: 0 });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { fetch(); }, [fetch]);

  return { models, loading, refresh: fetch };
}
```

- [ ] **Step 4: Create ModelCapabilityTags component**

```tsx
// web/src/modules/llm/components/ModelCapabilityTags.tsx
import { Tag } from 'antd';
import type { ModelCapability } from '../model/llm';

const CAP_COLORS: Record<ModelCapability, string> = {
  chat: 'blue', embedding: 'green', vision: 'purple',
  tool_use: 'orange', reasoning: 'red',
};

const CAP_LABELS: Record<ModelCapability, string> = {
  chat: '对话', embedding: '嵌入', vision: '视觉',
  tool_use: '工具调用', reasoning: '推理',
};

export function ModelCapabilityTags({ capabilities }: { capabilities: ModelCapability[] }) {
  return <>
    {capabilities.map(cap => (
      <Tag key={cap} color={CAP_COLORS[cap]}>{CAP_LABELS[cap]}</Tag>
    ))}
  </>;
}
```

- [ ] **Step 5: Create ProviderForm component**

```tsx
// web/src/modules/llm/components/ProviderForm.tsx
import { Form, Input, Select, Modal } from 'antd';
import type { CreateProviderInput, ProviderKind } from '../model/llm';

const KIND_OPTIONS: { label: string; value: ProviderKind }[] = [
  { label: 'OpenAI 兼容', value: 'openai_compat' },
  { label: 'Anthropic', value: 'anthropic' },
  { label: 'Ollama', value: 'ollama' },
];

interface Props {
  open: boolean;
  onCancel: () => void;
  onSubmit: (values: CreateProviderInput) => Promise<void>;
  loading?: boolean;
}

export function ProviderForm({ open, onCancel, onSubmit, loading }: Props) {
  const [form] = Form.useForm<CreateProviderInput>();

  return (
    <Modal
      title="添加厂商"
      open={open}
      onCancel={onCancel}
      onOk={() => form.submit()}
      confirmLoading={loading}
      destroyOnClose
    >
      <Form form={form} layout="vertical" onFinish={onSubmit}>
        <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入厂商名称' }]}>
          <Input placeholder="例如：我的千问" />
        </Form.Item>
        <Form.Item name="kind" label="类型" rules={[{ required: true }]}>
          <Select options={KIND_OPTIONS} />
        </Form.Item>
        <Form.Item name="baseUrl" label="Base URL" rules={[{ required: true }]}>
          <Input placeholder="https://api.example.com/v1" />
        </Form.Item>
        <Form.Item name="apiKey" label="API Key" rules={[{ required: true }]}>
          <Input.Password placeholder="sk-..." />
        </Form.Item>
      </Form>
    </Modal>
  );
}
```

- [ ] **Step 6: Create ModelEditDrawer component**

```tsx
// web/src/modules/llm/components/ModelEditDrawer.tsx
import { Drawer, Form, Input, InputNumber, Switch, Select } from 'antd';
import { ModelCapabilityTags } from './ModelCapabilityTags';
import type { Model, ModelCapability, UpdateModelInput } from '../model/llm';

interface Props {
  open: boolean;
  model: Model | null;
  onClose: () => void;
  onSubmit: (id: string, values: UpdateModelInput) => Promise<void>;
  loading?: boolean;
}

export function ModelEditDrawer({ open, model, onClose, onSubmit, loading }: Props) {
  const [form] = Form.useForm<UpdateModelInput>();

  // reset form when model changes
  // ... (form.resetFields with model values)

  return (
    <Drawer title="编辑模型" open={open} onClose={onClose} width={480}>
      <Form form={form} layout="vertical" onFinish={(v) => model && onSubmit(model.id, v)}>
        <Form.Item name="displayName" label="显示名称">
          <Input />
        </Form.Item>
        <Form.Item name="capabilities" label="能力标签">
          <Select mode="multiple" options={[
            { label: '对话', value: 'chat' }, { label: '嵌入', value: 'embedding' },
            { label: '视觉', value: 'vision' }, { label: '工具调用', value: 'tool_use' },
            { label: '推理', value: 'reasoning' },
          ]} />
        </Form.Item>
        <Form.Item name="contextWindow" label="上下文窗口 (tokens)">
          <InputNumber min={0} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item name="maxTokens" label="最大输出 (tokens)">
          <InputNumber min={0} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item name="inputPrice" label="输入价格 ($/1M tokens)">
          <InputNumber min={0} step={0.01} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item name="outputPrice" label="输出价格 ($/1M tokens)">
          <InputNumber min={0} step={0.01} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item name="recommended" label="推荐" valuePropName="checked">
          <Switch />
        </Form.Item>
      </Form>
    </Drawer>
  );
}
```

- [ ] **Step 7: Create pages**

`ProviderListPage.tsx` — Table with columns: name, kind badge, enabled switch, model count, actions (discover, health, edit, delete with Modal.confirm). Add button opens `ProviderForm` modal.

`ModelListPage.tsx` — Table with columns: displayName, name (API), provider name, capabilities (tag group), contextWindow, enabled switch, actions (edit drawer, delete). Filter by capability. Edit opens `ModelEditDrawer`.

- [ ] **Step 8: Add route in App.tsx**

```tsx
// Add import and route:
<Route path="/models" element={<ModelManagementPage />} />
```

`ModelManagementPage` renders Tabs with "厂商管理" and "模型目录".

- [ ] **Step 9: Add navigation entry**

Add "模型管理" tab in the sidebar/nav.

- [ ] **Step 10: Build frontend**

Run: `cd web && npx tsc --noEmit`
Expected: no type errors

- [ ] **Step 11: Commit**

```bash
git add web/src/modules/llm/ web/src/App.tsx web/src/constants/index.ts
git commit -m "[feat](frontend): add LLM model management pages"
```

---

### Task 11: Cleanup — remove deprecated code

**Files:**

- Delete or modify: `internal/llmgateway/infrastructure/static_catalog.go`
- Modify: `internal/llmgateway/domain/llmgateway.go` (remove old ProviderKind if unused)
- Modify: `internal/llmgateway/infrastructure/tenant_cache.go` (update to use ModelRegistry)
- Search and update all references to removed symbols

- [ ] **Step 1: Delete StaticModelCatalog**

Remove `internal/llmgateway/infrastructure/static_catalog.go` and all references.

- [ ] **Step 2: Remove old domain ProviderKind**

Remove `internal/llmgateway/domain/llmgateway.go` old `ProviderKind` constants — new domain types in `provider.go` replace them.

- [ ] **Step 3: Update TenantGatewayCache**

Replace `*Gateway` cache entries with metadata — or remove entirely if ModelRegistry's internal cache suffices.

- [ ] **Step 4: Run full test suite**

Run: `go test -short ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "[chore](llmgateway): remove deprecated static catalog and old provider types"
```

---

### Task 12: E2E verification

**Files:**

- No new files. Run existing E2E suite and add provider/model CRUD test cases.

- [ ] **Step 1: Run `make e2e-system-short`**

Run: `make e2e-system-short`
Expected: PASS, no regressions

- [ ] **Step 2: Run `STATEFUL_E2E_PROFILE=test STATEFUL_E2E_DURATION_SEC=600 STATEFUL_E2E_PACKS=all make e2e-system-soak`**

Expected: PASS, provider/model operations work end-to-end

- [ ] **Step 3: Update contract golden files if needed**

Run: `go test -run TestContracts -update ./api/http/...`
Then `make e2e-attestation-check`

- [ ] **Step 4: Commit any golden file changes**

```bash
git add api/http/testdata/contracts/
git commit -m "[test](contract): update golden files for model management API changes"
```

---

## Implementation Order Summary

```
Task 1  Domain entities & ports           ← foundation
Task 2  Protocol interface extraction      ← foundation
Task 3  Tenant DDL                         ← foundation
Task 4  PostgreSQL repositories            ← depends on 1,3
Task 5  ModelRegistry                      ← depends on 1,2,4
Task 6  Gateway refactor                   ← depends on 2,5
Task 7  Wiring layer update                ← depends on 4,5,6
Task 8  Application services               ← depends on 4,5
Task 9  HTTP handlers & routes             ← depends on 7,8
Task 10 Frontend                           ← depends on 9
Task 11 Cleanup                            ← depends on 6,7
Task 12 E2E verification                   ← depends on all
```
