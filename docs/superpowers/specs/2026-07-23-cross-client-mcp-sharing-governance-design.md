# Cross-Client MCP Sharing And Governance Design

## Status

Approved in conversation on 2026-07-23. This design extends
`2026-07-16-wsl2-mcp-process-governance-design.md`. It replaces that document's
outdated assumption that `code-review-graph` is stdio-only: the installed CLI
now supports Streamable HTTP with `serve --http`.

## Objective

Reduce duplicated MCP processes across Codex, Claude Code, VS Code, and Lingma
inside WSL2 while preserving per-client session isolation. Keep all current
code-graph MCPs during a seven-day observation window, then use measured tool
value and resource cost to recommend whether each service should remain shared,
become on-demand, be consolidated, or continue under observation.

The first release shares heavy services and state where the installed server
has a verified multi-client transport. It does not multiplex multiple clients
onto one raw stdio stream.

## Current Evidence

The runtime investigation on 2026-07-23 observed:

- a 12-CPU WSL2 VM with load averages above 200 during the incident;
- 6 GiB swap effectively exhausted;
- 43–64 percent CPU I/O wait during active samples;
- repeated per-agent trees for code-review-graph, codebase-memory, CodeGraph,
  Playwright, Obsidian, Fetch, Delve, Sequential Thinking, Yinxiang, and
  claude-mem access;
- 19 `code-review-graph --auto-watch` instances in the earlier incident window;
- a later governor snapshot with CodeGraph at 355 MiB PSS and Playwright at
  590 MiB PSS;
- Codex configuration that starts all configured stdio MCPs per session;
- Claude Code configuration with an overlapping but non-identical MCP set.

The existing `tools/mcp-governor` implementation is observation-only. It
captures process counts and PSS/USS but does not yet observe MCP tool calls,
manage services, route sessions, or clean up processes.

## Evidence And Claim Matrix

| Claim | Project/runtime evidence | Long-term knowledge | Upstream evidence | Boundary |
| --- | --- | --- | --- | --- |
| Per-session MCP startup is exhausting WSL resources | Process trees, PSI, `vmstat`, governor snapshot, client configs | Obsidian retrieval protocol says runtime evidence is authoritative | Not required for the local fact | Snapshot-specific; must remeasure after migration |
| Streamable HTTP can carry isolated MCP sessions | Installed `code-review-graph serve --help` exposes `--http` | The relevant Obsidian architecture note is provisional only | MCP 2025-11-25 transport specification defines `MCP-Session-Id`, session termination, and stream separation | Each server still requires concurrent-session verification |
| Playwright can share a server without sharing browser state | Installed Playwright MCP help exposes `--port`, `--endpoint`, and `--shared-browser-context` | No verified Obsidian note was used as key evidence | Microsoft Playwright MCP upstream CLI/README | Do not enable shared browser context for this design |
| Raw stdio must not be shared between independent clients | Existing design and installed stdio-only services | Provisional adapter principle is only supporting context | MCP lifecycle and JSON-RPC correlation requirements | A session-aware gateway may be added after tests |

## Chosen Approach

Use native network transports first, shared heavy backends second, and small
per-client adapters only where required. Do not build a universal MCP gateway
in the first release.

Alternatives rejected for the first release:

1. A universal gateway would centralize configuration, but it creates a larger
   correctness and security boundary around initialization, notifications,
   cancellation, concurrent writes, and transport-version compatibility.
2. On-demand per-client startup alone is simpler, but parallel agents would
   still duplicate heavy services and watchers.

## Architecture

```text
Codex / Claude Code / VS Code / Lingma
                 |
        independent MCP session
                 |
      localhost Router / Observer
          |                 |
          |                 `-- metadata-only metrics and lifecycle registry
          |
   +------+-----------------------------+
   | native shared services             | session-local or stdio frontends
   |                                    |
   | code-review-graph HTTP per repo     | codebase-memory frontend
   | Playwright network service          | Delve on-demand session
   | shared Chrome/CDP backend           | Sequential Thinking session
   | other verified network services     | unverified stdio services
   +------------------------------------+
```

Sharing applies to heavy processes and durable state, not to Agent session
state. Every client connection receives an independent session identity.

All listeners bind to a user-only Unix socket or `127.0.0.1`. A shared service
failure returns an explicit unavailable error; adapters must not silently fall
back to spawning an unmanaged duplicate.

## Service Classification

### Native sharing: first migration wave

#### code-review-graph

- Run one Streamable HTTP service per canonical repository identity.
- Use `realpath` plus a stable hash to derive the systemd instance ID.
- Keep one watcher per service instead of one watcher per Agent.
- Treat separate Git worktrees as separate indexes by default. Symlinks to the
  same intended repository resolve to one instance.
- Preserve all current tools during the seven-day observation window.

#### Playwright MCP

- Run one network MCP service per WSL user.
- Create an independent BrowserContext, page set, cookie jar, storage state,
  and output directory per MCP session.
- Do not enable `--shared-browser-context`.
- A BrowserContext crash or cleanup affects only its owning session.
- Permit one shared browser process initially; add a bounded browser pool only
  if measurements show the single process is a bottleneck.

### Shared heavy backend with session adapters: second migration wave

#### Chrome DevTools

- Share the Chrome/CDP backend.
- Allocate independent targets and debugging sessions to clients.
- Do not allow one client to attach to or close another client's target.

#### claude-mem / mcp-search

- Keep one user-level worker as the authoritative heavy process.
- Replace hook and shell spawn paths with idempotent activation checks.
- Retain a lightweight per-session MCP access layer until a native multi-client
  transport is verified.

#### codebase-memory

- The installed 0.8.1 CLI exposes stdio only.
- Share its persistent index and expensive indexing work where supported.
- Retain the lightweight frontend per client during the observation window.
- Do not route multiple clients through one stdio stream.

### Gateway candidates: third migration wave

The following services remain per-client until concurrent initialization,
notification, cancellation, reconnect, and write tests pass:

- Obsidian/Seekstone;
- Yinxiang;
- Fetch;
- the file-backed Memory server.

Seekstone is currently documented as stdio-only and reads the vault directly.
A gateway must serialize conflicting writes by resource key and preserve vault
path confinement. Yinxiang and Memory require equivalent concurrent-write and
atomicity tests. Fetch is stateless and a lower-risk gateway candidate, but its
resource benefit must justify the extra component.

### Deliberately session-local

- Sequential Thinking remains session-local because its state belongs to the
  current reasoning session.
- `mcp-delve` remains session-local because it owns mutable debugger state and
  one debug target. It changes from eager startup to activation on first debug
  tool call and stops after idle timeout.

### Observe before consolidation

`codegraph`, `code-review-graph`, and `codebase-memory` all remain enabled for
seven days. The report may recommend consolidation, but the observation system
must not disable or remove any server automatically.

## Service Management

Target user services are:

```text
mcp-router.service
mcp-governor-observe.timer
code-review-graph@<repo-id>.service
playwright-mcp.service
chrome-cdp.service
claude-mem-worker.service
```

Services activate on first use instead of all starting at WSL boot. Proposed
idle policies are:

- code graph services: stop 15 minutes after the last client disconnects;
- browser services: stop 10 minutes after the last client disconnects;
- Memory and knowledge services: stop after 30 idle minutes;
- client adapters: exit within 10 seconds of stdin closure or parent death.

Use `Restart=on-failure` with bounded restart frequency, startup/stop timeouts,
`TasksMax`, and measured `MemoryHigh`/`MemoryMax` values. WSL shutdown stops
services in reverse dependency order and waits for browser, watcher, and child
process termination.

The registry identifies a process by PID plus start time or pidfd semantics.
It never kills a process based only on its name or argument substring.

## Session And Data Isolation

- Assign an independent, unguessable session ID to every client connection.
- Partition temporary and output paths by hashed client and session identity.
- Never persist raw credentials, prompts, source content, note content, URLs
  with query strings, cookies, or tool arguments in metrics.
- Playwright BrowserContexts, pages, cookies, downloads, traces, and storage
  state are session-owned.
- Delve permits one debug target per session.
- Sequential Thinking state never crosses a session boundary.
- Memory, Obsidian, and Yinxiang writes use resource-keyed serialization;
  independent reads use bounded concurrency.
- Disconnect cleanup removes only the owning session's ephemeral resources.

## Cross-Client Configuration Governance

Create one service catalog containing MCP name, executable or endpoint,
transport, scope, session policy, idle policy, security policy, and client
availability. Generate Codex, Claude Code, VS Code, and Lingma configuration
from that catalog to prevent four manually maintained configurations from
drifting.

Workspace configuration may select which services are enabled, but it cannot
override loopback binding, credential handling, session isolation, or process
ownership rules.

Credentials remain in client-managed secure configuration or separate `0600`
environment files. Generated configuration, process arguments, metrics, and
logs contain no credential values.

## Seven-Day Observation

### Phases

1. Record a pre-migration per-client baseline.
2. Migrate native-shareable services one at a time.
3. Continue collecting the same metrics after each migration.
4. Produce the final report after seven full days of representative activity.

This preserves both the original duplication cost and the post-sharing result.

### Metrics

Record only metadata:

- client type: Codex, Claude Code, VS Code, or Lingma;
- server and tool name;
- call count, active days, and distinct hashed sessions;
- success, failure, cancellation, and timeout counts;
- P50 and P95 latency;
- response byte-size distribution without response content;
- maximum concurrency;
- process starts and cold-start duration;
- idle CPU, peak PSS, peak USS, and task count;
- resource cost per effective call.

An effective hit is a successful tool call with a non-empty result that is not
an error or help-only response. Initialization, tool listing, and health checks
do not count as tool hits.

Store raw events for 14 days and retain only the aggregate report afterward.
The state directory is `0700`; event and report files are `0600`. Repository
and session identifiers use a machine-local salted hash.

### Decision Output

For each server and tool, the report assigns one recommendation:

- keep as a shared resident service;
- keep but activate on demand;
- consolidation candidate;
- disable candidate;
- insufficient data, continue observing.

The report includes a code-graph capability overlap matrix, but makes no
automatic configuration change.

## Failure Handling

- systemd performs limited restart of shared services;
- clients receive `service_unavailable` during recovery;
- a failed session does not terminate unrelated sessions;
- write operations do not receive unsafe automatic retries;
- idempotent reads may use a bounded retry inside a total timeout budget;
- resource thresholds stop admission of new sessions before disrupting active
  sessions;
- configuration rollback restores the previous stdio mode for only the current
  migration wave;
- rollback never deletes indexes, browser profiles, Memory data, or notes.

## Verification

### Protocol and functional tests

- All four client types initialize, discover tools, and call them successfully.
- Concurrent clients receive only their own responses and notifications.
- Cancellation affects only the originating session.
- Client crash, terminal closure, reconnect, and WSL shutdown leave no session
  adapter or BrowserContext orphan.
- A singleton crash recovers without starting a duplicate.
- Clone, symlink, and worktree repository identities map as designed.
- Concurrent Obsidian, Yinxiang, and Memory writes do not lose, overwrite, or
  partially apply data.
- Delve and Sequential Thinking state cannot cross sessions.

### Resource acceptance criteria

Under seven concurrent Agents:

- at most one `code-review-graph` heavy process and watcher per repository;
- at most one Playwright service process group, with one BrowserContext per
  active session;
- client adapters exit within 10 seconds of client termination;
- registered orphans are identified within one governor reconciliation period;
- total MCP PSS is at least 50 percent below the pre-migration baseline;
- MCP process count is at least 60 percent below baseline;
- MCP load no longer keeps swap continuously exhausted;
- idle CPU I/O wait remains below 10 percent when no MCP work is active.

PSS and USS are authoritative for memory acceptance. Aggregate RSS must not be
used to claim savings.

## Rollout And Rollback

1. Extend the governor with metadata-only tool-call observation.
2. Capture the pre-migration baseline.
3. Migrate `code-review-graph` to per-repository HTTP services.
4. Migrate Playwright with BrowserContext isolation.
5. Migrate shared-backend services.
6. Run gateway compatibility experiments for stdio services.
7. Complete the seven-day report and request an explicit consolidation
   decision.

Only one service class migrates at a time. Any cross-session state leak,
write-integrity failure, or sustained outage rolls back that migration wave.
Stable earlier waves remain deployed.

## Non-Goals

- Removing or disabling any code-graph MCP before the seven-day report.
- Sharing raw stdio between clients.
- Sharing Playwright BrowserContexts, cookies, pages, or output directories.
- Exposing MCP endpoints outside WSL loopback.
- Recording prompts, arguments, source, note content, response content, or
  credentials.
- Killing unregistered processes based on command names.
- Automatically applying the report's consolidation recommendations.

## Sources

- Repository runtime and configuration evidence captured on 2026-07-23.
- `docs/superpowers/specs/2026-07-16-wsl2-mcp-process-governance-design.md`.
- `docs/operations/mcp-governor-baseline.md`.
- Installed `code-review-graph serve --help` output.
- Installed `codebase-memory-mcp --help` output, version 0.8.1.
- Installed Playwright MCP `--help` output.
- Model Context Protocol specification 2025-11-25, Transports:
  <https://modelcontextprotocol.io/specification/2025-11-25/basic/transports>.
- Microsoft Playwright MCP upstream documentation:
  <https://github.com/microsoft/playwright-mcp>.
- Seekstone installed README.
- Obsidian `99-系统/知识输入与证据检索协议.md`.
- Obsidian note `AI 原生系统的核心能力应独立于触发与接入适配器.md`,
  treated as provisional supporting context rather than key evidence.
