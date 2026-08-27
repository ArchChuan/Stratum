# 平台管理员管理（Platform Admin）设计

日期：2026-08-27
状态：设计（已获用户确认，待 review）

## 1. 背景与目标

### 现状问题

用户需要一个平台界面来**查看、添加、移除平台管理员**。当前平台管理员只有两个渠道，全在后端配置里，无任何 UI：

- `GlobalAdmin` 环境变量：GitHub 登录时自动把匹配的 `github_login` 标记为 `global_admin`。
- `EnsureAdminUser` seed（`internal/iam/infrastructure/persistence/admin_seed.go`）：启动时按配置的 username/password 幂等创建本地 `global_admin`。

改动一个管理员要动配置、重启服务，且无法在界面上查看当前有哪些管理员。

### 目标

提供「平台管理员」管理页面：**超级管理员**可在 UI 中查看当前全部平台管理员、搜索并提升**普通平台管理员**、移除普通平台管理员。

### 非目标（YAGNI）

- 不新建表（方案 A：扩展 `users.global_role`）。
- **UI 不开放产生/移除/降级超级管理员**——`global_admin` 仅程序预设（环境变量 + seed），维持现状。
- 不做档位切换（普通管理员不可在 UI 升级为超级管理员）。
- 不重写死代码 `SystemRole`（默认租户推导）体系，仅在守卫/前端判定迁移中移除对它的依赖。
- 不改 `/admin/providers`、`/admin/models`（LLM 配置，读写守卫独立，超出本次范围）。

## 2. 核心概念

**平台管理员 = 与租户角色完全脱钩的独立概念**（「租户一个体系，平台一个体系」）：

- 租户体系：`tenant_members.role`（member / admin / owner），前端 `useTenantRole` 读取。
- 平台体系：`users.global_role`（user / system_admin / global_admin），本次扩展。

两套体系在数据、守卫、前端三个层面互不推导、彻底分开。

**档位**（按操作敏感度划分，互斥）：

| 档位 | 取值 | 权限 |
|---|---|---|
| 普通平台管理员 | `system_admin` | 常规后台操作（租户管理、参数管理、平台审计查看） |
| 超级管理员 | `global_admin` | 全部后台 + 管理管理员 + 删除租户 |
| 普通用户 | `user` | 无后台权限 |

**超级管理员仅程序预设**：UI 只能新增/移除 `system_admin`；`global_admin` 只走环境变量 + seed 注入。最高权限身份不由 UI 产生，保证平台永远存在可信管理员入口，杜绝「最后一个超级管理员被误删」锁死。

## 3. 数据模型

**无 DDL 变更**。`pkg/migration/sql/001_public_schema.up.sql` 已定义 `users.global_role TEXT NOT NULL DEFAULT 'user'`，直接支持 `system_admin` 取值。

新增领域类型 `GlobalRole`（`internal/iam/domain/global_role.go`）：

```go
type GlobalRole string

const (
    GlobalRoleUser        GlobalRole = "user"
    GlobalRoleSystemAdmin GlobalRole = "system_admin"
    GlobalRoleGlobalAdmin GlobalRole = "global_admin"
)
```

- `Valid() bool`：仅接受上述三档。
- `AtLeast(min GlobalRole) bool`：rank `global_admin=3 > system_admin=2 > user=1`（复用 `SystemRole.AtLeast` 的排名逻辑，`internal/iam/domain/system_role.go`）。

## 4. 后端改动

### 4.1 守卫中间件（`api/middleware/require_role.go`）

- 新增 `RequirePlatformAdmin(minRole domain.GlobalRole)`：从 `ContextKeyGlobalRole` 读取角色，缺失或 `role < minRole` 时返回 403（fail closed）。
- `RequireGlobalAdmin()` 内部委托 `RequirePlatformAdmin(GlobalRoleGlobalAdmin)`，对外行为不变。
- 新增 `RequireSystemAdmin()` = `RequirePlatformAdmin(GlobalRoleSystemAdmin)`。

### 4.2 路由守卫拆分（`api/http/router.go:278` adminGroup）

现状：整个 `/admin` 组由 `RequireGlobalAdmin` 单一守卫。按敏感度拆分：

| 路由 | 现有守卫 | 新守卫 |
|---|---|---|
| GET/POST `/tenants`、GET/PATCH `/tenants/:id` | global_admin | `≥ system_admin` |
| `registerParameterAdminRoutes`（schema/List/Update/versions） | global_admin | `≥ system_admin` |
| `registerMemoryDLQAdminRoutes`（POST `/memory/dlq/replay`） | global_admin | `≥ system_admin` |
| DELETE `/tenants/:id`（删除租户） | global_admin | `global_admin`（不变） |
| 平台管理员组 `/admin/admins/*`（新增） | — | `global_admin` |
| `/admin/audit/platform`（`router.go:642` 独立分组） | `RequireGlobalAdmin` | `≥ system_admin`（平台审计对普通管理员可见） |

### 4.3 端口与仓储

**两条角色写入渠道物理分离**（关键约束）：

- **预设渠道（保持不变）**：现有 `OnboardRepo.SetGlobalRole(ctx, userID, role string)`（`onboard_repo.go:227`）继续服务 `global_admin` 注入——`auth_register_handler.go:76`、`auth_oauth_handler.go:97,141` 均在用它设置 `"global_admin"`（环境变量渠道），**不得收窄或改动**。
- **UI 管理渠道（新增）**：`AdminUserRepo`，方法名直接表达语义，**签名层面不接受档位参数**——杜绝"接受 role 参数却只允许 system_admin"的运行时校验设计。

新增 `AdminUserRepo`（`internal/iam/domain/port/admin_user_repo.go`），用户表是 **public schema 公共表**，不经过 `execTenant`：

```go
type AdminUser struct {
    UserID      string
    Username    string
    GitHubLogin string
    AvatarURL   string
    GlobalRole  domain.GlobalRole
}

type AdminUserRepo interface {
    SearchUsers(ctx context.Context, query string, limit int) ([]AdminUser, error) // 排除 guest
    ListAdmins(ctx context.Context) ([]AdminUser, error)                           // global_role ≠ 'user'
    SetAdminRole(ctx context.Context, userID string) error  // 提升为 system_admin（语义固定，不接受其他档位）
    RemoveAdminRole(ctx context.Context, userID string) error // 降回 user
    GetGlobalRole(ctx context.Context, userID string) (domain.GlobalRole, error)
}
```

- 实现 `internal/iam/infrastructure/persistence/admin_user_repo.go`：schema-qualified 访问 public 表，复用 `OnboardRepo` 的 SQL 访问方式（`GetGlobalRole` 可委托同源实现）。
- 事务内完成角色更新；持久化失败向上传播。

### 4.4 应用服务（`internal/iam/application/admin_service.go`）

`AdminService` 新增方法：

- `SearchUsers(ctx, query, page, pageSize)`：搜索非 guest、非已注册管理员用户，返回用户 id/username/github_login/avatar。
- `ListAdmins(ctx)`：返回全部平台管理员（含档位标签），供列表页展示。
- `SetAdminRole(ctx, actor, userID)`：
  - 校验 `actor` 为 `global_admin`（守卫兜底 + service 内二次校验）。
  - 校验目标非 guest、目标当前非 `global_admin`。
  - 调用 `AdminUserRepo.SetAdminRole`（**固定写 `system_admin`**，不传档位）。
- `RemoveAdminRole(ctx, actor, userID)`：校验 `actor` 为 `global_admin`；目标非 `global_admin`（**禁止动任何超级管理员，含自己**）。

### 4.5 Handler 与路由（`api/http/handler/admin_handler.go`）

`AdminHandler` 扩展 4 个端点，DTO 结构 + binding，handler 只做 bind/取 tenant/service/render 并交统一错误中间件：

- `GET /admin/users?q=&page=&page_size=` — 用户搜索
- `GET /admin/admins` — 平台管理员列表
- `POST /admin/admins` `{ user_id }` — 提升为普通平台管理员
- `DELETE /admin/admins/:user_id` — 移除普通平台管理员

### 4.6 权限生效机制

**无需改动 Refresh**。`api/http/handler/auth_session_handler.go:67-72` 的 `Refresh` 已在签发新 access token 前重新查询 `users.global_role` 写入 `GlobalRole` claims——权限变更后，用户下一次 refresh 即携带最新角色（「刷新即生效」天然成立）。

## 5. 前端改动

### 5.1 页面与路由

- 新增 `web/src/modules/iam/pages/admin/AdminsPage.tsx`：
  - **列表**：全部平台管理员（用户名、github_login、档位标签），`global_admin` 行**只读展示**（无档位切换、无移除按钮）。
  - **添加**：搜索非 guest 用户 → `Modal.confirm` 二次确认 → 提升为普通平台管理员（不选档位，固定 `system_admin`）。
  - **移除**：`system_admin` 行「移除」按钮，`Modal.confirm` 描述后果。
  - 空状态/搜索无结果按 `product.md` 规范处理。
- 路由 `/admin/admins`，`PrivateRoute requiredRole="global_admin"`（仅超级管理员可进入）。

### 5.2 权限判定迁移

- 新增 `usePlatformRole` hook（`web/src/modules/iam/hooks/usePlatformRole.ts`）：读 `user.global_role`，提供 `isGlobalAdmin` / `isSystemAdmin` / `hasPlatformRole(min)`。
- `PrivateRoute`（`web/src/modules/iam/components/PrivateRoute.tsx:50-58`）：现有 `system_admin` 判定依赖死代码 `user.system_role`（`DeriveSystemRole` 默认租户推导），**迁移为读 `user.global_role`**；`global_admin` 判定已读 `user.global_role`，不变。

### 5.3 API

`web/src/modules/iam/api/` 新增平台管理员 API（`searchUsers` / `listAdmins` / `setAdminRole` / `removeAdminRole`），统一走 `services/client.ts`，错误通知按前端规范。

## 6. 安全边界

1. **禁止动任何 `global_admin`（含自己）**：前端 `global_admin` 行置灰无操作 + 后端 `SetAdminRole`/`RemoveAdminRole` 校验拒绝，双层拦截。
2. **`SetAdminRole` 固定提升为 `system_admin`**：签名与实现均不接受其他档位，超级管理员不可由 UI 产生（`global_admin` 仅预设渠道注入）。
3. **排除 guest**：用户搜索与提升均排除 guest 用户。
4. **fail closed**：`RequirePlatformAdmin` 角色缺失/非法一律 403，禁止默认放行。
5. **持久化失败向上传播**：写库失败不得伪装成功。
6. 守卫在 service 层二次校验 actor 权限，不依赖单一中间件。

## 7. 测试计划

- **中间件矩阵**：三档（user/system_admin/global_admin）× 各敏感度路由（`≥ system_admin` 组 / `global_admin` 组 / 未登录 / 非法角色），验证 fail closed。
- **AdminUserRepo**：搜索过滤（guest 排除）、ListAdmins、Set/Get/Remove（含对 `global_admin` 目标拒绝）。
- **AdminService**：`SetAdminRole` 固定提升为 `system_admin`、拒绝目标为 `global_admin`/guest、拒绝非 `global_admin` 的 actor；`RemoveAdminRole` 拒绝 `global_admin`（含自己）；持久化失败传播。
- **契约测试**：4 个新端点 golden（`api/http/testdata/contracts/`）更新，`api/http/contract_test.go` 守护。
- **前端**：`AdminsPage` 渲染（超级只读、普通可操作、空状态）、`PrivateRoute` 平台角色判定迁移、`usePlatformRole`。
- 业务逻辑目标覆盖率 ≥80%，外部依赖 mock，完整套件 `-race`。

## 8. 待确认/后续

- `/admin/providers`、`/admin/models` 的读写守卫独立于本次范围，`system_admin` 是否可读后续单独评估（YAGNI，本次不动）。
- `system_role` 死代码（`DeriveSystemRole`）本次仅迁移前端判定，不删除；清理另立任务。
