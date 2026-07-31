---
name: stratum-e2e-development
description: >-
  在 Stratum 功能开发、Bug 修复或发布前验收时使用，尤其是需要设计端到端用例、验证真实浏览器/API/数据库链路、Skill/Agent/MCP/Memory/Knowledge/IAM 全流程或跨视口交互时。不用于需求分析或方案设计阶段。
---

# Stratum E2E 验证与 Bug 修复

**使用时机**：功能代码已写完，需要验证它在真实服务中是否正确工作；或收到 bug 报告，需要复现、定位、修复、再验证。

核心目标：不是"代码写完"，而是"用户目标在真实系统里跑通"。

## 统一验证入口

本 Skill 是 Stratum 唯一的测试、独立审查、系统验收和发布收口入口；不得创建平行 Test Skill。
Claude Code、Codex 和人工开发都读取同一份 `.test/verification.yaml`，调用相同的仓库脚本和 CI。

CI 是唯一验收权威。本地测试、Agent 自述和独立审查只能推动流程，不能签发 `accepted`。只有当前 commit、
manifest digest、runner identity、capability、cleanup 和 artifact digest 对应的 attestation 验证通过后，
本 Skill 才能报告完成。

### 确定性状态机

```text
received -> scoped -> classified -> planned -> local_verified
  -> reviewed -> ci_running -> attestation_verified -> accepted
```

失败终态为 `failed`、`blocked` 或 `incomplete`。状态不可由语言判断跳转；每次跳转必须有脚本、审查或 CI
证据。重跑只能用于诊断，第一次失败仍保留为失败证据，不能以重跑成功覆盖。

### 风险与审查

- `R0`：非执行文档；执行文档和生成一致性检查。
- `R1`：局部逻辑；执行静态检查、单测、构建和代码质量审查。
- `R2`：行为变化；增加真实集成/合同测试和 short E2E。
- `R3`：认证、租户、迁移、Agent/MCP/Memory、外部依赖或部署；增加失败路径、soak、规格审查和代码质量审查。
- `R4`：正式发布；增加 release soak、同 digest 晋升、发布证据审查和生产只读验证。

确定性分类器结果只能提高，不能由 Agent 降低；分类失败时 fail closed。实现 Agent 不能批准自己的规格审查
或代码质量审查，审查结果必须绑定 commit。

### Runner 边界

本 Skill 编排仓库 runner，但不实现 run-scope、lease、共享引用、动态端口、cleanup 或 attestation v2。
这些能力属于版本化验证内核。任何 lease、清理或证明字段缺失，都必须阻断 `accepted`。

按需读取：

- `references/verification-manifest.md`
- `references/review-contract.md`
- `references/failure-taxonomy.md`
- `references/agent-adapters.md`

## 系统级 stateful/soak 验收门禁

功能开发或 Bug 修复的普通测试通过后，必须使用仓库版本化实现完成系统验收，不能由 Skill 临时拼装另一套流程：

1. 运行 `bash scripts/quality/risk-regression-guard.sh --acceptance <changed-file...>`；输出 `short` 时运行 `make e2e-system-short`，输出 `soak` 时还要运行 `STATEFUL_E2E_DURATION_SEC=3600 STATEFUL_E2E_PACKS=all make e2e-system-soak`。
2. 持续监控 runner 到终态。失败时定位产品、环境或测试合同根因，修复后从失效阶段重新运行；不得跳过 pack、capability、清理或证据对账来取得通过。
3. 所有被选 capability 的主要操作必须由无头 Chromium 中的真实点击、输入、选择、刷新和跨角色会话发起。HTTP 只用于准备、响应证据和明确的拒绝断言；SQL 只用于测试身份、精确对账与清理。
4. 允许读取本地/临时测试数据库凭据，并只对本轮生成的 UUID 身份精确提升角色。凭据、token、cookie、密码、私钥、API key 和原始敏感响应只能保存在进程内，不得打印或写入 trace、日志、attestation。
5. runner 完成后运行 `make e2e-attestation-check`。源码变化、manifest 不匹配、报告过期、artifact 篡改、secret pattern、failed/skipped/unverified capability、未对账证据、清理失败或未声明残留实体都必须拒绝完成。
6. 最终报告列出 mode、seed、packs、action/evidence 计数、attestation 安全路径、清理结果、残留实体和未验证风险，并停止本轮启动的所有进程。

临时 Playwright spec、curl、手工浏览器和纯 API/数据库验证仅用于复现与诊断，不能替代系统验收。CI 强制运行 headless Chromium stateful E2E（`stateful-e2e` job），10 个关键 pack 全部通过才允许合并。

## 基本原则

- 验证驱动：先确认验收标准，再启动服务，再执行验证。
- 不只依赖单测；按影响范围做真实端到端验证。
- 前端改动必须模拟真实用户操作，不能只看页面能打开。
- 后端改动必须启动服务并请求真实 API，不能只看 handler 单测。
- 数据库相关改动必须验证写入、读取、约束、tenant schema、backfill 或迁移顺序。
- Agent/Skill/MCP/Memory/Knowledge/IAM 能力改动必须验证执行链路，不只验证配置保存。
- 验证失败时继续定位并修复，直到需求目标闭环。
- 不打印 token、refresh token、API key、password、原始密钥或敏感配置。
- 可以读取本地 `.env` 和测试数据库完成验证，但不得泄露敏感值。
- 可以创建 `tmp-` 前缀临时脚本做验证；完成前必须删除。
- 自己启动的后端、前端或辅助进程，完成前必须停止，或明确说明仍在运行和原因。

## 工作流程

### 0. 生成专业端到端用例

先从业务状态机和风险生成用例，不从页面控件清单机械枚举。参考 Playwright 官方最佳实践：验证用户可见行为、隔离测试、使用 role/label 等稳定定位器和 web-first assertions；参考测试金字塔，只把跨层关键旅程放进 E2E，组合爆炸下沉到单元或服务测试。

按以下顺序设计：

1. 写出当前任务的用户目标和终态，终态必须从需求与代码契约推导，不能从本 Skill 套用预设流程。
2. 画出状态迁移：初始状态、合法迁移、门禁、失败终态、可重试点。
3. 用决策表覆盖角色、资源状态、租户状态和关键配置组合。
4. 对输入做等价类与边界值：空值、非法 JSON、最大长度、重复名称、无权限。
5. 按风险排序：主业务闭环 > 权限/发布门禁 > 持久化/刷新 > 错误恢复 > 次要展示。
6. 每个 E2E 用例至少包含 UI 断言、关键 HTTP 断言和最终持久化证据；不能只断言 toast。

推荐最小用例集：

| 类型 | 目的 | 示例 |
|------|------|------|
| 主路径 | 证明当前任务的核心用户目标闭环 | 从入口操作到需求定义的最终状态 |
| 门禁 | 证明非法状态不能前进 | 从 domain 校验、路由中间件和页面条件渲染推导 |
| 权限 | 证明 UI 与后端权限一致 | 各角色入口可见性与写 API 授权结果一致 |
| 持久化 | 证明不是前端临时状态 | 刷新后可读回，DB 外键、状态和活动指针对齐 |
| 恢复 | 证明异步/失败路径可解释 | 异步任务失败显示具体错误，轮询到终态而非固定 sleep |
| 响应式 | 证明关键操作跨视口可用 | 桌面侧栏和移动抽屉均能完成同一主路径 |

#### 从当前上下文推导功能流程

本 Skill 只规定推导方法，不维护任何产品功能的固定步骤。每次执行时必须重新读取当前任务相关的：

- 用户需求、验收描述和本轮改动 diff。
- 前端 routes、页面、hooks、API client、条件渲染和权限控制。
- 后端 router、DTO、handler、application use case、domain 状态机与错误。
- persistence repository、迁移/tenant schema、异步 worker 和已有测试。

根据这些证据生成本次专属的"操作 → HTTP → application/domain → DB → UI 终态"矩阵。代码与文档冲突时，以用户确认的业务意图为准，并把冲突作为待澄清或缺陷暴露；不得为了符合 Skill 中的例子而改变测试目标。

#### 是否引入第三方测试 Skill

默认使用本 Skill、项目约定和 Playwright 官方文档。第三方 Skill 只有同时满足以下条件才引入：

- 提供本 Skill 没有的专业方法，而不是重复 Playwright 语法。
- 来源可信，仓库活跃且未归档；核验安装量、GitHub stars、维护者和源码内容。
- 不要求泄露凭据，不绕过项目安全规则，不改变业务验收口径。

安装量不能单独证明质量。若候选 Skill 只是通用 locator/POM 示例，直接使用官方文档，不增加依赖。

### 1. 确认验收标准

接手前先明确：

- 本次功能/修复的目标操作是什么。
- 成功时系统应该呈现什么状态（API 响应、页面状态、数据库写入、日志证据）。
- 涉及哪些层：前端、HTTP API、application、DB、tenant schema、Agent 链路。

写成可验证句子，例如：

```text
POST /agents/:id/execute/stream 返回 steps>0，日志中出现 react.tool.response 且 error 为空，知识库内容在 content_preview 中可见。
```

### 2. 选择验证层级

按改动影响面选择验证，不做无关大范围验证。

- 纯函数或模型转换：跑单元测试、边界输入、构建或类型检查。
- 后端 API：跑 Go 测试，启动后端，请求真实 HTTP API，验证错误响应和读回结果。
- 前端页面：跑前端测试、lint、build，启动前端，用 Playwright 或等价工具模拟点击、输入、保存、刷新。
- 数据库或 tenant schema：验证新租户 provision、旧租户 backfill、表列索引约束、写入读取、DDL 幂等。
- Agent/工具链路：验证配置保存、工具暴露、LLM tool call、工具路由、结果回传、execution/trace 记录。

### 3. 后端验证

需要真实服务时启动后端：

```bash
set -a; . ./.env; set +a; go run ./cmd/server
```

健康检查：

```bash
curl -fsS http://localhost:8080/health
```

后端常规测试：

```bash
go test ./... -count=1
```

如果全量耗时过高，先跑相关包；最终报告必须说明未跑全量的原因。

真实 API 验证必须检查：

- HTTP 状态码。
- 响应体关键字段。
- 错误响应是否符合预期。
- 写入后是否能 GET 读回。
- 数据库状态是否真的变化。

#### JWT Self-Mint（无需用户登录的后端 API 验证）

需要 Bearer token 时，从 `.env` 读取 `JWT_PRIVATE_KEY_PEM` 自签即可，无需走登录流程。

**模板详见 `references/jwt-self-mint-templates.md`**（Go + Python 两种方式，含关键约定）。

关键：

- claims 字段名必须与 `internal/iam/application/jwt_service.go` 中的 `jwtAccessClaims` 一致（`tid`/`role`/`global_role`/`system_role`）
- 私钥格式：`x509.ParsePKCS1PrivateKey`（PKCS#1 RSA）
- `.env` 中换行符可能是字面 `\n`，需 `strings.ReplaceAll` 转义
- 不得打印 token、私钥或任何原始凭据

#### 真实浏览器高权限会话

当前任务需要 tenant admin/owner 且测试环境没有可用高权限登录时，可创建临时 guest，再只提升该临时用户的 tenant role，并通过真实 refresh 流程取得对应 claims：

1. Playwright API context 调用 `POST /auth/guest`，响应只保存在内存，不输出 body 或 token。
2. 严格校验返回的 `tenant_id`、`user.sub` 为 UUID。
3. 在测试数据库中仅更新这一个临时 membership 为 `admin`。
4. 把 refresh cookie 注入 browser context；cookie 的 name、path、SameSite、Secure 必须与 `AuthHandler.setRefreshCookie` 一致。
5. 打开前端，让 `AuthProvider` 调用真实 `/auth/refresh`、`/auth/me` 和 tenant API 恢复会话。
6. 完成后清理临时用户或让 guest reaper 回收，并报告任何未清理数据。

注意：Stratum refresh cookie 的 path 是 `/`。若夹具错误地写成 `/auth`，首次 rotation 后会出现两个同名不同 path 的 cookie；后续 refresh 可能发送旧 token 并返回 401。不要把夹具错误误判为产品缺陷。

### 4. 前端验证

涉及 UI 时启动或复用前端服务：

```bash
npm --prefix web run dev
```

前端基础检查：

```bash
npm --prefix web run lint -- --quiet
npm --prefix web run build
```

有相关测试时运行对应测试：

```bash
npm --prefix web test -- <相关测试文件>
```

#### WSL2 前端验证（接口推断为主）

WSL2 headless 截图有字体缺失问题（中文乱码），**不使用截图**。改用以下方式（优先级从高到低）：

1. **纯 API 验证（最快）**：用 curl + JWT 验证后端接口返回值，从接口数据推断前端应渲染的内容。详见 Step 3 的 JWT Self-Mint。
2. **Playwright DOM 断言 + 网络拦截**：`page.evaluate()` 提取文本、`page.waitForResponse()` 捕获 API 响应。**模板详见 `references/playwright-tmp-spec-template.md`**。
3. **Windows 浏览器直访**：WSL2 端口自动转发，在 Windows Chrome/Edge 打开 `http://localhost:<port>` 做交互确认。

浏览器验证必须覆盖真实用户路径：

- 打开目标页面。
- 登录或注入有效认证态。
- 点击、输入、选择、保存、删除或执行。
- 等待真实请求完成。
- 验证成功/失败提示。
- 刷新页面后确认数据仍正确。

关键管理流程至少跑一个桌面视口和一个移动视口。移动端必须点击真实导航抽屉，不能用 `page.goto()` 绕过不可操作的菜单。优先使用：

- `getByRole()` 验证按钮、链接、tab、dialog 和菜单。
- `getByLabel()` 填写表单并同时验证可访问名称。
- `expect(locator)` 等待异步 UI 终态，不使用固定 sleep。
- Ant Design Select 若已有默认值，直接断言显示值；需要改变时点击可交互 selector，不强点内部 readonly input。

前端端口必须与后端 CORS 配置一致。Playwright 使用 `localhost:5173` 时，启动后端应显式设置 `FRONTEND_URL=http://localhost:5173`。登录停留在 `/login` 时先检查 OPTIONS 的 `Access-Control-Allow-Origin`，不要立刻修改认证代码。

### 5. 数据库验证

数据库相关功能必须验证：

- 目标表、列、索引、约束存在。
- INSERT / UPDATE / SELECT 与业务契约一致。
- 多租户 `search_path` 或 `tenantdb` 路由正确。
- 新租户 schema 能 provision。
- 已有租户 schema 能 backfill。
- `ADD CONSTRAINT`、`CREATE INDEX`、`ALTER TABLE ADD COLUMN` 都幂等。
- PostgreSQL `timestamptz` 显示 `2026-07-09 08:24:34+00` 是 UTC 时间，正常；北京时间需要加 8 小时。
- WSL 本地没有 `psql` 时，优先通过 Postgres 容器执行查询：

```bash
docker exec -i stratum-postgres-1 psql -U stratum -d stratum -P pager=off <<'SQL'
SELECT now();
SQL
```

Tenant schema 特别注意：

- tenant-only DDL 放 `pkg/storage/postgres/tenant_schema.sql`。
- `CREATE TABLE` 新增列后紧跟 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`。
- 依赖新列的索引、约束、查询必须排在 backfill 之后。
- 后续 backfill 可能因重复 constraint 中断；基础业务必需列应尽量放在建表或早期 ALTER。
- Stratum 常通过 PgBouncer 连接 Postgres；tenant schema provisioning 必须在事务里使用 `SET LOCAL search_path`，避免 transaction pooling 下普通 `SET search_path` 丢失或污染连接。
- 启动日志出现 `failed to provision tenant schema` 时，不要只验证当前租户；要确认所有历史租户是否能 provision，避免旧租户缺列导致后续 trace/knowledge/memory 链路随机失败。

### 6. Agent / Tool 链路验证

涉及 Agent、Skill、MCP、Memory、Knowledge 时，不能只验证配置保存。

必须按目标验证执行链路：

- 配置能创建和读回。
- Agent allowedSkills / MCP tools / 知识库 / 记忆配置能保存。
- 执行时工具能暴露给 LLM。
- LLM 能产生预期 tool call。
- tool call 能路由到正确后端能力。
- 工具结果能回到下一轮 LLM。
- 最终 response 正常返回。

关键证据：

- `error` 为空。
- `steps > 0`。
- `toolCalls` 包含预期工具名。
- trace / execution record / 日志中有关键步骤。

#### Agent 可观测性验证

验证 agent trace 基座时，优先使用已有可工作的 tenant/user/agent，避免把新建 agent 的 wiring 问题误判成 trace 问题。可先从数据库找最近成功 execution：

```bash
docker exec -i stratum-postgres-1 psql -U stratum -d stratum -P pager=off <<'SQL'
SELECT id, trace_id, agent_id, status, total_tokens, duration_ms, created_at
FROM "tenant_<tenant_id>".agent_executions
ORDER BY created_at DESC
LIMIT 5;
SQL
```

真实 API 调用应让 prompt 明确触发工具，例如"请回答你是谁，并调用可用工具回忆我是谁"。响应至少检查 HTTP status 200、`steps > 0`、`tokensUsed > 0`、`toolCalls` 包含预期工具名。

**质量检查 SQL 详见 `references/agent-observability-queries.md`**，包含：

- trace 事件质量聚合（execution_id 对齐、缺失字段、tool link 完整性）
- 工具 raw IO 和 summary 质量
- 下一轮上下文摘要检查

完成标准：

- `agent_executions.status = success`，且 `total_tokens`、`duration_ms` 大于 0。
- `agent_trace_events` 至少包含 LLM request/response、tool started/finished、final answer。
- `agent_tool_traces` 有 arguments、raw result、summary。
- `chat_messages` 有"本轮工具观察摘要"，用于下一轮上下文。
- `execution_id` 与 `trace_id` 在 execution、trace events、tool traces 三处对齐。
- 日志中没有 `failed to save trace events`、`failed to save tool traces`、`unused argument`、`column does not exist`。

常见判断：

- `CapGateway not set`：Agent wiring 或 TenantResolver 注入问题。
- `provider not found`：租户 LLM key 或 provider 注册问题，不是工具解析问题。
- `unknown tool`：工具名和 SkillToolIndex / MCP 映射问题。
- `toolCalls` 为空：工具可能暴露了，但 prompt、description 或 query 不足以触发模型调用。
- 数据库列不存在：tenant schema provision 未完成或 backfill 顺序错误。

### 7. 失败后循环修复

验证失败时：

1. 定位失败层：前端、HTTP、application、agent loop、外部模型、数据库、tenant schema、权限认证。
2. 收集证据：响应体、请求 payload、后端日志、数据库状态、调用链。
3. 找到最小根因，不猜测。
4. 修改代码或配置。
5. 重跑失败场景。
6. 通过后再跑相关回归测试。

不能因为单测通过就忽略真实 E2E 失败。

#### 区分测试夹具问题与产品缺陷

每次失败先确认边界证据：

- 页面未请求 API：检查路由、按钮可操作性和 locator。
- OPTIONS 被拒绝：检查前端 origin 与 `FRONTEND_URL`。
- 首次登录成功、刷新后 401：检查 refresh rotation、cookie path/domain/SameSite/Secure 和重复 cookie。
- Ant 控件点击超时：检查是否点了内部 readonly input 或被 message/modal 覆盖。
- UI 成功但 DB 无记录：检查异步 worker、tenant schema、job 终态和 repository 写入。
- UI 暴露管理入口但 API 返回 403：这是权限交互缺陷，应先写失败测试，再统一菜单与页面权限判断。

确认是产品缺陷后，使用 `superpowers:systematic-debugging` 定位根因；修复前使用 `superpowers:test-driven-development` 写能复现问题的最小测试，再重跑真实 E2E。

## 内嵌 Skill 调用

执行流程中可以随时调用其他 skill：

| 阶段 | 推荐 skill |
|------|-----------|
| 定位失败根因 | `superpowers:systematic-debugging` |
| 补充或改写测试 | `agent-skills:test` |
| 安全相关改动 | `agent-skills:security-and-hardening` |
| 代码质量扫描 | `code-review` 或 `simplify` |
| 提交并开 PR | `commit-commands:commit-push-pr` |

规则：

- 不强制每阶段都调，按实际需要触发。
- skill 返回后继续本 skill 的工作流程，不中断 E2E 闭环目标。

## 临时脚本规则

可以创建临时脚本验证：

- 临时 Go API E2E。
- 临时 Playwright spec。
- 临时 SQL 检查脚本。

要求：

- 文件名用 `tmp-` 前缀。
- 不提交临时脚本。
- 完成前删除。
- 不写入或打印密钥。
- 输出只包含状态码、ID、错误摘要、验证结果。
- 临时数据统一使用 `E2E-` 或 `tmp-` 前缀，便于精确清理和审计。
- 清理 SQL 必须限定到本轮生成的 ID，不允许宽泛删除。
- 若安全 hook 拒绝 `DELETE`/`rm`，不得绕过或换危险命令规避；删除可精确 patch 的临时文件，其余残留在最终报告中列明。

## 最终完成标准

最终回复前确认：

- 相关测试已通过。
- 后端服务已真实验证，或说明为什么不涉及。
- 前端功能已真实操作，或说明为什么不涉及。
- 数据库链路已验证，或说明为什么不涉及。
- 临时文件已删除。
- 自启动进程已停止，或明确说明仍在运行和原因。
- 未验证项、残余风险、无关环境问题必须明确暴露。
- 桌面与移动端关键路径已验证，或明确说明未覆盖的视口。
- UI、HTTP 和数据库三方证据一致；异步 job 已到终态。
- 测试夹具问题与产品缺陷已区分，修复项有回归测试。

推荐最终报告格式：

```text
已完成端到端验证。

改动结果：
- 完成了 xxx
- 修复了 xxx

验证结果：
- 后端测试：通过，命令 xxx
- 前端测试：通过，命令 xxx
- API E2E：通过，验证了 xxx
- 浏览器 E2E：通过，验证了 xxx
- 数据库链路：通过，验证了 xxx

清理情况：
- 临时脚本已删除
- 后端进程已停止 / 或服务仍运行在 xxx

剩余风险：
- xxx 不阻塞本次目标
```

如果没有完成完整 E2E，必须明确说：

```text
没有完成完整 E2E，原因是 xxx。
已完成的验证是 xxx。
未验证的风险是 xxx。
```

## References

按需加载，不进主 context：

| 文件 | 内容 | 何时读取 |
|------|------|----------|
| `references/jwt-self-mint-templates.md` | Go + Python JWT 自签模板 | 需要 Bearer token 调用 API 时 |
| `references/agent-observability-queries.md` | Agent trace/tool/chat 质量检查 SQL | 验证 Agent 执行链路时 |
| `references/playwright-tmp-spec-template.md` | Playwright 临时 spec 模板 | 需要浏览器自动化验证时 |
