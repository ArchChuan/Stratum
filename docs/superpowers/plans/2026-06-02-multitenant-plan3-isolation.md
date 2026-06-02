# Multi-Tenant Plan 3: 租户隔离 — Context 传递与存储隔离

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立 TenantContext 在整个请求链路的传递机制，并为 PostgreSQL、Milvus、Neo4j、NATS 四个存储层提供统一的租户隔离 helper，配合 JWT Middleware 自动注入租户身份。

**Architecture:** 新增 `pkg/tenantdb` 包集中管理所有租户隔离逻辑；PostgreSQL 通过 `SET LOCAL search_path` 在事务内切换 schema；Milvus/Neo4j/NATS 通过命名前缀隔离；JWT Middleware 从 Bearer token 解析 claims 并写入 context；`global_admin` 角色不要求 tenant_id。

**Tech Stack:** Go 1.24, `github.com/jackc/pgx/v5`（Plan 1 引入），`github.com/golang-jwt/jwt/v5 v5.2.1`（本 Plan 新增），`github.com/byteBuilderX/ClawHermes-AI-Go/pkg/postgres`（Plan 1 封装）

---

## File Map

| 文件 | 类型 | 职责 |
|------|------|------|
| `pkg/tenantdb/context.go` | Create | TenantContext struct；WithTenant / FromContext |
| `pkg/tenantdb/context_test.go` | Create | context 读写单元测试 |
| `pkg/tenantdb/postgres.go` | Create | ExecTenant — 事务内 SET LOCAL search_path |
| `pkg/tenantdb/postgres_test.go` | Create | ExecTenant 集成测试（integration tag） |
| `pkg/tenantdb/milvus.go` | Create | TenantCollection — 返回 collection 名称字符串 |
| `pkg/tenantdb/milvus_test.go` | Create | TenantCollection 单元测试 |
| `pkg/tenantdb/neo4j.go` | Create | TenantLabel — 返回 Neo4j label 字符串 |
| `pkg/tenantdb/neo4j_test.go` | Create | TenantLabel 单元测试 |
| `pkg/tenantdb/nats.go` | Create | TenantSubject — 返回 NATS subject 字符串 |
| `pkg/tenantdb/nats_test.go` | Create | TenantSubject 单元测试 |
| `pkg/tenantdb/schema.go` | Create | ProvisionTenantSchema — CREATE SCHEMA + DDL |
| `pkg/tenantdb/schema_test.go` | Create | ProvisionTenantSchema 集成测试（integration tag） |
| `internal/migration/sql/tenant_schema.sql` | Create | per-tenant 所有建表 DDL |
| `api/middleware/tenant.go` | Create | JWT Bearer 解析，注入 TenantContext |
| `api/middleware/tenant_test.go` | Create | Middleware 单元测试 |

---

### Task 1: TenantContext — context.go

**Files:**
- Create: `pkg/tenantdb/context.go`
- Create: `pkg/tenantdb/context_test.go`

- [ ] **Step 1: 添加 JWT 依赖**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go get github.com/golang-jwt/jwt/v5@v5.2.1
```

Expected: `go.mod` 和 `go.sum` 更新，无错误。

- [ ] **Step 2: 创建 pkg/tenantdb/context.go**

```go
// Package tenantdb provides tenant isolation helpers for all storage layers.
package tenantdb

import "context"

// Role represents the access role encoded in a JWT claim.
type Role string

const (
	RoleTenantAdmin Role = "tenant_admin"
	RoleTenantUser  Role = "tenant_user"
	RoleGlobalAdmin Role = "global_admin"
)

// TenantContext carries tenant identity through the request lifecycle.
// TenantID is empty for global_admin requests.
type TenantContext struct {
	TenantID string
	UserID   string
	Role     Role
}

type ctxKey struct{}

// WithTenant returns a new context with tc embedded.
func WithTenant(ctx context.Context, tc *TenantContext) context.Context {
	return context.WithValue(ctx, ctxKey{}, tc)
}

// FromContext extracts the TenantContext from ctx.
// Returns (nil, false) if not present.
func FromContext(ctx context.Context) (*TenantContext, bool) {
	tc, ok := ctx.Value(ctxKey{}).(*TenantContext)
	return tc, ok && tc != nil
}
```

- [ ] **Step 3: 创建 pkg/tenantdb/context_test.go**

```go
package tenantdb_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func TestWithTenantAndFromContext(t *testing.T) {
	tc := &tenantdb.TenantContext{
		TenantID: "acme",
		UserID:   "user-1",
		Role:     tenantdb.RoleTenantAdmin,
	}

	ctx := tenantdb.WithTenant(context.Background(), tc)

	got, ok := tenantdb.FromContext(ctx)
	if !ok {
		t.Fatal("expected TenantContext in context, got none")
	}
	if got.TenantID != "acme" {
		t.Errorf("TenantID: want %q, got %q", "acme", got.TenantID)
	}
	if got.UserID != "user-1" {
		t.Errorf("UserID: want %q, got %q", "user-1", got.UserID)
	}
	if got.Role != tenantdb.RoleTenantAdmin {
		t.Errorf("Role: want %q, got %q", tenantdb.RoleTenantAdmin, got.Role)
	}
}

func TestFromContext_Missing(t *testing.T) {
	_, ok := tenantdb.FromContext(context.Background())
	if ok {
		t.Fatal("expected no TenantContext in empty context")
	}
}

func TestGlobalAdminEmptyTenantID(t *testing.T) {
	tc := &tenantdb.TenantContext{
		TenantID: "",
		UserID:   "admin-1",
		Role:     tenantdb.RoleGlobalAdmin,
	}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	got, ok := tenantdb.FromContext(ctx)
	if !ok {
		t.Fatal("expected TenantContext in context")
	}
	if got.TenantID != "" {
		t.Errorf("global_admin should have empty TenantID, got %q", got.TenantID)
	}
}
```

- [ ] **Step 4: 运行测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./pkg/tenantdb/ -run TestWith -short
```

Expected:
```
--- PASS: TestWithTenantAndFromContext (0.00s)
--- PASS: TestFromContext_Missing (0.00s)
--- PASS: TestGlobalAdminEmptyTenantID (0.00s)
PASS
```

- [ ] **Step 5: go vet**

```bash
go vet ./pkg/tenantdb/
```

Expected: 无输出（无错误）。

- [ ] **Step 6: commit**

```bash
git add pkg/tenantdb/context.go pkg/tenantdb/context_test.go go.mod go.sum
git commit -m "feat(tenantdb): add TenantContext with WithTenant/FromContext"
```

---

### Task 2: PostgreSQL 隔离 — postgres.go

**Files:**
- Create: `pkg/tenantdb/postgres.go`
- Create: `pkg/tenantdb/postgres_test.go`

前置条件：Plan 1 的 `pkg/postgres` 包已存在，`Pool.DB()` 返回 `*pgxpool.Pool`。

- [ ] **Step 1: 创建 pkg/tenantdb/postgres.go**

```go
package tenantdb

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ExecTenant runs fn inside a transaction whose search_path is set to
// "tenant_{id}, public". Returns an error if ctx has no TenantContext.
func ExecTenant(ctx context.Context, pool *pgxpool.Pool, fn func(ctx context.Context, tx pgx.Tx) error) error {
	tc, ok := FromContext(ctx)
	if !ok {
		return fmt.Errorf("tenantdb: missing tenant context")
	}
	if tc.TenantID == "" {
		return fmt.Errorf("tenantdb: tenant_id is empty (global_admin cannot use ExecTenant)")
	}

	// sanitise: TenantID must only contain safe chars (validated at onboard time,
	// but we guard here too to prevent SQL injection via search_path)
	for _, r := range tc.TenantID {
		if !isSafeTenantIDChar(r) {
			return fmt.Errorf("tenantdb: invalid tenant_id %q", tc.TenantID)
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("tenantdb: begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	schema := "tenant_" + tc.TenantID
	if _, err := tx.Exec(ctx, "SET LOCAL search_path = "+schema+", public"); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("tenantdb: set search_path: %w", err)
	}

	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}

	return tx.Commit(ctx)
}

// isSafeTenantIDChar returns true for characters safe in a PostgreSQL identifier.
// Tenant IDs must be lowercase alphanumeric + underscore + hyphen.
func isSafeTenantIDChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= '0' && r <= '9') ||
		r == '_' || r == '-'
}
```

- [ ] **Step 2: 创建 pkg/tenantdb/postgres_test.go**

```go
//go:build integration

package tenantdb_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

// Integration test — requires a live PostgreSQL instance.
// Set TEST_POSTGRES_URL=postgres://user:pass@localhost:5432/dbname
func TestExecTenant_SetsSearchPath(t *testing.T) {
	url := os.Getenv("TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("TEST_POSTGRES_URL not set")
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	tc := &tenantdb.TenantContext{TenantID: "testco", UserID: "u1", Role: tenantdb.RoleTenantAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)

	var searchPath string
	err = tenantdb.ExecTenant(ctx, pool, func(ctx context.Context, tx interface{ QueryRow(context.Context, string, ...any) interface{ Scan(...any) error } }) error {
		return tx.QueryRow(ctx, "SHOW search_path").Scan(&searchPath)
	})
	if err != nil {
		t.Fatalf("ExecTenant: %v", err)
	}
	if searchPath != "tenant_testco, public" && searchPath != `"tenant_testco", "public"` {
		t.Errorf("unexpected search_path: %q", searchPath)
	}
}

func TestExecTenant_MissingContext(t *testing.T) {
	// pool can be nil because the error should be caught before any DB call
	err := tenantdb.ExecTenant(context.Background(), nil, func(_ context.Context, _ interface{ QueryRow(context.Context, string, ...any) interface{ Scan(...any) error } }) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for missing tenant context")
	}
	if err.Error() != "tenantdb: missing tenant context" {
		t.Errorf("unexpected error: %v", err)
	}
}
```

The integration test uses `pgx.Tx` directly. Revise to use the proper pgx.Tx type:

```go
//go:build integration

package tenantdb_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func TestExecTenant_SetsSearchPath(t *testing.T) {
	url := os.Getenv("TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("TEST_POSTGRES_URL not set")
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	tc := &tenantdb.TenantContext{TenantID: "testco", UserID: "u1", Role: tenantdb.RoleTenantAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)

	var searchPath string
	err = tenantdb.ExecTenant(ctx, pool, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SHOW search_path").Scan(&searchPath)
	})
	if err != nil {
		t.Fatalf("ExecTenant: %v", err)
	}
	// PostgreSQL returns search_path like `tenant_testco, public` or quoted form
	if searchPath == "" {
		t.Error("search_path is empty")
	}
}

func TestExecTenant_MissingContext(t *testing.T) {
	err := tenantdb.ExecTenant(context.Background(), nil, func(_ context.Context, _ pgx.Tx) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for missing tenant context")
	}
}

func TestExecTenant_EmptyTenantID(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "", UserID: "admin", Role: tenantdb.RoleGlobalAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	err := tenantdb.ExecTenant(ctx, nil, func(_ context.Context, _ pgx.Tx) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}
```

- [ ] **Step 3: 运行单元测试（无 DB）**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./pkg/tenantdb/ -run TestExecTenant_MissingContext -short
go test -v -race ./pkg/tenantdb/ -run TestExecTenant_EmptyTenantID -short
```

Expected: 两个 PASS（这两个 case 不需要 DB，build tag 为 integration 故不会被 -short 选中——这里改为用 non-integration 测试覆盖）。

注意：MissingContext 和 EmptyTenantID 两个测试不需要真实 DB，但放在 `//go:build integration` 文件中。将它们提到独立的 `postgres_unit_test.go` 中（无 build tag）：

```go
package tenantdb_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func TestExecTenant_MissingContext_Unit(t *testing.T) {
	err := tenantdb.ExecTenant(context.Background(), nil, func(_ context.Context, _ pgx.Tx) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for missing tenant context")
	}
}

func TestExecTenant_EmptyTenantID_Unit(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "", UserID: "admin", Role: tenantdb.RoleGlobalAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	err := tenantdb.ExecTenant(ctx, nil, func(_ context.Context, _ pgx.Tx) error {
		return nil
	})
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}
```

- [ ] **Step 4: 运行单元测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./pkg/tenantdb/ -run "TestExecTenant.*Unit" -short
```

Expected:
```
--- PASS: TestExecTenant_MissingContext_Unit
--- PASS: TestExecTenant_EmptyTenantID_Unit
PASS
```

- [ ] **Step 5: go vet**

```bash
go vet ./pkg/tenantdb/
```

Expected: 无输出。

- [ ] **Step 6: commit**

```bash
git add pkg/tenantdb/postgres.go pkg/tenantdb/postgres_test.go pkg/tenantdb/postgres_unit_test.go
git commit -m "feat(tenantdb): add ExecTenant for PostgreSQL schema isolation"
```

---

### Task 3: Milvus 隔离 — milvus.go

**Files:**
- Create: `pkg/tenantdb/milvus.go`
- Create: `pkg/tenantdb/milvus_test.go`

- [ ] **Step 1: 创建 pkg/tenantdb/milvus.go**

```go
package tenantdb

import (
	"context"
	"fmt"
)

// TenantCollection returns the Milvus collection name for a given kind,
// scoped to the tenant in ctx.
// Example: kind="knowledge" → "tenant_acme_knowledge"
// Returns an error if ctx has no TenantContext or TenantID is empty.
func TenantCollection(ctx context.Context, kind string) (string, error) {
	tc, ok := FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("tenantdb: missing tenant context")
	}
	if tc.TenantID == "" {
		return "", fmt.Errorf("tenantdb: tenant_id is empty")
	}
	return "tenant_" + tc.TenantID + "_" + kind, nil
}
```

- [ ] **Step 2: 创建 pkg/tenantdb/milvus_test.go**

```go
package tenantdb_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func TestTenantCollection(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "acme", UserID: "u1", Role: tenantdb.RoleTenantAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)

	got, err := tenantdb.TenantCollection(ctx, "knowledge")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "tenant_acme_knowledge"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestTenantCollection_MissingContext(t *testing.T) {
	_, err := tenantdb.TenantCollection(context.Background(), "knowledge")
	if err == nil {
		t.Fatal("expected error for missing tenant context")
	}
}

func TestTenantCollection_EmptyTenantID(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "", UserID: "admin", Role: tenantdb.RoleGlobalAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	_, err := tenantdb.TenantCollection(ctx, "knowledge")
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}
```

- [ ] **Step 3: 运行测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./pkg/tenantdb/ -run TestTenantCollection -short
```

Expected:
```
--- PASS: TestTenantCollection (0.00s)
--- PASS: TestTenantCollection_MissingContext (0.00s)
--- PASS: TestTenantCollection_EmptyTenantID (0.00s)
PASS
```

- [ ] **Step 4: commit**

```bash
git add pkg/tenantdb/milvus.go pkg/tenantdb/milvus_test.go
git commit -m "feat(tenantdb): add TenantCollection for Milvus isolation"
```

---

### Task 4: Neo4j 隔离 — neo4j.go

**Files:**
- Create: `pkg/tenantdb/neo4j.go`
- Create: `pkg/tenantdb/neo4j_test.go`

- [ ] **Step 1: 创建 pkg/tenantdb/neo4j.go**

```go
package tenantdb

import (
	"context"
	"fmt"
)

// TenantLabel returns the Neo4j node label for a given base label,
// scoped to the tenant in ctx.
// Example: label="Document" → "T_acme_Document"
// Returns an error if ctx has no TenantContext or TenantID is empty.
func TenantLabel(ctx context.Context, label string) (string, error) {
	tc, ok := FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("tenantdb: missing tenant context")
	}
	if tc.TenantID == "" {
		return "", fmt.Errorf("tenantdb: tenant_id is empty")
	}
	return "T_" + tc.TenantID + "_" + label, nil
}
```

- [ ] **Step 2: 创建 pkg/tenantdb/neo4j_test.go**

```go
package tenantdb_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func TestTenantLabel(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "acme", UserID: "u1", Role: tenantdb.RoleTenantAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)

	got, err := tenantdb.TenantLabel(ctx, "Document")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "T_acme_Document"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestTenantLabel_MissingContext(t *testing.T) {
	_, err := tenantdb.TenantLabel(context.Background(), "Document")
	if err == nil {
		t.Fatal("expected error for missing tenant context")
	}
}

func TestTenantLabel_EmptyTenantID(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "", UserID: "admin", Role: tenantdb.RoleGlobalAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	_, err := tenantdb.TenantLabel(ctx, "Document")
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}
```

- [ ] **Step 3: 运行测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./pkg/tenantdb/ -run TestTenantLabel -short
```

Expected:
```
--- PASS: TestTenantLabel (0.00s)
--- PASS: TestTenantLabel_MissingContext (0.00s)
--- PASS: TestTenantLabel_EmptyTenantID (0.00s)
PASS
```

- [ ] **Step 4: commit**

```bash
git add pkg/tenantdb/neo4j.go pkg/tenantdb/neo4j_test.go
git commit -m "feat(tenantdb): add TenantLabel for Neo4j isolation"
```

---

### Task 5: NATS 隔离 — nats.go

**Files:**
- Create: `pkg/tenantdb/nats.go`
- Create: `pkg/tenantdb/nats_test.go`

- [ ] **Step 1: 创建 pkg/tenantdb/nats.go**

```go
package tenantdb

import (
	"context"
	"fmt"
)

// TenantSubject returns the NATS subject scoped to the tenant in ctx.
// Example: subject="exec.completed" → "tenant.acme.exec.completed"
// Returns an error if ctx has no TenantContext or TenantID is empty.
func TenantSubject(ctx context.Context, subject string) (string, error) {
	tc, ok := FromContext(ctx)
	if !ok {
		return "", fmt.Errorf("tenantdb: missing tenant context")
	}
	if tc.TenantID == "" {
		return "", fmt.Errorf("tenantdb: tenant_id is empty")
	}
	return "tenant." + tc.TenantID + "." + subject, nil
}
```

- [ ] **Step 2: 创建 pkg/tenantdb/nats_test.go**

```go
package tenantdb_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func TestTenantSubject(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "acme", UserID: "u1", Role: tenantdb.RoleTenantAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)

	got, err := tenantdb.TenantSubject(ctx, "exec.completed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "tenant.acme.exec.completed"
	if got != want {
		t.Errorf("want %q, got %q", want, got)
	}
}

func TestTenantSubject_MissingContext(t *testing.T) {
	_, err := tenantdb.TenantSubject(context.Background(), "exec.completed")
	if err == nil {
		t.Fatal("expected error for missing tenant context")
	}
}

func TestTenantSubject_EmptyTenantID(t *testing.T) {
	tc := &tenantdb.TenantContext{TenantID: "", UserID: "admin", Role: tenantdb.RoleGlobalAdmin}
	ctx := tenantdb.WithTenant(context.Background(), tc)
	_, err := tenantdb.TenantSubject(ctx, "exec.completed")
	if err == nil {
		t.Fatal("expected error for empty tenant_id")
	}
}
```

- [ ] **Step 3: 运行测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./pkg/tenantdb/ -run TestTenantSubject -short
```

Expected:
```
--- PASS: TestTenantSubject (0.00s)
--- PASS: TestTenantSubject_MissingContext (0.00s)
--- PASS: TestTenantSubject_EmptyTenantID (0.00s)
PASS
```

- [ ] **Step 4: commit**

```bash
git add pkg/tenantdb/nats.go pkg/tenantdb/nats_test.go
git commit -m "feat(tenantdb): add TenantSubject for NATS isolation"
```

---

### Task 6: Per-tenant Schema DDL — tenant_schema.sql

**Files:**
- Create: `internal/migration/sql/tenant_schema.sql`

- [ ] **Step 1: 创建目录**

```bash
mkdir -p /home/yang/go-projects/ClawHermes-AI-Go/internal/migration/sql
```

- [ ] **Step 2: 创建 internal/migration/sql/tenant_schema.sql**

所有 FK 引用均在同一 schema 内（由 ProvisionTenantSchema 在 SET search_path 后执行）：

```sql
-- Per-tenant schema DDL
-- Execute after: SET search_path = tenant_{id}, public

-- agents
CREATE TABLE IF NOT EXISTS agents (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL,
    description  TEXT,
    config       JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- skills
CREATE TABLE IF NOT EXISTS skills (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id     UUID REFERENCES agents(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    type         TEXT NOT NULL,
    config       JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- mcp_configs
CREATE TABLE IF NOT EXISTS mcp_configs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id     UUID REFERENCES agents(id) ON DELETE CASCADE,
    server_id    TEXT NOT NULL,
    transport    TEXT NOT NULL,
    config       JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- sessions
CREATE TABLE IF NOT EXISTS sessions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id     UUID REFERENCES agents(id) ON DELETE SET NULL,
    user_id      TEXT NOT NULL,
    metadata     JSONB NOT NULL DEFAULT '{}',
    started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at     TIMESTAMPTZ
);

-- memory_entries
CREATE TABLE IF NOT EXISTS memory_entries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id   UUID REFERENCES sessions(id) ON DELETE CASCADE,
    role         TEXT NOT NULL,
    content      TEXT NOT NULL,
    type         TEXT NOT NULL DEFAULT 'short_term',
    importance   FLOAT8 NOT NULL DEFAULT 0,
    tags         TEXT[] NOT NULL DEFAULT '{}',
    metadata     JSONB NOT NULL DEFAULT '{}',
    expires_at   TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- entities (knowledge graph nodes)
CREATE TABLE IF NOT EXISTS entities (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL,
    type         TEXT NOT NULL,
    properties   JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- entity_relations
CREATE TABLE IF NOT EXISTS entity_relations (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    from_id      UUID NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    to_id        UUID NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
    relation     TEXT NOT NULL,
    properties   JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- knowledge_docs
CREATE TABLE IF NOT EXISTS knowledge_docs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    title        TEXT NOT NULL,
    content      TEXT NOT NULL,
    source       TEXT,
    metadata     JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- exec_history
CREATE TABLE IF NOT EXISTS exec_history (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id     UUID REFERENCES agents(id) ON DELETE SET NULL,
    session_id   UUID REFERENCES sessions(id) ON DELETE SET NULL,
    skill_id     UUID REFERENCES skills(id) ON DELETE SET NULL,
    input        JSONB NOT NULL DEFAULT '{}',
    output       JSONB NOT NULL DEFAULT '{}',
    status       TEXT NOT NULL DEFAULT 'pending',
    started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at  TIMESTAMPTZ
);

-- llm_api_keys
CREATE TABLE IF NOT EXISTS llm_api_keys (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider     TEXT NOT NULL,
    key_hint     TEXT NOT NULL,
    encrypted    TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- model_presets
CREATE TABLE IF NOT EXISTS model_presets (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL,
    provider     TEXT NOT NULL,
    model_id     TEXT NOT NULL,
    config       JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- model_usage
CREATE TABLE IF NOT EXISTS model_usage (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model_preset_id UUID REFERENCES model_presets(id) ON DELETE SET NULL,
    input_tokens    INT NOT NULL DEFAULT 0,
    output_tokens   INT NOT NULL DEFAULT 0,
    cost_usd        NUMERIC(12,6) NOT NULL DEFAULT 0,
    recorded_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- model_quotas
CREATE TABLE IF NOT EXISTS model_quotas (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    model_preset_id UUID REFERENCES model_presets(id) ON DELETE CASCADE,
    period          TEXT NOT NULL DEFAULT 'monthly',
    max_tokens      BIGINT NOT NULL,
    max_cost_usd    NUMERIC(12,2),
    reset_at        TIMESTAMPTZ NOT NULL
);

-- prompt_templates
CREATE TABLE IF NOT EXISTS prompt_templates (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL,
    template     TEXT NOT NULL,
    variables    TEXT[] NOT NULL DEFAULT '{}',
    metadata     JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- workflows
CREATE TABLE IF NOT EXISTS workflows (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL,
    definition   JSONB NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- workflow_runs
CREATE TABLE IF NOT EXISTS workflow_runs (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workflow_id  UUID NOT NULL REFERENCES workflows(id) ON DELETE CASCADE,
    status       TEXT NOT NULL DEFAULT 'pending',
    input        JSONB NOT NULL DEFAULT '{}',
    output       JSONB NOT NULL DEFAULT '{}',
    started_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at  TIMESTAMPTZ
);

-- scheduled_tasks
CREATE TABLE IF NOT EXISTS scheduled_tasks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT NOT NULL,
    cron_expr    TEXT NOT NULL,
    agent_id     UUID REFERENCES agents(id) ON DELETE CASCADE,
    payload      JSONB NOT NULL DEFAULT '{}',
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    last_run_at  TIMESTAMPTZ,
    next_run_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- webhooks
CREATE TABLE IF NOT EXISTS webhooks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    url          TEXT NOT NULL,
    events       TEXT[] NOT NULL DEFAULT '{}',
    secret_hint  TEXT,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- webhook_deliveries
CREATE TABLE IF NOT EXISTS webhook_deliveries (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    webhook_id   UUID NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
    event_type   TEXT NOT NULL,
    payload      JSONB NOT NULL DEFAULT '{}',
    status_code  INT,
    response     TEXT,
    delivered_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

- [ ] **Step 3: commit**

```bash
git add internal/migration/sql/tenant_schema.sql
git commit -m "feat(migration): add per-tenant schema DDL for all tenant tables"
```

---

### Task 7: ProvisionTenantSchema — schema.go

**Files:**
- Create: `pkg/tenantdb/schema.go`
- Create: `pkg/tenantdb/schema_test.go`

前置条件：Task 6 的 `internal/migration/sql/tenant_schema.sql` 已存在。`ProvisionTenantSchema` 将在 Plan 2 的 `CreateTenant`（`internal/tenant/onboard.go`）中被调用。

- [ ] **Step 1: 创建 pkg/tenantdb/schema.go**

```go
package tenantdb

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed ../../internal/migration/sql/tenant_schema.sql
var tenantSchemaDDL string

// ProvisionTenantSchema creates the schema for tenantID (if not exists) and
// executes all per-tenant DDL within that schema. Safe to call multiple times.
//
// tenantID must match [a-z0-9_-]+ (validated by isSafeTenantIDChar).
func ProvisionTenantSchema(ctx context.Context, pool *pgxpool.Pool, tenantID string) error {
	if tenantID == "" {
		return fmt.Errorf("tenantdb: tenantID must not be empty")
	}
	for _, r := range tenantID {
		if !isSafeTenantIDChar(r) {
			return fmt.Errorf("tenantdb: invalid tenantID %q", tenantID)
		}
	}

	schemaName := "tenant_" + tenantID

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("tenantdb: acquire conn: %w", err)
	}
	defer conn.Release()

	// 1. Create schema
	if _, err := conn.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+schemaName); err != nil {
		return fmt.Errorf("tenantdb: create schema %s: %w", schemaName, err)
	}

	// 2. Set search_path for this connection
	if _, err := conn.Exec(ctx, "SET search_path = "+schemaName+", public"); err != nil {
		return fmt.Errorf("tenantdb: set search_path: %w", err)
	}

	// 3. Execute each DDL statement from the embedded SQL file
	stmts := splitStatements(tenantSchemaDDL)
	for i, stmt := range stmts {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" || strings.HasPrefix(stmt, "--") {
			continue
		}
		if _, err := conn.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("tenantdb: exec statement %d: %w", i, err)
		}
	}

	return nil
}

// splitStatements splits a SQL file on semicolons, returning non-empty statements.
func splitStatements(sql string) []string {
	parts := strings.Split(sql, ";")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			result = append(result, s)
		}
	}
	return result
}
```

- [ ] **Step 2: 创建 pkg/tenantdb/schema_test.go**

```go
//go:build integration

package tenantdb_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func TestProvisionTenantSchema(t *testing.T) {
	url := os.Getenv("TEST_POSTGRES_URL")
	if url == "" {
		t.Skip("TEST_POSTGRES_URL not set")
	}

	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	defer pool.Close()

	tenantID := "integtest"
	if err := tenantdb.ProvisionTenantSchema(context.Background(), pool, tenantID); err != nil {
		t.Fatalf("ProvisionTenantSchema: %v", err)
	}

	// Verify schema exists
	var exists bool
	err = pool.QueryRow(context.Background(),
		"SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = $1)",
		"tenant_integtest",
	).Scan(&exists)
	if err != nil {
		t.Fatalf("query schema existence: %v", err)
	}
	if !exists {
		t.Error("schema tenant_integtest was not created")
	}

	// Verify at least one table exists (agents)
	var tableExists bool
	err = pool.QueryRow(context.Background(),
		"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema = $1 AND table_name = 'agents')",
		"tenant_integtest",
	).Scan(&tableExists)
	if err != nil {
		t.Fatalf("query table existence: %v", err)
	}
	if !tableExists {
		t.Error("table agents was not created in tenant_integtest schema")
	}

	// Calling twice is idempotent (IF NOT EXISTS)
	if err := tenantdb.ProvisionTenantSchema(context.Background(), pool, tenantID); err != nil {
		t.Fatalf("second ProvisionTenantSchema: %v", err)
	}
}

func TestProvisionTenantSchema_InvalidTenantID(t *testing.T) {
	err := tenantdb.ProvisionTenantSchema(context.Background(), nil, "bad tenant!")
	if err == nil {
		t.Fatal("expected error for invalid tenantID")
	}
}

func TestProvisionTenantSchema_EmptyTenantID(t *testing.T) {
	err := tenantdb.ProvisionTenantSchema(context.Background(), nil, "")
	if err == nil {
		t.Fatal("expected error for empty tenantID")
	}
}
```

- [ ] **Step 3: 添加 schema 校验的纯单元测试到 schema_unit_test.go**

```go
package tenantdb_test

import (
	"context"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func TestProvisionTenantSchema_EmptyTenantID_Unit(t *testing.T) {
	err := tenantdb.ProvisionTenantSchema(context.Background(), nil, "")
	if err == nil {
		t.Fatal("expected error for empty tenantID")
	}
}

func TestProvisionTenantSchema_InvalidTenantID_Unit(t *testing.T) {
	err := tenantdb.ProvisionTenantSchema(context.Background(), nil, "bad tenant!")
	if err == nil {
		t.Fatal("expected error for invalid tenantID")
	}
}
```

- [ ] **Step 4: 运行单元测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./pkg/tenantdb/ -run "TestProvisionTenantSchema.*Unit" -short
```

Expected:
```
--- PASS: TestProvisionTenantSchema_EmptyTenantID_Unit
--- PASS: TestProvisionTenantSchema_InvalidTenantID_Unit
PASS
```

- [ ] **Step 5: go vet**

```bash
go vet ./pkg/tenantdb/
```

Expected: 无输出。

- [ ] **Step 6: commit**

```bash
git add pkg/tenantdb/schema.go pkg/tenantdb/schema_test.go pkg/tenantdb/schema_unit_test.go
git commit -m "feat(tenantdb): add ProvisionTenantSchema with embedded DDL"
```

---

### Task 8: Tenant Middleware — api/middleware/tenant.go

**Files:**
- Create: `api/middleware/tenant.go`
- Create: `api/middleware/tenant_test.go`

前置条件：`github.com/golang-jwt/jwt/v5` 已在 Task 1 Step 1 中引入。

JWT Claims 结构约定：
- `sub` — user_id
- `tenant_id` — 租户 ID（global_admin 时为空字符串或缺失）
- `role` — "tenant_admin" | "tenant_user" | "global_admin"

- [ ] **Step 1: 创建 api/middleware/tenant.go**

```go
package middleware

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

// TenantClaims extends jwt.RegisteredClaims with tenant-specific fields.
type TenantClaims struct {
	jwt.RegisteredClaims
	TenantID string          `json:"tenant_id"`
	Role     tenantdb.Role   `json:"role"`
}

// TenantMiddleware parses the Bearer JWT token from the Authorization header
// and injects a TenantContext into the request context.
//
// For global_admin tokens (role="global_admin"), TenantID may be empty.
// For all other roles, a non-empty TenantID is required.
//
// jwtSecret is the HMAC-SHA256 signing secret used to verify the token.
func TenantMiddleware(jwtSecret []byte, logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing Authorization header"})
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid Authorization format, expected Bearer token"})
			return
		}
		tokenStr := parts[1]

		claims := &TenantClaims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return jwtSecret, nil
		})
		if err != nil || !token.Valid {
			logger.Warn("invalid JWT token", zap.Error(err))
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}

		// Validate role
		switch claims.Role {
		case tenantdb.RoleTenantAdmin, tenantdb.RoleTenantUser, tenantdb.RoleGlobalAdmin:
			// valid
		default:
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "unknown role in token"})
			return
		}

		// Non-admin roles must have a tenant_id
		if claims.Role != tenantdb.RoleGlobalAdmin && claims.TenantID == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "tenant_id required for non-global_admin role"})
			return
		}

		tc := &tenantdb.TenantContext{
			TenantID: claims.TenantID,
			UserID:   claims.Subject,
			Role:     claims.Role,
		}

		ctx := tenantdb.WithTenant(c.Request.Context(), tc)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
```

- [ ] **Step 2: 创建 api/middleware/tenant_test.go**

```go
package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"

	"github.com/byteBuilderX/ClawHermes-AI-Go/api/middleware"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
)

func init() {
	gin.SetMode(gin.TestMode)
}

var testSecret = []byte("test-secret-key-32-bytes-minimum!")

func makeToken(tenantID string, role tenantdb.Role, userID string) string {
	claims := middleware.TenantClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
		TenantID: tenantID,
		Role:     role,
	}
	token, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(testSecret)
	return token
}

func TestTenantMiddleware_ValidTenantAdmin(t *testing.T) {
	r := gin.New()
	r.Use(middleware.TenantMiddleware(testSecret, zap.NewNop()))
	r.GET("/test", func(c *gin.Context) {
		tc, ok := tenantdb.FromContext(c.Request.Context())
		if !ok {
			c.JSON(500, gin.H{"error": "no tenant context"})
			return
		}
		c.JSON(200, gin.H{"tenant_id": tc.TenantID, "role": string(tc.Role)})
	})

	token := makeToken("acme", tenantdb.RoleTenantAdmin, "user-1")
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTenantMiddleware_GlobalAdmin_NoTenantID(t *testing.T) {
	r := gin.New()
	r.Use(middleware.TenantMiddleware(testSecret, zap.NewNop()))
	r.GET("/test", func(c *gin.Context) {
		tc, ok := tenantdb.FromContext(c.Request.Context())
		if !ok {
			c.JSON(500, gin.H{"error": "no tenant context"})
			return
		}
		c.JSON(200, gin.H{"tenant_id": tc.TenantID, "role": string(tc.Role)})
	})

	token := makeToken("", tenantdb.RoleGlobalAdmin, "admin-1")
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTenantMiddleware_MissingHeader(t *testing.T) {
	r := gin.New()
	r.Use(middleware.TenantMiddleware(testSecret, zap.NewNop()))
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestTenantMiddleware_InvalidToken(t *testing.T) {
	r := gin.New()
	r.Use(middleware.TenantMiddleware(testSecret, zap.NewNop()))
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer not.a.valid.token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestTenantMiddleware_NonAdminMissingTenantID(t *testing.T) {
	r := gin.New()
	r.Use(middleware.TenantMiddleware(testSecret, zap.NewNop()))
	r.GET("/test", func(c *gin.Context) { c.Status(200) })

	// tenant_user with no tenant_id — should be rejected
	token := makeToken("", tenantdb.RoleTenantUser, "user-x")
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w.Code)
	}
}
```

- [ ] **Step 3: 运行测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./api/middleware/ -run TestTenantMiddleware -short
```

Expected:
```
--- PASS: TestTenantMiddleware_ValidTenantAdmin
--- PASS: TestTenantMiddleware_GlobalAdmin_NoTenantID
--- PASS: TestTenantMiddleware_MissingHeader
--- PASS: TestTenantMiddleware_InvalidToken
--- PASS: TestTenantMiddleware_NonAdminMissingTenantID
PASS
```

- [ ] **Step 4: 运行全量测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./... -short
```

Expected: 所有原有测试 + 新增测试全部 PASS，无 FAIL。

- [ ] **Step 5: go vet**

```bash
go vet ./pkg/tenantdb/ ./api/middleware/
```

Expected: 无输出。

- [ ] **Step 6: commit**

```bash
git add api/middleware/tenant.go api/middleware/tenant_test.go
git commit -m "feat(middleware): add TenantMiddleware for JWT-based tenant injection"
```

---

## 集成测试运行方式

集成测试（`//go:build integration`）需要实际 PostgreSQL 实例：

```bash
# 启动依赖（Plan 1 的 docker-compose 已配置 postgres）
docker compose up -d postgres

# 运行集成测试
TEST_POSTGRES_URL="postgres://clawhermes:clawhermes@localhost:5432/clawhermes" \
  go test -v -race -tags integration ./pkg/tenantdb/ -short
```

---

## ProvisionTenantSchema 调用点

Plan 2 的 `internal/tenant/onboard.go` 中 `CreateTenant` 函数在新建租户后调用：

```go
// 在 CreateTenant 中，persist 完 tenants 表记录后：
if err := tenantdb.ProvisionTenantSchema(ctx, pool.DB(), tenant.ID); err != nil {
    return nil, fmt.Errorf("provision schema: %w", err)
}
```

`pool.DB()` 返回 `*pgxpool.Pool`，与 `ProvisionTenantSchema` 签名匹配。
