# Remote Acceptance Remediation Design

## Context

Remote acceptance confirmed that the core assistant conversation works, but four defects prevent the Demo environment
from meeting the product contract:

1. internal tool-observation summaries are rendered as ordinary assistant messages after refresh;
2. the Demo environment does not run Opik, so execution history and diagnostic evidence return 503;
3. the platform-managed assistant exposes its complete system prompt through member-readable Agent APIs; and
4. guest login produced one transient 500 that cannot be reconstructed because the serving Pod and its logs were replaced.

Diagnostic artifacts also record `evidence_unavailable` without a structured recommended action. This design fixes all
five behaviors while preserving tenant isolation, frozen HTTP error bodies, and the existing Opik evidence contract.

## Decisions

### Message visibility

`chat_messages` will gain a non-null `visibility` column with a safe default of `user`. Supported values are `user` and
`internal`. Historical rows therefore remain visible without a backfill window.

`ChatMessage` carries the visibility value through the application and persistence layers. User input and final assistant
answers are stored as `user`; the tool-observation summary is stored as `internal`. The repository continues returning both
classes because Agent execution uses the same history port. The HTTP `ListMessages` boundary filters internal messages
before mapping wire responses. No content heuristic or `SkipOutbox` inference is permitted: `SkipOutbox` controls memory
publication, not user visibility.

The tenant schema baseline adds the column before its constraint. Tests cover a historical schema upgrade, storage
round-trip, execution history retention, and refreshed HTTP reads that contain only the two user-visible messages.

### Opik deployment and trace flow

Opik will be a separate Helm release in an `opik` namespace using the official chart pinned to version 2.1.32. Stratum will
not vendor or reproduce Opik's templates. A repository-owned Demo values overlay will bound replicas, resources, storage,
and optional components for the single-node environment. Persistent state remains in Opik-managed PVCs.

The deployment workflow will install or upgrade Opik before Stratum and wait for all required dependencies and the backend
readiness endpoint. It will then deploy an OpenTelemetry Collector configuration that receives Stratum OTLP traffic and
exports it to Opik's private OTLP endpoint. Stratum's `OPIK_URL` points to the in-cluster Opik backend API. Both the write
path and read path must be configured; deploying only the Opik UI/backend is not sufficient.

The workflow pins the chart and application versions, uses explicit timeouts, and fails the candidate deployment when Opik
or its collector is not ready. Diagnostics must not silently deploy Stratum with an empty `OPIK_URL`. The public Demo does
not expose Opik ingress as part of this change; Stratum uses ClusterIP services.

Remote capacity evidence on 2026-07-24 showed a roughly 32 GiB node using about 5 GiB before Opik. The values overlay must
still set requests and limits because capacity at one point in time is not an isolation control.

### Managed prompt confidentiality

The platform-managed assistant's `systemPrompt` is never returned by `/agents` or `/agents/:id`, regardless of tenant role.
The field remains present as an empty string to preserve the current JSON shape. The runtime profile source and model-only
settings endpoint remain unchanged. Custom Agent management behavior is outside this repair and remains compatible.

Sanitization occurs in the transport mapping based on the domain's managed identity, not on the display name and not in the
frontend. Tests cover both list and detail mapping and prove an ordinary custom Agent still maps according to the existing
contract.

### Guest provisioning recovery

The observed guest-login 500 predates the current backend Pod, so its precise SQL or transaction failure is unknown. The
repair therefore targets the demonstrated recovery gap without claiming an unverified cause.

Guest provisioning will be idempotent for the identity generated once by `CreateGuest`. Repeating the repository operation
with the same synthetic GitHub identity must resolve the same user, ensure membership in the default tenant, and return the
same IDs. The repository retries only explicitly classified transient PostgreSQL failures, with bounded attempts,
exponential backoff, context cancellation, and no retry for constraints, missing default tenant, validation, or other
permanent failures. Commit-ambiguity recovery relies on the idempotent identity, preventing duplicate guest accounts.

Logs record the operation stage, attempt, SQLSTATE classification, and request trace correlation through the existing
context. They must not contain access tokens, refresh tokens, cookies, database credentials, or raw sensitive payloads.
Tests inject retryable begin/query/commit failures, permanent failures, cancellation during backoff, idempotent replay, and
rollback behavior.

### Deterministic diagnostic actions

`BuildDiagnosticReport` maps known evidence-gap codes to bounded, deterministic recommended actions. At minimum,
`evidence_unavailable` produces an action to check the Opik backend, OTEL Collector export path, and dependency health.
Duplicate gaps produce one action, unknown codes do not invent advice, and action strings still pass existing artifact
sanitization and size limits. The model's prose may add context but is not the source of the persisted control response.

## Error Handling And Compatibility

- Tenant-scoped reads and writes continue through the existing tenant transaction wrapper.
- Internal-message filtering is fail closed at the HTTP boundary: only explicit user-visible messages are rendered.
- Opik unavailability remains a 503 evidence error; deployment readiness prevents it from becoming the normal Demo state.
- Existing response keys and the frozen `{"error":"internal server error"}` body remain unchanged.
- Managed prompt redaction keeps `systemPrompt` in JSON as an empty string to avoid an API shape break.
- Guest retry exhaustion propagates the last wrapped error; no false 201 is returned.

## Verification

Implementation follows test-driven development, one behavior at a time. Required verification includes:

1. focused Go tests for message visibility, managed DTO redaction, diagnostic actions, and guest transient classification;
2. PostgreSQL integration tests for tenant schema upgrade, visibility round-trip, guest idempotency, rollback, and failure
   propagation;
3. Helm lint/template and deployment-safety tests proving a pinned Opik release, bounded resources, non-empty `OPIK_URL`,
   collector export configuration, and readiness failure propagation;
4. the existing Opik integration contract against version 2.1.32;
5. a real backend/API/database chain that creates a conversation, invokes a tool, reloads messages, and observes exactly
   the user request and final answer;
6. member API checks proving the managed prompt is absent while the assistant remains executable;
7. repeated and fault-injected guest login checks proving either one 201 identity or an explicit failure without duplicate
   durable users; and
8. remote Demo verification that a new execution reaches Opik and is returned by `/api/agents/executions`, with a
   structured recommended action when evidence is deliberately made unavailable in a controlled test.

Before completion, run the repository risk guardrails, `go vet`, short Go tests, race tests required by the affected
packages, frontend checks if frontend code changes, Helm checks, and the task-specific end-to-end flow. Temporary scripts
and processes must be removed or stopped.

## Evidence Boundaries

Repository code, tests, the live K3s workload inventory, sanitized request-correlated logs, and Opik 2.1.32 upstream chart
are the primary evidence. Obsidian material informs Agent governance but does not override repository or runtime facts. The
lost guest request log remains an explicit evidence gap rather than a diagnosed database incident.
