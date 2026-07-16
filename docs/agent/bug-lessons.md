# 近期 Bug 经验沉淀

本文归纳 2026-06-27 至 2026-07-16 的 Git 修复历史与 claude-mem 记录。目标不是保存提交时间线，而是把已经修复、已有根因证据和验证结果的问题转成防复发规则。

## 使用边界

- Git 提交用于确认最终改动，claude-mem 用于补足现场现象、根因链和验证证据；结论冲突时以当前代码和可重复验证为准。
- 合并提交、重复 cherry-pick、同一问题的连续修补合并为一条经验。
- 未修复的审计发现、临时排障命令和仅有猜测的记录不写成强制规则。
- 本文解释规则为何存在；具体实现以当前代码及 `docs/agent/*.md` 专项规范为准。

## 高频硬规则

1. **先确认契约所有者**：URL 前缀、回调语义、配置键、DTO 字段和错误分类只能有一个权威来源；适配层不得再次拼接或复制。
2. **按完整生命周期修改数据**：创建、查询、更新、删除、重建、迁移、测试夹具及向量/缓存副本必须一起核对。
3. **可选设施不得改变业务结果**：指标、追踪、日志和心跳未初始化或失败时，不得覆盖原始错误、制造 panic 或提前终止状态转换。
4. **权限以后端为准**：前端隐藏只是体验；写操作必须在路由或 handler 侧校验角色，并用真实低权限身份验证。
5. **生产问题以运行现场为证据**：核对实际镜像摘要、路由日志、Pod/容器状态和依赖健康，不能用本地源码代替线上事实。
6. **部署配置必须端到端同名同形**：应用读取、Helm 模板、Secret、ConfigMap、CI 变量和运维文档必须共同验证。

## 1. API 与组件契约

### 经验

- “清空我的记忆”在生产返回 404，并非后端缺少路由，而是共享 Axios/Nginx 已拥有 `/api` 公共前缀，业务模块又传入 `/api/memory/clear`，形成重复前缀。
- 移动端 `ResponsiveDataView` 初版只触发 Table 级 `onChange`，遗漏 `pagination.onChange`；补齐回调后又因比较两个不同 TypeScript 函数签名导致 typecheck 失败。
- Agent 请求字段、路由挂载位置等问题也表明：只看调用点或只看服务端定义都不足以确认线上的完整契约。

### 防复发规则

- 前端 API 模块只传后端相对路径；公共前缀由共享 client 或反向代理统一拥有。新增请求必须通过仓库级 guard 检查字面量重复前缀。
- 封装第三方组件时，先记录并测试其事件传播语义；桌面与移动替代视图必须保持分页、筛选、排序和操作回调等行为等价。
- DTO、路由和配置字段使用实际定义生成测试样例，禁止凭命名习惯猜测 snake_case、前缀或参数形状。
- 修复契约问题至少验证调用参数、代理后的最终路径、后端路由命中和用户可见结果。

## 2. 权限与错误语义

### 经验

- 普通 tenant member 曾能看到或触发 Agent、Skill、MCP 的修改入口。最终修复同时增加后端角色门禁、路由限制与前端操作隐藏。
- Skill 草稿发布校验失败曾返回 500。根因不是 handler，而是领域校验错误没有稳定 sentinel、包装链未使用 `%w`、HTTP 映射也缺少对应 400 分类。
- OAuth 路由还暴露过 JWT PKCS#8 解析路径未覆盖的问题；认证限流过低也会把合法登录流量误判为攻击。

### 防复发规则

- 权限矩阵必须覆盖每个写入口；后端授权是安全边界，前端仅同步可用性。测试至少包含 owner/admin 成功与 member/guest 拒绝。
- 领域可预期校验失败使用可 `errors.Is` 匹配的 sentinel，并保持“定义 -> `%w` 包装 -> HTTP 映射 -> handler 测试”完整链路。
- 4xx/5xx 按责任归属划分：请求或业务状态不可接受是 4xx，服务端故障才是 5xx；不得用字符串匹配错误文本。
- 认证修复应覆盖真实密钥格式、回调超时、限流容量和完整登录跳转，而不只验证单个解析函数。

详见 `docs/agent/api.md`、`docs/agent/backend-go.md`。

## 3. 数据生命周期与 Schema

### 经验

- E2E 曾维护独立 tenant DDL，逐步漏掉 `rebuild_after`、`conversation_id` 等列；修一列后下一列继续失败。最终改为测试直接调用生产 schema provisioner。
- `tenant_schema.sql` 曾在 `CREATE TABLE knowledge_chunks` 前执行 `ALTER TABLE knowledge_chunks`，全新租户按顺序执行立即失败。
- Knowledge workspace 曾遗漏 `chunking_strategy` 持久化/透传，删除 workspace 时只删 child chunks、遗留 parent chunks；中文全文检索在 `simple` parser 下召回不足。
- Memory 清理和 Knowledge 删除跨 PostgreSQL、Milvus、parent/child 表及派生数据，单表成功不能代表业务删除完成。

### 防复发规则

- 测试、开发和生产共用 canonical schema provisioner；禁止复制 tenant DDL。迁移、`public_schema.sql`、`tenant_schema.sql` 必须按其职责同步。
- Schema 测试同时覆盖空库从零创建和旧版本升级；DDL 必须满足创建先于修改、扩展先于依赖对象。
- 新增持久化字段必须验证写入、读取、默认值、更新、API 透传和真实数据库 round-trip。
- 删除/清理前画出所有主数据、派生表、父子记录、向量集合、缓存和异步队列；测试验证全部目标及租户隔离。
- 数据库能力依赖扩展或定制镜像时，同时验证 extension、text search config、索引、镜像内容和降级行为。

详见 `docs/agent/migration-tenant.md`、`docs/agent/knowledge-workspace.md`、`docs/agent/milvus.md`。

## 4. 异步处理、并发与可观测性

### 经验

- Memory worker 的 Prometheus 变量在未调用 `RegisterMetrics` 时为 nil。业务错误已正确 `MarkFailed` 后，指标调用 panic，recover 又用 panic 文本二次 `MarkFailed`，覆盖了原始 `llm timeout`。
- JetStream DLQ 首版在 metadata 失败、publish/Term 交界和 heartbeat 停止时序上仍有竞态；去重 ID 若包含可变错误分类，同一原消息会产生多份死信。
- 外部连接重建、worker goroutine 和回调关闭过程曾出现 TOCTOU、send-on-closed-channel、锁内阻塞等风险。

### 防复发规则

- 指标、日志和 trace 是可选副作用：未注册时安全 no-op，失败时保留原始业务错误；recover 不得无条件覆盖已落库的失败原因。
- 消息只能有一个最终 disposition。状态顺序固定为：处理/续租 -> 发布下一阶段或 DLQ -> 停止 heartbeat -> Ack/Term；失败路径 Nak 并保留重试机会。
- DLQ 去重键只使用原 stream 与 sequence 等稳定身份，不包含会随重试变化的错误文本或分类；DLQ 不保存用户正文和原始敏感错误。
- 并发资源的检查和使用必须处于同一同步边界；锁外执行网络 I/O；channel 由单一所有者关闭。
- 测试需覆盖延迟 publish、metadata 错误、停止与 disposition 竞争、重复投递和 race detector。

详见 `docs/agent/backend-go.md`、`docs/agent/nats.md`、`docs/agent/observability.md`。

## 5. 前端响应式与用户工作流

### 经验

- 移动端适配不是把 Table 换成 Card 即完成：分页回调、危险操作、筛选、空状态、modal 宽度、抽屉焦点和可访问名称都曾在后续审查中补漏。
- tenant settings 出现长 URL/输入内容撑破视口；共享 overlay 需要同时限制 viewport 宽度和内部内容换行。
- 单测通过不等于前端可交付：曾出现 Vitest 全绿但 `tsc` 失败，也出现 jsdom/Ant Design CJK 文本渲染差异导致脆弱断言。
- Skill 草稿测试曾执行成功但 UI 丢弃输出，用户只能看到笼统成功提示。

### 防复发规则

- 响应式替代视图必须复用同一数据源与命令回调，测试桌面/移动两种渲染下的分页、筛选、查看、编辑和删除。
- 固定格式控件和 overlay 设置明确的宽高约束；长 URL、长单词、错误信息和动态按钮文本必须纳入窄视口测试。
- 前端完成门禁是 unit test + typecheck + lint + production build；真实用户流再用浏览器跨视口验证。
- 可访问性断言优先使用 role/name 语义并容忍组件库合法的文本分隔；样式断言验证用户可观察布局，不绑定 jsdom 偶然实现。
- 执行型功能必须展示后端返回的 stdout/result/error，不能只显示布尔成功状态。

详见 `docs/agent/frontend.md`、`docs/agent/product.md`。

## 6. 测试必须复现业务意图

### 经验

- Memory E2E 先后通过补字段、改 UUID、补 scope 才暴露测试夹具与生产 schema 漂移；只修当前断言会形成连续假绿。
- PostgreSQL lazy connect 使“连接对象创建成功”不等于数据库可用；跳过条件必须执行实际 ping。
- SSE/真实依赖测试若静默 skip，CI 会显示绿色却没有覆盖关键链路；Memory E2E workflow 也曾未因路径过滤触发。
- 真实 PostgreSQL 集成测试最终发现并验证了 legacy schema 替换、数据保留、加密审批和 exactly-once 状态机等单测无法证明的行为。

### 防复发规则

- 失败先区分产品 Bug、测试夹具漂移、环境不可用和错误断言，禁止为了 CI 绿色先改业务代码。
- 数据库可用性用 `Ping`/真实查询确认；依赖缺失的 skip 必须在输出和交付说明中显式列出。
- 关键 workflow 增加触发路径回归检查；PR 必须确认预期 job 实际出现并执行，而非只看已有 check 绿色。
- 单测验证分支与错误语义，集成测试验证真实协议/数据库，E2E 验证用户目标；三者不能互相替代。

## 7. 外部依赖、超时与降级

### 经验

- GitHub OAuth token exchange 没有边界时会拖住登录请求，最终增加 10 秒超时。
- Milvus 必须等待 etcd 与 MinIO 健康；StatefulSet 使用滚动更新在单副本持久化依赖上会因旧 Pod 占用 RWO volume 卡死。
- 认证限流阈值与访客登录链路的额外请求叠加，曾导致登录延迟和误限流。
- 重试、熔断若分散在 gateway、client 和 worker 多层会放大请求；预算必须按完整调用链设计。

### 防复发规则

- 每个外部调用有显式 timeout，且不超过上层请求剩余预算；重试仅由一个明确层负责并使用有界退避。
- readiness 表示关键依赖可承载流量，不只表示进程存活；可降级依赖必须把能力缺失暴露到日志、指标和响应语义。
- 单副本 RWO 状态依赖使用适合的更新策略，并通过 init/health gate 保证依赖顺序。
- 限流按完整用户工作流计数和压测，区分认证入口、普通 API 与高成本执行端点。

## 8. CI/CD、Helm 与 K3s

### 经验

- 应用读取 `POSTGRES_URL`/`REDIS_URL`，Helm 一度注入拆分的 `POSTGRES_HOST`/`REDIS_ADDR`，Pod 会退回 localhost 默认值。
- Registry 登录地址、镜像 repository 地址、Secret key 和模板引用曾多次不一致，导致 DNS 失败、ImagePullBackOff 或空凭据。
- 使用可变 tag 会让部署无法证明运行的是本次构建；配置改了但 Pod 未滚动也会继续使用旧值。
- K3s 公网 kubeconfig 存在 server 地址、证书 SAN、SSH tunnel 等多层问题；临时关闭 TLS 校验只能作为受控回退。
- Milvus/MinIO 凭据、镜像内容和依赖健康曾出现“模板看起来完整、运行时才失败”的情况。

### 防复发规则

- 建立应用配置键到 ConfigMap/Secret/Helm/CI 的映射表；`helm template` 后检查最终 env 名称、Secret key 引用和敏感值未泄漏。
- Registry host 用于登录，完整 repository 用于镜像；CI 在 build 前验证 host 可解析、凭据可登录、目标 repository 可访问。
- 构建和部署使用 commit SHA 等不可变 tag，并在部署后核对 Pod image ID/摘要；配置变化通过 checksum annotation 触发 rollout。
- CD 的成功标准包括：测试、镜像推送、rollout、Pod readiness、依赖健康和真实后端 smoke test；任一步失败输出非敏感诊断。
- 生产故障先 SSH 查看实际日志和资源状态，并核对运行版本；不得从本地分支推断远端根因。
- 运维文档、Helm values 和 workflow 同批更新；禁止文档声明不存在的重置、DLQ 或降级能力。

详见 `docs/agent/deployment-architecture.md`、`docs/deployment/k3s-demo.md`。

## 修复覆盖索引

| 主题 | 代表性 Git 证据 | claude-mem 证据 |
|------|-----------------|-----------------|
| Schema 单一来源与执行顺序 | `bde40c4` `05d2916` `c8e0a38` `3f0bf5d` | #8328 #8343 #8782 #8783 |
| 可选指标不影响业务 | `3c9612a` | #8733 #8734 |
| 外部调用超时与并发 | `81a9db5` `7d9c965` `53eec72` | #8185 #8186 |
| Knowledge 完整生命周期 | `a4ea700` `1997bc3` | #10972 #10974 #11031 #11073 |
| 前端 API/组件契约 | `95478c0` `ccacf6a` `5bb7b9a` `7f766f7` | #11597 #11598 #11696 #11697 #12299 #12317 |
| 移动端完整工作流 | `637a722` `6caeaa8` `aecddc1` `681da2d` | #11601 #11620 |
| RBAC 与错误映射 | `445fb53` `a53789f` | #12241 #12271 #12644 |
| 认证与限流 | `04959da` `6e3497b` `99d7ec7` | 真实登录链路及部署记录 |
| 异步 DLQ 终态 | `563e847` `629558e` | `docs/audits/service-governance-2026-07-15.md` 修复记录 |
| CI/CD 与 K3s | `ca1ce26` `a4c687a` `9eb652d` `7c0c916` `121f1b0` `8347489` `f998717` | #9013 #9230 及 7 月 3-6 日部署记录 |
