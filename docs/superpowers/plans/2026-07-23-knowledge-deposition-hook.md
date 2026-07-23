# Knowledge Deposition Hook Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or
> superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Require every completed Stratum Codex or Claude Code session to write a validated, session-bound knowledge
deposition report under ignored `tmp/`, without automatically changing any knowledge destination.

**Architecture:** A committed Bash core validates JSON, writes session-scoped JSON/Markdown reports atomically, and
checks task completion. Separate committed Codex and Claude adapters translate their lifecycle protocols into the shared
core. An idempotent installer registers the adapters in user-level hook configuration, while a narrowly tested local
worktree-policy exception permits only the report command in the primary checkout.

**Tech Stack:** Bash 5, jq, flock, Git, Markdown, Codex hooks, Claude Code hooks

---

## File Map

Committed project files:

- Create `docs/agent/knowledge-deposition.md`: canonical classification and report policy.
- Modify `docs/agent/instructions.md`: mandatory short rule and policy link.
- Modify `docs/agent/templates/agents-prefix.md`: global-to-project gate relationship, without duplicating categories.
- Modify `docs/agent/templates/claude-prefix.md`: same relationship for Claude Code.
- Regenerate `AGENTS.md` and `CLAUDE.md` using the existing generator.
- Create `scripts/knowledge-deposition/common.sh`: repository/session normalization and JSON validation primitives.
- Create `scripts/knowledge-deposition/report.sh`: validated atomic JSON/Markdown writer.
- Create `scripts/knowledge-deposition/check.sh`: read-only session report gate used by Stop adapters.
- Create `scripts/knowledge-deposition/codex-task-start.sh`: Codex UserPromptSubmit protocol adapter.
- Create `scripts/knowledge-deposition/codex-stop.sh`: Codex Stop protocol adapter.
- Create `scripts/knowledge-deposition/claude-task-start.sh`: Claude UserPromptSubmit protocol adapter.
- Create `scripts/knowledge-deposition/claude-stop.sh`: Claude Stop protocol adapter.
- Create `scripts/knowledge-deposition/install-hooks.sh`: idempotent user-level registration with backups.
- Create `scripts/knowledge-deposition/report-test.sh`: report schema, rendering, concurrency, and security tests.
- Create `scripts/knowledge-deposition/hooks-test.sh`: both lifecycle protocol suites and installer fixture tests.
- Modify `Makefile`: focused `knowledge-deposition-test` target.
- Modify `.pre-commit-config.yaml`: run focused tests when policy or implementation files change.
- Modify `scripts/quality/generate-agent-instructions-test.sh`: include the new policy file in drift-trigger assertions.
- Modify `docs/agent/knowledge-workspace.md`: operational path, local-only report status, and governance boundary.

Local machine files, modified only during the installation task and never added to Git:

- Modify `/home/yang/.local/lib/stratum-worktree-policy.sh`: exact primary-checkout report-command allowance.
- Modify `/home/yang/.local/bin/test-stratum-worktree-guard`: allow/deny coverage for that exception.
- Modify `/home/yang/.codex/hooks.json`: Codex UserPromptSubmit and Stop registration.
- Modify `/home/yang/.claude/settings.json`: Claude Code UserPromptSubmit and Stop registration.

## Task 1: Canonicalize The Classification Policy

**Files:**

- Create: `docs/agent/knowledge-deposition.md`
- Modify: `docs/agent/instructions.md`
- Modify: `docs/agent/templates/agents-prefix.md`
- Modify: `docs/agent/templates/claude-prefix.md`
- Modify: `scripts/quality/generate-agent-instructions-test.sh`
- Modify: `AGENTS.md`
- Modify: `CLAUDE.md`
- Test: `scripts/quality/generate-agent-instructions-test.sh`

- [ ] **Step 1: Add a failing instruction-governance assertion**

Extend the tracked-input assertion in `scripts/quality/generate-agent-instructions-test.sh` so the pre-commit file regex
must include `docs/agent/knowledge-deposition.md`, then assert generated instructions contain exactly one policy link:

```bash
assert_contains "${precommit_files}" 'docs/agent/knowledge-deposition\\.md' \
  'knowledge deposition policy triggers instruction drift check'

for generated in "${ROOT}/AGENTS.md" "${ROOT}/CLAUDE.md"; do
  [[ "$(grep -c 'docs/agent/knowledge-deposition\.md' "$generated")" -eq 1 ]] ||
    fail "${generated#"${ROOT}/"} must contain exactly one knowledge deposition policy link"
done
```

- [ ] **Step 2: Run the test and confirm the policy is missing**

Run: `bash scripts/quality/generate-agent-instructions-test.sh`

Expected: FAIL mentioning `knowledge deposition policy` or the missing pre-commit path.

- [ ] **Step 3: Write the canonical policy and compact instruction entry**

Create `docs/agent/knowledge-deposition.md` with these exact normative sections:

```markdown
# Knowledge deposition

## Task-end gate

Every substantive task must submit one session-bound report before the final response. A report either contains one or
more candidates or records `decision: none`. The report is a local audit artifact, not permission to modify a target.

## Destinations

| Destination | Use when |
|---|---|
| `skill` | An Agent can reuse an executable procedure, checklist, tool method, or pitfall. |
| `hook` | A deterministic lifecycle check can detect, block, prompt, or repair the condition. |
| `global_md` | A cross-project principle, stable preference, or safety boundary belongs in default context. |
| `obsidian` | A cross-project durable fact, principle, case, counterexample, or correction has evidence and boundaries. |
| `project_git` | A Stratum-specific architecture, contract, ADR, test, or runtime fact belongs with the project. |
| `none` | Work is routine, duplicate, transient, or unsupported by durable evidence. |

## Evidence and duplicate checks

Candidates state one claim, evidence paths, scope, exclusions or counterexamples, duplicate-check result, confidence, and
recommended target. Obsidian candidates additionally state knowledge type, Vault queries, related notes, verification
status, and governance action. Search snippets, raw logs, complete conversations, prompts, secrets, and unsupported guesses
are not durable evidence.

## Write boundary

Normal coding tasks keep Obsidian read-only and do not automatically change Skills, hooks, global instructions, or project
documentation. The final response summarizes the generated report. Knowledge write-back is a separate governed task.
```

Add this compact section to `docs/agent/instructions.md` after `Knowledge input and evidence`:

```markdown
## End-of-task knowledge gate

每个实质任务在最终回复前必须按 `docs/agent/knowledge-deposition.md` 生成会话绑定的候选报告；无候选也必须显式
记录 `none`。报告只进入忽略目录 `tmp/knowledge-deposition/`，不得在普通编码任务中自动写入 Skill、Hook、全局
指令、项目文档或 Obsidian。最终回复只摘要同一份报告，禁止另起一套分类结果。
```

Add one sentence to each prefix stating that user-level task-finalization policy delegates Stratum-specific categories and
report mechanics to `docs/agent/knowledge-deposition.md`; do not copy the destination table into either prefix.

- [ ] **Step 4: Update the drift trigger and regenerate root instructions**

Add `docs/agent/knowledge-deposition\.md` to the bounded `files:` regex in `.pre-commit-config.yaml` and the matching
fixture in `scripts/quality/generate-agent-instructions-test.sh`.

Run: `make agent-instructions`

Expected: `AGENTS.md` and `CLAUDE.md` are regenerated from their canonical inputs.

- [ ] **Step 5: Verify policy ownership and generated output**

Run: `make agent-instructions-check`

Expected: PASS, including exactly one policy link in each generated root file.

Run: `rg -n 'skill \| hook \| global_md|Knowledge deposition' AGENTS.md CLAUDE.md docs/agent`

Expected: the detailed destination definitions exist only in `docs/agent/knowledge-deposition.md`; generated files contain
the compact gate and link.

- [ ] **Step 6: Commit the policy**

```bash
git add docs/agent/knowledge-deposition.md docs/agent/instructions.md docs/agent/templates \
  scripts/quality/generate-agent-instructions-test.sh .pre-commit-config.yaml AGENTS.md CLAUDE.md
git commit -m '[docs](agent): define knowledge deposition policy'
```

## Task 2: Build The Validated Report Writer

**Files:**

- Create: `scripts/knowledge-deposition/common.sh`
- Create: `scripts/knowledge-deposition/report.sh`
- Create: `scripts/knowledge-deposition/report-test.sh`
- Modify: `Makefile`

- [ ] **Step 1: Write failing report contract tests**

Create `scripts/knowledge-deposition/report-test.sh`. Use `mktemp -d`, set
`KNOWLEDGE_DEPOSITION_ROOT` to the fixture root, and clean it in an EXIT trap. Add helpers that pipe JSON into
`report.sh --client codex --session session-a --task task-a --repo-root "$fixture"`. Cover these assertions:

```bash
assert_success candidates valid-candidates.json
assert_success none valid-none.json
assert_failure 'unknown destination' invalid-destination.json
assert_failure 'none decision requires an empty candidates array' none-with-candidate.json
assert_failure 'candidates decision requires at least one candidate' candidates-empty.json
assert_failure 'evidence paths must be repository-relative' absolute-evidence.json
assert_failure 'evidence paths must not traverse parents' traversing-evidence.json
assert_failure 'forbidden sensitive field' secret-field.json
assert_failure 'obsidian candidate requires knowledge_type' obsidian-missing-type.json
assert_failure 'multi-destination candidates require consumption_purpose' shared-claim-no-purpose.json
```

For the valid candidate fixture, include one `skill` and one `obsidian` candidate sharing `claim_group: "bounded-io"`, with
distinct non-empty `consumption_purpose` values. Assert the generated JSON equals normalized input fields, the Markdown
contains both destinations and evidence paths, and neither artifact contains `transcript`, `prompt`, `token`, or
`raw_response` keys.

- [ ] **Step 2: Run the writer test and confirm it fails**

Run: `bash scripts/knowledge-deposition/report-test.sh`

Expected: FAIL because `scripts/knowledge-deposition/report.sh` does not exist.

- [ ] **Step 3: Implement shared normalization and validation**

Create `common.sh` with this public interface:

```bash
#!/usr/bin/env bash
set -euo pipefail

knowledge_fail() { printf 'knowledge deposition: %s\n' "$1" >&2; exit 1; }

knowledge_sanitize_session() {
    local value=$1
    [[ "$value" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] ||
        knowledge_fail 'invalid session ID'
    printf '%s\n' "$value"
}

knowledge_is_stratum_root() {
    local root=$1
    [[ -f "$root/docs/agent/knowledge-deposition.md" && -f "$root/go.mod" ]] || return 1
    grep -q '^module github.com/ArchChuan/Stratum$' "$root/go.mod"
}

knowledge_report_path() {
    local root=$1 client=$2 session=$3 task_id=$4 date=$5
    printf '%s/tmp/knowledge-deposition/%s/%s-%s-%s.json\n' \
        "$root" "$date" "$client" "$session" "$task_id"
}
```

Add `knowledge_validate_payload` using one `jq -e` program. It must enforce the top-level fields from the design, the
destination enum, decision/array consistency, candidate required fields, relative evidence paths without `..`, Obsidian
fields, same-claim consumption purposes, and a recursive forbidden-key scan for keys matching
`transcript|prompt|password|secret|token|api_key|raw_response` case-insensitively. Return normalized compact JSON with
unknown top-level and candidate fields rejected.

- [ ] **Step 4: Implement atomic JSON and Markdown output**

Create `report.sh` that accepts only:

```text
report.sh --client codex|claude --session <safe-id> --task <safe-id> --repo-root <absolute-root>
```

Read one JSON object from stdin, derive repository identity from `git -C "$repo_root" rev-parse HEAD`, and add
`schema_version: 1`, client, session, task ID, worktree root, commit, and UTC creation time before calling
`knowledge_validate_payload`. Require the task ID to match the current marker for that client/session. Do not accept an
output path.

Use:

```bash
report_dir="$repo_root/tmp/knowledge-deposition/$(date -u +%F)"
mkdir -p "$report_dir"
exec 9>"$repo_root/tmp/knowledge-deposition/.lock"
flock 9
json_tmp=$(mktemp "$report_dir/.report.XXXXXX")
md_tmp=$(mktemp "$report_dir/.report.XXXXXX")
trap 'rm -f "${json_tmp:-}" "${md_tmp:-}"' EXIT
```

Write and revalidate JSON, render Markdown only from normalized JSON with `jq -r`, then `mv` both temporary files to the
session/task-ID paths. Render `latest.md` to a temporary file containing relative links to those two artifacts and rename
it while the lock is held. Print the absolute Markdown report path on success.

- [ ] **Step 5: Run tests and add concurrency assertions**

Run: `bash scripts/knowledge-deposition/report-test.sh`

Expected: all contract tests PASS.

Extend the test to launch 20 writers across distinct sessions in the background, wait for every PID, parse every JSON
with `jq -e`, and assert `latest.md` contains one complete JSON link and one complete Markdown link with no partial line.

Run: `bash scripts/knowledge-deposition/report-test.sh`

Expected: contract and concurrency tests PASS.

- [ ] **Step 6: Add the focused Make target and commit**

Add `knowledge-deposition-test` to `.PHONY` and implement:

```make
knowledge-deposition-test:
 /bin/bash scripts/knowledge-deposition/report-test.sh
 /bin/bash scripts/knowledge-deposition/hooks-test.sh
```

The second script will be added in Task 3; run only the existing test before this commit.

```bash
git add scripts/knowledge-deposition/common.sh scripts/knowledge-deposition/report.sh \
  scripts/knowledge-deposition/report-test.sh Makefile
git commit -m '[feat](agent): add knowledge deposition reports'
```

## Task 3: Add Session-Bound Codex And Claude Adapters

**Files:**

- Create: `scripts/knowledge-deposition/check.sh`
- Create: `scripts/knowledge-deposition/codex-task-start.sh`
- Create: `scripts/knowledge-deposition/codex-stop.sh`
- Create: `scripts/knowledge-deposition/claude-task-start.sh`
- Create: `scripts/knowledge-deposition/claude-stop.sh`
- Create: `scripts/knowledge-deposition/hooks-test.sh`

- [ ] **Step 1: Write failing lifecycle protocol tests**

Create `hooks-test.sh` with JSON fixtures containing `cwd`, `session_id`, `hook_event_name`, and `stop_hook_active`. Test
each adapter as a subprocess with fixture JSON on stdin.

Required Codex assertions:

```bash
jq -e '.continue == true and (.systemMessage | contains("codex-session-a")) and
  (.systemMessage | contains("task-"))' <<<"$codex_start"
jq -e '.decision == "block" and (.reason | contains("report is missing"))' <<<"$codex_missing"
jq -e '.continue == true and .suppressOutput == true' <<<"$codex_valid"
```

Required Claude assertions:

```bash
jq -e '.hookSpecificOutput.hookEventName == "UserPromptSubmit" and
  (.hookSpecificOutput.additionalContext | contains("claude-session-a"))' <<<"$claude_start"
jq -e '.decision == "block" and (.reason | contains("report is missing"))' <<<"$claude_missing"
jq -e '.continue == true and .suppressOutput == true' <<<"$claude_valid"
```

Also assert malformed input blocks in Stratum, a report for `session-b` cannot satisfy `session-a`, an invalid report
blocks with its schema error, `stop_hook_active: true` still blocks until repaired, an earlier task report cannot satisfy
Stop after another prompt changes the current task marker, and all four adapters return a quiet allow response for a
fixture repository without the Stratum identity files.

- [ ] **Step 2: Run the hook tests and confirm they fail**

Run: `bash scripts/knowledge-deposition/hooks-test.sh`

Expected: FAIL because the adapters do not exist.

- [ ] **Step 3: Implement the read-only completion checker**

Create `check.sh` with this CLI:

```text
check.sh --client codex|claude --session <safe-id> --repo-root <absolute-root>
```

Read `tmp/knowledge-deposition/current/<client>-<session>.json`, extract its task ID, and find exactly
`tmp/knowledge-deposition/*/<client>-<session>-<task-id>.json`. Validate it through `knowledge_validate_payload`, and
assert embedded client, session, task ID, repository root, and current Git worktree identity match. Print `valid` on
success. On failure print one actionable line that includes the exact `report.sh` invocation but never echoes report
content. The checker does not mutate task state.

- [ ] **Step 4: Implement both UserPromptSubmit adapters**

Each adapter reads stdin once, validates JSON with `jq -e`, extracts `.cwd` and `.session_id`, resolves the Git root, and
quietly allows non-Stratum roots. For Stratum, create `task-<UTC-nanoseconds>-<8-random-hex-bytes>` under the shared lock,
atomically replace `tmp/knowledge-deposition/current/<client>-<session>.json`, and inject this instruction with the
client-specific response envelope:

```text
Knowledge deposition task: <client>-<session>-<task>. Before the final response, classify this task using
docs/agent/knowledge-deposition.md and pipe one JSON object to:
bash scripts/knowledge-deposition/report.sh --client <client> --session <session> --task <task> --repo-root <root>
Then summarize the generated Markdown report. A decision of none still requires a report.
```

Codex emits `{"continue":true,"systemMessage":"..."}`. Claude emits
`hookSpecificOutput.hookEventName: "UserPromptSubmit"` plus `additionalContext`.

- [ ] **Step 5: Implement both Stop adapters**

Each Stop adapter extracts the same identity and invokes `check.sh`. On valid reports emit
`{"continue":true,"suppressOutput":true}`. On invalid reports emit `{"decision":"block","reason":"..."}`. Preserve
client-specific event fields if the local hook harness requires them. Do not special-case `stop_hook_active` into an
allow; the repaired report is the only path to success.

- [ ] **Step 6: Run protocol tests and commit**

Run: `bash scripts/knowledge-deposition/hooks-test.sh`

Expected: all Codex, Claude, cross-session, cross-task, re-entry, malformed, and non-Stratum cases PASS.

Run: `make knowledge-deposition-test`

Expected: report and hook suites PASS.

```bash
git add scripts/knowledge-deposition Makefile
git commit -m '[feat](agent): enforce session knowledge gate'
```

## Task 4: Add Idempotent User-Level Hook Installation

**Files:**

- Create: `scripts/knowledge-deposition/install-hooks.sh`
- Modify: `scripts/knowledge-deposition/hooks-test.sh`
- Local fixture inputs created only under test temp directories.

- [ ] **Step 1: Add failing installer fixture tests**

In `hooks-test.sh`, create minimal Codex and Claude JSON settings in a temp directory and invoke:

```bash
CODEX_HOOKS_JSON="$fixture/codex-hooks.json" \
CLAUDE_SETTINGS_JSON="$fixture/claude-settings.json" \
bash scripts/knowledge-deposition/install-hooks.sh --repo-root "$repo_root"
```

Assert exactly one UserPromptSubmit and one Stop command for each client, unrelated hooks remain byte-equivalent as parsed
JSON, every command points to `scripts/knowledge-deposition/<client>-<event>.sh`, and a second installer run produces no
additional entries. Assert malformed existing JSON fails before changing either file and both original files remain.

- [ ] **Step 2: Run tests and confirm the installer is missing**

Run: `bash scripts/knowledge-deposition/hooks-test.sh`

Expected: FAIL because `install-hooks.sh` does not exist.

- [ ] **Step 3: Implement transactional, idempotent registration**

Create `install-hooks.sh` with defaults:

```bash
codex_json=${CODEX_HOOKS_JSON:-/home/yang/.codex/hooks.json}
claude_json=${CLAUDE_SETTINGS_JSON:-/home/yang/.claude/settings.json}
```

Require `--repo-root` to resolve to the primary Stratum checkout. Validate both inputs with `jq -e` before mutation. Use
`jq` functions that remove only entries whose command exactly references one of the four knowledge-deposition adapters,
then append one canonical entry per event. Codex commands invoke Codex adapters; Claude commands invoke Claude adapters.
Preserve every unrelated object and array.

Write both new documents to sibling temporary files, parse them again, create timestamped `.knowledge-deposition.bak`
backups, then rename both. If the second rename fails, restore the first from its backup and return non-zero. Print the
four registered commands and backup paths; never print the full settings documents.

- [ ] **Step 4: Verify idempotence and commit**

Run: `make knowledge-deposition-test`

Expected: report, adapter, malformed-settings, preservation, rollback, and idempotence cases PASS.

```bash
git add scripts/knowledge-deposition/install-hooks.sh scripts/knowledge-deposition/hooks-test.sh
git commit -m '[feat](agent): install knowledge lifecycle hooks'
```

## Task 5: Permit Only The Report Command In The Primary Checkout

**Files:**

- Modify locally: `/home/yang/.local/lib/stratum-worktree-policy.sh`
- Modify locally: `/home/yang/.local/bin/test-stratum-worktree-guard`

- [ ] **Step 1: Add failing local policy cases**

Add these assertions to `/home/yang/.local/bin/test-stratum-worktree-guard`:

```bash
assert_policy 'primary knowledge report command' "$primary" Bash \
  'bash scripts/knowledge-deposition/report.sh --client codex --session session-a --task task-a --repo-root /home/yang/go-projects/stratum' allow
assert_policy 'primary report with caller output path' "$primary" Bash \
  'bash scripts/knowledge-deposition/report.sh --client codex --session session-a --task task-a --repo-root /home/yang/go-projects/stratum --output /tmp/x' deny
assert_policy 'primary report through shell chain' "$primary" Bash \
  'bash scripts/knowledge-deposition/report.sh --client codex --session session-a --task task-a --repo-root /home/yang/go-projects/stratum && touch README.md' deny
assert_policy 'primary arbitrary knowledge script' "$primary" Bash \
  'bash scripts/knowledge-deposition/install-hooks.sh --repo-root /home/yang/go-projects/stratum' deny
```

- [ ] **Step 2: Run the local guard test and confirm the exact report command is denied**

Run: `/home/yang/.local/bin/test-stratum-worktree-guard`

Expected: FAIL only on `primary knowledge report command`; unsafe variants remain denied.

- [ ] **Step 3: Add the exact command recognizer**

Add `is_knowledge_deposition_report` to the policy. Parse tokens without `eval`; require exactly ten tokens in this order:

```text
bash scripts/knowledge-deposition/report.sh --client codex|claude --session <safe-id> --task <safe-id> --repo-root <primary-root>
```

Reject shell metacharacters, variables, substitutions, extra flags, alternate repository roots, and unsafe session IDs.
Call `allow` only when this recognizer succeeds before the generic Bash mutation scan.

- [ ] **Step 4: Verify both platform wrappers and preserve a rollback copy**

Run: `/home/yang/.local/bin/test-stratum-worktree-guard`

Expected: all policy, Codex wrapper, and Claude wrapper cases PASS.

Create a dated backup of the installed policy before replacing it. Record the backup path in the task execution log; do
not add the local policy or backup to Git.

## Task 6: Register Hooks And Perform Real Lifecycle Verification

**Files:**

- Modify locally: `/home/yang/.codex/hooks.json`
- Modify locally: `/home/yang/.claude/settings.json`
- Generate locally: `tmp/knowledge-deposition/<date>/*`
- Modify: `docs/agent/knowledge-workspace.md`

- [ ] **Step 1: Read the end-to-end skill and define evidence commands**

Read `.agents/skills/stratum-e2e-development/SKILL.md` from the primary repository and follow its process. This change is
Agent lifecycle functionality, so synthetic shell tests alone are insufficient.

- [ ] **Step 2: Install user-level registrations transactionally**

Run from the feature worktree while explicitly targeting the primary checkout path that the hooks will use after merge:

```bash
bash scripts/knowledge-deposition/install-hooks.sh --repo-root /home/yang/go-projects/stratum
```

Expected: four registrations reported, both JSON files parse with `jq -e`, and backup paths are printed. If the primary
checkout does not yet contain the committed adapter scripts, do not leave active registrations pointing at missing files;
run installation only after the branch is merged or temporarily use fixture settings for pre-merge verification.

- [ ] **Step 3: Verify registered configuration without exposing unrelated settings**

Run bounded `jq` queries that select only commands containing `knowledge-deposition` from both settings files.

Expected: exactly one UserPromptSubmit and one Stop command per client; no settings document is printed in full.

- [ ] **Step 4: Exercise missing, invalid, and repaired reports with real hook payloads**

For each client, start a disposable local session in Stratum or use the closest officially supported hook harness:

1. Capture the UserPromptSubmit-injected report identity without recording the user prompt.
2. Attempt completion without a report and observe a Stop block containing `report is missing`.
3. Submit an invalid cross-session report and observe another block.
4. Run the exact report command with `decision: none` and an evidence-based reason.
5. Attempt completion again and observe success.
6. Parse the generated JSON, inspect Markdown, and verify `latest.md` links to complete artifacts.

Expected: both clients demonstrate the full missing-to-repaired lifecycle. If Codex exposes no true blocking Stop response
in the installed version, record the version and observed behavior, keep the deterministic checker, and leave that client
registration disabled rather than claiming enforcement.

- [ ] **Step 5: Document operations and boundaries**

Add to `docs/agent/knowledge-workspace.md`:

- canonical policy and report paths;
- JSON as source of truth and Markdown as rendered view;
- exact manual report command;
- local-only retention status;
- no automatic destination writes;
- installer command, backup behavior, and uninstall/recovery procedure;
- client limitations found during real verification.

- [ ] **Step 6: Commit operational documentation**

```bash
git add docs/agent/knowledge-workspace.md
git commit -m '[docs](agent): document knowledge task gate'
```

## Task 7: Wire Pre-Commit Coverage And Run Final Verification

**Files:**

- Modify: `.pre-commit-config.yaml`
- Modify: `scripts/quality/generate-agent-instructions-test.sh`
- Test: all files from Tasks 1-6

- [ ] **Step 1: Add a focused pre-commit hook**

Add this local hook beside the instruction-generation check:

```yaml
- id: knowledge-deposition-test
  name: knowledge deposition hooks are valid
  entry: /bin/bash -c 'make knowledge-deposition-test'
  language: system
  pass_filenames: false
  files: '^(scripts/knowledge-deposition/|docs/agent/(instructions|knowledge-deposition|knowledge-workspace)\.md|docs/agent/templates/(agents-prefix|claude-prefix)\.md|AGENTS\.md|CLAUDE\.md|Makefile)$'
```

Extend the generator fixture's local-repository hook accounting so it expects both named local hooks and still rejects a
lookalike hook outside `repos[].hooks`.

- [ ] **Step 2: Run focused verification**

Run:

```bash
make knowledge-deposition-test
make agent-instructions-check
bash scripts/quality/risk-regression-guard.sh
/home/yang/.local/bin/test-stratum-worktree-guard
```

Expected: every command exits 0; report tests include concurrent writers; both platform contracts pass.

- [ ] **Step 3: Run repository guardrails**

Run: `make risk-guardrails`

Expected: PASS. Any unrelated pre-existing failure must be reported with exact evidence and must not replace focused test
results.

Run: `pre-commit run --all-files`

Expected: all hooks PASS or an unrelated existing failure is isolated and documented; every hook matching changed files
must pass on a direct rerun.

- [ ] **Step 4: Inspect final changes and local-only state**

Run:

```bash
git diff --check origin/main...HEAD
git status --short --branch
git diff --stat origin/main...HEAD
git ls-files tmp .claude .codex
```

Expected: no whitespace errors; only intended project files are committed; `tmp/`, `.claude/`, and `.codex/` report no
tracked local artifacts.

- [ ] **Step 5: Commit pre-commit integration**

```bash
git add .pre-commit-config.yaml scripts/quality/generate-agent-instructions-test.sh
git commit -m '[test](agent): guard knowledge deposition lifecycle'
```

- [ ] **Step 6: Produce the task's own deposition report**

Use the actual client/session identity and pipe a final report into `report.sh`. At minimum it should contain the
`project_git` candidate for this implementation and should add `skill`, `hook`, `global_md`, or `obsidian` candidates only
when the completed evidence meets the canonical policy.

Expected: Stop gate accepts the report and the final response links its Markdown path.
