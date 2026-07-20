# 风险清扫经验固化与报告收口设计

## 目标

从 AR-001 至 AR-024 的修复过程提炼可执行约束，建立编码前、提交时和 CI/测试三层防错 harness，降低同类风险再次进入代码库的概率。同时完成本地历史报告收口，但不把自动报告或统一报告作为受版本控制的长期事实源。

## 范围

- 提炼本次风险清扫中已经由代码、测试和真实运行验证的通用规则。
- 将能够可靠自动判断的规则落入脚本、hook、linter、契约测试或 CI。
- 在 `AGENTS.md` 和 `CLAUDE.md` 中只保留需要人工判断的精简规则和自动化入口。
- 保留本地非知识类报告文件及来源元数据，删除其中已解决的风险正文。
- 将本地风险索引集中到 `tmp/risk-consolidated/reports/latest.md`。
- 排除 `agent-interview`、`interview-200` 和其他知识、访谈、建议类报告。

## 非目标

- 不新增受版本控制的统一风险报告。
- 不让 Git hook 或 CI 读取被忽略的 `tmp` 文件。
- 不把 AI 生成的风险判断直接作为阻断条件。
- 不在本次改动中迁移或删除 Hermes 调用的 `tmp/cron` 任务脚本。
- 不修改知识、访谈和建议类报告。

## 报告收口

### 本地统一报告

统一报告位于 `tmp/risk-consolidated/reports/latest.md`，包含：

1. 纳入和排除的报告来源；
2. 当前仍开放的风险或证据缺口；
3. AR-001 至 AR-024 的精简关闭索引；
4. 每项经验对应的 harness 落点；
5. 验证日期和主要运行证据。

该文件是本地复核索引，不进入 Git，也不作为代码事实的替代品。

### 历史报告处理

对纳入范围的本地 Markdown 报告：

- 保留标题、生成时间、生成器、运行模式、扫描基线和非风险执行结果；
- 删除已解决 finding 的说明、影响和建议；
- 增加迁移说明，指向本地统一报告；
- 只有统一报告中仍开放的证据缺口才能继续保留；
- 同步处理各目录的 `latest.md` 链接目标；
- 不修改 JSON、SARIF 等机读证据，也不修改被排除的知识类报告。

现有 `docs/audits/service-governance-2026-07-20-generated-reports.md` 在规则与验证证据提炼完成后从仓库删除，避免形成第二个长期台账。

## 三层防错 Harness

### 第一层：编码前约束

在 `AGENTS.md` 和 `CLAUDE.md` 中加入内容一致的高风险改动检查表，只保留无法稳定静态判断的原则：

- 授权、租户状态和外部依赖查询失败时必须 fail closed，禁止默认角色或默认放行；
- bearer credential 不得进入 URL、Web Storage、通用请求日志或下游错误正文；
- tenant-scoped 操作必须显式携带并校验 tenant ID，数据库访问必须经过租户边界封装；
- 请求和启动路径禁止自动执行 DropCollection、不可逆清理或无法审计的数据修复；
- 持久化失败必须向上传播，失败状态写回失败也必须暴露；
- 替换连接、client 或 worker 时必须关闭旧资源并等待 goroutine 退出；
- 涉及认证、租户、迁移、消息、向量库或外部依赖时，必须添加对应的失败路径与真实链路验证。

入口文档只说明“为什么”和“何时检查”，并指向统一的快速守卫命令，不复制 24 项历史 finding。

### 第二层：提交时快速守卫

新增受版本控制的 `scripts/quality/risk-regression-guard.sh` 及测试，根据 staged 或传入的改动路径调用现有确定性检查：

- `api/wiring` 或架构边界改动：运行 `arch-guard.sh`；
- migration 和 tenant schema 改动：运行 `check-migration-boundaries.sh` 及其测试；
- Helm、部署 workflow 和生产连接配置改动：运行 deployment safety 检查；
- IAM、HTTP contract、knowledge、memory、MCP、rate limit 和 readiness 改动：运行对应的定向 Go 测试包；
- 前端认证和依赖清单改动：运行 typecheck、相关认证测试或依赖审计；
- 所有 staged 文件继续由 gitleaks 检查。

快速守卫只组合已经存在且可重复的工具，不使用 AI。对于不能可靠静态判断的语义，不新增宽泛正则阻断。

pre-commit 以单一 hook 调用快速守卫；开发者也可直接运行同一命令。脚本在没有相关改动时快速退出，在工具缺失时明确失败或按已定义的开发环境契约给出错误，不静默跳过。

### 第三层：CI 与回归测试

CI 使用同一个快速守卫的全量模式，并继续执行 race、覆盖率、gosec、前端审计和真实 E2E。AR-001 至 AR-024 按下列类别映射到长期保护：

| 风险类别 | Finding | 长期保护 |
|---|---|---|
| 授权和 token 原子性 | AR-001、AR-002、AR-003、AR-017、AR-018 | IAM handler/application/infrastructure 测试、OAuth 浏览器测试、真实 PostgreSQL 回滚测试 |
| 敏感信息与错误边界 | AR-004、AR-012 | 日志 sentinel 测试、错误响应契约、gitleaks/gosec |
| 租户和数据安全 | AR-006、AR-007、AR-008、AR-009、AR-010 | tenant schema 顺序测试、持久化失败测试、Milvus 保留测试、outbox quarantine 集成测试 |
| 可用性和资源治理 | AR-011、AR-013、AR-014、AR-015、AR-016 | readiness 故障测试、client close/race 测试、Redis 共享配额测试、HTTP contract 测试 |
| 部署和供应链 | AR-005、AR-019、AR-020、AR-021、AR-022、AR-023、AR-024 | Helm render、deployment safety、阻断式 security/coverage gate、npm audit、tracked-worktree secret scan |

语义性风险由测试约束；可机械判断的配置和依赖风险由脚本或 CI 约束。任何保护都必须能在无 AI 的环境中重现。

## Hermes 现状与边界

Hermes 当前只接管调度和隔离执行，仍通过 `tmp/cron/hermes-run-task.sh` 分发并调用各任务脚本。现存锁文件和最新报告时间证明该链路仍在运行，因此本次不能直接删除 `tmp/cron`。

后续迁移必须按以下顺序独立执行：

1. 将任务实现迁入 Hermes 管理的稳定入口；
2. 修改 Hermes 任务定义，不再引用 `tmp/cron`；
3. 对每个任务执行一次真实调度并验证新鲜产物和失败码；
4. 确认没有调用方后再删除旧脚本和专用 worktree。

## 失败处理

- 快速守卫的子检查失败时原样返回非零，不吞掉输出。
- 检查器不可用时不得把结果标记为通过。
- 定向测试映射不明确时，退回相关模块测试或全量检查，而不是猜测成功。
- 本地报告清理前保留生成元数据和机读证据指针。
- 删除受版本控制审计文件前，必须确认其有效规则已进入入口文档、脚本或测试。

## 验证标准

- `AGENTS.md` 和 `CLAUDE.md` 包含一致的高风险检查表和同一个快速守卫入口。
- 快速守卫测试覆盖路径路由、无关改动快速退出、子检查失败传播和工具缺失。
- pre-commit 与 CI 调用同一个受版本控制的守卫脚本。
- AR-001 至 AR-024 均能映射到现有或新增的确定性保护；不能自动保护的项在入口检查表中明确。
- 本地统一报告存在，历史报告保留元数据但不再重复已关闭风险正文，知识类报告内容未变。
- 受版本控制的旧审计报告已删除。
- 架构、迁移、部署、secret scan、Go、前端和相关 E2E 验证通过。
