# Platform Assistant Model Settings Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Default the Stratum platform assistant to `glm-5.2`, move its model control to the Agent detail page, and remove the duplicate chat-page settings entry.

**Architecture:** Persist the default in the tenant schema and backfill only empty managed-assistant rows, while retaining the existing validated `/agents/system/settings` API as the sole mutation path. The Agent edit route branches on `isSystem`: ordinary Agents retain the full form, while the platform assistant renders a model-only form backed by the settings API. Chat becomes execution-only and no longer owns settings state.

**Tech Stack:** Go 1.25, PostgreSQL tenant schema, Gin, React 18, TypeScript, Ant Design 5, Vitest, Testing Library, Playwright.

---

## File Map

- Modify `pkg/constants/agent.go`: own the reviewed platform-assistant default model ID.
- Modify `pkg/storage/postgres/tenant_schema.sql`: seed new assistants and backfill only empty historical values.
- Modify `pkg/storage/postgres/tenant_schema_test.go`: freeze ordering, value consistency, and non-overwrite behavior.
- Modify `pkg/storage/postgres/tenant_schema_integration_test.go`: prove schema replay backfills empty values and preserves overrides.
- Create `web/src/modules/agent/components/SystemAssistantSettingsForm.tsx`: model-only detail form.
- Create `web/src/modules/agent/components/__tests__/SystemAssistantSettingsForm.test.tsx`: form loading and mutation contract.
- Modify `web/src/modules/agent/hooks/useEditAgentPage.ts`: load Agent identity first and use the proper mutation path.
- Modify `web/src/modules/agent/pages/EditAgentPage.tsx`: branch between managed and ordinary detail views.
- Create `web/src/modules/agent/pages/__tests__/EditAgentPage.test.tsx`: page-level managed/ordinary behavior.
- Modify `web/src/modules/agent/components/ChatHeader.tsx`: remove settings props and button.
- Modify `web/src/modules/agent/pages/AgentChatPage.tsx`: remove settings modal state and callbacks.
- Modify `web/src/modules/agent/hooks/useChatPage.ts`: remove the chat-only model update helper if unused.
- Delete `web/src/modules/agent/components/SystemAssistantModelModal.tsx`: remove the duplicate configuration surface.
- Modify Agent frontend tests that currently assert the chat settings entry.
- Create or modify `docs/e2e/platform-assistant-model-settings.md`: record real API, database, and browser evidence.

### Task 1: Persist The Reviewed Default Without Overwriting Tenant Choice

**Files:**

- Modify: `pkg/constants/agent.go`
- Modify: `pkg/storage/postgres/tenant_schema.sql`
- Modify: `pkg/storage/postgres/tenant_schema_test.go`
- Modify: `pkg/storage/postgres/tenant_schema_integration_test.go`

- [ ] **Step 1: Write failing schema contract tests**

Add a constants assertion and require the SQL seed and conditional backfill:

```go
func TestTenantSchemaDefaultsSystemAssistantModelWithoutOverwritingSelection(t *testing.T) {
    data, err := os.ReadFile("tenant_schema.sql")
    if err != nil { t.Fatal(err) }
    sql := string(data)
    for _, want := range []string{
        "'glm-5.2', '', 10, 8000, 'user', 'stratum.platform_assistant'",
        "UPDATE agents SET llm_model = 'glm-5.2'",
        "system_key = 'stratum.platform_assistant'",
        "BTRIM(llm_model) = ''",
    } {
        if !strings.Contains(sql, want) { t.Fatalf("missing %q", want) }
    }
    if constants.DefaultSystemAssistantModel != "glm-5.2" {
        t.Fatalf("default model = %q", constants.DefaultSystemAssistantModel)
    }
}
```

Extend the integration fixture with one empty managed row and one `qwen-plus` override, replay `tenant_schema.sql`, and assert the resulting models are respectively `glm-5.2` and `qwen-plus`.

- [ ] **Step 2: Run tests and verify the new contract fails**

Run:

```bash
go test ./pkg/storage/postgres -run 'TestTenantSchema(Default|ContainsSystemAssistant)' -count=1
```

Expected: FAIL because the constant, seed value, and conditional backfill do not exist.

- [ ] **Step 3: Add the constant and minimal idempotent SQL**

Add to `pkg/constants/agent.go`:

```go
const DefaultSystemAssistantModel = "glm-5.2"
```

Change the platform-assistant `VALUES` tuple to use `glm-5.2`, then add immediately after the identity check:

```sql
UPDATE agents
SET llm_model = 'glm-5.2', updated_at = NOW()
WHERE system_key = 'stratum.platform_assistant'
  AND BTRIM(llm_model) = '';
```

Do not update non-empty values and do not add tenant DDL to `pkg/migration/sql/`.

- [ ] **Step 4: Run focused schema tests**

Run:

```bash
go test ./pkg/storage/postgres -run 'TestTenantSchema(Default|ContainsSystemAssistant)' -count=1
```

Expected: PASS, including the historical-schema replay assertions when PostgreSQL integration prerequisites are available; otherwise the integration test must report its existing explicit skip.

- [ ] **Step 5: Commit the data contract**

```bash
git add pkg/constants/agent.go pkg/storage/postgres/tenant_schema.sql \
  pkg/storage/postgres/tenant_schema_test.go pkg/storage/postgres/tenant_schema_integration_test.go
git commit -m "[feat](agent): default platform assistant to glm-5.2"
```

### Task 2: Render A Model-Only Platform Assistant Detail Page

**Files:**

- Create: `web/src/modules/agent/components/SystemAssistantSettingsForm.tsx`
- Create: `web/src/modules/agent/components/__tests__/SystemAssistantSettingsForm.test.tsx`
- Modify: `web/src/modules/agent/hooks/useEditAgentPage.ts`
- Modify: `web/src/modules/agent/pages/EditAgentPage.tsx`
- Create: `web/src/modules/agent/pages/__tests__/EditAgentPage.test.tsx`

- [ ] **Step 1: Write failing component tests**

Mock `agentApi.getSystemSettings` and `agentApi.updateSystemSettings`. Assert the form has exactly one combobox, excludes prompt/Skill/MCP/knowledge fields, includes an unavailable current value, disables save during load, submits `{ llmModel }`, and uses persistent error plus two-second success notifications.

Core assertion:

```tsx
expect(within(form).getAllByRole('combobox')).toHaveLength(1);
expect(within(form).queryByText(/Prompt|Skill|MCP|知识库|Memory/)).not.toBeInTheDocument();
fireEvent.click(within(form).getByRole('button', { name: '保存修改' }));
await waitFor(() => expect(agentApi.updateSystemSettings).toHaveBeenCalledWith({
  llmModel: 'qwen-plus',
}));
```

Write page tests asserting `isSystem=true` renders this form and `isSystem=false` renders `AgentFormSections`.

- [ ] **Step 2: Run the new frontend tests and verify failure**

Run:

```bash
npm --prefix web test -- \
  src/modules/agent/components/__tests__/SystemAssistantSettingsForm.test.tsx \
  src/modules/agent/pages/__tests__/EditAgentPage.test.tsx
```

Expected: FAIL because the managed detail component and page branch do not exist.

- [ ] **Step 3: Implement the model-only form**

Create a focused component that loads settings on mount, constructs options as the union of the current model and available models, and marks a current unavailable model in its label:

```tsx
const options = [settings.llmModel, ...settings.availableModels]
  .filter((model, index, values) => model && values.indexOf(model) === index)
  .map((model) => ({
    value: model,
    label: model === settings.llmModel && !settings.ready ? `${model}（当前不可用）` : model,
  }));
```

Keep request-generation cancellation and mutation-generation checks from the existing modal so stale requests cannot update an unmounted page.

- [ ] **Step 4: Branch the edit hook and page by managed identity**

Return the loaded `agent` from `useEditAgentPage`. Skip Skill, MCP, and Knowledge lookups when the Agent is system-managed. For a managed Agent, render `SystemAssistantSettingsForm`; for an ordinary Agent, retain the existing `Form` and `AgentFormSections`. The managed save path must use only `agentApi.updateSystemSettings`.

- [ ] **Step 5: Run focused frontend tests**

Run the Task 2 command again.

Expected: PASS with no unhandled promise warnings.

- [ ] **Step 6: Commit the detail-page behavior**

```bash
git add web/src/modules/agent/components/SystemAssistantSettingsForm.tsx \
  web/src/modules/agent/components/__tests__/SystemAssistantSettingsForm.test.tsx \
  web/src/modules/agent/hooks/useEditAgentPage.ts \
  web/src/modules/agent/pages/EditAgentPage.tsx \
  web/src/modules/agent/pages/__tests__/EditAgentPage.test.tsx
git commit -m "[feat](agent): edit platform assistant model from details"
```

### Task 3: Remove The Chat-Page Configuration Surface

**Files:**

- Modify: `web/src/modules/agent/components/ChatHeader.tsx`
- Modify: `web/src/modules/agent/pages/AgentChatPage.tsx`
- Modify: `web/src/modules/agent/hooks/useChatPage.ts`
- Delete: `web/src/modules/agent/components/SystemAssistantModelModal.tsx`
- Modify: `web/src/modules/agent/components/__tests__/PlatformAssistant.test.tsx`
- Modify: `web/src/modules/agent/pages/__tests__/AgentChatMobile.test.tsx`
- Modify: `web/src/modules/agent/hooks/__tests__/useChatPage.test.tsx`

- [ ] **Step 1: Change tests to require an execution-only chat header**

Replace modal assertions with:

```tsx
render(<ChatHeader agent={systemAgent} isAdmin />);
expect(screen.getByText('系统内置')).toBeInTheDocument();
expect(screen.queryByRole('button', { name: '设置助手模型' })).not.toBeInTheDocument();
expect(screen.queryByText('助手设置')).not.toBeInTheDocument();
```

At page level assert the absence on desktop and mobile, while retaining the current model tag and member-facing unavailable hint.

- [ ] **Step 2: Run chat tests and verify failure**

Run:

```bash
npm --prefix web test -- \
  src/modules/agent/components/__tests__/PlatformAssistant.test.tsx \
  src/modules/agent/pages/__tests__/AgentChatMobile.test.tsx \
  src/modules/agent/hooks/__tests__/useChatPage.test.tsx
```

Expected: FAIL because the settings button and modal still exist.

- [ ] **Step 3: Remove the duplicate entry and dead state**

Remove `SettingOutlined`, `onOpenSettings`, and the settings button from `ChatHeader`. Remove `settingsTargetID`, its effects, the modal import/render, and `updateSystemAssistantModel` consumption from `AgentChatPage`. Delete the modal and remove the hook helper only after `rg` confirms no remaining consumer.

- [ ] **Step 4: Run chat tests and static reference checks**

Run:

```bash
npm --prefix web test -- \
  src/modules/agent/components/__tests__/PlatformAssistant.test.tsx \
  src/modules/agent/pages/__tests__/AgentChatMobile.test.tsx \
  src/modules/agent/hooks/__tests__/useChatPage.test.tsx
rg -n 'SystemAssistantModelModal|设置助手模型|onOpenSettings' web/src/modules/agent
```

Expected: tests PASS and `rg` returns no production references.

- [ ] **Step 5: Commit the chat cleanup**

```bash
git add -A web/src/modules/agent
git commit -m "[refactor](agent): remove chat model settings entry"
```

### Task 4: Verify API, Database, Browser, And Regression Boundaries

**Files:**

- Create or modify: `docs/e2e/platform-assistant-model-settings.md`

- [ ] **Step 1: Run focused backend and frontend suites**

```bash
go test ./pkg/storage/postgres ./internal/agent/application ./api/http/handler -count=1
npm --prefix web test -- src/modules/agent
make fe-lint
make fe-build
```

Expected: all commands PASS; skips must name missing external prerequisites.

- [ ] **Step 2: Run repository safety checks**

```bash
go vet ./...
go test -short ./...
make risk-guardrails
git diff --check origin/main...HEAD
```

Expected: all blocking checks PASS. Run `go test -v -race -timeout 30s ./...` before PR and report any pre-existing timeout separately rather than hiding it.

- [ ] **Step 3: Run the real tenant/API/browser flow using `stratum-e2e-development`**

Provision or upgrade a test tenant, then verify with sanitized evidence:

```text
DB: platform assistant model is glm-5.2 after an empty-value schema replay.
API: GET /agents/system/settings returns glm-5.2 and ready reflects tenant provider availability.
Browser: admin opens Agent list -> platform assistant edit -> changes model -> saves.
DB/API: the chosen override persists and tenant schema replay does not replace it.
Browser desktop/mobile: chat displays the model but has no model-settings control.
RBAC: member cannot open the admin edit route or call the settings mutation.
```

Do not print API keys, bearer tokens, cookies, or raw upstream responses.

- [ ] **Step 4: Record E2E evidence and commit**

Record commands, timestamps, environment scope, assertions, skips, and cleanup in `docs/e2e/platform-assistant-model-settings.md`.

```bash
git add docs/e2e/platform-assistant-model-settings.md
git commit -m "[test](agent): verify platform assistant model settings"
```

- [ ] **Step 5: Complete final review and knowledge gate**

Run the final diff/status review, request code review, fix confirmed findings, then generate the required report under `tmp/knowledge-deposition/`. Retain a knowledge candidate only if the work produces a new durable fact or regression lesson not already covered by the design and project docs.
