# Feishu Agent Session Stability Design

## Goal

Make the Feishu Claude Code and Codex bridges behave like their TUI counterparts: a conversation remains the same
logical native session until the user explicitly runs `/clear`. Process crashes, transport failures, service restarts,
turn timeouts, and idle time must not silently replace the conversation.

## Confirmed User Contract

- A session has no time-based expiry. The previously considered 48-hour refresh is removed.
- Only `/clear` may intentionally discard the current native session and create a new conversation.
- A failed backend process or connection may be replaced, but the replacement must resume the persisted native session ID.
- A recoverable current turn may be retried automatically at most once.
- The bridge must not replay a turn when tool side effects may already have occurred.
- A missing native session is reported explicitly. The bridge must not silently substitute a new session.

## Current Failure Mode

`ProtocolService._run_turn` currently catches every backend exception, evicts the backend, and replies with
`执行失败，后端会话已重置，请重试。`. The text conflates replacement of an in-memory backend with replacement of
the logical conversation. The installed Codex backend also has a 120-second turn deadline, the Claude backend has a
300-second deadline, and both failures enter the same generic branch.

Runtime evidence on 2026-07-21 also showed simultaneous Feishu WebSocket ping timeouts, TLS EOF failures, and opening
handshake timeouts for both bridge instances. Connection health therefore needs supervision independently of native
agent session ownership.

## Architecture

### Logical Session

The SQLite session record is the authority for the logical conversation. Its `native_session_id` remains unchanged
across backend eviction, service restart, Feishu reconnect, and recoverable turn failure. Backend cleanup must never
clear that value. `/clear` closes the live backend, increments the approval generation, clears the native session ID,
and is the sole normal path that starts a new conversation.

If a provider explicitly reports that the persisted native session does not exist, the session enters a visible
`resume_failed` condition. The user receives an actionable error asking them to run `/clear`; the bridge does not
create a replacement implicitly.

### Replaceable Backend

An in-memory Claude or Codex backend is a disposable connection to a durable logical session. On a recoverable
failure, the service closes and evicts the exact failed backend instance, constructs a new instance, and resumes the
persisted native session ID. Backend replacement does not bump the logical session generation.

The recovery attempt is bounded to one per accepted Feishu message. A per-turn recovery counter prevents recursive
or indefinite retry loops.

### Replay Safety

Automatic replay is permitted only when the bridge can establish one of these conditions:

1. the turn was rejected before native execution began; or
2. the native turn reached a confirmed interrupted/failed terminal state and no tool execution or approval was
   observed.

Once a tool request, approval, or tool execution is observed, or when timeout leaves completion uncertain, the bridge
must not resend the user message. It preserves the session and reports that the turn outcome is uncertain. This
prevents duplicate commits, writes, deployments, and external requests.

Each protocol adapter returns a classified failure carrying the phase, terminal-state confidence, and whether a tool
side effect may have started. `ProtocolService` owns the single retry decision; adapters do not independently replay
messages.

### Feishu Connection Supervision

The Feishu event connection is supervised separately from agent sessions. A connection-generation supervisor tracks
successful connection establishment and inbound heartbeat/activity. Repeated handshake failures, ping timeouts, or a
stale connection cause the Feishu client loop to be rebuilt with bounded exponential backoff and jitter.

Rebuilding the Feishu client must not close protocol backends or mutate session records. Systemd remains responsible
for restarting the process if the connection supervisor itself exits unexpectedly.

## State And Errors

Turn failures are classified as:

- `transport_failed`: request was not accepted; safe for one reconnect-and-retry.
- `turn_failed`: native terminal failure with no possible side effect; safe for one reconnect-and-retry.
- `turn_timeout_uncertain`: completion or tool effects are unknown; preserve session and do not replay.
- `resume_failed`: persisted native session cannot be resumed; require explicit `/clear`.
- `authentication_failed`: configuration error such as a missing provider credential; do not retry.

User replies describe the actual condition and never claim that a session was reset unless `/clear` completed. Logs
record the agent, logical session key hash, native session ID hash, failure class, retry count, and duration, but never
credentials, message contents, tool inputs, or raw upstream bodies.

## Verification

Unit and integration tests will prove:

- backend eviction preserves the persisted native session ID and generation;
- service restart resumes the same native session;
- a pre-execution transport failure reconnects and retries exactly once;
- a second failure stops without an unbounded retry;
- timeout after a tool signal does not replay the message;
- explicit native-session-missing errors require `/clear` and do not create a new session;
- `/clear` remains the only path that clears the native session ID;
- Feishu connection rebuilding does not mutate or close agent sessions;
- log and user-facing output contain no secrets or misleading reset statement.

End-to-end verification will run both bridge instances against controlled fake protocol backends and a fault-injected
Feishu transport. It will then exercise the installed services with non-destructive messages, verify native session ID
continuity across a forced backend reconnect, and confirm `/clear` is the only operation that changes the conversation.

## Scope

This change covers bridge session lifecycle, bounded recovery, error reporting, and Feishu connection supervision. It
does not add time-based session cleanup, transcript-based session reconstruction, unlimited retries, or changes to
Stratum application behavior.
