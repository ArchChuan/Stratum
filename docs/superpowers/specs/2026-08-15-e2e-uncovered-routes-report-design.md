# E2E 未覆盖路由报告(新用例沉淀补强)设计

日期:2026-08-15
状态:待评审
作者:Claude Code(brainstorming 流程产出,与用户确认)

## 背景与问题

现有浏览器 UI 端到端测试体系(`stratum-e2e-development` skill 生成)存在一个结构性缺口:

- `api/http/testdata/contracts/*.golden.json`(168 个契约快照)枚举了系统注册 API 路由,是"系统能力"的权威清单。
- `test/e2e/stateful/manifest.json`(120 条 capability)声明"应被覆盖的能力",每条 `http_evidence` 是**文字描述**,无法机器对账。
- 每次 stateful E2E 跑完只写 attestation 证据,不反写 manifest、不新增 pack。
- `reconcileCapabilities` 是**反向对账**(fail-closed):声明过的没跑到 → 失败。但"**存在但没声明**"的新功能路由,没有任何机制发现并提领成待补用例。

**根因**:差集两侧不可机器比较——左侧(golden 契约)结构化,右侧(实际点击覆盖)是字符串数组,无 (method, path)。

**目标**:新增一个"运行时实测 + 差集报告"机制:stateful E2E 跑完后,自动对比"注册路由全集"与"浏览器实际触发的 HTTP 请求",输出未覆盖 API 清单,供开发者/AI 补 pack + manifest。

## 已确认决策

| 决策点 | 结论 |
|---|---|
| 沉淀形态 | 待补清单(diff 报告),不自动写代码 |
| 触发时机 | stateful E2E 跑完后自动 |
| 覆盖判定 | 运行时实测:浏览器实际触发的结构化 (method, path) |
| path 归一化 | 动态段(`:id`)替换为 gin 参数占位符 `:param`,与 golden 对齐 |
| 路由全集 | 运行时 dump(gin `Routes()`),golden 契约做 cross-check 并集 |
| 未覆盖处理 | **仅告警**,不阻断(与 `unverified_capabilities` 的 fail-closed 互补) |
| 路由 dump 位置 | 后端只读端点 `GET /e2e/routes`(测试态暴露,参考 e2e 服务惯例) |

## 设计

### 1. 数据采集(运行时实测)

`web/e2e/stateful/core/evidence.ts` 的 `EvidenceRecord` 增加结构化字段,与现有字符串数组并存:

```ts
export interface EvidenceRecord {
  ui: string[];
  http: string[];              // 现有,保留(人类可读证据)
  database: string[];
  httpRequests: Set<string>;   // 新增:"METHOD /path" 归一化,去重
}
```

采集方式:`system-stateful.spec.ts` 在每个 actor context 上挂 `page.on('request')`,拦截 `fetch`/`xhr` 类型请求(排除 document/静态资源,与现有 pack 的 `waitForResponse` 判定一致),记录 method + path。

**path 归一化**:去掉 query;动态段按 gin 惯例替换为 `:param`(如 `/agents/abc` → `/agents/:param`),避免 `/agents/abc` 与 `/agents/xyz` 计为两条。归一化是纯函数,配表驱动单测。

### 2. 路由全集(枚举源)

- **主源(运行时 dump)**:后端新增只读端点 `GET /e2e/routes`,内部调用 gin `Routes()`(先例:`api/http/router_evaluation_rbac_test.go:90`),返回注册路由 `(method, path)` 全集。测试态暴露,与 oauth/mcp/fixture 等 e2e 服务同层级。
- **辅助源(cross-check)**:`api/http/testdata/contracts/*.golden.json` 解析出的 `(method, path)` 集合。两者取并集,避免 golden 漏记或 dump 缺路由。
- **排除集**:不需要浏览器 UI 触发的基础设施路由,维护在显式常量并注明排除原因:
  - `GET /health`、`GET /livez`、`GET /readyz`、`GET /metrics`(基础设施探活)
  - `GET /e2e/routes`(报告端点自身)

### 3. 差集与报告

差集:`注册路由全集 − 运行时实测覆盖 − 排除集` = `uncovered`。

输出 `test/e2e/stateful/uncovered-report.json`:

```json
{
  "generated_at": "2026-08-15T12:00:00Z",
  "tested_git_parent": "028508c64986",
  "route_total": 168,
  "covered": ["GET /memory", "POST /memory/clear", "..."],
  "uncovered": [
    { "method": "POST", "path": "/mcp/servers/:param/reconnect", "domain_hint": "mcp" }
  ],
  "excluded": [
    { "method": "GET", "path": "/health", "reason": "infra" }
  ]
}
```

- `domain_hint`:按 path 前缀推断(`/mcp/*` → mcp),帮助定位应补到哪个 pack。纯函数,配单测。
- spec 末尾打印摘要:`X 个注册路由,Y 已覆盖,Z 未覆盖`。
- **仅告警不阻断**:写入报告 + 打印,不使测试失败。与 `unverified_capabilities`(声明过没跑到 → fail)互补——那个是"说过没做到",这个是"做到了但没说"。

### 4. 测试与落地

- **纯函数单测**:归一化(normalizePath)、差集(diffRoutes)、排除(excludeRoutes)、domain_hint(domainForPath)均表驱动。
- **采集层测试**:mock `page` 的 `on('request')` 回调,验证记录、去重、归一化。
- **E2E 层**:现有 stateful E2E 跑完自动生成报告;报告**不 gate CI**(告警级),但报告格式由 schema 校验脚本守护(参考 `check-e2e-coverage.sh` 风格,如 `check-e2e-uncovered-report.sh`)。
- **文档**:`docs/agent/backend-go.md` 测试节补一句"新增 API 后如何看 uncovered 报告补用例"。

## 改动文件清单

| 文件 | 改动 |
|---|---|
| `web/e2e/stateful/core/evidence.ts` | `EvidenceRecord` 加 `httpRequests: Set<string>`(仅字段,纯函数不放这里) |
| `web/e2e/system-stateful.spec.ts` | 挂 `page.on('request')` 采集;跑完调 diff 生成报告 + 打印摘要 |
| `web/e2e/stateful/core/routes-diff.ts`(新模块) | 纯函数集中:normalizePath / diffRoutes / excludeRoutes / domainForPath |
| `api/http/router.go`(或 wiring) | 新增 `GET /e2e/routes` 只读端点 |
| `test/e2e/stateful/uncovered-report.json` | 报告输出;入 git 供 diff(与 attestation 同目录同提交节奏),标记 generated 不参与契约守卫 |
| `scripts/quality/check-e2e-uncovered-report.sh`(新) | 报告格式 schema 校验 |
| `docs/agent/backend-go.md` | 测试节补充说明 |

## 边界与不做的事

- 不做自动生成 pack / manifest 条目(形态已确认为待补清单)。
- 不把未覆盖作为 CI 阻断条件(告警级)。
- 不反向写 manifest,不改变 `reconcileCapabilities` 语义。
- 不做前端路由联动(方案 B 的 `e2e-surface.json` 联动作为后续增强项,不在首期范围)。
