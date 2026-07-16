# MCP Governor Baseline

Captured on 2026-07-16 in WSL. This is an observation-only baseline: no
Claude, Codex, VS Code, browser, or MCP process was stopped, restarted, or
reconfigured.

## Installation

The user timer is installed and active. The invoking shell did not export the
user bus variables, so installation was repeated with
`XDG_RUNTIME_DIR=/run/user/1000` and the matching user D-Bus address. Commands
below derive these values rather than assuming UID 1000. No privilege
escalation was used.

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
start/finish entries and no systemd sandbox failure. A post-baseline unit fix
restored scheduled PSS/USS visibility as described below.

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
this session, whereas the original user service entered a different user
namespace. This was the symptom that prompted the root-cause experiment below;
it is preserved here as historical evidence and is fixed in the installed unit.

At the active timestamp, the host had 15.3 GiB RAM, 2.14 GiB available, and
only 8.6 MiB of 4.0 GiB swap free. Memory PSI was zero across 10/60/300-second
averages; CPU `some` PSI was 1.16/1.09/1.03 percent, and I/O PSI was
0.00/0.01/0.28 percent. Swap exhaustion and low available memory justify a
longer observation even though immediate memory pressure was absent.

## Scheduled-service correction

Transient user services isolated the original unit properties one at a time.
Each row used the same binary, configuration, host activity, and JSON
aggregation. Values are bytes. Classified warning counts parse the
`process <pid>` identity from each warning and intersect it with the PIDs in
`.processes`; non-process warnings are ignored.

| Case | Chroma PSS | claude-mem PSS | Classified warnings | Total warnings |
| --- | ---: | ---: | ---: | ---: |
| Baseline transient unit | 851865600 | 160344064 | 0 | 92 |
| `NoNewPrivileges=yes` | 851865600 | not separately sampled | 0 | 92 |
| `PrivateTmp=yes` | 0 | not separately sampled | 119 | 426 |
| `ProtectSystem=strict` | 0 | not separately sampled | 119 | 426 |
| `ProtectHome=read-only` | 0 | not separately sampled | 119 | 426 |
| `ReadWritePaths=state-dir` | 0 | not separately sampled | 119 | 426 |
| `RestrictAddressFamilies=AF_UNIX` | 851865600 | not separately sampled | 0 | 92 |
| `LockPersonality=yes` | 851865600 | not separately sampled | 0 | 92 |
| Retained three-directive combination | 851863552 | 160344064 | 0 | 92 |
| Four filesystem directives together | 0 | 0 | 119 | 430 |
| Original seven-directive combination | 0 | 0 | 119 | 430 |

On systemd 255 in this WSL environment, the baseline transient service shared
the host user namespace, while `PrivateTmp=yes` alone created both a new mount
namespace and a new user namespace. UID/GID, capabilities, `NoNewPrivs`,
seccomp state, and `/proc` mount options were otherwise equal. The other three
filesystem directives independently produced the same PSS loss. With
`kernel.yama.ptrace_scope=1`, cross-user-namespace `smaps_rollup` reads fail
with `EACCES`. This common filesystem-namespace side effect, not
`NoNewPrivileges`, was the confirmed root cause.

The unit therefore retains `NoNewPrivileges=yes`,
`RestrictAddressFamilies=AF_UNIX`, and `LockPersonality=yes`, and removes only
the four directives proven to create the incompatible namespace. After
reinstallation, an explicit scheduled-service capture began at
`2026-07-16T21:08:30.283366602+08:00`, completed in 486 ms, and recorded
`2026-07-16T21:08:30.756072158+08:00`.

| Service | Processes | RSS MiB | PSS MiB | USS MiB | Orphans |
| --- | ---: | ---: | ---: | ---: | ---: |
| chroma | 6 | 830.6 | 812.4 | 810.8 | 0 |
| chrome-devtools | 16 | 404.8 | 299.3 | 297.3 | 0 |
| claude-mem | 8 | 265.4 | 152.9 | 151.0 | 0 |
| codegraph | 31 | 887.7 | 404.8 | 369.7 | 0 |
| headroom | 14 | 597.7 | 472.1 | 459.6 | 0 |
| obsidian | 14 | 1064.0 | 737.3 | 730.6 | 0 |
| playwright | 30 | 925.7 | 662.3 | 657.3 | 0 |
| **Classified total** | **119** | **4976.0** | **3541.2** | **3476.2** | **0** |

The corrected scheduled snapshot had 92 general process-scan permission
warnings and **zero PID-correlated classified-process warnings**. The corrected
query was also run against the retained original seven-directive artifact and
returned 119, then against the retained post-fix artifact and returned 0. At
`2026-07-16T21:08:30.797837287+08:00`, 2.34 GiB RAM was available and only
1.8 MiB of 4 GiB swap was free. Memory PSI `some/full` was
0.04/0.04, 0.03/0.03, and 0.04/0.03 percent over 10/60/300 seconds; CPU `some`
was 2.74/1.01/0.92 percent, and I/O `some` was 0.04/0.03/0.07 percent.

## Reproducing captures

Set up the user-manager environment once in shells that do not inherit it:

```sh
export XDG_RUNTIME_DIR="/run/user/$(id -u)"
export DBUS_SESSION_BUS_ADDRESS="unix:path=$XDG_RUNTIME_DIR/bus"
systemctl --user is-active mcp-governor-observe.timer
```

Capture a quiescent observational window without claiming machine idleness:

```sh
capture_dir=$(mktemp -d "$HOME/.local/state/mcp-governor/baseline.XXXXXX")
chmod 0700 "$capture_dir"
sleep 12
window_start=$(date --iso-8601=ns)
before=$(date +%s%N)
systemctl --user start mcp-governor-observe.service
after=$(date +%s%N)
cp "$HOME/.local/state/mcp-governor/snapshot.json" "$capture_dir/quiescent.json"
chmod 0600 "$capture_dir/quiescent.json"
printf 'start=%s elapsed_ms=%s captured_at=%s\n' "$window_start" \
  "$(( (after-before)/1000000 ))" \
  "$(jq -r .captured_at "$capture_dir/quiescent.json")"
```

For an active/current-concurrency window, allow existing natural session load,
perform only available read-only status operations, and capture without
starting a browser or synthetic workload:

```sh
systemctl --user list-timers mcp-governor-observe.timer --no-pager >/dev/null
window_start=$(date --iso-8601=ns)
before=$(date +%s%N)
systemctl --user start mcp-governor-observe.service
after=$(date +%s%N)
cp "$HOME/.local/state/mcp-governor/snapshot.json" "$capture_dir/active.json"
chmod 0600 "$capture_dir/active.json"
printf 'start=%s elapsed_ms=%s captured_at=%s\n' "$window_start" \
  "$(( (after-before)/1000000 ))" \
  "$(jq -r .captured_at "$capture_dir/active.json")"
```

Aggregate without printing command arguments or payloads:

```sh
jq '(.processes | map({key:(.pid|tostring),value:true}) | from_entries) as $pids |
  ([.warnings[] |
    (try capture("(?:^|: )process (?<pid>[0-9]+)(?: |:)") catch null) |
    select(. != null and $pids[.pid])] | length) as $classified_warnings |
  {services, total:{processes:(.services|map(.processes)|add),
  rss_bytes:(.services|map(.rss_bytes)|add),
  pss_bytes:(.services|map(.pss_bytes)|add),
  uss_bytes:(.services|map(.uss_bytes)|add),
  orphans:([.processes[]|select(.orphan)]|length),
  warnings:(.warnings|length),
  classified_warnings:$classified_warnings}}' \
  "$capture_dir/active.json"
jq '{permission_denied:([.warnings[]|select(test("permission denied"))]|length),
  malformed_or_fatal:([.warnings[]|select(test("malformed|fatal|parse";"i"))]|length)}' \
  "$capture_dir/active.json"
free -b
for resource in memory cpu io; do
  printf '%s\n' "$resource"
  sed -n '1,2p' "/proc/pressure/$resource"
done
```

For the recommended 24-hour evidence window, preserve 1,441 private samples
separated by approximately one minute and emit only aggregate values. The
collector rejects failed, malformed, or stale observations and verifies at
least 24 hours of coverage from the first and last captured timestamps. It does
not print process arguments:

```sh
set -euo pipefail
umask 077
evidence_dir=$(mktemp -d "$HOME/.local/state/mcp-governor/evidence.XXXXXX")
chmod 0700 "$evidence_dir"
source_snapshot="$HOME/.local/state/mcp-governor/snapshot.json"
metrics="$evidence_dir/metrics.tsv"
: >"$metrics"
chmod 0600 "$metrics"
previous_captured_at=$(jq -er '.captured_at // empty' "$source_snapshot" \
  2>/dev/null || true)

for sample_number in $(seq 1 1441); do
  sample=$(printf '%04d' "$sample_number")
  systemctl --user start mcp-governor-observe.service
  test -f "$source_snapshot"
  captured_at=$(jq -er '
    select(.version == 1 and .mode == "observe") |
    .captured_at | select(type == "string" and length > 0)' "$source_snapshot")
  test "$captured_at" != "$previous_captured_at"

  snapshot="$evidence_dir/$sample.json"
  install -m 0600 "$source_snapshot" "$snapshot"
  test "$(jq -er '.captured_at' "$snapshot")" = "$captured_at"
  jq -r '(.processes | map({key:(.pid|tostring),value:true}) |
      from_entries) as $pids |
    ([.warnings[] |
      (try capture("(?:^|: )process (?<pid>[0-9]+)(?: |:)") catch null) |
      select(. != null and $pids[.pid])] | length) as $classified_warnings |
    [.captured_at, (.services|map(.processes)|add),
     (.services|map(.pss_bytes)|add), (.services|map(.uss_bytes)|add),
     $classified_warnings] | @tsv' "$snapshot" >>"$metrics"
  previous_captured_at=$captured_at
  if test "$sample_number" -lt 1441; then sleep 60; fi
done

first_captured_at=$(awk 'NR == 1 {print $1}' "$metrics")
last_captured_at=$(awk 'END {print $1}' "$metrics")
coverage_seconds=$((
  $(date -d "$last_captured_at" +%s) - $(date -d "$first_captured_at" +%s)
))
test "$coverage_seconds" -ge 86400
printf 'samples=1441 coverage_seconds=%s first=%s last=%s\n' \
  "$coverage_seconds" "$first_captured_at" "$last_captured_at"
```

The freshness guard can be reproduced without invoking the service or touching
the live snapshot. This fixture deliberately reuses its previous timestamp; a
successful check would be an error:

```sh
set -euo pipefail
umask 077
fixture=$(mktemp)
trap 'rm -f "$fixture"' EXIT
printf '%s\n' '{"version":1,"mode":"observe","captured_at":"2026-01-01T00:00:00Z"}' \
  >"$fixture"
chmod 0600 "$fixture"
previous_captured_at=2026-01-01T00:00:00Z
captured_at=$(jq -er '
  select(.version == 1 and .mode == "observe") |
  .captured_at | select(type == "string" and length > 0)' "$fixture")
if test "$captured_at" != "$previous_captured_at"; then
  printf 'ERROR: stale fixture was accepted\n' >&2
  exit 1
fi
printf 'stale snapshot rejected\n'
```

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

MemoryHigh, MemoryMax, and TasksMax values are deferred. Two authoritative
PSS/USS points cannot establish peaks, and no limit should be placed below
observed active PSS plus a documented safety margin.
Collect scheduled, ownership-aware samples at one-minute cadence for at least 24
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
ownership matching and supplies defensible limits. Separately, investigate
whether systemd scopes can restore filesystem isolation around owned target
services independently of the observation process.

## Rollback

The active timer was not uninstalled during this baseline. To disable future
observations and verify the result:

```sh
export XDG_RUNTIME_DIR="/run/user/$(id -u)"
export DBUS_SESSION_BUS_ADDRESS="unix:path=$XDG_RUNTIME_DIR/bus"
systemctl --user disable --now mcp-governor-observe.timer
systemctl --user is-enabled mcp-governor-observe.timer
systemctl --user is-active mcp-governor-observe.timer
```

To uninstall executable and units while preserving configuration and state:

```sh
export XDG_RUNTIME_DIR="/run/user/$(id -u)"
export DBUS_SESSION_BUS_ADDRESS="unix:path=$XDG_RUNTIME_DIR/bus"
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
