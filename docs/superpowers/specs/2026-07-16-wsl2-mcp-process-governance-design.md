# WSL2 MCP Process Governance Design

## Objective

Reduce duplicated heavy MCP processes across all Claude, Codex, and VS Code
sessions running in WSL2 without changing unrelated MCP behavior. Governance
will combine limited MCP connection changes with systemd-managed singleton
backends, per-repository services, lightweight client bridges, and explicit
lifecycle cleanup.

This phase covers Chroma, CodeGraph, Obsidian, claude-mem, and the supporting
governor. Browser concurrency control is a separate second phase.

## Current Baseline

The assessment observed a 16 GiB WSL2 VM with 9.5 GiB used, 1 GiB of swap in
use, 330 processes, seven Claude agents, and 168 MCP-related command lines.
Memory PSI was near zero during sampling, so the system was not actively
thrashing, but previous load and swap use indicate startup and concurrency
peaks.

RSS aggregation overstates physical memory because wrappers and runtimes share
pages. Even so, the process inventory showed material duplication:

- Eight Obsidian MCP processes at roughly 125 MiB RSS each.
- Eight headroom MCP processes at roughly 77 MiB RSS each.
- Chroma Python processes approaching 400 MiB RSS per heavy instance.
- Repeated CodeGraph, Playwright, and Chrome DevTools process trees.
- At least eight related processes adopted by PID 1.

Implementation must establish PSS/USS baselines using `smem` or
`/proc/<pid>/smaps_rollup` before claiming memory savings.

## Scope And Constraints

Governance applies to all Claude, Codex, and VS Code sessions inside WSL2.
Existing client startup workflows remain unchanged. Global MCP configuration
may be changed only for the selected heavy services.

Singleton scope is:

- Chroma: one backend per WSL2 user.
- Obsidian: one heavy service per WSL2 user.
- claude-mem: one worker per WSL2 user.
- CodeGraph: one heavy service per repository.
- Browser services: excluded from this phase.

The design does not create a universal MCP process pool. Lightweight per-client
stdio adapters may remain where required for transport compatibility.

## Verified Transport Constraints

The installed services do not share one common network transport:

- `chroma-mcp` is a stdio MCP server, but it can connect to a Chroma HTTP
  backend using `--client-type http`.
- `codegraph serve --mcp` supports stdio only in the installed version.
- `obsidian-mcp-server` 0.10.14 starts a stdio transport and exposes no verified
  HTTP server option.
- claude-mem already uses a user-level Bun worker with repeated MCP access
  processes around it.
- headroom MCP is a stdio access layer over the shared proxy at
  `127.0.0.1:8787`; it already follows the singleton-backend pattern and is not
  migrated in this phase.

Consequently, implementation uses a hybrid model. It must not assume that a
stdio server can safely serve multiple clients by sharing stdin and stdout.

## Architecture

Clients retain stdio MCP compatibility through small adapters. Adapters connect
over a Unix socket or localhost HTTP endpoint to services managed by the user
systemd instance.

```text
Claude / Codex / VS Code
        | stdio
        v
lightweight per-client adapter
        | Unix socket or localhost HTTP
        v
systemd --user services
  |-- Chroma backend: user singleton
  |-- Obsidian gateway: user singleton
  |-- claude-mem worker: user singleton
  `-- CodeGraph gateway: repository singleton
```

Where a safe multi-session MCP gateway is unavailable, the first release keeps
the MCP frontend per client and singletons only the heavy state or computation
backend. A service must not be called singleton unless measurement confirms the
heavy runtime is shared.

## Components

### User Services

The target service layout is:

```text
chroma.service
claude-mem-worker.service
obsidian-mcp-gateway.service
codegraph@<repo-id>.service
mcp-governor.service
```

Unit files use `Restart=on-failure`, bounded startup and stop timeouts,
`MemoryHigh`/`MemoryMax` where supported, and `TasksMax` limits derived from
baseline measurements. Services bind only to a Unix socket or loopback address.

### Client Adapters

Each adapter owns exactly one client MCP session. It records its client PID,
service name, repository identity, and connection time. On stdin closure,
parent death, or transport failure, it releases its service reference and
exits.

Adapters must preserve MCP request IDs, cancellation, notifications, errors,
and initialization state. They must not multiplex independent clients onto a
single stdio stream.

### Repository Identity

CodeGraph instances are keyed by the canonical repository root, resolved with
`realpath`. A stable escaped or hashed identifier maps that root to the systemd
template instance. Symlinks and worktrees must not accidentally point to the
same service unless they resolve to the same intended index.

### Governor

The governor starts in observation-only mode. It maintains the registry of
adapters and singleton services, reports duplicates and orphans, and exposes
machine-readable status. Cleanup is enabled only after the observation results
match expected ownership.

Cleanup uses recorded PID identity plus process start time or pidfd semantics,
not process-name matching. It never kills an unregistered process merely
because its command line contains `mcp`.

## Service-Specific Design

### Chroma

A systemd-managed Chroma HTTP backend owns persistent data. Per-client
`chroma-mcp` frontends use `--client-type http` and connect over loopback. The
existing persistent directory is backed up and migrated using Chroma-supported
data procedures before clients switch.

The rollout must measure whether embedding or model initialization remains in
each MCP frontend. If it does, backend singletonization alone is insufficient;
further frontend sharing requires a session-aware gateway and is deferred until
validated.

### claude-mem

The existing Bun worker becomes the authoritative systemd user service.
Shell-start and hook-start paths change from spawning behavior to an idempotent
service activation check. MCP access layers remain per session initially and
must terminate with their owning client.

### Obsidian

Because the installed server is stdio-only, a gateway must explicitly isolate
client MCP sessions while sharing the heavy Obsidian-facing state. Claude is
migrated first. Codex and VS Code migrate only after concurrent read, write,
notification, cancellation, and reconnect tests pass.

If the gateway cannot preserve session semantics, Obsidian remains per-client;
the implementation must report that limitation instead of presenting a shared
stdio stream as a safe singleton.

### CodeGraph

CodeGraph remains one heavy instance per canonical repository. Since the
installed MCP mode is stdio-only, a repository gateway is required for true
multi-client sharing. Stratum is the first repository used for validation.

File watching and index updates must remain correct under concurrent clients.
If the CodeGraph process cannot safely separate sessions behind the gateway,
the fallback is one frontend per client against shared repository artifacts,
provided concurrent access is supported by CodeGraph.

## Lifecycle

Services activate on first connection rather than starting together at WSL2
boot. Proposed idle policies are:

- Chroma, Obsidian, and claude-mem: eligible to stop after 30 minutes without
  active clients or background work.
- CodeGraph: eligible to stop 15 minutes after the last repository client
  disconnects.
- Client adapters: exit within 10 seconds of client termination or stdin
  closure.

The governor reconciles adapter references with live process identity. A
singleton crash is recovered by systemd. During recovery, clients receive an
explicit unavailable error; adapters must not bypass governance by spawning a
second heavy instance.

## Security And Isolation

Network listeners bind to Unix sockets with user-only permissions or to
loopback with equivalent authentication. No MCP endpoint is exposed to the
Windows LAN interface by default.

Obsidian preserves its existing vault path policy and write permissions.
CodeGraph requests are routed only to the repository associated with the
client adapter. Chroma retains the existing data directory, tenant, and
database semantics. Logs must not contain note contents, source contents,
credentials, or MCP request payloads by default.

## Rollout And Rollback

Migration is incremental:

1. Record process counts, PSS/USS, startup latency, open file descriptors, and
   idle/active CPU in observation-only mode.
2. Move the existing claude-mem worker under systemd management.
3. Start the Chroma HTTP backend and migrate MCP clients to HTTP client mode.
4. Deploy the Obsidian gateway for Claude, then Codex and VS Code after
   validation.
5. Deploy the per-repository CodeGraph gateway for Stratum, then enable it for
   other repositories.
6. Enable governor cleanup and resource limits after ownership data is proven.
7. Design browser concurrency control as a separate phase.

Each migration stores the previous service-specific configuration and provides
a command-level rollback. Failure rolls back only the current service. Stable
earlier migrations remain in place. Rollback never deletes persistent data or
indexes.

## Verification

Required functional tests include:

- Concurrent clients issue overlapping requests without response or
  notification cross-talk.
- Cancellation affects only the originating session.
- Client crash, terminal closure, reconnect, and WSL shutdown leave no adapter
  orphan.
- Singleton service crash recovers without creating a duplicate.
- Chroma persistence, Obsidian reads and writes, claude-mem retrieval, and
  CodeGraph indexing retain existing behavior.
- CodeGraph repository identity remains correct for normal clones, symlinks,
  and worktrees.
- A failed or unavailable singleton returns a clear MCP error.

Resource acceptance criteria under seven concurrent agents are:

- At most one Chroma backend, one claude-mem worker, and one Obsidian heavy
  service per user.
- At most one CodeGraph heavy service per canonical repository.
- Client adapters exit within 10 seconds after their clients.
- Registered orphans are reclaimed within one governor reconciliation period.
- PSS/USS and process counts are lower than the recorded baseline, with the
  savings reported per service rather than as an unqualified aggregate RSS
  total.

## Non-Goals

- A universal MCP pool for every server.
- Changing unrelated Claude, Codex, or VS Code MCP configuration.
- Sharing one raw stdio transport among multiple clients.
- Browser process pooling or BrowserContext design in this phase.
- Killing processes based only on names or command-line patterns.
- Exposing MCP services outside WSL2 localhost.
