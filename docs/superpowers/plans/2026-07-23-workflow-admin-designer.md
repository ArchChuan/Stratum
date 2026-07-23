# Workflow Admin Designer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let tenant admins create, edit, validate, publish, and inspect immutable workflow versions without editing JSON.

**Architecture:** Add a self-contained `web/src/modules/workflow` module. Zod models isolate HTTP wire contracts, a reducer owns editable graph state, React Flow renders and edits the DAG, and small Ant Design panels own metadata, node configuration, validation, and publication.

**Tech Stack:** React 18, TypeScript, Ant Design 5, Zod, React Router 6, `@xyflow/react@12.11.2`, Vitest, Testing Library, Playwright

---

## Tasks

### Task 1: Add Workflow Frontend Models And API Client

**Files:**

- Modify: `web/package.json`
- Modify: `web/package-lock.json`
- Create: `web/src/modules/workflow/model/workflow.ts`
- Create: `web/src/modules/workflow/model/workflow.test.ts`
- Create: `web/src/modules/workflow/api/workflow.api.ts`
- Create: `web/src/modules/workflow/api/workflow.api.test.ts`
- Modify: `web/src/constants/index.ts`

- [ ] **Step 1: Install the reviewed graph dependency**

Run: `npm --prefix web install @xyflow/react@12.11.2`
Expected: package and lockfile contain exactly `@xyflow/react` major 12.

- [ ] **Step 2: Write failing Zod model tests**

Cover five node types, input schema fields, definition summaries, version pages, validation issues, and malformed API payload rejection.

```ts
expect(() => workflowNodeSchema.parse({ id: 'n1', type: 'unknown' })).toThrow();
expect(workflowInputSchema.parse({ task_label: '任务', fields: [] })).toBeDefined();
```

- [ ] **Step 3: Verify RED**

Run: `npm --prefix web test -- src/modules/workflow/model/workflow.test.ts`
Expected: FAIL because the module does not exist.

- [ ] **Step 4: Implement model schemas and inferred types**

Export schemas for `WorkflowDefinition`, `WorkflowVersion`, `WorkflowSpec`, `WorkflowNode`, `WorkflowEdge`, `WorkflowInputSchema`, `WorkflowPage`, and `ValidationIssue`. Keep wire snake_case inside the module.

- [ ] **Step 5: Write API tests and implement client methods**

Test and add `listWorkflows`, `getWorkflow`, `createWorkflow`, `updateWorkflowDraft`, `validateWorkflow`, `publishWorkflow`, and `listWorkflowVersions`, all using the shared Axios instance and Zod parsing.

- [ ] **Step 6: Add named UI constants and commit**

Add `WORKFLOW_DEFAULT_PAGE_SIZE`, `WORKFLOW_VALIDATION_FOCUS_MS`, and graph dimension constants. Run model/API tests and commit.

```bash
git add web/package.json web/package-lock.json web/src/modules/workflow web/src/constants
git commit -m '[feat](workflow): add frontend workflow contracts'
```

### Task 2: Add Admin Routes, Navigation, And Catalog

**Files:**

- Create: `web/src/modules/workflow/index.ts`
- Create: `web/src/modules/workflow/routes.tsx`
- Create: `web/src/modules/workflow/pages/WorkflowCatalogPage.tsx`
- Create: `web/src/modules/workflow/hooks/useWorkflowCatalog.ts`
- Create: `web/src/modules/workflow/components/WorkflowCatalogTable.tsx`
- Create: `web/src/modules/workflow/components/WorkflowCatalogHeader.tsx`
- Create: `web/src/modules/workflow/pages/WorkflowCatalogPage.test.tsx`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/app/layout/menu.config.tsx`
- Modify: `web/src/app/layout/__tests__/menu.config.test.tsx`

- [ ] **Step 1: Write failing route/menu/catalog tests**

Assert every member sees “工作流”, only admins see “新建工作流”, server pagination is forwarded, and empty/search states use the approved Chinese copy.

- [ ] **Step 2: Verify RED**

Run: `npm --prefix web test -- src/modules/workflow/pages/WorkflowCatalogPage.test.tsx src/app/layout/__tests__/menu.config.test.tsx`
Expected: FAIL because Workflow routes and menu items are absent.

- [ ] **Step 3: Implement routes and catalog hook**

Register `/workflows`, `/workflows/new`, `/workflows/:id/edit`, and `/workflows/:id/versions/:versionId`. Protect authoring routes with `requiredTenantRole="admin"`. The hook owns query, page, pageSize, loading, and persistent error notification.

- [ ] **Step 4: Implement responsive catalog UI**

Use `ResponsiveDataView` with no more than name, state/current version, updated time, and action columns. Members open the execution page; admins can also edit.

- [ ] **Step 5: Run tests and commit**

```bash
git add web/src/app web/src/modules/workflow
git commit -m '[feat](workflow): add workflow catalog routes'
```

### Task 3: Implement Reducer-Based Graph Editing

**Files:**

- Create: `web/src/modules/workflow/model/editor.ts`
- Create: `web/src/modules/workflow/model/editor.test.ts`
- Create: `web/src/modules/workflow/components/WorkflowCanvas.tsx`
- Create: `web/src/modules/workflow/components/nodes/WorkflowNodeCard.tsx`
- Create: `web/src/modules/workflow/components/WorkflowNodePalette.tsx`
- Create: `web/src/modules/workflow/components/WorkflowCanvas.test.tsx`

- [ ] **Step 1: Write failing reducer tests**

Cover insert, connect, delete, select, rename, condition-edge labels, dirty state, server reset, and deterministic conversion between domain nodes/edges and React Flow nodes/edges.

```ts
const next = workflowEditorReducer(initial, { type: 'node.insert', nodeType: 'agent' });
expect(next.spec.nodes).toHaveLength(1);
expect(next.dirty).toBe(true);
```

- [ ] **Step 2: Verify RED**

Run: `npm --prefix web test -- src/modules/workflow/model/editor.test.ts`
Expected: FAIL because the reducer does not exist.

- [ ] **Step 3: Implement the pure editor reducer**

Keep persistence IDs separate from canvas position metadata. Generate node and edge IDs in explicit action creators, not during render. Reject self-connections locally while leaving full validation to the backend.

- [ ] **Step 4: Write failing canvas behavior tests**

Assert stable node dimensions, keyboard delete, fit-view button, accessible node labels, non-color status/type icons, and palette insertion. Mock only React Flow viewport primitives that jsdom cannot implement.

- [ ] **Step 5: Implement canvas and custom node**

Use `ReactFlow`, `Background`, `Controls`, `MiniMap`, and typed `Handle` components. Do not nest canvas in a decorative Card; keep it full-height within the editor workspace.

- [ ] **Step 6: Run and commit**

Run: `npm --prefix web test -- src/modules/workflow/model/editor.test.ts src/modules/workflow/components/WorkflowCanvas.test.tsx`
Expected: PASS.

```bash
git add web/src/modules/workflow/model/editor* web/src/modules/workflow/components/WorkflowCanvas* web/src/modules/workflow/components/WorkflowNodePalette.tsx web/src/modules/workflow/components/nodes
git commit -m '[feat](workflow): edit workflow graphs visually'
```

### Task 4: Build Metadata, Input, And Node Configuration Panels

**Files:**

- Create: `web/src/modules/workflow/components/WorkflowMetadataForm.tsx`
- Create: `web/src/modules/workflow/components/WorkflowInputSchemaEditor.tsx`
- Create: `web/src/modules/workflow/components/WorkflowNodeInspector.tsx`
- Create: `web/src/modules/workflow/components/WorkflowAdvancedSettings.tsx`
- Create: `web/src/modules/workflow/components/WorkflowConfiguration.test.tsx`
- Reuse APIs from: `web/src/modules/agent/api/agent.api.ts`, `web/src/modules/skill/api/skill.api.ts`, `web/src/modules/mcp/api/mcp.api.ts`

- [ ] **Step 1: Write failing configuration tests**

Assert required name/task label, unique input keys, correct controls for every field type, pinned Skill revision selection, MCP effect class, one default condition edge, retry stepper, timeout input, and advanced settings collapsed by default.

- [ ] **Step 2: Verify RED**

Run: `npm --prefix web test -- src/modules/workflow/components/WorkflowConfiguration.test.tsx`
Expected: FAIL because configuration components do not exist.

- [ ] **Step 3: Implement metadata and input editor**

Use `Form.List` for additional fields, Select for field types/options, Switch for required, and input controls matching type. Reject reserved/duplicate keys before save.

- [ ] **Step 4: Implement node inspector**

Render type-specific basic fields first. Put input mapping, output mapping, retry, timeout, and effect details in `Collapse` labeled “高级设置”. Resource selectors show human names while saving IDs/revisions.

- [ ] **Step 5: Run and commit**

```bash
git add web/src/modules/workflow/components
git commit -m '[feat](workflow): configure workflow nodes and inputs'
```

### Task 5: Implement Save, Conflict, Validation, And Publish

**Files:**

- Create: `web/src/modules/workflow/hooks/useWorkflowDesigner.ts`
- Create: `web/src/modules/workflow/pages/WorkflowDesignerPage.tsx`
- Create: `web/src/modules/workflow/components/WorkflowDesignerHeader.tsx`
- Create: `web/src/modules/workflow/components/WorkflowValidationPanel.tsx`
- Create: `web/src/modules/workflow/pages/WorkflowDesignerPage.test.tsx`

- [ ] **Step 1: Write failing designer journey tests**

Cover create, explicit save, dirty navigation confirmation, revision conflict preserving local reducer state, validation issue focus, publish disabled before current-revision validation, confirmation modal, and immutable version navigation.

- [ ] **Step 2: Verify RED**

Run: `npm --prefix web test -- src/modules/workflow/pages/WorkflowDesignerPage.test.tsx`
Expected: FAIL because the designer hook/page do not exist.

- [ ] **Step 3: Implement designer orchestration**

The hook loads server state into the reducer, saves with `expected_revision`, stores the revision returned by save, validates that revision, and invalidates validation on any later edit. A 409 never resets local graph state.

- [ ] **Step 4: Implement validation focus and publication**

Validation issues carry `node_id` or `edge_id`; clicking an issue selects and centers that element. Publish uses `Modal.confirm` and describes immutable-version consequences. Success notification duration is at most two seconds; failures persist.

- [ ] **Step 5: Run and commit**

Run: `npm --prefix web test -- src/modules/workflow`
Expected: PASS.

```bash
git add web/src/modules/workflow
git commit -m '[feat](workflow): save validate and publish graphs'
```

### Task 6: Add Read-Only Version View And Browser E2E

**Files:**

- Create: `web/src/modules/workflow/pages/WorkflowVersionPage.tsx`
- Create: `web/src/modules/workflow/pages/WorkflowVersionPage.test.tsx`
- Create: `web/e2e/workflow-admin-designer.spec.ts`

- [ ] **Step 1: Write failing read-only tests**

Assert version DAG and input schema render without mutation handles, save, validate, publish, or node palette.

- [ ] **Step 2: Implement and run component tests**

Run: `npm --prefix web test -- src/modules/workflow/pages/WorkflowVersionPage.test.tsx`
Expected: PASS after the minimal read-only page is added.

- [ ] **Step 3: Add desktop Playwright admin journey**

Using a real admin session, create a workflow containing all five node types, configure inputs, save, validate, publish, refresh, and inspect the immutable version. Assert API responses and persisted read-back, not only toasts.

- [ ] **Step 4: Add mobile gate assertion**

At a phone viewport, assert catalog/version pages remain usable and the edit route shows a clear desktop-required state without mounting the graph editor.

- [ ] **Step 5: Run release checks and commit**

Run: `make fe-lint && make fe-build`
Expected: PASS.

Run: `npm --prefix web exec playwright test web/e2e/workflow-admin-designer.spec.ts --reporter=list`
Expected: PASS with desktop journey and mobile gate.

```bash
git add web/src/modules/workflow web/e2e/workflow-admin-designer.spec.ts
git commit -m '[test](workflow): verify admin designer journey'
```
