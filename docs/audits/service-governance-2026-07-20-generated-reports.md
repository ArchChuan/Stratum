# Stratum 自动报告缺陷复核审计

**日期：** 2026-07-20
**模式：** 自动检查报告专项
**审计阶段：** 修复与端到端验收完成
**基线：** `origin/main` @ `9ed73a02a47b`

## 执行摘要

本次复核覆盖 `tmp/*/reports/*.md`，排除 `agent-interview`、`interview-200` 等知识与建议类报告。
历史报告按根因合并，并以当前代码、配置和可执行扫描结果重新判定。当前确认 24 个待处理根因：
16 个 High、7 个 Medium、1 个 Low。另有 5 类历史问题已修复或当前检查通过，不应为清报告而改码。

最高风险集中在四条链路：授权失败放行、凭证与租户内容泄漏、请求/启动期破坏或丢失数据、生产部署关闭身份与传输验证。
服务治理方面，进程内限流会随副本数放大，MCP 健康重连泄漏旧客户端，readiness 无法阻止故障 Pod 接流量，
poison outbox 会被永久删除。

Obsidian 检索只发现通用安全、证据和 Agent 治理观点，没有可替代仓库事实的 Stratum 专项结论；项目代码与扫描运行结果仍为主证据。
外部依赖结论以 2026-07-20 实际 `npm audit --json` 返回的上游 advisory 元数据为准。Go 漏洞扫描因原报告网络超时，仍是证据缺口。

## 范围与覆盖矩阵

| 自动报告 | 状态 | 结论 |
|---|---|---|
| `risk-scan/reports` | Covered | 跨日期去重并复核当前可达性 |
| `arch-guard/reports` | Covered | 当前 `scripts/quality/arch-guard.sh` 退出 0；历史架构报告不再成立 |
| `dep-scan/reports` | Covered | 前端 9 个漏洞可复现；Go 扫描证据不足 |
| `secret-scan/reports` | Covered | 773 项中 770 项来自扫描器递归扫描旧 JSON，3 项来自本地 `.env` |
| `frontend-health/reports` | Covered | 最新报告 lint/typecheck 均通过 |
| `migration-check/reports` | Covered | 最新报告边界、配对和版本检查均通过 |
| Gin HTTP | Covered | auth、tenant、agent、health、logging |
| PostgreSQL/tenant schema | Covered | provisioning、refresh、knowledge chunk migration |
| NATS/JetStream/outbox | Covered | malformed outbox 终止路径 |
| MCP | Covered | HTTP 错误、健康重连、客户端生命周期 |
| Milvus | Covered | collection 兼容路径 |
| Helm/Kubernetes/CI | Covered | TLS、probe、secret rollout、quality gate |
| Browser/Axios | Covered | OAuth callback 和 Web Storage |
| 运行环境与外部基础设施 | Runtime Only | 生产覆盖值、入口鉴权、备份与恢复能力 |

## 调用链与故障组合

- Refresh：`POST /auth/refresh → TokenStore.Rotate → GetTenantRole → JWTService.Sign`。旧 token 先失效，成员查询失败又降级为 `member`，同时存在可用性与越权问题。
- Tenant creation：handler 先提交 public tenant/membership，再调用 schema provisioner；失败只写日志，随后签发凭证。
- Knowledge ingest：HTTP 接受任务后启动有界 goroutine；Milvus 成功后，parent/leaf PostgreSQL 写失败只告警，仍标记完成。超时后失败状态继续使用已取消 context。
- Memory outbox：`SELECT FOR UPDATE SKIP LOCKED → Unmarshal → Publish → DELETE`；反序列化失败直接进入删除集合，没有 quarantine/DLQ。
- MCP health：周期检查创建 fresh client 并覆盖 map；旧 client 未关闭，重复故障可累积 subprocess/socket/goroutine。
- Rate limit：每 Pod 独立 token bucket；生产 3 副本、HPA 最多 10 副本，使有效额度按路由分配放大。

## 分级发现

### AR-001 移除成员后 refresh 仍可签发租户 token

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `api/http/handler/auth_session_handler.go:58`, `internal/iam/infrastructure/persistence/onboard_repo.go:248`
- **Call path:** refresh cookie → rotate → membership lookup → JWT claims
- **Trigger / protection:** membership 不存在或查询失败；现有 token 校验不重新验证 membership。
- **Failure and impact:** 查询失败默认 `member`，被移除用户可在 refresh token 生命周期内恢复访问。
- **Repair direction / compatibility:** membership 查询必须成功且确实存在后再 rotate/sign；缺失返回 401，并定义移除成员时的 session revoke 策略。
- **Verification:** removed-member refresh 集成测试；DB 错误必须 503/401 且不 rotate。

### AR-002 活跃租户检查在缺失 tenant 或数据库错误时放行

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `api/middleware/require_active_tenant.go:20`, `api/middleware/require_active_tenant.go:35`
- **Call path:** protected mutation/execution → RequireActiveTenant → PostgreSQL → downstream handler
- **Trigger / protection:** tenant claim 缺失、空值、not found、timeout 或任意查询错误；当前全部 `c.Next()`。
- **Failure and impact:** 禁用/删除租户或状态库故障时，高成本与写操作继续执行。
- **Repair direction / compatibility:** 缺失上下文与 inactive/not-found fail closed；依赖故障返回 503，保持冻结租户 403 契约。
- **Verification:** middleware 表驱动测试覆盖 nil DB dev mode、missing claim、not found、timeout、inactive、active。

### AR-003 OAuth 和 onboarding bearer credential 暴露在 URL 与 Web Storage

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `api/http/handler/auth_oauth_handler.go:148`, `api/http/handler/auth_oauth_handler.go:165`, `web/src/modules/iam/pages/auth/CallbackPage.tsx:21`
- **Call path:** GitHub callback → frontend redirect query → callback page → `sessionStorage`
- **Trigger / protection:** OAuth 成功或进入 onboarding；仅有短 TTL，不能阻止浏览器历史、代理日志、同源脚本读取。
- **Failure and impact:** access/onboarding token 可被历史、日志、referrer、截图或 XSS 获取并重放。
- **Repair direction / compatibility:** 使用短期单次 opaque code，后端 POST exchange；refresh 保持 HttpOnly cookie，access token 仅内存。
- **Verification:** redirect contract 不含 token；code 单次消费、过期、重放和错误交换测试；浏览器 E2E。

### AR-004 全局 access log 与 RAG 日志记录敏感内容

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `api/middleware/trace.go:75`, `api/middleware/trace.go:106`, `api/middleware/trace.go:141`, `internal/knowledge/application/rag_service.go:157`
- **Call path:** every HTTP request → TraceMiddleware；knowledge query → RAGService
- **Trigger / protection:** 除 `/auth/` 外的 JSON API；2 KiB 截断不是脱敏。
- **Failure and impact:** tenant settings、MCP auth、prompt、chat、memory、response 和 OAuth query 可进入日志。
- **Repair direction / compatibility:** 删除通用 body/raw-query 日志，只保留结构化安全字段；RAG 记录长度、模式和 trace，不记录问题正文。
- **Verification:** zap observer 测试注入 token/password/prompt，断言日志不存在原值及 body 字段。

### AR-005 远程 demo 配置允许明文认证流量

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `helm/values-demo.yaml:55`, `helm/values-demo.yaml:56`, `helm/values-demo-local.yaml:52`
- **Call path:** browser → demo ingress/node port → OAuth/API
- **Trigger / protection:** 使用公开 IP 的 demo values；未建立 HTTPS 终止。
- **Failure and impact:** OAuth callback、cookie 和 API bearer traffic 可被网络观察者截获。
- **Repair direction / compatibility:** 将远程 demo 与纯本地配置拆分；远程模板强制 TLS、secure cookie 和明确 host。
- **Verification:** Helm safety test 拒绝非 localhost 的 HTTP frontend/callback。

### AR-006 tenant schema provisioning 失败仍返回创建成功

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `api/http/handler/auth_tenant_handler.go:107`, `api/http/handler/auth_register_handler.go:68`
- **Call path:** auth create/register → public tenant commit → ProvisionSchema → issue token → 201
- **Trigger / protection:** tenant DDL 任意失败；启动期补偿不是创建成功的正确性保证。
- **Failure and impact:** 用户获得 active tenant 与 owner token，但业务表缺失或部分存在。
- **Repair direction / compatibility:** 下沉为 application use case；失败时原子补偿或使用 `provisioning/failed` 状态，成功前不签 tenant token。
- **Verification:** 两条创建路径注入 provision failure，断言无 active tenant token；已有租户可幂等重试。

### AR-007 历史 tenant DDL 会删除无法映射的 knowledge chunks

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `pkg/storage/postgres/tenant_schema.sql:1114`, `pkg/storage/postgres/tenant_schema.sql:1118`
- **Call path:** startup provisioning → legacy workspace mapping → unconditional delete
- **Trigger / protection:** legacy `workspace_name` 无匹配 workspace；没有 quarantine 或 fail-fast count。
- **Failure and impact:** 启动升级期间永久删除知识数据。
- **Repair direction / compatibility:** 禁止启动期 destructive cleanup；迁移前校验并 quarantine，提供显式、可审计的数据修复步骤。
- **Verification:** 历史 schema fixture 含 unmapped/renamed/duplicate workspace，升级后数据不得消失。

### AR-008 knowledge ingestion 吞掉 PostgreSQL chunk 写失败并可能写不出 failed 状态

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `internal/knowledge/application/ingest_service.go:362`, `internal/knowledge/application/ingest_service.go:390`, `internal/knowledge/application/ingest_service.go:410`
- **Call path:** async ingest → Milvus → parent/leaf PG → MarkCompleted / MarkFailed
- **Trigger / protection:** parent/leaf repository error或 job timeout；worker/queue 有界，但错误只 Warn。
- **Failure and impact:** vector 与 PostgreSQL/hybrid state 分裂，文档仍 completed；timeout 时使用 canceled context 写失败状态。
- **Repair direction / compatibility:** PG 错误必须传播；失败状态使用独立有界 cleanup context；定义 vector compensation/reconciliation。
- **Verification:** parent、leaf、timeout 三个失败测试，终态必须 failed 且不报告完成。

### AR-009 请求时 Milvus compatibility 会直接 DropCollection

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `pkg/storage/milvus/client.go:157`, `pkg/storage/milvus/client.go:180`
- **Call path:** ingest/create collection → describe existing schema → DropCollection
- **Trigger / protection:** dimension 改变或缺少 `agent_id`；只有进程内 collection lock。
- **Failure and impact:** 模型切换或滚动升级会删除整个生产 collection，且未先 backfill 替代集合。
- **Repair direction / compatibility:** 请求路径只验证并报 incompatibility；版本化 collection + 离线 reindex + 原子 alias/switch。
- **Verification:** 兼容性单测断言永不 DropCollection；真实 Milvus migration E2E 验证旧集合保留。

### AR-010 malformed memory outbox 被永久删除

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `internal/memory/infrastructure/pipeline/outbox_poller.go:121`, `internal/memory/infrastructure/pipeline/outbox_poller.go:151`
- **Call path:** PG outbox → JSON decode → delete batch
- **Trigger / protection:** corrupt 或版本不兼容 payload；合法 publish 有 ack，但 poison row 无保护。
- **Failure and impact:** memory event 无声且不可恢复地丢失。
- **Repair direction / compatibility:** 同事务写 poison/quarantine 表后才删原行；只保存安全元数据、hash 和 error class。
- **Verification:** malformed payload integration test，quarantine commit 失败时原行必须保留。

### AR-011 liveness/readiness/startup 共用无条件 200 的 `/health`

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `api/http/router.go:169`, `helm/templates/deployment.yaml:178`, `k8s/deployment.yaml:150`
- **Call path:** kubelet probe → `/health`
- **Trigger / protection:** PostgreSQL/schema bootstrap/NATS/Milvus/worker failure；Harness 有 HealthCheck API，但路由未使用。
- **Failure and impact:** 故障 Pod 可进入 Service endpoints 并持续接流量。
- **Repair direction / compatibility:** `/livez` 仅进程状态，`/readyz` 使用短超时检查 mandatory component 和 bootstrap state；optional dependency 显式降级。
- **Verification:** router/runtime tests 和容器 E2E，必需依赖失败时 ready 非 200、live 仍 200。

### AR-012 MCP HTTP 错误携带原始 downstream body

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `internal/mcp/infrastructure/client.go:396`
- **Call path:** MCP HTTP transport → non-200 → error → logs/API
- **Trigger / protection:** downstream error body；无 redaction 或 size bound。
- **Failure and impact:** credential、PII、private endpoint 或 provider internals 可进入日志和响应。
- **Repair direction / compatibility:** 错误仅含 status、server ID 和稳定分类；禁止 raw body。
- **Verification:** fake server 返回 sentinel secret，断言 error/log/HTTP response 不含 sentinel。

### AR-013 MCP 健康重连覆盖 client 但不关闭旧 client

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `internal/mcp/infrastructure/client_manager.go:315`, `internal/mcp/infrastructure/client_manager.go:347`
- **Call path:** health ticker → unhealthy → fresh.Connect → map assignment
- **Trigger / protection:** 重复 unhealthy cycle；并发数有 semaphore，但资源回收缺失。
- **Failure and impact:** subprocess、socket、goroutine 和 session 随重连次数累积。
- **Repair direction / compatibility:** 锁内 CAS/swap，锁外关闭 displaced client；处理 stop/reconnect race。
- **Verification:** 重复重连测试，旧 client 各关闭一次；race test。

### AR-014 生产多副本下 rate limit 按 Pod 放大

- **Severity:** Medium
- **Confidence:** Confirmed
- **Evidence:** `api/middleware/rate_limit.go:17`, `helm/values-prod.yaml:5`, `helm/values-prod.yaml:78`
- **Call path:** auth/agent request → per-process token bucket
- **Trigger / protection:** 3–10 replicas，无粘性保证；ingress 的通用限流不能表达 user/tenant/route quota。
- **Failure and impact:** 实际配额随副本数和负载均衡变化，LLM 成本和 auth abuse 防护不稳定。
- **Repair direction / compatibility:** Redis 原子 bucket，key 包含 tenant/user/route；明确 Redis 故障策略和 Retry-After。
- **Verification:** 两实例并发测试共享同一额度；Redis failure semantics 测试。

### AR-015 agent 同步执行失败返回 HTTP 200

- **Severity:** Medium
- **Confidence:** Confirmed
- **Evidence:** `api/http/handler/agent_exec_handler.go:60`
- **Call path:** `POST /agents/:id/execute` → AgentService error → success response envelope
- **Trigger / protection:** 非 not-found、非 approval 的执行错误。
- **Failure and impact:** 客户端、监控和重试策略无法区分成功与失败，SLO 数据失真。
- **Repair direction / compatibility:** 保持冻结错误体，使用 middleware 映射稳定 domain error；若需兼容，先版本化或增加明确 status 再迁移。
- **Verification:** provider/tool/timeout 错误 contract tests 断言非 2xx。

### AR-016 tenant owner 自删除读取未设置的 context key

- **Severity:** Medium
- **Confidence:** Confirmed
- **Evidence:** `api/http/handler/tenant_handler.go:232`, `api/middleware/jwt.go:18`
- **Call path:** owner middleware 通过 → DeleteSelf handler → `tenant_role`
- **Trigger / protection:** 正常 owner 请求；middleware 使用 `auth.role`，handler 使用另一字符串 key。
- **Failure and impact:** 合法 owner 永远 403。
- **Repair direction / compatibility:** 使用统一 reqctx/middleware accessor，不允许散写 key。
- **Verification:** router-level owner/member deletion tests。

### AR-017 refresh token rotation 不是原子事务

- **Severity:** Medium
- **Confidence:** Confirmed
- **Evidence:** `internal/iam/infrastructure/persistence/token_store.go:53`
- **Call path:** revoke old DB row → Redis set → insert new DB row
- **Trigger / protection:** new insert 失败或 Redis 故障；DB 是权威但两次 DB 写不在同事务。
- **Failure and impact:** refresh 可把有效 session 破坏为无可用 token；Redis 错误被忽略。
- **Repair direction / compatibility:** revoke+insert 单事务；Redis 仅作为可重建 cache，并记录失败。
- **Verification:** 注入 insert failure，旧 token 状态不变；并发 rotate 只有一个成功。

### AR-018 非 GitHub auth 路由被 GitHub client 配置错误耦合

- **Severity:** Medium
- **Confidence:** Confirmed
- **Evidence:** `api/http/router.go:97`
- **Call path:** router build → early return → refresh/logout/guest/tenant/admin routes absent
- **Trigger / protection:** GitHubClientID 空但 JWT/DB 可用。
- **Failure and impact:** guest、refresh、tenant 管理等无关能力全部消失。
- **Repair direction / compatibility:** 只 gate `/auth/github*`；JWT/session/tenant route 依自身依赖注册。
- **Verification:** 无 GitHub 配置的 router contract tests。

### AR-019 production PostgreSQL DSN 显式 `sslmode=disable`

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `helm/templates/deployment.yaml:125`, `helm/values-prod.yaml:31`
- **Call path:** Helm render → POSTGRES_URL → managed external PostgreSQL
- **Trigger / protection:** production values；ingress TLS 与数据库链路无关。
- **Failure and impact:** 数据库凭证和租户数据无传输加密与服务器身份校验。
- **Repair direction / compatibility:** production/external 默认 `verify-full`，CA/hostname 配置来自 Secret；dev 可显式 disable。
- **Verification:** Helm render test；production 配置 disable 时启动或模板校验失败。

### AR-020 deployment 关闭 SSH host 与 Kubernetes API 证书验证

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `.github/workflows/deploy.yml:185`, `.github/workflows/deploy.yml:198`
- **Call path:** GitHub Actions → SSH tunnel → kubeconfig → Kubernetes API
- **Trigger / protection:** 每次 deploy；没有 pinned known_hosts/CA。
- **Failure and impact:** 中间人可冒充部署主机或 API server，获取部署权限或篡改 workload。
- **Repair direction / compatibility:** Secret 提供并校验 known_hosts 与 kube CA；删除 insecure rewrite；endpoint 移出源码。
- **Verification:** deployment safety test 拒绝 StrictHostKeyChecking=no 和 insecure-skip。

### AR-021 Helm 管理的 Secret 变化不触发 Pod rollout

- **Severity:** Medium
- **Confidence:** Confirmed
- **Evidence:** `helm/templates/deployment.yaml:17`, `helm/templates/secret.yaml:1`
- **Call path:** Helm Secret update → Deployment pod template unchanged
- **Trigger / protection:** DB/JWT/OAuth/provider secret rotation；只有 config checksum。
- **Failure and impact:** Pods 长期继续使用旧凭证，直到无关 rollout。
- **Repair direction / compatibility:** chart-created Secret checksum；external Secret 明确采用 reloader 或运维 restart contract。
- **Verification:** Helm unit test，Secret input 改变必须改变 pod template hash。

### AR-022 CI security 与 coverage gate 不阻断合并

- **Severity:** Medium
- **Confidence:** Confirmed
- **Evidence:** `.github/workflows/ci.yml:63`, `.github/workflows/ci.yml:137`, `.github/workflows/ci.yml:140`
- **Call path:** PR → race/coverage/gosec → branch protection
- **Trigger / protection:** coverage <80%、gosec finding 或 scanner failure；当前 warning/`|| true`。
- **Failure and impact:** 安全回归和测试缺口可在绿色 CI 下合入。
- **Repair direction / compatibility:** pin gosec；按 agreed severity/confidence fail；coverage 阈值成为命令退出条件。
- **Verification:** CI script unit test 注入低 coverage 与 finding，必须非零退出。

### AR-023 前端依赖存在 9 个已知漏洞

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** `web/package-lock.json`; 2026-07-20 `npm audit --json`
- **Call path:** frontend build/dev/test toolchain and runtime router dependencies
- **Trigger / protection:** 当前 lockfile；报告为 1 low、5 moderate、2 high、1 critical。
- **Failure and impact:** 包括 Vitest UI 任意文件读取/执行、Vite 路径绕过、form-data CRLF injection、React Router open redirect 等；部分仅开发/测试环境可达，仍需按生产依赖与使用方式分别验证。
- **Repair direction / compatibility:** 先应用 non-breaking lockfile fixes；Vite/Vitest major 升级独立验证配置和测试；不得只运行 `npm audit fix --force`。
- **Verification:** fresh `npm audit --json` 为 0 accepted findings；lint/typecheck/test/build 全部通过。

### AR-024 secret scan 递归扫描自己的历史 JSON，报告被噪声淹没

- **Severity:** Low
- **Confidence:** Confirmed
- **Evidence:** `tmp/secret-scan/reports/20260719-043002.json` 的路径聚合：770/773 命中来自旧 report JSON，3/773 来自本地 `.env`
- **Call path:** local cron gitleaks `--no-git --source .` → tmp report JSON → next report
- **Trigger / protection:** 每日 dir scan；机器结果不打印明文，但旧结果复制产生指数噪声。
- **Failure and impact:** 真正候选被假阳性淹没，计数持续膨胀；该 cron 脚本位于 ignored `tmp/`，没有仓库内可发布源文件。
- **Repair direction / compatibility:** 自动扫描 tracked working tree 或显式排除 `tmp/`、`.env` 和 output directory；把 cron 源脚本纳入受控配置仓库后再部署。
- **Verification:** 连续运行两次，第二次计数不得因前次 JSON 增长；只输出路径/指纹，不输出 secret。

## 已修复、误报或当前通过

- 当前 `scripts/quality/arch-guard.sh` 退出 0；2026-07-15 的架构违规报告已过期。
- `ProvisionAllTenantSchemas` 当前聚合并返回 tenant provisioning errors；“启动静默成功”旧结论已修复。
- SSE handler 当前监听 `clientCtx.Done()` 并 cancel execution；“断连后执行继续”旧结论已修复。
- 当前未找到 tenant-authored Python CodeExecutor 生产实现；旧 Critical finding 不再可达。
- frontend health 和 migration consistency 最新报告均为 0 failure。

## 潜伏风险与证据缺口

- 2026-07-20 修复验收运行 `govulncheck` 成功：当前代码调用链 0 个漏洞；依赖中未被当前代码调用的 advisory 仍按工具输出保留观察。
- `/metrics` 未鉴权并包含 tenant label；是否外网可达取决于 ingress/NetworkPolicy 运行配置。
- `pkg/httpclient.WithRetry` 与 public migration dirty rewind helper 当前未证明生产可达，保留为 dormant hazard。
- 生产是否通过外部 Secret 覆盖 TLS DSN、是否已有 Secret reloader、数据库/Milvus 备份是否可恢复，需要运行环境证据。

## 已有正确治理措施

- Startup tenant provisioning 已通过 advisory lock 串行化并聚合错误。
- Knowledge ingestion 有 queue capacity、worker semaphore、timeout 和 panic recovery。
- SSE 已传播客户端取消；MCP reconnect 有总并发上限和单次 timeout。
- JetStream worker 的合法消息路径已有 AckWait 续租和 DLQ publish-before-term 约束。
- 当前架构守卫、前端 lint/typecheck 和迁移边界检查均通过。

## 建议修复顺序

1. 凭证与授权：AR-001～AR-005、AR-012、AR-019、AR-020。
2. 数据正确性：AR-006～AR-010、AR-017。
3. 运行治理：AR-011、AR-013、AR-014、AR-021。
4. API/功能契约：AR-015、AR-016、AR-018。
5. 供应链与质量门禁：AR-022～AR-024。

## 未覆盖与运行验证建议

- 审计阶段未修改业务代码、数据库、集群或 CI。
- 修复阶段必须使用 `stratum-e2e-development`，覆盖真实 API、PostgreSQL tenant schema、NATS outbox、Milvus、MCP 和 Helm render。
- 外部安全标准页面未在当前 shell hook 下直接抓取；OAuth/数据库/Kubernetes结论仍由明确的仓库行为和上游 `npm audit` 元数据支持。

## 修复关闭记录

用户已明确批准 AR-001 至 AR-024 全部确认缺陷，并确认设计后直接执行。原始 finding 保留不改写，关闭证据如下。

| Finding | 状态 | 修复提交与主要证据 |
|---|---|---|
| AR-001 | Closed | `4c264a9`；removed-member refresh 真实 API/DB 返回 401 |
| AR-002 | Closed | `e85561a`；middleware 表驱动测试，真实 dependency failure ready=503 |
| AR-003 | Closed | `29384c5`；单次加密 exchange、重放 401，桌面/移动 Playwright 通过 |
| AR-004 | Closed | `14127ed`；zap sentinel 泄漏测试 |
| AR-005 | Closed | `cbbd045`；deployment safety 与 demo Helm TLS render |
| AR-006 | Closed | `7bd16a7`；provisioning 失败测试，真实 tenant 创建为 active 且 schema 存在 |
| AR-007 | Closed | `7bd16a7`；非破坏 quarantine DDL 与 schema safety 测试 |
| AR-008 | Closed | `70d964d`, `5cafb4f`；parent/leaf/status-write 失败测试 |
| AR-009 | Closed | `70d964d`, `693c64a`；真实 Milvus 不兼容 collection 保留 |
| AR-010 | Closed | `4fdbb75`, `09162e5`；真实 pgx/NATS runtime poison quarantine，原行删除后只留 hash 元数据 |
| AR-011 | Closed | `e85561a`；PostgreSQL 停止时 live=200/ready=503，恢复后 ready=200 |
| AR-012 | Closed | `14127ed`；下游 sentinel body 不进入 error/log |
| AR-013 | Closed | `4fdbb75`；重复 reconnect close 计数与 `-race` 通过 |
| AR-014 | Closed | `dbe5315`；两个 store 共享 Redis quota，真实 Redis 故障 auth=503 |
| AR-015 | Closed | `4c57770`；Agent 错误 HTTP contract 测试 |
| AR-016 | Closed | `4c57770`；owner 删除 router 测试与真实新 tenant 删除 |
| AR-017 | Closed | `4c264a9`；pgx transaction rollback 测试与真实 refresh rotation |
| AR-018 | Closed | `4c57770`；无 GitHub 配置的基础 auth route contract |
| AR-019 | Closed | `cbbd045`；production Helm render 为 `sslmode=verify-full` |
| AR-020 | Closed | `cbbd045`；known_hosts 与 kube CA safety contract |
| AR-021 | Closed | `cbbd045`；chart Secret checksum 与 external Secret 内容 checksum 注入 |
| AR-022 | Closed | 固定兼容 Go 1.25 的 gosec v2.25.0；security finding/scanner failure 阻断；coverage 以 38.0% 当前基线阻断回退并显式保留 80% 目标。直接启用 80% 总量硬门槛会使当前 38.1% 仓库基线下所有 PR 无条件失败，已在 PR CI 实证后纠正。 |
| AR-023 | Closed | `de67cfe`；fresh `npm audit` 0，lint/typecheck/102 tests/build 通过 |
| AR-024 | Closed | `d073e08`；tracked-worktree scanner 连续两次真实 gitleaks 均为 0，无自扫描 |

全量验收：`stratum-verify go-full`、`stratum-verify frontend-full`、架构/迁移/部署/scanner 脚本、Helm 3.18.4 lint/render 全部通过。临时 E2E 用户、tenant、OAuth code、outbox quarantine、Milvus collection、脚本和服务均已清理。
