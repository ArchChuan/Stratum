# Multi-Tenant 系统设计文档

**日期**: 2026-06-02
**状态**: 待审查
**分支**: feat/multi-tenant

---

## 1. 概述

为 ClawHermes AI Go 引入多租户能力。每个租户对应一个团队/组织，通过 GitHub OAuth 登录，租户内资源完全隔离。

### 1.1 核心目标

- 用户通过 GitHub OAuth 唯一入口登录
- 租户隔离所有资源：Agent、Skill、Memory、Knowledge、MCP 配置、执行历史、模型配置
- 全局管理员管理租户，租户管理员管理成员
- 新用户首次登录后必须创建或加入租户才能使用系统

### 1.2 不在本设计范围

- 计费/支付集成
- 多 GitHub OAuth App 支持
- 租户间资源共享

### 1.3 初始化说明

`global_admin` 账号通过环境变量 `GLOBAL_ADMIN_GITHUB_LOGIN` 在服务首次启动时种子写入 `public.users`，不通过注册流程创建。

---

## 2. 角色体系

```
全局管理员 (global_role='global_admin')
  ├── 管理所有租户（创建/禁用/删除）
  ├── 独立账号，不属于任何具体租户
  └── 访问 /admin/* 路由

租户管理员 (tenant_members.role='admin')
  ├── 管理本租户成员（邀请/移除/改角色）
  ├── 管理本租户资源配置和设置
  └── 访问 /tenant/* 管理路由

租户成员 (tenant_members.role='member')
  └── 使用本租户资源（Agent/Skill/RAG 等）
```

---

## 3. 数据模型

### 3.1 公共 Schema（`public`）

#### 用户表

```sql
users (
  id              UUID PRIMARY KEY,
  github_id       TEXT UNIQUE NOT NULL,
  github_login    TEXT NOT NULL,
  email           TEXT,
  display_name    TEXT,
  avatar_url      TEXT,
  global_role     TEXT DEFAULT 'user',   -- 'global_admin' | 'user'
  last_login_at   TIMESTAMPTZ,
  created_at      TIMESTAMPTZ DEFAULT now(),
  updated_at      TIMESTAMPTZ DEFAULT now()
)
```

#### 租户表

```sql
tenants (
  id              UUID PRIMARY KEY,
  name            TEXT NOT NULL,
  slug            TEXT UNIQUE NOT NULL,  -- URL 友好标识
  github_org_id   TEXT,                  -- 关联 GitHub Org（可选）
  github_org_name TEXT,
  avatar_url      TEXT,
  plan            TEXT DEFAULT 'free',   -- 'free' | 'pro' | 'enterprise'
  status          TEXT DEFAULT 'active', -- 'active' | 'suspended' | 'deleted'
  settings        JSONB DEFAULT '{}',
  created_at      TIMESTAMPTZ DEFAULT now(),
  updated_at      TIMESTAMPTZ DEFAULT now(),
  deleted_at      TIMESTAMPTZ
)
```

#### 租户成员关系

```sql
tenant_members (
  id              UUID PRIMARY KEY,
  tenant_id       UUID REFERENCES tenants(id),
  user_id         UUID REFERENCES users(id),
  role            TEXT NOT NULL,         -- 'admin' | 'member'
  invited_by      UUID REFERENCES users(id),
  joined_at       TIMESTAMPTZ DEFAULT now(),
  UNIQUE (tenant_id, user_id)
)
```

#### 邀请

```sql
invitations (
  id              UUID PRIMARY KEY,
  tenant_id       UUID REFERENCES tenants(id),
  email           TEXT NOT NULL,
  role            TEXT DEFAULT 'member',
  token_hash      TEXT NOT NULL,
  invited_by      UUID REFERENCES users(id),
  expires_at      TIMESTAMPTZ NOT NULL,
  accepted_at     TIMESTAMPTZ,
  created_at      TIMESTAMPTZ DEFAULT now()
)
```

#### Refresh Token

```sql
refresh_tokens (
  id              UUID PRIMARY KEY,
  user_id         UUID REFERENCES users(id),
  tenant_id       UUID REFERENCES tenants(id),  -- global_admin 为 NULL
  token_hash      TEXT NOT NULL,         -- SHA256，不存明文
  device_info     JSONB,                 -- user-agent, IP
  issued_at       TIMESTAMPTZ DEFAULT now(),
  expires_at      TIMESTAMPTZ NOT NULL,
  last_used_at    TIMESTAMPTZ,
  revoked_at      TIMESTAMPTZ
)
```

#### 租户 API Key（调用 ClawHermes API 的凭证）

```sql
tenant_api_keys (
  id              UUID PRIMARY KEY,
  tenant_id       UUID REFERENCES tenants(id),
  display_name    TEXT NOT NULL,
  key_hash        TEXT NOT NULL,         -- SHA256
  key_prefix      TEXT NOT NULL,         -- 显示用，如 "chk_live_..."
  scopes          TEXT[],                -- ['agents:read','agents:write','rag:query']
  expires_at      TIMESTAMPTZ,
  last_used_at    TIMESTAMPTZ,
  created_by      UUID REFERENCES users(id),
  revoked_at      TIMESTAMPTZ,
  created_at      TIMESTAMPTZ DEFAULT now()
)
```

#### 审计日志

```sql
audit_logs (
  id              UUID PRIMARY KEY,
  tenant_id       UUID REFERENCES tenants(id),
  user_id         UUID,
  action          TEXT NOT NULL,         -- 'agent.create' | 'key.revoke' | 'member.invite'
  resource_type   TEXT,
  resource_id     TEXT,
  old_value       JSONB,
  new_value       JSONB,
  ip_address      TEXT,
  user_agent      TEXT,
  trace_id        TEXT,
  created_at      TIMESTAMPTZ DEFAULT now()
)
```

#### 模型供应商目录

```sql
model_providers (
  id                    TEXT PRIMARY KEY,  -- 'openai' | 'anthropic' | 'ollama'
  name                  TEXT NOT NULL,
  base_url              TEXT,
  auth_type             TEXT,              -- 'bearer' | 'x-api-key' | 'none'
  supports_completion   BOOL DEFAULT true,
  supports_embedding    BOOL DEFAULT false,
  supports_streaming    BOOL DEFAULT false,
  supports_vision       BOOL DEFAULT false,
  status                TEXT DEFAULT 'active',
  created_at            TIMESTAMPTZ DEFAULT now()
)
```

#### 模型目录

```sql
models (
  id                        TEXT PRIMARY KEY, -- 'gpt-4o' | 'claude-opus-4-6'
  provider_id               TEXT REFERENCES model_providers(id),
  display_name              TEXT NOT NULL,
  type                      TEXT NOT NULL,    -- 'completion' | 'embedding' | 'multimodal'
  context_window            INT,
  max_output_tokens         INT,
  input_cost_per_1k         NUMERIC(10,6),   -- USD/1k tokens
  output_cost_per_1k        NUMERIC(10,6),
  embedding_dimensions      INT,
  supports_streaming        BOOL DEFAULT true,
  supports_function_calling BOOL DEFAULT false,
  supports_vision           BOOL DEFAULT false,
  supports_json_mode        BOOL DEFAULT false,
  is_deprecated             BOOL DEFAULT false,
  deprecated_at             TIMESTAMPTZ,
  available_from            TIMESTAMPTZ DEFAULT now(),
  metadata                  JSONB DEFAULT '{}'
)
```

---

### 3.2 每租户 Schema（`tenant_{id}`，注册时自动创建）

#### Agent

```sql
agents (
  id              UUID PRIMARY KEY,
  name            TEXT NOT NULL,
  type            TEXT NOT NULL,           -- 'react' | 'simple' | 'multimodal'
  description     TEXT,
  persona         TEXT,
  system_prompt   TEXT,
  llm_model       TEXT NOT NULL,
  max_iterations  INT DEFAULT 10,
  capabilities    JSONB DEFAULT '[]',
  status          TEXT DEFAULT 'active',   -- 'active' | 'inactive' | 'archived'
  tags            TEXT[],
  metadata        JSONB DEFAULT '{}',
  created_by      UUID,
  created_at      TIMESTAMPTZ DEFAULT now(),
  updated_at      TIMESTAMPTZ DEFAULT now()
)
```

#### Skill

```sql
skills (
  id              UUID PRIMARY KEY,
  name            TEXT NOT NULL,
  type            TEXT NOT NULL,           -- 'llm' | 'code' | 'mcp'
  description     TEXT,
  llm_model       TEXT,
  prompt_template TEXT,
  temperature     FLOAT,
  max_tokens      INT,
  language        TEXT,                    -- code skill: 'python' | 'javascript' | 'go'
  code            TEXT,
  input_schema    JSONB,
  output_schema   JSONB,
  timeout_seconds INT DEFAULT 30,
  version         TEXT DEFAULT '1.0.0',
  status          TEXT DEFAULT 'active',
  tags            TEXT[],
  metadata        JSONB DEFAULT '{}',
  created_by      UUID,
  created_at      TIMESTAMPTZ DEFAULT now(),
  updated_at      TIMESTAMPTZ DEFAULT now()
)
```

#### MCP 配置

```sql
mcp_configs (
  id              UUID PRIMARY KEY,
  name            TEXT NOT NULL,
  version         TEXT,
  transport       TEXT NOT NULL,           -- 'stdio' | 'sse' | 'http'
  command         TEXT,
  args            JSONB DEFAULT '[]',
  url             TEXT,
  env             JSONB DEFAULT '{}',
  capabilities    TEXT[],
  timeout_seconds INT DEFAULT 30,
  pool_config     JSONB,
  cache_config    JSONB,
  monitor_config  JSONB,
  status          TEXT DEFAULT 'active',
  created_by      UUID,
  created_at      TIMESTAMPTZ DEFAULT now(),
  updated_at      TIMESTAMPTZ DEFAULT now()
)
```

#### 会话

```sql
sessions (
  id              UUID PRIMARY KEY,
  user_id         UUID,
  agent_id        UUID REFERENCES agents(id),
  status          TEXT DEFAULT 'active',   -- 'active' | 'completed' | 'abandoned'
  message_count   INT DEFAULT 0,
  started_at      TIMESTAMPTZ DEFAULT now(),
  ended_at        TIMESTAMPTZ,
  metadata        JSONB DEFAULT '{}'
)
```

#### Memory

```sql
memory_entries (
  id              UUID PRIMARY KEY,
  type            TEXT NOT NULL,           -- 'short_term' | 'long_term' | 'entity' | 'summary'
  session_id      UUID REFERENCES sessions(id),
  agent_id        UUID REFERENCES agents(id),
  user_id         UUID,
  role            TEXT NOT NULL,           -- 'user' | 'assistant' | 'system'
  content         TEXT NOT NULL,
  importance      FLOAT DEFAULT 0.5,
  tags            TEXT[],
  milvus_vec_id   TEXT,
  metadata        JSONB DEFAULT '{}',
  expires_at      TIMESTAMPTZ,
  created_at      TIMESTAMPTZ DEFAULT now()
)
```

#### 实体与关系

```sql
entities (
  id              UUID PRIMARY KEY,
  name            TEXT NOT NULL,
  type            TEXT NOT NULL,           -- 'person' | 'org' | 'location' | 'concept'
  confidence      FLOAT,
  user_id         UUID,
  attributes      JSONB DEFAULT '{}',
  first_seen_at   TIMESTAMPTZ DEFAULT now(),
  last_seen_at    TIMESTAMPTZ DEFAULT now(),
  created_at      TIMESTAMPTZ DEFAULT now()
)

entity_relations (
  id              UUID PRIMARY KEY,
  from_entity_id  UUID REFERENCES entities(id),
  to_entity_id    UUID REFERENCES entities(id),
  relation_type   TEXT NOT NULL,
  confidence      FLOAT,
  metadata        JSONB DEFAULT '{}',
  last_seen_at    TIMESTAMPTZ DEFAULT now()
)
```

#### 执行历史

```sql
exec_history (
  id              UUID PRIMARY KEY,
  agent_id        UUID REFERENCES agents(id),
  session_id      UUID REFERENCES sessions(id),
  user_id         UUID,
  trigger_type    TEXT DEFAULT 'user',     -- 'api' | 'schedule' | 'agent' | 'user'
  input           TEXT NOT NULL,
  output          TEXT,
  status          TEXT NOT NULL,           -- 'success' | 'failed' | 'timeout' | 'cancelled'
  error_message   TEXT,
  duration_ms     INT NOT NULL,
  token_count     INT,
  iterations      INT,
  skill_calls     JSONB DEFAULT '[]',
  trace_id        TEXT,
  span_id         TEXT,
  metadata        JSONB DEFAULT '{}',
  created_at      TIMESTAMPTZ DEFAULT now()
)
```

#### 知识库文档

```sql
knowledge_docs (
  id              UUID PRIMARY KEY,
  filename        TEXT NOT NULL,
  original_name   TEXT,
  mime_type       TEXT,
  file_size_bytes BIGINT,
  chunk_count     INT DEFAULT 0,
  status          TEXT DEFAULT 'processing', -- 'processing' | 'ready' | 'failed'
  error_message   TEXT,
  milvus_coll     TEXT,
  neo4j_labels    TEXT[],
  metadata        JSONB DEFAULT '{}',
  created_by      UUID,
  created_at      TIMESTAMPTZ DEFAULT now(),
  updated_at      TIMESTAMPTZ DEFAULT now()
)
```

#### 模型相关（每租户）

```sql
llm_api_keys (
  id                UUID PRIMARY KEY,
  provider_id       TEXT,
  display_name      TEXT,
  encrypted_key     TEXT NOT NULL,         -- AES-256 加密，绝不存明文
  key_hint          TEXT,                  -- 末 4 位，如 "...sk3F"
  endpoint_override TEXT,
  is_default        BOOL DEFAULT false,
  status            TEXT DEFAULT 'active',
  last_used_at      TIMESTAMPTZ,
  last_verified_at  TIMESTAMPTZ,
  expires_at        TIMESTAMPTZ,
  created_by        UUID,
  created_at        TIMESTAMPTZ DEFAULT now(),
  updated_at        TIMESTAMPTZ DEFAULT now()
)

model_presets (
  id                UUID PRIMARY KEY,
  name              TEXT NOT NULL,
  provider_id       TEXT,
  model_id          TEXT,
  temperature       FLOAT DEFAULT 0.7,
  max_tokens        INT,
  top_p             FLOAT DEFAULT 1.0,
  frequency_penalty FLOAT DEFAULT 0.0,
  presence_penalty  FLOAT DEFAULT 0.0,
  stop_sequences    TEXT[],
  system_prompt     TEXT,
  is_default        BOOL DEFAULT false,
  created_by        UUID,
  created_at        TIMESTAMPTZ DEFAULT now(),
  updated_at        TIMESTAMPTZ DEFAULT now()
)

model_usage (
  id                UUID PRIMARY KEY,
  provider_id       TEXT,
  model_id          TEXT,
  agent_id          UUID REFERENCES agents(id),
  skill_id          UUID REFERENCES skills(id),
  exec_history_id   UUID REFERENCES exec_history(id),
  user_id           UUID,
  session_id        UUID REFERENCES sessions(id),
  usage_type        TEXT NOT NULL,
  prompt_tokens     INT DEFAULT 0,
  completion_tokens INT DEFAULT 0,
  total_tokens      INT NOT NULL,
  estimated_cost_usd NUMERIC(10,8),
  latency_ms        INT,
  status            TEXT NOT NULL,
  error_code        TEXT,
  created_at        TIMESTAMPTZ DEFAULT now()
)

model_quotas (
  id                UUID PRIMARY KEY,
  provider_id       TEXT,
  model_id          TEXT,
  quota_type        TEXT NOT NULL,         -- 'tokens' | 'requests' | 'cost_usd'
  period            TEXT DEFAULT 'monthly',
  limit_value       NUMERIC NOT NULL,
  alert_threshold   FLOAT DEFAULT 0.8,
  current_usage     NUMERIC DEFAULT 0,
  period_start      TIMESTAMPTZ,
  period_end        TIMESTAMPTZ,
  created_at        TIMESTAMPTZ DEFAULT now(),
  updated_at        TIMESTAMPTZ DEFAULT now()
)
```

#### 提示词模板

```sql
prompt_templates (
  id              UUID PRIMARY KEY,
  name            TEXT NOT NULL,
  description     TEXT,
  content         TEXT NOT NULL,
  variables       JSONB DEFAULT '[]',
  category        TEXT,
  version         INT DEFAULT 1,
  is_published    BOOL DEFAULT false,
  tags            TEXT[],
  created_by      UUID,
  created_at      TIMESTAMPTZ DEFAULT now(),
  updated_at      TIMESTAMPTZ DEFAULT now()
)
```

#### 工作流

```sql
workflows (
  id              UUID PRIMARY KEY,
  name            TEXT NOT NULL,
  description     TEXT,
  trigger_type    TEXT,
  definition      JSONB NOT NULL,
  status          TEXT DEFAULT 'active',
  version         INT DEFAULT 1,
  created_by      UUID,
  created_at      TIMESTAMPTZ DEFAULT now(),
  updated_at      TIMESTAMPTZ DEFAULT now()
)

workflow_runs (
  id              UUID PRIMARY KEY,
  workflow_id     UUID REFERENCES workflows(id),
  trigger_type    TEXT,
  triggered_by    UUID,
  status          TEXT NOT NULL,
  input           JSONB,
  output          JSONB,
  error_message   TEXT,
  started_at      TIMESTAMPTZ DEFAULT now(),
  ended_at        TIMESTAMPTZ,
  trace_id        TEXT
)
```

#### 定时任务 & Webhook

```sql
scheduled_tasks (
  id              UUID PRIMARY KEY,
  name            TEXT NOT NULL,
  target_type     TEXT NOT NULL,           -- 'agent' | 'workflow'
  target_id       UUID NOT NULL,
  cron_expr       TEXT NOT NULL,
  input           JSONB DEFAULT '{}',
  timezone        TEXT DEFAULT 'UTC',
  enabled         BOOL DEFAULT true,
  last_run_at     TIMESTAMPTZ,
  next_run_at     TIMESTAMPTZ,
  last_status     TEXT,
  created_by      UUID,
  created_at      TIMESTAMPTZ DEFAULT now()
)

webhooks (
  id              UUID PRIMARY KEY,
  name            TEXT NOT NULL,
  url             TEXT NOT NULL,
  secret_hash     TEXT,
  events          TEXT[],
  enabled         BOOL DEFAULT true,
  created_by      UUID,
  created_at      TIMESTAMPTZ DEFAULT now()
)

webhook_deliveries (
  id              UUID PRIMARY KEY,
  webhook_id      UUID REFERENCES webhooks(id),
  event_type      TEXT NOT NULL,
  payload         JSONB NOT NULL,
  status          TEXT NOT NULL,
  http_status     INT,
  response_body   TEXT,
  attempt_count   INT DEFAULT 1,
  next_retry_at   TIMESTAMPTZ,
  delivered_at    TIMESTAMPTZ,
  created_at      TIMESTAMPTZ DEFAULT now()
)
```

---

## 4. 存储层设计

### 4.1 整体视图

```
PostgreSQL  — 结构化数据（账户/配置/历史/审计）    [新增]
Redis       — 缓存/限流/JWT 黑名单                [新增]
Milvus      — 向量存储（collection-per-tenant）   [已有]
Neo4j       — 图谱（label namespace per tenant） [已有]
MinIO       — 文件存储（路径隔离）                 [已有]
NATS        — 事件总线（subject 前缀隔离）          [已有]
```

### 4.2 Redis Key 规范

| Key 模式 | 用途 | TTL |
|---------|------|-----|
| `session:{session_id}` | 会话缓存 | 30min |
| `ratelimit:{tenant_id}:{window}` | API 限流滑动窗口 | 1min |
| `jwt:blacklist:{jti}` | 已撤销 JWT | = token 剩余有效期 |
| `llm:cache:{hash}` | LLM 响应缓存 | 1h |
| `quota:{tenant_id}:{provider}:{period}` | 实时配额计数 | 同配额周期 |

### 4.3 MinIO Bucket 结构

```
bucket: clawhermes-{env}
  └── tenants/
      └── {tenant_id}/
          ├── knowledge/{doc_id}/{filename}
          ├── agent-assets/
          └── exports/            # TTL 24h
```

### 4.4 Milvus Collection 命名

```
tenant_{tenant_id}_knowledge
tenant_{tenant_id}_memory
tenant_{tenant_id}_entities
```

大租户内按 `agent_id` 分 partition。

### 4.5 Neo4j Label 命名（社区版）

```
节点：T_{tenant_id}_Document / T_{tenant_id}_Entity
关系：T_{tenant_id}_RELATES_TO
```

### 4.6 NATS Subject 规范

```
tenant.{tenant_id}.exec.completed
tenant.{tenant_id}.quota.alert
tenant.{tenant_id}.webhook.trigger

Stream：per-tenant，subject filter = tenant.{id}.>
```

---

## 5. 认证与注册流程

### 5.1 GitHub OAuth 登录

```
GET /auth/github → 302 → github.com/login/oauth/authorize
GET /auth/github/callback?code → 换 token → GET GitHub /user
  新用户 → 返回 onboarding_token（短期）
  老用户 → 签发 JWT + Refresh Token
```

### 5.2 Onboarding

```
路径 A 创建租户：
  POST /auth/register { action:'create_tenant', tenant_name, slug }
  事务：INSERT tenants → tenant_members(role='admin')
       → CREATE SCHEMA tenant_{id} → migration
       → 初始化 Milvus + NATS stream

路径 B 加入租户：
  POST /auth/register { action:'join_tenant', invitation_token }
  事务：验证 invitations → INSERT tenant_members(role='member')
```

### 5.3 Token 策略

```
JWT Access Token:  RS256, exp=15min, payload={sub,tid,role,jti}
Refresh Token:     SHA256 存库，30 天，Rotation（每次刷新换新 token）
撤销:              revoked_at 字段 + Redis jwt:blacklist:{jti}
前端存储:           access_token → 内存；refresh_token → httpOnly cookie
```

---

## 6. Context 传递与存储隔离

```go
// TenantContext 由 JWT middleware 注入
type TenantContext struct {
    TenantID, UserID, Role, GlobalRole string
}

// PostgreSQL：SET LOCAL search_path 在事务内隔离
func ExecTenant(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error

// Milvus：tenant_{id}_knowledge
func TenantCollection(ctx context.Context, kind string) string

// Neo4j：T_{id}_Document
func TenantLabel(ctx context.Context, label string) string

// NATS：tenant.{id}.exec.completed
func TenantSubject(ctx context.Context, subject string) string
```

---

## 7. API 路由

```
公开:            GET /auth/github, /auth/github/callback, POST /auth/refresh, /auth/logout
认证后:          GET /auth/me, POST /auth/register
全局管理员:      /admin/tenants CRUD
租户管理员:      /tenant/members CRUD, /tenant/invitations, /tenant/settings
账户:            /account/api-keys CRUD
租户资源:        /agents/* /skills/* /rag/* /memory/* /mcp/* /exec-history/*
```

---

## 8. 前端改动

新增页面：`LoginPage`、`OnboardingPage`、`CallbackPage`、`TenantsListPage`（admin）、`MembersPage`（tenant admin）、`ApiKeysPage`

路由守卫：未登录→/login，无租户→/onboarding，权限不足→403

导航栏右上角 dropdown 按角色展示：成员管理/租户设置（admin）、管理后台（global_admin）

---

## 9. 新增模块清单

| 模块 | 路径 | 说明 |
|------|------|------|
| Auth | `internal/auth/` | GitHub OAuth、JWT、onboarding |
| TenantDB | `pkg/tenantdb/` | ExecTenant + 各存储隔离 helper |
| Tenant Middleware | `api/middleware/tenant.go` | JWT 解析、context 注入 |
| Admin Handler | `api/handler/admin_handler.go` | 全局租户管理 |
| Tenant Handler | `api/handler/tenant_handler.go` | 成员/设置管理 |
| Schema Migration | `internal/migration/` | per-tenant schema 创建与迁移 |
| PostgreSQL | `pkg/postgres/` | 连接池封装 |
| Redis | `pkg/redis/` | 缓存、限流、黑名单 |
