# Feishu Agent Session Stability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve Claude and Codex logical sessions across transient backend failures and reconnect safely without replaying turns with possible side effects.

**Architecture:** Keep SQLite `native_session_id` as the durable conversation identity and treat protocol backend objects as replaceable connections. Protocol adapters classify failures by replay safety; `ProtocolService` performs at most one safe reconnect-and-retry and never clears a session except for `/clear`.

**Tech Stack:** Python 3.11, pytest, Claude Agent SDK, Codex app-server JSON-RPC, systemd user services

---

## Task 1: Classified protocol failures

**Files:**

- Modify: `/mnt/d/opc-os/07-Automation/feishu-agent-bridge/src/feishu_agent_bridge/protocol.py`
- Test: `/mnt/d/opc-os/07-Automation/feishu-agent-bridge/tests/test_protocol_service.py`

- [ ] Add failing tests that construct a replay-safe `BackendFailure` and an uncertain/tool-observed failure.
- [ ] Run `PYTHONPATH=src uvx --from 'pytest>=8,<9' pytest tests/test_protocol_service.py -q` and verify failure because `BackendFailure` is absent.
- [ ] Add `BackendFailure(RuntimeError)` with `kind`, `replay_safe`, and `session_missing` fields.
- [ ] Re-run the focused tests and verify pass.

## Task 2: Bounded reconnect and retry

**Files:**

- Modify: `/mnt/d/opc-os/07-Automation/feishu-agent-bridge/src/feishu_agent_bridge/protocol_service.py`
- Test: `/mnt/d/opc-os/07-Automation/feishu-agent-bridge/tests/test_protocol_service.py`

- [ ] Replace the existing failure test with tests proving a replay-safe first failure rebuilds the backend, resumes the same native ID, and retries exactly once.
- [ ] Add tests proving an uncertain failure is not replayed, a second safe failure is not retried again, and `session_missing` requests explicit `/clear` without creating a session.
- [ ] Run the focused tests and verify the new assertions fail against the current generic exception branch.
- [ ] Implement a two-attempt loop in `_run_turn`; evict only the failed backend instance, preserve state, and emit classified Chinese messages that never claim reset.
- [ ] Re-run focused tests and verify pass.

## Task 3: Adapter failure classification

**Files:**

- Modify: `/mnt/d/opc-os/07-Automation/feishu-agent-bridge/src/feishu_agent_bridge/protocol_codex.py`
- Modify: `/mnt/d/opc-os/07-Automation/feishu-agent-bridge/src/feishu_agent_bridge/protocol_claude.py`
- Test: `/mnt/d/opc-os/07-Automation/feishu-agent-bridge/tests/test_protocol_codex.py`
- Test: `/mnt/d/opc-os/07-Automation/feishu-agent-bridge/tests/test_protocol_claude.py`

- [ ] Add failing tests for pre-start transport failure, timeout uncertainty, observed approval/tool activity, authentication failure, and missing native session.
- [ ] Track whether native execution and approval/tool activity were observed during a turn.
- [ ] Translate adapter errors into `BackendFailure`; only failures known to occur before execution or after a confirmed side-effect-free terminal failure are replay-safe.
- [ ] Run both adapter test modules and verify pass.

## Task 4: Runtime regression verification

**Files:**

- Test: `/mnt/d/opc-os/07-Automation/feishu-agent-bridge/tests/`

- [ ] Run `PYTHONPATH=src uvx --from 'pytest>=8,<9' --with 'lark-oapi==1.7.1' --with 'claude-agent-sdk==0.2.122' pytest -q`.
- [ ] Run the offline self-test and both environment doctor checks without printing credential values.
- [ ] Verify source and installed runtime hashes before deployment to establish the rollback baseline.

## Task 5: Rollback-safe deployment and E2E

**Files:**

- Modify only through installer: `/home/yang/.local/share/feishu-agent-bridge/venv/`

- [ ] Create a timestamped runtime backup under `/home/yang/.local/state/feishu-agent-bridge/backups/`.
- [ ] Run `scripts/install-runtime.sh`, daemon-reload, and restart both bridge units; restore the backup automatically on failure.
- [ ] Verify both units are active and recent journals contain no startup traceback or credential material.
- [ ] Send controlled non-destructive turns through fake protocol backends to prove native ID continuity, one safe retry, and no uncertain replay.
- [ ] Confirm `/clear` remains the only tested operation that clears the persisted native session ID.
