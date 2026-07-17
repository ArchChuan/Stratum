# Tiered Memory

## Implemented scope

Phase 0 Facts remains the long-term fact slice described in `memory-facts.md`.
Phase 1 adds one active short-term snapshot for each tenant/user/agent tuple.
Phase 2 evolves the existing tenant-local `memory_summaries` abstraction into
dynamic long-term History. It does not add a parallel memory table.

## Active snapshot

`memory_active_snapshots` is a tenant-schema table. Its unique `(user_id,
agent_id)` key makes refreshes overwrite the prior snapshot while incrementing
`version`. The structured `work_context`, `personal_context`, and `top_of_mind`
arrays are limited in the domain to 8 items per section, 240 runes per item,
and 1200 runes total. Source provenance contains only a type and a reference;
the pipeline stores the originating message ID, not the message payload.

Snapshots are active for 24 hours. Repository reads require `status = 'active'`
and `expires_at > NOW()`. Explicit scoped deletion is supported, and user/agent
memory lifecycle deletion also removes owned snapshots. All operations execute
inside the existing tenant `search_path` transaction pattern.

## Data flow

Chat continues to write the existing memory outbox. The raw event passes through
the existing embedder and enricher workers. The enricher asks for bounded active
context fields in its existing structured response and upserts the snapshot only
after normal enrichment persistence. Snapshot refresh is optional derived state: a missing source event timestamp is
skipped, and validation or persistence failure is logged and metered while the
already-persisted enrichment continues to Ack. This avoids replaying the LLM and
core enrichment for a snapshot-only failure. Snapshot ordering uses source event
time, and the repository updates only when that time is strictly newer, so stale
or equal retries cannot overwrite context, extend TTL, or increment `version`.

## Injection

The injector reads snapshots with a 500 ms timeout and treats errors as an empty
snapshot. Rendering has a shared 1800-character cap. Active snapshot content is
rendered first with a 600-character allocation, quality-filtered Facts follow,
and relevant History receives a reserved share of the shared budget before legacy
conversation summary/entities use any remainder. History retrieval is limited to
3 rows and 500 characters, ranks
trigram relevance before importance, confidence, and recency, and accepts user
scope plus the current agent scope only. Its 500 ms read timeout and query errors
degrade to no History. All sections remain inside the same 1800-character total;
source references and expiry metadata are not injected.

## Dynamic History

`memory_summaries` keeps legacy conversation summary rows compatible and adds
nullable History metadata. History rows carry user/agent scope, one of
`recent_months`, `earlier_context`, or `long_term_background`, period bounds,
first/last boundaries plus exact source IDs, importance, confidence, status, timestamps, and
a deterministic aggregation key. They do not copy raw source messages or secret
payloads. The partial unique aggregation-key index makes concurrent or retried
batches idempotent while leaving legacy summary rows unaffected.

The per-tenant History worker runs through the existing `TenantWatcher`. It
aggregates only after 5 enriched entries are available and reads at most 50
entries for one conversation/user/agent scope. The tenant LLM call runs outside
the repository transaction with a 30-second timeout. A missing provider, timeout,
or write error leaves the source window eligible for a later pass; maintenance
and fact archival still run independently, and no failure enters the chat path.

Recent segments older than 90 days promote to earlier context; earlier segments
older than 365 days promote to long-term background. Recent and earlier tiers are capped at 12 active segments per conversation/user/agent scope.
The repository returns one bounded overflow group with full summaries; the worker
calls the tenant compressor outside a database transaction. It then atomically
inserts or confirms an idempotent next-tier replacement whose key is derived from
the exact source IDs and archives only those IDs. Compressor, validation, or write
failure leaves every source segment active. No SQL `LEFT` truncation is used.
Background has no automatic deletion in Phase 2.

Facts inactive for 180 days may be archived only when both importance and
confidence are below 0.8. Preference-category facts and `explicit_user` facts
are always protected, so explicit preferences and goals represented as protected
facts are not automatically archived. Superseded/deleted retention remains owned
by the existing GC behavior.

## Schema and rollback

Canonical idempotent DDL and legacy-tenant backfill live in
`pkg/storage/postgres/tenant_schema.sql`. Migrations `021` and `022` are paired
public schema markers and contain no tenant DDL. Their down markers intentionally
preserve tenant data. Operational rollback disables the memory pipeline or
deploys the prior application; additive columns remain compatible with legacy
summary readers. Removing History data or columns requires a separately reviewed
tenant-schema cleanup after data-retention approval.

Startup provisions every active tenant schema and aggregates failures. Any tenant
DDL failure propagates out of bootstrap, so successful public marker migrations
cannot mask an unupgraded tenant.

## Deferred limitations

History ranking uses PostgreSQL trigram similarity rather than a new Milvus
collection, preserving the existing abstraction and bounded read path. Tier
promotion currently uses age and per-scope capacity; History segments do not yet
track their own access count, so access frequency influences Facts archival but
not History promotion. Long-term background is compressed input but is not
capacity-purged automatically, avoiding irreversible loss until a reviewed
retention policy exists.
