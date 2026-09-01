# 平台管理页面只读可见 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 平台管理下 5 个页面对所有登录租户成员只读可见（数据可读、写控件置灰），平台管理员（system_admin / global_admin）保持既有编辑权限。

**Architecture:** 后端把平台管理 GET 接口从 `RequireSystemAdmin` 分组移动到 `protectedTenantMiddleware(c, RequireTenantRole("member"))` 只读分组（写接口留在 `adminGroup`），并用源码级 RBAC 测试守护路由归属。前端移除 `PrivateRoute` 的 `requiredRole`，新增共享 `PlatformAdminGate` 组件（提示条 + `PlatformAdminContext` 下发 `canEdit`），各页面写控件 `disabled={!canEdit}`；`/admin/audit` 页面无写控件，只开放访问。

**Tech Stack:** Go 1.25 / Gin v1.9、React 18 / Vite 6 / Ant Design 5 / Vitest。

## Global Constraints

- 主 checkout 对 agents 只读：所有文件改动在 worktree `/home/yang/go-projects/stratum-platform-admin-readonly/`，禁止在 main 提交。提交格式 `[type](scope): description`。
- **Gin 同 method+path 只能注册一次**，GET 路由必须「移动」而非复制，否则 panic。
- **后端是授权强制点（fail-closed）**：所有写接口必须留在 `adminGroup`（system_admin，删除租户 global_admin）；前端置灰只是体验层。
- **`GET /admin/users` 不开放**（仅添加管理员候选搜索用），保持在 `adminGroup`。
- 前端 `usePlatformRole` 读 `users.global_role`，rank 为 `user=1 < system_admin=2 < global_admin=3`，与后端 `middleware.RequirePlatformAdmin` 一致；前端唯一事实源。
- **`usePlatformAdminCanEdit` 默认 `canEdit=true`**（与改造前行为一致）：该 hook 只控 UI 可用性；生产路由必须用 `PlatformAdminGate` 包裹，由路由级源码测试守护（Task 3）。后台兜底是后端中间件。
- Go 行宽 ≤120；日志只用 Zap；行为数字不得内联。前端用户可见字符串中文；Bearer 不入 Web Storage。
- 验收：IAM/授权链路改动 → `.test/verification.yaml` R3，完整测试（`make test-verify-before-pr`），系统验收由 `stratum-e2e-tester` 执行。

---

### Task 1: 后端路由拆分——平台只读 GET 开放给租户成员

**Files:**

- Modify: `api/http/router.go:295-317`（adminGroup 段）、`api/http/router.go:335-348`（`registerParameterAdminRoutes`）、`api/http/router.go:696-700`（platformAudit 组）
- Create: `api/http/router_platform_admin_readonly_rbac_test.go`
- Test: 上述新测试 + 既有 `api/http/contract_test.go`（不应破坏，golden 用 global_admin token）

**Interfaces:**

- Consumes: `protectedTenantMiddleware(c, extra ...gin.HandlerFunc) []gin.HandlerFunc`（router.go:429）、`handler.NewAdminHandler`、`handler.NewParameterHandler`、`handler.NewPlatformAuditHandler`、`middleware.RequireTenantRole("member")`。
- Produces: 函数 `registerParameterReadRoutes(readGroup *gin.RouterGroup, c *wiring.Container)`、`registerParameterWriteRoutes(adminGroup *gin.RouterGroup, c *wiring.Container)`；删除 `registerParameterAdminRoutes`。

- [ ] **Step 1: 写失败测试（源码级 RBAC 断言）**

创建 `api/http/router_platform_admin_readonly_rbac_test.go`：

```go
package http

import (
	"os"
	"strings"
	"testing"
)

func readRouterSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("router.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestPlatformAdminReadRoutesRequireTenantMember guards the read-only surface of
// 平台管理 pages: every GET backing a 平台管理 page must be registered on a
// member-protected group so all logged-in tenant members can view the data.
func TestPlatformAdminReadRoutesRequireTenantMember(t *testing.T) {
	source := readRouterSource(t)
	if !strings.Contains(source, `platformRead := r.Group("/admin", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)`) {
		t.Fatal("platform admin read group must require authenticated tenant member context")
	}
	for _, line := range []string{
		`platformRead.GET("/tenants", adminHandler.ListTenants)`,
		`platformRead.GET("/tenants/:id", adminHandler.GetTenant)`,
		`platformRead.GET("/admins", adminHandler.ListAdmins)`,
		`registerParameterReadRoutes(platformRead, c)`,
		`readGroup.GET("/parameters/schema", paramHandler.Schema)`,
		`readGroup.GET("/parameters", paramHandler.List)`,
		`readGroup.GET("/parameters/versions/:groupKey", paramHandler.Versions)`,
	} {
		if !strings.Contains(source, line) {
			t.Fatalf("read route must be on member-protected group: %s", line)
		}
	}
	// 平台审计分组头存在，且旧 JWT+RequireSystemAdmin 门控必须被移除（分组定义在
	// 多行内，不做整行匹配）。
	if !strings.Contains(source, `platformAudit := r.Group("/admin/audit/platform",`) {
		t.Fatal("platform audit read group must be registered")
	}
	if strings.Contains(source, "platformAudit := r.Group(\"/admin/audit/platform\",\n\t\t\tmiddleware.JWTMiddleware") {
		t.Fatal("platform audit read group must not remain gated on system_admin")
	}
}

// TestPlatformAdminWriteRoutesRequireSystemAdmin guards the write surface: every
// 平台管理 mutation must stay on the system_admin-gated adminGroup (fail-closed).
func TestPlatformAdminWriteRoutesRequireSystemAdmin(t *testing.T) {
	source := readRouterSource(t)
	for _, line := range []string{
		`adminGroup.POST("/tenants", adminHandler.CreateTenant)`,
		`adminGroup.PATCH("/tenants/:id", adminHandler.UpdateTenant)`,
		`adminGroup.DELETE("/tenants/:id", middleware.RequireGlobalAdmin(), adminHandler.DeleteTenant)`,
		`registerParameterWriteRoutes(adminGroup, c)`,
		`adminGroup.PUT("/parameters", paramHandler.Update)`,
		`adminGroup.POST("/parameters/versions/:groupKey", paramHandler.CreateDraft)`,
		`adminGroup.GET("/users", adminHandler.SearchUsers)`,
	} {
		if !strings.Contains(source, line) {
			t.Fatalf("write route must stay on system_admin group: %s", line)
		}
	}
	if strings.Contains(source, `adminGroup := r.Group("/admin", jwtMW, middleware.RequireGlobalAdmin())`) {
		t.Fatal("platform admin group must require system_admin, never global_admin only")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-platform-admin-readonly && go test ./api/http/ -run 'TestPlatformAdminRead|TestPlatformAdminWrite' -count=1`
Expected: FAIL（`platformRead := ...` 等字符串尚不存在）。

- [ ] **Step 3: 重构 `api/http/router.go`**

3a. 替换 adminGroup 段（原 295-317 行）为「只读分组 + 写分组」：

```go
	// 平台管理只读接口：所有登录租户成员可读（租户/参数/管理员名单），写接口见 adminGroup。
	platformRead := r.Group("/admin", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
	{
		platformRead.GET("/tenants", adminHandler.ListTenants)
		platformRead.GET("/tenants/:id", adminHandler.GetTenant)
		registerParameterReadRoutes(platformRead, c)
		platformRead.GET("/admins", adminHandler.ListAdmins)
	}

	// /admin 常规后台写接口：system_admin 及以上（租户管理、参数、memory DLQ）。
	adminGroup := r.Group("/admin", jwtMW, middleware.RequireSystemAdmin())
	{
		adminGroup.POST("/tenants", adminHandler.CreateTenant)
		adminGroup.PATCH("/tenants/:id", adminHandler.UpdateTenant)
		// 高敏感：删除租户仅 global_admin。
		adminGroup.DELETE("/tenants/:id", middleware.RequireGlobalAdmin(), adminHandler.DeleteTenant)
		registerParameterWriteRoutes(adminGroup, c)
		registerMemoryDLQAdminRoutes(adminGroup, c)

		// 平台管理员管理：仅 global_admin（system_admin 不可自我管理或管理同级）。
		adminAdmins := adminGroup.Group("/admins", middleware.RequireGlobalAdmin())
		{
			adminAdmins.POST("", adminHandler.SetAdminRole)
			adminAdmins.DELETE("/:user_id", adminHandler.RemoveAdminRole)
		}

		// 用户搜索：供提升选择候选，system_admin 可见（候选不含管理员）。
		adminGroup.GET("/users", adminHandler.SearchUsers)
	}
```

3b. 把 `registerParameterAdminRoutes`（原 333-348 行）拆成两个函数（整体替换）：

```go
// registerParameterReadRoutes wires the unified parameter registry read-only
// endpoints (schema + platform values) for all tenant members.
func registerParameterReadRoutes(readGroup *gin.RouterGroup, c *wiring.Container) {
	if c.Parameters == nil || c.Parameters.Service == nil {
		return
	}
	paramHandler := handler.NewParameterHandler(c.Parameters.Service, c.Logger)
	readGroup.GET("/parameters/schema", paramHandler.Schema)
	readGroup.GET("/parameters", paramHandler.List)
	readGroup.GET("/parameters/versions/:groupKey", paramHandler.Versions)
}

// registerParameterWriteRoutes wires the unified parameter registry write
// endpoints, which remain gated by the parent group's system_admin middleware.
func registerParameterWriteRoutes(adminGroup *gin.RouterGroup, c *wiring.Container) {
	if c.Parameters == nil || c.Parameters.Service == nil {
		return
	}
	paramHandler := handler.NewParameterHandler(c.Parameters.Service, c.Logger)
	adminGroup.PUT("/parameters", paramHandler.Update)
	adminGroup.POST("/parameters/versions/:groupKey", paramHandler.CreateDraft)
	adminGroup.POST("/parameters/versions/:groupKey/:versionID/publish", paramHandler.Publish)
	adminGroup.POST("/parameters/versions/:groupKey/:versionID/rollback", paramHandler.Rollback)
}
```

3c. 平台审计（`registerAudit` 内，原 696-700 行）改为 member 组：

```go
		platformAudit := r.Group("/admin/audit/platform",
			protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
		platformAudit.GET("/events", platformHandler.List)
		platformAudit.GET("/events/:id", platformHandler.Get)
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./api/http/ -run 'TestPlatformAdminRead|TestPlatformAdminWrite' -count=1 && go test ./api/http/ -short -count=1`
Expected: 新测试 PASS；既有 contract / handler 测试 PASS（global_admin token 在 member 组仍通过）。

- [ ] **Step 5: 核对只读 DTO 无敏感字段（验证步骤）**

Open 并确认（不需要改代码，此步是证据核对）：

- `api/http/handler/admin_handler.go`：`ListTenants`/`GetTenant` 只返回 `TenantResponse{ID,Name,Slug,Plan,Status,CreatedAt,DeletedAt,MemberCount,IsDefault}`；`ListAdmins` 只返回 `AdminUserResponse{user_id,username,github_login,avatar_url,global_role}`。均不含联系方式/邀请码等 PII。
- `internal/parameters/application/service.go`：`PlatformValues` 中敏感参数值写库前经 `sanitize()`（SHA-256 `sha256:` 前缀）掩码，读路径不泄露原文。

- [ ] **Step 6: 提交**

```bash
git add api/http/router.go api/http/router_platform_admin_readonly_rbac_test.go
git commit -m "feat(platform-admin): open platform admin read routes to all tenant members"
```

---

### Task 2: 前端——`PlatformAdminGate` 共享组件 + 导出

**Files:**

- Create: `web/src/modules/iam/components/PlatformAdminGate.tsx`
- Create: `web/src/modules/iam/components/__tests__/PlatformAdminGate.test.tsx`
- Modify: `web/src/modules/iam/index.ts`（追加导出）

**Interfaces:**

- Consumes: `usePlatformRole`（`web/src/modules/iam/hooks/usePlatformRole.ts`，返回 `{ role, isSystemAdmin, isGlobalAdmin, hasPlatformRole(min) }`）。
- Produces: `PlatformAdminGate({ minRole: 'system_admin' | 'global_admin', children })`、`usePlatformAdminCanEdit(): boolean`，二者从 `@/modules/iam` 导出，供 Task 3-6 使用。

- [ ] **Step 1: 写失败测试**

创建 `web/src/modules/iam/components/__tests__/PlatformAdminGate.test.tsx`：

```tsx
import { render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';

import { PlatformAdminGate, usePlatformAdminCanEdit } from '../PlatformAdminGate';

const authState = vi.hoisted(() => ({ user: { global_role: 'system_admin' } }));
vi.mock('@/modules/iam/components/AuthContext', () => ({ useAuth: () => authState }));

const Probe = () => <div data-testid="can-edit">{String(usePlatformAdminCanEdit())}</div>;

beforeEach(() => {
  vi.clearAllMocks();
  authState.user = { global_role: 'system_admin' };
});

it('renders a readonly alert and canEdit=false for a plain member', () => {
  authState.user = { global_role: 'user' };
  render(
    <PlatformAdminGate minRole="system_admin">
      <Probe />
    </PlatformAdminGate>,
  );
  expect(screen.getByText('只读模式')).toBeInTheDocument();
  expect(screen.getByTestId('can-edit')).toHaveTextContent('false');
});

it('hides the alert and keeps canEdit=true for a system_admin', () => {
  render(
    <PlatformAdminGate minRole="system_admin">
      <Probe />
    </PlatformAdminGate>,
  );
  expect(screen.queryByText('只读模式')).not.toBeInTheDocument();
  expect(screen.getByTestId('can-edit')).toHaveTextContent('true');
});

it('requires the minRole rank: a system_admin cannot edit a global_admin gate', () => {
  render(
    <PlatformAdminGate minRole="global_admin">
      <Probe />
    </PlatformAdminGate>,
  );
  expect(screen.getByText('只读模式')).toBeInTheDocument();
  expect(screen.getByTestId('can-edit')).toHaveTextContent('false');
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-platform-admin-readonly/web && npx vitest run src/modules/iam/components/__tests__/PlatformAdminGate.test.tsx`
Expected: FAIL（模块 `../PlatformAdminGate` 不存在）。

- [ ] **Step 3: 创建组件**

创建 `web/src/modules/iam/components/PlatformAdminGate.tsx`：

```tsx
import { Alert } from 'antd';
import { createContext, useContext, type ReactNode } from 'react';

import { usePlatformRole } from '../hooks/usePlatformRole';

interface PlatformAdminGateProps {
  /** 可编辑所需的最低平台角色：'system_admin' | 'global_admin'。 */
  minRole: 'system_admin' | 'global_admin';
  children: ReactNode;
}

// 默认 canEdit=true 与改造前行为一致（未包裹 Gate 的页面仍可编辑，向后兼容既有
// 页面测试）。本组件只控制 UI 可用性，真正的写权限由后端中间件强制（fail-closed）；
// 生产路由必须用 PlatformAdminGate 包裹，由路由级源码测试守护（见 routes 测试）。
const PlatformAdminContext = createContext<{ canEdit: boolean }>({ canEdit: true });

/** 读取当前页面是否可编辑；须在 PlatformAdminGate 内使用（默认 true）。 */
export const usePlatformAdminCanEdit = (): boolean => useContext(PlatformAdminContext).canEdit;

export const PlatformAdminGate = ({ minRole, children }: PlatformAdminGateProps) => {
  const { hasPlatformRole } = usePlatformRole();
  const canEdit = hasPlatformRole(minRole);
  return (
    <PlatformAdminContext.Provider value={{ canEdit }}>
      {!canEdit && (
        <Alert
          type="info"
          showIcon
          message="只读模式"
          description="您当前为只读模式，仅平台管理员可编辑本页内容"
          style={{ marginBottom: 16 }}
        />
      )}
      {children}
    </PlatformAdminContext.Provider>
  );
};

export default PlatformAdminGate;
```

- [ ] **Step 4: 追加导出到 `web/src/modules/iam/index.ts`**

在第 3 行 `export { PrivateRoute } ...` 之后追加：

```ts
export { PlatformAdminGate, usePlatformAdminCanEdit } from './components/PlatformAdminGate';
```

- [ ] **Step 5: 运行测试确认通过**

Run: `npx vitest run src/modules/iam/components/__tests__/PlatformAdminGate.test.tsx`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add web/src/modules/iam/components/PlatformAdminGate.tsx web/src/modules/iam/components/__tests__/PlatformAdminGate.test.tsx web/src/modules/iam/index.ts
git commit -m "feat(iam): add PlatformAdminGate readonly gate component"
```

---

### Task 3: 前端路由——去 `requiredRole` + Gate 包裹

**Files:**

- Modify: `web/src/modules/llm/routes.tsx`
- Modify: `web/src/modules/iam/routes.tsx`
- Modify: `web/src/modules/parameters/routes.tsx`
- Create: `web/src/app/__tests__/platform-admin-routes.test.ts`
- Modify: `web/src/app/layout/__tests__/menu.config.test.tsx`（仅更新两条已过时注释）

**Interfaces:**

- Consumes: `PlatformAdminGate` / `usePlatformAdminCanEdit`（Task 2 产物）。
- Produces: 4 个 gated 路由（`/models`、`/admin/tenants`、`/admin/settings` 为 system_admin；`/admin/admins` 为 global_admin）；`/admin/audit` 不加 Gate。

- [ ] **Step 1: 写失败测试（路由源码级守护）**

创建 `web/src/app/__tests__/platform-admin-routes.test.ts`：

```ts
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const routesSource = (mod: 'llm' | 'iam' | 'parameters'): string =>
  readFileSync(new URL(`../../modules/${mod}/routes.tsx`, import.meta.url), 'utf8');

describe('平台管理路由：member 可读、编辑按 minRole 置灰', () => {
  it('模型管理 /models 去 requiredRole 且包 system_admin Gate', () => {
    const src = routesSource('llm');
    expect(src).not.toContain('requiredRole');
    expect(src).toContain('<PlatformAdminGate minRole="system_admin">');
  });

  it('全局租户 /admin/tenants 包 system_admin Gate', () => {
    const src = routesSource('iam');
    expect(src).toContain('<PlatformAdminGate minRole="system_admin">');
    expect(src).toContain('<TenantsListPage />');
  });

  it('平台管理员 /admin/admins 包 global_admin Gate', () => {
    expect(routesSource('iam')).toContain('<PlatformAdminGate minRole="global_admin">');
  });

  it('平台参数 /admin/settings 去 requiredRole 且包 system_admin Gate', () => {
    const src = routesSource('parameters');
    expect(src).not.toContain('requiredRole');
    expect(src).toContain('<PlatformAdminGate minRole="system_admin">');
  });

  it('审计日志 /admin/audit 不加 Gate（页面本身无写控件），仅 iam 两条管理路由被 Gate 包裹', () => {
    const src = routesSource('iam');
    expect(src).toContain('<PlatformAuditPage />');
    expect(src.match(/<PlatformAdminGate/g)?.length).toBe(2);
  });

  it('全部平台管理路由不再使用 requiredRole 守卫', () => {
    for (const mod of ['llm', 'iam', 'parameters'] as const) {
      expect(routesSource(mod)).not.toMatch(/requiredRole=/);
    }
  });
});
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-platform-admin-readonly/web && npx vitest run src/app/__tests__/platform-admin-routes.test.ts`
Expected: FAIL（各路由仍含 `requiredRole`、无 Gate）。

- [ ] **Step 3: 改写三个路由文件**

3a. `web/src/modules/llm/routes.tsx`（整体替换）：

```tsx
import { Route } from 'react-router-dom';

import { ModelManagementPage } from './pages/ModelManagementPage';

import { PlatformAdminGate, PrivateRoute } from '@/modules/iam';

// 模型目录为公共平台资源：所有登录租户成员可只读查看（数据 GET 对 member 开放），
// 编辑（厂商/模型增删改、启停）需 system_admin 及以上，由 PlatformAdminGate 置灰。
export const llmRoutes = [
  <Route
    key="models"
    path="/models"
    element={
      <PrivateRoute>
        <PlatformAdminGate minRole="system_admin">
          <ModelManagementPage />
        </PlatformAdminGate>
      </PrivateRoute>
    }
  />,
];
```

3b. `web/src/modules/iam/routes.tsx`：把 `iamPrivateRoutes` 中三条 admin 路由改守卫。

`admin-tenants`（原 51-59 行）：

```tsx
  <Route
    key="admin-tenants"
    path="/admin/tenants"
    element={
      <PrivateRoute>
        <PlatformAdminGate minRole="system_admin">
          <TenantsListPage />
        </PlatformAdminGate>
      </PrivateRoute>
    }
  />,
```

`admin-admins`（原 60-68 行）：

```tsx
  <Route
    key="admin-admins"
    path="/admin/admins"
    element={
      <PrivateRoute>
        <PlatformAdminGate minRole="global_admin">
          <AdminsPage />
        </PlatformAdminGate>
      </PrivateRoute>
    }
  />,
```

`admin-audit`（原 69-77 行）：

```tsx
  <Route
    key="admin-audit"
    path="/admin/audit"
    element={
      <PrivateRoute>
        <PlatformAuditPage />
      </PrivateRoute>
    }
  />,
```

同时更新文件头部 import（第 5 行 `import { PrivateRoute } from './components/PrivateRoute';` 后追加）：

```tsx
import { PlatformAdminGate } from './components/PlatformAdminGate';
```

3c. `web/src/modules/parameters/routes.tsx`（整体替换）：

```tsx
import { Route } from 'react-router-dom';

import { PlatformSettingsPage } from './pages/PlatformSettingsPage';

import { PlatformAdminGate, PrivateRoute } from '@/modules/iam';

export const parametersRoutes = [
  <Route
    key="admin-settings"
    path="/admin/settings"
    element={
      <PrivateRoute>
        <PlatformAdminGate minRole="system_admin">
          <PlatformSettingsPage />
        </PlatformAdminGate>
      </PrivateRoute>
    }
  />,
];
```

- [ ] **Step 4: 更新 `web/src/app/layout/__tests__/menu.config.test.tsx` 两条过时注释**

- 第 66 行 `// 平台管理菜单对普通用户常显；访问权限由 PrivateRoute(403) 承担` → `// 平台管理菜单对普通用户常显；普通成员只读可见（编辑控件由 PlatformAdminGate 置灰）`
- 第 82 行 `// 平台管理组常显，member 点击会被 PrivateRoute 403 拦截` → `// 平台管理组常显，member 只读（Gate 置灰），写权限按角色`

- [ ] **Step 5: 运行测试确认通过 + 构建**

Run: `npx vitest run src/app/__tests__/platform-admin-routes.test.ts && npx vitest run src/app/layout/__tests__/menu.config.test.tsx`
Expected: 新测试 PASS；menu 测试 PASS。
Run: `cd /home/yang/go-projects/stratum-platform-admin-readonly && make fe-build`
Expected: 构建成功（无 TS 类型错误）。

- [ ] **Step 6: 提交**

```bash
git add web/src/modules/llm/routes.tsx web/src/modules/iam/routes.tsx web/src/modules/parameters/routes.tsx web/src/app/__tests__/platform-admin-routes.test.ts web/src/app/layout/__tests__/menu.config.test.tsx
git commit -m "feat(platform-admin): replace route guards with PlatformAdminGate wrappers"
```

---

### Task 4: 前端——模型管理页置灰（ProviderListPage / ModelListPage）

**Files:**

- Modify: `web/src/modules/llm/pages/ProviderListPage.tsx`
- Modify: `web/src/modules/llm/pages/ModelListPage.tsx`
- Create: `web/src/modules/llm/pages/__tests__/ProviderListPageReadonly.test.tsx`
- Create: `web/src/modules/llm/pages/__tests__/ModelListPageReadonly.test.tsx`

**Interfaces:**

- Consumes: `usePlatformAdminCanEdit`（`@/modules/iam`）。
- Produces: 两页所有写控件 `disabled={!canEdit}`；读操作（刷新、能力筛选）保持可用。

- [ ] **Step 1: 写失败测试（Provider 页）**

创建 `web/src/modules/llm/pages/__tests__/ProviderListPageReadonly.test.tsx`：

```tsx
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeAll, beforeEach, expect, it, vi } from 'vitest';

import { ProviderListPage } from '../ProviderListPage';

import { PlatformAdminGate } from '@/modules/iam';

const authState = vi.hoisted(() => ({ user: { global_role: 'user' } }));
vi.mock('@/modules/iam/components/AuthContext', () => ({ useAuth: () => authState }));
vi.mock('@/modules/llm/hooks/useProviders', () => ({
  useProviders: () => ({
    providers: [
      { id: 'p1', name: '测试厂商', kind: 'openai_compat', baseUrl: 'https://example.com', defaultModel: '', enabled: true },
    ],
    loading: false,
    createLoading: false,
    updateLoading: false,
    refresh: vi.fn(),
    createProvider: vi.fn(),
    updateProvider: vi.fn(),
    deleteProvider: vi.fn(),
  }),
}));

beforeAll(() => {
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn() })));
});

beforeEach(() => {
  vi.clearAllMocks();
  authState.user = { global_role: 'user' };
});

it('disables every write control for a plain member and keeps 刷新 enabled', async () => {
  render(
    <PlatformAdminGate minRole="system_admin">
      <ProviderListPage />
    </PlatformAdminGate>,
  );
  expect(await screen.findByText('测试厂商')).toBeInTheDocument();
  // 只读提示条
  expect(screen.getByText('只读模式')).toBeInTheDocument();
  // 写控件全部置灰
  expect(screen.getByRole('button', { name: '添加厂商' })).toBeDisabled();
  expect(screen.getByRole('button', { name: '添加模型' })).toBeDisabled();
  expect(screen.getByRole('button', { name: '发现模型' })).toBeDisabled();
  expect(screen.getByRole('button', { name: '健康检查' })).toBeDisabled();
  expect(screen.getByRole('button', { name: /编辑/ })).toBeDisabled();
  expect(screen.getByRole('button', { name: /删除/ })).toBeDisabled();
  // 读操作刷新保持可用
  expect(screen.getByRole('button', { name: '刷新' })).not.toBeDisabled();
});

it('enables write controls for a system_admin', async () => {
  authState.user = { global_role: 'system_admin' };
  render(
    <PlatformAdminGate minRole="system_admin">
      <ProviderListPage />
    </PlatformAdminGate>,
  );
  expect(await screen.findByText('测试厂商')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '添加厂商' })).not.toBeDisabled();
  expect(screen.queryByText('只读模式')).not.toBeInTheDocument();
});
```

> 提示：ProviderListPage 行操作列的「编辑」「删除」是无文本图标按钮，Tooltip title 不构成按钮的 accessible name，`name: /编辑/` `/删除/` 无法命中。因此 Step 4 会给这两个按钮补 `aria-label="编辑"` / `aria-label="删除"`（同时提升无障碍），本测试的 accessible-name 断言依赖该 aria-label。

- [ ] **Step 2: 写失败测试（Model 页）**

创建 `web/src/modules/llm/pages/__tests__/ModelListPageReadonly.test.tsx`：

```tsx
import { render, screen } from '@testing-library/react';
import { beforeAll, beforeEach, expect, it, vi } from 'vitest';

import { ModelListPage } from '../ModelListPage';

import { PlatformAdminGate } from '@/modules/iam';

const authState = vi.hoisted(() => ({ user: { global_role: 'user' } }));
vi.mock('@/modules/iam/components/AuthContext', () => ({ useAuth: () => authState }));
vi.mock('@/modules/llm/hooks/useModels', () => ({
  useModels: () => ({
    models: [
      { id: 'm1', name: 'test-model', displayName: '测试模型', providerId: 'p1', capabilities: ['chat'], enabled: true },
    ],
    loading: false,
    refresh: vi.fn(),
    toggleModel: vi.fn(),
    updateModel: vi.fn(),
    updateModelPolicy: vi.fn(),
    deleteModel: vi.fn(),
  }),
}));

beforeAll(() => {
  vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false, addListener: vi.fn(), removeListener: vi.fn() })));
});

beforeEach(() => {
  vi.clearAllMocks();
  authState.user = { global_role: 'user' };
});

it('disables edit/delete and the enable toggle for a plain member', async () => {
  render(
    <PlatformAdminGate minRole="system_admin">
      <ModelListPage />
    </PlatformAdminGate>,
  );
  expect(await screen.findByText('测试模型')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '编辑' })).toBeDisabled();
  expect(screen.getByRole('button', { name: /删除/ })).toBeDisabled();
  // 启停 Switch 置灰（antd Switch disabled 时无 button role，用 input[role=switch] 判定）
  const switches = document.querySelectorAll('button[role="switch"].ant-switch-disabled');
  expect(switches.length).toBe(1);
  expect(screen.getByRole('button', { name: '刷新' })).not.toBeDisabled();
});

it('enables write controls for a system_admin', async () => {
  authState.user = { global_role: 'system_admin' };
  render(
    <PlatformAdminGate minRole="system_admin">
      <ModelListPage />
    </PlatformAdminGate>,
  );
  expect(await screen.findByText('测试模型')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '编辑' })).not.toBeDisabled();
  expect(screen.queryByText('只读模式')).not.toBeInTheDocument();
});
```

- [ ] **Step 3: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-platform-admin-readonly/web && npx vitest run src/modules/llm/pages/__tests__/ProviderListPageReadonly.test.tsx src/modules/llm/pages/__tests__/ModelListPageReadonly.test.tsx`
Expected: FAIL（`usePlatformAdminCanEdit` 未被使用、按钮未置灰）。

- [ ] **Step 4: 实现置灰**

4a. `ProviderListPage.tsx`：

- import 区追加 `import { usePlatformAdminCanEdit } from '@/modules/iam';`
- 组件首行（第 52 行 `const { ... } = useProviders();` 后）追加 `const canEdit = usePlatformAdminCanEdit();`
- 第 270 行「添加厂商」Button → `disabled={!canEdit}`
- 第 282-288 行「添加第一个厂商」Button → `disabled={!canEdit}`
- 行操作列内：第 223 行「添加模型」、第 232 行「发现模型」、第 239 行「健康检查」、第 243 行「编辑」、第 250 行「删除」五个 Button → 各加 `disabled={!canEdit}`
- 「编辑」「删除」是无文本图标按钮（Tooltip title 不是按钮 accessible name），补 `aria-label="编辑"` / `aria-label="删除"`——Step 1 测试的 `name: /编辑/` `/删除/` 断言依赖它，同时提升无障碍。
- 「刷新」Button（第 267 行）不加 disabled。

4b. `ModelListPage.tsx`：

- import 区追加 `import { usePlatformAdminCanEdit } from '@/modules/iam';`
- 第 44 行 `const { ... } = useModels();` 后追加 `const canEdit = usePlatformAdminCanEdit();`
- 第 163-167 行启停 `<Switch ...>` → `disabled={!canEdit}`
- 第 176 行「编辑」、第 179-184 行「删除」Button → `disabled={!canEdit}`
- 第 179-184 行「删除」是无文本图标按钮，补 `aria-label="删除"`——Step 1 测试的 `name: /删除/` 断言依赖它（「编辑」是文本按钮，无需 aria-label）。
- 能力筛选 Select（195-202 行）、刷新（203 行）不加 disabled。

- [ ] **Step 5: 运行测试确认通过**

Run: `npx vitest run src/modules/llm/pages/__tests__/ProviderListPageReadonly.test.tsx src/modules/llm/pages/__tests__/ModelListPageReadonly.test.tsx`
Expected: PASS。

- [ ] **Step 6: 提交**

```bash
git add web/src/modules/llm/pages/ProviderListPage.tsx web/src/modules/llm/pages/ModelListPage.tsx web/src/modules/llm/pages/__tests__/ProviderListPageReadonly.test.tsx web/src/modules/llm/pages/__tests__/ModelListPageReadonly.test.tsx
git commit -m "feat(llm): gray out model management write controls for readonly members"
```

---

### Task 5: 前端——租户页 / 管理员页置灰

**Files:**

- Modify: `web/src/modules/iam/pages/admin/TenantsListPage.tsx`
- Modify: `web/src/modules/iam/pages/admin/AdminsPage.tsx`
- Modify: `web/src/modules/iam/pages/admin/__tests__/TenantsListPage.test.tsx`（追加 member 用例）
- Modify: `web/src/modules/iam/pages/admin/__tests__/AdminsPage.test.tsx`（mock 改为 hoisted authState + 追加 member 用例）

**Interfaces:**

- Consumes: `usePlatformAdminCanEdit`（`@/modules/iam`）。
- Produces: 租户页「创建租户」与行内启停置灰（删除保留既有 `isGlobalAdmin` 限制）；管理员页「添加管理员」与行内移除置灰（保留既有 `isSuperAdmin` 保护）。

- [ ] **Step 1: 写失败测试（Tenants 页 member 用例）**

在 `web/src/modules/iam/pages/admin/__tests__/TenantsListPage.test.tsx` 末尾追加（文件已 mock `useAuth` → `authState`，`useResponsive` isMobile，`tenantApi`）：

```tsx
it('disables all write controls for a plain member (readonly view)', async () => {
  authState.user = { global_role: 'user' };
  api.listAllTenants.mockResolvedValue({
    tenants: [{
      id: 'tenant-2',
      name: '产品团队',
      slug: 'product',
      status: 'active',
      member_count: 8,
      created_at: '2026-07-10T00:00:00Z',
      is_default: false,
    }],
    total: 1,
    page: 1,
    page_size: 20,
  });
  render(
    <PlatformAdminGate minRole="system_admin">
      <TenantsListPage />
    </PlatformAdminGate>,
  );

  expect(await screen.findByText('产品团队')).toBeInTheDocument();
  // 只读提示条 + 写控件置灰；member 对启停/删除均无权
  expect(screen.getByText('只读模式')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '创建租户' })).toBeDisabled();
  expect(screen.getByRole('button', { name: /禁\s*用/ })).toBeDisabled();
  expect(screen.getByRole('button', { name: '删除租户' })).toBeDisabled();
});
```

同时文件头 import 追加（第 4 行后）：

```tsx
import { PlatformAdminGate } from '@/modules/iam';
```

- [ ] **Step 2: 写失败测试（Admins 页 member 用例）**

在 `web/src/modules/iam/pages/admin/__tests__/AdminsPage.test.tsx` 中：

- 把第 14 行 `vi.mock('@/modules/iam/components/AuthContext', () => ({ useAuth: () => ({}) }));` 改为 hoisted 可变状态：

```tsx
const authState = vi.hoisted(() => ({ user: { global_role: 'global_admin' } }));
vi.mock('@/modules/iam/components/AuthContext', () => ({ useAuth: () => authState }));
```

- 文件头追加 `import { PlatformAdminGate } from '@/modules/iam';`
- 末尾追加：

```tsx
it('disables add/remove controls for a plain member (readonly view)', async () => {
  authState.user = { global_role: 'user' };
  api.listAdmins.mockResolvedValue([
    { user_id: 'u-admin', username: '林管理员', github_login: 'linadmin', avatar_url: '', global_role: 'system_admin' },
  ]);
  render(
    <PlatformAdminGate minRole="global_admin">
      <AdminsPage />
    </PlatformAdminGate>,
  );

  expect(await screen.findByText('林管理员')).toBeInTheDocument();
  expect(screen.getByText('只读模式')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: '添加平台管理员' })).toBeDisabled();
  expect(screen.getByRole('button', { name: /移\s*除/ })).toBeDisabled();
});
```

- [ ] **Step 3: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-platform-admin-readonly/web && npx vitest run src/modules/iam/pages/admin/__tests__/TenantsListPage.test.tsx src/modules/iam/pages/admin/__tests__/AdminsPage.test.tsx`
Expected: 新增用例 FAIL（`usePlatformAdminCanEdit` 未被页面使用，控件未置灰）；既有用例仍 PASS（页面测试未包 Gate → 默认 `canEdit=true`）。

- [ ] **Step 4: 实现置灰**

4a. `TenantsListPage.tsx`：

- import 区追加 `import { PlatformAdminGate, usePlatformAdminCanEdit } from '@/modules/iam';`（若仅用到 hook，可只 `import { usePlatformAdminCanEdit } from '@/modules/iam';`）
- 组件内第 22 行 `const isGlobalAdmin = ...` 后追加 `const canEdit = usePlatformAdminCanEdit();`
- 第 182-189 行「创建租户」Button → `disabled={!canEdit}`
- 行内启停 Button（第 139 行 desktop、第 232 行 mobile）→ `disabled={!canEdit}`
- 删除按钮保持 `disabled={record.is_default || !isGlobalAdmin}`（member 因 `!isGlobalAdmin` 已禁用，无需改）。

4b. `AdminsPage.tsx`：

- import 区追加 `import { usePlatformAdminCanEdit } from '@/modules/iam';`
- 组件内追加 `const canEdit = usePlatformAdminCanEdit();`
- 第 134-141 行「添加管理员」Button → `disabled={!canEdit}`
- 行内移除：desktop（第 107/112 行）与 mobile（第 175/180 行）`DangerPopconfirm` 与 `Button` 的 `disabled={isSuperAdmin}` → `disabled={isSuperAdmin || !canEdit}`

- [ ] **Step 5: 运行测试确认通过**

Run: `npx vitest run src/modules/iam/pages/admin/__tests__/TenantsListPage.test.tsx src/modules/iam/pages/admin/__tests__/AdminsPage.test.tsx`
Expected: 全部 PASS（含新 member 用例与既有用例）。

- [ ] **Step 6: 提交**

```bash
git add web/src/modules/iam/pages/admin/TenantsListPage.tsx web/src/modules/iam/pages/admin/AdminsPage.tsx web/src/modules/iam/pages/admin/__tests__/TenantsListPage.test.tsx web/src/modules/iam/pages/admin/__tests__/AdminsPage.test.tsx
git commit -m "feat(iam): gray out tenant/admin page write controls for readonly members"
```

---

### Task 6: 前端——平台参数页置灰（PlatformSettingsPage + VersionHistory）

**Files:**

- Modify: `web/src/modules/parameters/pages/PlatformSettingsPage.tsx`
- Modify: `web/src/modules/parameters/components/VersionHistory.tsx`
- Modify: `web/src/modules/parameters/pages/__tests__/PlatformSettingsPage.test.tsx`（追加 useAuth mock + member 用例）

**Interfaces:**

- Consumes: `usePlatformAdminCanEdit`（`@/modules/iam`）。
- Produces: `VersionHistory` 新增可选 prop `disabled?: boolean`（发布/回滚按钮置灰）；平台参数 tab 表单 `<Form disabled={!canEdit}>` 级联置灰所有控件 + 保存按钮置灰。

- [ ] **Step 1: 写失败测试（member 只读）**

在 `web/src/modules/parameters/pages/__tests__/PlatformSettingsPage.test.tsx` 中：

- 文件头（第 1 行 import 后）追加：

```tsx
import { PlatformAdminGate } from '@/modules/iam';

const authState = vi.hoisted(() => ({ user: { global_role: 'user' } }));
vi.mock('@/modules/iam/components/AuthContext', () => ({ useAuth: () => authState }));
```

- `describe` 块末尾追加两个用例（复用文件内已有的 `defs`、`memoryDefs`、`version` 辅助）：

```tsx
  it('disables the whole platform-parameter form for a plain member', async () => {
    vi.mocked(parametersApi.schema).mockResolvedValue(defs());
    vi.mocked(parametersApi.list).mockResolvedValue({ 'memory.enrich_temperature': 0.9 });

    render(
      <PlatformAdminGate minRole="system_admin">
        <PlatformSettingsPage />
      </PlatformAdminGate>,
    );
    await screen.findByText('记忆丰富温度');

    // 只读提示条 + 表单级 disabled 级联（保存按钮与控件均置灰）
    expect(screen.getByText('只读模式')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: '保存记忆参数' })).toBeDisabled();
  });

  it('disables version publish/rollback for a plain member', async () => {
    vi.mocked(parametersApi.schema).mockResolvedValue(memoryDefs());
    vi.mocked(parametersApi.list).mockResolvedValue({ 'memory.enrich_temperature': 0.9 });
    vi.mocked(parametersApi.versions).mockResolvedValue([
      version(3, 3, 'draft', false, { 'memory.enrich_temperature': 0.7 }, 2, '草稿调温', 'admin-1'),
      version(2, 2, 'published', true, { 'memory.enrich_temperature': 0.9 }, 1, '调高温度', 'admin-1'),
      version(1, 1, 'published', false, { 'memory.enrich_temperature': 0.1 }, null, '初始化', 'system'),
    ]);

    render(
      <PlatformAdminGate minRole="system_admin">
        <PlatformSettingsPage />
      </PlatformAdminGate>,
    );
    await screen.findByText('版本历史（配置变更审计）');

    expect(screen.getByRole('button', { name: /发\s*布/ })).toBeDisabled();
    expect(screen.getByRole('button', { name: /回\s*滚/ })).toBeDisabled();
  });
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /home/yang/go-projects/stratum-platform-admin-readonly/web && npx vitest run src/modules/parameters/pages/__tests__/PlatformSettingsPage.test.tsx`
Expected: 新增用例 FAIL（表单/按钮未置灰）；既有用例仍 PASS（未包 Gate → `canEdit` 默认 true）。

- [ ] **Step 3: 实现 `VersionHistory.tsx` 的 `disabled` prop**

3a. 组件签名（第 68-78 行）改为：

```tsx
const VersionHistory = ({
  groupKey,
  labelMap,
  refreshTick,
  onEffectiveChange,
  disabled = false,
}: {
  groupKey: string;
  labelMap?: Record<string, string>;
  refreshTick?: number;
  onEffectiveChange?: (values: PlatformValues) => void;
  disabled?: boolean;
}) => {
```

3b. 「发布」Button（第 187-205 行）与「回滚」Button（第 210-234 行）各加 `disabled={disabled}`。

- [ ] **Step 4: 实现 `PlatformSettingsPage.tsx` 置灰**

4a. import 区追加 `import { usePlatformAdminCanEdit } from '@/modules/iam';`
4b. `PlatformTabPanel` 内（第 122 行 `const [form] = ...` 后）追加 `const canEdit = usePlatformAdminCanEdit();`
4c. 第 176 行 `<Form form={form} layout="vertical" onFinish={onFinish}>` → `<Form form={form} layout="vertical" onFinish={onFinish} disabled={!canEdit}>`
4d. 第 185 行保存 `<Button type="primary" htmlType="submit" loading={saving}>` → `<Button type="primary" htmlType="submit" loading={saving} disabled={!canEdit}>`
4e. 第 191-196 行 `<VersionHistory ... />` 传入 `disabled={!canEdit}`：

```tsx
      {groupKey ? (
        <VersionHistory
          groupKey={groupKey}
          labelMap={labelMap}
          refreshTick={refreshTick}
          onEffectiveChange={onEffectiveChange}
          disabled={!canEdit}
        />
      ) : null}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `npx vitest run src/modules/parameters/pages/__tests__/PlatformSettingsPage.test.tsx src/modules/parameters/components/__tests__/ParameterControl.test.tsx`
Expected: 全部 PASS（含既有保存/发布/回滚用例——未包 Gate 时 `canEdit=true`）。

- [ ] **Step 6: 提交**

```bash
git add web/src/modules/parameters/pages/PlatformSettingsPage.tsx web/src/modules/parameters/components/VersionHistory.tsx web/src/modules/parameters/pages/__tests__/PlatformSettingsPage.test.tsx
git commit -m "feat(parameters): gray out platform parameter write controls for readonly members"
```

---

### Task 7: 全量验证（R3：IAM/授权链路）

**Files:** 无新增代码。

- [ ] **Step 1: 运行风险回归守卫**

Run: `cd /home/yang/go-projects/stratum-platform-admin-readonly && bash scripts/quality/risk-regression-guard.sh --explain`
Expected: 打印七条原则说明；无阻断（本改动命中「授权 fail-closed」，见 Task 1 测试）。

- [ ] **Step 2: 后端全量快速验证**

Run: `go vet ./... && go test -short ./...`
Expected: PASS（含新 RBAC 测试、contract golden、handler 测试）。

- [ ] **Step 3: 前端 lint + 构建 + 全量单测**

Run: `make fe-lint && make fe-build && cd web && npx vitest run`
Expected: PASS。

- [ ] **Step 4: 系统验收（委托 stratum-e2e-tester）**

本改动属 IAM/授权链路（R3）→ 用 `stratum-e2e-tester` 执行 `stratum-e2e-development` skill 系统验收：按 `.test/verification.yaml` 选择 R2→e2e-short、R3→+e2e-soak；验证 member 可读 5 个平台管理页、写控件置灰、后端写接口 403、管理员编辑不受影响。

- [ ] **Step 5: 风险核查清单**

逐项确认：

1. 后端写接口全部留在 `adminGroup`（system_admin/global_admin），member 访问返回 403（fail-closed）——Task 1 测试守护。
2. Bearer 凭证未进入 URL/Web Storage/日志——未引入相关改动。
3. tenant-scoped 操作显式携带 tenantID——本次只移动路由分组，repository/port 未改。
4. 无不可逆清理/破坏性操作。
5. 持久化失败传播——未改 application/infrastructure 逻辑。
6. 连接/client/worker 替换——不涉及。
7. 认证/租户/迁移/消息/向量库失败路径——路由中间件组合与模型目录既有模式一致（`protectedTenantMiddleware(c, RequireTenantRole("member"))`），非新链路。

- [ ] **Step 6: PR 收尾**

在 clean commit 上创建 PR：`git push -u origin feat/platform-admin-readonly && gh pr create --base main`，PR 描述含 What/Why/HowToTest。push 后先检查 base 是否落后 `origin/main`，落后则合入 main 再等待 CI。

---

## Self-Review

**1. Spec coverage（对照 `docs/superpowers/specs/2026-08-31-platform-admin-readonly-design.md`）:**

- §2 后端只读分组 + 写接口保留 → Task 1。
- §2 敏感字段最小化核对 → Task 1 Step 5（验证步骤，DTO 已最小化，无需代码改动）。
- §3.1 去 requiredRole（4 个路由文件）→ Task 3。
- §3.2 `PlatformAdminGate` 组件 + `usePlatformAdminCanEdit` + 导出 → Task 2。
- §3.3 逐页置灰（模型管理 / 租户 / 参数 / 管理员；审计不加 Gate）→ Task 4/5/6。
- §4 后端源码级 RBAC 测试 + 前端 Gate/页面/路由测试 → Task 1/2/3/4/5/6。
- §4 契约与 E2E（R3）→ Task 7；契约 golden 用 global_admin token，不受影响（已核实 `contract_test.go:217-225`）。

**2. Placeholder scan:** 全部步骤含精确路径与完整代码/命令；无 TBD/TODO。「提示：/编辑/ 可能命中多处」是测试断言提示，非占位。

**3. Type consistency:**

- `PlatformAdminGate` 的 `minRole: 'system_admin' | 'global_admin'` 在 Task 2 定义、Task 3 各路由使用一致。
- `usePlatformAdminCanEdit(): boolean` 在 Task 2 定义，Task 4/5/6 各页面 `const canEdit = usePlatformAdminCanEdit();` 一致。
- `registerParameterReadRoutes(readGroup *gin.RouterGroup, c *wiring.Container)` / `registerParameterWriteRoutes(adminGroup *gin.RouterGroup, c *wiring.Container)` 在 Task 1 定义，router.go 调用一致，测试断言字符串与实现一致。
- `VersionHistory` 新 prop `disabled?: boolean` 在 Task 6 定义与使用一致。
- 测试 mock 的 hook 返回形状（`useProviders`/`useModels`）与页面解构字段一致。
- 默认 `canEdit=true` 使既有页面测试（未包 Gate）与新增 member 用例（包 Gate）语义自洽，已在 Global Constraints 说明理由。

**已知取舍（写入 PR 说明）：** 前端 `usePlatformAdminCanEdit` 默认 `true`（向后兼容、行为同改造前）；真正的授权强制在后端中间件（fail-closed），前端仅 UX 层。路由级源码测试（Task 3）保证生产路由必包 Gate，防止该默认值成为生产缺口。
