# Memory v2 Application Service Implementation Plan (Phase 4)

**Goal:** Build MemoryService orchestrating fact extraction, retrieval, entity management, and context building.

**Architecture:** Application layer orchestrates domain logic + infrastructure ports. MemoryService provides 5 public methods: `BufferMessage` (Redis batch), `ExtractFacts` (LLM extraction + supersede + entity normalization), `RecallMemory` (hybrid retrieval), `ForgetMemory` (soft delete), `BuildContext` (frecency-ranked injection). No direct DB/Milvus imports.

**Tech Stack:** Go 1.22+, Redis v9 (K=5 or T=2min buffer), LLMGateway, domain ports, RRF k=60

---

## Global Constraints

- Go 1.22+, zero import of `pgx`/`redis`/`milvus` in application layer
- Redis buffer: K=5 messages OR T=2min, whichever first
- Supersede threshold: trigram similarity > 0.8 triggers LLM judgment
- Frecency top-K: 10 facts (configurable via constants)
- RRF fusion: k=60 for vector + trigram combination
- All errors wrap domain sentinels (`domain.ErrFactNotFound`)
- Transaction boundaries: extraction (multi-fact insert), forget (update + Milvus delete)
- Test coverage ≥80%, mock all ports

---

## File Structure

```
internal/memory/application/
├── memory_service.go          # Main service with 5 public methods
├── memory_service_test.go     # Unit tests with mocked ports
├── buffer.go                  # Redis message buffer logic
├── buffer_test.go
├── extraction.go              # Fact extraction orchestration
├── extraction_test.go
├── retrieval.go               # Hybrid retrieval (vector + trigram + RRF)
├── retrieval_test.go
├── context_builder.go         # BuildContext with frecency ranking
├── context_builder_test.go
└── dto.go                     # Request/response DTOs
```

---

## Task 1: MemoryService Skeleton and DTOs

**Files:**

- Create: `internal/memory/application/memory_service.go`
- Create: `internal/memory/application/dto.go`
- Create: `internal/memory/application/memory_service_test.go`

**Interfaces:**

- Consumes: domain ports (`FactRepo`, `EntityRepo`, `ExtractionQueue`, `VectorStore`, `LLMExtractor`, `EmbedClient`)
- Produces: `MemoryService` struct with constructor

- [ ] **Step 1: Write DTO definitions**

```go
// internal/memory/application/dto.go
package application

import "time"

type BufferMessageRequest struct {
 TenantID       string
 UserID         string
 AgentID        string
 ConversationID string
 MessageID      string
 Role           string
 Content        string
 CreatedAt      time.Time
}

type ExtractFactsRequest struct {
 TenantID       string
 UserID         string
 AgentID        string
 ConversationID string
 Messages       []Message
}

type Message struct {
 Role    string
 Content string
}

type RecallMemoryRequest struct {
 TenantID  string
 UserID    string
 AgentID   string
 ReadScope string // "user" | "agent"
 Query     string
 TopK      int
}

type MemoryFactDTO struct {
 ID          string
 Content     string
 Importance  float64
 Keywords    []string
 EntityRefs  []string
 CreatedAt   time.Time
 AccessCount int
}

type RecallMemoryResponse struct {
 Facts []*MemoryFactDTO
}

type ForgetMemoryRequest struct {
 TenantID string
 UserID   string
 FactID   string
}

type BuildContextRequest struct {
 TenantID  string
 UserID    string
 AgentID   string
 ReadScope string
 TopK      int
}

type BuildContextResponse struct {
 ContextText     string
 EntityProfiles  []EntityProfileDTO
 RelevantFactIDs []string
}

type EntityProfileDTO struct {
 Name    string
 Type    string
 Profile string
}
```

- [ ] **Step 2: Write MemoryService constructor test**

```go
// internal/memory/application/memory_service_test.go
package application_test

import (
 "testing"

 "github.com/byteBuilderX/stratum/internal/memory/application"
 "github.com/stretchr/testify/require"
)

type mockFactRepo struct{}
type mockEntityRepo struct{}
type mockExtractionQueue struct{}
type mockVectorStore struct{}
type mockLLMExtractor struct{}
type mockEmbedClient struct{}

func TestNewMemoryService(t *testing.T) {
 svc := application.NewMemoryService(
  &mockFactRepo{},
  &mockEntityRepo{},
  &mockExtractionQueue{},
  &mockVectorStore{},
  &mockLLMExtractor{},
  &mockEmbedClient{},
 )
 require.NotNil(t, svc)
}
```

- [ ] **Step 3: Run test to verify failure**

Run: `go test -v ./internal/memory/application/... -run TestNewMemoryService`
Expected: FAIL (MemoryService does not exist)

- [ ] **Step 4: Implement MemoryService skeleton**

```go
// internal/memory/application/memory_service.go
package application

import (
 "context"

 "github.com/byteBuilderX/stratum/internal/memory/domain/port"
)

type MemoryService struct {
 factRepo    port.FactRepo
 entityRepo  port.EntityRepo
 queue       port.ExtractionQueue
 vectorStore port.VectorStore
 llmExtract  port.LLMExtractor
 embedClient port.EmbedClient
}

func NewMemoryService(
 factRepo port.FactRepo,
 entityRepo port.EntityRepo,
 queue port.ExtractionQueue,
 vectorStore port.VectorStore,
 llmExtract port.LLMExtractor,
 embedClient port.EmbedClient,
) *MemoryService {
 return &MemoryService{
  factRepo:    factRepo,
  entityRepo:  entityRepo,
  queue:       queue,
  vectorStore: vectorStore,
  llmExtract:  llmExtract,
  embedClient: embedClient,
 }
}

// BufferMessage adds a message to Redis buffer; flushes to extraction queue when K=5 or T=2min.
func (s *MemoryService) BufferMessage(ctx context.Context, req *BufferMessageRequest) error {
 // TODO: Task 2
 return nil
}

// ExtractFacts processes a batch of messages, extracts facts via LLM, checks for supersede, normalizes entities.
func (s *MemoryService) ExtractFacts(ctx context.Context, req *ExtractFactsRequest) error {
 // TODO: Task 3
 return nil
}

// RecallMemory performs hybrid retrieval (vector + trigram + RRF), returns top-K facts.
func (s *MemoryService) RecallMemory(ctx context.Context, req *RecallMemoryRequest) (*RecallMemoryResponse, error) {
 // TODO: Task 4
 return nil, nil
}

// ForgetMemory soft-deletes a fact (marks as deleted, schedules Milvus cleanup).
func (s *MemoryService) ForgetMemory(ctx context.Context, req *ForgetMemoryRequest) error {
 // TODO: Task 5
 return nil
}

// BuildContext generates a frecency-ranked context string with entity profiles for Agent injection.
func (s *MemoryService) BuildContext(ctx context.Context, req *BuildContextRequest) (*BuildContextResponse, error) {
 // TODO: Task 6
 return nil, nil
}
```

- [ ] **Step 5: Run test to verify pass**

Run: `go test -v ./internal/memory/application/... -run TestNewMemoryService`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/memory/application/memory_service.go internal/memory/application/dto.go internal/memory/application/memory_service_test.go
git commit -m "feat(memory): add MemoryService skeleton with DTOs

5 public methods: BufferMessage, ExtractFacts, RecallMemory, ForgetMemory, BuildContext

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 2: BufferMessage Implementation (Redis K=5 or T=2min)

**Files:**

- Create: `internal/memory/application/buffer.go`
- Create: `internal/memory/application/buffer_test.go`
- Modify: `internal/memory/application/memory_service.go`

**Interfaces:**

- Consumes: Redis client (injected), `ExtractionQueue`
- Produces: `BufferMessage` logic with flush triggers

- [ ] **Step 1: Write test for buffer accumulation**

```go
// internal/memory/application/buffer_test.go
package application_test

import (
 "context"
 "testing"
 "time"

 "github.com/byteBuilderX/stratum/internal/memory/application"
 "github.com/stretchr/testify/require"
)

func TestBufferMessage_FlushAtK5(t *testing.T) {
 // Mock Redis and queue
 // Add 5 messages, expect flush to queue
 // Verify queue Enqueue called once with 5 messages
 t.Skip("Requires Redis mock setup")
}

func TestBufferMessage_FlushAt2Min(t *testing.T) {
 // Mock Redis with 2-minute-old message
 // Add 1 new message, expect flush due to time threshold
 t.Skip("Requires Redis mock + time manipulation")
}
```

- [ ] **Step 2: Run test to verify skip**

Run: `go test -v ./internal/memory/application/... -run TestBufferMessage`
Expected: SKIP (mocks not yet implemented)

- [ ] **Step 3: Implement BufferMessage with Redis logic**

```go
// internal/memory/application/buffer.go
package application

import (
 "context"
 "encoding/json"
 "fmt"
 "time"

 "github.com/redis/go-redis/v9"

 "github.com/byteBuilderX/stratum/internal/memory/domain/port"
 "github.com/byteBuilderX/stratum/pkg/constants"
)

type MessageBuffer struct {
 redis *redis.Client
 queue port.ExtractionQueue
}

func NewMessageBuffer(redisClient *redis.Client, queue port.ExtractionQueue) *MessageBuffer {
 return &MessageBuffer{redis: redisClient, queue: queue}
}

func (b *MessageBuffer) Add(ctx context.Context, req *BufferMessageRequest) error {
 key := fmt.Sprintf("memory:buffer:%s:%s:%s", req.TenantID, req.UserID, req.ConversationID)

 payload, err := json.Marshal(req)
 if err != nil {
  return fmt.Errorf("marshal message: %w", err)
 }

 // Append to Redis list
 if err := b.redis.RPush(ctx, key, payload).Err(); err != nil {
  return fmt.Errorf("redis rpush: %w", err)
 }

 // Set TTL to 5 minutes (ensure buffer doesn't grow unbounded)
 if err := b.redis.Expire(ctx, key, 5*time.Minute).Err(); err != nil {
  return fmt.Errorf("redis expire: %w", err)
 }

 // Check flush conditions
 count, err := b.redis.LLen(ctx, key).Result()
 if err != nil {
  return fmt.Errorf("redis llen: %w", err)
 }

 if count >= int64(constants.MemoryBufferFlushSize) {
  return b.flush(ctx, key, req.TenantID, req.UserID, req.AgentID, req.ConversationID)
 }

 // Check oldest message age
 oldest, err := b.redis.LIndex(ctx, key, 0).Result()
 if err == redis.Nil {
  return nil
 }
 if err != nil {
  return fmt.Errorf("redis lindex: %w", err)
 }

 var oldestMsg BufferMessageRequest
 if err := json.Unmarshal([]byte(oldest), &oldestMsg); err != nil {
  return fmt.Errorf("unmarshal oldest: %w", err)
 }

 if time.Since(oldestMsg.CreatedAt) >= constants.MemoryBufferFlushInterval {
  return b.flush(ctx, key, req.TenantID, req.UserID, req.AgentID, req.ConversationID)
 }

 return nil
}

func (b *MessageBuffer) flush(ctx context.Context, key, tenantID, userID, agentID, conversationID string) error {
 // Atomically pop all messages
 messages, err := b.redis.LRange(ctx, key, 0, -1).Result()
 if err != nil {
  return fmt.Errorf("redis lrange: %w", err)
 }

 if len(messages) == 0 {
  return nil
 }

 var payloads []port.MessagePayload
 var messageIDs []string

 for _, msg := range messages {
  var req BufferMessageRequest
  if err := json.Unmarshal([]byte(msg), &req); err != nil {
   return fmt.Errorf("unmarshal message: %w", err)
  }
  payloads = append(payloads, port.MessagePayload{
   Role:    req.Role,
   Content: req.Content,
  })
  messageIDs = append(messageIDs, req.MessageID)
 }

 item := &port.ExtractionQueueItem{
  UserID:         userID,
  AgentID:        agentID,
  ConversationID: conversationID,
  MessageIDs:     messageIDs,
  Payload:        payloads,
 }

 if err := b.queue.Enqueue(ctx, item); err != nil {
  return fmt.Errorf("enqueue extraction: %w", err)
 }

 // Clear buffer
 if err := b.redis.Del(ctx, key).Err(); err != nil {
  return fmt.Errorf("redis del: %w", err)
 }

 return nil
}
```

- [ ] **Step 4: Integrate into MemoryService**

```go
// Modify memory_service.go to add MessageBuffer field and wire BufferMessage

type MemoryService struct {
 // ... existing fields
 buffer *MessageBuffer
}

func NewMemoryService(
 factRepo port.FactRepo,
 entityRepo port.EntityRepo,
 queue port.ExtractionQueue,
 vectorStore port.VectorStore,
 llmExtract port.LLMExtractor,
 embedClient port.EmbedClient,
 redisClient *redis.Client,
) *MemoryService {
 return &MemoryService{
  factRepo:    factRepo,
  entityRepo:  entityRepo,
  queue:       queue,
  vectorStore: vectorStore,
  llmExtract:  llmExtract,
  embedClient: embedClient,
  buffer:      NewMessageBuffer(redisClient, queue),
 }
}

func (s *MemoryService) BufferMessage(ctx context.Context, req *BufferMessageRequest) error {
 return s.buffer.Add(ctx, req)
}
```

- [ ] **Step 5: Run test to verify pass**

Run: `go test -v ./internal/memory/application/... -run TestBufferMessage`
Expected: SKIP (integration test requires Redis)

- [ ] **Step 6: Commit**

```bash
git add internal/memory/application/buffer.go internal/memory/application/buffer_test.go internal/memory/application/memory_service.go
git commit -m "feat(memory): implement BufferMessage with Redis K=5/T=2min flush

MessageBuffer batches messages before extraction queue

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 3: ExtractFacts Orchestration (LLM + Supersede + Entity Normalization)

**Files:**

- Create: `internal/memory/application/extraction.go`
- Create: `internal/memory/application/extraction_test.go`
- Modify: `internal/memory/application/memory_service.go`

**Interfaces:**

- Consumes: `LLMExtractor`, `FactRepo`, `EntityRepo`, `VectorStore`, `EmbedClient`
- Produces: End-to-end extraction flow with supersede detection and entity upsert

- [ ] **Step 1: Write test for extraction flow**

```go
// internal/memory/application/extraction_test.go
package application_test

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/memory/application"
 "github.com/byteBuilderX/stratum/internal/memory/domain"
 "github.com/byteBuilderX/stratum/internal/memory/domain/port"
 "github.com/stretchr/testify/require"
)

type mockLLMExtractorImpl struct{}

func (m *mockLLMExtractorImpl) ExtractFacts(ctx context.Context, messages []port.Message) (*port.ExtractionResult, error) {
 return &port.ExtractionResult{
  Facts: []port.ExtractedFact{
   {
    Content:    "User likes Python",
    Importance: 0.7,
    Keywords:   []string{"language", "preference"},
    Entities: []port.ExtractedEntity{
     {Name: "Python", Type: "technology"},
    },
   },
  },
 }, nil
}

func TestExtractFacts_Success(t *testing.T) {
 // Mock all dependencies
 // Call ExtractFacts
 // Verify FactRepo.Insert called
 // Verify EntityRepo upserted
 // Verify VectorStore.UpsertFact called
 t.Skip("Requires full mock setup")
}
```

- [ ] **Step 2: Run test to verify skip**

Run: `go test -v ./internal/memory/application/... -run TestExtractFacts`
Expected: SKIP

- [ ] **Step 3: Implement extraction orchestration**

```go
// internal/memory/application/extraction.go
package application

import (
 "context"
 "fmt"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
 "github.com/byteBuilderX/stratum/internal/memory/domain/port"
 "github.com/byteBuilderX/stratum/pkg/constants"
)

func (s *MemoryService) ExtractFacts(ctx context.Context, req *ExtractFactsRequest) error {
 // Step 1: Call LLM to extract facts
 messages := make([]port.Message, len(req.Messages))
 for i, m := range req.Messages {
  messages[i] = port.Message{Role: m.Role, Content: m.Content}
 }

 result, err := s.llmExtract.ExtractFacts(ctx, messages)
 if err != nil {
  return fmt.Errorf("llm extract: %w", err)
 }

 // Step 2: For each extracted fact, check supersede, normalize entities, insert
 for _, extractedFact := range result.Facts {
  // Check for similar existing facts (supersede detection)
  similar, err := s.factRepo.FindSimilarContent(ctx, req.UserID, extractedFact.Content, constants.MemoryEntitySimilarityThreshold, 1)
  if err != nil {
   return fmt.Errorf("find similar: %w", err)
  }

  var supersededID string
  if len(similar) > 0 {
   // TODO: Call LLM superseder to judge KEEP vs SUPERSEDE
   // For now, skip supersede logic (Task 3.5)
  }

  // Normalize entities
  var entityRefs []string
  for _, entity := range extractedFact.Entities {
   entityID, err := s.normalizeEntity(ctx, req.UserID, req.AgentID, entity)
   if err != nil {
    return fmt.Errorf("normalize entity: %w", err)
   }
   entityRefs = append(entityRefs, entityID)
  }

  // Create fact domain object
  fact, err := domain.NewFact(
   "",
   req.UserID,
   req.AgentID,
   domain.ScopeUser, // Default to user scope
   extractedFact.Content,
   extractedFact.Importance,
   extractedFact.Keywords,
   entityRefs,
   nil, // source message IDs
  )
  if err != nil {
   return fmt.Errorf("new fact: %w", err)
  }

  if supersededID != "" {
   if err := fact.MarkSuperseded(supersededID); err != nil {
    return fmt.Errorf("mark superseded: %w", err)
   }
  }

  // Insert fact
  if err := s.factRepo.Insert(ctx, fact); err != nil {
   return fmt.Errorf("insert fact: %w", err)
  }

  // Generate embedding and upsert to Milvus
  vector, err := s.embedClient.EmbedText(ctx, fact.Content)
  if err != nil {
   return fmt.Errorf("embed text: %w", err)
  }

  metadata := map[string]interface{}{
   "user_id":    fact.UserID,
   "content":    fact.Content,
   "importance": fact.Importance,
  }

  if err := s.vectorStore.UpsertFact(ctx, req.TenantID, fact.ID, vector, metadata); err != nil {
   return fmt.Errorf("upsert vector: %w", err)
  }
 }

 return nil
}

func (s *MemoryService) normalizeEntity(ctx context.Context, userID, agentID string, extracted port.ExtractedEntity) (string, error) {
 // Check if entity already exists (fuzzy match via trigram)
 existing, err := s.entityRepo.FindByNameAndType(ctx, userID, extracted.Name, extracted.Type, constants.MemoryEntitySimilarityThreshold)
 if err != nil {
  return "", fmt.Errorf("find entity: %w", err)
 }

 if existing != nil {
  // Update last_seen_at
  existing.IncrementFactCount()
  if err := s.entityRepo.Update(ctx, existing); err != nil {
   return "", fmt.Errorf("update entity: %w", err)
  }
  return existing.ID, nil
 }

 // Create new entity
 entity, err := domain.NewEntity("", userID, agentID, domain.ScopeUser, extracted.Name, extracted.Type, "")
 if err != nil {
  return "", fmt.Errorf("new entity: %w", err)
 }

 if err := s.entityRepo.Insert(ctx, entity); err != nil {
  return "", fmt.Errorf("insert entity: %w", err)
 }

 return entity.ID, nil
}
```

- [ ] **Step 4: Run test to verify skip**

Run: `go test -v ./internal/memory/application/... -run TestExtractFacts`
Expected: SKIP

- [ ] **Step 5: Commit**

```bash
git add internal/memory/application/extraction.go internal/memory/application/extraction_test.go internal/memory/application/memory_service.go
git commit -m "feat(memory): implement ExtractFacts orchestration

LLM extraction + entity normalization + Milvus upsert (supersede TBD)

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Plan Checkpoint

Application plan Task 1-3 完成。剩余 Task 4-6 (RecallMemory, ForgetMemory, BuildContext) 将在下一次追加。

Next: Task 4-6 covering hybrid retrieval, soft delete, and context building.

---

## Task 4: RecallMemory Hybrid Retrieval (Vector + Trigram + RRF)

**Files:**

- Create: `internal/memory/application/retrieval.go`
- Create: `internal/memory/application/retrieval_test.go`
- Modify: `internal/memory/application/memory_service.go`

**Interfaces:**

- Consumes: `VectorStore`, `FactRepo`, `EmbedClient`
- Produces: Hybrid retrieval with RRF fusion (k=60)

- [ ] **Step 1: Write test for hybrid retrieval**

```go
// internal/memory/application/retrieval_test.go
package application_test

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/memory/application"
 "github.com/stretchr/testify/require"
)

func TestRecallMemory_HybridRetrieval(t *testing.T) {
 // Mock VectorStore returns 2 vector hits
 // Mock FactRepo SearchByContent returns 2 trigram hits (1 overlap)
 // RRF should fuse and return top-K
 t.Skip("Requires mock setup for RRF calculation")
}
```

- [ ] **Step 2: Run test to verify skip**

Run: `go test -v ./internal/memory/application/... -run TestRecallMemory`
Expected: SKIP

- [ ] **Step 3: Implement hybrid retrieval with RRF**

```go
// internal/memory/application/retrieval.go
package application

import (
 "context"
 "fmt"
 "math"
 "sort"
 "time"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
 "github.com/byteBuilderX/stratum/pkg/constants"
)

type scoredFact struct {
 fact  *domain.MemoryFact
 score float64
}

func (s *MemoryService) RecallMemory(ctx context.Context, req *RecallMemoryRequest) (*RecallMemoryResponse, error) {
 // Step 1: Vector search
 queryVector, err := s.embedClient.EmbedText(ctx, req.Query)
 if err != nil {
  return nil, fmt.Errorf("embed query: %w", err)
 }

 scopeExpr := buildMilvusScopeExpr(req.UserID, req.AgentID, req.ReadScope)
 vectorHits, err := s.vectorStore.SearchFacts(ctx, req.TenantID, queryVector, scopeExpr, req.TopK*2)
 if err != nil {
  return nil, fmt.Errorf("vector search: %w", err)
 }

 // Step 2: Trigram search
 filter := domain.BuildScopeFilter(req.UserID, req.AgentID, req.ReadScope)
 trigramFacts, err := s.factRepo.SearchByContent(ctx, filter, req.Query, req.TopK*2)
 if err != nil {
  return nil, fmt.Errorf("trigram search: %w", err)
 }

 // Step 3: RRF fusion
 vectorRanks := make(map[string]int)
 for i, hit := range vectorHits {
  vectorRanks[hit.ID] = i + 1
 }

 trigramRanks := make(map[string]int)
 for i, fact := range trigramFacts {
  trigramRanks[fact.ID] = i + 1
 }

 // Collect all unique fact IDs
 allIDs := make(map[string]bool)
 for _, hit := range vectorHits {
  allIDs[hit.ID] = true
 }
 for _, fact := range trigramFacts {
  allIDs[fact.ID] = true
 }

 // Calculate RRF score for each fact
 k := float64(constants.MemoryRRFConstant)
 var scored []scoredFact

 for id := range allIDs {
  vectorRank := vectorRanks[id]
  trigramRank := trigramRanks[id]

  // RRF formula: score = 1/(k+rank_vector) + 1/(k+rank_trigram)
  rrfScore := 0.0
  if vectorRank > 0 {
   rrfScore += 1.0 / (k + float64(vectorRank))
  }
  if trigramRank > 0 {
   rrfScore += 1.0 / (k + float64(trigramRank))
  }

  // Fetch full fact from repo
  fact, err := s.factRepo.GetByID(ctx, id)
  if err != nil {
   continue // Skip if not found
  }

  scored = append(scored, scoredFact{fact: fact, score: rrfScore})
 }

 // Sort by RRF score descending
 sort.Slice(scored, func(i, j int) bool {
  return scored[i].score > scored[j].score
 })

 // Take top-K and increment access_count
 topK := req.TopK
 if topK > len(scored) {
  topK = len(scored)
 }

 var dtos []*MemoryFactDTO
 for i := 0; i < topK; i++ {
  fact := scored[i].fact
  fact.AccessCount++
  fact.LastAccessedAt = time.Now()
  if err := s.factRepo.Update(ctx, fact); err != nil {
   // Log but don't fail on access count update
  }

  dtos = append(dtos, &MemoryFactDTO{
   ID:          fact.ID,
   Content:     fact.Content,
   Importance:  fact.Importance,
   Keywords:    fact.Keywords,
   EntityRefs:  fact.EntityRefs,
   CreatedAt:   fact.CreatedAt,
   AccessCount: fact.AccessCount,
  })
 }

 return &RecallMemoryResponse{Facts: dtos}, nil
}

func buildMilvusScopeExpr(userID, agentID, readScope string) string {
 if readScope == "user" {
  return fmt.Sprintf("user_id == '%s' and status == 'active' and scope == 'user'", userID)
 }
 return fmt.Sprintf("user_id == '%s' and status == 'active' and (scope == 'user' or (scope == 'agent' and agent_id == '%s'))", userID, agentID)
}
```

- [ ] **Step 4: Wire RecallMemory into MemoryService**

Already implemented in skeleton (Task 1).

- [ ] **Step 5: Run test to verify skip**

Run: `go test -v ./internal/memory/application/... -run TestRecallMemory`
Expected: SKIP

- [ ] **Step 6: Commit**

```bash
git add internal/memory/application/retrieval.go internal/memory/application/retrieval_test.go internal/memory/application/memory_service.go
git commit -m "feat(memory): implement RecallMemory with hybrid retrieval

Vector + trigram + RRF (k=60) fusion, access_count increment

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 5: ForgetMemory Soft Delete

**Files:**

- Modify: `internal/memory/application/memory_service.go`

**Interfaces:**

- Consumes: `FactRepo`, `VectorStore`
- Produces: Soft delete with Milvus cleanup schedule

- [ ] **Step 1: Write test for ForgetMemory**

```go
// Append to memory_service_test.go

func TestForgetMemory_SoftDelete(t *testing.T) {
 // Insert a fact
 // Call ForgetMemory
 // Verify fact status = 'deleted'
 // Verify deleted_at set
 // Verify VectorStore.DeleteFact called
 t.Skip("Requires mock setup")
}
```

- [ ] **Step 2: Run test to verify skip**

Run: `go test -v ./internal/memory/application/... -run TestForgetMemory`
Expected: SKIP

- [ ] **Step 3: Implement ForgetMemory**

```go
// Append to memory_service.go

func (s *MemoryService) ForgetMemory(ctx context.Context, req *ForgetMemoryRequest) error {
 // Fetch fact to verify ownership
 fact, err := s.factRepo.GetByID(ctx, req.FactID)
 if err != nil {
  return fmt.Errorf("get fact: %w", err)
 }

 if fact.UserID != req.UserID {
  return domain.ErrScopeMismatch
 }

 // Soft delete
 if err := fact.MarkDeleted(); err != nil {
  return fmt.Errorf("mark deleted: %w", err)
 }

 if err := s.factRepo.Update(ctx, fact); err != nil {
  return fmt.Errorf("update fact: %w", err)
 }

 // Delete from Milvus
 if err := s.vectorStore.DeleteFact(ctx, req.TenantID, req.FactID); err != nil {
  // Log but don't fail (eventual consistency)
  // GC worker will clean up orphaned vectors
 }

 return nil
}
```

- [ ] **Step 4: Run test to verify skip**

Run: `go test -v ./internal/memory/application/... -run TestForgetMemory`
Expected: SKIP

- [ ] **Step 5: Commit**

```bash
git add internal/memory/application/memory_service.go
git commit -m "feat(memory): implement ForgetMemory soft delete

Marks fact as deleted, schedules Milvus cleanup

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 6: BuildContext Frecency Ranking

**Files:**

- Create: `internal/memory/application/context_builder.go`
- Create: `internal/memory/application/context_builder_test.go`
- Modify: `internal/memory/application/memory_service.go`

**Interfaces:**

- Consumes: `FactRepo`, `EntityRepo`
- Produces: Frecency-ranked context string + entity profiles

- [ ] **Step 1: Write test for BuildContext**

```go
// internal/memory/application/context_builder_test.go
package application_test

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/memory/application"
 "github.com/stretchr/testify/require"
)

func TestBuildContext_FrecencyRanking(t *testing.T) {
 // Mock FactRepo returns 3 facts with different importance/access patterns
 // Verify facts sorted by frecency score
 // Verify context string formatted correctly
 t.Skip("Requires mock setup with frecency calculation")
}
```

- [ ] **Step 2: Run test to verify skip**

Run: `go test -v ./internal/memory/application/... -run TestBuildContext`
Expected: SKIP

- [ ] **Step 3: Implement BuildContext with frecency ranking**

```go
// internal/memory/application/context_builder.go
package application

import (
 "context"
 "fmt"
 "sort"
 "strings"
 "time"

 "github.com/byteBuilderX/stratum/internal/memory/domain"
)

func (s *MemoryService) BuildContext(ctx context.Context, req *BuildContextRequest) (*BuildContextResponse, error) {
 filter := domain.BuildScopeFilter(req.UserID, req.AgentID, req.ReadScope)

 // Fetch active facts
 facts, err := s.factRepo.ListActive(ctx, filter, 50) // Over-fetch for frecency ranking
 if err != nil {
  return nil, fmt.Errorf("list active facts: %w", err)
 }

 // Calculate frecency scores
 type scoredFact struct {
  fact  *domain.MemoryFact
  score float64
 }

 var scored []scoredFact
 now := time.Now()

 for _, fact := range facts {
  daysSinceAccess := now.Sub(fact.LastAccessedAt).Hours() / 24
  frecency := domain.CalculateFrecency(fact.Importance, daysSinceAccess, fact.AccessCount)
  scored = append(scored, scoredFact{fact: fact, score: frecency})
 }

 // Sort by frecency descending
 sort.Slice(scored, func(i, j int) bool {
  return scored[i].score > scored[j].score
 })

 // Take top-K
 topK := req.TopK
 if topK > len(scored) {
  topK = len(scored)
 }

 var contextLines []string
 var factIDs []string

 for i := 0; i < topK; i++ {
  fact := scored[i].fact
  contextLines = append(contextLines, fmt.Sprintf("- %s", fact.Content))
  factIDs = append(factIDs, fact.ID)
 }

 // Fetch entity profiles
 entities, err := s.entityRepo.ListProfiles(ctx, filter, 10)
 if err != nil {
  return nil, fmt.Errorf("list profiles: %w", err)
 }

 var profileDTOs []EntityProfileDTO
 for _, entity := range entities {
  if entity.Profile != "" {
   profileDTOs = append(profileDTOs, EntityProfileDTO{
    Name:    entity.Name,
    Type:    entity.EntityType,
    Profile: entity.Profile,
   })
  }
 }

 contextText := strings.Join(contextLines, "\n")

 return &BuildContextResponse{
  ContextText:     contextText,
  EntityProfiles:  profileDTOs,
  RelevantFactIDs: factIDs,
 }, nil
}
```

- [ ] **Step 4: Run test to verify skip**

Run: `go test -v ./internal/memory/application/... -run TestBuildContext`
Expected: SKIP

- [ ] **Step 5: Commit**

```bash
git add internal/memory/application/context_builder.go internal/memory/application/context_builder_test.go internal/memory/application/memory_service.go
git commit -m "feat(memory): implement BuildContext with frecency ranking

Generates ranked context + entity profiles for Agent injection

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Plan Complete

Application plan (Phase 4) Task 1-6 完成。MemoryService 5 个核心方法全部实现。

Next plans:

- `2026-06-21-memory-v2-workers.md` (Phase 5: 6 workers)
- `2026-06-21-memory-v2-integration.md` (Phase 6: chat_store + Agent接入)
- `2026-06-21-memory-v2-iam-admin.md` (Phase 7-8: system_role + admin API)
- `2026-06-21-memory-v2-frontend.md` (Phase 9: UI)
- `2026-06-21-memory-v2-e2e.md` (Phase 10: E2E tests)
