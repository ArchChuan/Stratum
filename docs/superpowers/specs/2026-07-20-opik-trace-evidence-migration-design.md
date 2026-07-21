# Opik Trace Evidence Migration Design

## Goal

Move Agent execution observability and evaluation evidence out of tenant PostgreSQL trace tables. Opik becomes the
durable Trace/Span query and evaluation-evidence backend, encrypted object storage holds large payloads, and PostgreSQL
retains only transactional business control state.

## Scope

The runtime must stop reading and writing these observation tables:

- `agent_executions`
- `agent_tool_traces`
- `agent_trace_events`

The following PostgreSQL state remains because it is transactional business or resumable runtime state rather than
observability evidence:

- `agent_execution_checkpoints`
- `agent_tool_approvals`
- `evaluation_feedback`, `evaluation_jobs`
- `evaluation_experiments`, `evaluation_deployments`
- `optimization_jobs`, `optimization_candidates`
- Agent, Skill, MCP, Memory, Knowledge, IAM, and Workflow domain state

Physical table removal is a final migration step after historical-data disposition and production parity gates pass.
New application runtime paths must not use the three observation tables before that migration is enabled.

## Evidence And Version Constraints

Repository facts:

- The current Agent service writes all three observation tables and exposes execution, tool-trace, and trace-event APIs
  from them.
- Evaluation feedback currently resolves Skill revision and online experiment latency/status/cost through
  `agent_tool_traces` and `agent_trace_events`.
- The deployed Collector configurations use Collector Contrib `0.96.0` and `0.102.0` variants.

Upstream Opik facts checked against `comet-ml/opik` on 2026-07-20:

- Native OTel ingestion currently supports OTLP over HTTP, at `/api/v1/private/otel/v1/traces`; gRPC is not supported.
- Trace and Span REST resources are exposed at `/api/v1/private/traces` and `/api/v1/private/spans` with project,
  filter, pagination, and trace-ID query capabilities.
- OTel IDs are mapped to Opik IDs by the backend. A Stratum business trace ID therefore remains an explicit attribute,
  not an assumption that the two IDs are identical.
- `opik.metadata.*` attributes map explicitly into Opik metadata. Generic unmapped OTel attributes may be assigned to
  another default bucket, so all fields used by Stratum queries must also be emitted under `opik.metadata.stratum.*`.

The relevant Obsidian notes found for this topic are provisional migration notes without verified OTel/Opik claims;
they do not override repository or upstream evidence.

## Architecture

```text
Agent runtime
  |-- OTel low-volume attributes --------------------------+
  |                                                       |
  |-- encrypted payload writer --> MinIO evidence bucket  |
  |                         |                             |
  |                         +--> payload_ref + sha256 ----+
  v                                                       v
OTel Collector -- tail sampling --> Opik OTLP/HTTP --> Opik Trace/Span store
                                                       |
                                                       v
                           TraceEvidenceProvider <--- Opik REST adapter
                                |          |
                         Agent query API   Evaluation application

PostgreSQL: feedback, experiments, deployments, optimization, jobs,
            checkpoints, approvals, and domain configuration only
```

### Dependency Direction

The Agent and Evaluation consumers define their own minimal evidence ports. An Opik infrastructure adapter implements
those ports. Application packages never import Opik transport DTOs or HTTP concerns. Shared HTTP mechanics may use
`pkg/httpclient`, but `pkg/` must not import `internal/`.

## Telemetry Contract

The root `agent.execute` span carries:

- tenant, user, business trace, execution, conversation, and Agent IDs;
- evaluation and security flags;
- every experiment assignment and resource revision;
- final execution status, duration, token totals, and total estimated cost.

Every `react.llm` span carries model/provider, step, token usage, cost, latency, status, error metadata, input/output hash,
and optional payload references.

Every `react.tool` span carries tool call ID/name, provider/server/capability, selected resource revision, latency,
status, arguments/result hash, and optional payload references.

Fields required by Stratum queries are emitted twice when needed:

- standards/domain attribute, for example `gen_ai.usage.input_tokens` or `stratum.trace.id`;
- Opik query metadata alias, for example `opik.metadata.stratum.trace_id`.

No secret, credential, unrestricted PII, or large raw payload is stored in Span attributes.

## Payload Storage

Large or sensitive Tool/LLM input and output uses a `TracePayloadStore` consumer-side port. The MinIO adapter:

- applies the existing recursive sensitive-key redaction before serialization;
- encrypts each object with the platform AES key using authenticated encryption;
- uses a tenant-scoped key prefix and a non-guessable object ID;
- stores content type, byte length, SHA-256, and retention metadata;
- returns an opaque `payload_ref`; callers never expose MinIO credentials or a public URL.

Spans contain only `payload_ref`, SHA-256, byte length, and storage status. Payload storage failure does not fail Agent
execution and never causes raw content to fall back into OTel. It records `payload.storage_status=error` and a bounded,
non-sensitive error classification.

Payload capture remains opt-in. With capture disabled, hashes and searchable evidence remain available without writing
raw payload objects.

## Opik Integration

The Collector adds an `otlphttp/opik` exporter. Configuration supplies endpoint, project, workspace, and authorization
headers through environment expansion; no credentials enter Git. Jaeger may remain as an optional operational exporter,
but Opik is the authoritative Agent evidence backend.

The Opik adapter uses bounded HTTP timeouts, tenant/project scoping, response-size limits, pagination limits, and explicit
error translation:

- unavailable, timeout, or 5xx -> domain evidence-unavailable error -> HTTP `503`;
- missing trace -> not-found error -> HTTP `404`;
- invalid/mismatched tenant evidence -> not-found, without leaking cross-tenant existence;
- malformed upstream data -> evidence-invalid error -> HTTP `502`.

Agent execution is decoupled from synchronous Opik availability because OTel export is asynchronous. Trace queries and
feedback that requires validation fail explicitly while evidence is unavailable; they never fall back to PostgreSQL.

## Query Compatibility

Existing API routes remain stable:

- `GET /agents/executions`
- `GET /agents/executions/:traceID/tool-traces`
- `GET /agents/executions/:traceID/trace-events`

The application maps Opik Trace/Span responses back to the existing public DTOs. Large raw fields are represented by
payload references and availability metadata, not transparently downloaded. A separate authorized payload retrieval
path may be added only if a product workflow requires it.

Execution listing filters by tenant metadata and uses Opik pagination. Trace detail first resolves the Opik trace by
tenant plus `stratum.trace_id`, then queries spans using the resulting Opik trace ID.

## Feedback And Experiment Evaluation

Feedback submission continues to persist `evaluation_feedback`, but evidence attribution changes:

1. Resolve the trace through `TraceEvidenceProvider` with tenant isolation.
2. Locate the requested resource assignment in the trace evidence.
3. Validate request resource ID, revision ID, experiment ID, and variant against observed evidence.
4. Persist the validated feedback and idempotency key.
5. Optionally mirror the score to Opik as a secondary visualization action; PostgreSQL remains authoritative for
   Stratum rollout state.

Online experiment observations join PostgreSQL feedback rows with Opik evidence in application code. The provider
returns bounded bulk observations for trace IDs so the implementation does not issue one HTTP request per feedback row.
Metrics include score, success, security violation, latency, token usage, and cost.

Feedback and experiment evaluation return `503` when Opik evidence cannot be loaded. They do not make rollout decisions
from partial samples.

## Sampling And Retention

Evaluation, canary/experiment, execution failure, and runtime-known security-event traces remain 100% retained by tail
sampling. Ordinary traces remain sampled according to configuration.

Any trace eligible for post-hoc feedback must be retained at 100% before the sampling decision. Because a security flag
submitted later cannot recover a dropped trace, the product must either mark feedback-eligible traces at execution time
or treat absent evidence as an explicit unavailable outcome. This design does not silently accept unverifiable feedback.

Collector topology must route all spans for a trace to the same tail-sampling Collector instance, as required by the
Collector processor. Multi-replica deployment therefore needs trace-ID-aware load balancing before horizontal scaling.

## Migration

### Final Data Disposition Decision

On 2026-07-20 the historical rows in `agent_executions`, `agent_tool_traces`, and `agent_trace_events` were explicitly
approved for permanent deletion without archive or migration into Opik. Real Collector-to-Opik parity, tenant
isolation, failure/security retention, metric mapping, and feedback rollback gates passed before deletion. Canonical
tenant provisioning now drops the tables idempotently; immutable public migration history remains unchanged.

### Phase 1: Runtime Cutover

- Add Opik and payload-store adapters and configuration.
- Route OTel to Opik over HTTP.
- Switch Agent query and Evaluation evidence ports to Opik.
- Stop wiring PostgreSQL execution, tool-trace, and trace-event repositories.
- Keep old tables read-only and leave their DDL intact.

### Phase 2: Parity Gate

- Run deterministic Agent/LLM/Tool executions through Collector and Opik.
- Verify execution listing, hierarchy, evidence attribution, costs, failures, tenant isolation, and feedback decisions.
- Verify Opik and MinIO outage behavior.
- Decide whether historical rows are exported, retained for a fixed archive period, or deleted under an approved data
  governance decision.

### Phase 3: Physical Removal

- Remove obsolete repositories, ports, tests, DDL, indexes, and API fallback code.
- Add an idempotent tenant-schema migration that drops the three tables only after the deployment no longer references
  them.
- Verify both new and historical tenant schema provisioning.

## Testing

- Unit tests for attribute aliases, Opik DTO mapping, pagination, tenant filtering, error translation, payload encryption,
  redaction, and feedback evidence validation.
- Contract tests for Collector OTLP/HTTP exporter and required 100% sampling policies.
- HTTP handler tests preserving response shape and `404/502/503` mapping.
- Integration tests with a fake Opik server proving no PostgreSQL trace repository is called.
- Real E2E with self-hosted Opik, Collector, MinIO, Agent execution, Trace APIs, feedback, and experiment observation.
- Schema tests proving Phase 1 keeps old tables while runtime has zero references; Phase 3 drop migration is a separate
  approved change.

## Acceptance Criteria

- New Agent executions write no rows to the three observation tables.
- Agent execution succeeds while Opik or MinIO is unavailable.
- Evidence APIs and feedback return explicit errors when Opik evidence is unavailable.
- Trace APIs preserve their public JSON contracts while reading Opik.
- Evaluation rollout metrics are derived from Opik evidence plus PostgreSQL feedback/control state.
- Large/sensitive payloads do not enter PostgreSQL or OTel attributes.
- Tenant-scoped evidence cannot be queried across tenants.
- Full Go verification and real Opik/Collector/MinIO E2E pass before physical table removal is proposed.
