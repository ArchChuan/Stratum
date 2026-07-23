# Agent Interview Consolidation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace date-stamped Agent interview reports with a fixed, deduplicated domain library and make the daily job merge each new report into that library without data loss or partial publication.

**Architecture:** A tracked shell implementation under `scripts/agent-interview/` owns generation, deterministic validation, staging, publication, and failure semantics. Codex performs language classification and semantic fusion against the machine contract in `README.md`; shell code controls the fixed file allowlist, hashes, locks, validation, atomic directory swap, and deletion of consumed inbox files. The ignored `tmp/cron/` script is a deployed copy, while the ignored report library remains local data.

**Tech Stack:** Bash 5, GNU coreutils (`sha256sum`, `find`, `sort`, `flock`, `timeout`), Git, Codex CLI, Markdown.

---

## File Map

- Create `scripts/agent-interview/library-common.sh`: fixed filenames, required headings, path resolution, hashing, logging helpers, and shared validation primitives.
- Create `scripts/agent-interview/validate-library.sh`: deterministic `--library`, `--inbox`, and `--coverage-manifest` checks; never invokes a model.
- Create `scripts/agent-interview/daily-agent-interview.sh`: generate to inbox, copy the library to staging, invoke Codex fusion, validate, publish, and consume inputs.
- Create `scripts/agent-interview/testdata/fake-codex.sh`: deterministic generator/fuser used only by shell tests.
- Create `scripts/agent-interview/validate-library-test.sh`: structure, allowlist, ID, ledger, link, count, and coverage regression tests.
- Create `scripts/agent-interview/daily-agent-interview-test.sh`: idempotency, failure preservation, unexpected-file rejection, and successful publication tests.
- Modify `.github/workflows/ci.yml`: run both deterministic shell tests.
- Modify `Makefile`: expose `agent-interview-test` and include it in the appropriate quality aggregate.
- Create local `tmp/agent-interview/reports/{README.md,01-*.md,...,99-unclassified.md}`: consolidated content library.
- Create local `tmp/agent-interview/reports/inbox/`: transient report input directory.
- Replace local `tmp/cron/daily-agent-interview.sh`: deployed copy of the tracked task.
- Replace local `tmp/agent-interview/reports/latest.md`: symlink to `README.md`.
- Delete local `tmp/agent-interview/reports/20*.md`: only after coverage and library validation pass.

### Task 1: Add the deterministic library contract

**Files:**

- Create: `scripts/agent-interview/library-common.sh`
- Create: `scripts/agent-interview/validate-library.sh`
- Test: `scripts/agent-interview/validate-library-test.sh`

- [ ] **Step 1: Write the failing validator fixture test**

Create a temporary library with all 10 category files and `README.md`. Assert that a valid fixture passes, then independently assert failure for an extra category file, a missing required heading, duplicate stable IDs, a ledger/count mismatch, and a coverage manifest whose source question has no mapped stable ID.

The fixture must use these exact headings in category files:

```bash
required_headings=(
  '## 分类边界'
  '## 趋势与观点'
  '## 面试题'
  '## Stratum 可补强点'
  '## 跟踪关键词'
  '## 参考来源'
)
```

Stable entry lines use `### Q-<category>-<slug>`, `### T-<category>-<slug>`, or `### G-<category>-<slug>`. Processed ledger lines use:

```markdown
| <run-id> | <report-date> | <sha256> | <input-count> | <created> | <updated> | <duplicate> | <unclassified> |
```

- [ ] **Step 2: Run the validator test and verify it fails**

Run: `bash scripts/agent-interview/validate-library-test.sh`

Expected: FAIL because `validate-library.sh` does not exist.

- [ ] **Step 3: Implement shared constants and validator CLI**

In `library-common.sh`, declare the allowlist as a readonly Bash array and expose `resolve_root`, `sha256_file`, and `list_markdown_files`. The allowlist must contain exactly:

```bash
README.md
01-agent-runtime-and-workflow.md
02-tools-mcp-and-approval.md
03-context-and-memory.md
04-knowledge-and-rag.md
05-llm-gateway-and-model-routing.md
06-reliability-and-streaming.md
07-evaluation-observability-and-cost.md
08-security-iam-and-multitenancy.md
09-architecture-and-production-readiness.md
99-unclassified.md
```

Implement `validate-library.sh --library <dir> [--inbox <dir>] [--coverage-manifest <file>]`. It must reject missing and additional Markdown files, reject symlinks except `latest.md -> README.md`, require the six headings, collect stable IDs across every category and reject duplicates, validate 64-character lowercase SHA-256 ledger values, compare the index's declared unclassified count with actual `###` entries in `99-unclassified.md`, and verify each manifest row `<source>|<source-question-id>|<stable-id>` references an existing stable ID.

- [ ] **Step 4: Run the validator tests**

Run: `bash scripts/agent-interview/validate-library-test.sh`

Expected: PASS with `agent interview library validator tests passed`.

- [ ] **Step 5: Commit the deterministic contract**

```bash
git add scripts/agent-interview/library-common.sh \
  scripts/agent-interview/validate-library.sh \
  scripts/agent-interview/validate-library-test.sh
git commit -m "[feat](agent): validate interview knowledge library"
```

### Task 2: Implement staged daily fusion

**Files:**

- Create: `scripts/agent-interview/daily-agent-interview.sh`
- Create: `scripts/agent-interview/testdata/fake-codex.sh`
- Test: `scripts/agent-interview/daily-agent-interview-test.sh`

- [ ] **Step 1: Write failing end-to-end shell tests**

Build a temporary repository fixture containing a valid library. Use `fake-codex.sh` with explicit modes to verify:

1. `generate` creates one inbox report and `fuse` adds one stable entry plus one ledger row;
2. rerunning fusion with the same report hash leaves all published category checksums unchanged;
3. `FAKE_CODEX_MODE=invalid-output` causes nonzero exit, preserves the published library byte-for-byte, and retains the inbox report;
4. `FAKE_CODEX_MODE=fail` propagates the fake Codex exit code and retains the inbox report;
5. a staged extra category file is rejected before publication;
6. successful publication removes only the consumed inbox file.

- [ ] **Step 2: Run the daily task test and verify it fails**

Run: `bash scripts/agent-interview/daily-agent-interview-test.sh`

Expected: FAIL because the tracked daily task does not exist.

- [ ] **Step 3: Implement generation and fusion modes**

The task accepts `--generate-and-fuse` (default), `--fuse-only`, `--validate-only`, and `--dry-run`. Resolve paths from `STRATUM_ROOT` and `AGENT_INTERVIEW_OUT_DIR`; use the existing lock path and nonblocking `flock`.

Generation writes only to `reports/inbox/<run-id>.md`. Fusion must:

```bash
stage_root="$(mktemp -d "${OUT_DIR}/.fusion.XXXXXX")"
trap 'rm -rf "${stage_root}"' EXIT
cp -a "${REPORT_DIR}/." "${stage_root}/library/"
rm -rf "${stage_root}/library/inbox" "${stage_root}/library/latest.md"
mkdir -p "${stage_root}/library/inbox"
```

Invoke Codex with a prompt that embeds the fixed classification contract, requires it to modify only the stage directory, requires a coverage manifest, prohibits new category files, and tells it to preserve unresolved content in `99-unclassified.md`. Pass the inbox paths and their SHA-256 values explicitly.

After Codex returns, call `validate-library.sh` against the stage and coverage manifest. Compare each input hash with the ledger. Publish only after all validations pass by renaming the existing library to a same-filesystem backup, renaming the stage library into place, recreating `inbox/`, restoring any unconsumed inbox files, and removing the backup. If either rename fails, restore the backup and exit nonzero.

- [ ] **Step 4: Preserve the research requirements in the generation prompt**

Carry forward current requirements: Chinese output, 12-20 senior/staff questions, current public sources, Stratum grounding, source paths, trends, gaps, keywords, and secret exclusion. Add a required `## 输入元数据` section containing run ID and report date so fusion does not infer identity from filenames.

- [ ] **Step 5: Run staged fusion tests**

Run: `bash scripts/agent-interview/daily-agent-interview-test.sh`

Expected: PASS with `daily agent interview fusion tests passed`.

- [ ] **Step 6: Run ShellCheck or the repository shell syntax fallback**

Run: `command -v shellcheck >/dev/null && shellcheck scripts/agent-interview/*.sh scripts/agent-interview/testdata/*.sh || bash -n scripts/agent-interview/*.sh scripts/agent-interview/testdata/*.sh`

Expected: exit 0.

- [ ] **Step 7: Commit staged fusion**

```bash
git add scripts/agent-interview/daily-agent-interview.sh \
  scripts/agent-interview/daily-agent-interview-test.sh \
  scripts/agent-interview/testdata/fake-codex.sh
git commit -m "[feat](agent): fuse daily interview research"
```

### Task 3: Wire deterministic tests into project quality commands

**Files:**

- Modify: `Makefile`
- Modify: `.github/workflows/ci.yml`

- [ ] **Step 1: Add a failing Makefile contract assertion**

Extend `daily-agent-interview-test.sh` to require a target named `agent-interview-test` that invokes both shell tests. Require CI to contain `make agent-interview-test`.

- [ ] **Step 2: Run the contract assertion and verify it fails**

Run: `bash scripts/agent-interview/daily-agent-interview-test.sh`

Expected: FAIL because the Makefile target and CI step are absent.

- [ ] **Step 3: Add the Makefile and CI entries**

Add:

```make
.PHONY: agent-interview-test
agent-interview-test: ; bash scripts/agent-interview/validate-library-test.sh && bash scripts/agent-interview/daily-agent-interview-test.sh
```

Add a CI step after other deterministic shell checks:

```yaml
- name: Test agent interview consolidation
  run: make agent-interview-test
```

- [ ] **Step 4: Run the new quality target**

Run: `make agent-interview-test`

Expected: both test suites PASS.

- [ ] **Step 5: Commit quality wiring**

```bash
git add Makefile .github/workflows/ci.yml scripts/agent-interview/daily-agent-interview-test.sh
git commit -m "[test](agent): gate interview report fusion"
```

### Task 4: Build and validate the initial local library

**Files:**

- Create local: `/home/yang/go-projects/stratum/tmp/agent-interview/reports/README.md`
- Create local: `/home/yang/go-projects/stratum/tmp/agent-interview/reports/01-agent-runtime-and-workflow.md`
- Create local: `/home/yang/go-projects/stratum/tmp/agent-interview/reports/02-tools-mcp-and-approval.md`
- Create local: `/home/yang/go-projects/stratum/tmp/agent-interview/reports/03-context-and-memory.md`
- Create local: `/home/yang/go-projects/stratum/tmp/agent-interview/reports/04-knowledge-and-rag.md`
- Create local: `/home/yang/go-projects/stratum/tmp/agent-interview/reports/05-llm-gateway-and-model-routing.md`
- Create local: `/home/yang/go-projects/stratum/tmp/agent-interview/reports/06-reliability-and-streaming.md`
- Create local: `/home/yang/go-projects/stratum/tmp/agent-interview/reports/07-evaluation-observability-and-cost.md`
- Create local: `/home/yang/go-projects/stratum/tmp/agent-interview/reports/08-security-iam-and-multitenancy.md`
- Create local: `/home/yang/go-projects/stratum/tmp/agent-interview/reports/09-architecture-and-production-readiness.md`
- Create local: `/home/yang/go-projects/stratum/tmp/agent-interview/reports/99-unclassified.md`
- Create temporary local: `/home/yang/go-projects/stratum/tmp/agent-interview/initial-coverage.tsv`

- [ ] **Step 1: Inventory immutable source evidence**

Record SHA-256 hashes and extract every report's date, question heading, trend item, gap item, keyword, and URL into a temporary manifest. Exclude `latest.md` because it is a symlink alias of the newest report. The manifest assigns each source question an ID `<run-id>:Q<ordinal>` and initially leaves its stable ID empty.

Run: `find tmp/agent-interview/reports -maxdepth 1 -type f -name '20*.md' -print0 | sort -z | xargs -0 sha256sum`

Expected: 14 unique source files and 14 hashes.

- [ ] **Step 2: Run one controlled fusion into a separate staging directory**

Use the tracked fusion prompt and all 14 reports as inputs, but set `AGENT_INTERVIEW_OUT_DIR` to a temporary directory. Require the output to fill every manifest mapping, preserve sources and update dates, and place uncertain items in `99-unclassified.md`.

- [ ] **Step 3: Review semantic fusion against source samples**

For every category, compare at least three merged entries with all contributing source questions. Confirm the answer retained distinct Stratum implementation evidence, follow-up questions, relevant paths, and conflicting boundaries. Review every `99-unclassified.md` item individually.

- [ ] **Step 4: Run full coverage and structural validation**

Run: `bash scripts/agent-interview/validate-library.sh --library <staged-library> --coverage-manifest tmp/agent-interview/initial-coverage.tsv`

Expected: PASS; all 14 reports are in the ledger; every source question maps to one existing stable ID; counts are consistent.

- [ ] **Step 5: Verify source integrity immediately before publication**

Run the coverage validator once more against the staged library, then compare the recorded source hashes with the 14 current files using `sha256sum -c`. Any mismatch or unmapped item stops the migration before the report directory changes.

Expected: all hashes report `OK`, coverage PASS, and unclassified count equals the index.

- [ ] **Step 6: Publish through a recoverable same-filesystem swap**

Rename the current `reports/` directory to `reports.pre-consolidation`, rename the validated staged library to `reports/`, create `reports/inbox/`, and create `reports/latest.md -> README.md`. Run the validator against the published directory. If any rename or validation fails, remove the incomplete published directory and rename `reports.pre-consolidation` back to `reports/`.

Expected: only the fixed files, `latest.md`, and `inbox/` exist in the published directory; validator PASS; the 14 source reports remain recoverable in `reports.pre-consolidation` until the next step.

- [ ] **Step 7: Delete the validated rollback directory**

Confirm `reports.pre-consolidation` contains exactly the 14 manifest paths plus the old `latest.md` symlink and no other files. Delete that explicit rollback directory only after the published validator passes, then confirm `reports.pre-consolidation` no longer exists.

Expected: the date reports and old symlink are removed, the fixed library remains valid, and `inbox/` is empty.

### Task 5: Deploy and exercise the scheduled task locally

**Files:**

- Modify local: `/home/yang/go-projects/stratum/tmp/cron/daily-agent-interview.sh`

- [ ] **Step 1: Run the repository risk explanation before deployment**

Run: `bash scripts/quality/risk-regression-guard.sh --explain`

Expected: exit 0 and an explanation of applicable guards.

- [ ] **Step 2: Deploy the tracked script as the ignored cron copy**

Copy `scripts/agent-interview/daily-agent-interview.sh` to `tmp/cron/daily-agent-interview.sh`, preserve executable mode, and verify with `cmp` that the deployed bytes match the tracked source.

- [ ] **Step 3: Exercise dry-run and validation modes**

Run:

```bash
AGENT_INTERVIEW_DRY_RUN=1 tmp/cron/daily-agent-interview.sh --dry-run
tmp/cron/daily-agent-interview.sh --validate-only
```

Expected: both exit 0; validation reports the fixed library counts without changing category checksums.

- [ ] **Step 4: Run a disposable end-to-end fusion**

Copy the current library to a temporary output root, use the deterministic fake Codex, and run `--generate-and-fuse` twice. The first run adds one ledger entry; the second detects the same hash and makes no category changes.

Expected: both runs exit 0, the second reports an already processed input, and no temporary process remains.

- [ ] **Step 5: Run tracked verification**

Run:

```bash
make agent-interview-test
bash scripts/quality/risk-regression-guard.sh --explain
make risk-guardrails
git diff --check
```

Expected: all commands exit 0.

- [ ] **Step 6: Commit any final tracked corrections**

If verification required tracked corrections, commit only those corrections:

```bash
git add scripts/agent-interview Makefile .github/workflows/ci.yml
git commit -m "[fix](agent): harden interview fusion deployment"
```

Do not add ignored `tmp/` library data or the deployed cron copy to Git.

### Task 6: Final audit and handoff

**Files:**

- Verify: all files above

- [ ] **Step 1: Confirm tracked and local scopes**

Run: `git status --short --branch` in the feature worktree and `git status --short --ignored tmp/agent-interview tmp/cron/daily-agent-interview.sh` in the primary checkout.

Expected: tracked changes are committed on `feat/agent-interview-consolidation`; local library and cron deployment remain ignored.

- [ ] **Step 2: Confirm the scheduled caller still resolves**

Inspect `tmp/cron/hermes-run-task.sh` and the configured Hermes task definition. Verify the Agent interview task still calls `tmp/cron/daily-agent-interview.sh` and that the file is executable.

- [ ] **Step 3: Produce final statistics**

Report source report count, source question count, stable question count, merged duplicate count, trend count, gap count, source count, keyword count, and unclassified count from `README.md` and validator output.

- [ ] **Step 4: Classify knowledge deposition candidates**

Keep project-specific implementation and operating rules in Git/design docs. Submit a cross-project candidate only if verification demonstrates a reusable failure mode not already captured by existing knowledge; otherwise report `none`.
