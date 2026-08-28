# 平台管理员跨租户访问 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 平台管理员（`system_admin`/`global_admin`）看到所有 active 租户、进入任一 active 租户、租户内自动视为 `admin`（不越权 `owner`），全程不改写 `tenant_members`。

**Architecture:** 引入「有效租户角色」语义 `EffectiveTenantRole(realRole, globalRole)`——平台管理员在任意租户内有效角色 = max(真实角色, admin)，owner 保留。双路径覆盖：claims 路径（`SwitchTenant`/`Refresh` 签发提升后的 role + `RequireTenantRole` 纵深防御）与 DB 现查路径（`tenantRoleAdapter` 统一角色解析单点对平台管理员返回 admin）。新增两个只读 repository 方法（`ListAllTenants`、`TenantIsActive`）支撑「看到全部租户」与「进入非所属租户」。

**Tech Stack:** Go 1.25（Gin v1.9、pgx v5）、React 18 + Ant Design 5 + Vitest、pgxmock/v2（impl 测试）、`api/http/contract_test.go` + golden。

## Global Constraints

- 有效角色语义：平台管理员（`IsPlatformAdmin()` = `GlobalRole.AtLeast(GlobalRoleSystemAdmin)`）租户内至少 `admin`，绝不隐式提升到 `owner`；真实 `owner` 保留。`RequireTenantRole("owner")` 对平台管理员仍拒绝。
- 禁止改写 `tenant_members`；成员列表、成员计数语义不变。
- **修改 port 后必须同步全部 mock/stub**：`port.TenantRepo` 的实现者有 `persistence.TenantRepo`、`fakeTenantRepo`（tenant_handler_test.go）、`settingsTenantRepo`（tenant_service_test.go）、`memberTenantRepo`（tenant_service_extra_test.go）、`contractTenantRepo`（api/http/contract_test.go）、`contractTenantR`（scripts/record-contracts.go）；`port.OnboardRepo` 的实现者有 `persistence.OnboardRepo`、`forwardingOnboardRepo`（onboard_service_extra_test.go）、`onboardRepoFake`（auth_handler_test.go，嵌入接口、需覆写被调方法）。
- Go 行宽 ≤120；import 分组 stdlib → third-party → internal，组间空行；错误逐层 `fmt.Errorf("op: %w", err)`。
- `/tenant/list` 响应契约不变（`TenantListItem{TenantID,Name,IsDefault}`），不改 proto、不重跑 `make proto-gen`。
- `api/middleware/require_active_tenant.go` 只查 `public.tenants.status` 不查成员身份，平台管理员进入非所属 active 租户自动放行，**无需修改**。
- `auth.role`/`auth.global_role` 由 `JWTMiddleware` 从 JWT claims 注入（`api/middleware/jwt.go`），`auth.tenant_id` 同理。
- `domain.ErrDiagnosticForbidden` 在 `internal/agent/domain`（agent 包），`tenantRoleAdapter.ResolveTenantRole` 沿用该 sentinel（`api/wiring/system_assistant.go` 的 `domain` import 即 agent domain）。
- 每 task 末尾必须 commit（`[type](scope): description`）。

---

### Task 1: iam domain 角色提升 helper

**Files:**

- Modify: `internal/iam/domain/global_role.go`
- Create: `internal/iam/domain/tenant_role.go`
- Test: Create: `internal/iam/domain/tenant_role_test.go`

**Interfaces:**

- Produces: `func (r GlobalRole) IsPlatformAdmin() bool`；`const RoleMember/RoleAdmin/RoleOwner`；`func EffectiveTenantRole(realRole, globalRole string) string`（Task 2、5、6、8、9 依赖）。

- [ ] **Step 1: 写失败测试**

Create `internal/iam/domain/tenant_role_test.go`:

```go
package domain

import "testing"

func TestEffectiveTenantRole(t *testing.T) {
 tests := []struct {
  name       string
  realRole   string
  globalRole string
  want       string
 }{
  {"user member unchanged", "member", "user", "member"},
  {"user admin unchanged", "admin", "user", "admin"},
  {"user owner unchanged", "owner", "user", "owner"},
  {"user non-member unchanged", "", "user", ""},
  {"system admin member elevated", "member", "system_admin", "admin"},
  {"system admin non-member elevated", "", "system_admin", "admin"},
  {"system admin admin kept", "admin", "system_admin", "admin"},
  {"system admin owner kept", "owner", "system_admin", "owner"},
  {"global admin non-member elevated", "", "global_admin", "admin"},
  {"global admin owner kept", "owner", "global_admin", "owner"},
 }
 for _, tt := range tests {
  t.Run(tt.name, func(t *testing.T) {
   if got := EffectiveTenantRole(tt.realRole, tt.globalRole); got != tt.want {
    t.Fatalf("EffectiveTenantRole(%q, %q) = %q, want %q", tt.realRole, tt.globalRole, got, tt.want)
   }
  })
 }
}

func TestGlobalRoleIsPlatformAdmin(t *testing.T) {
 tests := []struct {
  role GlobalRole
  want bool
 }{
  {GlobalRoleUser, false},
  {GlobalRoleSystemAdmin, true},
  {GlobalRoleGlobalAdmin, true},
  {GlobalRole("garbage"), false},
 }
 for _, tt := range tests {
  if got := tt.role.IsPlatformAdmin(); got != tt.want {
   t.Fatalf("GlobalRole(%q).IsPlatformAdmin() = %v, want %v", tt.role, got, tt.want)
  }
 }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./internal/iam/domain/ -run 'TestEffectiveTenantRole|TestGlobalRoleIsPlatformAdmin'`
Expected: FAIL，`EffectiveTenantRole` undefined。

- [ ] **Step 3: 最小实现**

Append to `internal/iam/domain/global_role.go`:

```go
// IsPlatformAdmin reports whether r is a platform administrator
// (system_admin or above). Guards that elevate platform admins to tenant
// admins key off this predicate.
func (r GlobalRole) IsPlatformAdmin() bool {
 return r.AtLeast(GlobalRoleSystemAdmin)
}
```

Create `internal/iam/domain/tenant_role.go`:

```go
package domain

// Tenant role constants mirroring tenant_members.role values. Centralized so
// role promotion (EffectiveTenantRole) and guards agree on ranks.
const (
 RoleMember = "member"
 RoleAdmin  = "admin"
 RoleOwner  = "owner"
)

var tenantRoleRank = map[string]int{RoleMember: 1, RoleAdmin: 2, RoleOwner: 3}

// EffectiveTenantRole returns the user's effective role inside a tenant.
// Platform admins (system_admin/global_admin) are treated as at least "admin"
// in every tenant — including tenants they are not a member of — without ever
// being promoted above their real role past admin. "owner" is never granted
// implicitly. realRole may be empty (non-member).
func EffectiveTenantRole(realRole, globalRole string) string {
 if !GlobalRole(globalRole).IsPlatformAdmin() {
  return realRole
 }
 if tenantRoleRank[realRole] >= tenantRoleRank[RoleAdmin] {
  return realRole
 }
 return RoleAdmin
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./internal/iam/domain/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/iam/domain/global_role.go internal/iam/domain/tenant_role.go internal/iam/domain/tenant_role_test.go
git commit -m "feat(iam): add EffectiveTenantRole and IsPlatformAdmin predicate"
```

---

### Task 2: `RequireTenantRole` 平台管理员纵深防御

**Files:**

- Modify: `api/middleware/require_role.go`
- Test: Modify: `api/middleware/require_role_test.go`

**Interfaces:**

- Consumes: `iamdomain.GlobalRole(s).IsPlatformAdmin()`（Task 1）。
- Produces: `RequireTenantRole` 对平台管理员（`auth.global_role` ∈ {system_admin, global_admin}）且 `auth.role` < admin 时按 admin 校验；`minRole="owner"` 不放行。

- [ ] **Step 1: 追加失败测试**

Append to `api/middleware/require_role_test.go`:

```go
func TestRequireTenantRole_platformAdminMemberElevated(t *testing.T) {
 // system_admin + stale member role must pass the admin guard.
 gin.SetMode(gin.TestMode)
 r := gin.New()
 r.GET("/", func(c *gin.Context) {
  c.Set("auth.global_role", "system_admin")
  c.Set("auth.role", "member")
 }, middleware.RequireTenantRole("admin"), func(c *gin.Context) { c.Status(http.StatusOK) })
 w := httptest.NewRecorder()
 req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
 r.ServeHTTP(w, req)
 if w.Code != http.StatusOK {
  t.Fatalf("expected 200, got %d", w.Code)
 }
}

func TestRequireTenantRole_platformAdminMemberDeniedOwner(t *testing.T) {
 // Platform admin must NOT be elevated to owner: owner guard still rejects.
 gin.SetMode(gin.TestMode)
 r := gin.New()
 r.GET("/", func(c *gin.Context) {
  c.Set("auth.global_role", "global_admin")
  c.Set("auth.role", "member")
 }, middleware.RequireTenantRole("owner"), func(c *gin.Context) { c.Status(http.StatusOK) })
 w := httptest.NewRecorder()
 req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
 r.ServeHTTP(w, req)
 if w.Code != http.StatusForbidden {
  t.Fatalf("expected 403, got %d", w.Code)
 }
}

func TestRequireTenantRole_nonAdminMemberStillDenied(t *testing.T) {
 // Regression: ordinary member unaffected by platform-admin elevation.
 gin.SetMode(gin.TestMode)
 r := gin.New()
 r.GET("/", func(c *gin.Context) {
  c.Set("auth.global_role", "user")
  c.Set("auth.role", "member")
 }, middleware.RequireTenantRole("admin"), func(c *gin.Context) { c.Status(http.StatusOK) })
 w := httptest.NewRecorder()
 req, _ := http.NewRequest(http.MethodGet, "/", nil) //nolint:noctx
 r.ServeHTTP(w, req)
 if w.Code != http.StatusForbidden {
  t.Fatalf("expected 403, got %d", w.Code)
 }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./api/middleware/ -run TestRequireTenantRole_platformAdmin -v`
Expected: FAIL（`expected 200, got 403`）。

- [ ] **Step 3: 实现**

In `api/middleware/require_role.go`, replace the body of the returned closure inside `RequireTenantRole`:

```go
 return func(c *gin.Context) {
  roleVal, _ := c.Get(ctxRole)
  roleStr, _ := roleVal.(string)
  // Platform admins are treated as at least "admin" in every tenant
  // (defense in depth: switch-tenant/refresh already sign an elevated
  // role, but a stale or downgraded token must not lock out a platform
  // admin). owner is never granted implicitly.
  if grVal, ok := c.Get(ctxGlobalRole); ok {
   if grStr, _ := grVal.(string); iamdomain.GlobalRole(grStr).IsPlatformAdmin() && rank[roleStr] < rank["admin"] {
    roleStr = "admin"
   }
  }
  if rank[roleStr] < required {
   c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
    "code":    http.StatusForbidden,
    "message": "insufficient tenant role",
   })
   return
  }
  c.Next()
 }
```

(`iamdomain` 已 import；`rank` map 已在函数外层定义，保留。)

- [ ] **Step 4: 跑测试确认通过**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./api/middleware/`
Expected: PASS（含既有用例）。

- [ ] **Step 5: Commit**

```bash
git add api/middleware/require_role.go api/middleware/require_role_test.go
git commit -m "feat(middleware): treat platform admins as tenant admin in RequireTenantRole"
```

---

### Task 3: `TenantRepo.ListAllTenants`（port + impl + service + mock 同步）

**Files:**

- Modify: `internal/iam/domain/port/tenant_repo.go`
- Modify: `internal/iam/infrastructure/persistence/tenant_repo.go`
- Modify: `internal/iam/application/tenant_service.go`
- Modify (mock 同步): `api/http/handler/tenant_handler_test.go`、`internal/iam/application/tenant_service_test.go`、`internal/iam/application/tenant_service_extra_test.go`、`api/http/contract_test.go`、`scripts/record-contracts.go`
- Test: Modify: `internal/iam/infrastructure/persistence/tenant_repo_internal_test.go`、`internal/iam/application/tenant_service_extra_test.go`

**Interfaces:**

- Consumes: `domain.UserTenantInfo{TenantID, Name, IsDefault}`（`internal/iam/domain/tenant.go`）。
- Produces: `TenantRepo.ListAllTenants(ctx) ([]domain.UserTenantInfo, error)`；`TenantService.ListAllTenants(ctx) ([]domain.UserTenantInfo, error)`（Task 7 依赖）。

- [ ] **Step 1: 写失败测试（impl + service）**

Append to `internal/iam/infrastructure/persistence/tenant_repo_internal_test.go`:

```go
func TestTenantRepo_ListAllTenants(t *testing.T) {
 repo, mock := newTenantRepo(t)
 mock.ExpectQuery(`SELECT id, name, is_default FROM public.tenants`).
  WillReturnRows(pgxmock.NewRows([]string{"id", "name", "is_default"}).
   AddRow("t1", "Alpha", true).
   AddRow("t2", "Beta", false))
 tenants, err := repo.ListAllTenants(context.Background())
 require.NoError(t, err)
 require.Len(t, tenants, 2)
 require.Equal(t, "t1", tenants[0].TenantID)
 require.True(t, tenants[0].IsDefault)
 require.Equal(t, "Beta", tenants[1].Name)
}

func TestTenantRepo_ListAllTenants_Error(t *testing.T) {
 repo, mock := newTenantRepo(t)
 mock.ExpectQuery(`SELECT id, name, is_default FROM public.tenants`).
  WillReturnError(errAny)
 _, err := repo.ListAllTenants(context.Background())
 require.ErrorContains(t, err, "list all tenants")
}
```

(`tenant_repo_internal_test.go` 用 require 风格 + `errAny` sentinel（`pgxmock_test.go:12` 定义），无 `errors` import；`newTenantRepo(t)`/`pgxmock`/`context` 已就绪。)

Append to `internal/iam/application/tenant_service_extra_test.go`:

```go
func TestTenantService_ListAllTenants(t *testing.T) {
 repo := &listAllTenantRepo{}
 svc := NewTenantService(repo, zap.NewNop())
 got, err := svc.ListAllTenants(context.Background())
 if err != nil {
  t.Fatalf("ListAllTenants: %v", err)
 }
 if len(got) != 1 || got[0].TenantID != "t-all" {
  t.Fatalf("ListAllTenants = %+v, want [t-all]", got)
 }
}

// listAllTenantRepo embeds the port interface so only ListAllTenants needs a
// concrete implementation for this test.
type listAllTenantRepo struct {
 port.TenantRepo
}

func (r *listAllTenantRepo) ListAllTenants(_ context.Context) ([]domain.UserTenantInfo, error) {
 return []domain.UserTenantInfo{{TenantID: "t-all", Name: "All"}}, nil
}
```

(`tenant_service_extra_test.go` 已 import `port`（line 12）与 `domain`，无需补 import。)

- [ ] **Step 2: 跑测试确认失败（编译错误即失败）**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./internal/iam/application/ ./internal/iam/infrastructure/persistence/`
Expected: 编译失败（`TenantService.ListAllTenants undefined` / 接口缺方法）。

- [ ] **Step 3: 实现 + 同步 mock**

Port — append to interface in `internal/iam/domain/port/tenant_repo.go`:

```go
 // ListUserTenants returns all active tenants the user belongs to.
 ListUserTenants(ctx context.Context, userID string) ([]domain.UserTenantInfo, error)
 // ListAllTenants returns every active tenant (id/name/is_default), used by
 // platform admins to enumerate all tenants for cross-tenant access.
 ListAllTenants(ctx context.Context) ([]domain.UserTenantInfo, error)
```

Impl — append to `internal/iam/infrastructure/persistence/tenant_repo.go`:

```go
// ListAllTenants returns every non-deleted tenant ordered default-first, for
// platform-admin tenant enumeration. Operates on the public schema only.
func (r *TenantRepo) ListAllTenants(ctx context.Context) ([]domain.UserTenantInfo, error) {
 rows, err := r.db.Query(ctx,
  `SELECT id, name, is_default FROM public.tenants
   WHERE deleted_at IS NULL
   ORDER BY is_default DESC, created_at ASC`)
 if err != nil {
  return nil, fmt.Errorf("tenant_repo: list all tenants: %w", err)
 }
 defer rows.Close()

 var tenants []domain.UserTenantInfo
 for rows.Next() {
  var t domain.UserTenantInfo
  if err := rows.Scan(&t.TenantID, &t.Name, &t.IsDefault); err != nil {
   return nil, fmt.Errorf("tenant_repo: scan all tenant: %w", err)
  }
  tenants = append(tenants, t)
 }
 if err := rows.Err(); err != nil {
  return nil, fmt.Errorf("tenant_repo: iterate all tenants: %w", err)
 }
 return tenants, nil
}
```

Service — append to `internal/iam/application/tenant_service.go`:

```go
// ListAllTenants returns every active tenant (platform admins only — the
// handler gates by auth.global_role).
func (s *TenantService) ListAllTenants(ctx context.Context) ([]domain.UserTenantInfo, error) {
 return s.repo.ListAllTenants(ctx)
}
```

Mock 同步（每个文件新增该方法，返回 nil/nil 或字段值）：

`api/http/handler/tenant_handler_test.go` — struct `fakeTenantRepo` 加字段 `allTenants []domain.UserTenantInfo`，并加方法：

```go
func (f *fakeTenantRepo) ListAllTenants(_ context.Context) ([]domain.UserTenantInfo, error) {
 return f.allTenants, nil
}
```

`internal/iam/application/tenant_service_test.go` — append to `settingsTenantRepo`:

```go
func (r *settingsTenantRepo) ListAllTenants(context.Context) ([]domain.UserTenantInfo, error) {
 return nil, nil
}
```

`internal/iam/application/tenant_service_extra_test.go` — `memberTenantRepo` 嵌入 `*settingsTenantRepo`，已由上一步在 `tenant_service_test.go` 为 `settingsTenantRepo` 新增 `ListAllTenants`，自动继承，无需显式方法。

`api/http/contract_test.go` — append to `contractTenantRepo`:

```go
func (contractTenantRepo) ListAllTenants(context.Context) ([]iamdomain.UserTenantInfo, error) {
 return nil, nil
}
```

`scripts/record-contracts.go` — append to `contractTenantR`:

```go
func (contractTenantR) ListAllTenants(context.Context) ([]iamdomain.UserTenantInfo, error) {
 return nil, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./internal/iam/... ./api/http/... && go build ./scripts/...`
Expected: PASS + build 成功。

- [ ] **Step 5: Commit**

```bash
git add internal/iam/domain/port/tenant_repo.go internal/iam/infrastructure/persistence/tenant_repo.go internal/iam/application/tenant_service.go internal/iam/application/tenant_service_test.go internal/iam/application/tenant_service_extra_test.go internal/iam/infrastructure/persistence/tenant_repo_internal_test.go api/http/handler/tenant_handler_test.go api/http/contract_test.go scripts/record-contracts.go
git commit -m "feat(iam): add TenantRepo.ListAllTenants for platform-admin tenant enumeration"
```

---

### Task 4: `OnboardRepo.TenantIsActive`（port + impl + service + mock 同步）

**Files:**

- Modify: `internal/iam/domain/port/onboard_repo.go`
- Modify: `internal/iam/infrastructure/persistence/onboard_repo.go`
- Modify: `internal/iam/application/onboard_service.go`
- Modify (mock 同步): `internal/iam/application/onboard_service_extra_test.go`（`forwardingOnboardRepo`）、`api/http/handler/auth_handler_test.go`（`onboardRepoFake` 覆写）
- Test: Modify: `internal/iam/infrastructure/persistence/onboard_repo_mock_test.go`、`internal/iam/application/onboard_service_extra_test.go`

**Interfaces:**

- Consumes: `pgxPool`（onboard_repo.go:19）。
- Produces: `OnboardRepo.TenantIsActive(ctx, tenantID) (bool, error)`；`OnboardService.TenantIsActive(ctx, tenantID) (bool, error)`（Task 5 依赖）。

- [ ] **Step 1: 写失败测试**

Append to `internal/iam/infrastructure/persistence/onboard_repo_mock_test.go`:

```go
func TestOnboardRepo_TenantIsActive(t *testing.T) {
 mock := newIAMMock(t)
 repo := NewOnboardRepo(mock)
 mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM public.tenants`).
  WithArgs("t1").WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
 active, err := repo.TenantIsActive(context.Background(), "t1")
 require.NoError(t, err)
 require.True(t, active)
}

func TestOnboardRepo_TenantIsActive_NotActive(t *testing.T) {
 mock := newIAMMock(t)
 repo := NewOnboardRepo(mock)
 mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM public.tenants`).
  WithArgs("t1").WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(false))
 active, err := repo.TenantIsActive(context.Background(), "t1")
 require.NoError(t, err)
 require.False(t, active)
}

func TestOnboardRepo_TenantIsActive_Error(t *testing.T) {
 mock := newIAMMock(t)
 repo := NewOnboardRepo(mock)
 mock.ExpectQuery(`SELECT EXISTS\(SELECT 1 FROM public.tenants`).
  WithArgs("t1").WillReturnError(pgx.ErrTxClosed)
 _, err := repo.TenantIsActive(context.Background(), "t1")
 require.Error(t, err)
}
```

(`onboard_repo_mock_test.go` 用 `mock := newIAMMock(t)` + `NewOnboardRepo(mock)` 模式（line 52-53）；require 风格；已 import `pgx`/`pgxmock`，错误用例用 `pgx.ErrTxClosed`。)

Append to `internal/iam/application/onboard_service_extra_test.go`:

```go
func TestOnboardService_TenantIsActive(t *testing.T) {
 repo := &forwardingOnboardRepo{found: true}
 svc := NewOnboardService(repo)
 ok, err := svc.TenantIsActive(context.Background(), "t1")
 if err != nil {
  t.Fatalf("TenantIsActive: %v", err)
 }
 if !ok {
  t.Fatal("expected TenantIsActive=true")
 }
}
```

- [ ] **Step 2: 跑测试确认失败（编译错误即失败）**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./internal/iam/application/ ./internal/iam/infrastructure/persistence/`
Expected: 编译失败。

- [ ] **Step 3: 实现 + 同步 mock**

Port — append to interface in `internal/iam/domain/port/onboard_repo.go`:

```go
 // IsMember reports whether userID is an active member of tenantID.
 IsMember(ctx context.Context, userID, tenantID string) (bool, error)
 // TenantIsActive reports whether tenantID exists, is not deleted, and has
 // status 'active'. Gates platform-admin cross-tenant switch-ins, where
 // membership does not apply.
 TenantIsActive(ctx context.Context, tenantID string) (bool, error)
```

Impl — append to `internal/iam/infrastructure/persistence/onboard_repo.go`:

```go
// TenantIsActive reports whether the tenant is live (exists, not deleted,
// status 'active'). Used to gate platform-admin cross-tenant switch-ins,
// where membership does not apply.
func (r *OnboardRepo) TenantIsActive(ctx context.Context, tenantID string) (bool, error) {
 var active bool
 if err := r.db.QueryRow(ctx,
  `SELECT EXISTS(SELECT 1 FROM public.tenants
   WHERE id = $1 AND deleted_at IS NULL AND status = 'active')`,
  tenantID,
 ).Scan(&active); err != nil {
  return false, fmt.Errorf("onboard_repo: tenant is active: %w", err)
 }
 return active, nil
}
```

Service — append to `internal/iam/application/onboard_service.go`:

```go
// TenantIsActive reports whether tenantID is live (exists, active, not deleted).
func (s *OnboardService) TenantIsActive(ctx context.Context, tenantID string) (bool, error) {
 return s.repo.TenantIsActive(ctx, tenantID)
}
```

Mock 同步：

`internal/iam/application/onboard_service_extra_test.go` — append to `forwardingOnboardRepo`:

```go
func (r *forwardingOnboardRepo) TenantIsActive(_ context.Context, tenantID string) (bool, error) {
 return r.found, nil
}
```

`api/http/handler/auth_handler_test.go` — extend `onboardRepoFake` struct with fields and add value-receiver methods (needed by Task 5's SwitchTenant tests; defaults keep existing tests unaffected):

```go
type onboardRepoFake struct {
 iamport.OnboardRepo
 tenants      []domain.TenantInfo
 exists       bool
 autoJoinErr  error
 globalRole   string
 tenantRole   string
 tenantActive bool
 isMember     bool
}
```

append after `AutoJoinDefaultTenant`:

```go
func (f onboardRepoFake) GetGlobalRole(context.Context, string) (string, error) {
 return f.globalRole, nil
}
func (f onboardRepoFake) GetTenantRole(context.Context, string, string) (string, error) {
 return f.tenantRole, nil
}
func (f onboardRepoFake) TenantIsActive(context.Context, string) (bool, error) {
 return f.tenantActive, nil
}
func (f onboardRepoFake) IsMember(context.Context, string, string) (bool, error) {
 return f.isMember, nil
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./internal/iam/... ./api/http/...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/iam/domain/port/onboard_repo.go internal/iam/infrastructure/persistence/onboard_repo.go internal/iam/application/onboard_service.go internal/iam/application/onboard_service_extra_test.go internal/iam/infrastructure/persistence/onboard_repo_mock_test.go api/http/handler/auth_handler_test.go
git commit -m "feat(iam): add OnboardRepo.TenantIsActive to gate platform-admin switch-in"
```

---

### Task 5: `SwitchTenant` 平台管理员进入任一 active 租户

**Files:**

- Modify: `api/http/handler/auth_tenant_handler.go`
- Test: Create: `api/http/handler/auth_tenant_handler_test.go`

**Interfaces:**

- Consumes: `OnboardService.TenantIsActive`（Task 4）、`EffectiveTenantRole`（Task 1）。
- Produces: `POST /auth/switch-tenant` 对平台管理员签发 `role="admin"`（真实 owner 保留），非平台管理员维持原 IsMember 逻辑。

- [ ] **Step 1: 写失败测试**

Create `api/http/handler/auth_tenant_handler_test.go`:

```go
package handler_test

import (
 "crypto/rand"
 "crypto/rsa"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"

 "github.com/byteBuilderX/stratum/api/http/handler"
 "github.com/byteBuilderX/stratum/api/middleware"
 "github.com/byteBuilderX/stratum/internal/iam/application"
 "github.com/byteBuilderX/stratum/internal/iam/domain"
 iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
 iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
 "github.com/byteBuilderX/stratum/pkg/constants"
 "github.com/gin-gonic/gin"
 "go.uber.org/zap"
)

// newSwitchTenantHandler builds a real AuthHandler over fake OnboardRepo +
// real JWT signer + fake token store. onboardRepoFake already embeds the
// OnboardRepo interface, so only the methods SwitchTenant touches need
// overrides (globalRole/tenantRole/tenantActive/isMember, added in Task 4).
func newSwitchTenantHandler(repo onboardRepoFake) (*handler.AuthHandler, iamport.TokenService) {
 key, _ := rsa.GenerateKey(rand.Reader, 2048)
 jwtSvc := iamtoken.NewJWTService(key)
 h := handler.NewAuthHandler(handler.AuthHandlerDeps{
  JWTService:    jwtSvc,
  TokenStore:    &refreshTokenStoreFake{},
  OnboardSvc:    application.NewOnboardService(repo),
  Logger:        zap.NewNop(),
  FrontendURL:   "http://localhost",
  CallbackURL:   "http://localhost/cb",
  SecureCookies: false,
 })
 return h, jwtSvc
}

func switchTenantRequest(h *handler.AuthHandler, jwtSvc iamport.TokenService, tokenSub, globalRole, role string, tenantID string) (*httptest.ResponseRecorder, map[string]any) {
 claims := iamport.TokenClaims{Sub: tokenSub, TenantID: "current-tenant", Role: role, GlobalRole: globalRole}
 tok, _ := jwtSvc.Sign(claims, constants.AccessTokenTTL)
 gin.SetMode(gin.TestMode)
 r := gin.New()
 r.Use(middleware.ErrorHandler(zap.NewNop()))
 r.POST("/auth/switch-tenant", h.SwitchTenant)
 w := httptest.NewRecorder()
 req, _ := http.NewRequest(http.MethodPost, "/auth/switch-tenant", strings.NewReader(`{"tenant_id":"`+tenantID+`"}`)) //nolint:noctx
 req.Header.Set("Content-Type", "application/json")
 req.Header.Set("Authorization", "Bearer "+tok)
 r.ServeHTTP(w, req)
 var body map[string]any
 _ = json.Unmarshal(w.Body.Bytes(), &body)
 return w, body
}

func TestSwitchTenant_PlatformAdmin_NonMemberAllowed(t *testing.T) {
 h, jwtSvc := newSwitchTenantHandler(onboardRepoFake{
  globalRole:   string(domain.GlobalRoleSystemAdmin),
  tenantActive: true,
  tenantRole:   "",
  isMember:     false,
 })
 w, body := switchTenantRequest(h, jwtSvc, "u1", "system_admin", "member", "foreign-tenant")
 if w.Code != http.StatusOK {
  t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
 }
 gotRole := body["access_token"]
 if gotRole == nil {
  t.Fatal("expected access_token in response")
 }
 // 签发 token 内的 role 应为 admin（平台管理员在非所属租户提升）。
 tok, _ := gotRole.(string)
 claims, err := jwtSvc.Verify(tok)
 if err != nil {
  t.Fatalf("verify issued token: %v", err)
 }
 if claims.Role != "admin" {
  t.Fatalf("issued role = %q, want admin", claims.Role)
 }
 if claims.TenantID != "foreign-tenant" {
  t.Fatalf("issued tenant = %q, want foreign-tenant", claims.TenantID)
 }
}

func TestSwitchTenant_PlatformAdmin_InactiveTenantDenied(t *testing.T) {
 h, jwtSvc := newSwitchTenantHandler(onboardRepoFake{
  globalRole:   string(domain.GlobalRoleSystemAdmin),
  tenantActive: false,
 })
 w, _ := switchTenantRequest(h, jwtSvc, "u1", "system_admin", "member", "stopped-tenant")
 if w.Code != http.StatusForbidden {
  t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
 }
}

func TestSwitchTenant_OrdinaryUser_NonMemberDenied(t *testing.T) {
 h, jwtSvc := newSwitchTenantHandler(onboardRepoFake{
  globalRole: string(domain.GlobalRoleUser),
  isMember:   false,
 })
 w, _ := switchTenantRequest(h, jwtSvc, "u1", "user", "member", "foreign-tenant")
 if w.Code != http.StatusForbidden {
  t.Fatalf("expected 403, got %d body=%s", w.Code, w.Body.String())
 }
}

func TestSwitchTenant_OrdinaryUser_MemberAllowedKeepsRole(t *testing.T) {
 h, jwtSvc := newSwitchTenantHandler(onboardRepoFake{
  globalRole: string(domain.GlobalRoleUser),
  isMember:   true,
  tenantRole: "member",
 })
 w, body := switchTenantRequest(h, jwtSvc, "u1", "user", "member", "my-tenant")
 if w.Code != http.StatusOK {
  t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
 }
 tok, _ := body["access_token"].(string)
 claims, err := jwtSvc.Verify(tok)
 if err != nil {
  t.Fatalf("verify issued token: %v", err)
 }
 if claims.Role != "member" {
  t.Fatalf("issued role = %q, want member", claims.Role)
 }
}

func TestSwitchTenant_PlatformAdmin_OwnerKept(t *testing.T) {
 h, jwtSvc := newSwitchTenantHandler(onboardRepoFake{
  globalRole:   string(domain.GlobalRoleGlobalAdmin),
  tenantActive: true,
  tenantRole:   "owner",
  isMember:     true,
 })
 w, body := switchTenantRequest(h, jwtSvc, "u1", "global_admin", "owner", "owned-tenant")
 if w.Code != http.StatusOK {
  t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
 }
 tok, _ := body["access_token"].(string)
 claims, err := jwtSvc.Verify(tok)
 if err != nil {
  t.Fatalf("verify issued token: %v", err)
 }
 if claims.Role != "owner" {
  t.Fatalf("issued role = %q, want owner", claims.Role)
 }
}

（本文件为 `package handler_test`，复用 `auth_handler_test.go` 的同包 fakes：`onboardRepoFake`/`refreshTokenStoreFake`。`iamtoken` import 路径为 `internal/iam/infrastructure/token`；`Sign` 必须传 `constants.AccessTokenTTL`——TTL=0 生成 `ExpiresAt=now` 的 token，jwt/v5 `Verify` 校验 exp 失败。返回类型用 `iamport.TokenService`（有 Sign+Verify），`iamtoken.NewJWTService` 天然满足。）

- [ ] **Step 2: 跑测试确认失败**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./api/http/handler/ -run TestSwitchTenant -v`
Expected: FAIL（平台管理员非成员场景当前 403）。

- [ ] **Step 3: 实现**

In `api/http/handler/auth_tenant_handler.go`, replace lines 42–59 (the `IsMember` check through `GetTenantRole` fallback):

```go
 globalRole, err := h.deps.OnboardSvc.GetGlobalRole(ctx, claims.Sub)
 if err != nil {
  globalRole = claims.GlobalRole
 }
 isPlatformAdmin := domain.GlobalRole(globalRole).IsPlatformAdmin()
 if isPlatformAdmin {
  // 平台管理员无成员资格：改为校验租户本身是否 active，防止进入
  // 不存在/停用/已删除租户。
  active, aErr := h.deps.OnboardSvc.TenantIsActive(ctx, req.TenantID)
  if aErr != nil {
   _ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("tenant check failed")))
   return
  }
  if !active {
   _ = c.Error(middleware.NewHTTPError(http.StatusForbidden, errors.New("tenant is not active")))
   return
  }
 } else {
  isMember, mErr := h.deps.OnboardSvc.IsMember(ctx, claims.Sub, req.TenantID)
  if mErr != nil {
   _ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("membership check failed")))
   return
  }
  if !isMember {
   _ = c.Error(middleware.NewHTTPError(http.StatusForbidden, errors.New("not a member of this tenant")))
   return
  }
 }

 tenantRole, err := h.deps.OnboardSvc.GetTenantRole(ctx, claims.Sub, req.TenantID)
 if err != nil {
  tenantRole = ""
 }
 tenantRole = domain.EffectiveTenantRole(tenantRole, globalRole)
```

(删除原有重复的 `globalRole, err := ...` 块；`domain` import 已存在。)

- [ ] **Step 4: 跑测试确认通过 + 全量 handler 回归**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./api/http/handler/`
Expected: PASS（含新增 + 既有）。

- [ ] **Step 5: Commit**

```bash
git add api/http/handler/auth_tenant_handler.go api/http/handler/auth_tenant_handler_test.go
git commit -m "feat(auth): allow platform admins to switch into any active tenant"
```

---

### Task 6: `Refresh` 平台管理员非成员续期

**Files:**

- Modify: `api/http/handler/auth_session_handler.go`
- Modify: `api/http/handler/auth_handler_test.go`（扩展 `membershipReaderFake`：加 `globalRole` 字段 + `GetGlobalRole` 返回它）
- Test: Create: `api/http/handler/auth_session_handler_test.go`（`package handler_test`，复用 auth_handler_test.go 的 fakes）

**Interfaces:**

- Consumes: `membershipReader` 接口（`{GetTenantRole, GetGlobalRole}`，auth_handler.go:29）、`RefreshTokenStore`、`EffectiveTenantRole`。
- Produces: `POST /auth/refresh` 对平台管理员在非所属租户（`ErrMemberNotFound`）继续签发 `role="admin"`；普通用户非成员仍 401；现查失败仍 503。

- [ ] **Step 1: 写失败测试**

Create `api/http/handler/auth_session_handler_test.go`:

```go
package handler_test

import (
 "crypto/rand"
 "crypto/rsa"
 "net/http"
 "net/http/httptest"
 "testing"

 "github.com/byteBuilderX/stratum/api/http/handler"
 "github.com/byteBuilderX/stratum/api/middleware"
 "github.com/byteBuilderX/stratum/internal/iam/domain"
 iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
 "github.com/gin-gonic/gin"
 "go.uber.org/zap"
)

func TestRefresh_PlatformAdmin_NonMember_IssuesAdminRole(t *testing.T) {
 key, _ := rsa.GenerateKey(rand.Reader, 2048)
 jwtSvc := iamtoken.NewJWTService(key)
 h := handler.NewAuthHandler(handler.AuthHandlerDeps{
  JWTService: jwtSvc,
  // GetActiveClaims 返回 f.claims（auth_handler_test.go:43）——必须带 claims，否则 nil 解引用 panic。
  TokenStore: &refreshTokenStoreFake{claims: &domain.StoredSession{UserID: "user-1", TenantID: "tenant-1"}},
  MembershipReader: membershipReaderFake{
   roleErr:    domain.ErrMemberNotFound,
   globalRole: string(domain.GlobalRoleSystemAdmin),
  },
  Logger: zap.NewNop(),
 })
 gin.SetMode(gin.TestMode)
 r := gin.New()
 r.Use(middleware.ErrorHandler(zap.NewNop()))
 r.POST("/auth/refresh", h.Refresh)
 w := httptest.NewRecorder()
 req, _ := http.NewRequest(http.MethodPost, "/auth/refresh", nil) //nolint:noctx
 req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "some-refresh-token"})
 r.ServeHTTP(w, req)
 if w.Code != http.StatusOK {
  t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
 }
}

func TestRefresh_OrdinaryUser_NonMember_Still401(t *testing.T) {
 key, _ := rsa.GenerateKey(rand.Reader, 2048)
 jwtSvc := iamtoken.NewJWTService(key)
 h := handler.NewAuthHandler(handler.AuthHandlerDeps{
  JWTService: jwtSvc,
  TokenStore: &refreshTokenStoreFake{claims: &domain.StoredSession{UserID: "user-1", TenantID: "tenant-1"}},
  MembershipReader: membershipReaderFake{
   roleErr:    domain.ErrMemberNotFound,
   globalRole: string(domain.GlobalRoleUser),
  },
  Logger: zap.NewNop(),
 })
 gin.SetMode(gin.TestMode)
 r := gin.New()
 r.Use(middleware.ErrorHandler(zap.NewNop()))
 r.POST("/auth/refresh", h.Refresh)
 w := httptest.NewRecorder()
 req, _ := http.NewRequest(http.MethodPost, "/auth/refresh", nil) //nolint:noctx
 req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "some-refresh-token"})
 r.ServeHTTP(w, req)
 if w.Code != http.StatusUnauthorized {
  t.Fatalf("expected 401, got %d body=%s", w.Code, w.Body.String())
 }
}
```

**前置（本 task 新增步骤）：** 扩展 `api/http/handler/auth_handler_test.go` 的 `membershipReaderFake`（line 47-49、102）：

```go
type membershipReaderFake struct {
 roleErr    error
 globalRole string
}

func (f membershipReaderFake) GetGlobalRole(context.Context, string) (string, error) {
 return f.globalRole, nil
}
```

既有测试均用 `membershipReaderFake{roleErr: ...}`，`globalRole` 默认 `""` → 非平台管理员 → 既有 Refresh 断言（非成员 401/503）不变；`GetTenantRole` 值接收者不动。`GetActiveClaims` 返回 `f.claims`（auth_handler_test.go:43）——两个新测试都必须给 `claims`（如上），否则 nil 解引用 panic。cookie 名用字面量 `"refresh_token"`（`refreshTokenCookie` const 是 handler 包私有，外部包不可见）。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./api/http/handler/ -run TestRefresh_PlatformAdmin -v`
Expected: FAIL（平台管理员非成员当前 401）。

- [ ] **Step 3: 实现**

In `api/http/handler/auth_session_handler.go`, replace lines 43–55 (`MembershipReader` nil 检查之后到 `tenantRole == ""` 检查):

```go
 globalRole := ""
 if dbRole, rErr := h.deps.MembershipReader.GetGlobalRole(ctx, storedClaims.UserID); rErr == nil {
  globalRole = dbRole
 }
 tenantRole, err := h.deps.MembershipReader.GetTenantRole(ctx, storedClaims.UserID, storedClaims.TenantID)
 switch {
 case err == nil && tenantRole != "":
  tenantRole = domain.EffectiveTenantRole(tenantRole, globalRole)
 case errors.Is(err, domain.ErrMemberNotFound):
  if !domain.GlobalRole(globalRole).IsPlatformAdmin() {
   _ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errors.New("tenant membership no longer active")))
   return
  }
  tenantRole = domain.EffectiveTenantRole("", globalRole)
 default:
  _ = c.Error(middleware.NewHTTPError(http.StatusServiceUnavailable, errors.New("membership verification failed")))
  return
 }
```

然后删除原 lines 67–72 的 `globalRole := ""...` 块（已提前）。

- [ ] **Step 4: 跑测试确认通过 + 全量 handler 回归**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./api/http/handler/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add api/http/handler/auth_session_handler.go api/http/handler/auth_session_handler_test.go
git commit -m "feat(auth): refresh keeps platform admins alive in foreign tenants"
```

---

### Task 7: `ListUserTenants` 平台管理员返回全部租户

**Files:**

- Modify: `api/http/handler/tenant_handler.go`
- Test: Modify: `api/http/handler/tenant_handler_test.go`

**Interfaces:**

- Consumes: `TenantService.ListAllTenants`（Task 3）、`auth.global_role`（JWT 注入）。
- Produces: `GET /tenant/list` 平台管理员返回所有 active 租户；普通用户维持原逻辑。

- [ ] **Step 1: 写失败测试**

Append to `api/http/handler/tenant_handler_test.go`:

```go
func TestTenantHandler_ListUserTenants_PlatformAdminSeesAll(t *testing.T) {
 repo := &fakeTenantRepo{allTenants: []domain.UserTenantInfo{
  {TenantID: "t-foreign", Name: "Foreign", IsDefault: false},
  {TenantID: "t-mine", Name: "Mine", IsDefault: true},
 }}
 h := newTenantHandler(repo)
 gin.SetMode(gin.TestMode)
 r := gin.New()
 r.GET("/tenant/list", func(c *gin.Context) {
  c.Set("auth.sub", "u1")
  c.Set("auth.global_role", "system_admin")
 }, h.ListUserTenants)
 w := httptest.NewRecorder()
 req, _ := http.NewRequest(http.MethodGet, "/tenant/list", nil) //nolint:noctx
 r.ServeHTTP(w, req)
 if w.Code != http.StatusOK {
  t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
 }
 var resp gen.TenantListResponse
 if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
  t.Fatalf("unmarshal: %v", err)
 }
 if len(resp.Tenants) != 2 {
  t.Fatalf("tenants = %+v, want 2 (all tenants)", resp.Tenants)
 }
}

func TestTenantHandler_ListUserTenants_OrdinaryUserSeesOnlyOwn(t *testing.T) {
 repo := &fakeTenantRepo{allTenants: []domain.UserTenantInfo{
  {TenantID: "t-foreign", Name: "Foreign"},
 }, members: []domain.Member{}}
 // fakeTenantRepo.ListUserTenants 返回空——断言普通用户走 ListUserTenants 而非 ListAllTenants：
 // 通过 allTenants 与 ListUserTenants 返回差异区分。这里让 ListUserTenants 有数据。
 repo.userTenants = []domain.UserTenantInfo{{TenantID: "t-mine", Name: "Mine", IsDefault: true}}
 h := newTenantHandler(repo)
 gin.SetMode(gin.TestMode)
 r := gin.New()
 r.GET("/tenant/list", func(c *gin.Context) {
  c.Set("auth.sub", "u1")
  c.Set("auth.global_role", "user")
 }, h.ListUserTenants)
 w := httptest.NewRecorder()
 req, _ := http.NewRequest(http.MethodGet, "/tenant/list", nil) //nolint:noctx
 r.ServeHTTP(w, req)
 if w.Code != http.StatusOK {
  t.Fatalf("expected 200, got %d", w.Code)
 }
 var resp gen.TenantListResponse
 _ = json.Unmarshal(w.Body.Bytes(), &resp)
 if len(resp.Tenants) != 1 || resp.Tenants[0].TenantID != "t-mine" {
  t.Fatalf("tenants = %+v, want [t-mine]", resp.Tenants)
 }
}
```

**前置（本 task 新增步骤）：** 给 `api/http/handler/tenant_handler_test.go` 的 `fakeTenantRepo` 加字段 `userTenants []domain.UserTenantInfo`，并把 `ListUserTenants`（line 113-115，当前 `return nil, nil`）改为 `return f.userTenants, nil`。既有测试用 `newTenantHandler(&fakeTenantRepo{})` → `userTenants` 为 nil → 仍返回 `(nil, nil)`，行为不变。

- [ ] **Step 2: 跑测试确认失败**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./api/http/handler/ -run TestTenantHandler_ListUserTenants_PlatformAdmin -v`
Expected: FAIL（平台管理员场景当前只返回所属租户）。

- [ ] **Step 3: 实现**

In `api/http/handler/tenant_handler.go`, replace `ListUserTenants` (lines 263–280):

```go
// ListUserTenants GET /tenant/list — tenants the user can enter. Platform
// admins see every active tenant; ordinary users see only their memberships.
func (h *TenantHandler) ListUserTenants(c *gin.Context) {
 userID, ok := c.Get("auth.sub")
 userIDStr, _ := userID.(string)
 if !ok || userIDStr == "" {
  _ = c.Error(middleware.NewHTTPError(http.StatusUnauthorized, errUnauthorized))
  return
 }

 var tenants []domain.UserTenantInfo
 if grVal, ok := c.Get("auth.global_role"); ok {
  if grStr, _ := grVal.(string); domain.GlobalRole(grStr).IsPlatformAdmin() {
   all, err := h.svc.ListAllTenants(c.Request.Context())
   if err != nil {
    _ = c.Error(err)
    return
   }
   tenants = all
  }
 }
 if tenants == nil {
  own, err := h.svc.ListUserTenants(c.Request.Context(), userIDStr)
  if err != nil {
   _ = c.Error(err)
   return
  }
  tenants = own
 }

 items := make([]gen.TenantListItem, 0, len(tenants))
 for _, t := range tenants {
  items = append(items, gen.TenantListItem{TenantID: t.TenantID, Name: t.Name, IsDefault: t.IsDefault})
 }
 c.JSON(http.StatusOK, gen.TenantListResponse{Tenants: items})
}
```

(`domain` import 已存在；`gen`、`middleware`、`errUnauthorized` 已存在。)

- [ ] **Step 4: 跑测试确认通过 + 全量 handler 回归**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./api/http/handler/ ./api/http/`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add api/http/handler/tenant_handler.go api/http/handler/tenant_handler_test.go
git commit -m "feat(iam): tenant list returns all tenants for platform admins"
```

---

### Task 8: `tenantRoleAdapter` 统一角色解析单点

**Files:**

- Modify: `api/wiring/system_assistant.go`（struct + `ResolveTenantRole` + 新 helper）
- Modify: `api/wiring/agent.go`（4 个构造点）
- Test: Modify (append): `api/wiring/system_assistant_test.go`（文件已存在，`package wiring` 内部包，可直接引用未导出 `tenantRoleAdapter`/`newTenantRoleAdapter`）

**Interfaces:**

- Consumes: `tenantMemberRoleService`（agent.go:102，`GetMemberRole`）、`tenantGlobalRoleReader`（本 task 新增，`OnboardService` 满足）、`EffectiveTenantRole`（Task 1）、`iamdomain.ErrMemberNotFound`。
- Produces: `newTenantRoleAdapter(c *Container) tenantRoleAdapter`（替换 4 处构造点）。

- [ ] **Step 1: 写失败测试**

Append to `api/wiring/system_assistant_test.go`（文件已存在，`package wiring` 内部包；测试直接构造未导出的 `tenantRoleAdapter` 并调 `ResolveTenantRole`）:

```go
package wiring

import (
 "context"
 "errors"
 "testing"

 iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"
)

type fakeMemberRoleService struct {
 role string
 err  error
}

func (f fakeMemberRoleService) GetMemberRole(context.Context, string, string) (string, error) {
 return f.role, f.err
}

type fakeGlobalRoleReader struct {
 role string
 err  error
}

func (f fakeGlobalRoleReader) GetGlobalRole(context.Context, string) (string, error) {
 return f.role, f.err
}

func TestTenantRoleAdapter_PlatformAdminNonMemberElevated(t *testing.T) {
 a := tenantRoleAdapter{
  service: fakeMemberRoleService{err: iamdomain.ErrMemberNotFound},
  global:  fakeGlobalRoleReader{role: string(iamdomain.GlobalRoleSystemAdmin)},
 }
 got, err := a.ResolveTenantRole(context.Background(), "t1", "u1")
 if err != nil {
  t.Fatalf("ResolveTenantRole: %v", err)
 }
 if got != "admin" {
  t.Fatalf("ResolveTenantRole = %q, want admin", got)
 }
}

func TestTenantRoleAdapter_MemberPlatformAdminElevated(t *testing.T) {
 a := tenantRoleAdapter{
  service: fakeMemberRoleService{role: "member"},
  global:  fakeGlobalRoleReader{role: string(iamdomain.GlobalRoleSystemAdmin)},
 }
 got, err := a.ResolveTenantRole(context.Background(), "t1", "u1")
 if err != nil {
  t.Fatalf("ResolveTenantRole: %v", err)
 }
 if got != "admin" {
  t.Fatalf("ResolveTenantRole = %q, want admin", got)
 }
}

func TestTenantRoleAdapter_OwnerPlatformAdminKept(t *testing.T) {
 a := tenantRoleAdapter{
  service: fakeMemberRoleService{role: "owner"},
  global:  fakeGlobalRoleReader{role: string(iamdomain.GlobalRoleSystemAdmin)},
 }
 got, err := a.ResolveTenantRole(context.Background(), "t1", "u1")
 if err != nil {
  t.Fatalf("ResolveTenantRole: %v", err)
 }
 if got != "owner" {
  t.Fatalf("ResolveTenantRole = %q, want owner", got)
 }
}

func TestTenantRoleAdapter_OrdinaryMemberUnchanged(t *testing.T) {
 a := tenantRoleAdapter{
  service: fakeMemberRoleService{role: "member"},
  global:  fakeGlobalRoleReader{role: string(iamdomain.GlobalRoleUser)},
 }
 got, err := a.ResolveTenantRole(context.Background(), "t1", "u1")
 if err != nil {
  t.Fatalf("ResolveTenantRole: %v", err)
 }
 if got != "member" {
  t.Fatalf("ResolveTenantRole = %q, want member", got)
 }
}

func TestTenantRoleAdapter_GlobalRoleLookupFailureFailsClosed(t *testing.T) {
 a := tenantRoleAdapter{
  service: fakeMemberRoleService{role: "member"},
  global:  fakeGlobalRoleReader{err: errors.New("db down")},
 }
 if _, err := a.ResolveTenantRole(context.Background(), "t1", "u1"); err == nil {
  t.Fatal("expected error (fail closed) when global role lookup fails")
 }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./api/wiring/ -run TestTenantRoleAdapter -v`
Expected: FAIL（编译失败，`tenantRoleAdapter` 无 `global` 字段）。

- [ ] **Step 3: 实现**

In `api/wiring/system_assistant.go`, replace the `tenantRoleAdapter` block (currently lines 249–256) and add the interface + helper next to it:

```go
// tenantGlobalRoleReader reads users.global_role. OnboardService satisfies it.
type tenantGlobalRoleReader interface {
 GetGlobalRole(ctx context.Context, userID string) (string, error)
}

type tenantRoleAdapter struct {
 service tenantMemberRoleService
 global  tenantGlobalRoleReader
}

// ResolveTenantRole resolves the user's effective tenant role. Real owner/admin
// short-circuit (no extra query); members and non-members fall through to the
// platform role so platform admins are treated as tenant admins. Fails closed:
// a global-role lookup error propagates instead of defaulting to a role.
func (a tenantRoleAdapter) ResolveTenantRole(ctx context.Context, tenantID, userID string) (string, error) {
 if a.service == nil {
  return "", domain.ErrDiagnosticForbidden
 }
 realRole, err := a.service.GetMemberRole(ctx, tenantID, userID)
 if err != nil && !errors.Is(err, iamdomain.ErrMemberNotFound) {
  return "", err
 }
 if errors.Is(err, iamdomain.ErrMemberNotFound) {
  realRole = ""
 }
 if realRole == iamdomain.RoleOwner || realRole == iamdomain.RoleAdmin {
  return realRole, nil
 }
 if a.global == nil {
  return realRole, nil
 }
 gr, gErr := a.global.GetGlobalRole(ctx, userID)
 if gErr != nil {
  return "", gErr
 }
 return iamdomain.EffectiveTenantRole(realRole, gr), nil
}

// newTenantRoleAdapter assembles the DB-backed tenant role resolver with the
// platform-role reader needed to elevate platform admins.
func newTenantRoleAdapter(c *Container) tenantRoleAdapter {
 adapter := tenantRoleAdapter{service: tenantMemberService(c)}
 // OnboardSvc 满足 tenantGlobalRoleReader（GetGlobalRole）。wiring 构建顺序 platform → iam → agent，
 // buildAgent 运行时 c.Platform 已就绪（`c.Platform` 是 `*Platform` 指针，platform.go:36）；guard 使 nil 时行为与现状一致（无提升）。
 if c.Platform != nil && c.Platform.OnboardSvc != nil {
  adapter.global = c.Platform.OnboardSvc
 }
 return adapter
}
```

Add import to `api/wiring/system_assistant.go` 的 third-party 组（`errors`/`context` 已 import，仅需加）：`iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"`。

In `api/wiring/agent.go`, replace the 3 construction sites:

- line 408: `proposalAuthorizer{roles: tenantRoleAdapter{service: tenantMemberService(c)}}` → `proposalAuthorizer{roles: newTenantRoleAdapter(c)}`
- line 428 (in `injectTenantRoleResolvers`): `roles := tenantRoleAdapter{service: tenantMemberService(c)}` → `roles := newTenantRoleAdapter(c)`
- line 513 (in `wireOperationGate`): `roles := tenantRoleAdapter{service: tenantMemberService(c)}` → `roles := newTenantRoleAdapter(c)`

In `api/wiring/system_assistant.go` line 43 (`newDiagnosticProvider`): `tenantRoleAdapter{service: tenantMemberService(c)}` → `newTenantRoleAdapter(c)`。

- [ ] **Step 4: 跑测试确认通过 + 全量 wiring 编译**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && go test ./api/wiring/ && go build ./api/...`
Expected: PASS + build 成功。

- [ ] **Step 5: Commit**

```bash
git add api/wiring/system_assistant.go api/wiring/agent.go api/wiring/system_assistant_test.go
git commit -m "feat(wiring): tenantRoleAdapter elevates platform admins to tenant admin"
```

---

### Task 9: 前端 `useTenantRole` 平台管理员提升

**Files:**

- Modify: `web/src/modules/iam/hooks/useTenantRole.ts`
- Test: Create: `web/src/modules/iam/hooks/useTenantRole.test.ts`

**Interfaces:**

- Consumes: `useAuth().user` 形状（`{ role?, current_tenant?: {role?}, global_role? }`，见 AuthContext buildUser）。
- Produces: `useTenantRole().isAdmin/isOwner/isMember/hasTenantRole` 基于提升后的 effectiveRole。

- [ ] **Step 1: 写失败测试**

Create `web/src/modules/iam/hooks/useTenantRole.test.ts`:

```ts
import { describe, expect, it, vi } from 'vitest';
import { useTenantRole } from './useTenantRole';
import { useAuth } from '@/modules/iam';

vi.mock('@/modules/iam', () => ({
  useAuth: vi.fn(),
}));

describe('useTenantRole', () => {
  it('platform admin is admin even in a foreign tenant', () => {
    vi.mocked(useAuth).mockReturnValue({
      user: { role: 'member', global_role: 'system_admin', current_tenant: { role: 'member' } },
    } as any);
    const { role, isAdmin, isOwner } = useTenantRole();
    expect(role).toBe('admin');
    expect(isAdmin).toBe(true);
    expect(isOwner).toBe(false);
  });

  it('global admin with owner keeps owner', () => {
    vi.mocked(useAuth).mockReturnValue({
      user: { role: 'owner', global_role: 'global_admin', current_tenant: { role: 'owner' } },
    } as any);
    const { role, isAdmin, isOwner } = useTenantRole();
    expect(role).toBe('owner');
    expect(isOwner).toBe(true);
  });

  it('ordinary member unchanged', () => {
    vi.mocked(useAuth).mockReturnValue({
      user: { role: 'member', global_role: 'user', current_tenant: { role: 'member' } },
    } as any);
    const { role, isAdmin, isMember } = useTenantRole();
    expect(role).toBe('member');
    expect(isAdmin).toBe(false);
    expect(isMember).toBe(true);
  });

  it('hasTenantRole respects elevation', () => {
    vi.mocked(useAuth).mockReturnValue({
      user: { role: 'member', global_role: 'system_admin', current_tenant: { role: 'member' } },
    } as any);
    const { hasTenantRole } = useTenantRole();
    expect(hasTenantRole('admin')).toBe(true);
    expect(hasTenantRole('owner')).toBe(false);
  });
});
```

- [ ] **Step 2: 跑测试确认失败**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant/web && npx vitest run src/modules/iam/hooks/useTenantRole.test.ts`
Expected: FAIL（第一个用例 role 为 `member` 而非 `admin`）。

- [ ] **Step 3: 实现**

Replace the body of `web/src/modules/iam/hooks/useTenantRole.ts`:

```ts
import { useAuth } from '@/modules/iam';

// 租户角色层级，与后端 middleware.RequireTenantRole 一致：owner > admin > member。
const TENANT_ROLE_RANK: Record<string, number> = { member: 1, admin: 2, owner: 3 };
// 平台角色层级，与后端 middleware.RequirePlatformAdmin 一致。
const PLATFORM_ROLE_RANK: Record<string, number> = { user: 1, system_admin: 2, global_admin: 3 };

export interface TenantRoleInfo {
  /** 有效租户角色：平台管理员（system_admin/global_admin）至少视为 admin，owner 保留。 */
  role: string;
  /** admin 或 owner。可管理（新建/编辑/删除）各类资源。 */
  isAdmin: boolean;
  /** 仅 owner。 */
  isOwner: boolean;
  /** 普通成员（member）：只能对话/执行/查看，不能新建/修改/删除。 */
  isMember: boolean;
  /** 判断是否达到某最低角色要求。 */
  hasTenantRole: (min: string) => boolean;
}

/**
 * useTenantRole 统一读取当前租户角色，供前端按钮/入口的权限隐藏使用。
 *
 * 后端已对写操作做 admin 拦截，这里仅负责 UI 层隐藏对应入口，避免成员点了才被 403。
 * 平台管理员在任意租户内至少视为 admin（与后端 EffectiveTenantRole 语义一致），
 * 但绝不视为 owner。
 */
export const useTenantRole = (): TenantRoleInfo => {
  const { user } = useAuth();
  const rawRole = user?.role ?? user?.current_tenant?.role ?? 'member';
  const platformRank = PLATFORM_ROLE_RANK[user?.global_role ?? 'user'] ?? 0;
  const isPlatformAdmin = platformRank >= PLATFORM_ROLE_RANK.system_admin;
  const role = isPlatformAdmin && TENANT_ROLE_RANK[rawRole] < TENANT_ROLE_RANK.admin ? 'admin' : rawRole;
  const rank = TENANT_ROLE_RANK[role] ?? 0;

  return {
    role,
    isAdmin: rank >= TENANT_ROLE_RANK.admin,
    isOwner: rank >= TENANT_ROLE_RANK.owner,
    isMember: rank <= TENANT_ROLE_RANK.member,
    hasTenantRole: (min: string) => rank >= (TENANT_ROLE_RANK[min] ?? 0),
  };
};
```

- [ ] **Step 4: 跑测试 + lint + build**

Run:

```bash
cd /home/yang/go-projects/stratum-platform-admin-cross-tenant/web && npx vitest run src/modules/iam/hooks/useTenantRole.test.ts
cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && make fe-lint && make fe-build
```

Expected: 单测 PASS + lint/build 成功。

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/iam/hooks/useTenantRole.ts web/src/modules/iam/hooks/useTenantRole.test.ts
git commit -m "feat(web): useTenantRole treats platform admins as tenant admin"
```

---

### Task 10: 契约回归 + 全量测试

**Files:**

- 无新增改动（验证现有）
- Test: `api/http/contract_test.go`（Task 3 已同步 `contractTenantRepo`）、`api/http/testdata/contracts/*.golden.json`

- [ ] **Step 1: 契约测试 + 后端全量**

Run:

```bash
cd /home/yang/go-projects/stratum-platform-admin-cross-tenant
go vet ./...
go test ./... 2>&1 | tail -40
```

Expected: PASS（`/tenant/list` golden 不变——普通用户契约未改；`record-contracts.go` 已同步编译）。

- [ ] **Step 2: 确认 contract golden 未漂移**

Run: `git status --short`
Expected: 无 `*.golden.json` 变更（若 `api/http/testdata/contracts/` 有改动，检查 `record-contracts` 重跑差异是否仅因 ListAllTenants stub；应无变化）。

- [ ] **Step 3: Commit（如有残留）**

```bash
git add -A
git status --short
```

---

### Task 11: 系统验收 + PR → CI → CD 部署成功

**Files:**

- 无新增代码改动。

- [ ] **Step 1: 本地完整门槛**

Run: `cd /home/yang/go-projects/stratum-platform-admin-cross-tenant && make test-verify-before-pr`
Expected: 全绿（IAM 改动 → R2+ 按 `.test/verification.yaml` 升级）。

- [ ] **Step 2: 系统验收（stratum-e2e-tester）**

调用 `stratum-e2e-tester` agent 执行系统验收，覆盖：

1. system_admin 登录 → Header 租户下拉显示所有租户（含非所属）。
2. 切换到非所属租户 → 租户内 admin 操作（新建/编辑资源、成员管理）成功。
3. 普通 member 回归：下拉仅所属、非 admin 入口不可见。
4. 平台管理员 owner 专属操作仍被拒。

- [ ] **Step 3: Push + PR**

```bash
cd /home/yang/go-projects/stratum-platform-admin-cross-tenant
git push -u origin feat/platform-admin-cross-tenant
gh pr create --base main --title "feat(iam): platform admin cross-tenant access" --body "What: ...\nWhy: ...\nHowToTest: ..."
```

- [ ] **Step 4: CI 全绿 + base 同步检查**

等待 CI。期间 `git fetch origin main` 对比 PR base 是否落后；落后则合入 main → 本地验证 → push。

- [ ] **Step 5: 合并 → CD 部署**

CI 全绿后合并，经 CD 流水线部署到远端，回归验证 `GET /tenant/list`、`POST /auth/switch-tenant`、租户内 admin 写路径在远端行为一致。
