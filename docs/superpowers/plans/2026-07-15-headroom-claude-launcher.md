# Headroom Claude Health-Gated Launcher Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the per-launch Headroom wrapper with a fail-closed launcher that starts Claude only after the persistent Headroom proxy is healthy.

**Architecture:** A focused Bash launcher owns readiness checking and service recovery. The interactive Bash `claude()` function delegates to the launcher, while the existing user systemd service remains the only proxy owner and the existing Claude settings continue routing API traffic to port 8787.

**Tech Stack:** Bash, curl, Python 3 JSON parsing, systemd user services, Claude Code, Headroom 0.31.0

---

## Task 1: Add a deterministic launcher test harness

**Files:**

- Create: `/tmp/test-claude-headroom.sh`
- Test: `/tmp/test-claude-headroom.sh`

- [ ] **Step 1: Write the failing test harness**

Create a temporary Bash test that supplies fake `curl`, `systemctl`, `journalctl`, and Claude commands through environment overrides. Cover healthy startup, automatic recovery, fail-closed timeout, and exact argument forwarding. Each fake records invocations beneath a temporary directory and returns deterministic responses.

- [ ] **Step 2: Run the harness before implementation**

Run:

```bash
bash /tmp/test-claude-headroom.sh
```

Expected: FAIL because `/home/yang/.local/bin/claude-headroom` does not exist.

## Task 2: Implement the health-gated launcher

**Files:**

- Create: `/home/yang/.local/bin/claude-headroom`
- Test: `/tmp/test-claude-headroom.sh`

- [ ] **Step 1: Implement configuration and health checking**

The launcher must use these defaults while allowing test-only overrides:

```bash
HEALTH_URL="${HEADROOM_HEALTH_URL:-http://127.0.0.1:8787/health}"
SERVICE="${HEADROOM_SERVICE:-headroom-supervisor.service}"
WAIT_SECONDS="${HEADROOM_WAIT_SECONDS:-60}"
CURL_BIN="${HEADROOM_CURL_BIN:-curl}"
SYSTEMCTL_BIN="${HEADROOM_SYSTEMCTL_BIN:-systemctl}"
JOURNALCTL_BIN="${HEADROOM_JOURNALCTL_BIN:-journalctl}"
CLAUDE_BIN="${CLAUDE_REAL_BIN:-$HOME/.local/bin/claude}"
```

`health_ready` must call curl with a short timeout and parse JSON using Python 3. It succeeds only for `status == "healthy"` and `ready is True`.

- [ ] **Step 2: Implement service recovery and bounded polling**

If health initially fails, start an inactive user service or restart an active one. Poll once per second until the configured deadline. Reject non-numeric or non-positive wait values with exit code 2.

- [ ] **Step 3: Implement fail-closed diagnostics**

On timeout, print the health URL, service status, recent journal entries, and the manual recovery command. Exit non-zero without invoking Claude. Do not remove markers, manifests, settings, or sessions.

- [ ] **Step 4: Implement transparent Claude execution**

When ready, verify that the configured real Claude path is executable, then run:

```bash
exec "$CLAUDE_BIN" "$@"
```

- [ ] **Step 5: Run the deterministic test harness**

Run:

```bash
bash /tmp/test-claude-headroom.sh
```

Expected: all four cases PASS.

## Task 3: Switch the interactive Bash entry point

**Files:**

- Modify: `/home/yang/.bashrc:136-142`
- Test: interactive Bash command resolution

- [ ] **Step 1: Replace the existing wrapper function**

Replace the function that calls `headroom wrap claude --learn` with:

```bash
claude() {
    "$HOME/.local/bin/claude-headroom" "$@"
}
```

- [ ] **Step 2: Validate Bash syntax and resolution**

Run:

```bash
bash -n /home/yang/.bashrc
bash -ic 'type claude'
```

Expected: syntax check exits 0 and interactive Bash reports the new function body.

## Task 4: Verify against the real persistent service

**Files:**

- Verify: `/home/yang/.local/bin/claude-headroom`
- Verify: `/home/yang/.config/systemd/user/headroom-supervisor.service`
- Verify: `/home/yang/.claude/settings.json`

- [ ] **Step 1: Verify the healthy fast path**

Run:

```bash
timeout 45s bash -ic 'claude --version'
```

Expected: exit 0, Claude Code version printed, and no `HEADROOM WRAP` banner.

- [ ] **Step 2: Verify automatic recovery**

Stop `headroom-supervisor.service`, confirm port 8787 is unavailable, then invoke `claude --version`. Expected: the launcher starts the service, waits for `ready=true`, and Claude exits 0.

- [ ] **Step 3: Verify fail-closed behavior**

Use the deterministic harness with an always-unhealthy fake endpoint and a two-second timeout. Expected: non-zero exit, diagnostics printed, and the fake Claude invocation log remains absent.

- [ ] **Step 4: Verify routing and Headroom state**

Run:

```bash
curl -fsS http://127.0.0.1:8787/health
headroom doctor
```

Expected: proxy reports `healthy` and `ready=true`; doctor reports zero failures and no stale wrap marker.

- [ ] **Step 5: Remove temporary test artifacts**

Remove `/tmp/test-claude-headroom.sh` and any test-created temporary directory after all verification evidence has been recorded.

## Task 5: Record completion

**Files:**

- Modify: `docs/superpowers/plans/2026-07-15-headroom-claude-launcher.md`

- [ ] **Step 1: Mark completed checkboxes**

Update every successfully executed checkbox from `[ ]` to `[x]`. If any step is skipped or partially fails, leave it unchecked and record the exact reason beneath that step.

- [ ] **Step 2: Commit the implementation record**

Commit only this plan file; machine-local launcher and shell configuration remain outside the repository:

```bash
git add docs/superpowers/plans/2026-07-15-headroom-claude-launcher.md
git commit -m "docs(dev): record Headroom launcher implementation"
```
