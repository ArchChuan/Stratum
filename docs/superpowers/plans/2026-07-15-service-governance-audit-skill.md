# Service Governance Audit Skill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build, verify, and immediately use a Stratum-specific Skill that produces an evidence-based service-governance report before any code repair is authorized.

**Architecture:** Keep the phase gate and orchestration in `SKILL.md`, move detailed audit knowledge into focused references, and use a deterministic shell script only to collect candidate signals. Verify the Skill against baseline audit prompts, then run it against the current workspace and write the dated report under `docs/audits/`.

**Tech Stack:** Codex Agent Skills Markdown, Bash, ripgrep, Git, Go/React repository analysis

---

## Task 1: Establish RED baseline

**Files:**

- Create: `.agents/skills/service-governance-audit/evals/evals.json`
- Create: `.agents/skills/service-governance-audit-workspace/iteration-1/baseline.md`

- [ ] **Step 1: Define four evaluation prompts**

Define prompts for a full-project audit, a NATS-focused audit, a pressure request to audit and immediately repair everything, and an evidence-poor keyword-only finding.

- [ ] **Step 2: Run baseline prompts without the new Skill**

Run independent Codex evaluations without loading `.agents/skills/service-governance-audit/SKILL.md` and save concise behavior summaries in `baseline.md`.

- [ ] **Step 3: Record observable baseline gaps**

Record whether each baseline establishes a dependency matrix, requires evidence, controls false positives, writes the prescribed report, and stops at the repair authorization gate.

## Task 2: Implement the minimal Skill

**Files:**

- Create: `.agents/skills/service-governance-audit/SKILL.md`
- Create: `.agents/skills/service-governance-audit/references/audit-checklist.md`
- Create: `.agents/skills/service-governance-audit/references/stratum-dependency-matrix.md`
- Create: `.agents/skills/service-governance-audit/references/severity-and-evidence.md`
- Create: `.agents/skills/service-governance-audit/references/report-template.md`
- Create: `.agents/skills/service-governance-audit/references/open-source-attribution.md`
- Create: `.agents/skills/service-governance-audit/scripts/scan-governance-signals.sh`

- [ ] **Step 1: Write `SKILL.md`**

Include trigger conditions, read-only audit rules, scope selection, evidence workflow, report path, mandatory user approval gate, and the required `stratum-e2e-development` handoff after repair authorization.

- [ ] **Step 2: Write focused reference files**

Encode the dependency-specific checklist, severity rubric, evidence contract, report format, and MIT-source attribution without copying framework-specific implementations.

- [ ] **Step 3: Write the candidate-signal scanner**

Use `rg` to locate timeout, retry, rate-limit, circuit-breaker, concurrency, queue, client, and health/metric signals. The script must print candidate locations only and must not assign severity.

- [ ] **Step 4: Check shell syntax and Markdown**

Run:

```bash
bash -n .agents/skills/service-governance-audit/scripts/scan-governance-signals.sh
npx -y markdownlint-cli2 '.agents/skills/service-governance-audit/**/*.md'
```

Expected: both commands exit successfully.

## Task 3: Verify and refine the Skill

**Files:**

- Create: `.agents/skills/service-governance-audit-workspace/iteration-1/with-skill.md`
- Modify: `.agents/skills/service-governance-audit/SKILL.md`
- Modify: `.agents/skills/service-governance-audit/references/*.md`

- [ ] **Step 1: Re-run the four prompts with the Skill**

Verify that outputs establish scope, trace adjacent layers, distinguish evidence from inference, obey the report gate, and require E2E verification only after authorized repairs.

- [ ] **Step 2: Compare against baseline**

Record pass/fail evidence for scope completeness, evidence quality, false-positive control, report structure, repair gate, and Stratum-specific verification.

- [ ] **Step 3: Close discovered gaps**

Edit only the instructions responsible for observed failures, then repeat the affected evaluation.

## Task 4: Run the full-project read-only audit

**Files:**

- Create: `docs/audits/service-governance-2026-07-15.md`

- [ ] **Step 1: Read project rules and architecture references**

Read root and local agent instructions plus `docs/agent/architecture.md`, `backend-go.md`, `frontend.md`, `nats.md`, `milvus.md`, `observability.md`, `agent-chat-flow.md`, and other references selected by discovered dependencies.

- [ ] **Step 2: Inventory ingress, dependencies, consumers, and background work**

Use repository structure and scanner output to build a coverage matrix. Follow representative calls from entry point to adapter and configuration rather than treating keyword matches as findings.

- [ ] **Step 3: Validate governance findings**

For each candidate, confirm trigger conditions, existing protection, failure amplification, tenant scope, business impact, confidence, and a concrete verification method.

- [ ] **Step 4: Write the dated report**

Use the Skill report template and include findings, positive controls, evidence gaps, retry composition, recommended repair order, and explicit unreviewed areas.

- [ ] **Step 5: Validate the report and stop**

Run Markdownlint and `git diff --check` on the report. Do not modify business code until the user selects finding IDs or an explicit repair scope.

## Task 5: Commit Skill artifacts and hand off report

**Files:**

- Commit the Skill, evaluations, plan, and audit report without staging unrelated workspace files.

- [ ] **Step 1: Review scoped diff**

Run `git status --short` and inspect only the planned paths for secrets, generated noise, missing attribution, and accidental business-code changes.

- [ ] **Step 2: Commit the verified artifacts**

```bash
git add .agents/skills/service-governance-audit \
  .agents/skills/service-governance-audit-workspace \
  docs/superpowers/plans/2026-07-15-service-governance-audit-skill.md \
  docs/audits/service-governance-2026-07-15.md
git commit -m "feat(agent): add service governance audit skill"
```

- [ ] **Step 3: Present report gate**

Summarize finding counts and top risks, link the report, state validation results, and request explicit finding IDs or repair scope. Do not begin repairs in the same step.
