# 模型管理重构：多模型接入 & 多厂商模型供应

## Status

Approved | 2026-07-28

## Problem

当前 `llmgateway` 硬编码 Qwen/Zhipu 两个 provider，模型列表静态写死，`parseProvider()` 靠 model name 前缀路由，无模型元数据，无法接入 Anthropic/Ollama/DeepSeek 等非 OpenAI 协议厂商。每个租户的 provider 配置存储在 `public.tenants.settings` JSONB 中，无独立 entity。

## Goals

1. 支持多厂商接入（Anthropic、Ollama、DeepSeek 等），含非 OpenAI 协议厂商
2. 协议抽象层，OpenAI-compatible 和 Anthropic Messages API 等协议可独立实现
3. 模型成为一等公民：完整元数据（能力标签、context window、max tokens、价格、推荐）
4. 租户完全独立管理 provider 和模型
5. 混合 catalog：provider API 自动发现 → 本地同步 → 手动补元数据
6. 前端独立 Tab "模型管理"

## Non-Goals

- 多模型并行调用编排（单次请求仍用单一模型）
- 平台级预设 provider（每个租户自行配置）

## 修订记录

- 2026-08-04：模型 fallback / 自动切换移出 Non-Goals——模型级 fallback 链
  已实现（随本修订 PR 同提）：chat 调用（Complete/CompleteStream）在瞬态错误
  （429/5xx/超时/连接错误）下沿 `[primary] + 有序列举候选（同 provider 优先
  → Recommended desc → name asc，上限 3）` 降级，主模型失败立即重试 1 次；
  `context.Canceled` 永不触发降级；流式仅首 token 发出前失败可降级；
  链耗尽返回包装全部尝试的 permanent 错误（agent 层 RetryFn 对
  permanent 标记跳过重试，防放大）；指纹 `ModelResolved`/`ModelRoutedVia`
  贯通。能力画像路由（请求能力而非模型名）仍为非目标，延后。

---

## 1. Domain Model

### Provider

```go
type ProviderKind string
const (
    ProviderOpenAICompat ProviderKind = "openai_compat"
    ProviderAnthropic    ProviderKind = "anthropic"
    ProviderOllama       ProviderKind = "ollama"
)

type Provider struct {
    ID           string
    TenantID     string
    Name         string        // 租户自定义名称
    Kind         ProviderKind
    BaseURL      string
    APIKey       string        // AES-256 加密存储
    DefaultModel string
    Enabled      bool
    CreatedAt    time.Time
    UpdatedAt    time.Time
}
```

### Model

```go
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
    Name            string           // API model name
    DisplayName     string
    Capabilities    []ModelCapability
    ContextWindow   int
    MaxTokens       int
    InputPrice      float64
    OutputPrice     float64
    Recommended     bool
    Enabled         bool
    ProviderManaged bool             // provider 自动发现标记
    CreatedAt       time.Time
    UpdatedAt       time.Time
}
```

### Protocol Interfaces (infrastructure)

```go
type ChatProtocol interface {
    Complete(ctx context.Context, cfg ProviderConfig, req *CompletionRequest) (*CompletionResponse, error)
    CompleteStream(ctx context.Context, cfg ProviderConfig, req *CompletionRequest, onToken func(string)) (*CompletionResponse, error)
    Health(ctx context.Context, cfg ProviderConfig) error
    ListModels(ctx context.Context, cfg ProviderConfig) ([]string, error)
}

type EmbedProtocol interface {
    CreateEmbeddings(ctx context.Context, cfg ProviderConfig, req *EmbeddingRequest) (*EmbeddingResponse, error)
    BatchSize() int
}
```

`ProviderConfig` 从构造时注入改为方法参数，protocol 实现变为无状态单例。

---

## 2. Gateway & ModelRegistry

Gateway 不再持有 `map[ModelProvider]client`，改为委托 `ModelRegistry`：

```
Handler → Gateway.Complete(ctx, req)
  → ModelRegistry.Resolve(tenantID, modelName)
    → SELECT tenant.models JOIN tenant.providers
    → 返回 (ProviderConfig, Protocol)
  → protocol.Complete(ctx, cfg, req)
```

**ModelRegistry：**

- 包装 `port.ModelRepository` + `port.ProviderRepository`
- LRU cache，TTL = `constants.GatewayCacheTTL`
- `Resolve(ctx, tenantID, modelName)` → `(ResolvedModel, error)`
- `ListChatModels(ctx, tenantID)` / `ListEmbeddingModels(ctx, tenantID)`

**删除项：**

- `ModelProvider` string type 及常量
- `parseProvider()` 前缀匹配
- `StaticModelCatalog` 硬编码
- `tenantCapabilityResolver` 中手动 `NewQwenClient`/`NewZhipuClient` 逻辑（~100 行）

**TenantGatewayCache 适配：**
缓存粒度从 "整个 Gateway" 变为 "解析后的 provider config 集合"。

---

## 3. Tenant Resolver 简化

`tenantCapabilityResolver` 不再直接读 `settings.llm_api_keys` 并手动组装，改为：

```go
func (r *tenantCapabilityResolver) Resolve(ctx context.Context, tenantID string) (...) {
    r.registry.WarmTenant(ctx, tenantID)
    // 委托给 registry
}
```

`DiagnosticModelStatus`、`ValidateTenantChatModel`、`ListTenantChatModels` 全部委托给 `ModelRegistry`。

---

## 4. Provider Auto-Discovery

```go
func (s *ProviderService) DiscoverModels(ctx context.Context, provider *Provider) ([]Model, error) {
    proto := s.protocols[provider.Kind]
    names, _ := proto.ListModels(ctx, provider.Config())
    // upsert: ON CONFLICT (tenant_id, provider_id, name)
    //   - 新模型 INSERT
    //   - 已有模型保留用户编辑的元数据
    //   - provider 下掉的模型标记 enabled=false
}
```

创建 provider 后自动触发，也可手动调用 `POST /providers/:id/discover`。

---

## 5. Database Schema (tenant DDL)

```sql
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
```

存量租户 `settings.llm_api_keys` 通过一次性 backfill job 迁移，保留旧字段兼容一个版本后清理。

---

## 6. API Design

```
POST   /api/providers              # 创建 provider
GET    /api/providers              # 列出 provider
PUT    /api/providers/:id          # 更新 provider
DELETE /api/providers/:id          # 删除 provider（级联删模型）
POST   /api/providers/:id/discover # 触发模型自动发现
POST   /api/providers/:id/health   # 健康检查

GET    /api/models                 # 列出模型（按 capability 筛选）
GET    /api/models/:id             # 模型详情
PUT    /api/models/:id             # 更新模型元数据
PATCH  /api/models/:id/toggle      # 启用/禁用
```

**已有 API 兼容：**

- `GET /models` 行为不变，数据源切换为 registry
- `GET/PUT /agents/system/settings` 的 `availableModels` 从 registry 实时查询
- `GET /tenant/settings` 中 `llm_api_keys` 标记 deprecated

---

## 7. Frontend

新增顶级路由 `/models`，Tab "模型管理"，两个子 Tab：

### 厂商管理 (Providers)

- 厂商列表：名称、类型、状态、模型数、最后健康检查
- 添加厂商表单：名称、类型下拉、Base URL、API Key
- 操作：发现模型、健康检查、编辑、删除（Modal.confirm + 级联提示）

### 模型目录 (Models)

- 模型列表：显示名、API名、厂商、能力标签（色标 Tag）、上下文窗口、状态
- 编辑模型元数据：display_name、capabilities、pricing、recommended（Drawer）
- 启用/禁用开关
- 按 capability 筛选
- `provider_managed` 模型不可删除（仅禁用），手动添加的模型可删除

### 组件结构

```
web/src/modules/llm/
├── pages/
│   ├── ProviderListPage.tsx
│   └── ModelListPage.tsx
├── components/
│   ├── ProviderForm.tsx
│   ├── ProviderCard.tsx
│   ├── ModelEditDrawer.tsx
│   ├── ModelCapabilityTags.tsx
│   └── DiscoverResultModal.tsx
├── api/
│   └── llm.api.ts
├── model/
│   └── llm.ts
└── hooks/
    └── useProviders.ts / useModels.ts
```

---

## 8. Implementation Order

1. **Domain & Port** — `internal/llmgateway/domain/` 新增 Provider/Model 实体 + port 接口
2. **Protocol abstraction** — 提取 `ChatProtocol`/`EmbedProtocol` interface，`OpenAICompatProtocol` 复用现有逻辑，`AnthropicProtocol` 新实现
3. **Infrastructure** — Provider/Model repository (PostgreSQL)，ModelRegistry + cache
4. **Tenant DDL** — `tenant_schema.sql` 新增 providers/models 表，backfill migration
5. **Gateway refactor** — 删除 clients map，接入 ModelRegistry；tenant resolver 简化
6. **Application services** — ProviderService (CRUD + discover)，ModelService (CRUD + toggle)
7. **HTTP handlers + routes** — provider/model handler，注册路由
8. **Frontend** — llm module 页面 + 组件
9. **Cleanup** — 删除 StaticModelCatalog、ModelProvider 常量、parseProvider、旧 resolver 逻辑
10. **E2E verification** — `stratum-e2e-development` skill 全链路验收

---

## 9. Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| 存量租户 `llm_api_keys` 迁移失败 | 保留旧字段兼容一个版本，backfill 失败不阻断新功能 |
| Anthropic 协议实现复杂度 | 先只做 chat（非流式+流式），embedding 暂不支持 Anthropic |
| ModelRegistry cache 一致性问题 | provider/model 变更时主动 invalidate cache，TTL 兜底 |
| API 兼容性 | `GET /models` 行为不变，contract test 验证 golden file |
