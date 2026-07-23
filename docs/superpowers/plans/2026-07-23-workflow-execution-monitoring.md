# Workflow Execution And Monitoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let members start published workflows, inspect and reconnect to their own runs, and let admins monitor and resolve all tenant runs.

**Architecture:** Reuse Workflow wire models and the read-only graph renderer from the admin module. A dedicated run reducer applies snapshot data plus monotonic SSE events idempotently; hooks separate execution form state, paged history, and live connection lifecycle. Server-provided `available_actions` is the only source for visible controls.

**Tech Stack:** React 18, TypeScript, Ant Design 5, Zod, Fetch SSE over the shared client configuration, Vitest, Playwright

---

## Tasks

### Task 1: Add Execution And Run Wire Models

**Files:**

- Modify: `web/src/modules/workflow/model/workflow.ts`
- Modify: `web/src/modules/workflow/model/workflow.test.ts`
- Modify: `web/src/modules/workflow/api/workflow.api.ts`
- Modify: `web/src/modules/workflow/api/workflow.api.test.ts`
- Modify: `web/src/constants/index.ts`

- [ ] **Step 1: Write failing run model tests**

Cover run summary/detail, attempts, approvals, effect intents, progress, available actions, event union, page envelopes, and field-level start errors.

- [ ] **Step 2: Verify RED**

Run: `npm --prefix web test -- src/modules/workflow/model/workflow.test.ts`
Expected: FAIL because run/event schemas are missing.

- [ ] **Step 3: Implement discriminated event schemas**

Use `event_type` to discriminate lifecycle, node output delta, and tool-step events. Unknown event types must parse to an explicit generic event rather than crashing the stream.

- [ ] **Step 4: Add API methods**

Implement `startWorkflowRun`, `listWorkflowRuns`, `getWorkflowRun`, `cancelWorkflowRun`, `pauseWorkflowRun`, `resumeWorkflowRun`, `decideWorkflowApproval`, and `resolveWorkflowManualIntervention` with Zod response parsing.

- [ ] **Step 5: Add behavior constants and commit**

Add `WORKFLOW_STREAM_RECONNECT_BASE_MS`, `WORKFLOW_STREAM_RECONNECT_MAX_MS`, and `WORKFLOW_OUTPUT_MAX_CHARS`.

```bash
git add web/src/modules/workflow web/src/constants
git commit -m '[feat](workflow): model workflow execution states'
```

### Task 2: Generate And Submit The Versioned Input Form

**Files:**

- Create: `web/src/modules/workflow/components/WorkflowRunForm.tsx`
- Create: `web/src/modules/workflow/components/WorkflowRunForm.test.tsx`
- Create: `web/src/modules/workflow/hooks/useWorkflowExecution.ts`
- Create: `web/src/modules/workflow/pages/WorkflowExecutionPage.tsx`
- Modify: `web/src/modules/workflow/routes.tsx`

- [ ] **Step 1: Write failing dynamic-form tests**

Cover all seven field types, required rules, option rendering, defaults, backend field-error mapping, loading, duplicate-submit protection, and the absence of file controls.

- [ ] **Step 2: Verify RED**

Run: `npm --prefix web test -- src/modules/workflow/components/WorkflowRunForm.test.tsx`
Expected: FAIL because the run form does not exist.

- [ ] **Step 3: Implement schema-to-Ant controls**

Map short/long text to Input/Input.TextArea, number to InputNumber, choice types to Select, boolean to Switch, and date to DatePicker. Keep the main task first and expanded; render explanatory copy with `extra`.

- [ ] **Step 4: Implement stable idempotent submission**

Generate one idempotency key when the form becomes dirty, reuse it across transport retries, and replace it only after a successful run creation or intentional input reset. Navigate to `/workflow-runs/:runId` on success.

- [ ] **Step 5: Run and commit**

```bash
git add web/src/modules/workflow
git commit -m '[feat](workflow): run published workflow inputs'
```

### Task 3: Add My Runs And Admin Run Center

**Files:**

- Create: `web/src/modules/workflow/hooks/useWorkflowRuns.ts`
- Create: `web/src/modules/workflow/pages/WorkflowRunsPage.tsx`
- Create: `web/src/modules/workflow/components/WorkflowRunsTable.tsx`
- Create: `web/src/modules/workflow/components/WorkflowRunFilters.tsx`
- Create: `web/src/modules/workflow/pages/WorkflowRunsPage.test.tsx`
- Modify: `web/src/modules/workflow/routes.tsx`
- Modify: `web/src/app/layout/menu.config.tsx`

- [ ] **Step 1: Write failing role/pagination tests**

Assert members see only “我的运行”; admins can switch to “全部运行”; status, workflow, time, page, and page size reach the API; and no client-side ownership filter is treated as security.

- [ ] **Step 2: Verify RED**

Run: `npm --prefix web test -- src/modules/workflow/pages/WorkflowRunsPage.test.tsx`
Expected: FAIL because run center components are absent.

- [ ] **Step 3: Implement server-paged run center**

Use no more than workflow, status/progress, started time, finished time, and action columns. Provide Skeleton, “运行记录还是空的”, and “没有找到匹配的运行”.

- [ ] **Step 4: Add navigation and run-detail links**

Place “运行中心” under the Workflow menu. Keep admin scope selection in the page, not as a second navigation tree.

- [ ] **Step 5: Run and commit**

```bash
git add web/src/modules/workflow web/src/app/layout
git commit -m '[feat](workflow): add paged workflow run center'
```

### Task 4: Implement Idempotent Run State Reduction

**Files:**

- Create: `web/src/modules/workflow/model/run-state.ts`
- Create: `web/src/modules/workflow/model/run-state.test.ts`
- Create: `web/src/modules/workflow/components/WorkflowRunCanvas.tsx`
- Create: `web/src/modules/workflow/components/WorkflowRunInspector.tsx`
- Create: `web/src/modules/workflow/components/WorkflowRunProgress.tsx`

- [ ] **Step 1: Write failing reducer tests**

Start from a run detail snapshot and apply ordered, duplicate, and out-of-order events. Assert the reducer ignores `sequence_no <= lastSequence`, appends bounded output, records tool steps, updates attempts, and distinguishes connection status from run status.

```ts
const next = reduceRunEvent(state, { sequence_no: 4, event_type: 'workflow.node_succeeded', node_id: 'n1' });
expect(next.lastSequence).toBe(4);
expect(reduceRunEvent(next, duplicate)).toBe(next);
```

- [ ] **Step 2: Verify RED**

Run: `npm --prefix web test -- src/modules/workflow/model/run-state.test.ts`
Expected: FAIL because the run reducer is absent.

- [ ] **Step 3: Implement reducer and selectors**

Provide selectors for completed count, current node, failed node, pending approval, output by node, and tool steps by node. Never derive terminal state from SSE closure.

- [ ] **Step 4: Implement read-only run canvas and inspector**

Render the immutable run snapshot. Nodes use icon plus text for queued/running/succeeded/failed/skipped/paused/manual states. Selecting a node opens attempts, output, tools, timing, and error details.

- [ ] **Step 5: Run and commit**

```bash
git add web/src/modules/workflow/model/run-state* web/src/modules/workflow/components/WorkflowRun*
git commit -m '[feat](workflow): render workflow run state'
```

### Task 5: Add Resumable GET SSE Support

**Files:**

- Modify: `web/src/services/client.ts`
- Modify: `web/src/services/client.test.ts`
- Create: `web/src/modules/workflow/hooks/useWorkflowRunStream.ts`
- Create: `web/src/modules/workflow/hooks/useWorkflowRunStream.test.tsx`

- [ ] **Step 1: Write a failing shared-client test**

Specify `streamApiGet(path, {lastEventId,onEvent,onClose,onError})`: it uses the shared base URL, in-memory Authorization, cookies, GET, `Last-Event-ID`, parses SSE `id/event/data`, and returns an AbortController.

- [ ] **Step 2: Verify RED**

Run: `npm --prefix web test -- src/services/client.test.ts`
Expected: FAIL because only POST streaming exists.

- [ ] **Step 3: Extract a shared SSE parser and implement GET streaming**

Reuse authentication readiness and error parsing. Preserve existing `streamApiEvents` behavior. Do not put credentials or tokens in the URL.

- [ ] **Step 4: Write failing reconnect-hook tests**

Use fake timers to assert reconnect begins from the latest sequence, uses bounded exponential backoff, resets delay after a received event, stops at terminal run state, and aborts on unmount or run change.

- [ ] **Step 5: Implement `useWorkflowRunStream`**

Fetch an initial detail snapshot, connect with its latest sequence, apply events through the reducer, and refetch detail after reconnect conflict or terminal closure. Display `connected/reconnecting/offline` separately.

- [ ] **Step 6: Run and commit**

```bash
git add web/src/services/client.ts web/src/services/client.test.ts web/src/modules/workflow/hooks/useWorkflowRunStream*
git commit -m '[feat](workflow): resume workflow event streams'
```

### Task 6: Build Run Details And Role-Safe Controls

**Files:**

- Create: `web/src/modules/workflow/pages/WorkflowRunDetailPage.tsx`
- Create: `web/src/modules/workflow/components/WorkflowRunActions.tsx`
- Create: `web/src/modules/workflow/components/WorkflowApprovalPanel.tsx`
- Create: `web/src/modules/workflow/components/WorkflowManualInterventionPanel.tsx`
- Create: `web/src/modules/workflow/pages/WorkflowRunDetailPage.test.tsx`
- Modify: `web/src/modules/workflow/routes.tsx`

- [ ] **Step 1: Write failing detail/action tests**

Assert only backend `available_actions` render, member own-run cancel works, admins see pause/resume, approvals/manual actions require confirmation, generation conflicts refresh detail, failed nodes open automatically, and 404/403 do not reveal stale data.

- [ ] **Step 2: Verify RED**

Run: `npm --prefix web test -- src/modules/workflow/pages/WorkflowRunDetailPage.test.tsx`
Expected: FAIL because the detail page is absent.

- [ ] **Step 3: Implement the detail composition**

Compose progress, full-width run canvas, node inspector, final result, and action panels. On mobile, replace the canvas with an ordered step list using the same selectors; keep result and cancel usable.

- [ ] **Step 4: Implement controls with generation fencing**

Every mutation reads the current generation from reducer state. Conflict errors use persistent notification, refetch, and never report success. Destructive or state-changing operations use `Modal.confirm` with consequences.

- [ ] **Step 5: Run and commit**

```bash
git add web/src/modules/workflow
git commit -m '[feat](workflow): monitor and govern workflow runs'
```

### Task 7: Add Agent Shortcut And End-To-End Verification

**Files:**

- Modify: `web/src/modules/agent/components/ChatHeader.tsx`
- Modify: `web/src/modules/agent/hooks/useChatPage.ts`
- Modify or create tests beside those files
- Create: `web/e2e/workflow-execution-monitoring.spec.ts`

- [ ] **Step 1: Write failing Agent shortcut test**

Assert “运行固定工作流” opens a searchable menu of published workflows and navigates to the dedicated execution route. It must not embed designer configuration in chat.

- [ ] **Step 2: Implement the lightweight shortcut**

Use the Workflow catalog API and a menu/drawer appropriate to viewport. Keep the existing chat composer unchanged.

- [ ] **Step 3: Add the real browser/API/DB journey**

Admin publishes a workflow; member A starts it; member B receives denial for detail and cancel; member A observes progress, reconnects after browser refresh, and sees completion; admin processes an approval and a separate manual-intervention fixture.

- [ ] **Step 4: Cover desktop and mobile**

Desktop uses DAG and inspector. Mobile opens real navigation, starts the same workflow, views ordered steps, and cancels an owned run. Use DOM and response assertions rather than screenshots in WSL2.

- [ ] **Step 5: Run release verification**

Run: `npm --prefix web test -- src/modules/workflow src/modules/agent`
Expected: PASS.

Run: `make fe-lint && make fe-build`
Expected: PASS.

Run: `npm --prefix web exec playwright test web/e2e/workflow-execution-monitoring.spec.ts --reporter=list`
Expected: PASS.

Run: `make risk-guardrails`
Expected: `risk regression guard: passed`.

- [ ] **Step 6: Commit E2E coverage**

```bash
git add web/src/modules/agent web/e2e/workflow-execution-monitoring.spec.ts
git commit -m '[test](workflow): verify member workflow journey'
```
