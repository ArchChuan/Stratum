# Remove Knowledge Deposition Mechanism Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the Stratum knowledge deposition gate from Codex, Claude Code, and the repository while preserving
unrelated hooks and historical local reports.

**Architecture:** First add and verify an atomic two-client uninstaller beside the existing installer, then use it against
the live configurations before deleting the runtime it references. Remove repository enforcement and generated policy
inputs in separate commits, regenerate current instructions, and finish with live-config and repository residual checks.

**Tech Stack:** Bash, jq, flock, Git, pre-commit, generated Markdown instructions

---

## Tasks

### Task 1: Uninstall Live Codex And Claude Hooks Safely

**Files:**

- Create: `scripts/knowledge-deposition/uninstall-hooks.sh`
- Modify: `scripts/knowledge-deposition/hooks-test.sh`
- Read: `/home/yang/.codex/hooks.json`
- Read: `/home/yang/.claude/settings.json`

- [ ] **Step 1: Add a failing fixture test for exact removal**

Extend `scripts/knowledge-deposition/hooks-test.sh` with fixture JSON containing one managed task-start hook, one managed
stop hook, and unrelated hooks in the same events. Invoke the new command with fixture paths and assert:

```bash
CODEX_HOOKS_JSON="$codex_fixture" CLAUDE_SETTINGS_JSON="$claude_fixture" \
  bash "$SCRIPT_DIR/uninstall-hooks.sh" --repo-root "$actual_root"

! jq -e '.. | strings | select(contains("scripts/knowledge-deposition/"))' "$codex_fixture" "$claude_fixture"
jq -e '.hooks.UserPromptSubmit[0].hooks[0].command == "keep-codex-start"' "$codex_fixture"
jq -e '.hooks.Stop[0].hooks[0].command == "keep-claude-stop"' "$claude_fixture"
```

- [ ] **Step 2: Run the hook test and confirm the missing uninstaller fails**

Run: `bash scripts/knowledge-deposition/hooks-test.sh`

Expected: FAIL because `scripts/knowledge-deposition/uninstall-hooks.sh` does not exist.

- [ ] **Step 3: Implement the atomic uninstaller**

Create `uninstall-hooks.sh` by reusing the installer's safe-path validation, sorted advisory locks, SHA-256 change check,
private backups, two staged temporary files, and rollback behavior. Its jq transform must only remove managed commands:

```jq
def managed:
  (.command | type == "string") and
  (.command | test(
    "^bash /(?:\\\\.|[A-Za-z0-9_@%+=:,./-])*/scripts/knowledge-deposition/" +
    "(?:codex|claude)-(?:task-start|stop)\\.sh$"
  ));
def clean_event:
  map(
    if has("hooks") then
      .hooks = (.hooks | map(select(managed | not))) |
      select((.hooks | length) > 0)
    else . end
  );
.hooks.UserPromptSubmit = ((.hooks.UserPromptSubmit // []) | clean_event) |
.hooks.Stop = ((.hooks.Stop // []) | clean_event)
```

The command must be idempotent and print only backup paths, never configuration contents.

- [ ] **Step 4: Run the fixture test and verify it passes**

Run: `bash scripts/knowledge-deposition/hooks-test.sh`

Expected: PASS including exact removal, unrelated-hook preservation, idempotence, private backups, and rollback fixtures.

- [ ] **Step 5: Execute the verified uninstaller against both live clients**

Run:

```bash
bash scripts/knowledge-deposition/uninstall-hooks.sh \
  --repo-root /home/yang/go-projects/stratum
```

Expected: both configurations are replaced after private backups are created.

- [ ] **Step 6: Verify live absence and unrelated hook preservation**

Run:

```bash
jq -e '[.. | strings | select(contains("scripts/knowledge-deposition/"))] | length == 0' \
  /home/yang/.codex/hooks.json /home/yang/.claude/settings.json
jq -e '.hooks | type == "object"' /home/yang/.codex/hooks.json /home/yang/.claude/settings.json
```

Expected: both commands exit 0.

- [ ] **Step 7: Commit the tested uninstall capability before deleting it**

```bash
git add scripts/knowledge-deposition/uninstall-hooks.sh scripts/knowledge-deposition/hooks-test.sh
git commit -m '[fix](agent): add safe knowledge hook uninstaller'
```

### Task 2: Remove Repository Runtime And Enforcement

**Files:**

- Delete: `scripts/knowledge-deposition/`
- Modify: `Makefile`
- Modify: `.pre-commit-config.yaml`

- [ ] **Step 1: Add absence assertions before deleting the runtime**

Add temporary shell assertions to the relevant generator test that fail while the old target and pre-commit hook exist:

```bash
! grep -Fq 'knowledge-deposition-test:' "$ROOT/Makefile"
! grep -Fq 'id: knowledge-deposition-test' "$ROOT/.pre-commit-config.yaml"
```

- [ ] **Step 2: Run the test and verify the old enforcement makes it fail**

Run: `bash scripts/quality/generate-agent-instructions-test.sh`

Expected: FAIL on the old Make target or pre-commit hook.

- [ ] **Step 3: Delete the runtime and remove direct integrations**

Delete all tracked files under `scripts/knowledge-deposition/`. Remove `knowledge-deposition-test` from `.PHONY` and its
recipe from `Makefile`. Remove the complete `knowledge-deposition-test` hook block from `.pre-commit-config.yaml` and
remove `knowledge-deposition` from the agent-instructions hook file matcher.

- [ ] **Step 4: Run absence assertions**

Run: `bash scripts/quality/generate-agent-instructions-test.sh`

Expected: it progresses past runtime/build integration assertions; remaining failures may identify generator policy inputs
handled in Task 3.

- [ ] **Step 5: Commit runtime removal**

```bash
git add -A scripts/knowledge-deposition Makefile .pre-commit-config.yaml scripts/quality/generate-agent-instructions-test.sh
git commit -m '[chore](agent): remove knowledge deposition runtime'
```

### Task 3: Remove Generated Gate And Current Documentation

**Files:**

- Delete: `docs/agent/knowledge-deposition.md`
- Modify: `docs/agent/instructions.md`
- Modify: `docs/agent/templates/agents-prefix.md`
- Modify: `docs/agent/templates/claude-prefix.md`
- Modify: `docs/agent/knowledge-workspace.md`
- Modify: `scripts/quality/generate-agent-instructions.sh`
- Modify: `scripts/quality/generate-agent-instructions-test.sh`
- Regenerate: `AGENTS.md`
- Regenerate: `CLAUDE.md`

- [ ] **Step 1: Change generator tests to define the absent contract**

Remove fixture creation and assertions requiring `docs/agent/knowledge-deposition.md`. Add assertions that generated
outputs do not contain any of these active phrases:

```bash
for generated in "$FIXTURE/AGENTS.md" "$FIXTURE/CLAUDE.md"; do
  ! grep -Fq 'End-of-task knowledge gate' "$generated"
  ! grep -Fq 'knowledge-deposition' "$generated"
done
```

Update repository-level assertions to apply the same absent contract.

- [ ] **Step 2: Run the generator test and verify it fails against current inputs**

Run: `bash scripts/quality/generate-agent-instructions-test.sh`

Expected: FAIL because current prefixes/instructions/generator still inject the active policy.

- [ ] **Step 3: Remove policy inputs and simplify generator dependencies**

Delete the canonical policy document. Remove the prefix link and `End-of-task knowledge gate` section from the source
instructions and templates. Remove `KNOWLEDGE_DEPOSITION`, readability checks, concatenation, and pre-commit validation
specific to the removed document from the generator and its tests. Remove installation/report enforcement sections from
`docs/agent/knowledge-workspace.md` while keeping unrelated knowledge architecture.

- [ ] **Step 4: Regenerate both client instruction files**

Run: `bash scripts/quality/generate-agent-instructions.sh`

Expected: `AGENTS.md` and `CLAUDE.md` are regenerated without the task-end gate.

- [ ] **Step 5: Run generator tests and check mode**

Run:

```bash
bash scripts/quality/generate-agent-instructions-test.sh
bash scripts/quality/generate-agent-instructions.sh --check
```

Expected: both commands pass.

- [ ] **Step 6: Commit current documentation removal**

```bash
git add docs/agent scripts/quality/generate-agent-instructions.sh \
  scripts/quality/generate-agent-instructions-test.sh AGENTS.md CLAUDE.md
git commit -m '[docs](agent): remove knowledge deposition gate'
```

### Task 4: Residual Audit And Full Verification

**Files:**

- Modify only files identified by active-reference audit

- [ ] **Step 1: Scan for active references outside immutable history**

Run:

```bash
rg -n 'knowledge-deposition|knowledge deposition|Knowledge deposition|知识沉淀' \
  --glob '!docs/superpowers/specs/**' --glob '!docs/superpowers/plans/**' \
  --glob '!tmp/**' .
```

Expected: no active mechanism references. Any match must be classified and removed only when it describes current
installation, enforcement, or runtime behavior.

- [ ] **Step 2: Verify historical reports remain present**

Run:

```bash
find /home/yang/go-projects -path '*/tmp/knowledge-deposition/*' -type f -print -quit
```

Expected: at least one existing historical report path is returned; no report deletion command is run.

- [ ] **Step 3: Run repository verification**

Run:

```bash
bash scripts/quality/generate-agent-instructions-test.sh
bash scripts/quality/generate-agent-instructions.sh --check
make risk-guardrails
git diff --check origin/main...HEAD
```

Expected: every command exits 0.

- [ ] **Step 4: Reverify live client configurations**

Run:

```bash
jq -e '[.. | strings | select(contains("scripts/knowledge-deposition/"))] | length == 0' \
  /home/yang/.codex/hooks.json /home/yang/.claude/settings.json
```

Expected: exit 0 for both files.

- [ ] **Step 5: Commit any audit-only cleanup**

If Step 1 found active references, stage only those reviewed files and commit:

```bash
git commit -m '[chore](agent): remove residual knowledge gate references'
```

If Step 1 found no active references, do not create an empty commit.
