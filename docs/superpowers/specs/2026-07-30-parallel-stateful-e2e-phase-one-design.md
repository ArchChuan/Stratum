# Parallel Stateful E2E Phase One Design

> **DEPRECATED (2026-08-04):** 运行拓扑中的 `platform_mcp` / `internal_api` 端口已随 platform-mcp 移除（现为 frontend/backend/oauth/fixture 四端口）。

**Status:** Approved in conversation on 2026-07-30
**Scope:** Stateful E2E runner process, port, PostgreSQL, and shared-infrastructure lifecycle isolation
**Out of scope:** Redis key prefixes, NATS subject/consumer namespaces, and per-run Milvus collections

## 1. Problem

The stateful E2E runner currently serializes every worktree on one host with a global file lock and four fixed ports:

- frontend `15173`;
- backend `18080`;
- GitHub OAuth fixture `19090`;
- MCP, LLM, embedding, and Opik fixture `19091`.

Every run also uses the same `stratum_e2e` PostgreSQL database. Codex, Claude Code, CI commands, and manual shells therefore compete for the same endpoints and persistent state. A reachable health endpoint does not prove that the responding process belongs to the current run. Observed failures included a browser connecting to another worktree, OAuth identities coming from a previous runner, and an Agent resolving a stale duplicate `qwen-max` model to an unusable provider.

The first phase must permit multiple worktrees to execute stateful E2E concurrently without starting another PostgreSQL, Redis, NATS, or Milvus process per run.

## 2. Goals

1. Allocate a unique loopback port set for each run.
2. Create a unique PostgreSQL database inside the shared PostgreSQL instance for each run.
3. Derive every internal and browser-facing URL from the allocated run scope.
4. Preserve per-instance health identity so reachability cannot be mistaken for ownership.
5. Coordinate shared infrastructure only during startup, ownership changes, and cleanup; do not serialize browser execution.
6. Delete resources owned by the current run precisely and fail when cleanup is incomplete.
7. Reap abandoned databases only after a 24-hour TTL and only with positive ownership evidence.
8. Prove that two stateful runs can execute concurrently without connecting to or deleting each other's resources.

## 3. Non-Goals

- This phase does not add a Redis run prefix.
- This phase does not add a NATS run prefix or run-specific streams and durable consumers.
- This phase does not create a Milvus collection per run.
- This phase does not parallelize packs within one stateful run.
- This phase does not introduce Linux network namespaces or a per-run Docker Compose stack.
- This phase does not weaken attestation, cleanup, headless-browser, tenant, or credential controls.

Independent PostgreSQL databases and generated tenant/user identifiers form the phase-one data boundary. Redis, NATS, and Milvus remain shared infrastructure; any demonstrated cross-run contamination in those systems triggers phase two rather than an implicit expansion of this implementation.

## 4. Considered Approaches

### 4.1 Go run-scope helper with Bash orchestration

A small Go command owns run identifiers, port leases, database naming, lifecycle metadata, and safe database creation/deletion. The existing Bash runner consumes structured output and retains process orchestration.

This is the selected approach. It gives identifier and ownership rules typed, unit-testable boundaries without adding a daemon or duplicating infrastructure.

### 4.2 Pure Bash with inline Node scripts

This minimizes file count but expands quoting, JSON state, concurrency, and cleanup logic inside an already large shell script. The current incidents show that these boundaries need stronger tests and structured state. This approach is rejected.

### 4.3 Per-run Docker Compose project

This provides strong isolation but duplicates infrastructure containers and increases WSL and CI startup cost. It conflicts with the resource-usage goal and is rejected for phase one.

## 5. Architecture

```text
                         one host
  +----------------------------------------------------------+
  | Shared infrastructure                                   |
  | PostgreSQL | Redis | NATS | Milvus                       |
  +---------------------------+------------------------------+
                              |
                   short allocation/lease lock
                              |
            +-----------------+-----------------+
            |                                   |
  +---------v----------+              +---------v----------+
  | Run A              |              | Run B              |
  | run_id A           |              | run_id B           |
  | database A         |              | database B         |
  | six dynamic ports  |              | six dynamic ports  |
  | OAuth instance A   |              | OAuth instance B   |
  | result directory A |              | result directory B |
  | attestation A      |              | attestation B      |
  +--------------------+              +--------------------+
```

The global whole-run lock and fixed-port preflight are removed. A short host-scoped allocation lock protects only the lease registry, stale-resource audit, shared-infrastructure ownership changes, and allocation decisions. While holding it, a run registers its generated run ID, database name, port set, and infrastructure reference. The lock is released before database creation, migrations, service startup, and browser execution.

## 6. Run Scope

The helper emits one JSON run-scope document into a mode-`0700` temporary directory. The document contains no credentials.

```json
{
  "schema_version": 1,
  "run_id": "20260730t120102z-a1b2c3d4",
  "owner_pid": 12345,
  "created_at": "2026-07-30T12:01:02Z",
  "expires_at": "2026-07-31T12:01:02Z",
  "repository": "/absolute/worktree/path",
  "database_name": "stratum_e2e_20260730t120102z_a1b2c3d4",
  "ports": {
    "frontend": 24101,
    "backend": 24102,
    "oauth": 24103,
    "fixture": 24104,
    "platform_mcp": 24105,
    "internal_api": 24106
  },
  "infrastructure": {
    "lease_id": "20260730t120102z-a1b2c3d4",
    "started_by_e2e": false
  }
}
```

`run_id` is generated from UTC time plus cryptographic random bytes. It is safe for logs and filenames. The database name uses the same information with an underscore-only PostgreSQL identifier alphabet.

The lease registry lives below `${TMPDIR:-/tmp}/stratum-stateful-e2e/`. Registry and per-run files must be owned by the current Unix user and must not be group/world writable. Symbolic links are rejected.

## 7. Dynamic Port Allocation

The helper allocates six distinct IPv4 loopback ports. It asks the kernel for unused ports by binding `127.0.0.1:0`, records the returned port values, and closes the probes only after registering the complete set under the short allocation lock.

Closing a probe before the target process binds leaves a small race with non-cooperating processes. Therefore allocation is not treated as ownership proof:

1. every service receives an explicit listen address;
2. Vite receives `--strictPort` and cannot silently move;
3. all five child process-group leaders must remain alive after health checks;
4. OAuth health must echo the current `E2E_RUN_INSTANCE_ID`;
5. the fixture server gains the same instance identity contract;
6. a bind/startup failure stops the partial process group, releases the port lease, and retries a fresh six-port set;
7. retries are finite, with a default maximum of three complete allocation attempts.

All URLs are derived once from the selected set:

```text
frontend URL     http://127.0.0.1:<frontend>
backend URL      http://127.0.0.1:<backend>
OAuth URL        http://127.0.0.1:<oauth>
fixture base URL http://127.0.0.1:<fixture>
```

The runner exports derived values for `E2E_WEB_URL`, `E2E_API_URL`, `GITHUB_CALLBACK_URL`, `GITHUB_AUTHORIZE_URL`, `GITHUB_TOKEN_URL`, `GITHUB_USER_URL`, `QWEN_BASE_URL`, and explicit fixture listen addresses. Stateful packs must not embed any of the former fixed ports.

## 8. Per-Run PostgreSQL Database

The shared PostgreSQL server remains a single process. Each stateful run creates a database named:

```text
stratum_e2e_<UTC timestamp>_<random suffix>
```

Database lifecycle operations connect to the `postgres` maintenance database, not the run database. Creation and deletion are executed outside transaction blocks. The helper constructs identifiers only after strict validation; user-provided database identifiers are never interpolated.

The generated database name is reserved in the validated lease metadata while the short lock is held, but the actual `CREATE DATABASE` operation runs after the lock is released. Cryptographic uniqueness makes name collision negligible; an unexpected duplicate-name error is still propagated as a failed run rather than reusing an existing database.

The run-specific DSN retains the credentials, host, port, query parameters, and TLS settings from the validated base E2E DSN and replaces only the path. It is exported as `TEST_DATABASE_URL`, `STRATUM_TEST_POSTGRES_URL`, and `POSTGRES_URL` before migrations and service startup.

Cleanup order is mandatory:

```text
browser finished
  -> stop frontend
  -> stop backend and wait
  -> stop fixture and OAuth processes
  -> close runner-owned database clients
  -> DROP DATABASE <owned name> WITH (FORCE)
  -> acquire short lock
  -> remove infrastructure reference and run lease
  -> if this was the last reference, stop only E2E-owned infrastructure
  -> release short lock
  -> remove temporary directory
```

Database deletion failure changes the runner result to failure and reports only the safe database name. It must not print the DSN or credentials.

## 9. Shared Infrastructure Lease

The runner acquires a host-scoped lock only while it:

1. reads and validates the infrastructure lease registry;
2. checks PostgreSQL, Redis, NATS, and Milvus readiness;
3. starts missing shared infrastructure when allowed;
4. records a reference lease for the current run;
5. removes its reference during cleanup, after that run's processes and database are cleaned;
6. stops E2E-owned infrastructure only when the removed reference was the last active reference.

Infrastructure that was already running is marked external and is never stopped by the runner. Infrastructure started by E2E is stopped only by the last valid lease holder. A stale per-run lease does not authorize stopping infrastructure while any live lease remains.

This lock never covers database migration, service startup, Playwright, soak duration, attestation generation, or per-run database cleanup.

## 10. TTL Reaping

Normal cleanup deletes the current run database immediately. Startup also audits abandoned resources older than 24 hours.

A database is eligible for automatic reaping only when all conditions hold:

1. its name matches the exact generated database-name grammar;
2. a regular, user-owned lease file names the same database and run ID;
3. `created_at` and `expires_at` parse successfully and the 24-hour TTL has elapsed;
4. the recorded owner PID is not a live process whose command line belongs to that run;
5. the lease is not referenced by the active infrastructure registry;
6. the database is not the base database, `postgres`, `template0`, or `template1`.

If metadata is missing, malformed, symlinked, ownership-mismatched, or contradictory, the helper reports the safe resource identifier and leaves it untouched. Reaping is bounded and serialized under the short allocation lock.

## 11. Stateful Pack Changes

The Playwright context already receives `E2E_API_URL` and `E2E_WEB_URL`. A fixture URL is added to the pack context and sourced from `E2E_FIXTURE_URL`.

The following hard-coded fixture URLs must be replaced:

- MCP server URLs in `packs/mcp.ts` and `packs/agent-skill-mcp.ts`;
- context registration/evidence URLs in `packs/agent-context.ts`;
- Opik registration URLs in `packs/evaluation.ts`;
- managed model provider Base URL in `core/database.ts` and its tests.

Every pack receives URLs through the central runtime context. Packs do not read process environment directly and do not calculate ports.

## 12. Instance Identity

OAuth already requires `X-Stratum-E2E-Instance`. The combined fixture server gains the same requirement on `/health` and echoes the identity in the response header. The runner health checks send the current identity to both fixtures.

Backend and frontend ownership are established by child-process liveness plus run-specific URLs. Browser requests are recorded against those exact URLs. The safe result records the run ID and a topology summary containing port roles, but no credentials.

## 13. Attestation

Attestation remains source-bound and generated only after all selected capabilities pass and cleanup reconciliation succeeds. The result and attestation add:

- `run_id`;
- the six port roles and numeric ports;
- the generated database name;
- cleanup status for database and lease removal.

These fields are operational identifiers, not credentials. The attestation verifier rejects missing run topology, duplicate ports, non-loopback topology, unsafe database names, or incomplete cleanup for newly generated attestations. Backward compatibility for already committed schema-version-1 attestations is preserved; the new fields require a schema-version increment.

## 14. Failure Semantics

| Failure | Required behavior |
|---|---|
| Port allocation exhausted | Fail after the finite allocation budget; do not start services |
| Child bind failure | Stop this run's partial process groups, release lease, allocate a fresh set |
| Wrong fixture identity | Fail immediately and stop only this run's processes |
| Database creation failure | Release ports and infrastructure reference; propagate failure |
| Migration failure | Drop this run's database; propagate migration and cleanup errors |
| Browser or capability failure | Preserve safe diagnostics, run complete cleanup, return failure |
| Database drop failure | Report residual database name and return failure |
| Corrupt/stale metadata | Fail closed for deletion; report without deleting |
| `SIGINT` or `SIGTERM` | Run the same owned-resource cleanup path and wait for child groups |
| Host crash or `SIGKILL` | Leave lease metadata for TTL-based audit and recovery |

Cleanup errors are joined with the primary failure rather than replacing or hiding it.

## 15. Concurrency Sequence

```text
Run A                  short lock                 Run B
  | acquire -------------->|                        |
  | register ID, DB name   |                        |
  | and dynamic ports      |                        |
  | lease shared infra     |                        |
  | release -------------->|                        |
  |                         |<------------- acquire  |
  | create/migrate DB A    | register ID, DB name   |
  | start services A       | ports + infra ref      |
  |                         |<------------- release  |
  | Playwright A           | create/migrate DB B    |
  | Playwright A           | start services B       |
  | Playwright A           | Playwright B            |
  | stop/drop DB A         | Playwright B            |
  | release ref ---------->|                        |
  |              keep shared infra                  |
  |                         | cleanup B              |
  |                         | stop/drop DB B         |
  |                         |<---------- release ref |
  |                         | last ref: stop only    |
  |                         | E2E-owned infra        |
```

## 16. Verification

### 16.1 Unit and contract tests

- Run ID and database-name grammar.
- Six distinct dynamic loopback ports.
- Lease registry permissions and symlink rejection.
- Port-set retry after simulated bind failure.
- DSN path replacement without credential disclosure.
- Database create/drop command safety and failure propagation.
- TTL boundary: younger than 24 hours retained, older eligible resource reaped.
- Live PID, malformed metadata, mismatched ownership, and protected database rejection.
- Infrastructure reference counting and external-infrastructure preservation.
- Fixture identity headers.
- No fixed stateful port remains in packs or runner defaults.

### 16.2 Real concurrency acceptance

The repository runner contract starts two controlled stateful runner instances concurrently against one set of fake shared-infrastructure commands and asserts:

- distinct run IDs, databases, and all twelve ports;
- overlapping browser-execution intervals;
- independent process cleanup;
- deleting Run A does not remove Run B's lease or database;
- one failed migration does not stop or corrupt the other run.

### 16.3 System acceptance

1. Run two full 13-pack short executions concurrently from separate worktrees.
2. Confirm both attestations pass and contain distinct topology.
3. Run a 600-second `test` profile soak while another short run executes.
4. Run strict short and soak attestation verification.
5. Run `make code-quality`, `make risk-guardrails`, backend race tests, frontend typecheck/lint/build, and stateful runner contracts.
6. Confirm no runner-owned process, lease, temporary database, or generated tenant/vector data remains.

## 17. Evidence and Boundaries

Repository evidence takes precedence. Current code shows fixed URLs in the runner and stateful packs, a shared database DSN, and a whole-run lock. Runtime evidence showed cross-worktree endpoint and database contamination.

The relevant Obsidian note, `多存储（PG 向量库缓存）的租户操作必须保持隔离一致性`, is `provisional` and is used only as a lead. Its applicable boundary is that Milvus data must be deleted by tenant filter rather than dropping a shared collection; repository rules independently require the same behavior.

Official PostgreSQL 18 documentation confirms that `CREATE DATABASE` and `DROP DATABASE` are database-level operations outside transaction blocks, that deletion cannot target the currently connected database, and that `DROP DATABASE ... WITH (FORCE)` terminates eligible connections. Node.js documentation confirms that binding port `0` asks the operating system for an unused port. Vite 8 documentation states that it otherwise moves to the next port automatically and that `strictPort` makes bind conflicts fail explicitly. The repository is pinned to Vite 6.4, so the design uses only the longstanding CLI behavior already supported by the installed version and verifies it through the local contract test.

## 18. Rollout and Compatibility

The change replaces the global runner lock in one release rather than maintaining two silent modes. Explicit command overrides used by runner contract tests remain supported, but fixed-port environment defaults are removed from stateful execution.

CI continues to run one stateful job unless workflow parallelism is deliberately increased later. Local Codex, Claude Code, and manual worktrees gain parallel execution immediately because isolation is implemented in the repository runner rather than in a specific agent integration.

If phase-one verification reveals Redis, NATS, or Milvus contamination, the runner must continue to fail visibly. Phase two will add logical namespaces based on evidence; phase one must not hide contamination through retries or relaxed reconciliation.
