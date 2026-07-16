# Stratum 架构演化与编码规范

**生成时间**: 2026-06-22
**来源**: 从 2026-06-02 至 2026-06-21 共 18 份设计文档中提取
**目的**: 沉淀架构决策、分层规范、演化规律，作为新人 onboarding 和重构守护的参考

---

## 目录

1. [架构演化时间线](#1-架构演化时间线)
2. [DDD 分层规范](#2-ddd-分层规范)
3. [多租户架构](#3-多租户架构)
4. [跨域调用与反向依赖](#4-跨域调用与反向依赖)
5. [错误处理分层](#5-错误处理分层)
6. [安全规范](#6-安全规范)
7. [可观测性](#7-可观测性)
8. [前端编码规范](#8-前端编码规范)
9. [测试策略](#9-测试策略)
10. [CI 静态守护](#10-ci-静态守护)

---

## 1. 架构演化时间线

### 阶段 1：多租户基础（2026-06-02）

**动机**: 从单租户升级到多租户 SaaS，租户间资源完全隔离

**核心决策**:

- **PostgreSQL schema 隔离**: 每个租户一个独立 schema (`tenant_<uuid>`)，通过 `SET LOCAL search_path` 切换
- **公共表在 `public` schema**: `users`, `tenants`, `tenant_members`
- **租户表在 `tenant_*` schema**: `agents`, `skills`, `chat_conversations`, `memory_entries`, `knowledge_docs`, `mcp_servers`
- **GitHub OAuth 唯一入口**: 用户通过 GitHub 登录，首次登录后必须创建或加入租户
- **角色体系**: 全局管理员 (`global_admin`) 管理所有租户；租户管理员 (`admin`) 管理本租户成员；租户成员 (`member`) 使用资源

**技术约束**:

- 编号迁移（`pkg/migration/sql/NNN_*.sql`）只操作 `public` schema
- 引用 tenant-only 表的 DDL 必须放 `pkg/storage/postgres/tenant_schema.sql`
- `golang-migrate` dirty 状态修复: `force <version>` 标记为 clean
- 新增 tenant DDL 后必须在 `tenant_schema.sql` 中紧跟 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` 做 backfill

**遗留问题**: 多个子系统直接持有 `*pgxpool.Pool`，未分层

### 阶段 2：LLM Gateway 与租户级 API Key（2026-06-09）

**动机**: 不同租户使用不同供应商/模型/API Key，全局单一 Gateway 无法满足

**核心决策**:

- **租户级 API Key 存储**: `tenants.settings` JSONB 字段，结构键为 `llm_api_keys`；值在写入时加密
- **AES-256-GCM 加密**: 使用 `JWTPrivateKeyPEM` 作为唯一密钥源派生加密密钥
- **5 分钟 TTL 缓存**: `TenantGatewayCache` 缓存解密后的 `*Gateway` 实例，避免每次请求解密
- **动态 Gateway 解析**: Agent 执行时通过 `TenantResolver.InjectCompleter(ctx, tenantID)` 注入 per-tenant LLM 客户端

**安全约束**:

- `JWTPrivateKeyPEM` 泄露则所有租户 API key 暴露，必须通过 Vault/Secrets Manager 管理，**禁止入 git**
- 日志中**禁止打印解密后的 API key 明文**
- 前端 `.env` **禁止提交任何密钥**

**架构遗留**: `LLMGateway.Gateway` 仍是全局单例，但此时已无 API Key，成为空壳

---

### 阶段 3：DDD 分层重构（2026-06-16）

**动机**: 业务代码直接依赖 `pgxpool`/`redis`/`milvus` SDK，测试困难，分层混乱

**核心决策**:

- **Bounded Context 划分**: 8 个域 — `agent`, `memory`, `knowledge`, `skill`, `mcp`, `iam`, `llmgateway`, `platform`
- **三层架构强制**:
  - `domain/` — 纯业务逻辑，零第三方依赖（仅 stdlib + `pkg/constants`）
  - `application/` — 用例编排，只依赖 `domain` + `domain/port`
  - `infrastructure/` — 实现 `port` 接口，唯一可 import 第三方 SDK 的地方
- **Port 在消费方**: 依赖倒置原则（DIP），消费者声明接口，提供者实现
- **跨域调用走 Port**: 禁止 `internal/<ctx1>/application` 直接 import `internal/<ctx2>/application`

**依赖方向规则**:

```
api/http/handler → application → domain/port
                              ↓
                         infrastructure (实现 port)
```

**Context Map**:

```
agent    → memory.port, knowledge.port, skill.port, mcp.port, llmgateway.port
memory   → llmgateway.port
knowledge → llmgateway.port
skill    → llmgateway.port, mcp.port
mcp      → (无业务依赖)
iam      → (无业务依赖)
llmgateway → (无业务依赖)
platform → (被所有 context 依赖，不依赖任何 context)
```

**反向依赖修复**:

| 旧反向依赖 | 修复方式 |
|-----------|---------|
| `agent → memory/pipeline` | `agent.domain.port.MemoryRecaller`，由 memory 实现 |
| `config → knowledge` | config 拉入 `platform`，knowledge 单向依赖 platform |
| `agent → capgateway`（具体类型） | 改成 `agent.domain.port.CapabilityRouter` |

### 阶段 4：Handler 层全量 DDD 重构（2026-06-18）

**动机**: Handler 仍直接持有 `pgxpool.Pool`，写 raw SQL，业务编排混在 HTTP 层

**核心决策**:

- **Handler 退化为纯 transport**: `bind → svc.Method(ctx, input) → render(c.JSON 或 c.Error)`
- **所有业务编排下沉到 application 层**: Handler 方法体 ≤30 行，超出的全部拆到 Service
- **统一错误处理**: Handler 只调 `c.Error(svc.Xxx(...))`，由 `ErrorHandler` 中间件统一映射 domain 错误到 HTTP 状态码
- **消除八大违规模式**:
  1. Handler 持 `pgxpool.Pool` / `PgxPool`
  2. Handler 直引 `internal/*/infrastructure`
  3. Handler 直引 `pkg/tenantdb`
  4. Handler 内 raw SQL
  5. Handler 内 AES 加解密
  6. 跨 ctx application 直拼
  7. Handler 内 `context.Background()` 创建脱钩 ctx
  8. Handler 单方法 > 30 行业务编排

**Handler 强制黑名单**（depguard 锁死）:

```
× internal/*/infrastructure/...
× pkg/tenantdb
× pkg/storage/postgres
× github.com/jackc/pgx/...
× github.com/redis/go-redis/...
× github.com/milvus-io/milvus-sdk-go/...
× pkg/crypto  // AES 加解密禁用
```

**Handler 强制白名单**:

```
✓ api/http/dto
✓ api/middleware
✓ internal/<ctx>/application
✓ internal/<ctx>/domain  // 仅类型/sentinel error
✓ pkg/constants
✓ pkg/observability
✓ github.com/gin-gonic/gin
✓ go.uber.org/zap
✓ stdlib
```

**合规黄金标准**: `model_handler.go` 25 行 / 1 方法 — `bind → svc.Catalogue → c.JSON`

---

### 阶段 5：全局 LLM Gateway 清理（2026-06-21）

**动机**: 全局 `LLMGateway.Gateway` 构造时无 API Key，传给多个子系统后成为死代码路径

**核心决策**:

- **删除所有系统级 env key 读取**: `QWEN_API_KEY` / `ZHIPU_API_KEY` 全部移除
- **API Key 唯一来源**: 租户配置 `tenants.settings.llm_api_keys`，通过 `TenantGatewayCache` 解析
- **Context 注入模式**: `llmgateway.WithCompleter(ctx, completer)` 和 `CompleterFromContext(ctx)` 覆盖静态注入
- **当时的清理**: `DBSkillAdapter` / `SkillService` 的 `completer` 参数曾改为运行时 context 注入；这两个旧 Skill 类型现已随 instruction capability 重构移除
- **StaticModelCatalog**: 硬编码模型列表替代动态 gateway-based catalog（因全局 gateway 无 client）

**安全强化**: 全局禁止从环境变量读取 API Key，所有密钥必须来自租户配置

### 阶段 6：通用 Evaluation 控制面（2026-07-16）

当前源码已新增第 9 个 bounded context `evaluation`。它通过 `api/http/router.go` 暴露 suite 发布、异步 run/job、优化候选、实验阶段判断与 feedback 路由；tenant-scoped 数据表集中在 `pkg/storage/postgres/tenant_schema.sql`，前端入口位于 `web/src/modules/evaluation/`。

---

## 2. DDD 分层规范

### 2.1 目录结构（标准模板）

```
internal/<context>/
├── domain/              # 纯业务逻辑，零第三方依赖
│   ├── <entity>.go        # 实体 / 值对象 / 聚合根
│   ├── errors.go          # 领域错误（Err* 前缀）
│   └── port/              # 消费者侧接口（依赖倒置）
│       ├── repository.go    # 持久化接口
│       └── <service>.go     # 外部依赖接口
│
├── application/         # 用例编排，只依赖 domain + port
│   └── <service>.go       # Service 层，协调多个 domain/port
│
└── infrastructure/      # 实现 port，唯一可 import 第三方 SDK 的地方
    ├── persistence/       # Repository 实现（pgx/redis/milvus）
    │   └── <repo>_pg.go
    └── <adapter>.go       # 跨域 adapter（实现其他 context 的 port）
```

### 2.2 依赖规则（强制）

| 层 | 可以 import | 禁止 import |
|----|------------|-------------|
| **domain/** | `stdlib`, `pkg/constants` | 任何第三方库（`pgx`, `redis`, `nats`, `gin`, `zap`）；任何 `internal/` 兄弟 context |
| **application/** | `domain`, `domain/port`, `pkg/constants`, `pkg/observability`（仅类型） | `internal/*/infrastructure`, `pgx`, `redis`, `milvus`, `gin` |
| **infrastructure/** | 一切（第三方 SDK、`pgx`, `redis`, `milvus`, 兄弟 context 的 `application`） | 无限制，但遵循 Port 契约 |
| **handler/** | `application`, `domain`（仅错误类型）, `dto`, `middleware`, `pkg/constants` | `infrastructure`, `pgx`, `redis`, `pkg/tenantdb`, `pkg/crypto` |

### 2.3 Port 接口设计原则

1. **最小方法集**: 只暴露消费者需要的方法，不是 CRUD 全家桶
   - ✓ `MemoryRecaller.Recall(ctx, query, k) ([]string, error)`
   - ✗ `MemoryRepo.Create/Update/Delete/List...` 全暴露（太粗）

2. **放在消费方 domain/port/**: 依赖倒置原则（DIP）
   - `agent/domain/port/memory.go` 定义 `MemoryRecaller`
   - `memory/application/recall_service.go` 实现它
   - `api/wiring/` 注入实现

3. **接口复用规则**: 被 ≥2 个消费者使用时，仍放消费方包，不去被依赖方暴露

4. **跨域调用走 adapter**:

   ```go
   // agent/infrastructure/memory_adapter.go
   type memoryAdapter struct {
       svc *memory_app.RecallService  // 包装 memory 的 application
   }
   func (a *memoryAdapter) Recall(ctx, query, k) ([]string, error) {
       return a.svc.BuildContext(ctx, ...)  // 翻译接口
   }
   ```

### 2.4 贫血模型 vs 充血模型

**项目选择**: 充血模型（实体带方法）

**反模式**:

```go
// ✗ 贫血 — 实体纯字段，业务逻辑散在 service
type Agent struct {
    ID string
    Skills []string
}
func (s *AgentService) ValidateSkills(a *Agent) error { ... }  // 业务逻辑在 service
```

**正确做法**:

```go
// ✓ 充血 — 实体自检，不变量在构造函数/方法内校验
type Agent struct {
    id     string
    skills []Skill
}
func NewAgent(id string, skills []Skill) (*Agent, error) {
    if len(skills) > MaxSkills {
        return nil, ErrTooManySkills
    }
    return &Agent{id: id, skills: skills}, nil
}
func (a *Agent) AddSkill(s Skill) error {
    if a.HasSkill(s.ID) {
        return ErrDuplicateSkill
    }
    a.skills = append(a.skills, s)
    return nil
}
```

---

## 3. 多租户架构

### 3.1 租户上下文传播

**唯一入口**: `api/middleware/auth.go` 的 `AuthMiddleware` 从 JWT 解析 `tenant_id` 后注入 `gin.Context`:

```go
c.Set("tenant_context", &TenantContext{
    TenantID: claims.TenantID,
    UserID:   claims.UserID,
    Role:     claims.Role,
})
```

**Handler 层提取**:

```go
tc := middleware.TenantFromContext(c)
ctx := tenantdb.WithTenant(c.Request.Context(), tc.TenantID)
```

**禁止行为**:

- ✗ Handler 内 `context.Background()` 创建新 ctx（会丢失 tenant 信息）
- ✗ 异步 goroutine 直接用原 ctx（可能已 cancel，需 `context.WithoutCancel`）

### 3.2 数据库隔离（PostgreSQL schema）

**Schema 命名**: `tenant_<tenant_id>`（UUID 格式）

**租户切换**:

```go
// pkg/storage/postgres/tenant.go
func ExecTenant(ctx context.Context, pool *pgxpool.Pool, fn func(context.Context, pgx.Tx) error) error {
    tx, _ := pool.Begin(ctx)
    defer tx.Rollback(ctx)

    tenantID := TenantIDFromContext(ctx)
    schema := "tenant_" + tenantID
    _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %s, public",
        pgx.Identifier{schema}.Sanitize()))

    if err := fn(ctx, tx); err != nil {
        return err
    }
    return tx.Commit(ctx)
}
```

**表分类**:

- **公共表**（`public` schema）: `users`, `tenants`, `tenant_members`
- **租户表**（`tenant_*` schema）: `agents`, `skills`, `chat_conversations`, `memory_entries`, `knowledge_docs`, `mcp_servers`

**DDL 规则**:

- 编号迁移（`pkg/migration/sql/NNN_*.sql`）只操作 `public` schema，**禁止引用 tenant-only 表**
- Tenant DDL 放 `pkg/storage/postgres/tenant_schema.sql`，由 `ProvisionAllTenantSchemas` 幂等应用
- 新增列后必须紧跟 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` 做 backfill

### 3.3 Milvus Collection 命名

当前实现按数据域使用两套明确命名：

- Knowledge：每租户一个 `tenant_<tenant_id>_kb` collection，workspace 通过 partition 隔离（`pkg/storage/tenantnaming/milvus.go`）
- Memory 消息向量：`memory_<tenant_id>`；事实向量：`memory_facts_<tenant_id>`（`internal/memory/`）

tenant ID 中的短横线会替换为下划线。租户或用户数据删除时使用 filter / document ID 删除对应向量；不要把 `DropCollection` 当作常规数据删除操作。

---

## 4. 跨域调用与反向依赖

### 4.1 禁止模式

```go
// ✗ application 直接 import 兄弟 context 的 application
import "github.com/byteBuilderX/stratum/internal/memory/application"

func (s *AgentService) Execute(ctx, agentID) {
    memorySvc := memory_app.NewRecallService(...)  // 硬依赖
    memories := memorySvc.Recall(ctx, query, 5)
}
```

### 4.2 正确做法（消费者侧 Port）

**Step 1**: 消费方定义接口

```go
// agent/domain/port/memory.go
type MemoryRecaller interface {
    Recall(ctx context.Context, query string, k int) ([]string, error)
}
```

**Step 2**: 提供方实现（adapter 包装）

```go
// agent/infrastructure/memory_adapter.go
type memoryAdapter struct {
    svc *memory_app.RecallService
}
func (a *memoryAdapter) Recall(ctx, query, k) ([]string, error) {
    return a.svc.BuildContext(ctx, memory_app.RecallInput{Query: query, TopK: k})
}
```

**Step 3**: Wiring 注入

```go
// api/wiring/agent.go
memorySvc := memory_app.NewRecallService(...)
memoryPort := agent_infra.NewMemoryAdapter(memorySvc)
agentSvc := agent_app.NewAgentService(repo, memoryPort, ...)
```

### 4.3 Context Map（依赖方向守护）

CI 必须检查以下依赖方向，反向依赖一律拒绝：

```
agent    → memory, knowledge, skill, mcp, llmgateway
memory   → llmgateway
knowledge → llmgateway
skill    → llmgateway, mcp
mcp      → (无依赖)
iam      → (无依赖)
llmgateway → (无依赖)
platform → (无依赖，但被所有 context 依赖)
```

---

## 5. 错误处理分层

### 5.1 Domain 错误（sentinel error）

```go
// internal/agent/domain/errors.go
var (
    ErrAgentNotFound      = errors.New("agent not found")
    ErrInvalidSkillConfig = errors.New("invalid skill configuration")
    ErrTooManySkills      = errors.New("too many skills")
)
```

### 5.2 Infrastructure 错误翻译

```go
// internal/agent/infrastructure/persistence/agent_repo_pg.go
func (r *PgAgentRepo) Get(ctx, id) (*domain.Agent, error) {
    var row AgentRow
    err := r.pool.QueryRow(ctx, "SELECT ...").Scan(&row...)
    if err == pgx.ErrNoRows {
        return nil, domain.ErrAgentNotFound  // 翻译 pgx 错误到 domain 错误
    }
    if err != nil {
        return nil, fmt.Errorf("query agent: %w", err)
    }
    return rowToAgent(row), nil
}
```

### 5.3 Application 错误编排

```go
// internal/agent/application/agent_service.go
func (s *AgentService) Execute(ctx, agentID, input) (*Result, error) {
    agent, err := s.repo.Get(ctx, agentID)
    if err != nil {
        return nil, err  // 直接向上抛，不包装
    }
    if !agent.IsActive() {
        return nil, domain.ErrAgentInactive
    }
    // ...
}
```

### 5.4 Middleware 映射 HTTP 状态码

```go
// api/middleware/middleware.go + error_mapping.go
func ErrorHandler(logger *zap.Logger) gin.HandlerFunc {
    return func(c *gin.Context) {
        c.Next()
        for _, err := range c.Errors {
            switch {
            case errors.Is(err, agent_domain.ErrAgentNotFound):
                c.JSON(404, dto.ErrorResponse{Error: "agent not found"})
            case errors.Is(err, agent_domain.ErrInvalidSkillConfig):
                c.JSON(400, dto.ErrorResponse{Error: err.Error()})
            default:
                c.JSON(500, dto.ErrorResponse{Error: "internal server error"})
            }
            return
        }
    }
}
```

**Handler 层禁止行为**:

- ✗ 内联 `c.JSON(http.StatusXxx, dto.ErrorResponse{...})`
- ✗ 吞掉错误不上报

**Handler 层正确做法**:

```go
func (h *AgentHandler) GetAgent(c *gin.Context) {
    id := c.Param("id")
    agent, err := h.svc.Get(c.Request.Context(), id)
    if err != nil {
        c.Error(err)  // 交给 ErrorHandler 统一处理
        return
    }
    c.JSON(200, dto.AgentResponse{Agent: agent})
}
```

## 6. 安全规范

### 6.1 API Key 管理

**存储**:

- 租户 API Key 存储在 `tenants.settings.llm_api_keys` JSONB 字段
- 使用 AES-256-GCM 加密，密钥从 `JWTPrivateKeyPEM` 派生
- **绝对禁止**: 明文存储、存储在环境变量、提交到 git

**传输**:

- 前端 → 后端: HTTPS only
- 后端内部: 解密后的 key 仅在内存中，生命周期 ≤5 分钟（TTL cache）

**日志**:

- **禁止打印**: 解密后的 API key 明文、`password`、`token`、PII
- **允许打印**: 掩码后的 key（如 `sk-***xyz`）、model 名称、token 用量

**密钥源**:

- `JWTPrivateKeyPEM` 是唯一密钥源，泄露则所有租户 API key 暴露
- 必须通过 Vault / AWS Secrets Manager / Kubernetes Secret 管理
- **禁止入 git**，**禁止硬编码**

### 6.2 SQL 注入防护

**强制使用占位符**:

```go
// ✓ 正确
err := tx.QueryRow(ctx, "SELECT * FROM agents WHERE id = $1", agentID).Scan(...)

// ✗ 错误
query := fmt.Sprintf("SELECT * FROM agents WHERE id = '%s'", agentID)
err := tx.QueryRow(ctx, query).Scan(...)
```

**动态表名/列名**:

```go
// ✓ 使用 pgx.Identifier 防注入
schema := "tenant_" + tenantID
_, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %s, public",
    pgx.Identifier{schema}.Sanitize()))
```

### 6.3 SSRF 防护

**历史 HTTP Skill 执行决策（当前直接执行器已移除）**:

- 禁止访问内网 IP（`10.*.*.*`, `172.16.*.*`, `192.168.*.*`, `127.0.0.1`）
- 禁止访问元数据服务（`169.254.169.254`）
- URL scheme 白名单：`http`, `https`

**实现**:

```go
// pkg/httpclient/transport.go
func NewSafeDial() *net.Dialer {
    return &net.Dialer{
        Control: func(network, address string, c syscall.RawConn) error {
            host, _, _ := net.SplitHostPort(address)
            ip := net.ParseIP(host)
            if ip.IsPrivate() || ip.IsLoopback() {
                return errors.New("SSRF: private IP blocked")
            }
            return nil
        },
    }
}
```

### 6.4 JWT 安全

**算法**: RS256（非对称签名），禁止 HS256（对称密钥容易泄露）

**Claims 必填**:

```json
{
  "user_id": "uuid",
  "tenant_id": "uuid",
  "role": "admin|member",
  "exp": 1234567890,
  "iat": 1234567890
}
```

**验证流程**:

1. 验证签名（公钥验证）
2. 验证 `exp`（过期时间）
3. 验证 `tenant_id` 存在且租户未被禁用
4. 注入 `TenantContext` 到 gin.Context

---

## 7. 可观测性

### 7.1 日志规范

**初始化**: `observability.NewLogger(env)` — production → JSON，其余 → console+color

**字段分层**:

| 层 | 字段 | 注入位置 |
|----|------|----------|
| 链路 | `trace_id`, `tenant_id`, `user_id` | TraceMiddleware per-request |
| LLM | `model`, `provider`, `prompt_tokens`, `completion_tokens`, `latency_ms` | `llm.complete` 事件 |
| ReAct | `step`, `tool_name` | `react.llm` / `react.tool` 事件 |
| 访问 | `method`, `path`, `status`, `latency_ms`, `client_ip`, `ua` | TraceMiddleware after |

**事件命名**: `layer.operation`，如 `llm.complete`, `react.llm`, `react.tool`, `agent execution started`

**级别规则**:

| 级别 | 场景 | 自动附加 |
|------|------|----------|
| DEBUG | 开发调试，production 不输出 | - |
| INFO | 正常业务路径（HTTP < 400，LLM 成功，ReAct step） | - |
| WARN | 可预期异常（HTTP 4xx，重试中） | - |
| ERROR | 需处理异常（HTTP 5xx，外部调用失败） | stacktrace |

**安全红线**:

- **禁止记录**: `password`, `token`, `api_key`, PII
- **禁止打印**: 原始 HTTP response body（只记 status code + model）

### 7.2 Trace ID 统一

**生成**: UUID v7（时间有序，B-tree 友好）

**传播**:

1. `TraceMiddleware` 从请求头 `X-Request-ID` 读取，不存在则生成
2. 注入到 `gin.Context` 和 `context.Context` 的 `SpanContext`
3. 业务层通过 `observability.SpanFromContext(ctx)` 读取同一 trace_id
4. 响应头 `X-Request-ID` 回传给客户端

**日志字段统一**: 所有日志使用 `trace_id`（不再用 `request_id`）

### 7.3 指标规范

**Prometheus 指标**:

- `http_requests_total{method,path,status}` — HTTP 请求计数
- `llm_requests_total{provider,model,status}` — LLM 调用计数
- `llm_request_duration_seconds{provider,model}` — LLM 延迟
- `llm_tokens_total{provider,model,type="prompt|completion"}` — Token 用量
- `memory_pipeline_total{stage="embed|enrich",status}` — Memory pipeline 计数
- `skill_executions_total{type="http|llm|code",status}` — 旧 Skill 执行计数器仍在 metrics registry 中注册，当前 instruction Skill 路径没有递增调用点

**高基数标签截断**:

- `path` 超过 64 字符截断为 `<truncated>`
- `tool_name` / `skill_id` 白名单外的截断

---

## 8. 前端编码规范

### 8.1 常量管理

**所有行为常量集中在 `web/src/constants/index.ts`**:

```js
// API / 网络
export const API_DEFAULT_TIMEOUT_MS = 30000;
export const AGENT_EXEC_TIMEOUT_MS = 300000;

// 分页
export const DEFAULT_PAGE_SIZE = 20;
export const PAGE_SIZE_OPTIONS = [10, 20, 50, 100];

// MCP
export const MCP_DEFAULT_TIMEOUT_SEC = 30;
export const MCP_MAX_TIMEOUT_SEC = 300;
```

**禁止**: 页面内直接硬编码超时/分页/限流等数字

### 8.2 API 调用

**普通请求统一走 `web/src/services/client.ts` 的 axios 实例，SSE 统一走该文件的 `streamApiEvents`**:

```js
// ✓ 正确
import api from '../services/api';
const response = await api.get('/agents');

// ✗ 错误
const response = await fetch('/api/agents');  // 禁止裸 fetch
```

**错误处理**:

```js
try {
  const response = await api.post('/agents', payload);
  message.success('创建成功');
} catch (err) {
  message.error(err.response?.data?.error || '操作失败');
}
```

### 8.3 状态管理

**禁止**: 跨 `pages/` 目录导入组件

**页面组件**: ≤200 行，超出提取到 `hooks/` 或 `components/`

**useEffect 依赖**: 必须完整，避免 ESLint 警告

**异步 effect 清理**:

```js
useEffect(() => {
  let cancelled = false;
  fetchData().then(data => {
    if (!cancelled) setState(data);
  });
  return () => { cancelled = true; };
}, []);
```

### 8.4 UI 规范

**用户可见字符串**: 全部中文

**弹窗**: 使用 `message` / `Modal.confirm`，禁止 `alert()` / `confirm()`

**Token 安全**: 禁止存 `localStorage`，用 `httpOnly` cookie 或内存 Context

**空状态**: 所有列表必须有 Empty + 引导操作

**危险操作**: 删除/停用/清空用 `Modal.confirm`，描述后果

---

## 9. 测试策略

### 9.1 单元测试

**覆盖率**: ≥80%

**表驱动测试**:

```go
func TestAgentService_Create(t *testing.T) {
    tests := []struct {
        name    string
        input   CreateAgentInput
        wantErr error
    }{
        {"valid", CreateAgentInput{Name: "test"}, nil},
        {"empty name", CreateAgentInput{Name: ""}, domain.ErrInvalidName},
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            svc := NewAgentService(mockRepo, ...)
            _, err := svc.Create(context.Background(), tt.input)
            if !errors.Is(err, tt.wantErr) {
                t.Errorf("got %v, want %v", err, tt.wantErr)
            }
        })
    }
}
```

**Mock 所有外部依赖**: DB, Redis, Milvus, HTTP client, LLM client

**完整套件开 `-race`**: `go test -race ./...`

### 9.2 集成测试

**单独 build tag**: `//go:build integration`

**需要真实依赖**: PostgreSQL, Redis, NATS, Milvus

**清理策略**: 每个测试独立 tenant schema，测试后 DROP

### 9.3 契约测试

**API 向后兼容守护**: `api/http/contract_test.go` + `api/http/testdata/contracts/*.golden.json`

**规则**:

- 响应体字段不可删除，只可新增
- 错误码不可变更
- HTTP 路径不可变更

---

## 10. CI 静态守护

### 10.1 go-arch-lint 规则

```yaml
# .go-arch-lint.yml
rules:
  - name: "domain no external deps"
    from: "internal/*/domain/**"
    should_not_import:
      - "github.com/jackc/pgx"
      - "github.com/redis/go-redis"
      - "go.uber.org/zap"
    except:
      - "pkg/constants"

  - name: "handler no infrastructure"
    from: "api/http/handler/**"
    should_not_import:
      - "internal/*/infrastructure"
      - "pkg/tenantdb"
      - "pkg/storage/postgres"
```

### 10.2 depguard 配置

```yaml
# .golangci.yml
linters-settings:
  depguard:
    rules:
      handler:
        files: ["api/http/handler/*.go"]
        deny:
          - pkg: "github.com/jackc/pgx"
          - pkg: "pkg/tenantdb"
          - pkg: "pkg/crypto"
```

### 10.3 CI Pipeline

```yaml
# .github/workflows/ci.yml
- name: Lint
  run: |
    go vet ./...
    golangci-lint run
    go-arch-lint

- name: Test
  run: |
    go test -short ./...
    go test -race -timeout 30s ./...

- name: Contract Test
  run: go test -tags=contract ./api/http/...
```

---

## 附录：关键文件清单

### 配置文件

- `docs/engineering-standards.md` — 项目规范总入口
- `.golangci.yml` — Linter 配置
- `.go-arch-lint.yml` — 架构规则守护

### 核心基础设施

- `pkg/storage/postgres/tenant.go` — 租户 schema 切换
- `pkg/observability/trace.go` — Trace ID 生成与传播
- `api/middleware/trace.go` — TraceMiddleware
- `api/middleware/middleware.go` / `error_mapping.go` — 错误处理中间件与状态码映射

### Wiring 容器

- `api/wiring/wiring.go` — Container、BuildContainer 与逆序 Shutdown
- `api/wiring/<ctx>.go` — 各 context 的组装逻辑

### 测试

- `api/http/contract_test.go` — API 契约测试
- `api/http/testdata/contracts/*.golden.json` — 契约快照

---

**最后更新**: 2026-07-16
**维护者**: 项目架构组
