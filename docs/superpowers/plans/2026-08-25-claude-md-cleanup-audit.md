# CLAUDE.md 文档体系全量评估报告

日期：2026-08-25

状态：**待用户确认处理建议**（确认后进入第二阶段清洗）

评估对象：25 个（`instructions.md` 主体 + 16 个被引用 doc + 6 个未引用 doc + 2 个模板）

## 评估范围与方法

- 评估基准：代码、测试、CI、配置为唯一事实源（`go.mod`、`web/package.json`、`internal/`、`pkg/`、`api/`、`web/src/`、`.test/verification.yaml`、`Makefile`、`.github/workflows/`）；文档不自证。
- 执行方式：4 组并行只读子代理按角色分组评估（结构类 / 模块类 / 较新类 / 未引用+模板），主 agent 用实测基准交叉核对、去重汇总。

## 实测基准（主 agent 已核实）

- `internal/` 有 **14 个 context**：agent audit collab evaluation iam knowledge llmgateway mcp memory parameters platform scheduler skill workflow
- `pkg/` 有 20 个顶层目录：constants crypto dag httpclient jsonschema messaging migration observability platformknowledge postgres redis reqctx safetext storage tenantdb textchunk timeutil tokenutil vector；`storage/` 下 6 个子目录：filestore milvus objectstore postgres redis tenantnaming
- `api/middleware/` 有 14 个文件：body_limit error_mapping inject_tenant jwt metrics prometheus public_error rate_limit require_active_tenant require_default_tenant require_role system_role_check trace（**middleware 在 `api/middleware/`，不在 `api/http/` 下**）
- `web/src/modules/` 有 15 个：agent approvals audit collab dashboard evaluation iam knowledge llm mcp memory operation-gate parameters scheduled-task skill workflow
- OTEL v1.42.0、React Router v7.18.2、Go 1.25.12

## 评估总表

### 主体与模板

| 文件 | 最后修改 | 角色 | 状态 | 处理建议 |
|---|---|---|---|---|
| instructions.md | 2026-08-20 | 主体 | ⚠️ 6 处事实错误 | 修改（见下方清单） |
| templates/claude-prefix.md | 2026-07-28 | 模板 | ✅ | 无需更新 |
| templates/agents-prefix.md | 2026-07-28 | 模板 | ✅ | 无需更新 |

**instructions.md 事实错误清单**：

| # | 文档声称 | 系统实际 |
|---|---|---|
| 1 | context 列 11 个（缺 audit/collab/parameters） | 14 个 |
| 2 | OTEL v1.40.0 | v1.42.0（go.mod） |
| 3 | React Router 6.26 | ^7.18.2（web/package.json） |
| 4 | pkg 结构缺 8 项 | 缺 dag/jsonschema/messaging/platformknowledge/safetext/timeutil/tokenutil/postgres |
| 5 | "禁止修改 config/prod.yaml" | config/prod.yaml 不存在（config/ 只有 config.go/nacos.go/pgbouncer.ini/userlist.txt） |
| 6 | "middleware 位于 api/http/" | middleware 在 `api/middleware/`，api/http/ 下无 middleware 目录 |

### 被引用的 16 个 doc

| 文档 | 最后修改 | 状态 | 处理建议 |
|---|---|---|---|
| architecture.md | 2026-08-09 | ⚠️ | 修改（11→14 contexts；storage 补 filestore/objectstore；pkg 骨架补新目录） |
| project.md | 2026-08-09 | ⚠️ | 修改（14 contexts；platform 目录结构错误；middleware 漏 9 个；pkg/storage 补全；OTEL 1.42） |
| backend-go.md | 2026-08-21 | ⚠️ | 修改（AgentExecTimeout(90s) → DelegateExecutionTimeout=3min） |
| api.md | 2026-08-12 | ⚠️ | 修改（严重：路由表 8 vs 17；多条路由消失；角色断言过时；middleware 顺序；423→403） |
| agent.md | 2026-08-19 | ⚠️ | 修改（Profile 版本/类型名；system prompt 迁 DB；catalog 路径；错误串） |
| agent-chat-flow.md | 2026-08-13 | ⚠️ | 修改（system settings 路由不存在；错误串；Profile 版本） |
| milvus.md | 2026-07-16 | ✅ | 保留（可顺手补新 API 面） |
| nats.md | 2026-07-16 | ⚠️ | 修改（新增 2 stream；IAM hermes 用共享 core；DLQ 重放路由） |
| memory-facts.md | 2026-07-17 | ✅ | 保留 |
| constants.md | 2026-08-24 | ⚠️ | 修改（前端 AGENT_EXEC_TIMEOUT_MS 已移除） |
| migration-tenant.md | 2026-08-22 | ✅ | 保留 |
| frontend.md | 2026-08-20 | ⚠️ | 修改（React Router v7；无 TanStack Query/Zustand） |
| product.md | 2026-08-20 | ⚠️ | 修改（name 创建后不可改 → 已支持 renameTo） |
| observability.md | 2026-08-19 | ⚠️ | 修改（InitTracingFromEnv 路径） |
| deployment-architecture.md | 2026-08-04 | ⚠️ | 修改（HTTPS/observability.enabled/Opik/Nacos/zhparser）或降级为历史快照 |
| knowledge-workspace.md | 2026-07-29 | ⚠️ | 修改（大改：DTO 路径/schema/WorkspaceConfig/错误表/collection 命名） |

### 未引用的 6 个 doc

| 文档 | 最后修改 | 状态 | 处理建议 |
|---|---|---|---|
| bug-lessons.md | 2026-07-16 | ✅ | 保留（高价值独有经验规则） |
| e2e-standards.md | 2026-08-01 | ⚠️ | 保留但修正（golden 141→165；soak packs 缺 4 个） |
| memory-trajectory-reflection.md | 2026-08-22 | ✅ | 保留为 ADR（不建议并入 memory-facts） |
| review-platform-config-versions.md | 2026-08-24 | ✅ | 归档（review 已结束，代码即事实源） |
| tiered-memory.md | 2026-07-17 | ✅ | 合并进 memory-facts.md |
| verification-ci-authority.md | 2026-08-22 | ✅ | 合并进 e2e-standards.md 后删除原文件 |

## 处理建议汇总

| 处理方式 | 文档列表 |
|---|---|
| 修改（instructions.md 主体） | instructions.md |
| 修改（被引用 doc） | architecture · project · backend-go · api · agent · agent-chat-flow · nats · constants · frontend · product · observability · deployment-architecture · knowledge-workspace（13 个） |
| 修改（未引用 doc） | e2e-standards（就地修正 2 处） |
| 保留 | milvus · memory-facts · migration-tenant · bug-lessons · memory-trajectory-reflection · templates/*（6 个 + 2 模板） |
| 归档 | review-platform-config-versions |
| 合并 | tiered-memory → memory-facts；verification-ci-authority → e2e-standards |

## 建议执行顺序（确认后）

1. **instructions.md 硬事实**（context/dep/pkg/prod.yaml/middleware 位置）→ `make agent-instructions` 重新生成 CLAUDE.md/AGENTS.md
2. **被引用 doc**：knowledge-workspace（大改）> api（重生成路由表）> deployment-architecture > project > architecture > 其余
3. **未引用 doc**：合并/归档/就地修正
4. **e2e 测试重构**（门槛原则 + stratum-e2e-tester 机制写入 instructions.md）
5. **验证**：`make agent-instructions-check` + markdownlint + R3 闭环（本 PR 命中 agent-governance R3）

## 详细过时点附录

### 组 1 — 结构类

- **architecture.md**：核心分层规则（依赖方向、go-arch-lint/depguard、错误分层、契约测试）全部吻合。过时：①"11 个 bounded context" → 14，缺 audit/collab/parameters；②`pkg/storage/{postgres,redis,milvus,tenantnaming}` 缺 filestore/objectstore；③pkg 骨架只列 8 个，实际 20 个。
- **project.md**：主体目录树与依赖大多准确。过时：①context 11→14；②`platform/{domain,harness,runtime}` 中 runtime 目录不存在，实际为 alerting/application/domain/e2eattestation/e2erunscope/harness/infrastructure/verificationplan；③middleware 漏 BodyLimit/PrometheusMiddleware/RateLimit/RequireDefaultTenant/RequireSystemRole/SecurityHeaders/NamespaceMiddleware/TenantMiddleware/TrustedProxies；④pkg 缺 dag/jsonschema/messaging/platformknowledge/safetext/timeutil/tokenutil/postgres/redis；⑤OTEL v1.40→v1.42。
- **backend-go.md**：规范+踩坑红线，绝大多数符号存在。过时：流式超时组合里的 `AgentExecTimeout`(90s) 已不存在，实际执行预算常量是 `DelegateExecutionTimeout = 3 * time.Minute`（pkg/constants/timeouts.go:87）。

### 组 2 — 模块类

- **api.md**（漂移最严重）：①路由注册 8 个 → 实际 17 个（缺 registerModelCatalogue/Dashboard/ResourceChangeProposals/OperationProposals/Workflows/Collab/ScheduledTasks/Audit/LLMAdmin）；②`GET /models` 实际需 JWT+member；③`PATCH /tenant/embed-model` 路由已不存在；④Skill 草稿三路由已合并为单一 `PATCH /skills/:id/draft`；⑤Skill publish 实际 member+requireActive；⑥`POST /evaluations/experiments/:id/evaluate` 不存在；⑦MCP/Workflow 写操作角色从 admin 放宽为 member+service 校验；⑧middleware 注册顺序 `otelgin→Trace→ErrorHandler`（ErrorHandler 非首个）；⑨租户未激活返回 403 非 423。
- **agent.md**：工具名、能力边界、AgentConfig、上下文预算、ReasoningEffort 门控、MCP 审批、proposal 状态机均准确。过时：①Profile 版本 2026-07-23.v1 → 实际 2026-08-08.v3（system_assistant.go:12）；②类型名 `BuiltinSystemAssistantProfileSource` → `SystemAssistantProfile`；③Profile 不再覆盖 system prompt（已迁 DB 字段 agents.system_prompt）；④官方目录 manifest 不在 `docs/assistant/catalog.yaml`（目录为空），实际在 `internal/agent/infrastructure/officialdocs/generate/catalog.yaml`；⑤错误串 `system assistant is platform managed` 不存在。
- **agent-chat-flow.md**：流程描述大体准确。过时：`GET/PUT /agents/system/settings` 路由不存在；错误串同 agent.md；Profile 版本需更新。
- **milvus.md**：全部核对通过（SDK 2.4.2、VectorStore API、2s TCP 预检、wiring 失败 Warn、Search 参数顺序）。仅未列新增 HybridSearch/KeywordSearch/SearchWithFilter/DeleteByFilter 等。
- **nats.md**：三阶段常量/consumer/stream/指标准确。过时：①实际 `EnsureStreams` 建 5 条（新增 MEMORY_EXTRACTION、MEMORY_REFLECTION）；②"JetStream 专用于 memory pipeline"不准确——IAM hermes 客户端消费共享 NATS core 连接；③DLQ 已有 `ReplayService.ReplayByErrorCode` + `POST /admin/memory/dlq/replay`（"需另行设计"已过时）。
- **memory-facts.md**：provenance、source_document JSON、集合保留策略、020 迁移、DDL 权威位置、Top-N 常量全部核对一致。

### 组 3 — 较新类

- **constants.md**：后端 13 个 Agent 常量全部吻合。过时：前端 `AGENT_EXEC_TIMEOUT_MS` 已从 `web/src/constants/index.ts` 移除（仅历史文档残留）。
- **migration-tenant.md**：无实质过时，全部实现实体存在且路径一致。
- **frontend.md**：技术栈行过时——文档写 "React Router 6.26 · TanStack Query 5 · Zustand 5"，实际 `web/package.json` 为 react-router-dom ^7.18.2，无 @tanstack/react-query 与 zustand 依赖；目录表与 client.ts/streamApiEvents 准确。
- **product.md**："知识库 name 创建后不可改" 过时——`UpdateWorkspace` 支持 `renameTo`（collection 绑定 workspace ID 而非 name）；"技能独立测试运行" 在 web/src/modules/skill/ 无对应 UI，无法证实。
- **observability.md**：指标体系、Opik 双写、MinIO payload、collector PVC 均准确。过时：`InitTracingFromEnv` 实际在 `cmd/server/runtime.go:58`，文档写 `internal/platform/runtime`（目录不存在）。
- **deployment-architecture.md**：历史运维快照显著过时。①HTTP-only/secureCookies=false 已过时——现 `helm/values-demo.yaml` 为 secureCookies:"true"、frontendUrl:<https://demo.example.com，deploy.yml> 叠加 values-demo-remote-http.yaml（websecure+TLS）；②observability.enabled 默认已 true；③架构图缺 Opik、Nacos、cert-manager、zhparser postgres。
- **knowledge-workspace.md**（大改）：核心架构/流水线准确，但细节大面积过时——①DTO 路径 `api/http/dto/rag.go` 不存在（实际 proto 生成 `api/http/dto/gen/rag.go`）；②API 表缺 preview/editors/delete document/document access/request-access 等路由；③`rag_workspaces` 缺 system_key/management_mode/created_by 列；④`knowledge_docs` id 实为 UUID、缺 source/metadata/allowed_user_ids/allowed_role_ids；⑤缺 `knowledge_chunks_quarantine` 表；⑥DocRepo 缺 VisibleDocIDs/GetByID/SetDocAccess；⑦WorkspaceConfig 缺 Reranking/ScoreThreshold/RerankTopK/RerankModel/JudgeModel；⑧collection 命名已为 `kb_<ws>_<model>`（+CollectionLegacyName），文档仍写 `kb_<workspaceID>`；⑨vectorDim 移至 `pkg/constants/embedding.go` 的 `DimensionForModel`（text-embedding-v1→1536）；⑩嵌入模型白名单过时，现走 port.ModelExists 目录校验。

### 组 4 — 未引用 + 模板

- **bug-lessons.md**：8 类防复发规则，通用工程原则，无过时，引用的 docs/audits、docs/deployment 文件均存在。保留。
- **e2e-standards.md**：过时 2 处——①soak packs 缺 audit/operation-gate/collab/scheduled-task（实际 17 个 pack）；②golden 文件 141→实际 165 个。与 instructions.md e2e 章节有重叠，先就地修正。
- **memory-trajectory-reflection.md**：ADR（status: implemented），全部常量/表/迁移可验证。保留为 ADR。
- **review-platform-config-versions.md**：全部修复点可验证，review 已结束。归档。
- **tiered-memory.md**：全部常量/表/迁移与代码一致（ActiveSnapshotTTL=24h 等）。合并进 memory-facts.md。
- **verification-ci-authority.md**：三种验证权威与 verification.yaml、ci.yml、deploy.yml 一致。合并进 e2e-standards.md 后删除。
- **templates/**：两个 prefix 均为 3 行文件头，与现状一致，无需更新。
