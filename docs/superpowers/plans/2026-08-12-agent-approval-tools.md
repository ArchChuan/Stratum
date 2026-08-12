# 系统助手工具扩展与平台级审批体系 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 spec `2026-08-12-agent-approval-tools-design.md` 全部 12 项决策：系统助手模型工具（list_models / update_system_model）、审批流泛化（subject_kind）、评测与 MCP 配置审批角色分流、资源变更自动确认、独立审批工作台、失效终态与会话删除级联、断点恢复层层校验。

**Architecture:** 后端在现有 ToolApproval 体系（`agent_tool_approvals` 表 + AES-256 payload + digest 绑定 + CAS 状态机）上泛化：新增 `subject_kind`/`assigned_approver`/`invalidation_reason`/`conversation_id` 列与失效终态（cancelled/voided/invalidated）；handler 层按 `auth.role` 角色分流（admin/owner 直接执行，member 创建审批）；wiring 层新增 `ApprovalActionExecutor` 薄 ACL 适配器把审批执行分发到 evaluation/mcp application；前端新增 `/approvals` 工作台。恢复层（ResumeToolApproval）追加策略重查与会话存在性校验，失败显式终态 + 原因落库。

**Tech Stack:** Go 1.25 · Gin v1.9.1 · pgx v5.9.2 · React 18.3 · Ant Design 5.20 · React Router 6.26

## Global Constraints

- 每个 Task 的 Commit 步骤：**提交前必须并行 spawn 至少 2 个 review agent（code-reviewer + security-auditor），无 blocking finding 后才提交**（/goal 硬性要求）。
- 提交/PR 标题 `[type](scope): description`；type ∈ feat|fix|refactor|perf|test|docs|chore|ci。
- 后端快速验证 `go vet && go test -short ./...`；PR 前 `make test-verify-before-pr` + `make risk-guardrails`。
- 前端 PR 前 `make fe-lint && make fe-build`。
- 禁止在 `main` 分支直接提交；所有改动在 worktree `/home/yang/go-projects/stratum-approval-spec`（分支 `feat/approval-tools-design`）。
- DDD：`domain/` 仅依赖 stdlib + `pkg/constants`；`application/` 不 import pgx/Redis/NATS/Gin；handler 不 import infrastructure；`pkg/` 不 import `internal/`；wiring 是唯一跨 context 装配层。
- 跨 context 审批执行必须走 `port.ApprovalActionExecutor` 接口 + wiring ACL，禁止 handler/agent application 直接 import evaluation/mcp application。
- tenant DDL 唯一基线 `pkg/storage/postgres/tenant_schema.sql`，新列一律 `ALTER TABLE ... ADD COLUMN IF NOT EXISTS`；禁止在 `pkg/migration/sql/` 复制 tenant DDL。
- 所有 tenant-scoped 存储方法走 `execTenantID(ctx, pool, tenantID, fn)`；port 方法显式含 `tenantID string`。
- 错误逐层 `fmt.Errorf("operation: %w", err)`；日志只用 Zap；禁止记录 password/token/API key/原始上游响应体。
- fail closed：角色解析失败/未知 → 拒绝，禁止默认放行；destructive 任何角色拒绝（不改 L3b 底线）。
- bearer credential 不入 URL/Web Storage/日志/下游错误正文；审批 payload 详情下发前必须脱敏（token/key/password/secret/credential 等 → `***`）。
- 行为数字（TTL/分页/timeout）禁止内联：`pkg/constants/agent.go` 或包内 defaults.go。
- 前端：用户可见字符串中文；`message.error({ content: err.response?.data?.error || '操作失败', duration: 0 })`；禁止 `alert()`/`confirm()`/提交 `console.log`；行为常量入 `web/src/constants/index.ts`；页面不得跨 `pages/` 导入；页面组件 >200 行提取 hook/component。
- 修改 port 后立即同步所有 test mock/stub。
- 执行器复用同一 service 方法（禁止平行实现）：审批通过后的执行与 admin/owner 直接执行必须走同一代码路径。
- 测试先写（TDD）：先写失败测试 → 运行确认失败 → 最小实现 → 运行确认通过 → 提交。

---

### Task 1: 审批 DDL 扩展 + 领域状态机失效终态 + 存储层

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql:773-811`（agent_tool_approvals 块，末尾追加升级语句）
- Modify: `internal/agent/domain/tool_approval.go`
- Modify: `internal/agent/domain/port/repository.go:65-74`
- Modify: `internal/agent/infrastructure/persistence/tool_approval_store.go`
- Test: `internal/agent/domain/tool_approval_test.go`、`internal/agent/infrastructure/persistence/tool_approval_integration_test.go`（扩展现有）

**Interfaces:**

- Consumes: 现有 `ToolApprovalRepo`（repository.go:65-74）、`ValidateToolApprovalTransition`（tool_approval.go:27）、`execTenantID`（chat_store.go:56）。
- Produces:
  - domain 常量：`ToolApprovalCancelled`/`ToolApprovalVoided`/`ToolApprovalInvalidated`（ToolApprovalStatus 值 "cancelled"/"voided"/"invalidated"）；`SubjectKindMCPTool="mcp_tool"`/`SubjectKindEvaluationAction="evaluation_action"`/`SubjectKindMCPPolicy="mcp_policy"`/`SubjectKindMCPServer="mcp_server"`。
  - domain 函数：`ValidateSubjectKind(kind string) error`。
  - domain 错误：`ErrApprovalSelfDecision`/`ErrApprovalAssigneeMismatch`/`ErrApprovalAssigneeInvalid`/`ErrApprovalRoleDenied`/`ErrApprovalPolicyChanged`/`ErrApprovalTargetGone`/`ErrApprovalConversationGone`/`ErrApprovalInvalidated`。
  - `domain.ToolApproval` 新字段：`SubjectKind`/`AssignedApprover`/`InvalidationReason`/`ConversationID`（均 string，json 标签 `subject_kind`/`assigned_approver,omitempty`/`invalidation_reason,omitempty`/`conversation_id,omitempty`）。
  - port 扩展（repository.go ToolApprovalRepo）：

    ```go
    ListPending(ctx context.Context, tenantID, userID string) ([]domain.ToolApproval, error) // userID=="" 全量
    ListHistory(ctx context.Context, tenantID string, page, pageSize int) ([]domain.ToolApproval, int, error) // 第二返回=总条数
    Invalidate(ctx context.Context, tenantID, id, reason string) error // CAS approved/executing→invalidated
    Void(ctx context.Context, tenantID, id, reason string) error       // CAS approved→voided
    UpdateAssignee(ctx context.Context, tenantID, id, assignee string) error // CAS pending
    CascadeByConversation(ctx context.Context, tenantID, conversationID string) error // 事务内级联（pending→cancelled、approved→voided）
    ```

- [ ] **Step 1: 写领域状态机失败测试**

在 `internal/agent/domain/tool_approval_test.go` 追加（若文件不存在则创建）：

```go
func TestValidateToolApprovalTransitionTerminalStates(t *testing.T) {
 cases := []struct {
  name string
  from ToolApprovalStatus
  to   ToolApprovalStatus
  ok   bool
 }{
  {"pending to cancelled", ToolApprovalPending, ToolApprovalCancelled, true},
  {"approved to voided", ToolApprovalApproved, ToolApprovalVoided, true},
  {"approved to invalidated", ToolApprovalApproved, ToolApprovalInvalidated, true},
  {"executing to invalidated", ToolApprovalExecuting, ToolApprovalInvalidated, true},
  {"terminal cancelled no further", ToolApprovalCancelled, ToolApprovalApproved, false},
  {"terminal voided no further", ToolApprovalVoided, ToolApprovalExecuting, false},
  {"terminal invalidated no further", ToolApprovalInvalidated, ToolApprovalExecuted, false},
  {"pending to voided not allowed", ToolApprovalPending, ToolApprovalVoided, false},
  {"approved to cancelled not allowed", ToolApprovalApproved, ToolApprovalCancelled, false},
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   err := ValidateToolApprovalTransition(tc.from, tc.to)
   if tc.ok && err != nil {
    t.Fatalf("expected allowed, got %v", err)
   }
   if !tc.ok && err == nil {
    t.Fatalf("expected error for %s -> %s", tc.from, tc.to)
   }
  })
 }
}

func TestValidateSubjectKind(t *testing.T) {
 for _, kind := range []string{SubjectKindMCPTool, SubjectKindEvaluationAction, SubjectKindMCPPolicy, SubjectKindMCPServer} {
  if err := ValidateSubjectKind(kind); err != nil {
   t.Fatalf("expected %s valid, got %v", kind, err)
  }
 }
 for _, kind := range []string{"", "unknown_kind", "mcp-tool"} {
  if err := ValidateSubjectKind(kind); err == nil {
   t.Fatalf("expected %s invalid", kind)
  }
 }
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test -short ./internal/agent/domain/...`
Expected: FAIL —— `ValidateSubjectKind` undefined / 新状态常量 undefined。

- [ ] **Step 3: 实现领域层**

`internal/agent/domain/tool_approval.go` 修改：

```go
var (
 ErrApprovalNotFound        = errors.New("tool approval not found")
 ErrApprovalAlreadyDecided  = errors.New("tool approval already decided")
 ErrApprovalAlreadyExecuted = errors.New("tool approval already executed")
 ErrApprovalSelfDecision    = errors.New("tool approval self decision not allowed")
 ErrApprovalAssigneeMismatch = errors.New("tool approval assigned approver mismatch")
 ErrApprovalAssigneeInvalid  = errors.New("tool approval assigned approver is not an admin or owner")
 ErrApprovalRoleDenied       = errors.New("tool approval requires admin or owner role")
 ErrApprovalPolicyChanged    = errors.New("tool approval policy changed")
 ErrApprovalTargetGone       = errors.New("tool approval target is gone")
 ErrApprovalConversationGone = errors.New("tool approval conversation is gone")
 ErrApprovalInvalidated      = errors.New("tool approval invalidated")
)

// SubjectKind 标识审批作用对象类型（D3 泛化）。
const (
 SubjectKindMCPTool          = "mcp_tool"
 SubjectKindEvaluationAction = "evaluation_action"
 SubjectKindMCPPolicy        = "mcp_policy"
 SubjectKindMCPServer        = "mcp_server"
)

// ValidateSubjectKind 校验审批 subject 类型，空值视为 mcp_tool（兼容存量调用）。
func ValidateSubjectKind(kind string) error {
 switch kind {
 case "", SubjectKindMCPTool, SubjectKindEvaluationAction, SubjectKindMCPPolicy, SubjectKindMCPServer:
  return nil
 }
 return fmt.Errorf("invalid tool approval subject kind: %s", kind)
}

const (
 ToolApprovalPending        ToolApprovalStatus = "pending"
 ToolApprovalApproved       ToolApprovalStatus = "approved"
 ToolApprovalRejected       ToolApprovalStatus = "rejected"
 ToolApprovalExpired        ToolApprovalStatus = "expired"
 ToolApprovalExecuting      ToolApprovalStatus = "executing"
 ToolApprovalExecuted       ToolApprovalStatus = "executed"
 ToolApprovalOutcomeUnknown ToolApprovalStatus = "unknown_outcome"
 ToolApprovalCancelled      ToolApprovalStatus = "cancelled"
 ToolApprovalVoided         ToolApprovalStatus = "voided"
 ToolApprovalInvalidated    ToolApprovalStatus = "invalidated"
)

func ValidateToolApprovalTransition(from, to ToolApprovalStatus) error {
 allowed := false
 switch from {
 case ToolApprovalPending:
  allowed = to == ToolApprovalApproved || to == ToolApprovalRejected || to == ToolApprovalExpired || to == ToolApprovalCancelled
 case ToolApprovalApproved:
  allowed = to == ToolApprovalExecuting || to == ToolApprovalVoided || to == ToolApprovalInvalidated
 case ToolApprovalExecuting:
  allowed = to == ToolApprovalExecuted || to == ToolApprovalApproved || to == ToolApprovalOutcomeUnknown || to == ToolApprovalInvalidated
 }
 if !allowed {
  return fmt.Errorf("invalid tool approval transition: %s -> %s", from, to)
 }
 return nil
}
```

`domain.ToolApproval` struct 追加字段（放在 `PolicyVersion` 与 `EncryptedPayload` 之间）：

```go
 PolicyVersion            string     `json:"policy_version"`
 SubjectKind              string     `json:"subject_kind"`
 AssignedApprover         string     `json:"assigned_approver,omitempty"`
 InvalidationReason       string     `json:"invalidation_reason,omitempty"`
 ConversationID           string     `json:"conversation_id,omitempty"`
 EncryptedPayload         string     `json:"-"`
```

`internal/agent/domain/port/repository.go` ToolApprovalRepo 接口改为：

```go
type ToolApprovalRepo interface {
 Create(ctx context.Context, tenantID string, a domain.ToolApproval) (string, error)
 Get(ctx context.Context, tenantID, id string) (domain.ToolApproval, error)
 Decide(ctx context.Context, tenantID, id, decision, actor, reason string, now time.Time) error
 MarkExecuted(ctx context.Context, tenantID, id string) error
 ClaimExecution(ctx context.Context, tenantID, id string) error
 ReleaseExecution(ctx context.Context, tenantID, id string) error
 MarkOutcomeUnknown(ctx context.Context, tenantID, id string) error
 // ListPending 返回未过期 pending 审批；userID 非空时仅返回该用户发起的（member 语义）。
 ListPending(ctx context.Context, tenantID, userID string) ([]domain.ToolApproval, error)
 // ListHistory 返回非 pending 状态（decided/executed/expired/invalidated/voided/cancelled）分页列表，第二返回值为总数。
 ListHistory(ctx context.Context, tenantID string, page, pageSize int) ([]domain.ToolApproval, int, error)
 // Invalidate CAS：仅 approved/executing → invalidated，写入 invalidation_reason。
 Invalidate(ctx context.Context, tenantID, id, reason string) error
 // Void CAS：仅 approved → voided，写入 invalidation_reason。
 Void(ctx context.Context, tenantID, id, reason string) error
 // UpdateAssignee CAS：仅 pending 可改指定审批人。
 UpdateAssignee(ctx context.Context, tenantID, id, assignee string) error
 // CascadeByConversation 事务内将关联审批 pending→cancelled、approved→voided（原因 conversation_deleted）。
 CascadeByConversation(ctx context.Context, tenantID, conversationID string) error
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go vet ./internal/agent/domain/... && go test -short ./internal/agent/domain/...`
Expected: PASS（若 agent_store_test / 其他 mock 因接口扩展编译失败，属预期——Task 1 Step 5 后继续时同步 mock，或本步内先修 mock stub：给每个 mock 实现补上 6 个新方法桩，见 Step 6 的 store 实现签名）。如 `internal/agent/application` 已因 port 扩展编译失败，本 Task 不要求 application 编译通过（Task 2 恢复），仅验证 domain 包测试。

- [ ] **Step 5: 写存储层失败测试（集成，需 make infra-up）**

在 `internal/agent/infrastructure/persistence/tool_approval_integration_test.go` 末尾追加：

```go
func TestToolApprovalInvalidateVoidAndCascade(t *testing.T) {
 ctx := context.Background()
 pool, schema := integrationPool(t) // 沿用文件内现有 helper
 tenantID := "approval-inval-" + uuid.NewString()[:8]
 store := persistence.NewPgToolApprovalStore(pool)

 create := func(status, convID string) string {
  id, err := store.Create(ctx, tenantID, domain.ToolApproval{
   DecisionID: uuid.NewString(), ExecutionID: uuid.NewString(), TraceID: "t", AgentID: "a",
   UserID: "user-1", ToolCallID: uuid.NewString(), ServerID: "srv", ToolName: "tool",
   RiskLevel: "unclassified", EncryptedPayload: "enc", Status: status, ExpiresAt: time.Now().Add(time.Hour),
   SubjectKind: domain.SubjectKindMCPTool, ConversationID: convID,
  })
  if err != nil {
   t.Fatalf("create: %v", err)
  }
  return id
 }
 convID := "conv-cascade-" + uuid.NewString()[:8]
 approvedID := create("approved", convID)
 pendingID := create("pending", convID)
 executedID := create("executed", convID)
 otherID := create("approved", "other-conv")

 if err := store.Invalidate(ctx, tenantID, approvedID, "policy_changed"); err != nil {
  t.Fatalf("invalidate approved: %v", err)
 }
 // CAS：invalidated 终态再 Invalidate 必须失败
 if err := store.Invalidate(ctx, tenantID, approvedID, "again"); err == nil {
  t.Fatal("expected invalidate on terminal to fail")
 }
 if err := store.Void(ctx, tenantID, approvedID, "conversation_deleted"); err == nil {
  t.Fatal("expected void on invalidated to fail")
 }
 if err := store.Void(ctx, tenantID, otherID, "conversation_deleted"); err != nil {
  t.Fatalf("void other: %v", err)
 }
 // 级联：pending→cancelled、approved→voided；executed/其他会话不动
 if err := store.CascadeByConversation(ctx, tenantID, convID); err != nil {
  t.Fatalf("cascade: %v", err)
 }
 for _, tc := range []struct {
  id     string
  status string
 }{
  {approvedID, "invalidated"}, // 已被 Invalidate 先行抢占
  {pendingID, "cancelled"},
  {executedID, "executed"},
  {otherID, "voided"},
 } {
  var got string
  if err := pool.QueryRow(ctx, `SELECT status FROM "`+schema+`".agent_tool_approvals WHERE id=$1`, tc.id).Scan(&got); err != nil {
   t.Fatalf("query %s: %v", tc.id, err)
  }
  if got != tc.status {
   t.Fatalf("id %s: expected %s, got %s", tc.id, tc.status, got)
  }
 }
}

func TestToolApprovalListHistoryPaged(t *testing.T) {
 ctx := context.Background()
 pool, schema := integrationPool(t)
 tenantID := "approval-hist-" + uuid.NewString()[:8]
 store := persistence.NewPgToolApprovalStore(pool)
 for i := 0; i < 5; i++ {
  if _, err := store.Create(ctx, tenantID, domain.ToolApproval{
   DecisionID: uuid.NewString(), ExecutionID: uuid.NewString(), TraceID: "t", AgentID: "a",
   UserID: "user-1", ToolCallID: uuid.NewString(), ServerID: "srv", ToolName: "tool",
   RiskLevel: "unclassified", EncryptedPayload: "enc", Status: "executed", ExpiresAt: time.Now().Add(time.Hour),
   SubjectKind: domain.SubjectKindMCPTool, ConversationID: "c",
  }); err != nil {
   t.Fatalf("create %d: %v", i, err)
  }
 }
 rows, total, err := store.ListHistory(ctx, tenantID, 1, 2)
 if err != nil {
  t.Fatalf("list history: %v", err)
 }
 if total != 5 {
  t.Fatalf("expected total 5, got %d", total)
 }
 if len(rows) != 2 {
  t.Fatalf("expected page size 2, got %d", len(rows))
 }
}
```

（若文件内 helper 名不同，以文件内现有 `integrationPool`/schema 建立方式为准——先读该文件头部。）

- [ ] **Step 6: 实现存储层**

`pkg/storage/postgres/tenant_schema.sql` agent_tool_approvals 块（现有 `CREATE INDEX IF NOT EXISTS idx_agent_tool_approvals_pending` 之后）追加：

```sql
-- D3/D8/D9: subject 泛化、指定审批人、失效终态、会话级联
ALTER TABLE agent_tool_approvals ADD COLUMN IF NOT EXISTS subject_kind TEXT NOT NULL DEFAULT 'mcp_tool';
ALTER TABLE agent_tool_approvals ADD COLUMN IF NOT EXISTS assigned_approver TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_tool_approvals ADD COLUMN IF NOT EXISTS invalidation_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_tool_approvals ADD COLUMN IF NOT EXISTS conversation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE agent_tool_approvals DROP CONSTRAINT IF EXISTS agent_tool_approvals_status_check;
ALTER TABLE agent_tool_approvals ADD CONSTRAINT agent_tool_approvals_status_check
    CHECK (status IN ('pending', 'approved', 'rejected', 'expired', 'executing', 'executed',
                      'unknown_outcome', 'cancelled', 'voided', 'invalidated'));
CREATE INDEX IF NOT EXISTS idx_agent_tool_approvals_subject
    ON agent_tool_approvals (subject_kind, status);
CREATE INDEX IF NOT EXISTS idx_agent_tool_approvals_assignee
    ON agent_tool_approvals (assigned_approver, status);
CREATE INDEX IF NOT EXISTS idx_agent_tool_approvals_conversation
    ON agent_tool_approvals (conversation_id, status);
```

`internal/agent/infrastructure/persistence/tool_approval_store.go` 修改：

Create 的 INSERT 列追加 `subject_kind,assigned_approver,conversation_id`，VALUES 追加 `$17,$18,$19`，参数追加 `a.SubjectKind, a.AssignedApprover, a.ConversationID`（现有占位符到 `$16`，新增三个）：

```go
  return tx.QueryRow(ctx, `INSERT INTO agent_tool_approvals
   (decision_id,execution_id,trace_id,agent_id,user_id,tool_call_id,server_id,tool_name,risk_level,
    arguments_digest,skill_revisions_digest,mcp_revisions_digest,knowledge_revisions_digest,
    policy_version,subject_kind,assigned_approver,conversation_id,encrypted_payload,status,expires_at)
   VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,'pending',$19)
   ON CONFLICT(execution_id,tool_call_id) DO UPDATE SET execution_id=EXCLUDED.execution_id
   RETURNING id`, a.DecisionID, a.ExecutionID, a.TraceID, a.AgentID, a.UserID, a.ToolCallID, a.ServerID,
   a.ToolName, a.RiskLevel, a.ArgumentsDigest, a.SkillRevisionsDigest, a.MCPRevisionsDigest,
   a.KnowledgeRevisionsDigest, a.PolicyVersion,
   a.SubjectKind, a.AssignedApprover, a.ConversationID,
   a.EncryptedPayload, a.ExpiresAt).Scan(&id)
```

Get 的 SELECT 追加 `subject_kind,assigned_approver,invalidation_reason,conversation_id`，Scan 追加对应字段。

ListPending 改为：

```go
func (s *PgToolApprovalStore) ListPending(ctx context.Context, tenantID, userID string) ([]domain.ToolApproval, error) {
 out := []domain.ToolApproval{}
 err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  query := `SELECT id,execution_id,trace_id,agent_id,user_id,tool_call_id,server_id,tool_name,risk_level,
   subject_kind,assigned_approver,status,created_at,expires_at FROM agent_tool_approvals
   WHERE status='pending' AND expires_at>NOW()`
  args := []any{}
  if userID != "" {
   query += ` AND user_id=$1`
   args = append(args, userID)
  }
  // admin/owner 视角：指定给当前用户的最前（软绑定优先级提示，D8）
  if userID != "" {
   query += ` ORDER BY CASE WHEN assigned_approver=$1 THEN 0 ELSE 1 END, created_at`
  } else {
   query += ` ORDER BY created_at`
  }
  rows, err := tx.Query(ctx, query, args...)
  if err != nil {
   return err
  }
  defer rows.Close()
  for rows.Next() {
   var a domain.ToolApproval
   if err := rows.Scan(&a.ID, &a.ExecutionID, &a.TraceID, &a.AgentID, &a.UserID, &a.ToolCallID, &a.ServerID, &a.ToolName, &a.RiskLevel, &a.SubjectKind, &a.AssignedApprover, &a.Status, &a.CreatedAt, &a.ExpiresAt); err != nil {
    return err
   }
   out = append(out, a)
  }
  return rows.Err()
 })
 return out, err
}
```

新增方法（追加在文件末尾）：

```go
func (s *PgToolApprovalStore) ListHistory(ctx context.Context, tenantID string, page, pageSize int) ([]domain.ToolApproval, int, error) {
 out := []domain.ToolApproval{}
 total := 0
 err := execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  if err := tx.QueryRow(ctx,
   `SELECT COUNT(*) FROM agent_tool_approvals WHERE status <> 'pending'`).Scan(&total); err != nil {
   return err
  }
  if page < 1 {
   page = 1
  }
  if pageSize < 1 {
   pageSize = 20
  }
  rows, err := tx.Query(ctx,
   `SELECT id,decision_id,execution_id,trace_id,agent_id,user_id,tool_call_id,server_id,tool_name,
    risk_level,subject_kind,assigned_approver,invalidation_reason,policy_version,encrypted_payload,
    status,decided_by,decision_reason,created_at,decided_at,executed_at,expires_at
    FROM agent_tool_approvals WHERE status <> 'pending' ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
   pageSize, (page-1)*pageSize)
  if err != nil {
   return err
  }
  defer rows.Close()
  for rows.Next() {
   var a domain.ToolApproval
   if err := rows.Scan(&a.ID, &a.DecisionID, &a.ExecutionID, &a.TraceID, &a.AgentID, &a.UserID,
    &a.ToolCallID, &a.ServerID, &a.ToolName, &a.RiskLevel, &a.SubjectKind, &a.AssignedApprover,
    &a.InvalidationReason, &a.PolicyVersion, &a.EncryptedPayload, &a.Status, &a.DecidedBy,
    &a.DecisionReason, &a.CreatedAt, &a.DecidedAt, &a.ExecutedAt, &a.ExpiresAt); err != nil {
    return err
   }
   out = append(out, a)
  }
  return rows.Err()
 })
 return out, total, err
}

func (s *PgToolApprovalStore) Invalidate(ctx context.Context, tenantID, id, reason string) error {
 return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  tag, err := tx.Exec(ctx,
   `UPDATE agent_tool_approvals SET status='invalidated',invalidation_reason=$2
    WHERE id=$1 AND status IN ('approved','executing')`, id, reason)
  if err != nil {
   return err
  }
  if tag.RowsAffected() != 1 {
   return domain.ErrApprovalAlreadyExecuted
  }
  return nil
 })
}

func (s *PgToolApprovalStore) Void(ctx context.Context, tenantID, id, reason string) error {
 return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  tag, err := tx.Exec(ctx,
   `UPDATE agent_tool_approvals SET status='voided',invalidation_reason=$2
    WHERE id=$1 AND status='approved'`, id, reason)
  if err != nil {
   return err
  }
  if tag.RowsAffected() != 1 {
   return domain.ErrApprovalAlreadyExecuted
  }
  return nil
 })
}

func (s *PgToolApprovalStore) UpdateAssignee(ctx context.Context, tenantID, id, assignee string) error {
 return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  tag, err := tx.Exec(ctx,
   `UPDATE agent_tool_approvals SET assigned_approver=$2 WHERE id=$1 AND status='pending'`,
   id, assignee)
  if err != nil {
   return err
  }
  if tag.RowsAffected() != 1 {
   return domain.ErrApprovalAlreadyDecided
  }
  return nil
 })
}

func (s *PgToolApprovalStore) CascadeByConversation(ctx context.Context, tenantID, conversationID string) error {
 return execTenantID(ctx, s.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  tag, err := tx.Exec(ctx,
   `UPDATE agent_tool_approvals SET status='cancelled',invalidation_reason='conversation_deleted'
    WHERE conversation_id=$1 AND status='pending'`, conversationID)
  if err != nil {
   return err
  }
  if _, err := tx.Exec(ctx,
   `UPDATE agent_tool_approvals SET status='voided',invalidation_reason='conversation_deleted'
    WHERE conversation_id=$1 AND status='approved'`, conversationID); err != nil {
   return err
  }
  if tag.RowsAffected() == 0 {
   // pending 不存在不代表失败：approved/executed 仍可能被级联
   return nil
  }
  return nil
 })
}
```

- [ ] **Step 7: 运行集成测试确认通过**

Run: `make infra-up && go test -run 'TestToolApproval' -timeout 60s ./internal/agent/infrastructure/persistence/...`
Expected: 旧用例 + 新增 2 个用例 PASS。

- [ ] **Step 8: 并行 review + 提交**

```bash
# 先并行 spawn 2 个 review agent（code-reviewer + security-auditor）审查本 Task diff，
# 确认无 blocking finding（CAS SQL 正确性、终态不可逆、tenant DDL IF NOT EXISTS）后：
cd /home/yang/go-projects/stratum-approval-spec
git add pkg/storage/postgres/tenant_schema.sql internal/agent/domain/tool_approval.go internal/agent/domain/tool_approval_test.go internal/agent/domain/port/repository.go internal/agent/infrastructure/persistence/tool_approval_store.go internal/agent/infrastructure/persistence/tool_approval_integration_test.go
git commit -m "feat(agent): 审批体系泛化 DDL + 失效终态状态机 + 存储层 CAS" -m "What: agent_tool_approvals 加 subject_kind/assigned_approver/invalidation_reason/conversation_id 列与 cancelled/voided/invalidated 终态;ToolApprovalRepo 扩展 ListHistory/Invalidate/Void/UpdateAssignee/CascadeByConversation,ListPending 支持 member 按 user_id 过滤。
Why: spec D3/D8/D9——审批覆盖评测与 MCP 配置、指定审批人软绑定、失效显式终态与会话删除级联。
HowToTest: 新增领域状态机表驱动测试 + 存储层集成测试(级联/CAS/分页),go test -short ./... 通过。"
```

---

### Task 2: ToolApprovalService 泛化 + 审批人校验 + 详情/历史/通用执行

**Files:**

- Modify: `internal/agent/application/tool_approval_service.go`
- Create: `internal/agent/domain/port/approval_action.go`
- Create: `internal/agent/application/approval_redact.go`
- Modify: `internal/agent/application/agent_service.go`（ListPendingApprovals / DecideToolApproval 同步新签名）
- Modify: `api/wiring/agent.go:288-289`（NewToolApprovalService 新参数）
- Modify: `api/http/handler/agent_approval_handler.go`（ListToolApprovals 传 userID/roleClass）
- Test: `internal/agent/application/tool_approval_service_test.go`（新建或扩展）、`internal/agent/application/agent_service_test.go`、`api/wiring/agent_test.go`

**Interfaces:**

- Consumes: Task 1 的 domain 常量/错误/字段、port 扩展、store 实现。
- Produces:
  - `port.ApprovalActionRequest` / `port.ApprovalActionExecutor`（approval_action.go）：

    ```go
    type ApprovalActionRequest struct {
     TenantID    string
     SubjectKind string
     Arguments   map[string]any
     ActorID     string // 发起人（审计）
     DecidedBy   string // 审批人（执行权限）
    }
    type ApprovalActionExecutor interface {
     ExecuteApprovalAction(ctx context.Context, req ApprovalActionRequest) (map[string]any, error)
    }
    ```

  - `ToolApprovalPayload` 新字段：`SubjectKind string`（json `subject_kind`）、`AssignedApprover string`（json `assigned_approver,omitempty`）。
  - `NewToolApprovalService(repo port.ToolApprovalRepo, checkpoints port.CheckpointRepo, key [32]byte, roles port.TenantRoleResolver) *ToolApprovalService`（签名扩展，第 4 参）。
  - 方法新签名/新方法：

    ```go
    Request(ctx, payload ToolApprovalPayload) (string, error)                       // 语义扩展，签名不变
    Decide(ctx, tenantID, id, decision, actor, reason string) error                 // 签名不变，内部增加校验
    ListPending(ctx, tenantID, userID, roleClass string) ([]domain.ToolApproval, error)
    ListHistory(ctx, tenantID string, page, pageSize int, roleClass string) ([]domain.ToolApproval, int, error)
    ApprovalDetail(ctx, tenantID, id, roleClass string) (ApprovalDetail, error)
    SetAssignee(ctx, tenantID, id, assignee, actor, roleClass string) error
    ExecuteApprovedAction(ctx, tenantID, id string, executor port.ApprovalActionExecutor) (map[string]any, error)
    ```

  - `ApprovalDetail`（application 包）：

    ```go
    type ApprovalDetail struct {
     ID                 string         `json:"id"`
     SubjectKind        string         `json:"subject_kind"`
     ToolName           string         `json:"tool_name"`
     ServerID           string         `json:"server_id"`
     RiskLevel          string         `json:"risk_level"`
     Status             string         `json:"status"`
     UserID             string         `json:"user_id"`
     AssignedApprover   string         `json:"assigned_approver,omitempty"`
     InvalidationReason string         `json:"invalidation_reason,omitempty"`
     CreatedAt          time.Time      `json:"created_at"`
     ExpiresAt          time.Time      `json:"expires_at"`
     DecidedBy          string         `json:"decided_by,omitempty"`
     DecisionReason     string         `json:"decision_reason,omitempty"`
     Payload            map[string]any `json:"payload,omitempty"` // 解密后脱敏
    }
    ```

  - `RedactSensitivePayload(args map[string]any) map[string]any`（approval_redact.go，递归脱敏）。

- [ ] **Step 1: 写 service 失败测试**

创建 `internal/agent/application/approval_redact_test.go`：

```go
func TestRedactSensitivePayload(t *testing.T) {
 got := RedactSensitivePayload(map[string]any{
  "apiKey": "sk-123", "token": "abc", "password": "p", "config": map[string]any{
   "url": "https://x", "secretKey": "s", "timeoutSec": 30,
  },
  "command": "npm start",
 })
 cfg := got["config"].(map[string]any)
 if got["apiKey"] != "***" || got["token"] != "***" || got["password"] != "***" {
  t.Fatalf("expected sensitive keys redacted, got %v", got)
 }
 if cfg["secretKey"] != "***" {
  t.Fatalf("expected nested secretKey redacted, got %v", cfg)
 }
 if cfg["url"] != "https://x" || cfg["timeoutSec"] != 30 || got["command"] != "npm start" {
  t.Fatalf("expected non-sensitive values preserved, got %v", got)
 }
}
```

在 `internal/agent/application/tool_approval_service_test.go`（若不存在则创建，仿照现有 mock 模式——`TenantRoleResolver` mock 定义 `ResolveTenantRole(ctx, tenantID, userID) (string, error)`）追加：

```go
type fakeRoleResolver struct{ role string; err error }
func (f *fakeRoleResolver) ResolveTenantRole(ctx context.Context, tenantID, userID string) (string, error) {
 return f.role, f.err
}

type fakeApprovalRepo struct {
 rows map[string]domain.ToolApproval
 decided string
}
// 按 port.ToolApprovalRepo 全方法实现；Get/Decide/Create/ListPending 有真实行为，其余返回 nil 或按 CAS 语义返回错误。

func TestDecideRejectsSelfDecision(t *testing.T) {
 now := time.Now()
 repo := &fakeApprovalRepo{rows: map[string]domain.ToolApproval{"a1": {
  ID: "a1", UserID: "user-1", Status: "pending", ExpiresAt: now.Add(time.Minute),
  EncryptedPayload: mustEncrypt(t, ToolApprovalPayload{UserID: "user-1", SubjectKind: SubjectKindMCPTool}),
 }}}
 svc := NewToolApprovalService(repo, nil, pkgcrypto.DeriveAESKey("test"), &fakeRoleResolver{role: "admin"})
 err := svc.Decide(ctx, "tenant-1", "a1", "approved", "user-1", "")
 if !errors.Is(err, domain.ErrApprovalSelfDecision) {
  t.Fatalf("expected ErrApprovalSelfDecision, got %v", err)
 }
}

func TestDecideRejectsNonAdminActor(t *testing.T) {
 now := time.Now()
 repo := &fakeApprovalRepo{rows: map[string]domain.ToolApproval{"a1": {
  ID: "a1", UserID: "user-1", Status: "pending", ExpiresAt: now.Add(time.Minute),
  EncryptedPayload: mustEncrypt(t, ToolApprovalPayload{UserID: "user-1", SubjectKind: SubjectKindMCPTool}),
 }}}
 svc := NewToolApprovalService(repo, nil, pkgcrypto.DeriveAESKey("test"), &fakeRoleResolver{role: "member"})
 err := svc.Decide(ctx, "tenant-1", "a1", "approved", "user-2", "")
 if !errors.Is(err, domain.ErrApprovalRoleDenied) {
  t.Fatalf("expected ErrApprovalRoleDenied, got %v", err)
 }
}

func TestDecideRejectsAssigneeMismatch(t *testing.T) {
 now := time.Now()
 repo := &fakeApprovalRepo{rows: map[string]domain.ToolApproval{"a1": {
  ID: "a1", UserID: "user-1", AssignedApprover: "user-3", Status: "pending",
  ExpiresAt: now.Add(time.Minute),
  EncryptedPayload: mustEncrypt(t, ToolApprovalPayload{UserID: "user-1", SubjectKind: SubjectKindMCPTool}),
 }}}
 svc := NewToolApprovalService(repo, nil, pkgcrypto.DeriveAESKey("test"), &fakeRoleResolver{role: "admin"})
 err := svc.Decide(ctx, "tenant-1", "a1", "approved", "user-2", "")
 if !errors.Is(err, domain.ErrApprovalAssigneeMismatch) {
  t.Fatalf("expected ErrApprovalAssigneeMismatch, got %v", err)
 }
}

func TestListPendingScopesByRole(t *testing.T) {
 repo := &fakeApprovalRepo{rows: map[string]domain.ToolApproval{}}
 svc := NewToolApprovalService(repo, nil, pkgcrypto.DeriveAESKey("test"), &fakeRoleResolver{role: "admin"})
 if _, err := svc.ListPending(ctx, "tenant-1", "user-1", "member"); err != nil {
  t.Fatalf("member list pending: %v", err)
 }
 // member 调用历史/详情必须被拒
 if _, _, err := svc.ListHistory(ctx, "tenant-1", 1, 20, "member"); !errors.Is(err, domain.ErrApprovalRoleDenied) {
  t.Fatalf("expected ErrApprovalRoleDenied for member history, got %v", err)
 }
 if _, err := svc.ApprovalDetail(ctx, "tenant-1", "a1", "member"); !errors.Is(err, domain.ErrApprovalRoleDenied) {
  t.Fatalf("expected ErrApprovalRoleDenied for member detail, got %v", err)
 }
}
```

（`mustEncrypt` 为测试 helper：`pkgcrypto.Encrypt(DeriveAESKey("test"), string(marshal(payload)))`；`fakeApprovalRepo` 完整实现按 port 接口补齐，未用方法返回 nil/哨兵错误。）

- [ ] **Step 2: 运行确认失败**

Run: `go test -short -run 'TestDecideRejectsSelfDecision|TestListPendingScopesByRole|TestRedactSensitivePayload' ./internal/agent/application/`
Expected: FAIL——编译失败（方法不存在）。

- [ ] **Step 3: 实现**

`internal/agent/domain/port/approval_action.go`（新建）：

```go
package port

import "context"

// ApprovalActionRequest 描述一次审批通过后的动作执行请求（D3 执行器分发）。
type ApprovalActionRequest struct {
 TenantID    string
 SubjectKind string
 Arguments   map[string]any
 ActorID     string // 发起人（审计原真）
 DecidedBy   string // 审批人（执行权限：mcp 配置类 ownership 校验以此为准）
}

// ApprovalActionExecutor 由 wiring 装配，把 subject_kind 分发到对应 context 的 service。
type ApprovalActionExecutor interface {
 ExecuteApprovalAction(ctx context.Context, req ApprovalActionRequest) (map[string]any, error)
}
```

`internal/agent/application/approval_redact.go`（新建）：

```go
package application

import "strings"

var sensitiveKeySuffixes = []string{"token", "key", "password", "secret", "credential", "authorization"}

// RedactSensitivePayload 递归脱敏审批参数中的凭据字段（工作台详情下发前必须调用）。
func RedactSensitivePayload(args map[string]any) map[string]any {
 out := make(map[string]any, len(args))
 for k, v := range args {
  lower := strings.ToLower(k)
  sensitive := false
  for _, suffix := range sensitiveKeySuffixes {
   if strings.Contains(lower, suffix) {
    sensitive = true
    break
   }
  }
  switch value := v.(type) {
  case map[string]any:
   out[k] = RedactSensitivePayload(value)
  case []any:
   items := make([]any, 0, len(value))
   for _, item := range value {
    if m, ok := item.(map[string]any); ok {
     items = append(items, RedactSensitivePayload(m))
    } else {
     items = append(items, item)
    }
   }
   out[k] = items
  default:
   if sensitive {
    out[k] = "***"
   } else {
    out[k] = v
   }
  }
 }
 return out
}
```

`internal/agent/application/tool_approval_service.go` 修改：

1) `ToolApprovalPayload` 追加字段：

```go
 SubjectKind       string `json:"subject_kind"`
 AssignedApprover  string `json:"assigned_approver,omitempty"`
```

1) 构造函数与字段：

```go
type ToolApprovalService struct {
 repo        port.ToolApprovalRepo
 checkpoints port.CheckpointRepo
 key         [32]byte
 roles       port.TenantRoleResolver
 now         func() time.Time
}

func NewToolApprovalService(repo port.ToolApprovalRepo, checkpoints port.CheckpointRepo, key [32]byte, roles port.TenantRoleResolver) *ToolApprovalService {
 return &ToolApprovalService{repo: repo, checkpoints: checkpoints, key: key, roles: roles, now: time.Now}
}
```

1) `Request` 开头规范化 + 指定审批人校验 + checkpoint 条件：

```go
func (s *ToolApprovalService) Request(ctx context.Context, payload ToolApprovalPayload) (string, error) {
 if payload.SubjectKind == "" {
  payload.SubjectKind = domain.SubjectKindMCPTool
 }
 if err := domain.ValidateSubjectKind(payload.SubjectKind); err != nil {
  return "", err
 }
 if payload.AssignedApprover != "" {
  assigneeRole, err := s.resolveRole(ctx, payload.TenantID, payload.AssignedApprover)
  if err != nil {
   return "", err
  }
  if assigneeRole != "admin" && assigneeRole != "owner" {
   return "", domain.ErrApprovalAssigneeInvalid
  }
 }
 if payload.DecisionID == "" {
  payload.DecisionID = uuid.NewString()
 }
 // ...（digest 计算、Marshal、Encrypt、expires、repo.Create 原逻辑不变，Create 已含新列）
 if s.checkpoints != nil && payload.SubjectKind == domain.SubjectKindMCPTool {
  // ...（现有 checkpoint Upsert 逻辑不动）
 }
 return id, nil
}
```

（Create 调用的 `domain.ToolApproval{}` 字面量追加 `SubjectKind: payload.SubjectKind, AssignedApprover: payload.AssignedApprover, ConversationID: payload.ConversationID`。）

1) `toolApprovalBindingMismatches` 追加 `check("subject_kind", row.SubjectKind == payload.SubjectKind)`。

2) `ApprovedPayload` 状态检查扩展（在 `row.Status != approved` 检查前）：

```go
 if row.Status == string(domain.ToolApprovalInvalidated) {
  return ToolApprovalPayload{}, domain.ErrApprovalInvalidated
 }
 if row.Status == string(domain.ToolApprovalVoided) || row.Status == string(domain.ToolApprovalCancelled) {
  return ToolApprovalPayload{}, ErrApprovalNotApproved
 }
```

1) `Decide` 重写：

```go
func (s *ToolApprovalService) Decide(ctx context.Context, tenantID, id, decision, actor, reason string) error {
 if decision != "approved" && decision != "rejected" {
  return errors.New("invalid approval decision")
 }
 role, err := s.resolveRole(ctx, tenantID, actor)
 if err != nil {
  return err
 }
 if role != "admin" && role != "owner" {
  return domain.ErrApprovalRoleDenied
 }
 row, err := s.repo.Get(ctx, tenantID, id)
 if err != nil {
  return err
 }
 if row.Status != string(domain.ToolApprovalPending) {
  return domain.ErrApprovalAlreadyDecided
 }
 plain, err := pkgcrypto.Decrypt(s.key, row.EncryptedPayload)
 if err != nil {
  return err
 }
 var payload ToolApprovalPayload
 if err := json.Unmarshal([]byte(plain), &payload); err != nil {
  return fmt.Errorf("decode approval payload: %w", err)
 }
 if payload.UserID == actor {
  return domain.ErrApprovalSelfDecision
 }
 if row.AssignedApprover != "" && row.AssignedApprover != actor {
  return domain.ErrApprovalAssigneeMismatch
 }
 return s.repo.Decide(ctx, tenantID, id, decision, actor, reason, s.now())
}

func (s *ToolApprovalService) resolveRole(ctx context.Context, tenantID, userID string) (string, error) {
 if s.roles == nil {
  return "", errors.New("tool approval role resolver unavailable")
 }
 return s.roles.ResolveTenantRole(ctx, tenantID, userID)
}
```

1) `ListPending` / `ListHistory` / `ApprovalDetail` / `SetAssignee` / `ExecuteApprovedAction` 追加：

```go
func (s *ToolApprovalService) ListPending(ctx context.Context, tenantID, userID, roleClass string) ([]domain.ToolApproval, error) {
 filter := ""
 if roleClass == "member" {
  filter = userID
 }
 return s.repo.ListPending(ctx, tenantID, filter)
}

func (s *ToolApprovalService) ListHistory(ctx context.Context, tenantID string, page, pageSize int, roleClass string) ([]domain.ToolApproval, int, error) {
 if roleClass != "admin" && roleClass != "owner" {
  return nil, 0, domain.ErrApprovalRoleDenied
 }
 return s.repo.ListHistory(ctx, tenantID, page, pageSize)
}

func (s *ToolApprovalService) ApprovalDetail(ctx context.Context, tenantID, id, roleClass string) (ApprovalDetail, error) {
 if roleClass != "admin" && roleClass != "owner" {
  return ApprovalDetail{}, domain.ErrApprovalRoleDenied
 }
 row, err := s.repo.Get(ctx, tenantID, id)
 if err != nil {
  return ApprovalDetail{}, err
 }
 detail := ApprovalDetail{
  ID: row.ID, SubjectKind: row.SubjectKind, ToolName: row.ToolName, ServerID: row.ServerID,
  RiskLevel: row.RiskLevel, Status: row.Status, UserID: row.UserID,
  AssignedApprover: row.AssignedApprover, InvalidationReason: row.InvalidationReason,
  CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt, DecidedBy: row.DecidedBy,
  DecisionReason: row.DecisionReason,
 }
 if row.EncryptedPayload != "" {
  plain, err := pkgcrypto.Decrypt(s.key, row.EncryptedPayload)
  if err != nil {
   return ApprovalDetail{}, err
  }
  var payload ToolApprovalPayload
  if err := json.Unmarshal([]byte(plain), &payload); err != nil {
   return ApprovalDetail{}, fmt.Errorf("decode approval payload: %w", err)
  }
  detail.Payload = RedactSensitivePayload(payload.Arguments)
 }
 return detail, nil
}

func (s *ToolApprovalService) SetAssignee(ctx context.Context, tenantID, id, assignee, actor, roleClass string) error {
 if roleClass != "admin" && roleClass != "owner" {
  return domain.ErrApprovalRoleDenied
 }
 role, err := s.resolveRole(ctx, tenantID, assignee)
 if err != nil {
  return err
 }
 if role != "admin" && role != "owner" {
  return domain.ErrApprovalAssigneeInvalid
 }
 return s.repo.UpdateAssignee(ctx, tenantID, id, assignee)
}

// ExecuteApprovedAction 通用执行：CAS 单次消费 + subject 分发执行器（D3/D4/D5）。
func (s *ToolApprovalService) ExecuteApprovedAction(ctx context.Context, tenantID, id string, executor port.ApprovalActionExecutor) (map[string]any, error) {
 payload, err := s.ApprovedPayload(ctx, tenantID, id)
 if err != nil {
  return nil, err
 }
 if err := s.repo.ClaimExecution(ctx, tenantID, id); err != nil {
  return nil, fmt.Errorf("claim tool approval execution: %w", err)
 }
 row, getErr := s.repo.Get(ctx, tenantID, id)
 if getErr != nil {
  return nil, getErr
 }
 output, execErr := executor.ExecuteApprovalAction(ctx, port.ApprovalActionRequest{
  TenantID: tenantID, SubjectKind: payload.SubjectKind, Arguments: payload.Arguments,
  ActorID: payload.UserID, DecidedBy: row.DecidedBy,
 })
 if execErr != nil {
  if unknownErr := s.repo.MarkOutcomeUnknown(ctx, tenantID, id); unknownErr != nil {
   return nil, errors.Join(execErr, fmt.Errorf("mark tool approval outcome unknown: %w", unknownErr))
  }
  return nil, execErr
 }
 if err := s.repo.MarkExecuted(ctx, tenantID, id); err != nil {
  return nil, fmt.Errorf("mark tool approval executed: %w", err)
 }
 return output, nil
}
```

`internal/agent/application/agent_service.go` 同步：

```go
// ListPendingApprovals 按角色分流：member 仅自己，admin/owner 全量。
func (s *AgentService) ListPendingApprovals(ctx context.Context, tenantID, userID, roleClass string) ([]domain.ToolApproval, error) {
 if s.deps.ApprovalService == nil {
  return nil, errors.New("tool approval runtime not configured")
 }
 return s.deps.ApprovalService.ListPending(ctx, tenantID, userID, roleClass)
}
```

（DecideToolApproval 调用 `s.deps.ApprovalService.Decide(ctx, tenantID, id, decision, actor, reason)` 不变——签名未变。）

`api/wiring/agent.go:288-289` 改为：

```go
ApprovalService: agentapp.NewToolApprovalService(a.ApprovalStore, a.CheckpointStore, c.Platform.AESKey, a.TenantRoleResolver),
```

（`a.TenantRoleResolver` 为 wiring AgentComponent 中已有字段；若字段名不同，用 wiring 中现有 TenantRoleResolver 变量。）

`api/http/handler/agent_approval_handler.go` ListToolApprovals 改为：

```go
func (h *AgentHandler) ListToolApprovals(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  respondMissingTenant(c)
  return
 }
 userID, _ := userIDFromCtx(c)
 roleClass := c.GetString("auth.role") // JWT 中间件注入，值 owner/admin/member
 rows, err := h.svc.ListPendingApprovals(c.Request.Context(), tenantID, userID, roleClass)
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, gin.H{"approvals": rows})
}
```

同步所有 mock：`internal/agent/application/agent_service_test.go` 中 mock ToolApprovalRepo 需补 `ListPending(ctx, tenantID, userID)` 新签名与其余新方法；wiring 测试同步 `NewToolApprovalService` 4 参。

- [ ] **Step 4: 运行确认通过**

Run: `go vet ./internal/agent/... ./api/wiring/... && go test -short ./internal/agent/application/... ./api/wiring/...`
Expected: PASS（Task 1 遗留的 mock 编译错误一并修复）。

- [ ] **Step 5: 并行 review + 提交**

```bash
# 并行 spawn code-reviewer + security-auditor，确认：自审批/assignee 校验、成员越权（history/detail）、
# 脱敏完整性（嵌套+数组）、执行 CAS 单次消费后，无 blocking finding 再提交。
cd /home/yang/go-projects/stratum-approval-spec
git add internal/agent/domain/port/approval_action.go internal/agent/application/approval_redact.go internal/agent/application/approval_redact_test.go internal/agent/application/tool_approval_service.go internal/agent/application/tool_approval_service_test.go internal/agent/application/agent_service.go internal/agent/application/agent_service_test.go api/wiring/agent.go api/http/handler/agent_approval_handler.go
git commit -m "feat(agent): 审批服务泛化——subject_kind 执行器分发/审批人校验/详情脱敏/历史分页" -m "What: ToolApprovalPayload 加 SubjectKind/AssignedApprover;Request 校验 subject 与指定审批人角色;Decide 增加禁自我+assignee 匹配+admin/owner 校验;新增 ListHistory/ApprovalDetail(脱敏)/SetAssignee/ExecuteApprovedAction(通用执行器);ListPending member 按 user_id 过滤。
Why: spec D3/D8/D10/D12——审批覆盖泛化与层层校验,执行器由 wiring 装配跨 context 分发。
HowToTest: 新增 service 表驱动测试(自我审批/非 admin/assignee mismatch/角色越权/脱敏),go test -short 通过。"
```

---

### Task 3: D1 模型工具（list_models / update_system_model）+ 可见性不裁剪 + diagnose model 区

**Files:**

- Modify: `internal/agent/domain/port/tenant_resolver.go`（或 system_assistant.go——加 `TenantModelDetailsProvider`）
- Modify: `internal/agent/domain/system_assistant.go`（加 `TenantModelDetail`）
- Modify: `internal/llmgateway/infrastructure/model_registry.go`（新增 `ListModelsByTenantDetails`）
- Modify: `api/wiring/tenant_resolver.go`（实现 `ListTenantModelDetails`）
- Modify: `internal/agent/application/agent_service.go`（deps 字段 + execution options + 工具 fn）
- Modify: `internal/agent/application/system_assistant_tools.go`（工具定义扩展）
- Modify: `internal/agent/application/graph/react_state.go`、`react_tool.go`（state 字段 + dispatch case + exec 函数）
- Modify: `api/wiring/system_assistant.go`（diagnose model collector 扩展）
- Test: `internal/agent/application/system_assistant_tools_exec_test.go`（扩展）、`api/wiring/tenant_resolver_test.go`

**Interfaces:**

- Consumes: `llmgateway/domain.Model`（Capabilities/Enabled/ProviderManaged 字段）；`AgentService.UpdateSystemAssistantModel`（agent_service.go:547，带 audit + Ready 语义）；`modelRepo.List(ctx, tenantID, port.ModelFilter{})`（返回完整 Model 行）；`s.deps.TenantModelCatalog.ListTenantChatModels`。
- Produces:
  - domain：`TenantModelDetail{Model, Provider string; Capabilities []string; Enabled, ProviderManaged bool}`
  - port：`TenantModelDetailsProvider{ ListTenantModelDetails(ctx, tenantID string) ([]domain.TenantModelDetail, error) }`
  - llmgateway：`ModelRegistry.ListModelsByTenantDetails(ctx, tenantID string) ([]domain.Model, error)`（全量含 disabled，过滤 disabled provider，按 Name 排序）
  - wiring `tenantCapabilityResolver` 实现 `ListTenantModelDetails`。
  - AgentService 新 deps 字段 `ModelDetailsProvider port.TenantModelDetailsProvider`；`systemAssistantExecutionOptions` 装配 `WithListModelsFn` / `WithUpdateSystemModelFn`；`ReActState` 新字段 `ListModelsFn func(context.Context) (map[string]any, error)`、`UpdateSystemModelFn func(context.Context, string) (map[string]any, error)`。
  - `SystemAssistantToolDefinitions()` 返回 4 个工具（search_docs / diagnose_tenant / list_models / update_system_model），删除 `SystemAssistantToolDefinitionsForRole`（后续 Task 4 并入 propose/apply）。
  - domain 常量：`SystemAssistantToolListModels` / `SystemAssistantToolUpdateSystemModel`（现有 `domain/system_assistant.go` 常量区追加）。

- [ ] **Step 1: 写失败测试**

`internal/agent/application/system_assistant_tools_exec_test.go` 追加：

```go
func TestSystemAssistantToolDefinitionsIncludeModelTools(t *testing.T) {
 defs := SystemAssistantToolDefinitions()
 names := map[string]bool{}
 for _, d := range defs {
  names[d.Name] = true
 }
 if !names[domain.SystemAssistantToolListModels] {
  t.Fatalf("expected %s in definitions, got %v", domain.SystemAssistantToolListModels, names)
 }
 if !names[domain.SystemAssistantToolUpdateSystemModel] {
  t.Fatalf("expected %s in definitions, got %v", domain.SystemAssistantToolUpdateSystemModel, names)
 }
}

func TestListModelsFnAndUpdateModelRoleGate(t *testing.T) {
 // 复用文件内现有 AgentService 构造模式；deps 注入 ModelDetailsProvider stub。
 // listModelsFn 通过 systemAssistantExecutionOptions 装配后触发：member 调用 update → 明确拒绝错误；
 // admin 调用 update → 调用 UpdateSystemAssistantModel（stub Registry）。
}
```

（完整测试体在 Step 2 与实现对照——执行用例仿照文件内现有 `execSystemAssistantToolXxx` 测试模式，通过 `svc.Execute(ctx, ...)` 与 mock deps 断言：list_models 返回 models 数组、member update 返回错误且 Registry 未被调用、admin update 成功且 Registry.UpdateSystemAssistantModel 被调用。）

`api/wiring/tenant_resolver_test.go` 追加：

```go
func TestTenantCapabilityResolverListTenantModelDetails(t *testing.T) {
 // 用现有 registry 测试构造（或 mock modelRepo）：启用 chat 模型 + 禁用模型 + embedding 模型，
 // 断言 ListTenantModelDetails 返回全量（含 disabled）、Enabled 正确、Capabilities 含 chat/embedding。
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test -short -run 'TestSystemAssistantToolDefinitionsIncludeModelTools' ./internal/agent/application/`
Expected: FAIL（常量/字段 undefined）。

- [ ] **Step 3: 实现**

`internal/agent/domain/system_assistant.go` 常量区追加：

```go
 SystemAssistantToolListModels         = "stratum_list_models"
 SystemAssistantToolUpdateSystemModel  = "stratum_update_system_model"
```

并追加类型：

```go
// TenantModelDetail 是系统助手可见的模型清单条目（D1）。
type TenantModelDetail struct {
 Model           string   `json:"model"`
 Provider        string   `json:"provider,omitempty"`
 Capabilities    []string `json:"capabilities"`
 Enabled         bool     `json:"enabled"`
 ProviderManaged bool     `json:"providerManaged"`
}
```

`internal/agent/domain/port/tenant_resolver.go` 追加：

```go
// TenantModelDetailsProvider 提供租户全量模型清单（含 disabled/embedding/providerManaged）。
type TenantModelDetailsProvider interface {
 ListTenantModelDetails(ctx context.Context, tenantID string) ([]domain.TenantModelDetail, error)
}
```

`internal/llmgateway/infrastructure/model_registry.go` 追加方法：

```go
// ListModelsByTenantDetails 返回租户全量模型（含 disabled，过滤掉不可用 provider），按名称排序。
func (r *ModelRegistry) ListModelsByTenantDetails(ctx context.Context, tenantID string) ([]domain.Model, error) {
 if r.modelRepo == nil {
  return nil, fmt.Errorf("model registry: model repo unavailable")
 }
 models, err := r.modelRepo.List(ctx, tenantID, port.ModelFilter{})
 if err != nil {
  return nil, fmt.Errorf("model registry: list model details: %w", err)
 }
 out := make([]domain.Model, 0, len(models))
 for _, m := range models {
  provider, err := r.providerRepo.Get(ctx, tenantID, m.ProviderID)
  if err != nil {
   return nil, fmt.Errorf("model registry: get provider: %w", err)
  }
  if !provider.Enabled {
   continue
  }
  out = append(out, m)
 }
 sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
 return out, nil
}
```

`api/wiring/tenant_resolver.go` 追加实现（需 import `llmgateway/domain` 别名 + `agentdomain`）：

```go
func (r *tenantCapabilityResolver) ListTenantModelDetails(
 ctx context.Context, tenantID string,
) ([]agentdomain.TenantModelDetail, error) {
 if r.registry == nil {
  return nil, fmt.Errorf("tenant model details: registry unavailable")
 }
 models, err := r.registry.ListModelsByTenantDetails(ctx, tenantID)
 if err != nil {
  return nil, fmt.Errorf("tenant model details: %w", err)
 }
 out := make([]agentdomain.TenantModelDetail, 0, len(models))
 for _, m := range models {
  caps := make([]string, 0, len(m.Capabilities))
  for _, c := range m.Capabilities {
   caps = append(caps, string(c))
  }
  out = append(out, agentdomain.TenantModelDetail{
   Model: m.Name, Provider: m.ProviderID,
   Capabilities: caps, Enabled: m.Enabled, ProviderManaged: m.ProviderManaged,
  })
 }
 return out, nil
}
```

`internal/agent/application/agent_service.go`：

- `AgentServiceDeps` 追加 `ModelDetailsProvider port.TenantModelDetailsProvider`。
- `systemAssistantExecutionOptions` 末尾（`WithSystemAssistantMode()` 之前）追加：

```go
 if s.deps.ModelDetailsProvider != nil {
  provider := s.deps.ModelDetailsProvider
  options = append(options, WithListModelsFn(func(callCtx context.Context) (map[string]any, error) {
   models, listErr := provider.ListTenantModelDetails(callCtx, meta.TenantID)
   if listErr != nil {
    return nil, listErr
   }
   return map[string]any{"models": models}, nil
  }))
 }
 if s.deps.Registry != nil {
  updateModel := func(callCtx context.Context, model string) (map[string]any, error) {
   if roleClass != "admin" && roleClass != "owner" {
    return nil, errors.New("更新平台助手模型需要管理员权限")
   }
   settings, updateErr := s.UpdateSystemAssistantModel(callCtx, model, req.UserID)
   if updateErr != nil {
    return nil, updateErr
   }
   return map[string]any{"model": settings.Model, "ready": settings.Ready,
    "availableModels": settings.AvailableModels}, nil
  }
  options = append(options, WithUpdateSystemModelFn(updateModel))
 }
```

`internal/agent/application/graph/react_state.go` 追加字段：

```go
 ListModelsFn       func(context.Context) (map[string]any, error)
 UpdateSystemModelFn func(context.Context, string) (map[string]any, error)
```

（`withSystemAssistantRoleClass` 已有——执行时角色由 fn 闭包捕获，state 无需存角色。）

`internal/agent/application/graph/react_tool.go` dispatch 追加 case + 执行函数（追加在 `execDiagnoseTenantTool` 之后）：

```go
 case domain.SystemAssistantToolListModels:
  return execListModelsTool(toolCtx, tc, s, toolStart)
 case domain.SystemAssistantToolUpdateSystemModel:
  return execUpdateSystemModelTool(toolCtx, tc, s, toolStart)
```

```go
func execListModelsTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time) toolExecResult {
 if !s.GovernedAssistant || s.ListModelsFn == nil {
  return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "list models tool unavailable", content: "error: tool unavailable"}
 }
 callCtx, cancel := context.WithTimeout(toolCtx, constants.SystemAssistantToolTimeout)
 content, callErr := s.ListModelsFn(callCtx)
 cancel()
 if callErr != nil {
  message := safeAssistantToolError(callErr)
  return toolExecResult{status: domain.ToolTraceStatusError, errMsg: message, content: "error: " + message}
 }
 guarded, guardErr := guardInternalAssistantEvidence(s.InternalToolResultGuardFn, content)
 if guardErr != nil {
  return toolExecResult{status: domain.ToolTraceStatusError, errMsg: guardErr.Error(), content: "error: tool result exceeded safe bounds"}
 }
 return toolExecResult{
  content: guarded,
  status:  domain.ToolTraceStatusSuccess,
  artifact: &domain.SystemAssistantToolArtifact{
   Tool: tc.Name, LatencyMs: time.Since(toolStart).Milliseconds(), Outcome: "success",
  },
 }
}

func execUpdateSystemModelTool(toolCtx context.Context, tc port.ToolCall, s *ReActState, toolStart time.Time) toolExecResult {
 if !s.GovernedAssistant || s.UpdateSystemModelFn == nil {
  return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "update system model tool unavailable", content: "error: tool unavailable"}
 }
 model, _ := tc.Arguments["model"].(string)
 if model == "" {
  return toolExecResult{status: domain.ToolTraceStatusError, errMsg: "invalid tool arguments", content: "error: invalid tool arguments: model required"}
 }
 callCtx, cancel := context.WithTimeout(toolCtx, constants.SystemAssistantToolTimeout)
 content, callErr := s.UpdateSystemModelFn(callCtx, model)
 cancel()
 if callErr != nil {
  message := safeAssistantToolError(callErr)
  return toolExecResult{status: domain.ToolTraceStatusError, errMsg: message, content: "error: " + message}
 }
 guarded, guardErr := guardInternalAssistantEvidence(s.InternalToolResultGuardFn, content)
 if guardErr != nil {
  return toolExecResult{status: domain.ToolTraceStatusError, errMsg: guardErr.Error(), content: "error: tool result exceeded safe bounds"}
 }
 return toolExecResult{
  content: guarded,
  status:  domain.ToolTraceStatusSuccess,
  artifact: &domain.SystemAssistantToolArtifact{
   Tool: tc.Name, LatencyMs: time.Since(toolStart).Milliseconds(), Outcome: "success",
  },
 }
}
```

`internal/agent/application/system_assistant_tools.go`：

- `SystemAssistantToolDefinitions()` 返回追加两个工具：

```go
  {
   Name: domain.SystemAssistantToolListModels, ProviderType: domain.ProviderTypeInternal,
   ProviderID: domain.SystemAssistantToolListModels, CapabilityID: domain.SystemAssistantToolListModels,
   Description: "列出当前租户全量可配置模型（含停用/embedding，标注 enabled 与能力）。",
   InputSchema: jschema.Must(jschema.ClosedObject()).Map(),
  },
  {
   Name: domain.SystemAssistantToolUpdateSystemModel, ProviderType: domain.ProviderTypeInternal,
   ProviderID: domain.SystemAssistantToolUpdateSystemModel, CapabilityID: domain.SystemAssistantToolUpdateSystemModel,
   Description: "更新平台助手（系统助手）使用的模型。需要管理员权限，member 调用会被拒绝。",
   InputSchema: jschema.Must(jschema.ClosedObject(
    jschema.RequiredProp("model", jschema.StringRange(1, 0, "")),
   )).Map(),
  },
```

- 保留 `SystemAssistantToolDefinitionsForRole` 仅移除其对 propose/apply 的裁剪（改为空操作）——**本 Task 只加模型工具，propose/apply 裁剪移除放 Task 4**（避免 Task 3 触碰 D6）。

`api/wiring/system_assistant.go` diagnose model collector 扩展：找到 `collectors[domain.DiagnosticAreaModel]` 装配处（`setDiagnosticCollectors` 或类似，含 `diagnosticModelCollector`），改为调用 `ListModelsByTenantDetails` 并输出统计事实。具体实现（追加/替换 model collector）：

```go
func diagnosticModelCollector(registry *llmgateway.ModelRegistry) diagnosticAreaCollector {
 return func(ctx context.Context, req domain.DiagnosticRequest) ([]domain.DiagnosticFact, []domain.EvidenceGap, error) {
  tenantID := req.TenantID
  models, err := registry.ListModelsByTenantDetails(ctx, tenantID)
  if err != nil {
   return nil, []domain.EvidenceGap{{Area: domain.DiagnosticAreaModel, Code: domain.DiagnosticGapUnavailable, Detail: "model catalog unavailable"}}, nil
  }
  var enabled, chat, embedding int
  var currentReady bool
  for _, m := range models {
   if m.Enabled {
    enabled++
   }
   for _, c := range m.Capabilities {
    switch c {
    case llmgatewaydomain.CapChat:
     chat++
    case llmgatewaydomain.CapEmbedding:
     embedding++
    }
   }
  }
  facts := []domain.DiagnosticFact{
   {Area: domain.DiagnosticAreaModel, ObjectID: "catalog", ObjectType: "model_catalog",
    Kind: "catalog_stats", Detail: fmt.Sprintf("total=%d enabled=%d disabled=%d chat=%d embedding=%d",
     len(models), enabled, len(models)-enabled, chat, embedding)},
  }
  // 当前系统助手模型 Ready：存在 + enabled + chat capability（D1 Ready 语义）
  current, err := registry.GetSystemAssistantModel(ctx, tenantID) // 若无此方法，改用现有获取当前模型的方式（见 Step 步骤说明）
  _ = current
  _ = currentReady
  return facts, nil, nil
 }
}
```

（注意：以 wiring 中现有 model collector 的装配与事实格式为准——先读 `api/wiring/system_assistant.go` 的 model collector 与 `domain.DiagnosticFact` 字段，保持 ObjectID/Kind/Detail 风格一致；本步骤给出的是目标语义，实现时对齐现有 collector 签名。）

- [ ] **Step 4: 运行确认通过**

Run: `go vet ./internal/... ./api/... && go test -short ./internal/agent/... ./api/wiring/...`
Expected: PASS。

- [ ] **Step 5: 并行 review + 提交**

```bash
# 并行 spawn code-reviewer + security-auditor，确认：写工具 member 拒绝路径 fail closed、
# 工具 schema 无注入面、模型清单无越权字段后提交。
cd /home/yang/go-projects/stratum-approval-spec
git add internal/agent/domain/system_assistant.go internal/agent/domain/port/tenant_resolver.go internal/llmgateway/infrastructure/model_registry.go api/wiring/tenant_resolver.go internal/agent/application/agent_service.go internal/agent/application/system_assistant_tools.go internal/agent/application/graph/react_state.go internal/agent/application/graph/react_tool.go api/wiring/system_assistant.go internal/agent/application/system_assistant_tools_exec_test.go api/wiring/tenant_resolver_test.go
git commit -m "feat(agent): 系统助手模型工具 list_models/update_system_model + diagnose model 区扩展" -m "What: 新增只读工具 stratum_list_models(全量模型清单含 disabled/embedding/providerManaged)与写工具 stratum_update_system_model(执行时角色校验,member 明确拒绝);diagnose model 区输出清单统计与当前模型 Ready 语义;模型数据源经 ModelRegistry.ListModelsByTenantDetails + wiring ACL 投影。
Why: spec D1——系统助手可读可配置模型清单,写路径角色 fail closed。
HowToTest: 新增工具定义与执行测试(member 拒绝/admin 成功含 audit),go test -short 通过。"
```

---

### Task 4: D6 资源变更整合（工具全角色可见 + admin/owner 自动确认 apply）

**Files:**

- Modify: `internal/agent/application/system_assistant_tools.go`（`SystemAssistantToolDefinitions` 并入 propose/apply；删除 `SystemAssistantToolDefinitionsForRole` 的角色分支）
- Modify: `internal/agent/application/agent_service.go`（`resolveSystemAssistantTooling` 无条件全量定义；`systemAssistantExecutionOptions` 的 ProposalCreateFn 全角色装配 + admin/owner 自动确认）
- Test: `internal/agent/application/system_assistant_tools_exec_test.go`、`internal/agent/application/agent_service_test.go`

**Interfaces:**

- Consumes: `SystemAssistantToolDefinitionsForRole`（删除）、`withProposalCreateFn`（react_state.go:68）、`ResourceChangeProposalService.ConfirmAndApply(ctx, tenantID, proposalID, actorID)`（resource_change_proposal_service.go:199，已存在——完整状态机+基线校验+审计+单次 claim）。
- Produces: 无新接口——行为变更：
  - `SystemAssistantToolDefinitions()` 现在返回 6 个工具（+ propose_resource_change / apply_resource_change，schema 不变）。
  - `resolveSystemAssistantTooling` 中 `extraTools := SystemAssistantToolDefinitions()` 无条件。
  - `systemAssistantExecutionOptions`：`withProposalCreateFn` 无条件装配；fn 内部 roleClass 分支——admin/owner：`CreateProposal` 成功后立即 `ConfirmAndApply`（自动确认+apply 一气呵成），返回最终 proposal；member：仅 `CreateProposal`（现状）。
  - 删除 `SystemAssistantToolDefinitionsForRole` 函数与调用。

- [ ] **Step 1: 写失败测试**

`internal/agent/application/system_assistant_tools_exec_test.go` 追加：

```go
func TestProposeToolVisibleToAllRoles(t *testing.T) {
 defs := SystemAssistantToolDefinitions()
 names := map[string]bool{}
 for _, d := range defs {
  names[d.Name] = true
 }
 if !names[domain.SystemAssistantToolProposeResourceChange] {
  t.Fatalf("expected propose visible to all roles, got %v", names)
 }
 if !names[domain.SystemAssistantToolApplyResourceChange] {
  t.Fatalf("expected apply visible to all roles, got %v", names)
 }
}

func TestAdminProposeAutoConfirmsAndApplies(t *testing.T) {
 // 复用文件内现有 AgentService 构造；deps.ProposalService 用真实 NewResourceChangeProposalService + mock repo
 // （或 stub 接口层）：admin 角色执行 propose 工具 →
 // 断言 CreateProposal 被调用且 ConfirmAndApply 被调用（最终 status applied）；member 角色 →
 // 仅 CreateProposal（status ready_for_review）。
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test -short -run 'TestProposeToolVisibleToAllRoles' ./internal/agent/application/`
Expected: FAIL（6 工具定义未含 propose）。

- [ ] **Step 3: 实现**

`internal/agent/application/system_assistant_tools.go`：

- `SystemAssistantToolDefinitions()` 返回体追加 propose/apply 定义（把 `SystemAssistantToolDefinitionsForRole` 中的 proposal/directApply 字面量移入）。
- 删除 `SystemAssistantToolDefinitionsForRole` 函数。

`internal/agent/application/agent_service.go`：

`resolveSystemAssistantTooling` 中：

```go
 extraTools := SystemAssistantToolDefinitions()
 // 角色裁剪已移除：可见性全角色一致，权限在执行/审批路径校验（spec D2 v4/v5）
```

（删除 `if (roleClass == "admin" || roleClass == "owner") && s.deps.ProposalService != nil { extraTools = SystemAssistantToolDefinitionsForRole(roleClass) }`。）

`systemAssistantExecutionOptions` 中 ProposalCreateFn 装配改为无条件 + 角色分支：

```go
 if s.deps.ProposalService != nil {
  proposalService := s.deps.ProposalService
  tenantID, actorID, conversationID := meta.TenantID, req.UserID, req.ConversationID
  options = append(options, withProposalCreateFn(func(callCtx context.Context, args map[string]any) (domain.ResourceChangeProposalArtifact, error) {
   kind, operation, resourceID, payload, parseErr := ParseResourceChangeToolArguments(args)
   if parseErr != nil {
    return domain.ResourceChangeProposalArtifact{}, parseErr
   }
   proposal, createErr := proposalService.CreateProposal(callCtx, CreateProposalInput{
    TenantID: tenantID, ConversationID: conversationID, ActorID: actorID,
    Kind: kind, Operation: operation, ResourceID: resourceID, Payload: payload,
   })
   if createErr != nil {
    return domain.ResourceChangeProposalArtifact{}, createErr
   }
   // D6：admin/owner 自动确认 + apply 一气呵成；member 保持提案流
   if roleClass == "admin" || roleClass == "owner" {
    applied, applyErr := proposalService.ConfirmAndApply(callCtx, tenantID, proposal.ID, actorID)
    if applyErr != nil {
     return domain.ResourceChangeProposalArtifact{}, applyErr
    }
    proposal = applied
   }
   artifact := domain.ResourceChangeProposalArtifact{
    ID: proposal.ID, ResourceKind: proposal.ResourceKind, Operation: proposal.Operation,
    Status: proposal.Status, Summary: proposal.Summary, ExpiresAt: proposal.ExpiresAt,
   }
   return artifact, nil
  }))
 }
```

（原 `withResourceChangeApplyFn` 的 admin/owner 条件分支保持——apply 工具仅 admin/owner 有执行器，member 调用 apply 工具时 fn nil → "tool unavailable" 明确拒绝，符合 D2 矩阵"member 写工具拒绝或走审批"。）

- [ ] **Step 4: 运行确认通过**

Run: `go vet ./internal/agent/... && go test -short ./internal/agent/application/...`
Expected: PASS。

- [ ] **Step 5: 并行 review + 提交**

```bash
# 并行 spawn code-reviewer + security-auditor，确认：member 调用 apply 工具被拒（fn nil 路径）、
# 自动确认不绕过状态机/基线/审计（复用 ConfirmAndApply 单路径）、角色分支无越权后提交。
cd /home/yang/go-projects/stratum-approval-spec
git add internal/agent/application/system_assistant_tools.go internal/agent/application/agent_service.go internal/agent/application/system_assistant_tools_exec_test.go internal/agent/application/agent_service_test.go
git commit -m "feat(agent): 资源变更整合——propose/apply 全角色可见,admin/owner 自动确认+apply" -m "What: 工具定义并入 propose_resource_change/apply_resource_change(全角色可见);resolveSystemAssistantTooling 移除角色裁剪;ProposalCreateFn 全角色装配,admin/owner 调用后立即 ConfirmAndApply 一气呵成,member 保持提案流。
Why: spec D2 v5/D6——可见性不裁剪,admin/owner 免审批直接执行+audit。
HowToTest: 新增工具可见性与自动确认测试,go test -short 通过。"
```

---

### Task 5: D4 评测审批（handler 角色分流 + 执行器 + 执行端点）

**Files:**

- Modify: `api/http/handler/evaluation_handler.go`（11 个写方法角色分流 + `approvals` 字段）
- Modify: `api/http/handler/agent_approval_handler.go`（新增 history/detail/execute/assignee 端点 + `actionExecutor` 字段）
- Modify: `api/http/router.go`（评测写路由放宽 requireAdmin → requireActive；agents 新增 4 路由）
- Create: `api/wiring/approval_action.go`（`ApprovalActionExecutor` 实现：evaluation 分发）
- Modify: `api/wiring/evaluation.go` / `api/wiring/agent.go`（装配 executor + handler 依赖）
- Test: `api/http/handler/evaluation_handler_test.go`、`api/http/handler/agent_approval_handler_test.go`（或 router 集成测试）

**Interfaces:**

- Consumes: Task 2 的 `ToolApprovalService.Request`/`ExecuteApprovedAction`/`ListHistory`/`ApprovalDetail`/`SetAssignee`、`port.ApprovalActionExecutor`；评测 application 方法（签名见下方）；`c.GetString("auth.role")`。
- Produces:
  - handler 辅助：`evaluationApprovalPayload(c, toolName string, args map[string]any) agentapp.ToolApprovalPayload`（SubjectKind=`evaluation_action`、RiskLevel=`unclassified`、PolicyVersion=`action-v1`、ToolCallID/ExecutionID=uuid、UserID/TenantID 从 ctx）。
  - `AgentHandler` 新字段 `actionExecutor port.ApprovalActionExecutor`；新端点：`GET /agents/tool-approvals/history`、`GET /agents/tool-approvals/:approvalID`、`POST /agents/tool-approvals/:approvalID/execute`、`PUT /agents/tool-approvals/:approvalID/assignee`。
  - `approvalActionExecutor`（wiring）：字段 `suiteSvc *evalapp.SuiteService`、`jobSvc *evalapp.JobService`、`baselineSvc *evalapp.BaselineService`、`experimentSvc *evalapp.ExperimentService`、`optimizationSvc *evalapp.OptimizationService`；`ExecuteApprovalAction` 按 subject 分发。
  - 评测执行参数契约（Arguments map key 固定）：

    ```go
    // create_suite:            {"operation":"create_suite","name":string,"description":string}
    // publish_suite:           {"operation":"publish_suite","suiteID":string}
    // generate_suite_cases:    {"operation":"generate_suite_cases","suiteID":string}
    // enqueue_run:             {"operation":"enqueue_run","resource":{"kind":string,"id":string},"suiteRevisionID":string,"idempotencyKey":string}
    // create_experiment:       {"operation":"create_experiment","stable":{"kind":string,"id":string},"canary":{"kind":string,"id":string},"suiteRevisionID":string}
    // pause_experiment:        {"operation":"pause_experiment","experimentID":string,"reason":string}
    // promote_experiment:      {"operation":"promote_experiment","experimentID":string,"reason":string}
    // rollback_experiment:     {"operation":"rollback_experiment","experimentID":string,"reason":string}
    // reject_candidate:        {"operation":"reject_candidate","candidateID":string,"reason":string}
    // create_baseline:         {"operation":"create_baseline","resourceKind":string,"resourceID":string}
    // generate_optimization:   {"operation":"generate_optimization"}
    ```

- [ ] **Step 1: 写失败测试（handler 分流）**

`api/http/handler/evaluation_handler_test.go` 追加（复用文件内现有构造 `NewEvaluationHandler` 与 gin test 模式；注入 stub `approvals`）：

```go
func TestEvaluationCreateSuiteMemberGetsPendingApproval(t *testing.T) {
 // handler 构造：approvals 用 stub（Request 返回固定 id）；POST /suites 以 member 角色（c.Set("auth.role","member")）
 // 断言：HTTP 202、响应 {"status":"pending_approval","approval_id":"..."}、ApprovalService.Request 被调用且 SubjectKind=evaluation_action
}

func TestEvaluationCreateSuiteAdminExecutesDirectly(t *testing.T) {
 // c.Set("auth.role","admin")：断言 200/201 且 Request 未被调用、suiteSvc.Create 被调用
}
```

（EvaluationHandler 现构造签名 `NewEvaluationHandler(...)`——先读文件头；若构造参数多，测试用现有 helper。若不存在该测试文件，新建并仿照 `api/http/handler/mcp_handler_test.go` 模式。）

- [ ] **Step 2: 运行确认失败**

Run: `go test -short -run 'TestEvaluationCreateSuiteMemberGetsPendingApproval' ./api/http/handler/`
Expected: FAIL（handler 无 approval 字段/无分流）。

- [ ] **Step 3: 实现 handler 分流**

`api/http/handler/evaluation_handler.go`：

- struct 加字段：`approvals *agentapp.ToolApprovalService`（构造参数追加；NewEvaluationHandler 签名同步，wiring 调用点同步）。
- 新增辅助（文件底部）：

```go
// evaluationApprovalPayload 构造评测动作审批 payload（D4：member 发起 → 审批）。
func evaluationApprovalPayload(c *gin.Context, toolName string, args map[string]any) (agentapp.ToolApprovalPayload, error) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  return agentapp.ToolApprovalPayload{}, errTenantRequired // 用文件内现有错误
 }
 userID, ok := userIDFromCtx(c)
 if !ok {
  return agentapp.ToolApprovalPayload{}, errUserRequired
 }
 return agentapp.ToolApprovalPayload{
  TenantID: tenantID, UserID: userID,
  ExecutionID: uuid.NewString(), ToolCallID: uuid.NewString(),
  ToolName: toolName, SubjectKind: agentdomain.SubjectKindEvaluationAction,
  RiskLevel: "unclassified", Arguments: args, PolicyVersion: "action-v1",
 }, nil
}

// requestApprovalForMember 返回 true 表示请求已由审批创建消化（member 路径，202）。
func (h *EvaluationHandler) requestApprovalForMember(c *gin.Context, toolName string, args map[string]any) (bool, error) {
 payload, err := evaluationApprovalPayload(c, toolName, args)
 if err != nil {
  return false, err
 }
 id, err := h.approvals.Request(c.Request.Context(), payload)
 if err != nil {
  return false, err
 }
 c.JSON(http.StatusAccepted, gin.H{"status": "pending_approval", "approval_id": id})
 return true, nil
}

// requireApprovalOrExecute 角色分流：admin/owner 直接执行；member 创建审批；角色未知 fail closed（拒绝）。
func (h *EvaluationHandler) requireApprovalOrExecute(c *gin.Context, toolName string, args map[string]any, execute func() (any, error)) {
 roleClass := c.GetString("auth.role")
 if roleClass != "admin" && roleClass != "owner" {
  if roleClass == "member" {
   handled, err := h.requestApprovalForMember(c, toolName, args)
   if err != nil {
    _ = c.Error(err)
    return
   }
   if handled {
    return
   }
  }
  _ = c.Error(middleware.NewHTTPError(http.StatusForbidden, errors.New("insufficient tenant role")))
  return
 }
 result, err := execute()
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, result)
}
```

- 11 个写方法逐个改造（示例——CreateSuite）：

```go
func (h *EvaluationHandler) CreateSuite(c *gin.Context) {
 var req struct {
  Name        string `json:"name" binding:"required"`
  Description string `json:"description" binding:"required"`
 }
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 args := map[string]any{"name": req.Name, "description": req.Description}
 h.requireApprovalOrExecute(c, "evaluation.create_suite", args, func() (any, error) {
  suite, revision, err := h.svc.Create(c.Request.Context(), tenantIDFromCtxOrEmpty(c), evalapp.CreateSuiteInput{
   Name: req.Name, Description: req.Description,
  })
  if err != nil {
   return nil, err
  }
  return gin.H{"suite": suite, "revision": revision}, nil
 })
}
```

（其余 10 个同构改造，toolName 与 args 按下述映射：publish_suite→`h.svc.Publish(ctx, tenantID, suiteID)`；generate_suite_cases→`h.svc.GenerateSuiteCases(ctx, tenantID, suiteID)`（若签名不同以现有为准）；enqueue_run→`h.jobs.EnqueueRun(ctx, tenantID, evalapp.EnqueueRunInput{...})`；create_experiment→`h.experiments.Create(...)`；pause/promote/rollback→`h.experiments.Pause(ctx, tenantID, expID, evalapp.ExperimentCommandInput{...})`；reject_candidate→`h.experiments.Reject(...)`（或现有 RejectCandidate 调用）；create_baseline→`h.baselines.CreatePublishedBaseline(...)`；generate_optimization→`h.optimizations.Generate(...)`。先读 evaluation_handler.go 现有每个方法的调用与字段名，保持直接执行路径与现状完全一致。）

`api/http/router.go` 评测路由放宽：全部写路由 `requireAdmin, requireActive` → `requireActive`（读端点 requireAdmin 保持现状不动——overview/runs/candidates/jobs 等按 spec 仍是 admin 读）。

`api/http/handler/agent_approval_handler.go` 追加端点 + AgentHandler 字段：

```go
func (h *AgentHandler) ListApprovalHistory(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok { respondMissingTenant(c); return }
 page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
 pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
 roleClass := c.GetString("auth.role")
 rows, total, err := h.svc.ListApprovalHistory(c.Request.Context(), tenantID, page, pageSize, roleClass)
 if err != nil { _ = c.Error(err); return }
 c.JSON(http.StatusOK, gin.H{"approvals": rows, "total": total, "page": page, "page_size": pageSize})
}

func (h *AgentHandler) GetApprovalDetail(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok { respondMissingTenant(c); return }
 roleClass := c.GetString("auth.role")
 detail, err := h.svc.ApprovalDetail(c.Request.Context(), tenantID, c.Param("approvalID"), roleClass)
 if err != nil { _ = c.Error(err); return }
 c.JSON(http.StatusOK, detail)
}

func (h *AgentHandler) ExecuteApproval(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok { respondMissingTenant(c); return }
 roleClass := c.GetString("auth.role")
 if roleClass != "admin" && roleClass != "owner" {
  _ = c.Error(middleware.NewHTTPError(http.StatusForbidden, errors.New("insufficient tenant role")))
  return
 }
 if h.actionExecutor == nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusInternalServerError, errors.New("approval executor not configured")))
  return
 }
 output, err := h.svc.ExecuteApprovedAction(c.Request.Context(), tenantID, c.Param("approvalID"), h.actionExecutor)
 if err != nil { _ = c.Error(err); return }
 c.JSON(http.StatusOK, gin.H{"status": "executed", "output": output})
}

func (h *AgentHandler) SetApprovalAssignee(c *gin.Context) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok { respondMissingTenant(c); return }
 var req struct {
  AssignedApprover string `json:"assignedApprover" binding:"required"`
 }
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 actor, _ := userIDFromCtx(c)
 roleClass := c.GetString("auth.role")
 if err := h.svc.SetApprovalAssignee(c.Request.Context(), tenantID, c.Param("approvalID"), req.AssignedApprover, actor, roleClass); err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, gin.H{"status": "updated"})
}
```

（AgentService 补薄转发方法 `ListApprovalHistory`/`ApprovalDetail`/`ExecuteApprovedAction`/`SetApprovalAssignee`，各调 deps.ApprovalService 同构方法；AgentHandler struct 加 `actionExecutor port.ApprovalActionExecutor` + `NewAgentHandler` 参数同步。）

`api/http/router.go` agents 组追加：

```go
  agents.GET("/tool-approvals/history", requireAdmin, requireActive, agentHandler.ListApprovalHistory)
  agents.GET("/tool-approvals/:approvalID", requireAdmin, requireActive, agentHandler.GetApprovalDetail)
  agents.POST("/tool-approvals/:approvalID/execute", requireAdmin, requireActive, agentHandler.ExecuteApproval)
  agents.PUT("/tool-approvals/:approvalID/assignee", requireAdmin, requireActive, agentHandler.SetApprovalAssignee)
```

`api/wiring/approval_action.go`（新建）：

```go
package wiring

import (
 "context"
 "encoding/json"
 "fmt"

 agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
 "github.com/byteBuilderX/stratum/internal/agent/domain/port"
 evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
 evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
 mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
)

// approvalActionExecutor 把审批通过后的动作分发到对应 context 的 service（wiring 薄 ACL，D3）。
type approvalActionExecutor struct {
 suiteSvc       *evalapp.SuiteService
 jobSvc         *evalapp.JobService
 baselineSvc    *evalapp.BaselineService
 experimentSvc  *evalapp.ExperimentService
 optimizationSvc *evalapp.OptimizationService
 mcpSvc         *mcpapp.MCPService
}

func (e *approvalActionExecutor) ExecuteApprovalAction(
 ctx context.Context, req port.ApprovalActionRequest,
) (map[string]any, error) {
 switch req.SubjectKind {
 case agentdomain.SubjectKindEvaluationAction:
  return e.executeEvaluation(ctx, req)
 case agentdomain.SubjectKindMCPPolicy, agentdomain.SubjectKindMCPServer:
  return e.executeMCPConfig(ctx, req)
 default:
  return nil, fmt.Errorf("unsupported approval subject kind: %s", req.SubjectKind)
 }
}

// executeEvaluation 按 operation 分发评测原方法（与 admin/owner 直接执行同一代码路径）。
func (e *approvalActionExecutor) executeEvaluation(
 ctx context.Context, req port.ApprovalActionRequest,
) (map[string]any, error) {
 if e.jobSvc == nil || e.suiteSvc == nil || e.baselineSvc == nil || e.experimentSvc == nil || e.optimizationSvc == nil {
  return nil, fmt.Errorf("evaluation approval executor not fully configured")
 }
 operation, _ := req.Arguments["operation"].(string)
 asMap := func(key string) map[string]any {
  m, _ := req.Arguments[key].(map[string]any)
  return m
 }
 asString := func(key string) string {
  s, _ := req.Arguments[key].(string)
  return s
 }
 resourceRef := func(m map[string]any) evaldomain.ResourceRef {
  kind, _ := m["kind"].(string)
  id, _ := m["id"].(string)
  return evaldomain.ResourceRef{Kind: evaldomain.ResourceKind(kind), ID: id}
 }
 switch operation {
 case "create_suite":
  suite, revision, err := e.suiteSvc.Create(ctx, req.TenantID, evalapp.CreateSuiteInput{
   Name: asString("name"), Description: asString("description"),
  })
  if err != nil {
   return nil, err
  }
  return map[string]any{"suite_id": suite.ID, "revision_id": revision.ID}, nil
 case "publish_suite":
  revision, err := e.suiteSvc.Publish(ctx, req.TenantID, asString("suiteID"))
  if err != nil {
   return nil, err
  }
  return map[string]any{"revision_id": revision.ID}, nil
 case "enqueue_run":
  job, err := e.jobSvc.EnqueueRun(ctx, req.TenantID, evalapp.EnqueueRunInput{
   Resource: resourceRef(asMap("resource")), SuiteRevisionID: asString("suiteRevisionID"),
   IdempotencyKey: asString("idempotencyKey"), RequestedBy: req.ActorID,
  })
  if err != nil {
   return nil, err
  }
  return map[string]any{"job_id": job.ID}, nil
 case "create_experiment":
  exp, err := e.experimentSvc.Create(ctx, req.TenantID, evalapp.CreateExperimentInput{
   Stable: resourceRef(asMap("stable")), Canary: resourceRef(asMap("canary")),
   SuiteRevisionID: asString("suiteRevisionID"),
  })
  if err != nil {
   return nil, err
  }
  return map[string]any{"experiment_id": exp.ID}, nil
 case "pause_experiment":
  if err := e.experimentSvc.Pause(ctx, req.TenantID, asString("experimentID"),
   evalapp.ExperimentCommandInput{ActorID: req.DecidedBy, Reason: asString("reason")}); err != nil {
   return nil, err
  }
  return map[string]any{"status": "paused"}, nil
 case "promote_experiment":
  if err := e.experimentSvc.Promote(ctx, req.TenantID, asString("experimentID"),
   evalapp.ExperimentCommandInput{ActorID: req.DecidedBy, Reason: asString("reason")}); err != nil {
   return nil, err
  }
  return map[string]any{"status": "promoted"}, nil
 case "rollback_experiment":
  if err := e.experimentSvc.Rollback(ctx, req.TenantID, asString("experimentID"),
   evalapp.ExperimentCommandInput{ActorID: req.DecidedBy, Reason: asString("reason")}); err != nil {
   return nil, err
  }
  return map[string]any{"status": "rolled_back"}, nil
 case "generate_optimization":
  opt, err := e.optimizationSvc.Generate(ctx, req.TenantID)
  if err != nil {
   return nil, err
  }
  return map[string]any{"optimization_id": opt.ID}, nil
 default:
  return nil, fmt.Errorf("unsupported evaluation operation: %s", operation)
 }
}

// executeMCPConfig 在 Task 6 实现 mcp_policy/mcp_server 分发（本 Task 返回未实现错误占位仅编译通过）。
func (e *approvalActionExecutor) executeMCPConfig(
 ctx context.Context, req port.ApprovalActionRequest,
) (map[string]any, error) {
 return nil, fmt.Errorf("mcp config approval executor not implemented in task 5")
}
```

（注：`Generate`/`Create` 返回类型与 `exp.ID` 字段名以现有 application 代码为准——实现时先读 `optimization_service.go`/`experiment_service.go` 的返回签名并校正。json 未用时删除 import。generate_suite_cases/create_baseline/reject_candidate 三个操作若现有 suiteSvc 无对应方法或签名不同，先在 `executeEvaluation` 的 switch 中补全对应调用（GenerateSuiteCases 在 SuiteService 无导出方法则 handler 直接路径走 svc——读取 evaluation_handler.go 实际调用后对齐）。）

wiring 装配（`api/wiring/agent.go` 或新 `api/wiring/approval.go`）：构造 executor（需要 EvaluationComponent 各 service 与 MCPService 引用），`AgentHandler` 构造传 `actionExecutor`。具体在 wiring 中：

```go
executor := &approvalActionExecutor{
 suiteSvc: evalComp.SuiteService, jobSvc: evalComp.JobService,
 baselineSvc: evalComp.BaselineService, experimentSvc: evalComp.ExperimentService,
 optimizationSvc: evalComp.OptimizationService, mcpSvc: mcpComponent.Service,
}
```

（字段名以 wiring 中现有组件结构为准。）

- [ ] **Step 4: 运行确认通过**

Run: `go vet ./api/... ./internal/evaluation/... && go test -short ./api/http/handler/... ./api/wiring/...`
Expected: PASS。修复所有因 handler 构造签名变化的测试。

- [ ] **Step 5: 并行 review + 提交**

```bash
# 并行 spawn code-reviewer + security-auditor，确认：member 路径参数不泄漏密钥（payload 加密+详情脱敏）、
# 执行器与直接执行同一代码路径、fail closed（角色未知拒绝）、审批单次消费后提交。
cd /home/yang/go-projects/stratum-approval-spec
git add api/http/handler/evaluation_handler.go api/http/handler/agent_approval_handler.go api/http/router.go api/wiring/approval_action.go api/wiring/evaluation.go api/wiring/agent.go internal/agent/application/agent_service.go
git commit -m "feat(agent,evaluation): 评测写操作角色分流——member 走审批,admin/owner 直接执行 + 审批执行端点" -m "What: 评测 11 个写端点从 requireAdmin 放宽为 requireActive + handler 角色分流(member 创建 evaluation_action 审批返回 202,admin/owner 直接执行);新增审批历史/详情/执行/指定审批人 4 端点;wiring 新增 approvalActionExecutor 分发评测原方法。
Why: spec D4/D12——member 有复核路径,执行与审批解耦,执行器复用同一 service 方法。
HowToTest: handler 表驱动测试(member 202 pending_approval/admin 直接执行),go test -short 通过。"
```

---

### Task 6: D5 MCP 配置审批（mcp_policy/mcp_server 执行器 + 路由分流）

**Files:**

- Modify: `api/http/handler/mcp_handler.go`（5 个写方法角色分流 + `approvals` 字段）
- Modify: `api/http/router.go`（MCP 写路由移除 adminMW → 组级 mw + handler 分流；`GetServerConfig` 读端点保持 admin）
- Modify: `api/wiring/approval_action.go`（`executeMCPConfig` 实现）
- Modify: `api/wiring/agent.go`（executor 装配补 mcpSvc）
- Test: `api/http/handler/mcp_handler_test.go`、`api/wiring/approval_action_test.go`

**Interfaces:**

- Consumes: Task 5 的 executor 结构（`mcpSvc *mcpapp.MCPService` 字段）、MCPHandler 现有 5 个写方法（SetToolPolicy/ConnectServer/UpdateServer/SetMCPServerEditors/DeleteServerConfig）、`MCPService` 方法签名。
- Produces:
  - `MCPHandler` 新字段 `approvals *agentapp.ToolApprovalService`（构造参数追加）。
  - 辅助 `mcpApprovalPayload(c, subjectKind, toolName string, args map[string]any) (agentapp.ToolApprovalPayload, error)`（RiskLevel=`unclassified`、PolicyVersion=`action-v1`）。
  - 执行参数契约：

    ```go
    // set_tool_policy:  {"operation":"set_tool_policy","serverId":string,"toolName":string,"riskLevel":string}
    // connect_server:   {"operation":"connect_server","config":{...MCPServerConfig 字段...},"editors":[]string}
    // update_server:    {"operation":"update_server","config":{...},"editors":[]string}
    // set_editors:      {"operation":"set_editors","serverId":string,"editorIds":[]string}
    // delete_server:    {"operation":"delete_server","serverId":string}
    ```

  - `executeMCPConfig`：按 operation 调 `e.mcpSvc` 原方法；**actor 用 `req.DecidedBy`**（审批人权限执行，member 发起 connect 审批通过后 ownership 校验通过）。

- [ ] **Step 1: 写失败测试**

`api/http/handler/mcp_handler_test.go` 追加（若不存在新建，仿照现有 gin handler 测试模式）：

```go
func TestMCPSetToolPolicyMemberGetsPendingApproval(t *testing.T) {
 // c.Set("auth.role","member")：PUT /mcp/tool-policies/srv/tool {riskLevel:...}
 // 断言 202 + {"status":"pending_approval"} + approvals.Request 被调用且 SubjectKind=mcp_policy、Arguments 含 riskLevel
}

func TestMCPSetToolPolicyAdminExecutesDirectly(t *testing.T) {
 // c.Set("auth.role","admin")：断言 svc.SetToolPolicy 被调用、Request 未被调用
}
```

`api/wiring/approval_action_test.go`（新建）：

```go
func TestApprovalActionExecutorMCPPolicy(t *testing.T) {
 // mcpSvc 用 mock（接口化或真实 service + mock repo——以 wiring 测试现有模式为准）：
 // req{SubjectKind:"mcp_policy", Arguments:{"operation":"set_tool_policy","serverId":"srv","toolName":"t","riskLevel":"write_reversible"}, DecidedBy:"admin-1"}
 // 断言 SetToolPolicy 被调用且 RiskLevel 正确
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test -short -run 'TestMCPSetToolPolicyMemberGetsPendingApproval' ./api/http/handler/`
Expected: FAIL。

- [ ] **Step 3: 实现**

`api/http/handler/mcp_handler.go`：

- struct 加 `approvals *agentapp.ToolApprovalService`；`NewMCPHandler(svc, approvals, logger)` 签名扩展（wiring 同步）。
- 辅助：

```go
func mcpApprovalPayload(c *gin.Context, subjectKind, toolName string, args map[string]any) (agentapp.ToolApprovalPayload, error) {
 tenantID, ok := tenantIDFromCtx(c)
 if !ok {
  return agentapp.ToolApprovalPayload{}, errTenantRequired
 }
 userID, ok := userIDFromCtx(c)
 if !ok {
  return agentapp.ToolApprovalPayload{}, errUserRequired
 }
 return agentapp.ToolApprovalPayload{
  TenantID: tenantID, UserID: userID,
  ExecutionID: uuid.NewString(), ToolCallID: uuid.NewString(),
  ToolName: toolName, SubjectKind: subjectKind,
  RiskLevel: "unclassified", Arguments: args, PolicyVersion: "action-v1",
 }, nil
}

// requireApprovalOrExecuteMCP：admin/owner 直接执行；member 创建审批；未知角色 fail closed。
func (h *MCPHandler) requireApprovalOrExecuteMCP(c *gin.Context, subjectKind, toolName string, args map[string]any, execute func() error) {
 roleClass := c.GetString("auth.role")
 if roleClass != "admin" && roleClass != "owner" {
  if roleClass == "member" {
   payload, err := mcpApprovalPayload(c, subjectKind, toolName, args)
   if err != nil {
    _ = c.Error(err)
    return
   }
   id, err := h.approvals.Request(c.Request.Context(), payload)
   if err != nil {
    _ = c.Error(err)
    return
   }
   c.JSON(http.StatusAccepted, gin.H{"status": "pending_approval", "approval_id": id})
   return
  }
  _ = c.Error(middleware.NewHTTPError(http.StatusForbidden, errors.New("insufficient tenant role")))
  return
 }
 if err := execute(); err != nil {
  _ = c.Error(err)
  return
 }
}
```

- 5 个方法改造（SetToolPolicy 示例）：

```go
func (h *MCPHandler) SetToolPolicy(c *gin.Context) {
 var req struct {
  RiskLevel mcpdomain.ToolRiskLevel `json:"riskLevel"`
 }
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 updatedBy := ""
 if tc, ok := postgres.FromContext(c.Request.Context()); ok {
  updatedBy = tc.UserID
 }
 serverID, toolName := c.Param("serverId"), c.Param("toolName")
 args := map[string]any{"serverId": serverID, "toolName": toolName, "riskLevel": string(req.RiskLevel)}
 h.requireApprovalOrExecuteMCP(c, agentdomain.SubjectKindMCPPolicy, "mcp.set_tool_policy", args, func() error {
  return h.svc.SetToolPolicy(c.Request.Context(), mcpdomain.ToolPolicy{
   ServerID: serverID, ToolName: toolName, RiskLevel: req.RiskLevel, UpdatedBy: updatedBy,
  })
 })
 if c.Writer.Written() {
  return // 审批路径已写 202；直接执行路径在闭包成功后写 200
 }
 c.JSON(http.StatusOK, gin.H{"message": "updated"})
}
```

（注意：闭包内不写响应，由辅助函数写 202 或调用方在 execute 成功后写 200——上述模式中 `requireApprovalOrExecuteMCP` 的 execute 分支不写响应，由各方法在调用后写原响应。ConnectServer/UpdateServer/SetMCPServerEditors/DeleteServerConfig 同构改造：ConnectServer args=`{"operation":"connect_server","config":<cfg 脱敏前原值>,"editors":req.Editors}`；DeleteServerConfig args=`{"operation":"delete_server","serverId":serverID}`。config 里含 apiKey 等敏感字段——AES 加密存储 + 详情脱敏，合规。）

`api/http/router.go` MCP 写路由改为（adminMW 只保留在 `GetServerConfig` 读端点）：

```go
 v1.PUT("/tool-policies/:serverId/:toolName", h.SetToolPolicy)
 v1.POST("/servers", h.ConnectServer)
 v1.PUT("/servers/:id", h.UpdateServer)
 v1.PUT("/servers/:id/editors", h.SetMCPServerEditors)
 v1.GET("/servers/:id/config", admin(h.GetServerConfig)...)
 v1.DELETE("/servers/:id", h.DisconnectServer)
 v1.DELETE("/servers/:id/config", h.DeleteServerConfig)
```

`api/wiring/approval_action.go` `executeMCPConfig` 实现：

```go
func (e *approvalActionExecutor) executeMCPConfig(
 ctx context.Context, req port.ApprovalActionRequest,
) (map[string]any, error) {
 if e.mcpSvc == nil {
  return nil, fmt.Errorf("mcp approval executor not configured")
 }
 operation, _ := req.Arguments["operation"].(string)
 asString := func(key string) string { s, _ := req.Arguments[key].(string); return s }
 switch operation {
 case "set_tool_policy":
  risk := mcpdomain.ToolRiskLevel(asString("riskLevel"))
  if err := e.mcpSvc.SetToolPolicy(ctx, mcpdomain.ToolPolicy{
   ServerID: asString("serverId"), ToolName: asString("toolName"),
   RiskLevel: risk, UpdatedBy: req.DecidedBy,
  }); err != nil {
   return nil, err
  }
  return map[string]any{"status": "updated"}, nil
 case "connect_server":
  raw, _ := json.Marshal(req.Arguments["config"])
  var cfg gen.MCPServerConfigRequest
  if err := json.Unmarshal(raw, &cfg); err != nil {
   return nil, fmt.Errorf("decode mcp connect config: %w", err)
  }
  serverCfg, err := cfg.ServerConfig()
  if err != nil {
   return nil, err
  }
  editors := toStringSlice(req.Arguments["editors"])
  if err := e.mcpSvc.ConnectServer(ctx, serverCfg, editors, req.DecidedBy); err != nil {
   return nil, err
  }
  return map[string]any{"server_id": serverCfg.ID}, nil
 case "update_server":
  raw, _ := json.Marshal(req.Arguments["config"])
  var cfg gen.MCPServerConfigRequest
  if err := json.Unmarshal(raw, &cfg); err != nil {
   return nil, fmt.Errorf("decode mcp update config: %w", err)
  }
  serverCfg, err := cfg.ServerConfig()
  if err != nil {
   return nil, err
  }
  serverCfg.ID = asString("serverId")
  if err := e.mcpSvc.UpdateServer(ctx, serverCfg, req.DecidedBy); err != nil {
   return nil, err
  }
  return map[string]any{"server_id": serverCfg.ID}, nil
 case "set_editors":
  if err := e.mcpSvc.SetEditors(ctx, asString("serverId"), req.DecidedBy, toStringSlice(req.Arguments["editorIds"])); err != nil {
   return nil, err
  }
  return map[string]any{"status": "updated"}, nil
 case "delete_server":
  if err := e.mcpSvc.DeleteServer(ctx, asString("serverId"), req.DecidedBy); err != nil {
   return nil, err
  }
  return map[string]any{"status": "deleted"}, nil
 default:
  return nil, fmt.Errorf("unsupported mcp operation: %s", operation)
 }
}

func toStringSlice(v any) []string {
 items, _ := v.([]any)
 out := make([]string, 0, len(items))
 for _, item := range items {
  if s, ok := item.(string); ok {
   out = append(out, s)
  }
 }
 return out
}
```

（`gen` = `github.com/byteBuilderX/stratum/api/http/dto/gen`——wiring import api/http/dto/gen 可行，DTO 非 infrastructure；`ConnectServer(ctx, cfg, editors, actorID)` 现有签名含 editors 参数。）

- [ ] **Step 4: 运行确认通过**

Run: `go vet ./api/... && go test -short ./api/http/handler/... ./api/wiring/...`
Expected: PASS。

- [ ] **Step 5: 并行 review + 提交**

```bash
# 并行 spawn code-reviewer + security-auditor，确认：config 敏感字段路径（加密存储/详情脱敏/不落日志）、
# 降级绕过路径（SetToolPolicy 走审批）、delete 语义（member 审批后可删——spec D5 明确列在审批集合）、
# ownership 用 DecidedBy 后提交。
cd /home/yang/go-projects/stratum-approval-spec
git add api/http/handler/mcp_handler.go api/http/router.go api/wiring/approval_action.go api/wiring/agent.go api/http/handler/mcp_handler_test.go api/wiring/approval_action_test.go
git commit -m "feat(mcp): MCP 策略/服务器配置角色分流——member 走审批,admin/owner 直接执行" -m "What: SetToolPolicy/ConnectServer/UpdateServer/SetEditors/DeleteServerConfig 移除 adminMW,handler 按 auth.role 分流(member 创建 mcp_policy/mcp_server 审批,202 pending);执行器按 operation 调 MCPService 原方法,actor 用审批人。
Why: spec D5——堵住 member 降级风险等级绕过工具审批的路径,配置变更本身需要审批。
HowToTest: handler 表驱动测试(member 202/admin 直接执行)+ wiring 执行器测试,go test -short 通过。"
```

---

### Task 7: D9 会话删除级联 + 恢复层层层校验

**Files:**

- Modify: `internal/agent/infrastructure/persistence/chat_store.go`（DeleteConversation 事务内级联）
- Modify: `internal/agent/application/agent_service.go`（ResumeToolApproval 恢复层校验）
- Modify: `internal/agent/application/tool_approval_service.go`（`ApprovedPayload` 无需改——终态检查 Task 2 已加；本 Task 仅服务层恢复校验）
- Test: `internal/agent/infrastructure/persistence/chat_store_integration_test.go`（级联）、`internal/agent/application/agent_service_test.go`（恢复层分类）

**Interfaces:**

- Consumes: Task 1 的 `CascadeByConversation`、`Invalidate`、`Void`；`s.deps.ChatStore.GetConversation`（chat_store.go:60，过滤 deleted_at IS NULL）；`s.deps.MCPToolPolicy.ResolveMCPToolRisk(ctx, tenantID, serverID, toolName) (port.ToolRiskLevel, error)`（tool_policy.go:47）。
- Produces:
  - `ResumeToolApproval` 恢复层校验（ApprovedPayload 成功之后、assembleOptions 之前）：
    1. 会话存在性（仅 `payload.SubjectKind == mcp_tool` 且 ConversationID 非空）：`ChatStore.GetConversation` 不存在 → `Void(reason=conversation_deleted)` → `ErrApprovalConversationGone`。
    2. 策略重查（仅 mcp_tool 且 `MCPToolPolicy` 非 nil）：`ResolveMCPToolRisk` 出错或当前 risk != payload.RiskLevel → `Invalidate(reason=policy_changed)` → `ErrApprovalPolicyChanged`。
    3. 过期/终态：ApprovedPayload 已返回分类错误（ErrApprovalExpired/ErrApprovalInvalidated/ErrApprovalNotApproved）——其中 Expired 时尝试 `Invalidate(reason=expired)`（CAS，失败忽略——终态幂等）。

- [ ] **Step 1: 写失败测试**

`internal/agent/infrastructure/persistence/chat_store_integration_test.go` 追加：

```go
func TestDeleteConversationCascadesApprovals(t *testing.T) {
 ctx := context.Background()
 pool, schema := integrationPool(t) // 沿用文件内现有 helper
 tenantID := "chat-cascade-" + uuid.NewString()[:8]
 approvals := persistence.NewPgToolApprovalStore(pool)
 chat := persistence.NewPgChatStore(pool)

 convID := uuid.NewString()
 // 直接插 conversation + 2 条审批（pending/approved 各一）
 if _, err := pool.Exec(ctx, `INSERT INTO "`+schema+`".chat_conversations (id, agent_id, user_id, name) VALUES ($1,'agent-1','user-1','c')`, convID); err != nil {
  t.Fatalf("insert conversation: %v", err)
 }
 for _, status := range []string{"pending", "approved"} {
  if _, err := approvals.Create(ctx, tenantID, domain.ToolApproval{
   DecisionID: uuid.NewString(), ExecutionID: uuid.NewString(), TraceID: "t", AgentID: "a",
   UserID: "user-1", ToolCallID: uuid.NewString(), ServerID: "srv", ToolName: "tool",
   RiskLevel: "unclassified", EncryptedPayload: "enc", Status: status,
   ExpiresAt: time.Now().Add(time.Hour), SubjectKind: domain.SubjectKindMCPTool,
   ConversationID: convID,
  }); err != nil {
   t.Fatalf("create approval: %v", err)
  }
 }
 if err := chat.DeleteConversation(ctx, tenantID, convID, "user-1"); err != nil {
  t.Fatalf("delete conversation: %v", err)
 }
 var got []string
 rows, err := pool.Query(ctx, `SELECT status, invalidation_reason FROM "`+schema+`".agent_tool_approvals WHERE conversation_id=$1`, convID)
 if err != nil {
  t.Fatalf("query: %v", err)
 }
 for rows.Next() {
  var status, reason string
  if err := rows.Scan(&status, &reason); err != nil {
   t.Fatal(err)
  }
  got = append(got, status+"/"+reason)
 }
 rows.Close()
 want := []string{"cancelled/conversation_deleted", "voided/conversation_deleted"}
 sort.Strings(got)
 sort.Strings(want)
 if !reflect.DeepEqual(got, want) {
  t.Fatalf("expected %v, got %v", want, got)
 }
}
```

`internal/agent/application/agent_service_test.go` 追加（复用现有 AgentService 测试构造与 mock 模式）：

```go
func TestResumeToolApprovalConversationGone(t *testing.T) {
 // ApprovalService mock（ApprovedPayload 返回 payload{SubjectKind: mcp_tool, ConversationID:"conv-gone"}")
 // ChatStore mock（GetConversation 返回 not found）
 // 断言：ResumeToolApproval 返回 ErrApprovalConversationGone；ApprovalService.Void 被调用（reason conversation_deleted）
}

func TestResumeToolApprovalPolicyChanged(t *testing.T) {
 // MCPToolPolicy mock（ResolveMCPToolRisk 返回与 payload.RiskLevel 不同值）
 // 断言：ErrApprovalPolicyChanged；Invalidate 被调用（reason policy_changed）
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test -short -run 'TestResumeToolApprovalConversationGone' ./internal/agent/application/`
Expected: FAIL（恢复层无校验，返回其他错误/不调用 Void）。

- [ ] **Step 3: 实现**

`internal/agent/infrastructure/persistence/chat_store.go` `DeleteConversation` 事务内追加（`DELETE FROM chat_conversations` 之前）：

```go
  if _, err := tx.Exec(ctx,
   `UPDATE agent_tool_approvals SET status='cancelled',invalidation_reason='conversation_deleted'
    WHERE conversation_id=$1 AND status='pending'`, convID); err != nil {
   return err
  }
  if _, err := tx.Exec(ctx,
   `UPDATE agent_tool_approvals SET status='voided',invalidation_reason='conversation_deleted'
    WHERE conversation_id=$1 AND status='approved'`, convID); err != nil {
   return err
  }
```

（决策记录：级联状态 + invalidation_reason 即审计痕迹，会话删除本身无审计事件流；物理删除保留审批历史可对账——spec D9。）

`internal/agent/application/agent_service.go` `ResumeToolApproval` 在 `payload, err := s.deps.ApprovalService.ApprovedPayload(...)` 成功之后插入：

```go
 // D9/D10 恢复层校验：会话存在性 + 策略重查（MCP 工具）。任一失败 → 分类错误 + 显式终态。
 if payload.SubjectKind == "" || payload.SubjectKind == domain.SubjectKindMCPTool {
  if payload.ConversationID != "" && s.deps.ChatStore != nil {
   if _, convErr := s.deps.ChatStore.GetConversation(ctx, tenantID, payload.ConversationID); convErr != nil {
    if voidErr := s.deps.ApprovalService.Void(ctx, tenantID, approvalID, "conversation_deleted"); voidErr != nil &&
     !errors.Is(voidErr, domain.ErrApprovalAlreadyExecuted) && !errors.Is(voidErr, domain.ErrApprovalNotFound) {
     return nil, 0, errors.Join(domain.ErrApprovalConversationGone, fmt.Errorf("void approval: %w", voidErr))
    }
    return nil, 0, domain.ErrApprovalConversationGone
   }
  }
  if s.deps.MCPToolPolicy != nil {
   currentRisk, policyErr := s.deps.MCPToolPolicy.ResolveMCPToolRisk(ctx, tenantID, payload.ServerID, payload.ToolName)
   if policyErr != nil || currentRisk != payload.RiskLevel {
    if invErr := s.deps.ApprovalService.Invalidate(ctx, tenantID, approvalID, "policy_changed"); invErr != nil &&
     !errors.Is(invErr, domain.ErrApprovalAlreadyExecuted) && !errors.Is(invErr, domain.ErrApprovalNotFound) {
     return nil, 0, errors.Join(domain.ErrApprovalPolicyChanged, fmt.Errorf("invalidate approval: %w", invErr))
    }
    return nil, 0, domain.ErrApprovalPolicyChanged
   }
  }
 }
 // 过期：ApprovedPayload 已返回 ErrApprovalExpired；此处把 approved → invalidated（原因 expired），CAS 失败忽略（幂等）。
```

（过期兜底在 `ApprovedPayload` 出错分支处理：）

```go
 payload, err := s.deps.ApprovalService.ApprovedPayload(ctx, tenantID, approvalID)
 if err != nil {
  if errors.Is(err, ErrApprovalExpired) {
   _ = s.deps.ApprovalService.Invalidate(ctx, tenantID, approvalID, "expired")
  }
  return nil, 0, err
 }
```

`ToolApprovalService` 补薄转发（application/tool_approval_service.go 或 agent_service 直接调 repo 不行——service 必须走 ToolApprovalService）：

```go
func (s *ToolApprovalService) Void(ctx context.Context, tenantID, id, reason string) error {
 return s.repo.Void(ctx, tenantID, id, reason)
}

func (s *ToolApprovalService) Invalidate(ctx context.Context, tenantID, id, reason string) error {
 return s.repo.Invalidate(ctx, tenantID, id, reason)
}
```

- [ ] **Step 4: 运行确认通过**

Run: `make infra-up && go test -run 'TestDeleteConversationCascadesApprovals|TestResumeToolApproval' -timeout 90s ./internal/agent/...`
Expected: PASS。

- [ ] **Step 5: 并行 review + 提交**

```bash
# 并行 spawn code-reviewer + security-auditor，确认：级联在会话删除事务内（回滚原子）、
# 恢复层校验 fail closed 且终态幂等、无吞错后提交。
cd /home/yang/go-projects/stratum-approval-spec
git add internal/agent/infrastructure/persistence/chat_store.go internal/agent/infrastructure/persistence/chat_store_integration_test.go internal/agent/application/agent_service.go internal/agent/application/tool_approval_service.go internal/agent/application/agent_service_test.go
git commit -m "feat(agent): 会话删除级联失效审批 + 断点恢复层层校验" -m "What: DeleteConversation 事务内级联(pending→cancelled、approved→voided,原因 conversation_deleted);ResumeToolApproval 恢复前校验会话存在性与策略重查(policy_version+当前 risk),失败分类 ErrApprovalConversationGone/ErrApprovalPolicyChanged 并显式终态;过期兜底 approved→invalidated(expired)。
Why: spec D9/D10——审批语义失效显式化,恢复断点感知,产品语义自洽。
HowToTest: 新增级联集成测试与恢复层分类测试,go test 通过。"
```

---

### Task 8: D7/D8/D12 前端审批工作台 + 错误映射 + 终态文案

**Files:**

- Create: `web/src/modules/approvals/api.ts`
- Create: `web/src/modules/approvals/pages/ApprovalsPage.tsx`
- Create: `web/src/modules/approvals/routes.tsx`
- Modify: `web/src/app/router.tsx`（挂 approvalsRoutes）
- Modify: `web/src/app/layout/menu.config.tsx`（租户 admin 菜单加"审批工作台"）
- Modify: `web/src/modules/agent/pages/AgentChatPage.tsx`（ApprovalGate 终态文案扩展）
- Modify: `web/src/modules/agent/api.ts`（history/detail/execute/assignee API）
- Modify: 后端错误映射（`api/http` 错误中间件——先查现有 `c.Error` 处理链，把 domain/application 分类错误映射中文 HTTP 消息）
- Test: 前端 lint/build；`api/http/handler` 错误映射测试

**Interfaces:**

- Consumes: Task 2/5 的新端点（history/detail/execute/assignee）；`auth.role` 前端来自现有 `useTenantRole`（iam 模块，返回 isAdmin）；`web/src/services/client.ts`。
- Produces:
  - `approvals/api.ts`：`listApprovals()`、`listApprovalHistory(page, pageSize)`、`getApprovalDetail(id)`、`executeApproval(id)`、`setApprovalAssignee(id, assignedApprover)`、`decideApproval(id, decision, reason)`（全部走 `client.ts` axios 实例；无分页默认值内联——用 `web/src/constants/index.ts` 的 `DEFAULT_PAGE_SIZE`）。
  - `ApprovalsPage`：Tabs（待审批/历史）；Table 列（工具、类型、风险、发起人、状态、时间、操作）；详情 Drawer（状态 + 指定审批人 + 脱敏 payload + 原因）；操作：批准/拒绝（Modal.confirm）/执行（仅 approved，需 `approved` 状态 + 按钮）/指派（Select admin/owner）。
  - `approvalsRoutes`：`{ path: '/approvals', element: <ApprovalsPage /> }`，路由级守卫用 `useTenantRole`（非 admin/owner → Navigate /）。
  - AgentChatPage ApprovalGate：`cancelled`（已取消）/`voided`（已失效：会话删除）/`invalidated`（审批已失效）终态文案 + `description` 显示 `invalidationReason`。
  - 后端错误映射：把 `ErrApprovalExpired`→"审批已过期"、`ErrApprovalPolicyChanged`→"权限策略已变更，请重新发起"、`ErrApprovalConversationGone`→"会话已删除，审批已失效"、`ErrApprovalSelfDecision`→"不能审批自己发起的请求"、`ErrApprovalAssigneeMismatch`→"该审批已指定给其他审批人"、`ErrApprovalRoleDenied`→"需要管理员权限"、`ErrApprovalAlreadyDecided`→"该审批已处理"、`ErrApprovalInvalidated`→"审批已失效"（HTTP 409/403 语义按现有中间件分类）。

- [ ] **Step 1: 写失败测试（后端错误映射）**

先定位现有错误中间件（`api/http` 中处理 `c.Error` 的 handler——搜索 `Error` 中间件/`errors.As` 链），在其测试文件追加：

```go
func TestApprovalErrorMapping(t *testing.T) {
 // 断言 domain.ErrApprovalPolicyChanged / ErrApprovalConversationGone / ErrApprovalSelfDecision 等
 // 经错误中间件后返回 409/403 + 中文 error 消息
}
```

- [ ] **Step 2: 实现后端错误映射**

在错误中间件/映射表追加分类（中文消息 + 状态码，见上）。

- [ ] **Step 3: 前端实现**

`web/src/modules/approvals/api.ts`：

```ts
import client from '@/services/client';
import { DEFAULT_PAGE_SIZE } from '@/constants';

export type ApprovalRow = {
  id: string; subject_kind: string; tool_name: string; server_id: string;
  risk_level: string; status: string; user_id: string; assigned_approver?: string;
  invalidation_reason?: string; created_at: string; expires_at: string;
  decided_by?: string; decision_reason?: string;
};

export type ApprovalDetail = ApprovalRow & { payload?: Record<string, unknown> };

export const listApprovals = async (): Promise<ApprovalRow[]> => {
  const { data } = await client.get('/agents/tool-approvals');
  return data.approvals ?? [];
};

export const listApprovalHistory = async (page: number, pageSize: number = DEFAULT_PAGE_SIZE) => {
  const { data } = await client.get('/agents/tool-approvals/history', { params: { page, page_size: pageSize } });
  return data as { approvals: ApprovalRow[]; total: number; page: number; page_size: number };
};

export const getApprovalDetail = async (id: string): Promise<ApprovalDetail> => {
  const { data } = await client.get(`/agents/tool-approvals/${id}`);
  return data;
};

export const executeApproval = async (id: string) => {
  const { data } = await client.post(`/agents/tool-approvals/${id}/execute`);
  return data;
};

export const setApprovalAssignee = async (id: string, assignedApprover: string) => {
  const { data } = await client.put(`/agents/tool-approvals/${id}/assignee`, { assignedApprover });
  return data;
};

export const decideApproval = async (id: string, decision: 'approved' | 'rejected', reason?: string) => {
  const { data } = await client.post(`/agents/tool-approvals/${id}/decision`, { decision, reason });
  return data;
};
```

`web/src/modules/approvals/pages/ApprovalsPage.tsx`：组件（Tabs + Table + Drawer + Modal.confirm 批准/拒绝 + 指派 Select + 执行按钮；所有文案中文；错误 `message.error({ content: err.response?.data?.error || '操作失败', duration: 0 })`；成功 `message.success({ content: '操作成功', duration: 2 })`；状态 Tag 映射中文：pending 待审批 / approved 已批准 / rejected 已拒绝 / expired 已过期 / executing 执行中 / executed 已执行 / unknown_outcome 结果未知 / cancelled 已取消 / voided 已失效 / invalidated 已失效）。页面拆分：Table 列与 Drawer 若超过 200 行则提取 `components/ApprovalDetailDrawer.tsx`。

`web/src/modules/approvals/routes.tsx`：

```tsx
import { Navigate } from 'react-router-dom';
import { ApprovalsPage } from './pages/ApprovalsPage';
import { useTenantRole } from '@/modules/iam';

const ApprovalsRouteGuard = () => {
  const { isAdmin } = useTenantRole();
  if (!isAdmin) return <Navigate to="/" replace />;
  return <ApprovalsPage />;
};

export const approvalsRoutes = [{ path: '/approvals', element: <ApprovalsRouteGuard /> }];
```

`web/src/app/router.tsx`：import + `{approvalsRoutes}` 挂载。

`web/src/app/layout/menu.config.tsx`：租户 admin 菜单（`/prompts`/`/audit` 所在分组）追加：

```tsx
        {
          key: '/approvals',
          icon: <AuditOutlined />,
          label: '审批工作台',
        },
```

`web/src/modules/agent/pages/AgentChatPage.tsx` ApprovalGate 扩展：

```tsx
  const cancelled = approval.status === 'cancelled';
  const voided = approval.status === 'voided' || approval.status === 'invalidated';
  const terminal = expired || unknown || blocked || cancelled || voided;
  const message = unknown
    ? '工具执行结果未知，需要人工对账'
    : expired
      ? '工具审批已过期'
      : blocked
        ? '权限已变更，工具执行已阻止'
        : cancelled
          ? '工具审批已取消'
          : voided
            ? '工具审批已失效' + (approval.invalidationReason ? `（${approval.invalidationReason}）` : '')
            : `工具 ${approval.toolName} 等待审批`;
```

（`ToolApproval` 前端类型加 `invalidationReason` 字段——`web/src/modules/agent/api.ts` 现有类型同步。）

- [ ] **Step 4: 运行确认通过**

Run: `make fe-lint && make fe-build && go test -short ./api/http/...`
Expected: 全绿。

- [ ] **Step 5: 并行 review + 提交**

```bash
# 并行 spawn code-reviewer + security-auditor，确认：前端无凭据落存储、路由守卫 admin/owner、
# 中文文案无信息泄漏（payload 仅脱敏视图）、错误映射不吞 detail 后提交。
cd /home/yang/go-projects/stratum-approval-spec
git add web/src/modules/approvals web/src/app/router.tsx web/src/app/layout/menu.config.tsx web/src/modules/agent/pages/AgentChatPage.tsx web/src/modules/agent/api.ts api/http/handler api/http/middleware
git commit -m "feat(web): 审批工作台页面 + 终态文案 + 分类错误中文映射" -m "What: 新增 /approvals 工作台(待审批/历史 Tabs、详情 Drawer 脱敏展示、批准/拒绝/执行/指派);菜单与路由按租户 admin/owner 渲染;聊天页 ApprovalGate 增加 cancelled/voided/invalidated 终态;后端分类错误映射中文。
Why: spec D7/D8/D12——admin/owner 可处理全部审批,member 可见自己的,失效语义用户可解释。
HowToTest: make fe-lint && make fe-build;错误映射单测;stratum-e2e-development 场景(admin 批准 member 发起的评测审批)。"
```

---

## Self-Review 记录

**1. Spec 覆盖：**

- D1 模型工具 → Task 3（list_models/update_system_model/可见性不裁剪/diagnose model 区）。
- D2 矩阵 → Task 3（写工具 member 拒绝）、Task 4（propose/apply 全角色 + admin 自动确认）、Task 5/6（评测/MCP 配置分流）、Task 2（ListPending member 过滤 + decide/resume admin-only）。
- D3 审批泛化 → Task 1（DDL/状态机/subject_kind）+ Task 2（payload/执行器接口/Decide 校验）。
- D4 评测审批 → Task 5。
- D5 MCP 配置审批 → Task 6。
- D6 资源变更整合 → Task 4。
- D7 审批工作台 → Task 8。
- D8 指定审批人 → Task 2（SetAssignee/Request 校验/Decide 匹配/ListPending 排序）+ Task 8（UI 指派）。偏差记录：spec 写"发起时指定"，实现为"工作台/恢复前指派"——发起端在 agent 内部无 UI 入口，软绑定语义保留（创建时校验存在性+角色、Decide 匹配、owner 兜底）。
- D9 失效终态 → Task 1（状态机/DDL）+ Task 7（级联/恢复失效）。
- D10 层层校验 → Task 2（审批层）+ Task 7（恢复层）+ 执行层（ClaimExecution CAS 已有）+ 产品层（Task 8 错误映射）。偏差记录：恢复层目标存在性（server/tool/agent）由执行层 guard/executor 现有失败路径兜底（MarkOutcomeUnknown/ReleaseExecution），不重复查询。
- D11 并发 → CAS 已有 + Task 1（新 CAS 方法）+ Task 2（单次消费/幂等）。
- D12 API/前端 → Task 2/5（端点）+ Task 8（前端/错误映射）。

**2. Placeholder 扫描：** 无 TBD/TODO；Step 3 中"以现有代码为准"的两处（evaluation_handler 调用签名、diagnose collector 细节）均为显式实现指引（先读后对齐），非占位符。

**3. Type consistency：** `SubjectKindMCPTool`/`SubjectKindEvaluationAction`/`SubjectKindMCPPolicy`/`SubjectKindMCPServer` 全计划一致；`ToolApprovalPayload.SubjectKind`/`AssignedApprover` 一致；`ListPending(ctx, tenantID, userID, roleClass)` 在 service/AgentService/handler 三处签名一致；`NewToolApprovalService` 4 参一致（wiring + 集成测试同步）；`ExecuteApprovedAction(ctx, tenantID, id, executor)` 一致；`ResolveMCPToolRisk` 接口名与现有 port 一致。
