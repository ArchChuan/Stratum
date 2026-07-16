# Recent Bug Lessons Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 2026-06-27 至 2026-07-16 的 Git 与 claude-mem 修复记录归纳为可执行的项目防复发规则。

**Architecture:** `docs/agent/bug-lessons.md` 是详细经验的唯一来源，按根因主题组织；根目录 `CLAUDE.md` 和 `AGENTS.md` 仅保留高频硬规则摘要与索引。资料提取先建立修复覆盖表，再将重复提交和同一排障链合并，避免流水账。

**Tech Stack:** Markdown、Git、claude-mem MCP

---

## Task 1: Build the evidence map

**Files:**

- Reference: Git history from `2026-06-27` through `2026-07-16`
- Reference: claude-mem observations for project `stratum` in the same window

- [ ] **Step 1: List repair-related Git history**

Run:

```bash
git log --all --since='2026-06-27 00:00:00 +0800' --until='2026-07-16 23:59:59 +0800' \
  --date=short --pretty=format:'%h%x09%ad%x09%s'
```

Expected: commits covering application, frontend, tests, migrations, CI/CD, Helm and K3s fixes.

- [ ] **Step 2: Search claude-mem before fetching full observations**

Use `search` with project `stratum`, the same date bounds, and queries covering `bug fix`, `failed error panic 404`, migration, CI, deploy, frontend and permissions. Use `timeline` around relevant IDs, then fetch only selected observation IDs with `get_observations`.

Expected: an index of repaired failures, root causes and verification evidence without loading unrelated session content.

- [ ] **Step 3: Collapse evidence into root-cause themes**

Classify each supported repair under API contracts, authorization/error semantics, data lifecycle/schema, concurrency/observability, frontend responsiveness, tests, or delivery/runtime. Exclude unresolved findings, temporary debugging actions and duplicate cherry-picks.

Expected: every reusable repair maps to one theme; exclusions have an explicit reason.

## Task 2: Write the canonical lessons

**Files:**

- Create: `docs/agent/bug-lessons.md`

- [ ] **Step 1: Write source and usage boundaries**

State the exact date window, Git plus claude-mem sources, evidence threshold, and rule that newer code and specialist documents override historical implementation details.

- [ ] **Step 2: Write thematic lessons**

For each theme, record the observed failure pattern, consolidated root cause, mandatory prevention rule and verification method. Reference existing specialist documents rather than duplicating their full content.

- [ ] **Step 3: Check the document for unsupported claims**

Run:

```bash
rg -n 'TODO|TBD|待确认|可能|也许' docs/agent/bug-lessons.md
```

Expected: no output.

## Task 3: Update thin entry points and verify

**Files:**

- Modify: `CLAUDE.md`
- Modify: `AGENTS.md`

- [ ] **Step 1: Add matching high-frequency safeguards**

Add a concise section to both files covering contract ownership, full data-lifecycle checks, optional instrumentation safety, production evidence, and deployment configuration consistency. Link the detailed lessons document.

- [ ] **Step 2: Add the lessons document to both indexes**

Add `docs/agent/bug-lessons.md` beside the existing task-specific documentation index without changing unrelated entries.

- [ ] **Step 3: Verify links, formatting and scope**

Run:

```bash
test -f docs/agent/bug-lessons.md
rg -n 'bug-lessons\.md' CLAUDE.md AGENTS.md
git diff --check
git diff -- CLAUDE.md AGENTS.md docs/agent/bug-lessons.md
```

Expected: the new file exists, both entry points link it, whitespace checks pass, and the diff contains only the approved documentation changes.
