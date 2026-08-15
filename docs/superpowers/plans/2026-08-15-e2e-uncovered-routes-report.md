# E2E 未覆盖路由报告实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** stateful E2E 跑完后自动生成"未覆盖 API 路由"待补清单,供开发者/AI 补 pack + manifest。

**Architecture:** 浏览器 context 级监听 `request` 事件,收集实际触发的原始 (method, pathname);后端新增只读端点 `GET /e2e/routes` 用 gin `Routes()` dump 注册路由模板;diff 时用模板全集**反向匹配**运行时请求(匹配到的模板本身即归一化形态,无启发式);golden 契约做 cross-check 并集;跑完取差集写 `test/e2e/stateful/uncovered-report.json`,仅告警不阻断。

**Tech Stack:** Playwright(TS)、Gin(Go)、vitest(前端单测)、bash(报告格式校验)。

## Global Constraints

- Go 行宽 ≤120;import 分组 stdlib → third-party → internal。
- 新函数圈复杂度 ≤10、认知复杂度 ≤15、行数 ≤120、嵌套 ≤4;Go 测试表驱动;TS 纯函数配 vitest 单测。
- 不打印 token/API key/password;浏览器证据不输出敏感值。
- `GET /e2e/routes` 为只读端点,测试态暴露,不携带业务数据。
- 报告**仅告警不阻断**:不使 stateful E2E 失败,不改 `reconcileCapabilities` 语义。
- 归一化语义:运行时请求**不启发式猜 id**,而是由 gin 模板全集反向匹配;`normalizePath` 只把 `:xxx` 段统一为 `:param`(用于 golden 兜底与报告展示)。
- 禁止在 `main` 直接提交;worktree 已建(`feat/e2e-uncovered-report`),全部命令在 worktree 根执行。

---

### Task 1: 后端只读端点 `GET /e2e/routes`

**Files:**

- Modify: `api/http/router.go`(registerHealth 后追加 `registerE2ERoutes(r)` + 新函数)
- Create: `api/http/router_e2e_routes_test.go`(package http 白盒测试)

**Interfaces:**

- Consumes: gin `*gin.Engine`(`r.Routes()`);不依赖 `wiring.Container`。
- Produces: `GET /e2e/routes` 返回 `{"routes":[{"method":"GET","path":"/memory"}]}`,path 为 gin 模板形式(含 `:id` 等具名参数)。此端点被 Task4 `fetchRegisteredRoutes` 消费。

- [ ] **Step 1: 写失败测试**

```go
// api/http/router_e2e_routes_test.go
package http

import (
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "testing"

 "github.com/gin-gonic/gin"
)

type e2eRoutesPayload struct {
 Routes []struct {
  Method string `json:"method"`
  Path   string `json:"path"`
 } `json:"routes"`
}

func TestE2ERoutesEndpointListsRegisteredRoutes(t *testing.T) {
 gin.SetMode(gin.TestMode)
 r := gin.New()
 r.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })
 registerE2ERoutes(r)

 w := httptest.NewRecorder()
 r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/e2e/routes", nil)) //nolint:noctx
 if w.Code != http.StatusOK {
  t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
 }

 var payload e2eRoutesPayload
 if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
  t.Fatalf("decode /e2e/routes: %v", err)
 }
 // 端点自身 + 手动注册的 /health,至少 2 条。
 if len(payload.Routes) < 2 {
  t.Fatalf("routes=%d want >=2 (health + self)", len(payload.Routes))
 }
 found := false
 for _, route := range payload.Routes {
  if route.Method == http.MethodGet && route.Path == "/health" {
   found = true
   break
  }
 }
 if !found {
  t.Fatal("GET /health missing from /e2e/routes dump")
 }
}
```

> 白盒 `package http` 有先例:`api/http/router_health_test.go`(同款 `gin.New()` + httptest 模式)。不依赖完整 `wiring.Container`,避免 contract_test.go 的 100+ 行 stub 构造。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report && go test ./api/http/ -run TestE2ERoutesEndpointListsRegisteredRoutes -count=1`
Expected: FAIL(编译错:registerE2ERoutes 未定义)

- [ ] **Step 3: 实现端点**

`api/http/router.go` `NewRouter` 中 `registerHealth(r, c)`(第 43 行)之后追加一行 `registerE2ERoutes(r)`。文件末尾附近新增函数:

```go
// registerE2ERoutes exposes a read-only route inventory for the stateful
// E2E coverage report. Paths are gin template form (e.g. /agents/:id).
// Unauthenticated: it carries no business data, only route shapes.
func registerE2ERoutes(r *gin.Engine) {
 r.GET("/e2e/routes", func(ctx *gin.Context) {
  type routeEntry struct {
   Method string `json:"method"`
   Path   string `json:"path"`
  }
  routes := make([]routeEntry, 0)
  for _, route := range r.Routes() {
   routes = append(routes, routeEntry{Method: route.Method, Path: route.Path})
  }
  ctx.JSON(http.StatusOK, gin.H{"routes": routes})
 })
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report && go test ./api/http/ -run TestE2ERoutesEndpointListsRegisteredRoutes -count=1`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add api/http/router.go api/http/router_e2e_routes_test.go
git commit -m "feat(e2e): GET /e2e/routes 路由清单端点
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: 纯函数模块 `routes-diff.ts`(模板匹配 + 差集)

**Files:**

- Create: `web/e2e/stateful/core/routes-diff.ts`
- Create: `web/e2e/stateful/core/routes-diff.test.ts`

**Interfaces:**

- Consumes: 无(纯函数,仅类型)。
- Produces:
  - `type RouteShape = { method: string; path: string }`
  - `normalizePath(path: string): string` — 去 query;把 `:xxx` 段统一为 `:param`(golden 兜底 + 报告展示)。
  - `methodPathKey(method: string, path: string): string` — `"GET /memory"`。
  - `matchTemplate(registered: RouteShape[], method: string, path: string): RouteShape | null` — 运行时请求反向匹配模板(静态段逐一相等、`:xxx`/`*xxx` 段匹配任意单段),命中返回模板自身。
  - `excludeRoutes(registered: RouteShape[], excluded: RouteShape[]): RouteShape[]` — 过滤排除集。
  - `diffRoutes(registered: RouteShape[], coveredRaw: Set<string>, excluded: RouteShape[]): { covered: string[]; uncovered: RouteShape[] }` — coveredRaw 为原始 `"METHOD pathname"` 集合,内部经 matchTemplate 判定;uncovered 的 path 已 `normalizePath`。
  - `domainForPath(path: string): string` — 按首段前缀推断 pack domain。

- [ ] **Step 1: 写失败测试**

```ts
// web/e2e/stateful/core/routes-diff.test.ts
import { describe, expect, it } from 'vitest';

import {
  diffRoutes, domainForPath, excludeRoutes, matchTemplate, normalizePath,
  type RouteShape,
} from './routes-diff';

const registered: RouteShape[] = [
  { method: 'GET', path: '/memory' },
  { method: 'POST', path: '/memory/clear' },
  { method: 'POST', path: '/mcp/servers/:serverId/reconnect' },
  { method: 'GET', path: '/agents/:id/execute' },
  { method: 'GET', path: '/health' },
];

describe('normalizePath', () => {
  it('replaces named gin params with :param', () => {
    expect(normalizePath('/agents/:id/execute')).toBe('/agents/:param/execute');
    expect(normalizePath('/mcp/servers/:serverId/:toolName')).toBe('/mcp/servers/:param/:param');
  });

  it('strips query strings', () => {
    expect(normalizePath('/memory?page=1')).toBe('/memory');
  });

  it('leaves static and fixed-value segments untouched', () => {
    expect(normalizePath('/health')).toBe('/health');
    expect(normalizePath('/evaluations/candidates/candidate-1/reject')).toBe(
      '/evaluations/candidates/candidate-1/reject',
    );
  });
});

describe('matchTemplate', () => {
  it('matches a runtime path against its gin template', () => {
    expect(matchTemplate(registered, 'POST', '/mcp/servers/abc-123/reconnect')).toEqual({
      method: 'POST', path: '/mcp/servers/:serverId/reconnect',
    });
    expect(matchTemplate(registered, 'GET', '/agents/abc/execute')).toEqual({
      method: 'GET', path: '/agents/:id/execute',
    });
  });

  it('rejects on method mismatch and structure mismatch', () => {
    expect(matchTemplate(registered, 'GET', '/mcp/servers/abc/reconnect')).toBeNull();
    expect(matchTemplate(registered, 'POST', '/memory')).toBeNull();
    expect(matchTemplate(registered, 'GET', '/memory/clear/extra')).toBeNull();
  });

  it('returns null when no template matches', () => {
    expect(matchTemplate(registered, 'GET', '/not-a-route')).toBeNull();
  });
});

describe('excludeRoutes', () => {
  it('filters registered routes by excluded set', () => {
    const excluded = [{ method: 'GET', path: '/health' }, { method: 'GET', path: '/livez' }];
    const remaining = excludeRoutes(registered, excluded);
    expect(remaining.map((r) => r.path)).toEqual([
      '/memory', '/memory/clear', '/mcp/servers/:serverId/reconnect', '/agents/:id/execute',
    ]);
  });
});

describe('diffRoutes', () => {
  it('reports registered routes not matched by runtime requests', () => {
    const coveredRaw = new Set(['GET /memory', 'POST /memory/clear']);
    const { covered, uncovered } = diffRoutes(registered, coveredRaw, []);
    expect(covered).toEqual(['GET /memory', 'POST /memory/clear']);
    expect(uncovered.map((r) => r.path)).toEqual([
      '/mcp/servers/:param/reconnect',
      '/agents/:param/execute',
      '/health',
    ]);
  });

  it('excludes infra routes and normalizes runtime ids via template match', () => {
    const coveredRaw = new Set([
      'POST /mcp/servers/srv-42/reconnect',
      'GET /agents/alice/execute',
    ]);
    const excluded = [{ method: 'GET', path: '/health' }];
    const { covered, uncovered } = diffRoutes(registered, coveredRaw, excluded);
    // 模板匹配:不同具体 id 归一到同一模板,不进 uncovered。
    expect(covered).toContain('POST /mcp/servers/:param/reconnect');
    expect(covered).toContain('GET /agents/:param/execute');
    expect(uncovered.map((r) => r.path)).toEqual(['/memory', '/memory/clear']);
  });
});

describe('domainForPath', () => {
  it('maps leading path segment to pack domain', () => {
    expect(domainForPath('/mcp/servers/x/reconnect')).toBe('mcp');
    expect(domainForPath('/memory/clear')).toBe('memory');
    expect(domainForPath('/workflow-runs/1')).toBe('workflow');
    expect(domainForPath('/agents/x/execute')).toBe('agent');
    expect(domainForPath('/auth/me')).toBe('iam');
  });

  it('falls back to other for unknown prefixes', () => {
    expect(domainForPath('/unknown-thing/x')).toBe('other');
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report/web && npx vitest run e2e/stateful/core/routes-diff.test.ts`
Expected: FAIL(模块不存在)

- [ ] **Step 3: 实现纯函数**

```ts
// web/e2e/stateful/core/routes-diff.ts
export interface RouteShape {
  method: string;
  path: string;
}

// DOMAIN_PREFIXES maps leading path segments to stateful pack domain names.
// 与 test/e2e/stateful/manifest.json 的 domain 取值保持一致。
const DOMAIN_PREFIXES: Array<[string, string]> = [
  ['/workflow-runs', 'workflow'],
  ['/workflow-approvals', 'workflow'],
  ['/workflows', 'workflow'],
  ['/memory', 'memory'],
  ['/mcp', 'mcp'],
  ['/skills', 'skill'],
  ['/knowledge', 'knowledge'],
  ['/agents', 'agent'],
  ['/evaluations', 'evaluation'],
  ['/scheduled-tasks', 'scheduled-task'],
  ['/collaborations', 'collab'],
  ['/auth', 'iam'],
  ['/tenant', 'iam'],
  ['/admin', 'llm-admin'],
  ['/audit', 'audit'],
];

// normalizePath 去 query,并把 gin 具名参数段(:xxx)统一为 :param。
// 固定值段(如 candidate-1)不做启发式猜测——运行时 id 的归一化由 matchTemplate 完成。
export const normalizePath = (path: string): string => {
  const withoutQuery = path.split('?')[0] ?? '';
  return withoutQuery
    .split('/')
    .map((segment) => (segment.startsWith(':') ? ':param' : segment))
    .join('/');
};

export const methodPathKey = (method: string, path: string): string =>
  `${method} ${normalizePath(path)}`;

// matchTemplate 用注册模板反向匹配运行时请求:静态段逐一相等,参数段
// (:xxx 或 *xxx)匹配任意单段。命中返回模板自身(即归一化形态)。
// gin 不允许同 method 同静态结构的两个模板并存,故匹配结果唯一。
export const matchTemplate = (
  registered: RouteShape[],
  method: string,
  path: string,
): RouteShape | null => {
  const pathname = path.split('?')[0] ?? '';
  const segments = pathname.split('/').filter(Boolean);
  for (const route of registered) {
    if (route.method !== method) continue;
    const templateSegments = route.path.split('/').filter(Boolean);
    if (templateSegments.length !== segments.length) continue;
    let matched = true;
    for (let i = 0; i < templateSegments.length; i += 1) {
      const templateSegment = templateSegments[i];
      if (templateSegment.startsWith(':') || templateSegment.startsWith('*')) continue;
      if (templateSegment !== segments[i]) {
        matched = false;
        break;
      }
    }
    if (matched) return route;
  }
  return null;
};

export const excludeRoutes = (registered: RouteShape[], excluded: RouteShape[]): RouteShape[] => {
  const excludedKeys = new Set(excluded.map(({ method, path }) => methodPathKey(method, path)));
  return registered.filter(({ method, path }) => !excludedKeys.has(methodPathKey(method, path)));
};

export const diffRoutes = (
  registered: RouteShape[],
  coveredRaw: Set<string>,
  excluded: RouteShape[],
): { covered: string[]; uncovered: RouteShape[] } => {
  const candidates = excludeRoutes(registered, excluded);
  const covered = new Set<string>();
  for (const raw of coveredRaw) {
    const space = raw.indexOf(' ');
    if (space <= 0) continue;
    const method = raw.slice(0, space);
    const path = raw.slice(space + 1);
    const matched = matchTemplate(candidates, method, path);
    if (matched) covered.add(methodPathKey(matched.method, matched.path));
  }
  const uncovered: RouteShape[] = [];
  for (const route of candidates) {
    if (covered.has(methodPathKey(route.method, route.path))) continue;
    uncovered.push({ method: route.method, path: normalizePath(route.path) });
  }
  return { covered: [...covered].sort(), uncovered };
};

export const domainForPath = (path: string): string => {
  const normalized = normalizePath(path);
  for (const [prefix, domain] of DOMAIN_PREFIXES) {
    if (normalized.startsWith(prefix)) return domain;
  }
  return 'other';
};
```

> **实现说明**:`coveredRaw` 元素格式为 `"METHOD pathname"`(如 `"POST /mcp/servers/srv-42/reconnect"`),由 Task3 采集时按 `request.method() + ' ' + url.pathname` 拼出。diff 时经 `matchTemplate` 归一到模板,具体 id 值不会造成重复条目。排除集在 diff 前过滤,故被排除路由的请求即使发生也不计入 covered(它们本就不该被要求覆盖)。

- [ ] **Step 4: 跑测试确认通过**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report/web && npx vitest run e2e/stateful/core/routes-diff.test.ts`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add web/e2e/stateful/core/routes-diff.ts web/e2e/stateful/core/routes-diff.test.ts
git commit -m "test(e2e): 路由差集纯函数模块(模板匹配,无启发式)
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: evidence 采集运行时实测

**Files:**

- Modify: `web/e2e/stateful/core/evidence.ts`(加 `httpRequests: Set<string>` 字段 + 新增 `emptyEvidence`)
- Modify: `web/e2e/system-stateful.spec.ts`(删内联 `emptyEvidence`;context 级 `on('request')` 采集)
- Create: `web/e2e/stateful/core/evidence.test.ts`

**Interfaces:**

- Consumes: `EvidenceRecord` 扩字段;`normalizePath`(Task2,采集侧只用它剥离 query 段后拼 key,归一化判定留给 diff)。
- Produces: `emptyEvidence(): EvidenceRecord`(含 `httpRequests: new Set()`);spec 中每个 actor context 的 `fetch`/`xhr` 请求被记入共享 evidence.httpRequests,格式 `"METHOD pathname"`。

- [ ] **Step 1: 写失败测试**

```ts
// web/e2e/stateful/core/evidence.test.ts
import { describe, expect, it } from 'vitest';

import { emptyEvidence, summarizeEvidence } from './evidence';

describe('emptyEvidence', () => {
  it('initializes httpRequests as an empty set', () => {
    const evidence = emptyEvidence();
    expect(evidence.httpRequests).toBeInstanceOf(Set);
    expect(evidence.httpRequests.size).toBe(0);
    expect(evidence.ui).toEqual([]);
    expect(evidence.http).toEqual([]);
    expect(evidence.database).toEqual([]);
  });
});

describe('summarizeEvidence', () => {
  it('reports reconciled only when all three layers have evidence', () => {
    expect(summarizeEvidence({
      ui: ['x'], http: ['y'], database: ['z'], httpRequests: new Set(['GET /a']),
    })).toEqual({ ui: 1, http: 1, database: 1, reconciled: true });
    expect(summarizeEvidence({
      ui: ['x'], http: [], database: [], httpRequests: new Set(),
    })).toEqual({ ui: 1, http: 0, database: 0, reconciled: false });
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report/web && npx vitest run e2e/stateful/core/evidence.test.ts`
Expected: FAIL(`emptyEvidence` 未导出)

- [ ] **Step 3: 改 evidence.ts**

```ts
// web/e2e/stateful/core/evidence.ts
import type { EvidenceSummary } from './types';

export interface EvidenceRecord {
  ui: string[];
  http: string[];
  database: string[];
  httpRequests: Set<string>; // "METHOD pathname",原始形态,去重
}

export const emptyEvidence = (): EvidenceRecord => ({
  ui: [],
  http: [],
  database: [],
  httpRequests: new Set(),
});

export const summarizeEvidence = (record: EvidenceRecord): EvidenceSummary => ({
  ui: record.ui.length,
  http: record.http.length,
  database: record.database.length,
  reconciled: record.ui.length > 0 && record.http.length > 0 && record.database.length > 0,
});
```

- [ ] **Step 4: spec 接入采集**

`web/e2e/system-stateful.spec.ts` 改动:

1. 第 13 行 import 行改为 `import { emptyEvidence, type EvidenceRecord } from './stateful/core/evidence';`,并**删除第 43 行内联定义**:

```ts
// 删除:
// const emptyEvidence = (): EvidenceRecord => ({ ui: [], http: [], database: [] });
```

1. import 补充(Playwright 类型与 Task2 纯函数):

```ts
import type { BrowserContext } from '@playwright/test';
import { normalizePath } from './stateful/core/routes-diff';
```

1. 在 `const contexts = await createActorContexts(browser);`(第 63 行)之后、`const evidence = emptyEvidence();`(第 65 行)之前,插入:

```ts
const evidence = emptyEvidence();
recordHttpRequests(contexts, evidence, runtime.urls.api);
```

1. 文件底部(executePack 之后)新增 helper:

```ts
// recordHttpRequests 在每个 actor context 上采集后端 API 请求,供未覆盖路由差集。
const recordHttpRequests = (
  contexts: Record<ActorLabel, BrowserContext>,
  evidence: EvidenceRecord,
  backendOrigin: string,
): void => {
  for (const context of Object.values(contexts)) {
    context.on('request', (request) => {
      if (request.resourceType() !== 'fetch' && request.resourceType() !== 'xhr') return;
      const url = new URL(request.url());
      if (url.origin !== new URL(backendOrigin).origin) return;
      evidence.httpRequests.add(`${request.method()} ${url.pathname}`);
    });
  }
};
```

> 注:`contexts` 来自 `createActorContexts(browser)`,返回 `Record<ActorLabel, BrowserContext>`,故 helper 签名直接用 `BrowserContext` 类型,无需泛型断言。`normalizePath` 采集侧未用到(diff 时统一处理),如 import 后未使用会触发 lint,则去掉该 import——以编译为准。

- [ ] **Step 5: 编译与测试确认**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report/web && npx tsc --noEmit -p tsconfig.json`
Expected: 无类型错误

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report/web && npx vitest run e2e/stateful/core/evidence.test.ts`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add web/e2e/stateful/core/evidence.ts web/e2e/stateful/core/evidence.test.ts web/e2e/system-stateful.spec.ts
git commit -m "feat(e2e): 浏览器请求实测采集 evidence.httpRequests
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: 报告生成(spec 收尾)

**Files:**

- Modify: `web/e2e/system-stateful.spec.ts`(跑完 fetch 路由全集、调 buildUncoveredReport、写报告、打印摘要)
- Modify: `web/e2e/stateful/core/routes-diff.ts`(追加 `buildUncoveredReport`)
- Modify: `web/e2e/stateful/core/routes-diff.test.ts`(追加用例)

**Interfaces:**

- Consumes: `evidence.httpRequests`(Task3)、`diffRoutes`/`domainForPath`/`normalizePath`/`type RouteShape`(Task2)、`runtime.urls.api`、`safeResults.tested_git_parent`。
- Produces:
  - `type ExcludedRoute = RouteShape & { reason: string }`
  - `type UncoveredReport = { generated_at: string; tested_git_parent: string; route_total: number; covered: string[]; uncovered: Array<{ method: string; path: string; domain_hint: string }>; excluded: Array<{ method: string; path: string; reason: string }> }`
  - `buildUncoveredReport(registered: RouteShape[], coveredRaw: Set<string>, excluded: ExcludedRoute[], gitParent: string, generatedAt: string): UncoveredReport`
  - spec 写 `test/e2e/stateful/uncovered-report.json`。

- [ ] **Step 1: 追加失败测试**

```ts
// 追加到 web/e2e/stateful/core/routes-diff.test.ts。
// import 合并到文件顶部已有语句:
//   import { buildUncoveredReport, ... } from './routes-diff';
describe('buildUncoveredReport', () => {
  it('assembles a report with uncovered entries and domain hints', () => {
    const registered: RouteShape[] = [
      { method: 'GET', path: '/memory' },
      { method: 'POST', path: '/mcp/servers/:serverId/reconnect' },
      { method: 'GET', path: '/health' },
    ];
    const excluded = [{ method: 'GET', path: '/health', reason: 'infra' }];
    const coveredRaw = new Set(['GET /memory']);
    const report = buildUncoveredReport(registered, coveredRaw, excluded, 'abc123', '2026-08-15T00:00:00Z');
    expect(report.route_total).toBe(3);
    expect(report.generated_at).toBe('2026-08-15T00:00:00Z');
    expect(report.tested_git_parent).toBe('abc123');
    expect(report.covered).toEqual(['GET /memory']);
    expect(report.uncovered).toEqual([
      { method: 'POST', path: '/mcp/servers/:param/reconnect', domain_hint: 'mcp' },
    ]);
    expect(report.excluded).toEqual([{ method: 'GET', path: '/health', reason: 'infra' }]);
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report/web && npx vitest run e2e/stateful/core/routes-diff.test.ts`
Expected: FAIL(`buildUncoveredReport` 未定义)

- [ ] **Step 3: 实现 buildUncoveredReport**

追加到 `web/e2e/stateful/core/routes-diff.ts`:

```ts
export type ExcludedRoute = RouteShape & { reason: string };

export interface UncoveredReport {
  generated_at: string;
  tested_git_parent: string;
  route_total: number;
  covered: string[];
  uncovered: Array<{ method: string; path: string; domain_hint: string }>;
  excluded: Array<{ method: string; path: string; reason: string }>;
}

export const buildUncoveredReport = (
  registered: RouteShape[],
  coveredRaw: Set<string>,
  excluded: ExcludedRoute[],
  gitParent: string,
  generatedAt: string,
): UncoveredReport => {
  const { covered, uncovered } = diffRoutes(registered, coveredRaw, excluded);
  return {
    generated_at: generatedAt,
    tested_git_parent: gitParent,
    route_total: registered.length,
    covered,
    uncovered: uncovered.map(({ method, path }) => ({
      method,
      path,
      domain_hint: domainForPath(path),
    })),
    excluded: excluded.map(({ method, path, reason }) => ({ method, path, reason })),
  };
};
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report/web && npx vitest run e2e/stateful/core/routes-diff.test.ts`
Expected: PASS

- [ ] **Step 5: spec 接入报告生成**

`web/e2e/system-stateful.spec.ts`:

1. import 追加:`buildUncoveredReport, type RouteShape` from `./stateful/core/routes-diff`。
2. 在 `await writeFile(resultsPath, ...)`(第 157 行)之后、`expect(unverifiedCapabilities...)`(第 158 行)之前插入:

```ts
// ── 未覆盖路由报告(新用例沉淀)──────────────────────────────
// 仅告警不阻断:写入报告 + 打印摘要,不使测试失败。
// 与 unverified_capabilities(声明过没跑到→fail)互补——这个是"做到了但没说"。
const uncoveredReportPath = fileURLToPath(
  new URL('../../test/e2e/stateful/uncovered-report.json', import.meta.url),
);
let uncoveredSummary = '';
try {
  const excludedRoutes = [
    { method: 'GET', path: '/health', reason: 'infra' },
    { method: 'GET', path: '/livez', reason: 'infra' },
    { method: 'GET', path: '/readyz', reason: 'infra' },
    { method: 'GET', path: '/metrics', reason: 'infra' },
    { method: 'GET', path: '/e2e/routes', reason: 'self' },
  ];
  const report = buildUncoveredReport(
    await fetchRegisteredRoutes(backendURL),
    evidence.httpRequests,
    excludedRoutes,
    safeResults.tested_git_parent,
    new Date().toISOString(),
  );
  await writeFile(uncoveredReportPath, `${JSON.stringify(report, null, 2)}\n`, { mode: 0o600 });
  uncoveredSummary =
    `${report.route_total} 注册路由,${report.covered.length} 已覆盖,` +
    `${report.uncovered.length} 未覆盖(见 test/e2e/stateful/uncovered-report.json)`;
} catch (error) {
  uncoveredSummary = `uncovered 报告生成失败(不影响验收): ${String(error)}`;
}
console.log(uncoveredSummary);
```

1. 文件底部(executePack 之后)追加 helper:

```ts
// fetchRegisteredRoutes 从后端只读端点拉取注册路由模板全集(gin Routes() dump)。
const fetchRegisteredRoutes = async (backendURL: string): Promise<RouteShape[]> => {
  const response = await fetch(`${backendURL}/e2e/routes`);
  if (!response.ok) throw new Error(`GET /e2e/routes failed: ${response.status}`);
  const body = (await response.json()) as { routes: Array<{ method: string; path: string }> };
  return body.routes;
};
```

> 报告路径 `test/e2e/stateful/uncovered-report.json` 与 manifest 同目录(manifest 用 `new URL('../../test/e2e/stateful/manifest.json', import.meta.url)`,spec 位于 `web/e2e/`,两处 `../../` 都到仓库根)。`try/catch` 保证报告失败不阻断验收。

- [ ] **Step 6: 类型检查**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report/web && npx tsc --noEmit -p tsconfig.json`
Expected: 无类型错误

- [ ] **Step 7: 提交**

```bash
git add web/e2e/stateful/core/routes-diff.ts web/e2e/stateful/core/routes-diff.test.ts web/e2e/system-stateful.spec.ts
git commit -m "feat(e2e): 未覆盖路由报告生成(跑完自动,告警级)
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: golden 契约 cross-check 并集

**Files:**

- Modify: `web/e2e/stateful/core/routes-diff.ts`(追加 `mergeGoldenRoutes`)
- Modify: `web/e2e/stateful/core/routes-diff.test.ts`(追加用例)
- Modify: `web/e2e/system-stateful.spec.ts`(golden 解析 helper + 并集接入)

**Interfaces:**

- Consumes: `matchTemplate`/`normalizePath`/`type RouteShape`(Task2)。
- Produces:
  - `mergeGoldenRoutes(registered: RouteShape[], goldenRoutes: RouteShape[]): RouteShape[]` — 凡 golden 路径在 dump 中匹配不到模板的,追加进注册集(理论空集,防御 golden 漏记或 dump 缺路由)。
  - spec 底部 `loadGoldenRoutes(goldenDir: string): Promise<RouteShape[]>`(读 `api/http/testdata/contracts/*.golden.json` 的 method/path)。

- [ ] **Step 1: 追加失败测试**

```ts
// 追加到 web/e2e/stateful/core/routes-diff.test.ts。
// import 合并到文件顶部已有语句:
//   import { mergeGoldenRoutes, ... } from './routes-diff';
describe('mergeGoldenRoutes', () => {
  it('keeps registered routes that golden paths match, appends the rest once', () => {
    const registered: RouteShape[] = [
      { method: 'GET', path: '/agents/:id/execute' },
      { method: 'GET', path: '/health' },
    ];
    const golden: RouteShape[] = [
      // 固定值 canonical 能匹配到模板 /agents/:id/execute → 忽略
      { method: 'GET', path: '/agents/contract-1/execute' },
      // 模板形态匹配 → 忽略
      { method: 'GET', path: '/agents/:id/execute' },
      // dump 中不存在 → 追加一次(golden 同路径多 auth 场景需去重)
      { method: 'POST', path: '/legacy-only/run' },
      { method: 'POST', path: '/legacy-only/run' },
    ];
    const merged = mergeGoldenRoutes(registered, golden);
    expect(merged).toEqual([
      { method: 'GET', path: '/agents/:id/execute' },
      { method: 'GET', path: '/health' },
      { method: 'POST', path: '/legacy-only/run' },
    ]);
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report/web && npx vitest run e2e/stateful/core/routes-diff.test.ts`
Expected: FAIL(`mergeGoldenRoutes` 未定义)

- [ ] **Step 3: 实现 mergeGoldenRoutes**

追加到 `web/e2e/stateful/core/routes-diff.ts`:

```ts
// mergeGoldenRoutes 把 golden 契约路径并入注册集:能匹配到已注册模板的忽略,
// 匹配不到的(golden 漏记或 dump 缺路由)按归一化形态追加为额外待覆盖项。
// golden 同一路径含多 auth 场景,追加前按 (method, path) 去重。
export const mergeGoldenRoutes = (
  registered: RouteShape[],
  goldenRoutes: RouteShape[],
): RouteShape[] => {
  const extra: RouteShape[] = [];
  const seen = new Set(registered.map(({ method, path }) => methodPathKey(method, path)));
  for (const golden of goldenRoutes) {
    if (matchTemplate(registered, golden.method, golden.path)) continue;
    const key = methodPathKey(golden.method, golden.path);
    if (seen.has(key)) continue;
    seen.add(key);
    extra.push({ method: golden.method, path: normalizePath(golden.path) });
  }
  return [...registered, ...extra];
};
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report/web && npx vitest run e2e/stateful/core/routes-diff.test.ts`
Expected: PASS

- [ ] **Step 5: spec 接入 golden 并集**

`web/e2e/system-stateful.spec.ts`:

1. import 追加:`mergeGoldenRoutes` from `./stateful/core/routes-diff`。
2. 模块顶部常量区追加 golden 目录:

```ts
const goldenDirPath = fileURLToPath(new URL('../../api/http/testdata/contracts', import.meta.url));
```

1. Task4 Step5 的报告中,`buildUncoveredReport(await fetchRegisteredRoutes(backendURL), ...)` 改为:

```ts
const dumpRoutes = await fetchRegisteredRoutes(backendURL);
const registered = mergeGoldenRoutes(dumpRoutes, await loadGoldenRoutes(goldenDirPath));
const report = buildUncoveredReport(registered, evidence.httpRequests, excludedRoutes,
  safeResults.tested_git_parent, new Date().toISOString());
```

1. 底部追加 IO helper:

```ts
// loadGoldenRoutes 解析 golden 契约文件的 (method, path),供 cross-check 并集。
// golden 是数组(同路径多 auth 场景),每个 entry 含 method/path。
const loadGoldenRoutes = async (goldenDir: string): Promise<RouteShape[]> => {
  const entries = await readdir(goldenDir);
  const golden: RouteShape[] = [];
  for (const entry of entries) {
    if (!entry.endsWith('.golden.json')) continue;
    const body = JSON.parse(await readFile(join(goldenDir, entry), 'utf8')) as
      Array<{ method?: string; path?: string }>;
    for (const item of body) {
      if (typeof item.method === 'string' && typeof item.path === 'string') {
        golden.push({ method: item.method, path: item.path });
      }
    }
  }
  return golden;
};
```

> import 补充:第 3 行 `import { readFile, writeFile } from 'node:fs/promises';` 改为 `import { readdir, readFile, writeFile } from 'node:fs/promises';`;新增 `import { join } from 'node:path';`(归入现有 node import 区)。`new URL(entry, ...)` 用文件路径字符串做 base 会抛错,故用 `join` 拼绝对路径。

- [ ] **Step 6: 类型检查 + 单测**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report/web && npx tsc --noEmit -p tsconfig.json`
Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report/web && npx vitest run e2e/stateful/core/routes-diff.test.ts`
Expected: 无类型错误;PASS

- [ ] **Step 7: 提交**

```bash
git add web/e2e/stateful/core/routes-diff.ts web/e2e/stateful/core/routes-diff.test.ts web/e2e/system-stateful.spec.ts
git commit -m "feat(e2e): golden 契约 cross-check 并入路由全集
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: 报告格式 schema 校验脚本

**Files:**

- Create: `scripts/quality/check-e2e-uncovered-report.sh`
- Create: `scripts/quality/check-e2e-uncovered-report-test.sh`

**Interfaces:**

- Consumes: `test/e2e/stateful/uncovered-report.json`(Task4 产物)。
- Produces: 校验报告 JSON 形状(6 个顶层字段齐全;uncovered 每项含 method/path/domain_hint;excluded 每项含 reason),异常 exit 1。支持 `REPORT_PATH` 环境变量注入(自测用),默认仓库路径。

- [ ] **Step 1: 写校验脚本**

```bash
#!/usr/bin/env bash
# check-e2e-uncovered-report.sh — 校验 uncovered 报告 JSON 形状。
# 报告由 system-stateful.spec.ts 生成,告警级不 gate CI;此脚本守护其 schema,防漂移。
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
report="${REPORT_PATH:-$repo_dir/test/e2e/stateful/uncovered-report.json}"

[[ -f "$report" ]] || { echo "MISSING: $report (run stateful E2E first)"; exit 1; }

issues=0

for field in generated_at tested_git_parent route_total covered uncovered excluded; do
  jq -e "has(\"$field\")" "$report" >/dev/null 2>&1 \
    || { echo "MISSING top-level field: $field"; issues=$((issues + 1)); }
done

jq -e '.uncovered | all(.method != null and .path != null and .domain_hint != null)' "$report" \
  >/dev/null 2>&1 || { echo "uncovered entries must have method/path/domain_hint"; issues=$((issues + 1)); }

jq -e '.excluded | all(.reason != null)' "$report" >/dev/null 2>&1 \
  || { echo "excluded entries must have reason"; issues=$((issues + 1)); }

if [[ "$issues" -eq 0 ]]; then
  echo "PASS: uncovered report shape ok ($(jq -r '.uncovered | length' "$report") uncovered)"
else
  echo "FAIL: $issues uncovered-report shape issue(s)"
  exit 1
fi
```

- [ ] **Step 2: 写自测脚本**

```bash
#!/usr/bin/env bash
# check-e2e-uncovered-report-test.sh — 自测:合法报告通过、非法报告失败。
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
script="$repo_dir/scripts/quality/check-e2e-uncovered-report.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# 合法报告
cat > "$tmp/valid.json" <<'JSON'
{
  "generated_at": "2026-08-15T00:00:00Z",
  "tested_git_parent": "abc123",
  "route_total": 3,
  "covered": ["GET /memory"],
  "uncovered": [{"method": "POST", "path": "/mcp/servers/:param/reconnect", "domain_hint": "mcp"}],
  "excluded": [{"method": "GET", "path": "/health", "reason": "infra"}]
}
JSON
REPORT_PATH="$tmp/valid.json" "$script" || { echo "expected valid report to pass"; exit 1; }

# 缺 domain_hint 的非法报告
cat > "$tmp/invalid.json" <<'JSON'
{
  "generated_at": "2026-08-15T00:00:00Z",
  "tested_git_parent": "abc123",
  "route_total": 1,
  "covered": [],
  "uncovered": [{"method": "POST", "path": "/x"}],
  "excluded": []
}
JSON
if REPORT_PATH="$tmp/invalid.json" "$script" >/dev/null 2>&1; then
  echo "expected failure for missing domain_hint"; exit 1
fi

echo "SELFTEST PASS: valid accepted, invalid rejected"
```

- [ ] **Step 3: 跑自测**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report && bash scripts/quality/check-e2e-uncovered-report-test.sh`
Expected: 输出 `PASS: uncovered report shape ok (1 uncovered)` 与 `SELFTEST PASS: valid accepted, invalid rejected`,exit 0

- [ ] **Step 4: 挂到 make(可选,建议)**

`Makefile` 现有 quality 目标链参考 `make check` 中 `check-e2e-coverage.sh` 的挂法,新增 `check-e2e-uncovered-report.sh` 调用。注意:报告只在 stateful E2E 后生成,`make check` 若作为 git 门禁不应硬链此脚本(无报告时 exit 1 会误伤)。建议独立目标 `make check-e2e-report`,仅在 E2E 后显式调用。

- [ ] **Step 5: 提交**

```bash
chmod +x scripts/quality/check-e2e-uncovered-report.sh scripts/quality/check-e2e-uncovered-report-test.sh
git add scripts/quality/check-e2e-uncovered-report.sh scripts/quality/check-e2e-uncovered-report-test.sh
git commit -m "test(e2e): uncovered 报告 schema 校验脚本
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 7: 文档 + 全量验证

**Files:**

- Modify: `docs/agent/backend-go.md`(测试节补充)

**Interfaces:**

- Consumes: 前 6 个任务全部产物。
- Produces: 文档说明;全量测试通过证明。

- [ ] **Step 1: 文档补充**

`docs/agent/backend-go.md` 测试节末尾追加:

```md
- **新增 API 后如何发现未覆盖**:stateful E2E 跑完自动生成
  `test/e2e/stateful/uncovered-report.json`(仅告警,不阻断)。注册路由全集来自
  `GET /e2e/routes`(gin `Routes()` dump),与浏览器实测请求经模板匹配取差集;
  `domain_hint` 提示该补到哪个 pack。开发者/AI 照清单补 `manifest.json` capability +
  pack action,再跑 E2E 确认该项进入 `covered`。
- **排除集**:基础设施探活路由(`/health`、`/livez`、`/readyz`、`/metrics`)与
  `/e2e/routes` 自身在 spec 中显式排除,不要求浏览器 UI 覆盖。
```

- [ ] **Step 2: 前端全量单测**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report/web && npx vitest run`
Expected: PASS(含新增 routes-diff、evidence 测试;存量测试不回归)

- [ ] **Step 3: 后端全量快速测试**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report && go vet ./api/http/ && go test -short ./api/http/ -count=1`
Expected: PASS(含新增 `TestE2ERoutesEndpointListsRegisteredRoutes`)

- [ ] **Step 4: lint + build 前端**

Run: `cd /home/yang/go-projects/stratum-e2e-uncovered-report/web && npm run lint && npm run build`
Expected: PASS(lint 报错按项目风格修,`npm run build` 在 `tsc` 通过后应顺过)

- [ ] **Step 5: 提交文档**

```bash
git add docs/agent/backend-go.md
git commit -m "docs(agent): 新增 API 后的未覆盖报告使用说明
Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Self-Review 记录

**Spec coverage:** 逐条核对——

- 数据采集(运行时实测)→ Task3(context 级 `on('request')`);决策 3"运行时实测"。
- path 归一化 → Task2 `normalizePath` + `matchTemplate`(模板匹配即归一化,替代启发式,准确率更高;报告展示 `:param` 形态与 spec 例子一致)。
- 路由全集 → Task1(`GET /e2e/routes` gin dump)+ Task5(golden cross-check 并集);决策 1/5。
- 排除集 → Task2 `excludeRoutes`(独立纯函数,spec 第 86 行要求)+ Task4 spec 内 `excludedRoutes` 常量(含 reason)。
- 差集与报告 → Task4 `buildUncoveredReport` + `test/e2e/stateful/uncovered-report.json`;`domain_hint` 按前缀推断。
- 仅告警不阻断 → Task4 Step5 `try/catch` + 注释,不改 `reconcileCapabilities`。
- 纯函数单测 → Task2/4/5 表驱动 vitest;格式校验脚本 → Task6;文档 → Task7。
- 边界("不自动生成 pack/manifest、不反向写 manifest、不做前端路由联动")→ 计划不触碰这些路径,符合。

**Placeholder scan:** 已消除 `config.Default()`、`httpTestContainer` 占位——Task1 改白盒 `package http`(有 `router_health_test.go` 先例),零容器依赖。全部代码块完整。`loadGoldenRoutes` 的 IO 行为由 Task5 Step5 内联 helper 给出,非 TBD。

**Type consistency:** `RouteShape`(Task2)贯穿 Task4/5;`ExcludedRoute = RouteShape & { reason }`(Task4)兼容 `diffRoutes` 的 `RouteShape[]` 参数;`matchTemplate` 返回 `RouteShape | null` 被 `diffRoutes`/`mergeGoldenRoutes` 复用;`buildUncoveredReport(registered, coveredRaw, excluded, gitParent, generatedAt)` 签名与 Task4 测试一致;`emptyEvidence`(Task3)供 spec 复用且删除内联定义。采集格式 `"METHOD pathname"`(Task3)与 `diffRoutes` 解析逻辑(space 拆分)一致。
