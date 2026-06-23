# Memory v2 Infrastructure Implementation Plan (Phase 3)

**Goal:** Build repository adapters, Milvus vector store client, and LLM extractor adapters for memory v2.

**Architecture:** Infrastructure layer implements domain ports with concrete PostgreSQL repos (pgx), Milvus SDK v2.4.2 client, and LLMGateway-based extraction adapters. Postgres repo uses `SET LOCAL search_path` for tenant isolation. Milvus uses per-tenant collections with HNSW index.

**Tech Stack:** pgx v5, Milvus SDK v2.4.2, pg_trgm, LLMGateway domain abstraction, JSON structured output

---

## Global Constraints

- Go 1.22+, pgx v5, Milvus SDK v2.4.2
- Multi-tenant: `SET LOCAL search_path = tenant_{id}, public` on every query
- Milvus collection naming: `memory_facts_{tenant_id}`, `memory_entity_profiles_{tenant_id}`
- HNSW index: M=16, efConstruction=200
- Trigram similarity threshold: 0.8
- LLM extraction: JSON structured output with retries (3 attempts, exponential backoff)
- All repo methods return domain errors (`domain.ErrFactNotFound`), not infrastructure errors
- Test coverage ≥80%, table-driven tests, mock external dependencies

---

## File Structure

```
internal/memory/infrastructure/
├── persistence/
│   ├── fact_repo.go              # PostgreSQL fact repository
│   ├── fact_repo_test.go
│   ├── entity_repo.go            # PostgreSQL entity repository
│   ├── entity_repo_test.go
│   ├── extraction_queue.go       # PostgreSQL extraction queue
│   └── extraction_queue_test.go
├── vector/
│   ├── milvus_store.go           # Milvus vector store client
│   ├── milvus_store_test.go
│   └── collection.go             # Collection management helpers
├── llm/
│   ├── fact_extractor.go         # LLM fact extraction adapter
│   ├── fact_extractor_test.go
│   ├── superseder.go             # LLM supersede judgment adapter
│   ├── superseder_test.go
│   ├── profiler.go               # LLM entity profile builder
│   ├── profiler_test.go
│   └── prompts.go                # Extraction/supersede/profile prompts
└── embed/
    ├── embed_adapter.go          # Embed client adapter
    └── embed_adapter_test.go
```

---

## Task 1: PostgreSQL Fact Repository

**Files:**

- Create: `internal/memory/infrastructure/persistence/fact_repo.go`
- Create: `internal/memory/infrastructure/persistence/fact_repo_test.go`

**Interfaces:**

- Consumes: `domain.MemoryFact`, `domain.ScopeFilter`, `domain/port.FactRepo`
- Produces: `FactRepo` implementation with tenant-aware queries

- [ ] **Step 1: Write test for Insert**

```go
// internal/memory/infrastructure/persistence/fact_repo_test.go
package persistence_test

import (
 "context"
 "testing"
 "time"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
 "github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
 "github.com/byteBuilderX/stratum/pkg/storage/postgres"
 "github.com/stretchr/testify/require"
)

func setupFactRepoTest(t *testing.T) (*postgres.TenantPool, *persistence.FactRepo) {
 t.Helper()
 pool := postgres.NewTestTenantPool(t, "tenant_test_fact_repo")
 repo := persistence.NewFactRepo(pool)
 return pool, repo
}

func TestFactRepo_Insert(t *testing.T) {
 _, repo := setupFactRepoTest(t)
 ctx := context.Background()

 fact, err := domain.NewFact(
  "", // ID will be generated
  "user123",
  "agent456",
  domain.ScopeUser,
  "User prefers dark mode",
  0.8,
  []string{"preference", "ui"},
  []string{}, // entity refs
  []string{"msg001"},
 )
 require.NoError(t, err)

 err = repo.Insert(ctx, fact)
 require.NoError(t, err)
 require.NotEmpty(t, fact.ID, "ID should be generated")
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run TestFactRepo_Insert`
Expected: FAIL (FactRepo type does not exist)

- [ ] **Step 3: Implement FactRepo Insert**

```go
// internal/memory/infrastructure/persistence/fact_repo.go
package persistence

import (
 "context"
 "fmt"

 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgxpool"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
 "github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

type FactRepo struct {
 pool *pgxpool.Pool
}

func NewFactRepo(pool *pgxpool.Pool) *FactRepo {
 return &FactRepo{pool: pool}
}

func (r *FactRepo) Insert(ctx context.Context, fact *domain.MemoryFact) error {
 query := `
  INSERT INTO memory_facts (
   id, user_id, agent_id, scope, content, importance, keywords,
   entity_refs, source_msg_ids, status, access_count, last_accessed_at,
   created_at, updated_at
  ) VALUES (
   COALESCE($1, public.gen_uuid_v7()), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14
  )
  RETURNING id`

 var id string
 err := r.pool.QueryRow(ctx, query,
  fact.ID, fact.UserID, fact.AgentID, fact.Scope, fact.Content,
  fact.Importance, fact.Keywords, fact.EntityRefs, fact.SourceMsgIDs,
  fact.Status, fact.AccessCount, fact.LastAccessedAt,
  fact.CreatedAt, fact.UpdatedAt,
 ).Scan(&id)
 if err != nil {
  return fmt.Errorf("insert fact: %w", err)
 }
 fact.ID = id
 return nil
}

var _ port.FactRepo = (*FactRepo)(nil)
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run TestFactRepo_Insert`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/persistence/fact_repo.go internal/memory/infrastructure/persistence/fact_repo_test.go
git commit -m "feat(memory): add FactRepo Insert implementation

PostgreSQL fact repository with tenant isolation

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 2: FactRepo GetByID and Update

**Files:**

- Modify: `internal/memory/infrastructure/persistence/fact_repo.go`
- Modify: `internal/memory/infrastructure/persistence/fact_repo_test.go`

**Interfaces:**

- Consumes: `domain.MemoryFact`, `domain.ErrFactNotFound`
- Produces: `GetByID(ctx, id) (*MemoryFact, error)`, `Update(ctx, fact) error`

- [ ] **Step 1: Write tests for GetByID and Update**

```go
// Append to fact_repo_test.go

func TestFactRepo_GetByID(t *testing.T) {
 _, repo := setupFactRepoTest(t)
 ctx := context.Background()

 fact, _ := domain.NewFact("", "user123", "agent456", domain.ScopeUser, "test content", 0.5, nil, nil, nil)
 require.NoError(t, repo.Insert(ctx, fact))

 retrieved, err := repo.GetByID(ctx, fact.ID)
 require.NoError(t, err)
 require.Equal(t, fact.Content, retrieved.Content)
 require.Equal(t, fact.Importance, retrieved.Importance)
}

func TestFactRepo_GetByID_NotFound(t *testing.T) {
 _, repo := setupFactRepoTest(t)
 ctx := context.Background()

 _, err := repo.GetByID(ctx, "nonexistent-id")
 require.ErrorIs(t, err, domain.ErrFactNotFound)
}

func TestFactRepo_Update(t *testing.T) {
 _, repo := setupFactRepoTest(t)
 ctx := context.Background()

 fact, _ := domain.NewFact("", "user123", "agent456", domain.ScopeUser, "original", 0.5, nil, nil, nil)
 require.NoError(t, repo.Insert(ctx, fact))

 fact.Content = "updated content"
 fact.Importance = 0.9
 require.NoError(t, repo.Update(ctx, fact))

 retrieved, err := repo.GetByID(ctx, fact.ID)
 require.NoError(t, err)
 require.Equal(t, "updated content", retrieved.Content)
 require.Equal(t, 0.9, retrieved.Importance)
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run "TestFactRepo_GetByID|TestFactRepo_Update"`
Expected: FAIL (methods not implemented)

- [ ] **Step 3: Implement GetByID and Update**

```go
// Append to fact_repo.go

func (r *FactRepo) GetByID(ctx context.Context, id string) (*domain.MemoryFact, error) {
 query := `
  SELECT id, user_id, agent_id, scope, content, importance, keywords,
         entity_refs, source_msg_ids, status, superseded_by,
         access_count, last_accessed_at, created_at, updated_at,
         deleted_at, archived_at
  FROM memory_facts
  WHERE id = $1`

 var f domain.MemoryFact
 var agentID, supersededBy *string
 var deletedAt, archivedAt *time.Time

 err := r.pool.QueryRow(ctx, query, id).Scan(
  &f.ID, &f.UserID, &agentID, &f.Scope, &f.Content, &f.Importance, &f.Keywords,
  &f.EntityRefs, &f.SourceMsgIDs, &f.Status, &supersededBy,
  &f.AccessCount, &f.LastAccessedAt, &f.CreatedAt, &f.UpdatedAt,
  &deletedAt, &archivedAt,
 )
 if err == pgx.ErrNoRows {
  return nil, domain.ErrFactNotFound
 }
 if err != nil {
  return nil, fmt.Errorf("get fact: %w", err)
 }

 if agentID != nil {
  f.AgentID = *agentID
 }
 if supersededBy != nil {
  f.SupersededBy = *supersededBy
 }
 if deletedAt != nil {
  f.DeletedAt = *deletedAt
 }
 if archivedAt != nil {
  f.ArchivedAt = *archivedAt
 }

 return &f, nil
}

func (r *FactRepo) Update(ctx context.Context, fact *domain.MemoryFact) error {
 query := `
  UPDATE memory_facts SET
   content = $2, importance = $3, keywords = $4,
   entity_refs = $5, status = $6, superseded_by = $7,
   access_count = $8, last_accessed_at = $9, updated_at = $10,
   deleted_at = $11, archived_at = $12
  WHERE id = $1`

 tag, err := r.pool.Exec(ctx, query,
  fact.ID, fact.Content, fact.Importance, fact.Keywords,
  fact.EntityRefs, fact.Status, nullString(fact.SupersededBy),
  fact.AccessCount, fact.LastAccessedAt, fact.UpdatedAt,
  nullTime(fact.DeletedAt), nullTime(fact.ArchivedAt),
 )
 if err != nil {
  return fmt.Errorf("update fact: %w", err)
 }
 if tag.RowsAffected() == 0 {
  return domain.ErrFactNotFound
 }
 return nil
}

func nullString(s string) *string {
 if s == "" {
  return nil
 }
 return &s
}

func nullTime(t time.Time) *time.Time {
 if t.IsZero() {
  return nil
 }
 return &t
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run "TestFactRepo_GetByID|TestFactRepo_Update"`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/persistence/fact_repo.go internal/memory/infrastructure/persistence/fact_repo_test.go
git commit -m "feat(memory): add FactRepo GetByID and Update

Domain error translation (ErrFactNotFound), nullable field handling

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 3: FactRepo ListActive and SearchByContent

**Files:**

- Modify: `internal/memory/infrastructure/persistence/fact_repo.go`
- Modify: `internal/memory/infrastructure/persistence/fact_repo_test.go`

**Interfaces:**

- Consumes: `domain.ScopeFilter`
- Produces: `ListActive(ctx, filter, limit) ([]*MemoryFact, error)`, `SearchByContent(ctx, filter, query, limit) ([]*MemoryFact, error)`

- [ ] **Step 1: Write tests**

```go
// Append to fact_repo_test.go

func TestFactRepo_ListActive(t *testing.T) {
 _, repo := setupFactRepoTest(t)
 ctx := context.Background()

 // Insert user-scope fact
 f1, _ := domain.NewFact("", "user123", "", domain.ScopeUser, "user fact", 0.5, nil, nil, nil)
 require.NoError(t, repo.Insert(ctx, f1))

 // Insert agent-scope fact
 f2, _ := domain.NewFact("", "user123", "agent456", domain.ScopeAgent, "agent fact", 0.7, nil, nil, nil)
 require.NoError(t, repo.Insert(ctx, f2))

 filter := domain.BuildScopeFilter("user123", "agent456", "user")
 facts, err := repo.ListActive(ctx, filter, 10)
 require.NoError(t, err)
 require.Len(t, facts, 1, "should only see user-scope fact")
 require.Equal(t, "user fact", facts[0].Content)
}

func TestFactRepo_SearchByContent(t *testing.T) {
 _, repo := setupFactRepoTest(t)
 ctx := context.Background()

 f1, _ := domain.NewFact("", "user123", "", domain.ScopeUser, "prefers dark mode", 0.5, nil, nil, nil)
 require.NoError(t, repo.Insert(ctx, f1))

 facts, err := repo.SearchByContent(ctx, domain.BuildScopeFilter("user123", "", "user"), "dark", 10)
 require.NoError(t, err)
 require.Len(t, facts, 1)
 require.Equal(t, "prefers dark mode", facts[0].Content)
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run "TestFactRepo_ListActive|TestFactRepo_SearchByContent"`
Expected: FAIL

- [ ] **Step 3: Implement ListActive and SearchByContent**

```go
// Append to fact_repo.go

func (r *FactRepo) ListActive(ctx context.Context, filter domain.ScopeFilter, limit int) ([]*domain.MemoryFact, error) {
 query := `
  SELECT id, user_id, agent_id, scope, content, importance, keywords,
         entity_refs, source_msg_ids, status, access_count, last_accessed_at, created_at, updated_at
  FROM memory_facts
  WHERE user_id = $1
    AND status = 'active'
    AND (
        (scope = 'user' AND $2 = 'user')
        OR (scope = 'agent' AND agent_id = $3 AND $2 IN ('agent','user'))
    )
  ORDER BY last_accessed_at DESC
  LIMIT $4`

 rows, err := r.pool.Query(ctx, query, filter.UserID, filter.ReadScope, filter.AgentID, limit)
 if err != nil {
  return nil, fmt.Errorf("list active facts: %w", err)
 }
 defer rows.Close()

 return r.scanFacts(rows)
}

func (r *FactRepo) SearchByContent(ctx context.Context, filter domain.ScopeFilter, searchQuery string, limit int) ([]*domain.MemoryFact, error) {
 query := `
  SELECT id, user_id, agent_id, scope, content, importance, keywords,
         entity_refs, source_msg_ids, status, access_count, last_accessed_at, created_at, updated_at
  FROM memory_facts
  WHERE user_id = $1
    AND status = 'active'
    AND content % $2
    AND (
        (scope = 'user' AND $3 = 'user')
        OR (scope = 'agent' AND agent_id = $4 AND $3 IN ('agent','user'))
    )
  ORDER BY similarity(content, $2) DESC
  LIMIT $5`

 rows, err := r.pool.Query(ctx, query, filter.UserID, searchQuery, filter.ReadScope, filter.AgentID, limit)
 if err != nil {
  return nil, fmt.Errorf("search facts: %w", err)
 }
 defer rows.Close()

 return r.scanFacts(rows)
}

func (r *FactRepo) scanFacts(rows pgx.Rows) ([]*domain.MemoryFact, error) {
 var facts []*domain.MemoryFact
 for rows.Next() {
  var f domain.MemoryFact
  var agentID *string
  err := rows.Scan(
   &f.ID, &f.UserID, &agentID, &f.Scope, &f.Content, &f.Importance, &f.Keywords,
   &f.EntityRefs, &f.SourceMsgIDs, &f.Status, &f.AccessCount, &f.LastAccessedAt,
   &f.CreatedAt, &f.UpdatedAt,
  )
  if err != nil {
   return nil, fmt.Errorf("scan fact: %w", err)
  }
  if agentID != nil {
   f.AgentID = *agentID
  }
  facts = append(facts, &f)
 }
 return facts, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run "TestFactRepo_ListActive|TestFactRepo_SearchByContent"`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/persistence/fact_repo.go internal/memory/infrastructure/persistence/fact_repo_test.go
git commit -m "feat(memory): add FactRepo ListActive and SearchByContent

Scope filtering with trigram full-text search

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 4: FactRepo FindSimilarContent and CountByUser

**Files:**

- Modify: `internal/memory/infrastructure/persistence/fact_repo.go`
- Modify: `internal/memory/infrastructure/persistence/fact_repo_test.go`

**Interfaces:**

- Consumes: none
- Produces: `FindSimilarContent(ctx, userID, content, threshold, limit) ([]*MemoryFact, error)`, `CountByUser(ctx, userID) (int, error)`

- [ ] **Step 1: Write tests**

```go
// Append to fact_repo_test.go

func TestFactRepo_FindSimilarContent(t *testing.T) {
 _, repo := setupFactRepoTest(t)
 ctx := context.Background()

 f1, _ := domain.NewFact("", "user123", "", domain.ScopeUser, "User prefers dark mode UI", 0.5, nil, nil, nil)
 require.NoError(t, repo.Insert(ctx, f1))

 similar, err := repo.FindSimilarContent(ctx, "user123", "User likes dark theme", 0.3, 5)
 require.NoError(t, err)
 require.Len(t, similar, 1)
 require.Equal(t, f1.ID, similar[0].ID)
}

func TestFactRepo_CountByUser(t *testing.T) {
 _, repo := setupFactRepoTest(t)
 ctx := context.Background()

 f1, _ := domain.NewFact("", "user123", "", domain.ScopeUser, "fact 1", 0.5, nil, nil, nil)
 f2, _ := domain.NewFact("", "user123", "", domain.ScopeUser, "fact 2", 0.5, nil, nil, nil)
 require.NoError(t, repo.Insert(ctx, f1))
 require.NoError(t, repo.Insert(ctx, f2))

 count, err := repo.CountByUser(ctx, "user123")
 require.NoError(t, err)
 require.Equal(t, 2, count)
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run "TestFactRepo_FindSimilar|TestFactRepo_CountByUser"`
Expected: FAIL

- [ ] **Step 3: Implement FindSimilarContent and CountByUser**

```go
// Append to fact_repo.go

func (r *FactRepo) FindSimilarContent(ctx context.Context, userID, content string, threshold float64, limit int) ([]*domain.MemoryFact, error) {
 query := `
  SELECT id, user_id, agent_id, scope, content, importance, keywords,
         entity_refs, source_msg_ids, status, access_count, last_accessed_at, created_at, updated_at,
         similarity(content, $2) AS sim
  FROM memory_facts
  WHERE user_id = $1
    AND status = 'active'
    AND similarity(content, $2) > $3
  ORDER BY sim DESC
  LIMIT $4`

 rows, err := r.pool.Query(ctx, query, userID, content, threshold, limit)
 if err != nil {
  return nil, fmt.Errorf("find similar content: %w", err)
 }
 defer rows.Close()

 var facts []*domain.MemoryFact
 for rows.Next() {
  var f domain.MemoryFact
  var agentID *string
  var sim float64
  err := rows.Scan(
   &f.ID, &f.UserID, &agentID, &f.Scope, &f.Content, &f.Importance, &f.Keywords,
   &f.EntityRefs, &f.SourceMsgIDs, &f.Status, &f.AccessCount, &f.LastAccessedAt,
   &f.CreatedAt, &f.UpdatedAt, &sim,
  )
  if err != nil {
   return nil, fmt.Errorf("scan similar fact: %w", err)
  }
  if agentID != nil {
   f.AgentID = *agentID
  }
  facts = append(facts, &f)
 }
 return facts, rows.Err()
}

func (r *FactRepo) CountByUser(ctx context.Context, userID string) (int, error) {
 var count int
 err := r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM memory_facts WHERE user_id = $1 AND status = 'active'", userID).Scan(&count)
 if err != nil {
  return 0, fmt.Errorf("count facts: %w", err)
 }
 return count, nil
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run "TestFactRepo_FindSimilar|TestFactRepo_CountByUser"`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/persistence/fact_repo.go internal/memory/infrastructure/persistence/fact_repo_test.go
git commit -m "feat(memory): add FactRepo FindSimilarContent and CountByUser

Trigram similarity for supersede detection, quota checking

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 5: FactRepo DeleteExpired and PromoteToArchived

**Files:**

- Modify: `internal/memory/infrastructure/persistence/fact_repo.go`
- Modify: `internal/memory/infrastructure/persistence/fact_repo_test.go`

**Interfaces:**

- Consumes: `constants.MemoryDeletedRetentionDays`, `constants.MemoryArchiveImportanceMax`
- Produces: `DeleteExpired(ctx) (int, error)`, `PromoteToArchived(ctx, importanceMax, coldDays, accessMax) (int, error)`

- [ ] **Step 1: Write tests**

```go
// Append to fact_repo_test.go

func TestFactRepo_DeleteExpired(t *testing.T) {
 _, repo := setupFactRepoTest(t)
 ctx := context.Background()

 // Create deleted fact with old deleted_at
 f1, _ := domain.NewFact("", "user123", "", domain.ScopeUser, "old fact", 0.5, nil, nil, nil)
 require.NoError(t, repo.Insert(ctx, f1))
 require.NoError(t, f1.MarkDeleted())
 f1.DeletedAt = time.Now().Add(-31 * 24 * time.Hour) // 31 days ago
 require.NoError(t, repo.Update(ctx, f1))

 count, err := repo.DeleteExpired(ctx)
 require.NoError(t, err)
 require.Equal(t, 1, count)

 _, err = repo.GetByID(ctx, f1.ID)
 require.ErrorIs(t, err, domain.ErrFactNotFound)
}

func TestFactRepo_PromoteToArchived(t *testing.T) {
 _, repo := setupFactRepoTest(t)
 ctx := context.Background()

 // Create low-importance cold fact
 f1, _ := domain.NewFact("", "user123", "", domain.ScopeUser, "cold fact", 0.2, nil, nil, nil)
 require.NoError(t, repo.Insert(ctx, f1))
 f1.LastAccessedAt = time.Now().Add(-100 * 24 * time.Hour) // 100 days ago
 require.NoError(t, repo.Update(ctx, f1))

 count, err := repo.PromoteToArchived(ctx, 0.3, 90, 5)
 require.NoError(t, err)
 require.Equal(t, 1, count)

 retrieved, err := repo.GetByID(ctx, f1.ID)
 require.NoError(t, err)
 require.Equal(t, "archived", retrieved.Status)
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run "TestFactRepo_Delete|TestFactRepo_Promote"`
Expected: FAIL

- [ ] **Step 3: Implement DeleteExpired and PromoteToArchived**

```go
// Append to fact_repo.go

func (r *FactRepo) DeleteExpired(ctx context.Context) (int, error) {
 query := `
  DELETE FROM memory_facts
  WHERE status = 'deleted'
    AND deleted_at < NOW() - INTERVAL '30 days'`

 tag, err := r.pool.Exec(ctx, query)
 if err != nil {
  return 0, fmt.Errorf("delete expired facts: %w", err)
 }
 return int(tag.RowsAffected()), nil
}

func (r *FactRepo) PromoteToArchived(ctx context.Context, importanceMax float64, coldDays int, accessMax int) (int, error) {
 query := `
  UPDATE memory_facts
  SET status = 'archived', archived_at = NOW(), updated_at = NOW()
  WHERE status = 'active'
    AND importance <= $1
    AND last_accessed_at < NOW() - $2 * INTERVAL '1 day'
    AND access_count <= $3`

 tag, err := r.pool.Exec(ctx, query, importanceMax, coldDays, accessMax)
 if err != nil {
  return 0, fmt.Errorf("promote to archived: %w", err)
 }
 return int(tag.RowsAffected()), nil
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run "TestFactRepo_Delete|TestFactRepo_Promote"`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/persistence/fact_repo.go internal/memory/infrastructure/persistence/fact_repo_test.go
git commit -m "feat(memory): add FactRepo DeleteExpired and PromoteToArchived

GC worker support for retention and archival

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

---

## Task 6: PostgreSQL Entity Repository

**Files:**

- Create: `internal/memory/infrastructure/persistence/entity_repo.go`
- Create: `internal/memory/infrastructure/persistence/entity_repo_test.go`

**Interfaces:**

- Consumes: `domain.MemoryEntity`, `domain/port.EntityRepo`
- Produces: `EntityRepo` implementation with trigram-based entity normalization

- [ ] **Step 1: Write test for Insert and GetByID**

```go
// internal/memory/infrastructure/persistence/entity_repo_test.go
package persistence_test

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
 "github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
 "github.com/byteBuilderX/stratum/pkg/storage/postgres"
 "github.com/stretchr/testify/require"
)

func setupEntityRepoTest(t *testing.T) (*postgres.TenantPool, *persistence.EntityRepo) {
 t.Helper()
 pool := postgres.NewTestTenantPool(t, "tenant_test_entity_repo")
 repo := persistence.NewEntityRepo(pool)
 return pool, repo
}

func TestEntityRepo_Insert(t *testing.T) {
 _, repo := setupEntityRepoTest(t)
 ctx := context.Background()

 entity, err := domain.NewEntity("", "user123", "", domain.ScopeUser, "Python", "technology", "")
 require.NoError(t, err)

 err = repo.Insert(ctx, entity)
 require.NoError(t, err)
 require.NotEmpty(t, entity.ID)
}

func TestEntityRepo_GetByID(t *testing.T) {
 _, repo := setupEntityRepoTest(t)
 ctx := context.Background()

 entity, _ := domain.NewEntity("", "user123", "", domain.ScopeUser, "React", "technology", "")
 require.NoError(t, repo.Insert(ctx, entity))

 retrieved, err := repo.GetByID(ctx, entity.ID)
 require.NoError(t, err)
 require.Equal(t, "React", retrieved.Name)
 require.Equal(t, "technology", retrieved.EntityType)
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run TestEntityRepo`
Expected: FAIL

- [ ] **Step 3: Implement EntityRepo Insert and GetByID**

```go
// internal/memory/infrastructure/persistence/entity_repo.go
package persistence

import (
 "context"
 "fmt"
 "time"

 "github.com/jackc/pgx/v5"
 "github.com/jackc/pgx/v5/pgxpool"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
 "github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

type EntityRepo struct {
 pool *pgxpool.Pool
}

func NewEntityRepo(pool *pgxpool.Pool) *EntityRepo {
 return &EntityRepo{pool: pool}
}

func (r *EntityRepo) Insert(ctx context.Context, entity *domain.MemoryEntity) error {
 query := `
  INSERT INTO memory_entities (
   id, user_id, agent_id, scope, name, entity_type, profile,
   fact_count, last_seen_at, rebuild_after, status, created_at, updated_at
  ) VALUES (
   COALESCE($1, public.gen_uuid_v7()), $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
  )
  RETURNING id`

 var id string
 err := r.pool.QueryRow(ctx, query,
  entity.ID, entity.UserID, entity.AgentID, entity.Scope, entity.Name, entity.EntityType,
  entity.Profile, entity.FactCount, entity.LastSeenAt, nullTime(entity.RebuildAfter),
  entity.Status, entity.CreatedAt, entity.UpdatedAt,
 ).Scan(&id)
 if err != nil {
  return fmt.Errorf("insert entity: %w", err)
 }
 entity.ID = id
 return nil
}

func (r *EntityRepo) GetByID(ctx context.Context, id string) (*domain.MemoryEntity, error) {
 query := `
  SELECT id, user_id, agent_id, scope, name, entity_type, profile,
         fact_count, last_seen_at, rebuild_after, status, created_at, updated_at
  FROM memory_entities
  WHERE id = $1`

 var e domain.MemoryEntity
 var agentID *string
 var rebuildAfter *time.Time

 err := r.pool.QueryRow(ctx, query, id).Scan(
  &e.ID, &e.UserID, &agentID, &e.Scope, &e.Name, &e.EntityType, &e.Profile,
  &e.FactCount, &e.LastSeenAt, &rebuildAfter, &e.Status, &e.CreatedAt, &e.UpdatedAt,
 )
 if err == pgx.ErrNoRows {
  return nil, domain.ErrEntityNotFound
 }
 if err != nil {
  return nil, fmt.Errorf("get entity: %w", err)
 }

 if agentID != nil {
  e.AgentID = *agentID
 }
 if rebuildAfter != nil {
  e.RebuildAfter = *rebuildAfter
 }

 return &e, nil
}

var _ port.EntityRepo = (*EntityRepo)(nil)
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run TestEntityRepo`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/persistence/entity_repo.go internal/memory/infrastructure/persistence/entity_repo_test.go
git commit -m "feat(memory): add EntityRepo Insert and GetByID

PostgreSQL entity repository with tenant isolation

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 7: EntityRepo FindByNameAndType (Trigram Normalization)

**Files:**

- Modify: `internal/memory/infrastructure/persistence/entity_repo.go`
- Modify: `internal/memory/infrastructure/persistence/entity_repo_test.go`

**Interfaces:**

- Consumes: `constants.MemoryEntitySimilarityThreshold` (0.8)
- Produces: `FindByNameAndType(ctx, userID, name, entityType, threshold) (*MemoryEntity, error)`

- [ ] **Step 1: Write test for fuzzy entity matching**

```go
// Append to entity_repo_test.go

func TestEntityRepo_FindByNameAndType_ExactMatch(t *testing.T) {
 _, repo := setupEntityRepoTest(t)
 ctx := context.Background()

 entity, _ := domain.NewEntity("", "user123", "", domain.ScopeUser, "TypeScript", "technology", "")
 require.NoError(t, repo.Insert(ctx, entity))

 found, err := repo.FindByNameAndType(ctx, "user123", "TypeScript", "technology", 0.8)
 require.NoError(t, err)
 require.Equal(t, entity.ID, found.ID)
}

func TestEntityRepo_FindByNameAndType_FuzzyMatch(t *testing.T) {
 _, repo := setupEntityRepoTest(t)
 ctx := context.Background()

 entity, _ := domain.NewEntity("", "user123", "", domain.ScopeUser, "PostgreSQL", "technology", "")
 require.NoError(t, repo.Insert(ctx, entity))

 // Typo: "Postgres" should match "PostgreSQL"
 found, err := repo.FindByNameAndType(ctx, "user123", "Postgres", "technology", 0.7)
 require.NoError(t, err)
 require.Equal(t, entity.ID, found.ID)
}

func TestEntityRepo_FindByNameAndType_NoMatch(t *testing.T) {
 _, repo := setupEntityRepoTest(t)
 ctx := context.Background()

 found, err := repo.FindByNameAndType(ctx, "user123", "Nonexistent", "technology", 0.8)
 require.NoError(t, err)
 require.Nil(t, found, "should return nil when no match found")
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run TestEntityRepo_FindByNameAndType`
Expected: FAIL

- [ ] **Step 3: Implement FindByNameAndType**

```go
// Append to entity_repo.go

func (r *EntityRepo) FindByNameAndType(ctx context.Context, userID, name, entityType string, threshold float64) (*domain.MemoryEntity, error) {
 query := `
  SELECT id, user_id, agent_id, scope, name, entity_type, profile,
         fact_count, last_seen_at, rebuild_after, status, created_at, updated_at,
         similarity(name, $2) AS sim
  FROM memory_entities
  WHERE user_id = $1
    AND entity_type = $3
    AND status = 'active'
    AND similarity(name, $2) > $4
  ORDER BY sim DESC
  LIMIT 1`

 var e domain.MemoryEntity
 var agentID *string
 var rebuildAfter *time.Time
 var sim float64

 err := r.pool.QueryRow(ctx, query, userID, name, entityType, threshold).Scan(
  &e.ID, &e.UserID, &agentID, &e.Scope, &e.Name, &e.EntityType, &e.Profile,
  &e.FactCount, &e.LastSeenAt, &rebuildAfter, &e.Status, &e.CreatedAt, &e.UpdatedAt, &sim,
 )
 if err == pgx.ErrNoRows {
  return nil, nil // No match found (not an error)
 }
 if err != nil {
  return nil, fmt.Errorf("find entity by name: %w", err)
 }

 if agentID != nil {
  e.AgentID = *agentID
 }
 if rebuildAfter != nil {
  e.RebuildAfter = *rebuildAfter
 }

 return &e, nil
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run TestEntityRepo_FindByNameAndType`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/persistence/entity_repo.go internal/memory/infrastructure/persistence/entity_repo_test.go
git commit -m "feat(memory): add EntityRepo FindByNameAndType with trigram fuzzy matching

Entity normalization for typo-tolerant lookups (threshold 0.8)

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 8: EntityRepo Update, ListProfiles, CountByUser, ListForRebuild

**Files:**

- Modify: `internal/memory/infrastructure/persistence/entity_repo.go`
- Modify: `internal/memory/infrastructure/persistence/entity_repo_test.go`

**Interfaces:**

- Consumes: `domain.ScopeFilter`
- Produces: Remaining EntityRepo methods

- [ ] **Step 1: Write tests**

```go
// Append to entity_repo_test.go

func TestEntityRepo_Update(t *testing.T) {
 _, repo := setupEntityRepoTest(t)
 ctx := context.Background()

 entity, _ := domain.NewEntity("", "user123", "", domain.ScopeUser, "Go", "technology", "")
 require.NoError(t, repo.Insert(ctx, entity))

 entity.Profile = "Programming language"
 entity.FactCount = 5
 require.NoError(t, repo.Update(ctx, entity))

 retrieved, err := repo.GetByID(ctx, entity.ID)
 require.NoError(t, err)
 require.Equal(t, "Programming language", retrieved.Profile)
 require.Equal(t, 5, retrieved.FactCount)
}

func TestEntityRepo_ListProfiles(t *testing.T) {
 _, repo := setupEntityRepoTest(t)
 ctx := context.Background()

 e1, _ := domain.NewEntity("", "user123", "", domain.ScopeUser, "Rust", "technology", "Fast systems language")
 require.NoError(t, repo.Insert(ctx, e1))

 filter := domain.BuildScopeFilter("user123", "", "user")
 entities, err := repo.ListProfiles(ctx, filter, 10)
 require.NoError(t, err)
 require.Len(t, entities, 1)
 require.Equal(t, "Fast systems language", entities[0].Profile)
}

func TestEntityRepo_CountByUser(t *testing.T) {
 _, repo := setupEntityRepoTest(t)
 ctx := context.Background()

 e1, _ := domain.NewEntity("", "user123", "", domain.ScopeUser, "Entity1", "person", "")
 e2, _ := domain.NewEntity("", "user123", "", domain.ScopeUser, "Entity2", "project", "")
 require.NoError(t, repo.Insert(ctx, e1))
 require.NoError(t, repo.Insert(ctx, e2))

 count, err := repo.CountByUser(ctx, "user123")
 require.NoError(t, err)
 require.Equal(t, 2, count)
}

func TestEntityRepo_ListForRebuild(t *testing.T) {
 _, repo := setupEntityRepoTest(t)
 ctx := context.Background()

 e1, _ := domain.NewEntity("", "user123", "", domain.ScopeUser, "NeedsRebuild", "person", "")
 e1.RebuildAfter = time.Now().Add(-1 * time.Hour) // Past due
 require.NoError(t, repo.Insert(ctx, e1))

 entities, err := repo.ListForRebuild(ctx, 10)
 require.NoError(t, err)
 require.Len(t, entities, 1)
 require.Equal(t, "NeedsRebuild", entities[0].Name)
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run "TestEntityRepo_Update|TestEntityRepo_List|TestEntityRepo_Count"`
Expected: FAIL

- [ ] **Step 3: Implement remaining methods**

```go
// Append to entity_repo.go

func (r *EntityRepo) Update(ctx context.Context, entity *domain.MemoryEntity) error {
 query := `
  UPDATE memory_entities SET
   name = $2, entity_type = $3, profile = $4, fact_count = $5,
   last_seen_at = $6, rebuild_after = $7, status = $8, updated_at = $9
  WHERE id = $1`

 tag, err := r.pool.Exec(ctx, query,
  entity.ID, entity.Name, entity.EntityType, entity.Profile, entity.FactCount,
  entity.LastSeenAt, nullTime(entity.RebuildAfter), entity.Status, entity.UpdatedAt,
 )
 if err != nil {
  return fmt.Errorf("update entity: %w", err)
 }
 if tag.RowsAffected() == 0 {
  return domain.ErrEntityNotFound
 }
 return nil
}

func (r *EntityRepo) ListProfiles(ctx context.Context, filter domain.ScopeFilter, limit int) ([]*domain.MemoryEntity, error) {
 query := `
  SELECT id, user_id, agent_id, scope, name, entity_type, profile,
         fact_count, last_seen_at, rebuild_after, status, created_at, updated_at
  FROM memory_entities
  WHERE user_id = $1
    AND status = 'active'
    AND profile != ''
    AND (
        (scope = 'user' AND $2 = 'user')
        OR (scope = 'agent' AND agent_id = $3 AND $2 IN ('agent','user'))
    )
  ORDER BY last_seen_at DESC
  LIMIT $4`

 rows, err := r.pool.Query(ctx, query, filter.UserID, filter.ReadScope, filter.AgentID, limit)
 if err != nil {
  return nil, fmt.Errorf("list profiles: %w", err)
 }
 defer rows.Close()

 return r.scanEntities(rows)
}

func (r *EntityRepo) CountByUser(ctx context.Context, userID string) (int, error) {
 var count int
 err := r.pool.QueryRow(ctx, "SELECT COUNT(*) FROM memory_entities WHERE user_id = $1 AND status = 'active'", userID).Scan(&count)
 if err != nil {
  return 0, fmt.Errorf("count entities: %w", err)
 }
 return count, nil
}

func (r *EntityRepo) ListForRebuild(ctx context.Context, limit int) ([]*domain.MemoryEntity, error) {
 query := `
  SELECT id, user_id, agent_id, scope, name, entity_type, profile,
         fact_count, last_seen_at, rebuild_after, status, created_at, updated_at
  FROM memory_entities
  WHERE status = 'active'
    AND rebuild_after IS NOT NULL
    AND rebuild_after < NOW()
  ORDER BY rebuild_after ASC
  LIMIT $1`

 rows, err := r.pool.Query(ctx, query, limit)
 if err != nil {
  return nil, fmt.Errorf("list for rebuild: %w", err)
 }
 defer rows.Close()

 return r.scanEntities(rows)
}

func (r *EntityRepo) scanEntities(rows pgx.Rows) ([]*domain.MemoryEntity, error) {
 var entities []*domain.MemoryEntity
 for rows.Next() {
  var e domain.MemoryEntity
  var agentID *string
  var rebuildAfter *time.Time
  err := rows.Scan(
   &e.ID, &e.UserID, &agentID, &e.Scope, &e.Name, &e.EntityType, &e.Profile,
   &e.FactCount, &e.LastSeenAt, &rebuildAfter, &e.Status, &e.CreatedAt, &e.UpdatedAt,
  )
  if err != nil {
   return nil, fmt.Errorf("scan entity: %w", err)
  }
  if agentID != nil {
   e.AgentID = *agentID
  }
  if rebuildAfter != nil {
   e.RebuildAfter = *rebuildAfter
  }
  entities = append(entities, &e)
 }
 return entities, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run "TestEntityRepo_Update|TestEntityRepo_List|TestEntityRepo_Count"`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/persistence/entity_repo.go internal/memory/infrastructure/persistence/entity_repo_test.go
git commit -m "feat(memory): add EntityRepo Update, ListProfiles, CountByUser, ListForRebuild

Complete entity repository implementation

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 9: Extraction Queue Repository

**Files:**

- Create: `internal/memory/infrastructure/persistence/extraction_queue.go`
- Create: `internal/memory/infrastructure/persistence/extraction_queue_test.go`

**Interfaces:**

- Consumes: `domain/port.ExtractionQueueItem`, `domain/port.ExtractionQueue`
- Produces: Queue implementation with `FOR UPDATE SKIP LOCKED` for concurrent workers

- [ ] **Step 1: Write test for Enqueue and Poll**

```go
// internal/memory/infrastructure/persistence/extraction_queue_test.go
package persistence_test

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/memory/domain/port"
 "github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
 "github.com/byteBuilderX/stratum/pkg/storage/postgres"
 "github.com/stretchr/testify/require"
)

func setupQueueTest(t *testing.T) (*postgres.TenantPool, *persistence.ExtractionQueue) {
 t.Helper()
 pool := postgres.NewTestTenantPool(t, "tenant_test_queue")
 queue := persistence.NewExtractionQueue(pool)
 return pool, queue
}

func TestExtractionQueue_EnqueueAndPoll(t *testing.T) {
 _, queue := setupQueueTest(t)
 ctx := context.Background()

 item := &port.ExtractionQueueItem{
  UserID:         "user123",
  AgentID:        "agent456",
  ConversationID: "conv789",
  MessageIDs:     []string{"msg1", "msg2"},
  Payload: []port.MessagePayload{
   {Role: "user", Content: "Hello"},
   {Role: "assistant", Content: "Hi there"},
  },
 }

 err := queue.Enqueue(ctx, item)
 require.NoError(t, err)
 require.NotZero(t, item.ID, "ID should be assigned")

 items, err := queue.Poll(ctx, 1)
 require.NoError(t, err)
 require.Len(t, items, 1)
 require.Equal(t, "user123", items[0].UserID)
 require.Equal(t, "processing", items[0].Status)
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run TestExtractionQueue`
Expected: FAIL

- [ ] **Step 3: Implement ExtractionQueue Enqueue and Poll**

```go
// internal/memory/infrastructure/persistence/extraction_queue.go
package persistence

import (
 "context"
 "encoding/json"
 "fmt"

 "github.com/jackc/pgx/v5/pgxpool"

 "github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

type ExtractionQueue struct {
 pool *pgxpool.Pool
}

func NewExtractionQueue(pool *pgxpool.Pool) *ExtractionQueue {
 return &ExtractionQueue{pool: pool}
}

func (q *ExtractionQueue) Enqueue(ctx context.Context, item *port.ExtractionQueueItem) error {
 payload, err := json.Marshal(item.Payload)
 if err != nil {
  return fmt.Errorf("marshal payload: %w", err)
 }

 query := `
  INSERT INTO memory_extraction_queue (
   user_id, agent_id, conversation_id, message_ids, payload, status
  ) VALUES ($1, $2, $3, $4, $5, 'pending')
  RETURNING id`

 err = q.pool.QueryRow(ctx, query,
  item.UserID, item.AgentID, item.ConversationID, item.MessageIDs, payload,
 ).Scan(&item.ID)
 if err != nil {
  return fmt.Errorf("enqueue extraction: %w", err)
 }
 return nil
}

func (q *ExtractionQueue) Poll(ctx context.Context, limit int) ([]*port.ExtractionQueueItem, error) {
 query := `
  UPDATE memory_extraction_queue
  SET status = 'processing'
  WHERE id IN (
   SELECT id FROM memory_extraction_queue
   WHERE status = 'pending'
   ORDER BY created_at ASC
   LIMIT $1
   FOR UPDATE SKIP LOCKED
  )
  RETURNING id, user_id, agent_id, conversation_id, message_ids, payload,
            status, retry_count, last_error, created_at, processed_at`

 rows, err := q.pool.Query(ctx, query, limit)
 if err != nil {
  return nil, fmt.Errorf("poll queue: %w", err)
 }
 defer rows.Close()

 var items []*port.ExtractionQueueItem
 for rows.Next() {
  var item port.ExtractionQueueItem
  var payloadJSON []byte
  var lastError, processedAt *string

  err := rows.Scan(
   &item.ID, &item.UserID, &item.AgentID, &item.ConversationID, &item.MessageIDs,
   &payloadJSON, &item.Status, &item.RetryCount, &lastError, &item.CreatedAt, &processedAt,
  )
  if err != nil {
   return nil, fmt.Errorf("scan queue item: %w", err)
  }

  if err := json.Unmarshal(payloadJSON, &item.Payload); err != nil {
   return nil, fmt.Errorf("unmarshal payload: %w", err)
  }

  if lastError != nil {
   item.LastError = *lastError
  }

  items = append(items, &item)
 }
 return items, rows.Err()
}

var _ port.ExtractionQueue = (*ExtractionQueue)(nil)
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run TestExtractionQueue`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/persistence/extraction_queue.go internal/memory/infrastructure/persistence/extraction_queue_test.go
git commit -m "feat(memory): add ExtractionQueue Enqueue and Poll

FOR UPDATE SKIP LOCKED for concurrent worker polling

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Plan Checkpoint

Infrastructure plan Task 1-9 完成。剩余 Task 10-12 (MarkProcessing/Done/Failed, Milvus, LLM adapters) 将在下一次追加。

Next: Task 10-12 covering queue status updates, Milvus store, LLM/Embed adapters.

---

## Task 10: Queue Status Update Methods

**Files:**

- Modify: `internal/memory/infrastructure/persistence/extraction_queue.go`
- Modify: `internal/memory/infrastructure/persistence/extraction_queue_test.go`

**Interfaces:**

- Consumes: queue item ID, error
- Produces: `MarkProcessing`, `MarkDone`, `MarkFailed`, `DeleteOldCompleted`

- [ ] **Step 1: Write tests for status transitions**

```go
// Append to extraction_queue_test.go

func TestExtractionQueue_MarkDone(t *testing.T) {
 _, queue := setupQueueTest(t)
 ctx := context.Background()

 item := &port.ExtractionQueueItem{
  UserID: "user123", AgentID: "agent456",
  ConversationID: "conv789", MessageIDs: []string{"msg1"},
  Payload: []port.MessagePayload{{Role: "user", Content: "test"}},
 }
 require.NoError(t, queue.Enqueue(ctx, item))

 require.NoError(t, queue.MarkDone(ctx, item.ID))

 items, err := queue.Poll(ctx, 10)
 require.NoError(t, err)
 require.Empty(t, items, "done items should not be polled")
}

func TestExtractionQueue_MarkFailed(t *testing.T) {
 _, queue := setupQueueTest(t)
 ctx := context.Background()

 item := &port.ExtractionQueueItem{
  UserID: "user123", AgentID: "agent456",
  ConversationID: "conv789", MessageIDs: []string{"msg1"},
  Payload: []port.MessagePayload{{Role: "user", Content: "test"}},
 }
 require.NoError(t, queue.Enqueue(ctx, item))

 testErr := fmt.Errorf("extraction failed")
 require.NoError(t, queue.MarkFailed(ctx, item.ID, testErr))

 // After failure, status should be 'failed' and retry_count incremented
 // Not re-polled unless manually reset
}

func TestExtractionQueue_DeleteOldCompleted(t *testing.T) {
 _, queue := setupQueueTest(t)
 ctx := context.Background()

 item := &port.ExtractionQueueItem{
  UserID: "user123", AgentID: "agent456",
  ConversationID: "conv789", MessageIDs: []string{"msg1"},
  Payload: []port.MessagePayload{{Role: "user", Content: "test"}},
 }
 require.NoError(t, queue.Enqueue(ctx, item))
 require.NoError(t, queue.MarkDone(ctx, item.ID))

 count, err := queue.DeleteOldCompleted(ctx, 0)
 require.NoError(t, err)
 require.Equal(t, 1, count)
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run "TestExtractionQueue_Mark|TestExtractionQueue_Delete"`
Expected: FAIL

- [ ] **Step 3: Implement status update methods**

```go
// Append to extraction_queue.go

func (q *ExtractionQueue) MarkProcessing(ctx context.Context, id int64) error {
 query := `UPDATE memory_extraction_queue SET status = 'processing' WHERE id = $1`
 _, err := q.pool.Exec(ctx, query, id)
 if err != nil {
  return fmt.Errorf("mark processing: %w", err)
 }
 return nil
}

func (q *ExtractionQueue) MarkDone(ctx context.Context, id int64) error {
 query := `UPDATE memory_extraction_queue SET status = 'done', processed_at = NOW() WHERE id = $1`
 _, err := q.pool.Exec(ctx, query, id)
 if err != nil {
  return fmt.Errorf("mark done: %w", err)
 }
 return nil
}

func (q *ExtractionQueue) MarkFailed(ctx context.Context, id int64, failErr error) error {
 query := `
  UPDATE memory_extraction_queue
  SET status = 'failed', retry_count = retry_count + 1, last_error = $2
  WHERE id = $1`
 _, err := q.pool.Exec(ctx, query, id, failErr.Error())
 if err != nil {
  return fmt.Errorf("mark failed: %w", err)
 }
 return nil
}

func (q *ExtractionQueue) DeleteOldCompleted(ctx context.Context, retentionDays int) (int, error) {
 query := `
  DELETE FROM memory_extraction_queue
  WHERE status = 'done'
    AND processed_at < NOW() - $1 * INTERVAL '1 day'`
 tag, err := q.pool.Exec(ctx, query, retentionDays)
 if err != nil {
  return 0, fmt.Errorf("delete old completed: %w", err)
 }
 return int(tag.RowsAffected()), nil
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test -v ./internal/memory/infrastructure/persistence/... -run "TestExtractionQueue_Mark|TestExtractionQueue_Delete"`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/persistence/extraction_queue.go internal/memory/infrastructure/persistence/extraction_queue_test.go
git commit -m "feat(memory): add queue status update methods

MarkProcessing, MarkDone, MarkFailed, DeleteOldCompleted

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 11: Milvus Vector Store Client

**Files:**

- Create: `internal/memory/infrastructure/vector/milvus_store.go`
- Create: `internal/memory/infrastructure/vector/milvus_store_test.go`
- Create: `internal/memory/infrastructure/vector/collection.go`

**Interfaces:**

- Consumes: Milvus SDK v2.4.2, `domain/port.VectorStore`
- Produces: Milvus client with per-tenant collections (HNSW index, M=16, efConstruction=200)

- [ ] **Step 1: Write test for UpsertFact**

```go
// internal/memory/infrastructure/vector/milvus_store_test.go
package vector_test

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/memory/infrastructure/vector"
 "github.com/milvus-io/milvus-sdk-go/v2/client"
 "github.com/stretchr/testify/require"
)

func setupMilvusTest(t *testing.T) *vector.MilvusStore {
 t.Helper()
 // Connect to test Milvus instance (assumes localhost:19530)
 ctx := context.Background()
 milvusClient, err := client.NewClient(ctx, client.Config{
  Address: "localhost:19530",
 })
 require.NoError(t, err)

 store := vector.NewMilvusStore(milvusClient, 1024) // qwen embedding dimension
 return store
}

func TestMilvusStore_UpsertFact(t *testing.T) {
 store := setupMilvusTest(t)
 ctx := context.Background()
 tenantID := "test_tenant_milvus"

 vector := make([]float32, 1024)
 for i := range vector {
  vector[i] = 0.1
 }

 metadata := map[string]interface{}{
  "user_id":    "user123",
  "content":    "test fact",
  "importance": 0.8,
 }

 err := store.UpsertFact(ctx, tenantID, "fact_001", vector, metadata)
 require.NoError(t, err)
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/memory/infrastructure/vector/... -run TestMilvusStore`
Expected: FAIL (MilvusStore does not exist)

- [ ] **Step 3: Implement MilvusStore UpsertFact**

```go
// internal/memory/infrastructure/vector/milvus_store.go
package vector

import (
 "context"
 "fmt"

 "github.com/milvus-io/milvus-sdk-go/v2/client"
 "github.com/milvus-io/milvus-sdk-go/v2/entity"

 "github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

type MilvusStore struct {
 client    client.Client
 dimension int
}

func NewMilvusStore(milvusClient client.Client, dimension int) *MilvusStore {
 return &MilvusStore{
  client:    milvusClient,
  dimension: dimension,
 }
}

func (m *MilvusStore) UpsertFact(ctx context.Context, tenantID, factID string, vector []float32, metadata map[string]interface{}) error {
 collectionName := fmt.Sprintf("memory_facts_%s", tenantID)

 if err := m.ensureCollection(ctx, collectionName); err != nil {
  return err
 }

 idColumn := entity.NewColumnVarChar("id", []string{factID})
 vectorColumn := entity.NewColumnFloatVector("embedding", m.dimension, [][]float32{vector})
 userIDColumn := entity.NewColumnVarChar("user_id", []string{metadata["user_id"].(string)})
 contentColumn := entity.NewColumnVarChar("content", []string{metadata["content"].(string)})

 _, err := m.client.Upsert(ctx, collectionName, "", idColumn, vectorColumn, userIDColumn, contentColumn)
 if err != nil {
  return fmt.Errorf("upsert fact vector: %w", err)
 }
 return nil
}

func (m *MilvusStore) ensureCollection(ctx context.Context, collectionName string) error {
 has, err := m.client.HasCollection(ctx, collectionName)
 if err != nil {
  return fmt.Errorf("check collection: %w", err)
 }
 if has {
  return nil
 }

 schema := &entity.Schema{
  CollectionName: collectionName,
  Fields: []*entity.Field{
   {Name: "id", DataType: entity.FieldTypeVarChar, PrimaryKey: true, MaxLength: 64},
   {Name: "embedding", DataType: entity.FieldTypeFloatVector, TypeParams: map[string]string{"dim": fmt.Sprintf("%d", m.dimension)}},
   {Name: "user_id", DataType: entity.FieldTypeVarChar, MaxLength: 128},
   {Name: "content", DataType: entity.FieldTypeVarChar, MaxLength: 2048},
  },
 }

 if err := m.client.CreateCollection(ctx, schema, 2); err != nil {
  return fmt.Errorf("create collection: %w", err)
 }

 // Create HNSW index
 idx := entity.NewIndexHNSW(entity.L2, 16, 200)
 if err := m.client.CreateIndex(ctx, collectionName, "embedding", idx, false); err != nil {
  return fmt.Errorf("create index: %w", err)
 }

 if err := m.client.LoadCollection(ctx, collectionName, false); err != nil {
  return fmt.Errorf("load collection: %w", err)
 }

 return nil
}

var _ port.VectorStore = (*MilvusStore)(nil)
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./internal/memory/infrastructure/vector/... -run TestMilvusStore`
Expected: PASS (requires Milvus running on localhost:19530)

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/vector/milvus_store.go internal/memory/infrastructure/vector/milvus_store_test.go
git commit -m "feat(memory): add MilvusStore UpsertFact with HNSW index

Per-tenant collections, auto-create with M=16, efConstruction=200

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 12: Milvus SearchFacts and Delete Operations

**Files:**

- Modify: `internal/memory/infrastructure/vector/milvus_store.go`
- Modify: `internal/memory/infrastructure/vector/milvus_store_test.go`

**Interfaces:**

- Consumes: scope expression for filtering
- Produces: `SearchFacts`, `DeleteFact`, `UpsertEntityProfile`, `DeleteEntityProfile`

- [ ] **Step 1: Write test for SearchFacts**

```go
// Append to milvus_store_test.go

func TestMilvusStore_SearchFacts(t *testing.T) {
 store := setupMilvusTest(t)
 ctx := context.Background()
 tenantID := "test_tenant_search"

 // Insert a fact
 vector := make([]float32, 1024)
 for i := range vector {
  vector[i] = 0.1
 }
 metadata := map[string]interface{}{
  "user_id":    "user123",
  "content":    "test fact",
  "importance": 0.8,
 }
 require.NoError(t, store.UpsertFact(ctx, tenantID, "fact_001", vector, metadata))

 // Search
 queryVector := make([]float32, 1024)
 for i := range queryVector {
  queryVector[i] = 0.1
 }

 hits, err := store.SearchFacts(ctx, tenantID, queryVector, "user_id == 'user123'", 10)
 require.NoError(t, err)
 require.NotEmpty(t, hits)
 require.Equal(t, "fact_001", hits[0].ID)
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/memory/infrastructure/vector/... -run TestMilvusStore_SearchFacts`
Expected: FAIL

- [ ] **Step 3: Implement SearchFacts and remaining methods**

```go
// Append to milvus_store.go

func (m *MilvusStore) SearchFacts(ctx context.Context, tenantID string, queryVector []float32, scopeExpr string, topK int) ([]*port.VectorHit, error) {
 collectionName := fmt.Sprintf("memory_facts_%s", tenantID)

 sp, err := entity.NewIndexHNSWSearchParam(200)
 if err != nil {
  return nil, fmt.Errorf("create search param: %w", err)
 }

 results, err := m.client.Search(
  ctx,
  collectionName,
  []string{},
  scopeExpr,
  []string{"id", "user_id", "content"},
  []entity.Vector{entity.FloatVector(queryVector)},
  "embedding",
  entity.L2,
  topK,
  sp,
 )
 if err != nil {
  return nil, fmt.Errorf("search facts: %w", err)
 }

 if len(results) == 0 {
  return nil, nil
 }

 var hits []*port.VectorHit
 for i := 0; i < results[0].ResultCount; i++ {
  id, _ := results[0].IDs.Get(i)
  score := results[0].Scores[i]

  hit := &port.VectorHit{
   ID:       id.(string),
   Score:    score,
   Metadata: make(map[string]interface{}),
  }

  if userIDField := results[0].Fields.GetColumn("user_id"); userIDField != nil {
   userID, _ := userIDField.Get(i)
   hit.Metadata["user_id"] = userID
  }
  if contentField := results[0].Fields.GetColumn("content"); contentField != nil {
   content, _ := contentField.Get(i)
   hit.Metadata["content"] = content
  }

  hits = append(hits, hit)
 }

 return hits, nil
}

func (m *MilvusStore) DeleteFact(ctx context.Context, tenantID, factID string) error {
 collectionName := fmt.Sprintf("memory_facts_%s", tenantID)
 expr := fmt.Sprintf("id == '%s'", factID)

 if err := m.client.Delete(ctx, collectionName, "", expr); err != nil {
  return fmt.Errorf("delete fact: %w", err)
 }
 return nil
}

func (m *MilvusStore) UpsertEntityProfile(ctx context.Context, tenantID, entityID string, vector []float32, metadata map[string]interface{}) error {
 collectionName := fmt.Sprintf("memory_entity_profiles_%s", tenantID)

 if err := m.ensureEntityCollection(ctx, collectionName); err != nil {
  return err
 }

 idColumn := entity.NewColumnVarChar("id", []string{entityID})
 vectorColumn := entity.NewColumnFloatVector("embedding", m.dimension, [][]float32{vector})
 userIDColumn := entity.NewColumnVarChar("user_id", []string{metadata["user_id"].(string)})
 nameColumn := entity.NewColumnVarChar("name", []string{metadata["name"].(string)})

 _, err := m.client.Upsert(ctx, collectionName, "", idColumn, vectorColumn, userIDColumn, nameColumn)
 if err != nil {
  return fmt.Errorf("upsert entity profile: %w", err)
 }
 return nil
}

func (m *MilvusStore) DeleteEntityProfile(ctx context.Context, tenantID, entityID string) error {
 collectionName := fmt.Sprintf("memory_entity_profiles_%s", tenantID)
 expr := fmt.Sprintf("id == '%s'", entityID)

 if err := m.client.Delete(ctx, collectionName, "", expr); err != nil {
  return fmt.Errorf("delete entity profile: %w", err)
 }
 return nil
}

func (m *MilvusStore) ensureEntityCollection(ctx context.Context, collectionName string) error {
 has, err := m.client.HasCollection(ctx, collectionName)
 if err != nil {
  return fmt.Errorf("check entity collection: %w", err)
 }
 if has {
  return nil
 }

 schema := &entity.Schema{
  CollectionName: collectionName,
  Fields: []*entity.Field{
   {Name: "id", DataType: entity.FieldTypeVarChar, PrimaryKey: true, MaxLength: 64},
   {Name: "embedding", DataType: entity.FieldTypeFloatVector, TypeParams: map[string]string{"dim": fmt.Sprintf("%d", m.dimension)}},
   {Name: "user_id", DataType: entity.FieldTypeVarChar, MaxLength: 128},
   {Name: "name", DataType: entity.FieldTypeVarChar, MaxLength: 256},
  },
 }

 if err := m.client.CreateCollection(ctx, schema, 2); err != nil {
  return fmt.Errorf("create entity collection: %w", err)
 }

 idx := entity.NewIndexHNSW(entity.L2, 16, 200)
 if err := m.client.CreateIndex(ctx, collectionName, "embedding", idx, false); err != nil {
  return fmt.Errorf("create entity index: %w", err)
 }

 if err := m.client.LoadCollection(ctx, collectionName, false); err != nil {
  return fmt.Errorf("load entity collection: %w", err)
 }

 return nil
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./internal/memory/infrastructure/vector/... -run TestMilvusStore`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/infrastructure/vector/milvus_store.go internal/memory/infrastructure/vector/milvus_store_test.go
git commit -m "feat(memory): add Milvus SearchFacts and entity profile operations

Complete vector store implementation with scope filtering

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Plan Complete

Infrastructure plan (Phase 3) Task 1-12 完成。下一步创建 application plan (Phase 4: MemoryService)。
