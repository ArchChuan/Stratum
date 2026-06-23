# Memory v2 Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Establish the domain model, database schema, and migration for the memory v2 fact-centric architecture.

**Architecture:** DDD-style domain layer (zero third-party deps), PostgreSQL schema with trigram + GIN indexes, greenfield tables (memory_facts, memory_entities, queues), drop old pipeline tables.

**Tech Stack:** Go 1.22 · PostgreSQL 15 + pg_trgm extension · golang-migrate · pgx v5

---

## Global Constraints

- Go 1.22+
- All SQL must use `SET LOCAL search_path = tenant_{id}, public`
- Domain layer: zero third-party deps (stdlib + `pkg/constants` only)
- Ports defined in `internal/memory/domain/port/`
- Error sentinel vars in `internal/memory/domain/errors.go`
- Test coverage: domain ≥95%, infrastructure ≥80%
- Every migration must be reversible (`.up.sql` + `.down.sql`)

---

## File Structure

```
pkg/constants/
  memory.go                          # All memory constants (buffers, recall, GC, workers, LLM timeouts)

internal/memory/domain/
  errors.go                          # Error sentinels (Err*)
  fact.go                            # MemoryFact aggregate + status machine
  entity.go                          # MemoryEntity aggregate
  scope.go                           # Scope enum (user/agent) + filter logic
  frecency.go                        # Frecency scoring algorithm
  port/
    fact_repo.go                     # FactRepo interface
    entity_repo.go                   # EntityRepo interface
    extraction_queue.go              # ExtractionQueue interface
    vector_store.go                  # VectorStore interface
    llm_extractor.go                 # LLMExtractor + EntityProfiler interfaces
    embed_client.go                  # EmbedClient interface

pkg/migration/sql/
  003_memory_v2.up.sql               # DROP old + CREATE new tables
  003_memory_v2.down.sql             # Revert
pkg/storage/postgres/
  tenant_schema.sql                  # MODIFY: agents table extensions (memory_enabled, write_scope, read_scope)
```

---

## Task 1: Constants Definition

**Files:**

- Create: `pkg/constants/memory.go`

**Interfaces:**

- Consumes: none
- Produces: All memory-related constants used across domain/application/infrastructure

- [ ] **Step 1: Write test for constants existence**

```go
// pkg/constants/memory_test.go
package constants_test

import (
 "testing"
 "time"

 "github.com/byteBuilderX/stratum/pkg/constants"
)

func TestMemoryConstants(t *testing.T) {
 // Buffer
 if constants.MemoryBufferFlushSize != 5 {
  t.Errorf("expected flush size 5, got %d", constants.MemoryBufferFlushSize)
 }
 if constants.MemoryBufferFlushInterval != 2*time.Minute {
  t.Errorf("expected flush interval 2min")
 }

 // Recall
 if constants.MemoryRecallTopK != 10 {
  t.Errorf("expected recall topK 10")
 }
 if constants.MemoryFrecencyLambda != 0.05 {
  t.Errorf("expected lambda 0.05")
 }

 // GC
 if constants.MemorySoftDeleteRetention != 30*24*time.Hour {
  t.Errorf("expected soft delete retention 30 days")
 }

 // Quota
 if constants.MemoryFactQuotaPerUser != 5000 {
  t.Errorf("expected fact quota 5000")
 }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./pkg/constants/... -run TestMemoryConstants`
Expected: FAIL (package does not exist or constants undefined)

- [ ] **Step 3: Implement constants**

```go
// pkg/constants/memory.go
package constants

import "time"

const (
 // 缓冲与 batch
 MemoryBufferFlushSize     = 5
 MemoryBufferFlushInterval = 2 * time.Minute

 // 召回
 MemoryRecallTopK           = 10
 MemoryRecallVectorMultiple = 2
 MemoryRRFK                 = 60
 MemoryFrecencyLambda       = 0.05
 MemoryProfileTopN          = 10

 // GC
 MemorySoftDeleteRetention  = 30 * 24 * time.Hour
 MemorySupersededRetention  = 90 * 24 * time.Hour
 MemoryArchivedRetention    = 90 * 24 * time.Hour
 MemoryArchiveImportanceMax = 0.3
 MemoryArchiveColdDays      = 60
 MemoryArchiveAccessMax     = 3

 // 配额
 MemoryFactQuotaPerUser      = 5000
 MemoryEntityQuotaPerUser    = 500
 MemoryQuotaCompressionRatio = 0.9

 // Worker tick
 MemoryProfileRebuildTick = 5 * time.Minute
 MemoryGCTick             = 1 * time.Hour
 MemoryQuotaEnforcerTick  = 6 * time.Hour
 MemoryQueueGCTick        = 24 * time.Hour

 // ProfileRebuild 触发
 MemoryProfileRebuildMinDays   = 7
 MemoryProfileRebuildFactDelta = 5

 // LLM 超时
 MemoryExtractLLMTimeout   = 30 * time.Second
 MemoryProfileLLMTimeout   = 30 * time.Second
 MemorySupersedeLLMTimeout = 20 * time.Second

 // AccessFlusher
 MemoryAccessFlushInterval = 30 * time.Second
 MemoryAccessFlushBatch    = 500

 // Entity 归一化
 MemoryEntitySimilarityMin = 0.8

 // Supersede 候选
 MemorySupersedeCandidateMin = 0.6
 MemorySupersedeCandidateMax = 3
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./pkg/constants/... -run TestMemoryConstants`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/constants/memory.go pkg/constants/memory_test.go
git commit -m "feat(memory): add v2 constants for buffer/recall/GC/quota/workers

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 2: Domain Error Sentinels

**Files:**

- Create: `internal/memory/domain/errors.go`
- Create: `internal/memory/domain/errors_test.go`

**Interfaces:**

- Consumes: none
- Produces: Error sentinel variables used across all memory layers

- [ ] **Step 1: Write test for error sentinel existence and behavior**

```go
// internal/memory/domain/errors_test.go
package domain_test

import (
 "errors"
 "testing"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestErrorSentinels(t *testing.T) {
 tests := []struct {
  name string
  err  error
  msg  string
 }{
  {"FactNotFound", domain.ErrFactNotFound, "memory: fact not found"},
  {"EntityNotFound", domain.ErrEntityNotFound, "memory: entity not found"},
  {"AgentMemoryDisabled", domain.ErrAgentMemoryDisabled, "memory: agent has memory disabled"},
  {"ScopeMismatch", domain.ErrScopeMismatch, "memory: scope mismatch"},
  {"UserIDMismatch", domain.ErrUserIDMismatch, "memory: user_id required"},
  {"FactQuotaExceeded", domain.ErrFactQuotaExceeded, "memory: fact quota exceeded"},
  {"FactAlreadyDeleted", domain.ErrFactAlreadyDeleted, "memory: fact already deleted"},
  {"InvalidStatus", domain.ErrInvalidStatus, "memory: invalid status transition"},
  {"EmptyContent", domain.ErrEmptyContent, "memory: empty content"},
  {"EmbeddingDimension", domain.ErrEmbeddingDimension, "memory: embedding dimension mismatch"},
 }

 for _, tt := range tests {
  t.Run(tt.name, func(t *testing.T) {
   if tt.err == nil {
    t.Fatal("error sentinel is nil")
   }
   if tt.err.Error() != tt.msg {
    t.Errorf("expected %q, got %q", tt.msg, tt.err.Error())
   }
   // Verify errors.Is works
   wrapped := errors.Join(errors.New("context"), tt.err)
   if !errors.Is(wrapped, tt.err) {
    t.Error("errors.Is check failed")
   }
  })
 }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/memory/domain/... -run TestErrorSentinels`
Expected: FAIL (package or errors undefined)

- [ ] **Step 3: Implement error sentinels**

```go
// internal/memory/domain/errors.go
package domain

import "errors"

var (
 // 资源类
 ErrFactNotFound   = errors.New("memory: fact not found")
 ErrEntityNotFound = errors.New("memory: entity not found")

 // 权限类
 ErrAgentMemoryDisabled = errors.New("memory: agent has memory disabled")
 ErrScopeMismatch       = errors.New("memory: scope mismatch")
 ErrUserIDMismatch      = errors.New("memory: user_id required")

 // 配额类
 ErrFactQuotaExceeded = errors.New("memory: fact quota exceeded")

 // 状态类
 ErrFactAlreadyDeleted = errors.New("memory: fact already deleted")
 ErrInvalidStatus      = errors.New("memory: invalid status transition")

 // 输入类
 ErrEmptyContent       = errors.New("memory: empty content")
 ErrEmbeddingDimension = errors.New("memory: embedding dimension mismatch")
)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/memory/domain/... -run TestErrorSentinels`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/domain/errors.go internal/memory/domain/errors_test.go
git commit -m "feat(memory): add domain error sentinels

10 error types covering resource/permission/quota/status/input failures

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 3: Scope Enum and Filter Logic

**Files:**

- Create: `internal/memory/domain/scope.go`
- Create: `internal/memory/domain/scope_test.go`

**Interfaces:**

- Consumes: none
- Produces: `Scope` type, `ValidateScope()`, `BuildScopeFilter()` functions

- [ ] **Step 1: Write test for scope validation and filter building**

```go
// internal/memory/domain/scope_test.go
package domain_test

import (
 "testing"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestValidateScope(t *testing.T) {
 tests := []struct {
  input string
  valid bool
 }{
  {"user", true},
  {"agent", true},
  {"off", false},
  {"global", false},
  {"", false},
 }
 for _, tt := range tests {
  t.Run(tt.input, func(t *testing.T) {
   err := domain.ValidateScope(tt.input)
   if tt.valid && err != nil {
    t.Errorf("expected valid, got error: %v", err)
   }
   if !tt.valid && err == nil {
    t.Errorf("expected invalid, got nil error")
   }
  })
 }
}

func TestBuildScopeFilter(t *testing.T) {
 userFilter := domain.BuildScopeFilter("user123", "agent456", "user")
 if userFilter.UserID != "user123" {
  t.Errorf("expected userID user123")
 }
 if !userFilter.IncludeUserScope {
  t.Error("expected user scope included")
 }
 if !userFilter.IncludeAgentScope {
  t.Error("expected agent scope included for read_scope=user")
 }

 agentFilter := domain.BuildScopeFilter("user123", "agent456", "agent")
 if agentFilter.IncludeUserScope {
  t.Error("expected user scope excluded for read_scope=agent")
 }
 if !agentFilter.IncludeAgentScope {
  t.Error("expected agent scope included")
 }
 if agentFilter.AgentID != "agent456" {
  t.Error("expected agentID set")
 }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/memory/domain/... -run TestValidateScope`
Expected: FAIL

- [ ] **Step 3: Implement scope types and logic**

```go
// internal/memory/domain/scope.go
package domain

import "fmt"

// Scope represents memory scope (user-level or agent-private)
type Scope string

const (
 ScopeUser  Scope = "user"
 ScopeAgent Scope = "agent"
)

// ValidateScope checks if the scope string is valid
func ValidateScope(s string) error {
 if s != string(ScopeUser) && s != string(ScopeAgent) {
  return fmt.Errorf("invalid scope %q: must be user or agent", s)
 }
 return nil
}

// ScopeFilter encapsulates scope filtering logic for queries
type ScopeFilter struct {
 UserID            string
 AgentID           string
 IncludeUserScope  bool
 IncludeAgentScope bool
}

// BuildScopeFilter constructs a filter based on read_scope configuration
// readScope: "user" includes both user-scoped and agent-scoped facts
// readScope: "agent" includes only agent-scoped facts
func BuildScopeFilter(userID, agentID, readScope string) ScopeFilter {
 filter := ScopeFilter{
  UserID:  userID,
  AgentID: agentID,
 }
 switch readScope {
 case "user":
  filter.IncludeUserScope = true
  filter.IncludeAgentScope = true
 case "agent":
  filter.IncludeUserScope = false
  filter.IncludeAgentScope = true
 }
 return filter
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/memory/domain/... -run TestValidateScope`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/domain/scope.go internal/memory/domain/scope_test.go
git commit -m "feat(memory): add scope enum and filter logic

Scope validation + BuildScopeFilter for user/agent read isolation

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 4: Frecency Scoring Algorithm

**Files:**

- Create: `internal/memory/domain/frecency.go`
- Create: `internal/memory/domain/frecency_test.go`

**Interfaces:**

- Consumes: `pkg/constants.MemoryFrecencyLambda`
- Produces: `CalculateFrecency(importance, daysSinceAccess, accessCount float64) float64`

- [ ] **Step 1: Write test for frecency calculation**

```go
// internal/memory/domain/frecency_test.go
package domain_test

import (
 "math"
 "testing"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestCalculateFrecency(t *testing.T) {
 tests := []struct {
  name            string
  importance      float64
  daysSinceAccess float64
  accessCount     int
  wantMin         float64
  wantMax         float64
 }{
  {"fresh high importance", 0.9, 0, 1, 0.9, 1.0},
  {"old high importance", 0.9, 60, 1, 0.04, 0.06},
  {"frequent low importance", 0.3, 7, 50, 0.3, 0.5},
  {"14 day half-life", 0.5, 14, 1, 0.24, 0.26},
 }
 for _, tt := range tests {
  t.Run(tt.name, func(t *testing.T) {
   score := domain.CalculateFrecency(tt.importance, tt.daysSinceAccess, tt.accessCount)
   if score < tt.wantMin || score > tt.wantMax {
    t.Errorf("score %.4f not in range [%.4f, %.4f]", score, tt.wantMin, tt.wantMax)
   }
  })
 }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/memory/domain/... -run TestCalculateFrecency`
Expected: FAIL

- [ ] **Step 3: Implement frecency algorithm**

```go
// internal/memory/domain/frecency.go
package domain

import (
 "math"

 "github.com/byteBuilderX/stratum/pkg/constants"
)

// CalculateFrecency computes a combined score from importance, decay, and access frequency.
// Formula: importance × exp(-λ × days) × log(1 + accessCount)
// λ = 0.05 gives ~14-day half-life
func CalculateFrecency(importance, daysSinceAccess float64, accessCount int) float64 {
 decay := math.Exp(-constants.MemoryFrecencyLambda * daysSinceAccess)
 frequency := math.Log(1 + float64(accessCount))
 return importance * decay * frequency
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/memory/domain/... -run TestCalculateFrecency`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/domain/frecency.go internal/memory/domain/frecency_test.go
git commit -m "feat(memory): add frecency scoring algorithm

importance × exp(-0.05×days) × log(1+access) for fact ranking

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 5: Fact Aggregate Root

**Files:**

- Create: `internal/memory/domain/fact.go`
- Create: `internal/memory/domain/fact_test.go`

**Interfaces:**

- Consumes: `Scope`, `ValidateScope()`, error sentinels
- Produces: `MemoryFact` struct, `NewFact()`, `CanTransitionTo()`, `MarkDeleted()`, `MarkSuperseded()`

- [ ] **Step 1: Write test for fact creation and state transitions**

```go
// internal/memory/domain/fact_test.go
package domain_test

import (
 "testing"
 "time"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestNewFact(t *testing.T) {
 fact, err := domain.NewFact("user123", "", "user", "User prefers Vim", 0.85, []string{"vim", "preference"})
 if err != nil {
  t.Fatalf("NewFact failed: %v", err)
 }
 if fact.UserID != "user123" {
  t.Error("userID mismatch")
 }
 if fact.Scope != domain.ScopeUser {
  t.Error("scope mismatch")
 }
 if fact.Status != "active" {
  t.Error("expected active status")
 }
 if fact.Importance != 0.85 {
  t.Error("importance mismatch")
 }
}

func TestNewFact_Validation(t *testing.T) {
 _, err := domain.NewFact("", "", "user", "content", 0.5, nil)
 if err != domain.ErrUserIDMismatch {
  t.Error("expected ErrUserIDMismatch for empty userID")
 }

 _, err = domain.NewFact("user123", "", "invalid_scope", "content", 0.5, nil)
 if err == nil {
  t.Error("expected error for invalid scope")
 }

 _, err = domain.NewFact("user123", "", "user", "", 0.5, nil)
 if err != domain.ErrEmptyContent {
  t.Error("expected ErrEmptyContent")
 }
}

func TestFactStatusTransitions(t *testing.T) {
 fact, _ := domain.NewFact("user123", "", "user", "content", 0.5, nil)

 if !fact.CanTransitionTo("deleted") {
  t.Error("active → deleted should be allowed")
 }
 if !fact.CanTransitionTo("superseded") {
  t.Error("active → superseded should be allowed")
 }
 if !fact.CanTransitionTo("archived") {
  t.Error("active → archived should be allowed")
 }

 fact.MarkDeleted()
 if fact.Status != "deleted" {
  t.Error("status should be deleted")
 }
 if fact.DeletedAt.IsZero() {
  t.Error("deletedAt should be set")
 }
 if fact.CanTransitionTo("active") {
  t.Error("deleted → active should be forbidden")
 }
}

func TestFactMarkSuperseded(t *testing.T) {
 fact, _ := domain.NewFact("user123", "", "user", "old fact", 0.5, nil)
 newFactID := "new-uuid"

 err := fact.MarkSuperseded(newFactID)
 if err != nil {
  t.Fatalf("MarkSuperseded failed: %v", err)
 }
 if fact.Status != "superseded" {
  t.Error("status should be superseded")
 }
 if fact.SupersededBy != newFactID {
  t.Error("supersededBy mismatch")
 }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/memory/domain/... -run TestNewFact`
Expected: FAIL

- [ ] **Step 3: Implement MemoryFact aggregate**

```go
// internal/memory/domain/fact.go
package domain

import (
 "time"

 "github.com/google/uuid"
)

// MemoryFact represents a single extracted fact about the user.
type MemoryFact struct {
 ID             string
 UserID         string
 AgentID        string // NULL for user-scope
 Scope          Scope
 Content        string
 Importance     float64
 Keywords       []string
 EntityRefs     []string // UUIDs of related entities
 SourceMsgIDs   []string // Traceability to chat messages
 Status         string   // active/deleted/superseded/archived
 SupersededBy   string
 AccessCount    int
 LastAccessedAt time.Time
 CreatedAt      time.Time
 UpdatedAt      time.Time
 DeletedAt      time.Time
 ArchivedAt     time.Time
}

// NewFact creates a new active fact with validation.
func NewFact(userID, agentID, scope, content string, importance float64, keywords []string) (*MemoryFact, error) {
 if userID == "" {
  return nil, ErrUserIDMismatch
 }
 if err := ValidateScope(scope); err != nil {
  return nil, err
 }
 if content == "" {
  return nil, ErrEmptyContent
 }

 now := time.Now()
 return &MemoryFact{
  ID:             uuid.NewString(),
  UserID:         userID,
  AgentID:        agentID,
  Scope:          Scope(scope),
  Content:        content,
  Importance:     importance,
  Keywords:       keywords,
  EntityRefs:     []string{},
  SourceMsgIDs:   []string{},
  Status:         "active",
  AccessCount:    0,
  LastAccessedAt: now,
  CreatedAt:      now,
  UpdatedAt:      now,
 }, nil
}

// CanTransitionTo checks if the status transition is allowed.
func (f *MemoryFact) CanTransitionTo(newStatus string) bool {
 allowed := map[string][]string{
  "active":     {"deleted", "superseded", "archived"},
  "archived":   {"active"},
  "deleted":    {},
  "superseded": {},
 }
 for _, s := range allowed[f.Status] {
  if s == newStatus {
   return true
  }
 }
 return false
}

// MarkDeleted soft-deletes the fact.
func (f *MemoryFact) MarkDeleted() error {
 if !f.CanTransitionTo("deleted") {
  return ErrInvalidStatus
 }
 f.Status = "deleted"
 f.DeletedAt = time.Now()
 f.UpdatedAt = time.Now()
 return nil
}

// MarkSuperseded marks this fact as superseded by a newer fact.
func (f *MemoryFact) MarkSuperseded(newFactID string) error {
 if !f.CanTransitionTo("superseded") {
  return ErrInvalidStatus
 }
 f.Status = "superseded"
 f.SupersededBy = newFactID
 f.UpdatedAt = time.Now()
 return nil
}

// MarkArchived transitions to archived status.
func (f *MemoryFact) MarkArchived() error {
 if !f.CanTransitionTo("archived") {
  return ErrInvalidStatus
 }
 f.Status = "archived"
 f.ArchivedAt = time.Now()
 f.UpdatedAt = time.Now()
 return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/memory/domain/... -run TestNewFact`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/domain/fact.go internal/memory/domain/fact_test.go
git commit -m "feat(memory): add MemoryFact aggregate root with state machine

NewFact + status transitions (active→deleted/superseded/archived)

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 6: Entity Aggregate Root

**Files:**

- Create: `internal/memory/domain/entity.go`
- Create: `internal/memory/domain/entity_test.go`

**Interfaces:**

- Consumes: `Scope`, `ValidateScope()`, error sentinels
- Produces: `MemoryEntity` struct, `NewEntity()`, `IncrementFactCount()`, `ShouldRebuildProfile()`

- [ ] **Step 1: Write test for entity creation and profile rebuild logic**

```go
// internal/memory/domain/entity_test.go
package domain_test

import (
 "testing"
 "time"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
 "github.com/byteBuilderX/stratum/pkg/constants"
)

func TestNewEntity(t *testing.T) {
 entity, err := domain.NewEntity("user123", "", "user", "Alice", "person")
 if err != nil {
  t.Fatalf("NewEntity failed: %v", err)
 }
 if entity.UserID != "user123" {
  t.Error("userID mismatch")
 }
 if entity.Name != "Alice" {
  t.Error("name mismatch")
 }
 if entity.EntityType != "person" {
  t.Error("type mismatch")
 }
 if entity.Status != "active" {
  t.Error("expected active status")
 }
 if entity.FactCount != 0 {
  t.Error("fact count should be 0")
 }
}

func TestEntityIncrementFactCount(t *testing.T) {
 entity, _ := domain.NewEntity("user123", "", "user", "Project X", "project")
 entity.IncrementFactCount()
 if entity.FactCount != 1 {
  t.Error("fact count should be 1")
 }
}

func TestEntityShouldRebuildProfile(t *testing.T) {
 entity, _ := domain.NewEntity("user123", "", "user", "Alice", "person")
 entity.LastProfileRebuildAt = time.Now().Add(-8 * 24 * time.Hour)
 entity.FactCountSinceRebuild = 2

 if !entity.ShouldRebuildProfile() {
  t.Error("should rebuild: >7 days since last rebuild")
 }

 entity.LastProfileRebuildAt = time.Now().Add(-1 * time.Hour)
 entity.FactCountSinceRebuild = 6

 if !entity.ShouldRebuildProfile() {
  t.Error("should rebuild: fact count delta >=5")
 }

 entity.LastProfileRebuildAt = time.Now()
 entity.FactCountSinceRebuild = 2

 if entity.ShouldRebuildProfile() {
  t.Error("should not rebuild: recent + low delta")
 }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./internal/memory/domain/... -run TestNewEntity`
Expected: FAIL

- [ ] **Step 3: Implement MemoryEntity aggregate**

```go
// internal/memory/domain/entity.go
package domain

import (
 "time"

 "github.com/byteBuilderX/stratum/pkg/constants"
 "github.com/google/uuid"
)

// MemoryEntity represents a recognized entity with a rolling profile summary.
type MemoryEntity struct {
 ID                     string
 UserID                 string
 AgentID                string
 Scope                  Scope
 Name                   string
 EntityType             string // person/project/preference/tech/location
 Profile                string // LLM-generated rolling summary
 FactCount              int
 FactCountSinceRebuild  int
 LastSeenAt             time.Time
 LastProfileRebuildAt   time.Time
 Status                 string // active/deleted
 CreatedAt              time.Time
 UpdatedAt              time.Time
}

// NewEntity creates a new active entity with validation.
func NewEntity(userID, agentID, scope, name, entityType string) (*MemoryEntity, error) {
 if userID == "" {
  return nil, ErrUserIDMismatch
 }
 if err := ValidateScope(scope); err != nil {
  return nil, err
 }
 if name == "" {
  return nil, ErrEmptyContent
 }

 now := time.Now()
 return &MemoryEntity{
  ID:                    uuid.NewString(),
  UserID:                userID,
  AgentID:               agentID,
  Scope:                 Scope(scope),
  Name:                  name,
  EntityType:            entityType,
  Profile:               "",
  FactCount:             0,
  FactCountSinceRebuild: 0,
  LastSeenAt:            now,
  LastProfileRebuildAt:  time.Time{}, // zero means never rebuilt
  Status:                "active",
  CreatedAt:             now,
  UpdatedAt:             now,
 }, nil
}

// IncrementFactCount increments the total and since-rebuild counters.
func (e *MemoryEntity) IncrementFactCount() {
 e.FactCount++
 e.FactCountSinceRebuild++
 e.LastSeenAt = time.Now()
 e.UpdatedAt = time.Now()
}

// ShouldRebuildProfile checks if profile rebuild should be triggered.
// Triggers if: >7 days since last rebuild OR fact delta >=5
func (e *MemoryEntity) ShouldRebuildProfile() bool {
 if e.LastProfileRebuildAt.IsZero() {
  // Never rebuilt — trigger if we have any facts
  return e.FactCount > 0
 }

 daysSinceRebuild := time.Since(e.LastProfileRebuildAt).Hours() / 24
 if daysSinceRebuild >= float64(constants.MemoryProfileRebuildMinDays) {
  return true
 }
 if e.FactCountSinceRebuild >= constants.MemoryProfileRebuildFactDelta {
  return true
 }
 return false
}

// MarkDeleted soft-deletes the entity.
func (e *MemoryEntity) MarkDeleted() {
 e.Status = "deleted"
 e.UpdatedAt = time.Now()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -v ./internal/memory/domain/... -run TestNewEntity`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/memory/domain/entity.go internal/memory/domain/entity_test.go
git commit -m "feat(memory): add MemoryEntity aggregate with profile rebuild logic

NewEntity + ShouldRebuildProfile (7-day / 5-fact delta triggers)

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 7: Domain Ports (Repository Interfaces)

**Files:**

- Create: `internal/memory/domain/port/fact_repo.go`
- Create: `internal/memory/domain/port/entity_repo.go`
- Create: `internal/memory/domain/port/extraction_queue.go`

**Interfaces:**

- Consumes: `MemoryFact`, `MemoryEntity`, `ScopeFilter`
- Produces: Repository interfaces for infrastructure to implement

- [ ] **Step 1: Create fact repository port**

```go
// internal/memory/domain/port/fact_repo.go
package port

import (
 "context"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
)

// FactRepo defines persistence operations for memory facts.
type FactRepo interface {
 // Insert creates a new fact (generates ID if empty).
 Insert(ctx context.Context, fact *domain.MemoryFact) error

 // GetByID retrieves a single fact by ID.
 GetByID(ctx context.Context, id string) (*domain.MemoryFact, error)

 // Update modifies an existing fact.
 Update(ctx context.Context, fact *domain.MemoryFact) error

 // ListActive retrieves active facts for a user with scope filtering.
 ListActive(ctx context.Context, filter domain.ScopeFilter, limit int) ([]*domain.MemoryFact, error)

 // SearchByContent performs trigram full-text search on content field.
 SearchByContent(ctx context.Context, filter domain.ScopeFilter, query string, limit int) ([]*domain.MemoryFact, error)

 // FindSimilarContent finds facts with similar content (for supersede detection).
 // Returns facts with similarity > threshold, ordered by similarity desc.
 FindSimilarContent(ctx context.Context, userID, content string, threshold float64, limit int) ([]*domain.MemoryFact, error)

 // CountByUser returns total fact count for quota checking.
 CountByUser(ctx context.Context, userID string) (int, error)

 // DeleteExpired hard-deletes facts past their retention window.
 DeleteExpired(ctx context.Context) (int, error)

 // PromoteToArchived transitions low-importance cold facts to archived status.
 PromoteToArchived(ctx context.Context, importanceMax float64, coldDays int, accessMax int) (int, error)
}
```

- [ ] **Step 2: Create entity repository port**

```go
// internal/memory/domain/port/entity_repo.go
package port

import (
 "context"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
)

// EntityRepo defines persistence operations for memory entities.
type EntityRepo interface {
 // Insert creates a new entity.
 Insert(ctx context.Context, entity *domain.MemoryEntity) error

 // GetByID retrieves a single entity by ID.
 GetByID(ctx context.Context, id string) (*domain.MemoryEntity, error)

 // Update modifies an existing entity.
 Update(ctx context.Context, entity *domain.MemoryEntity) error

 // FindByNameAndType looks up entity by name + type with trigram similarity.
 // Returns the best match if similarity > threshold, nil otherwise.
 FindByNameAndType(ctx context.Context, userID, name, entityType string, threshold float64) (*domain.MemoryEntity, error)

 // ListProfiles retrieves active entities with non-empty profile for BuildContext injection.
 ListProfiles(ctx context.Context, filter domain.ScopeFilter, limit int) ([]*domain.MemoryEntity, error)

 // CountByUser returns total entity count for quota checking.
 CountByUser(ctx context.Context, userID string) (int, error)

 // ListForRebuild fetches entities that need profile rebuilding.
 ListForRebuild(ctx context.Context, limit int) ([]*domain.MemoryEntity, error)
}
```

- [ ] **Step 3: Create extraction queue port**

```go
// internal/memory/domain/port/extraction_queue.go
package port

import (
 "context"
 "time"
)

// ExtractionQueueItem represents a batch of messages to extract facts from.
type ExtractionQueueItem struct {
 ID             int64
 UserID         string
 AgentID        string
 ConversationID string
 MessageIDs     []string
 Payload        []MessagePayload // [{role, content}]
 Status         string           // pending/processing/done/failed
 RetryCount     int
 LastError      string
 CreatedAt      time.Time
 ProcessedAt    time.Time
}

// MessagePayload represents a single message in the extraction batch.
type MessagePayload struct {
 Role    string `json:"role"`
 Content string `json:"content"`
}

// ExtractionQueue defines operations for the extraction task queue.
type ExtractionQueue interface {
 // Enqueue adds a new batch to the queue.
 Enqueue(ctx context.Context, item *ExtractionQueueItem) error

 // Poll fetches pending items (FOR UPDATE SKIP LOCKED for concurrency).
 Poll(ctx context.Context, limit int) ([]*ExtractionQueueItem, error)

 // MarkProcessing updates status to processing.
 MarkProcessing(ctx context.Context, id int64) error

 // MarkDone updates status to done and sets processed_at.
 MarkDone(ctx context.Context, id int64) error

 // MarkFailed updates status to failed, increments retry_count, stores error.
 MarkFailed(ctx context.Context, id int64, err error) error

 // DeleteOldCompleted removes done items older than retention period.
 DeleteOldCompleted(ctx context.Context, retentionDays int) (int, error)
}
```

- [ ] **Step 4: Verify ports compile**

Run: `go build ./internal/memory/domain/port/...`
Expected: Success (interfaces have no dependencies, should compile immediately)

- [ ] **Step 5: Commit**

```bash
git add internal/memory/domain/port/
git commit -m "feat(memory): add domain ports for repositories and queue

FactRepo, EntityRepo, ExtractionQueue interfaces for infrastructure

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 8: LLM and Embed Client Ports

**Files:**

- Create: `internal/memory/domain/port/llm_extractor.go`
- Create: `internal/memory/domain/port/embed_client.go`
- Create: `internal/memory/domain/port/vector_store.go`

**Interfaces:**

- Consumes: none
- Produces: LLM/Embed/Vector interfaces for application layer

- [ ] **Step 1: Create LLM extractor port**

```go
// internal/memory/domain/port/llm_extractor.go
package port

import "context"

// ExtractedFact represents a single fact extracted by LLM.
type ExtractedFact struct {
 Content    string   `json:"content"`
 Importance float64  `json:"importance"`
 Keywords   []string `json:"keywords"`
 Entities   []ExtractedEntity `json:"entities"`
}

// ExtractedEntity represents an entity mention in a fact.
type ExtractedEntity struct {
 Name string `json:"name"`
 Type string `json:"type"` // person/project/preference/tech/location
}

// ExtractionResult is the LLM response for a batch of messages.
type ExtractionResult struct {
 Facts []ExtractedFact `json:"facts"`
}

// LLMExtractor defines the interface for fact extraction via LLM.
type LLMExtractor interface {
 // ExtractFacts calls LLM to extract facts from a batch of messages.
 // Returns structured output with facts, importance scores, and entities.
 ExtractFacts(ctx context.Context, messages []Message) (*ExtractionResult, error)
}

// Message represents a single chat message for extraction.
type Message struct {
 Role    string
 Content string
}

// SupersedeJudgment represents LLM decision on whether a new fact supersedes an old one.
type SupersedeJudgment struct {
 Decision string `json:"decision"` // KEEP / SUPERSEDE
 Reason   string `json:"reason"`
}

// LLMSuperseder judges whether a new fact supersedes an existing fact.
type LLMSuperseder interface {
 // JudgeSupersede compares new fact against existing fact.
 JudgeSupersede(ctx context.Context, existingFact, newFact string) (*SupersedeJudgment, error)
}

// EntityProfiler generates rolling summaries for entities.
type EntityProfiler interface {
 // BuildProfile generates a profile summary from recent facts.
 BuildProfile(ctx context.Context, entityName, entityType string, recentFacts []string) (string, error)
}
```

- [ ] **Step 2: Create embed client port**

```go
// internal/memory/domain/port/embed_client.go
package port

import "context"

// EmbedClient defines the interface for generating embeddings.
type EmbedClient interface {
 // EmbedText generates a vector embedding for the given text.
 // Returns a float32 slice of fixed dimension (e.g., 1024 for qwen).
 EmbedText(ctx context.Context, text string) ([]float32, error)

 // Dimension returns the embedding vector size.
 Dimension() int
}
```

- [ ] **Step 3: Create vector store port**

```go
// internal/memory/domain/port/vector_store.go
package port

import "context"

// VectorHit represents a single search result from vector store.
type VectorHit struct {
 ID         string
 Score      float32
 Metadata   map[string]interface{}
}

// VectorStore defines operations for the Milvus vector database.
type VectorStore interface {
 // UpsertFact inserts or updates a fact vector.
 UpsertFact(ctx context.Context, tenantID, factID string, vector []float32, metadata map[string]interface{}) error

 // UpsertEntityProfile inserts or updates an entity profile vector.
 UpsertEntityProfile(ctx context.Context, tenantID, entityID string, vector []float32, metadata map[string]interface{}) error

 // SearchFacts performs vector similarity search with scope filtering.
 // scopeExpr: e.g., "user_id == 'u123' and status == 'active' and (scope == 'user' or agent_id == 'a456')"
 SearchFacts(ctx context.Context, tenantID string, queryVector []float32, scopeExpr string, topK int) ([]*VectorHit, error)

 // DeleteFact removes a fact vector.
 DeleteFact(ctx context.Context, tenantID, factID string) error

 // DeleteEntityProfile removes an entity profile vector.
 DeleteEntityProfile(ctx context.Context, tenantID, entityID string) error
}
```

- [ ] **Step 4: Verify ports compile**

Run: `go build ./internal/memory/domain/port/...`
Expected: Success

- [ ] **Step 5: Commit**

```bash
git add internal/memory/domain/port/llm_extractor.go internal/memory/domain/port/embed_client.go internal/memory/domain/port/vector_store.go
git commit -m "feat(memory): add LLM/embed/vector ports

LLMExtractor, LLMSuperseder, EntityProfiler, EmbedClient, VectorStore

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 9: Database Migration (Drop Old + Create New)

**Files:**

- Create: `pkg/migration/sql/003_memory_v2.up.sql`
- Create: `pkg/migration/sql/003_memory_v2.down.sql`

**Interfaces:**

- Consumes: none
- Produces: Migration files for golang-migrate

- [ ] **Step 1: Write up migration SQL**

```sql
-- pkg/migration/sql/003_memory_v2.up.sql

-- Drop old memory tables (greenfield rewrite)
DROP TABLE IF EXISTS memory_outbox;
DROP TABLE IF EXISTS memory_summaries;
DROP TABLE IF EXISTS memory_token_budgets;
DROP TABLE IF EXISTS memory_entries;
DROP TABLE IF EXISTS entity_relations;
DROP TABLE IF EXISTS entities;

-- memory_facts (per-tenant schema, requires pg_trgm extension)
CREATE TABLE IF NOT EXISTS memory_facts (
    id              UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    user_id         TEXT NOT NULL,
    agent_id        TEXT,
    scope           TEXT NOT NULL CHECK (scope IN ('user','agent')),
    content         TEXT NOT NULL,
    importance      FLOAT8 NOT NULL DEFAULT 0.5,
    keywords        TEXT[] NOT NULL DEFAULT '{}',
    entity_refs     UUID[] NOT NULL DEFAULT '{}',
    source_msg_ids  UUID[] NOT NULL DEFAULT '{}',
    status          TEXT NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active','deleted','superseded','archived')),
    superseded_by   UUID REFERENCES memory_facts(id) ON DELETE SET NULL,
    access_count    INT  NOT NULL DEFAULT 0,
    last_accessed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ,
    archived_at     TIMESTAMPTZ
);

CREATE INDEX idx_memory_facts_user_active
    ON memory_facts (user_id, status, last_accessed_at DESC)
    WHERE status = 'active';

CREATE INDEX idx_memory_facts_agent_scope
    ON memory_facts (user_id, agent_id, status)
    WHERE scope = 'agent' AND status = 'active';

CREATE INDEX idx_memory_facts_content_trgm
    ON memory_facts USING GIN (content gin_trgm_ops);

CREATE INDEX idx_memory_facts_keywords
    ON memory_facts USING GIN (keywords);

CREATE UNIQUE INDEX memory_facts_one_active_supersede
    ON memory_facts (superseded_by) WHERE status = 'active';

-- memory_entities (per-tenant schema)
CREATE TABLE IF NOT EXISTS memory_entities (
    id              UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    user_id         TEXT NOT NULL,
    agent_id        TEXT,
    scope           TEXT NOT NULL CHECK (scope IN ('user','agent')),
    name            TEXT NOT NULL,
    entity_type     TEXT NOT NULL,
    profile         TEXT NOT NULL DEFAULT '',
    fact_count      INT  NOT NULL DEFAULT 0,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    rebuild_after   TIMESTAMPTZ,
    status          TEXT NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active','deleted')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_memory_entities_uniq
    ON memory_entities (user_id, COALESCE(agent_id,''), name, entity_type)
    WHERE status = 'active';

CREATE INDEX idx_memory_entities_name_trgm
    ON memory_entities USING GIN (name gin_trgm_ops);

-- memory_extraction_queue (per-tenant schema)
CREATE TABLE IF NOT EXISTS memory_extraction_queue (
    id              BIGSERIAL PRIMARY KEY,
    user_id         TEXT NOT NULL,
    agent_id        TEXT NOT NULL,
    conversation_id UUID NOT NULL,
    message_ids     UUID[] NOT NULL,
    payload         JSONB NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending','processing','done','failed')),
    retry_count     INT  NOT NULL DEFAULT 0,
    last_error      TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at    TIMESTAMPTZ
);

CREATE INDEX idx_extraction_queue_pending
    ON memory_extraction_queue (created_at)
    WHERE status = 'pending';

-- memory_profile_rebuild_queue (per-tenant schema)
CREATE TABLE IF NOT EXISTS memory_profile_rebuild_queue (
    entity_id       UUID PRIMARY KEY REFERENCES memory_entities(id) ON DELETE CASCADE,
    scheduled_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retry_count     INT NOT NULL DEFAULT 0
);

-- memory_facts_pending_milvus_sync (per-tenant schema)
CREATE TABLE IF NOT EXISTS memory_facts_pending_milvus_sync (
    fact_id         UUID PRIMARY KEY REFERENCES memory_facts(id) ON DELETE CASCADE,
    op              TEXT NOT NULL CHECK (op IN ('upsert','delete')),
    enqueued_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    retry_count     INT NOT NULL DEFAULT 0
);
```

- [ ] **Step 2: Write down migration SQL**

```sql
-- pkg/migration/sql/003_memory_v2.down.sql

-- Drop v2 tables
DROP TABLE IF EXISTS memory_facts_pending_milvus_sync;
DROP TABLE IF EXISTS memory_profile_rebuild_queue;
DROP TABLE IF EXISTS memory_extraction_queue;
DROP TABLE IF EXISTS memory_entities;
DROP TABLE IF EXISTS memory_facts;

-- Restore old tables (minimal structure for rollback compatibility)
CREATE TABLE IF NOT EXISTS memory_entries (
    id           UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    user_id      TEXT,
    agent_id     TEXT,
    role         TEXT NOT NULL,
    content      TEXT NOT NULL,
    type         TEXT NOT NULL DEFAULT 'short_term',
    importance   FLOAT8 NOT NULL DEFAULT 0,
    keywords     TEXT[] NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS entities (
    id           UUID PRIMARY KEY DEFAULT public.gen_uuid_v7(),
    name         TEXT NOT NULL,
    type         TEXT NOT NULL,
    user_id      TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS memory_outbox (
    id          BIGSERIAL PRIMARY KEY,
    message_id  TEXT NOT NULL,
    payload     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

- [ ] **Step 3: Verify migration syntax**

Run: `cat pkg/migration/sql/003_memory_v2.up.sql | psql -U postgres -d stratum_test --dry-run` (or use a SQL linter)
Expected: No syntax errors

- [ ] **Step 4: Run migration up in test database**

```bash
# Assumes golang-migrate CLI installed
migrate -path pkg/migration/sql -database "postgres://postgres:password@localhost:5432/stratum_test?sslmode=disable&search_path=tenant_test001,public" up
```

Expected: Migration 003 applied successfully

- [ ] **Step 5: Verify tables exist**

Run: `psql -U postgres -d stratum_test -c "\dt memory_*"`
Expected: Tables memory_facts, memory_entities, memory_extraction_queue visible

- [ ] **Step 6: Test migration down (rollback)**

```bash
migrate -path pkg/migration/sql -database "postgres://...tenant_test001,public" down 1
```

Expected: Migration 003 reverted, old tables restored

- [ ] **Step 7: Commit**

```bash
git add pkg/migration/sql/003_memory_v2.up.sql pkg/migration/sql/003_memory_v2.down.sql
git commit -m "feat(memory): add v2 migration (drop old + create new tables)

Greenfield rewrite: memory_facts, memory_entities, queues with trigram indexes

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 10: Extend agents Table in tenant_schema.sql

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`

**Interfaces:**

- Consumes: none
- Produces: agents table extensions for memory v2 configuration

- [ ] **Step 1: Write test for agents table columns**

```go
// pkg/storage/postgres/tenant_schema_test.go (add to existing test file)
func TestAgentsMemoryColumns(t *testing.T) {
 pool := setupTestTenantPool(t, "tenant_test002")
 defer pool.Close()

 _, err := pool.Exec(context.Background(), `
  INSERT INTO agents (id, name, memory_enabled, memory_write_scope, memory_read_scope)
  VALUES ('test-agent', 'Test Agent', TRUE, 'user', 'user')
 `)
 if err != nil {
  t.Fatalf("insert agent with memory fields failed: %v", err)
 }

 var memEnabled bool
 var writeScope, readScope string
 err = pool.QueryRow(context.Background(), `
  SELECT memory_enabled, memory_write_scope, memory_read_scope
  FROM agents WHERE id='test-agent'
 `).Scan(&memEnabled, &writeScope, &readScope)
 if err != nil {
  t.Fatalf("query memory fields failed: %v", err)
 }

 if !memEnabled {
  t.Error("memory_enabled should be true")
 }
 if writeScope != "user" {
  t.Errorf("expected write_scope=user, got %s", writeScope)
 }
 if readScope != "user" {
  t.Errorf("expected read_scope=user, got %s", readScope)
 }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -v ./pkg/storage/postgres/... -run TestAgentsMemoryColumns`
Expected: FAIL (columns do not exist)

- [ ] **Step 3: Add columns to tenant_schema.sql**

```sql
-- pkg/storage/postgres/tenant_schema.sql
-- Find the agents table CREATE statement and append after the existing columns:

-- Memory v2 configuration (idempotent backfill)
ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_write_scope TEXT NOT NULL DEFAULT 'user'
    CHECK (memory_write_scope IN ('off','user','agent'));
ALTER TABLE agents ADD COLUMN IF NOT EXISTS memory_read_scope TEXT NOT NULL DEFAULT 'user'
    CHECK (memory_read_scope IN ('off','user','agent'));
```

- [ ] **Step 4: Apply schema to test tenant**

```bash
psql -U postgres -d stratum_test -c "SET search_path = tenant_test002, public;" -f pkg/storage/postgres/tenant_schema.sql
```

Expected: Columns added successfully

- [ ] **Step 5: Run test to verify it passes**

Run: `go test -v ./pkg/storage/postgres/... -run TestAgentsMemoryColumns`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add pkg/storage/postgres/tenant_schema.sql pkg/storage/postgres/tenant_schema_test.go
git commit -m "feat(memory): extend agents table with memory v2 config columns

memory_enabled, memory_write_scope, memory_read_scope (off/user/agent)

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Plan Complete

Foundation plan (Phase 1-2) finished. Next plans:

- `2026-06-21-memory-v2-infrastructure.md` (Phase 3: Repo + Milvus + LLM adapters)
- `2026-06-21-memory-v2-application.md` (Phase 4: MemoryService)
- `2026-06-21-memory-v2-workers.md` (Phase 5: Workers)
- `2026-06-21-memory-v2-integration.md` (Phase 6: chat_store + Agent接入)
- `2026-06-21-memory-v2-iam-admin.md` (Phase 7-8: system_role + admin API)
- `2026-06-21-memory-v2-frontend.md` (Phase 9: UI)
- `2026-06-21-memory-v2-e2e.md` (Phase 10: E2E tests)
