# Headroom Claude Health-Gated Launcher Design

## Problem

The interactive Bash configuration currently defines `claude` as
`headroom wrap claude --learn`. Every Claude launch therefore enters
Headroom's proxy recovery and per-session marker lifecycle, even though a
persistent user systemd service already owns the proxy on port 8787.

When the persistent proxy is listening but has not yet become ready, Headroom
0.31.0 aborts recovery. Its error cleanup then references an uninitialized
`_wrap_settings_path`, producing a secondary `UnboundLocalError` and leaving
stale client or settings markers. This makes a temporary startup delay prevent
Claude from launching.

## Goals

- Never launch Claude unless Headroom reports both `status=healthy` and
  `ready=true`.
- Recover the existing persistent user service automatically when possible.
- Preserve every Claude CLI argument, including `--resume`.
- Avoid Headroom's per-launch wrap recovery and marker lifecycle.
- Produce a concise, actionable error when recovery exceeds 60 seconds.
- Keep the existing Claude routing, MCP, RTK, tokensave, and proxy settings.

## Non-Goals

- Bypassing Headroom when it is unavailable.
- Modifying the Stratum application.
- Patching the installed Headroom Python package.
- Replacing the existing Headroom supervisor or changing its resource limits.
- Reinstalling Claude Code, Headroom, MCP servers, or context tools.

## Selected Design

Create `~/.local/bin/claude-headroom`, a small Bash launcher with one purpose:
ensure the persistent Headroom proxy is ready before executing the real Claude
binary.

The launcher will:

1. Query `http://127.0.0.1:8787/health` with a short timeout.
2. Accept the proxy only when the JSON response contains
   `status="healthy"` and `ready=true`.
3. If the check fails, start or restart
   `headroom-supervisor.service` in the user systemd manager.
4. Poll health for at most 60 seconds.
5. On success, use `exec` to replace itself with the real Claude binary and
   pass all arguments unchanged.
6. On timeout, print service status and recent user-journal entries, then exit
   non-zero without launching Claude.

The interactive `claude()` function in `~/.bashrc` will call this launcher
instead of `headroom wrap claude --learn`.

## Routing and Ownership

`headroom-supervisor.service` remains the sole owner of the long-running proxy.
Claude continues to route through it using the existing setting:

```text
ANTHROPIC_BASE_URL=http://127.0.0.1:8787
```

The launcher will resolve the real Claude executable explicitly rather than
recursively invoking the interactive `claude()` function. Headroom's existing
durable MCP, RTK, and tokensave configuration remains unchanged; those tools
will no longer be reconfigured on every Claude launch.

## Failure Behavior

Failure is closed, not open. If the health endpoint does not become ready
within 60 seconds, the launcher will not bypass Headroom and will not start
Claude. Its diagnostic output will include:

- the failed health URL;
- `systemctl --user status headroom-supervisor.service`;
- recent logs for that user service;
- a manual recovery command.

The launcher must not delete deployment manifests, settings files, sessions,
or arbitrary markers during this path.

## Verification

Verification will cover four cases:

1. **Healthy proxy:** `claude --version` starts immediately and returns the
   installed Claude version.
2. **Stopped proxy:** stopping the user service and invoking the launcher
   causes it to start/restart the service, wait for readiness, and then launch
   Claude.
3. **Unavailable proxy:** a test-only health URL or service override forces a
   timeout; the launcher exits non-zero and never invokes Claude.
4. **Argument forwarding:** a harmless Claude invocation confirms arguments
   after `claude` are passed unchanged; the production `--resume` identifier is
   not opened during automated verification.

After verification, `headroom doctor` must report zero failures and no stale
wrap marker. The proxy health endpoint must still report the expected Headroom
version and ready state.

## Rollback

Rollback consists of restoring the previous `claude()` function in
`~/.bashrc` and removing `~/.local/bin/claude-headroom`. No application data,
Claude sessions, or Headroom deployment state is migrated by this design.
