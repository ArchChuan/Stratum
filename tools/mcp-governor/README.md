# MCP Governor

MCP Governor observes local MCP processes and records memory snapshots. It is observation-only: it sends no signals, does not stop or restart processes, does not rewrite configuration, and exposes no network listener.

Version 1 snapshots intentionally omit process argument vectors because they may contain credentials or other secrets. Arguments are used only in memory for service classification; the executable command name may be recorded.

## Build and run

Build the standalone module from this directory:

```sh
go build -o mcp-governor ./cmd/mcp-governor
```

Run one snapshot and emit JSON to standard output:

```sh
./mcp-governor snapshot --config config.example.json --output -
```

Omit `--output -` to write the configured snapshot file (by default, `~/.local/state/mcp-governor/snapshot.json`). The scanner reads Linux `/proc`; the systemd unit deliberately does not hide or restrict `/proc`.

## Install observation and report timers

Before changing any live client configuration, follow the [MCP observation rollout runbook](../../docs/operations/mcp-observation-runbook.md). It defines private backups, one-client-at-a-time gates, rollback, privacy inspection, and seven-day reporting.

The installer builds the binary, installs it as `~/.local/bin/mcp-governor` with mode `0755`, and creates the governor config, state, event, and report directories with mode `0700`. Newly created systemd user-unit directories also use `0700`; existing current-user-owned `XDG_CONFIG_HOME`, `systemd`, and `systemd/user` directories may retain non-writable modes such as `0755` for compatibility with earlier installs. It generates a 32-byte identity salt at `~/.config/mcp-governor/identity-salt` and installs the example catalog as `~/.config/mcp-governor/config.json`, both with mode `0600`, only when each file is absent. The installer validates ownership and type for `HOME`, the config/state/unit target directories, and managed files. Existing salt and catalog bytes are preserved; unsafe target symlinks, special files, foreign ownership, or group/world-writable validated roots cause installation to stop.

```sh
./scripts/install-user-units.sh
```

No `sudo` is needed. Observation snapshots run every minute. At 00:15 local time each day, the report timer aggregates the previous seven complete local calendar days into `~/.local/state/mcp-governor/reports/`; persistent timer catch-up is enabled. The timers only observe and report: they never automatically disable, stop, restart, or rewrite an MCP service.

Inspect the schedules and recent runs with:

```sh
systemctl --user status mcp-governor-observe.timer
systemctl --user status mcp-governor-report.timer
systemctl --user list-timers 'mcp-governor-*'
journalctl --user-unit=mcp-governor-observe.service
journalctl --user-unit=mcp-governor-report.service
jq . ~/.local/state/mcp-governor/snapshot.json
```

Generate the same durable seven-day report manually (using local-midnight calendar boundaries, including across daylight-saving transitions):

```sh
mcp-governor report-latest --config ~/.config/mcp-governor/config.json
```

PSS (proportional set size) apportions shared pages among the processes using them, so it is useful for estimating a process's share of total memory. USS (unique set size) counts pages private to that process and is a lower bound on memory reclaimed if it exits. Snapshot warnings indicate incomplete or changing `/proc` data, permission limitations, or processes that exited during collection; review them before drawing conclusions from totals.

## Disposable cross-client E2E

The [observation E2E harness](scripts/e2e-observation.sh) builds the governor and deterministic fake MCP server in a private temporary area, renders and validates all four native client config shapes, launches their rendered wrapper commands with isolated test sessions, exercises overlapping calls, cancellation and abrupt disconnect, checks metadata-only persistence and private modes, and smoke-tests report aggregation. It sets fake `HOME`, XDG, and state roots and never reads or writes live client configuration.

Run its shell acceptance test:

```sh
./scripts/e2e_observation_test.sh
```

## Disable or uninstall

Stop future observations without removing files:

```sh
systemctl --user disable --now mcp-governor-observe.timer mcp-governor-report.timer
```

To uninstall the executable and units while preserving configuration and snapshots:

```sh
systemctl --user disable --now mcp-governor-observe.timer mcp-governor-report.timer
rm -f ~/.local/bin/mcp-governor
rm -f "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/mcp-governor-observe.service"
rm -f "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/mcp-governor-observe.timer"
rm -f "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/mcp-governor-report.service"
rm -f "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/mcp-governor-report.timer"
systemctl --user daemon-reload
```

Configuration and collected snapshots are retained by default. To remove that data explicitly, also run:

```sh
rm -rf ~/.config/mcp-governor ~/.local/state/mcp-governor
```
