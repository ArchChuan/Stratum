# Stratum Go 包代码架构图

本目录记录 `internal/...` 与 `pkg/...` 的 Go 包架构图。当前源码有 62 个含 Go 文件的包：58 个有现行包图，4 个 evaluation 包尚未生成独立图。另有 5 个旧 Skill package 页面保留为已移除路径索引，不计入当前包数。

- 生成范围：`internal/...`、`pkg/...`
- 当前 Go 包总数：62（58 份现行包图 + 4 个尚未补图的 evaluation 包）
- 不包含：`api/`、`cmd/`、`config/`、前端及工作树副本
- 历史生成清单：[package-manifest.txt](package-manifest.txt)（仍记录生成时的 63 个路径，不是当前完整清单）

## internal/evaluation（待补独立包图）

- `internal/evaluation/domain` / `domain/port`：评估 suite、run、job、优化候选、实验、反馈及仓储契约。
- `internal/evaluation/application`：suite 发布、异步 job worker、评估执行、优化、实验阶段判定与反馈编排。
- `internal/evaluation/infrastructure/persistence`：tenant PostgreSQL 持久化。

## internal/agent

- [`internal/agent/application`](internal-agent-application.md) — 该包编排 Agent 的注册、配置 CRUD、同步/流式执行、上下文预算、会话与执行追踪持久化，并把领域端口、执行图和可观测能力组合为应用用例。
- [`internal/agent/application/a2a`](internal-agent-application-a2a.md) — 该包提供内存态的 Agent-to-Agent 协作协议，包括消息传输、能力发现、任务协商、协作会话和多种执行计划编排策略。
- [`internal/agent/application/graph`](internal-agent-application-graph.md) — 该包实现通用状态图运行器，以及 Agent 的 ReAct 和 Plan-Execute 两套确定性执行图、循环上下文压缩、节点重试与 checkpoint 写入逻辑。
- [`internal/agent/domain`](internal-agent-domain.md) — 该包定义 Agent bounded context 的核心领域模型、执行状态、计划步骤、工具观察、追踪事件、checkpoint 与领域错误。
- [`internal/agent/domain/port`](internal-agent-domain-port.md) — 该包声明 Agent 上下文的消费者侧出向契约，覆盖能力路由、LLM/技能/工具、历史压缩、知识与 RAG、记忆、租户解析和各类仓储。
- [`internal/agent/infrastructure/capability`](internal-agent-infrastructure-capability.md) — 该包实现 Agent capability 端口的 LLM 路由网关、LLM adapter 与可选历史摘要器。
- [`internal/agent/infrastructure/persistence`](internal-agent-infrastructure-persistence.md) — 该包提供 Agent 配置、聊天、执行、checkpoint、工具追踪、追踪事件、技能查找和租户设置的 PostgreSQL 实现，并统一租户 schema 执行与敏感数据脱敏。

## internal/iam

- [`internal/iam/application`](internal-iam-application.md) — 该包编排 IAM 用例：平台租户管理、RS256 令牌、用户入驻与租户成员/设置管理，并通过领域端口隔离持久化和清理设施。
- [`internal/iam/domain`](internal-iam-domain.md) — 该包定义 IAM 的领域数据、系统角色推导规则、租户管理输入模型和领域错误，不承担外部 IO。
- [`internal/iam/domain/port`](internal-iam-domain-port.md) — 该包声明 IAM 消费方所需的持久化、OAuth、会话、令牌、租户 Schema/向量清理与缓存失效契约。
- [`internal/iam/infrastructure/hermes`](internal-iam-infrastructure-hermes.md) — 该包封装 Hermes 的 NATS 事件客户端，提供连接、JSON 事件发布、按事件类型订阅分发和关闭能力。
- [`internal/iam/infrastructure/oauth`](internal-iam-infrastructure-oauth.md) — 该包实现 GitHub OAuth 端口，负责授权码换取访问令牌并读取 GitHub 用户资料。
- [`internal/iam/infrastructure/persistence`](internal-iam-infrastructure-persistence.md) — 该包以 PostgreSQL（以及令牌撤销所需的 Redis）实现 IAM 租户、入驻、成员设置、Schema 清理和刷新令牌存储。

## internal/knowledge

- [`internal/knowledge/application`](internal-knowledge-application.md) — 该包编排知识工作区、异步文档摄取和 RAG 查询，用端口连接解析、切块、嵌入、向量与 PostgreSQL 存储。
- [`internal/knowledge/domain`](internal-knowledge-domain.md) — 该包定义知识库文档、分块、工作区聚合、配置白名单/默认值及领域错误。
- [`internal/knowledge/domain/port`](internal-knowledge-domain-port.md) — 该包声明知识上下文对文档解析、切块、嵌入、向量索引和工作区/文档/分块仓储的出向契约。
- [`internal/knowledge/infrastructure/document`](internal-knowledge-infrastructure-document.md) — 该包实现文档解析能力，按文件扩展名或 MIME 提示从 PDF、DOCX、Markdown 或纯文本中提取统一文本。
- [`internal/knowledge/infrastructure/persistence`](internal-knowledge-infrastructure-persistence.md) — 该包用 PostgreSQL 实现知识工作区、文档与分块仓储，并协调删除租户对应的 Milvus 集合。

## internal/llmgateway

- [`internal/llmgateway/application`](internal-llmgateway-application.md) — 该包提供模型目录查询用例，向上层暴露稳定的聊天模型与嵌入模型列表。
- [`internal/llmgateway/domain`](internal-llmgateway-domain.md) — 该包定义统一 LLM/Embedding 请求响应、消息、工具调用、提供商类型、租户设置以及上下文中的补全器契约。
- [`internal/llmgateway/domain/port`](internal-llmgateway-domain-port.md) — 该包声明模型目录与租户 LLM 设置读取端口，为应用服务和基础设施租户网关解析提供边界。
- [`internal/llmgateway/infrastructure`](internal-llmgateway-infrastructure.md) — 该包实现统一 LLM 网关、OpenAI 兼容 HTTP 客户端、Qwen/Zhipu 构造、静态模型目录与租户网关 TTL 缓存。
- [`internal/llmgateway/infrastructure/embedding`](internal-llmgateway-infrastructure-embedding.md) — 该包把 llmgateway 的嵌入客户端包装成面向文本列表的嵌入服务，并固定或覆盖所用模型。

## internal/mcp

- [`internal/mcp/application`](internal-mcp-application.md) — 该包编排 MCP 服务器生命周期、能力发现、技能注册与状态统计，并用结构化日志记录连接变更。
- [`internal/mcp/domain`](internal-mcp-domain.md) — 该包定义 MCP 服务器配置、认证/重试、工具、资源、技能摘要及领域错误。
- [`internal/mcp/domain/port`](internal-mcp-domain-port.md) — 该包声明 MCP 客户端管理、服务器管理/持久化和技能访问/注册契约。
- [`internal/mcp/infrastructure`](internal-mcp-infrastructure.md) — 该包以自有 JSON-RPC 协议实现 MCP stdio/HTTP 客户端、连接管理/健康检查、能力缓存、端口适配和 Agent 工具桥接。

## internal/memory

- [`internal/memory/application`](internal-memory-application.md) — 该包编排记忆写入、缓冲与扫描、LLM 事实抽取、实体归一化、混合召回、遗忘和按用户/Agent 清理等应用用例。
- [`internal/memory/domain`](internal-memory-domain.md) — 该包承载记忆领域实体、事实状态机、作用域规则、frecency 算法、通用记忆模型、ID/时间生成和领域错误。
- [`internal/memory/domain/port`](internal-memory-domain-port.md) — 该包定义 memory 上下文的出向端口，包括事实/实体/通用记忆仓储、消息缓冲、抽取队列、LLM、embedding、向量索引和事件发布。
- [`internal/memory/infrastructure/persistence`](internal-memory-infrastructure-persistence.md) — 该包实现 memory 的 PostgreSQL/Redis/Milvus 持久化适配器，覆盖实体、事实、通用记忆、抽取队列、消息缓冲与向量数据清理。
- [`internal/memory/infrastructure/pipeline`](internal-memory-infrastructure-pipeline.md) — 该包构建基于 JetStream 和 PostgreSQL outbox 的异步记忆处理流水线，并提供富化、embedding、注入、召回及 LLM/向量适配能力。
- [`internal/memory/infrastructure/workers`](internal-memory-infrastructure-workers.md) — 该包提供按租户运行的后台记忆作业，包括事实抽取、过期清理、事实 supersede 判定、实体画像重建，以及租户发现与 worker 生命周期管理。

## internal/platform

- [`internal/platform/domain`](internal-platform-domain.md) — 平台领域层的最小环境模型，定义应用运行环境枚举，避免平台基础设施直接使用裸字符串。
- [`internal/platform/harness`](internal-platform-harness.md) — 提供组件生命周期编排、函数式组件适配和线程安全依赖容器；Harness 按注册顺序启动并按逆序停止组件。
- [`internal/platform/runtime`](internal-platform-runtime.md) — 承接应用启动期的运行时编排：初始化追踪、引导租户 schema、注册后台组件与 HTTP 服务，并响应进程退出信号。

## internal/skill

- [`internal/skill/application`](internal-skill-application.md) — 编排版本化 instruction Skill 的草稿、工作区、候选、发布与删除用例。
- [`internal/skill/domain`](internal-skill-domain.md) — 定义 capability、activation contract、instructions、requirements、发布校验与内容哈希。
- [`internal/skill/domain/port`](internal-skill-domain-port.md) — 定义版本仓储与 MCP 调用的最小消费者侧契约。
- [`internal/skill/infrastructure/persistence`](internal-skill-infrastructure-persistence.md) — 使用 PostgreSQL tenant 事务实现版本仓储和发布事务。
- 已移除路径索引：[`infrastructure`](internal-skill-infrastructure.md) · [`executors`](internal-skill-infrastructure-executors.md) · [`executors/code`](internal-skill-infrastructure-executors-code.md) · [`gateway`](internal-skill-infrastructure-gateway.md) · [`gateway/providers`](internal-skill-infrastructure-gateway-providers.md)

## pkg

- [`pkg/constants`](pkg-constants.md) — 集中定义跨包共享的 Agent、API、认证、知识、记忆、分页与超时常量，并提供知识集合命名函数。
- [`pkg/crypto`](pkg-crypto.md) — 提供基于 SHA-256 派生密钥的 AES-256-GCM 字符串加解密。
- [`pkg/httpclient`](pkg-httpclient.md) — 构造统一超时、User-Agent、重试与 SSRF 防护的 HTTP 客户端和 Transport。
- [`pkg/messaging/nats`](pkg-messaging-nats.md) — 封装 NATS 连接和带退避重试的消息发布。
- [`pkg/migration`](pkg-migration.md) — 封装 golang-migrate 的 public schema PostgreSQL 文件迁移执行，包括 dirty version 修复与增量升级。
- [`pkg/observability`](pkg-observability.md) — 提供 Zap 日志、Prometheus 指标和 OpenTelemetry trace provider、span 上下文与 HTTP handler。
- [`pkg/postgres`](pkg-postgres.md) — 为已迁移的 storage/postgres 包保留向后兼容构造入口。
- [`pkg/redis`](pkg-redis.md) — 为已迁移的 storage/redis 包保留向后兼容构造入口。
- [`pkg/reqctx`](pkg-reqctx.md) — 用 context 传播 trace ID 与 tenant ID，并提供对应的安全读取函数。
- [`pkg/storage/milvus`](pkg-storage-milvus.md) — 封装 Milvus 连接、集合/分区管理、向量写入、过滤搜索、关键词与混合检索，并提供通用 VectorIndex 适配器。
- [`pkg/storage/postgres`](pkg-storage-postgres.md) — 封装 pgx 连接池、最小查询接口、租户上下文与 search_path 事务，并执行 public/tenant schema 幂等 provisioning。
- [`pkg/storage/redis`](pkg-storage-redis.md) — 封装 go-redis 客户端及最小 KVStore 接口，统一 key-not-found 语义。
- [`pkg/storage/tenantnaming`](pkg-storage-tenantnaming.md) — 提供无数据库 IO 的租户级 Milvus collection/partition、NATS subject 与 Neo4j label 命名 DSL。
- [`pkg/tenantdb`](pkg-tenantdb.md) — 向后兼容地重导出 storage/postgres 的租户上下文/事务/schema API 与 storage/tenantnaming 的命名 API。
- [`pkg/textchunk`](pkg-textchunk.md) — 提供文本清洗、去重和递归、语义、结构化切块策略，以及旧版 Chunker API。
- [`pkg/timeutil`](pkg-timeutil.md) — 统一提供 Asia/Shanghai 时区位置与当前时间。
- [`pkg/tokenutil`](pkg-tokenutil.md) — 提供轻量 token 数估算、静态模型定价查询与美元成本计算。
- [`pkg/vector`](pkg-vector.md) — 向后兼容地重导出 storage/milvus 的向量存储类型与构造函数。
