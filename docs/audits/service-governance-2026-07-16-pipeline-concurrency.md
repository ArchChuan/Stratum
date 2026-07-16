# Stratum 流水线并发与锁专项审计报告

**日期：** 2026-07-16

**模式：** 专项

**审计阶段：** 只读静态审计 + GitHub Actions 运行状态核验

## 执行摘要

当前流水线不安全，核心问题不是 CI 测试并发，而是 CD 缺少部署级互斥和“只允许最新主干提交发布”的顺序保证。

审计确认 2 个 High、3 个 Medium 风险。最高风险路径是：连续合并产生两条 `main` push → 两条 Deploy workflow 并行构建和推送 → 同时写共享依赖镜像 tag → 随后竞争同一个 Helm release。即使 Helm 自身拒绝同时升级，也只能把竞争转化为随机失败；它不能阻止较旧 workflow 在较新版本之后完成部署，从而把线上静默回滚到旧提交。

2026-07-16 14:46:39 和 14:46:54（Asia/Shanghai）启动的两条真实 Deploy run（`29477732367`、`29477745703`）相隔 15 秒，并在审计时同时运行 `Mirror dependency images to Aliyun CR`，证明并发窗口已经实际出现。

## 范围与覆盖矩阵

| 边界 | 状态 | 说明 |
|---|---|---|
| GitHub CI workflow | Covered | 检查触发、共享状态和并发取消 |
| GitHub Deploy workflow | Covered | 检查 build、registry、K3s、Helm 与验证顺序 |
| GitHub Environment | Covered | 仓库未配置 environment protection |
| 容器镜像仓库 | Covered | 检查应用 SHA tag 与依赖共享 tag |
| K3s / Helm release | Covered | 检查同 release 并发升级和最后写入者 |
| PostgreSQL 启动 DDL | Partial | 静态检查启动路径；未在远端制造并发 DDL |
| Gitee 镜像 workflow | Covered | 检查并发 force-push 顺序 |
| 应用业务请求治理 | Not Reviewed | 不属于本次流水线锁专项范围 |

## 调用链与并发组合

```text
main push A ─┐
             ├─ test → build SHA image → mirror shared tags → apply secrets → helm upgrade stratum
main push B ─┘
```

- workflow 没有 `concurrency`，A、B 可完整并行。
- 应用 backend/frontend 使用 `github.sha`，单个应用镜像不可变。
- PostgreSQL、MinIO 等依赖使用跨 run 共享 tag，并通过“先 inspect、后 push”更新。
- deploy job 不检查当前 SHA 是否仍是 `main` 最新 HEAD。
- 两条 run 若同时执行 Helm，同一 release 通常由 Helm pending 状态使其中一条失败。
- 两条 run 若错开执行 Helm，最后完成者获胜；最后完成者可能是旧提交 A。

## 分级发现

### SG-001 缺少部署串行化与最新提交门禁

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** [.github/workflows/deploy.yml](/home/yang/go-projects/stratum/.github/workflows/deploy.yml:1)、[.github/workflows/deploy.yml](/home/yang/go-projects/stratum/.github/workflows/deploy.yml:118)、[.github/workflows/deploy.yml](/home/yang/go-projects/stratum/.github/workflows/deploy.yml:211)
- **Call path:** `main` push / manual dispatch → Build and Deploy → Helm release `stratum`
- **Trigger:** 在一条约 5-10 分钟的部署完成前再次合并 `main`，或同时手动触发部署。
- **Existing protection:** 应用镜像使用 SHA tag；Helm 对同一 release 的进行中操作有内部 pending 状态。
- **Failure and impact:** 同时进入 Helm 时随机一条失败；错开进入 Helm 时旧 run 可在新 run 之后完成，线上静默回滚到旧 SHA。共享 Secret、ConfigMap 和 release 状态由最后写入者决定。
- **Repair direction:** workflow 顶层增加固定 deployment concurrency group，并明确 `cancel-in-progress` 策略；deploy 前再次查询 `main` 最新 SHA，旧 run 主动退出。推荐构建可并行、部署必须串行且只部署最新 SHA。
- **Compatibility concern:** 直接对整个 workflow 使用 `cancel-in-progress: true` 会中断镜像构建并浪费缓存；应把互斥放在 deploy job 或拆分 deploy workflow。
- **Verification:** 连续推送 A/B，证明只有 B 能进入 Helm；A 即使构建完成也必须在部署门禁退出。

### SG-002 共享依赖镜像 tag 存在 check-then-push 竞争

- **Severity:** High
- **Confidence:** Confirmed
- **Evidence:** [.github/workflows/deploy.yml](/home/yang/go-projects/stratum/.github/workflows/deploy.yml:85)、[.github/workflows/deploy.yml](/home/yang/go-projects/stratum/.github/workflows/deploy.yml:104)、[.github/workflows/deploy.yml](/home/yang/go-projects/stratum/.github/workflows/deploy.yml:112)
- **Call path:** concurrent Build and Push jobs → registry manifest inspect → build/pull → push shared dependency tag
- **Trigger:** 两条 run 在目标 tag 尚不存在时同时通过 manifest 检查，或不同提交修改 zhparser Dockerfile 但继续复用 `16-zhparser`。
- **Existing protection:** 已存在 tag 时跳过 push；应用自身镜像使用 SHA tag。
- **Failure and impact:** 两条 run 同时写相同 tag，最后 push 者获胜；旧提交可覆盖新依赖镜像。`minio:latest` 还会把上游可变版本固化到本地共享 tag。数据库镜像与 schema 预期不一致时可导致启动失败或中文检索降级。
- **Repair direction:** 依赖镜像使用内容版本或 Dockerfile hash tag，并让 Helm values 固定到不可变 tag/digest；镜像同步拆成独立、串行、显式版本化 workflow。
- **Compatibility concern:** 修改依赖 tag 后要同步 Helm values、镜像预热和运维文档。
- **Verification:** 两个不同 Dockerfile 内容并发构建时产生不同 digest/tag，部署渲染结果引用预期 digest。

### SG-003 应用启动 DDL 没有跨 Pod advisory lock

- **Severity:** Medium
- **Confidence:** Probable
- **Evidence:** [internal/platform/runtime/runtime.go](/home/yang/go-projects/stratum/internal/platform/runtime/runtime.go:48)、[pkg/storage/postgres/tenant.go](/home/yang/go-projects/stratum/pkg/storage/postgres/tenant.go:127)、[pkg/storage/postgres/tenant.go](/home/yang/go-projects/stratum/pkg/storage/postgres/tenant.go:246)
- **Call path:** Helm rollout → one or more new backend Pods → `BootstrapTenants` → public/tenant DDL
- **Trigger:** 重叠 rollout、多副本同时冷启动，或 Pod 重启与部署同时发生。
- **Existing protection:** 多数 DDL 使用幂等语句；单 tenant DDL 在事务中执行；PostgreSQL 自带对象锁。
- **Failure and impact:** public DDL 按语句逐条执行且没有全局事务或 advisory lock；多个 Pod 可交叉执行。对象锁可造成等待、死锁或某个 Pod 启动失败。当前 demo 副本数为 1，降低但不消除重叠 workflow 产生的并发启动窗口。
- **Repair direction:** 把 schema 迁移改为独立 pre-deploy job，或在 bootstrap 外层使用固定 PostgreSQL advisory lock；应用 Pod 只在迁移成功后 rollout。
- **Compatibility concern:** advisory lock 必须设置获取超时并确保异常连接释放；不能无限阻塞 readiness。
- **Verification:** 并发启动 3 个实例，确认只有一个执行 DDL，其余等待后正常启动；故障注入后 lock 可恢复。

### SG-004 集群准备步骤与业务部署共享同一无锁 job

- **Severity:** Medium
- **Confidence:** Confirmed
- **Evidence:** [.github/workflows/deploy.yml](/home/yang/go-projects/stratum/.github/workflows/deploy.yml:157)、[.github/workflows/deploy.yml](/home/yang/go-projects/stratum/.github/workflows/deploy.yml:203)
- **Call path:** every deploy → apply namespace/secrets → install/patch metrics-server → Helm deploy
- **Trigger:** 任意两条 deploy workflow 重叠。
- **Existing protection:** `kubectl apply` 和 secret dry-run/apply 大体幂等；metrics patch 失败被 `|| true` 忽略。
- **Failure and impact:** 多个 run 可交叉更新共享 Secret 和系统组件；若 secrets 在两次触发间变化，构建 SHA 与运行配置不再形成确定组合。metrics patch 的真实错误会被隐藏。
- **Repair direction:** 将集群 bootstrap 与应用 deploy 分离；bootstrap 低频、串行执行，应用 deploy 只更新版本化 release 输入。
- **Compatibility concern:** 拆分后要定义 Secret 轮换的独立审计流程。
- **Verification:** 应用部署日志不再包含系统组件安装；Secret 更新有独立 run 和变更记录。

### SG-005 Gitee 镜像可被较旧 run 的 force-push 覆盖

- **Severity:** Medium
- **Confidence:** Confirmed
- **Evidence:** [.github/workflows/mirror.yml](/home/yang/go-projects/stratum/.github/workflows/mirror.yml:3)、[.github/workflows/mirror.yml](/home/yang/go-projects/stratum/.github/workflows/mirror.yml:16)
- **Call path:** concurrent main pushes → independent Mirror runs → `git push --force`
- **Trigger:** 较旧 run 因网络或 runner 调度较慢，在较新 run 后完成 force-push。
- **Existing protection:** 无；两个 push 都是强制写入。
- **Failure and impact:** Gitee `main` 或 tags 可被回退到旧提交，镜像仓库短暂或持续落后 GitHub；下游若从 Gitee 构建会取得错误版本。
- **Repair direction:** 为 mirror workflow 增加 ref-scoped concurrency；推送前核对 GitHub 当前 ref SHA，旧 run 退出；避免无条件 `--force` 更新 tags。
- **Compatibility concern:** 若依赖重写历史，需要明确允许 force 的人工维护流程，不能复用自动镜像 workflow。
- **Verification:** 连续推送 A/B 并人为延迟 A，最终 Gitee 必须停留在 B。

## 潜伏风险与证据缺口

- 未确认阿里云镜像仓库是否支持 tag immutability；当前 workflow 逻辑假设共享 tag 可覆盖。
- 未在远端数据库执行并发 DDL 故障注入，SG-003 的具体失败形态需要运行验证。
- 未检查组织级 GitHub Actions runner 配额；它只影响排队，不构成可靠部署锁。
- 仓库 API 未返回任何 GitHub Environment，当前 deploy job 也未声明 `environment`，因此没有 environment 级审批或互斥证据。

## 已有正确治理措施

- backend/frontend 应用镜像使用 `${{ github.sha }}`，避免应用镜像 tag 本身漂移。
- deploy 在 Helm 后执行 rollout status，失败可见。
- Helm 使用固定 release 和 namespace，能通过 pending release 状态拒绝真正同时的 upgrade。
- CI 与 Memory E2E 使用独立 GitHub-hosted runner/service，不共享测试数据库，CI 并发主要影响成本而非业务正确性。
- migration boundary guardrail 能阻止 tenant 表被错误放入 numbered public migration，但它不是运行时迁移锁。

## 建议修复顺序

1. SG-001：先增加部署串行化和 latest-SHA 门禁，立即消除旧版本覆盖新版本的主风险。
2. SG-002：版本化依赖镜像并拆分镜像同步，消除共享 tag 竞争。
3. SG-003：迁移与应用 rollout 解耦，增加数据库级锁。
4. SG-005：给 Gitee mirror 增加 ref 锁和 SHA 门禁。
5. SG-004：拆分低频集群 bootstrap 与日常应用部署。

## 未覆盖与运行验证建议

- 未修改 workflow、Helm、数据库或远端集群。
- 未取消当前并发 Deploy run。
- 建议在非生产 namespace 做 A/B 连续提交测试，记录进入 Helm 的 SHA、最终 Deployment image digest 和 release revision。
- 建议为并发部署、旧 run 退出、依赖 digest 和 migration lock 增加自动化验证。

## 修复授权门禁

用户已明确授权修复 `SG-001` 至 `SG-005`，并要求移除全部 Gitee 相关内容。

## 修复状态（2026-07-16）

| Finding | 状态 | 修复证据 |
|---------|------|----------|
| SG-001 | Implemented，待首条真实 CD 最终确认 | 生产可变步骤进入固定 `stratum-production` job concurrency，`cancel-in-progress: false`；main push 在可变操作前查询 GitHub 当前 SHA，过期 run 跳过 |
| SG-002 | Implemented，待 registry/CD 最终确认 | 固定所有依赖来源版本；依赖发布纳入串行区；发布后校验 registry digest；Helm 对 backend、frontend 及六项依赖全部使用 `repository@sha256` |
| SG-003 | Verified locally | 完整 tenant bootstrap 由 PostgreSQL session advisory lock 包裹，等锁受 2 分钟 context 限制且不截断已进入的 DDL，释放受 5 秒独立 timeout 限制；两个真实 PostgreSQL 连接的集成测试证明互斥和释放 |
| SG-004 | Implemented，待首条真实 CD 最终确认 | namespace、Secret、固定版本 metrics-server、Helm 和 rollout 全部位于同一生产串行 job；metrics-server patch 幂等且不再隐藏错误 |
| SG-005 | Resolved by removal | 删除 Gitee mirror workflow，并从部署文档移除对应入口；项目不再读取 Gitee secrets |

### 自动化验证证据

- `bash scripts/quality/check-deployment-safety-test.sh`：通过。
- `bash scripts/quality/check-helm-image-rendering-test.sh`：tag 兼容和八项 digest 渲染通过。
- `helm lint ./helm -f helm/values-demo.yaml`：通过。
- `go test ./pkg/storage/postgres ./internal/platform/runtime -run 'SchemaProvisionLock|BootstrapTenant' -count=1`：通过。
- `go test -race ./pkg/storage/postgres ./internal/platform/runtime -count=1`：通过。
- 使用本地 Compose PostgreSQL，设置测试连接后运行
  `go test -tags=integration ./pkg/storage/postgres -run TestWithSchemaProvisionLockSerializesConnections -count=1`：通过；测试后 PostgreSQL 服务已停止。
- 固定的 Redis 与 MinIO 上游 tag 已通过 `docker buildx imagetools inspect` 验证存在。

### 剩余运行时确认

本地验证不会触发生产部署。首次授权的真实 CD run 仍需确认：旧 main run 日志显示跳过、
Helm 渲染及 Deployment/StatefulSet 使用预期 digest、metrics-server 幂等步骤成功，以及最终
release SHA 与 GitHub 最新 main 一致。在完成该运行确认前，不把远端部署状态描述为已验证。
