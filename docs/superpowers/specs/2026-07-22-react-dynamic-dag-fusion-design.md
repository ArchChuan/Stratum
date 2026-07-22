# ReAct Dynamic DAG Fusion Design

## Decision

Stratum has one Agent architecture: ReAct. Planning, tool use, RAG, memory, skills, approvals, and collaboration are capabilities of that architecture, not Agent types.

Dynamic DAG planning is fused into the ReAct execution loop. The model explicitly chooses plan operations; deterministic runtime code validates and applies those operations, schedules ready nodes, persists progress, and returns node outcomes as observations to the same parent loop.

This replaces the current `PlanningAgent` branch and the top-level `BuildPlanExecuteGraph` alternative. It does not turn transient Agent plans into published Workflow definitions.

## Evidence And Boundaries

Repository evidence shows the current implementation has separate `AgentType` branches and a lazy Plan-Execute graph triggered after a stuck threshold. It also has a durable static Workflow DAG runtime with dependency validation, ready-set scheduling, attempts, checkpoints, cancellation, and recovery semantics.

The verified Obsidian principle "Agent 系统应分离执行循环、编排、状态、记忆与治理" supports separating local model decisions from deterministic orchestration and persistent state. It does not establish this exact component layout as an industry standard.

The ReAct paper describes interleaved reasoning and actions that can track and update action plans. Anthropic's "Building effective agents" recommends simple, composable patterns and distinguishes model-directed agents from predefined workflows. Neither source proves that Stratum must use a dynamic DAG; the DAG kernel is a project-specific choice justified by existing Workflow infrastructure and the need for explicit dependencies, concurrency, recovery, and approvals.

## Runtime Architecture

Every execution enters one ReAct kernel:

```text
Observe
  -> Decide
       -> answer
       -> call_tool
       -> create_plan
       -> revise_plan
       -> continue_plan
       -> cancel_plan
  -> Harness validates and executes the action
  -> Action result becomes an observation
  -> Observe
```

The active plan is part of the execution state. It is not a separate Agent instance or top-level graph.

Ready plan nodes use the same ReAct kernel with node-scoped context. Independent nodes may run concurrently, but they have no separate Agent type, identity, persona, or durable memory ownership. Node outputs are summarized observations returned to the parent loop.

## Components

### ReAct Kernel

The only model-directed decision loop. It receives conversation context, tool observations, plan observations, budgets, and execution controls. It may answer, call a tool, or issue a plan command.

### Plan Command Validator

Accepts structured commands and rejects invalid state transitions. The model cannot directly mutate runtime state.

Each mutating command carries `expected_revision`. The validator applies a command only when it matches the current plan revision, then increments the revision atomically.

### Shared DAG Kernel

A domain-neutral scheduling kernel extracted from the existing Workflow runtime. It owns:

- dependency and cycle validation;
- ready-set calculation;
- bounded concurrent attempts;
- node state transitions;
- cancellation propagation;
- checkpoint state;
- retry eligibility and idempotency metadata.

It must not depend on Workflow publication, versions, HTTP DTOs, repositories, or Agent prompts.

### Agent Plan Runtime

Adapts ReAct plan commands and node execution to the shared DAG kernel. It owns transient plan semantics, node prompts, summarized observations, execution budgets, and checkpoint serialization.

### Workflow Runtime

Continues to own user-defined, published, versioned business workflows. It consumes the shared DAG kernel but retains its existing application service, repositories, RBAC, and APIs.

## Plan Commands

The following built-in Harness actions are exposed to the model as tool definitions. They are not MCP tools and cannot be overridden by external tools.

```text
stratum_create_plan
stratum_revise_plan
stratum_continue_plan
stratum_cancel_plan
```

`create_plan` supplies nodes and dependencies. `revise_plan` applies explicit add, update, remove, or dependency operations against an expected revision. `continue_plan` asks the deterministic scheduler to execute the current ready set. `cancel_plan` cancels outstanding attempts and waits for them to stop.

The model supplies goals, dependency intent, and optional tool hints. Runtime code controls identifiers, revisions, statuses, attempts, concurrency, budgets, approvals, and idempotency keys.

## State Model

Agent execution state contains:

- conversation messages and observations;
- tool and trace history;
- total token, cost, time, node, and revision budgets;
- optional active plan runtime state;
- final output and execution status.

Plan runtime state contains:

- plan ID and revision;
- node definitions and dependencies;
- node statuses and attempt records;
- summarized outputs and errors;
- approval and uncertain-side-effect state;
- checkpoint version and timestamps.

Allowed plan lifecycle states are:

```text
none -> active -> revising -> active -> completed
active -> waiting_approval | blocked | failed | cancelled
```

Node lifecycle transitions are deterministic and validated. A model response cannot assign statuses directly.

## Scheduling And Context

The scheduler executes only nodes whose dependencies completed successfully and whose approval requirements are satisfied. Concurrency is bounded by a named constant.

Each node execution receives:

- the parent system instructions and capability catalog;
- the node goal and allowed tool hints;
- summaries of dependency outputs;
- remaining node and global budgets;
- tenant, trace, execution, and conversation identity.

It does not receive unrelated raw tool observations from sibling or parent execution history. Sub-node planning commands are disabled initially to prevent unbounded nested graphs.

After a ready set finishes, the parent loop receives one structured plan observation containing completed, failed, blocked, and pending nodes plus remaining budgets. The parent may answer, revise the graph, or continue it.

## Failure Semantics

- Invalid schema, dependency cycles, stale revisions, and forbidden transitions return corrective observations; they do not mutate plan state.
- Checkpoint persistence failure terminates the execution. The runtime must not report a plan command or node transition as successful when persistence failed.
- Ordinary node failures are recorded as attempts and returned to the parent loop for deterministic retry eligibility plus model-directed revision or termination.
- An external side effect with unknown outcome enters `failed_pending_confirmation` and is never automatically replayed.
- Token, cost, time, node-count, revision-count, concurrent-node, and attempt budgets are hard runtime limits.
- Cancellation propagates to every node context and waits for all tracked goroutines before completion.
- Panics inside node execution are recovered into failed attempts without losing wait-group accounting.

## Compatibility And Migration

The domain and application layers remove `AgentType` and all architecture switches. Agent creation and execution always construct the unified ReAct runtime.

For one compatibility stage, the HTTP handler may accept the existing `type` field but ignores it. Responses may retain the frozen compatibility value `react`; no internal decision may depend on it. The frontend removes its hidden `type` field.

The existing database `type` column remains temporarily because removing tenant-only DDL is a separate destructive migration. Repositories stop using it to choose execution behavior. A later reviewed migration may remove the column after code references reach zero.

The current stuck-threshold transition and `BuildPlanExecuteGraph` are removed after their capabilities move into the unified loop. Checkpoint rows remain compatible only where their persisted schema can represent the new revisioned state; otherwise a versioned runtime payload is introduced with explicit rejection of unsupported versions.

## Testing

### Domain And Kernel

- command schema and expected-revision compare-and-swap;
- dependency validation, cycle rejection, and ready-set calculation;
- node and plan state transition tables;
- bounded concurrency, cancellation, and attempt accounting;
- uncertain-side-effect states and retry prohibition.

### ReAct Integration

- a simple answer incurs no planning calls;
- the model explicitly creates a plan without a stuck threshold;
- ready nodes execute through the same ReAct kernel;
- node results return to the parent as observations;
- the parent revises a partially completed graph and continues;
- stale or invalid commands are correctable observations;
- final answer occurs only after the parent loop observes sufficient completion.

### Persistence And Compatibility

- every accepted transition persists before success is exposed;
- persistence failure propagates;
- checkpoint restore preserves revision and attempt identity;
- legacy `type` inputs do not alter execution architecture;
- repository reads of historical type values produce the same unified Agent behavior.

### End To End

A real API and database test creates a normal Agent, submits a multi-step request, observes plan creation, parallel ready-node execution, revision, checkpoint recovery, and final streamed output. Failure scenarios cover cancellation, stale revision, persistence failure, approval waiting, and uncertain external side effects.

## Success Criteria

- There is no domain-level concept of multiple Agent architectures.
- Every Agent execution uses one ReAct entry path.
- Planning is an explicit model-selected action inside that loop.
- DAG scheduling and state transitions are deterministic runtime code.
- Agent plans and published Workflows have separate product semantics and share only a domain-neutral DAG kernel.
- Existing simple ReAct tasks retain zero planning overhead.
- All plan mutations, attempts, approvals, failures, and recovery points are observable and tenant-scoped.
