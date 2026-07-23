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

The installer builds the binary, installs it as `~/.local/bin/mcp-governor` with mode `0755`, and creates private config, state, event, report, and user-unit directories with mode `0700`. It generates a 32-byte identity salt at `~/.config/mcp-governor/identity-salt` and installs the example catalog as `~/.config/mcp-governor/config.json`, both with mode `0600`, only when each file is absent. Existing salt and catalog files must already be private regular files; the installer validates and preserves their bytes. Unsafe symlinks, special files, permissions, or writable parent directories cause installation to stop.

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
