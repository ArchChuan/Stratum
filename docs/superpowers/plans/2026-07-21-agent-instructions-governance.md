# Agent Instructions Governance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace independently maintained `AGENTS.md` and `CLAUDE.md` with tracked, deterministic generated entry files backed by one canonical project-instruction document and two minimal tool-specific templates.

**Architecture:** `docs/agent/instructions.md` owns all shared project rules. A Bash generator composes each tracked root entry from a fixed generated-file banner, one tool-specific prefix, and the canonical shared body; `--check` detects drift without modifying files. Make, pre-commit, CI, and the existing risk guard reuse the same generator contract.

**Tech Stack:** Bash, Git, Make, pre-commit, GitHub Actions, Markdown.

---

## File structure

- Create `docs/agent/instructions.md`: only hand-written source for shared architecture, development, testing, security, risk, and product rules.
- Create `docs/agent/templates/agents-prefix.md`: Codex-only entry preface; no shared project rules.
- Create `docs/agent/templates/claude-prefix.md`: Claude Code-only entry preface; no shared project rules.
- Create `scripts/quality/generate-agent-instructions.sh`: deterministic generator and drift checker.
- Create `scripts/quality/generate-agent-instructions-test.sh`: isolated contract tests for generation, drift, failure safety, and Git integration.
- Create and track `AGENTS.md`: generated Codex entry.
- Create and track `CLAUDE.md`: generated Claude Code entry.
- Modify `.gitignore`: stop ignoring only the two root entry files; continue ignoring tool directories.
- Modify `Makefile`: expose generation and check targets.
- Modify `.pre-commit-config.yaml`: reject stale generated entries.
- Modify `.github/workflows/ci.yml`: run the same check in CI.
- Modify `scripts/quality/risk-regression-guard-test.sh`: validate the canonical risk section and generated entry consistency instead of two hand-maintained copies.

### Task 1: Add failing generator contract tests

**Files:**

- Create: `scripts/quality/generate-agent-instructions-test.sh`
- Test: `scripts/quality/generate-agent-instructions-test.sh`

- [ ] **Step 1: Create the test harness and fixture builder**

Create `scripts/quality/generate-agent-instructions-test.sh` with the following foundation:

```bash
#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GENERATOR="${ROOT}/scripts/quality/generate-agent-instructions.sh"
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "${TEST_ROOT}"' EXIT

fail() {
  echo "$*" >&2
  exit 1
}

assert_contains() {
  local file="$1" expected="$2"
  grep -Fq "${expected}" "${file}" || fail "${file} missing: ${expected}"
}

assert_not_contains() {
  local file="$1" unexpected="$2"
  if grep -Fq "${unexpected}" "${file}"; then
    fail "${file} unexpectedly contains: ${unexpected}"
  fi
}

new_fixture() {
  local name="$1" fixture="${TEST_ROOT}/${name}"
  mkdir -p "${fixture}/docs/agent/templates" "${fixture}/scripts/quality"
  cp "${GENERATOR}" "${fixture}/scripts/quality/generate-agent-instructions.sh"
  printf '# Shared\n\nshared-rule\n' > "${fixture}/docs/agent/instructions.md"
  printf '> Codex entry\n\ncodex-only\n' > "${fixture}/docs/agent/templates/agents-prefix.md"
  printf '> Claude Code entry\n\nclaude-only\n' > "${fixture}/docs/agent/templates/claude-prefix.md"
  printf '%s' "${fixture}"
}
```

- [ ] **Step 2: Add generation and content-isolation assertions**

Append:

```bash
fixture="$(new_fixture generate)"
/bin/bash "${fixture}/scripts/quality/generate-agent-instructions.sh"

assert_contains "${fixture}/AGENTS.md" 'generated; do not edit directly'
assert_contains "${fixture}/AGENTS.md" 'codex-only'
assert_contains "${fixture}/AGENTS.md" 'shared-rule'
assert_not_contains "${fixture}/AGENTS.md" 'claude-only'

assert_contains "${fixture}/CLAUDE.md" 'generated; do not edit directly'
assert_contains "${fixture}/CLAUDE.md" 'claude-only'
assert_contains "${fixture}/CLAUDE.md" 'shared-rule'
assert_not_contains "${fixture}/CLAUDE.md" 'codex-only'
```

- [ ] **Step 3: Add no-op and drift assertions**

Append:

```bash
agents_mtime="$(stat -c %Y "${fixture}/AGENTS.md")"
claude_mtime="$(stat -c %Y "${fixture}/CLAUDE.md")"
/bin/bash "${fixture}/scripts/quality/generate-agent-instructions.sh"
[[ "$(stat -c %Y "${fixture}/AGENTS.md")" == "${agents_mtime}" ]] || fail 'AGENTS.md rewritten without changes'
[[ "$(stat -c %Y "${fixture}/CLAUDE.md")" == "${claude_mtime}" ]] || fail 'CLAUDE.md rewritten without changes'

/bin/bash "${fixture}/scripts/quality/generate-agent-instructions.sh" --check
printf '\nnew-shared-rule\n' >> "${fixture}/docs/agent/instructions.md"
set +e
check_output="$(/bin/bash "${fixture}/scripts/quality/generate-agent-instructions.sh" --check 2>&1)"
status=$?
set -e
[[ "${status}" -ne 0 ]] || fail '--check succeeded with stale entries'
grep -Fq 'AGENTS.md' <<< "${check_output}" || fail '--check did not report AGENTS.md'
grep -Fq 'CLAUDE.md' <<< "${check_output}" || fail '--check did not report CLAUDE.md'
```

- [ ] **Step 4: Add single-template drift, subdirectory, and missing-input assertions**

Append:

```bash
/bin/bash "${fixture}/scripts/quality/generate-agent-instructions.sh"
printf '\nnew-codex-rule\n' >> "${fixture}/docs/agent/templates/agents-prefix.md"
set +e
check_output="$(/bin/bash "${fixture}/scripts/quality/generate-agent-instructions.sh" --check 2>&1)"
status=$?
set -e
[[ "${status}" -ne 0 ]] || fail '--check succeeded with stale Codex entry'
grep -Fq 'AGENTS.md' <<< "${check_output}" || fail '--check did not report AGENTS.md'
if grep -Fq 'CLAUDE.md' <<< "${check_output}"; then
  fail '--check reported unchanged CLAUDE.md'
fi

(
  cd "${fixture}/docs/agent"
  /bin/bash ../../scripts/quality/generate-agent-instructions.sh
)
/bin/bash "${fixture}/scripts/quality/generate-agent-instructions.sh" --check

missing_fixture="$(new_fixture missing-input)"
printf 'old-agents\n' > "${missing_fixture}/AGENTS.md"
printf 'old-claude\n' > "${missing_fixture}/CLAUDE.md"
rm "${missing_fixture}/docs/agent/templates/claude-prefix.md"
set +e
/bin/bash "${missing_fixture}/scripts/quality/generate-agent-instructions.sh" >/dev/null 2>&1
status=$?
set -e
[[ "${status}" -ne 0 ]] || fail 'generation succeeded with missing template'
[[ "$(cat "${missing_fixture}/AGENTS.md")" == 'old-agents' ]] || fail 'AGENTS.md changed after input failure'
[[ "$(cat "${missing_fixture}/CLAUDE.md")" == 'old-claude' ]] || fail 'CLAUDE.md changed after input failure'
```

- [ ] **Step 5: Add repository integration assertions**

Append:

```bash
/bin/bash "${GENERATOR}" --check

git -C "${ROOT}" ls-files --error-unmatch AGENTS.md >/dev/null || fail 'AGENTS.md is not tracked'
git -C "${ROOT}" ls-files --error-unmatch CLAUDE.md >/dev/null || fail 'CLAUDE.md is not tracked'

if git -C "${ROOT}" check-ignore -q AGENTS.md; then
  fail 'AGENTS.md is still ignored'
fi
if git -C "${ROOT}" check-ignore -q CLAUDE.md; then
  fail 'CLAUDE.md is still ignored'
fi

echo 'agent instruction generator tests passed'
```

- [ ] **Step 6: Run the test and verify RED**

Run:

```bash
/bin/bash scripts/quality/generate-agent-instructions-test.sh
```

Expected: FAIL before executing fixtures because `scripts/quality/generate-agent-instructions.sh` does not exist.

- [ ] **Step 7: Commit the failing test**

```bash
git add scripts/quality/generate-agent-instructions-test.sh
git commit -m "test(agent): define instruction generation contract"
```

### Task 2: Implement the deterministic generator

**Files:**

- Create: `scripts/quality/generate-agent-instructions.sh`
- Test: `scripts/quality/generate-agent-instructions-test.sh`

- [ ] **Step 1: Implement argument parsing, input validation, and rendering**

Create `scripts/quality/generate-agent-instructions.sh`:

```bash
#!/usr/bin/env bash

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMMON="${ROOT}/docs/agent/instructions.md"
AGENTS_PREFIX="${ROOT}/docs/agent/templates/agents-prefix.md"
CLAUDE_PREFIX="${ROOT}/docs/agent/templates/claude-prefix.md"
CHECK_ONLY=false

usage() {
  echo 'usage: generate-agent-instructions.sh [--check]' >&2
}

case "${1:-}" in
  '') ;;
  --check) CHECK_ONLY=true ;;
  *) usage; exit 2 ;;
esac
[[ "$#" -le 1 ]] || { usage; exit 2; }

for input in "${COMMON}" "${AGENTS_PREFIX}" "${CLAUDE_PREFIX}"; do
  [[ -r "${input}" ]] || {
    echo "agent instructions: missing or unreadable input ${input#"${ROOT}/"}" >&2
    exit 1
  }
done

TMP_DIR="$(mktemp -d "${ROOT}/.agent-instructions.XXXXXX")"
trap 'rm -rf "${TMP_DIR}"' EXIT

render() {
  local prefix="$1" output="$2"
  {
    printf '<!-- generated; do not edit directly -->\n'
    printf '<!-- source: docs/agent/instructions.md + %s -->\n\n' "${prefix#"${ROOT}/"}"
    cat "${prefix}"
    printf '\n---\n\n'
    cat "${COMMON}"
  } > "${output}"
}

render "${AGENTS_PREFIX}" "${TMP_DIR}/AGENTS.md"
render "${CLAUDE_PREFIX}" "${TMP_DIR}/CLAUDE.md"
```

- [ ] **Step 2: Implement drift checking and no-op updates**

Append:

```bash
outputs=(AGENTS.md CLAUDE.md)
drifted=()

for name in "${outputs[@]}"; do
  target="${ROOT}/${name}"
  generated="${TMP_DIR}/${name}"
  if [[ ! -f "${target}" ]] || ! cmp -s "${generated}" "${target}"; then
    drifted+=("${name}")
  fi
done

if [[ "${CHECK_ONLY}" == true ]]; then
  if [[ "${#drifted[@]}" -eq 0 ]]; then
    echo 'agent instructions: generated entries are current'
    exit 0
  fi
  printf 'agent instructions: stale generated entry %s\n' "${drifted[@]}" >&2
  echo 'run: make agent-instructions' >&2
  exit 1
fi

if [[ "${#drifted[@]}" -eq 0 ]]; then
  echo 'agent instructions: no changes'
  exit 0
fi
```

- [ ] **Step 3: Implement rollback-safe installation**

Append:

```bash
BACKUP_DIR="${TMP_DIR}/backup"
mkdir -p "${BACKUP_DIR}"
installed=()

rollback() {
  local failed_target="$1" rollback_failed=false name target backup
  for name in "${installed[@]}"; do
    target="${ROOT}/${name}"
    backup="${BACKUP_DIR}/${name}"
    if [[ -f "${backup}" ]]; then
      cp "${backup}" "${target}" || rollback_failed=true
    else
      rm -f "${target}" || rollback_failed=true
    fi
  done
  echo "agent instructions: failed to install ${failed_target}" >&2
  if [[ "${rollback_failed}" == true ]]; then
    echo 'agent instructions: rollback also failed; inspect AGENTS.md and CLAUDE.md' >&2
  fi
  exit 1
}

for name in "${drifted[@]}"; do
  target="${ROOT}/${name}"
  generated="${TMP_DIR}/${name}"
  [[ ! -f "${target}" ]] || cp "${target}" "${BACKUP_DIR}/${name}"
  install_tmp="${ROOT}/.${name}.tmp.$$"
  cp "${generated}" "${install_tmp}" || rollback "${name}"
  mv "${install_tmp}" "${target}" || rollback "${name}"
  installed+=("${name}")
done

printf 'agent instructions: generated %s\n' "${drifted[@]}"
```

- [ ] **Step 4: Check shell syntax**

Run:

```bash
/bin/bash -n scripts/quality/generate-agent-instructions.sh \
  scripts/quality/generate-agent-instructions-test.sh
```

Expected: exit 0 with no output.

- [ ] **Step 5: Run the test and confirm the next expected failure**

Run:

```bash
/bin/bash scripts/quality/generate-agent-instructions-test.sh
```

Expected: fixture-level generator tests pass, then repository integration fails because the canonical inputs and tracked root entries do not exist yet.

- [ ] **Step 6: Commit the generator**

```bash
git add scripts/quality/generate-agent-instructions.sh
git commit -m "feat(agent): add deterministic instruction generator"
```

### Task 3: Establish the canonical source and tracked generated entries

**Files:**

- Create: `docs/agent/instructions.md`
- Create: `docs/agent/templates/agents-prefix.md`
- Create: `docs/agent/templates/claude-prefix.md`
- Create: `AGENTS.md`
- Create: `CLAUDE.md`
- Modify: `.gitignore:118-125,190-200`
- Test: `scripts/quality/generate-agent-instructions-test.sh`

- [ ] **Step 1: Recover both current local instruction files as migration evidence**

The ignored files exist in the primary checkout, not in the new worktree. Read them without editing the primary checkout:

```bash
sed -n '1,380p' /home/yang/go-projects/stratum/AGENTS.md
sed -n '1,320p' /home/yang/go-projects/stratum/CLAUDE.md
```

Also read the tracked project facts used to resolve conflicts:

```bash
sed -n '1,280p' docs/agent/project.md
sed -n '1,240p' docs/agent/api.md
sed -n '1,240p' docs/agent/observability.md
test -f web/src/services/client.ts
grep '^go ' go.mod
```

Expected: both ignored inputs are readable; tracked files establish current paths and versions. Do not copy either entry wholesale.

- [ ] **Step 2: Create the canonical shared document**

Create `docs/agent/instructions.md` with this section order:

```markdown
# Stratum project instructions

## Default principle
## Knowledge input and evidence
## Technology and directory map
## Architecture decisions
## Multi-tenant DDL and repository rules
## DDD layering and cross-context dependencies
## Git workflow
## Development and end-to-end verification
## Backend conventions
## Frontend conventions
## Logging and security
## Risk regression harness
## Product design rules
## Layered context index
```

Use the following explicit merge decisions:

- Preserve the mandatory worktree workflow from the current `AGENTS.md`; it is absent from the compressed `CLAUDE.md` but enforced by repository hooks.
- Preserve the knowledge-input protocol verbatim in meaning, including repository precedence and `provisional` note handling.
- Use paths and commands proven by tracked files. In particular, use `web/src/services/client.ts` if the file check succeeds, and use the Go version declared by `go.mod` instead of retaining a stale heading.
- Merge the current Claude-only tenant safety rules into the shared tenant section: `execTenant`, tenant ID in ports, Milvus delete-by-filter, obsolete-table cleanup, `RESET search_path`, and schema-qualified startup SQL.
- Merge the current Claude-only pgx JSONB and concurrency/resource-lifecycle rules into backend conventions because they apply to all coding agents.
- Preserve the complete risk checklist and `make risk-guardrails` entry once, under `## Risk regression harness`.
- Preserve product rules from the current `AGENTS.md`, but remove duplicate copies of rules already enforced and explained elsewhere.
- Resolve any remaining contradiction using current code, tests, ADR/design documents, or runtime evidence. If evidence is still insufficient, stop and ask the user instead of combining both variants.
- Keep the document concise by linking detailed module documents, but keep all mandatory rules self-contained because recursive reference loading is not treated as verified.

- [ ] **Step 3: Create minimal tool-specific prefixes**

Create `docs/agent/templates/agents-prefix.md`:

```markdown
> Codex entry: this generated `AGENTS.md` applies to the repository root.

More deeply nested
`AGENTS.md` files may add narrower rules for their subtree; direct user
instructions remain higher priority.
```

Create `docs/agent/templates/claude-prefix.md`:

```markdown
> Claude Code entry: this generated `CLAUDE.md` applies to the repository root.

Claude Code hooks
under `.claude/hooks/` provide mechanical enforcement; this file defines the
project rules those hooks support.
```

Neither prefix may contain a level-1 heading or any architecture, testing,
security, risk, or product rule. The generated document gets its single level-1
heading from `docs/agent/instructions.md`.

- [ ] **Step 4: Remove both duplicated ignore entries**

Edit `.gitignore` so the first block becomes:

```gitignore
# Local AI assistants, code indexes, and developer automation
/.agents/
/.claude/
/.codex/
/.serena/
/.tokensave/
/.worktrees/
/scripts/agents.md
/scripts/daily-agent-interview.sh
/scripts/scan-risks-daily.sh
/scripts/update-docs-daily.sh
/test-results/
```

And the later block becomes:

```gitignore
# AI tooling artifacts
.claude/
.agents/
.codex/
.serena/
```

Do not remove ignores for `.claude/`, `.agents/`, `.codex/`, or `.serena/`.

- [ ] **Step 5: Generate and stage the tracked entries**

Run:

```bash
/bin/bash scripts/quality/generate-agent-instructions.sh
git add .gitignore docs/agent/instructions.md docs/agent/templates \
  AGENTS.md CLAUDE.md
```

Expected: generator reports both root files; `git status --short` shows all seven paths staged and no ignored-entry warning.

- [ ] **Step 6: Run generator and Markdown tests**

Run:

```bash
/bin/bash scripts/quality/generate-agent-instructions-test.sh
/usr/local/bin/pre-commit run markdownlint --files \
  docs/agent/instructions.md docs/agent/templates/agents-prefix.md \
  docs/agent/templates/claude-prefix.md AGENTS.md CLAUDE.md
```

Expected:

```text
agent instruction generator tests passed
markdownlint.............................................................Passed
```

- [ ] **Step 7: Review the generated-only diff**

Run:

```bash
git diff --cached --check
git diff --cached --stat
cmp <(sed -n '/^# Stratum project instructions/,$p' AGENTS.md) \
    <(sed -n '/^# Stratum project instructions/,$p' CLAUDE.md)
```

Expected: no whitespace errors; the shared sections compare equal; differences are limited to the banner source line and tool prefix.

- [ ] **Step 8: Commit canonical instructions and generated entries**

```bash
git commit -m "docs(agent): establish shared instruction source"
```

### Task 4: Wire Make, pre-commit, CI, and risk guard contracts

**Files:**

- Modify: `Makefile:1-8,181-186`
- Modify: `.pre-commit-config.yaml:20-40`
- Modify: `.github/workflows/ci.yml:16-42`
- Modify: `scripts/quality/risk-regression-guard-test.sh:91-118`
- Test: `scripts/quality/generate-agent-instructions-test.sh`
- Test: `scripts/quality/risk-regression-guard-test.sh`

- [ ] **Step 1: Extend generator tests with integration-entry assertions**

Before the final success echo in `scripts/quality/generate-agent-instructions-test.sh`, add:

```bash
grep -Eq '^agent-instructions:' "${ROOT}/Makefile" || fail 'Makefile generation target missing'
grep -Eq '^agent-instructions-check:' "${ROOT}/Makefile" || fail 'Makefile check target missing'
grep -Fq 'id: agent-instructions-check' "${ROOT}/.pre-commit-config.yaml" || fail 'pre-commit generator check missing'
grep -Fq 'make agent-instructions-check' "${ROOT}/.github/workflows/ci.yml" || fail 'CI generator check missing'
```

- [ ] **Step 2: Run the generator test and verify RED**

Run:

```bash
/bin/bash scripts/quality/generate-agent-instructions-test.sh
```

Expected: FAIL with `Makefile generation target missing`.

- [ ] **Step 3: Add Make targets and phony declarations**

Add `agent-instructions agent-instructions-check` to the top `.PHONY` list. Immediately before `risk-guardrails`, add:

```make
# ─── Agent 指令生成：公共事实源 → Codex / Claude Code 入口 ───────────────
agent-instructions:
 /bin/bash scripts/quality/generate-agent-instructions.sh

agent-instructions-check:
 /bin/bash scripts/quality/generate-agent-instructions-test.sh
 /bin/bash scripts/quality/generate-agent-instructions.sh --check
```

- [ ] **Step 4: Add the pre-commit drift hook**

Add after `risk-regression-guard` in `.pre-commit-config.yaml`:

```yaml
      - id: agent-instructions-check
        name: agent instructions are generated and current
        language: system
        entry: /bin/bash scripts/quality/generate-agent-instructions.sh --check
        pass_filenames: false
        require_serial: true
        files: '^(AGENTS\.md|CLAUDE\.md|docs/agent/instructions\.md|docs/agent/templates/(agents|claude)-prefix\.md|scripts/quality/generate-agent-instructions\.sh)$'
```

The hook checks only; it must not rewrite the index.

- [ ] **Step 5: Add the CI check to the existing guardrails job**

In `.github/workflows/ci.yml`, after checkout and before dependency installation, add:

```yaml
      - name: Verify generated agent instructions
        run: make agent-instructions-check
```

This target needs only Bash and Git, so it should run before Go/Node setup to fail quickly.

- [ ] **Step 6: Update risk guard consistency checks**

Add near the top of `scripts/quality/risk-regression-guard-test.sh`:

```bash
AGENT_INSTRUCTIONS="${ROOT}/docs/agent/instructions.md"
AGENT_INSTRUCTIONS_GENERATOR="${ROOT}/scripts/quality/generate-agent-instructions.sh"
```

After the Makefile assertion, add:

```bash
assert_file_contains "${AGENT_INSTRUCTIONS}" \
  '^## Risk regression harness$' 'canonical risk regression section'
for principle in 'fail closed' 'bearer credential' 'tenant-scoped' \
  '破坏性' '持久化失败' '关闭旧资源' '真实链路验证'; do
  if ! grep -q "${principle}" "${AGENT_INSTRUCTIONS}"; then
    echo "canonical instructions missing risk principle: ${principle}" >&2
    exit 1
  fi
done
/bin/bash "${AGENT_INSTRUCTIONS_GENERATOR}" --check
```

Keep the existing `--explain` assertions: they verify the executable guard and the canonical documentation still express the same principles from two independent directions.

- [ ] **Step 7: Run focused tests**

Run:

```bash
/bin/bash scripts/quality/generate-agent-instructions-test.sh
/bin/bash scripts/quality/risk-regression-guard-test.sh
make agent-instructions-check
```

Expected: all three commands pass; the final target runs the generator test and then reports generated entries current.

- [ ] **Step 8: Validate pre-commit and workflow syntax**

Run:

```bash
/usr/local/bin/pre-commit validate-config
/usr/local/bin/pre-commit run agent-instructions-check --all-files
/usr/local/bin/pre-commit run check-yaml --files .github/workflows/ci.yml .pre-commit-config.yaml
```

Expected: all hooks pass.

- [ ] **Step 9: Commit automation integration**

```bash
git add Makefile .pre-commit-config.yaml .github/workflows/ci.yml \
  scripts/quality/generate-agent-instructions-test.sh \
  scripts/quality/risk-regression-guard-test.sh
git commit -m "ci(agent): enforce generated instruction consistency"
```

### Task 5: Run final regression and evidence review

**Files:**

- Verify only; modify earlier task files only if a check reveals a real defect.

- [ ] **Step 1: Re-run generation and prove the worktree stays clean**

Run:

```bash
make agent-instructions
make agent-instructions-check
git status --short
```

Expected: generator reports no changes; checks pass; `git status --short` is empty.

- [ ] **Step 2: Verify tracking and ignore boundaries**

Run:

```bash
git ls-files --error-unmatch AGENTS.md CLAUDE.md \
  docs/agent/instructions.md \
  docs/agent/templates/agents-prefix.md \
  docs/agent/templates/claude-prefix.md
! git check-ignore -q AGENTS.md
! git check-ignore -q CLAUDE.md
git check-ignore -q .claude/
git check-ignore -q .codex/
git check-ignore -q .agents/
```

Expected: all canonical/generated files are tracked; root entries are not ignored; local tool directories remain ignored.

- [ ] **Step 3: Run formatting and targeted guardrails**

Run:

```bash
git diff --check origin/main...HEAD
/usr/local/bin/pre-commit run markdownlint --files \
  docs/agent/instructions.md docs/agent/templates/agents-prefix.md \
  docs/agent/templates/claude-prefix.md AGENTS.md CLAUDE.md \
  docs/superpowers/specs/2026-07-21-agent-instructions-governance-design.md \
  docs/superpowers/plans/2026-07-21-agent-instructions-governance.md
make risk-guardrails
```

Expected: no whitespace errors; Markdown passes; risk guard tests and all routed guardrails pass.

- [ ] **Step 4: Run repository-standard verification proportional to the change**

This change touches documentation and CI/configuration but no Go or frontend source. Run:

```bash
/usr/local/bin/pre-commit run --all-files
make agent-instructions-check
```

Expected: all pre-commit hooks pass or skip according to file types; generator checks pass. If an unrelated full-repository scanner fails, record the exact existing failure and separately rerun every hook matching files changed by this branch.

- [ ] **Step 5: Review the final commit range**

Run:

```bash
git log --oneline --decorate origin/main..HEAD
git diff --stat origin/main...HEAD
git diff --check origin/main...HEAD
```

Expected: two documentation commits (design and plan) plus four implementation commits; diff is limited to the design/plan, canonical instructions, templates, generator/tests, root entries, ignores, Makefile, pre-commit, CI, and risk guard test.

- [ ] **Step 6: Commit any verification-only correction**

Only if Step 1-5 exposed a defect and files were corrected:

```bash
git add <only-the-corrected-files>
git commit -m "fix(agent): correct instruction generation checks"
```

If no correction was needed, do not create an empty commit.
