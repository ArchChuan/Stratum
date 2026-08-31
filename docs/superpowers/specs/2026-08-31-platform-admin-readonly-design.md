# 平台管理页面只读可见设计（Platform Admin Read-Only View）

日期：2026-08-31 · 状态：设计评审

## 1. 背景与目标

### 需求

平台管理菜单（侧边栏「平台管理」分组）下 5 个页面，当前对非平台管理员（非 `system_admin`/`global_admin`）点击即渲染 403。需求改为：

1. **内容对所有人可见**：所有登录租户成员都能打开并看到页面数据（租户列表、平台参数、管理员名单、模型目录、平台审计日志）。
2. **所有人无编辑权限**：按钮、参数选择、输入框等写控件全部置灰（`disabled`），普通用户无法发起写操作。
3. **平台管理员保持编辑权限**：编辑能力仍按 `system_admin`/`global_admin` 分层，与现状一致。

### 现状（已通过代码调用链确认）

**页面与当前前端路由守卫**：

| 页面 | 路由 | 守卫 | 可编辑最低角色 |
|---|---|---|---|
| 模型管理 | `/models` | `llm/routes.tsx` `requiredRole="system_admin"` | system_admin |
| 全局租户 | `/admin/tenants` | `iam/routes.tsx` `requiredRole="system_admin"` | system_admin |
| 平台参数 | `/admin/settings` | `parameters/routes.tsx` `requiredRole="system_admin"` | system_admin |
| 平台管理员 | `/admin/admins` | `iam/routes.tsx` `requiredRole="global_admin"` | global_admin |
| 审计日志 | `/admin/audit` | `iam/routes.tsx` `requiredRole="system_admin"` | system_admin（页面本身只读） |

前端权限判定：`usePlatformRole()` 读 `users.global_role`，rank 为 `user=1 < system_admin=2 < global_admin=3`，与后端 `middleware.RequirePlatformAdmin` 一致。

**后端接口现状（`api/http/router.go`）**：

- `adminGroup := r.Group("/admin", jwtMW, RequireSystemAdmin())` 整体锁 system_admin，含 GET 与写接口。
- `/admin/admins` 子分组再叠加 `RequireGlobalAdmin()`。
- `/admin/audit/platform` 单独注册于 `registerAudit`，`JWTMiddleware + RequireSystemAdmin`。
- **例外**：`/admin/models`、`/admin/providers` 的 GET 已通过 `protectedTenantMiddleware(c, RequireTenantRole("member"))` 对所有租户成员开放；只有写操作要求 `RequireSystemAdmin`（即模型管理页数据普通成员本就可见）。

### 已确认的决策（用户拍板）

1. 后端只读接口对**所有登录租户成员**开放（GET 不再要求管理员角色）；写接口保持角色拦截。
2. 前端除控件置灰外，非管理员打开时页面顶部显示「只读模式」提示条。
3. 前端抽象采用**共享 Gate 组件**（`PlatformAdminGate`）+ 逐页控件 `disabled`。

### 非目标（YAGNI）

- 不改变平台管理员 / 租户管理员的既有编辑权限边界（编辑最低角色维持现状）。
- 不在后端读接口响应中增加"当前用户可写"标志（避免破坏契约，权限判定唯一来源仍是角色 rank）。
- 不做逐字段/逐资源的细粒度读权限，统一按页面分组开放。
- 审计日志页（`/admin/audit`）无写控件，只开放访问，不加只读提示条。

## 2. 后端：拆只读接口，开放给所有租户成员

### 改动文件

`api/http/router.go`（含 `registerParameterAdminRoutes`、`registerAudit`）。

### 路由重构

**新增公开只读分组**（与模型目录接口同一中间件组合）：

```go
platformRead := r.Group("/admin", protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
{
    platformRead.GET("/tenants", adminHandler.ListTenants)
    platformRead.GET("/tenants/:id", adminHandler.GetTenant)
    platformRead.GET("/parameters/schema", paramHandler.Schema)
    platformRead.GET("/parameters", paramHandler.List)
    platformRead.GET("/parameters/versions/:groupKey", paramHandler.Versions)
    platformRead.GET("/admins", adminHandler.ListAdmins)
}
```

**原管理员分组保留写接口**：

```go
adminGroup := r.Group("/admin", jwtMW, middleware.RequireSystemAdmin())
{
    adminGroup.POST("/tenants", adminHandler.CreateTenant)
    adminGroup.PATCH("/tenants/:id", adminHandler.UpdateTenant)
    adminGroup.DELETE("/tenants/:id", middleware.RequireGlobalAdmin(), adminHandler.DeleteTenant)

    adminGroup.PUT("/parameters", paramHandler.Update)
    adminGroup.POST("/parameters/versions/:groupKey", paramHandler.CreateDraft)
    adminGroup.POST("/parameters/versions/:groupKey/:versionID/publish", paramHandler.Publish)
    adminGroup.POST("/parameters/versions/:groupKey/:versionID/rollback", paramHandler.Rollback)

    adminAdmins := adminGroup.Group("/admins", middleware.RequireGlobalAdmin())
    adminAdmins.POST("", adminHandler.SetAdminRole)
    adminAdmins.DELETE("/:user_id", adminHandler.RemoveAdminRole)

    adminGroup.GET("/users", adminHandler.SearchUsers) // 保持 system_admin：仅添加管理员候选搜索用
    registerMemoryDLQAdminRoutes(adminGroup, c)
}
```

**平台审计（`registerAudit` 内）**：

```go
platformAudit := r.Group("/admin/audit/platform",
    protectedTenantMiddleware(c, middleware.RequireTenantRole("member"))...)
platformAudit.GET("/events", platformHandler.List)
platformAudit.GET("/events/:id", platformHandler.Get)
```

> ⚠️ Gin 对同一 method+path 重复注册会 panic，GET 路由必须**移动**而非复制。

### 数据暴露与敏感字段核对

开放后普通租户成员可读取跨租户数据：全平台租户列表、平台管理员名单、平台参数值。实现时逐项核对 handler 响应体：

- `ListTenants` / `GetTenant`：是否含 owner 联系方式、邀请码等 PII；若含则对非管理员仅在 DTO 层返回必要字段（租户 ID、名称、状态、创建时间）。
- `ListAdmins`：仅返回管理员 user 的 id、name、email、角色。
- `List`（参数）：确认敏感参数值已被掩码（与 `buildOperationPayloadSummary` 一致的掩码规则），未掩码的不外泄。

数据暴露属产品决策已接受范围，但敏感字段最小化是安全底线，纳入实现核对项。

## 3. 前端：去 403 守卫 + 共享只读 Gate

### 3.1 路由调整

5 个平台管理页面去掉 `PrivateRoute` 的 `requiredRole`，保留登录 + 当前租户校验（`PrivateRoute` 无参即只做 `user`/`current_tenant` 检查）：

- `web/src/modules/llm/routes.tsx`：`/models`
- `web/src/modules/iam/routes.tsx`：`/admin/tenants`、`/admin/admins`、`/admin/audit`
- `web/src/modules/parameters/routes.tsx`：`/admin/settings`

### 3.2 新增共享组件 `PlatformAdminGate`

文件：`web/src/modules/iam/components/PlatformAdminGate.tsx`（与 `PrivateRoute` 同目录，经 `@/modules/iam` 导出）。

```tsx
interface PlatformAdminGateProps {
  /** 可编辑所需的最低平台角色：'system_admin' | 'global_admin' */
  minRole: 'system_admin' | 'global_admin';
  children: ReactNode;
}
```

行为：

- 用现有 `usePlatformRole().hasPlatformRole(minRole)` 计算 `canEdit`。
- `canEdit === false` 时，在 children 顶部渲染 `<Alert type="info" showIcon>`：「只读模式」「您当前为只读模式，仅平台管理员可编辑本页内容」。
- 通过 `PlatformAdminContext` 下发 `canEdit`；页面内用 `usePlatformAdminCanEdit()` 读取。
- 配套 hook 定义于同文件：`usePlatformAdminCanEdit(): boolean`。

### 3.3 逐页接入与置灰

| 页面 | Gate minRole | 置灰控件（`disabled={!canEdit}`） |
|---|---|---|
| 模型管理 `/models` | `system_admin` | `ProviderListPage`：新增/编辑/删除厂商、发现模型、健康检查；`ModelListPage`：新增/编辑/删除模型、启停 |
| 全局租户 `/admin/tenants` | `system_admin` | 创建租户、行内编辑；删除保持既有 `!isGlobalAdmin` 限制（system_admin 也不可删） |
| 平台参数 `/admin/settings` | `system_admin` | 内联参数表单用 `<Form disabled={!canEdit}>` 级联禁用所有编辑控件；保存草稿、发布、回滚按钮 `disabled={!canEdit}` |
| 平台管理员 `/admin/admins` | `global_admin` | 添加管理员、行内移除（保留既有 `isSuperAdmin` 保护） |
| 审计日志 `/admin/audit` | —（不包裹） | 仅开放访问，无写控件 |

实现顺序：先接 `PlatformAdminGate` 包裹页面，再逐个写控件加 `disabled`，最后核对没有遗漏的写入口（含表格行内操作、抽屉/弹窗打开按钮）。

## 4. 测试与验证

### 后端

- 成员 token（`global_role=user`、租户 member）可 GET：`/admin/tenants`、`/admin/tenants/:id`、`/admin/parameters/schema`、`/admin/parameters`、`/admin/parameters/versions/:groupKey`、`/admin/admins`、`/admin/audit/platform/events`。
- 成员 token 对写接口仍 403：`POST /admin/tenants`、`PATCH /admin/tenants/:id`、`PUT /admin/parameters`、`POST /admin/admins`、`DELETE /admin/admins/:user_id`、`DELETE /admin/tenants/:id`。
- system_admin 可写租户/参数；global_admin 可管理管理员；`GET /admin/users` 仍 403（成员）。
- 更新既有断言非管理员 GET 403 的测试。

### 前端

- `PlatformAdminGate` 组件测试：无权限渲染提示条且 `usePlatformAdminCanEdit() === false`；有权限不渲染提示条且 `canEdit === true`。
- 各页测试：成员视角可进入页面（不渲染 403）、写控件 `disabled`；管理员视角控件可用。
- router 测试：成员访问 `/admin/tenants` 等不再渲染 403 `Result`。

### 契约与 E2E

- 核对 `api/http/testdata/contracts/*.golden.json` 是否断言这些 GET 的非管理员 403，一并更新。
- 本改动属 IAM/授权链路改动，按 `.test/verification.yaml` 走完整测试（R3：e2e-short，必要时升级），系统验收由 `stratum-e2e-tester` 执行。

## 5. 风险与边界

- **纵深防御**：后端写接口保持角色拦截（fail-closed 不变）；前端置灰只是体验层，即使前端判定 bug 也不会放行写操作。
- **跨租户数据可见**：租户列表/管理员名单/平台参数对全体成员可见，产品决策已确认，安全审计列入说明；敏感字段最小化核对见 §2。
- **Gin 路由冲突**：同 method+path 只能注册一次，GET 移动实现，避免 panic。
- **`GET /admin/users` 不开放**：仅添加管理员候选搜索使用，普通只读用户不需要。
- **唯一事实源**：前端 `usePlatformRole` 读 `users.global_role`，与后端中间件 rank 表一致。
