# Track Agent Instruction Files Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the eight existing module-level agent instruction files to Git without changing their contents.

**Architecture:** Copy the existing untracked instruction files from the primary checkout into the isolated feature worktree, then stage them as ordinary repository documentation. Verification compares every copied file byte for byte and checks the complete tracked-file inventory.

**Tech Stack:** Git, POSIX shell, Markdown

---

## Task 1: Track Existing Module Instructions

**Files:**

- Create: `web/AGENTS.md`
- Create: `web/CLAUDE.md`
- Create: `pkg/constants/AGENTS.md`
- Create: `pkg/constants/CLAUDE.md`
- Create: `pkg/migration/AGENTS.md`
- Create: `pkg/migration/CLAUDE.md`
- Create: `pkg/storage/AGENTS.md`
- Create: `pkg/storage/CLAUDE.md`

- [ ] **Step 1: Establish the source inventory**

Run:

```bash
git -C /home/yang/go-projects/stratum status --short --untracked-files=all
```

Expected: the eight scoped instruction files appear as untracked files.

- [ ] **Step 2: Copy the files without modifying content**

Use exact source and destination paths for each of the eight files. Do not generate, format, or rewrite Markdown content.

- [ ] **Step 3: Verify byte identity**

Run `cmp -s` once per source/destination pair.

Expected: all eight comparisons exit with status 0.

- [ ] **Step 4: Add and verify the files**

Run:

```bash
git add web/AGENTS.md web/CLAUDE.md \
  pkg/constants/AGENTS.md pkg/constants/CLAUDE.md \
  pkg/migration/AGENTS.md pkg/migration/CLAUDE.md \
  pkg/storage/AGENTS.md pkg/storage/CLAUDE.md
git diff --cached --check
git ls-files | rg '(^|/)(AGENTS|CLAUDE)\.md$'
```

Expected: no whitespace errors, and all ten repository instruction files are listed.

- [ ] **Step 5: Commit**

```bash
git commit -m "docs(agent): track module instruction files"
```

Expected: one documentation commit containing exactly the eight instruction files.
