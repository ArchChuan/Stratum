# Platform Assistant Model Recovery Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make a missing tenant platform-assistant model a safe, role-aware, recoverable failure across synchronous HTTP, SSE, and the real acceptance harness.

**Architecture:** The backend owns an allowlisted public error descriptor used by both middleware JSON responses and terminal SSE events. The frontend transports status/code/message as a structured `StreamRequestError`, while Agent chat state and UI interpret only the stable assistant-model code. Acceptance helpers remain strict but support PostgreSQL JSONB semantics and UUIDv7.

**Tech Stack:** Go 1.25, Gin, React 18, TypeScript, Vitest, Testing Library, Playwright, Bash, PostgreSQL.

---

## Task 1: Backend public error contract

**Files:**

- Create: `api/middleware/public_error.go`
- Create: `api/middleware/public_error_test.go`
- Modify: `api/middleware/middleware.go:48-69`
- Test: `api/middleware/middleware_test.go`

- [ ] **Step 1: Write failing descriptor and middleware tests**

Add table tests proving that a wrapped `agentdomain.ErrAssistantModelUnavailable` produces status 503, code `SYSTEM_ASSISTANT_MODEL_UNAVAILABLE`, and message `租户尚未配置平台助手模型`, while `errors.New("provider secret=hidden")` produces `internal server error` with an empty code. Add an HTTP recorder test that verifies the exact JSON body and asserts `provider secret=hidden` is absent.

- [ ] **Step 2: Run the tests and verify RED**

Run:

```bash
go test ./api/middleware -run 'Test(PublicError|ErrorHandler)' -count=1
```

Expected: FAIL because `PublicError` and the coded JSON response do not exist.

- [ ] **Step 3: Add the allowlisted descriptor**

Implement this focused API in `public_error.go`:

```go
const CodeSystemAssistantModelUnavailable = "SYSTEM_ASSISTANT_MODEL_UNAVAILABLE"

type PublicErrorDescriptor struct {
    Message string
    Code    string
}

func DescribePublicError(err error, status int) PublicErrorDescriptor {
    if errors.Is(err, agentdomain.ErrAssistantModelUnavailable) {
        return PublicErrorDescriptor{
            Message: "租户尚未配置平台助手模型",
            Code:    CodeSystemAssistantModelUnavailable,
        }
    }
    if status >= http.StatusInternalServerError {
        return PublicErrorDescriptor{Message: "internal server error"}
    }
    return PublicErrorDescriptor{Message: err.Error()}
}
```

Update `ErrorHandler` to call the descriptor. Add `code` only when non-empty; preserve workflow `issues`, 429 `Retry-After`, structured logging, and the existing status map.

- [ ] **Step 4: Run middleware tests and verify GREEN**

Run:

```bash
go test ./api/middleware -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the backend contract**

```bash
git add api/middleware/public_error.go api/middleware/public_error_test.go api/middleware/middleware.go api/middleware/middleware_test.go
git commit -m "[fix](api): expose safe assistant model error contract"
```

## Task 2: Safe terminal SSE error contract

**Files:**

- Modify: `api/http/handler/agent_exec_handler.go:136-151`
- Modify: `api/http/handler/agent_exec_handler_test.go`

- [ ] **Step 1: Write failing SSE payload tests**

Add `TestAgentExecutionErrorPayloadUsesPublicContract` with two cases. A wrapped assistant-model sentinel must decode to exactly `error` and `code`; an unknown error containing `api_key=do-not-leak` must decode to `{"error":"internal server error"}` and must not contain the secret-like text.

- [ ] **Step 2: Run the handler test and verify RED**

```bash
go test ./api/http/handler -run 'TestAgentExecutionErrorPayload' -count=1
```

Expected: FAIL because execution errors currently serialize `runErr.Error()`.

- [ ] **Step 3: Implement a shared safe SSE payload helper**

Add `agentExecutionErrorPayload(err error) []byte`. It calls `middleware.MapErrorToStatus` and `middleware.DescribePublicError`, marshals `error`, and conditionally includes `code`. Replace the raw `runErr.Error()` payload in `ExecuteAgentStream`; retain full internal logging.

- [ ] **Step 4: Run handler and middleware tests**

```bash
go test ./api/http/handler ./api/middleware -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit SSE safety**

```bash
git add api/http/handler/agent_exec_handler.go api/http/handler/agent_exec_handler_test.go
git commit -m "[fix](agent): align streaming execution errors"
```

## Task 3: Preserve structured stream failures in the frontend

**Files:**

- Modify: `web/src/services/client.ts:180-187`
- Modify: `web/src/services/client.test.ts`
- Modify: `web/src/modules/agent/model/agent.ts:153-181`
- Modify: `web/src/modules/agent/api/agent.api.ts:83-119`
- Create: `web/src/modules/agent/api/__tests__/agent.api.test.ts`

- [ ] **Step 1: Write failing HTTP and SSE transport tests**

In `client.test.ts`, return a 503 JSON response with `error` and `code`, then assert `onError` receives an error whose `message`, `status`, and `code` are preserved. In the Agent API test, send an SSE event with the same fields and assert the callback receives the same structured values.

- [ ] **Step 2: Run focused frontend tests and verify RED**

```bash
/home/yang/go-projects/stratum/web/node_modules/.bin/vitest run web/src/services/client.test.ts web/src/modules/agent/api
```

Expected: FAIL because current errors preserve only `message`.

- [ ] **Step 3: Implement `StreamRequestError`**

Export this infrastructure type from `client.ts`:

```ts
export class StreamRequestError extends Error {
  constructor(
    message: string,
    public readonly status?: number,
    public readonly code?: string,
  ) {
    super(message);
    this.name = 'StreamRequestError';
  }
}
```

Parse `{ error, message, code }` into `StreamRequestError(message, response.status, code)`. Extend `StreamCallbacks.onError` and terminal Agent SSE parsing to preserve the type and code rather than constructing a plain `Error`.

- [ ] **Step 4: Run focused frontend tests and verify GREEN**

Run the command from Step 2. Expected: PASS.

- [ ] **Step 5: Commit structured transport errors**

```bash
git add web/src/services/client.ts web/src/services/client.test.ts web/src/modules/agent/model/agent.ts web/src/modules/agent/api/agent.api.ts web/src/modules/agent/api
git commit -m "[fix](web): preserve streaming error codes"
```

## Task 4: Add role-aware chat recovery

**Files:**

- Modify: `web/src/modules/agent/hooks/ChatStreamContext.tsx`
- Modify: `web/src/modules/agent/hooks/useChatPage.ts`
- Modify: `web/src/modules/agent/model/agent.ts`
- Modify: `web/src/modules/agent/pages/AgentChatPage.tsx`
- Modify: `web/src/modules/agent/pages/__tests__/AgentChatMobile.test.tsx`
- Modify: `web/src/modules/agent/hooks/__tests__/useChatPage.test.tsx`

- [ ] **Step 1: Write failing member/admin recovery tests**

Extend the page mock with `streamFailure` and `clearStreamFailure`. For a selected system assistant and code `SYSTEM_ASSISTANT_MODEL_UNAVAILABLE`, assert an admin sees `租户尚未配置平台助手模型` plus button `设置助手模型`, and clicking it opens the existing dialog. Assert a member sees `租户尚未配置平台助手模型，请联系租户管理员配置` with no settings action. Assert ordinary-Agent and unrelated-error cases show no recovery action.

Add a hook test proving the structured failure is associated with the failed assistant message and that model-save clearing does not call `startStream` again.

- [ ] **Step 2: Run page and hook tests and verify RED**

```bash
/home/yang/go-projects/stratum/web/node_modules/.bin/vitest run \
  web/src/modules/agent/pages/__tests__/AgentChatMobile.test.tsx \
  web/src/modules/agent/hooks/__tests__/useChatPage.test.tsx
```

Expected: FAIL because stream state exposes only a string and the page has no recovery alert.

- [ ] **Step 3: Carry and clear structured failure state**

Define `AgentExecutionFailure { message: string; code?: string; status?: number }`. Replace internal `error: string | null` with this type, retaining `streamError` as the rendered safe message if needed for existing consumers. Expose `streamFailure` and `clearStreamFailure`; guard callbacks by the active controller as today. Clear stale failure when starting a new stream, selecting a different Agent/conversation, or successfully saving the system assistant model.

- [ ] **Step 4: Render the recovery alert**

In `AgentChatPage`, derive `assistantModelUnavailable` from the selected system assistant and exact public code. Render an unframed Ant Design `Alert` above the composer. Admin description contains a `设置助手模型` button wired to `setSettingsTargetID(agentObj.id)`; member description contains only contact-admin guidance. In `onSaved`, update the authoritative page model, clear the failure, and close the modal without calling `handleSend`.

- [ ] **Step 5: Run Agent frontend tests and verify GREEN**

```bash
/home/yang/go-projects/stratum/web/node_modules/.bin/vitest run web/src/modules/agent
```

Expected: PASS.

- [ ] **Step 6: Commit recovery UI**

```bash
git add web/src/modules/agent
git commit -m "[fix](agent): add assistant model recovery action"
```

## Task 5: Repair acceptance harness false negatives

**Files:**

- Modify: `scripts/e2e/platform-assistant-remote-verify.sh:142-145`
- Modify: `scripts/e2e/platform-assistant-remote-verify-test.sh`
- Modify: `web/e2e/support/real-platform-assistant.ts:5`
- Create: `web/e2e/support/real-platform-assistant.test.ts`

- [ ] **Step 1: Write failing SQL and UUIDv7 tests**

Extend the shell test with a static assertion that the verifier contains no `jsonb_object_length` and does contain `jsonb_object_keys` for configured identity. Add Vitest cases where `requireUUID('01981f2e-7b3a-7d32-8a21-123456789abc', 'id')` passes and malformed version/variant strings fail.

- [ ] **Step 2: Run harness tests and verify RED**

```bash
bash scripts/e2e/platform-assistant-remote-verify-test.sh
/home/yang/go-projects/stratum/web/node_modules/.bin/vitest run web/e2e/support/real-platform-assistant.test.ts
```

Expected: FAIL on unsupported JSONB function and UUID version 7.

- [ ] **Step 3: Correct SQL and UUID pattern**

Use an `EXISTS (SELECT 1 FROM jsonb_object_keys(COALESCE(t.settings->'llm_api_keys','{}'::jsonb)))` predicate for the exact configured tenant/admin identity. Change only the UUID version nibble class from `[1-5]` to `[1-8]`; retain the RFC variant class `[89ab]`.

- [ ] **Step 4: Run harness tests and verify GREEN**

Run the Step 2 commands. Expected: PASS.

- [ ] **Step 5: Commit harness corrections**

```bash
git add scripts/e2e/platform-assistant-remote-verify.sh scripts/e2e/platform-assistant-remote-verify-test.sh web/e2e/support/real-platform-assistant.ts web/e2e/support/real-platform-assistant.test.ts
git commit -m "[fix](e2e): accept valid assistant runtime evidence"
```

## Task 6: Regression, risk gates, and real journey

**Files:**

- Modify only if failures prove a defect in files already in scope.
- Evidence output: repository-versioned system attestation path selected by `risk-regression-guard.sh`.

- [ ] **Step 1: Run focused and package-level checks**

```bash
go test ./api/middleware ./api/http/handler -count=1
/home/yang/go-projects/stratum/web/node_modules/.bin/vitest run web/src/services/client.test.ts web/src/modules/agent web/e2e/support/real-platform-assistant.test.ts
make fe-lint
make fe-build
```

Expected: all PASS.

- [ ] **Step 2: Run repository guardrails**

```bash
make risk-guardrails
bash scripts/quality/risk-regression-guard.sh --acceptance \
  api/middleware/public_error.go api/http/handler/agent_exec_handler.go \
  web/src/services/client.ts web/src/modules/agent/pages/AgentChatPage.tsx \
  scripts/e2e/platform-assistant-remote-verify.sh web/e2e/support/real-platform-assistant.ts
```

Expected: guardrails PASS and acceptance mode is reported explicitly. If it reports `short`, run `make e2e-system-short`; if it reports `soak`, also run the configured soak command, then `make e2e-attestation-check`.

- [ ] **Step 3: Run the real role matrix without screenshots**

Use the repository `stratum-e2e-development` workflow against the isolated or deployed stack:

1. Create exact UUID-scoped member and admin test identities.
2. Ensure the test tenant has no assistant model without touching unrelated tenant settings.
3. Through headless Chromium, create/open a conversation and send a message.
4. Assert HTTP 503 plus the stable code, member guidance, and no settings action.
5. Repeat as admin, open the recovery action, configure a working tenant model through the existing modal, and retry explicitly.
6. Assert successful LLM/tool execution, execution record, backend trace/log correlation, and no secret in response/trace.
7. Precisely remove temporary users, memberships, refresh tokens, conversations, and test configuration; prove zero undeclared residual entities.

- [ ] **Step 4: Commit any evidence-contract updates**

Do not commit raw logs, tokens, cookies, keys, or temporary scripts. Commit only repository-defined attestation artifacts when the system runner requires them.

- [ ] **Step 5: Final verification and branch review**

```bash
git status --short
git log --oneline origin/main..HEAD
git diff --check origin/main...HEAD
```

Expected: clean worktree, the design and plan commits plus five focused implementation commits, and no whitespace errors.
