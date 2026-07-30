# Parallel Stateful E2E Phase One Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow multiple Stratum worktrees to run stateful browser E2E concurrently with per-run ports and PostgreSQL databases while sharing PostgreSQL, Redis, NATS, and Milvus processes.

**Architecture:** Add a focused Go package and CLI for run identity, dynamic loopback ports, lease metadata, conservative stale-resource reaping, and PostgreSQL database lifecycle. Keep Bash responsible for process orchestration, but replace its whole-run lock with short registry/lifecycle critical sections and feed one run scope into every service, Playwright pack, cleanup result, and schema-v2 attestation.

**Tech Stack:** Go 1.25.12, pgx v5, `golang.org/x/sys/unix`, Bash, Node.js 22, TypeScript 5.6, Playwright 1.61, Vitest 3.2, PostgreSQL, Vite 6.4.

---

## File Map

- Create `internal/platform/e2erunscope/scope.go`: run identifiers, safe database names, URL derivation, DSN replacement, and dynamic port sets.
- Create `internal/platform/e2erunscope/scope_test.go`: deterministic unit tests for identifiers, DSNs, and ports.
- Create `internal/platform/e2erunscope/registry.go`: secure lease files, persistent shared-infrastructure ownership, reference accounting, stale audit decisions, and atomic registry updates.
- Create `internal/platform/e2erunscope/registry_test.go`: permissions, symlink, ownership, PID, TTL, and reference-count tests.
- Create `internal/platform/e2erunscope/postgres.go`: maintenance-DSN derivation and exact create/drop/reap operations.
- Create `internal/platform/e2erunscope/postgres_test.go`: fake-admin lifecycle and protected-name tests.
- Create `cmd/e2e-run-scope/main.go`: JSON CLI adapter for `allocate`, `create-database`, `drop-database`, `release`, and `reap`.
- Create `cmd/e2e-run-scope/main_test.go`: CLI validation, JSON, and error-redaction tests.
- Modify `cmd/e2e-mcp-server/main.go` and `cmd/e2e-mcp-server/main_test.go`: require and echo the current instance identity on `/health`; require an explicit dynamic listen address.
- Modify `cmd/e2e-github-oauth/main.go` and `cmd/e2e-github-oauth/main_test.go`: remove the fixed-port default and retain its existing identity contract.
- Modify `web/e2e/stateful/core/runtime.ts` and `web/e2e/stateful/core/runtime.test.ts`: validate centrally supplied run URLs.
- Modify `web/e2e/stateful/core/database.ts` and `web/e2e/stateful/core/redaction.test.ts`: inject the model fixture base URL.
- Modify `web/e2e/stateful/packs/types.ts`, the four fixture-consuming packs, and `web/e2e/system-stateful.spec.ts`: pass `fixtureURL` through context without environment reads inside packs.
- Modify `internal/platform/e2eattestation/attestation.go`, its tests, and `test/e2e/stateful/attestation.schema.json`: add schema-v2 topology and owned-resource cleanup evidence while preserving schema-v1 verification.
- Modify `scripts/e2e/system-stateful.sh`: consume the run-scope CLI, use short locks, derive all URLs, retry whole port sets, clean before attesting, and remove fixed-port/global-lock behavior.
- Rewrite `scripts/e2e/system-stateful-test.sh`: cover dynamic topology, cleanup, retries, shared-infrastructure references, and true overlap.
- Modify `test/e2e/README.md`: document parallel behavior and exact residual cleanup commands.

### Task 1: Run-Scope Identity, URLs, Ports, and DSNs

**Files:**

- Create: `internal/platform/e2erunscope/scope.go`
- Create: `internal/platform/e2erunscope/scope_test.go`

- [ ] **Step 1: Run the mandatory risk explanation before implementation**

Run: `bash scripts/quality/risk-regression-guard.sh --explain`

Expected: exit `0`; output identifies the applicable database, external-dependency, cleanup, and E2E checks without changing the baseline.

- [ ] **Step 2: Write failing table-driven tests for the public run-scope contract**

Define tests around these exact public types and functions:

```go
type Ports struct {
 Frontend int `json:"frontend"`
 Backend  int `json:"backend"`
 OAuth    int `json:"oauth"`
 Fixture  int `json:"fixture"`
}

type Scope struct {
 SchemaVersion int       `json:"schema_version"`
 RunID         string    `json:"run_id"`
 OwnerPID      int       `json:"owner_pid"`
 CreatedAt     time.Time `json:"created_at"`
 ExpiresAt     time.Time `json:"expires_at"`
 Repository    string    `json:"repository"`
 DatabaseName  string    `json:"database_name"`
 Ports         Ports     `json:"ports"`
 Infrastructure InfrastructureLease `json:"infrastructure"`
}

func NewScope(repository string, ownerPID int, now time.Time, random io.Reader) (Scope, error)
func AllocatePorts() (Ports, error)
func Validate(scope Scope) error
func DatabaseURL(base string, databaseName string) (string, error)
func MaintenanceURL(base string) (string, error)
func URLs(ports Ports) RuntimeURLs
```

Cover UTC-plus-random ID grammar, PostgreSQL-safe name grammar, absolute repository paths, positive PID, four distinct non-zero ports, loopback URLs, preservation of DSN credentials/query/TLS parameters, replacement of only the database path, and rejection of non-loopback or non-E2E base DSNs.

- [ ] **Step 3: Run the new tests and observe the expected compile failure**

Run: `go test ./internal/platform/e2erunscope -run 'Test(NewScope|AllocatePorts|DatabaseURL|Validate)' -count=1`

Expected: FAIL because `Scope`, `NewScope`, and related functions do not exist.

- [ ] **Step 4: Implement the minimal scope package**

Use `crypto/rand`, `encoding/hex`, `net.ListenTCP("tcp4", 127.0.0.1:0)`, and `net/url`. Bind all four probes before reading addresses, reject duplicates, then close every listener with joined errors. Use these validation expressions:

```go
var runIDPattern = regexp.MustCompile(`^[0-9]{8}t[0-9]{6}z-[a-f0-9]{16}$`)
var databasePattern = regexp.MustCompile(`^stratum_e2e_[0-9]{8}t[0-9]{6}z_[a-f0-9]{16}$`)
```

`RuntimeURLs` must contain `Frontend`, `Backend`, `OAuth`, and `Fixture`; all values must be `http://127.0.0.1:<role-port>`. No function may log or include a full DSN in an error.

- [ ] **Step 5: Run focused tests and race tests**

Run: `go test -race ./internal/platform/e2erunscope -run 'Test(NewScope|AllocatePorts|DatabaseURL|Validate)' -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the scope primitive**

```bash
git add internal/platform/e2erunscope/scope.go internal/platform/e2erunscope/scope_test.go
git commit -m '[feat](e2e): add isolated run scope primitives'
```

### Task 2: Secure Lease Registry and Shared-Infrastructure References

**Files:**

- Create: `internal/platform/e2erunscope/registry.go`
- Create: `internal/platform/e2erunscope/registry_test.go`

- [ ] **Step 1: Write failing registry security and lifecycle tests**

Test this public surface:

```go
type InfrastructureLease struct {
 LeaseID     string `json:"lease_id"`
 StartedByE2E bool  `json:"started_by_e2e"`
}

type Registry struct { Root string }

type InfrastructureState struct {
 StartedByE2E bool      `json:"started_by_e2e"`
 StartedAt    time.Time `json:"started_at"`
 StartedByRun string    `json:"started_by_run"`
}

func (r Registry) Register(scope Scope) error
func (r Registry) Read(runID string) (Scope, error)
func (r Registry) MarkInfrastructureOwned(runID string, now time.Time) error
func (r Registry) Release(runID string) (ReleaseResult, error)
func (r Registry) ConfirmInfrastructureStopped(ownershipRunID string) error
func (r Registry) Stale(now time.Time, processAlive func(int, string) bool) ([]Scope, error)

type ReleaseResult struct {
 LastReference bool `json:"last_reference"`
 StopOwnedInfrastructure bool `json:"stop_owned_infrastructure"`
 OwnershipRunID string `json:"ownership_run_id,omitempty"`
}
```

Cover root mode `0700`, lease and infrastructure-state mode `0600`, atomic temp-file-plus-rename writes, rejection of symlink roots/files, rejection of files not owned by the current UID, rejection of group/world-writable metadata, live PID retention, exactly-24-hour boundary retention, older-than-24-hour eligibility, malformed JSON fail-closed behavior, removal of only the requested lease, and last-reference ownership decisions. Include the ordering case where Run A starts infrastructure, Run B registers, Run A releases first, and Run B's later release still returns `StopOwnedInfrastructure=true`.

- [ ] **Step 2: Run the tests and verify they fail before implementation**

Run: `go test ./internal/platform/e2erunscope -run 'TestRegistry' -count=1`

Expected: FAIL because `Registry` methods are undefined.

- [ ] **Step 3: Implement strict registry IO and reference accounting**

Use `os.Lstat` before every read or mutation, `syscall.Stat_t.Uid` for ownership, `filepath.Base(runID) == runID`, `O_CREATE|O_EXCL` for new leases, and `os.Rename` for updates. Never follow a symlink. Store one lease JSON per run below `<root>/runs/<run-id>.json` and one ownership document at `<root>/infrastructure.json`; derive active references by validating every regular lease rather than trusting a mutable counter.

`MarkInfrastructureOwned` writes the persistent ownership document only after the shared stack has started successfully. `Release` preserves that document while any validated reference remains. It returns `StopOwnedInfrastructure=true` plus the recorded `OwnershipRunID` only for the last valid reference when the ownership document exists. After the caller reports a successful stop, `ConfirmInfrastructureStopped` compares that returned ownership ID with the current ownership document and removes it; a stop failure preserves the document for explicit recovery. Corrupt metadata must return an error and preserve all resources.

- [ ] **Step 4: Run registry tests under the race detector**

Run: `go test -race ./internal/platform/e2erunscope -run 'TestRegistry' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit registry behavior**

```bash
git add internal/platform/e2erunscope/registry.go internal/platform/e2erunscope/registry_test.go
git commit -m '[feat](e2e): add secure shared run lease registry'
```

### Task 3: PostgreSQL Database Lifecycle and Run-Scope CLI

**Files:**

- Create: `internal/platform/e2erunscope/postgres.go`
- Create: `internal/platform/e2erunscope/postgres_test.go`
- Create: `cmd/e2e-run-scope/main.go`
- Create: `cmd/e2e-run-scope/main_test.go`

- [ ] **Step 1: Write failing database lifecycle tests around an injectable admin**

Use this narrow interface so unit tests never need a live database:

```go
type DatabaseAdmin interface {
 Exec(context.Context, string) error
 Exists(context.Context, string) (bool, error)
 Close(context.Context) error
}

func CreateDatabase(ctx context.Context, admin DatabaseAdmin, name string) error
func DropDatabase(ctx context.Context, admin DatabaseAdmin, name string) error
func ReapDatabases(ctx context.Context, registry Registry, admin DatabaseAdmin, now time.Time,
 processAlive func(int, string) bool) ([]string, error)
```

Assert exact SQL uses a strictly validated generated identifier, create refuses an existing database, drop uses `DROP DATABASE "name" WITH (FORCE)`, protected names are rejected, missing targets are idempotent on drop, and a database is removed only after its matching stale lease passes every registry check. Assert errors never contain the base DSN.

- [ ] **Step 2: Run the focused tests and verify failure**

Run: `go test ./internal/platform/e2erunscope -run 'Test(Create|Drop|Reap)Database' -count=1`

Expected: FAIL because database lifecycle functions do not exist.

- [ ] **Step 3: Implement PostgreSQL lifecycle with pgx outside transactions**

Add a concrete pgx adapter that connects to `MaintenanceURL(baseDSN)`. Do not call `Begin`; execute database-level commands directly. Quote only after `databasePattern` validation. Give connect/create/drop operations explicit context deadlines and join close errors with the primary error.

- [ ] **Step 4: Write failing CLI tests**

Test `run(args []string, stdout, stderr io.Writer, deps dependencies) error` for:

```text
allocate --repository <abs> --registry <dir>
create-database --scope <scope.json> --base-dsn-env TEST_DATABASE_URL
drop-database --scope <scope.json> --base-dsn-env TEST_DATABASE_URL
release --scope <scope.json> --registry <dir>
confirm-infrastructure-stopped --ownership-run-id <id> --registry <dir>
reap --registry <dir> --base-dsn-env TEST_DATABASE_URL
```

`allocate` prints one canonical JSON `Scope`; `release` prints one canonical JSON `ReleaseResult`; unknown commands and unsafe inputs return exit-worthy errors; stderr never contains credentials.

- [ ] **Step 5: Implement the thin CLI and run all package tests**

Keep parsing/JSON in `cmd/e2e-run-scope`; keep lifecycle logic in the internal package. Read the DSN from the named environment variable rather than a command-line argument so credentials do not enter process listings.

Run: `go test -race ./internal/platform/e2erunscope ./cmd/e2e-run-scope -count=1`

Expected: PASS.

- [ ] **Step 6: Commit database and CLI support**

```bash
git add internal/platform/e2erunscope/postgres.go internal/platform/e2erunscope/postgres_test.go \
  cmd/e2e-run-scope/main.go cmd/e2e-run-scope/main_test.go
git commit -m '[feat](e2e): manage per-run postgres databases'
```

### Task 4: Fixture Listen Addresses and Instance Identity

**Files:**

- Modify: `cmd/e2e-mcp-server/main.go`
- Modify: `cmd/e2e-mcp-server/main_test.go`
- Modify: `cmd/e2e-github-oauth/main.go`
- Modify: `cmd/e2e-github-oauth/main_test.go`

- [ ] **Step 1: Add failing fixture configuration and health tests**

For both fixture commands, require an explicit loopback `host:port` and non-empty `E2E_RUN_INSTANCE_ID`. Add MCP health cases matching OAuth:

```go
request := httptest.NewRequest(http.MethodGet, "/health", nil)
request.Header.Set("X-Stratum-E2E-Instance", instanceID)
response := httptest.NewRecorder()
newHandler(instanceID).ServeHTTP(response, request)
require.Equal(t, http.StatusNoContent, response.Code)
require.Equal(t, instanceID, response.Header().Get("X-Stratum-E2E-Instance"))
```

Also assert missing/wrong identity returns `409 Conflict` and does not echo the expected identity.

- [ ] **Step 2: Run tests and observe the MCP identity failure**

Run: `go test ./cmd/e2e-mcp-server ./cmd/e2e-github-oauth -run 'Test.*(Health|Config)' -count=1`

Expected: FAIL because MCP health is identity-blind and both commands still have fixed-port defaults.

- [ ] **Step 3: Implement explicit configuration**

Replace the package-global MCP handler with `newHandler(instanceID string) http.Handler`; route all existing endpoints through it. Make `loadServerConfig(getenv)` validate `E2E_MCP_LISTEN_ADDRESS` and identity. In OAuth, remove `defaultListenAddress` and make absent `E2E_GITHUB_LISTEN_ADDRESS` fail. Preserve loopback-only validation.

- [ ] **Step 4: Run fixture tests and commit**

Run: `go test -race ./cmd/e2e-mcp-server ./cmd/e2e-github-oauth -count=1`

Expected: PASS.

```bash
git add cmd/e2e-mcp-server cmd/e2e-github-oauth
git commit -m '[fix](e2e): bind fixtures to run identity'
```

### Task 5: Central Fixture URL Propagation in Playwright

**Files:**

- Modify: `web/e2e/stateful/core/runtime.ts`
- Modify: `web/e2e/stateful/core/runtime.test.ts`
- Modify: `web/e2e/stateful/core/database.ts`
- Modify: `web/e2e/stateful/core/redaction.test.ts`
- Modify: `web/e2e/stateful/packs/types.ts`
- Modify: `web/e2e/stateful/packs/mcp.ts`
- Modify: `web/e2e/stateful/packs/agent-skill-mcp.ts`
- Modify: `web/e2e/stateful/packs/agent-context.ts`
- Modify: `web/e2e/stateful/packs/evaluation.ts`
- Modify: `web/e2e/system-stateful.spec.ts`

- [ ] **Step 1: Add failing runtime URL validation tests**

Extend `RuntimeOptions` with:

```ts
urls: {
  api: string;
  web: string;
  fixture: string;
};
```

Tests must require `E2E_API_URL`, `E2E_WEB_URL`, and `E2E_FIXTURE_URL`, accept only `http:` URLs on `127.0.0.1` with explicit non-zero ports, and reject credentials, query strings, fragments, paths other than `/`, and duplicate role ports.

- [ ] **Step 2: Run Vitest and verify the new tests fail**

Run: `npm --prefix web test -- --run e2e/stateful/core/runtime.test.ts`

Expected: FAIL because runtime options do not contain validated URLs.

- [ ] **Step 3: Implement URL parsing and central context injection**

Add one pure `parseLoopbackBaseURL(raw, name)` helper in `runtime.ts`. Add `fixtureURL: string` to `PackContext` and the four local pack context interfaces. Replace literals as follows:

```ts
`${fixtureURL}/mcp`
`${fixtureURL}/e2e/context/register`
`${fixtureURL}/e2e/context/evidence`
`${fixtureURL}/e2e/opik/register`
`${fixtureURL}/v1`
```

Change managed-model setup to accept `fixtureURL` as a function argument. Packs must not call `process.env`.

- [ ] **Step 4: Add a no-fixed-port regression assertion**

In `runtime.test.ts`, scan the stateful runner/packs and assert none contain `15173`, `18080`, `19090`, or `19091`, excluding test fixture input strings that deliberately verify rejection.

- [ ] **Step 5: Run typecheck and targeted tests**

Run: `npm --prefix web test -- --run e2e/stateful/core/runtime.test.ts e2e/stateful/core/redaction.test.ts`

Run: `npm --prefix web run typecheck`

Expected: both PASS.

- [ ] **Step 6: Commit URL propagation**

```bash
git add web/e2e/stateful web/e2e/system-stateful.spec.ts
git commit -m '[refactor](e2e): inject per-run fixture urls'
```

### Task 6: Schema-v2 Run Topology and Cleanup Attestation

**Files:**

- Modify: `internal/platform/e2eattestation/attestation.go`
- Modify: `internal/platform/e2eattestation/attestation_test.go`
- Modify: `test/e2e/stateful/attestation.schema.json`
- Modify: `scripts/quality/e2e-attestation-test.sh`

- [ ] **Step 1: Add failing v2 validation and v1 compatibility tests**

Add these exact types:

```go
type RunTopology struct {
 RunID       string            `json:"run_id"`
 Host        string            `json:"host"`
 Ports       map[string]int    `json:"ports"`
 DatabaseName string           `json:"database_name"`
}

type OwnedCleanup struct {
 DatabaseDropped bool `json:"database_dropped"`
 LeaseRemoved    bool `json:"lease_removed"`
}
```

Embed `RunTopology *RunTopology` and `OwnedCleanup *OwnedCleanup` in `SafeResults` with `omitempty`. Test that newly generated attestations are schema version `2` and reject missing topology, wrong host, duplicate/non-positive ports, unsafe database names, or incomplete owned cleanup. Separately decode and verify an existing schema-v1 fixture with absent new fields.

- [ ] **Step 2: Run attestation tests and verify failure**

Run: `go test ./internal/platform/e2eattestation ./cmd/e2e-attestation -run 'Attestation|Topology|SchemaV1' -count=1`

Expected: FAIL because schema v2 and topology validation are absent.

- [ ] **Step 3: Implement dual-version verification and v2 generation**

`GenerateAttestation` always emits version `2` and requires valid topology/owned cleanup. `VerifyAttestation` accepts versions `1` and `2`; version `1` follows the current rules unchanged, while version `2` additionally calls `verifyRunTopology`. Require exactly the roles `frontend`, `backend`, `oauth`, and `fixture`, host `127.0.0.1`, four distinct ports, safe generated database grammar, and both cleanup booleans true.

- [ ] **Step 4: Update the JSON schema to v2**

Change `$id` and `schema_version`, add required `run_topology` and `owned_cleanup` objects with `additionalProperties: false`, exact port-role requirements, loopback host const, and database-name pattern. Preserve v1 compatibility in Go verification; the repository schema describes newly generated v2 documents.

- [ ] **Step 5: Run attestation guardrails and commit**

Run: `bash scripts/quality/e2e-attestation-test.sh`

Run: `go test -race ./internal/platform/e2eattestation ./cmd/e2e-attestation -count=1`

Expected: PASS.

```bash
git add internal/platform/e2eattestation test/e2e/stateful/attestation.schema.json scripts/quality/e2e-attestation-test.sh
git commit -m '[test](e2e): attest isolated run topology'
```

### Task 7: Replace the Whole-Run Lock with Scoped Runner Lifecycle

**Files:**

- Modify: `scripts/e2e/system-stateful.sh`
- Modify: `scripts/e2e/system-stateful-test.sh`

- [ ] **Step 1: Replace fixed-lock tests with failing dynamic-scope assertions**

Remove expectations for `STATEFUL_E2E_LOCK_FILE`, fixed-port preflight, and fixed URLs. Add assertions that the runner:

- creates a scope below `${TMPDIR}/stratum-stateful-e2e`;
- exports distinct dynamic URLs and the generated database DSN;
- includes Vite `--strictPort`;
- sends `X-Stratum-E2E-Instance` to both fixture health endpoints and verifies the response header;
- invokes database drop and lease release after stopping child process groups;
- does not invoke attestation until those cleanup markers exist;
- preserves the primary failure when cleanup also fails.

- [ ] **Step 2: Run the shell contract and verify it fails**

Run: `bash scripts/e2e/system-stateful-test.sh`

Expected: FAIL because the runner still takes the global lock and uses fixed ports/database.

- [ ] **Step 3: Introduce one idempotent owned-resource cleanup function**

Track `scope_file`, four child PIDs, `database_created`, `lease_registered`, `infra_reference`, and cleanup booleans. The function must execute in this order:

```text
stop/wait frontend -> backend -> fixture -> OAuth
drop the exact generated database
acquire the short registry lock
release the exact run lease
stop shared infrastructure only when release JSON says stop_owned_infrastructure=true
confirm the successful shared-infrastructure stop and clear its ownership record
release the short lock
write owned cleanup result
```

The trap calls the same function, joins cleanup status with the current exit status, and never deletes an unknown database or another run's lease.

- [ ] **Step 4: Allocate/register under only the short lock**

Use `${STATEFUL_E2E_REGISTRY_ROOT:-${TMPDIR:-/tmp}/stratum-stateful-e2e}` and a lock file inside that root. Under `flock`, run stale audit/reaping, allocate/register scope, check/start shared infrastructure, and mark ownership. Release the lock before `create-database`, migration, or any child process start.

Keep existing command overrides for contract tests, but replace fixed-port overrides with one `STATEFUL_E2E_SCOPE_COMMAND` seam that emits a validated scope document.

- [ ] **Step 5: Derive and export the complete runtime environment**

Read the scope JSON with `jq -er`, validate it through the Go CLI, and derive:

```bash
export E2E_API_URL="http://127.0.0.1:$backend_port"
export E2E_WEB_URL="http://127.0.0.1:$frontend_port"
export E2E_FIXTURE_URL="http://127.0.0.1:$fixture_port"
export GITHUB_CALLBACK_URL="$E2E_API_URL/auth/github/callback"
export GITHUB_AUTHORIZE_URL="$oauth_url/login/oauth/authorize"
export GITHUB_TOKEN_URL="$oauth_url/login/oauth/access_token"
export GITHUB_USER_URL="$oauth_url/user"
export QWEN_BASE_URL="$E2E_FIXTURE_URL/v1"
export E2E_GITHUB_LISTEN_ADDRESS="127.0.0.1:$oauth_port"
export E2E_MCP_LISTEN_ADDRESS="127.0.0.1:$fixture_port"
```

Build backend/frontend commands from these variables. The Vite command must end with `--host 127.0.0.1 --port "$frontend_port" --strictPort`.

- [ ] **Step 6: Implement finite whole-set retry**

For at most `${STATEFUL_E2E_PORT_ALLOCATION_ATTEMPTS:-3}` attempts, start all four processes, perform identity-aware health checks, and verify all PIDs are alive. On any bind/start/identity failure, stop every partial child, release the current lease, allocate a completely new scope and database, and retry. Migration or browser failures do not retry.

- [ ] **Step 7: Clean successfully before attestation**

After Playwright writes safe results, explicitly call cleanup. Use `jq` to add `run_topology` and `owned_cleanup` only after `database_dropped=true` and `lease_removed=true`, validate the result, then generate the attestation. Disable the EXIT trap only after attestation succeeds and the temporary run directory is removed.

- [ ] **Step 8: Run the shell contract and static shell checks**

Run: `bash -n scripts/e2e/system-stateful.sh scripts/e2e/system-stateful-test.sh`

Run: `bash scripts/e2e/system-stateful-test.sh`

Run: `rg -n '15173|18080|19090|19091|STATEFUL_E2E_LOCK_FILE|STATEFUL_E2E_PORT_PREFLIGHT' scripts/e2e/system-stateful.sh web/e2e/stateful`

Expected: syntax and contract PASS; `rg` returns no matches.

- [ ] **Step 9: Commit runner lifecycle changes**

```bash
git add scripts/e2e/system-stateful.sh scripts/e2e/system-stateful-test.sh
git commit -m '[refactor](e2e): isolate stateful runner lifecycle'
```

### Task 8: True Two-Runner Concurrency Contract

**Files:**

- Modify: `scripts/e2e/system-stateful-test.sh`
- Modify: `cmd/e2e-run-scope/main_test.go`

- [ ] **Step 1: Add a failing overlap test**

Start two runner processes concurrently against one temporary registry and fake shared-infrastructure commands. Each fake Playwright command writes `entered-<run-id>`, waits until both entered markers exist, then writes a safe result. Assert:

```text
run IDs differ
database names differ
all eight role ports differ
both entered markers existed before either exited
Run A cleanup never removes Run B lease/database markers
infrastructure up executes once
infrastructure down executes once, after the final release
```

Add a second case where Run A migration fails while Run B reaches Playwright and passes.

- [ ] **Step 2: Run the contract and verify the new case fails first**

Run: `bash scripts/e2e/system-stateful-test.sh`

Expected: FAIL until the fake scope/database hooks and reference lifecycle expose independent runs correctly.

- [ ] **Step 3: Complete test seams without weakening production validation**

Allow fake lifecycle commands only through the existing `STATEFUL_E2E_*_COMMAND` contract-test overrides. Production defaults must still execute `cmd/e2e-run-scope`; fake output must pass the same JSON validation and ownership grammar.

- [ ] **Step 4: Run the contract repeatedly and under timeout**

Run: `for i in 1 2 3; do timeout 60 bash scripts/e2e/system-stateful-test.sh; done`

Expected: all three iterations PASS without hanging or leaking marker processes.

- [ ] **Step 5: Commit concurrency proof**

```bash
git add scripts/e2e/system-stateful-test.sh cmd/e2e-run-scope/main_test.go
git commit -m '[test](e2e): prove concurrent worktree isolation'
```

### Task 9: Documentation and Focused Quality Gates

**Files:**

- Modify: `test/e2e/README.md`

- [ ] **Step 1: Update operator documentation**

Document that Codex, Claude Code, and manual worktrees use the same repository runner and may execute concurrently. Describe the registry location, safe run/database naming grammar, 24-hour conservative reaping, how to inspect a residual lease, and why Redis/NATS/Milvus remain shared in phase one. Do not document credentials or raw DSNs.

- [ ] **Step 2: Run all focused unit and contract suites**

Run: `go test -race ./internal/platform/e2erunscope ./cmd/e2e-run-scope ./cmd/e2e-mcp-server ./cmd/e2e-github-oauth ./internal/platform/e2eattestation ./cmd/e2e-attestation -count=1`

Run: `npm --prefix web test -- --run e2e/stateful`

Run: `npm --prefix web run typecheck`

Run: `bash scripts/e2e/system-stateful-test.sh`

Expected: all PASS.

- [ ] **Step 3: Run repository gates**

Run: `make code-quality`

Run: `make risk-guardrails`

Run: `go vet ./... && go test -short ./...`

Run: `make fe-lint && make fe-build`

Expected: all PASS; do not refresh `scripts/quality/code-quality-baseline.json`.

- [ ] **Step 4: Commit documentation**

```bash
git add test/e2e/README.md
git commit -m '[docs](e2e): document parallel stateful runs'
```

### Task 10: Real Stateful Concurrency and Soak Acceptance

**Files:**

- Generated locally, do not blindly commit: `test/e2e/attestations/<source-digest>.json`

- [ ] **Step 1: Use the project E2E skill and confirm shared infrastructure readiness**

Invoke `stratum-e2e-development`, then run: `make infra-up && make infra-wait`

Expected: one shared PostgreSQL, Redis, NATS, and Milvus stack is ready.

- [ ] **Step 2: Run two complete 13-pack short executions concurrently**

From two feature worktrees at the same source revision, start `make e2e-system-short` in separate shells. Record start/end timestamps and resulting attestation paths without printing credentials.

Expected: execution intervals overlap; both pass all 13 packs; attestations contain different run IDs, database names, and eight distinct ports.

- [ ] **Step 3: Verify cleanup isolation after both runs**

Inspect the validated registry and PostgreSQL catalog through safe-name-only queries. Confirm no lease or generated database from either successful run remains, no runner-owned child process remains, and tenant-filtered Milvus cleanup reports no generated residual entities.

Expected: zero owned residual resources; shared infrastructure remains available if it was external or another lease is active.

- [ ] **Step 4: Run the required 600-second test soak concurrently with a short run**

Run in one worktree:

```bash
STATEFUL_E2E_PROFILE=test STATEFUL_E2E_DURATION_SEC=600 STATEFUL_E2E_PACKS=all make e2e-system-soak
```

While it is active, run `make e2e-system-short` from the other worktree.

Expected: both pass with overlapping execution and distinct v2 topology.

- [ ] **Step 5: Verify source-bound attestations**

Run: `make e2e-attestation-check E2E_REQUIRED_MODE=short`

Run: `make e2e-attestation-check E2E_REQUIRED_MODE=soak E2E_REQUIRED_PROFILE=test`

Expected: both applicable v2 attestations verify, with no skipped/unreconciled capability and complete owned cleanup.

- [ ] **Step 6: Run final race and regression gates**

Run: `go test -v -race -timeout 30s ./...`

Run: `make code-quality risk-guardrails fe-lint fe-build`

Expected: all PASS. If the 30-second repository race timeout is an existing suite limitation, preserve the exact failing package/output and run that package with its established timeout; do not claim the gate passed unless the command actually exits `0`.

- [ ] **Step 7: Request code review before shipping**

Invoke `superpowers:requesting-code-review`. Resolve only evidence-backed findings, rerun affected tests, then use `ship-it` to push the branch, update PR #178, wait for required CI, squash-merge into `main`, and remove the feature worktree through the repository-approved workflow.

Expected: PR #178 contains the design, plan, implementation, and v2 acceptance evidence; required CI is green before squash merge.
