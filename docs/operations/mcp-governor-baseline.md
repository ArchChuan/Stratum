# MCP Governor Baseline

Captured on 2026-07-16 in WSL. This is an observation-only baseline: no
Claude, Codex, VS Code, browser, or MCP process was stopped, restarted, or
reconfigured.

## Installation

The user timer is installed and active. The invoking shell did not export the
user bus variables, so installation was repeated with
`XDG_RUNTIME_DIR=/run/user/1000` and the matching user D-Bus address. No
privilege escalation was used.

Installed paths:

| Artifact | Path | Mode |
| --- | --- | ---: |
| Binary | `~/.local/bin/mcp-governor` | `0755` |
| Configuration | `~/.config/mcp-governor/config.json` | `0600` |
| State directory | `~/.local/state/mcp-governor` | `0700` |
| Snapshot | `~/.local/state/mcp-governor/snapshot.json` | `0600` |
| Service | `~/.config/systemd/user/mcp-governor-observe.service` | `0644` |
| Timer | `~/.config/systemd/user/mcp-governor-observe.timer` | `0644` |

`systemctl --user list-timers` listed `mcp-governor-observe.timer`, and an
explicit service start completed successfully. The journal contained normal
start/finish entries and no systemd sandbox failure. The service sandbox does,
however, affect memory detail as described below.

## Observation windows

These windows are labels for the activity intentionally generated during the
measurement. They do not imply the WSL instance or its existing sessions were
fully idle.

### Quiescent observational window

After 12 seconds with no deliberate MCP operation, the installed service was
started. The window began at `2026-07-16T20:59:49.761971457+08:00`; the snapshot
timestamp is `2026-07-16T20:59:49.887722192+08:00`, and the service invocation
took 139 ms. A private `0600` copy was retained under the governor's `0700`
state directory for analysis.

| Service | Processes | RSS MiB | PSS MiB | USS MiB | Orphans |
| --- | ---: | ---: | ---: | ---: | ---: |
| chroma | 6 | 843.5 | unavailable | unavailable | 0 |
| chrome-devtools | 16 | 404.3 | unavailable | unavailable | 0 |
| claude-mem | 8 | 265.1 | unavailable | unavailable | 0 |
| codegraph | 31 | 871.3 | unavailable | unavailable | 0 |
| headroom | 14 | 600.5 | unavailable | unavailable | 0 |
| obsidian | 14 | 1100.0 | unavailable | unavailable | 0 |
| playwright | 30 | 950.6 | unavailable | unavailable | 0 |
| **Classified total** | **119** | **5035.5** | **unavailable** | **unavailable** | **0** |

There were 431 warnings: 119 classified-service and 312 general process-scan
`smaps_rollup` permission denials. The serialized zero PSS/USS values in this
snapshot mean unavailable, not zero consumption.

### Active/current-concurrency observational window

The existing concurrent Claude, Codex, VS Code, and MCP sessions supplied the
load. Only safe read-only discovery and timer-status operations were performed;
no browser or synthetic workload was started. The direct CLI window began at
`2026-07-16T21:00:08.009105235+08:00`; the snapshot timestamp is
`2026-07-16T21:00:08.458532409+08:00`, and collection took 455 ms.

| Service | Processes | RSS MiB | PSS MiB | USS MiB | Orphans |
| --- | ---: | ---: | ---: | ---: | ---: |
| chroma | 6 | 843.5 | 825.5 | 823.9 | 0 |
| chrome-devtools | 16 | 404.3 | 299.9 | 298.1 | 0 |
| claude-mem | 8 | 265.1 | 153.3 | 151.4 | 0 |
| codegraph | 31 | 871.3 | 405.9 | 368.8 | 0 |
| headroom | 14 | 600.5 | 473.7 | 460.0 | 0 |
| obsidian | 14 | 1100.0 | 775.0 | 768.2 | 0 |
| playwright | 30 | 950.6 | 686.3 | 681.3 | 0 |
| **Classified total** | **119** | **5035.5** | **3619.6** | **3551.8** | **0** |

PSS and USS are authoritative for memory governance. RSS includes shared pages
in every process that maps them, so summing RSS double-counts memory and must
not be used to set a limit. The active capture had 91 warnings, all general
process-scan `smaps_rollup` permission denials; none concerned a classified
service. There were no malformed or fatal warnings. The difference occurs
because the direct CLI could inspect the classified descendants available to
this session, whereas the hardened user service was denied those unrelated
process trees. This is a measurement-access limitation, not a unit startup or
sandbox setup failure.

At the active timestamp, the host had 15.3 GiB RAM, 2.14 GiB available, and
only 8.6 MiB of 4.0 GiB swap free. Memory PSI was zero across 10/60/300-second
averages; CPU `some` PSI was 1.16/1.09/1.03 percent, and I/O PSI was
0.00/0.01/0.28 percent. Swap exhaustion and low available memory justify a
longer observation even though immediate memory pressure was absent.

## Ownership and reclaimability

The installed configuration has no registry entries. Both snapshots contained
zero `.processes[] | select(.orphan)` rows, so no process is reclaimable.
Reclaimability requires an exact registry match on PID, start ticks, and client
identity; classification or PPID alone is insufficient.

The active snapshot separately contained three unregistered PPID-1 roots: two
`chroma` roots and one `codegraph` root. Immediately after the snapshot, each
still had the exact captured start ticks, PPID 1, sleeping status, and one
child. They are informational only, may change after the stated timestamp, and
are explicitly **not reclaimable** without registry-backed ownership.

## Preliminary recommendations

MemoryHigh, MemoryMax, and TasksMax values are deferred. A two-point sample,
with only one authoritative PSS/USS point, cannot establish peaks, and no limit
should be placed below observed active PSS plus a documented safety margin.
Collect direct, ownership-aware samples at one-minute cadence for at least 24
hours, including normal peak activity, then derive MemoryHigh from a high
percentile plus headroom and MemoryMax from the observed peak plus a larger
recovery margin. Derive TasksMax from peak task count, not the current process
count alone.

The first migration targets are:

1. `chroma`: highest measured target PSS at 825.5 MiB across 6 processes.
   Launch its root through a dedicated user unit/scope and write an atomic
   registry record containing the exact PID, start ticks, and client identity.
2. `claude-mem`: 153.3 MiB PSS across 8 processes. Apply the same owned-root
   launch and registry lifecycle next, preserving per-client separation before
   any enforcement is enabled.

Keep both migrations observation-only until the 24-hour series validates
ownership matching and supplies defensible limits. Separately, investigate a
systemd-compatible way to collect PSS/USS under WSL without weakening process
isolation broadly; until then, timer snapshots remain useful for counts and RSS
trends but not memory-limit sizing.

## Rollback

The active timer was not uninstalled during this baseline. To disable future
observations and verify the result:

```sh
systemctl --user disable --now mcp-governor-observe.timer
systemctl --user is-enabled mcp-governor-observe.timer
systemctl --user is-active mcp-governor-observe.timer
```

To uninstall executable and units while preserving configuration and state:

```sh
systemctl --user disable --now mcp-governor-observe.timer
rm -f ~/.local/bin/mcp-governor
rm -f "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/mcp-governor-observe.service"
rm -f "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/mcp-governor-observe.timer"
systemctl --user daemon-reload
systemctl --user is-enabled mcp-governor-observe.timer
systemctl --user is-active mcp-governor-observe.timer
```

Removing `~/.config/mcp-governor` or `~/.local/state/mcp-governor` is a separate,
explicit data-destruction decision.
