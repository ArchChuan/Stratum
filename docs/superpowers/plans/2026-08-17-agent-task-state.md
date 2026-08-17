# Agent 持久化 Task 状态（跨会话推进同一目标）实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 单个 agent 的目标可跨会话持续推进——task 状态持久化到 `agent_tasks`，新会话恢复活跃 task 并从 next_action 继续。

**Architecture:** 目标级独立表 `agent_tasks`（owner = tenant+agent+user，与 conversation 解耦）。执行结束从内存 `finalState.ActivePlan` 映射 TaskSnapshot 提取写库（挂点不在 checkpoint，因为 runtime_state 不编码 Plan）；新会话经 `GetLatestActiveForOwner` + 语义相关判断注入 task 摘要 + continue 指令。并发防护复用 `workflow_runs`/`task_steps` 已验证的 `claimed_by + lease_expires_at + generation` 三层模式。完成信号由 LLM 调保留 tool `stratum_complete_task`。

**Tech Stack:** Go 1.25、pgx v5、Gin、React 18 + Ant Design 5。多租户 schema 隔离（`execTenantID` + `SET LOCAL search_path`）。

**设计文档:** `docs/superpowers/specs/2026-08-17-agent-task-state-design.md`（已批准 + review 修订）。

## Global Constraints

- tenant-only DDL 唯一基线是 `pkg/storage/postgres/tenant_schema.sql`；禁止复制到 `pkg/migration/sql/`；表/索引用 `IF NOT EXISTS`。
- 所有 tenant-scoped 表访问必须经 `execTenantID(ctx, pool, tenantID, fn)`；port 方法签名显式含 `tenantID string`。
- `domain/` 仅依赖 stdlib + `pkg/constants`；`application/` 不 import pgx/Redis/NATS/Gin；handler 不 import infrastructure。
- 行为数字禁止内联：跨包放 `pkg/constants/agent.go`（`Default*`/`Max*` 语义命名）。
- 写路径（task upsert/claim）失败旁路降级（不阻断已产出的响应），读路径（恢复）fail closed。
- 错误逐层 `fmt.Errorf("operation: %w", err)`；日志只用 Zap；禁止 `fmt.Print`。
- pgx v5 写 JSONB：先 `json.Marshal` 再传 `string(b)`；conversation_id UUID 空串写 NULL。
- 测试表驱动、mock 外部依赖；新函数圈复杂度 ≤10、长度 ≤120 行、嵌套 ≤4。
- 修改 port 后立即同步所有 test mock/stub。
- 提交前 `make risk-guardrails`；编码前 `bash scripts/quality/risk-regression-guard.sh --explain`。

---

### Task 1: tenant_schema.sql 新增 agent_tasks 表

**Files:**

- Modify: `pkg/storage/postgres/tenant_schema.sql`（在 `agent_execution_checkpoints` 索引后、`agent_tool_approvals` 前，约 774 行处插入）

**Interfaces:**

- Produces: `agent_tasks` 表 + 索引 `idx_agent_tasks_owner`、`idx_agent_tasks_status`。后续 Task 3 的 PgTaskRepo 依赖此 DDL。

- [ ] **Step 1: 定位插入点**

读 `pkg/storage/postgres/tenant_schema.sql`，确认 `idx_agent_execution_checkpoints_status`（约 769-770 行）与 `CREATE TABLE IF NOT EXISTS agent_tool_approvals`（约 776 行）之间为空行。

- [ ] **Step 2: 插入 DDL**

在 `idx_agent_execution_checkpoints_status` 索引后插入：

```sql
-- Agent tasks (T10): cross-session progress on a single goal.
-- owner = (agent_id, user_id); multiple active tasks per owner are allowed.
-- last_conversation_id is a soft reference: deleting a conversation detaches
-- the task (claimed_by='', lease_expires_at=NULL) without deleting it.
-- generation is a claim fence: every claim bumps it and saves carry the
-- generation they saw, so a stale conversation cannot overwrite a task
-- re-claimed by another conversation (mirrors workflow_runs.generation).
CREATE TABLE IF NOT EXISTS agent_tasks (
    id                   TEXT        PRIMARY KEY,
    agent_id             TEXT        NOT NULL,
    user_id              TEXT        NOT NULL,
    goal                 TEXT        NOT NULL DEFAULT '',
    current_phase        TEXT        NOT NULL DEFAULT '',
    completed_steps      JSONB       NOT NULL DEFAULT '[]',
    next_action          TEXT        NOT NULL DEFAULT '',
    status               TEXT        NOT NULL CHECK (status IN ('active','completed','abandoned')),
    claimed_by           TEXT        NOT NULL DEFAULT '',
    lease_expires_at     TIMESTAMPTZ,
    generation           BIGINT      NOT NULL DEFAULT 0,
    last_conversation_id UUID,
    last_execution_id    TEXT        NOT NULL DEFAULT '',
    fail_count           INT         NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at           TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '30 days'
);
CREATE INDEX IF NOT EXISTS idx_agent_tasks_owner
    ON agent_tasks (agent_id, user_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_tasks_status
    ON agent_tasks (status, expires_at);
```

- [ ] **Step 3: 验证 DDL 语法与幂等应用**

```bash
cd /home/yang/go-projects/stratum-agent-task-state
go test ./pkg/storage/postgres/... -run TestProvisionTenantSchema -count=1 2>&1 | tail -5
```

Expected: 集成测试通过（若 `STRATUM_TEST_POSTGRES_URL` 未设则 skip）。幂等：同一 schema 二次 `ProvisionTenantSchema` 不报错。

- [ ] **Step 4: Commit**

```bash
git add pkg/storage/postgres/tenant_schema.sql
git commit -m "feat(agent): add agent_tasks table for cross-session goal progress"
```

---

### Task 2: pkg/constants 新增 task 行为常量

**Files:**

- Modify: `pkg/constants/agent.go`（在 `AgentFactCheckJudgeMaxTokens` 后、`DefaultPlanMaxNodes` 前，约 74 行处插入）

**Interfaces:**

- Produces: 以下常量，Task 3/7/8/9/10/11 全部引用。

- [ ] **Step 1: 插入常量**

```go
 // ---- agent task persistence (cross-session goal progress) ----

 // TaskLeaseDuration 是 task 的 claim lease：推进一次刷新一次；无 heartbeat，
 // 会话崩溃后 30 分钟自动释放，新会话可接管（复用 workflow claim 模式）。
 TaskLeaseDuration = 30 * time.Minute
 // TaskExpiresAt 是 task 自身保留窗口：30 天未推进则 CleanupExpired 回收。
 TaskExpiresAt = 30 * 24 * time.Hour
 // TaskFailThreshold 是恢复提示阈值：fail_count 达到后注入"上次多次失败，
 // 是否继续"提示（不自动改状态）。
 TaskFailThreshold = 3
 // TaskCleanupInterval 是 TaskCleanupWorker 的清理周期。
 TaskCleanupInterval = 10 * time.Minute
 // TaskSemanticSimilarityThreshold 是恢复注入的语义相关阈值：新消息的
 // bigram 覆盖 goal bigram 的比例达到该值才注入（0.25 = 每 4 个 bigram 至少
 // 命中 1 个，中文 2 字词粒度）。
 TaskSemanticSimilarityThreshold = 0.25
 // TaskMetadataKey 是 AgentResult.Metadata 中 task snapshot 的透出键
 // （白名单：仅此键 + TaskMetadataCompleteKey 透出前端，禁止透出其他 Metadata）。
 TaskMetadataKey = "stratum_task_snapshot"
 // TaskMetadataCompleteKey 标记本次执行中 LLM 调用了 stratum_complete_task。
 TaskMetadataCompleteKey = "stratum_task_complete"
```

- [ ] **Step 2: 编译验证**

```bash
go build ./pkg/constants/...
```

Expected: 编译通过。

- [ ] **Step 3: Commit**

```bash
git add pkg/constants/agent.go
git commit -m "feat(agent): task persistence behavior constants"
```

---

### Task 3: domain 实体 + TaskSnapshot 映射纯函数

**Files:**

- Create: `internal/agent/domain/task.go`
- Test: `internal/agent/domain/task_test.go`

**Interfaces:**

- Consumes: `domain.Plan`、`domain.PlanNodeStatus`（已有，plan.go）、`constants.TaskExpiresAt`/`TaskLeaseDuration`（Task 2）。
- Produces: `TaskStatus`/`TaskStatusActive`/`TaskStatusCompleted`/`TaskStatusAbandoned`；`Task` 结构（含 `Generation`/`ClaimedBy`/`LeaseExpiresAt`）；`TaskSnapshot` 结构（JSON tag camelCase）；`BuildTaskSnapshot(plan *Plan, completeRequested bool) TaskSnapshot`；`(TaskSnapshot) ToTask(id, agentID, userID, conversationID, executionID string) Task`。Task 4 的 port 与 Task 7 的挂点引用这些类型。

- [ ] **Step 1: 写失败测试**

`internal/agent/domain/task_test.go`：

```go
package domain

import (
 "testing"
 "time"

 "github.com/byteBuilderX/stratum/pkg/constants"
)

func TestBuildTaskSnapshot(t *testing.T) {
 succeeded := PlanNode{ID: "n1", Goal: "迁移订单服务", Status: PlanNodeStatusSucceeded}
 pending := PlanNode{ID: "n2", Goal: "验证迁移", DependsOn: []string{"n1"}, Status: PlanNodeStatusPending}
 failed := PlanNode{ID: "n3", Goal: "压测", Status: PlanNodeStatusFailed}
 cases := []struct {
  name            string
  plan            *Plan
  completeRequest bool
  wantStatus      TaskStatus
  wantPhase       string
  wantNext        string
  wantCompleted   int
  wantFailures    int
 }{
  {
   name:          "in progress with next action",
   plan:          &Plan{ID: "p1", Status: PlanStatusActive, Nodes: []PlanNode{succeeded, pending}},
   wantStatus:    TaskStatusActive,
   wantPhase:     "1/2 完成",
   wantNext:      "验证迁移",
   wantCompleted: 1,
  },
  {
   name:          "all nodes succeeded",
   plan:          &Plan{ID: "p1", Status: PlanStatusActive, Nodes: []PlanNode{succeeded, {ID: "n2", Goal: "验证迁移", Status: PlanNodeStatusSucceeded}},
   wantStatus:    TaskStatusCompleted,
   wantPhase:     "2/2 完成",
   wantNext:      "",
   wantCompleted: 2,
  },
  {
   name:          "plan completed status",
   plan:          &Plan{ID: "p1", Status: PlanStatusCompleted, Nodes: []PlanNode{succeeded}},
   wantStatus:    TaskStatusCompleted,
   wantCompleted: 1,
  },
  {
   name:            "complete requested by tool",
   plan:            &Plan{ID: "p1", Status: PlanStatusActive, Nodes: []PlanNode{pending}},
   completeRequest: true,
   wantStatus:      TaskStatusCompleted,
   wantNext:        "",
  },
  {
   name:         "failed node counted and blocks next",
   plan:         &Plan{ID: "p1", Status: PlanStatusActive, Nodes: []PlanNode{succeeded, pending, failed}},
   wantStatus:   TaskStatusActive,
   wantPhase:    "1/3 完成",
   wantNext:     "验证迁移",
   wantFailures: 1,
  },
  {
   name:       "nil plan",
   plan:       nil,
   wantStatus: TaskStatusActive,
  },
  {
   name:       "empty nodes",
   plan:       &Plan{ID: "p1", Status: PlanStatusActive},
   wantStatus: TaskStatusActive,
  },
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   snapshot := BuildTaskSnapshot(tc.plan, tc.completeRequest)
   if snapshot.Status != tc.wantStatus {
    t.Fatalf("status: got %q want %q", snapshot.Status, tc.wantStatus)
   }
   if snapshot.CurrentPhase != tc.wantPhase {
    t.Fatalf("phase: got %q want %q", snapshot.CurrentPhase, tc.wantPhase)
   }
   if snapshot.NextAction != tc.wantNext {
    t.Fatalf("next: got %q want %q", snapshot.NextAction, tc.wantNext)
   }
   if len(snapshot.CompletedSteps) != tc.wantCompleted {
    t.Fatalf("completed: got %d want %d", len(snapshot.CompletedSteps), tc.wantCompleted)
   }
   if snapshot.Failures != tc.wantFailures {
    t.Fatalf("failures: got %d want %d", snapshot.Failures, tc.wantFailures)
   }
  })
 }
}

func TestTaskSnapshotToTask(t *testing.T) {
 snapshot := TaskSnapshot{
  Goal: "迁移订单服务", CurrentPhase: "1/2 完成",
  CompletedSteps: []string{"n1"}, NextAction: "验证迁移", Status: TaskStatusActive, Failures: 1,
 }
 task := snapshot.ToTask("p1", "agent-1", "user-1", "11111111-1111-1111-1111-111111111111", "exec-1")
 if task.ID != "p1" || task.AgentID != "agent-1" || task.UserID != "user-1" {
  t.Fatalf("identity mismatch: %+v", task)
 }
 if task.ClaimedBy != "11111111-1111-1111-1111-111111111111" || task.LastExecutionID != "exec-1" {
  t.Fatalf("reference mismatch: %+v", task)
 }
 if task.FailCount != 1 || task.Status != TaskStatusActive {
  t.Fatalf("snapshot fields not copied: %+v", task)
 }
 if task.Generation != 0 {
  t.Fatalf("new task generation should be 0, got %d", task.Generation)
 }
 if !task.LeaseExpiresAt.After(time.Now()) || !task.ExpiresAt.After(time.Now().Add(constants.TaskExpiresAt-time.Hour)) {
  t.Fatalf("lease/expiry not set: lease=%s expiry=%s", task.LeaseExpiresAt, task.ExpiresAt)
 }
}
```

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/agent/domain/ -run 'TestBuildTaskSnapshot|TestTaskSnapshotToTask' -count=1 2>&1 | tail -8
```

Expected: FAIL（`undefined: BuildTaskSnapshot`、`undefined: TaskSnapshot`）。

- [ ] **Step 3: 实现 domain/task.go**

```go
package domain

import (
 "fmt"
 "time"

 "github.com/byteBuilderX/stratum/pkg/constants"
)

// TaskStatus 是 task 生命周期三态：active 可推进，completed 用户/LLM 明确完成
// （停更），abandoned 保留进度但不再推荐恢复（仅用户显式操作进入）。
type TaskStatus string

const (
 TaskStatusActive    TaskStatus = "active"
 TaskStatusCompleted TaskStatus = "completed"
 TaskStatusAbandoned TaskStatus = "abandoned"
)

// Task 是"跨会话推进同一目标"的持久化实体。owner 是 (agent_id, user_id)，
// 允许同一 owner 多个活跃 task。并发防护三字段（claimed_by/lease_expires_at/
// generation）复用 workflow_runs 先例。last_conversation_id 是软引用，会话
// 删除只 detach 不级联。
type Task struct {
 ID                 string
 AgentID            string
 UserID             string
 Goal               string
 CurrentPhase       string
 CompletedSteps     []string
 NextAction         string
 Status             TaskStatus
 ClaimedBy          string
 LeaseExpiresAt     time.Time
 Generation         int64
 LastConversationID string
 LastExecutionID    string
 FailCount          int
 CreatedAt          time.Time
 UpdatedAt          time.Time
 ExpiresAt          time.Time
}

// TaskSnapshot 是 Plan → Task 的一次提取结果（执行结束、挂点处生成）。
// JSON 用 camelCase 透出前端任务摘要条。Failures 是本次 execution 新增失败
// 节点数（挂点累加到 task.FailCount）。
type TaskSnapshot struct {
 Goal           string     `json:"goal"`
 CurrentPhase   string     `json:"currentPhase"`
 CompletedSteps []string   `json:"completedSteps"`
 NextAction     string     `json:"nextAction"`
 Status         TaskStatus `json:"status"`
 Failures       int        `json:"failures,omitempty"`
}

// BuildTaskSnapshot 从 Plan 映射 task 内容。Plan 无顶层 Goal，goal 取首个
// 节点；current_phase 从节点状态分布推导；next_action 取首个依赖满足的
// pending 节点。completeRequested（LLM 调 stratum_complete_task）或全部节点
// 达成或 plan 已 completed → status=completed。
func BuildTaskSnapshot(plan *Plan, completeRequested bool) TaskSnapshot {
 snapshot := TaskSnapshot{Status: TaskStatusActive}
 if plan == nil {
  return snapshot
 }
 completed := 0
 failed := 0
 var completedSteps []string
 for _, node := range plan.Nodes {
  switch node.Status {
  case PlanNodeStatusSucceeded:
   completed++
   completedSteps = append(completedSteps, node.ID)
  case PlanNodeStatusFailed, PlanNodeStatusFailedPendingConfirmation:
   failed++
  }
 }
 if len(plan.Nodes) > 0 {
  snapshot.Goal = plan.Nodes[0].Goal
  snapshot.CurrentPhase = fmt.Sprintf("%d/%d 完成", completed, len(plan.Nodes))
 }
 snapshot.CompletedSteps = completedSteps
 snapshot.Failures = failed
 snapshot.NextAction = nextActionOf(plan)
 if plan.Status == PlanStatusCompleted ||
  (len(plan.Nodes) > 0 && completed == len(plan.Nodes)) ||
  completeRequested {
  snapshot.Status = TaskStatusCompleted
 }
 return snapshot
}

// nextActionOf 返回首个 pending 且全部依赖已成功（无依赖即就绪）的节点目标；
// 无就绪节点返回空串（恢复时不注入）。
func nextActionOf(plan *Plan) string {
 byID := make(map[string]PlanNodeStatus, len(plan.Nodes))
 for _, node := range plan.Nodes {
  byID[node.ID] = node.Status
 }
 for _, node := range plan.Nodes {
  if node.Status != PlanNodeStatusPending {
   continue
  }
  ready := true
  for _, dep := range node.DependsOn {
   if byID[dep] != PlanNodeStatusSucceeded {
    ready = false
    break
   }
  }
  if ready {
   return node.Goal
  }
 }
 return ""
}

// ToTask 由新建路径组装持久化 Task：id 复用 plan.ID（一个 plan 一个稳定 task
// id），generation 从 0 起（新建无并发），lease/expiry 按常量设定。
func (s TaskSnapshot) ToTask(id, agentID, userID, conversationID, executionID string) Task {
 now := time.Now()
 return Task{
  ID: id, AgentID: agentID, UserID: userID,
  Goal: s.Goal, CurrentPhase: s.CurrentPhase, CompletedSteps: s.CompletedSteps,
  NextAction: s.NextAction, Status: s.Status,
  ClaimedBy: conversationID, LeaseExpiresAt: now.Add(constants.TaskLeaseDuration),
  LastConversationID: conversationID, LastExecutionID: executionID,
  FailCount: s.Failures, Generation: 0,
  CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(constants.TaskExpiresAt),
 }
}
```

- [ ] **Step 4: 运行确认通过**

```bash
go test ./internal/agent/domain/ -run 'TestBuildTaskSnapshot|TestTaskSnapshotToTask' -count=1
```

Expected: PASS（`ok internal/agent/domain`）。

- [ ] **Step 5: Commit**

```bash
git add internal/agent/domain/task.go internal/agent/domain/task_test.go
git commit -m "feat(agent): task entity and plan-to-task snapshot mapping"
```

---

### Task 4: TaskRepo port 接口

**Files:**

- Modify: `internal/agent/domain/port/repository.go`（在 `CheckpointRepo` 后、`ToolApprovalRepo` 前，约 63 行处插入）

**Interfaces:**

- Consumes: `domain.Task`、`domain.TaskStatus`（Task 3）。
- Produces: `TaskRepo` 接口。Task 5 实现、Task 7/8/9/10 消费。方法签名显式含 `tenantID string`（tenant 路由强制）。

- [ ] **Step 1: 插入接口**

```go
// TaskRepo persists cross-session goal progress for agents. All methods touch
// tenant-scoped agent_tasks and take an explicit tenantID.
type TaskRepo interface {
 // Claim 原子抢占/续约 task：条件更新（status=active 且未过期 且 无主/本会话/lease 过期），
 // bump generation 作 fence，返回 claim 后的 task（含新 generation）与是否成功。
 // 无行或不可 claim（completed/abandoned/被活跃会话占用）→ (nil, false, nil)。
 Claim(ctx context.Context, tenantID, taskID, conversationID string, lease time.Duration) (*domain.Task, bool, error)
 // Save 新建或乐观锁写回：INSERT 新行（generation=task.Generation）；已存在行仅当
 // generation==expectedGeneration 时更新（claim bump 后 stale 写被拒），冲突返回
 // ErrGenerationConflict。
 Save(ctx context.Context, tenantID string, task domain.Task, expectedGeneration int64) error
 // Get 加载单个 task；不存在返回 nil。
 Get(ctx context.Context, tenantID, taskID string) (*domain.Task, error)
 // GetLatestActiveForOwner 返回该 owner 最新的活跃 task（updated_at DESC），
 // 无活跃 task 返回 nil。恢复入口。
 GetLatestActiveForOwner(ctx context.Context, tenantID, agentID, userID string) (*domain.Task, error)
 // DetachConversation 解除某会话的 task 引用（claimed_by='', lease 清空），
 // task 本身保留。conversation 删除时在 DeleteConversation 事务内调用。
 DetachConversation(ctx context.Context, tenantID, conversationID string) error
 // DeleteExpired 回收 expires_at 已过的 task，返回删除行数。
 DeleteExpired(ctx context.Context, tenantID string) (int64, error)
}
```

- [ ] **Step 2: 同步 ErrGenerationConflict 哨兵**

`internal/agent/domain/task.go` 加哨兵错误（放 `TaskStatus` 定义前）：

```go
var (
 // ErrGenerationConflict 表示 task 写回时 generation 不匹配（被另一会话
 // 接管后旧会话 stale 写），调用方应降级只读不重试。
 ErrGenerationConflict = errors.New("task generation conflict")
)
```

并在文件顶部加 `"errors"` import。

- [ ] **Step 3: 编译验证 + 检查 mock 引用**

```bash
go build ./internal/agent/domain/...
rg -rn "TaskRepo" internal/ --glob '*mock*' --glob '*_test.go' | head
```

Expected: 编译通过；无现有 mock 需同步（TaskRepo 是全新接口）。若 rg 命中旧引用则更新之。

- [ ] **Step 4: Commit**

```bash
git add internal/agent/domain/port/repository.go internal/agent/domain/task.go
git commit -m "feat(agent): task repository port with tenantID and generation fence"
```

---

### Task 5: PgTaskRepo 实现（infrastructure）

**Files:**

- Create: `internal/agent/infrastructure/persistence/task_store.go`
- Test: `internal/agent/infrastructure/persistence/task_store_integration_test.go`

**Interfaces:**

- Consumes: `port.TaskRepo`（Task 4）、`domain.Task`（Task 3）、`execTenantID`（同包已有）、`ErrGenerationConflict`。
- Produces: `PgTaskRepo` + `NewPgTaskRepo(pool chatPoolIface)`。Task 10 wiring 装配。

- [ ] **Step 1: 写失败集成测试**

`internal/agent/infrastructure/persistence/task_store_integration_test.go`（setup 仿 `checkpoint_store_integration_test.go`）：

```go
package persistence

import (
 "context"
 "errors"
 "fmt"
 "os"
 "testing"
 "time"

 "github.com/byteBuilderX/stratum/internal/agent/domain"
 "github.com/byteBuilderX/stratum/pkg/storage/postgres"
 "github.com/jackc/pgx/v5/pgxpool"
 "go.uber.org/zap"
)

func TestTaskLifecycleRealPostgres(t *testing.T) {
 url := os.Getenv("STRATUM_TEST_POSTGRES_URL")
 if url == "" {
  t.Skip("STRATUM_TEST_POSTGRES_URL is not set")
 }
 ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
 defer cancel()
 pool, err := pgxpool.New(ctx, url)
 if err != nil {
  t.Fatal(err)
 }
 defer pool.Close()
 if err := postgres.ProvisionPublicSchema(ctx, pool, zap.NewNop()); err != nil {
  t.Fatal(err)
 }
 tenantID := fmt.Sprintf("tmp_task_%d", time.Now().UnixNano())
 schema := "tenant_" + tenantID
 t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`) })
 if err := postgres.ProvisionTenantSchema(ctx, pool, tenantID); err != nil {
  t.Fatal(err)
 }
 repo := NewPgTaskRepo(pool)
 conv := "11111111-1111-1111-1111-111111111111"
 task := domain.Task{
  ID: "plan-1", AgentID: "agent-1", UserID: "user-1", Goal: "迁移订单服务",
  CurrentPhase: "1/2 完成", CompletedSteps: []string{"n1"}, NextAction: "验证迁移",
  Status: domain.TaskStatusActive, ClaimedBy: conv,
  LeaseExpiresAt: time.Now().Add(time.Hour), LastConversationID: conv,
  LastExecutionID: "exec-1", FailCount: 0, Generation: 0,
  CreatedAt: time.Now(), UpdatedAt: time.Now(), ExpiresAt: time.Now().Add(24 * time.Hour),
 }

 // 新建：Save expectedGeneration=0
 if err := repo.Save(ctx, tenantID, task, 0); err != nil {
  t.Fatalf("save new: %v", err)
 }

 // GetLatestActiveForOwner
 latest, err := repo.GetLatestActiveForOwner(ctx, tenantID, "agent-1", "user-1")
 if err != nil {
  t.Fatalf("get latest active: %v", err)
 }
 if latest == nil || latest.ID != "plan-1" || latest.NextAction != "验证迁移" {
  t.Fatalf("latest mismatch: %+v", latest)
 }

 // Claim 由另一会话接管 → generation bump
 otherConv := "22222222-2222-2222-2222-222222222222"
 claimed, ok, err := repo.Claim(ctx, tenantID, "plan-1", otherConv, 30*time.Minute)
 if err != nil || !ok {
  t.Fatalf("claim should succeed: ok=%v err=%v", ok, err)
 }
 if claimed.Generation != 1 || claimed.ClaimedBy != otherConv {
  t.Fatalf("claim bump failed: gen=%d claimed_by=%s", claimed.Generation, claimed.ClaimedBy)
 }

 // stale 写被拒：旧 generation=0 不再匹配
 stale := task
 stale.Generation = 0
 if err := repo.Save(ctx, tenantID, stale, 0); !errors.Is(err, domain.ErrGenerationConflict) {
  t.Fatalf("stale save should conflict, got %v", err)
 }

 // 新会话 Save 用 claim 后 generation 成功
 task.Generation = claimed.Generation
 task.ClaimedBy = otherConv
 task.NextAction = "完成"
 if err := repo.Save(ctx, tenantID, task, claimed.Generation); err != nil {
  t.Fatalf("save with fresh generation: %v", err)
 }

 // Claim 被活跃会话占用 → 失败（不接管活跃 lease）
 if _, ok, err := repo.Claim(ctx, tenantID, "plan-1", conv, 30*time.Minute); ok {
  t.Fatal("claim by idle conversation on live lease must fail")
 } else if err != nil {
  t.Fatalf("claim conflict err: %v", err)
 }

 // DetachConversation：会话删除解除引用，task 保留
 if err := repo.DetachConversation(ctx, tenantID, otherConv); err != nil {
  t.Fatalf("detach: %v", err)
 }
 after, err := repo.Get(ctx, tenantID, "plan-1")
 if err != nil || after == nil {
  t.Fatalf("get after detach: %v", err)
 }
 if after.ClaimedBy != "" || after.LeaseExpiresAt != (time.Time{}) {
  t.Fatalf("detach should clear claim: claimed_by=%q lease=%s", after.ClaimedBy, after.LeaseExpiresAt)
 }
 if after.Status != domain.TaskStatusActive {
  t.Fatalf("detach must keep task active, got %s", after.Status)
 }

 // Claim 过期接管：lease 过期后可被新会话接管
 if err := repo.Save(ctx, tenantID, after, after.Generation); err != nil {
  t.Fatalf("re-save detached: %v", err)
 }
 if _, err := pool.Exec(context.Background(),
  `UPDATE tenant_`+tenantID+`.agent_tasks SET lease_expires_at = NOW() - INTERVAL '1 minute' WHERE id='plan-1'`); err != nil {
  t.Fatal(err)
 }
 if _, ok, err := repo.Claim(ctx, tenantID, "plan-1", conv, 30*time.Minute); err != nil || !ok {
  t.Fatalf("claim expired lease should succeed: ok=%v err=%v", ok, err)
 }

 // DeleteExpired
 if err := repo.Save(ctx, tenantID, task, task.Generation); err != nil {
  t.Fatalf("pre-cleanup save: %v", err)
 }
 if _, err := pool.Exec(context.Background(),
  `UPDATE tenant_`+tenantID+`.agent_tasks SET expires_at = NOW() - INTERVAL '1 second' WHERE id='plan-1'`); err != nil {
  t.Fatal(err)
 }
 if deleted, err := repo.DeleteExpired(ctx, tenantID); err != nil || deleted < 1 {
  t.Fatalf("delete expired: deleted=%d err=%v", deleted, err)
 }
 if got, err := repo.Get(ctx, tenantID, "plan-1"); err != nil || got != nil {
  t.Fatalf("task should be reclaimed: got=%+v err=%v", got, err)
 }
}
```

- [ ] **Step 2: 运行确认失败**

```bash
STRATUM_TEST_POSTGRES_URL="postgres://...@localhost:5432/stratum?sslmode=disable" \
  go test ./internal/agent/infrastructure/persistence/ -run TestTaskLifecycleRealPostgres -count=1 2>&1 | tail -8
```

Expected: FAIL（`undefined: NewPgTaskRepo`）。若本地无测试库，跳过本步，直接实现后到 CI 验证（Step 4 仍必须可编译）。

- [ ] **Step 3: 实现 task_store.go**

```go
package persistence

import (
 "context"
 "encoding/json"
 "errors"
 "fmt"
 "time"

 "github.com/byteBuilderX/stratum/internal/agent/domain"
 "github.com/jackc/pgx/v5"
)

// PgTaskRepo persists agent_tasks with lease-based claim and generation fence,
// mirroring the workflow_runs / task_steps concurrency pattern.
type PgTaskRepo struct {
 pool chatPoolIface
}

// NewPgTaskRepo constructs a Postgres-backed TaskRepo.
func NewPgTaskRepo(pool chatPoolIface) *PgTaskRepo {
 return &PgTaskRepo{pool: pool}
}

const taskSelectColumns = `id, agent_id, user_id, goal, current_phase, completed_steps,
 next_action, status, claimed_by, lease_expires_at, generation,
 COALESCE(last_conversation_id::text, ''), last_execution_id, fail_count,
 created_at, updated_at, expires_at`

func scanTask(row pgx.Row) (*domain.Task, error) {
 var t domain.Task
 var completedSteps []byte
 var leaseExpiresAt *time.Time
 err := row.Scan(&t.ID, &t.AgentID, &t.UserID, &t.Goal, &t.CurrentPhase, &completedSteps,
  &t.NextAction, &t.Status, &t.ClaimedBy, &leaseExpiresAt, &t.Generation,
  &t.LastConversationID, &t.LastExecutionID, &t.FailCount,
  &t.CreatedAt, &t.UpdatedAt, &t.ExpiresAt)
 if err != nil {
  return nil, err
 }
 if leaseExpiresAt != nil {
  t.LeaseExpiresAt = *leaseExpiresAt
 }
 if err := json.Unmarshal(completedSteps, &t.CompletedSteps); err != nil {
  return nil, fmt.Errorf("task_store: decode completed_steps: %w", err)
 }
 return &t, nil
}

// Claim 原子抢占/续约并 bump generation 作 fence。条件：status=active、
// 未过期、且（本会话 / 无主 / lease 过期）。返回 claim 后 task（含新
// generation）与是否成功；无行或不可 claim → (nil, false, nil)。
func (r *PgTaskRepo) Claim(ctx context.Context, tenantID, taskID, conversationID string, lease time.Duration) (*domain.Task, bool, error) {
 var task *domain.Task
 err := execTenantID(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  row := tx.QueryRow(ctx,
   `UPDATE agent_tasks
       SET claimed_by = $2,
           lease_expires_at = NOW() + $3::interval,
           generation = generation + 1,
           updated_at = NOW()
     WHERE id = $1
       AND status = 'active' AND expires_at > NOW()
       AND (claimed_by = $2 OR claimed_by = '' OR lease_expires_at < NOW())
     RETURNING `+taskSelectColumns,
   taskID, conversationID, lease.String())
  claimed, err := scanTask(row)
  if errors.Is(err, pgx.ErrNoRows) {
   return nil
  }
  if err != nil {
   return err
  }
  task = claimed
  return nil
 })
 if err != nil {
  return nil, false, fmt.Errorf("task_store: claim: %w", err)
 }
 return task, task != nil, nil
}

// Save 新建或乐观锁写回。新行 INSERT（generation=task.Generation）；已存在行
// 仅当 generation==expectedGeneration 时更新，冲突返回 ErrGenerationConflict。
func (r *PgTaskRepo) Save(ctx context.Context, tenantID string, task domain.Task, expectedGeneration int64) error {
 steps, err := json.Marshal(task.CompletedSteps)
 if err != nil {
  return fmt.Errorf("task_store: encode completed_steps: %w", err)
 }
 var conversationID any
 if task.LastConversationID == "" {
  conversationID = nil
 } else {
  conversationID = task.LastConversationID
 }
 err = execTenantID(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  tag, err := tx.Exec(ctx,
   `INSERT INTO agent_tasks
       (id, agent_id, user_id, goal, current_phase, completed_steps, next_action,
        status, claimed_by, lease_expires_at, generation, last_conversation_id,
        last_execution_id, fail_count, created_at, updated_at, expires_at)
    VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
    ON CONFLICT (id) DO UPDATE SET
       goal = EXCLUDED.goal,
       current_phase = EXCLUDED.current_phase,
       completed_steps = EXCLUDED.completed_steps,
       next_action = EXCLUDED.next_action,
       status = EXCLUDED.status,
       claimed_by = EXCLUDED.claimed_by,
       lease_expires_at = EXCLUDED.lease_expires_at,
       last_conversation_id = EXCLUDED.last_conversation_id,
       last_execution_id = EXCLUDED.last_execution_id,
       fail_count = EXCLUDED.fail_count,
       updated_at = NOW(),
       expires_at = EXCLUDED.expires_at
    WHERE agent_tasks.generation = $18`,
   task.ID, task.AgentID, task.UserID, task.Goal, task.CurrentPhase, string(steps),
   task.NextAction, string(task.Status), task.ClaimedBy, task.LeaseExpiresAt,
   task.Generation, conversationID, task.LastExecutionID, task.FailCount,
   task.CreatedAt, task.UpdatedAt, task.ExpiresAt, expectedGeneration)
  if err != nil {
   return fmt.Errorf("task_store: save: %w", err)
  }
  if tag.RowsAffected() != 1 {
   return domain.ErrGenerationConflict
  }
  return nil
 })
 if err != nil {
  return err
 }
 return nil
}

// Get 加载单个 task；不存在返回 (nil, nil)。
func (r *PgTaskRepo) Get(ctx context.Context, tenantID, taskID string) (*domain.Task, error) {
 var task *domain.Task
 err := execTenantID(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  row := tx.QueryRow(ctx, `SELECT `+taskSelectColumns+` FROM agent_tasks WHERE id = $1`, taskID)
  loaded, err := scanTask(row)
  if errors.Is(err, pgx.ErrNoRows) {
   return nil
  }
  if err != nil {
   return err
  }
  task = loaded
  return nil
 })
 if err != nil {
  return nil, fmt.Errorf("task_store: get: %w", err)
 }
 return task, nil
}

// GetLatestActiveForOwner 返回该 owner 最新的活跃 task（updated_at DESC），
// 无则 (nil, nil)。恢复入口。
func (r *PgTaskRepo) GetLatestActiveForOwner(ctx context.Context, tenantID, agentID, userID string) (*domain.Task, error) {
 var task *domain.Task
 err := execTenantID(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  row := tx.QueryRow(ctx,
   `SELECT `+taskSelectColumns+`
      FROM agent_tasks
     WHERE agent_id = $1 AND user_id = $2 AND status = 'active'
     ORDER BY updated_at DESC
     LIMIT 1`, agentID, userID)
  loaded, err := scanTask(row)
  if errors.Is(err, pgx.ErrNoRows) {
   return nil
  }
  if err != nil {
   return err
  }
  task = loaded
  return nil
 })
 if err != nil {
  return nil, fmt.Errorf("task_store: get latest active: %w", err)
 }
 return task, nil
}

// DetachConversation 解除某会话的 task 引用：claimed_by 清空、lease 置空，
// task 本身保留 active。空 conversationID 必须拒绝（fail closed），否则
// last_conversation_id IS NULL 会误伤未关联会话的 task。
func (r *PgTaskRepo) DetachConversation(ctx context.Context, tenantID, conversationID string) error {
 if conversationID == "" {
  return domain.ErrTaskConversationGone
 }
 err := execTenantID(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  _, err := tx.Exec(ctx,
   `UPDATE agent_tasks
       SET claimed_by = '', lease_expires_at = NULL, updated_at = NOW()
     WHERE last_conversation_id = $1::uuid`, conversationID)
  if err != nil {
   return fmt.Errorf("task_store: detach conversation: %w", err)
  }
  return nil
 })
 if err != nil {
  return err
 }
 return nil
}

// DeleteExpired 回收 expires_at 已过的 task，返回删除行数。
func (r *PgTaskRepo) DeleteExpired(ctx context.Context, tenantID string) (int64, error) {
 var deleted int64
 err := execTenantID(ctx, r.pool, tenantID, func(ctx context.Context, tx pgx.Tx) error {
  tag, err := tx.Exec(ctx, `DELETE FROM agent_tasks WHERE expires_at < NOW()`)
  if err != nil {
   return fmt.Errorf("task_store: delete expired: %w", err)
  }
  deleted = tag.RowsAffected()
  return nil
 })
 if err != nil {
  return 0, err
 }
 return deleted, nil
}
```

在 `internal/agent/domain/task.go` 加哨兵：

```go
var (
 // ErrGenerationConflict 表示 task 写回时 generation 不匹配（被另一会话
 // 接管后旧会话 stale 写），调用方应降级只读不重试。
 ErrGenerationConflict = errors.New("task generation conflict")
 // ErrTaskConversationGone 表示 detach 目标会话已不存在（空 conversationID），
 // 防止批量子查询误伤无关 task。
 ErrTaskConversationGone = errors.New("task conversation gone")
)
```

确认 `execTenantID` 与 `chatPoolIface` 在同包已有（`tool_approval_store.go`/`chat_store.go` 使用），无需新增。

- [ ] **Step 4: 编译 + 运行通过**

```bash
go build ./internal/agent/infrastructure/persistence/...
STRATUM_TEST_POSTGRES_URL="postgres://...@localhost:5432/stratum?sslmode=disable" \
  go test ./internal/agent/infrastructure/persistence/ -run TestTaskLifecycleRealPostgres -count=1
```

Expected: 编译通过；集成测试 PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/agent/infrastructure/persistence/task_store.go \
       internal/agent/infrastructure/persistence/task_store_integration_test.go \
       internal/agent/domain/task.go
git commit -m "feat(agent): postgres task repo with claim/generation/detach/cleanup"
```

---

### Task 6: stratum_complete_task 保留 tool

**Files:**

- Modify: `internal/agent/application/graph/react_state.go`（`ReActState` 结构，约 155 行 `DegradeReason` 后加字段）
- Modify: `internal/agent/application/graph/plan_tools.go`（`PlanToolDefinitions` + `ExecutePlanTool` switch）
- Test: `internal/agent/application/graph/plan_tools_test.go`（加 case）

**Interfaces:**

- Consumes: `state *ReActState`、`port.ToolCall`。
- Produces: `ReActState.TaskCompleteRequested bool` 字段；`stratum_complete_task` 进入保留 tool 列表。Task 7 挂点读 `finalState.TaskCompleteRequested`。

- [ ] **Step 1: 写失败测试**

`internal/agent/application/graph/plan_tools_test.go`（追加，`package graph_test` + testify require 风格与现有文件一致）：

```go
func TestExecutePlanToolCompleteTask(t *testing.T) {
 state := graph.ReActState{ActivePlan: &domain.Plan{ID: "plan-1", Revision: 1, Status: domain.PlanStatusActive}}
 call := port.ToolCall{
  ID: "call-1", Name: "stratum_complete_task",
  Arguments: map[string]any{"expected_revision": int64(1)},
 }
 content, err := graph.ExecutePlanTool(context.Background(), &state, call)
 require.NoError(t, err)
 require.True(t, state.TaskCompleteRequested, "TaskCompleteRequested should be set")
 require.Contains(t, content, "stratum_complete_task")
}
```

注意：`stratum_complete_task` case 直接 return，不经 `PersistPlanCheckpoint`，所以 `state.PlanCheckpointWriter` 为 nil 也合法（无需构造 writer）。

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/agent/application/graph/ -run TestExecutePlanToolCompleteTask -count=1 2>&1 | tail -6
```

Expected: FAIL（`state.TaskCompleteRequested` 编译失败 / 行为不符）。

- [ ] **Step 3: 实现**

`react_state.go` 的 `ReActState` 加字段（`DegradeReason string` 后）：

```go
 // TaskCompleteRequested 标记 LLM 调用了 stratum_complete_task（目标达成）。
 // 执行结束时由挂点读入，task 状态转 completed。完成信号独立于 plan 状态。
 TaskCompleteRequested bool
```

`plan_tools.go` 的 `PlanToolDefinitions` 加（`stratum_cancel_plan` 后）：

```go
  {Name: "stratum_complete_task", Description: "Mark the current goal as fully achieved and stop task tracking.", InputSchema: planSchema()},
```

`ExecutePlanTool` 的 switch 加（`stratum_cancel_plan` case 后、`default` 前）：

```go
 case "stratum_complete_task":
  // 完成信号不修改 plan（ApplyPlanCommand 无此 command），仅记录状态；
  // expected_revision 为 planSchema 强制参数，此处忽略（独立于 plan 版本）。
  state.TaskCompleteRequested = true
  return planObservation("stratum_complete_task", state.ActivePlan), nil
```

注意：`state.ActivePlan` 可能为 nil（无 plan 时 LLM 不应调此 tool；但即使 nil，`planObservation` 已做 nil 保护）。当前 `planObservation` 直接解引用 `plan.Nodes`——若 nil 会 panic。先确认：

```bash
grep -n "func planObservation" -A 10 internal/agent/application/graph/plan_tools.go
```

若 `planObservation` 未 nil 保护，改为：

```go
func planObservation(toolName string, plan *domain.Plan) string {
 status := map[string]string{}
 planID, revision := "", int64(0)
 if plan != nil {
  planID, revision = plan.ID, plan.Revision
  for _, node := range plan.Nodes {
   status[node.ID] = string(node.Status)
  }
 }
 payload, _ := json.Marshal(map[string]any{"tool": toolName, "plan_id": planID, "revision": revision, "status": plan.Status, "nodes": status})
 return string(payload)
}
```

（若原实现已有 nil 保护则跳过此修改。）

- [ ] **Step 4: 运行确认通过**

```bash
go test ./internal/agent/application/graph/ -run 'TestExecutePlanToolCompleteTask|TestExecutePlanTool' -count=1
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/agent/application/graph/react_state.go internal/agent/application/graph/plan_tools.go \
       internal/agent/application/graph/plan_tools_test.go
git commit -m "feat(agent): stratum_complete_task reserved tool marks goal achieved"
```

---

### Task 7: BaseAgent.TaskStore 装配 + 提取挂点

**Files:**

- Modify: `internal/agent/application/agent.go`
  - `BaseAgent` 结构（约 171 行 `CheckpointStore` 后）加 `TaskStore` 字段
  - `SetTaskStore`/`WithTaskStore` 方法（`SetCheckpointStore` 后，约 233 行）
  - `executeReAct`（668 行 `collectGraphResult` 后）加 `persistTaskSnapshot` 调用
  - `executePlanning`（813 行 `collectGraphResult` 后）加 `persistTaskSnapshot` 调用
  - 新方法 `persistTaskSnapshot` + `applySnapshot`（`collectGraphResult` 后）
  - `collectGraphResult`（约 1005 行 `Degraded` 处理后）加 `TaskCompleteRequested → result.Metadata`
- Test: `internal/agent/application/task_extraction_test.go`（新建，mock TaskRepo）

**Interfaces:**

- Consumes: `port.TaskRepo`（Task 4）、`domain.BuildTaskSnapshot`/`TaskSnapshot`（Task 3）、`constants.Task*`（Task 2）、`finalState.TaskCompleteRequested`（Task 6）。
- Produces: `BaseAgent.TaskStore` 字段 + `SetTaskStore`/`WithTaskStore`；`persistTaskSnapshot` 写 `result.Metadata[constants.TaskMetadataKey]`。Task 8 复用 `TaskStore` 与 `TaskMetadataKey`。

- [ ] **Step 1: 写失败测试**

`internal/agent/application/task_extraction_test.go`（`package application` 同包测试——访问 unexported 的 `persistTaskSnapshot`/`agentExecContext`，与 `export_test.go` 同包策略一致）：

```go
package application

import (
 "context"
 "sync"
 "testing"
 "time"

 agentgraph "github.com/byteBuilderX/stratum/internal/agent/application/graph"
 "github.com/byteBuilderX/stratum/internal/agent/domain"
 "github.com/byteBuilderX/stratum/pkg/constants"
 "go.uber.org/zap"
)

// mockTaskRepo 记录调用并返回可控结果，供挂点测试。
type mockTaskRepo struct {
 mu            sync.Mutex
 claimCalls    int
 saveCalls     int
 claimedGen    int64
 claimedTask   *domain.Task
 claimOK       bool
 claimErr      error
 saveErr       error
 latestActive  *domain.Task
 latestErr     error
 detachCalls   int
 deleteExpired int64
}

func (m *mockTaskRepo) Claim(ctx context.Context, tenantID, taskID, conversationID string, lease time.Duration) (*domain.Task, bool, error) {
 m.mu.Lock()
 defer m.mu.Unlock()
 m.claimCalls++
 if m.claimErr != nil {
  return nil, false, m.claimErr
 }
 if !m.claimOK {
  return nil, false, nil
 }
 t := *m.claimedTask
 t.Generation = m.claimedGen
 return &t, true, nil
}

func (m *mockTaskRepo) Save(ctx context.Context, tenantID string, task domain.Task, expectedGeneration int64) error {
 m.mu.Lock()
 defer m.mu.Unlock()
 m.saveCalls++
 return m.saveErr
}

func (m *mockTaskRepo) Get(ctx context.Context, tenantID, taskID string) (*domain.Task, error) {
 return nil, nil
}

func (m *mockTaskRepo) GetLatestActiveForOwner(ctx context.Context, tenantID, agentID, userID string) (*domain.Task, error) {
 return m.latestActive, m.latestErr
}

func (m *mockTaskRepo) DetachConversation(ctx context.Context, tenantID, conversationID string) error {
 m.mu.Lock()
 defer m.mu.Unlock()
 m.detachCalls++
 return nil
}

func (m *mockTaskRepo) DeleteExpired(ctx context.Context, tenantID string) (int64, error) {
 return m.deleteExpired, nil
}

func TestPersistTaskSnapshotUpdatesClaimedTask(t *testing.T) {
 repo := &mockTaskRepo{
  claimOK: true, claimedGen: 3,
  claimedTask: &domain.Task{ID: "plan-1", AgentID: "agent-1", UserID: "user-1", Generation: 2},
 }
 agent := &BaseAgent{Logger: zap.NewNop(), TaskStore: repo}
 result := &AgentResult{Metadata: map[string]interface{}{}}
 plan := &domain.Plan{ID: "plan-1", Status: domain.PlanStatusActive,
  Nodes: []domain.PlanNode{{ID: "n1", Goal: "迁移", Status: domain.PlanNodeStatusSucceeded},
   {ID: "n2", Goal: "验证", Status: domain.PlanNodeStatusPending}}}
 state := reActStateForTest(plan, false)

 agent.persistTaskSnapshot(context.Background(), agentExecContextForTest(), state, result)

 repo.mu.Lock()
 defer repo.mu.Unlock()
 if repo.saveCalls != 1 {
  t.Fatalf("save calls: got %d want 1", repo.saveCalls)
 }
 if _, ok := result.Metadata[constants.TaskMetadataKey]; !ok {
  t.Fatal("task snapshot should be written to result.Metadata")
 }
}

func TestPersistTaskSnapshotCompleteFlag(t *testing.T) {
 repo := &mockTaskRepo{claimOK: true, claimedGen: 1,
  claimedTask: &domain.Task{ID: "plan-1", Generation: 0}}
 agent := &BaseAgent{Logger: zap.NewNop(), TaskStore: repo}
 result := &AgentResult{}
 plan := &domain.Plan{ID: "plan-1", Status: domain.PlanStatusActive,
  Nodes: []domain.PlanNode{{ID: "n1", Goal: "迁移", Status: domain.PlanNodeStatusPending}}}
 state := reActStateForTest(plan, true)

 agent.persistTaskSnapshot(context.Background(), agentExecContextForTest(), state, result)

 if got := result.Metadata[constants.TaskMetadataCompleteKey]; got != true {
  t.Fatalf("complete flag: got %v want true", got)
 }
}

func TestPersistTaskSnapshotNilStoreNoop(t *testing.T) {
 agent := &BaseAgent{Logger: zap.NewNop()} // TaskStore nil
 result := &AgentResult{}
 plan := &domain.Plan{ID: "plan-1", Status: domain.PlanStatusActive,
  Nodes: []domain.PlanNode{{ID: "n1", Goal: "迁移", Status: domain.PlanNodeStatusSucceeded}}}
 state := reActStateForTest(plan, false)
 agent.persistTaskSnapshot(context.Background(), agentExecContextForTest(), state, result) // 不 panic
}
```

注意：`reActStateForTest` 与 `agentExecContextForTest` 是两个测试辅助，定义在同一测试文件末尾：

```go
func reActStateForTest(plan *domain.Plan, complete bool) agentgraph.ReActState {
 return agentgraph.ReActState{ActivePlan: plan, TaskCompleteRequested: complete}
}

func agentExecContextForTest() agentExecContext {
 return agentExecContext{
  agentID: "agent-1",
  cfg:     &ExecutionConfig{TenantID: "tenant-1", UserID: "user-1", ExecutionID: "exec-1", ConversationID: "11111111-1111-1111-1111-111111111111"},
 }
}
```

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/agent/application/ -run TestPersistTaskSnapshot -count=1 2>&1 | tail -6
```

Expected: FAIL（`undefined: agent.TaskStore` / `undefined: persistTaskSnapshot`）。

- [ ] **Step 3: 实现**

`agent.go` `BaseAgent` 结构加字段：

```go
 ChatStore          ChatStore
 CheckpointStore    CheckpointStore
 TaskStore          port.TaskRepo
```

`SetCheckpointStore` 后加：

```go
func (a *BaseAgent) SetTaskStore(store port.TaskRepo) {
 a.mu.Lock()
 defer a.mu.Unlock()
 a.TaskStore = store
}

func (a *BaseAgent) WithTaskStore(store port.TaskRepo) *BaseAgent {
 a.SetTaskStore(store)
 return a
}
```

`collectGraphResult` 加（`finalState.TerminatedBy` 处理后，函数末尾）：

```go
 // 完成信号（stratum_complete_task）记录到 result.Metadata 供透出。
 if finalState.TaskCompleteRequested {
  if result.Metadata == nil {
   result.Metadata = map[string]interface{}{}
  }
  result.Metadata[constants.TaskMetadataCompleteKey] = true
 }
```

`executeReAct`（`collectGraphResult` 调用后）加：

```go
 a.persistTaskSnapshot(ctx, ec, finalState, result)
```

`executePlanning`（`collectGraphResult` 调用后）加：

```go
 a.persistTaskSnapshot(ctx, ec, finalState, result)
```

新方法（`collectGraphResult` 后、`firstStopLossReason` 前）：

```go
// persistTaskSnapshot 在 ReAct/Planning 循环结束后从内存 finalState.ActivePlan
// 提取 task 快照并写库。挂点必须读内存态：checkpoint 的 runtime_state 只编码
// ActiveSkills、不编码 Plan（buildReActRuntimeState），且被最后一步 AfterStep
// 覆盖。写路径旁路降级：任何失败不阻断已产出的响应（仿 MemoryBuffer 模式）。
func (a *BaseAgent) persistTaskSnapshot(ctx context.Context, ec agentExecContext, finalState agentgraph.ReActState, result *AgentResult) {
 if a.TaskStore == nil || finalState.ActivePlan == nil || finalState.ActivePlan.ID == "" {
  return
 }
 taskCtx, cancel := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
 defer cancel()
 snapshot := domain.BuildTaskSnapshot(finalState.ActivePlan, finalState.TaskCompleteRequested)

 claimed, ok, err := a.TaskStore.Claim(taskCtx, ec.cfg.TenantID, finalState.ActivePlan.ID, ec.cfg.ConversationID, constants.TaskLeaseDuration)
 if err != nil {
  a.Logger.Error("agent: task claim failed, degrade read-only",
   zap.String("agent_id", ec.agentID), zap.String("task_id", finalState.ActivePlan.ID), zap.Error(err))
  return
 }
 if ok {
  applySnapshot(claimed, snapshot, ec.cfg.ConversationID, ec.cfg.ExecutionID)
  if err := a.TaskStore.Save(taskCtx, ec.cfg.TenantID, *claimed, claimed.Generation); err != nil {
   a.Logger.Error("agent: task save failed, degrade",
    zap.String("agent_id", ec.agentID), zap.String("task_id", claimed.ID), zap.Error(err))
   return
  }
  a.attachTaskSnapshot(result, snapshot)
  return
 }
 // Claim 无行：task 不存在（新建）或不可 claim（completed/被占）。区分后决定。
 existing, getErr := a.TaskStore.Get(taskCtx, ec.cfg.TenantID, finalState.ActivePlan.ID)
 if getErr == nil && existing != nil {
  a.Logger.Warn("agent: task not claimable, degrade read-only",
   zap.String("task_id", existing.ID), zap.String("status", string(existing.Status)))
  return
 }
 newTask := snapshot.ToTask(finalState.ActivePlan.ID, ec.agentID, ec.cfg.UserID,
  ec.cfg.ConversationID, ec.cfg.ExecutionID)
 if err := a.TaskStore.Save(taskCtx, ec.cfg.TenantID, newTask, 0); err != nil {
  a.Logger.Error("agent: task create failed, degrade",
   zap.String("agent_id", ec.agentID), zap.String("task_id", newTask.ID), zap.Error(err))
  return
 }
 a.attachTaskSnapshot(result, snapshot)
}

// applySnapshot 用本次提取结果覆盖已 claim 的 task，并顺延 lease/expiry、
// 累加失败数。claim 已 bump generation，此写回带该 generation 作乐观锁。
func applySnapshot(t *domain.Task, s domain.TaskSnapshot, conversationID, executionID string) {
 now := time.Now()
 t.Goal = s.Goal
 t.CurrentPhase = s.CurrentPhase
 t.CompletedSteps = s.CompletedSteps
 t.NextAction = s.NextAction
 t.Status = s.Status
 t.ClaimedBy = conversationID
 t.LeaseExpiresAt = now.Add(constants.TaskLeaseDuration)
 t.LastConversationID = conversationID
 t.LastExecutionID = executionID
 t.FailCount += s.Failures
 t.ExpiresAt = now.Add(constants.TaskExpiresAt)
}

// attachTaskSnapshot 将 task 快照写入 result.Metadata 供 SSE done 透出（白名单
// key，见 handler）。已有 Metadata（如 complete 标志）保留。
func attachTaskSnapshot(result *AgentResult, snapshot domain.TaskSnapshot) {
 if result.Metadata == nil {
  result.Metadata = map[string]interface{}{}
 }
 result.Metadata[constants.TaskMetadataKey] = snapshot
}
```

确认 `agent.go` import 已有 `time`、`constants`、`port`、`agentgraph`、`zap`（Task 1-6 探索确认全有）。若无 `time` 加之。

- [ ] **Step 4: 运行确认通过**

```bash
go test ./internal/agent/application/ -run 'TestPersistTaskSnapshot|TestExecute' -count=1
```

Expected: PASS。现有 `TestExecute` 系列不受影响（TaskStore nil 时挂点 no-op）。

- [ ] **Step 5: Commit**

```bash
git add internal/agent/application/agent.go internal/agent/application/task_extraction_test.go
git commit -m "feat(agent): extract task snapshot on execution end with claim fence"
```

---

### Task 8: 恢复注入（新会话续推）

**Files:**

- Create: `internal/agent/application/task_resume.go`（`semanticallyRelated` 纯函数 + `maybeInjectTaskResume`）
- Modify: `internal/agent/application/agent.go`
  - `executeReAct`（`resumeFromCheckpoint` 后、`buildReActInitState` 前，约 604 行）加 `maybeInjectTaskResume`
  - `executePlanning`（`BuildContextMessagesWithCompaction` 后、`buildReActInitState` 前，约 776 行）加 `maybeInjectTaskResume`
- Test: `internal/agent/application/task_resume_test.go`

**Interfaces:**

- Consumes: `a.TaskStore.GetLatestActiveForOwner`（Task 4）、`domain.TaskStatus`/`domain.Task`（Task 3）、`constants.TaskFailThreshold`/`TaskSemanticSimilarityThreshold`（Task 2）。
- Produces: `maybeInjectTaskResume(ctx, ec, msgs) []port.LLMMessage`——注入一条 system 消息（task 摘要 + continue 指令）。无活跃/语义无关/checkpoint 已恢复时原样返回。

- [ ] **Step 1: 写失败测试**

`internal/agent/application/task_resume_test.go`（`package application` 同包测试，共享 Task 7 定义的 `mockTaskRepo`）：

```go
package application

import (
 "context"
 "testing"

 "github.com/byteBuilderX/stratum/internal/agent/domain"
 "github.com/byteBuilderX/stratum/internal/agent/domain/port"
 "github.com/byteBuilderX/stratum/pkg/constants"
 "go.uber.org/zap"
)

func TestSemanticallyRelated(t *testing.T) {
 cases := []struct {
  name       string
  goal, text string
  want       bool
 }{
  {"same goal restated", "迁移订单服务到新架构", "继续迁移订单服务", true},
  {"exact", "迁移订单服务", "迁移订单服务", true},
  {"unrelated topic", "迁移订单服务到新架构", "帮我写一首诗", false},
  {"empty text", "迁移订单服务", "", false},
  {"empty goal", "", "迁移订单服务", false},
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   if got := semanticallyRelated(tc.goal, tc.text); got != tc.want {
    t.Fatalf("semanticallyRelated(%q, %q) = %v, want %v", tc.goal, tc.text, got, tc.want)
   }
  })
 }
}

func TestMaybeInjectTaskResume(t *testing.T) {
 conv := "11111111-1111-1111-1111-111111111111"
 cases := []struct {
  name         string
  latest       *domain.Task
  latestErr    error
  input        string
  goal         string
  wantInjected bool
 }{
  {
   name: "active task semantically related injects",
   latest: &domain.Task{ID: "plan-1", AgentID: "agent-1", UserID: "user-1",
    Status: domain.TaskStatusActive, Goal: "迁移订单服务", NextAction: "验证迁移"},
   input:        "继续迁移订单服务",
   wantInjected: true,
  },
  {
   name: "unrelated message does not inject",
   latest: &domain.Task{ID: "plan-1", AgentID: "agent-1", UserID: "user-1",
    Status: domain.TaskStatusActive, Goal: "迁移订单服务", NextAction: "验证迁移"},
   input:        "帮我写一首诗",
   wantInjected: false,
  },
  {
   name: "no next action does not inject",
   latest: &domain.Task{ID: "plan-1", AgentID: "agent-1", UserID: "user-1",
    Status: domain.TaskStatusActive, Goal: "迁移订单服务", NextAction: ""},
   input:        "继续迁移订单服务",
   wantInjected: false,
  },
  {
   name: "completed task does not inject",
   latest: &domain.Task{ID: "plan-1", AgentID: "agent-1", UserID: "user-1",
    Status: domain.TaskStatusCompleted, Goal: "迁移订单服务", NextAction: "验证迁移"},
   input:        "继续迁移订单服务",
   wantInjected: false,
  },
  {
   name:         "no active task does not inject",
   latest:       nil,
   input:        "继续迁移订单服务",
   wantInjected: false,
  },
  {
   name: "read failure fails closed",
   latest: &domain.Task{ID: "plan-1", AgentID: "agent-1", UserID: "user-1",
    Status: domain.TaskStatusActive, Goal: "迁移订单服务", NextAction: "验证迁移"},
   latestErr:    context.DeadlineExceeded,
   input:        "继续迁移订单服务",
   wantInjected: false,
  },
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   repo := &mockTaskRepo{latestActive: tc.latest, latestErr: tc.latestErr}
   agent := &BaseAgent{Logger: zap.NewNop(), TaskStore: repo}
   ec := agentExecContext{agentID: "agent-1", cfg: &ExecutionConfig{
    TenantID: "tenant-1", UserID: "user-1", ConversationID: conv}}
   msgs := []port.LLMMessage{{Role: "user", Content: tc.input}}
   ec.input = tc.input
   out := agent.maybeInjectTaskResume(context.Background(), ec, msgs)
   if got := len(out) > len(msgs); got != tc.wantInjected {
    t.Fatalf("injected: got %v want %v (out len %d vs base %d)", got, tc.wantInjected, len(out), len(msgs))
   }
  })
 }
}
```

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/agent/application/ -run 'TestSemanticallyRelated|TestMaybeInjectTaskResume' -count=1 2>&1 | tail -6
```

Expected: FAIL（`undefined: semanticallyRelated` / `maybeInjectTaskResume`）。

- [ ] **Step 3: 实现 task_resume.go**

```go
package application

import (
 "context"
 "strings"
 "unicode"

 "github.com/byteBuilderX/stratum/internal/agent/domain"
 "github.com/byteBuilderX/stratum/internal/agent/domain/port"
 "github.com/byteBuilderX/stratum/pkg/constants"
 "go.uber.org/zap"
)

// maybeInjectTaskResume 在新会话执行入口判断是否有可恢复的活跃 task：
// 最新活跃 task 且 next_action 非空且新消息与 task.goal 语义相关 → 注入一条
// system 消息（task 摘要 + continue 指令），让 agent 从 next_action 继续而非
// 从零重述。读失败 fail closed（不注入，防止假装有进度）。调用方保证
// checkpoint plan 未恢复时才进入（executeReAct 仅在 activePlan==nil 时调用）。
func (a *BaseAgent) maybeInjectTaskResume(ctx context.Context, ec agentExecContext, msgs []port.LLMMessage) []port.LLMMessage {
 if a.TaskStore == nil || ec.cfg.UserID == "" {
  return msgs
 }
 taskCtx, cancel := context.WithTimeout(ctx, constants.AgentDBQueryTimeout)
 defer cancel()
 task, err := a.TaskStore.GetLatestActiveForOwner(taskCtx, ec.cfg.TenantID, ec.agentID, ec.cfg.UserID)
 if err != nil {
  a.Logger.Error("agent: task resume load failed, fail closed",
   zap.String("agent_id", ec.agentID), zap.Error(err))
  return msgs
 }
 if task == nil || task.Status != domain.TaskStatusActive || task.NextAction == "" {
  return msgs
 }
 if !semanticallyRelated(task.Goal, ec.input) {
  return msgs
 }
 content := taskResumePrompt(task)
 return append([]port.LLMMessage{{Role: "system", Content: content}}, msgs...)
}

// taskResumePrompt 构造 task 摘要 + continue 指令。fail_count 达到阈值时附加
// "上次多次失败，是否继续"提示（不自动改状态）。
func taskResumePrompt(task *domain.Task) string {
 var b strings.Builder
 b.WriteString("检测到未完成的目标，请基于以下进度继续推进（不要重新规划，执行下一步）：\n")
 b.WriteString("目标：").WriteString(task.Goal).WriteString("\n")
 b.WriteString("当前进度：").WriteString(task.CurrentPhase).WriteString("\n")
 if len(task.CompletedSteps) > 0 {
  b.WriteString("已完成步骤数：").WriteString(itoa(len(task.CompletedSteps))).WriteString("\n")
 }
 b.WriteString("下一步：").WriteString(task.NextAction).WriteString("\n")
 if task.FailCount >= constants.TaskFailThreshold {
  b.WriteString("注意：该目标上次推进多次失败，请评估是否继续并调整策略。\n")
 }
 return b.String()
}

// itoa 避免引入 strconv 之外的依赖（微基准无关紧要，清晰优先）。
func itoa(n int) string {
 if n == 0 {
  return "0"
 }
 var digits []byte
 for n > 0 {
  digits = append([]byte{byte('0' + n%10)}, digits...)
  n /= 10
 }
 return string(digits)
}

// semanticallyRelated 判断新消息是否与 task.goal 语义相关：取两者字符 bigram
// 集合，计算 input bigram 对 goal bigram 的覆盖率（命中数/goal 总数）。中文
// 无空格分词，bigram（2 字）粒度对中文与英文均有效，且为纯函数、可测。
func semanticallyRelated(goal, text string) bool {
 if goal == "" || text == "" {
  return false
 }
 goalN := taskNGrams(goal)
 textN := taskNGrams(text)
 if len(goalN) == 0 || len(textN) == 0 {
  return false
 }
 hit := 0
 for ngram := range textN {
  if _, ok := goalN[ngram]; ok {
   hit++
  }
 }
 return float64(hit)/float64(len(goalN)) >= constants.TaskSemanticSimilarityThreshold
}

// taskNGrams 取字符串的 2-gram 集合（小写、去非字母数字字符；短串整体作一个
// token，防止"迁移"与"迁移订单服务"因 bigram 太少而漏匹配）。
func taskNGrams(s string) map[string]struct{} {
 var runes []rune
 for _, r := range strings.ToLower(strings.TrimSpace(s)) {
  if unicode.IsLetter(r) || unicode.IsDigit(r) {
   runes = append(runes, r)
  }
 }
 out := make(map[string]struct{})
 if len(runes) < 2 {
  if len(runes) == 1 {
   out[string(runes)] = struct{}{}
  }
  return out
 }
 for i := 0; i+2 <= len(runes); i++ {
  out[string(runes[i:i+2])] = struct{}{}
 }
 return out
}
```

- [ ] **Step 4: agent.go 接线**

`executeReAct`：

```go
 activePlan, restoredActives, initMessages := a.resumeFromCheckpoint(
  ctx, ec, initMessages,
 )
 if activePlan == nil {
  // 未恢复完整 checkpoint plan 才注入 task 摘要：两级同命中以 plan 为准。
  initMessages = a.maybeInjectTaskResume(ctx, ec, initMessages)
 }
```

`executePlanning`：

```go
 initMessages := BuildContextMessagesWithCompaction(
  ctx, ec.systemPrompt, ec.memCtx, ec.history, ec.input, maxTokens, ec.cfg.HistoryWindow,
  ec.cfg.OutputReserve, float64(ec.cfg.CompactionSafetyRatio), ec.historyCompactor,
 )
 initMessages = a.maybeInjectTaskResume(ctx, ec, initMessages)
```

- [ ] **Step 5: 运行确认通过**

```bash
go test ./internal/agent/application/ -run 'TestSemanticallyRelated|TestMaybeInjectTaskResume|TestExecute' -count=1
```

Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/agent/application/task_resume.go internal/agent/application/agent.go \
       internal/agent/application/task_resume_test.go
git commit -m "feat(agent): inject task resume summary on semantically related new session"
```

---

### Task 9: conversation 删除 detach（生命周期解耦）

**Files:**

- Modify: `internal/agent/infrastructure/persistence/chat_store.go`
  - 结构加 `taskDetach port.TaskRepo` 字段（`approvals` 字段后）
  - `SetTaskDetach(repo port.TaskRepo)` 方法
  - `DeleteConversation`（约 172 行事务内、approvals 级联后）加 detach 调用
- Test: `internal/agent/infrastructure/persistence/chat_store_integration_test.go`（追加 case）或独立 task detach 断言并入 Task 5 测试

**Interfaces:**

- Consumes: `port.TaskRepo.DetachConversation`（Task 4）。
- Produces: `PgChatStore.SetTaskDetach`。Task 10 wiring 调用。

- [ ] **Step 1: 写失败测试**

在 Task 5 的 `task_store_integration_test.go` 追加：

```go
func TestTaskDetachOnConversationDelete(t *testing.T) {
 // setup 与 TestTaskLifecycleRealPostgres 相同（复制其 pool/tenant provision 段）
 ...
 chat := NewPgChatStore(pool, zap.NewNop())
 repo := NewPgTaskRepo(pool)
 chat.SetTaskDetach(repo)
 conv := "33333333-3333-3333-3333-333333333333"
 // 建 task（owner agent-2/user-2）并 claim 该会话
 if err := repo.Save(ctx, tenantID, domain.Task{
  ID: "plan-9", AgentID: "agent-2", UserID: "user-2", Goal: "重构", Status: domain.TaskStatusActive,
  ClaimedBy: conv, LeaseExpiresAt: time.Now().Add(time.Hour), LastConversationID: conv,
  Generation: 0, CreatedAt: time.Now(), UpdatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
 }, 0); err != nil {
  t.Fatal(err)
 }
 // 删除会话 → detach
 if err := chat.DeleteConversation(ctx, tenantID, conv, "user-2"); err != nil && !errors.Is(err, domain.ErrNotFound) {
  t.Fatalf("delete conversation: %v", err)
 }
 after, err := repo.Get(ctx, tenantID, "plan-9")
 if err != nil {
  t.Fatal(err)
 }
 if after == nil {
  t.Fatal("task must survive conversation delete")
 }
 if after.ClaimedBy != "" || after.LeaseExpiresAt != (time.Time{}) {
  t.Fatalf("task should be detached: claimed_by=%q lease=%s", after.ClaimedBy, after.LeaseExpiresAt)
 }
 if after.Status != domain.TaskStatusActive {
  t.Fatalf("task must stay active, got %s", after.Status)
 }
}
```

（`DeleteConversation` 需要会话行存在才返回成功；无会话行时返回 `ErrNotFound` 而 detach 已执行——断言只验证 task 被 detach、保留 active，兼容两种返回。）

- [ ] **Step 2: 运行确认失败**

```bash
STRATUM_TEST_POSTGRES_URL="postgres://...@localhost:5432/stratum?sslmode=disable" \
  go test ./internal/agent/infrastructure/persistence/ -run 'TestTaskDetachOnConversationDelete' -count=1 2>&1 | tail -6
```

Expected: FAIL（`undefined: SetTaskDetach`）。

- [ ] **Step 3: 实现**

`chat_store.go` 结构加字段 + 方法 + 事务内调用：

```go
type PgChatStore struct {
 pool      chatPoolIface
 logger    *zap.Logger
 approvals port.ToolApprovalRepo
 // taskDetach 在会话删除事务内解除 agent_tasks 引用（claimed_by 清空）。
 taskDetach port.TaskRepo
}

// SetTaskDetach 装配会话删除时的 task detach 级联。
func (s *PgChatStore) SetTaskDetach(repo port.TaskRepo) {
 s.taskDetach = repo
}
```

`DeleteConversation` 事务内、approvals 级联后加：

```go
  if s.approvals != nil {
   if err := s.approvals.CascadeByConversation(ctx, tenantID, convID); err != nil {
    return err
   }
  }
  // 生命周期解耦：会话删除只解除 task 引用，不级联删 task（跨会话推进语义）。
  if s.taskDetach != nil {
   if err := s.taskDetach.DetachConversation(ctx, tenantID, convID); err != nil {
    return err
   }
  }
```

确认 `chat_store.go` 已有 `port` import（`approvals port.ToolApprovalRepo` 字段存在则必有）。

- [ ] **Step 4: 编译 + 运行通过**

```bash
go build ./internal/agent/infrastructure/persistence/...
STRATUM_TEST_POSTGRES_URL="postgres://...@localhost:5432/stratum?sslmode=disable" \
  go test ./internal/agent/infrastructure/persistence/ -run 'TestTaskDetachOnConversationDelete' -count=1
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/agent/infrastructure/persistence/chat_store.go \
       internal/agent/infrastructure/persistence/task_store_integration_test.go
git commit -m "feat(agent): detach tasks on conversation delete, keep task alive"
```

---

### Task 10: TaskCleanupWorker + wiring 装配

**Files:**

- Create: `internal/agent/application/task_cleanup.go`（仿 `checkpoint_cleanup.go`）
- Modify: `api/wiring/agent.go`
  - 约 279 行（`NewPgCheckpointStore` 后）加 `a.TaskStore = persistence.NewPgTaskRepo(db)`
  - 约 283 行（`SetApprovalCascade` 后）加 `chatStore.SetTaskDetach(a.TaskStore)`
  - 约 293 行（`CheckpointCleanup.Start` 后）加 TaskCleanupWorker 注册
  - `shutdown` 追加 `a.TaskCleanup.Stop()`
- Test: `internal/agent/application/task_cleanup_test.go`

**Interfaces:**

- Consumes: `port.TaskRepo.DeleteExpired`（Task 4）、`constants.TaskCleanupInterval`（Task 2）。
- Produces: `TaskCleanupWorker` + wiring 完成全链路装配。

- [ ] **Step 1: 写失败测试**

`internal/agent/application/task_cleanup_test.go`：

```go
package application

import (
 "context"
 "testing"
 "time"

 "github.com/byteBuilderX/stratum/pkg/observability"
 "go.uber.org/zap"
)

func TestTaskCleanupWorkerDeletesExpired(t *testing.T) {
 repo := &mockTaskRepo{deleteExpired: 3}
 worker := NewTaskCleanupWorker(
  func(context.Context) ([]string, error) { return []string{"tenant-1"}, nil },
  repo, 10*time.Millisecond, zap.NewNop(), observability.NoopMetrics{},
 )
 ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
 defer cancel()
 worker.Start(ctx)
 select {
 case <-ctx.Done():
 case <-time.After(150 * time.Millisecond):
 }
 worker.Stop()
 repo.mu.Lock()
 defer repo.mu.Unlock()
 if repo.deleteExpired == 0 {
  t.Fatal("DeleteExpired should have been called")
 }
}
```

- [ ] **Step 2: 运行确认失败**

```bash
go test ./internal/agent/application/ -run TestTaskCleanupWorker -count=1 2>&1 | tail -6
```

Expected: FAIL（`undefined: NewTaskCleanupWorker`）。

- [ ] **Step 3: 实现 task_cleanup.go**

仿 `checkpoint_cleanup.go`，仅替换 repo 类型与日志前缀：

```go
package application

import (
 "context"
 "sync"
 "time"

 "github.com/byteBuilderX/stratum/internal/agent/domain/port"
 "github.com/byteBuilderX/stratum/pkg/observability"
 "github.com/google/uuid"
 "go.uber.org/zap"
)

// TaskCleanupWorker periodically reclaims expired agent_tasks across tenants.
// It follows the same ticker+goroutine pattern as CheckpointCleanupWorker;
// DeleteExpired is idempotent (expires_at filter), so no lease/lock layer.
type TaskCleanupWorker struct {
 tenants  func(ctx context.Context) ([]string, error)
 repo     port.TaskRepo
 interval time.Duration
 logger   *zap.Logger
 metrics  observability.MetricsProvider
 stopCh   chan struct{}
 stopOnce sync.Once
 wg       sync.WaitGroup
}

// NewTaskCleanupWorker creates a worker that calls repo.DeleteExpired for every
// tenant on each tick.
func NewTaskCleanupWorker(
 tenants func(ctx context.Context) ([]string, error),
 repo port.TaskRepo,
 interval time.Duration,
 logger *zap.Logger,
 metrics observability.MetricsProvider,
) *TaskCleanupWorker {
 return &TaskCleanupWorker{
  tenants:  tenants,
  repo:     repo,
  interval: interval,
  logger:   logger,
  metrics:  metrics,
  stopCh:   make(chan struct{}),
 }
}

// Start begins the cleanup loop in a background goroutine.
func (w *TaskCleanupWorker) Start(ctx context.Context) {
 w.wg.Add(1)
 go func() {
  defer w.wg.Done()
  ticker := time.NewTicker(w.interval)
  defer ticker.Stop()
  for {
   select {
   case <-ctx.Done():
    return
   case <-w.stopCh:
    return
   case <-ticker.C:
    w.cleanupExpired(ctx)
   }
  }
 }()
}

// Stop signals the worker to stop and waits for the current tick to finish.
func (w *TaskCleanupWorker) Stop() {
 w.stopOnce.Do(func() { close(w.stopCh) })
 w.wg.Wait()
}

func (w *TaskCleanupWorker) cleanupExpired(ctx context.Context) {
 timestamp := float64(time.Now().Unix())
 w.metrics.SetComponentCycleTimestamp("task-cleanup", timestamp)
 tenantIDs, err := w.tenants(ctx)
 if err != nil {
  w.logger.Error("task cleanup: list tenants failed", zap.Error(err))
  w.metrics.IncComponentError("task-cleanup", "list_tenants")
  return
 }
 logger := w.logger
 if logger == nil {
  logger = zap.NewNop()
 }
 workerID := uuid.Must(uuid.NewV7()).String()
 var total int64
 for _, tenantID := range tenantIDs {
  deleted, err := w.repo.DeleteExpired(ctx, tenantID)
  if err != nil {
   logger.Error("task cleanup: delete expired failed",
    zap.String("worker_id", workerID),
    zap.String("tenant_id", tenantID),
    zap.Error(err))
   w.metrics.IncComponentError("task-cleanup", "delete")
   continue
  }
  total += deleted
 }
 w.metrics.RecordComponentCycle("task-cleanup")
 if total > 0 {
  logger.Info("task cleanup: deleted expired rows",
   zap.String("worker_id", workerID),
   zap.Int64("total_deleted", total))
 }
}
```

- [ ] **Step 4: wiring 装配**

`api/wiring/agent.go`：

```go
 if db != nil {
  a.CheckpointStore = persistence.NewPgCheckpointStore(db)
  a.TaskStore = persistence.NewPgTaskRepo(db)
  a.ApprovalStore = persistence.NewPgToolApprovalStore(db)
  chatStore := persistence.NewPgChatStore(db, c.Logger)
  // D9 会话删除级联：DeleteConversation 在同一租户事务内终结关联审批。
  chatStore.SetApprovalCascade(a.ApprovalStore)
  // 生命周期解耦：会话删除只解除 task 引用，task 保留可恢复。
  chatStore.SetTaskDetach(a.TaskStore)
  a.ChatStore = chatStore
  ...
  a.CheckpointCleanup.Start(ctx)
  a.TaskCleanup = agent.NewTaskCleanupWorker(
   agentCheckpointTenantLister{pool: db}.list,
   a.TaskStore,
   10*time.Minute,
   c.Logger,
   c.platformMetrics(),
  )
  a.TaskCleanup.Start(ctx)
  c.shutdown = append(c.shutdown, func(context.Context) error {
   a.CheckpointCleanup.Stop()
   a.TaskCleanup.Stop()
   return nil
  })
```

确认 `Agent` 结构（wiring 包内）有 `TaskCleanup` 字段——`CheckpointCleanup` 已有同模式字段，`TaskCleanup` 需新增（与 `CheckpointCleanup` 并列声明）。查找 `CheckpointCleanup` 声明位置并对称添加：

```go
// 在 Agent 结构中找到 CheckpointCleanup 字段，其后加：
 TaskCleanup *agent.TaskCleanupWorker
```

`10*time.Minute` 应改 `constants.TaskCleanupInterval`（wiring 已 import constants 则直接用）。

- [ ] **Step 5: 编译 + 运行通过**

```bash
go build ./...
go test ./internal/agent/application/ -run TestTaskCleanupWorker -count=1
```

Expected: 编译通过；测试 PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/agent/application/task_cleanup.go internal/agent/application/task_cleanup_test.go api/wiring/agent.go
git commit -m "feat(agent): task cleanup worker and full wiring assembly"
```

---

### Task 11: SSE done 事件白名单透出 task snapshot

**Files:**

- Modify: `api/http/handler/agent_exec_handler.go`（约 176-178 行构造 `AgentExecutionResult`）

**Interfaces:**

- Consumes: `result.Metadata[constants.TaskMetadataKey]`（Task 7）、`constants.TaskMetadataKey`（Task 2）。
- Produces: SSE done 事件 `metadata.stratum_task_snapshot`。前端（Task 12）读取。

- [ ] **Step 1: 改透出**

```go
func agentExecutionResultDTO(result *agent.AgentResult) AgentExecutionResult {
 thoughtsJSON, _ := json.Marshal(result.Thoughts)
 toolCallsJSON, _ := json.Marshal(result.ToolCalls)
 artifacts := executionArtifactsResponse(result.Artifacts)
 metadata := map[string]interface{}{"thoughtsJSON": string(thoughtsJSON), "toolCallsJSON": string(toolCallsJSON)}
 // 白名单透出 task snapshot（跨会话目标进度摘要条）。禁止透出 result.Metadata
 // 其他键——仅应用层写入的 task 数据可流出。
 if v, ok := result.Metadata[constants.TaskMetadataKey]; ok {
  metadata[constants.TaskMetadataKey] = v
 }
 return AgentExecutionResult{AgentID: result.AgentID, Input: result.Input, Output: result.Output, Steps: result.Steps,
  TokensUsed: result.TokensUsed, Duration: result.Duration.String(), Thoughts: result.Thoughts, ToolCalls: result.ToolCalls,
  Artifacts: artifacts, Metadata: metadata}
}

// agentExecutionDonePayload 的 done 帧序列化 struct 必须补上 Metadata 字段——
// 当前只输出 Done/Output/.../FactCheck，缺 metadata 则上面 DTO 写的 task
// snapshot 不会到达前端。字段追加到 struct 末尾（位置初始化同步追加）。
// 对应 struct: Done/Output/Steps/TokensUsed/Duration/Artifacts/Sources/Degraded/
// DegradeReason/FactCheck 之后加:
//     Metadata map[string]interface{} `json:"metadata,omitempty"`
// 并在位置字面量末尾追加 dto.Metadata。
```

确认 `agent_exec_handler.go` import `constants`；若缺失加入 `"github.com/byteBuilderX/stratum/pkg/constants"`。

- [ ] **Step 2: 编译 + 契约测试**

```bash
go build ./api/...
go test ./api/http/ -run TestAgentExecutionContract -count=1 2>&1 | tail -5
```

Expected: 编译通过；契约测试不回归（`metadata` 字段本就存在，新增 key 不破坏 golden——若 golden 快照对比严格，检查并同步 `contracts/*.golden.json`）。

- [ ] **Step 3: Commit**

```bash
git add api/http/handler/agent_exec_handler.go
git commit -m "feat(agent): whitelist task snapshot into SSE done metadata"
```

---

### Task 12: 后端全量验证

**Files:** 无（验证）

- [ ] **Step 1: 静态 + 快速测试**

```bash
cd /home/yang/go-projects/stratum-agent-task-state
go vet ./...
go test -short ./... 2>&1 | tail -30
```

Expected: vet 无输出；`-short` 全绿。若集成测试（真库）未配 URL 则 skip，属预期。

- [ ] **Step 2: 代码质量棘轮**

```bash
make code-quality
```

Expected: 通过（新函数圈复杂度 ≤10 等；若超限按 CLAUDE.md 决策阶梯重构）。

- [ ] **Step 3: 风险守卫**

```bash
make risk-guardrails
```

Expected: 通过。

- [ ] **Step 4: Commit（若验证产生修改）**

```bash
git add -A
git commit -m "chore(agent): backend verification for task persistence" || echo "no changes"
```

---

### Task 13: 前端对话内任务摘要条

**Files:**

- Modify: `web/src/modules/agent/model/agent.ts`（`AgentExecutionResult` 加 `metadata`、`ChatMessage` 加 `taskSnapshot`、新增 `TaskSnapshot` 接口）
- Modify: `web/src/modules/agent/hooks/useChatPage.ts`（makeMessage 参数/返回加 `taskSnapshot`，done 帧提取）
- Create: `web/src/modules/agent/components/TaskProgressBanner.tsx`
- Modify: `web/src/modules/agent/components/ChatMessageList.tsx`（assistant 消息渲染 taskSnapshot 摘要条）
- Test: `web/src/modules/agent/components/__tests__/TaskProgressBanner.test.tsx`

**Interfaces:**

- Consumes: SSE done `metadata.stratum_task_snapshot`（Task 11）。
- Produces: 消息对象 `taskSnapshot` 字段 + `TaskProgressBanner` 组件。无新 API 端点（最小正确，任务摘要随对话出现）。

- [ ] **Step 1: 类型 + 提取**

`model/agent.ts` 三处：

1. `AgentExecutionResult`（SSE done 的 result 类型，ChatStreamContext 的 `st.result: AgentExecutionResult | null`）加 `metadata`——不是 `agent.api.ts`，SSE result 类型定义在此：

```ts
export interface AgentExecutionResult {
  output?: string;
  steps?: ChatStep[];
  artifacts?: ExecutionArtifact[];
  sources?: ChatCitationSource[];
  error?: string;
  metadata?: Record<string, unknown>;  // SSE done 白名单透出（thoughtsJSON/toolCallsJSON/stratum_task_snapshot）
  [key: string]: unknown;
}
```

1. 新增导出 `TaskSnapshot` 接口（放 `ChatMessage` 之前，model 风格与 Go JSON camelCase 对齐）：

```ts
/** 后端 TaskSnapshot 的 JSON 形态（camelCase 与 Go 对齐） */
export interface TaskSnapshot {
  goal: string;
  currentPhase: string;
  completedSteps: string[];
  nextAction: string;
  status: 'active' | 'completed' | 'abandoned';
  failures?: number;
}
```

1. `ChatMessage` 加可选字段：

```ts
  /** 跨会话目标进度摘要（stratum_task_snapshot 透出）；无则 undefined */
  taskSnapshot?: TaskSnapshot;
```

`useChatPage.ts` 的 makeMessage 参数类型加 `taskSnapshot`、返回对象透传（改参数类型必须同步返回对象，否则字段丢失）：

```ts
const makeMessage = (msg: {
  id: string;
  role: string;
  content: string;
  steps?: ChatMessage['steps'];
  artifacts?: ChatMessage['artifacts'];
  interrupted?: boolean;
  sources?: ChatMessage['sources'];
  taskSnapshot?: ChatMessage['taskSnapshot'];
}): ChatMessage => ({
  id: msg.id,
  role: msg.role,
  content: msg.content,
  created_at: new Date().toISOString(),
  steps: msg.steps,
  artifacts: msg.artifacts,
  interrupted: msg.interrupted,
  sources: msg.sources,
  taskSnapshot: msg.taskSnapshot,
});
```

done 帧 makeMessage 调用处（`st.done && st.content` 分支，`content: st.error || finalContent` 附近）加：

```ts
                  taskSnapshot: parseTaskSnapshot(st.result?.metadata),
```

`useChatPage.ts` 文件内加提取辅助（或 `shared/lib/` 纯函数）；文件顶部 import 需补 `import type { TaskSnapshot } from '../model/agent';`：

```ts
function parseTaskSnapshot(meta: Record<string, unknown> | undefined): TaskSnapshot | undefined {
  const raw = meta?.['stratum_task_snapshot'];
  if (!raw || typeof raw !== 'object') return undefined;
  const s = raw as Partial<TaskSnapshot>;
  if (!s.goal || !s.currentPhase) return undefined;
  return {
    goal: s.goal,
    currentPhase: s.currentPhase,
    completedSteps: s.completedSteps ?? [],
    nextAction: s.nextAction ?? '',
    status: s.status ?? 'active',
    failures: s.failures,
  };
}
```

- [ ] **Step 2: 写失败组件测试**

`web/src/modules/agent/components/__tests__/TaskProgressBanner.test.tsx`：

```tsx
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import TaskProgressBanner from '../TaskProgressBanner';
import type { TaskSnapshot } from '../../model/agent';

const snapshot: TaskSnapshot = {
  goal: '迁移订单服务到新架构',
  currentPhase: '1/2 完成',
  completedSteps: ['n1'],
  nextAction: '验证迁移',
  status: 'active',
};

describe('TaskProgressBanner', () => {
  it('renders goal, phase and next action', () => {
    render(<TaskProgressBanner snapshot={snapshot} />);
    expect(screen.getByText('迁移订单服务到新架构')).toBeTruthy();
    expect(screen.getByText(/1\/2 完成/)).toBeTruthy();
    expect(screen.getByText(/验证迁移/)).toBeTruthy();
  });

  it('renders completed state', () => {
    render(<TaskProgressBanner snapshot={{ ...snapshot, status: 'completed', nextAction: '' }} />);
    expect(screen.getByText(/已完成/)).toBeTruthy();
  });
});
```

- [ ] **Step 3: 运行确认失败**

```bash
cd web && npx vitest run src/modules/agent/components/__tests__/TaskProgressBanner.test.tsx 2>&1 | tail -6
```

Expected: FAIL（`Cannot find module '../TaskProgressBanner'`）。

- [ ] **Step 4: 实现组件**

`web/src/modules/agent/components/TaskProgressBanner.tsx`：

```tsx
import { Card, Tag } from 'antd';
import type { TaskSnapshot } from '../model/agent';

interface Props {
  snapshot: TaskSnapshot;
}

/**
 * 跨会话目标进度摘要条：展示 task 的 goal / current_phase / next_action。
 * 仅随 assistant 回复出现（SSE done metadata.stratum_task_snapshot 白名单透出）。
 */
export default function TaskProgressBanner({ snapshot }: Props) {
  const done = snapshot.status === 'completed';
  return (
    <Card size="small" style={{ marginBottom: 8 }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
        <Tag color={done ? 'success' : 'processing'}>{done ? '已完成' : '任务推进中'}</Tag>
        <span>{snapshot.goal}</span>
        <span style={{ color: 'rgba(0,0,0,0.45)' }}>{snapshot.currentPhase}</span>
        {!done && snapshot.nextAction ? (
          <span style={{ color: 'rgba(0,0,0,0.45)' }}>下一步：{snapshot.nextAction}</span>
        ) : null}
      </div>
    </Card>
  );
}
```

- [ ] **Step 5: ChatMessageList 集成**

`ChatMessageList.tsx` 渲染 assistant 消息内容处，若 `message.taskSnapshot` 存在则在内容上方渲染：

```tsx
{msg.role === 'assistant' && msg.taskSnapshot ? (
  <TaskProgressBanner snapshot={msg.taskSnapshot} />
) : null}
```

并确认文件顶部 `import TaskProgressBanner from './TaskProgressBanner';`。

- [ ] **Step 6: 运行确认通过**

```bash
cd web && npx vitest run src/modules/agent/components/__tests__/TaskProgressBanner.test.tsx 2>&1 | tail -6
```

Expected: PASS。

- [ ] **Step 7: Commit**

```bash
git add web/src/modules/agent/hooks/useChatPage.ts \
       web/src/modules/agent/model/agent.ts \
       web/src/modules/agent/components/TaskProgressBanner.tsx \
       web/src/modules/agent/components/__tests__/TaskProgressBanner.test.tsx \
       web/src/modules/agent/components/ChatMessageList.tsx
git commit -m "feat(agent): task progress banner in chat from SSE metadata"
```

---

### Task 14: 前端验证 + 计划收尾

**Files:** 无（验证）

- [ ] **Step 1: 前端 lint + build**

```bash
cd /home/yang/go-projects/stratum-agent-task-state
make fe-lint && make fe-build
```

Expected: 全绿。

- [ ] **Step 2: 全量回归**

```bash
go test -v -race -timeout 30s ./...
```

Expected: 全绿。

- [ ] **Step 3: 计划自检清单（对照设计文档 §9/§11）**

逐项确认已覆盖，缺则补测试/补实现：

- [ ] Plan→TaskSnapshot 映射纯函数（Task 3 表驱动）
- [ ] repository：Upsert/Claim/GetLatestActiveForOwner/CleanupExpired/DetachConversation + generation 冲突 + lease 过期接管 + 多活跃（Task 5）
- [ ] 提取挂点读内存 ActivePlan（Task 7）——`checkpoint_enabled=false` 路径天然覆盖（挂点不依赖 checkpoint）
- [ ] 恢复链路：命中/未命中/语义相关/checkpoint plan 优先/fail closed（Task 8）
- [ ] stratum_complete_task → status=completed（Task 6 + 7）
- [ ] 无 plan 不建 task（挂点 `ActivePlan==nil` 早退，Task 7 断言）
- [ ] 会话删除 detach 不级联（Task 9）
- [ ] CleanupExpired 回收（Task 5 + 10）
- [ ] metadata 白名单透出（Task 11）

- [ ] **Step 4: Commit 收尾**

```bash
git add -A
git commit -m "chore(agent): frontend verification and plan closeout" || echo "no changes"
```

---

## Self-Review

**1. Spec coverage（设计 §2/§5/§6/§7/§8/§9/§10/§11）:**

| 设计要求 | 任务 |
|---|---|
| agent_tasks 表 + 索引（§4 DDL） | Task 1 |
| Task/TaskSnapshot 实体 + 映射（§7.1） | Task 3 |
| TaskRepo port 显式 tenantID（§10） | Task 4 |
| PgTaskRepo execTenantID（§10） | Task 5 |
| 三层并发防护（§5）claim bump generation / lease / 乐观锁 | Task 5（SQL）+ Task 7（挂点） |
| stratum_complete_task 保留 tool（§7.1/§7.3） | Task 6 |
| 提取挂点读内存 ActivePlan（§7.1，非 checkpoint） | Task 7 |
| 写路径旁路降级、读路径 fail closed（§8） | Task 7（ERROR+return）/ Task 8（fail closed） |
| 恢复注入 + 语义相关（§7.2） | Task 8 |
| checkpoint plan 优先（§7.2 两级同命中） | Task 8（activePlan==nil 才注入） |
| conversation 删除 detach（§6） | Task 9 |
| CleanupExpired 定时回收（§6） | Task 5（DeleteExpired）+ Task 10（worker） |
| wiring 装配（§10） | Task 10 |
| metadata 白名单透出（§7.1 完成信号 + §10 前端） | Task 11 + Task 13 |
| 前端对话内提示（§2 非目标边界内） | Task 13 |
| 常量（§10） | Task 2 |
| 测试矩阵（§9） | Task 3/5/7/8/9/10 + Task 14 自检 |
| 成功标准（§11） | Task 14 Step 3 逐条 + `stratum-e2e-development` skill 系统验收 |

**未实现项（有意，YAGNI）：** 轮数兜底触发（§7.3 "至多可配置"，信号组合已足够，不引入循环复杂度）。`abandoned` 状态（仅用户显式操作进入，本期无 UI，port 已预留 status 枚举）。task 管理页面（§2 非目标）。

**2. Placeholder scan:** 无 TBD/TODO；所有代码步骤含完整实现；`STRATUM_TEST_POSTGRES_URL` 是测试环境变量名（与现有集成测试一致），非占位符。

**3. Type consistency:**

- `TaskStatus`/`TaskSnapshot`/`BuildTaskSnapshot`/`ToTask`/`ErrGenerationConflict`/`ErrTaskConversationGone` 在 Task 3-5 定义，Task 7/8 使用同名同型。
- `TaskRepo` 六方法签名在 Task 4 定义，Task 5 实现、Task 7/8/9/10 mock 与消费全部一致（`Claim`/`Save`/`Get`/`GetLatestActiveForOwner`/`DetachConversation`/`DeleteExpired`）。
- `constants.TaskMetadataKey`/`TaskMetadataCompleteKey`/`TaskLeaseDuration`/`TaskExpiresAt`/`TaskFailThreshold`/`TaskSemanticSimilarityThreshold`/`TaskCleanupInterval` 在 Task 2 定义，Task 3-13 引用同名。
- 前端 `TaskSnapshot` 接口（camelCase）与 Go `TaskSnapshot` json tag 对齐；`taskSnapshot` 消息字段名在 agent.ts / useChatPage.ts / ChatMessageList.tsx 三处一致。
- `persistTaskSnapshot` 在 executeReAct 与 executePlanning 两个挂点同名调用，方法签名 `(ctx, ec agentExecContext, finalState agentgraph.ReActState, result *AgentResult)` 一致。
