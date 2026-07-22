# ReAct Dynamic DAG Fusion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace architecture-specific Agent execution with one ReAct loop that can explicitly create, revise, continue, and cancel a transient dynamic DAG.

**Architecture:** Extract a stdlib-only DAG validation and ready-set kernel into `pkg/dag`, then adapt Workflow without changing its product behavior. Add revisioned plan state and deterministic built-in plan commands to the existing ReAct graph; ready nodes execute through the same ReAct graph with isolated context and bounded concurrency, and every accepted mutation/checkpoint must persist before success is observed.

**Tech Stack:** Go 1.25, existing generic graph runtime, PostgreSQL checkpoint repository, React 18/TypeScript, Go `testing`/Testify, Vitest.

---

## Task 1: Domain-neutral DAG kernel

**Files:**

- Create: `pkg/dag/dag.go`
- Create: `pkg/dag/dag_test.go`

- [x] **Step 1: Write failing validation and ready-set tests**

Define table tests for duplicate/missing node IDs, missing dependency targets, self-dependencies, cycles, deterministic fan-out/fan-in ordering, failed-dependency blocking, and completed-node exclusion. Use this public contract:

```go
type Status string
const (
    StatusPending Status = "pending"
    StatusRunning Status = "running"
    StatusSucceeded Status = "succeeded"
    StatusFailed Status = "failed"
    StatusBlocked Status = "blocked"
    StatusCancelled Status = "cancelled"
)
type Node struct { ID string; DependsOn []string }
type Snapshot struct { Nodes []Node; Statuses map[string]Status }
func Validate(nodes []Node) error
func Ready(snapshot Snapshot) (ready, blocked []string, complete bool, err error)
```

- [x] **Step 2: Verify the tests fail**

Run: `go test ./pkg/dag`
Expected: FAIL because package functions do not exist.

- [x] **Step 3: Implement deterministic validation and scheduling**

Implement Kahn cycle detection, sorted dependency copies, sorted output IDs, and fail-closed unknown statuses. A pending node is ready only when all dependencies succeeded; it is blocked when any dependency is failed, blocked, or cancelled. `complete` is true only when every node is terminal.

- [x] **Step 4: Verify and commit**

Run: `go test ./pkg/dag`
Expected: PASS.

```bash
git add pkg/dag
git commit -m "[feat](dag): add deterministic scheduling kernel"
```

## Task 2: Workflow adapter with no behavior change

**Files:**

- Modify: `internal/workflow/application/ready_set.go`
- Modify: `internal/workflow/application/ready_set_test.go`
- Modify: `internal/workflow/application/service.go`
- Test: `internal/workflow/application/dag_scheduler_test.go`

- [x] **Step 1: Add characterization tests**

Add cases covering retry-wait, condition-edge selection, skipped branch propagation, failed upstream behavior, and stable ordering. Preserve the existing `ReadySet(spec, attempts)` and private `readySet` outputs.

- [x] **Step 2: Verify characterization tests pass before refactoring**

Run: `go test ./internal/workflow/application -run 'ReadySet|DAGScheduler'`
Expected: PASS.

- [x] **Step 3: Adapt Workflow to `pkg/dag`**

Map Workflow nodes and latest attempts to `dag.Node`/`dag.Status`; retain condition-edge resolution, retry timing, and branch skipping in Workflow-specific adapter code. Delete duplicated generic cycle/dependency readiness logic only after output parity is established.

- [x] **Step 4: Verify and commit**

Run: `go test ./internal/workflow/application -run 'ReadySet|DAGScheduler'`
Expected: PASS with unchanged Workflow behavior.

```bash
git add internal/workflow/application
git commit -m "[refactor](workflow): consume shared DAG kernel"
```

## Task 3: Revisioned Agent plan domain

**Files:**

- Create: `internal/agent/domain/plan.go`
- Create: `internal/agent/domain/plan_test.go`
- Modify: `pkg/constants/agent.go`

- [x] **Step 1: Write failing state-machine tests**

Cover creation, stale `expected_revision`, add/update/remove/dependency revisions, cycle rejection without mutation, lifecycle transitions, terminal-node immutability, node/revision/attempt budgets, and uncertain side-effect retry prohibition. Define `Plan`, `PlanNode`, `PlanAttempt`, `PlanStatus`, `PlanCommand`, `RevisionOperation`, and `ApplyPlanCommand` in the domain package.

- [x] **Step 2: Verify the tests fail**

Run: `go test ./internal/agent/domain -run Plan`
Expected: FAIL because plan domain types are absent.

- [x] **Step 3: Implement immutable command application**

Clone state before validation, compare `ExpectedRevision`, delegate dependency/cycle checks to `pkg/dag`, generate runtime node IDs, and increment revision exactly once for each accepted mutation. Add named limits for nodes, revisions, attempts, concurrent nodes, and checkpoint TTL in `pkg/constants/agent.go`.

- [x] **Step 4: Verify and commit**

Run: `go test ./internal/agent/domain -run Plan`
Expected: PASS.

```bash
git add internal/agent/domain/plan.go internal/agent/domain/plan_test.go pkg/constants/agent.go
git commit -m "[feat](agent): add revisioned plan state machine"
```

## Task 4: Plan checkpoint codec and fail-closed persistence

**Files:**

- Create: `internal/agent/application/graph/plan_checkpoint.go`
- Create: `internal/agent/application/graph/plan_checkpoint_test.go`
- Modify: `internal/agent/domain/agent.go`
- Modify: `internal/agent/domain/port/repository.go`
- Modify: `internal/agent/infrastructure/persistence/checkpoint_store_test.go`

- [x] **Step 1: Write failing codec and persistence tests**

Test versioned JSON round-trip preserving revision and attempt IDs, unsupported-version rejection, tenant/execution identity propagation, and a store error preventing a successful command observation. Use `planCheckpointVersion = 1` and store the envelope in `AgentExecutionCheckpoint.RuntimeStateJSON`.

- [x] **Step 2: Verify the tests fail**

Run: `go test ./internal/agent/application/graph ./internal/agent/infrastructure/persistence -run 'PlanCheckpoint|CheckpointStore'`
Expected: FAIL for missing codec/runtime behavior.

- [x] **Step 3: Implement the checkpoint boundary**

Add `PlanCheckpointWriter` and codec helpers in `plan_checkpoint.go`; include plan, global budgets, and active attempt identities. Always call `Upsert(ctx, tenantID, checkpoint)` before returning a successful mutation or transition observation, wrapping errors with `plan checkpoint: %w`.

- [x] **Step 4: Verify and commit**

Run: `go test ./internal/agent/application/graph ./internal/agent/infrastructure/persistence -run 'PlanCheckpoint|CheckpointStore'`
Expected: PASS.

```bash
git add internal/agent/application/graph/plan_checkpoint.go internal/agent/application/graph/plan_checkpoint_test.go internal/agent/domain internal/agent/infrastructure/persistence/checkpoint_store_test.go
git commit -m "[feat](agent): persist versioned plan checkpoints"
```

## Task 5: Built-in plan tools inside ReAct

**Files:**

- Create: `internal/agent/application/graph/plan_tools.go`
- Create: `internal/agent/application/graph/plan_tools_test.go`
- Modify: `internal/agent/application/graph/react.go`
- Modify: `internal/agent/application/graph/react_tools.go`

- [x] **Step 1: Write failing tool-definition and dispatch tests**

Assert that `stratum_create_plan`, `stratum_revise_plan`, `stratum_continue_plan`, and `stratum_cancel_plan` are always available, cannot be shadowed by external tools, reject malformed/stale commands as corrective tool observations without mutation, and create plans without any stuck counter.

- [x] **Step 2: Verify the tests fail**

Run: `go test ./internal/agent/application/graph -run 'PlanTool|ExplicitPlan'`
Expected: FAIL because built-in plan tools are unavailable.

- [x] **Step 3: Implement definitions and deterministic dispatch**

Append reserved JSON-schema tool definitions in `effectiveTools`; route reserved names before provider classification in `makeToolNode`. Decode arguments with `json.Marshal`/`json.Unmarshal`, call the domain command validator, persist accepted changes, and append a structured tool result containing plan ID, revision, statuses, and correction details.

- [x] **Step 4: Verify and commit**

Run: `go test ./internal/agent/application/graph -run 'PlanTool|ExplicitPlan'`
Expected: PASS.

```bash
git add internal/agent/application/graph/plan_tools.go internal/agent/application/graph/plan_tools_test.go internal/agent/application/graph/react.go internal/agent/application/graph/react_tools.go
git commit -m "[feat](agent): expose explicit plan actions in ReAct"
```

## Task 6: Ready-node execution through the same ReAct kernel

**Files:**

- Create: `internal/agent/application/graph/plan_runtime.go`
- Create: `internal/agent/application/graph/plan_runtime_test.go`
- Modify: `internal/agent/application/graph/react.go`

- [ ] **Step 1: Write failing runtime tests**

Test fan-out bounded concurrency, fan-in, dependency-summary-only context, inherited tenant/trace/execution/conversation identity, disabled nested plan tools, panic recovery, cancellation waiting for all workers, failure accounting, and `failed_pending_confirmation` never being replayed.

- [ ] **Step 2: Verify the tests fail**

Run: `go test -race ./internal/agent/application/graph -run 'PlanRuntime|PlanCancellation|PlanContext'`
Expected: FAIL because no unified node runtime exists.

- [ ] **Step 3: Implement the runtime**

Create a semaphore-bounded errgroup-style runner using `context.WithCancel`, `sync.WaitGroup`, and result channels sized to the ready set. Each node builds a fresh `ReActState` with parent system/capability context, node goal, dependency summaries, remaining budgets, and identities; omit unrelated messages and remove plan tools. Recover panics per worker, persist each transition before exposing it, cancel on checkpoint failure, then wait for every worker.

- [ ] **Step 4: Return a structured parent observation**

After a wave, append one tool observation with sorted `completed`, `failed`, `blocked`, and `pending` node summaries plus remaining budgets. The parent LLM then chooses answer, revision, continuation, or cancellation.

- [ ] **Step 5: Verify and commit**

Run: `go test -race ./internal/agent/application/graph -run 'PlanRuntime|PlanCancellation|PlanContext'`
Expected: PASS with maximum observed concurrency at or below the named limit.

```bash
git add internal/agent/application/graph/plan_runtime.go internal/agent/application/graph/plan_runtime_test.go internal/agent/application/graph/react.go
git commit -m "[feat](agent): execute DAG nodes through ReAct kernel"
```

## Task 7: Collapse application execution to one architecture

**Files:**

- Modify: `internal/agent/application/agent.go`
- Modify: `internal/agent/application/agent_service.go`
- Modify: `internal/agent/application/agent_service_test.go`
- Modify: `internal/agent/application/agent_service_internal_test.go`
- Delete: `internal/agent/application/graph/plan_execute.go`
- Delete: `internal/agent/application/graph/plan_execute_test.go`
- Modify: `internal/agent/application/graph/plan_checkpoint_integration_test.go`
- Modify: `internal/agent/domain/agent.go`

- [ ] **Step 1: Write failing unified-path tests**

Create agents with historical values `react`, `planning`, `cot`, `tool_calling`, `rag`, and `swarm`; assert all call `BuildReActGraph` behavior and simple answers expose no plan calls. Assert checkpoint failures from a plan still propagate from `Execute`.

- [ ] **Step 2: Verify the new tests fail**

Run: `go test ./internal/agent/application/... -run 'Unified|HistoricalType|SimpleAnswer'`
Expected: FAIL because `BaseAgent.Execute` still switches on type.

- [ ] **Step 3: Remove architecture switches**

Remove `AgentType`, architecture constants, `StuckThreshold`, old `PlanStep`/`StepResult`/`PlanRuntimeState`, the Planning alias, and the `switch agentType` branch. Always build the unified ReAct graph, wire checkpoint/runtime dependencies into `ReActState`, and retain the persisted database type only inside repository compatibility mapping.

- [ ] **Step 4: Delete the fallback graph**

Delete `BuildPlanExecuteGraph` and migrate its still-valid checkpoint integration case to explicit plan commands. Confirm no production references remain:

Run: `rg -n 'PlanningAgent|BuildPlanExecuteGraph|StuckThreshold|PlanTriggered' --glob '!docs/**'`
Expected: no matches.

- [ ] **Step 5: Verify and commit**

Run: `go test ./internal/agent/...`
Expected: PASS.

```bash
git add -A internal/agent
git commit -m "[refactor](agent): unify execution on ReAct"
```

## Task 8: HTTP, repository, and frontend compatibility

**Files:**

- Modify: `api/http/handler/agent_dto.go`
- Modify: `api/http/handler/agent_handler.go`
- Modify: `api/http/contract_test.go`
- Modify: `internal/agent/infrastructure/persistence/agent_repo.go`
- Modify: `internal/agent/infrastructure/persistence/agent_repo_test.go`
- Modify: `web/src/modules/agent/model/agent.ts`
- Modify: `web/src/modules/agent/model/__tests__/agent.test.ts`
- Modify: `web/src/modules/agent/pages/CreateAgentPage.tsx`
- Modify: `web/src/modules/agent/hooks/useEditAgentPage.ts`

- [ ] **Step 1: Add failing compatibility tests**

Assert request `type` is accepted but ignored, responses always return `react`, and rows containing every historical type map to identical application configuration. In Vitest, assert create/update payloads contain no `type` while response parsing tolerates it.

- [ ] **Step 2: Verify the tests fail**

Run: `go test ./api/http ./internal/agent/infrastructure/persistence -run 'Agent.*Type|HistoricalType'`
Run: `cd web && npm test -- --run src/modules/agent/model/__tests__/agent.test.ts`
Expected: at least one assertion fails because type still drives application/form state.

- [ ] **Step 3: Implement compatibility-only mapping**

Keep JSON request/response fields required by frozen contracts, discard request values at the handler boundary, return literal `react`, and stop surfacing repository `type` as an application choice. Remove hidden/default frontend form `type` while leaving tolerant response schema parsing.

- [ ] **Step 4: Verify and commit**

Run: `go test ./api/http ./internal/agent/infrastructure/persistence -run 'Agent.*Type|HistoricalType|Contract'`
Run: `cd web && npm test -- --run src/modules/agent/model/__tests__/agent.test.ts`
Expected: PASS.

```bash
git add api/http internal/agent/infrastructure/persistence web/src/modules/agent
git commit -m "[refactor](agent): make type compatibility-only"
```

## Task 9: Real-chain recovery and E2E coverage

**Files:**

- Create: `internal/agent/application/graph/plan_runtime_integration_test.go`
- Modify: `internal/agent/application/graph/plan_checkpoint_integration_test.go`
- Modify: `api/http/handler/agent_handler_integration_test.go`
- Modify: `docs/agent/agent-chat-flow.md`

- [ ] **Step 1: Read and follow the E2E skill**

Read `/home/yang/go-projects/stratum/.agents/skills/stratum-e2e-development/SKILL.md` completely and apply its environment, evidence, cleanup, and real-chain requirements.

- [ ] **Step 2: Add integration tests**

Use real PostgreSQL checkpoint storage and a scripted LLM capability to create a multi-step plan, run two ready nodes concurrently, revise after partial completion, restore by execution ID, and finish through the parent ReAct loop. Add explicit cancellation, approval waiting, stale revision, persistence failure, and uncertain-side-effect cases.

- [ ] **Step 3: Verify focused real-chain tests**

Run the exact integration command prescribed by the E2E skill and repository environment.
Expected: all plan lifecycle events are persisted under the same tenant/execution identity; no worker remains after cancellation.

- [ ] **Step 4: Update architecture documentation and commit**

Document one ReAct path, explicit plan tools, transient plan versus Workflow semantics, revision/checkpoint behavior, and nested-planning prohibition.

```bash
git add internal/agent/application/graph api/http/handler/agent_handler_integration_test.go docs/agent/agent-chat-flow.md
git commit -m "[test](agent): verify dynamic plan lifecycle"
```

## Task 10: Full verification and delivery readiness

**Files:**

- Modify only files needed to fix failures caused by Tasks 1-9.

- [ ] **Step 1: Confirm forbidden architecture remnants are gone**

Run: `rg -n 'PlanningAgent|BuildPlanExecuteGraph|StuckThreshold|PlanTriggered|unknown agent type' --glob '!docs/superpowers/**'`
Expected: no production or test matches.

- [ ] **Step 2: Run focused and static verification**

Run: `gofmt -w pkg/dag internal/agent internal/workflow/application api/http/handler`
Run: `go vet ./...`
Run: `go test -short ./...`
Expected: all commands exit 0.

- [ ] **Step 3: Run race and frontend verification**

Run: `go test -v -race -timeout 30s ./...`
Run: `make fe-lint && make fe-build`
Expected: all commands exit 0 with no races or TypeScript errors.

- [ ] **Step 4: Run guardrails**

Run: `make risk-guardrails`
Expected: all blocking guards pass; no failure is swallowed or downgraded.

- [ ] **Step 5: Review the final diff and commit fixes**

Run: `git diff --check && git status --short && git log --oneline origin/main..HEAD`
Expected: no whitespace errors; only scoped files are changed; commits follow repository title format.

```bash
git add -A
git commit -m "[chore](agent): finalize ReAct DAG integration"
```
