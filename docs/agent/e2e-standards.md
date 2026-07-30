# E2E 测试规范与门禁

## 五层防线

每层覆盖一种独特缺陷类型，层次间不可互相替代：

| 层 | 机制 | 缺陷类型 | 运行方式 |
|----|------|----------|----------|
| 1. 单元测试 | `go test -race` / `npm test` | 分支逻辑、错误语义 | CI `test` job |
| 2. 集成测试 | `go test -tags=integration` | 真实协议/数据库行为 | CI `workflow-e2e` job |
| 3. 契约金丝雀 | `make contract-test` | JSON 序列化形状 | CI `contract` job |
| 4. Manifest 能力声明 | `manifest.json` capabilities | 声明覆盖缺口 | `make e2e-attestation-check` |
| 5. 浏览器 E2E | Playwright headless | 真实 UI 用户旅程 | CI `stateful-e2e` job |

**规则**：新增端点必须覆盖全部 5 层。存量缺口按风险优先级渐进补齐。

## 三层风险策略

| 层级 | 触发条件 | 时长 | Packs | CI 行为 |
|------|----------|------|-------|---------|
| **short** | 每次 PR | ~10min | 关键 packs | **强制门禁** — `stateful-e2e` job |
| **soak** | auth/tenant/迁移/msg/vector/外部依赖 | 600s | all eligible | **强制门禁** — `make e2e-system-soak` |
| **release-soak** | 正式发布 | 3600s | all | 发布 workflow 中运行 |

### Short mode packs（CI 默认）

```
dashboard,iam,workflow,agent,skill,mcp,agent-skill-mcp,knowledge,memory,llm-admin
```

### Soak mode packs（全部）

```
dashboard,iam,workflow,agent,skill,mcp,agent-skill-mcp,knowledge,memory,evaluation,agent-context,evaluation-promotion,llm-admin
```

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

- **Build time**：`make contract-enforce` → 141 golden files + camelCase 扫描（CI `contract` job）
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

## CI 门禁链

```
PR open
  ├── system-e2e-attestation   (30s)
  ├── guardrails                (3min)
  ├── lint                      (2min)
  ├── test                      (5min)
  ├── contract                  (3s)    ← 新增：141 golden + camelCase
  ├── workflow-e2e              (2min)
  ├── platform-assistant-e2e    (2min)
  ├── platform-assistant-browser-e2e (10min)
  ├── tool-permission-browser   (3min)
  ├── stateful-e2e              (15min) ← 新增：10 pack 浏览器 E2E
  └── build (needs all above)   — 全部通过才允许合并
```

## 常用命令

```bash
# 契约测试
make contract-test          # 仅契约 golden 测试
make contract-enforce       # 契约测试 + camelCase 扫描
make record-contracts       # 录制/更新 golden files

# E2E 执行
make e2e-system-short       # short 模式（CI 默认）
make e2e-system-soak        # soak 模式（需要 STATEFUL_E2E_PACKS=all）
make e2e-system-release-soak # release soak（3600s）

# 审计
bash scripts/quality/check-e2e-coverage.sh   # 路由→golden→manifest→pack 交叉审计
bash scripts/quality/contract-audit.sh       # 开发者端路由覆盖审计
make e2e-attestation-check                   # attestation 一致性检查
```
