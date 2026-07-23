# Worktree Guard Semantic Classification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** Allow compound read-only diagnostics and GET/HEAD downloads to explicit `/tmp` files from the Stratum primary checkout without permitting repository, Git, remote, or ambiguous writes.

**Architecture:** Keep the shared Bash policy as the single decision engine and both platform hooks as thin adapters. Replace whole-command network matching with small target and request validators, test a candidate policy through an injectable contract path, then install it atomically with a recoverable backup.

**Tech Stack:** Bash, jq, sed, Git worktrees, shell contract tests

---

## Task 1: Make The Contract Test A Candidate-Policy Harness

**Files:**

- Modify: `/home/yang/.local/bin/test-stratum-worktree-guard`
- Test: `/home/yang/.local/bin/test-stratum-worktree-guard`

- [x] **Step 1: Back up the active user-level files**

Run:

```bash
backup_dir=$(mktemp -d /tmp/stratum-worktree-guard-backup.XXXXXX)
cp /home/yang/.local/lib/stratum-worktree-policy.sh "$backup_dir/policy.sh"
cp /home/yang/.local/bin/test-stratum-worktree-guard "$backup_dir/test-guard"
printf '%s\n' "$backup_dir"
```

Expected: one explicit `/tmp/stratum-worktree-guard-backup.*` directory containing both files. Keep this path for Task 4.

- [x] **Step 2: Add an injectable policy path to the contract**

Change the existing declaration from:

```bash
readonly policy=/home/yang/.local/lib/stratum-worktree-policy.sh
```

to:

```bash
readonly policy=${STRATUM_WORKTREE_POLICY_UNDER_TEST:-/home/yang/.local/lib/stratum-worktree-policy.sh}
```

- [x] **Step 3: Verify the unchanged active policy through the new seam**

Run:

```bash
bash -n /home/yang/.local/bin/test-stratum-worktree-guard
STRATUM_WORKTREE_POLICY_UNDER_TEST=/home/yang/.local/lib/stratum-worktree-policy.sh \
  /home/yang/.local/bin/test-stratum-worktree-guard
```

Expected: syntax check exits zero and the contract ends with `PASS: all Stratum policy and platform contracts`.

## Task 2: Add Failing Semantic Regression Cases

**Files:**

- Modify: `/home/yang/.local/bin/test-stratum-worktree-guard`
- Create: `/tmp/stratum-worktree-guard-candidate/policy.sh`
- Test: `/home/yang/.local/bin/test-stratum-worktree-guard`

- [x] **Step 1: Create a candidate from the active policy**

Run:

```bash
mkdir -p /tmp/stratum-worktree-guard-candidate
cp /home/yang/.local/lib/stratum-worktree-policy.sh /tmp/stratum-worktree-guard-candidate/policy.sh
chmod 0755 /tmp/stratum-worktree-guard-candidate/policy.sh
```

Expected: the candidate exists outside the repository and the active policy is unchanged.

- [x] **Step 2: Add allow cases matching the observed failures**

Add these assertions beside the existing curl cases:

```bash
assert_policy 'read-only external search with stderr suppression' "$primary" Bash \
    "rg -n 'curl|wget' /home/yang/.codex 2>/dev/null" allow
assert_policy 'compound read-only diagnostics' "$primary" Bash \
    "git status --short --branch && rg -n 'network' docs 2>/dev/null" allow
assert_policy 'read-only diagnostic pipeline' "$primary" Bash \
    "rg -n 'network' docs 2>/dev/null | head -n 20" allow
assert_policy 'curl download to explicit temp file' "$primary" Bash \
    'curl -sS --max-time 10 -o /tmp/stratum-official-page.html https://example.com/' allow
assert_policy 'wget download to explicit temp file' "$primary" Bash \
    'wget -q -O /tmp/stratum-official-page.html https://example.com/' allow
assert_policy 'download then inspect temp file' "$primary" Bash \
    'curl -sS -o /tmp/stratum-page.html https://example.com/ && sed -n 1,20p /tmp/stratum-page.html' allow
```

- [x] **Step 3: Add deny cases for every preserved boundary**

Add:

```bash
assert_policy 'curl output into primary checkout' "$primary" Bash \
    "curl -sS -o $primary/tmp-page.html https://example.com/" deny
assert_policy 'curl temp traversal output' "$primary" Bash \
    'curl -sS -o /tmp/a/../escape.html https://example.com/' deny
assert_policy 'curl variable output' "$primary" Bash \
    'curl -sS -o /tmp/$name https://example.com/' deny
assert_policy 'curl remote-name output' "$primary" Bash \
    'curl -sS -O https://example.com/page.html' deny
assert_policy 'curl POST request' "$primary" Bash \
    'curl -sS -X POST https://example.com/api' deny
assert_policy 'wget recursive retrieval' "$primary" Bash \
    'wget -r -P /tmp/stratum-site https://example.com/' deny
assert_policy 'compound command with repository write' "$primary" Bash \
    "rg -n 'network' docs && touch $primary/blocked" deny
assert_policy 'ambiguous heredoc' "$primary" Bash \
    $'sed -n 1p <<EOF\ntext\nEOF' deny
assert_policy 'process substitution' "$primary" Bash \
    'diff <(rg -l network docs) <(rg -l timeout docs)' deny
```

- [x] **Step 4: Run the candidate contract and verify RED**

Run:

```bash
STRATUM_WORKTREE_POLICY_UNDER_TEST=/tmp/stratum-worktree-guard-candidate/policy.sh \
  /home/yang/.local/bin/test-stratum-worktree-guard
```

Expected: FAIL first on `read-only external search with stderr suppression`, proving the observed false positive is captured before implementation.

## Task 3: Implement Target-Aware Network Classification

**Files:**

- Modify: `/tmp/stratum-worktree-guard-candidate/policy.sh`
- Test: `/home/yang/.local/bin/test-stratum-worktree-guard`

- [x] **Step 1: Generalize safe output target validation**

Add a helper adjacent to `is_safe_tmp_path`:

```bash
is_safe_output_path() {
    local path=$1

    [[ "$path" == /dev/null ]] && return 0
    is_safe_tmp_path "$path"
}
```

- [x] **Step 2: Extend the curl validator without allowing uploads**

Update `is_read_only_curl` so `-o|--output` and `--output=*` call `is_safe_output_path`, continue rejecting
`-O|--remote-name`, all data/form/upload options, and request methods other than GET or HEAD. Permit only no-op stderr
redirections after extracting and validating them; reject every other redirection target.

The output-option branches must be:

```bash
-o|--output)
    next=${words[index + 1]:-}
    is_safe_output_path "$next" || return 1
    index=$((index + 1))
    ;;
--output=*)
    next=${token#*=}
    is_safe_output_path "$next" || return 1
    ;;
```

- [x] **Step 3: Add a wget validator with the same destination boundary**

Add `is_read_only_wget` beside the curl validator. It must accept `-O <path>` and `--output-document=<path>` only when
`is_safe_output_path` succeeds, reject `-r|--recursive`, `-P|--directory-prefix`, `-b|--background`, and reject ordinary
remote-name-derived output when no explicit stdout, `/dev/null`, or safe `/tmp` output is supplied.

- [x] **Step 4: Classify supported compound commands segment by segment**

Add a quote-aware splitter that recognizes unquoted `|`, `&&`, `||`, and `;`, rejects unbalanced quotes, heredocs,
process substitution, command substitution, and empty segments, and emits each segment for validation. For each segment:

```bash
if is_read_only_curl "$segment" || is_read_only_wget "$segment"; then
    continue
fi

stripped=$(sed 's/[0-9]*>\/dev\/null//g; s/[0-9]*>&[0-9]*//g' <<<"$segment")
grep -qE "$mutation" <<<"$stripped" && return 1
```

Keep the existing mutation expression for filesystem writers, Git state changes, dependency changes, formatters, and
unknown interpreters. Remove `curl|wget` from that generic expression only after both dedicated validators are active.

- [x] **Step 5: Make denial categories precise**

Return a specific denial for ambiguous shell syntax and another for unsafe network operations. Do not include the full
command in the reason. Preserve `(effective cwd: ...)` and the worktree hint.

- [x] **Step 6: Run syntax and contract tests until GREEN**

Run:

```bash
bash -n /tmp/stratum-worktree-guard-candidate/policy.sh
bash -n /home/yang/.local/bin/test-stratum-worktree-guard
STRATUM_WORKTREE_POLICY_UNDER_TEST=/tmp/stratum-worktree-guard-candidate/policy.sh \
  /home/yang/.local/bin/test-stratum-worktree-guard
```

Expected: both syntax checks exit zero and the contract ends with `PASS: all Stratum policy and platform contracts`.

## Task 4: Install, Verify, And Document The Runtime Policy

**Files:**

- Modify: `/home/yang/.local/lib/stratum-worktree-policy.sh`
- Verify: `/home/yang/.codex/hooks/main-branch-guard.sh`
- Verify: `/home/yang/.claude/hooks/stratum-worktree-guard.sh`
- Modify: `docs/superpowers/plans/2026-07-23-worktree-guard-semantics.md`

- [x] **Step 1: Install the tested candidate atomically**

Run:

```bash
install -m 0755 /tmp/stratum-worktree-guard-candidate/policy.sh \
  /home/yang/.local/lib/stratum-worktree-policy.sh.next
mv /home/yang/.local/lib/stratum-worktree-policy.sh.next \
  /home/yang/.local/lib/stratum-worktree-policy.sh
```

Expected: no partial `.next` file remains and the installed file matches the tested candidate with `cmp`.

- [x] **Step 2: Verify the active contract and adapters**

Run:

```bash
bash -n /home/yang/.local/lib/stratum-worktree-policy.sh
bash -n /home/yang/.codex/hooks/main-branch-guard.sh
bash -n /home/yang/.claude/hooks/stratum-worktree-guard.sh
/home/yang/.local/bin/test-stratum-worktree-guard
```

Expected: every syntax check exits zero and the full active contract passes.

- [x] **Step 3: Replay the real commands through the active Codex adapter**

Send policy payloads for the observed `rg ... 2>/dev/null`, compound read-only diagnostics, safe `/tmp` download, upload,
and repository output cases through `/home/yang/.codex/hooks/main-branch-guard.sh`.

Expected: the first three return `{"continue":true}`; upload and repository output return a `permissionDecision:"deny"`
envelope.

- [x] **Step 4: Run repository guardrails in the feature worktree**

Run:

```bash
make risk-guardrails
git diff --check
```

Expected: both exit zero. Any unrelated baseline failure is reported explicitly and is not bypassed.

- [x] **Step 5: Record completed verification and commit the plan update**

Mark each completed checkbox in this plan, then run:

```bash
git add docs/superpowers/plans/2026-07-23-worktree-guard-semantics.md
git commit -m '[fix](guard): classify safe diagnostic commands'
```

Expected: the feature branch contains the design and completed implementation record; no repository source file outside
`docs/superpowers/` changed because the runtime hook is user-level configuration.

- [x] **Step 6: Remove temporary candidate files after all verification passes**

Run:

```bash
rm -rf /tmp/stratum-worktree-guard-candidate
```

Keep the timestamped backup until the user accepts the final verification report. If installation verification fails,
restore `policy.sh` and `test-guard` from the Task 1 backup before any further attempt.
