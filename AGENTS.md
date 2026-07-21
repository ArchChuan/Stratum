<!-- generated; do not edit directly -->
<!-- source: docs/agent/instructions.md + docs/agent/templates/agents-prefix.md -->

> Codex entry: this generated `AGENTS.md` applies to the repository root.

More deeply nested `AGENTS.md` files may add narrower rules for their subtree; direct user instructions remain higher priority.

---

# Stratum project instructions

## Default principle

正确 > 清晰 > 速度。有疑问先问，不默默猜测。先读完相关文件、接口和调用链，声明假设，选择最小正确解，只做任务相关修改；冲突必须选定一种方案，禁止静默混合。先定义成功标准，测试应验证业务意图；所有跳过、不确定和部分失败都必须暴露。路由、重试和状态机等控制逻辑必须硬编码，AI 只做语言任务。

## Knowledge input and evidence

架构设计、技术方案、重大重构，以及 Agent、Memory、Workflow、安全或数据治理任务，必须同时检查：

1. 仓库代码、测试、ADR 和真实运行证据；
2. `obsidian` MCP 中相关的已验证/evergreen 技术观点、案例、踩坑和变更记录；
3. 官方文档、标准、原始论文或上游源码中的当前外部证据。

仓库事实以代码、测试和运行结果为准。Obsidian 是只读长期知识输入，`provisional` 内容只能作为未核验线索；搜索摘要不能作为关键证据。来源冲突时记录版本、范围和反例，不得静默选择。知识写回是独立蒸馏任务。完整协议：`/mnt/c/Users/yangh/Documents/Obsidian Vault/99-系统/知识输入与证据检索协议.md`。

## Technology and directory map

- 后端使用 Go 1.25.12（以 `go.mod` 为准）。入口 `cmd/server/main.go` 通过 `api/wiring.BuildContainer` 构图；HTTP 路由、handler、DTO 和 middleware 位于 `api/http/`，组合根位于 `api/wiring/`。
- 业务上下文位于 `internal/<ctx>/{domain,application,infrastructure}`。当前上下文为 `agent`、`evaluation`、`iam`、`knowledge`、`llmgateway`、`mcp`、`memory`、`platform`、`skill`、`workflow`。
- 通用基础设施位于 `pkg/`：`constants`、`observability`、`reqctx`、`storage/{milvus,postgres,redis}`、`tenantdb`、`migration`、`httpclient`、`textchunk`、`crypto`。`pkg/vector` 仅兼容旧 import，新代码使用 `pkg/storage/milvus`。
- 关键后端依赖：Gin v1.9.1、NATS v1.51.0（JetStream）、Milvus SDK v2.4.2、pgx v5.9.2、go-redis v9.7.3、golang-jwt v5.3.1、OTEL v1.40.0、Zap v1.27.1。
- 前端位于 `web/`，使用 React 18.3、Vite 6.4、Ant Design 5.20、React Router 6.26、Axios 1.18、TypeScript。代码按 `web/src/modules/` 业务域组织，共享 API 客户端是 `web/src/services/client.ts`。
- 部署资源位于 `k8s/`、`helm/`、`grafana/`；模块的细节以本文件末尾索引为准。

## Architecture decisions

- PostgreSQL 采用多租户 schema 隔离；事务内通过 `SET LOCAL search_path` 切换，统一走 `pkg/tenantdb`。
- JWT 使用 RS256，网关可验证且无需共享签名密钥。
- 消息采用 NATS JetStream；持久化 subject 使用 `domain.action` 形式。
- GraphRAG 向量检索采用 Milvus；不要以 pgvector 平行实现同一能力。
- Harness 顺序启动组件、逆序关闭依赖，避免生命周期竞争。
- LLMGateway 屏蔽 Qwen、Zhipu 等 OpenAI-compatible provider 差异，业务层不直接绑定 provider。
- 业务数据按当前用例硬删除；审计记录遵循其独立保留策略。请求和启动路径不得擅自执行不可逆清理。

## Multi-tenant DDL and repository rules

- 编号迁移 `pkg/migration/sql/NNN_*.sql` 只操作 public schema，禁止引用 tenant-only 表。Tenant-only DDL 放在 `pkg/storage/postgres/tenant_schema.sql`，由租户 provision 流程幂等应用；新增后同步检查 `pkg/migration/sql/tenant_schema.sql` baseline。
- 新表和索引用 `IF NOT EXISTS`；新列必须紧跟 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` 以升级历史租户。新增 `NOT NULL` 列须有安全默认值，或按 nullable → 回填 → 约束迁移。依赖新列的索引、约束和查询必须排在 backfill 后，并覆盖历史 schema 顺序测试。
- INSERT 与目标 DDL 必须逐列核对，尤其是无 DEFAULT 的 NOT NULL 列。数据库写入必须验证事务回滚和失败传播。`golang-migrate force <version>` 只用于把版本标为 clean，再由 `Up()` 继续；禁止手改 `schema_migrations`。
- 所有访问 tenant-scoped 表的 repository 方法必须通过 `execTenant(ctx, tenantID, fn)`，禁止直接调用 `r.pool.Exec/Query`；对应 port 方法必须显式包含 `tenantID string`。
- 删除租户向量数据必须调用 Milvus delete-by-filter，禁止 DropCollection；collection 命名遵循 `pkg/storage/milvus` 的现有实现。
- 功能替代旧存储时，同时删除旧表 DDL 和全部 Go 引用，并在 tenant schema 中添加兼容的清理语句处理存量租户；先确认代码引用为零，破坏性迁移必须单独审查和验证。
- 在 `execTenant` 外使用 `SET search_path` 时，连接释放前必须执行 `RESET search_path`；启动路径 SQL 必须用 `public.table_name` 等 schema-qualified 名称，禁止依赖连接残留状态。

## DDD layering and cross-context dependencies

- 依赖方向是 `handler → application → domain/port`；infrastructure 实现 port，由 `api/wiring/Container` 集中装配和逆序关闭。
- `pkg/` 不 import `internal/`；`domain/` 仅依赖 stdlib 和 `pkg/constants`；`application/` 不 import pgx、Redis、NATS 或 Gin；handler 不 import infrastructure 或存储驱动。
- 跨 context 接口定义在消费方 `domain/port/`，provider 由 infrastructure 实现，`api/wiring/` 只做薄 ACL 适配；禁止 import 兄弟 context 的 application 或 infrastructure。跨租户能力使用请求时 `Resolver(ctx, tenantID)` 延迟解析。
- DTO 只定义结构和 binding；handler 只做 bind、获取 tenant、调用 service、render，并用 `c.Error(err)` 交给统一错误中间件；application 负责编排、事务、鉴权和领域事件；domain 维护实体、不变量和算法；infrastructure 负责 IO 和错误翻译。
- wiring 禁止散写裸 SQL；表访问移到 infrastructure repository，事务和编排移到 application service。错误按 domain `Err*` → infrastructure 翻译 → application 编排 → middleware 映射 HTTP；冻结响应体 `{"error":"..."}` 兼容性。

## Git workflow

禁止在 `main` 分支直接提交或推送。必须使用仓库入口从最新 `origin/main` 创建隔离 worktree，禁止用原生 branch/worktree 命令绕过：

```bash
bash scripts/new-worktree.sh ../stratum-<feature> feat/<feature>
cd ../stratum-<feature>
git push -u origin feat/<feature>
gh pr create --base main
```

CI 全绿后合并，再用 `git worktree remove ../stratum-<feature>` 清理。Commit/PR 标题格式为 `[type](scope): description`，type 使用 `feat|fix|refactor|perf|test|docs|chore|ci`；PR 描述包含 What、Why、HowToTest。

## Development and end-to-end verification

- 编码前运行 `bash scripts/quality/risk-regression-guard.sh --explain`。后端快速验证：`go vet && go test -short ./...`；PR 前：`go test -v -race -timeout 30s ./...`。前端 PR 前：`make fe-lint && make fe-build`。依赖服务可用 `make infra-up`。
- 功能开发、Bug 修复、前后端联调、数据库链路，或 Agent/Skill/MCP/Memory/Knowledge/IAM 能力改动必须使用 `stratum-e2e-development` skill，完成真实 API、浏览器操作、后端服务和测试数据库链路的端到端验证。目标未满足时继续定位修复；清理临时脚本和自启动进程，禁止输出 token、密钥或原始 API key。
- AI 生成测试前必须先读同域优质测试模板，复用 mock 和断言风格。代码是主、测试是行为契约；冲突时依据产品意图判断改实现或改测试，禁止为过测扭曲实现。
- API 兼容性由 `api/http/contract_test.go` 和 `api/http/testdata/contracts/*.golden.json` 守护。业务逻辑目标覆盖率 ≥80%，外部依赖须 mock，完整套件使用 `-race`。

## Backend conventions

- Go 行宽 ≤120；import 按 stdlib、third-party、internal 分组；圈复杂度 ≤10。错误逐层用 `fmt.Errorf("operation: %w", err)` 包装；日志只用 Zap，禁止 `fmt.Print`。
- timeout、TTL、分页、topK、chunkSize、poolSize、retry 等行为数字禁止内联：跨包放 `pkg/constants/<domain>.go`，包内共享放 `internal/<pkg>/defaults.go`，单文件放本文件 `const` 块；名称包含 `Default`/`Max`/`Min` 或单位语义。
- 外部依赖必须有超时预算、有限重试、熔断/隔离和确定性关闭。瞬态错误指数退避基准 100ms、上限 10s；流式 LLM 不用 flat timeout，使用 header/idle timeout 和外层执行预算。
- 修改 port 后立即搜索并同步所有 test mock/stub。新增 tenant repository 时同时保证 `execTenant`、port 的 `tenantID` 和测试 mock。
- pgx v5 向 JSONB 写自定义 Go struct 时，先 `json.Marshal`，再传 `string(b)`；禁止直接传 struct 或 `pgtype.JSONB{}`。
- `context.WithTimeout` 必须在每次循环迭代内创建并及时 cancel；独立 IO 应有界并发，所有 goroutine 用 WaitGroup 跟踪，错误/停止路径 cancel 后必须 wait。
- 替换有状态连接/client/worker 时创建新实例、原子写回并关闭旧资源。共享 client 指针须在锁内捕获后使用，避免检查后被 `Close` 置空。超时后仍可能产出资源的 buffered channel 必须排水并关闭迟到资源。

## Frontend conventions

- 所有普通 API 调用走 `web/src/services/client.ts` 的唯一 Axios 实例；流式请求也复用其 base URL、认证状态和统一错误约定，禁止新增平行客户端。
- 行为常量集中在 `web/src/constants/`，使用全大写下划线和 `_MS`、`_SEC`、`_SIZE` 等单位后缀；页面不得硬编码网络、分页、MCP、Skill 或 Memory 行为数字。
- 错误统一显示 `message.error(err.response?.data?.error || '操作失败')`；使用 `message` 和 `Modal.confirm`，禁止 `alert()`、`confirm()` 和提交 `console.log`。
- 页面不得跨 `pages/` 导入；组件超过 200 行应提取 hook、component 或纯函数。`useEffect` 依赖完整，异步 effect 使用 cancelled 标志清理。
- 用户可见字符串使用中文。Bearer token 不得存入 localStorage/Web Storage；使用 HttpOnly cookie 或内存 Context。

## Logging and security

- 使用 `observability.NewLogger(env)`；production 输出 JSON，其他环境输出 console。事件命名 `layer.operation`；请求链路记录 request/trace/tenant/user，LLM 与 ReAct 只记录 model、provider、token 数、step、tool 和 latency 等必要元数据。
- DEBUG 只用于开发；正常路径 INFO；可预期 4xx/重试 WARN；5xx 和外部调用失败 ERROR。禁止记录 password、token、API key、PII 或原始上游响应体。
- 密钥通过 Vault/AWS Secrets Manager 管理，禁止入 Git；禁止修改 `config/prod.yaml`；传输使用 TLS 1.2+，静态敏感数据使用 AES-256；前端 `.env` 禁止提交密钥。
- bearer credential 不得进入 URL、Web Storage、通用请求日志或下游错误正文。认证 token 必须单次消费，状态转换必须原子。

## Risk regression harness

高风险改动必须逐项检查以下七条原则：

1. 授权、租户状态和外部依赖查询失败时必须 fail closed，禁止默认角色或默认放行。
2. bearer credential 不得进入 URL、Web Storage、通用请求日志或下游错误正文。
3. tenant-scoped 操作必须显式携带并校验 tenant ID，数据库访问必须经过租户边界封装。
4. 请求和启动路径禁止自动执行 DropCollection、不可逆的破坏性清理或无法审计的数据修复。
5. 持久化失败必须向上传播，失败状态写回失败也必须暴露。
6. 替换连接、client 或 worker 时必须关闭旧资源并等待 goroutine 退出。
7. 涉及认证、租户、迁移、消息、向量库或外部依赖时，必须添加对应失败路径与真实链路验证。

开工先运行 `bash scripts/quality/risk-regression-guard.sh --explain`，提交前运行 `make risk-guardrails`。IAM/OAuth、租户 DDL、日志/错误边界、资源关闭、readiness、部署或供应链改动必须执行命中的专项测试。数据库变更验证 DDL、回滚、历史 schema 顺序和失败传播；外部依赖验证预算、有限重试、隔离和关闭；secret scan 覆盖 tracked worktree。守卫必须传播失败，禁止吞错、伪成功或降级绕过。

自动报告只能作为待复核线索；仅修复由当前代码、测试和运行证据确认仍成立的缺陷。`tmp/risk-consolidated/reports/latest.md` 是本地复核索引，代码、测试、运行证据和阻断式守卫始终是事实源。

## Product design rules

- 意图优先：首屏聚焦用户目标，技术参数折叠进高级设置，首屏 ≤3 个决策点。列表页 ≤5 列；表单首屏 ≤8 项，基础展开、高级折叠。
- AI 执行中展示流式输出和工具调用步骤；执行后用折叠面板展示工具名、耗时和摘要；失败定位到具体步骤。
- 管理员负责配置，终端用户负责对话；管理操作二次确认，终端用户界面不暴露配置入口。
- 知识库 `description` 必填，`name` 创建后不可改；Agent `max_iterations` 为 1–20 slider，绑定知识库时展示 description；Skill temperature 用带标签 Slider，并支持不经过 Agent 的独立测试运行；Memory 用户侧只读 content、时间、importance，管理侧增加 scope、agent_id。
- 交互三态：进行中使用 loading/Skeleton；成功 `message.success` ≤2s；失败 `message.error` 不自动消失。所有列表都有 Empty 和引导操作；无数据提示“X 还是空的”，搜索无结果提示“没有找到…”。
- 删除、停用、清空使用 `Modal.confirm` 并描述后果；必填通过 `rules`，说明用 `extra`，补充信息用 `tooltip`，避免重复。
- 命名：Modal 状态用 `createOpen/editOpen`，loading 用 `createLoading/deleteLoading`，service 用动词+实体名如 `createWorkspace`，Hook 返回值直接解构而不加 `state` 前缀。

## Layered context index

- 项目事实：`docs/agent/project.md`。
- 架构与后端：`docs/agent/architecture.md`、`docs/agent/backend-go.md`、`docs/agent/constants.md`、`docs/agent/migration-tenant.md`。
- 模块规则：`docs/agent/api.md`、`docs/agent/agent.md`、`docs/agent/agent-chat-flow.md`、`docs/agent/milvus.md`、`docs/agent/nats.md`、`docs/agent/memory-facts.md`。
- 前端与产品：`docs/agent/frontend.md`、`docs/agent/product.md`。
- 可观测、部署和知识：`docs/agent/observability.md`、`docs/agent/deployment-architecture.md`、`docs/agent/knowledge-workspace.md`。
