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

## Install the user timer

The installer builds the binary, installs it as `~/.local/bin/mcp-governor` with mode `0755`, creates governor-private config and state directories with mode `0700`, installs the example config as `~/.config/mcp-governor/config.json` with mode `0600` only when that file is absent, and enables the user timer. Existing directory modes and existing config content and permissions are preserved.

```sh
./scripts/install-user-units.sh
```

No `sudo` is needed. Inspect the schedule and recent runs with:

```sh
systemctl --user status mcp-governor-observe.timer
systemctl --user list-timers mcp-governor-observe.timer
journalctl --user-unit=mcp-governor-observe.service
jq . ~/.local/state/mcp-governor/snapshot.json
```

PSS (proportional set size) apportions shared pages among the processes using them, so it is useful for estimating a process's share of total memory. USS (unique set size) counts pages private to that process and is a lower bound on memory reclaimed if it exits. Snapshot warnings indicate incomplete or changing `/proc` data, permission limitations, or processes that exited during collection; review them before drawing conclusions from totals.

## Disable or uninstall

Stop future observations without removing files:

```sh
systemctl --user disable --now mcp-governor-observe.timer
```

To uninstall the executable and units while preserving configuration and snapshots:

```sh
systemctl --user disable --now mcp-governor-observe.timer
rm -f ~/.local/bin/mcp-governor
rm -f "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/mcp-governor-observe.service"
rm -f "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/mcp-governor-observe.timer"
systemctl --user daemon-reload
```

Configuration and collected snapshots are retained by default. To remove that data explicitly, also run:

```sh
rm -rf ~/.config/mcp-governor ~/.local/state/mcp-governor
```
