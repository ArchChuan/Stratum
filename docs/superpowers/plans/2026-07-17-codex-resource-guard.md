# Codex Resource Guard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serialize Stratum full-suite verification, bound its child concurrency, clean timed-out process groups, and reject raw unbounded verification from Codex hooks.

**Architecture:** A user-level `stratum-verify` wrapper owns one flock queue and runs named verification stages sequentially through a process-group timeout helper. A Codex `PreToolUse:Bash` hook denies repository-wide raw commands and points callers to the wrapper while allowing focused TDD commands. The aggregate `ai.slice` provides a final CPU and memory ceiling.

**Tech Stack:** Bash, jq, flock, setsid, systemd user slices, Vitest, Go toolchain.

---

## Task 1: Freeze Resource Hook Behavior

**Files:**

- Create: `/home/yang/.codex/hooks/resource-guard.sh`
- Create: `/home/yang/.codex/hooks/resource-guard-test.sh`
- Modify: `/home/yang/.codex/hooks.json`

- [ ] **Step 1: Write failing hook tests**

Create table-driven shell assertions that send Codex `PreToolUse` JSON and require denial for `npm test`, unbounded `npx vitest run`, `go test -short ./...`, `go vet ./...`, repository-wide govulncheck, and shell commands containing concurrent heavyweight stages. Require neutral allowance for `go test ./internal/memory/...`, `go test ./internal/memory -run TestName`, and Vitest with explicit `--maxWorkers`/`--minWorkers`.

- [ ] **Step 2: Run the test and verify RED**

Run:

```bash
bash /home/yang/.codex/hooks/resource-guard-test.sh
```

Expected: failure because `resource-guard.sh` does not exist.

- [ ] **Step 3: Implement the minimum guard**

Parse only `.tool_input.command` from object input. Return `permissionDecision:"deny"` with the matching `stratum-verify` mode for full-suite commands; otherwise return `{"continue":true}`. Do not print command environment or secrets.

- [ ] **Step 4: Register the hook**

Add the script as a global `PreToolUse` hook for `Bash`, alongside the existing sudo guard and history logger.

- [ ] **Step 5: Verify GREEN**

Run the hook test and parse every emitted response with `jq -e`.

## Task 2: Build the Serialized Verification Queue

**Files:**

- Create: `/home/yang/.local/bin/stratum-verify`
- Create: `/home/yang/.local/bin/stratum-verify-test`

- [ ] **Step 1: Write the queue-order failing test**

Use a temporary root with fake stage commands. Start two wrapper invocations, record start/end timestamps, and assert the second stage starts only after the first stage ends. Override the lock and command paths through test-only environment variables.

- [ ] **Step 2: Run the queue test and verify RED**

Run:

```bash
bash /home/yang/.local/bin/stratum-verify-test queue
```

Expected: failure because the wrapper does not exist.

- [ ] **Step 3: Implement modes and the shared lock**

Implement `frontend-test`, `frontend-full`, `go-test`, `go-full`, and `all`. Acquire a blocking `flock --close` lock, print one queue message before waiting, and execute stages serially. Use bounded defaults: Vitest workers 2 and `GOMAXPROCS=4` with Go package parallelism 4.

- [ ] **Step 4: Verify queue GREEN**

Run the queue test and assert ordered execution and zero overlap.

## Task 3: Terminate Timed-Out Process Groups

**Files:**

- Modify: `/home/yang/.local/bin/stratum-verify`
- Modify: `/home/yang/.local/bin/stratum-verify-test`

- [ ] **Step 1: Write the timeout failing test**

Use a fake stage that starts a long-lived child and records its PID. Configure a one-second timeout. Assert the wrapper exits non-zero and both the stage leader and child PID disappear.

- [ ] **Step 2: Run the timeout test and verify RED**

Run:

```bash
bash /home/yang/.local/bin/stratum-verify-test timeout
```

Expected: failure because the child remains alive or timeout handling is absent.

- [ ] **Step 3: Implement process-group timeout handling**

Start each stage under `setsid`, monitor its deadline, send `TERM` to the negative process-group ID, wait a short grace interval, send `KILL` if needed, and reap the leader before returning failure. Install INT/TERM/EXIT cleanup traps without killing unrelated processes.

- [ ] **Step 4: Verify timeout GREEN**

Run the timeout test and confirm no recorded PID remains.

## Task 4: Apply the Aggregate Resource Ceiling

**Files:**

- Modify: `/home/yang/.config/systemd/user/ai.slice`
- Create: `/home/yang/.config/ai-resource-control/resource-guard-test.sh`
- Modify: `/home/yang/.config/ai-resource-control/README.md`

- [ ] **Step 1: Write a failing property test**

Require the unit file to declare `CPUQuota=600%`, `MemoryHigh=8G`, and `MemoryMax=10G`. When systemd exposes runtime properties, require the corresponding values to be present.

- [ ] **Step 2: Run the property test and verify RED**

Run:

```bash
bash /home/yang/.config/ai-resource-control/resource-guard-test.sh
```

Expected: failure because the current unit has `CPUQuota=1000%`, `MemoryHigh=9G`, and no `MemoryMax`.

- [ ] **Step 3: Update and reload the slice**

Set the three approved limits, run `systemctl --user daemon-reload`, and restart only the slice if systemd can apply limits without terminating existing interactive sessions. Otherwise use `systemctl --user set-property --runtime ai.slice` for immediate non-disruptive enforcement and leave the unit file persistent for the next login.

- [ ] **Step 4: Verify runtime properties GREEN**

Run the property test and `systemctl --user show ai.slice -p CPUQuotaPerSecUSec -p MemoryHigh -p MemoryMax`.

## Task 5: Integrate and Verify the Guardrails

**Files:**

- Test: `/home/yang/.codex/hooks/resource-guard-test.sh`
- Test: `/home/yang/.local/bin/stratum-verify-test`
- Test: `/home/yang/.config/ai-resource-control/resource-guard-test.sh`
- Test: `/home/yang/go-projects/stratum/.codex/hooks/run-tests.sh`

- [ ] **Step 1: Run all isolated tests**

```bash
bash /home/yang/.codex/hooks/resource-guard-test.sh
bash /home/yang/.local/bin/stratum-verify-test all
bash /home/yang/.config/ai-resource-control/resource-guard-test.sh
bash .codex/hooks/run-tests.sh
```

Expected: all pass with no remaining child process.

- [ ] **Step 2: Verify focused commands remain usable**

Run one focused Go test and one focused Vitest file with bounded workers. Do not run the complete repository suites for this guardrail change.

- [ ] **Step 3: Verify hook registration and syntax**

Parse `/home/yang/.codex/hooks.json` with jq, run `bash -n` on every new script, and verify a representative denied and allowed command through the registered script.

- [ ] **Step 4: Inspect host state**

Confirm there are no test fixture children, no queued wrapper, and no new CPU or memory pressure warning caused by the verification itself.

- [ ] **Step 5: Report operational behavior**

Document wrapper modes, queue semantics, the exact incident pattern now denied, limits applied immediately versus on next login, and any residual risk from commands launched outside Codex hooks.
