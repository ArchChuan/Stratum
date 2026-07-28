# Codex Worktree Guard Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the Codex worktree guard use valid Codex `PreToolUse` allow behavior while proving that Codex and Claude Code enforce the same shared Stratum worktree decisions.

**Architecture:** Keep the existing shared policy and separate protocol adapters. Extend the installed parity test to normalize each client's output, require identical decisions and denial reasons, and require Codex allows to exit successfully with no unsupported JSON fields. Change only the Codex adapter's allow branch; session cwd remains the authoritative execution root documented by Codex.

**Tech Stack:** Bash, jq, Git worktrees, Codex and Claude Code lifecycle hooks

---

## Task 1: Establish the risk baseline

**Files:**

- Read: `scripts/quality/risk-regression-guard.sh`
- Read: `/home/yang/.local/bin/test-stratum-worktree-guard`
- Read: `/home/yang/.codex/hooks/main-branch-guard.sh`
- Read: `/home/yang/.claude/hooks/stratum-worktree-guard.sh`

- [ ] **Step 1: Explain the repository risk guard**

Run:

```bash
STRATUM_WORKTREE_ROOT=/home/yang/go-projects/stratum-codex-worktree-guard-parity \
  bash scripts/quality/risk-regression-guard.sh --explain
```

Expected: exit `0` and an explanation of applicable risk checks without modifying repository files.

- [ ] **Step 2: Record the current adapter contracts without printing environment values**

Run:

```bash
sed -n '1,100p' /home/yang/.codex/hooks/main-branch-guard.sh
sed -n '1,100p' /home/yang/.claude/hooks/stratum-worktree-guard.sh
```

Expected: Codex allow currently prints `{"continue":true}`, Claude allow prints an explicit Claude permission decision, and both call `/home/yang/.local/lib/stratum-worktree-policy.sh`.

## Task 2: Add a failing Codex allow-contract regression test

**Files:**

- Modify: `/home/yang/.local/bin/test-stratum-worktree-guard`
- Test: `/home/yang/.codex/hooks/main-branch-guard-test.sh`

- [ ] **Step 1: Add an assertion that a Codex allow is silent**

In `/home/yang/.local/bin/test-stratum-worktree-guard`, replace the existing Codex allow-contract assertion with:

```bash
assert_codex_allow() {
    local output status

    set +e
    output=$(input "$primary" Bash 'git status --short --branch' | "$codex")
    status=$?
    set -e

    [[ "$status" -eq 0 ]] || {
        printf 'FAIL Codex allow exit status: %s\n' "$status" >&2
        exit 1
    }
    [[ -z "$output" ]] || {
        printf 'FAIL Codex allow must be silent: %s\n' "$output" >&2
        exit 1
    }
    printf 'PASS Codex allow contract\n'
}
```

Keep the existing invocation of `assert_codex_allow` in the test sequence.

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
/home/yang/.codex/hooks/main-branch-guard-test.sh
```

Expected: FAIL with `Codex allow must be silent` because the adapter prints `{"continue":true}`.

## Task 3: Correct the Codex `PreToolUse` allow response

**Files:**

- Modify: `/home/yang/.codex/hooks/main-branch-guard.sh`
- Test: `/home/yang/.codex/hooks/main-branch-guard-test.sh`

- [ ] **Step 1: Implement the minimal valid allow branch**

Change the allow branch in `/home/yang/.codex/hooks/main-branch-guard.sh` to:

```bash
if [[ "$decision" == allow ]]; then
    exit 0
fi
```

Do not change the deny envelope or the shared policy.

- [ ] **Step 2: Run the focused test and verify GREEN**

Run:

```bash
/home/yang/.codex/hooks/main-branch-guard-test.sh
```

Expected: all shared-policy, Codex adapter, and Claude adapter assertions pass.

- [ ] **Step 3: Run syntax validation**

Run:

```bash
bash -n \
  /home/yang/.local/lib/stratum-worktree-policy.sh \
  /home/yang/.local/bin/test-stratum-worktree-guard \
  /home/yang/.codex/hooks/main-branch-guard.sh \
  /home/yang/.claude/hooks/stratum-worktree-guard.sh
```

Expected: exit `0` with no output.

## Task 4: Add explicit cross-client parity assertions

**Files:**

- Modify: `/home/yang/.local/bin/test-stratum-worktree-guard`
- Test: `/home/yang/.codex/hooks/main-branch-guard-test.sh`

- [ ] **Step 1: Add response normalizers**

Add these helpers after the existing adapter helpers:

```bash
codex_decision() {
    local payload=$1 output
    output=$(printf '%s' "$payload" | "$codex")
    if [[ -z "$output" ]]; then
        printf 'allow\n'
    else
        jq -r '.hookSpecificOutput.permissionDecision' <<<"$output"
    fi
}

claude_decision() {
    local payload=$1
    printf '%s' "$payload" | "$claude" |
        jq -r '.hookSpecificOutput.permissionDecision'
}

adapter_reason() {
    jq -r '.hookSpecificOutput.permissionDecisionReason // ""'
}
```

- [ ] **Step 2: Add a table-driven parity assertion**

Add:

```bash
assert_client_parity() {
    local name=$1 cwd=$2 tool=$3 command=$4 payload codex_output claude_output
    local codex_value claude_value codex_reason claude_reason

    payload=$(input "$cwd" "$tool" "$command")
    codex_output=$(printf '%s' "$payload" | "$codex")
    claude_output=$(printf '%s' "$payload" | "$claude")

    if [[ -z "$codex_output" ]]; then
        codex_value=allow
        codex_reason=
    else
        codex_value=$(jq -r '.hookSpecificOutput.permissionDecision' <<<"$codex_output")
        codex_reason=$(adapter_reason <<<"$codex_output")
    fi
    claude_value=$(jq -r '.hookSpecificOutput.permissionDecision' <<<"$claude_output")
    claude_reason=$(adapter_reason <<<"$claude_output")

    [[ "$codex_value" == "$claude_value" && "$codex_reason" == "$claude_reason" ]] || {
        printf 'FAIL client parity %s: codex=%s claude=%s\n' \
            "$name" "$codex_value" "$claude_value" >&2
        exit 1
    }
    printf 'PASS client parity %s\n' "$name"
}
```

- [ ] **Step 3: Cover the agreed decision matrix**

Add these calls to the test sequence:

```bash
assert_client_parity 'primary tracked edit' "$primary" Write ''
assert_client_parity 'primary Git mutation' "$primary" Bash 'git add README.md'
assert_client_parity 'primary read-only status' "$primary" Bash 'git status --short --branch'
assert_client_parity 'approved worktree helper' "$primary" Bash \
    'bash scripts/new-worktree.sh ../stratum-example feat/example'
assert_client_parity 'linked worktree edit' "$linked" Write ''
```

- [ ] **Step 4: Run the complete parity suite**

Run:

```bash
/home/yang/.local/bin/test-stratum-worktree-guard
```

Expected: every policy and client-parity assertion prints `PASS` and the command exits `0`.

## Task 5: Verify live primary and worktree behavior

**Files:**

- Verify: `/home/yang/.codex/hooks/main-branch-guard.sh`
- Verify: `/home/yang/.claude/hooks/stratum-worktree-guard.sh`

- [ ] **Step 1: Verify a primary-checkout edit is denied by both adapters**

Run:

```bash
jq -cn \
  --arg cwd /home/yang/go-projects/stratum \
  --arg path /home/yang/go-projects/stratum/README.md \
  '{cwd:$cwd,tool_name:"Write",tool_input:{file_path:$path}}' |
  /home/yang/.codex/hooks/main-branch-guard.sh
```

Repeat with `/home/yang/.claude/hooks/stratum-worktree-guard.sh`.

Expected: both responses deny the operation and mention the effective primary cwd plus `scripts/new-worktree.sh`.

- [ ] **Step 2: Verify a linked-worktree edit is allowed**

Run the same payload with:

```text
cwd=/home/yang/go-projects/stratum-codex-worktree-guard-parity
file_path=/home/yang/go-projects/stratum-codex-worktree-guard-parity/README.md
```

Expected: Codex exits `0` with no output; Claude returns `permissionDecision: allow`.

- [ ] **Step 3: Verify Codex session-root guidance**

Run:

```bash
codex exec --help | rg -- '--cd|-C'
```

Expected: the CLI documents `--cd`/`-C` as the workspace-root option. Interactive sessions must be started after `cd <linked-worktree>`; per-tool `workdir` is not treated as a replacement for the session cwd by the hook contract.

## Task 6: Run completion checks and record the local-only result

**Files:**

- Verify: `docs/superpowers/specs/2026-07-28-codex-worktree-guard-parity-design.md`
- Verify: `docs/superpowers/plans/2026-07-28-codex-worktree-guard-parity.md`

- [ ] **Step 1: Run the repository guardrails applicable to documentation-only tracked changes**

Run:

```bash
STRATUM_WORKTREE_ROOT=/home/yang/go-projects/stratum-codex-worktree-guard-parity \
  make risk-guardrails
```

Expected: exit `0`. If the risk selector requires an acceptance mode, follow its reported command and preserve the evidence path.

- [ ] **Step 2: Verify the tracked worktree is clean except for the plan commit**

Run:

```bash
git status --short --branch
git diff --check HEAD^ HEAD
```

Expected: no uncommitted files and no whitespace errors.

- [ ] **Step 3: Report the deployment boundary**

Record that the functional fix lives in user-level files under `~/.codex` and `~/.local`, while the repository contains the reviewed design and implementation plan. Do not claim the fix is portable to another machine unless an installer or managed-hook distribution mechanism is added in a separately approved change.
