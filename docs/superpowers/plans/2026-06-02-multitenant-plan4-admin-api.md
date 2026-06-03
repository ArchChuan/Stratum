# Multi-Tenant Plan 4: 管理 API — 全局管理员与租户管理员

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现全局管理员租户管理 API、租户管理员成员管理 API，以及两层权限中间件。

**Architecture:** 权限中间件从 Gin context 读取由前序 auth 中间件注入的 `global_role` 和 `tenant_member_role` claim；Admin handler 用 pgxpool 直接操作 `public.tenants` 表（软删除），Tenant handler 操作 `public.tenant_members` 和 `public.invitations` 表；路由通过 `router.Group` 挂载并链接对应 middleware。

**Tech Stack:** Go 1.24, Gin v1.9, pgx/v5 (pgxpool), crypto/rand, go.uber.org/zap, github.com/google/uuid

---

## File Map

| 文件 | 操作 | 职责 |
|---|---|---|
| `api/middleware/require_role.go` | Create | RequireGlobalAdmin() 和 RequireTenantRole(role) 两个 middleware factory |
| `api/middleware/require_role_test.go` | Create | 上述两个 middleware 的单元测试 |
| `api/model/admin.go` | Create | 所有管理类 request/response DTO |
| `api/handler/admin_handler.go` | Create | 全局管理员 handler：ListTenants, GetTenant, CreateTenant, UpdateTenant, DeleteTenant |
| `api/handler/admin_handler_test.go` | Create | mock pgxpool 测试 ListTenants / CreateTenant / DeleteTenant |
| `api/handler/tenant_handler.go` | Create | 租户管理员 handler：ListMembers, InviteMember, UpdateMemberRole, RemoveMember, GetSettings, UpdateSettings |
| `api/handler/tenant_handler_test.go` | Create | mock pgxpool 测试 ListMembers / InviteMember / RemoveMember |
| `api/router.go` | Modify | 注册 `/admin/*` 和 `/tenant/*` 路由组，注入对应 middleware |
| `internal/config/config.go` | Modify | 添加 `PostgresDSN` 和 `FrontendURL` 字段 |

---

### Task 1: 权限中间件

**Files:**
- Create: `api/middleware/require_role.go`
- Create: `api/middleware/require_role_test.go`

约定：auth 中间件（Plan 3）已将以下键注入 Gin context：
- `"global_role"` — string，值为 `"global_admin"` 或空
- `"tenant_member_role"` — string，值为 `"owner"` / `"admin"` / `"member"` 或空

- [ ] **Step 1: 创建 require_role.go**

```go
// api/middleware/require_role.go
package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// RequireGlobalAdmin aborts with 403 unless the request context has global_role == "global_admin".
func RequireGlobalAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get("global_role")
		if role != "global_admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    http.StatusForbidden,
				"message": "global admin role required",
			})
			return
		}
		c.Next()
	}
}

// RequireTenantRole aborts with 403 unless tenant_member_role matches one of the allowed roles.
// Role hierarchy: owner > admin > member.
// Pass the minimum required role; all roles at or above that level are accepted.
// Allowed values for role: "member", "admin", "owner".
func RequireTenantRole(minRole string) gin.HandlerFunc {
	rank := map[string]int{"member": 1, "admin": 2, "owner": 3}
	required := rank[minRole]

	return func(c *gin.Context) {
		roleVal, _ := c.Get("tenant_member_role")
		roleStr, _ := roleVal.(string)
		if rank[roleStr] < required {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    http.StatusForbidden,
				"message": "insufficient tenant role",
			})
			return
		}
		c.Next()
	}
}
```

- [ ] **Step 2: 创建 require_role_test.go**

```go
// api/middleware/require_role_test.go
package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newTestEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	return gin.New()
}

func TestRequireGlobalAdmin_allowed(t *testing.T) {
	r := newTestEngine()
	r.GET("/", func(c *gin.Context) { c.Set("global_role", "global_admin") }, RequireGlobalAdmin(), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequireGlobalAdmin_denied(t *testing.T) {
	r := newTestEngine()
	r.GET("/", RequireGlobalAdmin(), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequireTenantRole_ownerAllowedForAdmin(t *testing.T) {
	r := newTestEngine()
	r.GET("/", func(c *gin.Context) { c.Set("tenant_member_role", "owner") }, RequireTenantRole("admin"), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
}

func TestRequireTenantRole_memberDeniedForAdmin(t *testing.T) {
	r := newTestEngine()
	r.GET("/", func(c *gin.Context) { c.Set("tenant_member_role", "member") }, RequireTenantRole("admin"), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}

func TestRequireTenantRole_noRoleDenied(t *testing.T) {
	r := newTestEngine()
	r.GET("/", RequireTenantRole("member"), func(c *gin.Context) { c.Status(http.StatusOK) })
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}
```

- [ ] **Step 3: 运行测试，验证通过**

```bash
go test -v -race ./api/middleware/... -short -run "TestRequireGlobalAdmin|TestRequireTenantRole"
```

期望输出：所有 4 个测试 PASS。

- [ ] **Step 4: Commit**

```bash
git add api/middleware/require_role.go api/middleware/require_role_test.go
git commit -m "feat(middleware): add RequireGlobalAdmin and RequireTenantRole middleware"
```

---

### Task 2: Admin DTO

**Files:**
- Create: `api/model/admin.go`

- [ ] **Step 1: 创建 admin.go**

```go
// api/model/admin.go
package model

import "time"

// --- Tenant DTOs ---

// CreateTenantRequest is the body for POST /admin/tenants.
type CreateTenantRequest struct {
	Name   string `json:"name" binding:"required"`
	Slug   string `json:"slug" binding:"required"`
	Plan   string `json:"plan" binding:"required,oneof=free pro enterprise"`
	Status string `json:"status" binding:"required,oneof=active suspended"`
}

// UpdateTenantRequest is the body for PATCH /admin/tenants/:id.
// Only Plan and Status are settable by global admin via this endpoint.
type UpdateTenantRequest struct {
	Plan   string `json:"plan" binding:"omitempty,oneof=free pro enterprise"`
	Status string `json:"status" binding:"omitempty,oneof=active suspended"`
}

// TenantResponse is the shape returned by all tenant endpoints.
type TenantResponse struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Slug      string     `json:"slug"`
	Plan      string     `json:"plan"`
	Status    string     `json:"status"`
	CreatedAt time.Time  `json:"created_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// ListTenantsResponse wraps paginated tenant results.
type ListTenantsResponse struct {
	Tenants  []TenantResponse `json:"tenants"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

// --- Member DTOs ---

// InviteMemberRequest is the body for POST /tenant/members/invite.
type InviteMemberRequest struct {
	Email string `json:"email" binding:"required,email"`
	Role  string `json:"role" binding:"required,oneof=member admin owner"`
}

// InviteMemberResponse is returned after creating an invitation.
type InviteMemberResponse struct {
	InvitationID  string    `json:"invitation_id"`
	Email         string    `json:"email"`
	Role          string    `json:"role"`
	InvitationURL string    `json:"invitation_url"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// UpdateMemberRoleRequest is the body for PATCH /tenant/members/:user_id/role.
type UpdateMemberRoleRequest struct {
	Role string `json:"role" binding:"required,oneof=member admin owner"`
}

// MemberResponse represents a single tenant member.
type MemberResponse struct {
	UserID    string    `json:"user_id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	JoinedAt  time.Time `json:"joined_at"`
}

// ListMembersResponse wraps paginated member results.
type ListMembersResponse struct {
	Members  []MemberResponse `json:"members"`
	Total    int              `json:"total"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

// --- Settings DTOs ---

// UpdateSettingsRequest is the body for PATCH /tenant/settings.
// Settings is an arbitrary JSON object stored in tenant.settings JSONB column.
type UpdateSettingsRequest struct {
	Settings map[string]interface{} `json:"settings" binding:"required"`
}

// SettingsResponse wraps the current settings JSONB.
type SettingsResponse struct {
	TenantID string                 `json:"tenant_id"`
	Settings map[string]interface{} `json:"settings"`
}
```

- [ ] **Step 2: 确认编译无误**

```bash
go build ./api/model/...
```

期望：无输出（编译成功）。

- [ ] **Step 3: Commit**

```bash
git add api/model/admin.go
git commit -m "feat(model): add admin and tenant management DTOs"
```

---

### Task 3: Admin Handler

**Files:**
- Create: `api/handler/admin_handler.go`

前置条件：`internal/config/config.go` 需要添加 `PostgresDSN string`。

- [ ] **Step 1: 向 Config 添加 PostgresDSN 字段**

编辑 `/home/yang/go-projects/ClawHermes-AI-Go/internal/config/config.go`，在 `Config` struct 添加：

```go
PostgresDSN string
FrontendURL string
```

在 `Load()` 函数添加对应的 `getEnv` 调用：

```go
PostgresDSN: getEnv("POSTGRES_DSN", "postgres://postgres:postgres@localhost:5432/clawhermes?sslmode=disable"),
FrontendURL: getEnv("FRONTEND_URL", "http://localhost:3000"),
```

- [ ] **Step 2: 创建 admin_handler.go**

```go
// api/handler/admin_handler.go
package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type AdminHandler struct {
	db     *pgxpool.Pool
	logger *zap.Logger
}

func NewAdminHandler(db *pgxpool.Pool, logger *zap.Logger) *AdminHandler {
	return &AdminHandler{db: db, logger: logger}
}

// ListTenants GET /admin/tenants?status=active&page=1&page_size=20
func (h *AdminHandler) ListTenants(c *gin.Context) {
	status := c.Query("status")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 { page = 1 }
	if pageSize < 1 || pageSize > 100 { pageSize = 20 }
	offset := (page - 1) * pageSize

	var totalRows pgx.Row
	var rows pgx.Rows
	var err error

	if status != "" {
		totalRows = h.db.QueryRow(context.Background(),
			"SELECT COUNT(*) FROM public.tenants WHERE deleted_at IS NULL AND status=$1", status)
		rows, err = h.db.Query(context.Background(),
			"SELECT id, name, slug, plan, status, created_at FROM public.tenants WHERE deleted_at IS NULL AND status=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3",
			status, pageSize, offset)
	} else {
		totalRows = h.db.QueryRow(context.Background(),
			"SELECT COUNT(*) FROM public.tenants WHERE deleted_at IS NULL")
		rows, err = h.db.Query(context.Background(),
			"SELECT id, name, slug, plan, status, created_at FROM public.tenants WHERE deleted_at IS NULL ORDER BY created_at DESC LIMIT $1 OFFSET $2",
			pageSize, offset)
	}

	var total int
	if scanErr := totalRows.Scan(&total); scanErr != nil {
		h.logger.Error("count tenants failed", zap.Error(scanErr))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "database error"})
		return
	}
	if err != nil {
		h.logger.Error("list tenants failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "database error"})
		return
	}
	defer rows.Close()

	tenants := make([]model.TenantResponse, 0)
	for rows.Next() {
		var t model.TenantResponse
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Plan, &t.Status, &t.CreatedAt); err != nil {
			h.logger.Error("scan tenant row failed", zap.Error(err))
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "scan error"})
			return
		}
		tenants = append(tenants, t)
	}

	c.JSON(http.StatusOK, model.ListTenantsResponse{
		Tenants: tenants, Total: total, Page: page, PageSize: pageSize,
	})
}

// GetTenant GET /admin/tenants/:id
func (h *AdminHandler) GetTenant(c *gin.Context) {
	id := c.Param("id")
	var t model.TenantResponse
	err := h.db.QueryRow(context.Background(),
		"SELECT id, name, slug, plan, status, created_at, deleted_at FROM public.tenants WHERE id=$1", id,
	).Scan(&t.ID, &t.Name, &t.Slug, &t.Plan, &t.Status, &t.CreatedAt, &t.DeletedAt)
	if err != nil {
		h.logger.Warn("tenant not found", zap.String("id", id), zap.Error(err))
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "tenant not found"})
		return
	}
	c.JSON(http.StatusOK, t)
}

// CreateTenant POST /admin/tenants
func (h *AdminHandler) CreateTenant(c *gin.Context) {
	var req model.CreateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	id := uuid.New().String()
	now := time.Now().UTC()
	_, err := h.db.Exec(context.Background(),
		"INSERT INTO public.tenants(id, name, slug, plan, status, created_at) VALUES($1,$2,$3,$4,$5,$6)",
		id, req.Name, req.Slug, req.Plan, req.Status, now)
	if err != nil {
		h.logger.Error("create tenant failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "create failed"})
		return
	}
	h.logger.Info("tenant created", zap.String("id", id), zap.String("slug", req.Slug))
	c.JSON(http.StatusCreated, model.TenantResponse{
		ID: id, Name: req.Name, Slug: req.Slug,
		Plan: req.Plan, Status: req.Status, CreatedAt: now,
	})
}

// UpdateTenant PATCH /admin/tenants/:id — only plan and status
func (h *AdminHandler) UpdateTenant(c *gin.Context) {
	id := c.Param("id")
	var req model.UpdateTenantRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	tag, err := h.db.Exec(context.Background(),
		"UPDATE public.tenants SET plan=COALESCE(NULLIF($1,''), plan), status=COALESCE(NULLIF($2,''), status) WHERE id=$3 AND deleted_at IS NULL",
		req.Plan, req.Status, id)
	if err != nil {
		h.logger.Error("update tenant failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "update failed"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "tenant not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "tenant updated"})
}

// DeleteTenant DELETE /admin/tenants/:id — soft delete only
func (h *AdminHandler) DeleteTenant(c *gin.Context) {
	id := c.Param("id")
	now := time.Now().UTC()
	tag, err := h.db.Exec(context.Background(),
		"UPDATE public.tenants SET deleted_at=$1 WHERE id=$2 AND deleted_at IS NULL", now, id)
	if err != nil {
		h.logger.Error("delete tenant failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "delete failed"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "tenant not found"})
		return
	}
	h.logger.Info("tenant soft-deleted", zap.String("id", id))
	c.JSON(http.StatusOK, gin.H{"message": "tenant deleted"})
}
```

- [ ] **Step 3: 确认编译无误**

```bash
go build ./api/handler/...
```

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go api/handler/admin_handler.go
git commit -m "feat(handler): add AdminHandler for global admin tenant management"
```

---

### Task 4: Admin Handler 测试

**Files:**
- Create: `api/handler/admin_handler_test.go`

pgx/v5 没有官方 mock 库，使用 `pgxmock/v2` (`github.com/pashagolub/pgxmock/v2`)。

- [ ] **Step 1: 添加测试依赖**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go get github.com/pashagolub/pgxmock/v2@latest
```


- [ ] **Step 2: 创建 admin_handler_test.go**

```go
// api/handler/admin_handler_test.go
package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pashagolub/pgxmock/v2"
	"go.uber.org/zap"
)

func setupAdminRouter(h *AdminHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/admin/tenants", h.ListTenants)
	r.POST("/admin/tenants", h.CreateTenant)
	r.DELETE("/admin/tenants/:id", h.DeleteTenant)
	return r
}

func TestListTenants_noFilter(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	// COUNT query
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	// LIST query
	now := time.Now()
	mock.ExpectQuery("SELECT id, name, slug, plan, status, created_at FROM").
		WillReturnRows(pgxmock.NewRows([]string{"id", "name", "slug", "plan", "status", "created_at"}).
			AddRow("tid1", "Acme", "acme", "pro", "active", now))

	h := &AdminHandler{db: mock, logger: zap.NewNop()}
	r := setupAdminRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/admin/tenants", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	tenants, _ := resp["tenants"].([]interface{})
	if len(tenants) != 1 {
		t.Fatalf("expected 1 tenant, got %d", len(tenants))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestCreateTenant_success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectExec("INSERT INTO public.tenants").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	h := &AdminHandler{db: mock, logger: zap.NewNop()}
	r := setupAdminRouter(h)

	body := `{"name":"Acme","slug":"acme","plan":"pro","status":"active"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/admin/tenants", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestDeleteTenant_softDelete(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectExec("UPDATE public.tenants SET deleted_at").
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	h := &AdminHandler{db: mock, logger: zap.NewNop()}
	r := setupAdminRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/admin/tenants/tid1", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestDeleteTenant_notFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectExec("UPDATE public.tenants SET deleted_at").
		WillReturnResult(pgxmock.NewResult("UPDATE", 0))

	h := &AdminHandler{db: mock, logger: zap.NewNop()}
	r := setupAdminRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/admin/tenants/nonexistent", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
```

**注意：** `pgxmock.NewPool()` 返回的类型实现了 `pgxpool.Pool` 的接口子集；`AdminHandler.db` 字段类型需要改为接口以便注入 mock。在 `admin_handler.go` 中将 `db *pgxpool.Pool` 替换为以下接口：

```go
// PgxPool is the minimal pgxpool interface used by AdminHandler.
type PgxPool interface {
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
}
```

并将 `NewAdminHandler` 签名改为：

```go
func NewAdminHandler(db PgxPool, logger *zap.Logger) *AdminHandler
```

添加必要的 import：

```go
"github.com/jackc/pgx/v5/pgconn"
```

- [ ] **Step 3: 运行测试**

```bash
go test -v -race ./api/handler/... -short -run "TestListTenants|TestCreateTenant|TestDeleteTenant"
```

期望：4 个测试全 PASS。

- [ ] **Step 4: Commit**

```bash
git add api/handler/admin_handler.go api/handler/admin_handler_test.go
git commit -m "test(handler): add AdminHandler unit tests with pgxmock"
```

---

### Task 5: Tenant Handler

**Files:**
- Create: `api/handler/tenant_handler.go`

`tenant_id` 由 auth 中间件注入到 Gin context 的 `"tenant_id"` key。


- [ ] **Step 1: 创建 tenant_handler.go**

```go
// api/handler/tenant_handler.go
package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/model"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type TenantHandler struct {
	db          PgxPool
	logger      *zap.Logger
	frontendURL string
}

func NewTenantHandler(db PgxPool, logger *zap.Logger, frontendURL string) *TenantHandler {
	return &TenantHandler{db: db, logger: logger, frontendURL: frontendURL}
}

func tenantIDFromCtx(c *gin.Context) (string, bool) {
	v, exists := c.Get("tenant_id")
	if !exists {
		return "", false
	}
	id, ok := v.(string)
	return id, ok && id != ""
}

// ListMembers GET /tenant/members?page=1&page_size=20
func (h *TenantHandler) ListMembers(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 { page = 1 }
	if pageSize < 1 || pageSize > 100 { pageSize = 20 }
	offset := (page - 1) * pageSize

	var total int
	if err := h.db.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM public.tenant_members WHERE tenant_id=$1", tenantID,
	).Scan(&total); err != nil {
		h.logger.Error("count members failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "database error"})
		return
	}

	rows, err := h.db.Query(context.Background(),
		`SELECT tm.user_id, u.email, tm.role, tm.created_at
		 FROM public.tenant_members tm
		 JOIN public.users u ON u.id = tm.user_id
		 WHERE tm.tenant_id=$1
		 ORDER BY tm.created_at DESC LIMIT $2 OFFSET $3`,
		tenantID, pageSize, offset)
	if err != nil {
		h.logger.Error("list members failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "database error"})
		return
	}
	defer rows.Close()

	members := make([]model.MemberResponse, 0)
	for rows.Next() {
		var m model.MemberResponse
		if err := rows.Scan(&m.UserID, &m.Email, &m.Role, &m.JoinedAt); err != nil {
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "scan error"})
			return
		}
		members = append(members, m)
	}
	c.JSON(http.StatusOK, model.ListMembersResponse{
		Members: members, Total: total, Page: page, PageSize: pageSize,
	})
}

// InviteMember POST /tenant/members/invite
// Generates a secure random token, stores SHA-256 hash in invitations table,
// returns raw token embedded in the invitation URL.
func (h *TenantHandler) InviteMember(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	var req model.InviteMemberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}

	// Generate 32-byte random token
	rawBytes := make([]byte, 32)
	if _, err := rand.Read(rawBytes); err != nil {
		h.logger.Error("rand.Read failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "token generation failed"})
		return
	}
	rawToken := hex.EncodeToString(rawBytes)

	// Store SHA-256 hash (never store raw token)
	sum := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(sum[:])

	invitationID := uuid.New().String()
	expiresAt := time.Now().UTC().Add(72 * time.Hour) // 3 days

	_, err := h.db.Exec(context.Background(),
		`INSERT INTO public.invitations(id, tenant_id, email, role, token_hash, expires_at, created_at)
		 VALUES($1, $2, $3, $4, $5, $6, $7)`,
		invitationID, tenantID, req.Email, req.Role, tokenHash, expiresAt, time.Now().UTC())
	if err != nil {
		h.logger.Error("insert invitation failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "invitation creation failed"})
		return
	}

	invitationURL := fmt.Sprintf("%s/onboarding?invitation=%s", h.frontendURL, rawToken)
	h.logger.Info("invitation created",
		zap.String("invitation_id", invitationID),
		zap.String("tenant_id", tenantID),
		zap.String("email", req.Email))

	c.JSON(http.StatusCreated, model.InviteMemberResponse{
		InvitationID:  invitationID,
		Email:         req.Email,
		Role:          req.Role,
		InvitationURL: invitationURL,
		ExpiresAt:     expiresAt,
	})
}

// UpdateMemberRole PATCH /tenant/members/:user_id/role
func (h *TenantHandler) UpdateMemberRole(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	userID := c.Param("user_id")
	var req model.UpdateMemberRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	tag, err := h.db.Exec(context.Background(),
		"UPDATE public.tenant_members SET role=$1 WHERE tenant_id=$2 AND user_id=$3",
		req.Role, tenantID, userID)
	if err != nil {
		h.logger.Error("update member role failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "update failed"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "member not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "role updated"})
}

// RemoveMember DELETE /tenant/members/:user_id
func (h *TenantHandler) RemoveMember(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	userID := c.Param("user_id")
	tag, err := h.db.Exec(context.Background(),
		"DELETE FROM public.tenant_members WHERE tenant_id=$1 AND user_id=$2",
		tenantID, userID)
	if err != nil {
		h.logger.Error("remove member failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "remove failed"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "member not found"})
		return
	}
	h.logger.Info("member removed", zap.String("tenant_id", tenantID), zap.String("user_id", userID))
	c.JSON(http.StatusOK, gin.H{"message": "member removed"})
}

// GetSettings GET /tenant/settings
func (h *TenantHandler) GetSettings(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	var settingsJSON []byte
	err := h.db.QueryRow(context.Background(),
		"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL", tenantID,
	).Scan(&settingsJSON)
	if err != nil {
		h.logger.Warn("get settings failed", zap.String("tenant_id", tenantID), zap.Error(err))
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "tenant not found"})
		return
	}
	var settings map[string]interface{}
	if len(settingsJSON) > 0 {
		if err := json.Unmarshal(settingsJSON, &settings); err != nil {
			c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "settings parse error"})
			return
		}
	} else {
		settings = map[string]interface{}{}
	}
	c.JSON(http.StatusOK, model.SettingsResponse{TenantID: tenantID, Settings: settings})
}

// UpdateSettings PATCH /tenant/settings — only updates settings JSONB, never plan
func (h *TenantHandler) UpdateSettings(c *gin.Context) {
	tenantID, ok := tenantIDFromCtx(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Code: 401, Message: "tenant_id missing"})
		return
	}
	var req model.UpdateSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: err.Error()})
		return
	}
	settingsJSON, err := json.Marshal(req.Settings)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Code: 400, Message: "invalid settings"})
		return
	}
	tag, err := h.db.Exec(context.Background(),
		"UPDATE public.tenants SET settings=$1 WHERE id=$2 AND deleted_at IS NULL",
		settingsJSON, tenantID)
	if err != nil {
		h.logger.Error("update settings failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{Code: 500, Message: "update failed"})
		return
	}
	if tag.RowsAffected() == 0 {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Code: 404, Message: "tenant not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "settings updated"})
}
```

**注意：** `tenant_handler.go` 使用了 `encoding/json`，需在 import 块中加入：

```go
"encoding/json"
```

- [ ] **Step 2: 编译验证**

```bash
go build ./api/handler/...
```

- [ ] **Step 3: Commit**

```bash
git add api/handler/tenant_handler.go
git commit -m "feat(handler): add TenantHandler for member and settings management"
```

---

### Task 6: Tenant Handler 测试

**Files:**
- Create: `api/handler/tenant_handler_test.go`

- [ ] **Step 1: 创建 tenant_handler_test.go**

```go
// api/handler/tenant_handler_test.go
package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pashagolub/pgxmock/v2"
	"go.uber.org/zap"
)

func setupTenantRouter(h *TenantHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// Inject tenant_id the way auth middleware would
	inject := func(c *gin.Context) { c.Set("tenant_id", "tenant-abc") }
	r.GET("/tenant/members", inject, h.ListMembers)
	r.POST("/tenant/members/invite", inject, h.InviteMember)
	r.DELETE("/tenant/members/:user_id", inject, h.RemoveMember)
	return r
}

func TestListMembers_success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	// COUNT query
	mock.ExpectQuery("SELECT COUNT").
		WithArgs("tenant-abc").
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	// LIST query
	now := time.Now()
	mock.ExpectQuery("SELECT tm.user_id").
		WithArgs("tenant-abc", 20, 0).
		WillReturnRows(pgxmock.NewRows([]string{"user_id", "email", "role", "created_at"}).
			AddRow("user-1", "alice@example.com", "admin", now))

	h := &TenantHandler{db: mock, logger: zap.NewNop(), frontendURL: "http://localhost:3000"}
	r := setupTenantRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/tenant/members", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	members, _ := resp["members"].([]interface{})
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %d", len(members))
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestInviteMember_success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectExec("INSERT INTO public.invitations").
		WillReturnResult(pgxmock.NewResult("INSERT", 1))

	h := &TenantHandler{db: mock, logger: zap.NewNop(), frontendURL: "http://localhost:3000"}
	r := setupTenantRouter(h)

	body := `{"email":"bob@example.com","role":"member"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/tenant/members/invite", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// Verify invitation_url contains the frontend URL prefix
	url, _ := resp["invitation_url"].(string)
	if !strings.HasPrefix(url, "http://localhost:3000/onboarding?invitation=") {
		t.Errorf("unexpected invitation_url: %s", url)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestInviteMember_invalidEmail(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	h := &TenantHandler{db: mock, logger: zap.NewNop(), frontendURL: "http://localhost:3000"}
	r := setupTenantRouter(h)

	body := `{"email":"not-an-email","role":"member"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/tenant/members/invite", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestRemoveMember_success(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectExec("DELETE FROM public.tenant_members").
		WithArgs("tenant-abc", "user-1").
		WillReturnResult(pgxmock.NewResult("DELETE", 1))

	h := &TenantHandler{db: mock, logger: zap.NewNop(), frontendURL: "http://localhost:3000"}
	r := setupTenantRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/tenant/members/user-1", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestRemoveMember_notFound(t *testing.T) {
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatal(err)
	}
	defer mock.Close()

	mock.ExpectExec("DELETE FROM public.tenant_members").
		WillReturnResult(pgxmock.NewResult("DELETE", 0))

	h := &TenantHandler{db: mock, logger: zap.NewNop(), frontendURL: "http://localhost:3000"}
	r := setupTenantRouter(h)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodDelete, "/tenant/members/ghost-user", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
```

- [ ] **Step 2: 运行测试**

```bash
go test -v -race ./api/handler/... -short -run "TestListMembers|TestInviteMember|TestRemoveMember"
```

期望：5 个测试全 PASS。

- [ ] **Step 3: Commit**

```bash
git add api/handler/tenant_handler_test.go
git commit -m "test(handler): add TenantHandler unit tests with pgxmock"
```

---

### Task 7: 注册路由

**Files:**
- Modify: `api/router.go`

前置条件：`pgxpool.Pool` 实例由调用方（`cmd/server/main.go`）创建后传入 `SetupRouter`；本 Task 只修改 `router.go`，不触碰 main.go（main.go 的改动属于 Plan 5 部署阶段）。`SetupRouter` 签名扩展一个可选的 `*pgxpool.Pool` 参数。

- [ ] **Step 1: 修改 SetupRouter 签名，添加 pgxpool 和 frontendURL 参数**

编辑 `api/router.go`，将函数签名改为：

```go
func SetupRouter(
    cfg *config.Config,
    logger *zap.Logger,
    registry *orchestrator.Registry,
    gateway *llmgateway.Gateway,
    db *pgxpool.Pool, // nil-safe: admin/tenant routes skipped when nil
) *gin.Engine {
```

在 import 块中追加：

```go
"github.com/jackc/pgx/v5/pgxpool"
```

- [ ] **Step 2: 在 router.go 末尾（return router 之前）添加 admin 和 tenant 路由组**

```go
	// Admin + Tenant endpoints (require pgxpool; skipped in tests that pass nil)
	if db != nil {
		adminHandler := handler.NewAdminHandler(db, logger)
		tenantHandler := handler.NewTenantHandler(db, logger, cfg.FrontendURL)

		// Global admin routes: require global_admin role
		admin := router.Group("/admin")
		admin.Use(middleware.RequireGlobalAdmin())
		{
			admin.GET("/tenants", adminHandler.ListTenants)
			admin.POST("/tenants", adminHandler.CreateTenant)
			admin.GET("/tenants/:id", adminHandler.GetTenant)
			admin.PATCH("/tenants/:id", adminHandler.UpdateTenant)
			admin.DELETE("/tenants/:id", adminHandler.DeleteTenant)
		}

		// Tenant admin routes: require at least tenant admin role
		tenant := router.Group("/tenant")
		tenant.Use(middleware.RequireTenantRole("admin"))
		{
			tenant.GET("/members", tenantHandler.ListMembers)
			tenant.POST("/members/invite", tenantHandler.InviteMember)
			tenant.PATCH("/members/:user_id/role", tenantHandler.UpdateMemberRole)
			tenant.DELETE("/members/:user_id", tenantHandler.RemoveMember)
			tenant.GET("/settings", tenantHandler.GetSettings)
			tenant.PATCH("/settings", tenantHandler.UpdateSettings)
		}
	}
```

- [ ] **Step 3: 更新所有 SetupRouter 调用方**

搜索项目中所有调用 `SetupRouter` 的地方，补充 `db` 参数：

- `cmd/server/main.go`（若已存在）：传入已初始化的 `*pgxpool.Pool`，未接入 postgres 时传 `nil`
- 测试文件中（若有）：传 `nil`

```bash
grep -rn "SetupRouter" /home/yang/go-projects/ClawHermes-AI-Go --include="*.go"
```

逐一确认并修改，确保编译通过。

- [ ] **Step 4: 编译验证**

```bash
go build ./...
```

期望：无编译错误。

- [ ] **Step 5: 运行全量短测试**

```bash
go test -v -race ./... -short
```

期望：所有已有测试 PASS，新增测试 PASS，无 data race。

- [ ] **Step 6: Commit**

```bash
git add api/router.go
git commit -m "feat(router): register /admin/* and /tenant/* route groups with role middleware"
```

---

## 验收标准

| 检查项 | 标准 |
|---|---|
| 权限中间件 | `RequireGlobalAdmin` 和 `RequireTenantRole` 各自通过 4 个单元测试 |
| Admin API | `ListTenants` 支持 `?status` 过滤和分页；`DeleteTenant` 设 `deleted_at` 不删行 |
| Tenant API | `InviteMember` 仅存 SHA-256 hash，邀请链接含原始 token；`UpdateSettings` 不可改 `plan` |
| 测试覆盖 | `admin_handler_test.go` 4 个用例；`tenant_handler_test.go` 5 个用例 |
| 编译 | `go build ./...` 零错误 |
| 测试命令 | `go test -v -race ./... -short` 全 PASS |
