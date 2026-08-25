# E2E 测试规范与门禁

## 五层防线

每层覆盖一种独特缺陷类型，层次间不可互相替代：

| 层 | 机制 | 缺陷类型 | 运行方式 |
|----|------|----------|----------|
| 1. 单元测试 | `go test -race` / `npm test` | 分支逻辑、错误语义 | Local focused + CI `test` |
| 2. 集成测试 | `go test -tags=integration` | 真实协议/数据库行为 | Local focused + CI integration jobs |
| 3. 契约金丝雀 | `make contract-test` | JSON 序列化形状 | Local focused + CI `contract` |
| 4. Manifest 能力声明 | `manifest.json` capabilities | 声明覆盖缺口 | Local attestation v2 |
| 5. 浏览器 E2E | Playwright headless | 真实 UI 用户旅程 | Local before PR |

**规则**：新增端点必须覆盖全部 5 层。存量缺口按风险优先级渐进补齐。

## 三层风险策略

| 层级 | 触发条件 | 时长 | Packs | 执行位置 |
|------|----------|------|-------|---------|
| **short** | R2+ 创建 PR 前 | ~10min | 关键 packs | 本地 `make test-verify-before-pr` |
| **soak** | R3 auth/tenant/迁移/msg/vector/外部依赖 | 600s | all eligible | 本地 canonical entrypoint |
| **release-soak** | R4 显式发布意图 | 3600s | all | 本地显式执行 |

### Short mode packs（本地默认）

```
dashboard,iam,workflow,agent,skill,mcp,agent-skill-mcp,knowledge,memory,llm-admin
```

### Soak mode packs（全部）

```
dashboard,iam,workflow,agent,skill,mcp,agent-skill-mcp,knowledge,memory,audit,evaluation,agent-context,evaluation-promotion,llm-admin,operation-gate,collab,scheduled-task
```

Packs 定义以 `scripts/e2e/system-stateful.sh` 的 `all_packs` 为唯一事实源（共 17 个）；`iam-oauth` 是 `iam` pack 的辅助模块，不作为独立 pack 入列。

## 四条验收标准

一次 E2E 执行必须满足以下全部条件才能宣告完成：

1. **attestation 匹配** — 源码摘要与执行结果一致（`make e2e-attestation-check`）
2. **零 skipped capability** — manifest 声明的能力全部被执行，无跳过
3. **清理完成** — 测试数据清理完毕，无残留状态
4. **残留风险明确报告** — 确实无法覆盖的能力必须写进报告

临时 Playwright/curl/手工验证只能诊断，**不能替代系统验收**。

## 两条铁律

1. **所有操作必须通过真实 UI 点击** — 禁止 `request.post()` 代替表单、禁止 SQL 改状态代替点击
2. **每一步沉淀为可复用 Playwright 脚本** — 跑通后固化为 pack 里的 action，回归直接跑脚本

## camelCase JSON 标准

Go struct 缺少 `json:"camelCase"` tag 时序列化为 PascalCase，前端取值 → `undefined` → 静默失效。

**双重防御**：

- **Build time**：`make contract-enforce` → 165 golden files + camelCase 扫描（CI `contract` job）
- **Runtime**：E2E pack 使用 `assertCamelCaseKeys` / `assertNoPascalCase` 守卫每个 API 响应

### 添加新 camelCase 守卫

1. 在 `web/e2e/stateful/core/camelcase.ts` 定义键常量
2. 在 pack 的 API 响应处理中调用 `assertCamelCaseKeys(body, KEYS, label)`
3. 在 golden file 录制后运行 `make contract-enforce` 验证

## 新增端点清单

新增 HTTP 端点时，按顺序完成：

- [ ] **Layer 1**：单元测试覆盖核心逻辑 + 错误分支
- [ ] **Layer 2**：集成测试覆盖数据库/外部依赖路径
- [ ] **Layer 3**：运行 `make record-contracts` 生成 golden file → `make contract-enforce` 验证
- [ ] **Layer 4**：添加 manifest capability 声明（`test/e2e/stateful/manifest.json`）
- [ ] **Layer 5**：添加 E2E pack action（真实浏览器操作 + 数据库对账）
- [ ] 运行 `make e2e-system-short` 验证全链路
- [ ] 风险规则命中时运行 `make e2e-system-soak`

## 三种验证权威

Stratum 用三种独立权威取代单一 overloaded `accepted` 状态：本地浏览器权威、CI 合并权威、发布流水线权威。GitHub 不下载、不签名、不把本地报告当作 required status check。

```
Local before PR
  └── focused -> headless short -> R3 600s soak -> local report
PR CI
  └── static + unit + integration + contract + security + build（无浏览器）
Release pipeline
  └── exact CI head SHA + immutable digests + migration + rollout + health + rollback
```

### 本地浏览器权威（Local before PR）

`make test-verify-before-pr` 在 clean commit 上运行风险选择的 headless 浏览器验证。本地报告绑定被测 commit、verification manifest 摘要、attestation v2 能力结果与清理结果。它是开发者审计断言——GitHub 不下载、不签名、不视为 required status check。

### CI 合并权威（PR CI）

GitHub Actions 通过真实并行 job（static / unit / integration / contract golden / security / build，无浏览器）决定 PR 能否合并。`.test/verification.yaml` 的每个 `ci_checks` 标识都映射到 workflow job，兼容性聚合除非每个必需依赖结果都是 `success` 否则失败。

### CI 兜底的本地跳过（CI-owned skip）

`run-planned-checks.sh` 支持 `CI_OWNED=1`：当所选风险级别对应的本地检查也出现在 manifest 的 `ci_checks`（由上述真实 job 兜底）时，本地跳过而不是重跑。只有 CI 不覆盖的检查（如 `docs-lint`、E2E）留在本地。每个跳过的单元都打印 `skipped (CI-owned): <check>`——显式，绝不静默。若 plan 缺少 `ci_checks` 声明则 fail closed（宁可全跑也不误跳过）。分支 CI 已绿、只需本地 E2E 加定向测试时用 `CI_OWNED=1 make test-verify-before-pr`。默认关闭，无该变量时行为不变。

### 发布流水线权威（Release pipeline）

对 `workflow_run` 部署，候选是 `github.event.workflow_run.head_sha`。它必须是 `main` 上成功的 CI 运行，且构建任何镜像前仍须等于当前 `main` tip。所有 checkout 与镜像 tag 都使用该候选。部署固定 registry digests，并记录集群实际观测到的 backend/frontend/adapter 镜像摘要以及迁移、健康、回滚结果。

发布记录可以由 GitHub attest，因其产生于发布控制面内；该 attestation 不会追溯性地让本地浏览器证据变成 GitHub 信任边界。

## 常用命令

```bash
# 契约测试
make contract-test          # 仅契约 golden 测试
make contract-enforce       # 契约测试 + camelCase 扫描
make record-contracts       # 录制/更新 golden files

# E2E 执行
make test-verify-before-pr  # PR 前 canonical 入口，自动按风险选择
make e2e-system-short       # short 模式（仅开发迭代）
make e2e-system-soak        # soak 模式（仅开发迭代，需要 STATEFUL_E2E_PACKS=all）
make e2e-system-release-soak # release soak（3600s）

# 审计
bash scripts/quality/check-e2e-coverage.sh   # 路由→golden→manifest→pack 交叉审计
bash scripts/quality/contract-audit.sh       # 开发者端路由覆盖审计
make e2e-attestation-check                   # attestation 一致性检查
```
