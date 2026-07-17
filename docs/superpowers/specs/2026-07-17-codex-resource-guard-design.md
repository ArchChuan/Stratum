# Codex Resource Guard Design

## Incident

On 2026-07-17 two Codex sessions exhausted host CPU and memory for about 52 minutes. The resource monitor recorded sustained 97-100% CPU, about 510 MiB minimum available memory, and a full 4 GiB swap. Resource use fell immediately after the two session scopes ended.

One session launched eight heavyweight checks concurrently: npm audit, the full Vitest suite, ESLint, TypeScript, Vite build, govulncheck, go vet, and the full short Go test suite. The other launched TypeScript, Vitest, and ESLint concurrently. A tool wait later returned without exit codes, but the session did not prove that child processes had stopped before starting verification again.

## Goal

Prevent Codex verification commands from exhausting the workstation while preserving fast focused TDD commands. Heavy verification waits in one shared queue, runs with bounded parallelism, and terminates its complete process group on timeout.

## Command Boundary

Add a user-level `stratum-verify` command with these modes:

- `frontend-test`: run the frontend test suite with bounded Vitest workers.
- `frontend-full`: run frontend audit, typecheck, lint, tests, and build sequentially.
- `go-test`: run the short Go suite with bounded Go parallelism.
- `go-full`: run vet, vulnerability analysis, and tests sequentially.
- `all`: run frontend-full followed by go-full.

Every mode acquires the same flock. A second invocation waits and reports that it is queued. The lock is held by a dedicated process and is not inherited by children. The command uses an explicit stage timeout. Timeout handling terminates the stage's process group, waits for exit, and fails without starting the next stage.

Focused commands remain outside the queue when they select a package or named test. This keeps the red-green TDD loop responsive.

## Hook Policy

Add a Codex `PreToolUse:Bash` resource guard. It denies raw heavyweight commands that cover the entire project or frontend suite and directs the caller to the matching `stratum-verify` mode. The guard covers:

- unbounded `npm test` or `vitest run`;
- full frontend verification chains containing multiple test, lint, typecheck, audit, or build stages;
- `go test ./...`, `go test -short ./...`, `go vet ./...`, `go build ./...`, and repository-wide govulncheck;
- command text that attempts to start multiple heavyweight checks concurrently.

The guard allows targeted package tests, named `-run` tests, and bounded Vitest commands with explicit worker limits. It must return the existing Codex hook protocol and must not inspect or print environment secrets.

When a tool call times out or returns no exit status, agents must inspect the prior process or cgroup before retrying. A still-running job is allowed to finish through the shared queue; it must not be duplicated.

## Resource Ceiling

Keep all interactive AI tools in `ai.slice`, but reduce its aggregate CPU allowance from ten logical CPUs to six and add a hard memory ceiling above the normal working set but below host exhaustion. Retain memory accounting and the existing early pressure threshold. The hard ceiling is a fallback for commands that bypass the hook, not the primary scheduling mechanism.

Initial limits:

- `CPUQuota=600%`
- `MemoryHigh=8G`
- `MemoryMax=10G`

The limits apply to the aggregate AI slice so multiple Codex, Claude, MCP, and child processes cannot collectively starve the host.

## Failure Semantics

- Queue contention waits instead of failing.
- A stage timeout fails the wrapper and kills the complete stage process group.
- An interrupted wrapper releases its lock and does not leave a detached verification process.
- A hook denial explains the matching wrapper mode.
- Focused test commands remain available when they do not expand to the entire repository.

## Tests

Use fake commands and temporary state directories to prove:

1. Two heavyweight invocations execute serially in arrival order.
2. A timed-out stage and its child process are terminated.
3. Full raw frontend and Go commands are denied by the hook.
4. Focused Go and Vitest commands are allowed.
5. Concurrent heavyweight command text is denied.
6. Hook output conforms to the Codex `PreToolUse` JSON contract.
7. The installed systemd slice exposes the intended CPU and memory properties.

## Acceptance

- A second full verification waits without starting compiler or test children.
- No wrapper stage can use unbounded Vitest or Go parallelism.
- A simulated timeout leaves no child process running.
- Existing Codex hook tests and the new resource tests pass.
- A real focused Go test and focused frontend test still run normally.
- A dry-run or fake full verification demonstrates queue ordering without running the repository's complete suites.
