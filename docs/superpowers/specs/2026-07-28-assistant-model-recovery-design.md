# Platform Assistant Model Recovery Design

## Context

A real production journey reproduced this sequence for the tenant-managed platform assistant:

1. A user creates or opens a conversation while the tenant has no assistant model configured.
2. `POST /agents/stratum-platform-assistant/execute/stream` returns HTTP 503.
3. The backend log preserves the root cause as `system assistant model unavailable`.
4. The frontend renders only `internal server error`, with no recovery action.

The backend correctly fails closed and does not fall back to a platform model or credential. The defect is the loss of a safe domain error contract between the application, HTTP middleware, streaming client, and role-aware chat UI.

The same journey also found two false-negative acceptance defects: the remote verification script calls PostgreSQL's nonexistent `jsonb_object_length()` function, and the browser fixture rejects UUIDv7 identifiers produced by the platform.

## Goals

- Preserve HTTP 503 when the tenant assistant model is unavailable.
- Return one stable, safe error code and Chinese message from synchronous and streaming execution paths.
- Let a tenant administrator open the existing assistant settings modal directly from the failure state.
- Tell a non-administrator to contact a tenant administrator.
- Keep model selection and provider credentials tenant-scoped and fail closed.
- Repair the remote verification SQL and UUID validation so valid production behavior is not reported as failure.

## Non-goals

- Do not add a platform default model or provider fallback.
- Do not expose provider names, API keys, internal wrapped errors, or upstream responses.
- Do not add a readiness request before every message. The server error contract remains authoritative and handles configuration races.
- Do not prevent conversation creation when the model is missing. Conversations remain useful durable user state; execution is the gated operation.
- Do not implement standalone Skill evaluation, stateful soak attestation, or other evaluation-loop gaps in this change.

## Error Contract

The public contract for `agentdomain.ErrAssistantModelUnavailable` is:

```http
HTTP/1.1 503 Service Unavailable
Content-Type: application/json

{
  "error": "租户尚未配置平台助手模型",
  "code": "SYSTEM_ASSISTANT_MODEL_UNAVAILABLE"
}
```

The middleware will use an explicit allowlist of public errors. Unknown 5xx errors continue to return only `internal server error` and have no public code. Wrapped sentinel errors must retain the same public contract through `errors.Is`.

The synchronous execution endpoint delegates to the middleware and returns the JSON contract above. The streaming endpoint has two failure phases:

- Before SSE headers are committed, it delegates to the middleware and returns the same HTTP 503 JSON contract.
- After streaming starts, a terminal SSE data event carries the same `error` and `code`. Other execution failures retain a generic safe message and must not serialize `runErr.Error()` to the client.

This keeps HTTP and SSE behavior aligned without treating an SSE event as an HTTP status after headers have been written.

## Frontend Data Flow

The shared streaming client parses structured error responses into an application error that preserves HTTP status, public code, and safe message. The Agent API also preserves `code` from terminal SSE errors. It must not reduce either form to a plain string before the chat state consumes it.

The chat stream state records a structured execution failure. The chat page derives recovery UI only when all of these are true:

- the selected resource is the managed platform assistant;
- the failure code is `SYSTEM_ASSISTANT_MODEL_UNAVAILABLE`;
- the current tenant role is known.

For administrators, the error message includes a `设置助手模型` command. Activating it opens the existing `SystemAssistantModelModal`. After a successful save, the page updates the selected assistant's model and clears the obsolete recovery state; the user explicitly retries the message, so the UI never duplicates an execution automatically.

For members, the error message is `租户尚未配置平台助手模型，请联系租户管理员配置` and has no settings command. Authorization continues to be enforced by the backend even if frontend state is manipulated.

Unrelated stream failures continue to use the existing non-expiring error presentation and have no settings recovery action.

## Acceptance Harness Corrections

The remote verification script will count configured provider entries using a subquery over `jsonb_object_keys(COALESCE(...))`, matching supported PostgreSQL JSONB functions. It will preserve the current exact tenant/admin identity predicates.

The real browser fixture will accept RFC 4122 UUID variants with version nibbles 1 through 8, including UUIDv7, while retaining the variant-nibble check. It will not loosen tenant and user identifiers to arbitrary strings; fixed resource IDs remain allowed only through the separate `requireResourceID` contract.

## Security And Observability

- Server logs retain the wrapped internal error with trace, tenant, path, and status metadata.
- Public responses contain only the allowlisted code and Chinese message.
- Provider credentials remain server-side and are never included in the response, SSE event, frontend state, trace, or test output.
- Missing or invalid model configuration never falls back to another tenant or platform configuration.
- Tests assert that wrapped internal context and representative secret-like strings do not appear in the public response.

## Test Strategy

Backend RED tests will establish:

- wrapped `ErrAssistantModelUnavailable` maps to HTTP 503 plus the stable public code and message;
- unknown 5xx errors remain generic and code-free;
- synchronous and pre-stream execution failures share the same contract;
- post-start SSE failure uses a safe structured event and does not expose the wrapped error.

Frontend RED tests will establish:

- a non-2xx stream response preserves status, code, and message;
- a terminal SSE error preserves the same code;
- an administrator sees and can activate `设置助手模型` after this specific failure;
- a member sees contact-admin guidance without the settings action;
- unrelated failures do not open or advertise assistant settings;
- saving settings clears the stale recovery state without automatically resending the message.

Harness tests will establish:

- UUIDv7 tenant/resource identifiers pass the appropriate validator while malformed values fail;
- the remote verification SQL uses supported JSONB functions.

After unit, lint, build, and risk guardrails pass, a real headless-browser journey will verify both roles against a deployed or isolated stack. Evidence will include the HTTP response, user-visible recovery state, successful model configuration and retry, backend logs, trace identity, and exact cleanup of temporary identities and resources. No credential or raw sensitive payload may be printed or persisted.

## Delivery Boundary

This change is complete only when the production-style journey proves that a missing model is actionable rather than merely translated. The broader evaluation and evolution goal remains open: standalone Skill execution, immutable evaluation profiles, four-resource promotion/rollback closure, and system attestation require separate designs and verification cycles.
