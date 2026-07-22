# Agent 工具权限管理 Harness 实施计划

> **执行要求：** 使用 `superpowers:executing-plans` 按任务执行；功能与 Bug 修复阶段同时使用
> `stratum-e2e-development`。每个任务遵循测试先行、最小实现、局部验证、独立提交。

**目标：** 建立 Agent 工具权限的确定性决策、执行前强制校验、审批恢复、未知外部结果、结果治理和跨层
Harness，使 P0 安全不变量成为阻断式 CI。

**架构：** Tool Catalog 负责最小暴露；Agent application 中的纯授权决策点生成版本化决策；所有 MCP
副作用统一经过 Execution Guard；审批仅解除风险门槛，resume 在执行前重新授权；MCP 结果经过 Result Guard
后才进入 Agent Loop。真实 PostgreSQL、HTTP/SSE、Agent Loop、确定性 LLM stub 和 fake MCP 提供端到端证据。

**技术栈：** Go 1.25、Gin、pgx/PostgreSQL tenant schema、MCP client、React 18、Ant Design、Playwright、
现有 risk guardrails 与 E2E 基础设施。

**设计依据：**
`docs/superpowers/specs/2026-07-22-agent-tool-permission-harness-design.md`

---

## 范围约束

- 本计划只实施第一阶段安全 Harness 和阻断它的最小生产修复。
- 不实现任意表达式 ABAC、server 通配授权、sticky approval 或完整权限管理 UI。
- 用户级权限第一版只能收窄或拒绝，不得扩大 tenant、Agent 或 Skill 边界。
- MCP 标准没有通用幂等字段；只有工具契约明确支持时才传播幂等键，禁止擅自修改工具参数。
- 所有 tenant-scoped repository 继续通过 `execTenant`/`postgres.ExecTenant`。
- API 错误体保持 `{"error":"..."}` 兼容；新增错误由统一 middleware 映射。

## Task 1：建立授权决策纯模型与性质测试

**Files:**

- Create: `internal/agent/domain/tool_authorization.go`
- Create: `internal/agent/domain/tool_authorization_test.go`
- Modify: `internal/agent/domain/port/tool_policy.go`

### Step 1：写失败的表驱动测试

覆盖：缺 tenant、停用用户、Agent 未授权、active Skill 未声明、策略查询失败、未分类、read、reversible、
destructive。断言 `deny | allow | require_approval` 和稳定 reason code。

```bash
go test ./internal/agent/domain -run 'TestAuthorizeTool' -count=1
```

预期：因授权类型和函数尚不存在而失败。

### Step 2：写权限单调性性质测试

对生成的 Agent/Skill/User tool sets 验证：减少任一集合或激活 Skill 后，有效权限不能增加。使用固定 seed，
失败时输出最小输入，禁止依赖 LLM。

```bash
go test ./internal/agent/domain -run 'TestToolAuthorizationMonotonicity' -count=1
```

预期：失败。

### Step 3：实现最小纯决策模型

新增 `AuthorizationRequest`、`AuthorizationDecision`、effect/reason 常量和无 IO 的 `AuthorizeTool`。基础授权失败
必须 `deny`；只有基础权限成立后，风险才能映射为 allow 或 approval。

### Step 4：运行局部验证

```bash
go test ./internal/agent/domain -count=1
```

预期：通过。

### Step 5：提交

```bash
git add internal/agent/domain/tool_authorization.go internal/agent/domain/tool_authorization_test.go \
  internal/agent/domain/port/tool_policy.go
git commit -m "[feat](agent): add deterministic tool authorization model"
```

## Task 2：实现规范参数摘要与审批完整绑定

**Files:**

- Create: `internal/agent/application/tool_arguments.go`
- Create: `internal/agent/application/tool_arguments_test.go`
- Modify: `internal/agent/application/tool_approval_service.go`
- Modify: `internal/agent/application/tool_approval_service_test.go`
- Modify: `internal/agent/domain/tool_approval.go`
- Modify: `internal/agent/domain/port/repository.go`
- Modify: `internal/agent/infrastructure/persistence/tool_approval_store.go`
- Modify: `internal/agent/infrastructure/persistence/tool_approval_integration_test.go`
- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Modify: `pkg/storage/postgres/tenant_schema_test.go`
- Modify: `pkg/storage/postgres/tenant_schema_safety_test.go`
- Modify: `pkg/storage/postgres/tenant_schema_integration_test.go`

### Step 1：写规范摘要失败测试

验证 map key 顺序不影响摘要、数字/数组/嵌套对象稳定、摘要带版本前缀；不同参数必须产生不同摘要。不允许用
`fmt.Sprint` 或非规范 map 序列化。

```bash
go test ./internal/agent/application -run 'TestCanonicalToolArguments' -count=1
```

预期：失败。

### Step 2：写审批绑定失败测试

在 service 和 PostgreSQL 集成测试中分别改变 tenant、user、agent、execution、tool call、server、tool、参数
摘要、Skill revision 或 policy version，断言审批不能被消费。保留现有加密明文不可见断言。

```bash
go test ./internal/agent/application ./internal/agent/infrastructure/persistence \
  -run 'TestToolApproval.*Binding|TestToolApprovalEncrypted' -count=1
```

预期：失败。

### Step 3：扩展 tenant schema

为审批记录增加所需的稳定绑定列和安全默认/nullable 升级顺序；新列、backfill、约束和索引遵循历史 tenant
schema 规则。不要在 numbered public migration 中复制 tenant-only DDL。

### Step 4：实现规范摘要与完整匹配

`ToolApprovalPayload` 和持久化行保存 decision ID、arguments digest、Skill revision manifest 和 policy version。
执行时比较完整绑定；原始参数仍只存在 AES-GCM ciphertext 中。

### Step 5：验证 schema 与应用层

```bash
go test ./pkg/storage/postgres/... ./internal/agent/application/... \
  ./internal/agent/infrastructure/persistence/... -count=1
```

预期：通过，包括历史 schema 顺序与事务失败测试。

### Step 6：提交

```bash
git add internal/agent pkg/storage/postgres/tenant_schema.sql
git commit -m "[feat](agent): bind approvals to canonical tool decisions"
```

## Task 3：引入用户收窄策略端口与 fail-closed 解析

**Files:**

- Create: `internal/agent/domain/port/tool_authorizer.go`
- Create: `internal/agent/application/tool_authorizer.go`
- Create: `internal/agent/application/tool_authorizer_test.go`
- Modify: `internal/agent/application/agent_service.go`
- Modify: `internal/agent/application/agent_service_test.go`
- Modify: `api/wiring/agent.go`
- Modify: wiring tests/mocks returned by port search

### Step 1：写失败测试

覆盖用户允许、用户 deny、resolver 错误、用户停用、缺 tenant context。验证 resolver 只能收窄 Agent allowlist，
不能增加 Agent 未声明工具。

```bash
go test ./internal/agent/application -run 'TestToolAuthorizer' -count=1
```

预期：失败。

### Step 2：定义消费侧端口

端口定义在 Agent domain/port，输入显式携带 tenantID/userID/agentID/toolID；基础实现先适配当前 IAM
角色和 Agent allowlist。若仓库没有现成用户级 tool policy 存储，本阶段只实现 active user/role 的收窄规则，
不新增自由配置表。

### Step 3：接入 application 和 wiring

授权依赖解析错误返回稳定 `policy_lookup_failed`/`user_permission_denied`，不得默认为允许。修改 port 后立即
搜索并同步所有 mock/stub。

### Step 4：验证

```bash
go test ./internal/agent/application/... ./api/wiring/... -count=1
```

预期：通过。

### Step 5：提交

```bash
git add internal/agent api/wiring/agent.go
git commit -m "[feat](agent): enforce user-scoped tool authorization"
```

## Task 4：建立统一 Execution Guard

**Files:**

- Create: `internal/agent/application/tool_execution_guard.go`
- Create: `internal/agent/application/tool_execution_guard_test.go`
- Modify: `internal/agent/domain/port/mcp_tools.go`
- Modify: `internal/agent/application/graph/react.go`
- Modify: `internal/agent/application/graph/react_test.go`
- Modify: `internal/agent/application/agent_service.go`
- Modify: `api/wiring/agent.go`

### Step 1：写绕过目录的失败测试

直接向 ReAct tool node 注入模型伪造的 tool call，工具不在授权集合时 executor 调用次数保持零。另测批准前
合法、批准后 Agent/Skill/User 权限撤销，执行前复核拒绝。

```bash
go test ./internal/agent/application/graph ./internal/agent/application \
  -run 'Test.*ExecutionGuard|Test.*ForgedToolCall' -count=1
```

预期：至少一个场景显示现有路径可绕过统一授权或依赖间接重放。

### Step 2：实现 Guard

Guard 接收当前主体、Agent config、pinned Skill、工具定义、参数和可选 approval decision，调用纯 PDP 并执行
JSON Schema 输入校验。只有 Guard 可以调用底层 `MCPToolExecutor`。

### Step 3：收口调用路径

ReAct 不再直接持有裸 executor；`ApprovedToolCallFn` 不能在风险判断前直接执行。普通调用和 resume 都经过同一
Guard。Tool Catalog 继续做前置过滤，但不承担最终授权。

### Step 4：验证

```bash
go test ./internal/agent/application/... ./api/wiring/... -count=1
```

预期：通过；所有 executor fake 的调用次数断言明确。

### Step 5：提交

```bash
git add internal/agent api/wiring/agent.go
git commit -m "[refactor](agent): guard every MCP tool execution"
```

## Task 5：修正审批状态机与 unknown outcome

**Files:**

- Modify: `internal/agent/domain/tool_approval.go`
- Modify: `internal/agent/application/tool_approval_service.go`
- Modify: `internal/agent/application/tool_approval_service_test.go`
- Modify: `internal/agent/infrastructure/persistence/tool_approval_store.go`
- Modify: `internal/agent/infrastructure/persistence/tool_approval_integration_test.go`
- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Modify: `api/http/handler/agent_exec_handler_test.go`
- Create: `api/http/handler/agent_approval_handler_test.go`

### Step 1：写模型化状态机测试

覆盖 pending/approved/rejected/expired/executing/executed/unknown_outcome，验证非法转换失败；并发 resume 使用
barrier 同时开始，底层 executor 最多一次 attempt。执行前明确失败可回 approved；请求已发出后的 timeout、
cancel 或连接中断必须进入 unknown outcome。

```bash
go test -race ./internal/agent/application ./internal/agent/infrastructure/persistence \
  -run 'TestToolApproval.*State|TestToolApproval.*Concurrent|TestToolApproval.*Unknown' -count=1
```

预期：现有 timeout 释放为 approved 的测试失败。

### Step 2：实现显式执行结果分类

MCP executor/transport 返回足以区分 `not_sent`、`definite_failure`、`success`、`outcome_unknown` 的消费侧错误；
无法证明未发送时一律 unknown。不要按错误字符串判断。

### Step 3：持久化状态转换

新增原子 `MarkOutcomeUnknown`。`ReleaseExecution` 只用于明确未发送或确定失败；unknown 状态不能 resume，必须
通过后续对账/补偿流程处理。

### Step 4：验证

```bash
go test -race ./internal/agent/application/... ./internal/agent/infrastructure/persistence/... -count=1
```

预期：通过，race 无数据竞争。

### Step 5：提交

```bash
git add internal/agent pkg/storage/postgres/tenant_schema.sql api/http
git commit -m "[fix](agent): preserve unknown MCP execution outcomes"
```

## Task 6：传播 checkpoint 与审计持久化失败

**Files:**

- Modify: `internal/agent/application/agent_service.go`
- Modify: `internal/agent/application/agent_service_test.go`
- Modify: `internal/agent/application/tool_approval_service.go`
- Modify: `internal/agent/application/tool_approval_service_test.go`
- Modify: `internal/agent/infrastructure/persistence/checkpoint_store.go`
- Modify: related trace/evidence ports and mocks only if required

### Step 1：写失败注入测试

分别让 approval create、checkpoint upsert、execution claim、executed/unknown 状态写入、checkpoint complete 和
必要审计写入失败。断言错误向上传播，且副作用前失败时 executor 调用数为零。

```bash
go test ./internal/agent/application -run 'Test.*PersistenceFailure' -count=1
```

预期：checkpoint `MarkCompleted` 场景失败，因为现有代码吞错。

### Step 2：修复错误传播

删除 `_ =` 吞错路径；为“副作用已成功但终态写失败”返回可识别错误并保留对账状态。错误逐层 `%w` 包装，
日志不得包含原始参数或上游正文。

### Step 3：验证

```bash
go test ./internal/agent/application/... ./internal/agent/infrastructure/persistence/... -count=1
```

预期：通过。

### Step 4：提交

```bash
git add internal/agent
git commit -m "[fix](agent): propagate approval persistence failures"
```

## Task 7：增加 MCP Tool Result Guard

**Files:**

- Create: `internal/agent/application/tool_result_guard.go`
- Create: `internal/agent/application/tool_result_guard_test.go`
- Modify: `internal/agent/domain/port/mcp_tools.go`
- Modify: `internal/mcp/infrastructure/types.go`
- Modify: `internal/mcp/infrastructure/client.go`
- Modify: `internal/mcp/infrastructure/mcp_test.go`
- Modify: `internal/agent/application/graph/react.go`
- Modify: `internal/agent/application/graph/react_test.go`

### Step 1：写失败测试

覆盖 JSON-RPC protocol error、`isError`、output schema 不匹配、超大结果、敏感 sentinel 和包含恶意指令的
外部文本。断言结果不会原样进入 LLM message、日志或 trace；错误和截断仍保留可审计摘要。

```bash
go test ./internal/mcp/infrastructure ./internal/agent/application/... \
  -run 'Test.*ToolResultGuard|Test.*SensitiveToolResult' -count=1
```

预期：现有字符串化结果路径至少在 schema/不可信标记场景失败。

### Step 2：保留结构化 MCP 结果

基础 client 不再过早丢失 `isError`、structured content 和 schema 信息；消费侧 port 返回结构化结果。禁止把
完整原始上游错误正文拼入下游错误。

### Step 3：实现 Result Guard

使用确定性代码做 schema、大小和敏感信息检查；外部文本附加不可信来源 metadata。首版不尝试通过另一个
LLM 判断 prompt injection，也不承诺检测所有恶意内容。

### Step 4：验证

```bash
go test ./internal/mcp/... ./internal/agent/application/... -count=1
```

预期：通过。

### Step 5：提交

```bash
git add internal/mcp internal/agent
git commit -m "[feat](agent): validate MCP tool results before model use"
```

## Task 8：构建确定性 fake MCP 与 API/数据库 Harness

**Files:**

- Create: `internal/mcp/infrastructure/testserver/fake_server.go`
- Create: `internal/mcp/infrastructure/testserver/fake_server_test.go`
- Create: `internal/agent/infrastructure/persistence/tool_permission_harness_integration_test.go`
- Modify: `internal/mcp/infrastructure/integration_test.go`
- Modify: `api/http/handler/agent_exec_handler_test.go`
- Create: `api/http/handler/agent_approval_handler_test.go`
- Modify: `api/http/contract_test.go` only if public response contract intentionally changes
- Modify: affected `api/http/testdata/contracts/*.golden.json` only for approved contract changes

### Step 1：写 fake server 自测

支持动态 list changed、伪造 annotations、调用计数和参数摘要、read/reversible/destructive 状态、延迟、断连、
半成功、确定失败、unknown outcome、protocol error、`isError`、schema mismatch、超大和敏感结果。

```bash
go test ./internal/mcp/infrastructure/testserver -count=1
```

预期：在 fake 尚未实现时失败。

### Step 2：实现进程内确定性 server

优先复用仓库现有 MCP transport/SDK 测试模式。每个 scenario 显式配置行为；提供线程安全 attempt 查询和 reset；
关闭时等待 goroutine，测试 cleanup 必须确定性完成。

### Step 3：写 API/DB 安全场景

使用真实 tenant schema 覆盖跨租户 approval、完整绑定、并发 resume、unknown outcome、动态工具不扩权和敏感
数据不落明文。不要 mock repository。

### Step 4：验证

```bash
STRATUM_TEST_POSTGRES_URL="$STRATUM_TEST_POSTGRES_URL" \
  go test -race ./internal/mcp/infrastructure/... ./internal/agent/infrastructure/persistence/... \
  ./api/http/... -count=1
```

预期：测试数据库可用时通过；缺失环境变量必须明确 skip 原因，CI 必须提供该变量。

### Step 5：提交

```bash
git add internal/mcp/infrastructure/testserver internal/agent/infrastructure/persistence api/http
git commit -m "[test](agent): add deterministic tool permission harness"
```

## Task 9：接通真实 Agent Loop 与 SSE 场景

**Files:**

- Create: `internal/agent/application/tool_permission_e2e_test.go`
- Modify: `api/http/handler/agent_exec_handler_test.go`
- Modify: `test/e2e/testutil.go`
- Create: `test/e2e/agent_tool_permission_test.go`
- Modify: SSE event DTO only if `unknown_outcome` lacks an existing representation

### Step 1：写真实 Loop 失败场景

使用确定性 LLM stub 依次产出：伪造未授权 call、危险 call、批准后的相同 call、篡改参数 call。断言可见工具、
SSE `approval_required`、resume 结果、fake MCP attempts、checkpoint 和 audit 一致。

```bash
go test ./internal/agent/application ./api/http/handler \
  -run 'TestToolPermissionE2E|TestExecuteStream.*Approval' -count=1
```

预期：在 Harness 接线完成前失败。

### Step 2：补齐运行时接线

只补测试暴露的缺口。`deny`、`approval_required`、`unknown_outcome` 和内部错误必须是不同的 domain/application
错误，由 middleware 维持冻结错误体。

### Step 3：验证完整后端场景

```bash
go test -race -timeout 30s ./internal/agent/... ./internal/mcp/... ./api/http/... -count=1
```

预期：通过，无残留 goroutine 或 fake server。

### Step 4：提交

```bash
git add internal/agent api/http test/e2e
git commit -m "[test](agent): verify tool permissions through agent loop"
```

## Task 10：补齐审批 UI 状态与浏览器黄金路径

**Files:**

- Modify: `web/src/modules/agent/pages/AgentChatPage.tsx`
- Modify: `web/src/modules/agent/hooks/useChatPage.ts`
- Modify: `web/src/modules/agent/hooks/ChatStreamContext.tsx`
- Modify: `web/src/modules/agent/api/agent.api.ts`
- Modify: `web/src/modules/agent/model/agent.ts`
- Create: `web/src/constants/agent.ts` if approval behavior constants do not already have a domain file
- Modify: `web/src/modules/agent/pages/__tests__/AgentChatMobile.test.tsx`
- Create: `web/src/modules/agent/hooks/__tests__/useChatPage.test.tsx`
- Create: `web/e2e/agent-tool-permission.spec.ts`

### Step 1：先读同域页面与测试模板

确认现有 SSE、approval API、Ant Design message/Modal 和 Playwright fixture 风格。禁止跨 `pages/` import 或新建
Axios client。

### Step 2：写失败的组件/E2E 测试

覆盖六条黄金路径：read 成功、destructive 暂停、管理员批准后一次执行、拒绝、过期/撤权/unknown outcome、
member 无审批入口且跨租户不可见。

```bash
make fe-lint
make fe-build
cd web && npx playwright test e2e/agent-tool-permission.spec.ts
```

预期：缺失状态展示或权限控制的场景失败。

### Step 3：实现最小 UI

审批展示结构化摘要和风险，不显示敏感明文。成功通知 `duration <= 2`，失败通知 `duration: 0`；unknown outcome
明确提示需要对账，不能显示为可重试成功。所有用户可见文本使用中文。

### Step 4：浏览器验证

在桌面和移动视口执行六条黄金路径，检查 loading/success/error 三态、无重叠、无 console error。临时服务由
Harness 管理并在结束时关闭。

### Step 5：提交

```bash
git add web
git commit -m "[feat](agent): expose safe tool approval states"
```

## Task 11：接入阻断式 CI 与风险门禁

**Files:**

- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`
- Modify: `.github/workflows/memory-e2e.yml` only if extending the existing service-backed E2E job is cleaner than a new job in `ci.yml`
- Modify: `scripts/quality/risk-regression-guard.sh`
- Modify: `scripts/quality/risk-regression-guard-test.sh`
- Create/Modify: targeted test runner script only if existing Make targets cannot express the suite
- Modify: `docs/agent/agent.md`
- Modify: `docs/agent/agent-chat-flow.md`

### Step 1：写 guard 失败测试

验证 Agent/MCP/Skill/IAM/审批相关文件变化会触发 tool-permission suite，数据库依赖缺失在 CI 中是失败而不是
静默 skip。守卫自身必须传播子命令失败。

```bash
bash scripts/quality/risk-regression-guard-test.sh
```

预期：新增路由尚未接入时失败。

### Step 2：增加分级 target

PR 快速层运行纯决策、状态机和 API/DB；命中高风险路径时运行 Agent Loop + fake MCP；发布层由现有 E2E
工作流运行浏览器和 race。避免让所有无关文档改动启动数据库和浏览器。

### Step 3：更新事实文档

只记录最终实现和实际命令；若实现与设计不同，明确更新边界，不保留过时描述。

### Step 4：运行门禁

```bash
make risk-guardrails
make fe-lint
make fe-build
go vet ./...
go test -short ./...
```

预期：全部通过。

### Step 5：提交

```bash
git add Makefile .github/workflows scripts/quality docs/agent
git commit -m "[ci](agent): gate tool permission regressions"
```

## Task 12：完整验收与收口

**Files:**

- Modify: only files required by failures discovered during verification
- Create: E2E evidence artifact only if repository policy already tracks that artifact type

### Step 1：运行后端 PR 级验证

```bash
go vet ./...
go test -short ./...
go test -v -race -timeout 30s ./...
```

预期：全部通过；任何 skip 都列出名称和理由，关键 P0 不允许 skip。

### Step 2：运行前端和浏览器验证

```bash
make fe-lint
make fe-build
# 使用 stratum-e2e-development 规定的现有命令运行完整浏览器/API/DB 链路
```

预期：六条黄金路径通过，桌面/移动无布局冲突，无 console error。

### Step 3：运行安全与仓库门禁

```bash
make risk-guardrails
git diff --check origin/main...HEAD
```

预期：通过；secret scan 覆盖 tracked worktree。

### Step 4：核对设计不变量

逐项记录 P0-1 至 P0-10 的测试名称、层级和运行结果。确认没有将 unknown outcome 误报为失败或成功，没有
把 approval 当授权，没有新增 server 通配或 sticky approval。

### Step 5：提交验证修复

若验证产生必要修复，按所属 scope 单独提交；无修复则不创建空提交。

## PR 验收清单

- What：引入确定性授权、Execution/Result Guard、审批恢复安全语义、unknown outcome 和跨层 Harness。
- Why：阻止模型、用户或不可信 MCP 绕过工具权限，并为关键不变量提供可重复证据。
- HowToTest：列出 risk guard、Go short/race、前端 lint/build、真实 API/DB/Agent Loop/fake MCP 与浏览器命令。
- 明确剩余边界：无通用 ABAC；无 server 通配；无 sticky approval；不支持幂等的外部工具不能自动恢复未知结果。
