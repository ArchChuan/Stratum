# 平台管理员管理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 提供「平台管理员」管理能力——超级管理员（global_admin）可在 UI 中查看/添加/移除普通平台管理员（system_admin），超级管理员仅程序预设。

**Architecture:** 扩展 `users.global_role`（无 DDL）为三档（user/system_admin/global_admin）。后端新增 `AdminUserRepo`（public schema，与预设渠道 `OnboardRepo.SetGlobalRole` 物理分离）、`AdminService` 扩展、`RequirePlatformAdmin` 中间件、4 个 `/admin` 端点；路由按敏感度拆分守卫。前端新增 `AdminsPage` + `usePlatformRole`，`PrivateRoute` 平台角色判定迁移到 `user.global_role`。

**Tech Stack:** Go 1.25.12 / Gin / pgx v5（public schema 直接 pool）/ protoc-gen-ginstruct / React 18 / Ant Design 5 / zod / Vite

## Global Constraints

- `users` 是 public schema 公共表，仓储直接 `pool.Query/Exec`，**禁止 `execTenant`**（`pkg/migration-tenant.md`：public 表不经过租户 schema）。
- 预设渠道 `OnboardRepo.SetGlobalRole(ctx, userID, role string)`（`onboard_repo.go:227`）被 `auth_register_handler.go:76`、`auth_oauth_handler.go:97,141` 用于注入 `global_admin`——**禁止改动**；新 UI 渠道是独立的 `AdminUserRepo`。
- 守卫 fail closed：`RequirePlatformAdmin` 角色缺失/非法一律 403。
- **禁止动任何 `global_admin`（含自己）**：UI 置灰 + `AdminService` 校验双层。
- guest 排除用 `users.is_guest = false`（迁移 `019` 加的列）。
- Go 行宽 ≤120；错误用 `fmt.Errorf("op: %w", err)` 逐层包装；行为数字进 `pkg/constants`（分页 limit 用 `constants.DefaultPageSize`）。
- 前端统一走 `web/src/services/client.ts` 的 `api`；错误 `message.error({ content: err.response?.data?.error || '操作失败', duration: 3 })`；禁止 `alert()`/`console.log`；字符串中文。
- 改 proto 后跑 `make proto-gen`（生成物 `api/http/dto/gen/`、`web/src/services/gen/` 不入 git）。

---

### Task 1: GlobalRole 领域类型

**Files:**

- Create: `internal/iam/domain/global_role.go`
- Test: `internal/iam/domain/global_role_test.go`

**Interfaces:**

- Produces: `domain.GlobalRole` 类型 + `GlobalRoleUser/SystemAdmin/GlobalAdmin` 常量 + `Valid() bool` + `AtLeast(min GlobalRole) bool`。Task 2/4/5/6 依赖。

- [ ] **Step 1: 写失败测试** `internal/iam/domain/global_role_test.go`

```go
package domain

import "testing"

func TestGlobalRoleValid(t *testing.T) {
 cases := []struct {
  role GlobalRole
  want bool
 }{
  {GlobalRoleUser, true}, {GlobalRoleSystemAdmin, true}, {GlobalRoleGlobalAdmin, true},
  {GlobalRole(""), false}, {GlobalRole("owner"), false}, {GlobalRole("admin"), false},
 }
 for _, tc := range cases {
  if got := tc.role.Valid(); got != tc.want {
   t.Errorf("GlobalRole(%q).Valid() = %v, want %v", tc.role, got, tc.want)
  }
 }
}

func TestGlobalRoleAtLeast(t *testing.T) {
 cases := []struct {
  role GlobalRole
  min  GlobalRole
  want bool
 }{
  {GlobalRoleGlobalAdmin, GlobalRoleGlobalAdmin, true},
  {GlobalRoleGlobalAdmin, GlobalRoleSystemAdmin, true},
  {GlobalRoleGlobalAdmin, GlobalRoleUser, true},
  {GlobalRoleSystemAdmin, GlobalRoleGlobalAdmin, false},
  {GlobalRoleSystemAdmin, GlobalRoleSystemAdmin, true},
  {GlobalRoleSystemAdmin, GlobalRoleUser, true},
  {GlobalRoleUser, GlobalRoleSystemAdmin, false},
  {GlobalRoleUser, GlobalRoleUser, true},
  // 未知角色 fail closed：比 user 还低。
  {GlobalRole(""), GlobalRoleUser, false},
  {GlobalRole("owner"), GlobalRoleUser, false},
 }
 for _, tc := range cases {
  if got := tc.role.AtLeast(tc.min); got != tc.want {
   t.Errorf("GlobalRole(%q).AtLeast(%q) = %v, want %v", tc.role, tc.min, got, tc.want)
  }
 }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd /home/yang/go-projects/stratum-platform-admin && go test ./internal/iam/domain/ -run TestGlobalRole -v`
Expected: FAIL（`undefined: GlobalRole`）

- [ ] **Step 3: 实现** `internal/iam/domain/global_role.go`

```go
package domain

// GlobalRole is the platform-wide role on users.global_role, fully independent
// of tenant membership roles (tenant_members.role). "user" is the default for
// every account; "system_admin" and "global_admin" are platform admins.
type GlobalRole string

const (
 // GlobalRoleUser is the default platform role for all non-admin accounts.
 GlobalRoleUser GlobalRole = "user"
 // GlobalRoleSystemAdmin is a platform admin promoted by a global admin via UI.
 GlobalRoleSystemAdmin GlobalRole = "system_admin"
 // GlobalRoleGlobalAdmin is the super admin. Only provisioned programmatically
 // (env GlobalAdmin + seed); never settable through the UI.
 GlobalRoleGlobalAdmin GlobalRole = "global_admin"
)

var globalRoleRank = map[GlobalRole]int{
 GlobalRoleUser:        1,
 GlobalRoleSystemAdmin: 2,
 GlobalRoleGlobalAdmin: 3,
}

// Valid reports whether r is one of the three supported global roles.
func (r GlobalRole) Valid() bool {
 _, ok := globalRoleRank[r]
 return ok
}

// AtLeast reports whether r is at or above min in the platform hierarchy
// (global_admin > system_admin > user). Unknown roles rank below user so
// guards fail closed.
func (r GlobalRole) AtLeast(min GlobalRole) bool {
 return globalRoleRank[r] >= globalRoleRank[min]
}
```

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/iam/domain/ -run TestGlobalRole -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /home/yang/go-projects/stratum-platform-admin && \
git add internal/iam/domain/global_role.go internal/iam/domain/global_role_test.go && \
git commit -m "feat(iam): add GlobalRole domain type

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: RequirePlatformAdmin 中间件

**Files:**

- Modify: `api/middleware/require_role.go`
- Test: `api/middleware/require_role_test.go`

**Interfaces:**

- Consumes: `domain.GlobalRole`（Task 1）。
- Produces: `middleware.RequirePlatformAdmin(minRole domain.GlobalRole) gin.HandlerFunc`、`middleware.RequireSystemAdmin()`。`RequireGlobalAdmin()` 行为不变。Task 6 路由使用。

- [ ] **Step 1: 写失败测试**（追加到 `api/middleware/require_role_test.go`；若文件是空结构先建 `package middleware` + import gin/httptest）

```go
func TestRequirePlatformAdmin(t *testing.T) {
 // 构造带 ctxGlobalRole 的请求：c.Set(ctxGlobalRole, role) 后跑中间件。
 cases := []struct {
  name    string
  role    string
  min     domain.GlobalRole
  wantErr bool // 非零 = 403 拦截
 }{
  {"global passes global-min", "global_admin", domain.GlobalRoleGlobalAdmin, false},
  {"system passes system-min", "system_admin", domain.GlobalRoleSystemAdmin, false},
  {"system fails global-min", "system_admin", domain.GlobalRoleGlobalAdmin, true},
  {"user fails system-min", "user", domain.GlobalRoleSystemAdmin, true},
  {"missing fails", "", domain.GlobalRoleSystemAdmin, true},
  {"garbage fails closed", "owner", domain.GlobalRoleSystemAdmin, true},
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   r := httptest.NewRequest("GET", "/", nil)
   w := httptest.NewRecorder()
   c, _ := gin.CreateTestContext(w)
   c.Request = r
   c.Set(ctxGlobalRole, tc.role)
   RequirePlatformAdmin(tc.min)(c)
   gotErr := c.Writer.Status() == http.StatusForbidden
   if gotErr != tc.wantErr {
    t.Errorf("status=%d wantErr=%v", c.Writer.Status(), tc.wantErr)
   }
  })
 }
}

func TestRequireGlobalAdminDelegates(t *testing.T) {
 // system_admin 必须被 RequireGlobalAdmin 拒绝（行为回归）。
 r := httptest.NewRequest("GET", "/", nil)
 w := httptest.NewRecorder()
 c, _ := gin.CreateTestContext(w)
 c.Request = r
 c.Set(ctxGlobalRole, "system_admin")
 RequireGlobalAdmin()(c)
 if c.Writer.Status() != http.StatusForbidden {
  t.Errorf("RequireGlobalAdmin let system_admin through: status=%d", c.Writer.Status())
 }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./api/middleware/ -run TestRequirePlatformAdmin -v`
Expected: FAIL（`undefined: RequirePlatformAdmin`）

- [ ] **Step 3: 实现** 修改 `api/middleware/require_role.go`

import 块追加 `iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"`。

`RequireGlobalAdmin` 替换为委托实现，并新增两个函数：

```go
// RequirePlatformAdmin aborts with 403 unless the request context's
// global_role is at or above minRole. Fail-closed: missing or invalid role → 403.
func RequirePlatformAdmin(minRole iamdomain.GlobalRole) gin.HandlerFunc {
 return func(c *gin.Context) {
  roleVal, _ := c.Get(ctxGlobalRole)
  role, _ := roleVal.(string)
  if !iamdomain.GlobalRole(role).AtLeast(minRole) {
   c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
    "code":    http.StatusForbidden,
    "message": "insufficient platform role",
   })
   return
  }
  c.Next()
 }
}

// RequireSystemAdmin requires at least system_admin platform role.
func RequireSystemAdmin() gin.HandlerFunc {
 return RequirePlatformAdmin(iamdomain.GlobalRoleSystemAdmin)
}

// RequireGlobalAdmin aborts with 403 unless the request context has
// global_role == "global_admin".
func RequireGlobalAdmin() gin.HandlerFunc {
 return RequirePlatformAdmin(iamdomain.GlobalRoleGlobalAdmin)
}
```

> 注意：错误 message 从 `"global admin role required"` 变为 `"insufficient platform role"`。同步检查 `api/http/contract_test.go` 及 golden 中是否断言该 message；若断言则更新。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./api/middleware/ -run 'TestRequirePlatformAdmin|TestRequireGlobalAdminDelegates' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add api/middleware/require_role.go api/middleware/require_role_test.go && \
git commit -m "feat(iam): add RequirePlatformAdmin middleware

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: AdminUserRepo 端口与实现

**Files:**

- Modify: `internal/iam/domain/errors.go`（追加 `ErrUserNotFound`、`ErrForbidden`）
- Create: `internal/iam/domain/port/admin_user_repo.go`
- Create: `internal/iam/infrastructure/persistence/admin_user_repo.go`
- Test: `internal/iam/infrastructure/persistence/admin_user_repo_test.go`

**Interfaces:**

- Consumes: `domain.GlobalRole`（Task 1）、`persistence.pgxPool`（`onboard_repo.go:19` 已有接口）。
- Produces: `port.AdminUser` struct、`port.AdminUserRepo` 接口、`persistence.NewAdminUserRepo(pool)`。Task 4/6 依赖。

- [ ] **Step 1: 追加 domain 错误** `internal/iam/domain/errors.go`

```go
 ErrUserNotFound = errors.New("iam: user not found")
 ErrForbidden    = errors.New("iam: forbidden")
```

- [ ] **Step 2: 写失败测试** `internal/iam/infrastructure/persistence/admin_user_repo_test.go`

用 pgxmock（同 `onboard_repo_test.go` 模式）。骨架：

```go
package persistence

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/iam/domain"
 "github.com/pashagolub/pgxmock/v4"
)

func TestAdminUserRepoSearchExcludesGuestAndAdmin(t *testing.T) {
 mock, err := pgxmock.NewPool()
 if err != nil { t.Fatal(err) }
 repo := NewAdminUserRepo(mock)

 mock.ExpectQuery(`SELECT u.id, COALESCE\(u.username, ''\), u.github_login, COALESCE\(u.avatar_url, ''\)`).
  WithArgs("%alice%", 20).
  WillReturnRows(pgxmock.NewRows([]string{"id", "username", "github_login", "avatar_url"}).
   AddRow("u1", "alice", "alice-dev", nil))
 got, err := repo.SearchUsers(context.Background(), "alice", 20)
 if err != nil { t.Fatal(err) }
 if len(got) != 1 || got[0].UserID != "u1" || got[0].GitHubLogin != "alice-dev" {
  t.Fatalf("unexpected result: %+v", got)
 }
}

func TestAdminUserRepoSetAdminRoleMissing(t *testing.T) {
 mock, _ := pgxmock.NewPool()
 repo := NewAdminUserRepo(mock)
 mock.ExpectExec(`UPDATE public.users SET global_role = 'system_admin'`).
  WithArgs("ghost").WillReturnResult(pgxmock.NewResult("UPDATE", 0))
 if err := repo.SetAdminRole(context.Background(), "ghost"); err != domain.ErrUserNotFound {
  t.Fatalf("want ErrUserNotFound, got %v", err)
 }
}
```

（`ListAdmins`、`RemoveAdminRole`、`GetGlobalRole` 的 mock 用例参照同样模式：断言 SQL 含 `global_role IN ('system_admin','global_admin')` / `is_guest = false` 等关键过滤。）

- [ ] **Step 3: 运行确认失败**

Run: `go test ./internal/iam/infrastructure/persistence/ -run TestAdminUserRepo -v`
Expected: FAIL（`undefined: NewAdminUserRepo`）

- [ ] **Step 4: 实现** `internal/iam/domain/port/admin_user_repo.go`

```go
package port

import (
 "context"

 "github.com/byteBuilderX/stratum/internal/iam/domain"
)

// AdminUser is a platform-scoped user row, used by the platform-admin UI.
type AdminUser struct {
 UserID      string
 Username    string
 GitHubLogin string
 AvatarURL   string
 GlobalRole  domain.GlobalRole
}

// AdminUserRepo manages users.global_role for the platform-admin UI. It is a
// separate write channel from the programmatic OnboardRepo.SetGlobalRole
// (which seeds global_admin from env); its role writes are fixed to
// system_admin — signature-level guarantee that the UI cannot produce a super
// admin.
type AdminUserRepo interface {
 // SearchUsers returns non-guest, non-admin users matching query, newest first.
 SearchUsers(ctx context.Context, query string, limit int) ([]AdminUser, error)
 // ListAdmins returns all platform admins (system_admin + global_admin).
 ListAdmins(ctx context.Context) ([]AdminUser, error)
 // SetAdminRole promotes userID to system_admin. ErrUserNotFound if absent.
 SetAdminRole(ctx context.Context, userID string) error
 // RemoveAdminRole demotes userID back to user. ErrUserNotFound if absent.
 RemoveAdminRole(ctx context.Context, userID string) error
 // GetGlobalRole returns userID's current global_role.
 GetGlobalRole(ctx context.Context, userID string) (domain.GlobalRole, error)
}
```

实现 `internal/iam/infrastructure/persistence/admin_user_repo.go`：

```go
package persistence

import (
 "context"
 "errors"

 "github.com/jackc/pgx/v5"

 "github.com/byteBuilderX/stratum/internal/iam/domain"
 "github.com/byteBuilderX/stratum/internal/iam/domain/port"
)

// AdminUserRepo persists users.global_role for the platform-admin UI against
// the public schema. users is a public table, so methods use the pool directly
// (no execTenant).
type AdminUserRepo struct {
 pool pgxPool
}

// NewAdminUserRepo wires the pool.
func NewAdminUserRepo(pool pgxPool) *AdminUserRepo {
 return &AdminUserRepo{pool: pool}
}

func (r *AdminUserRepo) SearchUsers(ctx context.Context, query string, limit int) ([]port.AdminUser, error) {
 pattern := "%" + query + "%"
 rows, err := r.pool.Query(ctx,
  `SELECT u.id, COALESCE(u.username, ''), u.github_login, COALESCE(u.avatar_url, '')
     FROM public.users u
    WHERE u.is_guest = false
      AND u.global_role = 'user'
      AND (u.username ILIKE $1 OR u.github_login ILIKE $1)
    ORDER BY u.created_at DESC LIMIT $2`,
  pattern, limit)
 if err != nil {
  return nil, err
 }
 defer rows.Close()
 out := make([]port.AdminUser, 0)
 for rows.Next() {
  var u port.AdminUser
  if err := rows.Scan(&u.UserID, &u.Username, &u.GitHubLogin, &u.AvatarURL); err != nil {
   return nil, err
  }
  out = append(out, u)
 }
 return out, rows.Err()
}

func (r *AdminUserRepo) ListAdmins(ctx context.Context) ([]port.AdminUser, error) {
 rows, err := r.pool.Query(ctx,
  `SELECT u.id, COALESCE(u.username, ''), u.github_login, COALESCE(u.avatar_url, ''), u.global_role
     FROM public.users u
    WHERE u.global_role IN ('system_admin', 'global_admin')
      AND u.is_guest = false
    ORDER BY u.global_role DESC, u.created_at DESC`)
 if err != nil {
  return nil, err
 }
 defer rows.Close()
 out := make([]port.AdminUser, 0)
 for rows.Next() {
  var u port.AdminUser
  if err := rows.Scan(&u.UserID, &u.Username, &u.GitHubLogin, &u.AvatarURL, &u.GlobalRole); err != nil {
   return nil, err
  }
  out = append(out, u)
 }
 return out, rows.Err()
}

func (r *AdminUserRepo) SetAdminRole(ctx context.Context, userID string) error {
 tag, err := r.pool.Exec(ctx,
  "UPDATE public.users SET global_role = 'system_admin', updated_at = NOW() WHERE id = $1 AND is_guest = false",
  userID)
 if err != nil {
  return err
 }
 if tag.RowsAffected() == 0 {
  return domain.ErrUserNotFound
 }
 return nil
}

func (r *AdminUserRepo) RemoveAdminRole(ctx context.Context, userID string) error {
 tag, err := r.pool.Exec(ctx,
  "UPDATE public.users SET global_role = 'user', updated_at = NOW() WHERE id = $1",
  userID)
 if err != nil {
  return err
 }
 if tag.RowsAffected() == 0 {
  return domain.ErrUserNotFound
 }
 return nil
}

func (r *AdminUserRepo) GetGlobalRole(ctx context.Context, userID string) (domain.GlobalRole, error) {
 var role domain.GlobalRole
 err := r.pool.QueryRow(ctx,
  "SELECT global_role FROM public.users WHERE id = $1", userID).Scan(&role)
 if errors.Is(err, pgx.ErrNoRows) {
  return "", domain.ErrUserNotFound
 }
 return role, err
}
```

- [ ] **Step 5: 运行确认通过**

Run: `go test ./internal/iam/infrastructure/persistence/ -run TestAdminUserRepo -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/iam/domain/errors.go internal/iam/domain/port/admin_user_repo.go \
        internal/iam/infrastructure/persistence/admin_user_repo.go \
        internal/iam/infrastructure/persistence/admin_user_repo_test.go && \
git commit -m "feat(iam): add AdminUserRepo for platform-admin UI

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: AdminService 扩展

**Files:**

- Modify: `internal/iam/application/admin_service.go`
- Test: `internal/iam/application/admin_service_test.go`

**Interfaces:**

- Consumes: `port.AdminUserRepo`（Task 3）、`domain.GlobalRole`、`domain.ErrForbidden`/`ErrUserNotFound`。
- Produces: `AdminService.SearchUsers(ctx, query, limit) ([]port.AdminUser, error)`、`ListAdmins(ctx)`、`SetAdminRole(ctx, actorID, userID) error`、`RemoveAdminRole(ctx, actorID, userID) error`、option `WithUserRepo`。Task 6 依赖。

- [ ] **Step 1: 写失败测试** `internal/iam/application/admin_service_test.go`

用 stub 实现 `port.AdminUserRepo`（mock repo，不 mock service）。骨架：

```go
package application

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/iam/domain"
 "github.com/byteBuilderX/stratum/internal/iam/domain/port"
)

type stubAdminUserRepo struct {
 roles map[string]domain.GlobalRole
 set   map[string]int // userID -> 调用次数
}

func (s *stubAdminUserRepo) SearchUsers(context.Context, string, int) ([]port.AdminUser, error) { return nil, nil }
func (s *stubAdminUserRepo) ListAdmins(context.Context) ([]port.AdminUser, error)               { return nil, nil }
func (s *stubAdminUserRepo) GetGlobalRole(_ context.Context, id string) (domain.GlobalRole, error) {
 role, ok := s.roles[id]
 if !ok { return "", domain.ErrUserNotFound }
 return role, nil
}
func (s *stubAdminUserRepo) SetAdminRole(_ context.Context, id string) error {
 if s.roles[id] == domain.GlobalRoleGlobalAdmin { return domain.ErrForbidden }
 s.roles[id] = domain.GlobalRoleSystemAdmin
 return nil
}
func (s *stubAdminUserRepo) RemoveAdminRole(_ context.Context, id string) error {
 s.roles[id] = domain.GlobalRoleUser
 return nil
}

func TestAdminServiceSetAdminRole(t *testing.T) {
 repo := &stubAdminUserRepo{roles: map[string]domain.GlobalRole{
  "super": domain.GlobalRoleGlobalAdmin,
  "alice": domain.GlobalRoleUser,
 }}
 svc := NewAdminService(nil, WithUserRepo(repo))

 // 非 super actor 被拒绝。
 if err := svc.SetAdminRole(context.Background(), "alice", "bob"); err != domain.ErrForbidden {
  t.Fatalf("non-admin actor: want ErrForbidden, got %v", err)
 }
 // super 提升 alice 成功。
 if err := svc.SetAdminRole(context.Background(), "super", "alice"); err != nil {
  t.Fatalf("promote: %v", err)
 }
 // 禁止把 super 本身作为目标。
 if err := svc.SetAdminRole(context.Background(), "super", "super"); err != domain.ErrForbidden {
  t.Fatalf("touch super target: want ErrForbidden, got %v", err)
 }
}

func TestAdminServiceRemoveAdminRole(t *testing.T) {
 repo := &stubAdminUserRepo{roles: map[string]domain.GlobalRole{
  "super": domain.GlobalRoleGlobalAdmin,
  "admin": domain.GlobalRoleSystemAdmin,
 }}
 svc := NewAdminService(nil, WithUserRepo(repo))
 // 禁止移除 super（含 actor 自己）。
 if err := svc.RemoveAdminRole(context.Background(), "super", "super"); err != domain.ErrForbidden {
  t.Fatalf("self: want ErrForbidden, got %v", err)
 }
 // 移除普通 admin 成功。
 if err := svc.RemoveAdminRole(context.Background(), "super", "admin"); err != nil {
  t.Fatalf("remove: %v", err)
 }
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/iam/application/ -run TestAdminService -v`
Expected: FAIL（`undefined: WithUserRepo` / `undefined field`）

- [ ] **Step 3: 实现** 修改 `internal/iam/application/admin_service.go`

struct 增加字段，import 增加 `port` 与 `errors`/`fmt`：

```go
type AdminService struct {
 repo             port.AdminTenantRepo
 userRepo         port.AdminUserRepo
 // ... 其余字段不变
}

// WithUserRepo sets the platform-admin user repository.
func WithUserRepo(r port.AdminUserRepo) AdminServiceOption {
 return func(s *AdminService) { s.userRepo = r }
}
```

文件末尾追加方法（import `fmt`、`port`）：

```go
// SearchUsers returns non-guest, non-admin users as promotion candidates.
func (s *AdminService) SearchUsers(ctx context.Context, query string, limit int) ([]port.AdminUser, error) {
 if s.userRepo == nil {
  return nil, domain.ErrUserRepoUnavailable
 }
 return s.userRepo.SearchUsers(ctx, query, limit)
}

// ListAdmins returns all platform admins with their role.
func (s *AdminService) ListAdmins(ctx context.Context) ([]port.AdminUser, error) {
 if s.userRepo == nil {
  return nil, domain.ErrUserRepoUnavailable
 }
 return s.userRepo.ListAdmins(ctx)
}

// SetAdminRole promotes a user to system_admin. Only a global_admin actor may
// promote, and only non-guest, non-global-admin targets.
func (s *AdminService) SetAdminRole(ctx context.Context, actorID, userID string) error {
 if s.userRepo == nil {
  return domain.ErrUserRepoUnavailable
 }
 actorRole, err := s.userRepo.GetGlobalRole(ctx, actorID)
 if err != nil {
  return fmt.Errorf("set admin role: actor check: %w", err)
 }
 if actorRole != domain.GlobalRoleGlobalAdmin {
  return domain.ErrForbidden
 }
 targetRole, err := s.userRepo.GetGlobalRole(ctx, userID)
 if err != nil {
  return err
 }
 if targetRole == domain.GlobalRoleGlobalAdmin {
  return domain.ErrForbidden // never touch a super admin
 }
 return s.userRepo.SetAdminRole(ctx, userID)
}

// RemoveAdminRole demotes a system_admin back to user. The actor must be a
// global_admin; the target must not be one (including the actor themself).
func (s *AdminService) RemoveAdminRole(ctx context.Context, actorID, userID string) error {
 if s.userRepo == nil {
  return domain.ErrUserRepoUnavailable
 }
 actorRole, err := s.userRepo.GetGlobalRole(ctx, actorID)
 if err != nil {
  return fmt.Errorf("remove admin role: actor check: %w", err)
 }
 if actorRole != domain.GlobalRoleGlobalAdmin {
  return domain.ErrForbidden
 }
 targetRole, err := s.userRepo.GetGlobalRole(ctx, userID)
 if err != nil {
  return err
 }
 if targetRole == domain.GlobalRoleGlobalAdmin {
  return domain.ErrForbidden // never touch a super admin (incl. self)
 }
 return s.userRepo.RemoveAdminRole(ctx, userID)
}
```

在 `internal/iam/domain/errors.go` 追加 `ErrUserRepoUnavailable = errors.New("iam: user repo unavailable")`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/iam/application/ -run TestAdminService -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/iam/application/admin_service.go internal/iam/application/admin_service_test.go internal/iam/domain/errors.go && \
git commit -m "feat(iam): AdminService platform-admin methods

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: proto DTO 与生成

**Files:**

- Modify: `proto/admin/admin.proto`
- Generate: `make proto-gen`（产出 `api/http/dto/gen/admin.go`、`web/src/services/gen/`）

**Interfaces:**

- Produces: `gen.SearchUsersRequest/Response`、`gen.AdminUserResponse`、`gen.ListAdminsResponse`、`gen.SetAdminRoleRequest`。Task 6/8 依赖。

- [ ] **Step 1: 追加 proto message** 到 `proto/admin/admin.proto` 末尾

```proto
message SearchUsersRequest {
  string query = 1;
  int32 limit = 2;
}

message AdminUserResponse {
  string user_id = 1;
  string username = 2;
  string github_login = 3;
  // @omitempty
  optional string avatar_url = 4;
  string global_role = 5;
}

message SearchUsersResponse {
  repeated AdminUserResponse users = 1;
}

message ListAdminsResponse {
  repeated AdminUserResponse admins = 1;
}

message SetAdminRoleRequest {
  // @binding: required
  string user_id = 1;
}
```

- [ ] **Step 2: 生成 DTO**

Run: `make proto-gen`
Expected: `api/http/dto/gen/admin.go` 与 `web/src/services/gen/` 更新（生成物不入 git）

- [ ] **Step 3: 编译确认**

Run: `go build ./...`
Expected: 成功

- [ ] **Step 4: Commit**（只提交 proto，不提交生成物）

```bash
git add proto/admin/admin.proto && \
git commit -m "feat(admin): platform-admin DTO contracts

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: AdminHandler 端点与路由守卫拆分

**Files:**

- Modify: `api/http/handler/admin_handler.go`
- Modify: `api/http/router.go`（adminGroup 守卫拆分 + 新路由 + audit 守卫）
- Modify: `api/wiring/iam.go`（AdminService 装配 userRepo）
- Modify: `api/http/contract_test.go` + `api/http/testdata/contracts/*.golden.json`（admin 路径注册 + golden 更新）

**Interfaces:**

- Consumes: `AdminService`（Task 4）、`gen.*`（Task 5）、`middleware.RequireSystemAdmin/RequireGlobalAdmin`（Task 2）、`middleware.ContextKeySub`。
- Produces: 4 个新端点路由。

- [ ] **Step 1: 装配 userRepo** `api/wiring/iam.go`（`NewAdminService` 调用处追加 option）

```go
iam.AdminService = application.NewAdminService(
 iampersistence.NewAdminTenantRepo(db),
 application.WithUserRepo(iampersistence.NewAdminUserRepo(db)),
 opts...,
)
```

- [ ] **Step 2: 写 handler** `api/http/handler/admin_handler.go` 追加方法

```go
// SearchUsers GET /admin/users?query=&limit= — 平台管理员候选用户搜索。
func (h *AdminHandler) SearchUsers(c *gin.Context) {
 limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
 if limit < 1 || limit > constants.MaxPageSize {
  limit = constants.DefaultPageSize
 }
 users, err := h.svc.SearchUsers(c.Request.Context(), c.Query("query"), limit)
 if err != nil {
  _ = c.Error(err)
  return
 }
 resp := make([]gen.AdminUserResponse, 0, len(users))
 for _, u := range users {
  resp = append(resp, gen.AdminUserResponse{
   UserId: u.UserID, Username: u.Username, GithubLogin: u.GitHubLogin, AvatarUrl: &u.AvatarURL, GlobalRole: string(u.GlobalRole),
  })
 }
 c.JSON(http.StatusOK, gen.SearchUsersResponse{Users: resp})
}

// ListAdmins GET /admin/admins — 全部平台管理员列表。
func (h *AdminHandler) ListAdmins(c *gin.Context) {
 admins, err := h.svc.ListAdmins(c.Request.Context())
 if err != nil {
  _ = c.Error(err)
  return
 }
 resp := make([]gen.AdminUserResponse, 0, len(admins))
 for _, u := range admins {
  resp = append(resp, gen.AdminUserResponse{
   UserId: u.UserID, Username: u.Username, GithubLogin: u.GitHubLogin, AvatarUrl: &u.AvatarURL, GlobalRole: string(u.GlobalRole),
  })
 }
 c.JSON(http.StatusOK, gen.ListAdminsResponse{Admins: resp})
}

// SetAdminRole POST /admin/admins {user_id} — 提升为普通平台管理员。
func (h *AdminHandler) SetAdminRole(c *gin.Context) {
 var req gen.SetAdminRoleRequest
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 actorID := c.GetString(middleware.ContextKeySub)
 if err := h.svc.SetAdminRole(c.Request.Context(), actorID, req.UserId); err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, gin.H{"message": "admin role set"})
}

// RemoveAdminRole DELETE /admin/admins/:user_id — 移除普通平台管理员。
func (h *AdminHandler) RemoveAdminRole(c *gin.Context) {
 actorID := c.GetString(middleware.ContextKeySub)
 if err := h.svc.RemoveAdminRole(c.Request.Context(), actorID, c.Param("user_id")); err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, gin.H{"message": "admin role removed"})
}
```

> `gen.AdminUserResponse.AvatarUrl` 是 `*string`（optional）。空串时 handler 传 `&u.AvatarURL` 即可，生成代码带 omitempty。若生成字段类型不同以实际 gen 代码为准。

- [ ] **Step 3: 路由守卫拆分** `api/http/router.go`

现状（278 行）：`adminGroup := r.Group("/admin", jwtMW, middleware.RequireGlobalAdmin())`。

改为基础组降为 `RequireSystemAdmin()`，把高敏感路由拆出：

```go
// /admin 常规后台：system_admin 及以上（租户管理、参数、memory DLQ）。
adminGroup := r.Group("/admin", jwtMW, middleware.RequireSystemAdmin())
{
 adminGroup.GET("/tenants", adminHandler.ListTenants)
 adminGroup.POST("/tenants", adminHandler.CreateTenant)
 adminGroup.GET("/tenants/:id", adminHandler.GetTenant)
 adminGroup.PATCH("/tenants/:id", adminHandler.UpdateTenant)
 registerParameterAdminRoutes(adminGroup, c)
 registerMemoryDLQAdminRoutes(adminGroup, c)

 // 高敏感：删除租户 + 平台管理员管理 → 仅 global_admin。
 adminGroup.DELETE("/tenants/:id", middleware.RequireGlobalAdmin(), adminHandler.DeleteTenant)

 adminAdmins := adminGroup.Group("/admins", middleware.RequireGlobalAdmin())
 {
  adminAdmins.GET("", adminHandler.ListAdmins)
  adminAdmins.POST("", adminHandler.SetAdminRole)
  adminAdmins.DELETE("/:user_id", adminHandler.RemoveAdminRole)
 }

 // 用户搜索：供提升选择候选，system_admin 可见（候选不含管理员）。
 adminGroup.GET("/users", adminHandler.SearchUsers)
}
```

> 若原 `adminGroup` 是扁平的 `{ ... }` 代码块而非嵌套分组，保持最小改动：基础组用 `RequireSystemAdmin()`，`DELETE /tenants/:id` 与 `/admins` 路由在挂载时各自追加 `middleware.RequireGlobalAdmin()`。

同时 642 行平台审计改为 system_admin 可见：

```go
platformAudit := r.Group("/admin/audit/platform",
 middleware.JWTMiddleware(c.Platform.JWTService, c.Platform.Metrics),
 middleware.RequireSystemAdmin())
```

- [ ] **Step 4: 契约测试注册 + golden 更新** `api/http/contract_test.go`

将 `/admin/users`、`/admin/admins` 加入 admin 路径集合（164 行附近），并按现有模式为新端点加用例；运行契约测试后如有 golden 漂移则 `UPDATE_GOLDEN=1 go test ./api/http/` 刷新（golden 是仓库文件，改动随 commit 提交）。

- [ ] **Step 5: 验证**

Run: `go build ./... && go vet ./api/... ./internal/iam/...`
Expected: 成功

Run: `go test -short ./api/http/ ./api/middleware/ ./internal/iam/...`
Expected: PASS（契约 golden 已更新）

- [ ] **Step 6: Commit**

```bash
git add api/http/handler/admin_handler.go api/http/router.go api/wiring/iam.go \
        api/http/contract_test.go api/http/testdata/contracts/ && \
git commit -m "feat(admin): platform-admin endpoints and guard split

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 7: 后端验证

- [ ] **Step 1: 全量编译与 vet**

Run: `go vet ./...`
Expected: PASS

- [ ] **Step 2: 短测试**

Run: `go test -short ./...`
Expected: PASS

- [ ] **Step 3: 质量门禁（增量棘轮）**

Run: `make code-quality`
Expected: 新代码圈复杂度 ≤10、认知复杂度 ≤15、行数 ≤120、嵌套 ≤4。

- [ ] **Step 4: 风险守卫**

Run: `make risk-guardrails`
Expected: PASS

- [ ] **Step 5: Commit（如有格式化/质量微调）**

---

### Task 8: 前端权限层与 API

**Files:**

- Create: `web/src/modules/iam/hooks/usePlatformRole.ts`
- Modify: `web/src/modules/iam/components/PrivateRoute.tsx`（platform 判定迁移到 `user.global_role`）
- Modify: `web/src/modules/iam/api/tenant.api.ts`（新增 `platformAdminApi`）
- Test: `web/src/modules/iam/hooks/usePlatformRole.test.ts`、`web/src/modules/iam/components/PrivateRoute.test.tsx`

**Interfaces:**

- Consumes: `gen` 前端类型（`web/src/services/gen/`，Task 5）、`useAuth`。
- Produces: `usePlatformRole()` hook、`platformAdminApi`、`PrivateRoute` 统一读 `global_role`。

- [ ] **Step 1: 写失败测试** `usePlatformRole.test.ts`

用 AuthContext mock 注入不同 `user.global_role`，断言 `isSystemAdmin`/`isGlobalAdmin`/`hasPlatformRole`（参照 `useTenantRole` 现有测试风格）。

- [ ] **Step 2: 实现 `usePlatformRole.ts`**

```ts
import { useAuth } from '@/modules/iam';

// 平台角色层级，与后端 middleware.RequirePlatformAdmin 一致：global_admin > system_admin > user。
const PLATFORM_ROLE_RANK: Record<string, number> = { user: 1, system_admin: 2, global_admin: 3 };

export interface PlatformRoleInfo {
  /** 当前平台角色，取自 user.global_role，默认 user。 */
  role: string;
  /** system_admin 或 global_admin。 */
  isSystemAdmin: boolean;
  /** 仅 global_admin。 */
  isGlobalAdmin: boolean;
  /** 判断是否达到某最低平台角色要求。 */
  hasPlatformRole: (min: string) => boolean;
}

/**
 * usePlatformRole 统一读取平台角色（users.global_role），供平台管理入口的权限隐藏使用。
 * 与 useTenantRole 完全脱钩（租户一个体系，平台一个体系）。
 */
export const usePlatformRole = (): PlatformRoleInfo => {
  const { user } = useAuth();
  const role = user?.global_role ?? 'user';
  const rank = PLATFORM_ROLE_RANK[role] ?? 0;
  return {
    role,
    isSystemAdmin: rank >= PLATFORM_ROLE_RANK.system_admin,
    isGlobalAdmin: rank >= PLATFORM_ROLE_RANK.global_admin,
    hasPlatformRole: (min: string) => rank >= (PLATFORM_ROLE_RANK[min] ?? 0),
  };
};
```

- [ ] **Step 3: 迁移 `PrivateRoute.tsx`**

替换 50-58 行的 requiredRole 判定为统一读 `user.global_role`（移除 `system_role` 依赖）：

```tsx
if (requiredRole) {
  const platformRank: Record<string, number> = { user: 1, system_admin: 2, global_admin: 3 };
  const role = user.global_role ?? 'user';
  const ok = (platformRank[role] ?? 0) >= (platformRank[requiredRole] ?? 0);
  if (!ok) { /* 现有 403 Result 逻辑不变 */ }
}
```

同步检查 `PrivateRoute.test.tsx` 中 `system_admin` 断言，改用 `user.global_role: 'system_admin'` 作为输入。

- [ ] **Step 4: 新增 `platformAdminApi`**（追加到 `tenant.api.ts`）

```ts
const adminUserSchema = z.object({
  user_id: z.string(),
  username: z.string(),
  github_login: z.string(),
  avatar_url: z.string().optional(),
  global_role: z.string().optional(),
});
const searchUsersSchema = z.object({ users: z.array(adminUserSchema) });
const listAdminsSchema = z.object({ admins: z.array(adminUserSchema) });

export type AdminUser = z.infer<typeof adminUserSchema>;

export const platformAdminApi = {
  searchUsers: async (query: string, limit = 20): Promise<AdminUser[]> => {
    const res = await api.get('/admin/users', { params: { query, limit } });
    return searchUsersSchema.parse(res.data).users;
  },
  listAdmins: async (): Promise<AdminUser[]> => {
    const res = await api.get('/admin/admins');
    return listAdminsSchema.parse(res.data).admins;
  },
  setAdminRole: (userId: string) => api.post('/admin/admins', { user_id: userId }),
  removeAdminRole: (userId: string) => api.delete(`/admin/admins/${userId}`),
};
```

- [ ] **Step 5: 验证**

Run: `cd web && npx vitest run src/modules/iam/hooks/usePlatformRole.test.ts src/modules/iam/components/PrivateRoute.test.tsx`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add web/src/modules/iam/hooks/usePlatformRole.ts web/src/modules/iam/components/PrivateRoute.tsx \
        web/src/modules/iam/api/tenant.api.ts \
        web/src/modules/iam/hooks/usePlatformRole.test.ts web/src/modules/iam/components/PrivateRoute.test.tsx && \
git commit -m "feat(iam): platform-role hook and PrivateRoute migration

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 9: AdminsPage 页面、路由与菜单

**Files:**

- Create: `web/src/modules/iam/pages/admin/AdminsPage.tsx`
- Modify: `web/src/modules/iam/routes.tsx`（`/admin/admins`）
- Modify: `web/src/app/layout/menu.config.tsx`（平台管理组对 system_admin 可见，`/admin/admins` 仅 global_admin）
- Test: `web/src/modules/iam/pages/admin/__tests__/AdminsPage.test.tsx`

**Interfaces:**

- Consumes: `platformAdminApi`（Task 8）、`usePlatformRole`、`useAuth`。

- [ ] **Step 1: 写失败测试** `AdminsPage.test.tsx`

mock `platformAdminApi`：`listAdmins` 返回一个 `global_admin` + 一个 `system_admin`；断言 global_admin 行无操作按钮、system_admin 行有移除按钮；空列表显示 Empty。

- [ ] **Step 2: 实现 `AdminsPage.tsx`**

```tsx
import { UserAddOutlined } from '@ant-design/icons';
import { App, Button, Empty, Modal, Select, Space, Table, Tag, Typography } from 'antd';
import { useEffect, useState } from 'react';

import { usePlatformRole } from '../../hooks/usePlatformRole';
import { platformAdminApi, type AdminUser } from '../../api/tenant.api';

const { Title, Text } = Typography;

const ROLE_TAG: Record<string, { color: string; label: string }> = {
  global_admin: { color: 'gold', label: '超级管理员' },
  system_admin: { color: 'blue', label: '平台管理员' },
};

export const AdminsPage = () => {
  const { message, modal } = App.useApp();
  const { user } = usePlatformRole(); // role 用于自身展示，页面守卫已保证 global_admin 可进
  const [admins, setAdmins] = useState<AdminUser[]>([]);
  const [loading, setLoading] = useState(false);
  const [createOpen, setCreateOpen] = useState(false);
  const [createLoading, setCreateLoading] = useState(false);
  const [searchValue, setSearchValue] = useState('');
  const [candidates, setCandidates] = useState<AdminUser[]>([]);
  const [selected, setSelected] = useState<string>();

  const fetchAdmins = async () => {
    setLoading(true);
    try {
      setAdmins(await platformAdminApi.listAdmins());
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchAdmins();
  }, []);

  const handleSearch = async (q: string) => {
    setSearchValue(q);
    if (!q.trim()) {
      setCandidates([]);
      return;
    }
    setCandidates(await platformAdminApi.searchUsers(q.trim()));
  };

  const handleCreate = () => {
    if (!selected) return;
    modal.confirm({
      title: '提升为平台管理员',
      content: '确认将该用户提升为普通平台管理员？超级管理员不受此操作影响。',
      onOk: async () => {
        setCreateLoading(true);
        try {
          await platformAdminApi.setAdminRole(selected);
          message.success({ content: '已提升为平台管理员', duration: 2 });
          setCreateOpen(false);
          setSelected(undefined);
          setSearchValue('');
          setCandidates([]);
          await fetchAdmins();
        } catch (err: any) {
          message.error({ content: err.response?.data?.error || '操作失败', duration: 3 });
        } finally {
          setCreateLoading(false);
        }
      },
    });
  };

  const handleRemove = (record: AdminUser) => {
    modal.confirm({
      title: '移除平台管理员',
      content: `确认移除「${record.username || record.github_login}」的平台管理员权限？其用户账号不受影响。`,
      onOk: async () => {
        try {
          await platformAdminApi.removeAdminRole(record.user_id);
          message.success({ content: '已移除平台管理员', duration: 2 });
          await fetchAdmins();
        } catch (err: any) {
          message.error({ content: err.response?.data?.error || '操作失败', duration: 3 });
        }
      },
    });
  };

  const columns = [
    { title: '用户名', dataIndex: 'username', key: 'username', render: (v: string, r: AdminUser) => v || r.github_login },
    { title: 'GitHub', dataIndex: 'github_login', key: 'github_login' },
    {
      title: '档位',
      dataIndex: 'global_role',
      key: 'global_role',
      render: (role: string) => {
        const cfg = ROLE_TAG[role] ?? { color: 'default', label: role };
        return <Tag color={cfg.color}>{cfg.label}</Tag>;
      },
    },
    {
      title: '操作',
      key: 'actions',
      render: (_: unknown, record: AdminUser) =>
        record.global_role === 'global_admin' ? (
          <Text type="secondary">超级管理员仅程序预设</Text>
        ) : (
          <Button danger size="small" onClick={() => handleRemove(record)}>
            移除
          </Button>
        ),
    },
  ];

  return (
    <div>
      <div className="responsive-page-header" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 20 }}>
        <div>
          <Title level={4} style={{ margin: 0 }}>平台管理员</Title>
          <Text type="secondary" style={{ fontSize: 13 }}>管理普通平台管理员；超级管理员仅由部署配置预设</Text>
        </div>
        <Button type="primary" icon={<UserAddOutlined />} onClick={() => setCreateOpen(true)}>
          添加平台管理员
        </Button>
      </div>

      <Table<AdminUser> rowKey="user_id" columns={columns} dataSource={admins} loading={loading} pagination={false} locale={{ emptyText: <Empty description="平台管理员还是空的" /> }} />

      <Modal
        title="添加平台管理员"
        open={createOpen}
        onCancel={() => setCreateOpen(false)}
        onOk={handleCreate}
        okText="提升"
        confirmLoading={createLoading}
        okButtonProps={{ disabled: !selected }}
      >
        <Space direction="vertical" style={{ width: '100%' }}>
          <Text type="secondary">搜索并选择要提升的用户（普通用户，不含 guest 与已有管理员）</Text>
          <Select
            showSearch
            filterOption={false}
            onSearch={handleSearch}
            onSelect={(v: string) => setSelected(v)}
            value={selected}
            placeholder="输入用户名或 GitHub 登录名搜索"
            style={{ width: '100%' }}
            options={candidates.map((u) => ({
              value: u.user_id,
              label: `${u.username || u.github_login}${u.github_login ? ` (${u.github_login})` : ''}`,
            }))}
          />
          {searchValue && !candidates.length && <Text type="secondary">没有找到匹配的用户</Text>}
        </Space>
      </Modal>
    </div>
  );
};

export default AdminsPage;
```

- [ ] **Step 3: 路由** `web/src/modules/iam/routes.tsx` 追加

```tsx
<Route
  key="admin-admins"
  path="/admin/admins"
  element={
    <PrivateRoute requiredRole="global_admin">
      <AdminsPage />
    </PrivateRoute>
  }
/>,
```

并 import `AdminsPage`。

- [ ] **Step 4: 菜单** `web/src/app/layout/menu.config.tsx`

平台管理组可见性从 `user?.global_role === 'global_admin'` 放宽为 platform role 非 user（system_admin 也可见常规后台），且 `/admin/admins` 子项仅 global_admin：

```tsx
if (user?.global_role && user.global_role !== 'user') {
  base.push({
    key: 'platform-admin-group',
    icon: <SettingOutlined />,
    label: '平台管理',
    children: [
      { key: '/models', icon: <ApiOutlined />, label: '模型管理' },
      { key: '/admin/tenants', icon: <GlobalOutlined />, label: '全局租户' },
      { key: '/admin/settings', icon: <SettingOutlined />, label: '平台参数' },
      ...(user.global_role === 'global_admin'
        ? [{ key: '/admin/admins', icon: <SafetyCertificateOutlined />, label: '平台管理员' }]
        : []),
    ],
  });
}
```

> import 增加 `SafetyCertificateOutlined`。`/admin/settings` 路由是否存在需核对 `iam/routes.tsx`/app router（若不存在则菜单子项保持现状，不加新入口）。

- [ ] **Step 5: 验证**

Run: `cd web && npx vitest run src/modules/iam/pages/admin/__tests__/AdminsPage.test.tsx`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add web/src/modules/iam/pages/admin/AdminsPage.tsx web/src/modules/iam/routes.tsx \
        web/src/app/layout/menu.config.tsx \
        web/src/modules/iam/pages/admin/__tests__/AdminsPage.test.tsx && \
git commit -m "feat(iam): platform admins management page

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 10: 前端验证

- [ ] **Step 1: 前端全部单测**

Run: `cd web && npx vitest run`
Expected: PASS

- [ ] **Step 2: Lint**

Run: `make fe-lint`
Expected: PASS

- [ ] **Step 3: Build**

Run: `make fe-build`
Expected: PASS（TS 类型检查通过）

---

### Task 11: 系统验收、PR/CI 与 CD 部署

- [ ] **Step 1: 完整后端测试**

Run: `go test -v -race -timeout 30s ./...`
Expected: PASS

- [ ] **Step 2: 系统验收**

由 `stratum-e2e-tester` agent 封装 `stratum-e2e-development` skill 执行（含无头 Chromium e2e-short；R3 风险自动加 soak）。按 `.test/verification.yaml` 确定风险级。验收报告须全绿无 failed/skipped/unreconciled。

- [ ] **Step 3: 检查 base 是否落后**

```bash
git fetch origin main && git merge-base --is-ancestor origin/main HEAD || echo "BEHIND"
```

若落后：`git merge origin/main` → 本地验证无冲突且测试通过 → push。

- [ ] **Step 4: push 与 PR**

```bash
git push -u origin feat/platform-admin
gh pr create --base main --title "feat(iam): 平台管理员管理" --body "<What/Why/HowToTest>"
```

PR body 结尾：`🤖 Generated with [Claude Code](https://claude.com/claude-code)`。

- [ ] **Step 5: 等 CI 全绿**

CI 通过后合并 PR（`gh pr merge --squash`）。

- [ ] **Step 6: CD 部署**

合并后 CD 流水线自动构建部署到远端集群。验证：

- `kubectl rollout status deploy/... -n <ns>` 完成；
- 新页面 `/admin/admins` 可访问；
- 提升/移除一名 system_admin 后刷新 token 生效（`users.global_role` 变更可查）。

- [ ] **Step 7: 清理 worktree**

```bash
cd /home/yang/go-projects/stratum && git worktree remove ../stratum-platform-admin
```
