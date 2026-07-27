# 平台助手规划完成度加固设计

**状态：已确认，进入实施**

## 1. 背景

内置平台助手的一期、二期和远程验收修复已经合入 `main`，但逐条对照
`2026-07-23-builtin-platform-assistant-design.md`、两份阶段计划及
`2026-07-24-remote-acceptance-remediation.md` 后，仍有部分完成证据不足：

- Phase 2 PostgreSQL E2E 使用统一假 Applier，没有证明 Agent、Skill、MCP、Knowledge 四个本域
  application service 和 repository 的真实写入、回读与租户边界。
- 两份 Playwright 用例拦截了业务 API，没有证明浏览器、HTTP handler、application 和 PostgreSQL
  形成同一条链路。
- 计划明确要求的过期、已知失败、未知结果、中断恢复和完整安全输入矩阵没有全部进入真实链路门禁。
- Phase 2 要求记录确认前草案编辑次数，当前只有提案结果与审阅时长指标。
- Demo 已部署 Opik，但运行证据表明当前租户没有 admin/owner，也没有 LLM Provider。远程真实模型执行缺少
  合法前置条件，不能通过伪造 JWT、提升存量账号或引入平台共享模型来补证。

本设计只补齐原规格已经要求的完成证据和指标，不扩展产品范围。

## 2. 目标与非目标

### 2.1 目标

1. 以可追踪矩阵证明一期、二期和远程修复计划的每个完成门禁。
2. 四类提案在 PostgreSQL E2E 中调用真实本域 application service 和 repository，并验证创建、更新、
   回读、租户隔离、冲突和凭据边界。
3. 至少一条管理员审阅流程穿过真实浏览器、前端 API client、Gin handler、ProposalService 和 PostgreSQL。
4. 把过期、已知失败、未知结果、重复确认、中断恢复和敏感输入纳入阻断式测试。
5. 持久化每条提案的草案编辑次数，并用有界 Prometheus histogram 建立人工返工基线。
6. 提供 CD 可复现的远程验收器；缺少合法管理员或 Provider 时明确报告前置条件未满足，不伪成功。

### 2.2 非目标

- 不新增平台共享模型、测试模型或 Provider 兜底。
- 不伪造远程管理员、JWT claims、API Key 或资源写入结果。
- 不放宽成员权限，不新增删除、Skill 发布、MCP 执行或 Knowledge 上传能力。
- 不因测试方便绕过 `execTenant`、真实 handler 或共享 Axios client。
- 不把测试 MCP、测试租户或浏览器夹具部署到 Demo。

## 3. 方案选择

采用“生产能力最小补齐 + 测试链路真实化”。保留现有 Profile、ProposalService、四域 adapter 和审阅页；
生产代码只增加编辑计数及指标，主要工作放在可复现 E2E harness。

拒绝以下替代方案：

- 在 Demo 内置假管理员或假模型：违反租户模型与权限边界，得到的是伪造证据。
- 继续用统一 fake Applier 和网络拦截：测试稳定，但无法证明跨层契约和真实持久化。
- 仅记录剩余限制：不能满足“完成所有规划”的目标。

## 4. 数据与指标

`resource_change_proposals` 增加：

```sql
edit_count INT NOT NULL DEFAULT 0
```

Tenant schema 必须在建表定义后紧跟 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`，支持历史租户幂等升级。
`UpdateDraft` 在同一租户事务中更新 payload、baseline、状态、事件并原子递增 `edit_count`。Domain、repository
扫描、HTTP DTO 和前端 schema 携带该计数，但 UI 只将其作为审计信息展示，不允许客户端提交或覆盖。

管理员确认后记录：

```text
system_assistant_resource_proposal_draft_edits
  labels: kind, operation
  observation: edit_count
```

标签必须保持有界；tenant、actor、resource、proposal、payload 和错误正文不得成为标签。

## 5. 后端真实链路 E2E

E2E 使用临时 tenant schema，直接装配生产 repository/application/adapters：

- Agent：`PgAgentRepo` + `AgentService`。
- Skill：`PgSkillRevisionRepo` + `VersionService`。
- Knowledge：PostgreSQL workspace repository + `WorkspaceService`；不注入文档摄取服务，避免扩展到上传。
- MCP：生产 MCP service/manager/repository，连接由测试进程提供的无认证 `streamable-http` MCP server；
  更新凭据保留场景使用已存安全配置，测试输出只断言 marker 不出现，不打印原值。
- Proposal：`PgResourceChangeProposalRepo` + 生产 authorizer、baseline resolver、四域 adapter 和
  `ResourceChangeProposalService`。

真实链路覆盖四类资源创建与更新、回读、member 拒绝、系统 Agent 拒绝、过期、stale、已知失败、
`unknown_outcome`、并发单 claim、确认后中断恢复、租户隔离及敏感字段矩阵。外部依赖故障通过有明确
副作用边界的测试 double 注入到对应 port；只在这一边界使用 double，不替代四域成功主路径。

## 6. HTTP 与浏览器真实链路

新增测试 harness 启动真实 Gin proposal routes 和真实前端 dev/preview server。认证夹具只为本轮创建的临时
tenant/user 签发内存 token 或 cookie，并通过现有 IAM middleware 建立 admin/member 会话；业务请求不得
被 Playwright `page.route()` 拦截。

浏览器主路径：

```text
临时管理员登录
-> 打开真实提案 URL
-> GET 读取 PostgreSQL 提案及事件
-> 修改字段并 PATCH
-> 刷新后读回 edit_count 与 payload
-> 确认一次
-> production adapter 更新真实 Knowledge workspace
-> 页面显示 applied 与安全回读
-> PostgreSQL 验证 proposal、events、workspace 一致
```

桌面和移动视口都执行主路径。补充场景验证 member 门禁，以及 stale、expired、failed、unknown_outcome
终态不出现确认或重试入口。截图和 DOM overflow 检查不得包含 token 或凭据。

## 7. 远程验收

CD 后的远程验收分为两层：

1. 无凭据层必须通过：健康/readiness、Opik/Collector Ready、`/agents/executions` 200、Prompt 脱敏、
   tenant schema 升级、访客登录稳定、内部消息过滤。
2. 需租户配置层：只有远程存在合法 admin/owner 且租户 Provider 可用时，才执行真实平台助手对话和提案。
   不满足时输出结构化 `prerequisite_missing`，明确缺少的类别，不输出任何密钥或账号细节，也不把该项记为通过。

远程验收脚本保持只读或使用本轮合法账号创建的临时资源；资源清理必须精确限定本轮 ID。

## 8. 错误与恢复

- 权限、租户或 baseline 查询失败继续 fail closed。
- `expired` 在同一事务内持久化状态和事件。
- `unknown_outcome` 不提供 API/UI 重试；中断在 applying lease 后只允许转为 `unknown_outcome`。
- 已知无副作用失败进入 `failed`；可能已发生外部副作用的失败才进入 `unknown_outcome`。
- 终态不可逆，重复确认不再次调用 Applier。
- 指标或审计持久化失败必须向上传播，不得声称应用完成。

## 9. 完成标准

- 原 Phase 1、Phase 2、远程修复三份计划的完成门禁形成逐项证据矩阵，无“由单测推断真实链路”的条目。
- Go 单元、race、真实 PostgreSQL E2E、前端单测、lint、build、桌面/移动 Playwright、风险守卫全部通过。
- 四类提案成功主路径使用真实本域服务；浏览器主路径不 mock 业务 API。
- 编辑计数升级兼容新旧 tenant schema，指标标签有界。
- Demo 无凭据层通过；需配置层只有在合法前置条件存在时通过，否则保留为明确未满足条件，绝不伪造。
- 临时 schema、进程、脚本和浏览器数据均清理，工作区无残留。
