# Observation Table Drop And PgBouncer Lock Design

## Scope

This change has two independently committed parts:

1. Permanently remove the runtime-obsolete tenant tables `agent_executions`, `agent_tool_traces`, and
   `agent_trace_events` through canonical tenant provisioning.
2. Replace the schema bootstrap session advisory lock with a transaction-scoped advisory lock that is safe with
   PgBouncer transaction pooling.

The two unrelated WSL proxy documents remain outside both commits. No GitHub issue is created.

## Tenant Table Removal

The tables are tenant-only, so numbered public migrations must not reference them. Canonical
`pkg/storage/postgres/tenant_schema.sql` will drop them with `DROP TABLE IF EXISTS` before other tenant DDL and will no
longer recreate, alter, or index them. Existing tenants lose historical rows on the next successful provisioning;
new tenants never create the tables. Repeated provisioning remains idempotent.

Schema tests must first fail against the retained-table DDL, then prove the drop statements are present and all
recreation statements are absent. The PostgreSQL integration test must seed all three historical tables and rows,
provision the tenant twice, and prove the tables remain absent.

## PgBouncer-Safe Lock

`WithSchemaProvisionLock` currently sends session-level lock and unlock statements as separate implicit transactions.
Under PgBouncer transaction pooling they can execute on different PostgreSQL backends, leaving the first backend's
session lock behind.

The replacement opens an explicit transaction, executes `SELECT pg_advisory_xact_lock($1)`, runs the existing schema
bootstrap callback while that transaction stays open, and then commits. PgBouncer pins that transaction to one server
connection, and PostgreSQL releases the transaction-level lock automatically on commit or rollback. There is no
explicit unlock statement.

Tests cover lock acquisition failure, callback failure with rollback, commit failure, successful commit, serialization
between real PostgreSQL connections, and absence of a session advisory lock after completion. The lock fix does not
change tenant DDL or the callback's existing database access paths.

## Evidence

Repository evidence is authoritative: the runtime-source guard already proves the three tables have no Agent or API
consumer, and real Opik/MinIO failure scenarios have passed. PostgreSQL documentation distinguishes session-level locks
from transaction-level locks; PgBouncer documents that transaction pooling preserves backend affinity only for the
duration of a transaction. Obsidian search returned only general distributed-system guidance, not a verified project-
specific counterexample.
