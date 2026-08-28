# 平台管理员跨租户访问设计（Platform Admin Cross-Tenant Access）

日期：2026-08-28 · 状态：设计评审

## 1. 背景与目标

### 需求

平台管理员（`users.global_role = system_admin`）与平台超级管理员（`users.global_role = global_admin`）应能够：

1. **看到所有租户**：租户切换入口（Header 下拉，数据来自 `GET /tenant/list`）列出全部 active 租户，而不只是自己所属的租户。
2. **进入所有租户**：`POST /auth/switch-tenant` 允许平台管理员切换到任一 active 租户（目前非成员直接 403）。
3. **自动有租户管理员同等的权限**：进入任意租户后，在租户内所有受 `RequireTenantRole("admin")` 保护的 API 与内部角色校验中，按 `admin` 角色对待。

### 现状（已通过代码调用链确认）

两套相互独立的角色体系：

- **平台角色** `users.global_role`：`user` < `system_admin` < `global_admin`，守卫为 `middleware.RequirePlatformAdmin/RequireSystemAdmin/RequireGlobalAdmin`，前端 `usePlatformRole` 读取。
- **租户角色** `tenant_members.role`：`member` < `admin` < `owner`，守卫为 `middleware.RequireTenantRole`，前端 `useTenantRole` 读取。

跨租户访问存在三条障碍：

| 能力 | 障碍点 | 现状行为 |
|---|---|---|
| 看到所有租户 | `TenantHandler.ListUserTenants`（`GET /tenant/list`） | 仅返回 `tenant_members` JOIN 后的所属租户 |
| 进入所有租户 | `AuthHandler.SwitchTenant`（`POST /auth/switch-tenant`） | `OnboardSvc.IsMember` 非成员 → 403 |
| 自动有 admin 权限 | ① `SwitchTenant` 中 `GetTenantRole` 非成员 fallback `"member"`；② `RequireTenantRole` rank 表只认 member/admin/owner；③ `Refresh` 现查 `tenant_members` 非成员 → 401/503；④ `tenantRoleAdapter.ResolveTenantRole`（knowledge/mcp/skill/agent 统一注入）现查 `tenant_members` 非成员 → fail-closed 403 | 即使进入也只是 member 或无权限 |

### 已确认的决策（用户拍板）

1. 能力由 `system_admin` 与 `global_admin` **两者**共同拥有。
2. 进入后为**完整 admin 权限**：租户内所有资源读写管理 + 成员管理（邀请/移除/改角色）。
3. **不新增专项审计**（沿用现有审计机制）。
4. 前端入口：**Header 租户切换下拉直接列出全部租户**。

### 非目标（YAGNI）

- 不授予平台管理员 `owner` 级能力（不获得删除租户等 owner 专属操作）。
- 不改写 `tenant_members`，不把平台管理员变成真实成员（不污染成员列表/计数）。
- 不改动 `/tenant/list` 响应契约（`TenantListItem{TenantID, Name, IsDefault}`），无需改 proto。
- 不做"进入租户"的专项审计记录。

## 2. 核心语义：有效租户角色提升

定义**有效租户角色** `EffectiveTenantRole(realRole, globalRole) string`：

```
若 globalRole 不是平台管理员（非 system_admin/global_admin）：
    返回 realRole
否则（平台管理员）：
    rank(realRole) < rank(admin)  →  返回 "admin"   // 非成员 / member 提升
    rank(realRole) >= rank(admin) →  返回 realRole  // admin 保持，owner 保留
```

- 租户角色 rank：`member=1, admin=2, owner=3`。
- 平台管理员真实为 owner 时保留 owner（不降级）；真实为 admin/member/非成员时至少视为 admin。
- **不越权到 owner**：`RequireTenantRole("owner")` 保护的操作对平台管理员仍拒绝。

**两条覆盖路径必须同时满足**：

- **claims 路径**：middleware `RequireTenantRole` 读 JWT `auth.role`；`/auth/me` 也直接读 claims。只要 `SwitchTenant`/`Refresh` 签发提升后的 role，此路径即正确；`RequireTenantRole` 额外做平台角色放行作为纵深防御。
- **DB 现查路径**：`tenantRoleAdapter.ResolveTenantRole` 与 `Refresh` 现查 `tenant_members`，非成员 fail-closed。必须让统一角色解析单点对平台管理员返回 `admin`。

## 3. 共享 helper（`internal/iam/domain`）

新增两个领域级成员（纯函数/方法，无 IO）：

```go
// global_role.go 追加
// IsPlatformAdmin 报告 r 是否为平台管理员（system_admin 及以上）。
func (r GlobalRole) IsPlatformAdmin() bool { return r.AtLeast(GlobalRoleSystemAdmin) }

// 新增 tenant_role.go
const (
    RoleMember = "member"
    RoleAdmin  = "admin"
    RoleOwner  = "owner"
)

// EffectiveTenantRole 返回用户在某租户内的有效角色（见 §2）。
// realRole 允许为空字符串（表示非成员）。
func EffectiveTenantRole(realRole, globalRole string) string { ... }
```

## 4. 后端改动

### 4.1 `api/middleware/require_role.go` — `RequireTenantRole`

在 rank 比较前，读取 `auth.global_role`（`ctxGlobalRole`）：

```go
if gr, ok := c.Get(ctxGlobalRole); ok {
    if s, _ := gr.(string); iamdomain.GlobalRole(s).IsPlatformAdmin() {
        if rank[roleStr] < rank["admin"] {
            roleStr = "admin"
        }
    }
}
```

- 覆盖 claims 路径；即使 token 内 role 意外为 `member`，平台管理员仍通过 admin 级 API。
- `minRole = "owner"` 时提升后仍不足，平台管理员不被放行 owner 级操作。

### 4.2 `api/http/handler/auth_tenant_handler.go` — `SwitchTenant`

```go
isPlatformAdmin := domain.GlobalRole(claims.GlobalRole).IsPlatformAdmin()
if !isPlatformAdmin {
    isMember, err := h.deps.OnboardSvc.IsMember(ctx, claims.Sub, req.TenantID)
    // 原逻辑：非成员 → 403
} else {
    active, err := h.deps.OnboardSvc.TenantIsActive(ctx, req.TenantID)  // 新增
    if err != nil { 500 }
    if !active { 403 "tenant not active" }
}
tenantRole, err := h.deps.OnboardSvc.GetTenantRole(ctx, claims.Sub, req.TenantID)
if err != nil { tenantRole = "" }  // 非成员（ErrMemberNotFound）
tenantRole = domain.EffectiveTenantRole(tenantRole, claims.GlobalRole)
```

- 平台管理员跳过 `IsMember`，改为 `TenantIsActive` 校验（防止进入不存在/停用租户）。
- 签发 role 一律过 `EffectiveTenantRole`。

### 4.3 `api/http/handler/auth_session_handler.go` — `Refresh`

调整取 globalRole 顺序，并处理非成员分支：

```go
globalRole, _ := h.deps.MembershipReader.GetGlobalRole(ctx, storedClaims.UserID)  // 提前
tenantRole, err := h.deps.MembershipReader.GetTenantRole(ctx, storedClaims.UserID, storedClaims.TenantID)
switch {
case err == nil && tenantRole != "":
    tenantRole = domain.EffectiveTenantRole(tenantRole, globalRole)
case errors.Is(err, domain.ErrMemberNotFound):
    if !domain.GlobalRole(globalRole).IsPlatformAdmin() { 401 }  // 非平台管理员非成员维持原行为
    tenantRole = domain.EffectiveTenantRole("", globalRole)       // 平台管理员 → admin
case err != nil || tenantRole == "":
    503  // 现查失败，fail closed
}
```

- 平台管理员在非所属租户刷新时不再 401/503，继续以 admin 签发。

### 4.4 `api/http/handler/tenant_handler.go` — `ListUserTenants`

```go
gr, _ := c.Get("auth.global_role")
if s, _ := gr.(string); iamdomain.GlobalRole(s).IsPlatformAdmin() {
    tenants, err := h.svc.ListAllTenants(ctx)   // 所有 active 租户
    ...
}
tenants, err := h.svc.ListUserTenants(ctx, userIDStr)  // 原逻辑
```

- `auth.global_role` 由 JWT middleware 注入，与 `RequireTenantRole` 同源。

### 4.5 新增 repository 方法

- `internal/iam/domain/port/tenant_repo.go` + `TenantRepo` 实现（`tenant_repo.go`）：

```go
// ListAllTenants 返回所有 active 租户的 id/name/is_default（平台管理员租户列表）。
ListAllTenants(ctx context.Context) ([]domain.UserTenantInfo, error)
```

实现：`SELECT id, name, is_default FROM public.tenants WHERE deleted_at IS NULL ORDER BY is_default DESC, created_at ASC`（与 `ListUserTenants` 同风格）。

- `internal/iam/domain/port/onboard_repo.go` + `OnboardRepo` 实现：

```go
// TenantIsActive 报告租户是否存在、未删除且状态为 active。
TenantIsActive(ctx context.Context, tenantID string) (bool, error)
```

实现：`SELECT EXISTS(SELECT 1 FROM public.tenants WHERE id=$1 AND deleted_at IS NULL AND status='active')`。

- `internal/iam/application/tenant_service.go`：`ListAllTenants` 透传；`OnboardService`：`TenantIsActive` 透传。

### 4.6 `api/wiring/agent.go` — `tenantRoleAdapter` 统一角色解析单点

`tenantRoleAdapter.ResolveTenantRole` 被 knowledge/mcp/skill/agent 四个 context 统一注入（`injectTenantRoleResolvers`，agent.go:427-436），是 DB 现查路径的单点。

```go
type tenantRoleAdapter struct {
    service tenantMemberRoleService  // GetMemberRole（TenantService）
    global  tenantGlobalRoleReader   // GetGlobalRole（OnboardService）— 新增
}

func (a tenantRoleAdapter) ResolveTenantRole(ctx, tenantID, userID string) (string, error) {
    if a.service == nil { return "", domain.ErrDiagnosticForbidden }
    realRole, err := a.service.GetMemberRole(ctx, tenantID, userID)
    if err != nil && !errors.Is(err, domain.ErrMemberNotFound) { return "", err }
    if err != nil { realRole = "" }  // 非成员
    if realRole == "owner" || realRole == "admin" { return realRole, nil }  // 短路，免额外查询
    if a.global == nil { return realRole, nil }
    gr, gErr := a.global.GetGlobalRole(ctx, userID)
    if gErr != nil { return "", gErr }  // fail closed
    return domain.EffectiveTenantRole(realRole, gr), nil
}
```

新增构造 helper 并替换 4 处构造点（agent.go:408、428、513，system_assistant.go:43）：

```go
func newTenantRoleAdapter(c *Container) tenantRoleAdapter {
    return tenantRoleAdapter{
        service: tenantMemberService(c),
        global:  c.Platform.OnboardSvc, // OnboardSvc 满足 tenantGlobalRoleReader 接口（GetGlobalRole），无需额外 adapter
    }
}
```

`tenantGlobalRoleReader` 接口 = `{ GetGlobalRole(ctx, userID) (string, error) }`，`OnboardService` 天然满足；若其签名实际返回 `GlobalRole` 类型则接口对应调整为该返回类型。

- 性能：真实角色为 owner/admin 时短路，零额外查询；member/非成员才多一次 `users.global_role` 查询。

## 5. 前端改动

### 5.1 `web/src/modules/iam/hooks/useTenantRole.ts`

```ts
const PLATFORM_ROLE_RANK = { user: 1, system_admin: 2, global_admin: 3 };
const TENANT_ROLE_RANK = { member: 1, admin: 2, owner: 3 };

const role = user?.role ?? user?.current_tenant?.role ?? 'member';
const platformRank = PLATFORM_ROLE_RANK[user?.global_role ?? 'user'] ?? 0;
// 平台管理员（system_admin/global_admin）在任意租户内至少视为 admin。
const effectiveRole =
  platformRank >= PLATFORM_ROLE_RANK.system_admin &&
  TENANT_ROLE_RANK[role] < TENANT_ROLE_RANK.admin
    ? 'admin'
    : role;
```

`isAdmin`/`hasTenantRole` 基于 `effectiveRole` 计算。作为前端兜底（后端 `/auth/me` 返回的 `role` 已由 switch/refresh 提升，此处防御边界情况）。

### 5.2 `AppShell` 租户下拉

后端 `GET /tenant/list` 对平台管理员返回全部租户后，下拉自动列出全部，无需改交互逻辑。**可选增强**（不影响契约）：给非所属租户加小标签区分。作为可选项，默认不阻塞。

## 6. 安全边界与错误处理

- **fail closed**：`ResolveTenantRole` 中 `GetGlobalRole` 失败 → 返回错误，上层 403；`Refresh` 中现查失败 → 503；不允许默认放行。
- **不越权**：平台管理员不被放行 `RequireTenantRole("owner")`；`EffectiveTenantRole` 只提升到 admin 不提升到 owner。
- **不污染成员数据**：全程不改写 `tenant_members`；成员列表、成员计数、审计中的成员语义不变。
- **不可进入异常租户**：平台管理员 switch 进入前校验 `TenantIsActive`，不存在/停用/已删除租户拒绝。
- **token 生命周期**：`Refresh` 放行平台管理员，保证跨租户会话在 access token 过期后可续期。

## 7. 测试与验证

本改动为 **IAM 能力改动 → 完整测试门槛**（`make test-verify-before-pr`，按 `.test/verification.yaml` 风险分级）。

### 单元测试

- `EffectiveTenantRole` 表驱动：普通 user（member/admin/owner/非成员）；平台管理员（member→admin / 非成员→admin / admin→admin / owner→owner）。
- `api/middleware/require_role_test.go`：平台管理员 `auth.role=member` 通过 admin API；平台管理员不被放行 owner API；普通 user 行为不变。
- `tenantRoleAdapter` 四象限：非成员平台管理员→admin / member 平台管理员→admin / owner 平台管理员→owner / 普通 member→member；`GetGlobalRole` 失败 fail-closed。
- auth handler：`SwitchTenant` 平台管理员进入非所属 active 租户成功（role=admin）、进入停用/不存在租户被拒；`Refresh` 平台管理员非所属租户续期成功；`ListUserTenants` 平台管理员返回全部、普通用户仅所属。
- repository：`ListAllTenants`、`TenantIsActive`（含不存在/停用分支）。

### 契约测试

- `/tenant/list` 响应结构不变，contract test 回归。

### 前端

- `fe-lint` + `fe-build`；`useTenantRole` 平台管理员逻辑单测（如项目有 jest 环境）。

### 端到端系统验收（stratum-e2e-tester）

1. system_admin 登录 → Header 租户下拉显示所有租户（含非所属）。
2. 切换到非所属租户 → 租户内 admin 操作入口可见（新建/编辑资源、成员管理），且写操作成功。
3. 普通 member 回归：租户下拉仅所属、非 admin 入口不可见。
4. 平台管理员 `RequireTenantRole("owner")` 操作（如删除租户）仍被拒。

## 8. 部署与验收（推进到 CD 部署成功）

- 遵循 Git workflow：本 worktree（`feat/platform-admin-cross-tenant`，从最新 `origin/main` 创建）→ PR → CI 全绿 → 合并 → 本地 Docker E2E 验收 → 经 CD 流水线部署到远端，回归确认服务健康。
- 涉及权限、鉴权改动：合并前后执行命中的风险专项测试（`make risk-guardrails`）。
- 部署后：验证 `GET /tenant/list`、`POST /auth/switch-tenant`、租户内 admin 写路径在远端行为一致。

## 9. 影响面

| 层 | 文件 |
|---|---|
| domain | `internal/iam/domain/global_role.go`、新增 `tenant_role.go` |
| middleware | `api/middleware/require_role.go` |
| handler | `auth_tenant_handler.go`、`auth_session_handler.go`、`tenant_handler.go` |
| application | `tenant_service.go`、`onboard_service.go` |
| port/repo | `tenant_repo.go`（port+impl）、`onboard_repo.go`（port+impl） |
| wiring | `api/wiring/agent.go`、`api/wiring/system_assistant.go` |
| 前端 | `useTenantRole.ts`、`AppShell.tsx`（可选） |
| 测试 | 上述各文件的测试 + contract + e2e |

## 10. 待确认

- AppShell 非所属租户的可选视觉区分（小标签/分组）默认不实现，如需要作为增强跟进。
