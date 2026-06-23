# Memory v2 Integration Implementation Plan (Phase 6)

**Goal:** Integrate memory v2 with chat_store (BufferMessage hook) and Agent (BuildContext injection + recall_memory/forget_memory tools).

**Architecture:** ChatStore calls `MemoryService.BufferMessage` after message persist. Agent calls `BuildContext` before ReAct loop, injects as system context. Add 2 new tools: `recall_memory` (explicit query) and `forget_memory` (user requests deletion). Zero breaking changes to existing chat/agent APIs.

**Tech Stack:** Go 1.22+, existing Agent domain, ChatStore

---

## Global Constraints

- Zero breaking changes to existing HTTP APIs (chat/agent endpoints unchanged)
- Memory v2 is opt-in per agent via `memory_enabled` flag (default true)
- BufferMessage is fire-and-forget (errors logged, not propagated to chat response)
- BuildContext injects at top of system prompt: "## Relevant Context\n{context}\n## Entity Profiles\n{profiles}"
- Tools are auto-registered when agent has memory_enabled=true
- Test coverage ≥80%, mock MemoryService

---

## File Structure

```
internal/agent/application/
├── agent.go                    # Modify: inject BuildContext before ReAct
├── agent_test.go               # Add: context injection test
├── tools/
│   ├── recall_memory_tool.go   # New tool: explicit memory query
│   ├── recall_memory_tool_test.go
│   ├── forget_memory_tool.go   # New tool: fact deletion
│   └── forget_memory_tool_test.go

internal/agent/infrastructure/persistence/
├── chat_store.go               # Modify: add BufferMessage hook
└── chat_store_test.go

api/wiring/
├── agent.go                    # Modify: wire MemoryService into Agent
└── memory.go                   # Modify: expose MemoryService to wiring
```

---

## Task 1: ChatStore BufferMessage Hook

**Files:**

- Modify: `internal/agent/infrastructure/persistence/chat_store.go`
- Modify: `internal/agent/infrastructure/persistence/chat_store_test.go`

**Interfaces:**

- Consumes: `application.MemoryService.BufferMessage`
- Produces: `SaveMessage` calls BufferMessage after persist (fire-and-forget)

- [ ] **Step 1: Write test for BufferMessage hook**

```go
// Append to chat_store_test.go

type mockMemoryService struct {
 bufferCalls int
 lastReq     *application.BufferMessageRequest
}

func (m *mockMemoryService) BufferMessage(ctx context.Context, req *application.BufferMessageRequest) error {
 m.bufferCalls++
 m.lastReq = req
 return nil
}

func TestChatStore_SaveMessage_CallsBufferMessage(t *testing.T) {
 pool := setupChatStoreTest(t)
 memSvc := &mockMemoryService{}
 store := persistence.NewChatStore(pool).WithMemoryService(memSvc)

 msg := &domain.ChatMessage{
  ID:             "msg123",
  ConversationID: "conv456",
  Role:           "user",
  Content:        "Hello",
  CreatedAt:      time.Now(),
 }

 ctx := context.WithValue(context.Background(), "tenant_id", "tenant001")
 ctx = context.WithValue(ctx, "user_id", "user123")
 ctx = context.WithValue(ctx, "agent_id", "agent456")

 err := store.SaveMessage(ctx, msg)
 require.NoError(t, err)
 require.Equal(t, 1, memSvc.bufferCalls, "should call BufferMessage")
 require.Equal(t, "msg123", memSvc.lastReq.MessageID)
 require.Equal(t, "user123", memSvc.lastReq.UserID)
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/agent/infrastructure/persistence/... -run TestChatStore_SaveMessage_CallsBufferMessage`
Expected: FAIL (WithMemoryService method does not exist)

- [ ] **Step 3: Implement BufferMessage hook**

```go
// Modify chat_store.go

type MessageBufferer interface {
 BufferMessage(ctx context.Context, req *application.BufferMessageRequest) error
}

type ChatStore struct {
 pool      *pgxpool.Pool
 memorySvc MessageBufferer // Optional: nil when memory disabled
}

func (s *ChatStore) WithMemoryService(svc MessageBufferer) *ChatStore {
 s.memorySvc = svc
 return s
}

func (s *ChatStore) SaveMessage(ctx context.Context, msg *domain.ChatMessage) error {
 // Existing persist logic...
 query := `INSERT INTO chat_messages (id, conversation_id, role, content, steps_json, is_error, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`
 _, err := s.pool.Exec(ctx, query,
  msg.ID, msg.ConversationID, msg.Role, msg.Content,
  msg.StepsJSON, msg.IsError, msg.CreatedAt,
 )
 if err != nil {
  return fmt.Errorf("save message: %w", err)
 }

 // Fire-and-forget memory buffer (v2 integration)
 if s.memorySvc != nil {
  tenantID := ctx.Value("tenant_id").(string)
  userID := ctx.Value("user_id").(string)
  agentID := ctx.Value("agent_id").(string)

  bufReq := &application.BufferMessageRequest{
   TenantID:       tenantID,
   UserID:         userID,
   AgentID:        agentID,
   ConversationID: msg.ConversationID,
   MessageID:      msg.ID,
   Role:           msg.Role,
   Content:        msg.Content,
   CreatedAt:      msg.CreatedAt,
  }
  if err := s.memorySvc.BufferMessage(ctx, bufReq); err != nil {
   // Log but don't fail the chat save
   logger := ctx.Value("logger").(*zap.Logger)
   if logger != nil {
    logger.Warn("memory.buffer_failed",
     zap.String("message_id", msg.ID),
     zap.Error(err))
   }
  }
 }

 return nil
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./internal/agent/infrastructure/persistence/... -run TestChatStore_SaveMessage_CallsBufferMessage`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/infrastructure/persistence/chat_store.go internal/agent/infrastructure/persistence/chat_store_test.go
git commit -m "feat(memory): add BufferMessage hook to ChatStore

Fire-and-forget memory buffering after message persist

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 2: Agent BuildContext Injection

**Files:**

- Modify: `internal/agent/application/agent.go`
- Modify: `internal/agent/application/agent_test.go`

**Interfaces:**

- Consumes: `application.MemoryService.BuildContext`
- Produces: Agent injects context before ReAct loop

- [ ] **Step 1: Write test for context injection**

```go
// Append to agent_test.go

func TestAgent_InjectsMemoryContext(t *testing.T) {
 memSvc := &mockMemoryService{
  contextText: "- User prefers dark mode\n- User knows Python",
  profiles: []application.EntityProfileDTO{
   {Name: "Python", Type: "technology", Profile: "Programming language"},
  },
 }
 agent := application.NewReActAgent(config).WithMemoryService(memSvc)

 req := &application.ExecuteRequest{
  TenantID: "tenant001",
  UserID:   "user123",
  AgentID:  "agent456",
  Input:    "What languages do I know?",
 }

 ctx := context.Background()
 result, err := agent.Execute(ctx, req)
 require.NoError(t, err)
 require.Contains(t, result.SystemPrompt, "## Relevant Context")
 require.Contains(t, result.SystemPrompt, "User prefers dark mode")
 require.Contains(t, result.SystemPrompt, "## Entity Profiles")
 require.Contains(t, result.SystemPrompt, "Python: Programming language")
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/agent/application/... -run TestAgent_InjectsMemoryContext`
Expected: FAIL (WithMemoryService does not exist)

- [ ] **Step 3: Implement BuildContext injection**

```go
// Modify agent.go

type ContextBuilder interface {
 BuildContext(ctx context.Context, req *application.BuildContextRequest) (*application.BuildContextResponse, error)
}

type ReActAgent struct {
 // ... existing fields
 memorySvc ContextBuilder
}

func (a *ReActAgent) WithMemoryService(svc ContextBuilder) *ReActAgent {
 a.memorySvc = svc
 return a
}

func (a *ReActAgent) Execute(ctx context.Context, req *ExecuteRequest) (*ExecuteResult, error) {
 // Build memory context (if enabled)
 systemPrompt := a.config.SystemPrompt
 if a.memorySvc != nil && a.config.MemoryEnabled {
  memReq := &application.BuildContextRequest{
   TenantID:  req.TenantID,
   UserID:    req.UserID,
   AgentID:   req.AgentID,
   ReadScope: a.config.MemoryReadScope, // "user" | "agent"
   TopK:      constants.MemoryContextTopK,
  }
  memCtx, err := a.memorySvc.BuildContext(ctx, memReq)
  if err != nil {
   // Log but don't fail execution
   a.logger.Warn("agent.memory_context_failed", zap.Error(err))
  } else {
   systemPrompt = injectMemoryContext(systemPrompt, memCtx)
  }
 }

 // Continue with existing ReAct loop...
 return a.executeReActLoop(ctx, req, systemPrompt)
}

func injectMemoryContext(basePrompt string, memCtx *application.BuildContextResponse) string {
 var sb strings.Builder
 sb.WriteString(basePrompt)
 sb.WriteString("\n\n## Relevant Context\n")
 sb.WriteString(memCtx.ContextText)
 if len(memCtx.EntityProfiles) > 0 {
  sb.WriteString("\n\n## Entity Profiles\n")
  for _, ep := range memCtx.EntityProfiles {
   sb.WriteString(fmt.Sprintf("- %s (%s): %s\n", ep.Name, ep.Type, ep.Profile))
  }
 }
 return sb.String()
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./internal/agent/application/... -run TestAgent_InjectsMemoryContext`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/application/agent.go internal/agent/application/agent_test.go
git commit -m "feat(memory): inject BuildContext into Agent system prompt

Frecency-ranked facts + entity profiles prepended before ReAct loop

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 3: recall_memory Tool

**Files:**

- Create: `internal/agent/application/tools/recall_memory_tool.go`
- Create: `internal/agent/application/tools/recall_memory_tool_test.go`

**Interfaces:**

- Consumes: `application.MemoryService.RecallMemory`
- Produces: Tool schema + Execute implementation

- [ ] **Step 1: Write tool test**

```go
// recall_memory_tool_test.go
package tools_test

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/agent/application/tools"
 "github.com/stretchr/testify/require"
)

func TestRecallMemoryTool_Execute(t *testing.T) {
 memSvc := &mockMemoryService{
  facts: []*application.MemoryFactDTO{
   {Content: "User prefers Vim", Importance: 0.8},
   {Content: "User knows Go", Importance: 0.7},
  },
 }
 tool := tools.NewRecallMemoryTool(memSvc, "tenant001", "user123", "agent456", "user")

 input := map[string]interface{}{"query": "What editor does user prefer?"}
 result, err := tool.Execute(context.Background(), input)
 require.NoError(t, err)
 require.Contains(t, result, "User prefers Vim")
 require.Contains(t, result, "User knows Go")
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/agent/application/tools/... -run TestRecallMemoryTool`
Expected: FAIL

- [ ] **Step 3: Implement recall_memory tool**

```go
// recall_memory_tool.go
package tools

import (
 "context"
 "fmt"
 "strings"

 "github.com/byteBuilderX/stratum/internal/agent/domain"
 "github.com/byteBuilderX/stratum/internal/memory/application"
 "github.com/byteBuilderX/stratum/pkg/constants"
)

type MemoryRecaller interface {
 RecallMemory(ctx context.Context, req *application.RecallMemoryRequest) (*application.RecallMemoryResponse, error)
}

type RecallMemoryTool struct {
 memorySvc MemoryRecaller
 tenantID  string
 userID    string
 agentID   string
 readScope string
}

func NewRecallMemoryTool(svc MemoryRecaller, tenantID, userID, agentID, readScope string) *RecallMemoryTool {
 return &RecallMemoryTool{
  memorySvc: svc,
  tenantID:  tenantID,
  userID:    userID,
  agentID:   agentID,
  readScope: readScope,
 }
}

func (t *RecallMemoryTool) Name() string {
 return "recall_memory"
}

func (t *RecallMemoryTool) Description() string {
 return "Search user's long-term memory for relevant facts. Use this when you need context about user's preferences, past conversations, or mentioned entities."
}

func (t *RecallMemoryTool) Schema() domain.ToolSchema {
 return domain.ToolSchema{
  Type: "object",
  Properties: map[string]domain.PropertySchema{
   "query": {
    Type:        "string",
    Description: "Natural language query to search memory (e.g., 'user's favorite language', 'projects we discussed')",
   },
  },
  Required: []string{"query"},
 }
}

func (t *RecallMemoryTool) Execute(ctx context.Context, input map[string]interface{}) (string, error) {
 query, ok := input["query"].(string)
 if !ok || query == "" {
  return "", fmt.Errorf("missing required parameter: query")
 }

 req := &application.RecallMemoryRequest{
  TenantID:  t.tenantID,
  UserID:    t.userID,
  AgentID:   t.agentID,
  ReadScope: t.readScope,
  Query:     query,
  TopK:      constants.MemoryRecallTopK,
 }

 resp, err := t.memorySvc.RecallMemory(ctx, req)
 if err != nil {
  return "", fmt.Errorf("recall memory: %w", err)
 }

 if len(resp.Facts) == 0 {
  return "No relevant memories found for this query.", nil
 }

 var sb strings.Builder
 sb.WriteString(fmt.Sprintf("Found %d relevant memories:\n\n", len(resp.Facts)))
 for i, fact := range resp.Facts {
  sb.WriteString(fmt.Sprintf("%d. %s (importance: %.2f)\n", i+1, fact.Content, fact.Importance))
 }

 return sb.String(), nil
}

var _ domain.Tool = (*RecallMemoryTool)(nil)
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./internal/agent/application/tools/... -run TestRecallMemoryTool`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/application/tools/recall_memory_tool.go internal/agent/application/tools/recall_memory_tool_test.go
git commit -m "feat(memory): add recall_memory tool for explicit queries

Agent can query memory with natural language during ReAct loop

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 4: forget_memory Tool

**Files:**

- Create: `internal/agent/application/tools/forget_memory_tool.go`
- Create: `internal/agent/application/tools/forget_memory_tool_test.go`

**Interfaces:**

- Consumes: `application.MemoryService.ForgetMemory`
- Produces: Tool for user-requested fact deletion

- [ ] **Step 1: Write tool test**

```go
func TestForgetMemoryTool_Execute(t *testing.T) {
 memSvc := &mockMemoryService{}
 tool := tools.NewForgetMemoryTool(memSvc, "tenant001", "user123")

 input := map[string]interface{}{"fact_id": "fact123"}
 result, err := tool.Execute(context.Background(), input)
 require.NoError(t, err)
 require.Contains(t, result, "Memory forgotten")
 require.Equal(t, 1, memSvc.forgetCalls)
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/agent/application/tools/... -run TestForgetMemoryTool`
Expected: FAIL

- [ ] **Step 3: Implement forget_memory tool**

```go
// forget_memory_tool.go
package tools

import (
 "context"
 "fmt"

 "github.com/byteBuilderX/stratum/internal/agent/domain"
 "github.com/byteBuilderX/stratum/internal/memory/application"
)

type MemoryForgetter interface {
 ForgetMemory(ctx context.Context, req *application.ForgetMemoryRequest) error
}

type ForgetMemoryTool struct {
 memorySvc MemoryForgetter
 tenantID  string
 userID    string
}

func NewForgetMemoryTool(svc MemoryForgetter, tenantID, userID string) *ForgetMemoryTool {
 return &ForgetMemoryTool{
  memorySvc: svc,
  tenantID:  tenantID,
  userID:    userID,
 }
}

func (t *ForgetMemoryTool) Name() string {
 return "forget_memory"
}

func (t *ForgetMemoryTool) Description() string {
 return "Delete a specific memory fact. Use when user explicitly requests to forget something."
}

func (t *ForgetMemoryTool) Schema() domain.ToolSchema {
 return domain.ToolSchema{
  Type: "object",
  Properties: map[string]domain.PropertySchema{
   "fact_id": {
    Type:        "string",
    Description: "ID of the memory fact to delete (obtained from recall_memory results)",
   },
  },
  Required: []string{"fact_id"},
 }
}

func (t *ForgetMemoryTool) Execute(ctx context.Context, input map[string]interface{}) (string, error) {
 factID, ok := input["fact_id"].(string)
 if !ok || factID == "" {
  return "", fmt.Errorf("missing required parameter: fact_id")
 }

 req := &application.ForgetMemoryRequest{
  TenantID: t.tenantID,
  UserID:   t.userID,
  FactID:   factID,
 }

 if err := t.memorySvc.ForgetMemory(ctx, req); err != nil {
  return "", fmt.Errorf("forget memory: %w", err)
 }

 return fmt.Sprintf("Memory forgotten (fact_id: %s). This fact will no longer appear in context.", factID), nil
}

var _ domain.Tool = (*ForgetMemoryTool)(nil)
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test -v ./internal/agent/application/tools/... -run TestForgetMemoryTool`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/application/tools/forget_memory_tool.go internal/agent/application/tools/forget_memory_tool_test.go
git commit -m "feat(memory): add forget_memory tool for user-requested deletion

Soft-deletes fact, removes from Milvus, no longer appears in context

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Task 5: Wire Memory Tools into Agent

**Files:**

- Modify: `internal/agent/application/agent.go`
- Modify: `api/wiring/agent.go`

**Interfaces:**

- Produces: Auto-register recall_memory/forget_memory when memory_enabled=true

- [ ] **Step 1: Write test for tool registration**

```go
func TestAgent_RegistersMemoryTools(t *testing.T) {
 memSvc := &mockMemoryService{}
 agent := application.NewReActAgent(config).WithMemoryService(memSvc)

 tools := agent.ListTools()
 toolNames := make([]string, len(tools))
 for i, t := range tools {
  toolNames[i] = t.Name()
 }

 require.Contains(t, toolNames, "recall_memory")
 require.Contains(t, toolNames, "forget_memory")
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test -v ./internal/agent/application/... -run TestAgent_RegistersMemoryTools`
Expected: FAIL

- [ ] **Step 3: Implement tool auto-registration**

```go
// Modify agent.go

func (a *ReActAgent) WithMemoryService(svc ContextBuilder) *ReActAgent {
 a.memorySvc = svc
 // Auto-register memory tools
 if a.config.MemoryEnabled {
  a.RegisterTool(tools.NewRecallMemoryTool(
   svc, a.config.TenantID, a.config.UserID, a.config.AgentID, a.config.MemoryReadScope,
  ))
  a.RegisterTool(tools.NewForgetMemoryTool(
   svc, a.config.TenantID, a.config.UserID,
  ))
 }
 return a
}
```

- [ ] **Step 4: Wire in Container**

```go
// Modify api/wiring/agent.go

func BuildAgent(deps AgentDeps) *application.ReActAgent {
 agent := application.NewReActAgent(deps.Config)

 // Wire memory service if enabled
 if deps.Config.MemoryEnabled && deps.MemoryService != nil {
  agent.WithMemoryService(deps.MemoryService)
 }

 // Register other tools...
 return agent
}
```

- [ ] **Step 5: Run test to verify pass**

Run: `go test -v ./internal/agent/application/... -run TestAgent_RegistersMemoryTools`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/agent/application/agent.go api/wiring/agent.go
git commit -m "feat(memory): auto-register recall/forget tools when memory enabled

Tools available in ReAct loop when agent.memory_enabled=true

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

## Plan Complete

Integration plan finished. Memory v2 fully wired into chat flow + Agent execution loop. Zero breaking changes.
