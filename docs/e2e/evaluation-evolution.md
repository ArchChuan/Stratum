# 评测与进化中心端到端证据

## 验收范围

目标矩阵覆盖 Skill、Agent、MCP、Knowledge 的已发布基线、基线运行、有界候选、候选运行、金丝雀、
反馈归因和人工晋级/回滚；共享失败场景覆盖 LLM、评测、安全停止、Opik、MinIO、provider/MCP/Knowledge
依赖故障、幂等重放与稳定版本继续服务。浏览器仅使用 DOM 和 HTTP 断言，覆盖 1440×900 与 390×844，
不生成截图。

## 固定版本和来源

- Go：`go.mod` 的 `go 1.25.0` / `toolchain go1.25.12`。
- PostgreSQL：`postgres:16.4-alpine`（Task 7 隔离 compose）。
- MinIO：`minio/minio:RELEASE.2024-07-16T23-46-41Z`。
- OTEL Collector：`otel/opentelemetry-collector-contrib:0.96.0`，与仓库 compose 一致。
- Opik：`2.1.32`，官方 release `https://github.com/comet-ml/opik/releases/tag/2.1.32`，审阅的 upstream
  commit 为 `69eb4a5812d067d1fbe07437bdec50ecf316adb4`，compose 位于 tag 内
  `deployment/docker-compose/docker-compose.yaml`，并使用同目录 `clickhouse_config/` 与
  `nginx_default_local.conf`。通过 `E2E_OPIK_COMPOSE_FILE` 引用该完整目录，禁止用 Jaeger 静默替代。

## 场景与证据契约

- `center.spec.ts`：四类资源的 HTTP 查询、tenant not-found、运行/候选/实验/decision 时间线、推荐可见、
  明确人工命令、刷新持久化、桌面与移动路径。
- `failure-modes.spec.ts`：member read/admin command、重复 idempotency、稳定 serving、失败投影脱敏，及
  真实 SQL `promotion_evidence` 扫描；同时验证真实 MCP 加密 revision/tool call、Agent provider 失败恢复、
  Skill 精确 published revision、Knowledge 引用与 Milvus 依赖故障恢复。
- `fixtures.ts`：只从临时 manifest 和进程环境取得生成 ID/token；token 不写入 Web Storage；SQL 使用
  精确 tenant schema 和资源 ID；扫描 bearer、已知 secret、raw payload marker、upstream body marker。
- `bootstrap.go`：通过公开 API 创建 Skill、Agent、MCP、Knowledge 资源及 baseline/suite/run，轮询真实 worker
  终态；SQL 只用于既有 UI 投影和最终持久化证据，不替代四类 `ResourceAdapter.ExecuteRevision` 执行。

## 2026-07-23 执行记录

- `bash scripts/quality/risk-regression-guard.sh --explain`：通过，输出七条高风险原则。
- 初次 Playwright RED：失败，根因是既有 `web/playwright.config.ts` 的 `testDir=web/e2e`，无法发现新的
  根级 Task 7 spec；随后增加专用配置。
- 首次隔离服务 RED：PostgreSQL/MinIO 健康后 OTEL 容器退出；根因是误用了依赖 Jaeger、Loki、Docker
  日志挂载和外部变量的仓库全局 collector 配置。修复为 Task 7 临时目录内的最小 OTLP → Opik 配置，
  并通过 host-gateway 访问固定的 Opik 2.1.32 端口。
- 第二次 Opik RED：官方 compose 同时包含 image/build，profile 启动会触发本地 backend/python/frontend
  构建。修复为 `--no-build` 且只显式启动 mysql、redis、clickhouse-init、clickhouse、zookeeper、
  minio、mc、backend；临时 override 仅暴露 backend 8080，并关闭 AI/guardrails/python evaluator。
  脚本静态断言排除 python-backend、frontend、demo-data-generator、guardrails-backend、otel-collector。
- 第三次 Opik RED：`mc` 与 `clickhouse-init` 正常以 0 退出，但 compose `--wait` 将已退出 service 视为
  非运行失败。修复为分别运行两个 initializer、检查 `ExitCode=0`，再只对长期运行服务和 backend 使用
  `--wait`。
- 第四次 Opik RED：`docker compose wait/ps --format json` 在当前 Compose 版本对已退出 initializer 返回
  `no containers for project`。修复为用完全相同的 project/双 compose 文件参数取得 `ps -aq` CID，并
  轮询 `docker inspect` 的状态与退出码；仅接受 `exited 0`。
- 第五次 Opik RED：长期服务 `up` 再次解析依赖并重启 `clickhouse-init`，backend migration 与
  ClickHouse 配置写入竞态，连接 8123 被拒绝。修复为先等待 mysql/redis/zookeeper/minio，再只运行
  一次 initializer；ClickHouse 和 backend 均用 `--no-deps`，ClickHouse 连续五次健康后才启动 backend，
  并断言 initializer CID 未变化。
- 第六次 Opik RED：ClickHouse 本地 health 已健康且无重启，但从 Opik Redis 容器访问 8123 仍失败。
  tagged `clickhouse_config` 没有 `listen_host`，容器 health 因 loopback 产生假阳性。Task 7 override 只读
  挂载最小 `<listen_host>0.0.0.0</listen_host>` 配置，并保留跨容器连续十次成功门禁。
- 第七次 RED：Opik backend 健康后，Stratum guest bootstrap 返回非 201。先增强边界证据：同时轮询
  `/health` 与 `/readyz`；bootstrap 只报告 HTTP status 和脱敏后的冻结 `{error}`；失败时仅输出经过已知
  key、bearer、credential/raw/upstream marker 过滤的后端日志尾部。
- 第八次 RED：路由、Axios 和 API 测试共同证明 `/auth/guest` 没有 `/api/v1` 前缀；真实 404 来自
  `api/wiring/platform.go` 仅在 `GITHUB_CLIENT_ID` 非空时构造 JWT service，而 `registerAuth` 在 JWT service
  为空时整组不注册。Task 7 不修改该现有装配耦合，只在隔离进程设置非生产 dummy client ID、空 secret，
  并在启动前断言 dummy ID 与临时 RSA key 都存在；E2E 不调用 GitHub OAuth。
- 第九次 RED：cleanup 只终止后台 subshell，遗留 `go run` 子进程占用 18080；下一轮误把旧 DB/server
  的健康响应当成当前轮次，造成跨轮次污染。修复为 backend/frontend 各用独立 process group，cleanup
  终止整个组并 wait，ready 后校验 PID 存活；Opik backend 还通过精确 compose `rm -sf backend` 清理。
- 第十次 RED：新进程和新数据库隔离后 `/health` 成功但 `/readyz` 连续失败；当前 readiness 响应只给
  `not_ready`，不暴露具体 component。为了继续收集主链路证据，脚本将其作为显式 concern 输出，最多
  轮询 30 秒后使用 `/health` 继续；最终状态不得把 readiness 记为通过。
- 第十一次 RED：真实 readiness/guest 均通过后，host `psql` 实际是缺少 PostgreSQL client 的
  `pg_wrapper` shim。修复为 bootstrap 与 Playwright SQL 证据都通过 `docker exec -i` 调用本轮精确
  PostgreSQL CID 内的 `psql`，不依赖宿主客户端且不可能命中共享数据库。
- 第十二次 RED：default tenant schema 存在，但缺少当前分支新增的 `eval_suites` 等 evaluation 表。
  fixture 不复制 DDL，而是在 seed 前事务内 `SET LOCAL search_path` 并幂等应用仓库唯一 tenant baseline
  `pkg/storage/postgres/tenant_schema.sql`，同时形成 migration-order 证据。
- 第十三次 RED：bootstrap 把 guest tenant UUID 的连字符替换成下划线，违反 `pkg/storage/postgres/tenant.go`
  的真实 `tenant_` + 原始 tenant ID 规则；未命中的 `search_path` 使未限定 DDL 落入 `public`。修复为保留
  原始 tenant ID，并对 PostgreSQL identifier 做双引号转义；Playwright SQL fixture 同步使用同一规则。
- 第十四次 RED：响应式用例的文本 locator 同时命中多处资源 ID/系统建议，且实验表首行是已回滚的
  Knowledge 实验。修复为按 table cell、heading 和 manifest 中 Skill experiment ID 使用 role locator；Ant
  Design 的中文按钮 accessible name 为“晋 级”，因此 locator 允许字符间空白。
- 第六次 Opik RED：ClickHouse 本地 health 已健康且无重启，但 backend 容器网络到 8123 仍短暂拒绝连接。
  修复为额外从已健康的 Opik Redis 容器执行 `nc -z clickhouse 8123`，要求连续十次成功后才启动 backend。
- 本机检测到测试 LLM 环境变量名称，未输出值；没有读取或修改任何生产 tenant。
- `npm --prefix web run test -- --run src/modules/evaluation`：通过，10 个文件、111 个测试，无失败。
- `go test -race ./internal/evaluation/... ./internal/agent/... ./internal/mcp/... ./internal/knowledge/...`：
  通过；所有含测试的目标包 race 检查成功，仅无测试文件的包报告 `[no test files]`。
- 后续真实执行 RED/GREEN：增加公开 baseline API；MCP 使用本地 JSON-RPC server 完成 discovery 与
  `tools/call`，revision payload 写入 MinIO 后验证为非空密文且不含 server ID/URL 明文；Agent 使用租户加密
  Qwen-compatible provider 配置，两轮 LLM 调用触发真实 MCP tool，Opik exact trace/tool trace/event 均可查询。
- provider 503 场景发现兼容客户端会把原始响应体写入 case error 与日志；修复 Complete/Stream/Embedding
  错误边界后，仅保留 provider、operation 和 HTTP status。评测 job 成功记录失败 case/run，恢复 provider 后
  同一 stable revision 再次成功。
- Skill 通过公开 create → activation confirm → publish → Agent binding → `/evaluations/runs` 链路执行；断言
  精确 published revision、trace、tokens 与本地 LLM 请求计数。
- Knowledge 使用 `etcd:v3.5.16`、`milvus:v2.4.15` 和本地 1024 维 embedding helper 完成 workspace、multipart
  ingest、document completed、direct retrieval、baseline 与 evaluation run；引用保留真实 document/chunk identity。
  依赖故障通过可控 TCP proxy 切断 Stratum→Milvus 连接，Milvus→etcd 保持健康，重新启用后同一 revision 恢复。
- Knowledge 首轮发现 `AssertionExact` 按 JSON 字节顺序比较 struct/map；改为规范化 JSON 语义比较并保留字符串
  exact 与不可 marshal 失败。另修复 Knowledge embedding resolver 忽略 `QWEN_BASE_URL` 的问题，回归测试证明
  请求只命中租户配置的本地 endpoint。
- Opik/Collector 稳定性证据：Collector 接入 Opik project network，OTLP `projectName=Default Project`；所有
  compose/network 操作后重新解析动态 OTLP/metrics host port。Opik 在 Milvus 并发启动时 ClickHouse migration
  会读超时，因此固定启动顺序为基础服务 → Opik ready → Milvus/proxy → Stratum。exact evidence 在加载环境下
  使用共享 90 秒总预算，仅重试 404/502/503/504，最终仍要求 200 且证据非空。
- `STRATUM_WORKTREE_ROOT=... E2E_OPIK_COMPOSE_FILE=... E2E_OPIK_VERSION=2.1.32 bash
  scripts/e2e/evaluation-evolution.sh`：最终通过，14/14 Playwright 用例通过，run ID
  `e2e-evolution-1784808479-139567`；脚本 exit 0 后完成隔离进程、容器、volume 和临时凭据文件清理。
- 当前状态：四 provider 的真实执行、HTTP/DOM、tenant schema、权限、幂等、加密、Opik trace、Milvus 故障
  恢复、稳定版本继续服务和人工决策证据均已通过；未访问或修改生产 tenant，也未调用生产 LLM endpoint。

## 清理要求

脚本以 trap 停止自己启动的后端、前端和隔离 compose，删除临时 JWT、manifest、日志和 secret 文件；
数据库清理由删除整个 Task 7 专属 volume 完成，不触碰共享/生产数据库。任何 skip 或清理失败均使脚本失败。
