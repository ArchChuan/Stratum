# Stratum 平台助手统一 MCP 控制面设计

**状态：已确认，待用户复核文档**

## 1. 背景

当前 Stratum 平台助手与普通 Agent 共用 Agent Loop、会话、执行记录和模型调用，但平台助手的官方文档检索、
租户诊断和资源变更提案由代码直接注入的 internal tools 提供。普通 Agent 则通过租户 MCP 配置、工具绑定、
MCP Client Manager 和 Tool Resolver 调用工具。两条工具链使平台助手绕过了普通 Agent 使用的 MCP 基础设施，
也让工具发现、连接治理、权限、trace 和部署形成两套实现。

本设计把平台助手定位为“使用统一 Agent/MCP 基础设施、由平台托管且拥有更高资源管理权限的特殊 Agent”。
平台助手不再获得专用 internal tools，而是绑定平台维护的多租户共享 Platform MCP Server，通过受控身份委托
调用 Stratum 现有 HTTP API。

## 2. 已确认决策

| 主题 | 决策 |
| --- | --- |
| Agent 定位 | 平台助手与普通 Agent 使用同一数据模型和执行基础设施 |
| 工具基础设施 | 平台助手与普通 Agent 共用 Tool Resolver、MCP Client Manager、策略、trace 和审计 |
| 内置 MCP 绑定 | 只有平台助手可以绑定 Stratum Platform MCP；普通 Agent 强制拒绝 |
| Platform MCP 形态 | 同仓库独立二进制、独立 Deployment、多租户共享、无状态 |
| 业务入口 | Platform MCP 通过 Stratum HTTP API 执行业务，不直连数据库或领域 infrastructure |
| 权限差异 | 普通 Agent 不具备平台资源 CRUD；平台助手按角色、风险和审批获得受控 CRUD |
| 高风险操作 | 删除、密钥新增和轮换必须经过管理员二次确认 |
| 密钥处理 | 密钥不进入模型、MCP 参数、对话、SSE、trace、提案或日志 |
| 监控范围 | 只诊断当前租户业务资源，不读取主机、Kubernetes、全局日志或其他租户 |
| 领域范围 | Agent、Skill、MCP、Knowledge、Model、Workflow、Memory |
| IAM | 本次明确排除，不实现、隐藏或预留 IAM 管理工具 |
| 前端入口 | Agent 管理下独立“平台助手”和“Agent 列表”两个 Tab |
| 可观测性 | Platform MCP 接入同一日志、OTEL、Prometheus、Grafana 和告警体系 |
| 交付 | 分阶段交付，每阶段独立验收 |

## 3. 目标与非目标

### 3.1 目标

- 删除平台助手工具旁路，使所有 Agent 工具调用经过统一 MCP 基础设施。
- 为每个租户幂等注册系统托管 MCP，并只绑定平台助手。
- 即使用户复制内置 MCP 的名称、URL 或工具定义，也不能获得平台权限。
- Platform MCP 只通过 Stratum HTTP API 访问业务能力，保留既有 handler、application、domain 和 repository 边界。
- 以短期身份委托、服务身份、风险策略和审批状态共同决定权限。
- 为创建、更新、删除和凭据变更提供可审计、可恢复、可回读的执行链。
- Platform MCP 可独立扩缩容和观测，并与一次 Agent execution 完整关联。
- 平台助手前端入口独立，但继续复用普通 Agent 的会话和执行数据模型。

### 3.2 非目标

- 不为普通 Agent 提供 Stratum 平台资源 CRUD。
- 不接入 IAM 成员邀请、角色修改、成员移除或邀请撤销。
- 不允许任意 HTTP、URL、method、header、SQL 或 Shell 工具。
- 不读取 PostgreSQL、Redis、NATS、Milvus、Pod、节点、主机或全局告警作为助手诊断数据。
- 不让 Platform MCP 直连业务数据库或兄弟 context infrastructure。
- 不把用户登录 Token 转发给 Platform MCP，也不给 Platform MCP 永久管理员 Token。
- 不在请求路径静默创建、修复或升级系统托管 MCP。
- 不长期保留 internal tool fallback。

## 4. 仓库现状与约束

### 4.1 当前实现

- `internal/agent/application/system_assistant_tools.go` 定义平台助手专用 internal tools。
- `api/wiring/system_assistant.go` 把诊断工具直接适配到 Agent runtime。
- 普通 Agent 使用 `mcp_configs`、`agent_mcp_tool_links`、MCP registry、client manager 和 tool policy。
- `resource_change_proposals` 已具备提案、确认、应用、冲突和未知结果基础。
- HTTP API 使用 RS256 Bearer JWT，经 `JWTMiddleware`、`InjectTenantContext` 和角色 middleware 注入租户边界。
- 当前 JWT claims 不区分 audience、token usage、tool、route、resource、execution 或 approval。
- Helm 已有 ServiceAccount、ClusterIP、NetworkPolicy 和 cert-manager 安装入口，但现有 NetworkPolicy 边界较宽，
  cert-manager issuer 主要服务公网 ACME，不足以直接承担内部 workload identity。

### 4.2 强制项目约束

- tenant-scoped repository 必须经过 `execTenant(ctx, tenantID, fn)`。
- 跨 context 依赖通过消费方 port 和 wiring ACL，不允许 application 导入兄弟 infrastructure。
- 权限、租户状态和外部依赖失败必须 fail closed。
- 持久化失败和失败状态写回失败必须向上传播。
- 删除业务数据遵循当前硬删除规则；Milvus 删除使用 delete-by-filter，禁止 DropCollection。
- 认证、租户、外部依赖和 MCP 改动需要真实浏览器、API、数据库和 stateful soak 验证。

## 5. 总体架构

```text
用户
  -> Stratum 平台助手
  -> 通用 Agent Loop
  -> 通用 Tool Resolver
  -> 通用 MCP Client Manager
  -> Stratum Platform MCP Server
  -> Stratum HTTP API
  -> Handler -> Application -> Domain Port -> Infrastructure
```

平台助手不再额外注入专用工具。现有能力迁移为 Platform MCP tools，后续工具也使用相同目录、策略、调用、
trace、超时、重试和错误链路。

### 5.1 代码边界

```text
cmd/server/main.go          Stratum Backend
cmd/platform-mcp/main.go    Platform MCP Server
```

Platform MCP 可以依赖：

- MCP 协议适配；
- Stratum HTTP API client；
- delegation token 和 mTLS；
- 工具合同、安全 DTO；
- `pkg/observability`、HTTP client 和通用常量。

Platform MCP 不得 import `internal/<ctx>/application` 或 `internal/<ctx>/infrastructure`。这个依赖约束保证它不能
绕过 HTTP API 直接执行领域能力。

## 6. 系统托管 MCP 身份与租户生命周期

### 6.1 数据标识

`mcp_configs` 增加受保护字段：

```text
system_key       nullable, tenant 内唯一
management_mode  tenant_managed | platform_managed
```

内置记录使用：

```text
system_key = stratum-platform-mcp
management_mode = platform_managed
```

普通 MCP CRUD 必须拒绝客户端提交或修改这些字段。URL、名称、工具名和 capabilities 都不能代表系统身份。

### 6.2 Provision

租户 provision 在激活前幂等执行：

1. 创建或验证唯一平台助手实例；
2. 创建或验证唯一系统托管 Platform MCP 配置；
3. 建立平台助手到全部允许工具的系统绑定；
4. 写入平台维护的工具风险策略；
5. 校验系统身份和绑定一致性。

任一步失败时租户不得激活。历史租户通过显式升级任务补齐；普通请求路径不得修复。

### 6.3 防复制原则

用户复制同一 URL 或名称创建的记录仍是 `tenant_managed`。MCP Client Manager 只能在以下全部成立时加载平台
服务身份并请求 invocation token：

```text
agent.system_key == stratum-platform-assistant
+ server.system_key == stratum-platform-mcp
+ server.management_mode == platform_managed
+ 系统绑定关系有效
```

绝对禁止根据 URL、名称、transport 或工具名附加平台凭据。

## 7. 身份委托与 HTTP API 鉴权

### 7.1 双 Token

Agent Runtime 签发 `mcp_invocation` token：

```text
aud = stratum-platform-mcp
tenant_id, user_id, agent_id, server_id
tool_name, execution_id, approval_id
jti, issued_at, expires_at
```

它只能调用 Platform MCP，不能直接调用 Stratum API。

Platform MCP 使用 mTLS 服务身份和 invocation token 调用内部 Token Exchange。Backend 重新查询成员角色、受管
Agent/MCP 身份、绑定、工具合同、审批和资源版本后，签发 `mcp_api_delegation` token：

```text
aud = stratum-api
sub, tenant_id, current_role
agent_id, server_id, tool_name, execution_id
http_method, path_template, resource_id
approval_id, jti, issued_at, expires_at
```

### 7.2 API 入口

现有认证层扩展为两种明确 token usage：

```text
普通 access token
  -> 现有 JWTMiddleware

mcp_api_delegation
  -> DelegationJWTMiddleware
  -> DelegatedScopeMiddleware
  -> InjectTenantContext
  -> 现有角色 middleware
  -> 原有 Handler/Application
```

`DelegatedScopeMiddleware` 必须比对 method、路由模板、资源 ID、租户、tool 和 approval。delegation token 不能用于
其他 API、其他资源、其他租户或其他工具。

### 7.3 独立 Token Service

现有用户 `JWTService` 不扩展为兼容所有 token。新增独立 delegation claims 和 service，严格验证：

- RS256；
- issuer；
- audience；
- token usage；
- issued-at、expiry、jti；
- tool、route、resource 和 approval scope。

角色 claims 不是最终事实。写操作和审批确认前必须从 IAM repository 重新读取当前成员角色。

## 8. 网络、TLS 与 Workload Identity

### 8.1 部署身份

Backend 和 Platform MCP 使用独立 ServiceAccount，默认禁用 Kubernetes API token 自动挂载。Platform MCP 不获得
读取 Secret、Pod、数据库或基础设施资源的 RBAC 权限。

### 8.2 内部服务

```text
Service/stratum                 ClusterIP :8080 public application API
Service/stratum-internal        ClusterIP :8443 mTLS internal API
Service/stratum-platform-mcp    ClusterIP :8443 mTLS MCP endpoint
```

`stratum-internal` 和 `stratum-platform-mcp` 不创建 Ingress、NodePort 或 LoadBalancer。

### 8.3 内部 CA

cert-manager 创建独立内部 CA issuer，并为两个 workload 签发短周期证书。证书包含：

```text
spiffe://stratum.local/ns/stratum/sa/stratum-backend
spiffe://stratum.local/ns/stratum/sa/stratum-platform-mcp
```

服务端不能只验证“同一 CA”，必须精确验证 DNS SAN、EKU 和预期 SPIFFE URI。证书通过动态加载器原子轮换；新证书
验证失败时继续使用旧证书，并产生告警。

### 8.4 NetworkPolicy

- Agent Runtime/Backend 只可访问 Platform MCP 8443。
- Platform MCP 只可访问 DNS、Backend internal 8443 和 OTEL Collector。
- Platform MCP 不可访问 PostgreSQL、Redis、NATS、Milvus 或 Kubernetes API。
- Backend internal 8443 只接受 Platform MCP Pod selector。

NetworkPolicy 是减小攻击面，不替代 mTLS、JWT 和业务授权。

## 9. 工具合同和风险策略

每个工具使用代码维护的封闭合同：

```go
ToolContract{
    ToolName:     "stratum_agent_delete",
    HTTPMethod:   "DELETE",
    PathTemplate: "/agents/:id",
    RiskLevel:    Destructive,
    MinimumRole:  Admin,
    Approval:     SecondConfirmation,
}
```

模型不能提供 method、path、header 或任意 URL。Platform MCP 根据业务参数和合同构造请求。

风险等级：

| 等级 | 示例 | 规则 |
| --- | --- | --- |
| `read` | 列表、详情、租户业务状态 | 按当前角色直接执行 |
| `write_reversible` | 创建、普通配置更新 | 创建提案，管理员确认 |
| `destructive` | 删除、清空 Memory | 影响分析和管理员二次确认 |
| `credential` | 新增、轮换 Provider/MCP 密钥 | 安全表单和管理员二次确认 |
| `forbidden` | 修改系统身份、跨租户、IAM | 永久拒绝 |

平台代码定义每个系统工具不可降低的风险下限；租户策略只能保持或提高风险，不能降低。MCP Server 返回的工具
描述不能自行声明可信风险等级。

## 10. 提案、审批与密钥

### 10.1 提案模型

扩展现有 `resource_change_proposals`，至少支持：

```text
tenant/conversation/execution/agent/proposer/confirmer
tool/resource/operation/risk
safe_payload/secret_session_id/baseline_fingerprint/impact_snapshot
approval_stage/status/expires/idempotency/lease
safe_error/result_reference/timestamps
```

状态机：

```text
draft -> validating -> ready_for_review -> first_confirmed
      -> impact_checking -> ready_for_final_confirmation
      -> applying -> applied

terminal/side states:
invalid, rejected, expired, stale, failed, unknown_outcome, cancelled
```

`write_reversible` 需要一次确认；`destructive` 和 `credential` 需要两阶段确认。第二次确认前重新计算影响范围、角色、
资源指纹和审批有效期。状态转换由硬编码表控制。

### 10.2 密钥会话

密钥不经 Agent 或 MCP：

```text
助手创建 credential proposal
-> 前端打开专用安全表单
-> 用户直接提交 Stratum API
-> API 加密保存短期 secret session
-> proposal 只保存 secret_session_id
-> 二次确认
-> application 原子替换正式凭据
-> 关闭/刷新旧 client
-> 清理临时 secret
```

临时 secret 绑定 tenant、user、proposal 和短 TTL。新凭据验证失败时保留旧凭据。日志和审计只记录操作类型与
secret reference，不记录值或可逆摘要。

## 11. 分阶段工具范围

### 11.1 阶段一：统一控制面

- Platform MCP 独立服务、MCP transport 和工具目录；
- 系统托管注册和平台助手绑定；
- mTLS、workload identity、双 token 和 Token Exchange；
- 风险策略、提案审批、幂等和 replay 防护；
- 迁移官方文档检索与租户业务诊断 internal tools；
- 完成可观测性、部署和安全失败测试。

### 11.2 阶段二：核心资源

- Agent：list/get/create/update/delete；
- Skill：list/get/create/update draft/publish/archive/delete；
- MCP：list/get/create/update/test/set credential/rotate credential/delete；
- Knowledge：list/get/create/update/ingestion status/delete；
- Model/Provider：list/create/update/test/set credential/delete。

平台内置 Agent、Skill、MCP 和 Knowledge 资源不可被租户修改或删除。Knowledge 删除向量数据必须
delete-by-filter。Provider 删除前检查关联模型和 Agent。

### 11.3 阶段三：Workflow 与 Memory

- Workflow：list/get/create/update draft/validate/publish/run/cancel/delete；
- Memory：search/get/create/update/delete/clear scope。

Workflow 的发布和运行分别审批，运行绑定版本、输入摘要、预算、幂等键和副作用节点。Memory 按本人、Agent 和
租户管理 scope 隔离；清空 scope 必须展示数量和不可恢复后果。

### 11.4 明确排除 IAM

Platform MCP 不注册任何 IAM 管理工具。模型遇到相关请求只引导用户前往租户成员管理页面，不能用通用 HTTP 工具
绕过限制。

## 12. 前端信息架构

```text
Agent 管理
  |- 平台助手
  |   |- 固定使用 Stratum 平台助手
  |   |- 新建会话
  |   |- 历史会话列表
  |   |- 对话执行
  |   |- 资源变更提案
  |   `- 平台助手设置
  `- Agent 列表
      |- 普通 Agent 列表
      |- 创建、编辑普通 Agent
      `- 选择 Agent 后进入对话
```

平台助手 Tab 不显示 Agent 选择器，创建会话时固定使用系统助手 ID。底层继续复用 `agents`、会话、消息、执行、
trace、proposal、AgentService、SSE 和 MCP infrastructure。

普通 Agent 列表必须由后端查询契约排除系统助手，避免只在前端隐藏导致分页、搜索和权限不一致。

建议路由：

```text
/agents/assistant
/agents/assistant/settings
/agents
/agents/:id/chat
/agents/:id/edit
```

## 13. 部署架构

Platform MCP 是同仓库独立镜像、独立 Deployment、平台级多租户共享服务。生产默认至少两个副本，并可水平扩容。

```text
Agent Runtime -> Platform MCP ClusterIP -> Backend internal ClusterIP
```

Platform MCP 不保存审批、幂等、密钥或业务状态。持久状态仍由 Stratum 数据库和既有 secret store 管理。

健康端点：

- `/healthz`：仅进程和 HTTP Server 存活；
- `/readyz`：证书/CA 已加载、Token Exchange 可达、工具合同已加载、replay store 可用；
- `/metrics`：仅内部 Prometheus 抓取。

发布顺序：

1. Backend 发布 delegation 和 internal listener；
2. 发布 CA、证书、Service 和 NetworkPolicy；
3. 发布 Platform MCP，暂不绑定租户；
4. 安全失败与连通性测试；
5. 测试租户绑定和真实 E2E；
6. 分批补齐历史租户；
7. 新租户默认 provision；
8. 删除旧 internal tools。

## 14. 可观测性

Platform MCP 复用 `pkg/observability`、Zap、OTEL Collector、Prometheus、Grafana 和现有告警体系，并使用独立
`service.name=stratum-platform-mcp`。

完整 trace：

```text
agent.tool_call
  -> mcp.client.call
  -> platform_mcp.tool
     -> platform_mcp.token_exchange
     -> platform_mcp.stratum_api
        -> backend.handler
        -> application
        -> repository
```

传播 W3C `traceparent`/`tracestate`；baggage 不携带用户输入、Memory、Token 或密钥。Platform MCP 不直接写
`agent_traces`，Agent Runtime 接收 MCP result 后写 Agent trace/artifact，Backend 写业务审计。

建议指标：

```text
platform_mcp_requests_total
platform_mcp_request_duration_seconds
platform_mcp_in_flight_requests
platform_mcp_auth_denials_total
platform_mcp_token_exchange_total
platform_mcp_api_requests_total
platform_mcp_replay_denials_total
platform_mcp_unknown_outcomes_total
platform_mcp_tls_certificate_expiry_seconds
platform_mcp_tool_contract_mismatches_total
```

Prometheus labels 仅使用有限枚举，如 tool class、risk、outcome 和 status class；禁止 tenant、user、resource、
execution、proposal 或原始错误作为 label。

告警覆盖副本不可用、readiness、Token Exchange 错误、API 5xx/超时、授权/重放拒绝激增、unknown outcome、
合同不一致、证书轮换失败和延迟超预算。

## 15. 失败语义

- 身份、租户、成员角色或系统绑定读取失败：拒绝执行；
- MCP 连接失败：显式报告能力不可用，不回退 internal tool；
- 单个诊断项失败：记录证据缺口，不判断为健康；
- 审批过期或资源变化：`stale`；
- 可确定的 API 失败：`failed`；
- 超时后无法确认副作用：`unknown_outcome`，禁止自动重试；
- 凭据验证失败：保留旧凭据；
- 审计或状态持久化失败：向上传播，不返回伪成功；
- Platform MCP 不可用：普通 Agent 和非平台管理业务不受影响。

## 16. 测试与验收

### 16.1 安全失败测试

必须覆盖：

- 复制内置 MCP URL/名称不能获得权限；
- 普通 Agent 伪造绑定不能调用平台工具；
- 普通 MCP CRUD 不能写系统身份字段；
- invocation token 不能调用 Stratum API；
- delegation token 不能跨 method、path、resource、tool 或 tenant；
- token 重放和过期被拒绝；
- 管理员降级后旧审批失效；
- 资源变化后审批转 `stale`；
- 密钥不进入对话、SSE、MCP 参数、trace、proposal、日志和错误；
- Platform MCP 无法直连数据库和基础设施；
- 系统托管 MCP 不能被租户修改、停用或删除；
- 平台助手和普通 Agent 经过同一 Tool Resolver/MCP Client Manager。

### 16.2 每阶段门禁

- Go 单元、集成、`go vet`、`go test -race`；
- API contract golden；
- 前端 lint、测试和 build；
- `make risk-guardrails`；
- 真实 PostgreSQL 租户隔离、回滚和失败传播；
- headless Chromium UI/API/数据库对账；
- `make e2e-system-short`；
- 600 秒 test profile stateful soak；
- attestation check，无 skipped/unreconciled capability。

阶段一必须真实验证：

```text
平台助手新建会话
-> 通用 Tool Resolver
-> MCP client
-> mTLS Platform MCP
-> Token Exchange
-> delegated Stratum API
-> DB/诊断结果
-> MCP result
-> Agent artifact
-> UI 展示
```

## 17. 证据与边界

| 主张 | 项目证据 | Obsidian 输入 | 外部规范 | 边界 |
| --- | --- | --- | --- | --- |
| 当前平台助手工具存在旁路 | `system_assistant_tools.go`、wiring、MCP registry | 已验证 Agent 分层原则 | MCP Tools 2025-06-18、2025-11-25、2026-07-28 已核验 | 当前仓库事实优先 |
| 权限和审批属于 Harness/业务层 | proposal service、tool policy、role middleware | “Agent 系统应分离执行循环、编排、状态、记忆与治理” verified | MCP Tools 要求 server 实施 access control；Authorization 负责 HTTP OAuth 边界 | MCP 协议本身不替代业务授权 |
| AIOps 必须区分事实、调查、执行和治理 | 当前诊断 evidence/gap 结构 | 对应笔记为 provisional，仅作设计线索 | 不作为关键外部事实 | 本设计仅做租户业务诊断 |
| 共享服务不能依赖 URL 代表身份 | 当前 mcp config 可由租户创建 | 最小工具权限原则 | 三个版本均要求 OAuth resource/audience 绑定；当前安全指南仍记录 token passthrough 风险 | 使用系统身份、mTLS 和 scoped JWT |

已核验的一手来源与逐项主张矩阵见
`docs/evidence/platform-mcp-protocol-2026-07-29.md`。兼容基线固定为 `2025-06-18`，并比较：

- MCP Authorization，固定版本 `2025-06-18`：
  <https://modelcontextprotocol.io/specification/2025-06-18/basic/authorization>
- MCP Tools，固定版本 `2025-06-18`：
  <https://modelcontextprotocol.io/specification/2025-06-18/server/tools>
- MCP Authorization/Tools，过渡版本 `2025-11-25`；
- MCP Authorization/Tools/Streamable HTTP，核验时 current 版本 `2026-07-28`。

`/specification/latest` 在 2026-07-29 跳转到 `2026-07-28`。该版本把 Streamable HTTP 改为无 session 的
per-request metadata 合同，要求每个 POST 带协议版本和 mirrored method/name headers，与 2025 session-based wire
contract 不兼容。因此阶段一显式实现 `2025-06-18`：初始化协商该版本，后续请求传播协议版本和 session header；
Authorization、audience/resource、token passthrough、tool input/access-control 仍按证据矩阵中的严格决策执行。
升级 `2026-07-28` 必须作为独立兼容性变更，禁止静默混用两个 transport 合同。

## 18. 完成标准

本设计完成的判定不是“出现了一个 MCP endpoint”，而是同时满足：

- 平台助手不再注入专用 internal tools；
- 平台助手和普通 Agent 确实使用统一 MCP infrastructure；
- 只有系统托管平台助手可以获得 Platform MCP 委托；
- 复制配置、伪造绑定、跨租户、跨工具和重放均被真实测试拒绝；
- Platform MCP 只通过 Stratum HTTP API 执行业务；
- 高风险和密钥操作经过二次确认且秘密不进入 Agent 链路；
- 独立前端 Tab 使用同一 Agent/会话/执行数据模型；
- 部署、证书、NetworkPolicy、trace、指标、告警和 E2E 一并通过验收。
