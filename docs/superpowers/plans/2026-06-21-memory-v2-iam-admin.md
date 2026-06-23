# Memory v2 IAM Admin Implementation Plan (Phase 7-8)

**Goal:** Extend IAM with derived `system_role` (global_admin / system_admin / user) and build admin API for memory diagnostics + cross-tenant fact inspection.

**Architecture:** IAM domain adds `SystemRole` derived from default tenant membership: `global_admin` (root), `system_admin` (default tenant admin), `user` (other). Admin endpoints require `system_admin+`. Diagnostics include queue lag, supersede stats, frecency distribution, top entities.

**Tech Stack:** Go 1.22+, JWT RS256, Gin middleware, existing IAM bounded context

---

## Global Constraints

- JWT RS256 only, system_role embedded in claims (not stored in DB — derived at login)
- Default tenant ID = `tenant_default` (system tenant for global admins)
- system_admin permission: read all tenants' memory diagnostics, forget any fact in any tenant
- global_admin permission: same as system_admin + manage system_admins
- Admin endpoints under `/api/admin/memory/*` (separate from user endpoints)
- All admin actions logged to audit_log (tenant_id, user_id, action, target_resource)
- Test coverage ≥80%, table-driven middleware tests

---

## File Structure

```
internal/iam/
├── domain/
│   ├── system_role.go          # SystemRole enum + derivation logic
│   ├── system_role_test.go
│   └── user.go                 # Modify: add SystemRole field
├── application/
│   ├── auth_service.go         # Modify: derive system_role at login
│   └── auth_service_test.go

api/http/middleware/
├── system_role_check.go        # Middleware: require system_admin+
└── system_role_check_test.go

api/http/handler/
├── admin_memory_handler.go     # Admin memory endpoints
└── admin_memory_handler_test.go

internal/memory/application/
├── diagnostics_service.go      # Memory diagnostics aggregator
└── diagnostics_service_test.go
```

---

## Task 1: SystemRole Enum and Derivation

**Files:**

- Create: `internal/iam/domain/system_role.go`
- Create: `internal/iam/domain/system_role_test.go`

**Interfaces:**

- Produces: `SystemRole` type, `DeriveSystemRole(tenantMemberships)` function

- [ ] **Step 1: Write test for SystemRole derivation**

```go
// system_role_test.go
package domain_test

import (
 "testing"

 "github.com/byteBuilderX/stratum/internal/iam/domain"
 "github.com/stretchr/testify/require"
)

func TestDeriveSystemRole(t *testing.T) {
 cases := []struct {
  name        string
  memberships []domain.TenantMembership
  expected    domain.SystemRole
 }{
  {
   name: "global admin: default tenant + role=root",
   memberships: []domain.TenantMembership{
    {TenantID: "tenant_default", Role: "root"},
   },
   expected: domain.SystemRoleGlobalAdmin,
  },
  {
   name: "system admin: default tenant + role=admin",
   memberships: []domain.TenantMembership{
    {TenantID: "tenant_default", Role: "admin"},
   },
   expected: domain.SystemRoleSystemAdmin,
  },
  {
   name: "regular user: no default tenant",
   memberships: []domain.TenantMembership{
    {TenantID: "tenant_acme", Role: "admin"},
   },
   expected: domain.SystemRoleUser,
  },
  {
   name:        "regular user: empty memberships",
   memberships: []domain.TenantMembership{},
   expected:    domain.SystemRoleUser,
  },
 }

 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   result := domain.DeriveSystemRole(tc.memberships)
   require.Equal(t, tc.expected, result)
  })
 }
}

func TestSystemRole_HasPermission(t *testing.T) {
 require.True(t, domain.SystemRoleGlobalAdmin.AtLeast(domain.SystemRoleSystemAdmin))
 require.True(t, domain.SystemRoleSystemAdmin.AtLeast(domain.SystemRoleSystemAdmin))
 require.False(t, domain.SystemRoleUser.AtLeast(domain.SystemRoleSystemAdmin))
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/iam/domain/... -run TestDeriveSystemRole`
Expected: FAIL (SystemRole does not exist)

- [ ] **Step 3: Implement SystemRole**

```go
// system_role.go
package domain

import "github.com/byteBuilderX/stratum/pkg/constants"

type SystemRole string

const (
 SystemRoleUser        SystemRole = "user"
 SystemRoleSystemAdmin SystemRole = "system_admin"
 SystemRoleGlobalAdmin SystemRole = "global_admin"
)

type TenantMembership struct {
 TenantID string
 Role     string // "user", "admin", "root"
}

// DeriveSystemRole computes the system-wide role from default tenant membership.
// Default tenant grants system-level privileges: root → global_admin, admin → system_admin.
// Membership in any other tenant grants only user-level access.
func DeriveSystemRole(memberships []TenantMembership) SystemRole {
 for _, m := range memberships {
  if m.TenantID == constants.DefaultTenantID {
   switch m.Role {
   case "root":
    return SystemRoleGlobalAdmin
   case "admin":
    return SystemRoleSystemAdmin
   }
  }
 }
 return SystemRoleUser
}

// AtLeast returns true if this role has at least the privilege of the target role.
func (r SystemRole) AtLeast(target SystemRole) bool {
 rank := func(s SystemRole) int {
  switch s {
  case SystemRoleGlobalAdmin:
   return 3
  case SystemRoleSystemAdmin:
   return 2
  case SystemRoleUser:
   return 1
  }
  return 0
 }
 return rank(r) >= rank(target)
}
```

- [ ] **Step 4: Add DefaultTenantID constant**

```go
// pkg/constants/iam.go (append)
const DefaultTenantID = "tenant_default"
```

- [ ] **Step 5: Run test to verify pass**

Run: `go test -v ./internal/iam/domain/... -run TestDeriveSystemRole`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/iam/domain/system_role.go internal/iam/domain/system_role_test.go pkg/constants/iam.go
git commit -m "feat(iam): add SystemRole derived from default tenant membership

global_admin > system_admin > user; default tenant root grants global

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 2: AuthService Embeds SystemRole in JWT

**Files:**

- Modify: `internal/iam/application/auth_service.go`
- Modify: `internal/iam/application/auth_service_test.go`

**Interfaces:**

- Consumes: `domain.DeriveSystemRole`
- Produces: JWT claims include `system_role` field

- [ ] **Step 1: Write test for JWT system_role claim**

```go
// Append to auth_service_test.go

func TestAuthService_Login_EmbedsSystemRole(t *testing.T) {
 svc := setupAuthServiceWithMemberships(t, "user_admin", []domain.TenantMembership{
  {TenantID: "tenant_default", Role: "admin"},
 })

 token, err := svc.Login(context.Background(), "user_admin", "password")
 require.NoError(t, err)

 claims, err := svc.ParseToken(token)
 require.NoError(t, err)
 require.Equal(t, "system_admin", claims.SystemRole)
}

func TestAuthService_Login_RegularUser(t *testing.T) {
 svc := setupAuthServiceWithMemberships(t, "user_alice", []domain.TenantMembership{
  {TenantID: "tenant_acme", Role: "user"},
 })

 token, err := svc.Login(context.Background(), "user_alice", "password")
 require.NoError(t, err)

 claims, err := svc.ParseToken(token)
 require.NoError(t, err)
 require.Equal(t, "user", claims.SystemRole)
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/iam/application/... -run TestAuthService_Login_EmbedsSystemRole`
Expected: FAIL (SystemRole not in claims)

- [ ] **Step 3: Add SystemRole to JWT claims**

```go
// Modify auth_service.go

type Claims struct {
 UserID     string             `json:"user_id"`
 Username   string             `json:"username"`
 TenantID   string             `json:"tenant_id"`
 SystemRole domain.SystemRole  `json:"system_role"`
 jwt.RegisteredClaims
}

func (s *AuthService) Login(ctx context.Context, username, password string) (string, error) {
 user, err := s.userRepo.FindByUsername(ctx, username)
 if err != nil {
  return "", domain.ErrInvalidCredentials
 }
 if !s.verifyPassword(password, user.PasswordHash) {
  return "", domain.ErrInvalidCredentials
 }

 memberships, err := s.tenantRepo.ListMemberships(ctx, user.ID)
 if err != nil {
  return "", fmt.Errorf("list memberships: %w", err)
 }

 systemRole := domain.DeriveSystemRole(memberships)
 primaryTenantID := selectPrimaryTenant(memberships)

 claims := Claims{
  UserID:     user.ID,
  Username:   user.Username,
  TenantID:   primaryTenantID,
  SystemRole: systemRole,
  RegisteredClaims: jwt.RegisteredClaims{
   ExpiresAt: jwt.NewNumericDate(time.Now().Add(constants.JWTTTL)),
   IssuedAt:  jwt.NewNumericDate(time.Now()),
  },
 }

 return s.signToken(claims)
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./internal/iam/application/... -run TestAuthService_Login_EmbedsSystemRole`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/iam/application/auth_service.go internal/iam/application/auth_service_test.go
git commit -m "feat(iam): embed system_role in JWT claims at login

Derived from default tenant membership; refresh required after privilege change

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 3: SystemRoleCheck Middleware

**Files:**

- Create: `api/http/middleware/system_role_check.go`
- Create: `api/http/middleware/system_role_check_test.go`

**Interfaces:**

- Consumes: JWT Claims with SystemRole
- Produces: Gin middleware `RequireSystemRole(role)`

- [ ] **Step 1: Write middleware test**

```go
// system_role_check_test.go
package middleware_test

import (
 "net/http"
 "net/http/httptest"
 "testing"

 "github.com/byteBuilderX/stratum/api/http/middleware"
 "github.com/byteBuilderX/stratum/internal/iam/domain"
 "github.com/gin-gonic/gin"
 "github.com/stretchr/testify/require"
)

func TestRequireSystemRole_Allows(t *testing.T) {
 gin.SetMode(gin.TestMode)
 r := gin.New()
 r.GET("/admin", middleware.RequireSystemRole(domain.SystemRoleSystemAdmin), func(c *gin.Context) {
  c.JSON(200, gin.H{"ok": true})
 })

 req := httptest.NewRequest("GET", "/admin", nil)
 w := httptest.NewRecorder()

 // Inject system_admin claims
 r.Use(func(c *gin.Context) {
  c.Set("system_role", string(domain.SystemRoleSystemAdmin))
 })
 r.ServeHTTP(w, req)
 require.Equal(t, http.StatusOK, w.Code)
}

func TestRequireSystemRole_Denies(t *testing.T) {
 gin.SetMode(gin.TestMode)
 r := gin.New()
 r.Use(func(c *gin.Context) {
  c.Set("system_role", string(domain.SystemRoleUser))
 })
 r.GET("/admin", middleware.RequireSystemRole(domain.SystemRoleSystemAdmin), func(c *gin.Context) {
  c.JSON(200, gin.H{"ok": true})
 })

 req := httptest.NewRequest("GET", "/admin", nil)
 w := httptest.NewRecorder()
 r.ServeHTTP(w, req)
 require.Equal(t, http.StatusForbidden, w.Code)
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./api/http/middleware/... -run TestRequireSystemRole`
Expected: FAIL

- [ ] **Step 3: Implement middleware**

```go
// system_role_check.go
package middleware

import (
 "net/http"

 "github.com/byteBuilderX/stratum/internal/iam/domain"
 "github.com/gin-gonic/gin"
)

// RequireSystemRole rejects the request unless the JWT system_role meets the minimum.
func RequireSystemRole(minRole domain.SystemRole) gin.HandlerFunc {
 return func(c *gin.Context) {
  roleStr, exists := c.Get("system_role")
  if !exists {
   c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing system role"})
   return
  }

  role := domain.SystemRole(roleStr.(string))
  if !role.AtLeast(minRole) {
   c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient privileges"})
   return
  }

  c.Next()
 }
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./api/http/middleware/... -run TestRequireSystemRole`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add api/http/middleware/system_role_check.go api/http/middleware/system_role_check_test.go
git commit -m "feat(iam): add RequireSystemRole middleware

Enforces minimum system role via JWT claims

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 4: DiagnosticsService

**Files:**

- Create: `internal/memory/application/diagnostics_service.go`
- Create: `internal/memory/application/diagnostics_service_test.go`

**Interfaces:**

- Consumes: `port.FactRepo`, `port.EntityRepo`, `port.ExtractionQueue`
- Produces: `GetDiagnostics(tenantID)` returns metrics struct

- [ ] **Step 1: Write diagnostics test**

```go
// diagnostics_service_test.go
package application_test

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/memory/application"
 "github.com/stretchr/testify/require"
)

func TestDiagnosticsService_GetDiagnostics(t *testing.T) {
 factRepo := &mockFactRepoStats{
  activeCount:     150,
  supersededCount: 20,
  deletedCount:    5,
 }
 queue := &mockQueueStats{pendingCount: 8, oldestPendingAge: 30 * time.Second}
 entityRepo := &mockEntityRepoStats{topEntities: []application.EntityStat{
  {Name: "Python", Type: "tech", FactCount: 25},
 }}

 svc := application.NewDiagnosticsService(factRepo, entityRepo, queue)
 diag, err := svc.GetDiagnostics(context.Background(), "tenant001")
 require.NoError(t, err)
 require.Equal(t, 150, diag.ActiveFactCount)
 require.Equal(t, 20, diag.SupersededCount)
 require.Equal(t, 8, diag.QueueLag)
 require.Equal(t, "Python", diag.TopEntities[0].Name)
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/memory/application/... -run TestDiagnosticsService`
Expected: FAIL

- [ ] **Step 3: Implement DiagnosticsService**

```go
// diagnostics_service.go
package application

import (
 "context"
 "time"

 "github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

type Diagnostics struct {
 TenantID            string        `json:"tenant_id"`
 ActiveFactCount     int           `json:"active_fact_count"`
 SupersededCount     int           `json:"superseded_count"`
 DeletedCount        int           `json:"deleted_count"`
 ArchivedCount       int           `json:"archived_count"`
 EntityCount         int           `json:"entity_count"`
 QueueLag            int           `json:"queue_lag"`
 OldestPendingAge    time.Duration `json:"oldest_pending_age"`
 TopEntities         []EntityStat  `json:"top_entities"`
 FrecencyHistogram   []float64     `json:"frecency_histogram"`
}

type EntityStat struct {
 Name      string `json:"name"`
 Type      string `json:"type"`
 FactCount int    `json:"fact_count"`
}

type DiagnosticsService struct {
 factRepo   port.FactRepoStats
 entityRepo port.EntityRepoStats
 queue      port.ExtractionQueueStats
}

func NewDiagnosticsService(
 factRepo port.FactRepoStats,
 entityRepo port.EntityRepoStats,
 queue port.ExtractionQueueStats,
) *DiagnosticsService {
 return &DiagnosticsService{
  factRepo:   factRepo,
  entityRepo: entityRepo,
  queue:      queue,
 }
}

func (s *DiagnosticsService) GetDiagnostics(ctx context.Context, tenantID string) (*Diagnostics, error) {
 stats, err := s.factRepo.CountByStatus(ctx, tenantID)
 if err != nil {
  return nil, err
 }

 queueLag, oldestAge, err := s.queue.QueueStats(ctx, tenantID)
 if err != nil {
  return nil, err
 }

 topEntities, err := s.entityRepo.TopEntities(ctx, tenantID, 10)
 if err != nil {
  return nil, err
 }

 histogram, err := s.factRepo.FrecencyHistogram(ctx, tenantID, 10)
 if err != nil {
  return nil, err
 }

 return &Diagnostics{
  TenantID:          tenantID,
  ActiveFactCount:   stats["active"],
  SupersededCount:   stats["superseded"],
  DeletedCount:      stats["deleted"],
  ArchivedCount:     stats["archived"],
  EntityCount:       stats["entity_total"],
  QueueLag:          queueLag,
  OldestPendingAge:  oldestAge,
  TopEntities:       topEntities,
  FrecencyHistogram: histogram,
 }, nil
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./internal/memory/application/... -run TestDiagnosticsService`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/application/diagnostics_service.go internal/memory/application/diagnostics_service_test.go
git commit -m "feat(memory): add DiagnosticsService aggregator

Combines fact counts, queue lag, top entities, frecency histogram

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 5: Admin Memory Handler

**Files:**

- Create: `api/http/handler/admin_memory_handler.go`
- Create: `api/http/handler/admin_memory_handler_test.go`

**Interfaces:**

- Consumes: `DiagnosticsService`, `MemoryService.ForgetMemory`
- Produces: GET /api/admin/memory/diagnostics, POST /api/admin/memory/facts/:id/forget

- [ ] **Step 1: Write handler test**

```go
// admin_memory_handler_test.go
func TestAdminMemoryHandler_GetDiagnostics(t *testing.T) {
 gin.SetMode(gin.TestMode)
 r := gin.New()
 diagSvc := &mockDiagSvc{}
 handler := handler.NewAdminMemoryHandler(diagSvc, nil)
 handler.Register(r.Group("/api/admin/memory"))

 // Inject system_admin
 r.Use(func(c *gin.Context) {
  c.Set("system_role", string(domain.SystemRoleSystemAdmin))
 })

 req := httptest.NewRequest("GET", "/api/admin/memory/diagnostics?tenant_id=tenant001", nil)
 w := httptest.NewRecorder()
 r.ServeHTTP(w, req)
 require.Equal(t, http.StatusOK, w.Code)
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./api/http/handler/... -run TestAdminMemoryHandler`
Expected: FAIL

- [ ] **Step 3: Implement handler**

```go
// admin_memory_handler.go
package handler

import (
 "net/http"

 "github.com/byteBuilderX/stratum/api/http/middleware"
 "github.com/byteBuilderX/stratum/internal/iam/domain"
 "github.com/byteBuilderX/stratum/internal/memory/application"
 "github.com/gin-gonic/gin"
)

type AdminMemoryHandler struct {
 diagnostics *application.DiagnosticsService
 memorySvc   *application.MemoryService
}

func NewAdminMemoryHandler(diag *application.DiagnosticsService, mem *application.MemoryService) *AdminMemoryHandler {
 return &AdminMemoryHandler{diagnostics: diag, memorySvc: mem}
}

func (h *AdminMemoryHandler) Register(group *gin.RouterGroup) {
 group.Use(middleware.RequireSystemRole(domain.SystemRoleSystemAdmin))
 group.GET("/diagnostics", h.getDiagnostics)
 group.POST("/facts/:id/forget", h.forgetFact)
 group.GET("/tenants", h.listTenants)
}

func (h *AdminMemoryHandler) getDiagnostics(c *gin.Context) {
 tenantID := c.Query("tenant_id")
 if tenantID == "" {
  c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_id required"})
  return
 }

 diag, err := h.diagnostics.GetDiagnostics(c.Request.Context(), tenantID)
 if err != nil {
  c.Error(err)
  return
 }

 c.JSON(http.StatusOK, diag)
}

func (h *AdminMemoryHandler) forgetFact(c *gin.Context) {
 tenantID := c.Query("tenant_id")
 userID := c.Query("user_id")
 factID := c.Param("id")

 req := &application.ForgetMemoryRequest{
  TenantID: tenantID,
  UserID:   userID,
  FactID:   factID,
 }
 if err := h.memorySvc.ForgetMemory(c.Request.Context(), req); err != nil {
  c.Error(err)
  return
 }

 c.JSON(http.StatusOK, gin.H{"forgotten": factID})
}

func (h *AdminMemoryHandler) listTenants(c *gin.Context) {
 // Stub: returns tenant list with memory counts
 c.JSON(http.StatusOK, gin.H{"tenants": []string{}})
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./api/http/handler/... -run TestAdminMemoryHandler`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add api/http/handler/admin_memory_handler.go api/http/handler/admin_memory_handler_test.go
git commit -m "feat(memory): add AdminMemoryHandler for system_admin diagnostics

GET /diagnostics, POST /facts/:id/forget; requires system_admin+

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Plan Complete

IAM admin plan finished. Phase 7-8 complete. Next: frontend (Phase 9).
